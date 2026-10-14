package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"pricepulse/internal/domain"
)

// listingColumns are read by scanListing in this order.
const listingColumns = `l.id, l.url, l.title, l.currency, l.current_price, l.lowest_price,
	COALESCE(array_to_json(l.lowest_price_dates), '[]'), l.highest_price,
	l.last_checked_at, l.last_attempt_at, l.last_error, l.created_at,
	(SELECT count(*) FROM products p WHERE p.listing_id = l.id), ` + detailColumns

func scanListing(s rowScanner) (*domain.Listing, error) {
	var l domain.Listing
	var lowest, highest sql.NullFloat64
	var checked, attempt sql.NullTime
	var lowDates []byte
	var det detailsDest
	err := s.Scan(append([]any{&l.ID, &l.URL, &l.Title, &l.Currency, &l.CurrentPrice, &lowest, &lowDates, &highest,
		&checked, &attempt, &l.LastError, &l.CreatedAt, &l.Trackers}, det.targets(&l.ListingDetails)...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.LowestPrice, l.HighestPrice = nullFloat(lowest), nullFloat(highest)
	det.apply(&l.ListingDetails)
	l.LastCheckedAt, l.LastAttemptAt = nullTime(checked), nullTime(attempt)
	if l.LowestPriceDates, err = parseTimes(lowDates); err != nil {
		return nil, err
	}
	return &l, nil
}

// Listing returns one listing, or ErrNotFound.
func (r *ProductRepository) Listing(ctx context.Context, id int64) (*domain.Listing, error) {
	return scanListing(r.db.QueryRowContext(ctx, `SELECT `+listingColumns+` FROM listings l WHERE l.id = $1`, id))
}

// ListingByKey returns the listing for a normalized URL key, or ErrNotFound.
func (r *ProductRepository) ListingByKey(ctx context.Context, key string) (*domain.Listing, error) {
	return scanListing(r.db.QueryRowContext(ctx, `SELECT `+listingColumns+` FROM listings l WHERE l.url_key = $1`, key))
}

// FindOrCreateListing returns the listing for key, creating it (with fetchURL
// as the page to scrape) if it doesn't exist. created tells which happened.
func (r *ProductRepository) FindOrCreateListing(ctx context.Context, fetchURL, key string) (*domain.Listing, bool, error) {
	// ON CONFLICT DO NOTHING + RETURNING yields a row only when we inserted;
	// otherwise the UNION's second branch finds the existing listing. Under a
	// race with another insert of the same key, neither may see a row yet,
	// so retry once (by then the other transaction has committed).
	for attempt := 0; attempt < 2; attempt++ {
		var id int64
		var created bool
		err := r.db.QueryRowContext(ctx, `
			WITH ins AS (
				INSERT INTO listings (url, url_key) VALUES ($1, $2)
				ON CONFLICT (url_key) DO NOTHING
				RETURNING id
			)
			SELECT id, true FROM ins
			UNION ALL
			SELECT id, false FROM listings WHERE url_key = $2
			LIMIT 1`,
			fetchURL, key,
		).Scan(&id, &created)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("find or create listing: %w", err)
		}
		l, err := r.Listing(ctx, id)
		return l, created, err
	}
	return nil, false, errors.New("find or create listing: concurrent insert, try again")
}

// TouchListingView records a public page view. Recently viewed listings keep
// being re-checked even if nobody tracks them.
func (r *ProductRepository) TouchListingView(ctx context.Context, id int64) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE listings SET last_viewed_at = now(), view_count = view_count + 1 WHERE id = $1`, id,
	); err != nil {
		return fmt.Errorf("touch listing: %w", err)
	}
	return nil
}

// PublicHistory returns a listing's store prices (never manual entries),
// oldest first.
func (r *ProductRepository) PublicHistory(ctx context.Context, listingID int64) ([]domain.PriceHistory, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, price, recorded_at, source FROM listing_prices
		WHERE listing_id = $1 AND source = 'scraper'
		ORDER BY recorded_at, id`, listingID)
	if err != nil {
		return nil, fmt.Errorf("public history: %w", err)
	}
	return collectHistory(rows)
}

