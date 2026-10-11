package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/store"
)

func TestPlanTeamMembershipPreservesDirectoryAndChecksAccess(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx := context.Background()
	ws := f.handler.WorkspaceSlug
	var planID, otherPlanID int64
	for _, id := range []*int64{&planID, &otherPlanID} {
		err := f.store.Pool.QueryRow(ctx, `INSERT INTO plans(workspace_id,name,lead_account_id,scheduling,exclusion_rules,permissions,created_by)
			VALUES($1,'Team setup',$2,'{"estimation":"StoryPoints"}','{}',jsonb_build_array(jsonb_build_object('type','View','holderType','AccountId','holder',$3::text)),$2) RETURNING id`, ws, f.ownerID, f.otherID).Scan(id)
		if err != nil {
			t.Fatal(err)
		}
	}
	teamID, err := f.store.CreateAtlassianTeam(ctx, ws, f.ownerID, "Platform", "")
	if err != nil {
		t.Fatal(err)
	}
	post := func(user string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/plans/"+strconv.FormatInt(planID, 10)+"/teams/membership", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[user]})
		r.SetPathValue("id", strconv.FormatInt(planID, 10))
		w := httptest.NewRecorder()
		f.handler.PlanTeamMembership(w, r)
		if w.Code != want {
			t.Fatalf("team mutation: got %d, want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	add := url.Values{"action": {"add-atlassian"}, "atlassianTeam": {teamID}}
	post(f.otherID, add, http.StatusForbidden)
	post(f.ownerID, add, http.StatusSeeOther)
	post(f.ownerID, add, http.StatusBadRequest)
	teams, total, err := f.store.PlanTeams(ctx, ws, planID, 0, 100)
	if err != nil || total != 1 {
		t.Fatalf("teams after refused/duplicate writes: %v, %d, %v", teams, total, err)
	}
	remove := url.Values{"action": {"remove"}, "team": {strconv.FormatInt(teams[0].ID, 10)}}
	post(f.otherID, remove, http.StatusForbidden)
	foreignID, err := f.store.SavePlanTeam(ctx, ws, otherPlanID, store.PlanTeam{Name: "Other plan", PlanningStyle: "Kanban"}, true)
	if err != nil {
		t.Fatal(err)
	}
	post(f.ownerID, url.Values{"action": {"remove"}, "team": {strconv.FormatInt(foreignID, 10)}}, http.StatusBadRequest)
	if _, err := f.store.PlanTeam(ctx, ws, otherPlanID, "", foreignID); err != nil {
		t.Fatal("removing from one plan changed another:", err)
	}
	post(f.ownerID, remove, http.StatusSeeOther)
	if _, total, err := f.store.PlanTeams(ctx, ws, planID, 0, 100); err != nil || total != 0 {
		t.Fatalf("team not removed: %d, %v", total, err)
	}
	if _, err := f.store.AtlassianTeam(ctx, ws, teamID); err != nil {
		t.Fatal("plan removal deleted the directory team:", err)
	}

	for _, capacity := range []string{"-1", "NaN", "+Inf"} {
		form := url.Values{"action": {"add-planonly"}, "name": {"Mobile"}, "planningStyle": {"Kanban"}, "capacity": {capacity}, "member": {f.ownerID}}
		w := post(f.ownerID, form, http.StatusBadRequest)
		if !strings.Contains(w.Body.String(), `value="Mobile"`) || !strings.Contains(w.Body.String(), `value="Kanban" selected`) || !strings.Contains(w.Body.String(), `value="`+f.ownerID+`" checked`) {
			t.Fatal("refused team form lost its name, planning style or selected members")
		}
	}
	post(f.ownerID, url.Values{"action": {"add-planonly"}, "name": {"Mobile"}, "planningStyle": {"Kanban"}, "capacity": {"12"}, "member": {f.ownerID}}, http.StatusSeeOther)
	teams, total, err = f.store.PlanTeams(ctx, ws, planID, 0, 100)
	if err != nil || total != 1 || teams[0].Name != "Mobile" || teams[0].Capacity == nil || *teams[0].Capacity != 12 || len(teams[0].MemberIDs) != 1 {
		t.Fatalf("plan-only team not saved: %+v, %d, %v", teams, total, err)
	}
	if _, err := f.store.Pool.Exec(ctx, `UPDATE plans SET permissions=jsonb_build_array(jsonb_build_object('type','Edit','holderType','AccountId','holder',$2::text)) WHERE id=$1`, planID, f.otherID); err != nil {
		t.Fatal(err)
	}
	post(f.otherID, url.Values{"action": {"add-planonly"}, "name": {"Editor team"}, "planningStyle": {"Scrum"}}, http.StatusSeeOther)
	if _, total, err := f.store.PlanTeams(ctx, ws, planID, 0, 100); err != nil || total != 2 {
		t.Fatalf("a plan editor could not add a team: %d, %v", total, err)
	}
	if _, err := f.store.Pool.Exec(ctx, `INSERT INTO plan_teams(plan_id,name,planning_style) SELECT $1,'Team '||n,'Kanban' FROM generate_series(1,101) n`, planID); err != nil {
		t.Fatal(err)
	}
	plan, err := f.store.Plan(ctx, ws, planID)
	if err != nil {
		t.Fatal(err)
	}
	work, err := f.store.PlanWork(ctx, ws, f.ownerID, plan, time.Now())
	if err != nil || len(work.Teams) != 103 {
		t.Fatalf("plan truncated its teams: %d, %v", len(work.Teams), err)
	}
	last := work.Teams[len(work.Teams)-1]
	w := f.post(t, func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", strconv.FormatInt(planID, 10))
		f.handler.PlanTeamSettings(w, r)
	}, f.ownerID, "/plans/"+strconv.FormatInt(planID, 10)+"/teams", "", url.Values{"team": {strconv.FormatInt(last.ID, 10)}, "planningStyle": {"Kanban"}, "capacity": {"45"}})
	if w.Code != http.StatusSeeOther || strings.Contains(w.Header().Get("Location"), "error=") {
		t.Fatal("team settings beyond the first page were refused:", w.Code, w.Header().Get("Location"))
	}
	saved, err := f.store.PlanTeamByID(ctx, ws, planID, last.ID)
	if err != nil || saved.Capacity == nil || *saved.Capacity != 45 {
		t.Fatalf("team beyond the first page was not changed: %+v, %v", saved, err)
	}
}
