package store

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

const (
	apiTaskArchiveIssues        = "jira-issues-archive"
	apiTaskExportArchivedIssues = "jira-archived-issues-export"
)

var (
	// ErrIssueArchiveRunning is an archive request while another is still running.
	ErrIssueArchiveRunning = errors.New("a request to archive issues is already running")
	// ErrArchivedIssueExportRunning is an export request while another is still running.
	ErrArchivedIssueExportRunning = errors.New("a request to export archived issues is already running")
	// ErrArchivedIssueExportNotFound is an export that does not exist or belongs to someone else.
	ErrArchivedIssueExportNotFound = errors.New("archived issue export not found")
)

// IssueArchivalGroup collects the issues one reason kept from being archived or restored.
type IssueArchivalGroup struct {
	Count          int      `json:"count"`
	IssueIDsOrKeys []string `json:"issueIdsOrKeys"`
	Message        string   `json:"message"`
}

// IssueArchivalOutcome is how many issues changed and why the others did not.
type IssueArchivalOutcome struct {
	Updated int
	Errors  map[string]*IssueArchivalGroup
}

var issueArchivalMessages = map[string]string{
	"issueIsSubtask":             "Subtasks can't be archived or restored directly, only through their parent issue.",
	"issuesInArchivedProjects":   "The issues belong to archived or deleted projects.",
	"issuesInUnlicensedProjects": "The issues belong to projects that aren't software, service management or business projects.",
	"issuesNotFound":             "The issues weren't found.",
	"userDoesNotHavePermission":  "You don't have permission to archive or restore these issues.",
}

func (outcome *IssueArchivalOutcome) reject(reason, ref string) {
	group := outcome.Errors[reason]
	if group == nil {
		group = &IssueArchivalGroup{IssueIDsOrKeys: []string{}, Message: issueArchivalMessages[reason]}
		outcome.Errors[reason] = group
	}
	group.Count++
	group.IssueIDsOrKeys = append(group.IssueIDsOrKeys, ref)
}

// RejectIssueArchival records issues refused before reaching the store, such as
// for a caller without permission.
func (outcome *IssueArchivalOutcome) RejectIssueArchival(reason string, refs []string) {
	for _, ref := range refs {
		outcome.reject(reason, ref)
	}
}

// NewIssueArchivalOutcome is an outcome with nothing changed or refused yet.
func NewIssueArchivalOutcome() IssueArchivalOutcome {
	return IssueArchivalOutcome{Errors: map[string]*IssueArchivalGroup{}}
}

// ChangeIssueArchival archives or restores issues named by id or key, taking
// their subtasks with them. Subtasks can't be named directly, and only issues
// in active software, service management and business projects qualify.
// Archiving removes an issue from synced clients; restoring returns it.
func (s *Store) ChangeIssueArchival(ctx context.Context, workspaceID, actorID string, refs []string, archive bool, outcome *IssueArchivalOutcome) error {
	seen := map[string]bool{}
	for _, raw := range refs {
		ref := strings.TrimSpace(raw)
		if ref == "" || seen[strings.ToUpper(ref)] {
			continue
		}
		seen[strings.ToUpper(ref)] = true
		var issueID, lifecycle, projectType string
		var subtask, archived bool
		err := s.Pool.QueryRow(ctx, `SELECT i.id, it.subtask, p.lifecycle_state, COALESCE(p.project_type_key,''), i.archived_at IS NOT NULL
			FROM issues i JOIN issue_types it ON it.id=i.issuetype_id JOIN projects p ON p.id=i.project_id
			WHERE i.workspace_id=$1 AND (i.id=$2 OR i.jira_id::text=$2 OR upper(i.key)=upper($2))`, workspaceID, ref).
			Scan(&issueID, &subtask, &lifecycle, &projectType, &archived)
		switch {
		case err != nil:
			outcome.reject("issuesNotFound", ref)
			continue
		case subtask:
			outcome.reject("issueIsSubtask", ref)
			continue
		case lifecycle != "ACTIVE":
			outcome.reject("issuesInArchivedProjects", ref)
			continue
		case projectType != "" && projectType != "software" && projectType != "service_desk" && projectType != "business":
			outcome.reject("issuesInUnlicensedProjects", ref)
			continue
		case archived == archive:
			continue
		}
		changed, err := s.setIssueArchived(ctx, workspaceID, actorID, issueID, archive)
		if err != nil {
			return err
		}
		outcome.Updated += changed
	}
	return nil
}

