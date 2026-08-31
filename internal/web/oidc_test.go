package web

import (
	"testing"
)

func TestValidOIDCURL(t *testing.T) {
	t.Setenv("ZZIRA_ALLOW_INSECURE_OIDC", "")
	if err := validOIDCURL("https://sso.example.test/tenant"); err != nil {
		t.Fatalf("HTTPS URL rejected: %v", err)
	}
	for _, raw := range []string{"", "relative/path", "http://sso.example.test"} {
		if err := validOIDCURL(raw); err == nil {
			t.Fatalf("insecure or non-absolute URL %q accepted", raw)
		}
	}
	t.Setenv("ZZIRA_ALLOW_INSECURE_OIDC", "true")
	if err := validOIDCURL("http://localhost:8080"); err != nil {
		t.Fatalf("explicit local insecure OIDC URL rejected: %v", err)
	}
	if err := validOIDCURL("http://sso.example.test"); err == nil {
		t.Fatal("non-loopback insecure OIDC URL accepted")
	}
	for _, raw := range []string{"https://user:pass@sso.example.test", "https://sso.example.test?x=1", "https://sso.example.test#fragment"} {
		if err := validOIDCURL(raw); err == nil {
			t.Fatalf("ambiguous OIDC URL %q accepted", raw)
		}
	}
}
