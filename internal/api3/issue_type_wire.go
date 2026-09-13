package api3

import (
	"context"
	"net/http"
	"strings"
)

// issueTypeIDs translates between the issue type ids clients use — Jira's
// numeric ids — and the ids the product stores. Every API that names an issue
// type goes through it, so no stored id reaches a client.
type issueTypeIDs struct {
	wire     map[string]string
	internal map[string]string
}

func (h *Handler) issueTypeIDTranslator(ctx context.Context, workspaceID string) (issueTypeIDs, error) {
	types, err := h.Store.IssueTypesForWorkspace(ctx, workspaceID)
	if err != nil {
		return issueTypeIDs{}, err
	}
	t := issueTypeIDs{wire: make(map[string]string, len(types)), internal: make(map[string]string, len(types)*2)}
	for _, issueType := range types {
		jira := jiraIDString(issueType.JiraID)
		t.wire[issueType.ID] = jira
		t.internal[jira] = issueType.ID
		t.internal[issueType.ID] = issueType.ID
	}
	return t, nil
}

// toWire returns the numeric id for a stored id. A value that is not a stored
// issue type id — the literal "default" a scheme mapping uses, say — is left as
// it is.
func (t issueTypeIDs) toWire(id string) string {
	if wire, ok := t.wire[id]; ok {
		return wire
	}
	return id
}

// toInternal returns the stored id for an id a client sent. An id the site does
// not have is passed through unchanged, so the store's own validation refuses it
// with the answer it already gives.
func (t issueTypeIDs) toInternal(id string) string {
	if internal, ok := t.internal[strings.TrimSpace(id)]; ok {
		return internal
	}
	return id
}

func (t issueTypeIDs) allToInternal(ids []string) []string {
	if ids == nil {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = t.toInternal(id)
	}
	return out
}

func (t issueTypeIDs) allToWire(ids []string) []string {
	if ids == nil {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = t.toWire(id)
	}
	return out
}

// mapKeysToInternal translates the keys of an issue type mapping, such as a
// workflow scheme's issue type to workflow map.
func (t issueTypeIDs) mapKeysToInternal(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[t.toInternal(key)] = value
	}
	return out
}

// issueTypeIDsFor builds a translator for a request. If the site's issue types
// cannot be read, the empty translator passes ids through unchanged, so the
// operation's own validation still decides.
func (h *Handler) issueTypeIDsFor(r *http.Request, workspaceID string) issueTypeIDs {
	ids, err := h.issueTypeIDTranslator(r.Context(), workspaceID)
	if err != nil {
		return issueTypeIDs{wire: map[string]string{}, internal: map[string]string{}}
	}
	return ids
}
