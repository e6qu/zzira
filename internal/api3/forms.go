package api3

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

func (h *Handler) issueFormsRoute(w http.ResponseWriter, r *http.Request) {
	wsID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/jira/forms/cloud/"), "/"), "/")
	if len(parts) < 4 || parts[0] == "" || parts[1] != "issue" || parts[3] != "form" {
		jiraError(w, http.StatusNotFound, "Form resource does not exist.")
		return
	}
	issue, issueErr := h.resolveIssue(r, wsID, parts[2])
	if issueErr != nil {
		writeJerr(w, issueErr)
		return
	}
	if len(parts) == 4 {
		switch r.Method {
		case http.MethodGet:
			forms, err := h.Store.IssueForms(r.Context(), wsID, issue.ID)
			if err != nil {
				jiraError(w, http.StatusInternalServerError, "Could not load forms.")
				return
			}
			values := make([]map[string]any, 0, len(forms))
			for _, form := range forms {
				values = append(values, formIndexBean(form))
			}
			writeJSON(w, http.StatusOK, values)
		case http.MethodPost:
			var request struct {
				FormTemplate struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"formTemplate"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "A formTemplate id is required.")
				return
			}
			form, err := h.Store.AttachIssueForm(r.Context(), wsID, issue.ID, request.FormTemplate.ID, request.FormTemplate.Name)
			if err != nil {
				jiraError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, formIndexBean(form))
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	formID := parts[4]
	if len(parts) == 5 {
		switch r.Method {
		case http.MethodGet:
			form, err := h.Store.IssueForm(r.Context(), wsID, issue.ID, formID)
			if err != nil {
				jiraError(w, http.StatusNotFound, "Form does not exist.")
				return
			}
			writeJSON(w, http.StatusOK, formBean(form))
		case http.MethodPut:
			var request struct {
				Answers json.RawMessage `json:"answers"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "Form answers must be a JSON object.")
				return
			}
			form, err := h.Store.SaveIssueFormAnswers(r.Context(), wsID, issue.ID, formID, request.Answers)
			if err != nil {
				status := http.StatusBadRequest
				if err == pgx.ErrNoRows {
					status = http.StatusPreconditionFailed
					if _, loadErr := h.Store.IssueForm(r.Context(), wsID, issue.ID, formID); loadErr != nil {
						status = http.StatusNotFound
					}
				}
				jiraError(w, status, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, formBean(form))
		case http.MethodDelete:
			if err := h.Store.DeleteIssueForm(r.Context(), wsID, issue.ID, formID); err != nil {
				jiraError(w, http.StatusNotFound, "Form does not exist.")
				return
			}
			writeJSON(w, http.StatusOK, formID)
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if len(parts) != 7 || parts[5] != "action" || r.Method != http.MethodPut {
		jiraError(w, http.StatusNotFound, "Form resource does not exist.")
		return
	}
	var err error
	switch parts[6] {
	case "submit":
		_, err = h.Store.SetIssueFormStatus(r.Context(), wsID, issue.ID, formID, true)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]string{"status": "submitted"})
			return
		}
	case "reopen":
		_, err = h.Store.SetIssueFormStatus(r.Context(), wsID, issue.ID, formID, false)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]string{"status": "open"})
			return
		}
	case "external":
		_, err = h.Store.SetIssueFormVisibility(r.Context(), wsID, issue.ID, formID, false)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]string{"visibility": "external"})
			return
		}
	case "internal":
		_, err = h.Store.SetIssueFormVisibility(r.Context(), wsID, issue.ID, formID, true)
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]string{"visibility": "internal"})
			return
		}
	default:
		jiraError(w, http.StatusNotFound, "Form action does not exist.")
		return
	}
	jiraError(w, http.StatusNotFound, "Form does not exist.")
}

func formIndexBean(form *models.IssueForm) map[string]any {
	return map[string]any{
		"formTemplate": map[string]string{"id": form.TemplateID}, "id": form.ID, "internal": form.Internal,
		"lock": form.Locked, "name": form.Name, "submitted": form.Submitted, "updated": form.Updated,
	}
}

func formBean(form *models.IssueForm) map[string]any {
	answers := map[string]any{}
	_ = json.Unmarshal(form.Answers, &answers)
	status, visibility := "o", "i"
	if form.Submitted {
		status = "s"
	}
	if !form.Internal {
		visibility = "e"
	}
	return map[string]any{
		"id": form.ID, "updated": form.Updated,
		"design": map[string]any{
			"conditions": map[string]any{}, "layout": []any{}, "questions": map[string]any{}, "sections": map[string]any{},
			"settings": map[string]any{"language": "en", "name": form.Name, "primaryLocale": "en-US", "submit": map[string]bool{"lock": form.Locked, "pdf": false}},
		},
		"state": map[string]any{"answers": answers, "status": status, "visibility": visibility},
	}
}
