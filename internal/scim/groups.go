package scim

import (
	"errors"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

// groupRequest is the part of a SCIM group an identity provider sends.
type groupRequest struct {
	Schemas     []string `json:"schemas"`
	ExternalID  string   `json:"externalId"`
	DisplayName string   `json:"displayName"`
	Members     []struct {
		Value   string `json:"value"`
		Display string `json:"display"`
		Type    string `json:"type"`
	} `json:"members"`
}

func (request groupRequest) memberIDs() []string {
	ids := make([]string, 0, len(request.Members))
	for _, member := range request.Members {
		if id := strings.TrimSpace(member.Value); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// Groups lists or provisions the directory's groups.
func (h *Handler) Groups(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, directoryID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listGroups(w, r, directoryID)
	case http.MethodPost:
		h.createGroup(w, r, actorID, workspaceID, directoryID)
	default:
		scimError(w, http.StatusMethodNotAllowed, "That method is not supported for Groups.", "")
	}
}

func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request, directoryID string) {
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
	displayName, externalID := "", ""
	switch attribute {
	case "":
	case "displayname":
		displayName = value
	case "externalid":
		externalID = value
	default:
		scimError(w, http.StatusBadRequest, "Groups can be filtered by displayName or externalId.", "invalidFilter")
		return
	}
	groups, err := h.Store.SCIMGroups(r.Context(), directoryID, displayName, externalID)
	if err != nil {
		scimError(w, http.StatusInternalServerError, "The directory could not be read.", "")
		return
	}
	resources := make([]map[string]any, 0, len(groups))
	for _, entry := range groups {
		resources = append(resources, h.groupBean(directoryID, entry))
	}
	writeSCIM(w, http.StatusOK, listResponse(resources, startIndex, count))
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request, actorID, workspaceID, directoryID string) {
	var request groupRequest
	if err := decodeBody(r, &request); err != nil {
		scimError(w, http.StatusBadRequest, err.Error(), "invalidSyntax")
		return
	}
	name := strings.TrimSpace(request.DisplayName)
	if name == "" {
		scimError(w, http.StatusBadRequest, "displayName is required.", "invalidValue")
		return
	}
	group, err := h.Store.CreateDirectoryGroup(r.Context(), workspaceID, actorID, directoryID, name, "")
	if err != nil {
		h.writeStoreError(w, err, "The group could not be provisioned.")
		return
	}
	if err := h.Store.SaveSCIMGroupIdentity(r.Context(), group.ID, request.ExternalID); err != nil {
		h.writeStoreError(w, err, "The identity provider's own id could not be recorded.")
		return
	}
	for _, userID := range request.memberIDs() {
		if err := h.Store.SetGroupMember(r.Context(), workspaceID, actorID, directoryID, group.ID, userID, true); err != nil {
			h.writeStoreError(w, err, "A member could not be added to the group.")
			return
		}
	}
	h.markManaged(r, directoryID)
	entry, err := h.Store.SCIMGroupByID(r.Context(), directoryID, group.ID)
	if err != nil {
		scimError(w, http.StatusInternalServerError, "The provisioned group could not be read back.", "")
		return
	}
	w.Header().Set("Location", h.groupLocation(directoryID, group.ID))
	writeSCIM(w, http.StatusCreated, h.groupBean(directoryID, entry))
}

// Group reads, replaces, patches or removes one group.
func (h *Handler) Group(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, directoryID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	groupID := r.PathValue("groupId")
	entry, err := h.Store.SCIMGroupByID(r.Context(), directoryID, groupID)
	if errors.Is(err, store.ErrSCIMNotFound) {
		scimError(w, http.StatusNotFound, "The group was not found.", "")
		return
	}
	if err != nil {
		scimError(w, http.StatusInternalServerError, "The directory could not be read.", "")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeSCIM(w, http.StatusOK, h.groupBean(directoryID, entry))
	case http.MethodPut:
		h.replaceGroup(w, r, actorID, workspaceID, directoryID, entry)
	case http.MethodPatch:
		h.patchGroup(w, r, actorID, workspaceID, directoryID, entry)
	case http.MethodDelete:
		if err := h.Store.DeleteDirectoryGroup(r.Context(), workspaceID, actorID, directoryID, groupID); err != nil {
			h.writeStoreError(w, err, "The group could not be removed.")
			return
		}
		h.markManaged(r, directoryID)
		w.WriteHeader(http.StatusNoContent)
	default:
		scimError(w, http.StatusMethodNotAllowed, "That method is not supported for a group.", "")
	}
}

// replaceGroup makes the group what the request says it is: its name, and
// exactly the members it lists.
func (h *Handler) replaceGroup(w http.ResponseWriter, r *http.Request, actorID, workspaceID, directoryID string, entry store.SCIMGroup) {
	var request groupRequest
	if err := decodeBody(r, &request); err != nil {
		scimError(w, http.StatusBadRequest, err.Error(), "invalidSyntax")
		return
	}
	if name := strings.TrimSpace(request.DisplayName); name != "" && name != entry.Group.Name {
		if err := h.Store.RenameDirectoryGroup(r.Context(), workspaceID, actorID, directoryID, entry.Group.ID, name); err != nil {
			h.writeStoreError(w, err, "The group could not be renamed.")
			return
		}
	}
	if err := h.Store.SaveSCIMGroupIdentity(r.Context(), entry.Group.ID, request.ExternalID); err != nil {
		h.writeStoreError(w, err, "The identity provider's own id could not be recorded.")
		return
	}
	if request.Members != nil {
		wanted := map[string]bool{}
		for _, userID := range request.memberIDs() {
			wanted[userID] = true
		}
		for _, member := range entry.Members {
			if !wanted[member.UserID] {
				if err := h.Store.SetGroupMember(r.Context(), workspaceID, actorID, directoryID, entry.Group.ID, member.UserID, false); err != nil {
					h.writeStoreError(w, err, "A member could not be removed from the group.")
					return
				}
			}
			delete(wanted, member.UserID)
		}
		for userID := range wanted {
			if err := h.Store.SetGroupMember(r.Context(), workspaceID, actorID, directoryID, entry.Group.ID, userID, true); err != nil {
				h.writeStoreError(w, err, "A member could not be added to the group.")
				return
			}
		}
	}
	h.markManaged(r, directoryID)
	h.writeGroup(w, r, directoryID, entry.Group.ID)
}

func (h *Handler) patchGroup(w http.ResponseWriter, r *http.Request, actorID, workspaceID, directoryID string, entry store.SCIMGroup) {
	operations, err := patchOperations(r)
	if err != nil {
		scimError(w, http.StatusBadRequest, err.Error(), "invalidSyntax")
		return
	}
	for _, operation := range operations {
		path := strings.ToLower(strings.TrimSpace(operation.Path))
		switch {
		case path == "displayname":
			name := operation.stringValue()
			if name == "" {
				scimError(w, http.StatusBadRequest, "displayName takes a name.", "invalidValue")
				return
			}
			if err := h.Store.RenameDirectoryGroup(r.Context(), workspaceID, actorID, directoryID, entry.Group.ID, name); err != nil {
				h.writeStoreError(w, err, "The group could not be renamed.")
				return
			}
		case path == "externalid":
			if err := h.Store.SaveSCIMGroupIdentity(r.Context(), entry.Group.ID, operation.stringValue()); err != nil {
				h.writeStoreError(w, err, "The identity provider's own id could not be recorded.")
				return
			}
		case path == "members", strings.HasPrefix(path, "members["):
			if err := h.patchMembers(r, actorID, workspaceID, directoryID, entry, operation, path); err != nil {
				h.writeStoreError(w, err, "The group's members could not be changed.")
				return
			}
		case path == "":
			var fragment groupRequest
			if err := operation.decodeValue(&fragment); err != nil {
				scimError(w, http.StatusBadRequest, "The patch value is not a group fragment.", "invalidValue")
				return
			}
			if name := strings.TrimSpace(fragment.DisplayName); name != "" && name != entry.Group.Name {
				if err := h.Store.RenameDirectoryGroup(r.Context(), workspaceID, actorID, directoryID, entry.Group.ID, name); err != nil {
					h.writeStoreError(w, err, "The group could not be renamed.")
					return
				}
			}
			for _, userID := range fragment.memberIDs() {
				if err := h.Store.SetGroupMember(r.Context(), workspaceID, actorID, directoryID, entry.Group.ID, userID, operation.Op != "remove"); err != nil {
					h.writeStoreError(w, err, "The group's members could not be changed.")
					return
				}
			}
		default:
			scimError(w, http.StatusBadRequest, "A group patch can change displayName, externalId or members.", "invalidPath")
			return
		}
	}
	h.markManaged(r, directoryID)
	h.writeGroup(w, r, directoryID, entry.Group.ID)
}

// patchMembers applies one members operation: add or remove the people it
// names, or, for a replace of the whole attribute, make the membership
// exactly what it lists.
func (h *Handler) patchMembers(r *http.Request, actorID, workspaceID, directoryID string, entry store.SCIMGroup, operation patchOperation, path string) error {
	// A provider removes one member by naming them in the path:
	// members[value eq "usr_1"].
	if inner, found := strings.CutPrefix(path, "members["); found {
		expression := strings.TrimSuffix(inner, "]")
		attribute, value, err := equalityFilter(expression)
		if err != nil || attribute != "value" || value == "" {
			return store.ErrAdminValidation
		}
		return h.Store.SetGroupMember(r.Context(), workspaceID, actorID, directoryID, entry.Group.ID, value, operation.Op != "remove")
	}
	var members []struct {
		Value string `json:"value"`
	}
	if len(operation.Value) > 0 {
		if err := operation.decodeValue(&members); err != nil {
			return store.ErrAdminValidation
		}
	}
	if operation.Op == "remove" && len(members) == 0 {
		// remove members with no value empties the group.
		for _, member := range entry.Members {
			if err := h.Store.SetGroupMember(r.Context(), workspaceID, actorID, directoryID, entry.Group.ID, member.UserID, false); err != nil {
				return err
			}
		}
		return nil
	}
	wanted := map[string]bool{}
	for _, member := range members {
		if id := strings.TrimSpace(member.Value); id != "" {
			wanted[id] = true
		}
	}
	if operation.Op == "replace" {
		for _, member := range entry.Members {
			if !wanted[member.UserID] {
				if err := h.Store.SetGroupMember(r.Context(), workspaceID, actorID, directoryID, entry.Group.ID, member.UserID, false); err != nil {
					return err
				}
			}
		}
	}
	for id := range wanted {
		if err := h.Store.SetGroupMember(r.Context(), workspaceID, actorID, directoryID, entry.Group.ID, id, operation.Op != "remove"); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) writeGroup(w http.ResponseWriter, r *http.Request, directoryID, groupID string) {
	entry, err := h.Store.SCIMGroupByID(r.Context(), directoryID, groupID)
	if err != nil {
		scimError(w, http.StatusInternalServerError, "The group could not be read back.", "")
		return
	}
	writeSCIM(w, http.StatusOK, h.groupBean(directoryID, entry))
}
