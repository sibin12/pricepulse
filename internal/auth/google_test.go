package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeIDToken builds an unsigned JWT with the given claims (the signature
// part is ignored by verifyIDToken, see its comment).
func fakeIDToken(claims map[string]any) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "RS256"}) + "." + enc(claims) + ".sig"
}

func TestGoogleFlow(t *testing.T) {
	login, err := NewGoogleLogin()
	if err != nil {
		t.Fatal(err)
	}

	var claims map[string]any
	// Fake token endpoint: checks the PKCE verifier against the challenge the
	// auth URL carried, like Google does, then returns an ID token.
	var challenge string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || r.Form.Get("code") != "good-code" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": fakeIDToken(claims)})
	}))
	defer srv.Close()

	g := NewGoogle(GoogleConfig{ClientID: "client-123", ClientSecret: "s", RedirectURL: "http://localhost/cb", TokenURL: srv.URL})
	u, _ := url.Parse(g.AuthCodeURL(login))
	challenge = u.Query().Get("code_challenge")
	if u.Query().Get("state") != login.State || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("auth URL missing state/PKCE: %s", u)
	}

	valid := func() map[string]any {
		return map[string]any{
			"iss": "https://accounts.google.com", "aud": "client-123", "sub": "g-42",
			"email": "Ana@Example.com", "email_verified": true,
			"exp": time.Now().Add(time.Hour).Unix(), "nonce": login.Nonce,
		}
	}

	claims = valid()
	id, err := g.Exchange(context.Background(), "good-code", login)
	if err != nil || id.Sub != "g-42" || id.Email != "ana@example.com" {
		t.Fatalf("valid flow: %+v, %v", id, err)
	}

	bad := map[string]func(map[string]any){
		"wrong audience":   func(c map[string]any) { c["aud"] = "other-app" },
		"wrong issuer":     func(c map[string]any) { c["iss"] = "https://evil.example" },
		"expired":          func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() },
		"nonce mismatch":   func(c map[string]any) { c["nonce"] = "replayed" },
		"unverified email": func(c map[string]any) { c["email_verified"] = false },
	}
	for name, mutate := range bad {
		claims = valid()
		mutate(claims)
		if _, err := g.Exchange(context.Background(), "good-code", login); err == nil {
			t.Errorf("%s: should be rejected", name)
		}
	}

	// A different PKCE verifier (e.g. an attacker who stole the code) fails.
	claims = valid()
	stolen := login
	stolen.Verifier = "attacker-verifier"
	if _, err := g.Exchange(context.Background(), "good-code", stolen); err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("wrong PKCE verifier should fail with invalid_grant, got %v", err)
	}
}

func TestGoogleLoginCookieRoundTrip(t *testing.T) {
	l, _ := NewGoogleLogin()
	l.Next = "/history/7?x=1"
	got, ok := DecodeGoogleLogin(l.Encode())
	if !ok || got != l {
		t.Fatalf("round trip failed: %+v", got)
	}
	for _, bad := range []string{"", "a.b", "a..c", "a.b.c", "a.b.c.d.e", "a.b.c.!!!"} {
		if _, ok := DecodeGoogleLogin(bad); ok {
			t.Errorf("%q should not decode", bad)
		}
	}
}

func TestSafeNext(t *testing.T) {
	for _, ok := range []string{"/", "/history/7", "/products/3?tab=x"} {
		if SafeNext(ok) != ok {
			t.Errorf("%q should be allowed", ok)
		}
	}
	for _, bad := range []string{"", "history", "https://evil.example", "//evil.example", "/\\evil.example", "/a\nb"} {
		if SafeNext(bad) != "" {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
