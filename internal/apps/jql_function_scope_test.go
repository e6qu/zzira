package apps

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJQLFunctionPrecomputationUsesConnectReadScope(t *testing.T) {
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/rest/api/3/jql/function/computation"},
		{http.MethodPost, "/rest/api/3/jql/function/computation"},
		{http.MethodPost, "/rest/api/3/jql/function/computation/search"},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		scope, supported := appAPIScope(request)
		if !supported || scope != "read:jira-work" {
			t.Fatalf("%s %s scope = %q, %v", test.method, test.path, scope, supported)
		}
	}
}
