package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestSyncKeepsRestrictedHistoryHiddenAfterIssueDelete(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	workspaceID := NewID("ws")
	projectID := NewID("prj")
	schemeID := NewID("sch")
	memberID := NewID("usr")
	excludedID := NewID("usr")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces (id,slug,name) VALUES ($1,$1,'Security test')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO projects (id,workspace_id,key,name) VALUES ($1,$2,'SEC','Security test')`, projectID, workspaceID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, cleanup := range []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM actions WHERE workspace_id=$1`, []any{workspaceID}},
			{`DELETE FROM deleted_issue_visibility WHERE workspace_id=$1`, []any{workspaceID}},
			{`DELETE FROM projects WHERE id=$1`, []any{projectID}},
			{`DELETE FROM security_schemes WHERE id=$1`, []any{schemeID}},
			{`DELETE FROM workspaces WHERE id=$1`, []any{workspaceID}},
		} {
			if _, err := st.Pool.Exec(ctx, cleanup.sql, cleanup.args...); err != nil {
				t.Errorf("cleanup %q: %v", cleanup.sql, err)
			}
		}
	}()

	scheme := models.SecurityScheme{
		ID: schemeID, Name: "Restricted",
		Levels: []models.SecurityLevel{{ID: "private", Name: "Private", Members: []string{memberID}}},
	}
	if err := st.CreateSecurityScheme(ctx, scheme); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignSecurityScheme(ctx, projectID, schemeID); err != nil {
		t.Fatal(err)
	}
	issue, _, err := st.CreateIssue(ctx, memberID, projectID, "secret", json.RawMessage(`{"type":"doc","version":1}`), "st_todo", "it_task", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	level := "private"
	if _, _, err := st.UpdateIssue(ctx, memberID, workspaceID, issue.ID, IssueUpdate{SecurityLevelID: &level}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE issues SET labels=ARRAY['classified'] WHERE id=$1`, issue.ID); err != nil {
		t.Fatal(err)
	}
	if total, labels, err := st.Labels(ctx, workspaceID, excludedID, "class"); err != nil || total != 0 || len(labels) != 0 {
		t.Fatalf("excluded labels total=%d labels=%v err=%v", total, labels, err)
	}
	if total, labels, err := st.Labels(ctx, workspaceID, memberID, "class"); err != nil || total != 1 || len(labels) != 1 {
		t.Fatalf("member labels total=%d labels=%v err=%v", total, labels, err)
	}
	if _, _, err := st.CreateComment(ctx, memberID, workspaceID, issue.ID, json.RawMessage(`{"type":"doc","version":1}`)); err != nil {
		t.Fatal(err)
	}

	beforeDelete, err := st.ActionsSince(ctx, workspaceID, excludedID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeDelete) != 0 {
		t.Fatalf("excluded user received %d restricted actions before delete", len(beforeDelete))
	}
	if _, _, err := st.DeleteIssue(ctx, memberID, workspaceID, issue.ID, "test"); err != nil {
		t.Fatal(err)
	}
	afterDelete, to, err := st.ActionPageSince(ctx, workspaceID, excludedID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterDelete) != 0 {
		t.Fatalf("excluded user received restricted history after delete: %+v", afterDelete)
	}
	if to != 4 {
		t.Fatalf("scan boundary=%d, want 4", to)
	}
}
