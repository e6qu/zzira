package scim

import (
	"errors"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/store"
)

// userRequest is the part of a SCIM user an identity provider sends that this
// directory can hold.
type userRequest struct {
	Schemas     []string `json:"schemas"`
	ExternalID  string   `json:"externalId"`
	UserName    string   `json:"userName"`
	DisplayName string   `json:"displayName"`
	Active      *bool    `json:"active"`
	Name        struct {
		GivenName  string `json:"givenName"`
		FamilyName string `json:"familyName"`
		Formatted  string `json:"formatted"`
	} `json:"name"`
	Emails []struct {
		Value   string `json:"value"`
		Primary bool   `json:"primary"`
		Type    string `json:"type"`
	} `json:"emails"`
}

// email is the address the provider means: userName, or the primary email when
// the provider sends a userName that is not one.
func (request userRequest) email() string {
	if strings.Contains(request.UserName, "@") {
		return strings.TrimSpace(request.UserName)
	}
	for _, email := range request.Emails {
		if email.Primary && strings.Contains(email.Value, "@") {
			return strings.TrimSpace(email.Value)
		}
	}
	for _, email := range request.Emails {
		if strings.Contains(email.Value, "@") {
			return strings.TrimSpace(email.Value)
		}
	}
	return ""
}

// name is what to call the person: displayName, the formatted name, the parts
// together, or the address before the @.
func (request userRequest) name() string {
	if display := strings.TrimSpace(request.DisplayName); display != "" {
		return display
	}
	if formatted := strings.TrimSpace(request.Name.Formatted); formatted != "" {
		return formatted
	}
	if joined := strings.TrimSpace(strings.TrimSpace(request.Name.GivenName) + " " + strings.TrimSpace(request.Name.FamilyName)); joined != "" {
		return joined
	}
	if email := request.email(); email != "" {
		return strings.SplitN(email, "@", 2)[0]
	}
	return ""
}

// Users lists or provisions the directory's people.
func (h *Handler) Users(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, directoryID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listUsers(w, r, directoryID)
	case http.MethodPost:
		h.createUser(w, r, actorID, workspaceID, directoryID)
	default:
		scimError(w, http.StatusMethodNotAllowed, "That method is not supported for Users.", "")
	}
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request, directoryID string) {
	startIndex, count, err := pageParameters(r)
	if err != nil {
		scimError(w, http.StatusBadRequest, err.Error(), "invalidValue")
		return
	}
	attribute, value, err := equalityFilter(r.URL.Query().Get("filter"))
	if err != nil {
		scimError(w, http.StatusBadRequest, err.Error(), "invalidFilter")
		return
	}
	userName, externalID := "", ""
	switch attribute {
	case "":
	case "username", "emails.value", "emails[type eq \"work\"].value":
		userName = value
	case "externalid":
		externalID = value
	default:
		scimError(w, http.StatusBadRequest, "Users can be filtered by userName or externalId.", "invalidFilter")
		return
	}
	users, err := h.Store.SCIMUsers(r.Context(), directoryID, userName, externalID)
	if err != nil {
		scimError(w, http.StatusInternalServerError, "The directory could not be read.", "")
		return
	}
	resources := make([]map[string]any, 0, len(users))
	for _, entry := range users {
		resources = append(resources, h.userBean(directoryID, entry))
	}
	writeSCIM(w, http.StatusOK, listResponse(resources, startIndex, count))
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request, actorID, workspaceID, directoryID string) {
	var request userRequest
	if err := decodeBody(r, &request); err != nil {
		scimError(w, http.StatusBadRequest, err.Error(), "invalidSyntax")
		return
	}
	email, name := request.email(), request.name()
	if email == "" {
		scimError(w, http.StatusBadRequest, "userName must be an email address, or the request must carry one.", "invalidValue")
		return
	}
	existing, err := h.Store.SCIMUsers(r.Context(), directoryID, email, "")
	if err != nil {
		scimError(w, http.StatusInternalServerError, "The directory could not be read.", "")
		return
	}
	if len(existing) > 0 {
		scimError(w, http.StatusConflict, "A user with that userName already exists in this directory.", "uniqueness")
		return
	}
	// A provisioned person signs in through the identity provider, so the
	// password they never set is one nobody can use.
	hash, err := authn.HashPassword(store.NewID("scim-provisioned"))
	if err != nil {
		scimError(w, http.StatusInternalServerError, "The person could not be provisioned.", "")
		return
	}
	user, err := h.Store.InviteDirectoryUserWithAccess(r.Context(), workspaceID, actorID, directoryID, email, name, hash, store.InviteOptions{})
	if err != nil {
		h.writeStoreError(w, err, "The person could not be provisioned.")
		return
	}
	if err := h.Store.SaveSCIMUserIdentity(r.Context(), directoryID, user.ID, request.ExternalID, request.Name.GivenName, request.Name.FamilyName); err != nil {
		h.writeStoreError(w, err, "The identity provider's own id could not be recorded.")
		return
	}
	if request.Active != nil && !*request.Active {
		if err := h.Store.SetDirectoryUserActive(r.Context(), workspaceID, actorID, directoryID, user.ID, false); err != nil {
			h.writeStoreError(w, err, "The person could not be deactivated.")
			return
		}
	}
	h.markManaged(r, directoryID)
	entry, err := h.Store.SCIMUserByID(r.Context(), directoryID, user.ID)
	if err != nil {
		scimError(w, http.StatusInternalServerError, "The provisioned person could not be read back.", "")
		return
	}
	w.Header().Set("Location", h.userLocation(directoryID, user.ID))
	writeSCIM(w, http.StatusCreated, h.userBean(directoryID, entry))
}

