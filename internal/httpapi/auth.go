package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"pricepulse/internal/auth"
	"pricepulse/internal/domain"
	"pricepulse/internal/repository"
)

const (
	sessionCookie = "pp_session"
	// The Google login cookie only lives for the round trip to Google and is
	// scoped to the callback path so it's never sent anywhere else.
	googleCookie     = "pp_google_login"
	googleCookiePath = "/auth/google"
)

// ctxKey is a private type for context keys, so no other package can collide
// with (or read) the user we store in the request context.
type ctxKey struct{}

// currentUser returns the logged-in user set by requireUser.
func currentUser(r *http.Request) *domain.User {
	u, _ := r.Context().Value(ctxKey{}).(*domain.User)
	return u
}

// requireUser wraps a handler so it only runs for logged-in users; everyone
// else gets 401. The user is then available via currentUser(r).
func (h *handler) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "please log in")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		defer cancel()
		u, err := h.users.UserBySession(ctx, auth.HashToken(c.Value))
		if errors.Is(err, repository.ErrNotFound) {
			h.clearCookie(w, sessionCookie, "/")
			writeError(w, http.StatusUnauthorized, "your session has expired, please log in again")
			return
		}
		if err != nil {
			h.serverError(w, "load session", err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	}
}

// authConfig tells the login page which sign-in methods are available.
func (h *handler) authConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"google": h.google != nil})
}

// requireAdmin is requireUser plus a role check: non-admins get 403.
func (h *handler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return h.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if !currentUser(r).IsAdmin() {
			writeError(w, http.StatusForbidden, "admins only")
			return
		}
		next(w, r)
	})
}

// meResponse is the logged-in user plus what they're allowed to do, so the
// UI can show "3 of 20 products" and hide features that are switched off.
type meResponse struct {
	*domain.User
	Limits       domain.Limits `json:"limits"`
	ProductCount int           `json:"product_count"`
}

