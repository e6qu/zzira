package api3

import (
	"slices"
	"strings"
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

func TestOwnedJQLFunctionsAreAdvertised(t *testing.T) {
	values := make([]string, 0, len(jqlFunctions))
	for _, function := range jqlFunctions {
		values = append(values, function.Value)
	}
	for _, expected := range []string{"currentLogin()", "lastLogin()", "approved()", "approver()", "myApproval()", "myPendingApproval()", "myPending()", "pending()", "pendingApprovalBy()", "pendingBy()", "breached()", "completed()", "everBreached()", "paused()", "remaining()", "running()", "withinCalendarHours()"} {
		if !slices.Contains(values, expected) {
			t.Fatalf("JQL function catalog omits %s", expected)
		}
	}
}

// TestJQLFunctionCatalogMatchesCompiler keeps the advertised catalog and the
// names apps may not reuse in step with the functions the compiler resolves.
func TestJQLFunctionCatalogMatchesCompiler(t *testing.T) {
	for _, function := range jqlFunctions {
		if name := strings.TrimSuffix(function.Value, "()"); !jql.IsBuiltInFunction(name) {
			t.Errorf("%s is advertised but not reserved as built in", function.Value)
		}
	}
	advertised := map[string]bool{}
	for _, function := range jqlFunctions {
		advertised[strings.ToLower(strings.TrimSuffix(function.Value, "()"))] = true
	}
	for _, name := range jql.BuiltInFunctionNames() {
		if !advertised[name] {
			t.Errorf("%s() is built in but missing from the catalog", name)
		}
	}
}

func TestPersonalDataAccountNamesOnePerson(t *testing.T) {
	users := []*models.User{
		{ID: "usr_ana", Email: "ana@example.test", DisplayName: "Ana Soursop"},
		{ID: "usr_bo", Email: "bo@example.test", DisplayName: "Bo"},
		{ID: "usr_bo2", Email: "bo2@example.test", DisplayName: "Bo"},
	}
	for value, want := range map[string]string{"usr_ana": "usr_ana", "ANA@example.test": "usr_ana", "ana soursop": "usr_ana"} {
		if got, found := personalDataAccount(users, value); !found || got != want {
			t.Fatalf("%q = %q (%v), want %q", value, got, found, want)
		}
	}
	// A shared display name and an unknown name name no one.
	for _, value := range []string{"Bo", "mia"} {
		if got, found := personalDataAccount(users, value); found {
			t.Fatalf("%q resolved to %q", value, got)
		}
	}
}

func TestJQLSuggestionEscapesBeforeHighlighting(t *testing.T) {
	if got := highlightJQLSuggestion(`<script>Ready</script>`, "ready"); got != `&lt;script&gt;<b>Ready</b>&lt;/script&gt;` {
		t.Fatalf("highlight = %q", got)
	}
}
