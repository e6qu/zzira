// Package authn: password auth, signed-content-free session cookies, and API
// tokens for Basic auth. All state lives in Postgres so any replica validates.
package authn

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/e6qu/zzira/internal/store"
)

const (
	sessionCookie = "zzira_session"
	sessionTTL    = 30 * 24 * time.Hour
)

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// UnusablePasswordHash hashes a random value nothing will ever type, for an
// account that only ever authenticates via OIDC (its password field is NOT
// NULL and must never successfully compare).
func UnusablePasswordHash() (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	return HashPassword(token)
}

func hashToken(token string) string { return store.HashToken(token) }

// SessionHash exposes the token hashing for logout handling.
func SessionHash(token string) string { return hashToken(token) }

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func NewAPIToken() (plain string, hash string, err error) {
	first, err := randomToken()
	if err != nil {
		return "", "", err
	}
	second, err := randomToken()
	if err != nil {
		return "", "", err
	}
	plain = "zzira_" + first + second
	return plain, hashToken(plain), nil
}

// SecureCookies reports whether browser cookies must be HTTPS-only. An
// explicit COOKIE_SECURE value wins; otherwise an HTTPS external URL enables
// the safe production default while local HTTP development remains usable.
func SecureCookies() bool {
	if configured := os.Getenv("COOKIE_SECURE"); configured != "" {
		return configured == "true"
	}
	externalURL, err := url.Parse(os.Getenv("ZZIRA_EXTERNAL_URL"))
	return err == nil && strings.EqualFold(externalURL.Scheme, "https") && externalURL.Host != ""
}

// SetSessionCookie issues the opaque session cookie.
func SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is deployment-configured via COOKIE_SECURE

		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   SecureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- clearing cookie carries no data

		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: SecureCookies(),
	})
}

// Login creates a session and returns the cookie token.
func Login(ctx context.Context, st *store.Store, email, password string) (string, error) {
	id, hash, _, err := st.UserByEmail(ctx, email)
	if err != nil {
		return "", ErrUnauthorized
	}
	if !CheckPassword(hash, password) {
		return "", ErrUnauthorized
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := st.CreateSession(ctx, hashToken(token), id, sessionTTL); err != nil {
		return "", err
	}
	return token, nil
}

// LoginOIDC creates a normal opaque session for a verified external identity.
func LoginOIDC(ctx context.Context, st *store.Store, userID, idToken, issuer, subject, sid string) (string, error) {
	if userID == "" || idToken == "" || issuer == "" || subject == "" {
		return "", ErrUnauthorized
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := st.CreateOIDCSession(ctx, hashToken(token), userID, idToken, issuer, subject, sid, sessionTTL); err != nil {
		return "", err
	}
	return token, nil
}

var ErrUnauthorized = unauthorized{}

type unauthorized struct{}

func (unauthorized) Error() string { return "unauthorized" }

// Identify resolves the caller from (1) Basic auth email:api-token, or
// (2) the session cookie. Returns userID or ErrUnauthorized.
func Identify(ctx context.Context, st *store.Store, r *http.Request) (string, error) {
	if user, pass, ok := r.BasicAuth(); ok {
		if i := strings.IndexByte(user, '@'); i > 0 { // Jira-style: email + API token
			userID, err := st.UserByAPIToken(ctx, hashToken(pass))
			if err == nil {
				if id, _, _, e := st.UserByEmail(ctx, user); e == nil && id == userID {
					return userID, nil
				}
			}
		}
		return "", ErrUnauthorized
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", ErrUnauthorized
	}
	return st.SessionUser(ctx, hashToken(c.Value))
}

// ProtectCookieMutations rejects cross-origin unsafe requests authenticated by
// a browser session. API-token clients use Authorization and therefore do not
// depend on ambient browser credentials. Requiring an explicit same-origin
// Origin header avoids accepting a request merely because a session cookie was
// attached to it.
func ProtectCookieMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !unsafeMethod(r.Method) || r.Header.Get("Authorization") != "" {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := r.Cookie(sessionCookie); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		origin, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || origin.Scheme == "" || origin.Host == "" || origin.Host != r.Host || (origin.Scheme != "http" && origin.Scheme != "https") {
			http.Error(w, "cross-origin request blocked", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders installs browser hardening that applies equally to HTML,
// API, worker, and static responses. Inline handlers remain temporarily
// allowed by the renderer, while remote scripts, plugins, framing, and foreign
// form targets are denied. oidcFormActionOrigin, when non-empty, is added to
// form-action alongside 'self': Chrome enforces form-action against a form
// submission's full redirect chain, not just its literal action target, so
// RP-initiated OIDC logout -- the sign-out form posts to this same origin's
// own /logout, which then 303s the browser to the identity provider to end
// the SSO session -- is itself a same-origin form action but every provider
// redirect after it left 'self' and got silently blocked (net::ERR_ABORTED,
// with no further navigation at all) until the provider's origin was
// explicitly allowed here.
func SecurityHeaders(next http.Handler, oidcFormActionOrigin string) http.Handler {
	formAction := "form-action 'self'"
	if oidcFormActionOrigin != "" {
		formAction += " " + oidcFormActionOrigin
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; "+
				"style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; "+
				"worker-src 'self' blob:; object-src 'none'; base-uri 'self'; "+
				"frame-ancestors 'none'; "+formAction)
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func unsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
