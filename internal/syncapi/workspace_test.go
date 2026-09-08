package syncapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSelectedWorkspaceSlug(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		target     string
		want       string
		allowed    bool
	}{
		{name: "configured workspace", configured: "acme", target: "/sync", want: "acme", allowed: true},
		{name: "matching request", configured: "acme", target: "/sync?workspace=acme", want: "acme", allowed: true},
		{name: "other workspace rejected", configured: "acme", target: "/sync?workspace=other", want: "acme", allowed: false},
		{name: "load test explicit workspace", target: "/sync?workspace=load100", want: "load100", allowed: true},
		{name: "legacy default", target: "/sync", want: "zzira", allowed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, allowed := selectedWorkspaceSlug(tt.configured, httptest.NewRequest("GET", tt.target, nil))
			if got != tt.want || allowed != tt.allowed {
				t.Fatalf("selectedWorkspaceSlug()=(%q,%v), want (%q,%v)", got, allowed, tt.want, tt.allowed)
			}
		})
	}
}

func TestUnauthorizedReplicaResponseDoesNotChallengeBrowser(t *testing.T) {
	h := &Handler{}

	apiReq := httptest.NewRequest(http.MethodGet, "/sync", nil)
	apiRes := httptest.NewRecorder()
	h.ServeHTTP(apiRes, apiReq)
	if apiRes.Code != http.StatusUnauthorized || apiRes.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("API unauthorized response = %d, challenge %q", apiRes.Code, apiRes.Header().Get("WWW-Authenticate"))
	}

	replicaReq := httptest.NewRequest(http.MethodGet, "/sync", nil)
	replicaReq.Header.Set("X-Zzira-Replica", "browser")
	replicaRes := httptest.NewRecorder()
	h.ServeHTTP(replicaRes, replicaReq)
	if replicaRes.Code != http.StatusUnauthorized || replicaRes.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("replica unauthorized response = %d, challenge %q", replicaRes.Code, replicaRes.Header().Get("WWW-Authenticate"))
	}
}
