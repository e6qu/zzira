package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type notificationEntryView struct {
	Entry          models.NotificationSchemeEntry
	EventName      string
	RecipientLabel string
}

type notificationSchemeCard struct {
	Scheme   *models.NotificationScheme
	Entries  []notificationEntryView
	Projects []*models.Project
}

type notificationSchemesData struct {
	Schemes  []notificationSchemeCard
	Events   []store.NotificationEventDefinition
	Projects []*models.Project
	Members  []*models.User
	Groups   []*models.Group
	Roles    []*models.ProjectRole
	Fields   []*models.CustomField
	Notice   string
	Error    string
}

type projectNotificationsData struct {
	Project *models.Project
	Scheme  notificationSchemeCard
}

func notificationMutationMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrNotificationSchemeValidation), errors.Is(err, store.ErrNotificationSchemeConflict), errors.Is(err, store.ErrNotificationSchemeNotFound), errors.Is(err, store.ErrProjectPermission):
		return err.Error()
	default:
		return "Could not update notification schemes."
	}
}

func notificationRecipientLabel(entry models.NotificationSchemeEntry, members []*models.User, groups []*models.Group, roles []*models.ProjectRole, fields []*models.CustomField) string {
	switch entry.NotificationType {
	case "CurrentAssignee":
		return "Current assignee"
	case "Reporter":
		return "Reporter"
	case "CurrentUser":
		return "Person who triggered the event"
	case "ProjectLead":
		return "Project lead"
	case "ComponentLead":
		return "Leads of selected components"
	case "AllWatchers":
		return "All watchers"
	case "EmailAddress":
		return entry.Parameter
	case "User":
		for _, member := range members {
			if member.ID == entry.Recipient {
				return member.DisplayName
			}
		}
	case "Group":
		for _, group := range groups {
			if group.ID == entry.Recipient {
				return group.Name
			}
		}
	case "ProjectRole":
		for _, role := range roles {
			if strconv.FormatInt(role.ID, 10) == entry.Recipient {
				return role.Name + " project role"
			}
		}
	case "UserCustomField", "GroupCustomField":
		for _, field := range fields {
			if field.ID == entry.Recipient {
				return field.Name + " (" + field.ID + ")"
			}
		}
	}
	if entry.Parameter != "" {
		return entry.Parameter
	}
	return entry.NotificationType
}

func notificationCards(schemes []*models.NotificationScheme, projects []*models.Project, members []*models.User, groups []*models.Group, roles []*models.ProjectRole, fields []*models.CustomField, mappings []store.NotificationSchemeMapping) []notificationSchemeCard {
	byScheme := map[int64]map[string]bool{}
	for _, mapping := range mappings {
		if byScheme[mapping.SchemeID] == nil {
			byScheme[mapping.SchemeID] = map[string]bool{}
		}
		byScheme[mapping.SchemeID][mapping.ProjectID] = true
	}
	cards := make([]notificationSchemeCard, 0, len(schemes))
	for _, scheme := range schemes {
		card := notificationSchemeCard{Scheme: scheme}
		for _, configured := range scheme.Events {
			event, _ := store.NotificationEvent(configured.EventID)
			for _, entry := range configured.Notifications {
				card.Entries = append(card.Entries, notificationEntryView{Entry: entry, EventName: event.Name, RecipientLabel: notificationRecipientLabel(entry, members, groups, roles, fields)})
			}
		}
		for _, project := range projects {
			if byScheme[scheme.ID][project.ID] {
				card.Projects = append(card.Projects, project)
			}
		}
		cards = append(cards, card)
	}
	return cards
}

func (h *Handler) loadNotificationSchemesPage(r *http.Request, workspaceID string) (notificationSchemesData, error) {
	data := notificationSchemesData{Events: store.NotificationEvents(), Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	var err error
	data.Projects, err = h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
	if err == nil {
		data.Members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID)
	}
	if err == nil {
		data.Groups, err = h.Store.GroupsByWorkspace(r.Context(), workspaceID)
	}
	if err == nil {
		data.Roles, err = h.Store.ProjectRoles(r.Context(), workspaceID)
	}
	if err == nil {
		data.Fields, err = h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
	}
	var schemes []*models.NotificationScheme
	if err == nil {
		schemes, err = h.Store.NotificationSchemes(r.Context(), workspaceID, true)
	}
	var mappings []store.NotificationSchemeMapping
	if err == nil {
		mappings, err = h.Store.NotificationSchemeMappings(r.Context(), workspaceID)
	}
	if err == nil {
		data.Schemes = notificationCards(schemes, data.Projects, data.Members, data.Groups, data.Roles, data.Fields, mappings)
	}
	return data, err
}

