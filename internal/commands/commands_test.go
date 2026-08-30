package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestCreateIssueToSyncPipeline is the V0 integration gate: it runs against a
// real Postgres and walks the entire spine — command → state + action in one
// txn → permission-filtered /sync range. Skipped unless TEST_DATABASE_URL is set
// (make test-integration).
func TestCreateIssueToSyncPipeline(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	svc := &Service{Store: st}
	issue, action, err := svc.CreateIssue(ctx, CreateIssueInput{
		ActorID:     "usr_test",
		WorkspaceID: "ws_default",
		ProjectKey:  "ZZ",
		Summary:     "pipeline test issue",
		Description: "desc body",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Station 2→1: state written, key allocated.
	if issue.Key == "" || issue.ID == "" {
		t.Fatalf("issue missing id/key: %+v", issue)
	}

	// Station 2→3: action appended with ordered seq and snapshot payload.
	if action.Op != models.OpUpsert || action.EntityType != models.EntityIssue || action.SchemaV != models.SchemaVersion {
		t.Fatalf("unexpected action: %+v", action)
	}
	var payload models.IssueUpsertPayload
	if err := json.Unmarshal(action.Payload, &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if payload.Issue.Key != issue.Key {
		t.Fatalf("payload issue key = %q, want %q", payload.Issue.Key, issue.Key)
	}

	// Station 3: the sync range contains exactly the new action.
	head, err := st.Head(ctx, "ws_default")
	if err != nil {
		t.Fatal(err)
	}
	if head < action.Seq {
		t.Fatalf("head %d < action seq %d", head, action.Seq)
	}
	actions, err := st.ActionsSince(ctx, "ws_default", "usr_test", action.Seq-1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Seq != action.Seq {
		t.Fatalf("ActionsSince returned %d actions", len(actions))
	}

	// Determinism: the same (workspace, since, head) returns the same range.
	again, err := st.ActionsSince(ctx, "ws_default", "usr_test", action.Seq-1, 100)
	if err != nil || len(again) != 1 || again[0].Seq != actions[0].Seq {
		t.Fatalf("sync range not deterministic")
	}

	// Station 1: issue readable by id and by key.
	byKey, err := st.IssueByIDOrKey(ctx, "ws_default", issue.Key)
	if err != nil || byKey.ID != issue.ID {
		t.Fatalf("IssueByIDOrKey by key failed")
	}
	byID, err := st.IssueByIDOrKey(ctx, "ws_default", issue.ID)
	if err != nil || byID.Key != issue.Key {
		t.Fatalf("IssueByIDOrKey by id failed")
	}
}

func TestUpdateTransitionCommentChangelogPipeline(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := &Service{Store: st}

	issue, _, err := svc.CreateIssue(ctx, CreateIssueInput{
		ActorID: "usr_test", WorkspaceID: "ws_default", ProjectKey: "ZZ",
		Summary: "V1 pipeline issue", Description: "original body",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Edit: diff must capture summary + description.
	newSummary := "V1 pipeline issue (edited)"
	_, action, err := svc.UpdateIssue(ctx, UpdateIssueInput{
		ActorID: "usr_test", WorkspaceID: "ws_default", IssueIDOrKey: issue.Key,
		Summary: &newSummary, Description: adf.ParagraphDoc("new body"),
	})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	var payload models.IssueUpdatePayload
	if err := json.Unmarshal(action.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload.Diff["summary"]; !ok {
		t.Fatalf("diff missing summary: %+v", payload.Diff)
	}
	if _, ok := payload.Diff["description"]; !ok {
		t.Fatalf("diff missing description: %+v", payload.Diff)
	}

	// Transition 21: To Do → In Progress (project pinned to the default
	// workflow so earlier live assigns of other workflows cannot skew this).
	if err := st.AssignWorkflowToProject(ctx, issue.ProjectID, "wf_default"); err != nil {
		t.Fatalf("assign workflow: %v", err)
	}
	_, tAction, err := svc.TransitionIssue(ctx, "usr_test", "ws_default", issue.Key, "21")
	if err != nil {
		t.Fatalf("TransitionIssue: %v", err)
	}
	var tp models.IssueUpdatePayload
	if err := json.Unmarshal(tAction.Payload, &tp); err != nil {
		t.Fatal(err)
	}
	stItem, ok := tp.Diff["status"]
	if !ok || stItem.ToString != "In Progress" {
		t.Fatalf("status diff wrong: %+v", tp.Diff)
	}
	// Illegal transition must fail.
	if _, _, err := svc.TransitionIssue(ctx, "usr_test", "ws_default", issue.Key, "21"); err == nil {
		t.Fatal("21 from In Progress must fail")
	}

	// Comments: create + author-only delete.
	comment, _, err := svc.AddComment(ctx, AddCommentInput{
		ActorID: "usr_test", WorkspaceID: "ws_default", IssueIDOrKey: issue.Key, PlainText: "first!",
	})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if adf.PlainText(comment.Body) != "first!" {
		t.Fatalf("comment body = %q", adf.PlainText(comment.Body))
	}
	if _, err := svc.DeleteComment(ctx, "usr_other", "ws_default", comment.ID); err == nil {
		t.Fatal("non-author delete must fail")
	}

	// Changelog: derived from the log, ordered, with the right fields.
	entries, err := st.IssueChangelog(ctx, "ws_default", issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("changelog entries = %d, want >= 2", len(entries))
	}
	last := entries[len(entries)-1]
	if len(last.Items) != 1 || last.Items[0].Field != "status" || last.Items[0].ToString != "In Progress" {
		t.Fatalf("last changelog entry wrong: %+v", last.Items)
	}
}

// Regression: applying a security level on a project WITHOUT a scheme used to
// nil-deref in the exclusion logic.
func TestSecurityLevelWithoutSchemeDoesNotPanic(t *testing.T) {
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

	excluded, err := authz.ExcludedMembersForLevel(ctx, st, "ws_default", "prj_default", "lvl_private")
	if err != nil {
		t.Fatalf("excluded members: %v", err)
	}
	// both seeded users are non-members of lvl_private in this DB state
	if len(excluded) == 0 {
		t.Log("no excluded members (level members may cover everyone)")
	}
}

func TestPlainTextToADF(t *testing.T) {
	raw := plainTextToADF("hello")
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["type"] != "doc" {
		t.Fatalf("adf type = %v", doc["type"])
	}
	if empty := plainTextToADF(""); len(empty) == 0 {
		t.Fatal("empty ADF should still be a doc")
	}
}

func TestJQLSearchPipeline(t *testing.T) {
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
	svc := &Service{Store: st}

	marker := fmt.Sprintf("jqlmarker%d", time.Now().UnixNano())
	issue, _, err := svc.CreateIssue(ctx, CreateIssueInput{
		ActorID: "usr_test", WorkspaceID: "ws_default", ProjectKey: "ZZ",
		Summary: "issue with " + marker,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	q, err := jql.Parse(`summary ~ "` + marker + `" ORDER BY key DESC`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	compiled := jql.CompileAt(q, "usr_test", jql.DefaultResolver(), 2)
	issues, total, err := st.Search(ctx, "ws_default", "usr_test", compiled, 10, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 1 || len(issues) != 1 || issues[0].Key != issue.Key {
		t.Fatalf("search total=%d issues=%v", total, issues)
	}

	// currentUser() resolves through the compiler.
	q, _ = jql.Parse("reporter = currentUser()")
	compiled = jql.CompileAt(q, "usr_test", jql.DefaultResolver(), 2)
	_, total, err = st.Search(ctx, "ws_default", "usr_test", compiled, 10, 0)
	if err != nil || total < 1 {
		t.Fatalf("currentUser search total=%d err=%v", total, err)
	}
}
