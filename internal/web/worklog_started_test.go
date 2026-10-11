package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
)

func TestWorklogStartAndRefusedDraft(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx := context.Background()
	ws := f.handler.WorkspaceSlug
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM worklogs WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, query, ws); err != nil {
				t.Error(err)
			}
		}
	})
	_, err := f.handler.Commands.CreateProject(ctx, f.ownerID, ws, commands.CreateProjectInput{Key: "WST", Name: "Dated work", ProjectTypeKey: "software", LeadAccountID: f.ownerID})
	if err != nil {
		t.Fatal(err)
	}
	issue, _, err := f.handler.Commands.CreateIssue(ctx, commands.CreateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, ProjectIDOrKey: "WST", IssueTypeID: "it_task", Summary: "Record earlier work"})
	if err != nil {
		t.Fatal(err)
	}
	handler := func(w http.ResponseWriter, r *http.Request) { f.handler.AddWorklog(w, r, issue.Key) }
	values := url.Values{"timeSpent": {"bad duration"}, "started": {"2026-10-01T08:34:56"}, "comment": {"Earlier investigation <notes>"}, "adjustEstimate": {"new"}, "newEstimate": {"3h"}}
	response := f.post(t, handler, f.ownerID, "/issues/"+issue.Key+"/worklogs", "", values)
	if response.Code != 400 {
		t.Fatalf("refused status: %d %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{`role="alert"`, `value="bad duration"`, `value="2026-10-01T08:34:56"`, `value="Earlier investigation &lt;notes&gt;"`, `value="3h"`, `value="new" checked`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("refused draft lost %s", expected)
		}
	}
	logs, err := f.store.WorklogsByIssue(ctx, issue.ID)
	if err != nil || len(logs) != 0 {
		t.Fatalf("refused work persisted: %v %v", logs, err)
	}
	values.Set("timeSpent", "1h")
	values.Set("started", "bad date")
	response = f.post(t, handler, f.ownerID, "/issues/"+issue.Key+"/worklogs", "", values)
	if response.Code != 400 || !strings.Contains(response.Body.String(), `value="bad date"`) {
		t.Fatal("invalid date discarded its draft")
	}
	values.Set("started", "2026-10-01T08:34:56")
	response = f.post(t, handler, f.ownerID, "/issues/"+issue.Key+"/worklogs", "", values)
	if response.Code != 200 {
		t.Fatalf("logged status: %d %s", response.Code, response.Body.String())
	}
	logs, err = f.store.WorklogsByIssue(ctx, issue.ID)
	if err != nil || len(logs) != 1 || logs[0].Started != "2026-10-01T08:34:56.000Z" || logs[0].UpdateAuthorID != f.ownerID {
		t.Fatalf("logged metadata: %+v %v", logs, err)
	}
	if !strings.Contains(response.Body.String(), `datetime="2026-10-01T08:34:56.000Z"`) {
		t.Fatal("work-start time missing from activity")
	}
}
