// Package scraper downloads a product page and extracts its price and name.
//
// It is intentionally polite and transparent: it identifies itself with an
// honest User-Agent, waits between requests to the same site, caps response
// size and time, and does not try to evade bot protection. Sites that block
// bots or render prices with JavaScript will simply report an error.
package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	// ErrNoPrice means the page loaded but no price could be found in it.
	ErrNoPrice = errors.New("no price found on the page")
	// ErrBlockedAddress means the URL resolved to a private/internal address.
	ErrBlockedAddress = errors.New("refusing to fetch a private or local network address")
)

// StatusError is returned when the store answers with a non-200 status.
type StatusError struct{ Code int }

func (e *StatusError) Error() string {
	return fmt.Sprintf("store responded %d %s", e.Code, http.StatusText(e.Code))
}

// Result is what a successful scrape returns.
type Result struct {
	Price    float64 // always > 0
	Currency string  // ISO code like "USD" if the page states it, else ""
	Title    string  // product name if found, else ""
	Source   string  // which extraction strategy found the price (for logs/debugging)

	// Optional product details; zero values mean "the page didn't say".
	Image       string // absolute http(s) URL of the main product image
	Brand       string
	Rating      float64 // average rating, on a scale of BestRating (usually 5)
	BestRating  float64
	RatingCount int
	ListPrice   float64 // MRP / list price before discount; 0 if unknown
	InStock     *bool   // nil if the page doesn't state availability
}

// Options configures a Scraper. Zero values get sensible defaults in New.
type Options struct {
	Timeout      time.Duration // whole-request limit (default 15s)
	MinHostGap   time.Duration // minimum spacing between requests to one host (default 5s)
	MaxBodyBytes int64         // response size cap (default 5 MiB)
	UserAgent    string
	// AllowPrivate permits fetching localhost/private IPs. Keep it false in
	// production: otherwise anyone who can add a product could make the server
	// probe your internal network (SSRF).
	AllowPrivate bool
}

// Scraper is safe for concurrent use by multiple goroutines.
type Scraper struct {
	client  *http.Client
	ua      string
	maxBody int64
	gate    *hostGate
}

// New builds a Scraper with its own HTTP client.
func New(o Options) *Scraper {
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Second
	}
	if o.MinHostGap == 0 {
		o.MinHostGap = 5 * time.Second
	}
	if o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = 5 << 20
	}
	if o.UserAgent == "" {
		o.UserAgent = "PricePulse/0.1 (personal price tracker)"
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !o.AllowPrivate {
		// Control runs after DNS resolution with the real IP being dialed, so a
		// hostname that resolves to 127.0.0.1 (or redirects there) is still caught.
		dialer.Control = blockPrivate
	}

	transport := &http.Transport{
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}

	return &Scraper{
		client:  &http.Client{Timeout: o.Timeout, Transport: transport},
		ua:      o.UserAgent,
		maxBody: o.MaxBodyBytes,
		gate:    &hostGate{gap: o.MinHostGap, next: map[string]time.Time{}},
	}
}

// Scrape fetches rawURL and extracts the price. ctx cancels both the wait for
// the per-host gap and the HTTP request itself.
func (s *Scraper) Scrape(ctx context.Context, rawURL string) (Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Result{}, fmt.Errorf("invalid product URL %q", rawURL)
	}

	if err := s.gate.wait(ctx, strings.ToLower(u.Hostname())); err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", s.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")
	req.Header.Set("Accept-Language", "en;q=0.9")

	resp, err := s.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Result{}, &StatusError{Code: resp.StatusCode}
	}

	// LimitReader stops a huge (or endless) response from exhausting memory.
	res, err := Extract(io.LimitReader(resp.Body, s.maxBody))
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// blockPrivate is a net.Dialer Control hook that rejects non-public IPs.
func blockPrivate(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return ErrBlockedAddress
	}
	return nil
}

// hostGate spaces out requests per host so we never hammer one store, even
// when several of its products are checked concurrently.
type hostGate struct {
	mu   sync.Mutex
	gap  time.Duration
	next map[string]time.Time // earliest time the next request to a host may start
}

// wait reserves the next free slot for host and sleeps until it arrives.
// Reserving under the lock (rather than sleeping under it) lets callers for
// different hosts proceed in parallel while same-host callers queue up.
func (g *hostGate) wait(ctx context.Context, host string) error {
	g.mu.Lock()
	now := time.Now()
	slot := g.next[host]
	if slot.Before(now) {
		slot = now
	}
	g.next[host] = slot.Add(g.gap)
	g.mu.Unlock()

	d := time.Until(slot)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
