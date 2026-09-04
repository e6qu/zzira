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
	Project     *models.Project
	Board       *models.Board
	OverviewURL string
	WorkURL     string
	CreateURL   string
	BoardURL    string
	BacklogURL  string
}

type workspaceNavigation struct {
	Projects []projectNavigationItem
	Current  *projectNavigationItem
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

	firstBoard := make(map[string]*models.Board, len(boards))
	for _, board := range boards {
		if firstBoard[board.ProjectID] == nil {
			firstBoard[board.ProjectID] = board
		}
	}

	navigation := &workspaceNavigation{Projects: make([]projectNavigationItem, 0, len(projects))}
	for _, project := range projects {
		item := projectNavigationItem{
			Project:     project,
			Board:       firstBoard[project.ID],
			OverviewURL: "/projects/" + url.PathEscape(project.Key),
			WorkURL:     "/issues/" + url.PathEscape(project.Key),
			CreateURL:   "/issues/new?project=" + url.QueryEscape(project.Key),
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
	writePageStatus(w, name, pageData{User: user, Data: data, Active: active, Navigation: navigation}, status)
}
