package apps

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
)

func TestParseDynamicJiraIssueFieldNeedsNoReadScope(t *testing.T) {
	modules, err := parseDynamicModules([]byte(`{"jiraIssueFields":[{"key":"risk-score","name":{"value":"Risk score"},"description":{"value":"Calculated risk"},"type":"number"}]}`), &models.AppInstallation{})
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 1 || modules[0].Type != "jiraIssueFields" || modules[0].IssueField.Type != models.CustomFieldNumber || !modules[0].IssueField.Dynamic {
		t.Fatalf("dynamic issue fields = %+v", modules)
	}
	if _, err := parseDynamicModules([]byte(`{"jiraIssueFields":[{"key":"owner","name":{"value":"Owner"},"type":"user"}]}`), &models.AppInstallation{}); err == nil {
		t.Fatal("accepted an unsupported dynamic issue-field type")
	}
}

func TestSignedLifecycleAndScopedStorage(t *testing.T) {
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
	workspaceID, workspaceSlug, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	appKey := "runtime." + strings.ReplaceAll(strings.ToLower(store.NewID("test")), "_", "-")
	secret := []byte("a-test-shared-secret-that-is-long-enough")
	box, err := secretbox.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	descriptorRaw := []byte(fmt.Sprintf(`{"key":%q,"name":"Runtime test","baseUrl":"https://apps.example.test/runtime","version":"1.0.0","scopes":["read:jira-work","read:app-storage","write:app-storage"],"modules":[{"key":"runtime-page","type":"jira:globalPage","location":"jira.navigation","title":"Runtime test","body":"Host-rendered module content."}]}`, appKey))
	descriptor, err := ParseDescriptor(descriptorRaw)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal(secret, workspaceID+"/"+appKey)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := st.InstallApp(ctx, workspaceID, actorID, descriptor, descriptorRaw, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM app_installations WHERE id=$1`, installation.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, installation.PrincipalID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, installation.PrincipalID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_type='app' AND target_id=$1`, appKey)
	})

	now := time.Now().UTC().Truncate(time.Second)
	handler := &Handler{Store: st, Secrets: box, WorkspaceSlug: workspaceSlug, Now: func() time.Time { return now }}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /apps/{appKey}/lifecycle/{event}", handler.Lifecycle)
	mux.HandleFunc("GET /apps/{appKey}/storage/{key}", handler.Storage)
	mux.HandleFunc("PUT /apps/{appKey}/storage/{key}", handler.Storage)
	mux.HandleFunc("DELETE /apps/{appKey}/storage/{key}", handler.Storage)
	call := func(method, path, body, requestID string, validSignature bool, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		signature := SignRequest(secret, now.Unix(), requestID, method, request.URL.RequestURI(), []byte(body))
		if !validSignature {
			signature = strings.Repeat("0", 64)
		}
		request.Header.Set("X-Zzira-App-Timestamp", fmt.Sprint(now.Unix()))
		request.Header.Set("X-Zzira-App-Request-Id", requestID)
		request.Header.Set("X-Zzira-App-Signature", signature)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}

	storagePath := "/apps/" + appKey + "/storage/preferences"
	created := call("PUT", storagePath, `{"view":"compact"}`, "request-create-001", true, 200)
	if !strings.Contains(created.Body.String(), `"version":1`) {
		t.Fatal(created.Body.String())
	}
	call("PUT", storagePath, `{"view":"compact"}`, "request-create-001", true, 409)
	read := call("GET", storagePath, "", "request-read-0001", true, 200)
	if !strings.Contains(read.Body.String(), `"view":"compact"`) {
		t.Fatal(read.Body.String())
	}
	call("GET", storagePath, "", "request-invalid-1", false, 401)
	call("POST", "/apps/"+appKey+"/lifecycle/disabled", "", "request-disable-1", true, 204)
	call("GET", storagePath, "", "request-suspended-1", true, 403)
	call("POST", "/apps/"+appKey+"/lifecycle/enabled", "", "request-enable-01", true, 204)

	expandedRaw := []byte(fmt.Sprintf(`{"key":%q,"name":"Runtime test","baseUrl":"https://apps.example.test/runtime","version":"2.0.0","scopes":["read:jira-work","write:jira-work","read:app-storage","write:app-storage"],"modules":[]}`, appKey))
	call("POST", "/apps/"+appKey+"/lifecycle/upgraded", string(expandedRaw), "request-scope-expand", true, 400)
	upgradedRaw := []byte(fmt.Sprintf(`{"key":%q,"name":"Runtime test","baseUrl":"https://apps.example.test/runtime","version":"2.0.0","scopes":["read:jira-work","read:app-storage","write:app-storage"],"modules":[{"key":"runtime-page","type":"jira:globalPage","location":"jira.navigation","title":"Runtime test v2","body":"Upgraded host-rendered content."}]}`, appKey))
	call("POST", "/apps/"+appKey+"/lifecycle/upgraded", string(upgradedRaw), "request-upgrade-1", true, 204)
	upgraded, err := st.AppInstallation(ctx, workspaceID, appKey)
	if err != nil || upgraded.Version != "2.0.0" || len(upgraded.Modules) != 1 || upgraded.Modules[0].Title != "Runtime test v2" || upgraded.Modules[0].ID != installation.Modules[0].ID {
		t.Fatalf("upgraded installation = %+v, %v", upgraded, err)
	}
	call("POST", "/apps/"+appKey+"/lifecycle/uninstalled", "", "request-remove-01", true, 204)
	uninstalled, err := st.AppInstallation(ctx, workspaceID, appKey)
	if err != nil || uninstalled.Status != "uninstalled" || len(uninstalled.Modules) != 0 || len(uninstalled.Scopes) != 0 {
		t.Fatalf("uninstalled app = %+v, %v", uninstalled, err)
	}
	call("GET", storagePath, "", "request-after-remove", true, 401)
	replacementSecret := []byte("replacement-shared-secret-that-is-long-enough")
	replacementCiphertext, err := box.Seal(replacementSecret, workspaceID+"/"+appKey)
	if err != nil {
		t.Fatal(err)
	}
	reinstalled, err := st.InstallApp(ctx, workspaceID, actorID, descriptor, descriptorRaw, replacementCiphertext)
	if err != nil || reinstalled.ID != installation.ID || reinstalled.Status != "active" || len(reinstalled.Modules) != 1 {
		t.Fatalf("reinstalled app = %+v, %v", reinstalled, err)
	}
	decryptedReplacement, err := box.Open(reinstalled.SecretCiphertext, workspaceID+"/"+appKey)
	if err != nil || !bytes.Equal(decryptedReplacement, replacementSecret) {
		t.Fatalf("replacement credential = %q, %v", decryptedReplacement, err)
	}
	if _, err := st.AppStorage(ctx, reinstalled.ID, "preferences"); err == nil {
		t.Fatal("reinstall restored storage deleted during uninstall")
	}
	call("GET", storagePath, "", "request-old-secret-1", true, 401)

	var auditCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE target_type='app' AND target_id=$1`, appKey).Scan(&auditCount); err != nil || auditCount != 6 {
		t.Fatalf("app audit count = %d, %v", auditCount, err)
	}
}

func TestAppPrincipalScopesAndContextualModules(t *testing.T) {
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
	workspaceID, workspaceSlug, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	appKey := "context." + strings.ReplaceAll(strings.ToLower(store.NewID("test")), "_", "-")
	secret := []byte("context-app-shared-secret-that-is-long-enough")
	box, err := secretbox.New(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(fmt.Sprintf(`{"key":%q,"name":"Context app","baseUrl":"https://apps.example.test/context","version":"1.0.0","scopes":["read:jira-work","read:confluence-content"],"modules":[{"key":"issue-panel","type":"jira:issuePanel","location":"jira.issue.view","title":"Issue context","body":"Release risk from the app."},{"key":"gadget","type":"jira:dashboardGadget","location":"jira.dashboard","title":"App health","body":"All app checks are healthy."},{"key":"page-byline","type":"confluence:contentBylineItem","location":"confluence.content.byline","title":"Page review","body":"Reviewed by the app."}]}`, appKey))
	descriptor, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal(secret, workspaceID+"/"+appKey)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, raw, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM app_installations WHERE id=$1`, installation.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, installation.PrincipalID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, installation.PrincipalID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_type='app' AND target_id=$1`, appKey)
	})
	if installation.PrincipalID == "" || !strings.HasPrefix(installation.PrincipalID, "app_") {
		t.Fatalf("principal id = %q", installation.PrincipalID)
	}
	if member, err := st.IsMember(ctx, workspaceID, installation.PrincipalID); err != nil || !member {
		t.Fatalf("app principal membership = %v, %v", member, err)
	}
	dashboardModuleID := ""
	for location, want := range map[string]string{"jira.issue.view": "Issue context", "jira.dashboard": "App health", "confluence.content.byline": "Page review"} {
		modules, err := st.AppModulesByLocation(ctx, workspaceID, location)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, module := range modules {
			if module.InstallationID == installation.ID && module.Title == want {
				found = true
				if location == "jira.dashboard" {
					dashboardModuleID = module.ID
				}
			}
		}
		if !found {
			t.Fatalf("module %q missing at %s: %+v", want, location, modules)
		}
	}
	dashboard, err := st.SaveDashboard(ctx, workspaceID, adminID, "", store.DashboardDetails{Name: "App runtime test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM dashboards WHERE id=$1`, dashboard.ID) })
	gadget, err := st.SaveDashboardGadget(ctx, workspaceID, adminID, dashboard.ID, 0, store.GadgetUpdate{ModuleKey: "app:" + dashboardModuleID})
	if err != nil || gadget.Title != "App health" {
		t.Fatalf("app dashboard gadget = %+v, %v", gadget, err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	handler := &Handler{Store: st, Secrets: box, WorkspaceSlug: workspaceSlug, Now: func() time.Time { return now }}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principalID, err := authn.Identify(r.Context(), st, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(principalID))
	})
	api := handler.APIPrincipal(inner)
	call := func(method, path, body, requestID string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-Zzira-App-Key", appKey)
		req.Header.Set("X-Zzira-App-Timestamp", fmt.Sprint(now.Unix()))
		req.Header.Set("X-Zzira-App-Request-Id", requestID)
		req.Header.Set("X-Zzira-App-Signature", SignRequest(secret, now.Unix(), requestID, method, req.URL.RequestURI(), []byte(body)))
		response := httptest.NewRecorder()
		api.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	if response := call("GET", "/rest/api/3/myself?expand=groups", "", "principal-jira-read", 200); response.Body.String() != installation.PrincipalID {
		t.Fatalf("Jira principal = %q", response.Body.String())
	}
	connectRequest := httptest.NewRequest("GET", "/rest/api/3/myself?expand=groups", nil)
	connectJWT, err := SignConnectJWT(secret, appKey, connectRequest, nil, now, 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	connectRequest.Header.Set("Authorization", "JWT "+connectJWT)
	connectResponse := httptest.NewRecorder()
	api.ServeHTTP(connectResponse, connectRequest)
	if connectResponse.Code != http.StatusOK || connectResponse.Body.String() != installation.PrincipalID {
		t.Fatalf("Connect Jira principal = %d %q", connectResponse.Code, connectResponse.Body.String())
	}
	connectTampered := httptest.NewRequest("GET", "/rest/api/3/myself?expand=permissions", nil)
	connectTampered.Header.Set("Authorization", "JWT "+connectJWT)
	connectTamperedResponse := httptest.NewRecorder()
	api.ServeHTTP(connectTamperedResponse, connectTampered)
	if connectTamperedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("Connect tampered query = %d, want 401", connectTamperedResponse.Code)
	}
	tampered := httptest.NewRequest("GET", "/rest/api/3/myself?expand=permissions", nil)
	tampered.Header.Set("X-Zzira-App-Key", appKey)
	tampered.Header.Set("X-Zzira-App-Timestamp", fmt.Sprint(now.Unix()))
	tampered.Header.Set("X-Zzira-App-Request-Id", "principal-query-tamper")
	tampered.Header.Set("X-Zzira-App-Signature", SignRequest(secret, now.Unix(), "principal-query-tamper", "GET", "/rest/api/3/myself?expand=groups", nil))
	tamperedResponse := httptest.NewRecorder()
	api.ServeHTTP(tamperedResponse, tampered)
	if tamperedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("tampered query = %d, want 401", tamperedResponse.Code)
	}
	if response := call("GET", "/wiki/api/v2/spaces", "", "principal-wiki-read", 200); response.Body.String() != installation.PrincipalID {
		t.Fatalf("Confluence principal = %q", response.Body.String())
	}
	call("POST", "/rest/api/3/issue", `{}`, "principal-jira-write", 403)
	if err := st.UpdateAppState(ctx, workspaceID, adminID, appKey, "suspended", true); err != nil {
		t.Fatal(err)
	}
	call("GET", "/rest/api/3/myself", "", "principal-suspended", 403)
	if err := st.UpdateAppState(ctx, workspaceID, adminID, appKey, "active", true); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateAppState(ctx, workspaceID, adminID, appKey, "uninstalled", true); err != nil {
		t.Fatal(err)
	}
	if gadgets, err := st.DashboardGadgets(ctx, workspaceID, adminID, dashboard.ID); err != nil || len(gadgets) != 0 {
		t.Fatalf("gadgets after app uninstall = %+v, %v", gadgets, err)
	}
	if member, err := st.IsMember(ctx, workspaceID, installation.PrincipalID); err != nil || member {
		t.Fatalf("uninstalled app principal membership = %v, %v", member, err)
	}
	reinstalled, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, raw, ciphertext)
	if err != nil || reinstalled.PrincipalID != installation.PrincipalID {
		t.Fatalf("reinstalled principal = %+v, %v", reinstalled, err)
	}
	if user, err := st.UserByID(ctx, reinstalled.PrincipalID); err != nil || !user.Active {
		t.Fatalf("reinstalled principal user = %+v, %v", user, err)
	}
}

