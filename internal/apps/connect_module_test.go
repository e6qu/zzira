package apps

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestConnectModuleURLPreservesBasePathAndSignsAppRelativeRequest(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	secret := []byte("remote-module-secret")
	target, err := ConnectModuleURL("https://app.example.test/connect/jira", "/panel?selected={issue.key}", "https://zzira.example.test", "workspace-client-key", secret, "channel-7", url.Values{"issue.key": {"OPS-7"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/connect/jira/panel" || parsed.Query().Get("selected") != "OPS-7" || parsed.Query().Get("issue.key") != "OPS-7" || parsed.Query().Get("xdm_e") != "https://zzira.example.test" {
		t.Fatalf("remote module URL = %s", target)
	}
	token := parsed.Query().Get("jwt")
	if token == "" || !strings.Contains(target, "xdm_c=channel-7") {
		t.Fatalf("signed module URL = %s", target)
	}
	query := parsed.Query()
	query.Del("jwt")
	request := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/panel", RawQuery: query.Encode()}}
	if err := verifyConnectJWT(token, secret, request, nil, now); err != nil {
		t.Fatalf("module JWT verification: %v", err)
	}
}

func TestConnectModuleURLExpandsProjectContext(t *testing.T) {
	target, err := ConnectModuleURL("https://app.example.test/connect", "/project?selected={project.key}&id={project.id}", "https://zzira.example.test", "workspace-client-key", []byte("remote-module-secret"), "channel-project", url.Values{"project.key": {"OPS"}, "project.id": {"10001"}}, time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("selected") != "OPS" || parsed.Query().Get("id") != "10001" || parsed.Query().Get("project.key") != "OPS" || parsed.Query().Get("project.id") != "10001" {
		t.Fatalf("project module URL = %s", target)
	}
}