// User reads, replaces, patches or deprovisions one person.
func (h *Handler) User(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, directoryID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	userID := r.PathValue("userId")
	entry, err := h.Store.SCIMUserByID(r.Context(), directoryID, userID)
	if errors.Is(err, store.ErrSCIMNotFound) {
		scimError(w, http.StatusNotFound, "The user was not found.", "")
		return
	}
	if err != nil {
		scimError(w, http.StatusInternalServerError, "The directory could not be read.", "")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeSCIM(w, http.StatusOK, h.userBean(directoryID, entry))
	case http.MethodPut:
		h.replaceUser(w, r, actorID, workspaceID, directoryID, entry)
	case http.MethodPatch:
		h.patchUser(w, r, actorID, workspaceID, directoryID, entry)
	case http.MethodDelete:
		if err := h.Store.RemoveDirectoryUser(r.Context(), workspaceID, actorID, directoryID, userID); err != nil {
			h.writeStoreError(w, err, "The person could not be deprovisioned.")
			return
		}
		h.markManaged(r, directoryID)
		w.WriteHeader(http.StatusNoContent)
	default:
		scimError(w, http.StatusMethodNotAllowed, "That method is not supported for a user.", "")
	}
}

func (h *Handler) replaceUser(w http.ResponseWriter, r *http.Request, actorID, workspaceID, directoryID string, entry store.SCIMUser) {
	var request userRequest
	if err := decodeBody(r, &request); err != nil {
		scimError(w, http.StatusBadRequest, err.Error(), "invalidSyntax")
		return
	}
	if name := request.name(); name != "" && name != entry.User.DisplayName {
		if err := h.Store.UpdateDirectoryUserProfile(r.Context(), workspaceID, actorID, directoryID, entry.User.ID, store.ManagedProfileUpdate{DisplayName: name}); err != nil {
			h.writeStoreError(w, err, "The person could not be updated.")
			return
		}
	}
	if err := h.Store.SaveSCIMUserIdentity(r.Context(), directoryID, entry.User.ID, request.ExternalID, request.Name.GivenName, request.Name.FamilyName); err != nil {
		h.writeStoreError(w, err, "The identity provider's own id could not be recorded.")
		return
	}
	// A replace without active says nothing about it, so the person stays as
	// they are; SCIM's own deactivation is active:false.
	if request.Active != nil && *request.Active != entry.User.Active {
		if err := h.Store.SetDirectoryUserActive(r.Context(), workspaceID, actorID, directoryID, entry.User.ID, *request.Active); err != nil {
			h.writeStoreError(w, err, "The person's access could not be changed.")
			return
		}
	}
	h.markManaged(r, directoryID)
	h.writeUser(w, r, directoryID, entry.User.ID)
}

