package httpapi

import (
	"encoding/json"
	"net/http"
)

// maxBodyBytes caps request bodies at 1 MiB.
const maxBodyBytes = 1 << 20

// decodeJSON reads the request body into dst. On failure it writes a 400 and
// returns false, so handlers can simply `return`.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	// MaxBytesReader stops a huge body from eating memory; reading past the limit errors.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body) // streams from the body instead of loading it all first
	dec.DisallowUnknownFields()    // typos like "targetprice" become a 400 instead of silently ignored
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// writeJSON sends v as JSON with the given status. Header order matters: headers
// must be set before WriteHeader, and the status before the body.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already sent, so an encode error can't change the response;
	// we deliberately ignore it.
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends errors in one consistent shape: {"error": "message"}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
