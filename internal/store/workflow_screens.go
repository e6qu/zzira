package store

import (
	"context"

	"github.com/e6qu/zzira/internal/workflow"
)

// ScreenIDParameter names the screen a transition collects its fields on. A
// transition that names one follows that screen: the fields it asks for are
// whatever the screen holds when the work item is being transitioned, not a
// list copied when the workflow was saved.
const ScreenIDParameter = "screenId"

// resolveTransitionScreens fills in the fields of every transition that names
// a screen, so the rest of the product reads one field list however the
// transition was configured. A screen that no longer exists leaves the
// transition asking for nothing, as a deleted screen does in Jira.
func resolveTransitionScreens(ctx context.Context, q workflowSchemeQuerier, workspaceID string, wf *workflow.Workflow) error {
	needed := map[string]bool{}
	for _, transition := range wf.Transitions {
		if transition.Screen == nil {
			continue
		}
		if id := transition.Screen.Parameters[ScreenIDParameter]; id != "" {
			needed[id] = true
		}
	}
	if len(needed) == 0 {
		return nil
	}
	fields := make(map[string]string, len(needed))
	for id := range needed {
		screen, err := screenFieldsQuery(ctx, q, workspaceID, id)
		if err != nil {
			return err
		}
		fields[id] = screen
	}
	for index, transition := range wf.Transitions {
		if transition.Screen == nil {
			continue
		}
		id := transition.Screen.Parameters[ScreenIDParameter]
		if id == "" {
			continue
		}
		parameters := make(map[string]string, len(transition.Screen.Parameters))
		for key, value := range transition.Screen.Parameters {
			parameters[key] = value
		}
		parameters["fields"] = fields[id]
		screen := *transition.Screen
		screen.Parameters = parameters
		wf.Transitions[index].Screen = &screen
	}
	return nil
}

// screenFieldsQuery reads a screen's fields, tab by tab and in order, as the
// comma-separated list a transition screen carries.
func screenFieldsQuery(ctx context.Context, q workflowSchemeQuerier, workspaceID, screenID string) (string, error) {
	rows, err := q.Query(ctx, `SELECT f.field_id FROM screen_tab_fields f
		JOIN screen_tabs t ON t.id=f.tab_id
		JOIN screens s ON s.id=t.screen_id
		WHERE s.workspace_id=$1 AND s.id=$2
		ORDER BY t.position, t.id, f.position, f.field_id`, workspaceID, screenID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	list := ""
	for rows.Next() {
		var field string
		if err := rows.Scan(&field); err != nil {
			return "", err
		}
		if list != "" {
			list += ","
		}
		list += field
	}
	return list, rows.Err()
}

// forgetResolvedScreenFields drops the fields a screen supplied, so a saved
// workflow records the screen it names rather than a copy of what that screen
// held at the time.
func forgetResolvedScreenFields(wf *workflow.Workflow) {
	for index, transition := range wf.Transitions {
		if transition.Screen == nil || transition.Screen.Parameters[ScreenIDParameter] == "" {
			continue
		}
		parameters := make(map[string]string, len(transition.Screen.Parameters))
		for key, value := range transition.Screen.Parameters {
			if key == "fields" {
				continue
			}
			parameters[key] = value
		}
		screen := *transition.Screen
		screen.Parameters = parameters
		wf.Transitions[index].Screen = &screen
	}
}
