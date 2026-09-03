package api3

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/workflow"
)

// workflowRoute serves the admin workflow surface:
//
//	GET  /rest/api/3/workflow/search  → list stored workflows
//	POST /rest/api/3/workflow         → create/replace a workflow definition
//	PUT  /rest/api/3/workflow/project/{keyOrId} {workflowId: …} → assign
//	GET  /rest/api/3/workflow/project/{keyOrId}              → current id
func (h *Handler) workflowRoute(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspaceAdmin(r); e != nil {
		writeJerr(w, e)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/rest/api/3")
	switch {
	case rel == "/workflow/search" && r.Method == http.MethodGet:
		workflows, err := h.Store.ListWorkflows(r.Context())
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		values := make([]map[string]any, 0, len(workflows))
		for _, wf := range workflows {
			values = append(values, map[string]any{
				"id":          wf.ID,
				"name":        wf.Name,
				"description": "",
				"isDefault":   wf.ID == "wf_default",
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"maxResults": 50,
			"startAt":    0,
			"total":      len(values),
			"values":     values,
		})
	case rel == "/workflow" && r.Method == http.MethodPost:
		var req struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Transitions []struct {
				ID   string   `json:"id"`
				Name string   `json:"name"`
				From []string `json:"from"`
				To   string   `json:"to"`
			} `json:"transitions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.Name == "" || len(req.Transitions) == 0 {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{
				"workflow": "id, name, and at least one transition are required.",
			})
			return
		}
		wf := workflow.Workflow{ID: req.ID, Name: req.Name}
		for _, t := range req.Transitions {
			wf.Transitions = append(wf.Transitions, workflow.Transition{ID: t.ID, Name: t.Name, From: t.From, To: t.To})
		}
		if err := h.Store.CreateWorkflow(r.Context(), wf); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": wf.ID, "name": wf.Name})
	case strings.HasPrefix(rel, "/workflow/project/"):
		keyOrID := strings.TrimPrefix(rel, "/workflow/project/")
		wsID, _, e := h.authWorkspaceAdmin(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
		project, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, keyOrID)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The project does not exist.")
			return
		}
		if r.Method == http.MethodGet {
			workflowID := project.WorkflowID
			if workflowID == "" {
				workflowID = workflow.Default().ID
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"projectKeyOrId": keyOrID,
				"workflowId":     workflowID,
			})
			return
		}
		if r.Method == http.MethodPut {
			var req struct {
				WorkflowID string `json:"workflowId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.WorkflowID == "" {
				jiraFieldError(w, http.StatusBadRequest, map[string]string{"workflowId": "workflowId is required."})
				return
			}
			if _, err := h.Store.WorkflowByID(r.Context(), req.WorkflowID); err != nil {
				jiraFieldError(w, http.StatusBadRequest, map[string]string{"workflowId": "The workflow does not exist."})
				return
			}
			if err := h.Store.AssignWorkflowToProject(r.Context(), project.ID, req.WorkflowID); err != nil {
				jiraError(w, http.StatusInternalServerError, "internal error")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

// roleRoute serves GET /rest/api/3/role — the workspace role registry.
func (h *Handler) roleRoute(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	writeJSON(w, http.StatusOK, []map[string]any{
		{"id": 10000, "name": "Administrator", "description": "Workspace administrators", "self": h.BaseURL + "/rest/api/3/role/10000"},
		{"id": 10001, "name": "Member", "description": "Workspace members", "self": h.BaseURL + "/rest/api/3/role/10001"},
	})
}
