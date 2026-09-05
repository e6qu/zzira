package api3

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

func dashboardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrDashboardValidation):
		jiraError(w, 400, err.Error())
	case errors.Is(err, store.ErrDashboardPermission):
		jiraError(w, 403, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, 404, "Dashboard or gadget does not exist.")
	default:
		log.Print("dashboard operation: ", strconv.Quote(err.Error()))
		jiraError(w, 500, "Could not complete the dashboard operation.")
	}
}
func decodeDashboard(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		jiraError(w, 400, "Invalid dashboard request: "+err.Error())
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		jiraError(w, 400, "Expected one JSON value.")
		return false
	}
	return true
}
func dashboardQuery(w http.ResponseWriter, r *http.Request, keys ...string) bool {
	for key, values := range r.URL.Query() {
		found := false
		for _, k := range keys {
			if key == k {
				found = true
			}
		}
		if !found {
			jiraError(w, 400, "Unsupported dashboard parameter: "+key)
			return false
		}
		if len(values) > 1 && key != "moduleKey" && key != "uri" && key != "gadgetId" {
			jiraError(w, 400, "Repeated dashboard parameter: "+key)
			return false
		}
	}
	if v := r.URL.Query().Get("extendAdminPermissions"); v != "" && v != "false" {
		jiraError(w, 400, "Admin permission extension is not supported.")
		return false
	}
	return true
}
func (h *Handler) dashboardBean(d *models.Dashboard) map[string]any {
	return map[string]any{"id": d.ID, "name": d.Name, "description": d.Description, "self": h.BaseURL + "/rest/api/3/dashboard/" + d.ID, "view": h.BaseURL + "/dashboards/" + d.ID, "owner": map[string]any{"accountId": d.OwnerID, "displayName": d.OwnerName, "self": h.BaseURL + "/rest/api/3/user?accountId=" + url.QueryEscape(d.OwnerID)}, "isFavourite": d.Favourite, "isWritable": d.Writable, "popularity": d.Popularity, "automaticRefreshMs": d.RefreshMS, "sharePermissions": d.SharePermissions, "editPermissions": d.EditPermissions, "systemDashboard": false}
}
func dashboardDetails(w http.ResponseWriter, r *http.Request) (store.DashboardDetails, bool) {
	var in store.DashboardDetails
	if !decodeDashboard(w, r, &in) {
		return in, false
	}
	if in.SharePermissions == nil || in.EditPermissions == nil {
		jiraError(w, 400, "name, sharePermissions and editPermissions are required.")
		return in, false
	}
	return in, true
}
func (h *Handler) dashboardRoute(w http.ResponseWriter, r *http.Request, p []string) {
	w.Header().Set("Cache-Control", "no-store")
	ws, user, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if (len(p) == 0 || len(p) == 1 && p[0] == "search") && r.Method == http.MethodGet {
		h.dashboardList(w, r, ws, user, len(p) > 0)
		return
	}
	if len(p) == 1 && p[0] == "gadgets" && r.Method == http.MethodGet {
		if !dashboardQuery(w, r) {
			return
		}
		writeJSON(w, 200, map[string]any{"gadgets": models.GadgetCatalog()})
		return
	}
	if len(p) == 0 && r.Method == http.MethodPost {
		if !dashboardQuery(w, r, "extendAdminPermissions") {
			return
		}
		in, ok := dashboardDetails(w, r)
		if !ok {
			return
		}
		d, err := h.Store.SaveDashboard(r.Context(), ws, user, "", in)
		if err != nil {
			dashboardError(w, err)
			return
		}
		writeJSON(w, 200, h.dashboardBean(d))
		return
	}
	if len(p) == 0 {
		jiraError(w, 405, "Method not allowed.")
		return
	}
	if len(p) == 1 {
		switch r.Method {
		case http.MethodGet:
			if !dashboardQuery(w, r) {
				return
			}
			d, err := h.Store.Dashboard(r.Context(), ws, user, p[0])
			if err != nil {
				dashboardError(w, err)
				return
			}
			writeJSON(w, 200, h.dashboardBean(d))
		case http.MethodPut:
			if !dashboardQuery(w, r, "extendAdminPermissions") {
				return
			}
			in, ok := dashboardDetails(w, r)
			if !ok {
				return
			}
			d, err := h.Store.SaveDashboard(r.Context(), ws, user, p[0], in)
			if err != nil {
				dashboardError(w, err)
				return
			}
			writeJSON(w, 200, h.dashboardBean(d))
		case http.MethodDelete:
			if !dashboardQuery(w, r) {
				return
			}
			if err := h.Store.DeleteDashboard(r.Context(), ws, user, p[0]); err != nil {
				dashboardError(w, err)
				return
			}
			w.WriteHeader(204)
		default:
			jiraError(w, 405, "Method not allowed.")
		}
		return
	}
	if len(p) == 2 && p[1] == "copy" && r.Method == http.MethodPost {
		if !dashboardQuery(w, r, "extendAdminPermissions") {
			return
		}
		in, ok := dashboardDetails(w, r)
		if !ok {
			return
		}
		d, err := h.Store.CopyDashboard(r.Context(), ws, user, p[0], in)
		if err != nil {
			dashboardError(w, err)
			return
		}
		writeJSON(w, 200, h.dashboardBean(d))
		return
	}
	if p[1] == "gadget" && len(p) <= 3 {
		h.dashboardGadgetRoute(w, r, ws, user, p)
		return
	}
	if p[1] == "items" && (len(p) == 4 || len(p) == 5) && p[3] == "properties" {
		h.dashboardPropertyRoute(w, r, ws, user, p)
		return
	}
	jiraError(w, 404, "No dashboard resource found.")
}
func (h *Handler) dashboardList(w http.ResponseWriter, r *http.Request, ws, user string, search bool) {
	if search {
		if !dashboardQuery(w, r, "dashboardName", "accountId", "owner", "orderBy", "startAt", "maxResults", "status", "expand") {
			return
		}
	} else if !dashboardQuery(w, r, "filter", "startAt", "maxResults") {
		return
	}
	q := r.URL.Query()
	start, limit := 0, 20
	if search {
		limit = 50
	}
	for key, target := range map[string]*int{"startAt": &start, "maxResults": &limit} {
		if raw, ok := q[key]; ok {
			n, err := strconv.Atoi(raw[0])
			if err != nil || n < 0 || (key == "maxResults" && n == 0) {
				jiraError(w, 400, "Invalid "+key)
				return
			}
			*target = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	filter := q.Get("filter")
	if filter != "" && filter != "my" && filter != "favourite" {
		jiraError(w, 400, "Unknown dashboard filter.")
		return
	}
	if status := q.Get("status"); status != "" && status != "active" {
		jiraError(w, 400, "Only active dashboards are supported.")
		return
	}
	for _, expand := range strings.Split(q.Get("expand"), ",") {
		switch expand {
		case "", "description", "owner", "view", "sharePermissions", "editPermissions", "isFavourite":
		default:
			jiraError(w, 400, "Unsupported dashboard expansion.")
			return
		}
	}
	all, err := h.Store.Dashboards(r.Context(), ws, user)
	if err != nil {
		dashboardError(w, err)
		return
	}
	filtered := []*models.Dashboard{}
	for _, d := range all {
		if filter == "my" && d.OwnerID != user || filter == "favourite" && !d.Favourite {
			continue
		}
		owner := q.Get("accountId")
		if owner == "" {
			owner = q.Get("owner")
		}
		if owner != "" && d.OwnerID != owner {
			continue
		}
		if !strings.Contains(strings.ToLower(d.Name), strings.ToLower(q.Get("dashboardName"))) {
			continue
		}
		filtered = append(filtered, d)
	}
	order := q.Get("orderBy")
	desc := strings.HasPrefix(order, "-")
	order = strings.TrimLeft(order, "+-")
	if order == "" {
		order = "name"
	}
	switch order {
	case "name", "description", "owner", "id", "favorite_count", "is_favorite":
	default:
		jiraError(w, 400, "Unknown dashboard ordering.")
		return
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		cmp := 0
		switch order {
		case "favorite_count":
			cmp = a.Popularity - b.Popularity
		case "is_favorite":
			if a.Favourite != b.Favourite {
				if a.Favourite {
					cmp = 1
				} else {
					cmp = -1
				}
			}
		case "id":
			ai, _ := strconv.ParseInt(a.ID, 10, 64)
			bi, _ := strconv.ParseInt(b.ID, 10, 64)
			if ai < bi {
				cmp = -1
			} else if ai > bi {
				cmp = 1
			}
		default:
			av, bv := a.Name, b.Name
			if order == "description" {
				av, bv = a.Description, b.Description
			}
			if order == "owner" {
				av, bv = a.OwnerName, b.OwnerName
			}
			cmp = strings.Compare(strings.ToLower(av), strings.ToLower(bv))
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
	total := len(filtered)
	offset := min(start, total)
	end := offset + min(limit, total-offset)
	values := []any{}
	for _, d := range filtered[offset:end] {
		values = append(values, h.dashboardBean(d))
	}
	link := func(n int) string {
		copy := r.URL.Query()
		copy.Set("startAt", strconv.Itoa(n))
		copy.Set("maxResults", strconv.Itoa(limit))
		return h.BaseURL + r.URL.Path + "?" + copy.Encode()
	}
	out := map[string]any{"startAt": start, "maxResults": limit, "total": total}
	if search {
		out["values"] = values
		out["isLast"] = end == total
		out["self"] = link(start)
		if end < total {
			out["nextPage"] = link(end)
		}
	} else {
		out["dashboards"] = values
		if end < total {
			out["next"] = link(end)
		}
		if start > 0 {
			out["prev"] = link(max(0, start-limit))
		}
	}
	writeJSON(w, 200, out)
}
func (h *Handler) dashboardGadgetRoute(w http.ResponseWriter, r *http.Request, ws, user string, p []string) {
	if len(p) == 2 && r.Method == http.MethodGet {
		if !dashboardQuery(w, r, "moduleKey", "uri", "gadgetId") {
			return
		}
		all, err := h.Store.DashboardGadgets(r.Context(), ws, user, p[0])
		if err != nil {
			dashboardError(w, err)
			return
		}
		out := []models.DashboardGadget{}
		q := r.URL.Query()
		matches := func(key, value string) bool {
			if len(q[key]) == 0 {
				return true
			}
			for _, v := range q[key] {
				for _, item := range strings.Split(v, ",") {
					if item == value {
						return true
					}
				}
			}
			return false
		}
		for _, g := range all {
			if matches("moduleKey", g.ModuleKey) && matches("gadgetId", strconv.FormatInt(g.ID, 10)) && matches("uri", "") {
				out = append(out, g)
			}
		}
		writeJSON(w, 200, map[string]any{"gadgets": out})
		return
	}
	if !dashboardQuery(w, r) {
		return
	}
	var gid int64
	if len(p) == 3 {
		var err error
		gid, err = strconv.ParseInt(p[2], 10, 64)
		if err != nil || gid <= 0 {
			jiraError(w, 404, "Gadget does not exist.")
			return
		}
	}
	if len(p) == 3 && r.Method == http.MethodDelete {
		if err := h.Store.DeleteDashboardGadget(r.Context(), ws, user, p[0], gid); err != nil {
			dashboardError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	if len(p) == 2 && r.Method == http.MethodPost || len(p) == 3 && r.Method == http.MethodPut {
		var in store.GadgetUpdate
		if !decodeDashboard(w, r, &in) {
			return
		}
		g, err := h.Store.SaveDashboardGadget(r.Context(), ws, user, p[0], gid, in)
		if err != nil {
			dashboardError(w, err)
			return
		}
		if gid == 0 {
			writeJSON(w, 200, g)
		} else {
			w.WriteHeader(204)
		}
		return
	}
	jiraError(w, 405, "Method not allowed.")
}
func (h *Handler) dashboardPropertyRoute(w http.ResponseWriter, r *http.Request, ws, user string, p []string) {
	if !dashboardQuery(w, r) {
		return
	}
	gid, err := strconv.ParseInt(p[2], 10, 64)
	if err != nil || gid <= 0 {
		jiraError(w, 404, "Gadget does not exist.")
		return
	}
	if r.Method == http.MethodGet {
		props, err := h.Store.DashboardProperties(r.Context(), ws, user, p[0], gid)
		if err != nil {
			dashboardError(w, err)
			return
		}
		if len(p) == 4 {
			keys := make([]string, 0, len(props))
			for k := range props {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out := []any{}
			for _, k := range keys {
				out = append(out, map[string]string{"key": k, "self": h.BaseURL + r.URL.EscapedPath() + "/" + url.PathEscape(k)})
			}
			writeJSON(w, 200, map[string]any{"keys": out})
			return
		}
		value, ok := props[p[4]]
		if !ok {
			jiraError(w, 404, "Property does not exist.")
			return
		}
		writeJSON(w, 200, map[string]any{"key": p[4], "value": value})
		return
	}
	if len(p) == 5 && (r.Method == http.MethodPut || r.Method == http.MethodDelete) {
		var value json.RawMessage
		if r.Method == http.MethodPut {
			if !decodeDashboard(w, r, &value) {
				return
			}
		}
		created, err := h.Store.SetDashboardProperty(r.Context(), ws, user, p[0], gid, p[4], value)
		if err != nil {
			dashboardError(w, err)
			return
		}
		status := 200
		if created {
			status = 201
		}
		if r.Method == http.MethodDelete {
			status = 204
		}
		w.WriteHeader(status)
		return
	}
	jiraError(w, 405, "Method not allowed.")
}
