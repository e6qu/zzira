// Package authn: password auth, signed-content-free session cookies, and API
// tokens for Basic auth. All state lives in Postgres so any replica validates.
package authn

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
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

// SetSessionCookieFor issues the opaque session cookie for as long as the
// session it carries lasts, which an authentication policy can make shorter
// than the site's own duration.
func SetSessionCookieFor(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is deployment-configured via COOKIE_SECURE

		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   SecureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- clearing cookie carries no data

		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: SecureCookies(),
	})
}

// ErrSSORequired is an authentication policy that admits this person only
// through the identity provider. It is told apart from a wrong password
// because the person can do something about it.
var ErrSSORequired = errors.New("single sign-on is required for this account")

// Login creates a session and returns the cookie token, with how long the
// session lasts: the site's own duration, or the shorter one an
// authentication policy gives this person.
func Login(ctx context.Context, st *store.Store, email, password string) (string, time.Duration, error) {
	if LocalCredentialsRefused(ctx) {
		return "", 0, ErrUnauthorized
	}
	id, hash, _, err := st.UserByEmail(ctx, email)
	if err != nil {
		return "", 0, ErrUnauthorized
	}
	if !CheckPassword(hash, password) {
		return "", 0, ErrUnauthorized
	}
	policy, err := st.AuthenticationPolicyForUser(ctx, id)
	if err != nil {
		return "", 0, err
	}
	if policy.Enforced && policy.EnforceSSO {
		return "", 0, ErrSSORequired
	}
	ttl := policy.SessionDuration(sessionTTL)
	token, err := randomToken()
	if err != nil {
		return "", 0, err
	}
	if err := st.CreateSession(ctx, hashToken(token), id, ttl); err != nil {
		return "", 0, err
	}
	return token, ttl, nil
}

// LoginOIDC creates a normal opaque session for a verified external identity,
// which an authentication policy can make shorter.
func LoginOIDC(ctx context.Context, st *store.Store, userID, idToken, issuer, subject, sid string) (string, time.Duration, error) {
	if userID == "" || idToken == "" || issuer == "" || subject == "" {
		return "", 0, ErrUnauthorized
	}
	policy, err := st.AuthenticationPolicyForUser(ctx, userID)
	if err != nil {
		return "", 0, err
	}
	ttl := policy.SessionDuration(sessionTTL)
	token, err := randomToken()
	if err != nil {
		return "", 0, err
	}
	if err := st.CreateOIDCSession(ctx, hashToken(token), userID, idToken, issuer, subject, sid, ttl); err != nil {
		return "", 0, err
	}
	return token, ttl, nil
}

// LoginIdentityProvider creates a session for either OIDC or OAuth identity
// providers. OAuth-only providers do not issue an ID token, so idToken may be
// empty; issuer and subject remain the immutable identity key.
func LoginIdentityProvider(ctx context.Context, st *store.Store, userID, idToken, issuer, subject, sid, providerKey string) (string, time.Duration, error) {
	if userID == "" || issuer == "" || subject == "" || providerKey == "" {
		return "", 0, ErrUnauthorized
	}
	policy, err := st.AuthenticationPolicyForUser(ctx, userID)
	if err != nil {
		return "", 0, err
	}
	ttl := policy.SessionDuration(sessionTTL)
	token, err := randomToken()
	if err != nil {
		return "", 0, err
	}
	if err := st.CreateIdentityProviderSession(ctx, hashToken(token), userID, idToken, issuer, subject, sid, providerKey, ttl); err != nil {
		return "", 0, err
	}
	return token, ttl, nil
}

// ErrPasswordRefused is a new password the rules refuse. It carries what the
// rule is, because the person is about to type another one.
type ErrPasswordRefused struct{ Reason string }

func (err ErrPasswordRefused) Error() string { return err.Reason }

