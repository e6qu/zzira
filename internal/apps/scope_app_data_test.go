package apps

import (
	"net/http/httptest"
	"testing"
)

// TestForgeAppPropertyScopes checks app properties are classified under
// Forge's app-data scopes rather than the content scopes, so an app granted
// only content access cannot read or write another surface's data by accident.
func TestForgeAppPropertyScopes(t *testing.T) {
	for _, c := range []struct{ method, path, want string }{
		{"GET", "/wiki/api/v2/app/properties", "read:app-data:confluence"},
		{"GET", "/wiki/api/v2/app/properties/theme", "read:app-data:confluence"},
		{"PUT", "/wiki/api/v2/app/properties/theme", "write:app-data:confluence"},
		{"DELETE", "/wiki/api/v2/app/properties/theme", "write:app-data:confluence"},
		{"GET", "/wiki/api/v2/pages", "read:confluence-content"},
	} {
		scope, ok := appAPIScope(httptest.NewRequest(c.method, c.path, nil))
		if !ok || scope != c.want {
			t.Fatalf("%s %s: got %q (%v) want %q", c.method, c.path, scope, ok, c.want)
		}
	}
	if !allowedScopes["read:app-data:confluence"] || !allowedScopes["write:app-data:confluence"] {
		t.Fatal("an app descriptor must be able to request the app-data scopes")
	}
}
