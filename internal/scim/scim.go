// Package scim serves SCIM 2.0 user and group provisioning for one directory,
// which is how an identity provider creates, updates and deactivates the
// people and groups of an organization it manages.
//
// Every write goes through the same directory operations an administrator's
// own API uses, so a person an identity provider provisions is the same person
// the site already knows and carries the same audit trail. The directory
// becomes SCIM-managed the first time a provider writes to it.
package scim

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/store"
)

const (
	userSchema         = "urn:ietf:params:scim:schemas:core:2.0:User"
	groupSchema        = "urn:ietf:params:scim:schemas:core:2.0:Group"
	listResponseSchema = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	patchOpSchema      = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	errorSchema        = "urn:ietf:params:scim:api:messages:2.0:Error"
	contentType        = "application/scim+json"
	// maximumPage is the largest page a list returns, as Atlassian's SCIM
	// caps one.
	maximumPage = 100
)

// Handler serves /scim/directory/{directoryId}/...
type Handler struct {
	Store         *store.Store
	BaseURL       string
	WorkspaceSlug string
}

func writeSCIM(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("scim response write: %v", err)
	}
}

// scimError answers in the shape RFC 7644 gives an error, which providers
// read to decide whether to retry.
func scimError(w http.ResponseWriter, status int, detail, scimType string) {
	body := map[string]any{"schemas": []string{errorSchema}, "status": strconv.Itoa(status), "detail": detail}
	if scimType != "" {
		body["scimType"] = scimType
	}
	writeSCIM(w, status, body)
}

// authorize answers who is provisioning and which directory they name. SCIM
// runs as the organization's administrator: the provider holds a bearer token
// of one, as it holds an API key in Atlassian.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (actorID, workspaceID, directoryID string, ok bool) {
	actorID, err := authn.IdentifyBearer(r.Context(), h.Store, r)
	if err != nil {
		scimError(w, http.StatusUnauthorized, "A valid bearer provisioning token is required.", "")
		return "", "", "", false
	}
	workspaceID, err = h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		scimError(w, http.StatusInternalServerError, "Site lookup failed.", "")
		return "", "", "", false
	}
	allowed, err := authz.Allowed(r.Context(), h.Store, workspaceID, actorID, authz.AdministerSite)
	if err != nil {
		scimError(w, http.StatusInternalServerError, "Permission evaluation failed.", "")
		return "", "", "", false
	}
	if !allowed {
		scimError(w, http.StatusForbidden, "Organization administration permission is required.", "")
		return "", "", "", false
	}
	directoryID, err = h.Store.SCIMDirectory(r.Context(), workspaceID, r.PathValue("directoryId"))
	if errors.Is(err, store.ErrSCIMNotFound) {
		scimError(w, http.StatusNotFound, "The directory was not found.", "")
		return "", "", "", false
	}
	if err != nil {
		scimError(w, http.StatusInternalServerError, "Directory lookup failed.", "")
		return "", "", "", false
	}
	return actorID, workspaceID, directoryID, true
}

// markManaged records that a provider writes to this directory the first time
// one does, which is what makes the organization SCIM-managed.
func (h *Handler) markManaged(r *http.Request, directoryID string) {
	if err := h.Store.MarkDirectorySCIMManaged(r.Context(), directoryID); err != nil {
		log.Printf("scim mark directory managed: %v", err)
	}
}

func (h *Handler) userLocation(directoryID, userID string) string {
	return strings.TrimRight(h.BaseURL, "/") + "/scim/directory/" + directoryID + "/Users/" + userID
}

func (h *Handler) groupLocation(directoryID, groupID string) string {
	return strings.TrimRight(h.BaseURL, "/") + "/scim/directory/" + directoryID + "/Groups/" + groupID
}

func (h *Handler) userBean(directoryID string, entry store.SCIMUser) map[string]any {
	groups := make([]map[string]any, 0, len(entry.Groups))
	for _, group := range entry.Groups {
		groups = append(groups, map[string]any{"value": group.ID, "display": group.Name, "$ref": h.groupLocation(directoryID, group.ID), "type": "direct"})
	}
	bean := map[string]any{
		"schemas":     []string{userSchema},
		"id":          entry.User.ID,
		"userName":    entry.User.Email,
		"displayName": entry.User.DisplayName,
		"active":      entry.User.Active,
		"emails":      []map[string]any{{"value": entry.User.Email, "type": "work", "primary": true}},
		"groups":      groups,
		"meta": map[string]any{
			"resourceType": "User",
			"created":      entry.Created.UTC().Format("2006-01-02T15:04:05Z"),
			"lastModified": entry.Updated.UTC().Format("2006-01-02T15:04:05Z"),
			"location":     h.userLocation(directoryID, entry.User.ID),
		},
	}
	if entry.ExternalID != "" {
		bean["externalId"] = entry.ExternalID
	}
	if entry.GivenName != "" || entry.FamilyName != "" {
		bean["name"] = map[string]any{"givenName": entry.GivenName, "familyName": entry.FamilyName, "formatted": entry.User.DisplayName}
	}
	return bean
}

