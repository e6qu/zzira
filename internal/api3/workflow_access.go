package api3

import (
	"net/http"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/workflow"
)

// workflowAccess answers which workflows a caller may read or change, as Jira
// documents for its workflow resources: Administer Jira reaches every
// workflow, Administer projects reaches a project's own workflows, and View
// read-only workflow reads them.
type workflowAccess struct {
	h           *Handler
	r           *http.Request
	workspaceID string
	userID      string
	admin       bool
	held        map[string]bool
}

func (h *Handler) authWorkflowAccess(r *http.Request) (*workflowAccess, *jerr) {
	workspaceID, userID, e := h.authWorkspace(r)
	if e != nil {
		return nil, e
	}
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Store, workspaceID, userID)
	if err != nil {
		return nil, &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	return &workflowAccess{h: h, r: r, workspaceID: workspaceID, userID: userID, admin: admin, held: map[string]bool{}}, nil
}

func (a *workflowAccess) holds(projectID, permission string) bool {
	if projectID == "" {
		return false
	}
	key := projectID + "\x00" + permission
	if allowed, known := a.held[key]; known {
		return allowed
	}
	allowed, err := a.h.Store.HasProjectPermission(a.r.Context(), a.workspaceID, a.userID, projectID, "", permission)
	a.held[key] = err == nil && allowed
	return a.held[key]
}

// canChange reports whether the caller may create or update workflows in a
// scope: every scope for site administrators, a project's own scope for its
// administrators.
func (a *workflowAccess) canChange(projectID string) bool {
	return a.admin || a.holds(projectID, "ADMINISTER_PROJECTS")
}

// canRead reports whether the caller may read workflows in a scope, or, for a
// project, the workflows the project uses.
func (a *workflowAccess) canRead(projectID string) bool {
	return a.canChange(projectID) || a.holds(projectID, "VIEW_READONLY_WORKFLOW")
}

// canChangeWorkflows reports whether the caller may change every existing
// workflow an update names; unknown workflows are left to validation.
func (a *workflowAccess) canChangeWorkflows(references []string) bool {
	if a.admin {
		return true
	}
	stored := a.h.workflowIDsFor(a.r, a.workspaceID)
	for _, reference := range references {
		wf, err := a.h.Store.WorkflowByID(a.r.Context(), a.workspaceID, stored.toInternal(reference))
		if err != nil {
			continue
		}
		if !a.canChange(wf.ProjectID) {
			return false
		}
	}
	return true
}

// scoped keeps the workflows the caller may read.
func (a *workflowAccess) scoped(workflows []workflow.Workflow) []workflow.Workflow {
	if a.admin {
		return workflows
	}
	kept := make([]workflow.Workflow, 0, len(workflows))
	for _, wf := range workflows {
		if a.canRead(wf.ProjectID) {
			kept = append(kept, wf)
		}
	}
	return kept
}

// errWorkflowPermission is Jira's answer to a caller without the workflow
// permissions an operation needs.
func errWorkflowPermission() *jerr {
	return &jerr{http.StatusUnauthorized, "You don't have permission to perform this operation on these workflows.", nil}
}
