package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestProjectTemplatesListSaveAndRemove(t *testing.T) {
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

	workspaceID := NewID("ws_template_test")
	actorID := NewID("usr_template_test")
	projectID := NewID("project_template_test")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM project_templates WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, projectID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, actorID)
	})
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Template list')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Template admin')`, actorID, actorID+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'TPL','Template source')`, projectID, workspaceID); err != nil {
		t.Fatal(err)
	}

	// A site with no templates lists none rather than failing.
	if templates, err := st.ProjectTemplates(ctx, workspaceID); err != nil || len(templates) != 0 {
		t.Fatalf("templates before any are saved = %+v, %v", templates, err)
	}

	snapshot, err := st.SaveProjectTemplate(ctx, workspaceID, actorID, projectID, "SNAPSHOT", "Copied delivery", "Starts from a copy.", nil)
	if err != nil || snapshot.Key == "" || snapshot.Type != "SNAPSHOT" {
		t.Fatalf("snapshot template = %+v, %v", snapshot, err)
	}
	live, err := st.SaveProjectTemplate(ctx, workspaceID, actorID, projectID, "LIVE", "Live delivery", "", nil)
	if err != nil || live.ProjectID == "" {
		t.Fatalf("live template = %+v, %v", live, err)
	}

	// The newest template is listed first, and each carries its configuration.
	templates, err := st.ProjectTemplates(ctx, workspaceID)
	if err != nil || len(templates) != 2 {
		t.Fatalf("templates = %+v, %v", templates, err)
	}
	if templates[0].Name != "Live delivery" || templates[1].Name != "Copied delivery" {
		t.Fatalf("templates are not newest first: %q then %q", templates[0].Name, templates[1].Name)
	}
	if templates[1].Snapshot.ProjectTypeKey == "" || templates[1].Description != "Starts from a copy." {
		t.Fatalf("snapshot template = %+v", templates[1])
	}

	// A template belongs to its own site.
	if other, err := st.ProjectTemplates(ctx, "ws_default"); err != nil {
		t.Fatal(err)
	} else {
		for _, template := range other {
			if template.Key == snapshot.Key {
				t.Fatal("a template reached another site")
			}
		}
	}

	// Jira's limits, and one name per site.
	if _, err := st.SaveProjectTemplate(ctx, workspaceID, actorID, projectID, "SNAPSHOT", strings.Repeat("n", 51), "", nil); err == nil {
		t.Fatal("a name of 51 characters was accepted")
	}
	if _, err := st.SaveProjectTemplate(ctx, workspaceID, actorID, projectID, "SNAPSHOT", "Long description", strings.Repeat("d", 151), nil); err == nil {
		t.Fatal("a description of 151 characters was accepted")
	}
	if _, err := st.SaveProjectTemplate(ctx, workspaceID, actorID, projectID, "SNAPSHOT", "Copied delivery", "", nil); err == nil {
		t.Fatal("two templates of one site share a name")
	}
	if _, err := st.SaveProjectTemplate(ctx, workspaceID, actorID, projectID, "MIRROR", "Mirrored", "", nil); err == nil {
		t.Fatal("an unsupported template type was accepted")
	}

	// A renamed template keeps its key, and a removed one is gone.
	name, description := "Renamed delivery", "Renamed."
	if err := st.EditProjectTemplate(ctx, workspaceID, snapshot.Key, &name, &description, nil); err != nil {
		t.Fatal(err)
	}
	renamed, err := st.ProjectTemplate(ctx, workspaceID, snapshot.Key, "")
	if err != nil || renamed.Name != name || renamed.Description != description {
		t.Fatalf("renamed template = %+v, %v", renamed, err)
	}
	if err := st.RemoveProjectTemplate(ctx, workspaceID, snapshot.Key); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveProjectTemplate(ctx, workspaceID, snapshot.Key); err == nil {
		t.Fatal("a template was removed twice")
	}
	remaining, err := st.ProjectTemplates(ctx, workspaceID)
	if err != nil || len(remaining) != 1 || remaining[0].Key != live.Key {
		t.Fatalf("templates after removal = %+v, %v", remaining, err)
	}
}
