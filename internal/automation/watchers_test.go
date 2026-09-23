package automation

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// Two more of Jira's actions: somebody is added to the people a work item
// tells about itself, and a work item is copied.
func TestWatcherAndCloneActions(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	issueID := store.NewID("iss")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,'WCH','Watching','wf_default')`, projectID, fx.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,updated_seq) VALUES($1,$2,$3,'WCH-90','Payments are slow','st_todo','it_task',0)`,
		issueID, fx.ws, projectID); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	rules := 0
	run := func(actions []map[string]any) error {
		t.Helper()
		rules++
		body, _ := json.Marshal(ruleBody(fmt.Sprintf("Watchers and copies %d", rules), fx.admin, "ENABLED", "key = WCH-90", actions))
		uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body)
		if err != nil {
			return err
		}
		if err := fx.service.EnqueueNow(fx.ctx, fx.ws, uuid); err != nil {
			return err
		}
		return runner.DrainOnce(fx.ctx, fx.ws)
	}

	if err := run([]map[string]any{{"component": "ACTION", "type": "jira.issue.watchers", "value": map[string]string{"action": "add", "accountId": fx.member}}}); err != nil {
		t.Fatalf("add a watcher: %v", err)
	}
	watchers, err := fx.store.WatchersByIssue(fx.ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	watching := false
	for _, watcher := range watchers {
		watching = watching || watcher == fx.member
	}
	if !watching {
		t.Fatalf("the work item is watched by %+v", watchers)
	}

	if err := run([]map[string]any{{"component": "ACTION", "type": "jira.issue.watchers", "value": map[string]string{"action": "remove", "accountId": fx.member}}}); err != nil {
		t.Fatalf("remove a watcher: %v", err)
	}
	if watchers, err = fx.store.WatchersByIssue(fx.ctx, issueID); err != nil {
		t.Fatal(err)
	}
	for _, watcher := range watchers {
		if watcher == fx.member {
			t.Fatal("the watcher was not removed")
		}
	}

	// A copy, with the summary Jira writes when a rule says nothing.
	if err := run([]map[string]any{{"component": "ACTION", "type": "jira.issue.clone", "value": map[string]string{}}}); err != nil {
		t.Fatalf("clone: %v", err)
	}
	var copies int
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FROM issues WHERE project_id=$1 AND summary=$2`, projectID, "Copy of Payments are slow").Scan(&copies); err != nil {
		t.Fatal(err)
	}
	if copies != 1 {
		t.Fatalf("%d copies were made", copies)
	}
	// Running again makes no second copy, because the copy is already there.
	if err := run([]map[string]any{{"component": "ACTION", "type": "jira.issue.clone", "value": map[string]string{}}}); err != nil {
		t.Fatal(err)
	}
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FROM issues WHERE project_id=$1 AND summary=$2`, projectID, "Copy of Payments are slow").Scan(&copies); err != nil {
		t.Fatal(err)
	}
	if copies != 1 {
		t.Fatalf("%d copies after running again", copies)
	}

	// An action that names nobody, or neither adds nor removes, says so.
	for _, value := range []map[string]string{{"action": "add"}, {"action": "sideways", "accountId": fx.member}} {
		if err := run([]map[string]any{{"component": "ACTION", "type": "jira.issue.watchers", "value": value}}); err == nil {
			t.Fatalf("the rule ran with %v", value)
		}
	}
}
