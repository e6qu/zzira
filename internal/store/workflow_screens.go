package store

import (
	"context"
	"strings"

	"github.com/e6qu/zzira/internal/models"
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
	tabs := make(map[string][]models.FieldTab, len(needed))
	for id := range needed {
		layout, err := transitionScreenLayoutQuery(ctx, q, workspaceID, id)
		if err != nil {
			return err
		}
		tabs[id] = layout
		var ordered []string
		for _, tab := range layout {
			ordered = append(ordered, tab.Fields...)
		}
		fields[id] = strings.Join(ordered, ",")
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
		wf.Transitions[index].ScreenTabs = tabs[id]
	}
	return nil
}

// transitionScreenLayoutQuery reads the live tabs and their fields together.
// The left join retains empty tabs without inventing fields for them.
func transitionScreenLayoutQuery(ctx context.Context, q workflowSchemeQuerier, workspaceID, screenID string) ([]models.FieldTab, error) {
	rows, err := q.Query(ctx, `SELECT t.id::text,t.name,f.field_id FROM screen_tabs t
		JOIN screens s ON s.id=t.screen_id
		LEFT JOIN screen_tab_fields f ON f.tab_id=t.id AND f.screen_id=t.screen_id
		WHERE s.workspace_id=$1 AND s.id=$2
		ORDER BY t.position, t.id, f.position, f.field_id`, workspaceID, screenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tabs := []models.FieldTab{}
	lastID := ""
	for rows.Next() {
		var id, name string
		var field *string
		if err := rows.Scan(&id, &name, &field); err != nil {
			return nil, err
		}
		if id != lastID {
			tabs = append(tabs, models.FieldTab{Name: name, Fields: []string{}})
			lastID = id
		}
		if field != nil {
			tabs[len(tabs)-1].Fields = append(tabs[len(tabs)-1].Fields, *field)
		}
	}
	return tabs, rows.Err()
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
