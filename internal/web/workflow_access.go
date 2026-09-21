package web

import (
	"net/http"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

// Workflow administration in the browser follows the rule the REST workflow
// resources already use (internal/api3/workflow_access.go): Administer Jira
// reaches every workflow, and Administer projects reaches the workflows that
// belong to that project. A project administrator configures their own
// project's workflow without being made a site administrator.

// canAdministerWorkflow reports whether the caller may change a workflow.
// The built-in workflow is read-only for everyone; callers check that
// separately because it answers with its own message.
func (h *Handler) canAdministerWorkflow(r *http.Request, workspaceID, userID string, wf workflow.Workflow) bool {
	if admin, err := h.Store.IsAdmin(r.Context(), workspaceID, userID); err == nil && admin {
		return true
	}
	if wf.ProjectID == "" {
		return false
	}
	allowed, err := h.Store.CanAdministerProject(r.Context(), workspaceID, userID, wf.ProjectID)
	return err == nil && allowed
}

// requireWorkflowAdminPage loads the workflow a request names and answers
// whether the caller may change it, so every workflow mutation shares one
// gate. The draft is returned because every mutation edits the draft.
func (h *Handler) requireWorkflowAdminPage(w http.ResponseWriter, r *http.Request, workflowID string) (*models.User, string, workflow.Workflow, bool) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return nil, "", workflow.Workflow{}, false
	}
	if workflowID == workflow.Default().ID {
		http.Error(w, "the built-in workflow is read-only; create a copy to edit it", http.StatusBadRequest)
		return nil, "", workflow.Workflow{}, false
	}
	wf, err := h.Store.WorkflowDraftByID(r.Context(), workspaceID, workflowID)
	if err != nil {
		http.NotFound(w, r)
		return nil, "", workflow.Workflow{}, false
	}
	if !h.canAdministerWorkflow(r, workspaceID, user.ID, wf) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, "", workflow.Workflow{}, false
	}
	return user, workspaceID, wf, true
}

// administeredProjects lists the projects the caller administers, which is
// every project for a site administrator.
func (h *Handler) administeredProjects(r *http.Request, workspaceID, userID string) ([]*models.Project, bool, error) {
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, userID)
	if err != nil {
		return nil, false, err
	}
	if admin {
		projects, err := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
		return projects, true, err
	}
	projects, err := h.Store.ProjectsWithPermissions(r.Context(), workspaceID, userID, []string{"ADMINISTER_PROJECTS"})
	return projects, false, err
}