func (h *Handler) patchUser(w http.ResponseWriter, r *http.Request, actorID, workspaceID, directoryID string, entry store.SCIMUser) {
	operations, err := patchOperations(r)
	if err != nil {
		scimError(w, http.StatusBadRequest, err.Error(), "invalidSyntax")
		return
	}
	profile := store.ManagedProfileUpdate{DisplayName: entry.User.DisplayName}
	externalID, givenName, familyName := entry.ExternalID, entry.GivenName, entry.FamilyName
	active, changedActive, changedProfile, changedIdentity := entry.User.Active, false, false, false
	for _, operation := range operations {
		path := strings.ToLower(strings.TrimSpace(operation.Path))
		switch {
		case path == "active":
			value, ok := operation.boolValue()
			if !ok {
				scimError(w, http.StatusBadRequest, "active takes true or false.", "invalidValue")
				return
			}
			active, changedActive = value, true
		case path == "displayname":
			profile.DisplayName, changedProfile = operation.stringValue(), true
		case path == "name.givenname":
			givenName, changedIdentity = operation.stringValue(), true
		case path == "name.familyname":
			familyName, changedIdentity = operation.stringValue(), true
		case path == "externalid":
			externalID, changedIdentity = operation.stringValue(), true
		case path == "":
			// A patch with no path carries a fragment of the resource.
			var fragment userRequest
			if err := operation.decodeValue(&fragment); err != nil {
				scimError(w, http.StatusBadRequest, "The patch value is not a user fragment.", "invalidValue")
				return
			}
			if fragment.Active != nil {
				active, changedActive = *fragment.Active, true
			}
			if name := fragment.name(); name != "" {
				profile.DisplayName, changedProfile = name, true
			}
			if fragment.ExternalID != "" || fragment.Name.GivenName != "" || fragment.Name.FamilyName != "" {
				if fragment.ExternalID != "" {
					externalID = fragment.ExternalID
				}
				if fragment.Name.GivenName != "" {
					givenName = fragment.Name.GivenName
				}
				if fragment.Name.FamilyName != "" {
					familyName = fragment.Name.FamilyName
				}
				changedIdentity = true
			}
		default:
			scimError(w, http.StatusBadRequest, "A user patch can change active, displayName, name.givenName, name.familyName or externalId.", "invalidPath")
			return
		}
	}
	if changedProfile {
		if err := h.Store.UpdateDirectoryUserProfile(r.Context(), workspaceID, actorID, directoryID, entry.User.ID, profile); err != nil {
			h.writeStoreError(w, err, "The person could not be updated.")
			return
		}
	}
	if changedIdentity {
		if err := h.Store.SaveSCIMUserIdentity(r.Context(), directoryID, entry.User.ID, externalID, givenName, familyName); err != nil {
			h.writeStoreError(w, err, "The identity provider's own id could not be recorded.")
			return
		}
	}
	if changedActive && active != entry.User.Active {
		if err := h.Store.SetDirectoryUserActive(r.Context(), workspaceID, actorID, directoryID, entry.User.ID, active); err != nil {
			h.writeStoreError(w, err, "The person's access could not be changed.")
			return
		}
	}
	h.markManaged(r, directoryID)
	h.writeUser(w, r, directoryID, entry.User.ID)
}

func (h *Handler) writeUser(w http.ResponseWriter, r *http.Request, directoryID, userID string) {
	entry, err := h.Store.SCIMUserByID(r.Context(), directoryID, userID)
	if err != nil {
		scimError(w, http.StatusInternalServerError, "The person could not be read back.", "")
		return
	}
	writeSCIM(w, http.StatusOK, h.userBean(directoryID, entry))
}
