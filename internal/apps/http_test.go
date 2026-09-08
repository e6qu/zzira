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

	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
)

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
		signature := SignRequest(secret, now.Unix(), requestID, method, request.URL.EscapedPath(), []byte(body))
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
	if err != nil || upgraded.Version != "2.0.0" || len(upgraded.Modules) != 1 || upgraded.Modules[0].Title != "Runtime test v2" {
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
