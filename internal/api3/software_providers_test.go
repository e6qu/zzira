package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestSoftwareProviderModules covers the DevOps provider modules as a
// provider app sees them: bulk submissions with per-entity validation,
// update ordering, reads, deletes and linked workspaces.
func TestSoftwareProviderModules(t *testing.T) {
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
	if err = store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	ws, actor, projectID, issueID := store.NewID("ws"), store.NewID("usr"), store.NewID("project"), store.NewID("iss")
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Providers')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Provider app')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actor, store.HashToken(actor))
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id,project_type_key) VALUES($1,$2,'DEV','Providers','wf_default',$3,'software')`, projectID, ws, actor)
	exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,updated_seq) VALUES($1,$2,$3,'DEV-1','Ship it','st_todo','it_task',$4,0)`, issueID, ws, projectID, actor)
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM software_provider_entities WHERE workspace_id=$1`, `DELETE FROM software_linked_workspaces WHERE workspace_id=$1`,
			`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM custom_fields WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(query, ws)
		}
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actor)
		exec(`DELETE FROM users WHERE id=$1`, actor)
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, BaseURL: "https://zzira.test", WorkspaceSlug: ws}
	call := func(method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(actor+"@example.test", actor)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	decode := func(raw string) map[string]any {
		t.Helper()
		out := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(raw, err)
		}
		return out
	}

	// Feature flags: accepted, failed and unknown issues are reported apart.
	flag := func(id, key string, sequence int) string {
		return `{"schemaVersion":"1.0","id":"` + id + `","key":"` + key + `","updateSequenceId":` + string(rune('0'+sequence)) + `,
			"displayName":"Dark mode","associations":[{"associationType":"issueIdOrKeys","values":["DEV-1","NOPE-9"]}],
			"summary":{"status":{"enabled":true,"rollout":{"percentage":50}},"lastUpdated":"2026-09-14T10:00:00Z"},
			"details":[{"url":"https://flags.example.com/ff-1","lastUpdated":"2026-09-14T10:00:00Z","environment":{"name":"prod","type":"production"},"status":{"enabled":true}}]}`
	}
	submitted := decode(call(http.MethodPost, "/rest/featureflags/0.1/bulk",
		`{"properties":{"accountId":"acc-1"},"flags":[`+flag("ff-1", "dark-mode", 2)+`,{"id":"ff-2","key":"broken","updateSequenceId":1}]}`, http.StatusAccepted))
	if accepted := submitted["acceptedFeatureFlags"].([]any); len(accepted) != 1 || accepted[0] != "ff-1" {
		t.Fatal(submitted)
	}
	if failed := submitted["failedFeatureFlags"].(map[string]any); failed["ff-2"] == nil {
		t.Fatal(submitted)
	}
	if unknown := submitted["unknownIssueKeys"].([]any); len(unknown) != 1 || unknown[0] != "NOPE-9" {
		t.Fatal(submitted)
	}
	if stored := decode(call(http.MethodGet, "/rest/featureflags/0.1/flag/ff-1", "", http.StatusOK)); stored["key"] != "dark-mode" {
		t.Fatal(stored)
	}
	var associated []string
	if err = st.Pool.QueryRow(ctx, `SELECT issue_ids FROM software_provider_entities WHERE workspace_id=$1 AND entity_id='ff-1'`, ws).Scan(&associated); err != nil || len(associated) != 1 || associated[0] != issueID {
		t.Fatalf("associated=%v err=%v", associated, err)
	}
	call(http.MethodPost, "/rest/featureflags/0.1/bulk", `{"flags":[`+flag("ff-1", "stale", 1)+`]}`, http.StatusAccepted)
	if stored := decode(call(http.MethodGet, "/rest/featureflags/0.1/flag/ff-1", "", http.StatusOK)); stored["key"] != "dark-mode" {
		t.Fatalf("an older update replaced newer data: %v", stored)
	}
	call(http.MethodPost, "/rest/featureflags/0.1/bulk", `{"properties":{"accountId":"acc-1"},"flags":[`+flag("ff-1", "dark-mode-v2", 3)+`]}`, http.StatusAccepted)
	if stored := decode(call(http.MethodGet, "/rest/featureflags/0.1/flag/ff-1", "", http.StatusOK)); stored["key"] != "dark-mode-v2" {
		t.Fatal(stored)
	}
	if rejected := call(http.MethodPost, "/rest/featureflags/0.1/bulk", `{"flags":[],"extra":true}`, http.StatusBadRequest); !strings.HasPrefix(rejected, `[{"message":`) {
		t.Fatal(rejected)
	}
	call(http.MethodDelete, "/rest/featureflags/0.1/bulkByProperties", "", http.StatusBadRequest)
	call(http.MethodDelete, "/rest/featureflags/0.1/bulkByProperties?accountId=other", "", http.StatusAccepted)
	call(http.MethodGet, "/rest/featureflags/0.1/flag/ff-1", "", http.StatusOK)
	call(http.MethodDelete, "/rest/featureflags/0.1/bulkByProperties?accountId=acc-1&_updateSequenceId=9", "", http.StatusAccepted)
	call(http.MethodGet, "/rest/featureflags/0.1/flag/ff-1", "", http.StatusNotFound)

	// Remote links: a link only associated with unknown issues is rejected.
	link := `{"id":"rl-1","updateSequenceNumber":1,"displayName":"Runbook","url":"https://docs.example.com/runbook","type":"document","lastUpdated":"2026-09-14T10:00:00Z",
		"associations":[{"associationType":"issueIdOrKeys","values":["DEV-1"]}],"status":{"appearance":"success","label":"Current"}}`
	orphan := `{"id":"rl-2","updateSequenceNumber":1,"displayName":"Lost","url":"https://docs.example.com/lost","type":"other","lastUpdated":"2026-09-14T10:00:00Z",
		"associations":[{"associationType":"issueIdOrKeys","values":["NOPE-1"]}]}`
	links := decode(call(http.MethodPost, "/rest/remotelinks/1.0/bulk", `{"remoteLinks":[`+link+`,`+orphan+`]}`, http.StatusAccepted))
	if accepted := links["acceptedRemoteLinks"].([]any); len(accepted) != 1 || links["rejectedRemoteLinks"].(map[string]any)["rl-2"] == nil {
		t.Fatal(links)
	}
	call(http.MethodGet, "/rest/remotelinks/1.0/remotelink/rl-1", "", http.StatusOK)
	call(http.MethodDelete, "/rest/remotelinks/1.0/remotelink/rl-1", "", http.StatusAccepted)
	call(http.MethodGet, "/rest/remotelinks/1.0/remotelink/rl-1", "", http.StatusNotFound)

	// Security: linked workspaces and vulnerabilities.
	call(http.MethodPost, "/rest/security/1.0/linkedWorkspaces/bulk", `{"workspaceIds":["sec-ws-1"]}`, http.StatusAccepted)
	call(http.MethodPost, "/rest/security/1.0/linkedWorkspaces/bulk", `{"workspaceIds":["bad id"]}`, http.StatusBadRequest)
	if linked := decode(call(http.MethodGet, "/rest/security/1.0/linkedWorkspaces", "", http.StatusOK)); len(linked["workspaceIds"].([]any)) != 1 {
		t.Fatal(linked)
	}
	if one := decode(call(http.MethodGet, "/rest/security/1.0/linkedWorkspaces/sec-ws-1", "", http.StatusOK)); one["workspaceId"] != "sec-ws-1" || one["updatedAt"] == "" {
		t.Fatal(one)
	}
	vulnerability := `{"schemaVersion":"1.0","id":"vuln-1","updateSequenceNumber":1,"containerId":"repo/app","displayName":"CVE-2026-1","description":"Remote code execution",
		"url":"https://security.example.com/vuln-1","type":"sca","introducedDate":"2026-09-01T00:00:00Z","lastUpdated":"2026-09-14T10:00:00Z",
		"severity":{"level":"critical"},"status":"open","addAssociations":[{"associationType":"issueIdOrKeys","values":["DEV-1"]}]}`
	if accepted := decode(call(http.MethodPost, "/rest/security/1.0/bulk", `{"operationType":"SCAN","vulnerabilities":[`+vulnerability+`]}`, http.StatusAccepted)); len(accepted["acceptedVulnerabilities"].([]any)) != 1 {
		t.Fatal(accepted)
	}
	call(http.MethodPost, "/rest/security/1.0/bulk", `{"operationType":"LATER","vulnerabilities":[`+vulnerability+`]}`, http.StatusBadRequest)
	call(http.MethodGet, "/rest/security/1.0/vulnerability/vuln-1", "", http.StatusOK)
	call(http.MethodDelete, "/rest/security/1.0/linkedWorkspaces/bulk?workspaceIds=sec-ws-1", "", http.StatusAccepted)
	call(http.MethodGet, "/rest/security/1.0/linkedWorkspaces/sec-ws-1", "", http.StatusNotFound)

	// Operations: incidents, post-incident reviews and linked workspaces.
	if linked := decode(call(http.MethodPost, "/rest/operations/1.0/linkedWorkspaces/bulk", `{"workspaceIds":["ops-1"]}`, http.StatusAccepted)); len(linked["acceptedWorkspaceIds"].([]any)) != 1 {
		t.Fatal(linked)
	}
	incident := `{"schemaVersion":"1.0","id":"inc-1","updateSequenceNumber":1,"affectedComponents":["api"],"summary":"API down","description":"Errors",
		"url":"https://ops.example.com/inc-1","createdDate":"2026-09-14T09:00:00Z","lastUpdated":"2026-09-14T10:00:00Z","status":"open",
		"severity":{"level":"P1"},"associations":[{"associationType":"issueIdOrKeys","values":["DEV-1"]}]}`
	if accepted := decode(call(http.MethodPost, "/rest/operations/1.0/bulk", `{"incidents":[`+incident+`]}`, http.StatusAccepted)); len(accepted["acceptedIncidents"].([]any)) != 1 {
		t.Fatal(accepted)
	}
	review := `{"schemaVersion":"1.0","id":"pir-1","updateSequenceNumber":1,"reviews":["inc-1"],"summary":"API outage review","description":"Lessons",
		"url":"https://ops.example.com/pir-1","createdDate":"2026-09-14T11:00:00Z","lastUpdated":"2026-09-14T11:00:00Z","status":"in progress"}`
	call(http.MethodPost, "/rest/operations/1.0/bulk", `{"reviews":[`+review+`]}`, http.StatusAccepted)
	call(http.MethodGet, "/rest/operations/1.0/incidents/inc-1", "", http.StatusOK)
	call(http.MethodGet, "/rest/operations/1.0/post-incident-reviews/pir-1", "", http.StatusOK)
	call(http.MethodDelete, "/rest/operations/1.0/post-incident-reviews/pir-1", "", http.StatusAccepted)
	call(http.MethodGet, "/rest/operations/1.0/post-incident-reviews/pir-1", "", http.StatusNotFound)

	// DevOps components: enum values are validated per component.
	component := `{"schemaVersion":"1.0","id":"cmp-1","updateSequenceNumber":1,"name":"Checkout","description":"Payments","url":"https://compass.example.com/cmp-1",
		"avatarUrl":"https://compass.example.com/cmp-1.png","tier":"Tier 1","componentType":"Service","lastUpdated":"2026-09-14T10:00:00Z"}`
	invalid := strings.Replace(strings.Replace(component, `"Tier 1"`, `"Tier 9"`, 1), "cmp-1", "cmp-2", 1)
	components := decode(call(http.MethodPost, "/rest/devopscomponents/1.0/bulk", `{"devopsComponents":[`+component+`,`+invalid+`]}`, http.StatusAccepted))
	if len(components["acceptedComponents"].([]any)) != 1 || components["failedComponents"].(map[string]any)["cmp-2"] == nil {
		t.Fatal(components)
	}
	call(http.MethodGet, "/rest/devopscomponents/1.0/devopscomponents/cmp-1", "", http.StatusOK)
	call(http.MethodDelete, "/rest/devopscomponents/1.0/devopscomponents/cmp-1", "", http.StatusAccepted)
	call(http.MethodGet, "/rest/devopscomponents/1.0/devopscomponents/cmp-1", "", http.StatusNotFound)
}
