package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"pricepulse/internal/domain"
)

// --- global settings ---

// Settings returns the global configuration.
func (r *UserRepository) Settings(ctx context.Context) (domain.AppSettings, error) {
	var s domain.AppSettings
	err := r.db.QueryRowContext(ctx, `
		SELECT max_products, check_interval_minutes, alerts_enabled, manual_checks_enabled, signups_open,
		       public_lookups, public_check_interval_minutes, updated_at
		FROM app_settings WHERE id = 1`,
	).Scan(&s.Defaults.MaxProducts, &s.Defaults.CheckIntervalMinutes, &s.Defaults.AlertsEnabled,
		&s.Defaults.ManualChecksEnabled, &s.SignupsOpen, &s.PublicLookups, &s.PublicCheckIntervalMinutes, &s.UpdatedAt)
	if err != nil {
		return s, fmt.Errorf("get settings: %w", err)
	}
	return s, nil
}

// UpdateSettings replaces the global configuration.
func (r *UserRepository) UpdateSettings(ctx context.Context, s domain.AppSettings) (domain.AppSettings, error) {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE app_settings
		SET max_products = $1, check_interval_minutes = $2, alerts_enabled = $3,
		    manual_checks_enabled = $4, signups_open = $5,
		    public_lookups = $6, public_check_interval_minutes = $7, updated_at = now()
		WHERE id = 1`,
		s.Defaults.MaxProducts, s.Defaults.CheckIntervalMinutes, s.Defaults.AlertsEnabled,
		s.Defaults.ManualChecksEnabled, s.SignupsOpen, s.PublicLookups, s.PublicCheckIntervalMinutes,
	); err != nil {
		return s, fmt.Errorf("update settings: %w", err)
	}
	return r.Settings(ctx)
}

// Limits returns the effective limits for one user (defaults + overrides),
// computed in SQL with COALESCE: the override if set, else the default.
func (r *UserRepository) Limits(ctx context.Context, userID int64) (domain.Limits, error) {
	var l domain.Limits
	err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(u.max_products, s.max_products),
		       COALESCE(u.check_interval_minutes, s.check_interval_minutes),
		       COALESCE(u.alerts_enabled, s.alerts_enabled),
		       COALESCE(u.manual_checks_enabled, s.manual_checks_enabled)
		FROM users u CROSS JOIN app_settings s
		WHERE u.id = $1`, userID,
	).Scan(&l.MaxProducts, &l.CheckIntervalMinutes, &l.AlertsEnabled, &l.ManualChecksEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	if err != nil {
		return l, fmt.Errorf("get limits: %w", err)
	}
	return l, nil
}

// --- users, for admins ---

// adminUserColumns are read by scanAdminUser in this order. The settings
// table is joined so the effective limits come back in the same query.
const adminUserColumns = `
	u.id, u.email, u.role, u.created_at, u.disabled,
	u.password_hash IS NOT NULL, u.google_sub IS NOT NULL, u.last_login_at,
	(SELECT count(*) FROM products p WHERE p.user_id = u.id),
	u.max_products, u.check_interval_minutes, u.alerts_enabled, u.manual_checks_enabled,
	s.max_products, s.check_interval_minutes, s.alerts_enabled, s.manual_checks_enabled`

func scanAdminUser(sc rowScanner) (*domain.AdminUser, error) {
	var u domain.AdminUser
	var lastLogin sql.NullTime
	var maxP, interval sql.NullInt64
	var alerts, manual sql.NullBool
	var defaults domain.Limits
	err := sc.Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt, &u.Disabled,
		&u.HasPassword, &u.GoogleLinked, &lastLogin, &u.ProductCount,
		&maxP, &interval, &alerts, &manual,
		&defaults.MaxProducts, &defaults.CheckIntervalMinutes, &defaults.AlertsEnabled, &defaults.ManualChecksEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if lastLogin.Valid {
		u.LastLoginAt = &lastLogin.Time
	}
	// Convert SQL NULLs into nil pointers ("use the default").
	if maxP.Valid {
		v := int(maxP.Int64)
		u.Overrides.MaxProducts = &v
	}
	if interval.Valid {
		v := int(interval.Int64)
		u.Overrides.CheckIntervalMinutes = &v
	}
	if alerts.Valid {
		u.Overrides.AlertsEnabled = &alerts.Bool
	}
	if manual.Valid {
		u.Overrides.ManualChecksEnabled = &manual.Bool
	}
	u.Effective = u.Overrides.Apply(defaults)
	return &u, nil
}

