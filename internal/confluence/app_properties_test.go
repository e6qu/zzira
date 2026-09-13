package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestForgeAppProperties pins the app-only surfaces: Forge app properties and
// the data policy metadata. Only a request authenticated as the app reaches
// them; a person, even an administrator, is refused.
func TestForgeAppProperties(t *testing.T) {
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
	ws, admin := store.NewID("ws"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'App property test')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Admin')`, admin, admin+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, admin)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, admin, store.HashToken(admin))

	// Two installed apps, installed the way the app runtime installs one: a
	// non-human principal that is a member of the workspace.
	install := func(key string) string {
		t.Helper()
		principal := "app_principal_" + store.NewID("t")
		exec(`INSERT INTO users(id,email,password_hash,display_name,active) VALUES ($1,$2,'!app-principal!',$3,true)`, principal, principal+"@apps.zzira.invalid", key)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'member')`, ws, principal)
		exec(`INSERT INTO app_installations(workspace_id,principal_id,app_key,name,base_url,version,secret_ciphertext,descriptor)
			VALUES ($1,$2,$3,$3,'https://app.example.test','1','\x00'::bytea,'{}'::jsonb)`, ws, principal, key)
		return key
	}
	firstKey, secondKey := install("forge-first-"+strings.ToLower(store.NewID("k"))), install("forge-second-"+strings.ToLower(store.NewID("k")))
	t.Cleanup(func() {
		exec(`DELETE FROM wiki_app_properties WHERE installation_id IN (SELECT id FROM app_installations WHERE workspace_id=$1)`, ws)
		var principals []string
		rows, _ := st.Pool.Query(ctx, `SELECT principal_id FROM app_installations WHERE workspace_id=$1`, ws)
		for rows.Next() {
			var p string
			_ = rows.Scan(&p)
			principals = append(principals, p)
		}
		rows.Close()
		exec(`DELETE FROM app_installations WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		for _, p := range append(principals, admin) {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, p)
			exec(`DELETE FROM users WHERE id=$1`, p)
		}
	})
	installation := func(key string) context.Context {
		t.Helper()
		value, err := st.AppInstallation(ctx, ws, key)
		if err != nil {
			t.Fatal(err)
		}
		return apps.ContextWithInstallation(context.Background(), value)
	}
	firstApp, secondApp := installation(firstKey), installation(secondKey)

	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	// asApp sends a request as an app; asPerson sends it with an API token.
	send := func(appCtx context.Context, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "/wiki/api/v2"+path, strings.NewReader(body))
		if appCtx != nil {
			request = request.WithContext(appCtx)
		} else {
			request.SetBasicAuth(admin+"@example.test", admin)
		}
		response := httptest.NewRecorder()
		http.Handler(h).ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	object := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}

	// A person is refused, even an administrator.
	send(nil, "GET", "/app/properties", "", 401)
	send(nil, "PUT", "/app/properties/theme", `{"dark":true}`, 401)
	send(nil, "GET", "/data-policies/metadata", "", 401)

	// Creating is 201; replacing is 200.
	send(firstApp, "PUT", "/app/properties/user-preferences", `{"theme":"dark","language":"en"}`, 201)
	send(firstApp, "PUT", "/app/properties/user-preferences", `{"theme":"light"}`, 200)
	got := object(send(firstApp, "GET", "/app/properties/user-preferences", "", 200))
	if got["key"] != "user-preferences" || got["value"].(map[string]any)["theme"] != "light" {
		t.Fatalf("read after replace: %v", got)
	}
	// Any JSON value is a property value, not only an object.
	send(firstApp, "PUT", "/app/properties/build-number", `7`, 201)
	send(firstApp, "PUT", "/app/properties/bad", `{not json`, 400)

	// Each app sees only its own properties.
	send(secondApp, "GET", "/app/properties/user-preferences", "", 404)
	send(secondApp, "PUT", "/app/properties/user-preferences", `{"theme":"blue"}`, 201)
	if still := object(send(firstApp, "GET", "/app/properties/user-preferences", "", 200)); still["value"].(map[string]any)["theme"] != "light" {
		t.Fatalf("another app changed this app's property: %v", still)
	}

	// A key is at most 127 characters.
	send(firstApp, "PUT", "/app/properties/"+strings.Repeat("k", 128), `1`, 400)
	send(firstApp, "GET", "/app/properties/"+strings.Repeat("k", 128), "", 400)
	send(firstApp, "PUT", "/app/properties/"+strings.Repeat("k", 127), `1`, 201)

	// Listing pages through the app's keys in order.
	for _, key := range []string{"a1", "a2", "a3"} {
		send(firstApp, "PUT", "/app/properties/"+key, `true`, 201)
	}
	page := object(send(firstApp, "GET", "/app/properties?limit=2", "", 200))
	results := page["results"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["key"] != "a1" || results[1].(map[string]any)["key"] != "a2" {
		t.Fatalf("first page: %v", page)
	}
	next := page["_links"].(map[string]any)["next"].(string)
	seen := 2
	for next != "" {
		body := object(send(firstApp, "GET", strings.TrimPrefix(next, "/wiki/api/v2"), "", 200))
		seen += len(body["results"].([]any))
		next, _ = body["_links"].(map[string]any)["next"].(string)
	}
	// a1, a2, a3, build-number, user-preferences and the 127-character key.
	if seen != 6 {
		t.Fatalf("walking every page saw %d properties", seen)
	}
	send(firstApp, "GET", "/app/properties?limit=251", "", 400)
	send(firstApp, "GET", "/app/properties?cursor=!!!", "", 400)

	// Deleting removes it; deleting again is still done.
	send(firstApp, "DELETE", "/app/properties/build-number", "", 204)
	send(firstApp, "GET", "/app/properties/build-number", "", 404)
	send(firstApp, "DELETE", "/app/properties/build-number", "", 204)

	// Data policies block nothing here, and the app is told so.
	metadata := object(send(firstApp, "GET", "/data-policies/metadata", "", 200))
	if blocked, ok := metadata["anyContentBlocked"].(bool); !ok || blocked {
		t.Fatalf("data policy metadata: %v", metadata)
	}
}
