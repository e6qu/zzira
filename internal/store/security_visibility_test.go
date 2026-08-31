package store

import (
	"context"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

// TestSecurityVisibilityAcrossReadPaths walks every read path with a
// restricted issue: JQL search, project list, board, bootstrap snapshot.
// Demo is a level member; ana is not.
func TestSecurityVisibilityAcrossReadPaths(t *testing.T) {
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
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	var demoID, anaID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email='demo@zzira.dev'`).Scan(&demoID); err != nil {
		t.Skip("demo user not seeded")
	}
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email='ana@zzira.dev'`).Scan(&anaID); err != nil {
		t.Skip("ana user not seeded")
	}

	// find a security-restricted issue (V5 live run applied lvl_private to ZZ-9,
	// and the e2e suite creates more); skip gracefully when none exists
	var restrictedID, restrictedKey, restrictedLevel, projectID string
	err = st.Pool.QueryRow(ctx, `
		SELECT i.id, i.key, i.security_level_id, i.project_id
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		JOIN security_schemes ss ON ss.id = p.security_scheme_id
		, jsonb_array_elements(ss.levels) lvl
		WHERE i.security_level_id IS NOT NULL AND lvl->>'id' = i.security_level_id
		ORDER BY i.updated_seq DESC LIMIT 1`).Scan(&restrictedID, &restrictedKey, &restrictedLevel, &projectID)
	if err != nil {
		t.Skip("no security-restricted issue present; run the V5 e2e spec first")
	}

	visibleTo := func(userID string) map[string]bool {
		out := map[string]bool{}
		issues, _ := st.IssuesByProject(ctx, "ws_default", projectID, userID)
		for _, i := range issues {
			out[i.ID] = true
		}
		return out
	}

	// project navigator: ana must not see it, demo must
	if visibleTo(anaID)[restrictedID] {
		t.Fatal("ana sees the restricted issue in IssuesByProject")
	}
	if !visibleTo(demoID)[restrictedID] {
		t.Fatal("demo lost the restricted issue in IssuesByProject")
	}

	// JQL search: match the restricted key, per user
	q, err := jql.Parse(`key = "` + restrictedKey + `"`)
	if err != nil {
		t.Fatal(err)
	}
	compiled := jql.CompileAt(q, demoID, jql.DefaultResolver(), 2)
	demoIssues, demoTotal, err := st.Search(ctx, "ws_default", demoID, compiled, 10, 0)
	if err != nil || demoTotal != 1 || len(demoIssues) != 1 {
		t.Fatalf("demo search total=%d n=%d err=%v", demoTotal, len(demoIssues), err)
	}
	compiledAna := jql.CompileAt(q, anaID, jql.DefaultResolver(), 2)
	_, anaTotal, err := st.Search(ctx, "ws_default", anaID, compiledAna, 10, 0)
	if err != nil || anaTotal != 0 {
		t.Fatalf("ana search total=%d err=%v (must be 0)", anaTotal, err)
	}

	// board: restricted issue absent from ana's board rows
	boards, err := st.BoardsByWorkspace(ctx, "ws_default")
	if err != nil || len(boards) == 0 {
		t.Skip("no board present")
	}
	boardCols, err := st.BoardIssues(ctx, boards[0].ID, anaID)
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range boardCols {
		for _, i := range col {
			if i.ID == restrictedID {
				t.Fatal("ana's board contains the restricted issue")
			}
		}
	}

	// bootstrap snapshots are user-shaped
	anaSnap, err := st.BootstrapSnapshot(ctx, "ws_default", anaID)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range anaSnap.Issues {
		if i.ID == restrictedID {
			t.Fatal("ana's bootstrap snapshot contains the restricted issue")
		}
	}
	for _, c := range anaSnap.Comments {
		if c.IssueID == restrictedID {
			t.Fatal("ana's bootstrap snapshot contains comments on the restricted issue")
		}
	}
	for _, a := range anaSnap.Attachments {
		if a.IssueID == restrictedID {
			t.Fatal("ana's bootstrap snapshot contains attachments on the restricted issue")
		}
	}
	for _, w := range anaSnap.Worklogs {
		if w.IssueID == restrictedID {
			t.Fatal("ana's bootstrap snapshot contains worklogs on the restricted issue")
		}
	}
	demoSnap, err := st.BootstrapSnapshot(ctx, "ws_default", demoID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range demoSnap.Issues {
		if i.ID == restrictedID {
			found = true
		}
	}
	if !found {
		t.Fatal("demo's bootstrap snapshot lost the restricted issue")
	}

	// Dashboard aggregates must have the same visibility boundary as navigator
	// and search; otherwise counts and activity leak confidential issue data.
	stats, err := st.DashboardData(ctx, "ws_default", anaID)
	if err != nil {
		t.Fatal(err)
	}
	var visibleCount int
	err = st.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM issues i WHERE i.workspace_id=$1 AND `+VisibleIssuePredicate("i", "$2"), "ws_default", anaID).Scan(&visibleCount)
	if err != nil {
		t.Fatal(err)
	}
	var dashboardCount int
	for _, count := range stats.StatusCounts {
		dashboardCount += int(count.Count)
	}
	if dashboardCount != visibleCount {
		t.Fatalf("ana dashboard count=%d, want visible count %d", dashboardCount, visibleCount)
	}
	for _, activity := range stats.Recent {
		if activity.IssueKey == restrictedKey {
			t.Fatal("ana's dashboard activity contains the restricted issue")
		}
	}

	_ = models.SchemaVersion
}