func (h *handler) meFor(ctx context.Context, u *domain.User) (*meResponse, error) {
	limits, err := h.users.Limits(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	n, err := h.repo.Count(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &meResponse{User: u, Limits: limits, ProductCount: n}, nil
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	resp, err := h.meFor(ctx, currentUser(r))
	if err != nil {
		h.serverError(w, "load account", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeMe answers a successful login/sign-up with the same shape as /auth/me.
func (h *handler) writeMe(w http.ResponseWriter, r *http.Request, status int, u *domain.User) {
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	resp, err := h.meFor(ctx, u)
	if err != nil {
		h.serverError(w, "load account", err)
		return
	}
	writeJSON(w, status, resp)
}

// signupsOpen reports whether new accounts may be created (admin setting).
func (h *handler) signupsOpen(ctx context.Context) (bool, error) {
	s, err := h.users.Settings(ctx)
	return s.SignupsOpen, err
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *handler) register(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decodeJSON(w, r, &req) {
		return
	}
	email, msg := auth.NormalizeEmail(req.Email)
	if msg == "" {
		msg = auth.ValidatePassword(req.Password)
	}
	if msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	if open, err := h.signupsOpen(ctx); err != nil {
		h.serverError(w, "load settings", err)
		return
	} else if !open {
		writeError(w, http.StatusForbidden, "new sign-ups are currently closed")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.serverError(w, "hash password", err)
		return
	}

	u, err := h.users.CreateUser(ctx, email, hash, "")
	if errors.Is(err, repository.ErrEmailTaken) {
		writeError(w, http.StatusConflict, "an account with this email already exists; log in instead")
		return
	}
	if err != nil {
		h.serverError(w, "create user", err)
		return
	}
	if !h.startSession(w, r, u) {
		return
	}
	h.writeMe(w, r, http.StatusCreated, u)
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var req credentials
	if !decodeJSON(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	// Rate limit per IP+email so password guessing is slow, without letting
	// one attacker lock a user out from every network.
	key := clientIP(r) + "|" + email
	if err := h.limiter.Allow(key); err != nil {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts; wait 15 minutes and try again")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	u, hash, disabled, err := h.users.UserByEmail(ctx, email)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		h.serverError(w, "load user", err)
		return
	}
	// The same message (and, via EqualizeTiming, the same response time) for
	// "no such account", "wrong password" and "Google-only account" means the
	// login form can't be used to discover who has an account.
	if u == nil || hash == "" {
		auth.EqualizeTiming(req.Password)
		h.limiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "incorrect email or password")
		return
	}
	if !auth.CheckPassword(hash, req.Password) {
		h.limiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "incorrect email or password")
		return
	}

	h.limiter.Reset(key)
	// Only revealed after a correct password, so it can't be used to probe
	// which accounts exist.
	if disabled {
		writeError(w, http.StatusForbidden, "this account has been suspended; contact the administrator")
		return
	}
	if !h.startSession(w, r, u) {
		return
	}
	h.writeMe(w, r, http.StatusOK, u)
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
		defer cancel()
		// Delete server-side too: clearing the cookie alone would leave a
		// stolen copy of the token usable until it expired.
		if err := h.users.DeleteSession(ctx, auth.HashToken(c.Value)); err != nil {
			h.logger.Printf("logout: %v", err)
		}
	}
	h.clearCookie(w, sessionCookie, "/")
	w.WriteHeader(http.StatusNoContent)
}

// googleStart redirects the browser to Google's sign-in page.
func (h *handler) googleStart(w http.ResponseWriter, r *http.Request) {
	if h.google == nil {
		writeError(w, http.StatusNotFound, "Google sign-in is not configured")
		return
	}
	l, err := auth.NewGoogleLogin()
	if err != nil {
		h.serverError(w, "google login secrets", err)
		return
	}
	l.Next = auth.SafeNext(r.URL.Query().Get("next"))
	http.SetCookie(w, &http.Cookie{
		Name:     googleCookie,
		Value:    l.Encode(),
		Path:     googleCookiePath,
		MaxAge:   600, // 10 minutes to finish signing in at Google
		HttpOnly: true,
		Secure:   h.secureCookies,
		// Lax (not Strict) because the callback is a top-level navigation
		// coming *from* Google, and Strict cookies would not be sent with it.
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, h.google.AuthCodeURL(l), http.StatusFound)
}

// googleCallback is where Google sends the browser back after sign-in. It
// always ends with a redirect to the web app: home on success, or the login
// page with an error code the UI turns into a message.
func (h *handler) googleCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(code string) {
		http.Redirect(w, r, h.appURL+"/login?error="+url.QueryEscape(code), http.StatusFound)
	}
	if h.google == nil {
		fail("google_unavailable")
		return
	}

	// The secrets cookie is single-use: clear it whatever happens next.
	c, err := r.Cookie(googleCookie)
	h.clearCookie(w, googleCookie, googleCookiePath)
	if err != nil {
		fail("google_expired") // took >10 minutes, or cookies are blocked
		return
	}
	l, ok := auth.DecodeGoogleLogin(c.Value)
	q := r.URL.Query()
	// Constant-time compare so the state can't be guessed byte by byte.
	if !ok || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(l.State)) != 1 {
		fail("google_state")
		return
	}
	if q.Get("error") != "" { // e.g. "access_denied" when the user clicks Cancel
		fail("google_cancelled")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	id, err := h.google.Exchange(ctx, q.Get("code"), l)
	if err != nil {
		h.logger.Printf("google sign-in: %v", err)
		fail("google_failed")
		return
	}

	u, err := h.findOrCreateGoogleUser(ctx, id)
	switch {
	case errors.Is(err, errEmailHasAccount):
		fail("google_email_exists")
		return
	case errors.Is(err, repository.ErrDisabled):
		fail("account_disabled")
		return
	case errors.Is(err, errSignupsClosed):
		fail("signups_closed")
		return
	}
	if err != nil {
		h.logger.Printf("google sign-in: %v", err)
		fail("google_failed")
		return
	}
	if !h.startSessionCookieOnly(w, r, u) {
		fail("google_failed")
		return
	}
	next := l.Next
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, h.appURL+next, http.StatusFound)
}

// errEmailHasAccount means a Google sign-in's email already belongs to an
// account that wasn't created through this Google account.
var errEmailHasAccount = errors.New("email already has an account")

// errSignupsClosed means a new Google user arrived while sign-ups are closed.
var errSignupsClosed = errors.New("sign-ups closed")

// findOrCreateGoogleUser maps a Google identity to an account:
//  1. already linked (by Google's stable ID) -> that account
//  2. an existing account with the same email -> refuse (errEmailHasAccount)
//  3. otherwise -> a new account without a password
//
// Why not link in case 2? Password sign-ups never prove they own their email.
// If we linked, an attacker could register *your* email with their own
// password before you ever visit; your later Google sign-in would then land in
// the attacker's account, which they can still open with their password
// ("pre-account takeover"). Safe automatic linking needs email verification
// for password accounts first.
func (h *handler) findOrCreateGoogleUser(ctx context.Context, id *auth.GoogleIdentity) (*domain.User, error) {
	u, err := h.users.UserByGoogleSub(ctx, id.Sub)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	_, _, _, err = h.users.UserByEmail(ctx, id.Email)
	if err == nil {
		return nil, errEmailHasAccount
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if open, err := h.signupsOpen(ctx); err != nil {
		return nil, err
	} else if !open {
		return nil, errSignupsClosed
	}
	u, err = h.users.CreateUser(ctx, id.Email, "", id.Sub)
	if errors.Is(err, repository.ErrEmailTaken) {
		// Someone registered this email between our check and the insert.
		return nil, errEmailHasAccount
	}
	return u, err
}

// startSession creates a session and sets the cookie, writing a 500 on failure.
func (h *handler) startSession(w http.ResponseWriter, r *http.Request, u *domain.User) bool {
	if !h.startSessionCookieOnly(w, r, u) {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return false
	}
	return true
}

// startSessionCookieOnly creates a session and sets the cookie; it logs but
// does not write an error response (the Google flow redirects instead).
func (h *handler) startSessionCookieOnly(w http.ResponseWriter, r *http.Request, u *domain.User) bool {
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		h.logger.Printf("session token: %v", err)
		return false
	}
	expires := time.Now().Add(h.sessionTTL)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), queryTimeout)
	defer cancel()
	if err := h.users.CreateSession(ctx, hash, u.ID, expires); err != nil {
		h.logger.Printf("create session: %v", err)
		return false
	}
	// Opportunistic cleanup; failures here don't matter.
	go h.users.DeleteExpiredSessions(context.Background())

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,                 // JavaScript can't read it, so XSS can't steal it
		Secure:   h.secureCookies,      // HTTPS-only in production
		SameSite: http.SameSiteLaxMode, // not sent on cross-site POSTs (CSRF defense)
	})
	return true
}

func (h *handler) clearCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: path, MaxAge: -1,
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode,
	})
}

// clientIP is the connecting address. If you deploy behind a reverse proxy,
// read X-Forwarded-For from *your* proxy here instead (never trust it blindly).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
