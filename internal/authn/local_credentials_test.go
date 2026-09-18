package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// TestLocalCredentialsRefused covers an installation published behind single
// sign-on. Seeding it with -mode=demo mints a password and an API token for
// every person in the scenario; with ZZIRA_LOCAL_CREDENTIALS=off none of them
// is a way in, and only the session the identity provider established is.
func TestLocalCredentialsRefused(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	const password = "demo1234"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	userID := store.NewID("usr")
	email := userID + "@example.invalid"
	if _, err := st.CreateUser(ctx, userID, email, hash, "Demo Person"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})
	plain, tokenHash, err := NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAPIToken(ctx, store.NewID("tok"), userID, tokenHash, "demo"); err != nil {
		t.Fatal(err)
	}

	basic := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "https://zzira.example/rest/api/3/myself", nil)
		r.SetBasicAuth(email, plain)
		return r
	}
	bearer := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "https://zzira.example/rest/api/3/myself", nil)
		r.Header.Set("Authorization", "Bearer "+plain)
		return r
	}
	withCookie := func(token string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "https://zzira.example/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		return r
	}

	// What the installation does today, and keeps doing by default.
	passwordSession, err := Login(ctx, st, email, password)
	if err != nil {
		t.Fatalf("password sign-in with local credentials on: %v", err)
	}
	for name, request := range map[string]*http.Request{
		"api token in basic auth": basic(),
		"api token as bearer":     bearer(),
		"password session":        withCookie(passwordSession),
	} {
		if got, err := Identify(ctx, st, request); err != nil || got != userID {
			t.Fatalf("%s with local credentials on: %q, %v", name, got, err)
		}
	}

	// The instance's own setting closes every one of them.
	closed := WithoutLocalCredentials(ctx)
	if _, err := Login(closed, st, email, password); err != ErrUnauthorized {
		t.Fatalf("password sign-in with local credentials off: %v, want ErrUnauthorized", err)
	}
	for name, request := range map[string]*http.Request{
		"api token in basic auth": basic(),
		"api token as bearer":     bearer(),
		// A session minted from a password before the switch is one of the
		// credentials being refused, so it goes with them.
		"password session": withCookie(passwordSession),
	} {
		if got, err := Identify(closed, st, request); err == nil {
			t.Fatalf("%s with local credentials off resolved %q, want refused", name, got)
		}
	}
	if _, err := IdentifyBearer(closed, st, bearer()); err != ErrUnauthorized {
		t.Fatalf("bearer API token with local credentials off: %v, want ErrUnauthorized", err)
	}

	// Single sign-on still works, which is the whole point of closing the
	// rest: the session an identity provider established is accepted.
	ssoSession, err := LoginOIDC(ctx, st, userID, "id-token", "https://issuer.example.invalid", userID+"-subject", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Identify(closed, st, withCookie(ssoSession)); err != nil || got != userID {
		t.Fatalf("single sign-on session with local credentials off: %q, %v", got, err)
	}
}

// The middleware is what puts every request under the setting, so nothing
// reaches a handler without it.
func TestRefuseLocalCredentialsMarksEveryRequest(t *testing.T) {
	var refused bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		refused = LocalCredentialsRefused(r.Context())
	})
	request := httptest.NewRequest(http.MethodGet, "https://zzira.example/login", nil)
	next.ServeHTTP(httptest.NewRecorder(), request)
	if refused {
		t.Fatal("a request outside the middleware was marked as refusing local credentials")
	}
	RefuseLocalCredentials(next).ServeHTTP(httptest.NewRecorder(), request)
	if !refused {
		t.Fatal("a request through the middleware was not marked as refusing local credentials")
	}
}
