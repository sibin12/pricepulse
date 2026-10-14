// Package worker periodically re-checks listing prices in the background and
// also serves on-demand "check now" and public lookup requests from the API.
// It works on listings (one per product page), so a product followed by many
// users is still fetched from the store only once per check.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"pricepulse/internal/domain"
	"pricepulse/internal/repository"
	"pricepulse/internal/scraper"
)

// Scraper is the one method the worker needs. Depending on an interface
// instead of *scraper.Scraper lets tests plug in a fake.
type Scraper interface {
	Scrape(ctx context.Context, url string) (scraper.Result, error)
}

// Nudger is told whenever a new price is stored, so the alert notifier can
// email immediately instead of waiting for its next poll.
type Nudger interface {
	Nudge()
}

// Config controls scheduling. How often each product is re-checked is not
// configured here: it comes from the database (global default or the owner's
// override), so admins can change it without restarting.
type Config struct {
	Poll        time.Duration // how often to look for due products (e.g. 1m)
	Concurrency int           // max products scraped at the same time
	BatchSize   int           // max products picked up per poll
}

// Worker is safe for concurrent use: the background loop and HTTP handlers
// can call Check at the same time.
type Worker struct {
	repo    *repository.ProductRepository
	scraper Scraper
	logger  *log.Logger
	cfg     Config
	alerts  Nudger

	mu       sync.Mutex
	inflight map[int64]*call // listing ID -> check currently running
}

// call lets a second request for the same listing wait for the first one's
// result instead of scraping the store twice (a tiny "singleflight").
type call struct {
	done chan struct{}
	err  error
}

// New creates a Worker, filling in defaults for zero config values.
func New(repo *repository.ProductRepository, s Scraper, alerts Nudger, logger *log.Logger, cfg Config) *Worker {
	if cfg.Poll <= 0 {
		cfg.Poll = time.Minute
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	return &Worker{repo: repo, scraper: s, alerts: alerts, logger: logger, cfg: cfg, inflight: map[int64]*call{}}
}

// Run checks due products every Poll until ctx is cancelled (server shutdown).
// It runs one pass immediately so a freshly started server catches up at once.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Printf("price worker started (polling every %s, concurrency %d; re-check intervals come from admin settings)",
		w.cfg.Poll, w.cfg.Concurrency)
	ticker := time.NewTicker(w.cfg.Poll)
	defer ticker.Stop()

	for {
		w.runDue(ctx)
		select {
		case <-ctx.Done():
			w.logger.Println("price worker stopped")
			return
		case <-ticker.C:
		}
	}
}

// runDue scrapes every listing that is due, at most Concurrency at a time,
// and returns when the whole batch is finished.
func (w *Worker) runDue(ctx context.Context) {
	due, err := w.repo.ListingsDue(ctx, w.cfg.BatchSize)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Printf("worker: list due listings: %v", err)
		}
		return
	}
	if len(due) == 0 {
		return
	}

	// A buffered channel used as a semaphore: sending takes a slot, receiving
	// frees it, so at most cap(sem) goroutines scrape at once.
	sem := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup
	for _, l := range due {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := w.Check(ctx, id); err != nil && ctx.Err() == nil {
				w.logger.Printf("worker: check listing %d: %v", id, err)
			}
		}(l.ID)
	}
	wg.Wait()
}

// Check scrapes one listing now and stores the outcome. A failed scrape is
// not an error here: it is recorded on the listing (LastError). Errors are
// only returned for database problems, unknown listings, or cancellation.
func (w *Worker) Check(ctx context.Context, listingID int64) error {
	w.mu.Lock()
	if c, ok := w.inflight[listingID]; ok {
		w.mu.Unlock()
		select {
		case <-c.done:
			return c.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	w.inflight[listingID] = c
	w.mu.Unlock()

	c.err = w.check(ctx, listingID)

	w.mu.Lock()
	delete(w.inflight, listingID)
	w.mu.Unlock()
	close(c.done) // wakes every waiter; they read c.err
	return c.err
}

func (w *Worker) check(ctx context.Context, id int64) error {
	l, err := w.repo.Listing(ctx, id)
	if err != nil {
		return err
	}

	res, scrapeErr := w.scraper.Scrape(ctx, l.URL)

	// If we are shutting down (or the caller gave up), don't record a bogus
	// failure: the listing simply stays due and is retried next time.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Database writes use a context detached from ctx's cancellation, so a
	// scrape that already finished is always saved, but still time-bounded.
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()

	if scrapeErr != nil {
		msg := describe(scrapeErr)
		w.logger.Printf("worker: listing %d (%s): %s", id, l.URL, msg)
		return w.repo.MarkListingFailed(dbCtx, id, msg, now)
	}

	// Only well-formed ISO currency codes are stored (browsers reject others).
	currency := strings.ToUpper(strings.TrimSpace(res.Currency))
	if !isCurrencyCode(currency) {
		currency = ""
	}
	if err := w.repo.RecordListingPrice(dbCtx, id, res.Price, currency, truncate(res.Title, 200), details(res), now); err != nil {
		return err
	}

	// The notifier decides (from the database) whether any tracker's target
	// was hit; we just wake it so a drop is emailed within seconds.
	w.alerts.Nudge()
	w.logger.Printf("worker: listing %d price %.2f %s (via %s)", id, res.Price, currency, res.Source)
	return nil
}

// describe turns a scrape error into a short message suitable for the UI.
func describe(err error) string {
	var status *scraper.StatusError
	var netErr net.Error
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, scraper.ErrNoPrice):
		return "No price found on the page. The store may load prices with JavaScript or not publish structured data."
	case errors.Is(err, scraper.ErrBlockedAddress):
		return "This URL points to a private or local network address, which is not allowed."
	case errors.As(err, &status):
		if status.Code == 403 || status.Code == 429 || status.Code == 503 {
			return fmt.Sprintf("The store refused the request (%d). It probably blocks automated access.", status.Code)
		}
		return fmt.Sprintf("The store responded with an error (%d).", status.Code)
	case errors.As(err, &dnsErr):
		return "The store's domain could not be found."
	case errors.As(err, &netErr) && netErr.Timeout():
		return "The store took too long to respond."
	}
	return truncate("Could not fetch the page: "+err.Error(), 300)
}

// details converts the scraper's optional fields (zero = unknown) into the
// domain's pointers (nil = unknown, keep the previous value).
func details(r scraper.Result) domain.ListingDetails {
	d := domain.ListingDetails{ImageURL: truncate(r.Image, 1000), Brand: truncate(r.Brand, 100), InStock: r.InStock}
	if r.Rating > 0 && r.BestRating > 0 {
		rating, best := r.Rating, r.BestRating
		d.Rating, d.BestRating = &rating, &best
		if r.RatingCount > 0 {
			count := r.RatingCount
			d.RatingCount = &count
		}
	}
	if r.ListPrice > 0 {
		mrp := r.ListPrice
		d.MRP = &mrp
	}
	return d
}

// isCurrencyCode reports whether s looks like an ISO 4217 code ("USD", "INR").
func isCurrencyCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
