package automation

import (
	"encoding/json"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// One tick of the runner works the whole queue, not one run of it. A tick
// that executed a single run made the queue move at one run a second, so a
// rule somebody asked to run now waited behind everything already queued.
func TestOneTickWorksTheWholeQueue(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	issueID := store.NewID("iss")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,'DRN','Draining','wf_default')`, projectID, fx.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,updated_seq) VALUES($1,$2,$3,'DRN-1','Work to label','st_todo','it_task',0)`,
		issueID, fx.ws, projectID); err != nil {
		t.Fatal(err)
	}
	actions := []map[string]any{{"component": "ACTION", "type": "jira.issue.edit", "value": map[string]string{"field": "description", "value": "Edited by {{rule.name}}"}}}
	body, _ := json.Marshal(ruleBody("Drain the queue", fx.admin, "ENABLED", "key = DRN-1", actions))
	uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body)
	if err != nil {
		t.Fatal(err)
	}
	// Three runs are waiting when the tick comes.
	for i := 0; i < 3; i++ {
		if err := fx.service.EnqueueNow(fx.ctx, fx.ws, uuid); err != nil {
			t.Fatal(err)
		}
	}
	runner := &Runner{Service: fx.service}
	if err := runner.drain(fx.ctx, fx.ws); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FROM automation_runs r JOIN automation_rules l ON l.uuid=r.rule_uuid
		WHERE l.workspace_id=$1 AND r.state='PENDING'`, fx.ws).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("%d runs were still waiting after one tick", pending)
	}
}
