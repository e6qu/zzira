package web

import (
	"context"

	appRuntime "github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/models"
)

// appConditionFactsFor are what Connect conditions may ask about a person, in
// a project and on a work item when a module is shown with them.
func (h *Handler) appConditionFactsFor(ctx context.Context, workspaceID string, user *models.User, project *models.Project, issue *models.Issue) appRuntime.ConnectConditionFacts {
	facts := appRuntime.ConnectConditionFacts{}
	if user == nil {
		return facts
	}
	facts.LoggedIn = true
	if admin, err := h.Store.IsAdmin(ctx, workspaceID, user.ID); err == nil {
		facts.SiteAdmin = admin
	}
	projectID := ""
	if project != nil {
		projectID = project.ID
	} else if issue != nil {
		projectID = issue.ProjectID
	}
	if projectID != "" {
		answers := map[string]bool{}
		facts.ProjectPermission = func(permission string) bool {
			if answer, ok := answers[permission]; ok {
				return answer
			}
			allowed, err := h.Store.HasProjectPermission(ctx, workspaceID, user.ID, projectID, "", permission)
			answers[permission] = err == nil && allowed
			return answers[permission]
		}
	}
	if issue != nil {
		answers := map[string]bool{}
		facts.IssuePermission = func(permission string) bool {
			if answer, ok := answers[permission]; ok {
				return answer
			}
			allowed, err := h.Store.HasProjectPermission(ctx, workspaceID, user.ID, issue.ProjectID, issue.ID, permission)
			answers[permission] = err == nil && allowed
			return answers[permission]
		}
		facts.Issue = &appRuntime.ConnectIssueFacts{
			AssignedToCurrentUser: issue.Assignee != nil && issue.Assignee.ID == user.ID,
			ReportedByCurrentUser: issue.Reporter != nil && issue.Reporter.ID == user.ID,
			Unassigned:            issue.Assignee == nil || issue.Assignee.ID == "",
		}
	}
	return facts
}

// appModulesShown keeps the modules whose conditions hold for the facts.
func appModulesShown(modules []models.AppModule, facts appRuntime.ConnectConditionFacts) []models.AppModule {
	shown := modules[:0:0]
	for _, module := range modules {
		if appModuleShown(module, facts) {
			shown = append(shown, module)
		}
	}
	return shown
}

func appModuleShown(module models.AppModule, facts appRuntime.ConnectConditionFacts) bool {
	return appRuntime.ConnectConditionsMet(module.Conditions, facts)
}
