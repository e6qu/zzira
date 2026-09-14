package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// teamIDPattern matches an Atlassian team id.
var teamIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type teamsPageData struct {
	Teams             []store.AtlassianTeam
	Name, Description string
	Error             string
}

type teamPageData struct {
	Team       store.AtlassianTeam
	Members    []*models.User
	Candidates []*models.User
	CanEdit    bool
	Saved      string
	Error      string
}

// TeamsPage lists the site's Atlassian teams and creates new ones; anyone on
// the site can start a team.
func (h *Handler) TeamsPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	data := teamsPageData{}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		id, err := h.Store.CreateAtlassianTeam(r.Context(), workspaceID, user.ID, r.PostFormValue("name"), r.PostFormValue("description"))
		if err == nil {
			http.Redirect(w, r, "/teams/"+url.PathEscape(id)+"?saved="+url.QueryEscape("Team created"), http.StatusSeeOther)
			return
		}
		if !errors.Is(err, store.ErrPlanValidation) {
			log.Printf("create team: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		data.Error = strings.TrimPrefix(err.Error(), store.ErrPlanValidation.Error()+": ")
		data.Name, data.Description = r.PostFormValue("name"), r.PostFormValue("description")
		status = http.StatusBadRequest
	}
	teams, err := h.Store.AtlassianTeams(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data.Teams = teams
	h.writeWorkspacePageStatus(w, r, "page_teams", user, workspaceID, data, "people", "", status)
}

// TeamPage shows a team; its members and site administrators manage it.
func (h *Handler) TeamPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	teamID := r.PathValue("id")
	if !teamIDPattern.MatchString(teamID) {
		http.NotFound(w, r)
		return
	}
	team, err := h.Store.AtlassianTeam(r.Context(), workspaceID, teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Store, workspaceID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	canEdit := admin
	for _, member := range team.MemberIDs {
		canEdit = canEdit || member == user.ID
	}
	data := teamPageData{CanEdit: canEdit, Saved: r.URL.Query().Get("saved")}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !canEdit {
			http.Error(w, "Only team members and site administrators can change this team.", http.StatusForbidden)
			return
		}
		if !parseForm(w, r) {
			return
		}
		switch r.PostFormValue("action") {
		case "add", "remove":
			err = h.Store.SetAtlassianTeamMember(r.Context(), workspaceID, teamID, r.PostFormValue("accountId"), r.PostFormValue("action") == "add")
			if err == nil {
				message := "Member added"
				if r.PostFormValue("action") == "remove" {
					message = "Member removed"
				}
				http.Redirect(w, r, "/teams/"+url.PathEscape(team.ID)+"?saved="+url.QueryEscape(message), http.StatusSeeOther)
				return
			}
		case "delete":
			if err = h.Store.DeleteAtlassianTeam(r.Context(), workspaceID, teamID); err == nil {
				http.Redirect(w, r, "/teams", http.StatusSeeOther)
				return
			}
		default:
			http.Error(w, "unknown team action", http.StatusBadRequest)
			return
		}
		if !errors.Is(err, store.ErrPlanValidation) {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		data.Error = strings.TrimPrefix(err.Error(), store.ErrPlanValidation.Error()+": ")
		data.Saved = ""
		status = http.StatusBadRequest
	}
	people, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	members := map[string]bool{}
	for _, member := range team.MemberIDs {
		members[member] = true
	}
	for _, person := range people {
		if members[person.ID] {
			data.Members = append(data.Members, person)
		} else if person.Active {
			data.Candidates = append(data.Candidates, person)
		}
	}
	data.Team = team
	h.writeWorkspacePageStatus(w, r, "page_team", user, workspaceID, data, "people", "", status)
}

type serviceRegistryPageData struct {
	Services []store.ServiceRegistryService
	Tiers    []store.ServiceRegistryTier
	CanEdit  bool
	Saved    string
	Error    string
}

// ServiceRegistryPage lists the services the site operates; site
// administrators maintain them.
func (h *Handler) ServiceRegistryPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Store, workspaceID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := serviceRegistryPageData{CanEdit: admin, Saved: r.URL.Query().Get("saved")}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !admin {
			http.Error(w, "Only site administrators can change services.", http.StatusForbidden)
			return
		}
		if !parseForm(w, r) {
			return
		}
		tier, _ := strconv.Atoi(r.PostFormValue("tier"))
		id := r.PathValue("id")
		message := ""
		switch {
		case id == "":
			_, err = h.Store.CreateServiceRegistryService(r.Context(), workspaceID, user.ID, r.PostFormValue("name"), r.PostFormValue("description"), tier)
			message = "Service created"
		case r.PostFormValue("action") == "delete":
			err = h.Store.DeleteServiceRegistryService(r.Context(), workspaceID, id)
			message = "Service deleted"
		default:
			err = h.Store.UpdateServiceRegistryService(r.Context(), workspaceID, id, r.PostFormValue("name"), r.PostFormValue("description"), tier)
			message = "Service updated"
		}
		if err == nil {
			http.Redirect(w, r, "/service-registry?saved="+url.QueryEscape(message), http.StatusSeeOther)
			return
		}
		if !errors.Is(err, store.ErrServiceRegistryValidation) {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		data.Error = strings.TrimPrefix(err.Error(), store.ErrServiceRegistryValidation.Error()+": ")
		data.Saved = ""
		status = http.StatusBadRequest
	}
	if data.Services, err = h.Store.ServiceRegistryServices(r.Context(), workspaceID, nil); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if data.Tiers, err = h.Store.ServiceRegistryTiers(r.Context()); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePageStatus(w, r, "page_service_registry", user, workspaceID, data, "service", "", status)
}
