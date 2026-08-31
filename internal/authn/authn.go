// Package authn: password auth, signed-content-free session cookies, and API
// tokens for Basic auth. All state lives in Postgres so any replica validates.
package authn

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
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

// SetSessionCookie issues the session cookie. Secure is enabled when
// COOKIE_SECURE=true (TLS termination in front); local plain-HTTP dev sets
// it explicitly to false.
func SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is deployment-configured via COOKIE_SECURE

		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   os.Getenv("COOKIE_SECURE") == "true", // #nosec G124 -- deployment-configured; TLS-terminated deployments set COOKIE_SECURE=true
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- clearing cookie carries no data

		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: os.Getenv("COOKIE_SECURE") == "true", // #nosec G124 -- deployment-configured
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
func LoginOIDC(ctx context.Context, st *store.Store, userID, idToken string) (string, error) {
	if userID == "" || idToken == "" {
		return "", ErrUnauthorized
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := st.CreateOIDCSession(ctx, hashToken(token), userID, idToken, sessionTTL); err != nil {
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
