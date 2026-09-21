package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/store"
)

// TestAuthenticationPolicyJourney covers an organization that decides how its
// people sign in: one policy admits its members only through the identity
// provider, and the default policy shortens everyone else's session. The
// administration API writes them; sign-in is where they are felt.
func TestAuthenticationPolicyJourney(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	const password = "demo1234"
	hash, err := authn.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := store.NewID("ws")
	adminID, memberID, outsiderID := store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	for _, userID := range []string{adminID, memberID, outsiderID} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$1 || '@example.invalid',$2,$1)`, userID, hash); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Authentication policy test')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, workspaceID, adminID, "admin"); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{memberID, outsiderID} {
		if err := st.AddMember(ctx, workspaceID, userID, "member"); err != nil {
			t.Fatal(err)
		}
	}
	adminToken := store.NewID("secret")
	if err := st.CreateAPIToken(ctx, store.NewID("tok"), adminID, store.HashToken(adminToken), "authentication-policy-test"); err != nil {
		t.Fatal(err)
	}
	organization, err := st.OrganizationByWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id IN ($1,$2,$3)`, adminID, memberID, outsiderID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id IN ($1,$2,$3)`, adminID, memberID, outsiderID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_policies WHERE organization_id=$1::uuid`, organization.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `
			DELETE FROM role_bindings rb
			WHERE rb.principal_id IN ($1,$2,$3)
			   OR rb.scope_id=$4
			   OR rb.scope_id IN (SELECT id::text FROM sites WHERE organization_id=$4::uuid)
			   OR rb.scope_id IN (SELECT p.id::text FROM products p JOIN sites s ON s.id=p.site_id WHERE s.organization_id=$4::uuid)`,
			adminID, memberID, outsiderID, organization.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organizations WHERE id::text=$1`, organization.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2,$3)`, adminID, memberID, outsiderID)
	}()

	handler := &Handler{Store: st, BaseURL: "https://zzira.example", WorkspaceSlug: workspaceID}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/policies", handler.Policies)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/policies", handler.Policies)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/policies/{policyId}", handler.PolicyDetails)
	mux.HandleFunc("PUT /admin/v1/orgs/{orgId}/policies/{policyId}", handler.PolicyDetails)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/policies/{policyId}/members", handler.PolicyMembers)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/policies/{policyId}/members/{accountId}", handler.PolicyMemberDetails)
	mux.HandleFunc("DELETE /admin/v1/orgs/{orgId}/policies/{policyId}/members/{accountId}", handler.PolicyMemberDetails)

	call := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(encoded)
		} else {
			reader = bytes.NewReader(nil)
		}
		request := httptest.NewRequest(method, path, reader)
		request.Header.Set("Authorization", "Bearer "+adminToken)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, response.Code, want, response.Body.String())
		}
		if response.Body.Len() == 0 {
			return nil
		}
		var decoded map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s: %v body=%s", method, path, err, response.Body.String())
		}
		return decoded
	}
	policiesPath := "/admin/v1/orgs/" + organization.ID + "/policies"
	authenticationPolicy := func(name string, config map[string]any, status string) map[string]any {
		return map[string]any{"data": map[string]any{"type": "policy", "attributes": map[string]any{
			"type": "authentication-policy", "name": name, "status": status, "config": config,
		}}}
	}

	// Settings are what an authentication policy carries: it is refused
	// without them, with values that belong to another policy type, and with
	// a session nobody could work in.
	call(http.MethodPost, policiesPath, map[string]any{"data": map[string]any{"type": "policy", "attributes": map[string]any{
		"type": "authentication-policy", "name": "No settings", "status": "enabled",
	}}}, http.StatusBadRequest)
	call(http.MethodPost, policiesPath, map[string]any{"data": map[string]any{"type": "policy", "attributes": map[string]any{
		"type": "authentication-policy", "name": "Values instead", "status": "enabled",
		"config": map[string]any{"sessionDurationMinutes": 60}, "rule": map[string]any{"in": []string{"192.0.2.0/24"}},
	}}}, http.StatusBadRequest)
	call(http.MethodPost, policiesPath, authenticationPolicy("Too short", map[string]any{"sessionDurationMinutes": 1}, "enabled"), http.StatusBadRequest)
	call(http.MethodPost, policiesPath, authenticationPolicy("Weak passwords", map[string]any{
		"sessionDurationMinutes": 60, "passwordMinimumLength": 4,
	}, "enabled"), http.StatusBadRequest)

	created := call(http.MethodPost, policiesPath, authenticationPolicy("Contractors", map[string]any{
		"enforceSSO": true, "sessionDurationMinutes": 30,
	}, "enabled"), http.StatusAccepted)
	policyID := created["data"].(map[string]any)["id"].(string)
	policyPath := policiesPath + "/" + policyID
	fetched := call(http.MethodGet, policyPath, nil, http.StatusOK)
	config := fetched["data"].(map[string]any)["attributes"].(map[string]any)["rule"].(map[string]any)["config"].(map[string]any)
	if config["enforceSSO"] != true || config["sessionDurationMinutes"].(float64) != 30 {
		t.Fatalf("policy did not keep its settings: %#v", fetched)
	}
	listed := call(http.MethodGet, policiesPath+"?type=authentication-policy", nil, http.StatusOK)
	if len(listed["data"].([]any)) != 1 {
		t.Fatalf("unexpected authentication policy page: %#v", listed)
	}

	// Until a policy covers them, everyone signs in with a password and keeps
	// the site's own session length.
	if signIn, err := authn.Login(ctx, st, memberID+"@example.invalid", password); err != nil || signIn.TTL != 30*24*time.Hour {
		t.Fatalf("sign-in with no policy: ttl=%s err=%v", signIn.TTL, err)
	}

	memberPath := policyPath + "/members/" + memberID
	call(http.MethodPost, policyPath+"/members/"+store.NewID("usr"), nil, http.StatusNotFound)
	call(http.MethodPost, memberPath, nil, http.StatusAccepted)
	members := call(http.MethodGet, policyPath+"/members", nil, http.StatusOK)
	listedMembers := members["data"].([]any)
	if len(listedMembers) != 1 || listedMembers[0].(map[string]any)["id"] != memberID {
		t.Fatalf("unexpected member page: %#v", members)
	}

	// The policy is felt at sign-in: its member is admitted only through the
	// identity provider, and the session it makes lasts as long as the policy
	// says.
	if _, err := authn.Login(ctx, st, memberID+"@example.invalid", password); !errors.Is(err, authn.ErrSSORequired) {
		t.Fatalf("password sign-in under a single sign-on policy: %v, want ErrSSORequired", err)
	}
	_, ttl, err := authn.LoginOIDC(ctx, st, memberID, "id-token", "https://issuer.example.invalid", memberID+"-subject", "")
	if err != nil || ttl != 30*time.Minute {
		t.Fatalf("single sign-on under the policy: ttl=%s err=%v", ttl, err)
	}

	// Everyone else keeps their password until a default policy says
	// otherwise, and then gets that policy's session length.
	if signIn, err := authn.Login(ctx, st, outsiderID+"@example.invalid", password); err != nil || signIn.TTL != 30*24*time.Hour {
		t.Fatalf("sign-in outside the policy: ttl=%s err=%v", signIn.TTL, err)
	}
	defaults := call(http.MethodPost, policiesPath, authenticationPolicy("Everyone else", map[string]any{
		"sessionDurationMinutes": 60, "default": true,
	}, "enabled"), http.StatusAccepted)
	defaultID := defaults["data"].(map[string]any)["id"].(string)
	if signIn, err := authn.Login(ctx, st, outsiderID+"@example.invalid", password); err != nil || signIn.TTL != time.Hour {
		t.Fatalf("sign-in under the default policy: ttl=%s err=%v", signIn.TTL, err)
	}
	// A policy of one's own wins over the default.
	if _, err := authn.Login(ctx, st, memberID+"@example.invalid", password); !errors.Is(err, authn.ErrSSORequired) {
		t.Fatalf("the default policy overrode the member's own: %v", err)
	}
	// A disabled policy enforces nothing.
	call(http.MethodPut, policiesPath+"/"+defaultID, authenticationPolicy("Everyone else", map[string]any{
		"sessionDurationMinutes": 60, "default": true,
	}, "disabled"), http.StatusAccepted)
	if signIn, err := authn.Login(ctx, st, outsiderID+"@example.invalid", password); err != nil || signIn.TTL != 30*24*time.Hour {
		t.Fatalf("sign-in under a disabled default policy: ttl=%s err=%v", signIn.TTL, err)
	}

	// Membership moves with the person: joining a second policy leaves the
	// first, so one policy always answers for them.
	call(http.MethodPost, policiesPath+"/"+defaultID+"/members/"+memberID, nil, http.StatusAccepted)
	stillListed := call(http.MethodGet, policyPath+"/members", nil, http.StatusOK)
	if len(stillListed["data"].([]any)) != 0 {
		t.Fatalf("the member stayed under two policies: %#v", stillListed)
	}
	call(http.MethodDelete, policiesPath+"/"+defaultID+"/members/"+memberID, nil, http.StatusNoContent)
	call(http.MethodDelete, policiesPath+"/"+defaultID+"/members/"+memberID, nil, http.StatusNotFound)
	if signIn, err := authn.Login(ctx, st, memberID+"@example.invalid", password); err != nil || signIn.TTL != 30*24*time.Hour {
		t.Fatalf("sign-in after leaving every policy: ttl=%s err=%v", signIn.TTL, err)
	}

	// A policy can ask for longer passwords than the site does, and that is
	// read where a password is set rather than at sign-in.
	longer := call(http.MethodPost, policiesPath, authenticationPolicy("Longer passwords", map[string]any{
		"sessionDurationMinutes": 60, "passwordMinimumLength": 16,
	}, "enabled"), http.StatusAccepted)
	longerID := longer["data"].(map[string]any)["id"].(string)
	call(http.MethodPost, policiesPath+"/"+longerID+"/members/"+outsiderID, nil, http.StatusAccepted)
	var refused authn.ErrPasswordRefused
	if err := authn.ChangePassword(ctx, st, outsiderID, password, "still-too-short", ""); !errors.As(err, &refused) {
		t.Fatalf("a password under the policy's rule: %v, want ErrPasswordRefused", err)
	}
	if err := authn.ChangePassword(ctx, st, outsiderID, password, "a-password-long-enough", ""); err != nil {
		t.Fatalf("a password that meets the policy's rule: %v", err)
	}
	// Someone the policy does not cover keeps the site's own rule.
	if err := authn.ChangePassword(ctx, st, memberID, password, "nine12345", ""); err != nil {
		t.Fatalf("a password under the site's own rule was refused: %v", err)
	}
	call(http.MethodDelete, policiesPath+"/"+longerID+"/members/"+outsiderID, nil, http.StatusNoContent)

	// Members belong to authentication policies alone.
	accessPolicy := call(http.MethodPost, policiesPath, map[string]any{"data": map[string]any{"type": "policy", "attributes": map[string]any{
		"type": "ip-allowlist", "name": "Office network", "status": "disabled", "rule": map[string]any{"in": []string{"192.0.2.0/24"}},
	}}}, http.StatusAccepted)
	accessID := accessPolicy["data"].(map[string]any)["id"].(string)
	call(http.MethodGet, policiesPath+"/"+accessID+"/members", nil, http.StatusBadRequest)
	call(http.MethodPost, policiesPath+"/"+accessID+"/members/"+memberID, nil, http.StatusBadRequest)

	// Every change is written to the organization's audit log.
	events, _, err := st.QueryOrganizationAuditEvents(ctx, organization.ID, store.OrganizationAuditFilter{Action: "policy.member-added", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("policy member additions in the audit log: %d, want 3", len(events))
	}
}
