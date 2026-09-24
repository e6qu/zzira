package store_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

// Copying a scheme carries its configuration and nothing else: the copy is
// named after the original, holds the same rules, and is assigned to no
// project. A second copy takes the next name, as Jira's do.
func TestCopyingSchemesCarriesTheirConfiguration(t *testing.T) {
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
	ws, admin := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Copies')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Copy admin')`, admin, admin+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, admin)
	t.Cleanup(func() {
		for _, statement := range []string{
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(statement, ws)
		}
		exec(`DELETE FROM users WHERE id=$1`, admin)
	})

	// A permission scheme with a grant.
	scheme, err := st.CreatePermissionScheme(ctx, ws, admin, "Delivery", "Who may do what",
		[]store.PermissionGrantInput{{Permission: "BROWSE_PROJECTS", HolderType: "anyone"}})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := st.CopyPermissionScheme(ctx, ws, admin, scheme.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copied.Name != "Copy of Delivery" || copied.Description != scheme.Description {
		t.Fatalf("the copy is %q / %q", copied.Name, copied.Description)
	}
	if copied.ProjectCount != 0 || copied.Default {
		t.Fatalf("the copy took the original's place: %+v", copied)
	}
	full, err := st.PermissionScheme(ctx, ws, copied.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Grants) != 1 || full.Grants[0].Permission != "BROWSE_PROJECTS" || full.Grants[0].HolderType != "anyone" {
		t.Fatalf("the copy's grants: %+v", full.Grants)
	}
	again, err := st.CopyPermissionScheme(ctx, ws, admin, scheme.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != "Copy 2 of Delivery" {
		t.Fatalf("the second copy is %q", again.Name)
	}

	// A notification scheme with a recipient.
	notifications, err := st.CreateNotificationScheme(ctx, ws, admin, "Tell the team", "",
		[]store.NotificationEntryInput{{EventID: 1, NotificationType: "CurrentAssignee"}})
	if err != nil {
		t.Fatal(err)
	}
	copiedNotifications, err := st.CopyNotificationScheme(ctx, ws, admin, notifications.ID)
	if err != nil {
		t.Fatal(err)
	}
	fullNotifications, err := st.NotificationScheme(ctx, ws, copiedNotifications.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range fullNotifications.Events {
		for _, entry := range event.Notifications {
			if event.EventID == 1 && entry.NotificationType == "CurrentAssignee" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the copy notifies nobody: %+v", fullNotifications.Events)
	}

	// An issue security scheme with a level and its members.
	security, err := st.CreateIssueSecurityScheme(ctx, ws, admin, "Restricted", "", []store.SecurityLevelInput{{
		Name: "Private", Description: "Named people only", Default: true,
		Members: []store.SecurityLevelMemberInput{{Type: "user", Parameter: admin}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	copiedSecurity, err := st.CopyIssueSecurityScheme(ctx, ws, admin, security.ID)
	if err != nil {
		t.Fatal(err)
	}
	fullSecurity, err := st.IssueSecurityScheme(ctx, ws, copiedSecurity.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(fullSecurity.Levels) != 1 || fullSecurity.Levels[0].Name != "Private" {
		t.Fatalf("the copy's levels: %+v", fullSecurity.Levels)
	}
	if fullSecurity.DefaultLevelID != fullSecurity.Levels[0].ID {
		t.Fatal("the copy lost which level is the default")
	}
	if len(fullSecurity.Levels[0].Grants) != 1 || fullSecurity.Levels[0].Grants[0].HolderType != "user" {
		t.Fatalf("the copy's level members: %+v", fullSecurity.Levels[0].Grants)
	}
	// The next copy takes the next name, as every scheme's copy does. The
	// name check once read a table the site never had, so every copy after
	// the first was named as if it were the first.
	securityAgain, err := st.CopyIssueSecurityScheme(ctx, ws, admin, security.ID)
	if err != nil {
		t.Fatal(err)
	}
	if securityAgain.Name != "Copy 2 of Restricted" {
		t.Fatalf("the second copy is %q", securityAgain.Name)
	}

	// A screen with its tabs and fields, in order.
	screen, err := st.CreateScreen(ctx, ws, admin, "Triage", "What we ask when work arrives")
	if err != nil {
		t.Fatal(err)
	}
	originalTabs, err := st.ScreenTabs(ctx, ws, screen.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(originalTabs) == 0 {
		t.Fatal("a new screen has no tab to put fields on")
	}
	firstTab := originalTabs[0].ID
	for _, field := range []string{"summary", "priority"} {
		if _, err := st.AddScreenTabField(ctx, ws, admin, screen.ID, firstTab, field); err != nil {
			t.Fatal(err)
		}
	}
	second, err := st.AddScreenTab(ctx, ws, admin, screen.ID, "More")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddScreenTabField(ctx, ws, admin, screen.ID, second.ID, "labels"); err != nil {
		t.Fatal(err)
	}
	copiedScreen, err := st.CopyScreen(ctx, ws, admin, screen.ID)
	if err != nil {
		t.Fatal(err)
	}
	copiedTabs, err := st.ScreenTabs(ctx, ws, copiedScreen.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copiedScreen.Name != "Copy of Triage" || len(copiedTabs) != 2 {
		t.Fatalf("the copied screen: %q with %d tabs", copiedScreen.Name, len(copiedTabs))
	}
	if copiedTabs[1].Name != "More" {
		t.Fatalf("the second tab is called %q", copiedTabs[1].Name)
	}
	for index, want := range []string{"summary,priority", "labels"} {
		fields, err := st.ScreenTabFields(ctx, ws, copiedScreen.ID, copiedTabs[index].ID)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(fieldNames(fields), ","); got != want {
			t.Fatalf("tab %d holds %q, want %q", index+1, got, want)
		}
	}

	// A screen scheme, and a work type screen scheme that maps to it.
	screenScheme, err := st.CreateScreenScheme(ctx, ws, admin, "Triage screens", "", map[string]string{"default": screen.ID})
	if err != nil {
		t.Fatal(err)
	}
	copiedScheme, err := st.CopyScreenScheme(ctx, ws, admin, screenScheme.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copiedScheme.Screens["default"] != screen.ID {
		t.Fatalf("the copied screen scheme points at %v", copiedScheme.Screens)
	}
	typeScheme, err := st.CreateIssueTypeScreenScheme(ctx, ws, admin, "Triage by work type", "",
		[]models.IssueTypeScreenSchemeItem{{IssueTypeID: "default", ScreenSchemeID: screenScheme.ID}})
	if err != nil {
		t.Fatal(err)
	}
	copiedTypeScheme, err := st.CopyIssueTypeScreenScheme(ctx, ws, admin, typeScheme.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(copiedTypeScheme.Mappings) == 0 || copiedTypeScheme.Mappings[0].ScreenSchemeID != screenScheme.ID {
		t.Fatalf("the copied work type screen scheme maps %+v", copiedTypeScheme.Mappings)
	}

	// A field configuration, with a field made required.
	configuration, err := st.CreateFieldConfiguration(ctx, ws, admin, "Tight", "Everything we insist on")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetFieldConfigurationItems(ctx, ws, admin, configuration.ID,
		[]models.FieldConfigurationItem{{FieldID: "duedate", IsRequired: true, Description: "When it is needed"}}); err != nil {
		t.Fatal(err)
	}
	copiedConfiguration, err := st.CopyFieldConfiguration(ctx, ws, admin, configuration.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := st.FieldConfigurationItems(ctx, ws, copiedConfiguration.ID)
	if err != nil {
		t.Fatal(err)
	}
	required := false
	for _, item := range items {
		if item.FieldID == "duedate" && item.IsRequired && item.Description == "When it is needed" {
			required = true
		}
	}
	if !required {
		t.Fatal("the copied field configuration lost what it insists on")
	}

	// A workflow scheme carries the workflow each work type uses, and takes
	// no project with it.
	var workflowID, issueTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM workflows WHERE workspace_id IS NULL OR workspace_id=$1 ORDER BY id LIMIT 1`, ws).Scan(&workflowID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM issue_types WHERE workspace_id IS NULL ORDER BY jira_id LIMIT 1`).Scan(&issueTypeID); err != nil {
		t.Fatal(err)
	}
	routing, err := st.CreateWorkflowScheme(ctx, ws, admin, workflow.Scheme{
		Name: "Routing", Description: "Which workflow runs what",
		DefaultWorkflowID: workflowID, IssueTypeMappings: map[string]string{issueTypeID: workflowID},
	})
	if err != nil {
		t.Fatal(err)
	}
	routingCopy, err := st.CopyWorkflowScheme(ctx, ws, admin, routing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if routingCopy.Name != "Copy of Routing" || routingCopy.Description != routing.Description {
		t.Fatalf("the copy is %q / %q", routingCopy.Name, routingCopy.Description)
	}
	if routingCopy.DefaultWorkflowID != workflowID || routingCopy.IssueTypeMappings[issueTypeID] != workflowID {
		t.Fatalf("the copy lost what routes the work: %+v", copiedScheme)
	}
	if routingCopy.IsDefault {
		t.Fatal("the copy took the site default's place")
	}
	projects, err := st.ProjectsForWorkflowScheme(ctx, ws, routingCopy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("the copy arrived in use: %+v", projects)
	}
	routingAgain, err := st.CopyWorkflowScheme(ctx, ws, admin, routing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if routingAgain.Name != "Copy 2 of Routing" {
		t.Fatalf("the second copy is %q", routingAgain.Name)
	}
}

// fieldNames is the ids of a tab's fields, in order.
func fieldNames(fields []models.ScreenField) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.ID)
	}
	return names
}
