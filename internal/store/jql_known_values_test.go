package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/jql"
)

func TestJQLKnownValuesFollowLiveWorkspaceCatalogues(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	ws, other := NewID("ws"), NewID("ws")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{ws, other} {
		exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Live JQL catalogue')`, id)
	}
	t.Cleanup(func() { exec(`DELETE FROM workspaces WHERE id IN ($1,$2)`, ws, other) })
	for field, table := range map[string]string{"priority": "priorities", "resolution": "resolutions", "issuetype": "issue_types"} {
		var id, name, jiraID string
		if err := st.Pool.QueryRow(ctx, `SELECT id,name,jira_id::text FROM `+table+` WHERE workspace_id IS NULL ORDER BY jira_id LIMIT 1`).Scan(&id, &name, &jiraID); err != nil {
			t.Fatal(err)
		}
		renamed := "Renamed " + field + " " + ws
		exec(`INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,name) VALUES($1,$2,$3,$4)`, ws, field, id, renamed)
		values, err := st.jqlKnownValues(ctx, ws)
		if err != nil {
			t.Fatal(err)
		}
		if !values[field][strings.ToLower(renamed)] || !values[field][jiraID] {
			t.Fatalf("renamed live %s lost its name or id: %+v", field, values[field])
		}
		exec(`UPDATE issue_metadata_overrides SET deleted=TRUE WHERE workspace_id=$1 AND entity_type=$2 AND entity_id=$3`, ws, field, id)
		values, err = st.jqlKnownValues(ctx, ws)
		if err != nil || values[field][strings.ToLower(renamed)] || values[field][jiraID] {
			t.Fatalf("deleted %s remains valid: %+v %v", field, values[field], err)
		}
		otherValues, err := st.jqlKnownValues(ctx, other)
		if err != nil || !otherValues[field][strings.ToLower(name)] || !otherValues[field][jiraID] {
			t.Fatalf("deletion escaped its workspace: %+v %v", otherValues[field], err)
		}
		resolver, err := st.JQLResolver(ctx, ws)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{renamed, jiraID} {
			query, err := jql.Parse(fmt.Sprintf(`%s = "%s"`, field, value))
			if err != nil {
				t.Fatal(err)
			}
			if compiled := jql.Compile(query, "", resolver); compiled.Err == nil {
				t.Fatalf("deleted %s value compiled: %s", field, value)
			}
			if compiled := jql.CompileLenientAt(query, "", resolver, 1); compiled.Err != nil || len(compiled.Warnings) != 1 {
				t.Fatalf("deleted %s value warning: %+v", field, compiled)
			}
		}
	}
	// Deleting every priority must not silently turn validation off.
	exec(`INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,deleted)
       SELECT $1,'priority',id,TRUE FROM priorities WHERE workspace_id IS NULL OR workspace_id=$1
       ON CONFLICT(workspace_id,entity_type,entity_id) DO UPDATE SET deleted=TRUE`, ws)
	resolver, err := st.JQLResolver(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if values, exists := resolver.KnownValues["priority"]; !exists || len(values) != 0 {
		t.Fatalf("empty priority catalogue lost its contract: %+v", resolver.KnownValues)
	}
	query, err := jql.Parse(`priority = Anything`)
	if err != nil {
		t.Fatal(err)
	}
	if compiled := jql.Compile(query, "", resolver); compiled.Err == nil {
		t.Fatal("empty priority catalogue accepted an unknown value")
	}
	query, err = jql.Parse(`resolution = Unresolved`)
	if err != nil {
		t.Fatal(err)
	}
	if compiled := jql.Compile(query, "", resolver); compiled.Err != nil {
		t.Fatalf("Unresolved was treated as a deleted resolution: %v", compiled.Err)
	}
}
