package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// TestForgotPassword covers the page anyone can post to: it sends a sign-in
// link to an address that has an account, says the same thing whatever it
// finds, and will not carry a link at all on a site that cannot send email.
func TestForgotPassword(t *testing.T) {
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
	workspaceID, userID := store.NewID("ws"), store.NewID("usr")
	email := userID + "@example.invalid"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Forgotten password test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Forgetful Person')`, userID, email)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, userID)
	t.Cleanup(func() {
		exec(`DELETE FROM email_outbox WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM password_setup_links WHERE user_id=$1`, userID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, userID)
	})
	queued := func() []string {
		t.Helper()
		rows, queryErr := st.Pool.Query(ctx, `SELECT body FROM email_outbox WHERE recipient=$1 ORDER BY id`, email)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		defer rows.Close()
		bodies := []string{}
		for rows.Next() {
			var body string
			if scanErr := rows.Scan(&body); scanErr != nil {
				t.Fatal(scanErr)
			}
			bodies = append(bodies, body)
		}
		return bodies
	}
	ask := func(handler *Handler, address string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"email": {address}}
		request := httptest.NewRequest(http.MethodPost, "https://zzira.example/password/forgot", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.ForgotPasswordSubmit(response, request)
		return response
	}

	// A site with no way to send email says so rather than pretending a link
	// is on its way.
	silent := &Handler{Store: st, WorkspaceSlug: workspaceID}
	response := ask(silent, email)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "cannot send email") {
		t.Fatalf("a site that cannot send email: status=%d body=%s", response.Code, response.Body.String())
	}
	if len(queued()) != 0 {
		t.Fatalf("a message was queued by a site that cannot send one: %v", queued())
	}

	handler := &Handler{Store: st, WorkspaceSlug: workspaceID, InvitationNotificationsConfigured: true, IdentityExternalURL: "https://zzira.example"}

	// An address with no account here gets the same answer as one with.
	unknown := ask(handler, "nobody-"+email)
	known := ask(handler, email)
	if unknown.Body.String() != known.Body.String() {
		t.Fatal("the page told an unknown address apart from a known one")
	}
	messages := queued()
	if len(messages) != 1 || !strings.Contains(messages[0], "https://zzira.example/password/set?token=") {
		t.Fatalf("unexpected queued messages: %v", messages)
	}

	// Asking again straight away sends nothing: the form is open to anyone,
	// and a mailbox is not a place to put a queue.
	if response := ask(handler, email); response.Code != http.StatusOK {
		t.Fatalf("asking again: status=%d", response.Code)
	}
	if again := queued(); len(again) != 1 {
		t.Fatalf("a second link was sent within the cooldown: %v", again)
	}

	// The link that was sent is the one that works.
	link := messages[0][strings.Index(messages[0], "https://"):]
	link = strings.Fields(link)[0]
	token := link[strings.Index(link, "token=")+len("token="):]
	decoded, err := url.QueryUnescape(token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PasswordLinkUser(ctx, store.HashToken(decoded)); err != nil {
		t.Fatalf("the emailed link does not open: %v", err)
	}
}
