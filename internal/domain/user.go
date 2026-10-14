package domain

import "time"

// Role controls what an account may do.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// User is an account. The password hash is deliberately not part of this
// struct, so it can never be accidentally serialized into an API response.
type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"` // stored lowercased; used for login and alert emails
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// Limits are the effective rules for one user: the global defaults with that
// user's overrides applied on top.
type Limits struct {
	MaxProducts          int  `json:"max_products"`           // how many products they may track
	CheckIntervalMinutes int  `json:"check_interval_minutes"` // how often their prices are re-checked
	AlertsEnabled        bool `json:"alerts_enabled"`         // whether they get "target hit" emails
	ManualChecksEnabled  bool `json:"manual_checks_enabled"`  // whether "Check price now" is allowed
}

// Overrides are a user's personal exceptions to the global defaults.
// nil means "use the default", so changing a default later still affects
// every user who hasn't been given a custom value.
type Overrides struct {
	MaxProducts          *int  `json:"max_products"`
	CheckIntervalMinutes *int  `json:"check_interval_minutes"`
	AlertsEnabled        *bool `json:"alerts_enabled"`
	ManualChecksEnabled  *bool `json:"manual_checks_enabled"`
}

// Apply returns defaults with every non-nil override replacing its value.
func (o Overrides) Apply(defaults Limits) Limits {
	l := defaults
	if o.MaxProducts != nil {
		l.MaxProducts = *o.MaxProducts
	}
	if o.CheckIntervalMinutes != nil {
		l.CheckIntervalMinutes = *o.CheckIntervalMinutes
	}
	if o.AlertsEnabled != nil {
		l.AlertsEnabled = *o.AlertsEnabled
	}
	if o.ManualChecksEnabled != nil {
		l.ManualChecksEnabled = *o.ManualChecksEnabled
	}
	return l
}

// Allowed ranges, shared by validation in the API and the admin UI.
const (
	MinCheckIntervalMinutes = 5           // anything faster is unfair to stores
	MaxCheckIntervalMinutes = 7 * 24 * 60 // one week
	MaxProductsLimit        = 10000
)

// AppSettings is the single global configuration row, edited by admins.
type AppSettings struct {
	Defaults    Limits `json:"defaults"`     // limits for every user without an override
	SignupsOpen bool   `json:"signups_open"` // whether new accounts can be created
	// Public price-history lookups (no login needed), and how often products
	// that are only looked up (tracked by nobody) are re-checked.
	PublicLookups              bool      `json:"public_lookups"`
	PublicCheckIntervalMinutes int       `json:"public_check_interval_minutes"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

// AdminUser is a user as shown in the admin dashboard.
type AdminUser struct {
	User
	Disabled     bool       `json:"disabled"`      // suspended: can't log in, checks and alerts paused
	HasPassword  bool       `json:"has_password"`  // can log in with email + password
	GoogleLinked bool       `json:"google_linked"` // can log in with Google
	LastLoginAt  *time.Time `json:"last_login_at"`
	ProductCount int        `json:"product_count"`
	Overrides    Overrides  `json:"overrides"`
	Effective    Limits     `json:"effective"` // what actually applies right now
}

// AdminStats are the numbers on the admin dashboard.
type AdminStats struct {
	Users           int `json:"users"`
	Admins          int `json:"admins"`
	DisabledUsers   int `json:"disabled_users"`
	Products        int `json:"products"`         // tracked products (one per user per listing)
	Listings        int `json:"listings"`         // distinct product pages being followed or looked up
	FailingProducts int `json:"failing_products"` // listings whose last check failed
	Checks24h       int `json:"checks_24h"`       // prices recorded in the last 24 hours
	Alerts7d        int `json:"alerts_7d"`        // alert emails sent in the last 7 days
}
