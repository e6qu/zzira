package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedirectLocal(t *testing.T) {
	for _, target := range []string{"/projects/ZZ", "/projects/ZZ/settings?saved=1", "/wiki/spaces/10000", "/wiki/spaces/10000/pages/10001"} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			redirectLocal(rec, httptest.NewRequest("POST", "http://zzira.test/projects/new", nil), target)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != target {
				t.Fatalf("redirect = %d %q", rec.Code, rec.Header().Get("Location"))
			}
		})
	}
	for _, target := range []string{"", "relative", "https://evil.test/path", "//evil.test", "///evil.test", "/\\evil.test", "/%2fevil.test", "/%5cevil.test", "javascript:alert(1)", "/wiki\r\nLocation: https://evil.test", "/wiki/%0aevil", "/%zz"} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			redirectLocal(rec, httptest.NewRequest("POST", "http://zzira.test/projects/new", nil), target)
			if rec.Code != http.StatusInternalServerError || rec.Header().Get("Location") != "" {
				t.Fatalf("unsafe redirect = %d %q", rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}
