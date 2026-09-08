package apps

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestConnectCanonicalRequestMatchesAtlassianRules(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://example.test/jira/path/to/service/?zee_last=param&repeated=parameter+1&first=param&repeated=parameter+2", nil)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical := "GET&/path/to/service&first=param&repeated=parameter%201%2Cparameter%202&zee_last=param"
	wantHash := sha256.Sum256([]byte(wantCanonical))
	got, err := connectQueryHash(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("query hash = %s, want hash of %q", got, wantCanonical)
	}
	if got := connectCanonicalQuery(url.Values{"symbols": {"+,*~"}, "empty": {""}}); got != "empty=&symbols=%2B%2C%2A~" {
		t.Fatalf("canonical encoded query = %q", got)
	}
}

func TestConnectJWTVerifiesSignatureTimeAndQueryHash(t *testing.T) {
	secret := []byte("connect-jwt-test-secret")
	now := time.Unix(1_800_000_000, 0).UTC()
	request, _ := http.NewRequest(http.MethodGet, "https://zzira.test/rest/api/3/issue/ZZ-1?expand=names", nil)
	token, err := SignConnectJWT(secret, "connect.example", request, nil, now, 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if issuer, err := connectIssuer(token); err != nil || issuer != "connect.example" {
		t.Fatalf("issuer = %q, %v", issuer, err)
	}
	if err := verifyConnectJWT(token, secret, request, nil, now); err != nil {
		t.Fatal(err)
	}
	tampered, _ := http.NewRequest(http.MethodGet, "https://zzira.test/rest/api/3/issue/ZZ-1?expand=changelog", nil)
	if err := verifyConnectJWT(token, secret, tampered, nil, now); err == nil {
		t.Fatal("accepted a token after query tampering")
	}
	if err := verifyConnectJWT(token, secret, request, nil, now.Add(7*time.Minute)); err == nil {
		t.Fatal("accepted an expired Connect token")
	}
}
