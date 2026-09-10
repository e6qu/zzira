package web

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/models"
)

const projectPreferenceCookie = "zzira_project"

type projectNavigationItem struct {
	Project        *models.Project
	Board          *models.Board
	OverviewURL    string
	WorkURL        string
	CreateURL      string
	BoardURL       string
	BacklogURL     string
	BacklogEnabled bool
	ReportsEnabled bool
}

type workspaceNavigation struct {
	Projects                []projectNavigationItem
	AppModules              []models.AppModule
	ProjectAppModules       []models.AppModule
	ProjectAdminAppModules  []models.AppModule
	AdminAppModules         []models.AppModule
	Current                 *projectNavigationItem
	CanAdmin                bool
	CanManageCurrentProject bool
	CanServiceAgent         bool
}

// workspaceNavigation builds the project-aware application shell. preferred
// may be either a project ID or key; a valid remembered key is used on pages
// that do not otherwise carry project context.
func (h *Handler) workspaceNavigation(r *http.Request, workspaceID, preferred string) (*workspaceNavigation, error) {
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list projects for navigation: %w", err)
	}
	boards, err := h.Store.BoardsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list boards for navigation: %w", err)
	}
	appModules, err := h.Store.AppNavigationModules(r.Context(), workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list app modules for navigation: %w", err)
	}
	projectAppModules, err := h.Store.AppModulesByLocation(r.Context(), workspaceID, "jira.project.page")
	if err != nil {
		return nil, fmt.Errorf("list project app modules for navigation: %w", err)
	}
	for index := range projectAppModules {
		projectAppModules[index].IconURL = appModuleIconPath(projectAppModules[index])
	}
	projectAdminAppModules, err := h.Store.AppModulesByLocation(r.Context(), workspaceID, "jira.project.settings")
	if err != nil {
		return nil, fmt.Errorf("list project admin app modules for navigation: %w", err)
	}
	adminAppModules, err := h.Store.AppModulesByLocation(r.Context(), workspaceID, "jira.admin")
	if err != nil {
		return nil, fmt.Errorf("list admin app modules for navigation: %w", err)
	}

	firstBoard := make(map[string]*models.Board, len(boards))
	for _, board := range boards {
		if firstBoard[board.ProjectID] == nil {
			firstBoard[board.ProjectID] = board
		}
	}

	navigation := &workspaceNavigation{Projects: make([]projectNavigationItem, 0, len(projects)), AppModules: appModules, ProjectAppModules: projectAppModules, ProjectAdminAppModules: projectAdminAppModules, AdminAppModules: adminAppModules}
	currentUser := h.currentUser(r)
	if currentUser != nil {
		navigation.CanAdmin, err = h.Store.IsAdmin(r.Context(), workspaceID, currentUser.ID)
		if err != nil {
			return nil, err
		}
		navigation.CanServiceAgent, err = h.Store.IsAnyServiceAgent(r.Context(), workspaceID, currentUser.ID)
		if err != nil {
			return nil, err
		}
	}
	for _, project := range projects {
		item := projectNavigationItem{
			Project:        project,
			Board:          firstBoard[project.ID],
			OverviewURL:    "/projects/" + url.PathEscape(project.Key),
			WorkURL:        "/issues/" + url.PathEscape(project.Key),
			CreateURL:      "/issues/new?project=" + url.QueryEscape(project.Key),
			BacklogEnabled: true,
			ReportsEnabled: true,
		}
		if project.ProjectTypeKey == "software" {
			features, featureErr := h.Store.ProjectFeatures(r.Context(), workspaceID, project.ID)
			if featureErr != nil {
				return nil, fmt.Errorf("load features for project %s: %w", project.Key, featureErr)
			}
			for _, feature := range features {
				switch feature.Key {
				case "jsw.classic.backlog":
					item.BacklogEnabled = feature.State == "ENABLED"
				case "jsw.classic.reports":
					item.ReportsEnabled = feature.State == "ENABLED"
				}
			}
		}
		if item.Board != nil {
			item.BoardURL = "/board/" + url.PathEscape(item.Board.ID)
			item.BacklogURL = item.BoardURL + "/backlog"
		}
		navigation.Projects = append(navigation.Projects, item)
	}

	selection := strings.TrimSpace(preferred)
	if selection == "" {
		if cookie, cookieErr := r.Cookie(projectPreferenceCookie); cookieErr == nil {
			selection = cookie.Value
		}
	}
	navigation.Current = selectCurrentProject(navigation.Projects, selection)
	if currentUser != nil && navigation.Current != nil {
		navigation.CanManageCurrentProject, err = h.Store.CanAdministerProject(r.Context(), workspaceID, currentUser.ID, navigation.Current.Project.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve project administration: %w", err)
		}
	}
	return navigation, nil
}

func selectCurrentProject(projects []projectNavigationItem, selection string) *projectNavigationItem {
	for index := range projects {
		project := projects[index].Project
		if project.ID == selection || strings.EqualFold(project.Key, selection) {
			return &projects[index]
		}
	}
	if len(projects) > 0 {
		return &projects[0]
	}
	return nil
}

func rememberCurrentProject(w http.ResponseWriter, navigation *workspaceNavigation) {
	if navigation == nil || navigation.Current == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is deployment-configured consistently with the session cookie.
		Name: projectPreferenceCookie, Value: navigation.Current.Project.Key, Path: "/",
		HttpOnly: true, Secure: authn.SecureCookies(), SameSite: http.SameSiteLaxMode,
		MaxAge: int((365 * 24 * time.Hour).Seconds()),
	})
}

func (h *Handler) writeWorkspacePage(w http.ResponseWriter, r *http.Request, name string, user *models.User, workspaceID string, data any, active, preferredProject string) {
	h.writeWorkspacePageStatus(w, r, name, user, workspaceID, data, active, preferredProject, http.StatusOK)
}

func (h *Handler) writeWorkspacePageStatus(w http.ResponseWriter, r *http.Request, name string, user *models.User, workspaceID string, data any, active, preferredProject string, status int) {
	navigation, err := h.workspaceNavigation(r, workspaceID, preferredProject)
	if err != nil {
		log.Printf("render %s navigation: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if preferredProject != "" {
		rememberCurrentProject(w, navigation)
	}
	var announcement *models.AnnouncementBanner
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
	if err != nil {
		log.Printf("render %s site configuration: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if configuration.Announcement.IsEnabled {
		banner := configuration.Announcement
		announcement = &banner
	}
	writePageStatus(w, name, pageData{User: user, Data: data, Active: active, Navigation: navigation, Announcement: announcement}, status)
}
