package web

import (
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/store"
)

type planTeamDraft struct {
	Action, Name, AtlassianID, PlanningStyle, Capacity string
	MemberIDs                                          map[string]bool
}

func readPlanTeamDraft(r *http.Request) planTeamDraft {
	draft := planTeamDraft{
		Action: r.PostFormValue("action"), Name: r.PostFormValue("name"),
		AtlassianID:   r.PostFormValue("atlassianTeam"),
		PlanningStyle: r.PostFormValue("planningStyle"), Capacity: r.PostFormValue("capacity"),
		MemberIDs: map[string]bool{},
	}
	for _, id := range r.PostForm["member"] {
		draft.MemberIDs[id] = true
	}
	return draft
}

// PlanTeamMembership adds an existing site team or creates a plan-only team.
// Removing a team here leaves the site directory team intact.
func (h *Handler) PlanTeamMembership(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	_, workspaceID, plan, edit, _, scenario, ok := h.planContext(w, r)
	if !ok {
		return
	}
	if !edit {
		http.Error(w, "You cannot edit this plan.", http.StatusForbidden)
		return
	}
	var err error
	notice := "Team added to the plan."
	switch r.PostFormValue("action") {
	case "remove":
		id, parseErr := strconv.ParseInt(r.PostFormValue("team"), 10, 64)
		if parseErr != nil || id <= 0 {
			http.Error(w, "Choose a team of the plan.", http.StatusBadRequest)
			return
		}
		err = h.Store.DeletePlanTeam(r.Context(), workspaceID, plan.ID, id)
		notice = "Team removed from the plan."
	case "add-atlassian":
		id := r.PostFormValue("atlassianTeam")
		if !teamIDPattern.MatchString(id) {
			h.renderPlanPage(w, r, "Choose an existing team.", http.StatusBadRequest)
			return
		}
		_, err = h.Store.SavePlanTeam(r.Context(), workspaceID, plan.ID,
			store.PlanTeam{AtlassianTeamID: id, PlanningStyle: "Scrum"}, true)
	case "add-planonly":
		team := store.PlanTeam{Name: r.PostFormValue("name"), PlanningStyle: r.PostFormValue("planningStyle"), MemberIDs: r.PostForm["member"]}
		if text := r.PostFormValue("capacity"); text != "" {
			value, parseErr := strconv.ParseFloat(text, 64)
			if parseErr != nil {
				h.renderPlanPage(w, r, "The capacity must be a number.", http.StatusBadRequest)
				return
			}
			team.Capacity = &value
		}
		_, err = h.Store.SavePlanTeam(r.Context(), workspaceID, plan.ID, team, true)
	default:
		http.Error(w, "Choose what to do with the team.", http.StatusBadRequest)
		return
	}
	if err != nil {
		if message := planErrorMessage(err); message != "" {
			h.renderPlanPage(w, r, message, http.StatusBadRequest)
			return
		}
		http.Error(w, "Could not change the plan's teams.", http.StatusInternalServerError)
		return
	}
	planBack(w, r, plan, scenario.ID, "", "notice", notice)
}
