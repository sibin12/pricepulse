// Package repository contains the data-access layer.
//
// Handlers talk to this package instead of writing SQL themselves. That keeps
// SQL in one place, makes it easy to test or swap the storage later, and keeps
// HTTP code free of database details.
package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"pricepulse/internal/domain"
)

// ErrNotFound is returned when a product with the requested ID does not exist.
// Callers check it with errors.Is to turn it into a 404 instead of a 500.
var ErrNotFound = errors.New("not found")

// ErrLimitReached is returned by Create when the user already has their
// maximum number of products.
var ErrLimitReached = errors.New("product limit reached")

// AlreadyTrackingError is returned by Create when the user already tracks
// this listing; ProductID is their existing product.
type AlreadyTrackingError struct{ ProductID int64 }

func (e *AlreadyTrackingError) Error() string { return "already tracking this product" }

// Schema creates the tables on startup. IF NOT EXISTS makes it idempotent: safe on every boot.
// NUMERIC is used for money columns because it is exact (floats can drift).
// TIMESTAMPTZ stores an absolute instant, avoiding time zone bugs.
// (For a real project, use a migration tool such as golang-migrate instead.)
const Schema = `
CREATE TABLE IF NOT EXISTS products (
	id              BIGSERIAL PRIMARY KEY,
	url             TEXT           NOT NULL,
	title           TEXT           NOT NULL,
	current_price   NUMERIC(12, 2) NOT NULL,
	target_price    NUMERIC(12, 2) NOT NULL,
	last_checked_at TIMESTAMPTZ    NOT NULL,
	created_at      TIMESTAMPTZ    NOT NULL
);

-- Legacy per-product history (before listings existed). Kept so upgrading
-- never deletes data; its rows are copied into listing_prices once.
CREATE TABLE IF NOT EXISTS price_history (
	id          BIGSERIAL PRIMARY KEY,
	product_id  BIGINT         NOT NULL REFERENCES products(id) ON DELETE CASCADE,
	price       NUMERIC(12, 2) NOT NULL,
	recorded_at TIMESTAMPTZ    NOT NULL
);
CREATE INDEX IF NOT EXISTS price_history_product_time_idx
	ON price_history (product_id, recorded_at);

-- Legacy per-product scraper columns (now on listings).
ALTER TABLE products ADD COLUMN IF NOT EXISTS last_attempt_at TIMESTAMPTZ;
ALTER TABLE products ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN IF NOT EXISTS currency TEXT NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN IF NOT EXISTS alert_sent_at TIMESTAMPTZ;

-- Accounts. Emails are stored lowercased so the UNIQUE constraint is
-- effectively case-insensitive.
-- password_hash is NULL for accounts that only sign in with Google;
-- google_sub is Google's stable account ID (emails can change, sub never does).
CREATE TABLE IF NOT EXISTS users (
	id            BIGSERIAL PRIMARY KEY,
	email         TEXT        NOT NULL UNIQUE,
	password_hash TEXT,
	google_sub    TEXT        UNIQUE,
	created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Login sessions. Only a SHA-256 hash of each session token is stored, so a
-- leaked database (or backup) can't be used to log in as anyone.
CREATE TABLE IF NOT EXISTS sessions (
	token_hash BYTEA       PRIMARY KEY,
	user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions (user_id);

-- Product ownership. Nullable because products created before accounts
-- existed have no owner; the first user to register adopts them.
ALTER TABLE products ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS products_user_idx ON products (user_id);

-- Roles, suspension and per-user overrides. A NULL override means "use the
-- global default from app_settings".
ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS max_products INT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS check_interval_minutes INT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS alerts_enabled BOOLEAN;
ALTER TABLE users ADD COLUMN IF NOT EXISTS manual_checks_enabled BOOLEAN;

-- Global settings: exactly one row (the CHECK makes a second row impossible).
CREATE TABLE IF NOT EXISTS app_settings (
	id                     INT         PRIMARY KEY DEFAULT 1 CHECK (id = 1),
	max_products           INT         NOT NULL DEFAULT 20,
	check_interval_minutes INT         NOT NULL DEFAULT 360,
	alerts_enabled         BOOLEAN     NOT NULL DEFAULT true,
	manual_checks_enabled  BOOLEAN     NOT NULL DEFAULT true,
	signups_open           BOOLEAN     NOT NULL DEFAULT true,
	updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO app_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
ALTER TABLE app_settings ADD COLUMN IF NOT EXISTS public_lookups BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE app_settings ADD COLUMN IF NOT EXISTS public_check_interval_minutes INT NOT NULL DEFAULT 1440;

-- Listings: one row per product page (by normalized URL), shared by every
-- user who tracks it and by public lookups. The store is scraped once per
-- listing, and its price history powers the public price-history pages.
-- lowest_price_dates holds one timestamp per (UTC) day on which the
-- all-time lowest price was seen.
CREATE TABLE IF NOT EXISTS listings (
	id                 BIGSERIAL      PRIMARY KEY,
	url                TEXT           NOT NULL,
	url_key            TEXT           NOT NULL UNIQUE,
	title              TEXT           NOT NULL DEFAULT '',
	currency           TEXT           NOT NULL DEFAULT '',
	current_price      NUMERIC(12, 2) NOT NULL DEFAULT 0,
	lowest_price       NUMERIC(12, 2),
	lowest_price_dates TIMESTAMPTZ[]  NOT NULL DEFAULT '{}',
	highest_price      NUMERIC(12, 2),
	last_checked_at    TIMESTAMPTZ,
	last_attempt_at    TIMESTAMPTZ,
	last_error         TEXT           NOT NULL DEFAULT '',
	last_viewed_at     TIMESTAMPTZ,
	view_count         BIGINT         NOT NULL DEFAULT 0,
	created_at         TIMESTAMPTZ    NOT NULL DEFAULT now()
);

-- Price observations. source = 'scraper' rows are public; 'manual' rows were
-- typed in by one user (user_id) and are only ever shown to that user.
CREATE TABLE IF NOT EXISTS listing_prices (
	id          BIGSERIAL      PRIMARY KEY,
	listing_id  BIGINT         NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
	price       NUMERIC(12, 2) NOT NULL,
	recorded_at TIMESTAMPTZ    NOT NULL,
	source      TEXT           NOT NULL DEFAULT 'scraper' CHECK (source IN ('scraper', 'manual')),
	user_id     BIGINT         REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS listing_prices_listing_time_idx ON listing_prices (listing_id, recorded_at);

-- Each product now points at its listing.
ALTER TABLE products ADD COLUMN IF NOT EXISTS listing_id BIGINT REFERENCES listings(id);
CREATE INDEX IF NOT EXISTS products_listing_idx ON products (listing_id);

-- Product details read from the store page (NULL/'' = not published).
ALTER TABLE listings ADD COLUMN IF NOT EXISTS image_url TEXT NOT NULL DEFAULT '';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS brand TEXT NOT NULL DEFAULT '';
ALTER TABLE listings ADD COLUMN IF NOT EXISTS rating NUMERIC(4, 2);
ALTER TABLE listings ADD COLUMN IF NOT EXISTS best_rating NUMERIC(4, 2);
ALTER TABLE listings ADD COLUMN IF NOT EXISTS rating_count INT;
ALTER TABLE listings ADD COLUMN IF NOT EXISTS mrp NUMERIC(12, 2);
ALTER TABLE listings ADD COLUMN IF NOT EXISTS in_stock BOOLEAN;

-- Bucket list: a note on why you want it, and checking it off once bought.
-- Bought products stop sending alerts and stop using one of your slots.
ALTER TABLE products ADD COLUMN IF NOT EXISTS note TEXT NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN IF NOT EXISTS bought_at TIMESTAMPTZ;
ALTER TABLE products ADD COLUMN IF NOT EXISTS bought_price NUMERIC(12, 2);`

