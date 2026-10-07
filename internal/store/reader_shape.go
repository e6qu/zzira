package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

// commentVisibilityPredicate asks the same group/role question as CommentVisibleTo
// inside a snapshot transaction. Its arguments are SQL expressions, never input.
func commentVisibilityPredicate(workspace, project, reader, kind, value string) string {
	return fmt.Sprintf(`(COALESCE(%[4]s,'')='' OR (%[4]s='group' AND EXISTS (
 SELECT 1 FROM group_members gm JOIN groups g ON g.id=gm.group_id
 JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id
 WHERE si.workspace_id=%[1]s AND d.active AND g.id::text=%[5]s AND gm.user_id=%[3]s))
 OR (%[4]s='role' AND EXISTS (SELECT 1 FROM role_bindings rb
 WHERE rb.scope_type='project' AND rb.scope_id=%[2]s AND rb.role_key=%[5]s
 AND ((rb.principal_type='user' AND rb.principal_id=%[3]s) OR
 (rb.principal_type='group' AND EXISTS (SELECT 1 FROM group_members gm
 JOIN groups g ON g.id=gm.group_id JOIN directories d ON d.id=g.directory_id
 WHERE gm.group_id::text=rb.principal_id AND gm.user_id=%[3]s AND d.active))))))`, workspace, project, reader, kind, value)
}

func (s *Store) readableIssueReference(ctx context.Context, workspaceID, readerID, reference string) (bool, error) {
	var visible bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issues i WHERE i.workspace_id=$1 AND (i.id=$3 OR i.key=$3 OR i.jira_id::text=$3)
 AND jira_has_project_permission($1,i.project_id,$2,i.id,'BROWSE_PROJECTS')
 AND (COALESCE(i.security_level_id,'')='' OR jira_issue_security_visible($1,i.project_id,i.id,$2,i.security_level_id)))`, workspaceID, readerID, reference).Scan(&visible)
	return visible, err
}

func redactParentChange(item models.ChangeItem, readable func(string) (bool, error)) (models.ChangeItem, error) {
	for _, side := range []struct{ ref, text *string }{{&item.From, &item.FromString}, {&item.To, &item.ToString}} {
		reference := *side.ref
		if reference == "" {
			reference = *side.text
		}
		if reference == "" {
			continue
		}
		allowed, err := readable(reference)
		if err != nil {
			return item, err
		}
		if !allowed {
			*side.ref = ""
			*side.text = ""
		}
	}
	return item, nil
}

// IssueChangelogForReader removes references to inaccessible parents from history.
func (s *Store) IssueChangelogForReader(ctx context.Context, workspaceID, readerID, issueID string) ([]models.ChangelogEntry, error) {
	entries, err := s.IssueChangelog(ctx, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	readable := s.cachedIssueReader(ctx, workspaceID, readerID)
	for i := range entries {
		items := entries[i].Items[:0]
		for _, item := range entries[i].Items {
			if item.Field == "parent" {
				item, err = redactParentChange(item, readable)
				if err != nil {
					return nil, err
				}
				if item.From == "" && item.To == "" && item.FromString == "" && item.ToString == "" {
					continue
				}
			}
			items = append(items, item)
		}
		entries[i].Items = items
	}
	return entries, nil
}

func (s *Store) cachedIssueReader(ctx context.Context, workspaceID, readerID string) func(string) (bool, error) {
	cache := map[string]bool{}
	return func(ref string) (bool, error) {
		if visible, known := cache[ref]; known {
			return visible, nil
		}
		visible, err := s.readableIssueReference(ctx, workspaceID, readerID, ref)
		if err == nil {
			cache[ref] = visible
		}
		return visible, err
	}
}

// shapeReplicaActions preserves sequence progress while removing private values.
// A now-inaccessible comment becomes a delete so an existing replica drops it.
func (s *Store) shapeReplicaActions(ctx context.Context, workspaceID, readerID string, actions []models.Action) ([]models.Action, error) {
	readable := s.cachedIssueReader(ctx, workspaceID, readerID)
	for i := range actions {
		action := &actions[i]
		switch action.EntityType {
		case models.EntityIssue:
			if action.Op != models.OpUpsert {
				continue
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(action.Payload, &payload); err != nil {
				return nil, err
			}
			var issue map[string]json.RawMessage
			if err := json.Unmarshal(payload["issue"], &issue); err != nil {
				return nil, err
			}
			if raw := issue["parent"]; len(raw) > 0 && string(raw) != "null" {
				var parent models.IssueParent
				if err := json.Unmarshal(raw, &parent); err != nil {
					return nil, err
				}
				allowed, err := readable(parent.ID)
				if err != nil {
					return nil, err
				}
				if !allowed {
					delete(issue, "parent")
				}
			}
			if raw := payload["diff"]; len(raw) > 0 {
				var diff map[string]models.ChangeItem
				if err := json.Unmarshal(raw, &diff); err != nil {
					return nil, err
				}
				if item, ok := diff["parent"]; ok {
					item, err := redactParentChange(item, readable)
					if err != nil {
						return nil, err
					}
					if item.From == "" && item.To == "" && item.FromString == "" && item.ToString == "" {
						delete(diff, "parent")
					} else {
						diff["parent"] = item
					}
				}
				payload["diff"], _ = json.Marshal(diff)
			}
			payload["issue"], _ = json.Marshal(issue)
			action.Payload, _ = json.Marshal(payload)
		case models.EntityComment:
			if action.Op != models.OpUpsert {
				continue
			}
			var payload models.CommentUpsertPayload
			if err := json.Unmarshal(action.Payload, &payload); err != nil {
				return nil, err
			}
			current, err := s.CommentByRef(ctx, workspaceID, action.EntityID)
			visible := false
			if err == nil {
				issue, issueErr := s.IssueByIDOrKey(ctx, workspaceID, current.IssueID)
				if issueErr != nil {
					return nil, issueErr
				}
				visible, err = s.CommentVisibleTo(ctx, workspaceID, issue.ProjectID, readerID, current)
			}
			if err != nil && !errors.Is(err, ErrCommentNotFound) {
				return nil, err
			}
			if !visible {
				action.Op = models.OpDelete
				action.Payload, _ = json.Marshal(models.CommentDeletePayload{CommentID: payload.Comment.ID, IssueID: payload.Comment.IssueID})
			}
		}
	}
	return actions, nil
}
