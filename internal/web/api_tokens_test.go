package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// apiTokenFixture is a workspace with two members, because a token belongs to
// the person who made it and to nobody else.
type apiTokenFixture struct {
	store   *store.Store
	handler *Handler
	ownerID string
	otherID string
	tokens  map[string]string
}

func newAPITokenFixture(t *testing.T) *apiTokenFixture {
	t.Helper()
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
	workspaceID, ownerID, otherID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'API token test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Token owner')`, ownerID, ownerID+"@example.invalid")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Someone else')`, otherID, otherID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, ownerID)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, otherID)
	t.Cleanup(func() {
		exec(`DELETE FROM api_tokens WHERE user_id IN ($1,$2)`, ownerID, otherID)
		exec(`DELETE FROM sessions WHERE user_id IN ($1,$2)`, ownerID, otherID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id IN ($1,$2)`, ownerID, otherID)
	})
	fixture := &apiTokenFixture{
		store:   st,
		handler: &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID},
		ownerID: ownerID,
		otherID: otherID,
		tokens:  map[string]string{},
	}
	for _, id := range []string{ownerID, otherID} {
		session, err := authn.LoginOIDC(ctx, st, id, "id-token", "https://issuer.example.invalid", id+"-subject", "")
		if err != nil {
			t.Fatal(err)
		}
		fixture.tokens[id] = session
	}
	return fixture
}

func (f *apiTokenFixture) post(t *testing.T, handler func(http.ResponseWriter, *http.Request), userID, path, tokenID string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[userID]})
	if tokenID != "" {
		request.SetPathValue("token", tokenID)
	}
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}

// A person creates a token for themselves, sees the secret once, signs in
// with it, and revokes it.
func TestAPITokenIsCreatedShownOnceAndRevoked(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx := context.Background()
	response := f.post(t, f.handler.CreateAPIToken, f.ownerID, "/profile/tokens", "",
		url.Values{"label": {"Release script"}, "expiresOn": {time.Now().AddDate(0, 1, 0).Format("2006-01-02")}})
	if response.Code != http.StatusOK {
		t.Fatalf("create = %d, want 200: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	secret := ""
	if _, rest, found := strings.Cut(body, `data-new-api-token>`); found {
		secret, _, _ = strings.Cut(rest, "<")
	}
	if !strings.HasPrefix(secret, "zzira_") {
		t.Fatalf("the page did not carry the new token: %q", secret)
	}

	// The secret signs in as its owner, which is the whole point of it.
	userID, err := f.store.UserByAPIToken(ctx, store.HashToken(secret))
	if err != nil || userID != f.ownerID {
		t.Fatalf("the token authenticates as %q (%v), want the owner", userID, err)
	}

	tokens, err := f.store.APITokensForUser(ctx, f.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || tokens[0].Label != "Release script" || tokens[0].CreatedAt == "" || tokens[0].ExpiresAt == "" {
		t.Fatalf("listed tokens = %+v", tokens)
	}

	// The list never carries the secret again: only its hash was kept.
	page := f.post(t, f.handler.CreateAPIToken, f.ownerID, "/profile/tokens", "", url.Values{"label": {"Second"}})
	if strings.Contains(page.Body.String(), secret) {
		t.Fatal("the profile page showed a secret created earlier")
	}

	// A reload replays the request that made the token. It makes nothing, and
	// the page says so rather than showing a secret it does not have.
	requestID := store.NewID("tkreq")
	first := f.post(t, f.handler.CreateAPIToken, f.ownerID, "/profile/tokens", "",
		url.Values{"label": {"Reloaded"}, "requestId": {requestID}})
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "data-new-api-token") {
		t.Fatalf("the first arrival = %d without a secret", first.Code)
	}
	replay := f.post(t, f.handler.CreateAPIToken, f.ownerID, "/profile/tokens", "",
		url.Values{"label": {"Reloaded"}, "requestId": {requestID}})
	if replay.Code != http.StatusOK {
		t.Fatalf("the replayed arrival = %d, want 200", replay.Code)
	}
	if strings.Contains(replay.Body.String(), "data-new-api-token") {
		t.Fatal("the replayed arrival showed a secret")
	}
	if !strings.Contains(replay.Body.String(), "already created") {
		t.Fatal("the replayed arrival did not say the token was already made")
	}
	reloaded := 0
	listed, err := f.store.APITokensForUser(ctx, f.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range listed {
		if token.Label == "Reloaded" {
			reloaded++
		}
	}
	if reloaded != 1 {
		t.Fatalf("a reloaded creation left %d tokens, want 1", reloaded)
	}

	// Somebody else's revocation does nothing.
	denied := f.post(t, f.handler.RevokeAPIToken, f.otherID, "/profile/tokens/"+tokens[0].ID+"/revoke", tokens[0].ID, url.Values{})
	if denied.Code != http.StatusBadRequest {
		t.Fatalf("revoking another person's token = %d, want 400", denied.Code)
	}
	if _, err := f.store.UserByAPIToken(ctx, store.HashToken(secret)); err != nil {
		t.Fatal("a refused revocation removed the token anyway")
	}

	revoked := f.post(t, f.handler.RevokeAPIToken, f.ownerID, "/profile/tokens/"+tokens[0].ID+"/revoke", tokens[0].ID, url.Values{})
	if revoked.Code != http.StatusSeeOther {
		t.Fatalf("revoke = %d, want 303: %s", revoked.Code, revoked.Body.String())
	}
	if _, err := f.store.UserByAPIToken(ctx, store.HashToken(secret)); err == nil {
		t.Fatal("the revoked token still signs in")
	}
}

// What a token cannot be: unlabelled, immortal, expired on arrival, or one of
// an unbounded number.
func TestAPITokenRefusals(t *testing.T) {
	f := newAPITokenFixture(t)
	for _, check := range []struct {
		name string
		form url.Values
	}{
		{"no label", url.Values{"label": {"  "}}},
		// A date is the end of that day in UTC, so a day that is past
		// wherever the clock is set takes two days, not one.
		{"expiry in the past", url.Values{"label": {"Old"}, "expiresOn": {time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")}}},
		{"expiry beyond a year", url.Values{"label": {"Forever"}, "expiresOn": {time.Now().UTC().AddDate(1, 0, 2).Format("2006-01-02")}}},
		// The day exactly a year out is the date the form offers, so it has
		// to be accepted; only a later one is refused.
		{"expiry that is not a date", url.Values{"label": {"Whenever"}, "expiresOn": {"next tuesday"}}},
	} {
		t.Run(check.name, func(t *testing.T) {
			response := f.post(t, f.handler.CreateAPIToken, f.ownerID, "/profile/tokens", "", check.form)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s = %d, want 400", check.name, response.Code)
			}
			if !strings.Contains(response.Body.String(), "That token was not created") {
				t.Fatalf("%s did not say what was wrong", check.name)
			}
		})
	}
	tokens, err := f.store.APITokensForUser(context.Background(), f.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("a refused request still made %d token(s)", len(tokens))
	}
	accepted := f.post(t, f.handler.CreateAPIToken, f.ownerID, "/profile/tokens", "",
		url.Values{"label": {"A year out"}, "expiresOn": {time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")}})
	if accepted.Code != http.StatusOK {
		t.Fatalf("the date the form offers = %d, want 200: %s", accepted.Code, accepted.Body.String())
	}
}
