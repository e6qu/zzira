package commands_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestWikiDatabaseSchemaRowsViewsAndPermissions(t *testing.T) {
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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var memberID string
	if err := st.Pool.QueryRow(ctx, `SELECT u.id FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.workspace_id=$1 AND m.role='member' AND u.email='ana@zzira.dev' LIMIT 1`, workspaceID).Scan(&memberID); err != nil {
		t.Skip("no member fixture")
	}

	service := &commands.Service{Store: st}
	spaceKey := "DB" + time.Now().UTC().Format("150405000000")
	space, err := service.CreateWikiSpace(ctx, workspaceID, adminID, spaceKey, "Database test", "Typed data", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM wiki_spaces WHERE id::text=$1`, space.ID) })
	database, err := service.CreateWikiContent(ctx, workspaceID, adminID, models.WikiContent{Type: "database", SpaceID: space.ID, Title: "Release register"})
	if err != nil {
		t.Fatal(err)
	}
	privateDatabase, err := service.CreateWikiContent(ctx, workspaceID, adminID, models.WikiContent{Type: "database", SpaceID: space.ID, Title: "Private register", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.WikiDatabaseData(ctx, workspaceID, memberID, privateDatabase.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("private database visible to member: %v", err)
	}
	if _, err := service.SaveWikiDatabaseRow(ctx, workspaceID, adminID, database.ID, "", map[string]string{}); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("schema-less row error = %v", err)
	}

	if _, err := service.AddWikiDatabaseColumn(ctx, workspaceID, adminID, database.ID, models.WikiDatabaseColumn{Key: "phase", Name: "Phase", Type: "select"}); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("select without options error = %v", err)
	}
	phase, err := service.AddWikiDatabaseColumn(ctx, workspaceID, adminID, database.ID, models.WikiDatabaseColumn{Key: "phase", Name: "Phase", Type: "select", Options: []string{"Planned", "Released"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AddWikiDatabaseColumn(ctx, workspaceID, adminID, database.ID, models.WikiDatabaseColumn{Key: "score", Name: "Score", Type: "number"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveWikiDatabaseRow(ctx, workspaceID, adminID, database.ID, "", map[string]string{"phase": "Planned", "score": "many"}); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("invalid number error = %v", err)
	}
	first, err := service.SaveWikiDatabaseRow(ctx, workspaceID, adminID, database.ID, "", map[string]string{"phase": "Planned", "score": "2"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.SaveWikiDatabaseRow(ctx, workspaceID, adminID, database.ID, "", map[string]string{"phase": "Released", "score": "10"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveWikiDatabaseRow(ctx, workspaceID, adminID, database.ID, first.ID, map[string]string{"phase": "Released", "score": "3"}); err != nil {
		t.Fatal(err)
	}
	view, err := service.SaveWikiDatabaseView(ctx, workspaceID, adminID, database.ID, models.WikiDatabaseView{Name: "Released first", FilterKey: "phase", FilterValue: "Released", SortKey: "score", SortDirection: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := st.WikiDatabaseData(ctx, workspaceID, memberID, database.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Columns) != 2 || len(data.Rows) != 2 || len(data.Views) != 1 || data.Rows[0].Values["phase"] == "" {
		t.Fatalf("database data = %+v", data)
	}
	if err := service.DeleteWikiDatabaseColumn(ctx, workspaceID, adminID, database.ID, phase.ID); err != nil {
		t.Fatal(err)
	}
	data, err = st.WikiDatabaseData(ctx, workspaceID, adminID, database.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := data.Rows[0].Values["phase"]; exists || data.Views[0].FilterKey != "" || data.Views[0].FilterValue != "" {
		t.Fatalf("deleted column references remain: %+v", data)
	}
	if err := service.DeleteWikiDatabaseView(ctx, workspaceID, adminID, database.ID, view.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteWikiDatabaseRow(ctx, workspaceID, adminID, database.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	data, err = st.WikiDatabaseData(ctx, workspaceID, adminID, database.ID)
	if err != nil || len(data.Rows) != 1 || len(data.Views) != 0 {
		t.Fatalf("final database data = %+v, %v", data, err)
	}
}
