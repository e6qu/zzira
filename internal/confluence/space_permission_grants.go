package confluence

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

func (h *Handler) spacePermissionGrantBean(grant store.WikiSpacePermissionGrant) map[string]any {
	key, target, _ := strings.Cut(grant.Permission, "/")
	return map[string]any{
		"id":        wikiNumericID(grant.ID),
		"subject":   map[string]any{"type": grant.SubjectType, "identifier": grant.SubjectID},
		"operation": map[string]any{"key": key, "target": target},
		"_links":    map[string]string{"base": h.BaseURL + "/wiki"},
	}
}

type spacePermissionSubject struct {
	Type       string `json:"type"`
	Identifier string `json:"identifier"`
}

func (h *V1Handler) v1AddSpacePermission(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey string) {
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		Subject   spacePermissionSubject `json:"subject"`
		Operation struct {
			Key    string `json:"key"`
			Target string `json:"target"`
		} `json:"operation"`
	}
	if !decode(w, r, &input) {
		return
	}
	grant, err := h.Store.AddWikiSpacePermission(r.Context(), ws, actor, spaceKey,
		input.Subject.Type, input.Subject.Identifier, input.Operation.Key, input.Operation.Target)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.spacePermissionGrantBean(grant))
}

// v1AddSpaceCustomContentPermissions grants several operations at once, which
// is how an app asks for what its custom content needs.
func (h *V1Handler) v1AddSpaceCustomContentPermissions(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey string) {
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		Subject    spacePermissionSubject `json:"subject"`
		Operations []struct {
			Key    string `json:"key"`
			Target string `json:"target"`
			Access bool   `json:"access"`
		} `json:"operations"`
	}
	if !decode(w, r, &input) {
		return
	}
	operations := make([][2]string, 0, len(input.Operations))
	for _, operation := range input.Operations {
		// `access: false` describes a permission the app does not want, so
		// granting it would be the opposite of what was asked.
		if !operation.Access {
			continue
		}
		operations = append(operations, [2]string{operation.Key, operation.Target})
	}
	if err := h.Store.AddWikiSpaceCustomContentPermissions(r.Context(), ws, actor, spaceKey,
		input.Subject.Type, input.Subject.Identifier, operations); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *V1Handler) v1RemoveSpacePermission(w http.ResponseWriter, r *http.Request, ws, actor, spaceKey, grantID string) {
	if !supportedQuery(w, r) {
		return
	}
	if err := h.Store.RemoveWikiSpacePermission(r.Context(), ws, actor, spaceKey, grantID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

// v1CheckContentPermission answers whether a subject may do something to one
// piece of content.
func (h *V1Handler) v1CheckContentPermission(w http.ResponseWriter, r *http.Request, ws, actor, contentID string) {
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		Subject   spacePermissionSubject `json:"subject"`
		Operation string                 `json:"operation"`
	}
	if !decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Operation) == "" {
		failure(w, 400, "operation is required.")
		return
	}
	allowed, err := h.Store.CheckWikiContentPermission(r.Context(), ws, actor, contentID,
		input.Subject.Type, input.Subject.Identifier, input.Operation)
	if err != nil {
		writeError(w, err)
		return
	}
	bean := map[string]any{"hasPermission": allowed, "_links": map[string]string{"base": h.BaseURL + "/wiki"}}
	if !allowed {
		bean["errors"] = []any{map[string]any{
			"translation": "The subject does not have permission to " + input.Operation + " this content.",
			"args":        []any{},
		}}
	}
	respond(w, 200, bean)
}

// spacePermissionsCatalogue reports what may be granted. Each entry names the
// permissions it requires, because granting anything in a space is meaningless
// without being able to read it.
func (h *Handler) spacePermissionsCatalogue(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "cursor", "limit") {
		return
	}
	permissions, err := h.Store.WikiSpacePermissions(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	results := make([]any, 0, len(permissions))
	for _, permission := range permissions {
		key, target, _ := strings.Cut(permission, "/")
		entry := map[string]any{
			"id": permission, "displayName": spacePermissionDisplayName(key, target),
			"description":           spacePermissionDescription(key, target),
			"requiredPermissionIds": []string{},
		}
		if permission != "read/space" {
			entry["requiredPermissionIds"] = []string{"read/space"}
		}
		results = append(results, entry)
	}
	respond(w, 200, map[string]any{
		"results": results,
		"_links":  map[string]string{"base": h.BaseURL + "/wiki"},
	})
}

func spacePermissionDisplayName(key, target string) string {
	verb := map[string]string{
		"read": "View", "create": "Add", "update": "Edit", "delete": "Delete", "administer": "Administer",
	}[key]
	if verb == "" {
		verb = strings.Title(key)
	}
	noun := map[string]string{
		"space": "space", "page": "pages", "blogpost": "blog posts", "comment": "comments",
		"attachment": "attachments", "folder": "folders", "embed": "embeds", "database": "databases",
		"whiteboard": "whiteboards", "custom": "custom content",
	}[target]
	if noun == "" {
		noun = target
	}
	return verb + " " + noun
}

func spacePermissionDescription(key, target string) string {
	if key == "administer" {
		return "Administer the space and everything in it."
	}
	return spacePermissionDisplayName(key, target) + " in this space."
}

// spaceRoleMode reports how this site governs spaces.
func (h *Handler) spaceRoleMode(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	mode, err := h.Store.WikiSpaceRoleMode(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{"mode": mode})
}
