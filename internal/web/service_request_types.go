package web

import (
	"net/http"

	"github.com/e6qu/zzira/internal/models"
)

// serviceRequestTypeGroupView is one portal group as the agent workspace shows
// it: the group, the request types it shows in portal order, and whether it is
// already first or last among the desk's groups.
type serviceRequestTypeGroupView struct {
	Group        models.ServiceRequestTypeGroup
	RequestTypes []serviceRequestTypeOrderView
	First, Last  bool
}

// serviceRequestTypeOrderView is a request type inside one group, with whether
// it already starts or ends that group.
type serviceRequestTypeOrderView struct {
	RequestType models.ServiceRequestType
	First, Last bool
}

// serviceRequestTypeGroupChoice is a group a request type may be put in.
type serviceRequestTypeGroupChoice struct {
	ID, Name string
	Member   bool
}

// serviceRequestTypeAdminView is one request type as its administrator edits
// it: its text, the work type it is raised as, and the groups it appears in.
type serviceRequestTypeAdminView struct {
	RequestType models.ServiceRequestType
	WorkType    string
	Groups      []serviceRequestTypeGroupChoice
}

// serviceRequestTypeAdmin reads what the request type settings of a service
// desk show: the portal's groups in order with their request types, the
// request types that are in no group and so are off the portal, and the work
// types a new request type can be raised as.
func (h *Handler) serviceRequestTypeAdmin(r *http.Request, workspaceID string, desk *models.ServiceDesk, data *servicePageData) error {
	portal, err := h.Store.ServicePortalGroups(r.Context(), workspaceID, desk.ID, "")
	if err != nil {
		return err
	}
	groups := make([]serviceRequestTypeGroupView, 0, len(portal))
	for index, group := range portal {
		view := serviceRequestTypeGroupView{Group: group.Group, First: index == 0, Last: index == len(portal)-1}
		for position, requestType := range group.RequestTypes {
			view.RequestTypes = append(view.RequestTypes, serviceRequestTypeOrderView{
				RequestType: requestType, First: position == 0, Last: position == len(group.RequestTypes)-1})
		}
		groups = append(groups, view)
	}
	data.RequestTypeGroups = groups
	data.UngroupedRequestTypes, err = h.Store.ServiceUngroupedRequestTypes(r.Context(), workspaceID, desk.ID)
	if err != nil {
		return err
	}
	workTypes, err := h.Store.ProjectIssueTypes(r.Context(), workspaceID, desk.ProjectID, nil)
	if err != nil {
		return err
	}
	names := make(map[string]string, len(workTypes))
	for _, workType := range workTypes {
		if workType.Subtask {
			continue
		}
		data.WorkTypes = append(data.WorkTypes, workType)
		names[workType.ID] = workType.Name
	}
	for _, requestType := range data.RequestTypes {
		view := serviceRequestTypeAdminView{RequestType: requestType, WorkType: names[requestType.IssueTypeID]}
		for _, group := range portal {
			member := false
			for _, id := range requestType.GroupIDs {
				member = member || id == group.Group.ID
			}
			view.Groups = append(view.Groups, serviceRequestTypeGroupChoice{ID: group.Group.ID, Name: group.Group.Name, Member: member})
		}
		data.RequestTypeAdmin = append(data.RequestTypeAdmin, view)
	}
	return nil
}

// requireServiceDeskAdmin admits the people who administer a service desk, the
// permission Jira asks for request type settings.
func (h *Handler) requireServiceDeskAdmin(w http.ResponseWriter, r *http.Request, workspaceID, userID, deskID string) bool {
	admin, err := h.Store.IsServiceDeskAdmin(r.Context(), workspaceID, deskID, userID)
	if err != nil {
		http.Error(w, "Could not authorize service administration.", http.StatusInternalServerError)
		return false
	}
	if !admin {
		http.Error(w, "Service desk administrator access is required.", http.StatusForbidden)
		return false
	}
	return true
}

// ServiceRequestTypeSettings creates, edits, groups, orders and deletes the
// request types a portal offers.
func (h *Handler) ServiceRequestTypeSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	deskID := r.PathValue("desk")
	if !h.requireServiceDeskAdmin(w, r, workspaceID, user.ID, deskID) {
		return
	}
	if !parseForm(w, r) {
		return
	}
	requestTypeID := r.PostFormValue("requestTypeId")
	switch r.PostFormValue("action") {
	case "create":
		if _, err := h.Commands.CreateServiceRequestType(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("name"),
			r.PostFormValue("description"), r.PostFormValue("helpText"), r.PostFormValue("workTypeId"), r.PostForm["groupId"]); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "update":
		if err := h.Commands.UpdateServiceRequestType(r.Context(), user.ID, workspaceID, deskID, requestTypeID,
			r.PostFormValue("name"), r.PostFormValue("description"), r.PostFormValue("helpText")); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "groups":
		if err := h.Commands.SetServiceRequestTypeGroups(r.Context(), user.ID, workspaceID, deskID, requestTypeID, r.PostForm["groupId"]); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "move":
		if err := h.Commands.MoveServiceRequestType(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("groupId"),
			requestTypeID, r.PostFormValue("direction")); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "delete":
		if err := h.Commands.DeleteServiceRequestType(r.Context(), user.ID, workspaceID, deskID, requestTypeID); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	default:
		http.Error(w, "Choose a request type action.", http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#request-types")
}

// ServiceRequestTypeGroupSettings creates, renames, orders and deletes the
// groups the portal lists request types under.
func (h *Handler) ServiceRequestTypeGroupSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	deskID := r.PathValue("desk")
	if !h.requireServiceDeskAdmin(w, r, workspaceID, user.ID, deskID) {
		return
	}
	if !parseForm(w, r) {
		return
	}
	groupID := r.PostFormValue("groupId")
	switch r.PostFormValue("action") {
	case "create":
		if _, err := h.Commands.CreateServiceRequestTypeGroup(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("name")); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "rename":
		if err := h.Commands.RenameServiceRequestTypeGroup(r.Context(), user.ID, workspaceID, deskID, groupID, r.PostFormValue("name")); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "move":
		if err := h.Commands.MoveServiceRequestTypeGroup(r.Context(), user.ID, workspaceID, deskID, groupID, r.PostFormValue("direction")); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	case "delete":
		if err := h.Commands.DeleteServiceRequestTypeGroup(r.Context(), user.ID, workspaceID, deskID, groupID); err != nil {
			http.Error(w, err.Error(), commandErrorStatus(err))
			return
		}
	default:
		http.Error(w, "Choose a group action.", http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"#request-types")
}
