package api3

import (
	"encoding/json"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestSearchProjectionFieldsAliasesMetadataAndRendering(t *testing.T) {
	definitions := searchFieldDefinitions([]*models.CustomField{{
		ID: "customfield_20000", Name: "Risk score", Type: models.CustomFieldNumber,
		AppKey: "example.connect", AppModuleKey: "risk-score",
	}})
	bean := map[string]any{
		"id": "10001", "key": "OPS-1", "self": "https://zzira.test/rest/api/3/issue/10001",
		"fields": map[string]any{
			"summary":           "Risk <review>",
			"description":       json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Review"}]}]}`),
			"customfield_20000": json.RawMessage(`7`),
		},
	}
	remapped := remapSearchIssueFields(bean, definitions, true)
	requested := normalizeSearchFields([]string{"example.connect__risk-score", "summary", "-description"}, definitions, true)
	projected := projectSearchIssue(remapped, requested, false)
	fields := projected["fields"].(map[string]any)
	if len(fields) != 2 || fields["example.connect__risk-score"] == nil || fields["summary"] != "Risk <review>" {
		t.Fatalf("projected fields = %#v", fields)
	}
	names, schemas := searchFieldMetadata(requested, false, true, definitions)
	if names["example.connect__risk-score"] != "Risk score" || schemas["example.connect__risk-score"].(map[string]any)["customId"] != int64(20000) {
		t.Fatalf("metadata = %#v %#v", names, schemas)
	}
	rendered := renderedSearchFields(fields)
	if rendered["summary"] != "Risk &lt;review&gt;" || rendered["example.connect__risk-score"] != nil {
		t.Fatalf("rendered fields = %#v", rendered)
	}
	idsOnly := projectSearchIssue(remapped, nil, false)
	if len(idsOnly) != 1 || idsOnly["id"] != "10001" {
		t.Fatalf("enhanced default = %#v", idsOnly)
	}
}

func TestSearchOptionValidation(t *testing.T) {
	options := searchOptions{Fields: []string{"summary,description"}, Expand: []string{"schema,names", "schema"}, Properties: []string{"one,two", "one"}, Validate: "warn"}
	if err := validateSearchOptions(&options); err != nil {
		t.Fatal(err.message)
	}
	if len(options.Fields) != 2 || len(options.Expand) != 2 || len(options.Properties) != 2 {
		t.Fatalf("normalized options = %#v", options)
	}
	for _, bad := range []searchOptions{
		{Expand: []string{"changelog"}},
		{Properties: []string{"1", "2", "3", "4", "5", "6"}},
		{Validate: "maybe"},
	} {
		if err := validateSearchOptions(&bad); err == nil {
			t.Fatalf("accepted invalid options %#v", bad)
		}
	}
}
