package api3

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

func decodeProjectRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		jiraError(w, 400, "Invalid project request: "+err.Error())
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		jiraError(w, 400, "Expected one JSON object.")
		return false
	}
	return true
}

func projectError(w http.ResponseWriter, err error) {
	var validation *commands.ProjectValidationError
	switch {
	case errors.As(err, &validation):
		jiraFieldError(w, 400, validation.Fields)
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, 403, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, 404, "Project does not exist or you do not have permission to see it.")
	default:
		log.Printf("project operation: %v", err)
		jiraError(w, 500, "Could not save project.")
	}
}

func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var in commands.CreateProjectInput
	if !decodeProjectRequest(w, r, &in) {
		return
	}
	p, err := h.Commands.CreateProject(r.Context(), userID, wsID, in)
	if err != nil {
		projectError(w, err)
		return
	}
	id, err := strconv.ParseInt(p.ID, 10, 64)
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "key": p.Key, "self": h.BaseURL + "/rest/api/3/project/" + p.ID})
}

func (h *Handler) updateProject(w http.ResponseWriter, r *http.Request, key string) {
	wsID, userID, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var in store.ProjectUpdate
	if !decodeProjectRequest(w, r, &in) {
		return
	}
	p, err := h.Commands.UpdateProject(r.Context(), userID, wsID, key, in)
	if err != nil {
		projectError(w, err)
		return
	}
	h.writeProject(w, r, p)
}

func (h *Handler) writeProject(w http.ResponseWriter, r *http.Request, p *models.Project) {
	bean := h.projectBean(p)
	if p.LeadAccountID != "" {
		lead, err := h.Store.UserByID(r.Context(), p.LeadAccountID)
		if err != nil {
			projectError(w, err)
			return
		}
		bean["lead"] = h.userBean(lead)
	}
	writeJSON(w, http.StatusOK, bean)
}

func filterProjects(r *http.Request, projects []*models.Project) ([]*models.Project, *jerr) {
	q := r.URL.Query()
	for key := range q {
		switch key {
		case "query", "keys", "id", "typeKey", "startAt", "maxResults", "orderBy":
		default:
			return nil, &jerr{400, "Unsupported project search parameter: " + key, nil}
		}
	}
	order := q.Get("orderBy")
	if order == "" {
		order = "key"
	}
	field := strings.TrimLeft(order, "+-")
	if field != "key" && field != "name" {
		return nil, &jerr{400, "orderBy must be key, name, -key or -name.", nil}
	}
	keys, ids := commaQuerySet(r, "keys"), commaQuerySet(r, "id")
	query := strings.ToLower(q.Get("query"))
	types := commaQuerySet(r, "typeKey")
	out := make([]*models.Project, 0, len(projects))
	for _, p := range projects {
		if len(types) > 0 && !querySetContains(types, "software") {
			continue
		}
		if len(keys) > 0 && !querySetContains(keys, p.Key) {
			continue
		}
		if len(ids) > 0 && !querySetContains(ids, p.ID) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(p.Key), query) && !strings.Contains(strings.ToLower(p.Name), query) {
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Key, out[j].Key
		if field == "name" {
			a, b = strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		}
		if a == b {
			a, b = out[i].Key, out[j].Key
		}
		if strings.HasPrefix(order, "-") {
			return a > b
		}
		return a < b
	})
	return out, nil
}

func (h *Handler) projectPageURL(r *http.Request, start, limit int) string {
	q := r.URL.Query()
	q.Set("startAt", strconv.Itoa(start))
	q.Set("maxResults", strconv.Itoa(limit))
	return h.BaseURL + "/rest/api/3/project/search?" + q.Encode()
}