// RecordListingPrice saves a scraped price, all in one transaction:
//   - a new history row
//   - the listing's current price, currency, name and check status
//   - the all-time lowest price and the days it was seen: a new low replaces
//     the list; the same low on a new (UTC) day is appended
//   - the all-time highest price
//   - every tracking product's current price, re-arming alerts for products
//     whose price went back above their target
//   - store details (image, brand, rating, MRP, stock); a detail the page
//     didn't include this time keeps its previous value
func (r *ProductRepository) RecordListingPrice(ctx context.Context, listingID int64, price float64, currency, title string, d domain.ListingDetails, at time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO listing_prices (listing_id, price, recorded_at, source) VALUES ($1, $2, $3, 'scraper')`,
		listingID, price, at,
	); err != nil {
		return fmt.Errorf("insert price: %w", err)
	}

	// In SET, every column reference means the row's OLD value, so
	// lowest_price below is the low *before* this price.
	res, err := tx.ExecContext(ctx, `
		UPDATE listings SET
			current_price   = $2,
			last_checked_at = $3,
			last_attempt_at = $3,
			last_error      = '',
			currency        = CASE WHEN $4 <> '' THEN $4 ELSE currency END,
			title           = CASE WHEN $5 <> '' THEN $5 ELSE title END,
			highest_price   = GREATEST(COALESCE(highest_price, $2::numeric), $2::numeric),
			lowest_price_dates = CASE
				WHEN lowest_price IS NULL OR $2::numeric < lowest_price THEN ARRAY[$3::timestamptz]
				WHEN $2::numeric = lowest_price AND NOT EXISTS (
					SELECT 1 FROM unnest(lowest_price_dates) d
					WHERE (d AT TIME ZONE 'UTC')::date = ($3::timestamptz AT TIME ZONE 'UTC')::date
				) THEN lowest_price_dates || $3::timestamptz
				ELSE lowest_price_dates END,
			lowest_price    = LEAST(COALESCE(lowest_price, $2::numeric), $2::numeric),
			image_url       = CASE WHEN $6 <> '' THEN $6 ELSE image_url END,
			brand           = CASE WHEN $7 <> '' THEN $7 ELSE brand END,
			rating          = COALESCE($8, rating),
			best_rating     = COALESCE($9, best_rating),
			rating_count    = COALESCE($10, rating_count),
			mrp             = COALESCE($11, mrp),
			in_stock        = COALESCE($12, in_stock)
		WHERE id = $1`,
		listingID, price, at, currency, title,
		d.ImageURL, d.Brand, d.Rating, d.BestRating, d.RatingCount, d.MRP, d.InStock,
	)
	if err != nil {
		return fmt.Errorf("update listing: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE products SET
			current_price = $2, last_checked_at = $3,
			alert_sent_at = CASE WHEN $2 > target_price THEN NULL ELSE alert_sent_at END
		WHERE listing_id = $1`,
		listingID, price, at,
	); err != nil {
		return fmt.Errorf("update tracking products: %w", err)
	}
	return tx.Commit()
}

