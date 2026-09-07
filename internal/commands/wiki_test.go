package commands

import (
	"reflect"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestNormalizeWikiLabels(t *testing.T) {
	got, err := normalizeWikiLabels([]models.WikiLabel{{Name: " Release-Ready "}, {Name: "release-ready"}, {Prefix: "TEAM", Name: "Engineering_Content"}, {Name: " "}})
	if err != nil {
		t.Fatal(err)
	}
	want := []models.WikiLabel{{Name: "release-ready", Prefix: "global"}, {Name: "engineering_content", Prefix: "team"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeWikiLabels() = %#v, want %#v", got, want)
	}
	for _, labels := range [][]models.WikiLabel{
		{{Name: "two words"}},
		{{Prefix: "system", Name: "protected"}},
		{{Name: ""}},
	} {
		if _, err := normalizeWikiLabels(labels); err == nil {
			t.Fatalf("normalizeWikiLabels(%#v) succeeded", labels)
		}
	}
}