// ChangePassword replaces the signed-in person's own password. They prove
// they know the current one, the new one meets the rules their authentication
// policy applies, and every other session of theirs ends -- a session opened
// with the old password does not outlive it.
func ChangePassword(ctx context.Context, st *store.Store, userID, current, next, sessionToken string) error {
	if LocalCredentialsRefused(ctx) {
		return ErrUnauthorized
	}
	hash, err := st.UserPasswordHash(ctx, userID)
	if err != nil {
		return ErrUnauthorized
	}
	if !CheckPassword(hash, current) {
		return ErrUnauthorized
	}
	policy, err := st.AuthenticationPolicyForUser(ctx, userID)
	if err != nil {
		return err
	}
	if policy.Enforced && policy.EnforceSSO {
		return ErrSSORequired
	}
	if reason := policy.PasswordRefused(next); reason != "" {
		return ErrPasswordRefused{Reason: reason}
	}
	if CheckPassword(hash, next) {
		return ErrPasswordRefused{Reason: "That is the password already in use."}
	}
	replacement, err := HashPassword(next)
	if err != nil {
		return err
	}
	return st.SetUserPassword(ctx, userID, replacement, hashToken(sessionToken))
}

// NewPasswordLink mints a sign-in link's secret and the hash kept for it. The
// secret is shown once, to whoever will pass it on; only its hash is stored.
func NewPasswordLink(ctx context.Context, st *store.Store, userID string) (string, time.Time, error) {
	token, err := randomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires, err := st.CreatePasswordLink(ctx, userID, hashToken(token))
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// PasswordLinkPolicy is whom a sign-in link is for and the rules their
// authentication policy applies, which the page asks for before showing a
// form nobody could fill in.
func PasswordLinkPolicy(ctx context.Context, st *store.Store, token string) (string, store.AuthenticationPolicy, error) {
	userID, err := st.PasswordLinkUser(ctx, hashToken(token))
	if err != nil {
		return "", store.AuthenticationPolicy{}, err
	}
	policy, err := st.AuthenticationPolicyForUser(ctx, userID)
	if err != nil {
		return "", store.AuthenticationPolicy{}, err
	}
	if policy.Enforced && policy.EnforceSSO {
		return "", store.AuthenticationPolicy{}, ErrSSORequired
	}
	return userID, policy, nil
}

// SetPasswordWithLink spends a sign-in link on a new password. Every session
// that account had ends with it.
func SetPasswordWithLink(ctx context.Context, st *store.Store, token, next string) error {
	_, policy, err := PasswordLinkPolicy(ctx, st, token)
	if err != nil {
		return err
	}
	if reason := policy.PasswordRefused(next); reason != "" {
		return ErrPasswordRefused{Reason: reason}
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	_, err = st.UsePasswordLink(ctx, hashToken(token), hash)
	return err
}

var ErrUnauthorized = unauthorized{}

type principalContextKey struct{}

// WithPrincipal attaches an already-authenticated non-human account to a
// request. The app runtime verifies its signed request before using this hook.
func WithPrincipal(ctx context.Context, principalID string) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principalID)
}

type anonymousContextKey struct{}

// WithAnonymous marks a request that presents no credentials on an operation
// Jira lets anonymous callers use. Identify resolves such a request to the
// anonymous user, whose access comes only from permissions granted to anyone.
func WithAnonymous(ctx context.Context) context.Context {
	return context.WithValue(ctx, anonymousContextKey{}, true)
}

// Anonymous reports whether the request is Jira's anonymous user.
func Anonymous(ctx context.Context) bool {
	anonymous, _ := ctx.Value(anonymousContextKey{}).(bool)
	return anonymous
}

type localCredentialsContextKey struct{}

// RefuseLocalCredentials closes an installation to every credential it issued
// itself -- a password and an API token -- so the only way in is a session an
// identity provider established.
//
// It is the instance's own configuration, not a site setting the REST API
// exposes: Jira's public surface is unchanged either way, and a caller
// refused here gets the same 401 it gets for a wrong password. An
// installation published on the internet behind single sign-on turns it on,
// because seeding a demo mints a password and an API token for every person
// in the scenario, and those would otherwise be working non-SSO logins past
// the provider. Leaving it off keeps password sign-in and API tokens, which
// is what a local or private installation wants.
//
// It is one middleware over the whole server rather than a field on a
// handler because Identify is reached from api3, agile, confluence, admin,
// automation, syncapi and web, and the browser's own password form calls
// Login directly; a field on any one of those would leave the others open.
func RefuseLocalCredentials(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithoutLocalCredentials(r.Context())))
	})
}

// WithoutLocalCredentials marks a request on an installation that accepts
// only its identity provider's sessions.
func WithoutLocalCredentials(ctx context.Context) context.Context {
	return context.WithValue(ctx, localCredentialsContextKey{}, true)
}

