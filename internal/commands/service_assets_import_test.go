package commands_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestAnAssetInventoryIsImportedFromAFile(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
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
	service := &commands.Service{Store: st}
	projectKey := "I" + time.Now().UTC().Format("150405000")
	project, err := service.CreateProject(ctx, adminID, workspaceID, commands.CreateProjectInput{Key: projectKey, Name: "Asset import test", LeadAccountID: adminID, ProjectTypeKey: "service_desk", ProjectTemplateKey: "com.atlassian.servicedesk:simplified-it-service-management"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID) })
	var deskID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, project.ID).Scan(&deskID); err != nil {
		t.Fatal(err)
	}
	schema, err := service.CreateServiceAssetSchema(ctx, adminID, workspaceID, deskID, models.ServiceAssetSchema{
		Key: "service", Name: "Business service",
		Attributes: []models.ServiceAssetAttribute{
			{Key: "tier", Name: "Service tier", Type: "select", Required: true, Options: []string{"Tier 1", "Tier 2"}},
			{Key: "capacity", Name: "Capacity", Type: "number"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveServiceAssetObject(ctx, adminID, workspaceID, deskID, models.ServiceAssetObject{SchemaID: schema.ID, Key: "SVC-1", Label: "Checkout", X: 700, Y: 300, Values: map[string]string{"tier": "Tier 2", "capacity": "100"}}); err != nil {
		t.Fatal(err)
	}

	// The heading names one attribute the way the schema does and the other by
	// the key it is stored under, and the first row is the object that is
	// already there.
	imported, err := service.ImportServiceAssetObjects(ctx, adminID, workspaceID, deskID, schema.ID, strings.Join([]string{
		"Key,Label,Service tier,capacity",
		"svc-1,Checkout,Tier 1,250",
		"SVC-2,Search,Tier 2,",
		"SVC-3,Notifications,Tier 2,40",
		"",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if imported.Created != 2 || imported.Updated != 1 {
		t.Fatalf("import wrote %d new and %d updated objects", imported.Created, imported.Updated)
	}
	inventory, err := st.ServiceAssetInventory(ctx, workspaceID, adminID, deskID)
	if err != nil {
		t.Fatal(err)
	}
	objects := map[string]models.ServiceAssetObject{}
	for _, object := range inventory.Objects {
		objects[object.Key] = object
	}
	if len(objects) != 3 {
		t.Fatalf("inventory holds %d objects", len(objects))
	}
	if updated := objects["SVC-1"]; updated.Values["tier"] != "Tier 1" || updated.Values["capacity"] != "250" || updated.X != 700 || updated.Y != 300 {
		t.Fatalf("the imported row left SVC-1 as %+v", updated)
	}
	if created := objects["SVC-2"]; created.Label != "Search" || created.Values["tier"] != "Tier 2" || created.Values["capacity"] != "" {
		t.Fatalf("SVC-2 = %+v", created)
	}
	if objects["SVC-2"].X == objects["SVC-3"].X && objects["SVC-2"].Y == objects["SVC-3"].Y {
		t.Fatalf("two new objects were placed on top of each other: %+v", objects)
	}

	// A file is read whole before anything is written, so one bad row leaves
	// the inventory as it was.
	for _, refusal := range []struct{ name, file, want string }{
		{"a column that is not an attribute", "Key,Label,Owner\nSVC-9,Ledger,Ana", "not Key, Label, X, Y, or an attribute"},
		{"no label column", "Key,Service tier\nSVC-9,Tier 1", "needs a Key column and a Label column"},
		{"a value the attribute refuses", "Key,Label,Service tier\nSVC-9,Ledger,Tier 9", "row 2: choose a supported value"},
		{"the same key twice", "Key,Label,Service tier\nSVC-9,Ledger,Tier 1\nsvc-9,Ledger again,Tier 1", "row 3: SVC-9 is already row 2"},
		{"a heading row alone", "Key,Label,Service tier", "at least one object"},
	} {
		if _, err := service.ImportServiceAssetObjects(ctx, adminID, workspaceID, deskID, schema.ID, refusal.file); err == nil || !strings.Contains(err.Error(), refusal.want) {
			t.Fatalf("%s: error = %v", refusal.name, err)
		}
	}
	after, err := st.ServiceAssetInventory(ctx, workspaceID, adminID, deskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Objects) != 3 {
		t.Fatalf("a refused import left %d objects", len(after.Objects))
	}
}