// ListUsers returns every account for the admin dashboard, newest first.
func (r *UserRepository) ListUsers(ctx context.Context) ([]domain.AdminUser, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+adminUserColumns+` FROM users u CROSS JOIN app_settings s ORDER BY u.created_at DESC, u.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := []domain.AdminUser{}
	for rows.Next() {
		u, err := scanAdminUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

// AdminUser returns one account for the admin dashboard, or ErrNotFound.
func (r *UserRepository) AdminUser(ctx context.Context, id int64) (*domain.AdminUser, error) {
	return scanAdminUser(r.db.QueryRowContext(ctx,
		`SELECT `+adminUserColumns+` FROM users u CROSS JOIN app_settings s WHERE u.id = $1`, id))
}

// UpdateUser sets a user's role, suspension and overrides (nil = use default).
// Suspending also deletes their sessions, logging them out everywhere now.
func (r *UserRepository) UpdateUser(ctx context.Context, id int64, role domain.Role, disabled bool, o domain.Overrides) (*domain.AdminUser, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE users
		SET role = $2, disabled = $3, max_products = $4, check_interval_minutes = $5,
		    alerts_enabled = $6, manual_checks_enabled = $7
		WHERE id = $1`,
		id, role, disabled, o.MaxProducts, o.CheckIntervalMinutes, o.AlertsEnabled, o.ManualChecksEnabled,
	)
	if err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, id); err != nil {
			return nil, fmt.Errorf("revoke sessions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return r.AdminUser(ctx, id)
}

// DeleteUser removes an account; its products, history and sessions go with
// it (ON DELETE CASCADE).
func (r *UserRepository) DeleteUser(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Stats returns the numbers for the admin dashboard in one round trip.
func (r *UserRepository) Stats(ctx context.Context) (domain.AdminStats, error) {
	var s domain.AdminStats
	err := r.db.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM users),
			(SELECT count(*) FROM users WHERE role = 'admin'),
			(SELECT count(*) FROM users WHERE disabled),
			(SELECT count(*) FROM products),
			(SELECT count(*) FROM listings),
			(SELECT count(*) FROM listings WHERE last_error <> ''),
			(SELECT count(*) FROM listing_prices WHERE source = 'scraper' AND recorded_at > now() - interval '24 hours'),
			(SELECT count(*) FROM products WHERE alert_sent_at > now() - interval '7 days')`,
	).Scan(&s.Users, &s.Admins, &s.DisabledUsers, &s.Products, &s.Listings, &s.FailingProducts, &s.Checks24h, &s.Alerts7d)
	if err != nil {
		return s, fmt.Errorf("stats: %w", err)
	}
	return s, nil
}

// EnsureAdmin makes sure an admin account exists for email (from .env):
//   - no such account: create it with the given password hash, as admin
//   - account exists: make it an admin and un-suspend it, without touching
//     its password (so ADMIN_EMAIL can also promote your own account)
//
// Returns "created", "promoted" or "" (already an active admin).
func (r *UserRepository) EnsureAdmin(ctx context.Context, email, passwordHash string) (string, error) {
	var role domain.Role
	var disabled bool
	err := r.db.QueryRowContext(ctx, `SELECT role, disabled FROM users WHERE email = $1`, email).Scan(&role, &disabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Inserted directly (not via CreateUser) so the admin never adopts
		// products created before accounts existed.
		if _, err := r.db.ExecContext(ctx,
			`INSERT INTO users (email, password_hash, role) VALUES ($1, $2, 'admin')`, email, passwordHash,
		); err != nil {
			return "", fmt.Errorf("create admin: %w", err)
		}
		return "created", nil
	case err != nil:
		return "", fmt.Errorf("look up admin: %w", err)
	case role == domain.RoleAdmin && !disabled:
		return "", nil
	}
	if _, err := r.db.ExecContext(ctx,
		`UPDATE users SET role = 'admin', disabled = false WHERE email = $1`, email,
	); err != nil {
		return "", fmt.Errorf("promote admin: %w", err)
	}
	return "promoted", nil
}
