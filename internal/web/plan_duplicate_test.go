package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestPlanDuplicationChecksAdminAndRetainsRefusedName(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: fmt.Sprintf("PD%d", time.Now().UnixNano()%100000000), Name: "Copy sources", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := strconv.ParseInt(project.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM plans WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, query, ws); err != nil {
				t.Error(err)
			}
		}
	})
	planID, err := f.store.CreatePlan(ctx, ws, f.ownerID, store.Plan{Name: "Original plan", LeadAccountID: f.ownerID, Scheduling: store.PlanScheduling{Estimation: "StoryPoints"}, IssueSources: []store.PlanIssueSource{{Type: "Project", Value: projectID}}, Permissions: []store.PlanPermission{{Type: "Edit", HolderType: "AccountId", Holder: f.otherID}}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/plans/" + strconv.FormatInt(planID, 10)
	call := func(user, method, suffix string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path+suffix, strings.NewReader(form.Encode()))
		r.SetPathValue("id", strconv.FormatInt(planID, 10))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[user]})
		w := httptest.NewRecorder()
		if method == http.MethodGet {
			f.handler.PlanSettingsPage(w, r)
		} else {
			f.handler.DuplicatePlan(w, r)
		}
		if w.Code != want {
			t.Fatalf("duplicate plan got %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	adminPage := call(f.ownerID, http.MethodGet, "/settings", nil, http.StatusOK)
	if !strings.Contains(adminPage.Body.String(), `action="`+path+`/duplicate"`) || !strings.Contains(adminPage.Body.String(), `value="Copy of Original plan"`) {
		t.Fatal("administrator has no named duplicate control")
	}
	editorPage := call(f.otherID, http.MethodGet, "/settings", nil, http.StatusOK)
	if strings.Contains(editorPage.Body.String(), `action="`+path+`/duplicate"`) {
		t.Fatal("a non-admin editor was offered duplication")
	}
	form := url.Values{"action": {"duplicate"}, "name": {"My copy"}}
	call(f.otherID, http.MethodPost, "/duplicate", form, http.StatusForbidden)
	form.Set("name", "   ")
	refused := call(f.ownerID, http.MethodPost, "/duplicate", form, http.StatusBadRequest)
	if !strings.Contains(refused.Body.String(), `id="plan-duplicate-name" name="name" value="   "`) {
		t.Fatal("refused duplication lost its submitted name")
	}
	var count int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM plans WHERE workspace_id=$1`, ws).Scan(&count); err != nil || count != 1 {
		t.Fatalf("refused copies created plans: %d err=%v", count, err)
	}
	form.Set("name", "My copy")
	copied := call(f.ownerID, http.MethodPost, "/duplicate", form, http.StatusSeeOther)
	copiedID, err := strconv.ParseInt(strings.TrimPrefix(copied.Header().Get("Location"), "/plans/"), 10, 64)
	if err != nil || copiedID == planID {
		t.Fatalf("invalid copy location: %s", copied.Header().Get("Location"))
	}
	copy, err := f.store.Plan(ctx, ws, copiedID)
	if err != nil || copy.Name != "My copy" {
		t.Fatalf("browser copy not persisted: %+v err=%v", copy, err)
	}
}
