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

// ErrEmailTaken is returned when registering an email that already has an account.
var ErrEmailTaken = errors.New("email already registered")

// UserRepository stores accounts and login sessions.
type UserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// ErrDisabled is returned when a suspended account tries to sign in.
var ErrDisabled = errors.New("account disabled")

// CreateUser inserts a regular account. Pass passwordHash "" for a Google-only
// account and googleSub "" for a password-only one (both are stored as NULL).
// If it is the first regular (non-admin) account, it also adopts any products
// created before accounts existed (user_id IS NULL), so upgrading doesn't
// strand old data. Admin accounts seeded from .env don't count, so they never
// take the original owner's products.
func (r *UserRepository) CreateUser(ctx context.Context, email, passwordHash, googleSub string) (*domain.User, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	u := &domain.User{Email: email, Role: domain.RoleUser}
	err = tx.QueryRowContext(ctx,
		`INSERT INTO users (email, password_hash, google_sub) VALUES ($1, $2, $3) RETURNING id, created_at`,
		email, nullIfEmpty(passwordHash), nullIfEmpty(googleSub),
	).Scan(&u.ID, &u.CreatedAt)
	// 23505 is PostgreSQL's unique_violation: the UNIQUE(email) constraint
	// fired. Relying on the constraint (instead of checking first) is race-free.
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return nil, ErrEmailTaken
	}
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	// Two simultaneous "first" registrations could both see count = 1 here, but
	// the UPDATE row locks make the second one re-check user_id IS NULL after the
	// first commits, so products are only ever adopted once.
	if _, err := tx.ExecContext(ctx,
		`UPDATE products SET user_id = $1
		 WHERE user_id IS NULL AND (SELECT count(*) FROM users WHERE role = 'user') = 1`,
		u.ID,
	); err != nil {
		return nil, fmt.Errorf("adopt existing products: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}

// UserByEmail returns the account, its password hash ("" for Google-only
// accounts) and whether it is suspended, or ErrNotFound.
func (r *UserRepository) UserByEmail(ctx context.Context, email string) (*domain.User, string, bool, error) {
	var u domain.User
	var hash sql.NullString
	var disabled bool
	err := r.db.QueryRowContext(ctx,
		`SELECT id, email, role, created_at, password_hash, disabled FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt, &hash, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", false, ErrNotFound
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("get user: %w", err)
	}
	return &u, hash.String, disabled, nil
}

// UserByGoogleSub finds the account linked to a Google account ID, or
// ErrNotFound, or ErrDisabled if that account is suspended.
func (r *UserRepository) UserByGoogleSub(ctx context.Context, sub string) (*domain.User, error) {
	var u domain.User
	var disabled bool
	err := r.db.QueryRowContext(ctx,
		`SELECT id, email, role, created_at, disabled FROM users WHERE google_sub = $1`, sub,
	).Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err == nil && disabled {
		return nil, ErrDisabled
	}
	if err != nil {
		return nil, fmt.Errorf("get user by google id: %w", err)
	}
	return &u, nil
}

// CreateSession stores a session (by token hash) that expires at expiresAt,
// and records the login time for the admin dashboard.
func (r *UserRepository) CreateSession(ctx context.Context, tokenHash []byte, userID int64, expiresAt time.Time) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt,
	); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("record login: %w", err)
	}
	return nil
}

// UserBySession returns the user for a valid, unexpired session, or
// ErrNotFound. Suspended users have no valid sessions, so suspending an
// account logs it out everywhere immediately.
func (r *UserRepository) UserBySession(ctx context.Context, tokenHash []byte) (*domain.User, error) {
	var u domain.User
	err := r.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.role, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND NOT u.disabled`,
		tokenHash,
	).Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &u, nil
}

// DeleteSession logs a session out. Deleting a missing session is not an error.
func (r *UserRepository) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions removes stale rows so the table doesn't grow forever.
func (r *UserRepository) DeleteExpiredSessions(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= now()`); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

// nullIfEmpty stores "" as SQL NULL (so UNIQUE allows many accounts without
// a Google ID, and "no password" is explicit).
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
