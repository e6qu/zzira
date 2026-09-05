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
	"sort"
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

type roleRequest struct {
	Role       string `json:"role"`
	RoleID     string `json:"roleId"`
	Resource   string `json:"resource"`
	ResourceID string `json:"resourceId"`
}

type roleContext struct {
	Organization *models.Organization
	Site         *models.Site
	Products     []*models.Product
	DirectoryIDs map[string]bool
}

func productResourceID(product *models.Product) string {
	return "ari:cloud:" + product.Key + "::site/" + product.SiteID
}

func siteResourceID(siteID string) string {
	return "ari:cloud:platform::site/" + siteID
}

func organizationResourceID(organizationID string) string {
	return "ari:cloud:platform::org/" + organizationID
}

func (h *Handler) loadRoleContext(w http.ResponseWriter, r *http.Request, workspaceID string) (roleContext, bool) {
	organization, ok := h.organizationForRequest(w, r, workspaceID)
	if !ok {
		return roleContext{}, false
	}
	site, err := h.Store.SiteByWorkspace(r.Context(), workspaceID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Site lookup failed.")
		return roleContext{}, false
	}
	products, err := h.Store.ProductsBySite(r.Context(), site.ID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Product lookup failed.")
		return roleContext{}, false
	}
	directories, err := h.Store.DirectoriesByOrganization(r.Context(), organization.ID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Directory lookup failed.")
		return roleContext{}, false
	}
	directoryIDs := make(map[string]bool, len(directories))
	for _, directory := range directories {
		directoryIDs[directory.ID] = true
	}
	return roleContext{Organization: organization, Site: site, Products: products, DirectoryIDs: directoryIDs}, true
}

func (h *Handler) principalInDirectory(r *http.Request, directoryID, principalType, principalID string) (bool, error) {
	if principalType == "group" {
		groups, err := h.Store.GroupsByDirectory(r.Context(), directoryID)
		if err != nil {
			return false, err
		}
		for _, group := range groups {
			if group.ID == principalID {
				return true, nil
			}
		}
		return false, nil
	}
	users, err := h.Store.DirectoryUsers(r.Context(), directoryID)
	if err != nil {
		return false, err
	}
	for _, user := range users {
		if user.ID == principalID {
			return true, nil
		}
	}
	return false, nil
}

func (h *Handler) userStatusInDirectory(r *http.Request, directoryID, accountID string) (string, bool, error) {
	users, err := h.Store.DirectoryUsers(r.Context(), directoryID)
	if err != nil {
		return "", false, err
	}
	for _, user := range users {
		if user.ID == accountID {
			if user.Active {
				return "active", true, nil
			}
			return "suspended", true, nil
		}
	}
	return "", false, nil
}

func roleTarget(context roleContext, role, resource string, organizationOnly bool) (string, string, error) {
	role = strings.TrimSpace(role)
	resource = strings.TrimSpace(resource)
	if organizationOnly {
		if role != "atlassian/org-admin" {
			return "", "", errors.New("role must be atlassian/org-admin")
		}
		return "organization", context.Organization.ID, nil
	}
	if role == "atlassian/org-admin" {
		return "organization", context.Organization.ID, nil
	}
	if role == "atlassian/site-admin" {
		if resource != context.Site.ID && resource != siteResourceID(context.Site.ID) {
			return "", "", errors.New("resource must identify this site")
		}
		return "site", context.Site.ID, nil
	}
	allowedProductRoles := map[string]bool{
		"atlassian/user":              true,
		"atlassian/user-access-admin": true,
		"atlassian/admin":             true,
		"atlassian/guest":             true,
		"atlassian/contributor":       true,
		"atlassian/customer":          true,
		"atlassian/basic":             true,
		"atlassian/stakeholder":       true,
		"atlassian/viewer":            true,
		"atlassian/ai-access":         true,
	}
	if !allowedProductRoles[role] {
		return "", "", errors.New("role is not supported")
	}
	for _, product := range context.Products {
		if resource == product.ID || resource == productResourceID(product) {
			if (role == "atlassian/customer" || role == "atlassian/stakeholder") && product.Key != "jira-service-management" {
				return "", "", errors.New("customer and stakeholder roles require Jira Service Management")
			}
			return "product", product.ID, nil
		}
	}
	return "", "", errors.New("resource must identify an enabled product in this site")
}

