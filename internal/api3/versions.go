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
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

func versionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrVersionValidation):
		jiraError(w, 400, err.Error())
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, 403, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, 404, "Version or project does not exist.")
	default:
		log.Print("version operation: ", strconv.Quote(err.Error()))
		jiraError(w, 500, "Could not complete the version operation.")
	}
}
func decodeVersionRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		jiraError(w, 400, "Invalid version request: "+err.Error())
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		jiraError(w, 400, "Expected one JSON object.")
		return false
	}
	return true
}
func versionQuery(w http.ResponseWriter, r *http.Request, keys ...string) bool {
	for key := range r.URL.Query() {
		found := false
		for _, allowed := range keys {
			if key == allowed {
				found = true
			}
		}
		if !found {
			jiraError(w, 400, "Unsupported version parameter: "+key)
			return false
		}
	}
	if expand := r.URL.Query().Get("expand"); expand != "" && expand != "issuesstatus" {
		jiraError(w, 400, "Only the issuesstatus expansion is supported.")
		return false
	}
	return true
}
func (h *Handler) versionBean(v *models.Version) map[string]any {
	result := map[string]any{"id": v.ID, "self": h.BaseURL + "/rest/api/3/version/" + v.ID, "name": v.Name, "description": v.Description, "released": v.Released, "archived": v.Archived, "overdue": !v.Released && v.ReleaseDate != "" && v.ReleaseDate < time.Now().UTC().Format("2006-01-02")}
	if id, err := strconv.ParseInt(v.ProjectID, 10, 64); err == nil {
		result["projectId"] = id
	}
	for key, date := range map[string]string{"startDate": v.StartDate, "releaseDate": v.ReleaseDate} {
		if date != "" {
			result[key] = date
			t, err := time.Parse("2006-01-02", date)
			if err == nil {
				result["user"+strings.ToUpper(key[:1])+key[1:]] = t.Format("2/Jan/2006")
			}
		}
	}
	return result
}
func (h *Handler) expandedVersion(r *http.Request, ws, user string, v *models.Version) (map[string]any, error) {
	bean := h.versionBean(v)
	if r.URL.Query().Get("expand") == "issuesstatus" {
		issues, err := h.Store.VersionIssues(r.Context(), ws, user, v.ProjectID, v.ID, "fixVersions")
		if err != nil {
			return nil, err
		}
		bean["issuesStatusForFixVersion"] = store.VersionProgress(issues)
	}
	return bean, nil
}
func (h *Handler) versionRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	ws, user, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if len(parts) == 0 && r.Method == http.MethodPost {
		if !versionQuery(w, r) {
			return
		}
		var in struct {
			store.VersionUpdate
			ProjectID *int64 `json:"projectId"`
			Project   string `json:"project"`
		}
		if !decodeVersionRequest(w, r, &in) {
			return
		}
		key := in.Project
		if in.ProjectID != nil {
			key = strconv.FormatInt(*in.ProjectID, 10)
		}
		p, err := h.Store.ProjectByIDOrKey(r.Context(), ws, key)
		if err != nil {
			versionError(w, err)
			return
		}
		if in.Released != nil && *in.Released {
			jiraError(w, 400, "Create the version before releasing it.")
			return
		}
		v, err := h.Store.SaveVersion(r.Context(), ws, user, p.ID, "", in.VersionUpdate)
		if err != nil {
			versionError(w, err)
			return
		}
		writeJSON(w, 201, h.versionBean(v))
		return
	}
	if len(parts) == 0 {
		jiraError(w, 405, "Method not allowed.")
		return
	}
	v, err := h.Store.Version(r.Context(), ws, parts[0])
	if err != nil {
		versionError(w, err)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if !versionQuery(w, r, "expand") {
				return
			}
			bean, err := h.expandedVersion(r, ws, user, v)
			if err != nil {
				versionError(w, err)
				return
			}
			writeJSON(w, 200, bean)
		case http.MethodPut:
			if !versionQuery(w, r) {
				return
			}
			var up store.VersionUpdate
			if !decodeVersionRequest(w, r, &up) {
				return
			}
			saved, err := h.Store.SaveVersion(r.Context(), ws, user, v.ProjectID, v.ID, up)
			if err != nil {
				versionError(w, err)
				return
			}
			writeJSON(w, 200, h.versionBean(saved))
		case http.MethodDelete:
			if !versionQuery(w, r, "moveFixIssuesTo", "moveAffectedIssuesTo") {
				return
			}
			if err := h.Store.DeleteVersion(r.Context(), ws, user, v.ID, r.URL.Query().Get("moveFixIssuesTo"), r.URL.Query().Get("moveAffectedIssuesTo")); err != nil {
				versionError(w, err)
				return
			}
			w.WriteHeader(204)
		default:
			jiraError(w, 405, "Method not allowed.")
		}
		return
	}
	if len(parts) == 3 && parts[1] == "mergeto" && r.Method == http.MethodPut {
		if !versionQuery(w, r) {
			return
		}
		if err := h.Store.DeleteVersion(r.Context(), ws, user, v.ID, parts[2], parts[2]); err != nil {
			versionError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	if len(parts) == 2 && parts[1] == "removeAndSwap" && r.Method == http.MethodPost {
		if !versionQuery(w, r) {
			return
		}
		var in struct {
			MoveFixIssuesTo            *int64            `json:"moveFixIssuesTo"`
			MoveAffectedIssuesTo       *int64            `json:"moveAffectedIssuesTo"`
			CustomFieldReplacementList []json.RawMessage `json:"customFieldReplacementList"`
		}
		if !decodeVersionRequest(w, r, &in) {
			return
		}
		if len(in.CustomFieldReplacementList) > 0 {
			jiraError(w, 400, "Version picker custom fields are not supported.")
			return
		}
		fix, affected := "", ""
		if in.MoveFixIssuesTo != nil {
			fix = strconv.FormatInt(*in.MoveFixIssuesTo, 10)
		}
		if in.MoveAffectedIssuesTo != nil {
			affected = strconv.FormatInt(*in.MoveAffectedIssuesTo, 10)
		}
		if err := h.Store.DeleteVersion(r.Context(), ws, user, v.ID, fix, affected); err != nil {
			versionError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodGet && (parts[1] == "relatedIssueCounts" || parts[1] == "unresolvedIssueCount") {
		if !versionQuery(w, r) {
			return
		}
		fixed, err := h.Store.VersionIssues(r.Context(), ws, user, v.ProjectID, v.ID, "fixVersions")
		if err != nil {
			versionError(w, err)
			return
		}
		result := map[string]any{"self": h.BaseURL + "/rest/api/3/version/" + v.ID + "/" + parts[1]}
		if parts[1] == "unresolvedIssueCount" {
			result["issuesCount"] = len(fixed)
			result["issuesUnresolvedCount"] = len(fixed) - store.VersionProgress(fixed).Done
		} else {
			affected, err := h.Store.VersionIssues(r.Context(), ws, user, v.ProjectID, v.ID, "versions")
			if err != nil {
				versionError(w, err)
				return
			}
			result["issuesFixedCount"] = len(fixed)
			result["issuesAffectedCount"] = len(affected)
			result["issueCountWithCustomFieldsShowingVersion"] = 0
			result["customFieldUsage"] = []any{}
		}
		writeJSON(w, 200, result)
		return
	}
	jiraError(w, 404, "No version resource found.")
}
func (h *Handler) projectVersions(w http.ResponseWriter, r *http.Request, key string, paginated bool) {
	ws, user, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if r.Method != http.MethodGet {
		jiraError(w, 405, "Method not allowed.")
		return
	}
	if paginated {
		if !versionQuery(w, r, "startAt", "maxResults", "orderBy", "query", "status", "expand") {
			return
		}
	} else if !versionQuery(w, r, "expand") {
		return
	}
	p, err := h.Store.ProjectByIDOrKey(r.Context(), ws, key)
	if err != nil {
		versionError(w, err)
		return
	}
	versions, err := h.Store.ProjectVersions(r.Context(), p.ID)
	if err != nil {
		versionError(w, err)
		return
	}
	query := r.URL.Query()
	states := map[string]bool{}
	if raw := query.Get("status"); raw != "" {
		for _, status := range strings.Split(raw, ",") {
			if status != "released" && status != "unreleased" && status != "archived" {
				jiraError(w, 400, "Unknown version status.")
				return
			}
			states[status] = true
		}
	}
	filtered := []*models.Version{}
	for _, v := range versions {
		term := strings.ToLower(query.Get("query"))
		if term != "" && !strings.Contains(strings.ToLower(v.Name), term) && !strings.Contains(strings.ToLower(v.Description), term) {
			continue
		}
		if len(states) > 0 && !states[strings.ToLower(v.State())] {
			continue
		}
		filtered = append(filtered, v)
	}
	order := query.Get("orderBy")
	desc := strings.HasPrefix(order, "-")
	order = strings.TrimPrefix(strings.TrimPrefix(order, "-"), "+")
	if order == "" {
		order = "sequence"
	}
	switch order {
	case "sequence", "name", "description", "startDate", "releaseDate":
	default:
		jiraError(w, 400, "Unknown version ordering.")
		return
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		if order == "sequence" {
			if desc {
				return a.Position > b.Position
			}
			return a.Position < b.Position
		}
		av, bv := "", ""
		switch order {
		case "name":
			av, bv = a.Name, b.Name
		case "description":
			av, bv = a.Description, b.Description
		case "startDate":
			av, bv = a.StartDate, b.StartDate
		case "releaseDate":
			av, bv = a.ReleaseDate, b.ReleaseDate
		}
		if order == "startDate" || order == "releaseDate" {
			if av == "" {
				return false
			}
			if bv == "" {
				return true
			}
		}
		if desc {
			return av > bv
		}
		return av < bv
	})
	total := len(filtered)
	start, limit := 0, total
	if paginated {
		var e *jerr
		start, limit, e = metadataPage(r)
		if e != nil {
			writeJerr(w, e)
			return
		}
	}
	end := min(start, total) + min(limit, total-min(start, total))
	values := []map[string]any{}
	for _, v := range filtered[min(start, total):end] {
		bean, err := h.expandedVersion(r, ws, user, v)
		if err != nil {
			versionError(w, err)
			return
		}
		values = append(values, bean)
	}
	if !paginated {
		writeJSON(w, 200, values)
		return
	}
	link := func(offset int) string {
		q := r.URL.Query()
		q.Set("startAt", strconv.Itoa(offset))
		q.Set("maxResults", strconv.Itoa(limit))
		return h.BaseURL + r.URL.EscapedPath() + "?" + q.Encode()
	}
	result := map[string]any{"self": link(start), "startAt": start, "maxResults": limit, "total": total, "isLast": end >= total, "values": values}
	if end < total && limit > 0 {
		result["nextPage"] = link(end)
	}
	writeJSON(w, 200, result)
}