func (h *Handler) NotificationSchemesPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		_, err := h.Store.CreateNotificationScheme(r.Context(), workspaceID, user.ID, r.PostFormValue("name"), r.PostFormValue("description"), nil)
		if err != nil {
			redirectLocal(w, r, "/settings/notification-schemes?error="+url.QueryEscape(notificationMutationMessage(err)))
			return
		}
		redirectLocal(w, r, "/settings/notification-schemes?notice="+url.QueryEscape("Notification scheme created."))
		return
	}
	data, err := h.loadNotificationSchemesPage(r, workspaceID)
	if err != nil {
		http.Error(w, "Could not load notification schemes.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_notification_schemes", user, workspaceID, data, "notification-schemes", "")
}

func (h *Handler) NotificationSchemeMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	schemeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || schemeID <= 0 {
		http.NotFound(w, r)
		return
	}
	notice := "Notification scheme updated."
	switch r.PostFormValue("action") {
	case "update":
		name, description := r.PostFormValue("name"), r.PostFormValue("description")
		err = h.Store.UpdateNotificationScheme(r.Context(), workspaceID, user.ID, schemeID, &name, &description)
	case "delete":
		err = h.Store.DeleteNotificationScheme(r.Context(), workspaceID, user.ID, schemeID)
		notice = "Notification scheme deleted."
	case "add-notification":
		var eventID int64
		eventID, err = strconv.ParseInt(r.PostFormValue("eventId"), 10, 64)
		if err == nil {
			err = h.Store.AddNotificationEntries(r.Context(), workspaceID, user.ID, schemeID, []store.NotificationEntryInput{{EventID: eventID, NotificationType: r.PostFormValue("notificationType"), Parameter: r.PostFormValue("parameter")}})
		}
		notice = "Event recipient added."
	case "remove-notification":
		var entryID int64
		entryID, err = strconv.ParseInt(r.PostFormValue("notificationId"), 10, 64)
		if err == nil {
			err = h.Store.DeleteNotificationEntry(r.Context(), workspaceID, user.ID, schemeID, entryID)
		}
		notice = "Event recipient removed."
	case "assign-project":
		err = h.Store.AssignNotificationScheme(r.Context(), workspaceID, user.ID, r.PostFormValue("project"), schemeID)
		notice = "Project notification scheme assigned."
	default:
		err = store.ErrNotificationSchemeValidation
	}
	target := "/settings/notification-schemes"
	if err != nil {
		redirectLocal(w, r, target+"?error="+url.QueryEscape(notificationMutationMessage(err)))
		return
	}
	redirectLocal(w, r, target+"?notice="+url.QueryEscape(notice))
}

func (h *Handler) ProjectNotificationsPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, project, ok := h.requireProjectAdminPage(w, r)
	if !ok {
		return
	}
	scheme, _, err := h.Store.AssignedNotificationScheme(r.Context(), workspaceID, project.ID, true)
	if err != nil {
		http.Error(w, "Could not load project notifications.", http.StatusInternalServerError)
		return
	}
	members, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project notifications.", 500)
		return
	}
	groups, err := h.Store.GroupsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project notifications.", 500)
		return
	}
	roles, err := h.Store.ProjectRoles(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project notifications.", 500)
		return
	}
	fields, err := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project notifications.", 500)
		return
	}
	cards := notificationCards([]*models.NotificationScheme{scheme}, []*models.Project{project}, members, groups, roles, fields, []store.NotificationSchemeMapping{{SchemeID: scheme.ID, ProjectID: project.ID}})
	h.writeWorkspacePage(w, r, "page_project_notifications", user, workspaceID, projectNotificationsData{Project: project, Scheme: cards[0]}, "project-notifications", project.Key)
}