// Migrate applies Schema, moves pre-listing data over, and then enforces
// "one product per user per listing".
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := migrateToListings(ctx, db); err != nil {
		return fmt.Errorf("migrate to listings: %w", err)
	}
	if err := rekeyListings(ctx, db); err != nil {
		return fmt.Errorf("re-key listings: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS products_user_listing_idx ON products (user_id, listing_id)`,
	); err != nil {
		return fmt.Errorf("unique product per listing: %w", err)
	}
	return nil
}

// productColumns and productFrom join each product with its listing;
// scanProduct reads the columns in exactly this order. They are separate so
// queries can add columns (e.g. the owner's email) and joins.
const (
	productColumns = `p.id, l.url, p.title, l.title, p.current_price, p.target_price, l.currency,
	       p.last_checked_at, p.created_at, l.last_attempt_at, l.last_error, p.alert_sent_at,
	       p.listing_id, l.lowest_price, COALESCE(array_to_json(l.lowest_price_dates), '[]'), l.highest_price,
	       p.note, p.bought_at, p.bought_price, ` + detailColumns
	productFrom   = ` FROM products p JOIN listings l ON l.id = p.listing_id`
	productSelect = `SELECT ` + productColumns + productFrom
)

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so one scan function
// serves single-row and multi-row queries.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanProduct(s rowScanner) (*domain.Product, error) {
	var p domain.Product
	var listingTitle string
	// Nullable columns scan into sql.Null* types, then become nil pointers.
	var attempt, alerted, bought sql.NullTime
	var lowest, highest, boughtPrice sql.NullFloat64
	var lowDates []byte
	var det detailsDest
	err := s.Scan(append([]any{&p.ID, &p.URL, &p.Title, &listingTitle, &p.CurrentPrice, &p.TargetPrice, &p.Currency,
		&p.LastCheckedAt, &p.CreatedAt, &attempt, &p.LastError, &alerted,
		&p.ListingID, &lowest, &lowDates, &highest,
		&p.Note, &bought, &boughtPrice}, det.targets(&p.ListingDetails)...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// The user's own name wins; otherwise the store's name; otherwise the domain.
	if p.Title == "" {
		p.Title = listingTitle
	}
	if p.Title == "" {
		p.Title = domain.DefaultTitle(p.URL)
	}
	p.LastAttemptAt = nullTime(attempt)
	p.AlertSentAt = nullTime(alerted)
	p.LowestPrice = nullFloat(lowest)
	p.HighestPrice = nullFloat(highest)
	p.BoughtAt = nullTime(bought)
	p.BoughtPrice = nullFloat(boughtPrice)
	det.apply(&p.ListingDetails)
	if p.LowestPriceDates, err = parseTimes(lowDates); err != nil {
		return nil, err
	}
	return &p, nil
}

// ProductRepository persists products, listings and prices in PostgreSQL.
type ProductRepository struct {
	// db is a connection *pool*, not a single connection. It is safe for
	// concurrent use by many goroutines (one per HTTP request).
	db *sql.DB
}

// NewProductRepository wraps a shared *sql.DB. Create the pool once at startup
// and inject it here (dependency injection) instead of opening connections
// per request, which would be slow and exhaust Postgres connections.
func NewProductRepository(db *sql.DB) *ProductRepository {
	return &ProductRepository{db: db}
}

// Create starts tracking listingID for userID, unless they already track it
// (*AlreadyTrackingError) or already have maxProducts products
// (ErrLimitReached). title "" means "use the store's name". The new product
// starts with the listing's latest known price, so its history is visible
// immediately when someone else already follows the same product.
func (r *ProductRepository) Create(ctx context.Context, userID int64, maxProducts int, listingID int64, title, note string, targetPrice float64, now time.Time) (*domain.Product, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Count-then-insert is racy: two simultaneous requests could both see
	// "19 of 20" and both insert. A per-user advisory lock (released at
	// commit) makes concurrent creates for the same user take turns.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return nil, fmt.Errorf("lock user: %w", err)
	}
	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM products WHERE user_id = $1 AND listing_id = $2`, userID, listingID).Scan(&existing)
	if err == nil {
		return nil, &AlreadyTrackingError{ProductID: existing}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("check duplicate: %w", err)
	}
	// Only active wishes use a slot; bought ones are checked off.
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM products WHERE user_id = $1 AND bought_at IS NULL`, userID).Scan(&count); err != nil {
		return nil, fmt.Errorf("count products: %w", err)
	}
	if count >= maxProducts {
		return nil, ErrLimitReached
	}

	// $1..$n are bound parameters: values are sent separately from the SQL
	// text, which prevents SQL injection. The legacy url column is still
	// filled so older code paths and NOT NULL constraints stay satisfied.
	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO products (user_id, listing_id, url, title, note, target_price, current_price, last_checked_at, created_at)
		SELECT $1, l.id, l.url, $3, $6, $4, l.current_price, COALESCE(l.last_checked_at, $5), $5
		FROM listings l WHERE l.id = $2
		RETURNING id`,
		userID, listingID, title, targetPrice, now, note,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert product: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return r.Owned(ctx, userID, id)
}

// List returns the user's products, newest first.
func (r *ProductRepository) List(ctx context.Context, userID int64) ([]domain.Product, error) {
	rows, err := r.db.QueryContext(ctx, productSelect+` WHERE p.user_id = $1 ORDER BY p.created_at DESC, p.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return collectProducts(rows)
}

