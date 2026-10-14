// Package httpapi is the HTTP delivery layer: it turns HTTP requests into
// repository calls and repository results into JSON responses.
package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pricepulse/internal/alerts"
	"pricepulse/internal/auth"
	"pricepulse/internal/domain"
	"pricepulse/internal/repository"
)

const (
	// queryTimeout bounds every database call made while serving a request.
	queryTimeout = 3 * time.Second
	// checkTimeout bounds an on-demand scrape (waiting for the store + saving).
	// It must stay below the server's WriteTimeout or the response is cut off.
	checkTimeout = 25 * time.Second
)

// Checker scrapes a listing's price on demand. It is satisfied by
// *worker.Worker; the interface keeps this package independent of the worker.
type Checker interface {
	Check(ctx context.Context, listingID int64) error
}

// Alerter is the email-alert API the handlers need; satisfied by *alerts.Notifier.
type Alerter interface {
	Enabled() bool
	Nudge()
	SendTest(ctx context.Context, to string) error
}

// Deps are everything the HTTP layer needs, built once in main.
type Deps struct {
	Logger        *log.Logger
	DB            *sql.DB
	Products      *repository.ProductRepository
	Users         *repository.UserRepository
	Checker       Checker
	Alerts        Alerter
	Google        *auth.Google  // nil when Google sign-in isn't configured
	AppURL        string        // web UI base URL, for redirects after Google sign-in
	SecureCookies bool          // set true when served over HTTPS
	SessionTTL    time.Duration // how long a login lasts
}

// handler holds the dependencies shared by all routes. Methods on it are the
// route handlers, so they can reach the repositories without global variables.
type handler struct {
	db            *sql.DB
	repo          *repository.ProductRepository
	users         *repository.UserRepository
	checker       Checker
	alerts        Alerter
	google        *auth.Google
	limiter       *auth.Limiter
	lookupLimiter *auth.Limiter
	appURL        string
	secureCookies bool
	sessionTTL    time.Duration
	logger        *log.Logger
}

// NewRouter registers all routes and returns them as one http.Handler.
func NewRouter(d Deps) http.Handler {
	if d.SessionTTL <= 0 {
		d.SessionTTL = 30 * 24 * time.Hour
	}
	h := &handler{
		db: d.DB, repo: d.Products, users: d.Users, checker: d.Checker, alerts: d.Alerts,
		google: d.Google, appURL: strings.TrimRight(d.AppURL, "/"),
		secureCookies: d.SecureCookies, sessionTTL: d.SessionTTL, logger: d.Logger,
		// 10 failed logins per IP+email per 15 minutes.
		limiter: auth.NewLimiter(10, 15*time.Minute),
		// 20 lookups of not-yet-known products (each one fetches a store page)
		// per IP per 10 minutes, so the public page can't be used as a free
		// scraping service.
		lookupLimiter: auth.NewLimiter(20, 10*time.Minute),
	}

	// Go 1.22+ ServeMux supports "METHOD /path/{param}" patterns, so no
	// third-party router is needed. A wrong method on a known path gets 405.
	mux := http.NewServeMux()

	// Public routes.
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /auth/config", h.authConfig)
	mux.HandleFunc("POST /auth/register", h.register)
	mux.HandleFunc("POST /auth/login", h.login)
	mux.HandleFunc("POST /auth/logout", h.logout)

	// Public price-history pages (login optional; admins can switch them off).
	mux.HandleFunc("POST /lookup", h.lookup)
	mux.HandleFunc("GET /listings/{id}", h.getListing)
	mux.HandleFunc("GET /auth/google/start", h.googleStart)
	mux.HandleFunc("GET /auth/google/callback", h.googleCallback)

	// Everything else requires a logged-in user, and only ever touches that
	// user's own products.
	mux.HandleFunc("GET /auth/me", h.requireUser(h.me))
	mux.HandleFunc("GET /products", h.requireUser(h.listProducts))
	mux.HandleFunc("POST /products", h.requireUser(h.createProduct))
	mux.HandleFunc("GET /products/{id}", h.requireUser(h.getProduct))
	mux.HandleFunc("PATCH /products/{id}", h.requireUser(h.updateProduct))
	mux.HandleFunc("DELETE /products/{id}", h.requireUser(h.deleteProduct))
	mux.HandleFunc("GET /products/{id}/history", h.requireUser(h.listHistory))
	mux.HandleFunc("POST /products/{id}/prices", h.requireUser(h.recordPrice))
	mux.HandleFunc("POST /products/{id}/check", h.requireUser(h.checkProduct))
	mux.HandleFunc("POST /products/{id}/bought", h.requireUser(h.markBought))
	mux.HandleFunc("DELETE /products/{id}/bought", h.requireUser(h.unmarkBought))
	mux.HandleFunc("GET /alerts", h.requireUser(h.alertStatus))
	mux.HandleFunc("POST /alerts/test", h.requireUser(h.sendTestAlert))

	// Admin dashboard: requireAdmin returns 403 for everyone else.
	mux.HandleFunc("GET /admin/stats", h.requireAdmin(h.adminStats))
	mux.HandleFunc("GET /admin/settings", h.requireAdmin(h.adminGetSettings))
	mux.HandleFunc("PUT /admin/settings", h.requireAdmin(h.adminUpdateSettings))
	mux.HandleFunc("GET /admin/users", h.requireAdmin(h.adminListUsers))
	mux.HandleFunc("GET /admin/users/{id}", h.requireAdmin(h.adminGetUser))
	mux.HandleFunc("PUT /admin/users/{id}", h.requireAdmin(h.adminUpdateUser))
	mux.HandleFunc("DELETE /admin/users/{id}", h.requireAdmin(h.adminDeleteUser))
	return mux
}