// setIssueArchived archives or restores one issue and its subtasks in one
// transaction, recording each change for synced clients.
func (s *Store) setIssueArchived(ctx context.Context, workspaceID, actorID, issueID string, archive bool) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT i.id, i.project_id, i.security_level_id FROM issues i
		WHERE i.workspace_id=$1 AND (i.id=$2 OR i.parent_id=$2) AND (i.archived_at IS NOT NULL) <> $3
		ORDER BY (i.id=$2) DESC, i.jira_id FOR UPDATE`, workspaceID, issueID, archive)
	if err != nil {
		return 0, err
	}
	type target struct {
		id, projectID   string
		securityLevelID *string
	}
	targets := []target{}
	for rows.Next() {
		var t target
		if err = rows.Scan(&t.id, &t.projectID, &t.securityLevelID); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, t := range targets {
		seq, seqErr := nextSeq(ctx, tx, workspaceID)
		if seqErr != nil {
			return 0, seqErr
		}
		action := &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: t.id, SchemaV: models.SchemaVersion, ActorID: actorID}
		if archive {
			if _, err = tx.Exec(ctx, `UPDATE issues SET archived_at=now(), archived_by=$3, updated_seq=$4 WHERE workspace_id=$1 AND id=$2`, workspaceID, t.id, actorID, seq); err != nil {
				return 0, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO deleted_issue_visibility(workspace_id,issue_id,project_id,security_level_id) VALUES($1,$2,$3,$4)
				ON CONFLICT (workspace_id,issue_id) DO UPDATE SET project_id=EXCLUDED.project_id, security_level_id=EXCLUDED.security_level_id`,
				workspaceID, t.id, t.projectID, t.securityLevelID); err != nil {
				return 0, err
			}
			action.Op = models.OpDelete
			action.Payload, err = json.Marshal(models.DeletePayload{Reason: "archived"})
		} else {
			if _, err = tx.Exec(ctx, `UPDATE issues SET archived_at=NULL, archived_by=NULL, updated_seq=$3 WHERE workspace_id=$1 AND id=$2`, workspaceID, t.id, seq); err != nil {
				return 0, err
			}
			if _, err = tx.Exec(ctx, `DELETE FROM deleted_issue_visibility WHERE workspace_id=$1 AND issue_id=$2`, workspaceID, t.id); err != nil {
				return 0, err
			}
			restored, scanErr := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, t.id))
			if scanErr != nil {
				return 0, scanErr
			}
			action.Op = models.OpUpsert
			action.Payload, err = json.Marshal(models.IssueUpdatePayload{Diff: map[string]models.ChangeItem{}, Issue: *restored})
		}
		if err != nil {
			return 0, err
		}
		if err = appendAction(ctx, tx, action); err != nil {
			return 0, err
		}
	}
	return len(targets), tx.Commit(ctx)
}

