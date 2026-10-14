package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"pricepulse/internal/auth"
	"pricepulse/internal/domain"
	"pricepulse/internal/repository"
)

// Public price-history pages: anyone can paste a product link and see the
// price history PricePulse has collected for it. Logged-in users also learn
// whether they already track it. Only store-scraped prices are shown; prices
// users typed in by hand stay private.

// lookupResponse is what both public endpoints return.
type lookupResponse struct {
	Listing *domain.Listing       `json:"listing"`
	History []domain.PriceHistory `json:"history"`
	// MyProductID is set when the logged-in user already tracks this product.
	MyProductID *int64 `json:"my_product_id"`
}

// lookup finds (or starts) the price history for a product URL. A product
// nobody has looked up before is fetched from the store right away (rate
// limited per IP); known products are answered from the database.
func (h *handler) lookup(w http.ResponseWriter, r *http.Request) {
	user := h.optionalUser(r)
	if !h.publicAllowed(w, r, user) {
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	fetchURL, key, err := domain.ListingURL(req.URL)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()

	listing, err := h.repo.ListingByKey(ctx, key)
	if errors.Is(err, repository.ErrNotFound) {
		// A new product costs a store request: rate limit before creating it.
		if err := h.lookupLimiter.Allow(clientIP(r)); err != nil {
			writeError(w, http.StatusTooManyRequests, "too many new lookups from your network; please wait a few minutes")
			return
		}
		h.lookupLimiter.Fail(clientIP(r)) // count this lookup
		listing, _, err = h.repo.FindOrCreateListing(ctx, fetchURL, key)
	}
	if err != nil {
		h.serverError(w, "lookup listing", err)
		return
	}

	// Never checked yet (new, or created by someone moments ago): fetch now so
	// the visitor sees a price instead of an empty page.
	if listing.LastAttemptAt == nil {
		if err := h.checker.Check(ctx, listing.ID); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			h.serverError(w, "check listing", err)
			return
		}
	}
	h.writeListing(w, r, listing.ID, user)
}

// getListing serves a listing's shareable page: /history/{id} in the web app.
func (h *handler) getListing(w http.ResponseWriter, r *http.Request) {
	user := h.optionalUser(r)
	if !h.publicAllowed(w, r, user) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h.writeListing(w, r, id, user)
}

func (h *handler) writeListing(w http.ResponseWriter, r *http.Request, id int64, user *domain.User) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), queryTimeout)
	defer cancel()

	listing, err := h.repo.Listing(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}
	if err != nil {
		h.serverError(w, "get listing", err)
		return
	}
	history, err := h.repo.PublicHistory(ctx, id)
	if err != nil {
		h.serverError(w, "listing history", err)
		return
	}
	resp := lookupResponse{Listing: listing, History: history}
	if user != nil {
		pid, found, err := h.repo.ProductForListing(ctx, user.ID, id)
		if err != nil {
			h.serverError(w, "find product", err)
			return
		}
		if found {
			resp.MyProductID = &pid
		}
	}
	// Viewing keeps an untracked product's history being collected.
	if err := h.repo.TouchListingView(ctx, id); err != nil {
		h.logger.Printf("touch listing %d: %v", id, err)
	}
	writeJSON(w, http.StatusOK, resp)
}

// publicAllowed enforces the admin's "public lookups" switch: when it is off,
// only logged-in users may use the price-history pages.
func (h *handler) publicAllowed(w http.ResponseWriter, r *http.Request, user *domain.User) bool {
	if user != nil {
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	s, err := h.users.Settings(ctx)
	if err != nil {
		h.serverError(w, "load settings", err)
		return false
	}
	if !s.PublicLookups {
		writeError(w, http.StatusUnauthorized, "log in to look up price history")
		return false
	}
	return true
}

// optionalUser returns the logged-in user, or nil for anonymous visitors.
// Unlike requireUser it never rejects the request.
func (h *handler) optionalUser(r *http.Request) *domain.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	u, err := h.users.UserBySession(ctx, auth.HashToken(c.Value))
	if err != nil {
		return nil
	}
	return u
}
