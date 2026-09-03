package authn

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtectCookieMutations(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	tests := []struct {
		name, method, origin, authorization string
		want                                int
	}{
		{name: "safe request", method: http.MethodGet, want: http.StatusNoContent},
		{name: "same origin form", method: http.MethodPost, origin: "https://zzira.example", want: http.StatusNoContent},
		{name: "missing origin", method: http.MethodPost, want: http.StatusForbidden},
		{name: "cross origin", method: http.MethodDelete, origin: "https://attacker.example", want: http.StatusForbidden},
		{name: "api token", method: http.MethodPost, authorization: "Basic dXNlcjp0b2tlbg==", want: http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "https://zzira.example/rest/api/3/issue", nil)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session"})
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Authorization", tt.authorization)
			w := httptest.NewRecorder()
			ProtectCookieMutations(next).ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("status=%d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "https://zzira.example/", nil)
	SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(w, r)

	for _, header := range []string{
		"Content-Security-Policy",
		"Permissions-Policy",
		"Referrer-Policy",
		"X-Content-Type-Options",
		"X-Frame-Options",
	} {
		if w.Header().Get(header) == "" {
			t.Errorf("%s is missing", header)
		}
	}
}

func TestSecureCookiesDefaultToTheExternalURL(t *testing.T) {
	t.Setenv("COOKIE_SECURE", "")
	t.Setenv("ZZIRA_EXTERNAL_URL", "https://zzira.example")
	if !SecureCookies() {
		t.Fatal("HTTPS external URL did not enable secure cookies")
	}
	t.Setenv("ZZIRA_EXTERNAL_URL", "http://localhost:8080")
	if SecureCookies() {
		t.Fatal("local HTTP URL unexpectedly enabled secure cookies")
	}
	t.Setenv("COOKIE_SECURE", "true")
	if !SecureCookies() {
		t.Fatal("explicit COOKIE_SECURE=true was ignored")
	}
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("ZZIRA_EXTERNAL_URL", "https://zzira.example")
	if SecureCookies() {
		t.Fatal("explicit COOKIE_SECURE=false was ignored")
	}
}
