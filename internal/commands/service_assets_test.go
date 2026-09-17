package commands_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestServiceAssetInventoryRelationshipsAndRequestImpact(t *testing.T) {
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
	projectKey := "A" + time.Now().UTC().Format("150405000")
	project, err := service.CreateProject(ctx, adminID, workspaceID, commands.CreateProjectInput{Key: projectKey, Name: "Asset impact test", LeadAccountID: adminID, ProjectTypeKey: "service_desk", ProjectTemplateKey: "com.atlassian.servicedesk:simplified-it-service-management"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID) })
	var deskID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, project.ID).Scan(&deskID); err != nil {
		t.Fatal(err)
	}

	schema, err := service.CreateServiceAssetSchema(ctx, adminID, workspaceID, deskID, models.ServiceAssetSchema{
		Key: "service", Name: "Business service", Description: "Customer-facing services",
		Attributes: []models.ServiceAssetAttribute{
			{Key: "tier", Name: "Service tier", Type: "select", Required: true, Options: []string{"Tier 1", "Tier 2"}},
			{Key: "capacity", Name: "Capacity", Type: "number", Required: true},
			{Key: "active", Name: "Active", Type: "boolean"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if schema.Key != "SERVICE" {
		t.Fatalf("normalized schema key = %q", schema.Key)
	}
	if _, err := service.SaveServiceAssetObject(ctx, adminID, workspaceID, deskID, models.ServiceAssetObject{SchemaID: schema.ID, Key: "database", Label: "Checkout database", X: 700, Y: 300, Values: map[string]string{"tier": "Tier 1", "capacity": "many"}}); err == nil || !strings.Contains(err.Error(), "must be a number") {
		t.Fatalf("invalid typed object error = %v", err)
	}
	database, err := service.SaveServiceAssetObject(ctx, adminID, workspaceID, deskID, models.ServiceAssetObject{SchemaID: schema.ID, Key: "database", Label: "Checkout database", X: 700, Y: 300, Values: map[string]string{"tier": "Tier 1", "capacity": "1200", "active": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	storefront, err := service.SaveServiceAssetObject(ctx, adminID, workspaceID, deskID, models.ServiceAssetObject{SchemaID: schema.ID, Key: "storefront", Label: "Customer storefront", X: 300, Y: 300, Values: map[string]string{"tier": "Tier 1", "capacity": "5000"}})
	if err != nil {
		t.Fatal(err)
	}
	relation, err := service.CreateServiceAssetRelationship(ctx, adminID, workspaceID, deskID, models.ServiceAssetRelationship{Relationship: "depends on", From: models.ServiceAssetObject{ID: storefront.ID}, To: models.ServiceAssetObject{ID: database.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if relation.ID == "" {
		t.Fatal("relationship has no id")
	}
	inventory, err := st.ServiceAssetInventory(ctx, workspaceID, adminID, deskID)
	if err != nil || len(inventory.Schemas) != 1 || len(inventory.Objects) != 2 || len(inventory.Relationships) != 1 {
		t.Fatalf("inventory = %+v, %v", inventory, err)
	}
	if _, err := service.CreateServiceAssetRelationship(ctx, adminID, workspaceID, deskID, models.ServiceAssetRelationship{Relationship: "depends on", From: models.ServiceAssetObject{ID: database.ID}, To: models.ServiceAssetObject{ID: database.ID}}); err == nil {
		t.Fatal("self relationship accepted")
	}

	// A portal Assets field offers the desk's objects, labelled with their
	// schema, to anyone who can raise a request, and names one by id or key.
	assetChoices, err := st.ServicePortalPickerChoices(ctx, workspaceID, deskID, adminID, models.CustomFieldAsset, "")
	if err != nil || len(assetChoices) != 2 || assetChoices[0].Value != "Checkout database (Business service)" || assetChoices[1].Value != "Customer storefront (Business service)" {
		t.Fatalf("asset choices = %+v, %v", assetChoices, err)
	}
	if id, label, err := st.ServiceAssetObjectInProject(ctx, workspaceID, project.ID, "database", ""); err != nil || id != database.ID || label != "Checkout database" {
		t.Fatalf("asset by key = %q %q, %v", id, label, err)
	}
	if _, _, err := st.ServiceAssetObjectInProject(ctx, workspaceID, project.ID, "NOPE", ""); err == nil {
		t.Fatal("an Assets object outside the service project resolved")
	}

	// A second schema shows that a field scoped to one offers only its objects.
	laptops, err := service.CreateServiceAssetSchema(ctx, adminID, workspaceID, deskID, models.ServiceAssetSchema{Key: "LAPTOPS", Name: "Laptops",
		Attributes: []models.ServiceAssetAttribute{{Key: "model", Name: "Model", Type: "text", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	laptop, err := service.SaveServiceAssetObject(ctx, adminID, workspaceID, deskID, models.ServiceAssetObject{SchemaID: laptops.ID, Key: "laptop", Label: "Design laptop", X: 100, Y: 100, Values: map[string]string{"model": "M4"}})
	if err != nil {
		t.Fatal(err)
	}
	scopedChoices, err := st.ServicePortalPickerChoices(ctx, workspaceID, deskID, adminID, models.CustomFieldAsset, laptops.ID)
	if err != nil || len(scopedChoices) != 1 || scopedChoices[0].ID != laptop.ID || scopedChoices[0].Value != "Design laptop (Laptops)" {
		t.Fatalf("scoped asset choices = %+v, %v", scopedChoices, err)
	}
	if all, err := st.ServicePortalPickerChoices(ctx, workspaceID, deskID, adminID, models.CustomFieldAsset, ""); err != nil || len(all) != 3 {
		t.Fatalf("unscoped asset choices = %+v, %v", all, err)
	}
	if id, _, err := st.ServiceAssetObjectInProject(ctx, workspaceID, project.ID, "laptop", laptops.ID); err != nil || id != laptop.ID {
		t.Fatalf("asset by key within its schema = %q, %v", id, err)
	}
	if _, _, err := st.ServiceAssetObjectInProject(ctx, workspaceID, project.ID, "database", laptops.ID); err == nil {
		t.Fatal("an Assets object outside the field's schema resolved")
	}

	requestTypes, err := st.ServiceRequestTypes(ctx, workspaceID, deskID, "")
	if err != nil || len(requestTypes) == 0 {
		t.Fatalf("request types = %+v, %v", requestTypes, err)
	}
	request, err := service.CreateServiceRequest(ctx, commands.CreateServiceRequestInput{ActorID: adminID, WorkspaceID: workspaceID, ServiceDeskID: deskID, RequestTypeID: requestTypes[0].ID, Summary: "Checkout database unavailable", Description: "The storefront cannot complete orders."})
	if err != nil {
		t.Fatal(err)
	}
	// A request names an Assets object in a portal field, by key or id.
	assetFieldID := "customfield_" + time.Now().UTC().Format("150405000")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO custom_fields(id,name,type,description,workspace_id) VALUES($1,'Affected asset',$2,'',$3)`, assetFieldID, models.CustomFieldAsset, workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM custom_fields WHERE id=$1`, assetFieldID) })
	if _, err := service.CreateServiceRequest(ctx, commands.CreateServiceRequestInput{ActorID: adminID, WorkspaceID: workspaceID, ServiceDeskID: deskID, RequestTypeID: requestTypes[0].ID,
		Summary: "Laptop replacement", Fields: map[string]json.RawMessage{assetFieldID: json.RawMessage(`"NOPE"`)}}); err == nil {
		t.Fatal("a request named an Assets object that is not in the service project")
	}
	assetRequest, err := service.CreateServiceRequest(ctx, commands.CreateServiceRequestInput{ActorID: adminID, WorkspaceID: workspaceID, ServiceDeskID: deskID, RequestTypeID: requestTypes[0].ID,
		Summary: "Database upgrade", Fields: map[string]json.RawMessage{assetFieldID: json.RawMessage(`"database"`)}})
	if err != nil {
		t.Fatal(err)
	}
	if stored := string(assetRequest.Issue.Fields[assetFieldID]); stored != `"`+database.ID+`"` {
		t.Fatalf("stored asset field = %s, want the object id %s", stored, database.ID)
	}

	// Scoping the form's Assets field to the laptop schema narrows what a
	// request may name to that schema's objects.
	formFields, err := st.ServiceRequestTypeFields(ctx, workspaceID, deskID, requestTypes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	scopedForm := append([]models.ServiceRequestTypeField{}, formFields...)
	scopedForm = append(scopedForm, models.ServiceRequestTypeField{ID: assetFieldID, AssetSchemaID: laptops.ID})
	if err := service.SetServiceRequestTypeFields(ctx, adminID, workspaceID, deskID, requestTypes[0].ID, scopedForm); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateServiceRequest(ctx, commands.CreateServiceRequestInput{ActorID: adminID, WorkspaceID: workspaceID, ServiceDeskID: deskID, RequestTypeID: requestTypes[0].ID,
		Summary: "Database upgrade", Fields: map[string]json.RawMessage{assetFieldID: json.RawMessage(`"database"`)}}); err == nil {
		t.Fatal("a scoped Assets field accepted an object of another schema")
	}
	scopedRequest, err := service.CreateServiceRequest(ctx, commands.CreateServiceRequestInput{ActorID: adminID, WorkspaceID: workspaceID, ServiceDeskID: deskID, RequestTypeID: requestTypes[0].ID,
		Summary: "Laptop replacement", Fields: map[string]json.RawMessage{assetFieldID: json.RawMessage(`"laptop"`)}})
	if err != nil {
		t.Fatal(err)
	}
	if stored := string(scopedRequest.Issue.Fields[assetFieldID]); stored != `"`+laptop.ID+`"` {
		t.Fatalf("stored scoped asset field = %s, want %s", stored, laptop.ID)
	}
	if reread, err := st.ServiceRequestTypeFields(ctx, workspaceID, deskID, requestTypes[0].ID); err != nil || !slices.ContainsFunc(reread, func(field models.ServiceRequestTypeField) bool {
		return field.ID == assetFieldID && field.AssetSchemaID == laptops.ID
	}) {
		t.Fatalf("request type fields = %+v, %v", reread, err)
	}
	// A context configured for several objects lets the field hold a list.
	assetContexts, err := st.CustomFieldContexts(ctx, workspaceID, assetFieldID, nil)
	if err != nil || len(assetContexts) == 0 {
		t.Fatalf("asset field contexts = %+v, %v", assetContexts, err)
	}
	if err := st.SetCustomFieldContextAssetsMultiple(ctx, workspaceID, adminID, assetFieldID, assetContexts[0].ID, true); err != nil {
		t.Fatal(err)
	}
	severalForm, err := st.ServiceRequestTypeFields(ctx, workspaceID, deskID, requestTypes[0].ID)
	if err != nil || !slices.ContainsFunc(severalForm, func(field models.ServiceRequestTypeField) bool {
		return field.ID == assetFieldID && field.AssetsMultiple
	}) {
		t.Fatalf("the form did not carry the context's cardinality: %+v, %v", severalForm, err)
	}
	if _, err := service.CreateServiceRequest(ctx, commands.CreateServiceRequestInput{ActorID: adminID, WorkspaceID: workspaceID, ServiceDeskID: deskID, RequestTypeID: requestTypes[0].ID,
		Summary: "Two laptops", Fields: map[string]json.RawMessage{assetFieldID: json.RawMessage(`["laptop","database"]`)}}); err == nil {
		t.Fatal("a scoped Assets field accepted a list naming another schema's object")
	}
	severalRequest, err := service.CreateServiceRequest(ctx, commands.CreateServiceRequestInput{ActorID: adminID, WorkspaceID: workspaceID, ServiceDeskID: deskID, RequestTypeID: requestTypes[0].ID,
		Summary: "Laptop refresh", Fields: map[string]json.RawMessage{assetFieldID: json.RawMessage(`["laptop"]`)}})
	if err != nil {
		t.Fatal(err)
	}
	if stored := string(severalRequest.Issue.Fields[assetFieldID]); stored != `["`+laptop.ID+`"]` {
		t.Fatalf("stored Assets list = %s, want [%s]", stored, laptop.ID)
	}
	if _, err := service.CreateServiceRequest(ctx, commands.CreateServiceRequestInput{ActorID: adminID, WorkspaceID: workspaceID, ServiceDeskID: deskID, RequestTypeID: requestTypes[0].ID,
		Summary: "Repeated laptop", Fields: map[string]json.RawMessage{assetFieldID: json.RawMessage(`["laptop","laptop"]`)}}); err == nil {
		t.Fatal("an Assets field listed the same object twice")
	}
	// Only an Assets object field holds several objects.
	summaryContexts, err := st.CustomFieldContexts(ctx, workspaceID, "customfield_10000", nil)
	if err == nil && len(summaryContexts) > 0 {
		if err := st.SetCustomFieldContextAssetsMultiple(ctx, workspaceID, adminID, "customfield_10000", summaryContexts[0].ID, true); err == nil {
			t.Fatal("a field that holds no Assets object was configured for several")
		}
	}

	// Only an Assets field offers a schema, and only one of this desk's.
	summaryScoped := append([]models.ServiceRequestTypeField{}, formFields...)
	for index := range summaryScoped {
		if summaryScoped[index].ID == "summary" {
			summaryScoped[index].AssetSchemaID = laptops.ID
		}
	}
	if err := service.SetServiceRequestTypeFields(ctx, adminID, workspaceID, deskID, requestTypes[0].ID, summaryScoped); err == nil {
		t.Fatal("a field that holds no Assets object offered a schema")
	}
	strayScoped := append([]models.ServiceRequestTypeField{}, formFields...)
	strayScoped = append(strayScoped, models.ServiceRequestTypeField{ID: assetFieldID, AssetSchemaID: "00000000-0000-0000-0000-000000000000"})
	if err := service.SetServiceRequestTypeFields(ctx, adminID, workspaceID, deskID, requestTypes[0].ID, strayScoped); err == nil {
		t.Fatal("a field offered an Assets schema of another service project")
	}

	if err := service.SetServiceRequestAsset(ctx, adminID, workspaceID, request.Issue.Key, database.ID, "affected", true); err != nil {
		t.Fatal(err)
	}
	impact, err := st.ServiceRequestAssetImpact(ctx, workspaceID, adminID, request.Issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(impact) != 2 || !impact[0].Direct || impact[0].Object.ID != database.ID || impact[1].Direct || impact[1].Depth != 1 || impact[1].Object.ID != storefront.ID {
		t.Fatalf("impact = %+v", impact)
	}
	if err := service.SetServiceRequestAsset(ctx, adminID, workspaceID, request.Issue.Key, database.ID, "", false); err != nil {
		t.Fatal(err)
	}
	if err := service.SetServiceRequestAsset(ctx, adminID, workspaceID, request.Issue.Key, database.ID, "", false); err == nil {
		t.Fatal("disconnecting an absent asset succeeded")
	}

	var memberID string
	if err := st.Pool.QueryRow(ctx, `SELECT u.id FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.workspace_id=$1 AND m.role='member' AND NOT EXISTS(SELECT 1 FROM service_desk_agents a WHERE a.service_desk_id=$2 AND a.user_id=u.id) LIMIT 1`, workspaceID, deskID).Scan(&memberID); err == nil {
		if _, err := st.ServiceAssetInventory(ctx, workspaceID, memberID, deskID); !errors.Is(err, store.ErrProjectPermission) {
			t.Fatalf("non-agent inventory error = %v", err)
		}
	}
}
