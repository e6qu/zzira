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

func TestWikiWhiteboardObjectsConnectorsAndPermissions(t *testing.T) {
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
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email='ana@zzira.dev'`).Scan(&memberID); err != nil {
		t.Skip("no member fixture")
	}
	service := &commands.Service{Store: st}
	space, err := service.CreateWikiSpace(ctx, workspaceID, adminID, "WB"+time.Now().UTC().Format("150405000000"), "Canvas test", "Visual map", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM wiki_spaces WHERE id::text=$1`, space.ID) })
	whiteboard, err := service.CreateWikiContent(ctx, workspaceID, adminID, models.WikiContent{Type: "whiteboard", SpaceID: space.ID, Title: "Delivery map"})
	if err != nil {
		t.Fatal(err)
	}
	privateBoard, err := service.CreateWikiContent(ctx, workspaceID, adminID, models.WikiContent{Type: "whiteboard", SpaceID: space.ID, Title: "Private map", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.WikiWhiteboardData(ctx, workspaceID, memberID, privateBoard.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("private whiteboard visible to member: %v", err)
	}
	if _, err := service.SaveWikiWhiteboardObject(ctx, workspaceID, adminID, whiteboard.ID, models.WikiWhiteboardObject{Type: "sticky", Title: "Bad", Color: "yellow", X: -1, Y: 0, Width: 200, Height: 100}); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("invalid position error = %v", err)
	}
	from, err := service.SaveWikiWhiteboardObject(ctx, workspaceID, adminID, whiteboard.ID, models.WikiWhiteboardObject{Type: "sticky", Title: "Build", Body: "Package release", Color: "yellow", X: 80, Y: 100, Width: 220, Height: 120})
	if err != nil {
		t.Fatal(err)
	}
	to, err := service.SaveWikiWhiteboardObject(ctx, workspaceID, adminID, whiteboard.ID, models.WikiWhiteboardObject{Type: "shape", Title: "Deploy", Color: "green", X: 500, Y: 100, Width: 220, Height: 120})
	if err != nil {
		t.Fatal(err)
	}
	from.X, from.Y, from.Color = 120, 140, "blue"
	if _, err := service.SaveWikiWhiteboardObject(ctx, workspaceID, adminID, whiteboard.ID, *from); err != nil {
		t.Fatal(err)
	}
	connector, err := service.SaveWikiWhiteboardConnector(ctx, workspaceID, adminID, whiteboard.ID, models.WikiWhiteboardConnector{FromObjectID: from.ID, ToObjectID: to.ID, Label: "ships", Style: "dashed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveWikiWhiteboardConnector(ctx, workspaceID, adminID, privateBoard.ID, models.WikiWhiteboardConnector{FromObjectID: from.ID, ToObjectID: to.ID, Style: "solid"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-whiteboard connector error = %v", err)
	}
	data, err := st.WikiWhiteboardData(ctx, workspaceID, memberID, whiteboard.ID)
	if err != nil || len(data.Objects) != 2 || len(data.Connectors) != 1 || data.Connectors[0].FromX != 230 || data.Connectors[0].ToX != 610 {
		t.Fatalf("whiteboard data = %+v, %v", data, err)
	}
	if err := service.DeleteWikiWhiteboardConnector(ctx, workspaceID, adminID, whiteboard.ID, connector.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteWikiWhiteboardObject(ctx, workspaceID, adminID, whiteboard.ID, to.ID); err != nil {
		t.Fatal(err)
	}
	data, err = st.WikiWhiteboardData(ctx, workspaceID, adminID, whiteboard.ID)
	if err != nil || len(data.Objects) != 1 || len(data.Connectors) != 0 {
		t.Fatalf("final whiteboard data = %+v, %v", data, err)
	}
}
