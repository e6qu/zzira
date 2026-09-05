// Package confluence implements the delivered Confluence Cloud v2 contract.
package confluence

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Handler struct {
	Store                  *store.Store
	Commands               *commands.Service
	WorkspaceSlug, BaseURL string
}

func respond(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		log.Printf("confluence response: %v", err)
	}
}
func failure(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]any{"errors": []any{map[string]any{"status": status, "code": http.StatusText(status), "title": message}}})
}
func writeError(w http.ResponseWriter, err error) {
	var pgerr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		failure(w, 404, "Content does not exist or you do not have permission to view it.")
	case errors.Is(err, store.ErrProjectPermission):
		failure(w, 403, err.Error())
	case errors.Is(err, store.ErrWikiConflict):
		failure(w, 409, err.Error())
	case errors.Is(err, store.ErrWikiValidation):
		failure(w, 400, err.Error())
	case errors.As(err, &pgerr) && pgerr.Code == "23505":
		failure(w, 400, "A space with this key or a published page with this title already exists.")
	default:
		log.Printf("confluence operation: %v", err)
		failure(w, 500, "Could not complete the wiki operation.")
	}
}
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		failure(w, 400, "Invalid request: "+err.Error())
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		failure(w, 400, "Expected one JSON object.")
		return false
	}
	return true
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	actor, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		failure(w, 401, "Authentication required.")
		return
	}
	ws, err := h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		writeError(w, err)
		return
	}
	member, err := h.Store.IsMember(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	if !member {
		failure(w, 403, "Workspace membership required.")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "spaces" && r.Method == "GET":
		h.spaces(w, r, ws, actor)
	case len(parts) == 1 && parts[0] == "spaces" && r.Method == "POST":
		h.createSpace(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "spaces" && r.Method == "GET":
		if !supportedQuery(w, r, "description-format") {
			return
		}
		if format := r.URL.Query().Get("description-format"); format != "" && format != "plain" {
			failure(w, 400, "Only plain space descriptions are supported.")
			return
		}
		space, err := h.Store.WikiSpace(r.Context(), ws, actor, parts[1])
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, h.spaceBean(space))
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "pages" && r.Method == "GET":
		if _, err := h.Store.WikiSpace(r.Context(), ws, actor, parts[1]); err != nil {
			writeError(w, err)
			return
		}
		h.pages(w, r, ws, actor, parts[1])
	case len(parts) == 1 && parts[0] == "pages" && r.Method == "GET":
		h.pages(w, r, ws, actor, "")
	case len(parts) == 1 && parts[0] == "pages" && r.Method == "POST":
		h.savePage(w, r, ws, actor, "")
	case len(parts) == 2 && parts[0] == "pages" && r.Method == "PUT":
		h.savePage(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "pages" && r.Method == "GET":
		if !supportedQuery(w, r, "body-format", "status") {
			return
		}
		if !storageFormat(w, r) {
			return
		}
		page, err := h.Store.WikiPage(r.Context(), ws, actor, parts[1])
		if err != nil {
			writeError(w, err)
			return
		}
		status := r.URL.Query().Get("status")
		if status == "" {
			status = "current"
		}
		if page.Status != status {
			failure(w, 404, "Page not found with the requested status.")
			return
		}
		respond(w, 200, h.pageBean(page, r.URL.Query().Get("body-format") != ""))
	case len(parts) == 2 && parts[0] == "pages" && r.Method == "DELETE":
		if !supportedQuery(w, r) {
			return
		}
		page, err := h.Store.WikiPage(r.Context(), ws, actor, parts[1])
		if err != nil {
			writeError(w, err)
			return
		}
		if page.Status == "trashed" {
			failure(w, 400, "Permanent deletion is not implemented.")
			return
		}
		page.Status = "trashed"
		page.Version.Number++
		page.Version.Message = "Moved to trash"
		if _, err := h.Commands.SaveWikiPage(r.Context(), ws, actor, *page); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "versions" && r.Method == "GET":
		if !supportedQuery(w, r, "limit", "cursor") {
			return
		}
		versions, err := h.Store.WikiVersions(r.Context(), ws, actor, parts[1])
		if err != nil {
			writeError(w, err)
			return
		}
		values := make([]any, 0, len(versions))
		for _, v := range versions {
			values = append(values, v)
		}
		h.list(w, r, values)
	default:
		failure(w, 404, "This Confluence resource is not implemented.")
	}
}

func supportedQuery(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for key := range r.URL.Query() {
		found := false
		for _, a := range allowed {
			if a == key {
				found = true
				break
			}
		}
		if !found {
			failure(w, 400, "Unsupported query parameter: "+key)
			return false
		}
	}
	return true
}
func storageFormat(w http.ResponseWriter, r *http.Request) bool {
	format := r.URL.Query().Get("body-format")
	if format != "" && format != "storage" {
		failure(w, 400, "Only the storage body format is currently supported.")
		return false
	}
	return true
}
func (h *Handler) spaceBean(s *models.WikiSpace) map[string]any {
	return map[string]any{"id": s.ID, "key": s.Key, "name": s.Name, "type": "global", "status": "current", "authorId": s.AuthorID, "spaceOwnerId": s.AuthorID, "createdAt": s.CreatedAt, "description": map[string]any{"plain": models.WikiBody{Representation: "plain", Value: s.Description}}, "_links": map[string]string{"webui": "/spaces/" + s.ID, "base": h.BaseURL + "/wiki"}}
}
func (h *Handler) pageBean(p *models.WikiPage, body bool) map[string]any {
	bean := map[string]any{"id": p.ID, "status": p.Status, "title": p.Title, "spaceId": p.SpaceID, "authorId": p.AuthorID, "ownerId": p.AuthorID, "lastOwnerId": p.AuthorID, "createdAt": p.CreatedAt, "version": p.Version, "_links": map[string]string{"webui": "/spaces/" + p.SpaceID + "/pages/" + p.ID, "base": h.BaseURL + "/wiki"}}
	if p.ParentID != "" {
		bean["parentId"] = p.ParentID
		bean["parentType"] = "page"
	}
	if body {
		bean["body"] = map[string]any{"storage": p.Body}
	}
	return bean
}

func (h *Handler) createSpace(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var in struct {
		Name, Key, Alias   string
		Description        models.WikiBody
		CreatePrivateSpace bool
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Key == "" {
		in.Key = in.Alias
	} else if in.Alias != "" && in.Key != in.Alias {
		failure(w, 400, "Separate space aliases are not supported.")
		return
	}
	if in.Description.Representation != "" && in.Description.Representation != "plain" {
		failure(w, 400, "Space description must use plain representation.")
		return
	}
	s, err := h.Commands.CreateWikiSpace(r.Context(), ws, actor, in.Key, in.Name, in.Description.Value, in.CreatePrivateSpace)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 201, h.spaceBean(s))
}

func (h *Handler) spaces(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "limit", "cursor", "keys", "ids", "type", "status", "description-format") {
		return
	}
	q := r.URL.Query()
	if f := q.Get("description-format"); f != "" && f != "plain" {
		failure(w, 400, "Only plain space descriptions are supported.")
		return
	}
	items, err := h.Store.WikiSpaces(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	values := []any{}
	for _, s := range items {
		if !queryContains(r, "keys", s.Key) || !queryContains(r, "ids", s.ID) {
			continue
		}
		if q.Get("type") != "" && q.Get("type") != "global" {
			continue
		}
		if q.Get("status") != "" && q.Get("status") != "current" {
			continue
		}
		values = append(values, h.spaceBean(s))
	}
	h.list(w, r, values)
}
func queryContains(r *http.Request, key, value string) bool {
	raw, ok := r.URL.Query()[key]
	if !ok {
		return true
	}
	for _, chunk := range raw {
		for _, v := range strings.Split(chunk, ",") {
			if v == value {
				return true
			}
		}
	}
	return false
}

func (h *Handler) pages(w http.ResponseWriter, r *http.Request, ws, actor, space string) {
	if !supportedQuery(w, r, "limit", "cursor", "space-id", "id", "status", "title", "body-format") {
		return
	}
	if !storageFormat(w, r) {
		return
	}
	q := r.URL.Query()
	status := q.Get("status")
	if status == "" {
		status = "current"
	}
	if status != "current" && status != "draft" && status != "trashed" {
		failure(w, 400, "Unsupported page status.")
		return
	}
	pages, err := h.Store.WikiPages(r.Context(), ws, actor, space, status, q.Get("title"))
	if err != nil {
		writeError(w, err)
		return
	}
	values := []any{}
	for _, p := range pages {
		if queryContains(r, "space-id", p.SpaceID) && queryContains(r, "id", p.ID) {
			values = append(values, h.pageBean(p, q.Get("body-format") != ""))
		}
	}
	h.list(w, r, values)
}

func (h *Handler) savePage(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "root-level") {
		return
	}
	var in struct {
		ID, SpaceID, Title, Status string
		ParentID                   *string
		Body                       models.WikiBody
		Version                    models.WikiVersion
	}
	if !decode(w, r, &in) {
		return
	}
	if id != "" && in.ID != id {
		failure(w, 400, "The body id must match the page URL.")
		return
	}
	if id == "" && in.ID != "" {
		failure(w, 400, "New page IDs are assigned by the server.")
		return
	}
	if root := r.URL.Query().Get("root-level"); root != "" && root != "true" {
		failure(w, 400, "Only root-level=true is supported.")
		return
	} else if root == "true" && in.ParentID != nil {
		failure(w, 400, "A root page cannot have a parentId.")
		return
	}
	if id != "" {
		old, err := h.Store.WikiPage(r.Context(), ws, actor, id)
		if err != nil {
			writeError(w, err)
			return
		}
		if in.SpaceID == "" {
			in.SpaceID = old.SpaceID
		}
		if in.ParentID == nil {
			in.ParentID = &old.ParentID
		}
	}
	if in.Status == "trashed" {
		failure(w, 400, "Use DELETE to move a page to trash.")
		return
	}
	parentID := ""
	if in.ParentID != nil {
		parentID = *in.ParentID
	}
	p, err := h.Commands.SaveWikiPage(r.Context(), ws, actor, models.WikiPage{ID: id, SpaceID: in.SpaceID, ParentID: parentID, Title: in.Title, Status: in.Status, Body: in.Body, Version: in.Version})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.pageBean(p, true))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, values []any) {
	start, limit := 0, 25
	if raw, ok := r.URL.Query()["limit"]; ok {
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || n > 250 {
			failure(w, 400, "limit must be between 1 and 250.")
			return
		}
		limit = n
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			failure(w, 400, "Invalid cursor.")
			return
		}
		n, err := strconv.Atoi(string(b))
		if err != nil || n < 0 {
			failure(w, 400, "Invalid cursor.")
			return
		}
		start = n
	}
	start = min(start, len(values))
	end := start + min(limit, len(values)-start)
	links := map[string]string{"base": h.BaseURL + "/wiki"}
	if end < len(values) {
		q := r.URL.Query()
		q.Set("cursor", base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end))))
		next := r.URL.Path + "?" + q.Encode()
		links["next"] = next
		w.Header().Set("Link", fmt.Sprintf("<%s%s>; rel=\"next\"", h.BaseURL, next))
	}
	respond(w, 200, map[string]any{"results": values[start:end], "_links": links})
}
