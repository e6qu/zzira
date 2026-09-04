package web

import (
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestSelectCurrentProject(t *testing.T) {
	projects := []projectNavigationItem{
		{Project: &models.Project{ID: "prj_alpha", Key: "ALPHA", Name: "Alpha"}},
		{Project: &models.Project{ID: "prj_beta", Key: "BETA", Name: "Beta"}},
	}

	tests := []struct {
		name      string
		selection string
		wantKey   string
	}{
		{name: "defaults to first", wantKey: "ALPHA"},
		{name: "matches ID", selection: "prj_beta", wantKey: "BETA"},
		{name: "matches key without case sensitivity", selection: "beta", wantKey: "BETA"},
		{name: "invalid preference falls back", selection: "missing", wantKey: "ALPHA"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selected := selectCurrentProject(projects, test.selection)
			if selected == nil || selected.Project.Key != test.wantKey {
				t.Fatalf("selected project = %#v, want key %q", selected, test.wantKey)
			}
		})
	}
}

func TestSelectCurrentProjectHandlesEmptyWorkspace(t *testing.T) {
	if selected := selectCurrentProject(nil, "ZZ"); selected != nil {
		t.Fatalf("selected project = %#v, want nil", selected)
	}
}
