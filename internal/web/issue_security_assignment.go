package web

import (
	"net/http"
	"net/url"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type securityAssignmentData struct {
	Project *models.Project
	Scheme  *models.SecurityScheme
	Levels  []store.IssueSecurityLevelUsage
	Chosen  map[string]string
	Error   string
}

func (h *Handler) assignIssueSecurityScheme(w http.ResponseWriter, r *http.Request, user *models.User, workspaceID, schemeID string) {
	projectID := r.PostFormValue("project")
	current, project, err := h.Store.AssignedIssueSecurityScheme(r.Context(), workspaceID, projectID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	target, err := h.Store.IssueSecurityScheme(r.Context(), workspaceID, schemeID, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	used, err := h.Store.ProjectIssueSecurityLevelUsage(r.Context(), workspaceID, project.ID)
	if err != nil {
		http.Error(w, "Could not load the project's security levels.", http.StatusInternalServerError)
		return
	}
	data := securityAssignmentData{Project: project, Scheme: target, Levels: used, Chosen: map[string]string{}}
	seen := map[string]bool{}
	for _, level := range used {
		seen[level.ID] = true
	}
	if current != nil && current.ID != target.ID {
		// Include unused levels too, so work created while the queued migration
		// waits can use any level the old scheme currently offers.
		for _, level := range current.Levels {
			if !seen[level.ID] {
				data.Levels = append(data.Levels, store.IssueSecurityLevelUsage{ID: level.ID, Name: level.Name})
			}
		}
	}
	if current != nil && current.ID == target.ID {
		data.Levels = nil
	}
	var mappings []store.IssueSecurityLevelMapping
	if len(data.Levels) > 0 {
		if r.PostFormValue("mapped") != "true" {
			h.writeWorkspacePage(w, r, "page_issue_security_assignment", user, workspaceID, data, "issue-security-schemes", "")
			return
		}
		validTargets := map[string]bool{"none": true}
		for _, level := range target.Levels {
			validTargets[level.ID] = true
		}
		for _, level := range data.Levels {
			chosen := r.PostFormValue("level." + level.ID)
			data.Chosen[level.ID] = chosen
			if !validTargets[chosen] {
				data.Error = "Choose a replacement for every security level."
				continue
			}
			if chosen == "none" {
				chosen = ""
			}
			mappings = append(mappings, store.IssueSecurityLevelMapping{OldLevelID: level.ID, NewLevelID: chosen})
		}
		if data.Error != "" {
			h.writeWorkspacePageStatus(w, r, "page_issue_security_assignment", user, workspaceID, data, "issue-security-schemes", "", http.StatusBadRequest)
			return
		}
	}
	_, err = h.Store.EnqueueAssignIssueSecurityScheme(r.Context(), workspaceID, user.ID, project.ID, schemeID, mappings)
	if err != nil {
		data.Error = issueSecurityMutationMessage(err)
		h.writeWorkspacePageStatus(w, r, "page_issue_security_assignment", user, workspaceID, data, "issue-security-schemes", "", http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/settings/issue-security-schemes?notice="+url.QueryEscape("Project assignment queued."))
}