// Count returns how many active (not yet bought) products a user has: the
// number that counts against their limit.
func (r *ProductRepository) Count(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM products WHERE user_id = $1 AND bought_at IS NULL`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count products: %w", err)
	}
	return n, nil
}

// Owned returns the product only if it belongs to userID. Someone else's
// product gives ErrNotFound (not "forbidden"), so users can't even learn
// which product IDs exist.
func (r *ProductRepository) Owned(ctx context.Context, userID, id int64) (*domain.Product, error) {
	return scanProduct(r.db.QueryRowContext(ctx, productSelect+` WHERE p.id = $1 AND p.user_id = $2`, id, userID))
}

// ProductForListing returns the ID of userID's product for a listing, if any
// (used to show "you're tracking this" on public pages).
func (r *ProductRepository) ProductForListing(ctx context.Context, userID, listingID int64) (int64, bool, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM products WHERE user_id = $1 AND listing_id = $2`, userID, listingID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find product for listing: %w", err)
	}
	return id, true, nil
}

// Update changes the title and/or target price. A nil pointer means "leave
// unchanged": it is sent as SQL NULL, and COALESCE then keeps the old value.
// Changing the target re-arms the email alert for the new target.
func (r *ProductRepository) Update(ctx context.Context, userID, id int64, title, note *string, targetPrice *float64) (*domain.Product, error) {
	// Inside SET, column names refer to the row's *old* values, so
	// "$3 <> target_price" compares the new target with the previous one.
	res, err := r.db.ExecContext(ctx, `
		UPDATE products
		SET title = COALESCE($2, title),
		    target_price = COALESCE($3, target_price),
		    note = COALESCE($5, note),
		    alert_sent_at = CASE WHEN $3::numeric IS NOT NULL AND $3::numeric <> target_price
		                         THEN NULL ELSE alert_sent_at END
		WHERE id = $1 AND user_id = $4`,
		id, title, targetPrice, userID, note)
	if err != nil {
		return nil, fmt.Errorf("update product: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return r.Owned(ctx, userID, id)
}

// Delete stops tracking a product. The shared listing and its public history
// stay; only this user's manual price entries for it are removed.
func (r *ProductRepository) Delete(ctx context.Context, userID, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var listingID int64
	err = tx.QueryRowContext(ctx, `DELETE FROM products WHERE id = $1 AND user_id = $2 RETURNING listing_id`, id, userID).Scan(&listingID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete product: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM listing_prices WHERE listing_id = $1 AND user_id = $2 AND source = 'manual'`, listingID, userID,
	); err != nil {
		return fmt.Errorf("delete manual prices: %w", err)
	}
	return tx.Commit()
}

// RecordManualPrice stores a price the user typed in. It is private to them:
// it updates their product's current price (and alert state) and appears in
// their history, but never in the shared listing or public pages.
func (r *ProductRepository) RecordManualPrice(ctx context.Context, userID, productID int64, price float64, at time.Time) (*domain.PriceHistory, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	// Rollback after a successful Commit is a harmless no-op, so deferring it
	// guarantees cleanup on every early return below.
	defer tx.Rollback()

	var listingID int64
	err = tx.QueryRowContext(ctx, `
		UPDATE products
		SET current_price = $3, last_checked_at = $4,
		    -- Rising back above the target re-arms the alert for the next drop.
		    alert_sent_at = CASE WHEN $3 > target_price THEN NULL ELSE alert_sent_at END
		WHERE id = $1 AND user_id = $2
		RETURNING listing_id`,
		productID, userID, price, at,
	).Scan(&listingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update current price: %w", err)
	}

	h := &domain.PriceHistory{Price: price, RecordedAt: at, Source: domain.SourceManual}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO listing_prices (listing_id, price, recorded_at, source, user_id)
		VALUES ($1, $2, $3, 'manual', $4) RETURNING id`,
		listingID, price, at, userID,
	).Scan(&h.ID)
	if err != nil {
		return nil, fmt.Errorf("insert manual price: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return h, nil
}

// History returns a product's price history as its owner sees it: the
// listing's store prices plus their own manual entries, oldest first.
func (r *ProductRepository) History(ctx context.Context, userID, productID int64) ([]domain.PriceHistory, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT lp.id, lp.price, lp.recorded_at, lp.source
		FROM products p JOIN listing_prices lp ON lp.listing_id = p.listing_id
		WHERE p.id = $1 AND p.user_id = $2
		  AND (lp.source = 'scraper' OR lp.user_id = $2)
		ORDER BY lp.recorded_at, lp.id`,
		productID, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}
	return collectHistory(rows)
}

// DueAlert is a product whose alert email should be sent, plus where to send it.
type DueAlert struct {
	Product domain.Product
	Email   string // the owner's email address
}

// AlertsDue returns products whose price is at/below target and whose alert
// email has not been sent yet, with their owner's email. This query doubles as
// the retry queue: a failed send leaves alert_sent_at NULL, so the product is
// returned again next time. Bought products, ownerless products, suspended
// users and users whose alerts are switched off are skipped.
func (r *ProductRepository) AlertsDue(ctx context.Context, limit int) ([]DueAlert, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+productColumns+`, u.email`+productFrom+`
		JOIN users u ON u.id = p.user_id CROSS JOIN app_settings s
		WHERE p.current_price > 0 AND p.current_price <= p.target_price AND p.alert_sent_at IS NULL
		  AND p.bought_at IS NULL
		  AND NOT u.disabled AND COALESCE(u.alerts_enabled, s.alerts_enabled)
		ORDER BY p.id
		LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list due alerts: %w", err)
	}
	defer rows.Close()

	due := []DueAlert{}
	for rows.Next() {
		var d DueAlert
		p, err := scanProduct(withExtra(rows, &d.Email))
		if err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		d.Product = *p
		due = append(due, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due alerts: %w", err)
	}
	return due, nil
}

// MarkBought checks a product off the bucket list: bought at price on at.
// It then stops sending alerts and no longer counts against the user's limit.
func (r *ProductRepository) MarkBought(ctx context.Context, userID, id int64, price float64, at time.Time) (*domain.Product, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE products SET bought_at = $3, bought_price = $4 WHERE id = $1 AND user_id = $2`, id, userID, at, price)
	if err != nil {
		return nil, fmt.Errorf("mark bought: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return r.Owned(ctx, userID, id)
}

// UnmarkBought puts a bought product back on the wish list, if the user has
// a free slot for it (ErrLimitReached otherwise).
func (r *ProductRepository) UnmarkBought(ctx context.Context, userID, id int64, maxProducts int) (*domain.Product, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return nil, fmt.Errorf("lock user: %w", err)
	}
	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM products WHERE user_id = $1 AND bought_at IS NULL`, userID,
	).Scan(&count); err != nil {
		return nil, fmt.Errorf("count products: %w", err)
	}
	if count >= maxProducts {
		return nil, ErrLimitReached
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE products SET bought_at = NULL, bought_price = NULL,
		       -- re-arm: if it is already at the target, the user hears about it again
		       alert_sent_at = NULL
		WHERE id = $1 AND user_id = $2 AND bought_at IS NOT NULL`, id, userID)
	if err != nil {
		return nil, fmt.Errorf("unmark bought: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return r.Owned(ctx, userID, id)
}

// MarkAlertSent records that the alert email for a product went out.
func (r *ProductRepository) MarkAlertSent(ctx context.Context, id int64, at time.Time) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE products SET alert_sent_at = $2 WHERE id = $1`, id, at); err != nil {
		return fmt.Errorf("mark alert sent: %w", err)
	}
	return nil
}

// --- helpers ---

func collectProducts(rows *sql.Rows) ([]domain.Product, error) {
	// Close returns the connection to the pool; forgetting it leaks connections.
	defer rows.Close()
	// Start with an empty (non-nil) slice so the JSON is [] rather than null.
	products := []domain.Product{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		products = append(products, *p)
	}
	// rows.Err reports errors that ended the iteration early (e.g. a dropped connection).
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate products: %w", err)
	}
	return products, nil
}

func collectHistory(rows *sql.Rows) ([]domain.PriceHistory, error) {
	defer rows.Close()
	history := []domain.PriceHistory{}
	for rows.Next() {
		var h domain.PriceHistory
		if err := rows.Scan(&h.ID, &h.Price, &h.RecordedAt, &h.Source); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		history = append(history, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history: %w", err)
	}
	return history, nil
}

// withExtra wraps a row scanner so extra columns after the product columns
// are scanned into the given destinations.
func withExtra(s rowScanner, extra ...any) rowScanner {
	return scanFunc(func(dest ...any) error { return s.Scan(append(dest, extra...)...) })
}

type scanFunc func(dest ...any) error

func (f scanFunc) Scan(dest ...any) error { return f(dest...) }

func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func nullFloat(f sql.NullFloat64) *float64 {
	if !f.Valid {
		return nil
	}
	return &f.Float64
}

// parseTimes decodes a TIMESTAMPTZ[] that was selected through array_to_json
// (lib/pq has no native support for scanning timestamp arrays).
func parseTimes(raw []byte) ([]time.Time, error) {
	ts := []time.Time{}
	if len(raw) == 0 {
		return ts, nil
	}
	if err := json.Unmarshal(raw, &ts); err != nil {
		return nil, fmt.Errorf("decode timestamps: %w", err)
	}
	return ts, nil
}

// detailColumns are the listing's optional store details; detailsDest scans
// them (several are nullable) into a domain.ListingDetails.
const detailColumns = `l.image_url, l.brand, l.rating, l.best_rating, l.rating_count, l.mrp, l.in_stock`

type detailsDest struct {
	rating, best, mrp sql.NullFloat64
	count             sql.NullInt64
	stock             sql.NullBool
}

func (d *detailsDest) targets(out *domain.ListingDetails) []any {
	return []any{&out.ImageURL, &out.Brand, &d.rating, &d.best, &d.count, &d.mrp, &d.stock}
}

func (d *detailsDest) apply(out *domain.ListingDetails) {
	out.Rating, out.BestRating, out.MRP = nullFloat(d.rating), nullFloat(d.best), nullFloat(d.mrp)
	if d.count.Valid {
		n := int(d.count.Int64)
		out.RatingCount = &n
	}
	if d.stock.Valid {
		out.InStock = &d.stock.Bool
	}
}
