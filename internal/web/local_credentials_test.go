package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/store"
)

// The browser's own sign-in form is the other way a password reaches the
// server, so an installation that accepts only its identity provider refuses
// it there too -- with the right password, which is the only case worth
// testing -- and stops offering the box at all.
func TestLocalCredentialsOffRefusesThePasswordForm(t *testing.T) {
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
	hash, err := authn.HashPassword(password)
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
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	h := &Handler{Store: st}
	submit := func(handler http.Handler) *httptest.ResponseRecorder {
		body := strings.NewReader("email=" + email + "&password=" + password)
		request := httptest.NewRequest(http.MethodPost, "/login", body)
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := submit(http.HandlerFunc(h.LoginSubmit)); response.Code != http.StatusSeeOther {
		t.Fatalf("password sign-in with local credentials on: %d %s", response.Code, response.Body.String())
	}
	response := submit(authn.RefuseLocalCredentials(http.HandlerFunc(h.LoginSubmit)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("password sign-in with local credentials off: %d", response.Code)
	}
	if cookie := response.Header().Get("Set-Cookie"); strings.Contains(cookie, "zzira_session=") && !strings.Contains(cookie, "zzira_session=;") {
		t.Fatalf("a refused password sign-in still set a session cookie: %q", cookie)
	}

	// The page stops offering a box whose every answer is a 401. Two
	// providers keep /login rendering rather than redirecting straight
	// through to the only one.
	h.IdentityProviders = &ProviderRegistry{}
	page := func(handler http.Handler) string {
		request := httptest.NewRequest(http.MethodGet, "/login", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET /login: %d", response.Code)
		}
		return response.Body.String()
	}
	if body := page(http.HandlerFunc(h.LoginForm)); !strings.Contains(body, `name="password"`) {
		t.Fatalf("the sign-in page offers no password box with local credentials on: %s", body)
	}
	if body := page(authn.RefuseLocalCredentials(http.HandlerFunc(h.LoginForm))); strings.Contains(body, `name="password"`) {
		t.Fatalf("the sign-in page still offers a password box with local credentials off: %s", body)
	}
	if body := page(authn.RefuseLocalCredentials(http.HandlerFunc(h.SignedOut))); strings.Contains(body, "Use password") {
		t.Fatalf("the signed-out page still offers a password link with local credentials off: %s", body)
	}
}
