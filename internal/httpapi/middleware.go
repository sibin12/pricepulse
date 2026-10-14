package httpapi

import (
	"log"
	"net/http"
	"time"
)

// A middleware is a function that takes a handler and returns a new handler
// that does extra work before/after calling the original one.

// CORS adds the headers that let a browser app on another origin (the React dev
// server on :3000) call this API. Browsers enforce this; curl and servers do not.
func CORS(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		// Lets the browser send/receive the session cookie on cross-origin fetches.
		// Only safe because Allow-Origin is one specific origin, never "*".
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Vary", "Origin") // tells caches the response depends on the Origin header
		// Before a JSON POST/PUT/PATCH/DELETE, browsers send an OPTIONS "preflight" asking
		// permission. We answer it here with 204 so it never reaches the mux (which would 405).
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder wraps http.ResponseWriter to remember the status code, because
// the standard ResponseWriter gives no way to read it back for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader intercepts the status, stores it, then forwards to the real writer.
func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Logging prints one line per request: method, path, status and duration.
func Logging(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// Default 200 because handlers that never call WriteHeader implicitly send 200.
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Printf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Microsecond))
	})
}

// CheckOrigin rejects state-changing requests (POST/PATCH/DELETE) that a
// browser sent from a different site. Together with SameSite=Lax cookies this
// blocks CSRF: another website can't make your logged-in browser delete
// products or change settings. Requests without an Origin header (curl,
// server-to-server) carry no browser cookies by accident, so they pass.
func CheckOrigin(allowed string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if o := r.Header.Get("Origin"); o != "" && o != allowed {
				writeError(w, http.StatusForbidden, "cross-site request blocked")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