func TestConnectDynamicModulesAndIssueFields(t *testing.T) {
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
	workspaceID, workspaceSlug, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	appKey := "dynamic." + strings.ReplaceAll(strings.ToLower(store.NewID("test")), "_", "-")
	secret := []byte("dynamic-connect-secret-that-is-long-enough")
	box, err := secretbox.New(bytes.Repeat([]byte{13}, 32))
	if err != nil {
		t.Fatal(err)
	}
	descriptorRaw := []byte(fmt.Sprintf(`{"key":%q,"name":"Dynamic test","baseUrl":"https://connect.example.test/base","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"webPanels":[{"key":"static-panel","url":"/static","location":"atl.jira.view.issue.right.context","name":{"value":"Static panel"}}],"jiraIssueFields":[{"key":"static-score","name":{"value":"Static score"},"description":{"value":"Installed with the app"},"type":"number"}]}}`, appKey))
	descriptor, err := ParseDescriptor(descriptorRaw)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal(secret, workspaceID+"/"+appKey)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, descriptorRaw, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM app_installations WHERE id=$1`, installation.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, installation.PrincipalID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, installation.PrincipalID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_type='app' AND target_id=$1`, appKey)
	})
	fieldByKey := func(fields []*models.CustomField, key string) *models.CustomField {
		t.Helper()
		for _, field := range fields {
			if field.AppInstallationID == installation.ID && field.AppModuleKey == key {
				return field
			}
		}
		return nil
	}
	fields, err := st.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil || fieldByKey(fields, "static-score") == nil {
		t.Fatalf("static Connect issue field = %+v, %v", fields, err)
	}
	staticFieldID := fieldByKey(fields, "static-score").ID
	otherWorkspaceFields, err := st.CustomFieldsForWorkspace(ctx, "different-workspace")
	if err != nil || fieldByKey(otherWorkspaceFields, "static-score") != nil {
		t.Fatalf("app field leaked across workspaces: %+v, %v", otherWorkspaceFields, err)
	}
	var projectID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 ORDER BY id LIMIT 1`, workspaceID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	projectFields, err := st.CustomFieldsForProject(ctx, projectID)
	if err != nil || fieldByKey(projectFields, "static-score") == nil {
		t.Fatalf("app field missing from issue metadata: %+v, %v", projectFields, err)
	}

	handler := &Handler{Store: st, Secrets: box, WorkspaceSlug: workspaceSlug}
	dynamicPath := "/rest/atlassian-connect/1/app/module/dynamic"
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+dynamicPath, handler.DynamicModules)
	mux.HandleFunc("POST "+dynamicPath, handler.DynamicModules)
	mux.HandleFunc("DELETE "+dynamicPath, handler.DynamicModules)
	mux.HandleFunc("GET /wiki"+dynamicPath, handler.DynamicModules)
	secured := handler.APIPrincipal(mux)
	now := time.Now().UTC().Truncate(time.Second)
	call := func(method, target, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		token, err := SignConnectJWT(secret, appKey, request, []byte(body), now, 3*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "JWT "+token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		secured.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s = %d, want %d: %s", method, target, response.Code, want, response.Body.String())
		}
		return response
	}

	dynamic := `{"webPanels":[{"key":"dynamic-risk","url":"/risk?issue={issue.key}","location":"atl.jira.view.issue.right.context","name":{"value":"Dynamic risk"}}],"webItems":[{"key":"dynamic-nav","url":"/dynamic-nav","location":"system.top.navigation.bar","name":{"value":"Dynamic navigation"}}],"webhooks":[{"key":"dynamic-hook","event":"jira:issue_created","url":"/hooks/dynamic","filter":"project = ZZ"}],"jiraIssueFields":[{"key":"dynamic-score","name":{"value":"Dynamic score"},"description":{"value":"Registered at runtime"},"type":"number"}]}`
	call(http.MethodPost, dynamicPath, dynamic, http.StatusOK)
	listed := call(http.MethodGet, dynamicPath, "", http.StatusOK)
	if !strings.Contains(listed.Body.String(), `"dynamic-risk"`) || !strings.Contains(listed.Body.String(), `"webPanels"`) || !strings.Contains(listed.Body.String(), `"dynamic-nav"`) || !strings.Contains(listed.Body.String(), `"dynamic-hook"`) || !strings.Contains(listed.Body.String(), `"dynamic-score"`) {
		t.Fatalf("dynamic modules = %s", listed.Body.String())
	}
	wikiListed := call(http.MethodGet, "/wiki"+dynamicPath, "", http.StatusOK)
	if !strings.Contains(wikiListed.Body.String(), `"dynamic-hook"`) {
		t.Fatalf("Confluence dynamic module alias = %s", wikiListed.Body.String())
	}
	modules, err := st.AppModulesByLocation(ctx, workspaceID, "jira.issue.view")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, module := range modules {
		if module.InstallationID == installation.ID && module.Key == "dynamic-risk" && module.Dynamic && module.RemoteURL == "/risk?issue={issue.key}" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dynamic issue panel not materialized: %+v", modules)
	}
	navigation, err := st.AppNavigationModules(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, module := range navigation {
		found = found || module.InstallationID == installation.ID && module.Key == "dynamic-nav" && module.Dynamic && module.Type == "jira:webItem"
	}
	if !found {
		t.Fatalf("dynamic web item not materialized: %+v", navigation)
	}
	hooks, err := st.ActiveAppWebhooks(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, hook := range hooks {
		found = found || hook.InstallationID == installation.ID && hook.Key == "dynamic-hook" && hook.Dynamic && hook.JQL == "project = ZZ"
	}
	if !found {
		t.Fatalf("dynamic webhook not materialized: %+v", hooks)
	}
	fields, err = st.CustomFieldsForWorkspace(ctx, workspaceID)
	dynamicField := fieldByKey(fields, "dynamic-score")
	if err != nil || dynamicField == nil || !dynamicField.Dynamic || dynamicField.Type != models.CustomFieldNumber {
		t.Fatalf("dynamic issue field not materialized: %+v, %v", fields, err)
	}
	dynamicFieldID := dynamicField.ID
	call(http.MethodPost, dynamicPath, `{"webPanels":[{"key":"static-panel","url":"/duplicate","location":"atl.jira.view.issue.right.context","name":{"value":"Duplicate"}}]}`, http.StatusBadRequest)
	if err := st.UpdateAppState(ctx, workspaceID, adminID, appKey, "uninstalled", true); err != nil {
		t.Fatal(err)
	}
	fields, err = st.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil || fieldByKey(fields, "static-score") != nil || fieldByKey(fields, "dynamic-score") != nil {
		t.Fatalf("uninstalled app fields remain visible: %+v, %v", fields, err)
	}
	if _, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, descriptorRaw, ciphertext); err != nil {
		t.Fatal(err)
	}
	modules, err = st.AppModulesByLocation(ctx, workspaceID, "jira.issue.view")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, module := range modules {
		found = found || module.InstallationID == installation.ID && module.Key == "dynamic-risk" && module.Dynamic
	}
	if !found {
		t.Fatalf("dynamic issue panel was not restored after reinstall: %+v", modules)
	}
	fields, err = st.CustomFieldsForWorkspace(ctx, workspaceID)
	reinstalledStatic, reinstalledDynamic := fieldByKey(fields, "static-score"), fieldByKey(fields, "dynamic-score")
	if err != nil || reinstalledStatic == nil || reinstalledDynamic == nil || reinstalledStatic.ID != staticFieldID || reinstalledDynamic.ID != dynamicFieldID {
		t.Fatalf("reinstalled app fields did not retain IDs: %+v, %v", fields, err)
	}

	upgradeRaw := []byte(fmt.Sprintf(`{"key":%q,"name":"Dynamic test","baseUrl":"https://connect.example.test/base","authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"webPanels":[{"key":"dynamic-risk","url":"/promoted","location":"atl.jira.view.issue.right.context","name":{"value":"Promoted static risk"}}],"webItems":[{"key":"dynamic-nav","url":"/promoted-nav","location":"system.top.navigation.bar","name":{"value":"Promoted navigation"}}],"jiraIssueFields":[{"key":"static-score","name":{"value":"Static score"},"description":{"value":"Installed with the app"},"type":"number"},{"key":"dynamic-score","name":{"value":"Promoted score"},"description":{"value":"Now static"},"type":"number"}]}}`, appKey))
	upgrade, err := ParseDescriptor(upgradeRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpgradeApp(ctx, workspaceID, appKey, upgrade, upgradeRaw); err != nil {
		t.Fatal(err)
	}
	listed = call(http.MethodGet, dynamicPath, "", http.StatusOK)
	if strings.Contains(listed.Body.String(), "dynamic-risk") || strings.Contains(listed.Body.String(), "dynamic-nav") {
		t.Fatalf("static upgrade did not remove conflicting dynamic module: %s", listed.Body.String())
	}
	fields, err = st.CustomFieldsForWorkspace(ctx, workspaceID)
	promotedField := fieldByKey(fields, "dynamic-score")
	if err != nil || promotedField == nil || promotedField.Dynamic || promotedField.ID != dynamicFieldID || promotedField.Name != "Promoted score" {
		t.Fatalf("dynamic issue field was not promoted in place: %+v, %v", fields, err)
	}
	call(http.MethodPost, dynamicPath, `{"webPanels":[{"key":"dynamic-remove","url":"/remove","location":"atl.jira.view.issue.right.context","name":{"value":"Remove me"}}]}`, http.StatusOK)
	call(http.MethodDelete, dynamicPath+"?moduleKey=dynamic-remove", "", http.StatusNoContent)
	listed = call(http.MethodGet, dynamicPath, "", http.StatusOK)
	if strings.Contains(listed.Body.String(), "dynamic-remove") {
		t.Fatalf("deleted dynamic module remains: %s", listed.Body.String())
	}
	call(http.MethodPost, dynamicPath, `{"jiraIssueFields":[{"key":"remove-field","name":{"value":"Remove field"},"type":"string"}]}`, http.StatusOK)
	call(http.MethodDelete, dynamicPath+"?moduleKey=remove-field", "", http.StatusNoContent)
	fields, err = st.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil || fieldByKey(fields, "remove-field") != nil {
		t.Fatalf("deleted dynamic issue field remains visible: %+v, %v", fields, err)
	}
	call(http.MethodDelete, dynamicPath, "", http.StatusNoContent)
	listed = call(http.MethodGet, dynamicPath, "", http.StatusOK)
	if strings.Contains(listed.Body.String(), "dynamic-hook") {
		t.Fatalf("delete-all retained a dynamic webhook: %s", listed.Body.String())
	}
}
