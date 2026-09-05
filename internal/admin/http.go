// Package admin serves the Atlassian organization administration contract.
package admin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type Handler struct {
	Store         *store.Store
	BaseURL       string
	WorkspaceSlug string
}

type adminError struct {
	Errors []adminErrorItem `json:"errors"`
}

type adminErrorItem struct {
	Status int    `json:"status"`
	Title  string `json:"title"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("admin API response write: %v", err)
	}
}

func failure(w http.ResponseWriter, status int, title string) {
	writeJSON(w, status, adminError{Errors: []adminErrorItem{{Status: status, Title: title}}})
}

func rejectUnknownQuery(values url.Values, allowed ...string) error {
	known := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		known[name] = true
	}
	for name := range values {
		if !known[name] {
			return fmt.Errorf("query parameter %q is not supported", name)
		}
	}
	return nil
}

func decodeJSONBody(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	userID, err := authn.IdentifyBearer(r.Context(), h.Store, r)
	if err != nil {
		failure(w, http.StatusUnauthorized, "A valid bearer organization API key is required.")
		return "", "", false
	}
	workspaceID, err := h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Site lookup failed.")
		return "", "", false
	}
	allowed, err := authz.Allowed(r.Context(), h.Store, workspaceID, userID, authz.AdministerSite)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Permission evaluation failed.")
		return "", "", false
	}
	if !allowed {
		failure(w, http.StatusForbidden, "Organization administration permission is required.")
		return "", "", false
	}
	return userID, workspaceID, true
}

func (h *Handler) organizationForRequest(w http.ResponseWriter, r *http.Request, workspaceID string) (*models.Organization, bool) {
	organization, err := h.Store.OrganizationByWorkspace(r.Context(), workspaceID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Organization lookup failed.")
		return nil, false
	}
	if organization.ID != r.PathValue("orgId") {
		failure(w, http.StatusNotFound, "Organization was not found.")
		return nil, false
	}
	return organization, true
}

func (h *Handler) orgModel(organization *models.Organization) map[string]any {
	base := strings.TrimRight(h.BaseURL, "/") + "/admin/v1/orgs/" + url.PathEscape(organization.ID)
	return map[string]any{
		"id":   organization.ID,
		"type": "orgs",
		"attributes": map[string]any{
			"name": organization.Name,
		},
		"relationships": map[string]any{
			"domains": map[string]any{"links": map[string]any{"related": base + "/domains"}},
			"users":   map[string]any{"links": map[string]any{"related": base + "/users"}},
		},
		"links": map[string]any{"self": base},
	}
}

func (h *Handler) Organizations(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query(), "cursor"); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.URL.Query().Get("cursor") != "" {
		failure(w, http.StatusBadRequest, "The organization cursor is invalid or expired.")
		return
	}
	organization, err := h.Store.OrganizationByWorkspace(r.Context(), workspaceID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Organization lookup failed.")
		return
	}
	self := strings.TrimRight(h.BaseURL, "/") + "/admin/v1/orgs"
	writeJSON(w, http.StatusOK, map[string]any{
		"data":  []any{h.orgModel(organization)},
		"links": map[string]any{"self": self},
	})
}

func (h *Handler) Organization(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	organization, ok := h.organizationForRequest(w, r, workspaceID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": h.orgModel(organization)})
}

func parsePage(values url.Values) (int, int, error) {
	limit := 20
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, errors.New("limit must be between 1 and 100")
		}
		limit = parsed
	}
	offset := 0
	if cursor := values.Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || !strings.HasPrefix(string(decoded), "offset:") {
			return 0, 0, errors.New("cursor is invalid or expired")
		}
		parsed, err := strconv.Atoi(strings.TrimPrefix(string(decoded), "offset:"))
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("cursor is invalid or expired")
		}
		offset = parsed
	}
	return offset, limit, nil
}

func cursorFor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("offset:%d", offset)))
}

func pageSlice[T any](items []T, offset, limit int) ([]T, string) {
	if offset >= len(items) {
		return []T{}, ""
	}
	end := offset + limit
	if end >= len(items) {
		return items[offset:], ""
	}
	return items[offset:end], cursorFor(end)
}

func (h *Handler) Directories(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query(), "cursor", "limit"); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	organization, ok := h.organizationForRequest(w, r, workspaceID)
	if !ok {
		return
	}
	offset, limit, err := parsePage(r.URL.Query())
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	directories, err := h.Store.DirectoriesByOrganization(r.Context(), organization.ID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Directory lookup failed.")
		return
	}
	page, next := pageSlice(directories, offset, limit)
	data := make([]map[string]any, 0, len(page))
	for _, directory := range page {
		data = append(data, map[string]any{
			"directoryId": directory.ID,
			"name":        directory.Name,
			"icon":        strings.TrimRight(h.BaseURL, "/") + "/static/icons/directory.svg",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":  data,
		"links": map[string]any{"self": cursorFor(offset), "next": next},
	})
}

func (h *Handler) Groups(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	allowedQuery := []string{}
	if r.Method == http.MethodGet {
		allowedQuery = []string{"cursor", "limit", "searchTerm"}
	}
	if err := rejectUnknownQuery(r.URL.Query(), allowedQuery...); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	organization, ok := h.organizationForRequest(w, r, workspaceID)
	if !ok {
		return
	}
	directoryID := r.PathValue("directoryId")
	directories, err := h.Store.DirectoriesByOrganization(r.Context(), organization.ID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Directory lookup failed.")
		return
	}
	found := false
	for _, directory := range directories {
		if directory.ID == directoryID {
			found = true
			break
		}
	}
	if !found {
		failure(w, http.StatusNotFound, "Directory was not found.")
		return
	}

	if r.Method == http.MethodPost {
		var input struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := decodeJSONBody(r, &input); err != nil {
			failure(w, http.StatusBadRequest, "Request body must contain a valid group name and description.")
			return
		}
		_, err := h.Store.CreateDirectoryGroup(r.Context(), workspaceID, actorID, directoryID, input.Name, input.Description)
		switch {
		case errors.Is(err, store.ErrAdminValidation):
			failure(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, store.ErrAdminConflict):
			failure(w, http.StatusConflict, err.Error())
		case err != nil:
			failure(w, http.StatusInternalServerError, "Group creation failed.")
		default:
			w.WriteHeader(http.StatusCreated)
		}
		return
	}

	offset, limit, err := parsePage(r.URL.Query())
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	groups, err := h.Store.GroupsByDirectory(r.Context(), directoryID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Group lookup failed.")
		return
	}
	if query := strings.TrimSpace(r.URL.Query().Get("searchTerm")); query != "" {
		filtered := groups[:0]
		for _, group := range groups {
			if strings.Contains(strings.ToLower(group.Name+" "+group.Description), strings.ToLower(query)) {
				filtered = append(filtered, group)
			}
		}
		groups = filtered
	}
	page, next := pageSlice(groups, offset, limit)
	data := make([]map[string]any, 0, len(page))
	for _, group := range page {
		data = append(data, map[string]any{
			"id": group.ID, "name": group.Name, "description": group.Description,
			"directoryId": group.DirectoryID, "externalSynced": false, "managedBy": "admins",
			"managementAccess": map[string]bool{"deletable": true, "modifiable": true, "readable": true},
			"counts":           map[string]int{"users": group.MemberCount, "resources": 0},
			"links":            map[string]any{"self": cursorFor(offset)},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":  data,
		"links": map[string]any{"self": cursorFor(offset), "next": next},
	})
}

func (h *Handler) GroupMembership(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if _, ok := h.organizationForRequest(w, r, workspaceID); !ok {
		return
	}
	var input struct {
		AccountID string `json:"accountId"`
	}
	if err := decodeJSONBody(r, &input); err != nil || strings.TrimSpace(input.AccountID) == "" {
		failure(w, http.StatusBadRequest, "accountId is required.")
		return
	}
	err := h.Store.SetGroupMember(r.Context(), workspaceID, actorID, r.PathValue("directoryId"), r.PathValue("groupId"), input.AccountID, true)
	switch {
	case errors.Is(err, store.ErrAdminValidation):
		failure(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrAdminConflict):
		failure(w, http.StatusConflict, err.Error())
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, store.ErrAdminNotFound):
		failure(w, http.StatusNotFound, "Directory, group, or user was not found.")
	case err != nil:
		failure(w, http.StatusInternalServerError, "Group membership update failed.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) DeleteGroupMembership(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if _, ok := h.organizationForRequest(w, r, workspaceID); !ok {
		return
	}
	err := h.Store.SetGroupMember(r.Context(), workspaceID, actorID, r.PathValue("directoryId"), r.PathValue("groupId"), r.PathValue("accountId"), false)
	switch {
	case errors.Is(err, store.ErrAdminValidation):
		failure(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, store.ErrAdminNotFound):
		failure(w, http.StatusNotFound, "Directory, group, or membership was not found.")
	case err != nil:
		failure(w, http.StatusInternalServerError, "Group membership update failed.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
