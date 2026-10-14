// Package domain holds the core business entities of PricePulse.
//
// It is deliberately free of any database, HTTP or framework imports: the
// domain layer should not know *how* data is stored or delivered. Every other
// layer (repository, HTTP handlers, scraper worker) depends on this package,
// never the reverse.
package domain

import (
	"net/url"
	"strings"
	"time"
)

// Product is one user's tracking of a Listing: the shared product page and its
// price history, plus this user's own name, target price and alert state.
//
// The `json:"..."` struct tags control how the struct is encoded/decoded by
// encoding/json, so the API returns snake_case keys (what JS frontends expect)
// while the Go fields stay idiomatic CamelCase.
type Product struct {
	ID            int64     `json:"id"`              // assigned by PostgreSQL (BIGSERIAL), so int64
	URL           string    `json:"url"`             // page of the product being tracked
	Title         string    `json:"title"`           // the user's name for it, else the store's product name
	CurrentPrice  float64   `json:"current_price"`   // most recently observed price; 0 = never priced
	TargetPrice   float64   `json:"target_price"`    // price at/below which the user wants an alert
	Currency      string    `json:"currency"`        // ISO 4217 code from the store page, e.g. "INR"; "" if unknown
	LastCheckedAt time.Time `json:"last_checked_at"` // when a price was last successfully recorded
	CreatedAt     time.Time `json:"created_at"`      // when tracking started

	// Scraper status. LastAttemptAt is a pointer so "never attempted" is
	// encoded as JSON null instead of a fake zero date.
	LastAttemptAt *time.Time `json:"last_attempt_at"` // last automatic/manual check, successful or not
	LastError     string     `json:"last_error"`      // why the last check failed; "" if it succeeded

	// AlertSentAt is set when the "price hit target" email went out, and reset
	// to nil when the price rises above the target again or the target changes,
	// so each drop to the target produces exactly one email.
	AlertSentAt *time.Time `json:"alert_sent_at"`

	// Bucket list: why the user wants it, and whether they got it.
	Note        string     `json:"note"`         // "why I want it" (optional)
	BoughtAt    *time.Time `json:"bought_at"`    // nil = still wishing
	BoughtPrice *float64   `json:"bought_price"` // what they paid

	// From the shared listing (store prices only, never manual entries).
	ListingDetails
	ListingID        int64       `json:"listing_id"`
	LowestPrice      *float64    `json:"lowest_price"`       // all-time lowest; nil until first price
	LowestPriceDates []time.Time `json:"lowest_price_dates"` // every day the lowest price was seen
	HighestPrice     *float64    `json:"highest_price"`
}

// AtTarget reports whether the product has a known price at or below its target.
func (p *Product) AtTarget() bool {
	return p.CurrentPrice > 0 && p.CurrentPrice <= p.TargetPrice
}

// DefaultTitle makes a readable fallback title from the URL's host,
// e.g. "https://www.example.com/item/42" -> "example.com".
func DefaultTitle(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return rawURL
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

// Price sources: the scraper (shared, public) or a user's manual entry
// (private to that user).
const (
	SourceScraper = "scraper"
	SourceManual  = "manual"
)

// PriceHistory is one price observation of a listing.
type PriceHistory struct {
	ID         int64     `json:"id"`
	Price      float64   `json:"price"`
	RecordedAt time.Time `json:"recorded_at"`
	Source     string    `json:"source"` // SourceScraper or SourceManual
}

// Listing is one product page on a store, shared by everyone who tracks or
// looks it up. Its price history is collected once, no matter how many users
// follow it, and powers the public price-history pages.
type Listing struct {
	ID               int64       `json:"id"`
	URL              string      `json:"url"`
	Title            string      `json:"title"`    // store's product name ("" until first successful check)
	Currency         string      `json:"currency"` // ISO code, "" if unknown
	CurrentPrice     float64     `json:"current_price"`
	LowestPrice      *float64    `json:"lowest_price"`
	LowestPriceDates []time.Time `json:"lowest_price_dates"` // one entry per day the lowest was seen
	HighestPrice     *float64    `json:"highest_price"`
	LastCheckedAt    *time.Time  `json:"last_checked_at"` // last successful price
	LastAttemptAt    *time.Time  `json:"last_attempt_at"` // last check, successful or not
	LastError        string      `json:"last_error"`
	Trackers         int         `json:"trackers"` // how many users track it
	CreatedAt        time.Time   `json:"created_at"`
	ListingDetails
}

// ListingDetails are optional facts read from the store page. Nil/empty means
// the store didn't publish them.
type ListingDetails struct {
	ImageURL    string   `json:"image_url"`
	Brand       string   `json:"brand"`
	Rating      *float64 `json:"rating"`      // e.g. 4.3
	BestRating  *float64 `json:"best_rating"` // scale of Rating, usually 5
	RatingCount *int     `json:"rating_count"`
	MRP         *float64 `json:"mrp"` // list price before discount
	InStock     *bool    `json:"in_stock"`
}

// DisplayTitle is the store's name for the product, or the site's domain
// until the first successful check.
func (l *Listing) DisplayTitle() string {
	if l.Title != "" {
		return l.Title
	}
	return DefaultTitle(l.URL)
}
