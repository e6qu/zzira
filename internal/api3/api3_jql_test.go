package api3

import (
	"slices"
	"testing"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

func TestJQLParseStructurePreservesHistoryFunctionsAndOrder(t *testing.T) {
	parsed, err := jql.Parse(`status CHANGED FROM "To Do" AFTER startOfMonth(-1M) ORDER BY priority DESC, updated ASC`)
	if err != nil {
		t.Fatal(err)
	}
	structure := jqlQueryStructure(parsed)
	where := structure["where"].(map[string]any)
	if where["operator"] != "changed" || len(where["predicates"].([]map[string]any)) != 2 {
		t.Fatalf("where = %#v", where)
	}
	orders := structure["orderBy"].(map[string]any)["fields"].([]map[string]any)
	if len(orders) != 2 || orders[0]["direction"] != "desc" || orders[1]["direction"] != "asc" {
		t.Fatalf("orders = %#v", orders)
	}
}

func TestLoginDateFunctionsAreAdvertised(t *testing.T) {
	values := make([]string, 0, len(jqlFunctions))
	for _, function := range jqlFunctions {
		values = append(values, function.Value)
	}
	for _, expected := range []string{"currentLogin()", "lastLogin()"} {
		if !slices.Contains(values, expected) {
			t.Fatalf("JQL function catalog omits %s", expected)
		}
	}
}

func TestMigrateJQLPersonalDataOnlyChangesUserOperands(t *testing.T) {
	members := []*models.User{{ID: "usr_ana", Email: "ana@example.test", DisplayName: "Ana Soursop"}}
	query := `project = "Ana Soursop" AND assignee = "Ana Soursop" AND reporter != ana@example.test`
	got := migrateJQLPersonalData(query, members)
	want := `project = "Ana Soursop" AND assignee = usr_ana AND reporter != usr_ana`
	if got != want {
		t.Fatalf("migration = %q, want %q", got, want)
	}
}

func TestJQLSuggestionEscapesBeforeHighlighting(t *testing.T) {
	if got := highlightJQLSuggestion(`<script>Ready</script>`, "ready"); got != `&lt;script&gt;<b>Ready</b>&lt;/script&gt;` {
		t.Fatalf("highlight = %q", got)
	}
}
