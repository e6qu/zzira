package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// The custom field settings page keeps a select field's options: it adds,
// reorders in both directions, and deletes -- moving work items to another
// option when they hold the one going.
func TestCustomFieldOptionsAreKeptFromTheSettingsPage(t *testing.T) {
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
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Field options test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Field owner')`, adminID, adminID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	t.Cleanup(func() {
		exec(`DELETE FROM sessions WHERE user_id=$1`, adminID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	seq, err := st.NextCustomFieldNumber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fieldID := fmt.Sprintf("customfield_%d", seq)
	field, err := st.CreateWorkspaceCustomFieldOfKind(ctx, workspaceID, fieldID, "Risk",
		models.CustomFieldSelect, models.CustomFieldTypeKeys[models.CustomFieldSelect], "How risky the change is")
	if err != nil {
		t.Fatal(err)
	}
	contexts, err := st.CustomFieldContexts(ctx, workspaceID, field.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(contexts) == 0 {
		t.Fatal("a new field has no context")
	}
	contextID := contexts[0].ID

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID}
	token, err := authn.LoginOIDC(ctx, st, adminID, "id-token", "https://issuer.example.invalid", adminID+"-subject", "")
	if err != nil {
		t.Fatal(err)
	}
	post := func(form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		form.Set("contextId", contextID)
		request := httptest.NewRequest(http.MethodPost, "/settings/custom-fields/"+field.ID, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(&http.Cookie{Name: "zzira_session", Value: token})
		request.SetPathValue("id", field.ID)
		response := httptest.NewRecorder()
		h.CustomFieldContextMutation(response, request)
		return response
	}
	values := func() []string {
		t.Helper()
		options, optionErr := st.CustomFieldOptions(ctx, workspaceID, field.ID, contextID)
		if optionErr != nil {
			t.Fatal(optionErr)
		}
		out := make([]string, 0, len(options))
		for _, option := range options {
			out = append(out, option.Value)
		}
		return out
	}
	optionID := func(value string) string {
		t.Helper()
		options, optionErr := st.CustomFieldOptions(ctx, workspaceID, field.ID, contextID)
		if optionErr != nil {
			t.Fatal(optionErr)
		}
		for _, option := range options {
			if option.Value == value {
				return option.ID
			}
		}
		t.Fatalf("no option %q among %v", value, values())
		return ""
	}
	for _, value := range []string{"Low", "Medium", "High"} {
		if response := post(url.Values{"action": {"add-option"}, "value": {value}}); response.Code != http.StatusSeeOther {
			t.Fatalf("add %s = %d", value, response.Code)
		}
	}
	if got := strings.Join(values(), ","); got != "Low,Medium,High" {
		t.Fatalf("options after adding = %s", got)
	}

	// Down and up are the moves the API expresses as "after this option".
	if response := post(url.Values{"action": {"move-option"}, "optionId": {optionID("Low")}, "direction": {"down"}}); response.Code != http.StatusSeeOther {
		t.Fatalf("move down = %d", response.Code)
	}
	if got := strings.Join(values(), ","); got != "Medium,Low,High" {
		t.Fatalf("options after moving Low down = %s", got)
	}
	if response := post(url.Values{"action": {"move-option"}, "optionId": {optionID("High")}, "direction": {"up"}}); response.Code != http.StatusSeeOther {
		t.Fatalf("move up = %d", response.Code)
	}
	if got := strings.Join(values(), ","); got != "Medium,High,Low" {
		t.Fatalf("options after moving High up = %s", got)
	}
	if response := post(url.Values{"action": {"move-option"}, "optionId": {optionID("Medium")}, "direction": {"last"}}); response.Code != http.StatusSeeOther {
		t.Fatalf("move last = %d", response.Code)
	}
	if got := strings.Join(values(), ","); got != "High,Low,Medium" {
		t.Fatalf("options after moving Medium last = %s", got)
	}

	if response := post(url.Values{"action": {"delete-option"}, "optionId": {optionID("Low")}}); response.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", response.Code)
	}
	if got := strings.Join(values(), ","); got != "High,Medium" {
		t.Fatalf("options after deleting Low = %s", got)
	}
}