// health is used by load balancers/monitors to know if the service works.
// It checks the database too, so "healthy" means "actually able to serve".
func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel() // always release the context's timer
	if err := h.db.PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "database": "down"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy", "database": "up"})
}

func (h *handler) listProducts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	products, err := h.repo.List(ctx, currentUser(r).ID)
	if err != nil {
		h.serverError(w, "list products", err)
		return
	}
	writeJSON(w, http.StatusOK, products)
}

// createProduct validates input, saves the product, and returns 201 + the product.
// If an initial current_price is given it is also recorded as the first history point.
func (h *handler) createProduct(w http.ResponseWriter, r *http.Request) {
	// An anonymous struct models exactly what the client may send. We don't decode
	// straight into domain.Product so clients can't set fields like id or created_at.
	var req struct {
		URL          string  `json:"url"`
		Title        string  `json:"title"`
		Note         string  `json:"note"` // optional "why I want it"
		TargetPrice  float64 `json:"target_price"`
		CurrentPrice float64 `json:"current_price"` // optional; 0 means "not checked yet"
	}
	if !decodeJSON(w, r, &req) {
		return
	}

	req.Title = strings.TrimSpace(req.Title)
	req.Note = strings.TrimSpace(req.Note)
	if msg := validateNote(req.Note); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}

	// Decoding only checks JSON *shape*; business rules must be checked explicitly.
	// 422 = well-formed request, but the values are unacceptable.
	fetchURL, key, err := domain.ListingURL(req.URL)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if req.TargetPrice <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "target_price must be greater than 0")
		return
	}
	if req.CurrentPrice < 0 {
		writeError(w, http.StatusUnprocessableEntity, "current_price cannot be negative")
		return
	}

	// Derived from the request context: the query is cancelled if the client
	// disconnects, and bounded by a deadline either way.
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	uid := currentUser(r).ID

	limits, err := h.users.Limits(ctx, uid)
	if err != nil {
		h.serverError(w, "load limits", err)
		return
	}
	// Everyone tracking the same product page shares one listing (and one
	// price history), so a product someone already follows has history at once.
	listing, _, err := h.repo.FindOrCreateListing(ctx, fetchURL, key)
	if err != nil {
		h.serverError(w, "find listing", err)
		return
	}
	// Store times in UTC so they are consistent regardless of server location.
	now := time.Now().UTC()
	p, err := h.repo.Create(ctx, uid, limits.MaxProducts, listing.ID, req.Title, req.Note, req.TargetPrice, now)
	var already *repository.AlreadyTrackingError
	switch {
	case errors.As(err, &already):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "you're already tracking this product",
			"product_id": already.ProductID,
		})
		return
	case errors.Is(err, repository.ErrLimitReached):
		writeError(w, http.StatusForbidden, fmt.Sprintf(
			"you've reached your limit of %d products; delete one or ask an admin to raise your limit", limits.MaxProducts))
		return
	case err != nil:
		h.serverError(w, "create product", err)
		return
	}
	if req.CurrentPrice > 0 {
		// A price typed in by the user is private to them (never public).
		if _, err := h.repo.RecordManualPrice(ctx, uid, p.ID, req.CurrentPrice, now); err != nil {
			h.serverError(w, "record initial price", err)
			return
		}
		if p, err = h.repo.Owned(ctx, uid, p.ID); err != nil {
			h.serverError(w, "reload product", err)
			return
		}
	}
	// The listing may already be at or below this target: let the notifier look.
	h.alerts.Nudge()
	writeJSON(w, http.StatusCreated, p)
}

