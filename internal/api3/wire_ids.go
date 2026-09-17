package api3

import (
	"context"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/workflow"
)

// statusIDs translates between the status ids clients use — Jira's numeric
// ids — and the ids the product stores, for every status on a site.
type statusIDs struct {
	wire     map[string]string
	internal map[string]string
}

func (h *Handler) statusIDTranslator(ctx context.Context, workspaceID string) (statusIDs, error) {
	statuses, err := h.Store.StatusesForAdministration(ctx, workspaceID)
	if err != nil {
		return statusIDs{}, err
	}
	t := statusIDs{wire: make(map[string]string, len(statuses)), internal: make(map[string]string, len(statuses)*2)}
	for _, status := range statuses {
		wire := statusWireID(status)
		t.wire[status.ID] = wire
		t.internal[wire] = status.ID
		t.internal[status.ID] = status.ID
	}
	return t, nil
}

// statusIDsFor builds a status translator for a request. If the site's statuses
// cannot be read, ids pass through unchanged so the operation's own validation decides.
func (h *Handler) statusIDsFor(r *http.Request, workspaceID string) statusIDs {
	ids, err := h.statusIDTranslator(r.Context(), workspaceID)
	if err != nil {
		return statusIDs{wire: map[string]string{}, internal: map[string]string{}}
	}
	return ids
}

func (t statusIDs) toWire(id string) string {
	if wire, ok := t.wire[id]; ok {
		return wire
	}
	return id
}

func (t statusIDs) toInternal(id string) string {
	if internal, ok := t.internal[strings.TrimSpace(id)]; ok {
		return internal
	}
	return id
}

func (t statusIDs) allToWire(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = t.toWire(id)
	}
	return out
}

// workflowIDs translates between the UUIDs clients identify workflows by and
// the ids the product stores.
type workflowIDs struct {
	wire     map[string]string
	internal map[string]string
}

func (h *Handler) workflowIDsFor(r *http.Request, workspaceID string) workflowIDs {
	t := workflowIDs{wire: map[string]string{}, internal: map[string]string{}}
	workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
	if err != nil {
		return t
	}
	for _, wf := range workflows {
		t.add(wf)
	}
	return t
}

func (t workflowIDs) add(wf workflow.Workflow) {
	if wf.EntityID == "" {
		return
	}
	t.wire[wf.ID] = wf.EntityID
	t.internal[wf.EntityID] = wf.ID
	t.internal[wf.ID] = wf.ID
}

func (t workflowIDs) toWire(id string) string {
	if wire, ok := t.wire[id]; ok {
		return wire
	}
	return id
}

func (t workflowIDs) toInternal(id string) string {
	if internal, ok := t.internal[strings.TrimSpace(id)]; ok {
		return internal
	}
	return id
}

// workflowWireID is the UUID clients know a workflow by.
func workflowWireID(wf workflow.Workflow) string {
	if wf.EntityID != "" {
		return wf.EntityID
	}
	return wf.ID
}

// withWorkflowEntityID fills in a workflow's UUID when the store handed back a
// workflow without one, as a batch create or update does.
func withWorkflowEntityID(wf workflow.Workflow, ids workflowIDs) workflow.Workflow {
	if wf.EntityID == "" {
		if wire := ids.toWire(wf.ID); wire != wf.ID {
			wf.EntityID = wire
		}
	}
	return wf
}