func apiRole(role string) string {
	switch role {
	case "atlassian/product-user", "atlassian/site-user":
		return "atlassian/user"
	case "atlassian/product-admin":
		return "atlassian/user-access-admin"
	default:
		return role
	}
}

func queryMatches(values url.Values, name, candidate string) bool {
	raw := values[name]
	if len(raw) == 0 {
		return true
	}
	for _, value := range raw {
		for _, item := range strings.Split(value, ",") {
			if strings.TrimSpace(item) == candidate {
				return true
			}
		}
	}
	return false
}

func roleResource(context roleContext, binding *models.RoleBinding) (resourceID, owner string, ok bool) {
	switch binding.ScopeType {
	case "organization":
		if binding.ScopeID == context.Organization.ID {
			return organizationResourceID(context.Organization.ID), "platform", true
		}
	case "site":
		if binding.ScopeID == context.Site.ID {
			return siteResourceID(context.Site.ID), "platform", true
		}
	case "product":
		for _, product := range context.Products {
			if binding.ScopeID == product.ID {
				return productResourceID(product), product.Key, true
			}
		}
	}
	return "", "", false
}

func (h *Handler) Workspaces(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	context, ok := h.loadRoleContext(w, r, workspaceID)
	if !ok {
		return
	}
	var input struct {
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSONBody(r, &input); err != nil {
			failure(w, http.StatusBadRequest, "Workspace search body is invalid.")
			return
		}
	}
	if input.Cursor != "" && input.Limit != 0 {
		failure(w, http.StatusBadRequest, "A cursor cannot be combined with other workspace search fields.")
		return
	}
	values := url.Values{}
	if input.Limit > 0 {
		values.Set("limit", strconv.Itoa(input.Limit))
	}
	if input.Cursor != "" {
		values.Set("cursor", input.Cursor)
	}
	offset, limit, err := parsePage(values)
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	page, next := pageSlice(context.Products, offset, limit)
	data := make([]map[string]any, 0, len(page))
	for _, product := range page {
		data = append(data, map[string]any{
			"id":   productResourceID(product),
			"type": "workspaces",
			"attributes": map[string]any{
				"name": product.Name, "typeKey": product.Key, "type": product.Key,
				"status": "online", "hostUrl": strings.TrimRight(h.BaseURL, "/"),
				"createdAt": product.CreatedAt,
			},
			"links": map[string]any{"self": strings.TrimRight(h.BaseURL, "/") + "/admin/v2/orgs/" + url.PathEscape(context.Organization.ID) + "/workspaces"},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": data, "links": map[string]any{"self": cursorFor(offset), "next": next},
		"meta": map[string]any{"total": len(context.Products)},
	})
}

func (h *Handler) UserRoleMutation(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	context, ok := h.loadRoleContext(w, r, workspaceID)
	if !ok {
		return
	}
	var input roleRequest
	if err := decodeJSONBody(r, &input); err != nil {
		failure(w, http.StatusBadRequest, "Role request is invalid.")
		return
	}
	organizationOnly := strings.Contains(r.URL.Path, "/role-assignments/")
	scopeType, scopeID, err := roleTarget(context, input.Role, input.Resource, organizationOnly)
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	assign := strings.HasSuffix(r.URL.Path, "/assign")
	err = h.Store.SetRoleBinding(r.Context(), workspaceID, actorID, "user", r.PathValue("userId"), scopeType, scopeID, input.Role, assign)
	switch {
	case errors.Is(err, store.ErrAdminNotFound), errors.Is(err, pgx.ErrNoRows):
		failure(w, http.StatusNotFound, "Organization, resource, or user was not found.")
	case errors.Is(err, store.ErrAdminValidation):
		failure(w, http.StatusBadRequest, err.Error())
	case err != nil:
		failure(w, http.StatusInternalServerError, "Role assignment failed.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) GroupRoleMutation(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	context, ok := h.loadRoleContext(w, r, workspaceID)
	if !ok {
		return
	}
	directoryID := r.PathValue("directoryId")
	if !context.DirectoryIDs[directoryID] {
		failure(w, http.StatusNotFound, "Directory was not found.")
		return
	}
	found, err := h.principalInDirectory(r, directoryID, "group", r.PathValue("groupId"))
	if err != nil {
		failure(w, http.StatusInternalServerError, "Group lookup failed.")
		return
	}
	if !found {
		failure(w, http.StatusNotFound, "Group was not found.")
		return
	}
	var input roleRequest
	if err := decodeJSONBody(r, &input); err != nil {
		failure(w, http.StatusBadRequest, "Role request is invalid.")
		return
	}
	scopeType, scopeID, err := roleTarget(context, input.RoleID, input.ResourceID, false)
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	assign := strings.HasSuffix(r.URL.Path, "/assign")
	err = h.Store.SetRoleBinding(r.Context(), workspaceID, actorID, "group", r.PathValue("groupId"), scopeType, scopeID, input.RoleID, assign)
	switch {
	case errors.Is(err, store.ErrAdminNotFound), errors.Is(err, pgx.ErrNoRows):
		failure(w, http.StatusNotFound, "Organization, directory, group, or resource was not found.")
	case errors.Is(err, store.ErrAdminValidation):
		failure(w, http.StatusBadRequest, err.Error())
	case err != nil:
		failure(w, http.StatusInternalServerError, "Role assignment failed.")
	default:
		if assign {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func (h *Handler) RoleAssignments(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query(), "cursor", "limit", "directoryIds", "resourceOwners", "resourceIds", "roleIds"); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	context, ok := h.loadRoleContext(w, r, workspaceID)
	if !ok {
		return
	}
	principalType, principalID := "group", r.PathValue("groupId")
	if accountID := r.PathValue("accountId"); accountID != "" {
		principalType, principalID = "user", accountID
	}
	directoryID := r.PathValue("directoryId")
	if !context.DirectoryIDs[directoryID] {
		failure(w, http.StatusNotFound, "Directory was not found.")
		return
	}
	userDirectoryStatus := ""
	var found bool
	var err error
	if principalType == "user" {
		userDirectoryStatus, found, err = h.userStatusInDirectory(r, directoryID, principalID)
	} else {
		found, err = h.principalInDirectory(r, directoryID, principalType, principalID)
	}
	if err != nil {
		failure(w, http.StatusInternalServerError, "Directory principal lookup failed.")
		return
	}
	if !found {
		failure(w, http.StatusNotFound, "User or group was not found.")
		return
	}
	bindings, err := h.Store.RoleBindingsForPrincipal(r.Context(), workspaceID, principalType, principalID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Role assignment lookup failed.")
		return
	}
	type aggregate struct {
		ResourceID    string
		ResourceOwner string
		Roles         map[string][]string
	}
	byResource := map[string]*aggregate{}
	order := make([]string, 0)
	if !queryMatches(r.URL.Query(), "directoryIds", directoryID) {
		bindings = nil
	}
	for _, binding := range bindings {
		resourceID, owner, found := roleResource(context, binding)
		if !found {
			continue
		}
		role := apiRole(binding.RoleKey)
		if !queryMatches(r.URL.Query(), "resourceIds", resourceID) ||
			!queryMatches(r.URL.Query(), "resourceOwners", owner) ||
			!queryMatches(r.URL.Query(), "roleIds", role) {
			continue
		}
		item := byResource[resourceID]
		if item == nil {
			item = &aggregate{ResourceID: resourceID, ResourceOwner: owner, Roles: map[string][]string{}}
			byResource[resourceID] = item
			order = append(order, resourceID)
		}
		methods := item.Roles[role]
		method := binding.Assignment
		seen := false
		for _, existing := range methods {
			seen = seen || existing == method
		}
		if !seen {
			item.Roles[role] = append(methods, method)
		}
	}
	sort.Strings(order)
	offset, limit, err := parsePage(r.URL.Query())
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	page, next := pageSlice(order, offset, limit)
	data := make([]map[string]any, 0, len(page))
	for _, resourceID := range page {
		item := byResource[resourceID]
		if principalType == "group" {
			roles := make([]string, 0, len(item.Roles))
			for role := range item.Roles {
				roles = append(roles, role)
			}
			sort.Strings(roles)
			data = append(data, map[string]any{"resourceId": item.ResourceID, "resourceOwner": item.ResourceOwner, "roles": roles})
			continue
		}
		assignments := make([]map[string]any, 0, len(item.Roles))
		roles := make([]string, 0, len(item.Roles))
		for role, methods := range item.Roles {
			roles = append(roles, role)
			assignments = append(assignments, map[string]any{"role": role, "roleAssignmentMethods": methods})
		}
		sort.Strings(roles)
		sort.Slice(assignments, func(i, j int) bool {
			return assignments[i]["role"].(string) < assignments[j]["role"].(string)
		})
		data = append(data, map[string]any{
			"resourceId": item.ResourceID, "resourceOwner": item.ResourceOwner,
			"roles": roles, "roleAssignments": assignments,
			"directoryId": directoryID, "userDirectoryStatus": userDirectoryStatus,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "links": map[string]any{"self": cursorFor(offset), "next": next}})
}

func userStatus(user *models.User) string {
	if user.Active {
		return "active"
	}
	return "inactive"
}

func multiDirectoryUser(user *models.User) map[string]any {
	status := userStatus(user)
	membershipStatus := "active"
	if !user.Active {
		membershipStatus = "suspended"
	}
	return map[string]any{
		"accountId": user.ID, "accountType": "atlassian", "status": status,
		"accountStatus": status, "membershipStatus": membershipStatus,
		"name": user.DisplayName, "nickname": user.DisplayName, "email": user.Email,
		"emailVerified": true, "claimStatus": "managed", "mfaEnabled": false,
		"timeZone": user.TimeZone, "managementSource": "invited",
		"counts": map[string]int{"groups": 0, "resources": 0},
	}
}

func filterUsers(users []*models.User, query string) []*models.User {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return users
	}
	filtered := make([]*models.User, 0)
	for _, user := range users {
		if strings.Contains(strings.ToLower(user.DisplayName+" "+user.Email), query) {
			filtered = append(filtered, user)
		}
	}
	return filtered
}

func (h *Handler) DirectoryUsers(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query(), "cursor", "limit", "searchTerm"); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	context, ok := h.loadRoleContext(w, r, workspaceID)
	if !ok {
		return
	}
	directoryID := r.PathValue("directoryId")
	if !context.DirectoryIDs[directoryID] {
		failure(w, http.StatusNotFound, "Directory was not found.")
		return
	}
	users, err := h.Store.DirectoryUsers(r.Context(), directoryID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Directory user lookup failed.")
		return
	}
	users = filterUsers(users, r.URL.Query().Get("searchTerm"))
	offset, limit, err := parsePage(r.URL.Query())
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	page, next := pageSlice(users, offset, limit)
	data := make([]map[string]any, 0, len(page))
	for _, user := range page {
		data = append(data, multiDirectoryUser(user))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "links": map[string]any{"self": cursorFor(offset), "next": next}})
}

func (h *Handler) ManagedUsers(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query(), "cursor"); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	context, ok := h.loadRoleContext(w, r, workspaceID)
	if !ok {
		return
	}
	if r.URL.Query().Get("cursor") != "" {
		failure(w, http.StatusBadRequest, "The user cursor is invalid or expired.")
		return
	}
	users := make([]*models.User, 0)
	seen := map[string]bool{}
	for directoryID := range context.DirectoryIDs {
		directoryUsers, err := h.Store.DirectoryUsers(r.Context(), directoryID)
		if err != nil {
			failure(w, http.StatusInternalServerError, "Managed account lookup failed.")
			return
		}
		for _, user := range directoryUsers {
			if !seen[user.ID] {
				seen[user.ID] = true
				users = append(users, user)
			}
		}
	}
	data := make([]map[string]any, 0, len(users))
	for _, user := range users {
		data = append(data, map[string]any{
			"account_id": user.ID, "account_type": "atlassian", "account_status": userStatus(user),
			"name": user.DisplayName, "email": user.Email, "picture": strings.TrimRight(h.BaseURL, "/") + "/static/icons/user.svg",
			"access_billable": user.Active,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "meta": map[string]int{"total": len(data)}, "links": map[string]string{"self": ""}})
}

func (h *Handler) DirectoryUserCount(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query(), "searchTerm"); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	context, ok := h.loadRoleContext(w, r, workspaceID)
	if !ok {
		return
	}
	directoryID := r.PathValue("directoryId")
	if !context.DirectoryIDs[directoryID] {
		failure(w, http.StatusNotFound, "Directory was not found.")
		return
	}
	users, err := h.Store.DirectoryUsers(r.Context(), directoryID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Directory user count failed.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": len(filterUsers(users, r.URL.Query().Get("searchTerm")))})
}

func (h *Handler) DirectoryUserDetails(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	context, ok := h.loadRoleContext(w, r, workspaceID)
	if !ok {
		return
	}
	directoryID := r.PathValue("directoryId")
	if !context.DirectoryIDs[directoryID] {
		failure(w, http.StatusNotFound, "Directory was not found.")
		return
	}
	users, err := h.Store.DirectoryUsers(r.Context(), directoryID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Directory user lookup failed.")
		return
	}
	for _, user := range users {
		if user.ID == r.PathValue("userId") {
			writeJSON(w, http.StatusOK, map[string]any{"data": multiDirectoryUser(user)})
			return
		}
	}
	failure(w, http.StatusNotFound, "User was not found.")
}

func (h *Handler) DirectoryUserLifecycle(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if _, ok := h.organizationForRequest(w, r, workspaceID); !ok {
		return
	}
	directoryID, accountID := r.PathValue("directoryId"), r.PathValue("accountId")
	var err error
	switch {
	case r.Method == http.MethodDelete:
		err = h.Store.RemoveDirectoryUser(r.Context(), workspaceID, actorID, directoryID, accountID)
	case strings.HasSuffix(r.URL.Path, "/suspend"):
		err = h.Store.SetDirectoryUserActive(r.Context(), workspaceID, actorID, directoryID, accountID, false)
	default:
		err = h.Store.SetDirectoryUserActive(r.Context(), workspaceID, actorID, directoryID, accountID, true)
	}
	switch {
	case errors.Is(err, store.ErrAdminValidation):
		failure(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pgx.ErrNoRows), errors.Is(err, store.ErrAdminNotFound):
		failure(w, http.StatusNotFound, "Directory or user was not found.")
	case err != nil:
		failure(w, http.StatusInternalServerError, "User lifecycle update failed.")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) InviteUsers(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	context, ok := h.loadRoleContext(w, r, workspaceID)
	if !ok {
		return
	}
	var input struct {
		Emails []string `json:"emails"`
	}
	if err := decodeJSONBody(r, &input); err != nil || len(input.Emails) < 1 || len(input.Emails) > 25 {
		failure(w, http.StatusBadRequest, "Between 1 and 25 email addresses are required.")
		return
	}
	directoryID := ""
	for id := range context.DirectoryIDs {
		directoryID = id
		break
	}
	if directoryID == "" {
		failure(w, http.StatusConflict, "Organization has no active directory.")
		return
	}
	passwordHash, err := authn.UnusablePasswordHash()
	if err != nil {
		failure(w, http.StatusInternalServerError, "Invitation credential creation failed.")
		return
	}
	data := make([]map[string]any, 0, len(input.Emails))
	for _, email := range input.Emails {
		name := strings.TrimSpace(strings.SplitN(email, "@", 2)[0])
		user, err := h.Store.InviteDirectoryUser(r.Context(), workspaceID, actorID, directoryID, email, name, passwordHash)
		if err != nil {
			if errors.Is(err, store.ErrAdminValidation) {
				failure(w, http.StatusBadRequest, err.Error())
			} else if errors.Is(err, store.ErrAdminConflict) {
				failure(w, http.StatusConflict, err.Error())
			} else {
				failure(w, http.StatusInternalServerError, "User invitation failed.")
			}
			return
		}
		data = append(data, map[string]any{"id": user.ID, "email": user.Email, "results": []any{}})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}