func (h *handler) getProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	p, err := h.repo.Owned(ctx, currentUser(r).ID, id)
	if err != nil {
		h.repoError(w, "get product", err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// updateProduct supports partial updates: only fields present in the JSON change.
func (h *handler) updateProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// Pointer fields distinguish "field omitted" (nil) from "field sent".
	var req struct {
		Title       *string  `json:"title"`
		Note        *string  `json:"note"`
		TargetPrice *float64 `json:"target_price"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Title == nil && req.TargetPrice == nil && req.Note == nil {
		writeError(w, http.StatusUnprocessableEntity, "nothing to update: send title, note and/or target_price")
		return
	}
	if req.Note != nil {
		n := strings.TrimSpace(*req.Note)
		if msg := validateNote(n); msg != "" {
			writeError(w, http.StatusUnprocessableEntity, msg)
			return
		}
		req.Note = &n
	}
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" {
			writeError(w, http.StatusUnprocessableEntity, "title cannot be empty")
			return
		}
		req.Title = &t
	}
	if req.TargetPrice != nil && *req.TargetPrice <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "target_price must be greater than 0")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	p, err := h.repo.Update(ctx, currentUser(r).ID, id, req.Title, req.Note, req.TargetPrice)
	if err != nil {
		h.repoError(w, "update product", err)
		return
	}
	h.alerts.Nudge() // a new target may already be met by the current price
	writeJSON(w, http.StatusOK, p)
}

func (h *handler) deleteProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	if err := h.repo.Delete(ctx, currentUser(r).ID, id); err != nil {
		h.repoError(w, "delete product", err)
		return
	}
	// 204 No Content: success, and there is nothing meaningful to return.
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	// Check ownership first: someone else's (or an unknown) ID gives 404,
	// never another user's history.
	if _, err := h.repo.Owned(ctx, currentUser(r).ID, id); err != nil {
		h.repoError(w, "get product", err)
		return
	}
	history, err := h.repo.History(ctx, currentUser(r).ID, id)
	if err != nil {
		h.serverError(w, "list history", err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// recordPrice logs a price by hand (for stores the scraper can't read). It is
// private to this user: it never appears on the public price-history page.
func (h *handler) recordPrice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Price float64 `json:"price"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Price <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "price must be greater than 0")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	// Ownership is enforced inside RecordManualPrice (ErrNotFound otherwise).
	entry, err := h.repo.RecordManualPrice(ctx, currentUser(r).ID, id, req.Price, time.Now().UTC())
	if err != nil {
		h.repoError(w, "record price", err)
		return
	}
	h.alerts.Nudge() // email right away if this price hits the target
	writeJSON(w, http.StatusCreated, entry)
}

// checkProduct scrapes the product's page right now and returns the updated
// product. A failed scrape still returns 200: the reason is in last_error, and
// the UI shows it. Only unknown products and server problems are HTTP errors.
func (h *handler) checkProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()

	product, err := h.repo.Owned(ctx, currentUser(r).ID, id)
	if err != nil {
		h.repoError(w, "check product", err)
		return
	}
	limits, err := h.users.Limits(ctx, currentUser(r).ID)
	if err != nil {
		h.serverError(w, "load limits", err)
		return
	}
	if !limits.ManualChecksEnabled {
		writeError(w, http.StatusForbidden, "manual price checks are turned off for your account; prices are still checked automatically")
		return
	}
	if err := h.checker.Check(ctx, product.ListingID); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "the price check took too long; it will be retried automatically")
			return
		}
		h.serverError(w, "check product", err)
		return
	}
	p, err := h.repo.Owned(ctx, currentUser(r).ID, id)
	if err != nil {
		h.repoError(w, "reload product", err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// alertStatus tells the UI whether email alerts are configured and where they
// go (always the logged-in user's own address).
func (h *handler) alertStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    h.alerts.Enabled(),
		"recipients": []string{currentUser(r).Email},
	})
}