func (h *Handler) groupBean(directoryID string, entry store.SCIMGroup) map[string]any {
	members := make([]map[string]any, 0, len(entry.Members))
	for _, member := range entry.Members {
		members = append(members, map[string]any{"value": member.UserID, "display": member.DisplayName, "$ref": h.userLocation(directoryID, member.UserID), "type": "User"})
	}
	bean := map[string]any{
		"schemas":     []string{groupSchema},
		"id":          entry.Group.ID,
		"displayName": entry.Group.Name,
		"members":     members,
		"meta": map[string]any{
			"resourceType": "Group",
			"created":      entry.Created.UTC().Format("2006-01-02T15:04:05Z"),
			"lastModified": entry.Updated.UTC().Format("2006-01-02T15:04:05Z"),
			"location":     h.groupLocation(directoryID, entry.Group.ID),
		},
	}
	if entry.ExternalID != "" {
		bean["externalId"] = entry.ExternalID
	}
	return bean
}

// listResponse is SCIM's page envelope. startIndex is one-based.
func listResponse(resources []map[string]any, startIndex, count int) map[string]any {
	total := len(resources)
	if startIndex < 1 {
		startIndex = 1
	}
	if count < 0 {
		count = 0
	}
	if count == 0 || count > maximumPage {
		count = maximumPage
	}
	page := []map[string]any{}
	if startIndex <= total {
		end := startIndex - 1 + count
		if end > total {
			end = total
		}
		page = resources[startIndex-1 : end]
	}
	return map[string]any{
		"schemas":      []string{listResponseSchema},
		"totalResults": total,
		"startIndex":   startIndex,
		"itemsPerPage": len(page),
		"Resources":    page,
	}
}

func pageParameters(r *http.Request) (startIndex, count int, err error) {
	startIndex, count = 1, maximumPage
	if raw := strings.TrimSpace(r.URL.Query().Get("startIndex")); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 {
			return 0, 0, errors.New("startIndex must be a positive integer")
		}
		startIndex = value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("count")); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 0 {
			return 0, 0, errors.New("count must be zero or a positive integer")
		}
		count = value
	}
	return startIndex, count, nil
}

// equalityFilter reads the filters identity providers actually send: one
// attribute equal to one value. Anything else is refused as SCIM asks, rather
// than answered with a list that quietly ignored the filter.
func equalityFilter(raw string) (attribute, value string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	parts := strings.SplitN(raw, " ", 3)
	if len(parts) != 3 || !strings.EqualFold(parts[1], "eq") {
		return "", "", errors.New("only filters of the form attribute eq \"value\" are supported")
	}
	value = strings.TrimSpace(parts[2])
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		value = value[1 : len(value)-1]
	}
	return strings.ToLower(strings.TrimSpace(parts[0])), value, nil
}

func decodeBody(r *http.Request, destination any) error {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return errors.New("the request body could not be read")
	}
	if len(body) == 0 {
		return errors.New("a request body is required")
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return errors.New("the request body is not valid JSON")
	}
	return nil
}

// writeStoreError turns a store refusal into the SCIM answer a provider can
// act on: a conflict it should resolve, a validation error it should fix, or
// a failure it may retry.
func (h *Handler) writeStoreError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, store.ErrSCIMNotFound):
		scimError(w, http.StatusNotFound, "The resource was not found.", "")
	case errors.Is(err, store.ErrAdminConflict):
		scimError(w, http.StatusConflict, message(err), "uniqueness")
	case errors.Is(err, store.ErrAdminValidation):
		scimError(w, http.StatusBadRequest, message(err), "invalidValue")
	case errors.Is(err, store.ErrAdminNotFound):
		scimError(w, http.StatusNotFound, "The resource was not found.", "")
	default:
		log.Printf("scim: %v", err)
		scimError(w, http.StatusInternalServerError, fallback, "")
	}
}

// message is a store error without the sentinel it wraps, which is the part
// that says what to do about it.
func message(err error) string {
	text := err.Error()
	if _, rest, found := strings.Cut(text, ": "); found {
		return strings.TrimSpace(rest)
	}
	return text
}

// patchOperation is one step of a SCIM PATCH.
type patchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

func (operation patchOperation) stringValue() string {
	var text string
	if json.Unmarshal(operation.Value, &text) == nil {
		return strings.TrimSpace(text)
	}
	return ""
}

func (operation patchOperation) boolValue() (bool, bool) {
	var value bool
	if json.Unmarshal(operation.Value, &value) == nil {
		return value, true
	}
	// Providers have been known to send the string "False".
	var text string
	if json.Unmarshal(operation.Value, &text) == nil {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func (operation patchOperation) decodeValue(destination any) error {
	if len(operation.Value) == 0 {
		return errors.New("the patch carries no value")
	}
	return json.Unmarshal(operation.Value, destination)
}

// patchOperations reads a PATCH body: the operations it carries, in order.
// remove without a value is an operation in its own right, so an empty value
// is not an error.
func patchOperations(r *http.Request) ([]patchOperation, error) {
	var request struct {
		Schemas    []string         `json:"schemas"`
		Operations []patchOperation `json:"Operations"`
	}
	if err := decodeBody(r, &request); err != nil {
		return nil, err
	}
	if len(request.Operations) == 0 {
		return nil, errors.New("a patch carries at least one operation")
	}
	for index, operation := range request.Operations {
		switch strings.ToLower(strings.TrimSpace(operation.Op)) {
		case "add", "replace", "remove":
			request.Operations[index].Op = strings.ToLower(strings.TrimSpace(operation.Op))
		default:
			return nil, errors.New("a patch operation is add, replace or remove")
		}
	}
	return request.Operations, nil
}
