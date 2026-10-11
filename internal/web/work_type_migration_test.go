package web

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestWorkTypeDeletionRecordsEveryMigrationAndRollsBackFailedActions(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, q, ws); err != nil {
				t.Error(err)
			}
		}
	})
	source, err := f.store.CreateIssueType(ctx, ws, "Migration source", "", "standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	target, err := f.store.CreateIssueType(ctx, ws, "Migration replacement", "", "standard", nil)
	if err != nil {
		t.Fatal(err)
	}
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "MIG", Name: "Migration", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := f.store.CreateIssueTypeScheme(ctx, ws, "Migration types", "", source.ID, []string{source.ID, target.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AssignIssueTypeScheme(ctx, ws, scheme, project.ID); err != nil {
		t.Fatal(err)
	}
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Original work", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", source.ID, "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Cross the 100-row batch boundary without generating unrelated actions.
	if _, err := f.store.Pool.Exec(ctx, `INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,updated_seq)
	 SELECT $1||'-batch-'||lpad(n::text,3,'0'),$2,$3,'MIG-'||(n+1)::text,'Migrated work '||n,'st_todo',$4,$5 FROM generate_series(1,100) n`, issue.ID, ws, project.ID, source.ID, issue.UpdatedSeq); err != nil {
		t.Fatal(err)
	}
	var checkpoint int64
	if err := f.store.Pool.QueryRow(ctx, `SELECT seq FROM workspaces WHERE id=$1`, ws).Scan(&checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteIssueType(ctx, ws, f.otherID, source.ID, target.ID); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("non-administrator deletion: %v", err)
	}
	// Reject the last action, after the first batch has already been written.
	// The constraint is unique to this fixture and affects no other workspace.
	constraint := pgx.Identifier{"migration_action_" + strings.TrimPrefix(ws, "ws_")}.Sanitize()
	lastID := issue.ID + "-batch-100"
	literal := "'" + strings.ReplaceAll(lastID, "'", "''") + "'"
	if _, err := f.store.Pool.Exec(ctx, `ALTER TABLE actions ADD CONSTRAINT `+constraint+` CHECK (entity_id <> `+literal+`) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	dropConstraint := func() {
		t.Helper()
		if _, err := f.store.Pool.Exec(ctx, `ALTER TABLE actions DROP CONSTRAINT IF EXISTS `+constraint); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(dropConstraint)
	if err := f.store.DeleteIssueType(ctx, ws, f.ownerID, source.ID, target.ID); err == nil {
		t.Fatal("action failure did not refuse deletion")
	}
	var unchanged int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE workspace_id=$1 AND issuetype_id=$2 AND updated_seq=$3`, ws, source.ID, issue.UpdatedSeq).Scan(&unchanged); err != nil || unchanged != 101 {
		t.Fatalf("partial migration survived rollback: %d %v", unchanged, err)
	}
	var afterFailure int64
	if err := f.store.Pool.QueryRow(ctx, `SELECT seq FROM workspaces WHERE id=$1`, ws).Scan(&afterFailure); err != nil || afterFailure != checkpoint {
		t.Fatalf("failed migration changed checkpoint: %d %v", afterFailure, err)
	}
	if _, err := f.store.IssueTypeInWorkspace(ctx, ws, source.ID); err != nil {
		t.Fatalf("failed migration deleted source metadata: %v", err)
	}
	if actions, err := f.store.ActionsSince(ctx, ws, f.ownerID, checkpoint, 200); err != nil || len(actions) != 0 {
		t.Fatalf("failed migration published actions: %d %v", len(actions), err)
	}
	dropConstraint()
	if err := f.store.DeleteIssueType(ctx, ws, f.ownerID, source.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	actions, err := f.store.ActionsSince(ctx, ws, f.ownerID, checkpoint, 200)
	if err != nil || len(actions) != 101 {
		t.Fatalf("migration sync actions: %d %v", len(actions), err)
	}
	seen := map[string]bool{}
	for _, action := range actions {
		var payload models.IssueUpdatePayload
		if err := json.Unmarshal(action.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		change := payload.Diff["issuetype"]
		if seen[action.EntityID] || action.ActorID != f.ownerID || action.EntityType != models.EntityIssue || action.Op != models.OpUpsert || payload.Issue.ID != action.EntityID || payload.Issue.IssueType.ID != target.ID || change.From != strconv.FormatInt(source.JiraID, 10) || change.To != strconv.FormatInt(target.JiraID, 10) || change.FromString != source.Name || change.ToString != target.Name {
			t.Fatalf("incorrect migration action: %+v %+v", action, payload)
		}
		seen[action.EntityID] = true
		current, err := f.store.IssueByIDOrKey(ctx, ws, action.EntityID)
		if err != nil || current.UpdatedSeq != action.Seq || current.IssueType.ID != target.ID {
			t.Fatalf("snapshot/checkpoint mismatch: %+v %v", current, err)
		}
	}
	history, err := f.store.IssueChangelog(ctx, ws, issue.ID)
	if err != nil || len(history) != 1 || history[0].AuthorID != f.ownerID || len(history[0].Items) != 1 || history[0].Items[0].Field != "issuetype" {
		t.Fatalf("migration history: %+v %v", history, err)
	}
}
