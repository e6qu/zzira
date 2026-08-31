// Package workflow is the ZZIRA workflow engine: statuses, transitions, and
// validation. Instance-based — a project may run its own workflow (stored as
// JSON in the workflows table) while Default covers everything unassigned.
// Pure — shared by server and wasm client.
package workflow

// Transition is one workflow edge.
type Transition struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	From []string `json:"from"`
	To   string   `json:"to"`
}

// Workflow is a named set of transitions over the global status registry.
type Workflow struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Transitions []Transition `json:"transitions"`
}

// Default is the built-in workflow: To Do ↔ In Progress → Done, Done → To Do.
// Transition ids match the Jira-style string ids clients expect.
func Default() Workflow {
	return Workflow{
		ID:   "wf_default",
		Name: "Default",
		Transitions: []Transition{
			{ID: "11", Name: "To Do", From: []string{"st_inprogress", "st_done"}, To: "st_todo"},
			{ID: "21", Name: "In Progress", From: []string{"st_todo", "st_done"}, To: "st_inprogress"},
			{ID: "31", Name: "Done", From: []string{"st_todo", "st_inprogress"}, To: "st_done"},
		},
	}
}

// Available returns the transitions legal from the given status.
func (w Workflow) Available(statusID string) []Transition {
	var out []Transition
	for _, t := range w.Transitions {
		for _, from := range t.From {
			if from == statusID {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// Find returns the transition with the given id, or nil.
func (w Workflow) Find(transitionID string) *Transition {
	for i := range w.Transitions {
		if w.Transitions[i].ID == transitionID {
			return &w.Transitions[i]
		}
	}
	return nil
}

// Validate reports whether transition id is legal from the given status.
func (w Workflow) Validate(transitionID, currentStatusID string) (*Transition, bool) {
	for i := range w.Transitions {
		t := &w.Transitions[i]
		if t.ID != transitionID {
			continue
		}
		for _, from := range t.From {
			if from == currentStatusID {
				return t, true
			}
		}
		return t, false
	}
	return nil, false
}
