package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GoogleConfig holds OAuth client settings from the Google Cloud console.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string // must exactly match an "Authorized redirect URI" in the console

	// Endpoints; empty means Google's real ones. Overridable for tests.
	AuthURL  string
	TokenURL string
}

// Google implements "Sign in with Google" using the OpenID Connect
// authorization code flow with PKCE, using only the standard library.
type Google struct {
	cfg    GoogleConfig
	client *http.Client
}

// GoogleIdentity is what we learn about the user from Google.
type GoogleIdentity struct {
	Sub   string // stable, unique Google account ID
	Email string // lowercased; only returned when Google has verified it
}

func NewGoogle(cfg GoogleConfig) *Google {
	if cfg.AuthURL == "" {
		cfg.AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = "https://oauth2.googleapis.com/token"
	}
	return &Google{cfg: cfg, client: &http.Client{Timeout: 15 * time.Second}}
}

// GoogleLogin holds the per-attempt secrets. They are stored in a short-lived
// cookie between the redirect to Google and the callback.
type GoogleLogin struct {
	State    string // returned by Google unchanged; must match (stops CSRF/login forgery)
	Nonce    string // embedded in the ID token; must match (stops token replay)
	Verifier string // PKCE secret; proves the code is redeemed by whoever started the login
	Next     string // app path to return to after signing in ("" = home)
}

// NewGoogleLogin creates fresh random secrets for one sign-in attempt.
func NewGoogleLogin() (GoogleLogin, error) {
	var l GoogleLogin
	for _, dst := range []*string{&l.State, &l.Nonce, &l.Verifier} {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return l, err
		}
		*dst = base64.RawURLEncoding.EncodeToString(buf)
	}
	return l, nil
}

// Encode/DecodeGoogleLogin serialize the login for the cookie. Every part is
// base64url, which never contains ".", so "." is a safe separator.
func (l GoogleLogin) Encode() string {
	return l.State + "." + l.Nonce + "." + l.Verifier + "." + base64.RawURLEncoding.EncodeToString([]byte(l.Next))
}

func DecodeGoogleLogin(s string) (GoogleLogin, bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return GoogleLogin{}, false
	}
	next, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return GoogleLogin{}, false
	}
	return GoogleLogin{State: parts[0], Nonce: parts[1], Verifier: parts[2], Next: SafeNext(string(next))}, true
}

// SafeNext returns path if it is a same-site app path like "/history/7", and
// "" otherwise. Anything else ("https://evil.example", "//evil.example",
// "/\\evil.example") would turn the login into an open redirect that sends
// freshly signed-in users to an attacker's site.
func SafeNext(path string) string {
	if len(path) > 512 || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\\r\n\t") {
		return ""
	}
	return path
}

// AuthCodeURL is where the browser is sent to sign in at Google.
func (g *Google) AuthCodeURL(l GoogleLogin) string {
	challenge := sha256.Sum256([]byte(l.Verifier))
	q := url.Values{
		"client_id":             {g.cfg.ClientID},
		"redirect_uri":          {g.cfg.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {"openid email"}, // only what we need: an ID and an email
		"state":                 {l.State},
		"nonce":                 {l.Nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"}, // let users pick which Google account
	}
	return g.cfg.AuthURL + "?" + q.Encode()
}

// Exchange redeems the authorization code for an ID token and validates it.
func (g *Google) Exchange(ctx context.Context, code string, l GoogleLogin) (*GoogleIdentity, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {g.cfg.ClientID},
		"client_secret": {g.cfg.ClientSecret},
		"redirect_uri":  {g.cfg.RedirectURL},
		"grant_type":    {"authorization_code"},
		"code_verifier": {l.Verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || body.IDToken == "" {
		return nil, fmt.Errorf("token endpoint: %s %s (HTTP %d)", body.Error, body.Description, resp.StatusCode)
	}
	return g.verifyIDToken(body.IDToken, l.Nonce)
}

// idClaims are the ID token fields we check.
type idClaims struct {
	Iss           string `json:"iss"`
	Aud           string `json:"aud"`
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Exp           int64  `json:"exp"`
	Nonce         string `json:"nonce"`
}

// verifyIDToken checks the ID token's claims.
//
// We don't verify the token's signature: the token came straight from Google's
// token endpoint over HTTPS in response to our own authenticated request, so
// TLS already guarantees it is genuine. The OpenID Connect spec (Core §3.1.3.7)
// explicitly allows this. If tokens were ever accepted from the browser
// instead, signature checking against Google's public keys would be required.
func (g *Google) verifyIDToken(token, nonce string) (*GoogleIdentity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed ID token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("malformed ID token payload")
	}
	var c idClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, errors.New("malformed ID token claims")
	}

	switch {
	case c.Iss != "https://accounts.google.com" && c.Iss != "accounts.google.com":
		return nil, fmt.Errorf("ID token from unexpected issuer %q", c.Iss)
	case c.Aud != g.cfg.ClientID:
		return nil, errors.New("ID token was issued for a different app")
	case time.Now().Unix() > c.Exp:
		return nil, errors.New("ID token expired")
	case c.Nonce == "" || c.Nonce != nonce:
		return nil, errors.New("ID token nonce mismatch")
	case c.Sub == "":
		return nil, errors.New("ID token has no subject")
	case c.Email == "" || !c.EmailVerified:
		// An unverified email could belong to someone else, and we use the email
		// to link accounts, so it must be verified.
		return nil, errors.New("Google account email is not verified")
	}
	return &GoogleIdentity{Sub: c.Sub, Email: strings.ToLower(c.Email)}, nil
}
