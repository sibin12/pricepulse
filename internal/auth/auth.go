// Package auth holds the security primitives for accounts: password hashing,
// session tokens, and login rate limiting. It has no HTTP or database code.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	MinPasswordLen = 8
	// bcrypt only uses the first 72 bytes of a password; longer ones are
	// rejected rather than silently truncated.
	MaxPasswordBytes = 72
	// bcryptCost 12 takes ~200ms per hash: fast enough for a login, slow
	// enough that brute-forcing a stolen hash is very expensive.
	bcryptCost = 12
)

// HashPassword returns a bcrypt hash (which embeds its own random salt).
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(h), err
}

// CheckPassword reports whether password matches hash, in constant time.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// dummyHash is compared against when an email doesn't exist, so a login for an
// unknown account takes as long as one for a real account. Otherwise the
// response time would reveal which emails are registered.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("pricepulse-timing-equalizer"), bcryptCost)

// EqualizeTiming burns the same time as a real password check.
func EqualizeTiming(password string) {
	bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}

// NormalizeEmail validates an email address and lowercases it, returning an
// error message suitable for the user, or "".
func NormalizeEmail(raw string) (string, string) {
	email := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(email)
	// ParseAddress also accepts "Name <a@b.c>"; require a bare address.
	if err != nil || addr.Address != email || !strings.Contains(email[strings.LastIndex(email, "@")+1:], ".") {
		return "", "enter a valid email address"
	}
	if len(email) > 254 {
		return "", "email is too long"
	}
	return email, ""
}

// ValidatePassword returns an error message suitable for the user, or "".
func ValidatePassword(password string) string {
	switch {
	case len([]rune(password)) < MinPasswordLen:
		return "password must be at least 8 characters"
	case len(password) > MaxPasswordBytes:
		return "password must be at most 72 bytes"
	}
	return ""
}

// NewSessionToken returns a random token for the cookie and the SHA-256 hash
// to store in the database. 32 random bytes = 256 bits: unguessable.
func NewSessionToken() (token string, hash []byte, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashToken(token), nil
}

// HashToken maps a cookie token to the value stored in the sessions table.
// A fast hash is fine here (unlike passwords) because tokens are random and long.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ErrTooManyAttempts is returned by the limiter when a key is locked out.
var ErrTooManyAttempts = errors.New("too many failed attempts")

// Limiter slows down password guessing: after Max failures for a key within
// Window, further attempts are refused until the window passes. Keys are
// "ip|email", so one attacker can't lock out a user from everywhere.
// It is in-memory: fine for one server instance; use Redis for several.
type Limiter struct {
	Max    int
	Window time.Duration

	mu       sync.Mutex
	failures map[string][]time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{Max: max, Window: window, failures: map[string][]time.Time{}}
}

// Allow reports whether another attempt is permitted for key.
func (l *Limiter) Allow(key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.recent(key)) >= l.Max {
		return ErrTooManyAttempts
	}
	return nil
}

// Fail records a failed attempt.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[key] = append(l.recent(key), time.Now())
}

// Reset clears a key after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// recent drops failures older than the window. Caller holds the lock.
func (l *Limiter) recent(key string) []time.Time {
	cutoff := time.Now().Add(-l.Window)
	kept := l.failures[key][:0]
	for _, t := range l.failures[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = kept
	return kept
}