func (s *Store) apiTaskRunning(ctx context.Context, workspaceID, kind string) (bool, error) {
	var running bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM api_tasks WHERE workspace_id=$1 AND kind=$2 AND status IN ('ENQUEUED','RUNNING'))`, workspaceID, kind).Scan(&running)
	return running, err
}

type archiveIssuesTaskPayload struct {
	IssueIDs []string `json:"issueIds"`
}

// EnqueueIssueArchive queues archiving the issues a JQL query matched. Only one
// such request runs at a time.
func (s *Store) EnqueueIssueArchive(ctx context.Context, workspaceID, actorID string, issueIDs []string) (APITask, error) {
	running, err := s.apiTaskRunning(ctx, workspaceID, apiTaskArchiveIssues)
	if err != nil {
		return APITask{}, err
	}
	if running {
		return APITask{}, ErrIssueArchiveRunning
	}
	task, err := queuedAPITask(workspaceID, actorID, "Archive issues", apiTaskArchiveIssues, archiveIssuesTaskPayload{IssueIDs: issueIDs})
	if err != nil {
		return APITask{}, err
	}
	if err := s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	return task, nil
}

func (s *Store) executeArchiveIssuesTask(ctx context.Context, task APITask) error {
	var payload archiveIssuesTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode issue archive: %w", err)
	}
	outcome := NewIssueArchivalOutcome()
	for start := 0; start < len(payload.IssueIDs); start += 1000 {
		end := min(start+1000, len(payload.IssueIDs))
		if err := s.ChangeIssueArchival(ctx, task.WorkspaceID, task.SubmittedBy, payload.IssueIDs[start:end], true, &outcome); err != nil {
			return err
		}
		if end < len(payload.IssueIDs) {
			if err := s.UpdateAPITaskProgress(ctx, task, end*100/len(payload.IssueIDs), fmt.Sprintf("Archived %d of %d issues.", end, len(payload.IssueIDs))); err != nil {
				return err
			}
		}
	}
	return s.CompleteAPITask(ctx, task, fmt.Sprintf("Archived %d issues.", outcome.Updated), map[string]any{"numberOfIssuesUpdated": outcome.Updated, "errors": outcome.Errors})
}

// ArchivedIssuesFilter selects archived issues to export. Issue types are stored
// ids, projects are keys, and dates are YYYY-MM-DD, inclusive.
type ArchivedIssuesFilter struct {
	ArchivedBy []string `json:"archivedBy,omitempty"`
	DateAfter  string   `json:"dateAfter,omitempty"`
	DateBefore string   `json:"dateBefore,omitempty"`
	IssueTypes []string `json:"issueTypes,omitempty"`
	Projects   []string `json:"projects,omitempty"`
	Reporters  []string `json:"reporters,omitempty"`
}

// EnqueueArchivedIssuesExport queues a CSV export of archived issues. Only one
// export runs at a time.
func (s *Store) EnqueueArchivedIssuesExport(ctx context.Context, workspaceID, actorID string, filter ArchivedIssuesFilter) (APITask, error) {
	running, err := s.apiTaskRunning(ctx, workspaceID, apiTaskExportArchivedIssues)
	if err != nil {
		return APITask{}, err
	}
	if running {
		return APITask{}, ErrArchivedIssueExportRunning
	}
	task, err := queuedAPITask(workspaceID, actorID, "Export archived issues", apiTaskExportArchivedIssues, filter)
	if err != nil {
		return APITask{}, err
	}
	if err := s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	return task, nil
}

// ArchivedIssueExportPath is where the person who requested an export downloads it.
func ArchivedIssueExportPath(taskID string) string {
	return "/secure/archived-issues-export/" + taskID + ".csv"
}

func (s *Store) executeArchivedIssuesExport(ctx context.Context, task APITask) error {
	var filter ArchivedIssuesFilter
	if err := json.Unmarshal(task.Payload, &filter); err != nil {
		return fmt.Errorf("decode archived issue export: %w", err)
	}
	query := `SELECT i.key, i.jira_id, i.summary, COALESCE(ito.name, it.name), st.name, p.key, COALESCE(COALESCE(pro.name, pr.name),''),
			COALESCE(a.display_name,''), COALESCE(r.display_name,''), i.created_at, i.updated_at, COALESCE(COALESCE(reso.name, res.name),''),
			COALESCE(ab.display_name, i.archived_by, ''), i.archived_at
		FROM issues i
		JOIN projects p ON p.id=i.project_id
		JOIN statuses st ON st.id=i.status_id
		JOIN issue_types it ON it.id=i.issuetype_id
		LEFT JOIN issue_metadata_overrides ito ON ito.workspace_id=i.workspace_id AND ito.entity_type='issuetype' AND ito.entity_id=it.id
		LEFT JOIN priorities pr ON pr.id=i.priority_id
		LEFT JOIN issue_metadata_overrides pro ON pro.workspace_id=i.workspace_id AND pro.entity_type='priority' AND pro.entity_id=pr.id
		LEFT JOIN resolutions res ON res.id=i.resolution_id
		LEFT JOIN issue_metadata_overrides reso ON reso.workspace_id=i.workspace_id AND reso.entity_type='resolution' AND reso.entity_id=res.id
		LEFT JOIN users a ON a.id=i.assignee_id
		LEFT JOIN users r ON r.id=i.reporter_id
		LEFT JOIN users ab ON ab.id=i.archived_by
		WHERE i.workspace_id=$1 AND i.archived_at IS NOT NULL
		  AND (cardinality($2::text[])=0 OR i.archived_by=ANY($2))
		  AND ($3='' OR i.archived_at::date >= $3::date)
		  AND ($4='' OR i.archived_at::date <= $4::date)
		  AND (cardinality($5::text[])=0 OR i.issuetype_id=ANY($5))
		  AND (cardinality($6::text[])=0 OR upper(p.key)=ANY($6))
		  AND (cardinality($7::text[])=0 OR i.reporter_id=ANY($7))
		ORDER BY i.archived_at, i.jira_id`
	projects := make([]string, 0, len(filter.Projects))
	for _, key := range filter.Projects {
		projects = append(projects, strings.ToUpper(strings.TrimSpace(key)))
	}
	rows, err := s.Pool.Query(ctx, query, task.WorkspaceID, nonNilStrings(filter.ArchivedBy), filter.DateAfter, filter.DateBefore,
		nonNilStrings(filter.IssueTypes), projects, nonNilStrings(filter.Reporters))
	if err != nil {
		return err
	}
	var content strings.Builder
	writer := csv.NewWriter(&content)
	_ = writer.Write([]string{"Issue key", "Issue id", "Summary", "Issue Type", "Status", "Project key", "Priority", "Assignee", "Reporter", "Created", "Updated", "Resolution", "Archived by", "Archived date"})
	count := 0
	for rows.Next() {
		var key, summary, issueType, status, projectKey, priority, assignee, reporter, resolution, archivedBy string
		var jiraID int64
		var created, updated, archivedAt time.Time
		if err = rows.Scan(&key, &jiraID, &summary, &issueType, &status, &projectKey, &priority, &assignee, &reporter, &created, &updated, &resolution, &archivedBy, &archivedAt); err != nil {
			rows.Close()
			return err
		}
		_ = writer.Write([]string{key, fmt.Sprint(jiraID), summary, issueType, status, projectKey, priority, assignee, reporter,
			created.UTC().Format(time.RFC3339), updated.UTC().Format(time.RFC3339), resolution, archivedBy, archivedAt.UTC().Format(time.RFC3339)})
		count++
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	writer.Flush()
	if _, err = s.Pool.Exec(ctx, `INSERT INTO archived_issue_exports(task_id,workspace_id,requested_by,content) VALUES($1,$2,$3,$4)
		ON CONFLICT (task_id) DO UPDATE SET content=EXCLUDED.content`, task.ID, task.WorkspaceID, task.SubmittedBy, content.String()); err != nil {
		return err
	}
	var email string
	if err = s.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, task.SubmittedBy).Scan(&email); err == nil && email != "" {
		if _, err = s.Pool.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body,dedupe_key) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT(dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`, task.WorkspaceID, email, "Your archived issues export is ready",
			fmt.Sprintf("The export of %d archived issues is ready to download:\n%s", count, ArchivedIssueExportPath(task.WireID())), "archived-issues-export:"+task.ID); err != nil {
			return err
		}
	}
	return s.CompleteAPITask(ctx, task, fmt.Sprintf("Exported %d archived issues.", count), map[string]any{"fileUrl": ArchivedIssueExportPath(task.WireID()), "issueCount": count})
}

// ArchivedIssueExport returns an export's CSV to the person who requested it.
func (s *Store) ArchivedIssueExport(ctx context.Context, workspaceID, taskID, userID string) (string, error) {
	var content string
	err := s.Pool.QueryRow(ctx, `SELECT content FROM archived_issue_exports WHERE workspace_id=$1 AND task_id=(SELECT id FROM api_tasks WHERE workspace_id=$1 AND (id=$2 OR jira_id::text=$2)) AND requested_by=$3`, workspaceID, taskID, userID).Scan(&content)
	if err != nil {
		return "", ErrArchivedIssueExportNotFound
	}
	return content, nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
