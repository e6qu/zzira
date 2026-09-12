package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestSiteSettings pins Confluence's site settings: the look and feel for the
// site and for one space, the themes a site offers, and the system information.
func TestSiteSettings(t *testing.T) {
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
	ws, admin, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Settings test site')`, ws)
	for _, value := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Settings user')`, value.id, value.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_look_and_feel WHERE workspace_id=$1`,
			`DELETE FROM wiki_site_settings WHERE workspace_id=$1`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, id := range []string{admin, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v1 := &V1Handler{Handler: h}
	call := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/wiki/rest/api"+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		var handler http.Handler = v1
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
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
	call(admin, "POST", "/space", map[string]any{"key": "SET", "name": "Settings space"}, 200)

	// The system information describes the site.
	info := object(call(member, "GET", "/settings/systemInfo", nil, 200))
	if info["cloudId"] == "" || info["siteTitle"] != "Settings test site" {
		t.Fatalf("system info: %v", info)
	}
	if info["baseUrl"] != "https://zzira.test/wiki" {
		t.Fatalf("system info base url: %v", info)
	}

	// The theme list leaves out the default, because it is what a site shows
	// when no theme is chosen rather than a theme to choose.
	themes := object(call(member, "GET", "/settings/theme", nil, 200))
	results, _ := themes["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("themes: %v", themes)
	}
	for _, raw := range results {
		theme, _ := raw.(map[string]any)
		if theme["themeKey"] == store.DefaultWikiThemeKey {
			t.Fatalf("the default theme should not be offered as a choice: %v", themes)
		}
	}
	// It can still be read by key, because a space may be showing it.
	if one := object(call(member, "GET", "/settings/theme/"+store.DefaultWikiThemeKey, nil, 200)); one["name"] != "Default theme" {
		t.Fatalf("theme by key: %v", one)
	}
	call(member, "GET", "/settings/theme/nope", nil, 404)
	// No theme is assigned to the whole site, which is reported as none.
	call(member, "GET", "/settings/theme/selected", nil, 404)

	// The look and feel answers the whole structure, for the site and a space.
	site := object(call(member, "GET", "/settings/lookandfeel", nil, 200))
	for _, key := range []string{"selected", "global", "custom", "theme"} {
		if _, ok := site[key]; !ok {
			t.Fatalf("%s missing from the look and feel: %v", key, site)
		}
	}
	if site["selected"] != "global" {
		t.Fatalf("a site starts on its global settings: %v", site)
	}
	space := object(call(member, "GET", "/settings/lookandfeel?spaceKey=SET", nil, 200))
	if space["spaceKey"] != "SET" || space["selected"] != "global" {
		t.Fatalf("space look and feel: %v", space)
	}
	call(member, "GET", "/settings/lookandfeel?spaceKey=NOPE", nil, 404)

	// Writing the custom settings does not select them; that is a separate act.
	written := object(call(admin, "POST", "/settings/lookandfeel/custom",
		map[string]any{"headings": map[string]any{"color": "#FF0000"}}, 200))
	custom, _ := written["custom"].(map[string]any)
	headings, _ := custom["headings"].(map[string]any)
	if headings["color"] != "#FF0000" {
		t.Fatalf("custom settings: %v", written)
	}
	if written["selected"] != "global" {
		t.Fatalf("writing custom settings should not select them: %v", written)
	}
	call(member, "POST", "/settings/lookandfeel/custom",
		map[string]any{"headings": map[string]any{"color": "#FF0000"}}, 403)
	call(admin, "POST", "/settings/lookandfeel/custom", map[string]any{}, 400)

	// A space chooses which settings it shows.
	chosen := object(call(admin, "PUT", "/settings/lookandfeel",
		map[string]any{"spaceKey": "SET", "lookAndFeelType": "custom"}, 200))
	if chosen["lookAndFeelType"] != "custom" || chosen["spaceKey"] != "SET" {
		t.Fatalf("selection: %v", chosen)
	}
	if after := object(call(member, "GET", "/settings/lookandfeel?spaceKey=SET", nil, 200)); after["selected"] != "custom" {
		t.Fatalf("the selection did not stick: %v", after)
	}
	// The site's own look is the global one, so there is nothing to choose.
	call(admin, "PUT", "/settings/lookandfeel", map[string]any{"lookAndFeelType": "custom"}, 400)
	call(admin, "PUT", "/settings/lookandfeel", map[string]any{"spaceKey": "SET", "lookAndFeelType": "sideways"}, 400)
	// A space with no theme has no theme settings to show.
	call(admin, "PUT", "/settings/lookandfeel", map[string]any{"spaceKey": "SET", "lookAndFeelType": "theme"}, 400)
	call(admin, "PUT", "/settings/lookandfeel",
		map[string]any{"spaceKey": "SET", "lookAndFeelType": "theme"}, 400)
	call(member, "PUT", "/settings/lookandfeel", map[string]any{"spaceKey": "SET", "lookAndFeelType": "global"}, 403)

	// Once the space has a theme, it may show the theme's settings.
	call(admin, "PUT", "/space/SET/theme",
		map[string]any{"themeKey": "com.atlassian.confluence.plugins.confluence-dark-theme:dark"}, 200)
	call(admin, "PUT", "/settings/lookandfeel", map[string]any{"spaceKey": "SET", "lookAndFeelType": "theme"}, 200)

	// Resetting returns the custom settings to the defaults, and leaves the
	// selection where it was — which is Confluence's own rule.
	call(member, "DELETE", "/settings/lookandfeel/custom", nil, 403)
	call(admin, "DELETE", "/settings/lookandfeel/custom", nil, 204)
	reset := object(call(admin, "GET", "/settings/lookandfeel", nil, 200))
	resetCustom, _ := reset["custom"].(map[string]any)
	resetHeadings, _ := resetCustom["headings"].(map[string]any)
	if resetHeadings["color"] == "#FF0000" {
		t.Fatalf("the reset did not return the custom settings to the defaults: %v", reset)
	}
	if selection := object(call(admin, "GET", "/settings/lookandfeel?spaceKey=SET", nil, 200)); selection["selected"] != "theme" {
		t.Fatalf("resetting the site's custom settings changed a space's selection: %v", selection)
	}
}