// LocalCredentialsRefused reports whether this installation refuses the
// credentials it issued itself. Sign-in pages read it so they do not offer a
// password box that can only ever be answered with a 401.
func LocalCredentialsRefused(ctx context.Context) bool {
	refused, _ := ctx.Value(localCredentialsContextKey{}).(bool)
	return refused
}

// PresentsCredentials reports whether a request carries any credential: an
// authenticated app principal, an Authorization header or a session cookie.
// A request presenting a credential that fails to verify is unauthorized,
// never anonymous.
func PresentsCredentials(r *http.Request) bool {
	if principalID, _ := r.Context().Value(principalContextKey{}).(string); principalID != "" {
		return true
	}
	if r.Header.Get("Authorization") != "" {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	return err == nil && c.Value != ""
}

type unauthorized struct{}

func (unauthorized) Error() string { return "unauthorized" }

// Identify resolves the caller from (1) Basic auth email:api-token, or
// (2) the session cookie. Returns userID or ErrUnauthorized. An anonymous
// request resolves to the empty user ID.
func Identify(ctx context.Context, st *store.Store, r *http.Request) (string, error) {
	if Anonymous(ctx) {
		return "", nil
	}
	if principalID, _ := ctx.Value(principalContextKey{}).(string); principalID != "" {
		return principalID, nil
	}
	if user, pass, ok := r.BasicAuth(); ok {
		if i := strings.IndexByte(user, '@'); i > 0 && !LocalCredentialsRefused(ctx) { // Jira-style: email + API token
			userID, err := st.UserByAPIToken(ctx, hashToken(pass))
			if err == nil {
				if id, _, _, e := st.UserByEmail(ctx, user); e == nil && id == userID {
					return userID, nil
				}
			}
		}
		return "", ErrUnauthorized
	}
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer ") {
		return IdentifyBearer(ctx, st, r)
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", ErrUnauthorized
	}
	if LocalCredentialsRefused(ctx) {
		// A session minted from a password before the installation closed to
		// local credentials is one of those credentials too, so only a
		// session an identity provider established is still a way in.
		return st.IdentityProviderSessionUser(ctx, hashToken(c.Value))
	}
	return st.SessionUser(ctx, hashToken(c.Value))
}

// IdentifyBearer resolves an API token presented with the bearer scheme. Jira
// site APIs commonly use email/token Basic authentication, while Atlassian's
// organization administration API presents an admin API key as a bearer token.
func IdentifyBearer(ctx context.Context, st *store.Store, r *http.Request) (string, error) {
	if LocalCredentialsRefused(ctx) {
		return "", ErrUnauthorized
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", ErrUnauthorized
	}
	userID, err := st.UserByAPIToken(ctx, hashToken(parts[1]))
	if err != nil {
		return "", ErrUnauthorized
	}
	return userID, nil
}

// ProtectCookieMutations rejects cross-origin unsafe requests authenticated by
// a browser session. API-token clients use Authorization and therefore do not
// depend on ambient browser credentials.
//
// Where the browser says what kind of request this is, that answer decides:
// Sec-Fetch-Site is set by the browser, cannot be written by a page, and is
// the only thing a form on the sign-in page carries. That page asks for no
// referrer, so Chrome serializes its Origin as "null" -- and a browser still
// holding the cookie of a session that has ended (a password changed on
// another device, an account suspended and restored) could then not sign in
// again at all. Without Fetch Metadata, the Origin must match this host.
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
		switch r.Header.Get("Sec-Fetch-Site") {
		case "same-origin", "none":
			// Same document origin, or no initiator at all: a typed address
			// or a bookmark, neither of which another site can arrange.
			next.ServeHTTP(w, r)
			return
		case "same-site", "cross-site":
			// A sibling host is another origin here: this site is served
			// from one.
			http.Error(w, "cross-origin request blocked", http.StatusForbidden)
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
	return SecurityHeadersDynamic(next, func() string { return oidcFormActionOrigin })
}

// SecurityHeadersDynamic refreshes allowed logout origins for providers added
// or rotated at runtime. The registry returns origins only from validated OIDC
// discovery documents.
func SecurityHeadersDynamic(next http.Handler, oidcFormActionOrigins func() string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		formAction := "form-action 'self'"
		if origins := oidcFormActionOrigins(); origins != "" {
			formAction += " " + origins
		}
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; "+
				"style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self'; frame-src 'self' https:; "+
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
