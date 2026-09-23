package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

// A site names a work type, a priority, a resolution or a status in each
// language its people read, and each person is answered in theirs -- including
// when they ask for a regional language the site was translated into without
// the region, or the other way round.
func TestIssueMetadataTranslationsAnswerTheReadersLanguage(t *testing.T) {
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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	types, err := st.IssueTypesForWorkspace(ctx, workspaceID)
	if err != nil || len(types) == 0 {
		t.Fatalf("work types: %v (%d)", err, len(types))
	}
	workType := types[0]
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM issue_metadata_translations WHERE workspace_id=$1 AND entity_id=$2`, workspaceID, workType.ID)
	})

	// Something that is not on the site cannot be named in another language.
	if err := st.SaveIssueMetadataTranslation(ctx, workspaceID, actorID, MetadataTranslation{
		EntityType: "issuetype", EntityID: "it_nothing", Locale: "es", Name: "Nada",
	}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("translating a work type that is not there: %v", err)
	}
	// Neither can something the site does not translate at all.
	if err := st.SaveIssueMetadataTranslation(ctx, workspaceID, actorID, MetadataTranslation{
		EntityType: "sprint", EntityID: workType.ID, Locale: "es", Name: "Carrera",
	}); err == nil {
		t.Fatal("a sprint is not a thing a site translates, and it was accepted")
	}
	// A language tag that is not one is refused before anything is written.
	if err := st.SaveIssueMetadataTranslation(ctx, workspaceID, actorID, MetadataTranslation{
		EntityType: "issuetype", EntityID: workType.ID, Locale: "Espanol!", Name: "Tarea",
	}); err == nil || !strings.Contains(err.Error(), "language tag") {
		t.Fatalf("an unusable language tag: %v", err)
	}

	for _, translation := range []MetadataTranslation{
		{EntityType: "issuetype", EntityID: workType.ID, Locale: "es", Name: "Tarea", Description: "Un trabajo pequeño."},
		{EntityType: "issuetype", EntityID: workType.ID, Locale: "pt-BR", Name: "Tarefa"},
	} {
		if err := st.SaveIssueMetadataTranslation(ctx, workspaceID, actorID, translation); err != nil {
			t.Fatalf("save the %s name: %v", translation.Locale, err)
		}
	}

	held, err := st.IssueMetadataTranslations(ctx, workspaceID, "issuetype")
	if err != nil {
		t.Fatalf("read the translations: %v", err)
	}
	if len(held[workType.ID]) != 2 {
		t.Fatalf("%s has %d translations, want 2", workType.Name, len(held[workType.ID]))
	}

	// A reader asking for a language reads it; asking for a region the site
	// has no words for falls back to the language, and the other way round.
	for _, want := range []struct{ locale, name string }{
		{"es", "Tarea"}, {"es-MX", "Tarea"}, {"pt-br", "Tarefa"}, {"pt", "Tarefa"},
	} {
		names, err := st.IssueMetadataNamesInLocale(ctx, workspaceID, want.locale)
		if err != nil {
			t.Fatalf("read %s: %v", want.locale, err)
		}
		if got := names["issuetype:"+workType.ID].Name; got != want.name {
			t.Fatalf("a reader of %s calls %s %q, want %q", want.locale, workType.Name, got, want.name)
		}
	}
	// A language the site has not been translated into reads the site's own
	// names, which is what the rest of the API speaks.
	names, err := st.IssueMetadataNamesInLocale(ctx, workspaceID, "fi")
	if err != nil {
		t.Fatalf("read fi: %v", err)
	}
	if _, translated := names["issuetype:"+workType.ID]; translated {
		t.Fatal("the site answered Finnish with a translation it does not have")
	}

	if err := st.DeleteIssueMetadataTranslation(ctx, workspaceID, actorID, "issuetype", workType.ID, "es"); err != nil {
		t.Fatalf("remove the Spanish name: %v", err)
	}
	if err := st.DeleteIssueMetadataTranslation(ctx, workspaceID, actorID, "issuetype", workType.ID, "es"); err == nil {
		t.Fatal("removing a translation twice was accepted")
	}
	names, err = st.IssueMetadataNamesInLocale(ctx, workspaceID, "es")
	if err != nil {
		t.Fatalf("read es: %v", err)
	}
	if _, translated := names["issuetype:"+workType.ID]; translated {
		t.Fatal("the Spanish name survived being removed")
	}
}