// MarkListingFailed records a failed check; the last known price is kept.
func (r *ProductRepository) MarkListingFailed(ctx context.Context, id int64, msg string, at time.Time) error {
	res, err := r.db.ExecContext(ctx, `UPDATE listings SET last_attempt_at = $3, last_error = $2 WHERE id = $1`, id, msg, at)
	if err != nil {
		return fmt.Errorf("mark listing failed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListingRef identifies a listing to scrape.
type ListingRef struct {
	ID  int64
	URL string
}

// ListingsDue returns up to `limit` listings whose next check is due:
//   - tracked listings use the shortest re-check interval among their
//     (non-suspended) trackers: each user's override, else the global default
//   - untracked listings viewed publicly in the last 30 days use the public
//     re-check interval, if public lookups are enabled
//
// Never-checked listings come first, then the most stale ones.
func (r *ProductRepository) ListingsDue(ctx context.Context, limit int) ([]ListingRef, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT l.id, l.url
		FROM listings l
		CROSS JOIN app_settings s
		LEFT JOIN LATERAL (
			SELECT min(COALESCE(u.check_interval_minutes, s.check_interval_minutes)) AS mins
			FROM products p LEFT JOIN users u ON u.id = p.user_id
			WHERE p.listing_id = l.id AND p.bought_at IS NULL AND (u.id IS NULL OR NOT u.disabled)
		) t ON true
		WHERE (t.mins IS NOT NULL OR (s.public_lookups AND l.last_viewed_at > now() - interval '30 days'))
		  AND (l.last_attempt_at IS NULL OR l.last_attempt_at <
		       now() - make_interval(mins => COALESCE(t.mins, s.public_check_interval_minutes)))
		ORDER BY l.last_attempt_at NULLS FIRST, l.id
		LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list due listings: %w", err)
	}
	defer rows.Close()

	due := []ListingRef{}
	for rows.Next() {
		var ref ListingRef
		if err := rows.Scan(&ref.ID, &ref.URL); err != nil {
			return nil, fmt.Errorf("scan listing: %w", err)
		}
		due = append(due, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate listings: %w", err)
	}
	return due, nil
}

// migrateToListings moves products created before listings existed onto
// listings, copying their price history, in a single transaction. It only
// touches products without a listing, so it is a no-op after the first run.
// The legacy price_history rows are left in place (nothing is deleted).
func migrateToListings(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	type legacy struct {
		id                   int64
		url, title, currency string
		lastError            string
		lastAttempt          sql.NullTime
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, url, title, currency, last_error, last_attempt_at
		FROM products WHERE listing_id IS NULL ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	var todo []legacy
	for rows.Next() {
		var p legacy
		if err := rows.Scan(&p.id, &p.url, &p.title, &p.currency, &p.lastError, &p.lastAttempt); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(todo) == 0 {
		return nil
	}

	var touched []int64
	for _, p := range todo {
		fetch, key, err := domain.ListingURL(p.url)
		if err != nil { // shouldn't happen (URLs were validated), but never lose data
			fetch, key = p.url, p.url
		}
		// The product's (scraped) name becomes the listing's name, unless it
		// was just the auto-generated domain.
		listingTitle := p.title
		if listingTitle == p.url || listingTitle == domain.DefaultTitle(p.url) {
			listingTitle = ""
		}
		var lid int64
		var storedTitle string
		// "DO UPDATE SET url_key = EXCLUDED.url_key" is a no-op that makes
		// RETURNING produce the id whether the row was inserted or existed.
		err = tx.QueryRowContext(ctx, `
			INSERT INTO listings (url, url_key, title, currency, last_error, last_attempt_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (url_key) DO UPDATE SET url_key = EXCLUDED.url_key
			RETURNING id, title`,
			fetch, key, listingTitle, p.currency, p.lastError, p.lastAttempt,
		).Scan(&lid, &storedTitle)
		if err != nil {
			return fmt.Errorf("listing for product %d: %w", p.id, err)
		}
		// Keep the product's title only if the user renamed it to something
		// other than the store's name.
		customTitle := p.title
		if customTitle == storedTitle || customTitle == listingTitle || listingTitle == "" {
			customTitle = ""
		}
		if _, err := tx.ExecContext(ctx, `UPDATE products SET listing_id = $2, title = $3 WHERE id = $1`, p.id, lid, customTitle); err != nil {
			return fmt.Errorf("link product %d: %w", p.id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO listing_prices (listing_id, price, recorded_at, source)
			SELECT $2, price, recorded_at, 'scraper' FROM price_history WHERE product_id = $1`,
			p.id, lid,
		); err != nil {
			return fmt.Errorf("copy history of product %d: %w", p.id, err)
		}
		touched = append(touched, lid)
	}

	if err := recomputeListingStats(ctx, tx, touched); err != nil {
		return err
	}
	return tx.Commit()
}

// recomputeListingStats rebuilds current/lowest/highest prices and the
// lowest-price days from the full store history of the given listings.
func recomputeListingStats(ctx context.Context, tx *sql.Tx, ids []int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE listings l SET
			current_price = s.cur, last_checked_at = s.last_at, lowest_price = s.lo, highest_price = s.hi
		FROM (
			SELECT listing_id,
			       (array_agg(price ORDER BY recorded_at DESC, id DESC))[1] AS cur,
			       max(recorded_at) AS last_at, min(price) AS lo, max(price) AS hi
			FROM listing_prices
			WHERE source = 'scraper' AND listing_id = ANY($1)
			GROUP BY listing_id
		) s
		WHERE l.id = s.listing_id`, pq.Array(ids),
	); err != nil {
		return fmt.Errorf("recompute prices: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE listings l SET lowest_price_dates = s.dates
		FROM (
			SELECT x.listing_id, array_agg(x.first_at ORDER BY x.first_at) AS dates
			FROM (
				SELECT lp.listing_id, min(lp.recorded_at) AS first_at
				FROM listing_prices lp JOIN listings l2 ON l2.id = lp.listing_id
				WHERE lp.source = 'scraper' AND lp.price = l2.lowest_price AND lp.listing_id = ANY($1)
				GROUP BY lp.listing_id, (lp.recorded_at AT TIME ZONE 'UTC')::date
			) x
			GROUP BY x.listing_id
		) s
		WHERE l.id = s.listing_id`, pq.Array(ids),
	); err != nil {
		return fmt.Errorf("recompute lowest dates: %w", err)
	}
	return nil
}

// rekeyListings re-applies the current URL rules (domain.ListingURL) to every
// listing, so improving them, e.g. learning a new tracking parameter, also
// cleans up listings created earlier. If a listing's new key already belongs
// to another listing it is left unchanged: merging two price histories should
// be a deliberate decision, not a side effect of starting the server.
func rekeyListings(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT id, url, url_key FROM listings`)
	if err != nil {
		return err
	}
	type row struct {
		id       int64
		url, key string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.url, &r.key); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range all {
		fetch, key, err := domain.ListingURL(r.url)
		if err != nil || (fetch == r.url && key == r.key) {
			continue
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE listings SET url = $2, url_key = $3
			WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM listings WHERE url_key = $3 AND id <> $1)`,
			r.id, fetch, key,
		); err != nil {
			return fmt.Errorf("listing %d: %w", r.id, err)
		}
	}
	return nil
}