// sendTestAlert emails a sample message so the user can verify SMTP settings.
func (h *handler) sendTestAlert(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()

	to := currentUser(r).Email
	if err := h.alerts.SendTest(ctx, to); err != nil {
		if errors.Is(err, alerts.ErrDisabled) {
			writeError(w, http.StatusConflict, "email alerts are not configured on the server")
			return
		}
		// Unlike most errors we show the detail: SMTP errors ("535 bad
		// credentials") are exactly what the user needs to fix their settings.
		h.logger.Printf("send test email: %v", err)
		writeError(w, http.StatusBadGateway, "sending failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent_to": []string{to}})
}

// markBought checks a product off the bucket list. The price paid defaults to
// the current price; it is used to show how much the user saved.
func (h *handler) markBought(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Price float64 `json:"price"` // optional
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	if req.Price < 0 {
		writeError(w, http.StatusUnprocessableEntity, "price cannot be negative")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	uid := currentUser(r).ID

	if req.Price == 0 {
		p, err := h.repo.Owned(ctx, uid, id)
		if err != nil {
			h.repoError(w, "get product", err)
			return
		}
		req.Price = p.CurrentPrice
	}
	if req.Price <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "enter the price you paid")
		return
	}
	p, err := h.repo.MarkBought(ctx, uid, id, req.Price, time.Now().UTC())
	if err != nil {
		h.repoError(w, "mark bought", err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// unmarkBought puts a product back on the wish list (needs a free slot).
func (h *handler) unmarkBought(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	uid := currentUser(r).ID
	limits, err := h.users.Limits(ctx, uid)
	if err != nil {
		h.serverError(w, "load limits", err)
		return
	}
	p, err := h.repo.UnmarkBought(ctx, uid, id, limits.MaxProducts)
	if errors.Is(err, repository.ErrLimitReached) {
		writeError(w, http.StatusForbidden, fmt.Sprintf(
			"your bucket list is full (%d items); remove or check off an item first", limits.MaxProducts))
		return
	}
	if err != nil {
		h.repoError(w, "unmark bought", err)
		return
	}
	h.alerts.Nudge() // it may already be at its target
	writeJSON(w, http.StatusOK, p)
}

// --- request helpers ---

// validateNote limits the "why I want it" note to something card-sized.
func validateNote(n string) string {
	if len([]rune(n)) > 280 {
		return "note must be at most 280 characters"
	}
	return ""
}

// pathID parses the {id} path segment. On failure it writes a 400 and returns false,
// so handlers can simply `return`.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

// --- error helpers ---

// repoError maps repository errors to HTTP statuses: ErrNotFound -> 404, else 500.
func (h *handler) repoError(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}
	h.serverError(w, op, err)
}

// serverError logs the real error for us but returns a generic message to the
// client, so we don't leak internals (table names, SQL details).
func (h *handler) serverError(w http.ResponseWriter, op string, err error) {
	h.logger.Printf("%s: %v", op, err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}
