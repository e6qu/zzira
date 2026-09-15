package store

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestDashboardWallboardSlideshowUsesViewableDashboards(t *testing.T) {
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
	if err = Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, ownerID, viewerID := NewID("ws"), NewID("usr"), NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Wallboard test')`, workspaceID)
	for _, id := range []string{ownerID, viewerID} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, id, id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, id)
	}
	t.Cleanup(func() {
		exec(`DELETE FROM dashboard_wallboard_slideshows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM dashboards WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{ownerID, viewerID} {
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	show, err := st.DashboardWallboardSlideshow(ctx, workspaceID)
	if err != nil || len(show.DashboardIDs) != 0 || show.IntervalSeconds != 30 || show.RandomOrder {
		t.Fatalf("default slide show = %+v, %v", show, err)
	}
	shared, err := st.SaveDashboard(ctx, workspaceID, ownerID, "", DashboardDetails{Name: "Delivery", SharePermissions: []models.DashboardShare{{Type: "loggedin"}}})
	if err != nil {
		t.Fatal(err)
	}
	private, err := st.SaveDashboard(ctx, workspaceID, ownerID, "", DashboardDetails{Name: "Owner notes"})
	if err != nil {
		t.Fatal(err)
	}
	for name, invalid := range map[string]WallboardSlideshow{
		"short interval":    {DashboardIDs: []string{shared.ID}, IntervalSeconds: 4},
		"long interval":     {DashboardIDs: []string{shared.ID}, IntervalSeconds: 3601},
		"no dashboards":     {IntervalSeconds: 30},
		"unviewable":        {DashboardIDs: []string{shared.ID, private.ID}, IntervalSeconds: 30},
		"missing dashboard": {DashboardIDs: []string{"999999999"}, IntervalSeconds: 30},
	} {
		actor := ownerID
		if name == "unviewable" {
			actor = viewerID
		}
		if err := st.SaveDashboardWallboardSlideshow(ctx, workspaceID, actor, invalid); !errors.Is(err, ErrDashboardValidation) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if err := st.SaveDashboardWallboardSlideshow(ctx, workspaceID, ownerID, WallboardSlideshow{DashboardIDs: []string{private.ID, shared.ID, private.ID}, IntervalSeconds: 45, RandomOrder: true}); err != nil {
		t.Fatal(err)
	}
	show, err = st.DashboardWallboardSlideshow(ctx, workspaceID)
	if err != nil || !reflect.DeepEqual(show, WallboardSlideshow{DashboardIDs: []string{private.ID, shared.ID}, IntervalSeconds: 45, RandomOrder: true}) {
		t.Fatalf("saved slide show = %+v, %v", show, err)
	}
	// There is one slide show for the site; the next save replaces it.
	if err := st.SaveDashboardWallboardSlideshow(ctx, workspaceID, viewerID, WallboardSlideshow{DashboardIDs: []string{shared.ID}, IntervalSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if show, err = st.DashboardWallboardSlideshow(ctx, workspaceID); err != nil || !reflect.DeepEqual(show.DashboardIDs, []string{shared.ID}) || show.IntervalSeconds != 60 || show.RandomOrder {
		t.Fatalf("replaced slide show = %+v, %v", show, err)
	}
}
