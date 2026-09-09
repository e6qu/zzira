package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const (
	ProjectLifecycleActive   = "ACTIVE"
	ProjectLifecycleArchived = "ARCHIVED"
	ProjectLifecycleTrashed  = "TRASHED"
	apiTaskDeleteProject     = "project-delete"
)

var (
	ErrProjectLifecycleConflict = errors.New("project is not in a valid state for this operation")
	ErrProjectArchived          = errors.New("archived projects must be restored before deletion")
)

const projectSelectColumns = `id,workspace_id,key,name,COALESCE(workflow_id,''),COALESCE(security_scheme_id,''),description,url,COALESCE(lead_account_id,''),assignee_type,project_type_key,COALESCE(category_id,''),sender_email,lifecycle_state,COALESCE(to_char(archived_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),COALESCE(to_char(trashed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),COALESCE(lifecycle_actor_id,'')`
const projectSelectColumnsP = `p.id,p.workspace_id,p.key,p.name,COALESCE(p.workflow_id,''),COALESCE(p.security_scheme_id,''),p.description,p.url,COALESCE(p.lead_account_id,''),p.assignee_type,p.project_type_key,COALESCE(p.category_id,''),p.sender_email,p.lifecycle_state,COALESCE(to_char(p.archived_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),COALESCE(to_char(p.trashed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),COALESCE(p.lifecycle_actor_id,'')`

func scanProject(row pgx.Row) (*models.Project, error) {
	p := &models.Project{}
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.Key, &p.Name, &p.WorkflowID, &p.SecuritySchemeID, &p.Description, &p.URL, &p.LeadAccountID, &p.AssigneeType, &p.ProjectTypeKey, &p.CategoryID, &p.SenderEmail, &p.LifecycleState, &p.ArchivedAt, &p.TrashedAt, &p.LifecycleActor)
	return p, err
}

func (s *Store) ProjectByIDOrKeyAnyState(ctx context.Context, workspaceID, idOrKey string) (*models.Project, error) {
	return scanProject(s.Pool.QueryRow(ctx, `SELECT `+projectSelectColumns+` FROM projects WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2))`, workspaceID, idOrKey))
}

func (s *Store) ProjectsByLifecycle(ctx context.Context, workspaceID, state string) ([]*models.Project, error) {
	if state != ProjectLifecycleActive && state != ProjectLifecycleArchived && state != ProjectLifecycleTrashed {
		return nil, ErrProjectLifecycleConflict
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+projectSelectColumns+` FROM projects WHERE workspace_id=$1 AND lifecycle_state=$2 ORDER BY key`, workspaceID, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []*models.Project{}
	for rows.Next() {
		project, scanErr := scanProject(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *Store) RecordProjectView(ctx context.Context, workspaceID, userID, projectID string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO project_views(workspace_id,user_id,project_id,last_viewed_at)
		SELECT $1,$2,p.id,now() FROM projects p
		WHERE p.workspace_id=$1 AND p.id=$3 AND p.lifecycle_state='ACTIVE'
		ON CONFLICT(workspace_id,user_id,project_id) DO UPDATE SET last_viewed_at=EXCLUDED.last_viewed_at`, workspaceID, userID, projectID)
	return err
}

func (s *Store) RecentProjects(ctx context.Context, workspaceID, userID string) ([]*models.Project, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+projectSelectColumnsP+`
		FROM project_views view JOIN projects p ON p.id=view.project_id
		WHERE view.workspace_id=$1 AND view.user_id=$2 AND p.lifecycle_state='ACTIVE'
		ORDER BY view.last_viewed_at DESC,p.id LIMIT 20`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []*models.Project{}
	for rows.Next() {
		project, scanErr := scanProject(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func appendIssueLifecycleAction(ctx context.Context, tx pgx.Tx, workspaceID, actorID, issueID, op, reason string) error {
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	var payload []byte
	if op == models.OpDelete {
		payload, err = json.Marshal(models.DeletePayload{Reason: reason})
	} else {
		issue, issueErr := scanIssue(tx.QueryRow(ctx, issueJoin+` WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, issueID))
		if issueErr != nil {
			return issueErr
		}
		payload, err = json.Marshal(models.IssueUpdatePayload{Issue: *issue})
	}
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: issueID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID})
}

func appendProjectChildLifecycleActions(ctx context.Context, tx pgx.Tx, project *models.Project, actorID, op, reason string) error {
	issueRows, err := tx.Query(ctx, `SELECT id FROM issues WHERE project_id=$1 ORDER BY jira_id`, project.ID)
	if err != nil {
		return err
	}
	issueIDs := []string{}
	for issueRows.Next() {
		var id string
		if err = issueRows.Scan(&id); err != nil {
			issueRows.Close()
			return err
		}
		issueIDs = append(issueIDs, id)
	}
	err = issueRows.Err()
	issueRows.Close()
	if err != nil {
		return err
	}

	boardRows, err := tx.Query(ctx, `
		SELECT b.id,b.project_id,p.key,p.name,p.workspace_id,b.name,b.type,b.column_status_ids,b.filter_jql,
		       b.quick_filters,b.swimlane_strategy,b.card_fields,b.column_limits
		FROM boards b JOIN projects p ON p.id=b.project_id
		WHERE b.project_id=$1 ORDER BY b.id`, project.ID)
	if err != nil {
		return err
	}
	boards := []*models.Board{}
	for boardRows.Next() {
		board, scanErr := scanBoard(boardRows)
		if scanErr != nil {
			boardRows.Close()
			return scanErr
		}
		boards = append(boards, board)
	}
	err = boardRows.Err()
	boardRows.Close()
	if err != nil {
		return err
	}
	sprints := []*models.Sprint{}
	sprintIssues := []models.SprintIssue{}
	for _, board := range boards {
		sprintRows, sprintErr := tx.Query(ctx, `
			SELECT id,board_id,name,state,
			       COALESCE(to_char(start_date AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
			       COALESCE(to_char(end_date AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),goal
			FROM sprints WHERE board_id=$1 ORDER BY created_at,id`, board.ID)
		if sprintErr != nil {
			return sprintErr
		}
		for sprintRows.Next() {
			sprint := &models.Sprint{}
			if sprintErr = sprintRows.Scan(&sprint.ID, &sprint.BoardID, &sprint.Name, &sprint.State, &sprint.StartDate, &sprint.EndDate, &sprint.Goal); sprintErr != nil {
				sprintRows.Close()
				return sprintErr
			}
			sprints = append(sprints, sprint)
		}
		sprintErr = sprintRows.Err()
		sprintRows.Close()
		if sprintErr != nil {
			return sprintErr
		}
	}
	for _, sprint := range sprints {
		rows, sprintIssueErr := tx.Query(ctx, `SELECT sprint_id,issue_id,rank FROM sprint_issues WHERE sprint_id=$1 ORDER BY rank,issue_id`, sprint.ID)
		if sprintIssueErr != nil {
			return sprintIssueErr
		}
		for rows.Next() {
			var item models.SprintIssue
			if sprintIssueErr = rows.Scan(&item.SprintID, &item.IssueID, &item.Rank); sprintIssueErr != nil {
				rows.Close()
				return sprintIssueErr
			}
			sprintIssues = append(sprintIssues, item)
		}
		sprintIssueErr = rows.Err()
		rows.Close()
		if sprintIssueErr != nil {
			return sprintIssueErr
		}
	}
	emit := func(entityType, entityID string, payload any) error {
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			return encodeErr
		}
		seq, seqErr := nextSeq(ctx, tx, project.WorkspaceID)
		if seqErr != nil {
			return seqErr
		}
		return appendAction(ctx, tx, &models.Action{WorkspaceID: project.WorkspaceID, Seq: seq, EntityType: entityType, EntityID: entityID, Op: op, SchemaV: models.SchemaVersion, Payload: encoded, ActorID: actorID})
	}
	emitBoard := func(board *models.Board) error {
		payload := any(models.BoardUpsertPayload{Board: *board})
		if op == models.OpDelete {
			payload = models.DeletePayload{Reason: reason}
		}
		return emit(models.EntityBoard, board.ID, payload)
	}
	emitSprint := func(sprint *models.Sprint) error {
		payload := any(models.SprintUpsertPayload{Sprint: *sprint})
		if op == models.OpDelete {
			payload = models.DeletePayload{Reason: reason}
		}
		return emit(models.EntitySprint, sprint.ID, payload)
	}
	emitSprintIssue := func(item models.SprintIssue) error {
		payload := models.SprintIssuePayload{SprintID: item.SprintID, IssueID: item.IssueID, Rank: item.Rank, Removed: op == models.OpDelete}
		return emit(models.EntitySprintIssue, item.SprintID+":"+item.IssueID, payload)
	}
	emitRestoredIssueDetails := func() error {
		comments := []*models.Comment{}
		rows, queryErr := tx.Query(ctx, commentJoin+`WHERE c.issue_id=ANY($1::text[]) ORDER BY c.created_at,c.id`, issueIDs)
		if queryErr != nil {
			return queryErr
		}
		for rows.Next() {
			comment, scanErr := scanComment(rows)
			if scanErr != nil {
				rows.Close()
				return scanErr
			}
			comments = append(comments, comment)
		}
		queryErr = rows.Err()
		rows.Close()
		if queryErr != nil {
			return queryErr
		}
		attachments := []*models.Attachment{}
		rows, queryErr = tx.Query(ctx, attachmentJoin+`WHERE a.issue_id=ANY($1::text[]) ORDER BY a.created_at,a.id`, issueIDs)
		if queryErr != nil {
			return queryErr
		}
		for rows.Next() {
			attachment, scanErr := scanAttachment(rows)
			if scanErr != nil {
				rows.Close()
				return scanErr
			}
			attachments = append(attachments, attachment)
		}
		queryErr = rows.Err()
		rows.Close()
		if queryErr != nil {
			return queryErr
		}
		worklogs := []*models.Worklog{}
		rows, queryErr = tx.Query(ctx, worklogJoin+`WHERE w.issue_id=ANY($1::text[]) ORDER BY w.created_at,w.id`, issueIDs)
		if queryErr != nil {
			return queryErr
		}
		for rows.Next() {
			worklog, scanErr := scanWorklog(rows)
			if scanErr != nil {
				rows.Close()
				return scanErr
			}
			worklogs = append(worklogs, worklog)
		}
		queryErr = rows.Err()
		rows.Close()
		if queryErr != nil {
			return queryErr
		}
		links := []*models.IssueLink{}
		rows, queryErr = tx.Query(ctx, linkJoin+`WHERE l.inward_id=ANY($1::text[]) OR l.outward_id=ANY($1::text[]) ORDER BY l.created_at,l.id`, issueIDs)
		if queryErr != nil {
			return queryErr
		}
		for rows.Next() {
			link, scanErr := scanLink(rows)
			if scanErr != nil {
				rows.Close()
				return scanErr
			}
			links = append(links, link)
		}
		queryErr = rows.Err()
		rows.Close()
		if queryErr != nil {
			return queryErr
		}
		type watcher struct{ issueID, userID string }
		watchers := []watcher{}
		rows, queryErr = tx.Query(ctx, `SELECT issue_id,user_id FROM watchers WHERE issue_id=ANY($1::text[]) ORDER BY issue_id,created_at,user_id`, issueIDs)
		if queryErr != nil {
			return queryErr
		}
		for rows.Next() {
			var item watcher
			if scanErr := rows.Scan(&item.issueID, &item.userID); scanErr != nil {
				rows.Close()
				return scanErr
			}
			watchers = append(watchers, item)
		}
		queryErr = rows.Err()
		rows.Close()
		if queryErr != nil {
			return queryErr
		}
		for _, comment := range comments {
			if emitErr := emit(models.EntityComment, comment.ID, models.CommentUpsertPayload{Comment: *comment}); emitErr != nil {
				return emitErr
			}
		}
		for _, attachment := range attachments {
			if emitErr := emit(models.EntityAttachment, attachment.ID, models.AttachmentUpsertPayload{Attachment: *attachment}); emitErr != nil {
				return emitErr
			}
		}
		for _, worklog := range worklogs {
			if emitErr := emit(models.EntityWorklog, worklog.ID, models.WorklogUpsertPayload{Worklog: *worklog}); emitErr != nil {
				return emitErr
			}
		}
		for _, link := range links {
			if emitErr := emit(models.EntityIssueLink, link.ID, models.IssueLinkPayload{Link: *link}); emitErr != nil {
				return emitErr
			}
		}
		for _, watcher := range watchers {
			if emitErr := emit(models.EntityWatcher, watcher.issueID, models.WatcherPayload{IssueID: watcher.issueID, AccountID: watcher.userID}); emitErr != nil {
				return emitErr
			}
		}
		return nil
	}
	if op == models.OpDelete {
		for _, item := range sprintIssues {
			if err = emitSprintIssue(item); err != nil {
				return err
			}
		}
		for _, sprint := range sprints {
			if err = emitSprint(sprint); err != nil {
				return err
			}
		}
		for _, board := range boards {
			if err = emitBoard(board); err != nil {
				return err
			}
		}
		for _, issueID := range issueIDs {
			if err = appendIssueLifecycleAction(ctx, tx, project.WorkspaceID, actorID, issueID, op, reason); err != nil {
				return err
			}
		}
		return nil
	}
	for _, board := range boards {
		if err = emitBoard(board); err != nil {
			return err
		}
	}
	for _, sprint := range sprints {
		if err = emitSprint(sprint); err != nil {
			return err
		}
	}
	for _, issueID := range issueIDs {
		if err = appendIssueLifecycleAction(ctx, tx, project.WorkspaceID, actorID, issueID, op, reason); err != nil {
			return err
		}
	}
	if err = emitRestoredIssueDetails(); err != nil {
		return err
	}
	for _, item := range sprintIssues {
		if err = emitSprintIssue(item); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) setProjectLifecycle(ctx context.Context, workspaceID, actorID, idOrKey, target string) (*models.Project, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	project, err := scanProject(tx.QueryRow(ctx, `SELECT `+projectSelectColumns+` FROM projects WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2)) FOR UPDATE`, workspaceID, idOrKey))
	if err != nil {
		return nil, err
	}
	current := project.LifecycleState
	if (target == ProjectLifecycleArchived || target == ProjectLifecycleTrashed) && current != ProjectLifecycleActive {
		return nil, ErrProjectLifecycleConflict
	}
	if target == ProjectLifecycleActive && current == ProjectLifecycleActive {
		return nil, ErrProjectLifecycleConflict
	}
	if target != ProjectLifecycleActive && target != ProjectLifecycleArchived && target != ProjectLifecycleTrashed {
		return nil, ErrProjectLifecycleConflict
	}
	reason := "project " + target
	if target != ProjectLifecycleActive {
		if err = appendProjectChildLifecycleActions(ctx, tx, project, actorID, models.OpDelete, reason); err != nil {
			return nil, err
		}
	} else {
		if err = appendProjectChildLifecycleActions(ctx, tx, project, actorID, models.OpUpsert, ""); err != nil {
			return nil, err
		}
	}
	project, err = scanProject(tx.QueryRow(ctx, `UPDATE projects SET lifecycle_state=$3,archived_at=CASE WHEN $3='ARCHIVED' THEN now() ELSE NULL END,trashed_at=CASE WHEN $3='TRASHED' THEN now() ELSE NULL END,lifecycle_actor_id=$4 WHERE workspace_id=$1 AND id=$2 RETURNING `+projectSelectColumns, workspaceID, project.ID, target, actorID))
	if err != nil {
		return nil, err
	}
	if target == ProjectLifecycleActive {
		if err = writeProjectAction(ctx, tx, actorID, project); err != nil {
			return nil, err
		}
	} else if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project", project.ID, models.OpDelete, models.DeletePayload{Reason: reason}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return project, nil
}

func (s *Store) ArchiveProject(ctx context.Context, workspaceID, actorID, idOrKey string) (*models.Project, error) {
	return s.setProjectLifecycle(ctx, workspaceID, actorID, idOrKey, ProjectLifecycleArchived)
}

func (s *Store) TrashProject(ctx context.Context, workspaceID, actorID, idOrKey string) (*models.Project, error) {
	return s.setProjectLifecycle(ctx, workspaceID, actorID, idOrKey, ProjectLifecycleTrashed)
}

func (s *Store) RestoreProject(ctx context.Context, workspaceID, actorID, idOrKey string) (*models.Project, error) {
	return s.setProjectLifecycle(ctx, workspaceID, actorID, idOrKey, ProjectLifecycleActive)
}

type deleteProjectTaskPayload struct {
	ProjectID string `json:"projectId"`
}

func (s *Store) EnqueueProjectDeleteTask(ctx context.Context, workspaceID, actorID, idOrKey string) (APITask, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return APITask{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return APITask{}, err
	}
	var projectID, state string
	if err = tx.QueryRow(ctx, `SELECT id,lifecycle_state FROM projects WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2)) FOR SHARE`, workspaceID, idOrKey).Scan(&projectID, &state); err != nil {
		return APITask{}, err
	}
	if state == ProjectLifecycleArchived {
		return APITask{}, ErrProjectArchived
	}
	if state != ProjectLifecycleActive {
		return APITask{}, ErrProjectLifecycleConflict
	}
	task, err := queuedAPITask(workspaceID, actorID, "Delete project", apiTaskDeleteProject, deleteProjectTaskPayload{ProjectID: projectID})
	if err != nil {
		return APITask{}, err
	}
	if err = insertAPITask(ctx, tx, task); err != nil {
		return APITask{}, err
	}
	return task, tx.Commit(ctx)
}

func (s *Store) PermanentDeleteProject(ctx context.Context, workspaceID, actorID, idOrKey string, task *APITask) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	project, err := scanProject(tx.QueryRow(ctx, `SELECT `+projectSelectColumns+` FROM projects WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2)) FOR UPDATE`, workspaceID, idOrKey))
	if err != nil {
		return err
	}
	if project.LifecycleState == ProjectLifecycleArchived {
		return ErrProjectArchived
	}
	attachmentRows, err := tx.Query(ctx, `SELECT a.blob_ref,a.issue_id FROM attachments a JOIN issues i ON i.id=a.issue_id WHERE i.project_id=$1 ORDER BY a.id`, project.ID)
	if err != nil {
		return err
	}
	type attachmentDeletion struct{ blobRef, issueID string }
	attachmentDeletions := []attachmentDeletion{}
	for attachmentRows.Next() {
		var deletion attachmentDeletion
		if err = attachmentRows.Scan(&deletion.blobRef, &deletion.issueID); err != nil {
			attachmentRows.Close()
			return err
		}
		attachmentDeletions = append(attachmentDeletions, deletion)
	}
	err = attachmentRows.Err()
	attachmentRows.Close()
	if err != nil {
		return err
	}
	for _, deletion := range attachmentDeletions {
		if _, err = tx.Exec(ctx, `INSERT INTO attachment_blob_deletions(blob_ref,workspace_id,issue_id) VALUES($1,$2,$3) ON CONFLICT(blob_ref) DO NOTHING`, deletion.blobRef, workspaceID, deletion.issueID); err != nil {
			return err
		}
	}
	visibilityRows, err := tx.Query(ctx, `SELECT id,security_level_id FROM issues WHERE project_id=$1 ORDER BY jira_id`, project.ID)
	if err != nil {
		return err
	}
	type deletedVisibility struct {
		issueID         string
		securityLevelID *string
	}
	deletedVisibilities := []deletedVisibility{}
	for visibilityRows.Next() {
		var visibility deletedVisibility
		if err = visibilityRows.Scan(&visibility.issueID, &visibility.securityLevelID); err != nil {
			visibilityRows.Close()
			return err
		}
		deletedVisibilities = append(deletedVisibilities, visibility)
	}
	err = visibilityRows.Err()
	visibilityRows.Close()
	if err != nil {
		return err
	}
	for _, visibility := range deletedVisibilities {
		if _, err = tx.Exec(ctx, `INSERT INTO deleted_issue_visibility(workspace_id,issue_id,project_id,security_level_id) VALUES($1,$2,$3,$4) ON CONFLICT(workspace_id,issue_id) DO UPDATE SET project_id=EXCLUDED.project_id,security_level_id=EXCLUDED.security_level_id`, workspaceID, visibility.issueID, project.ID, visibility.securityLevelID); err != nil {
			return err
		}
	}
	if err = appendProjectChildLifecycleActions(ctx, tx, project, actorID, models.OpDelete, "project permanently deleted"); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project", project.ID, models.OpDelete, project); err != nil {
		return err
	}
	if command, deleteErr := tx.Exec(ctx, `DELETE FROM projects WHERE workspace_id=$1 AND id=$2`, workspaceID, project.ID); deleteErr != nil {
		return deleteErr
	} else if command.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	if task != nil {
		if err = completeAPITask(ctx, tx, workspaceID, task.ID, "Project deleted.", projectDeleteTaskResult(project.ID)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func projectDeleteTaskResult(projectID string) map[string]any {
	return map[string]any{"projectId": projectID, "deletedAt": time.Now().UTC().Format(time.RFC3339)}
}

func (s *Store) ProjectInsight(ctx context.Context, workspaceID, projectID string) (int, int64, error) {
	var count int64
	var lastUpdated *time.Time
	err := s.Pool.QueryRow(ctx, `SELECT count(*),max(i.updated_at) FROM issues i JOIN projects p ON p.id=i.project_id WHERE p.workspace_id=$1 AND p.id=$2 AND p.lifecycle_state='ACTIVE'`, workspaceID, projectID).Scan(&count, &lastUpdated)
	if err != nil {
		return 0, 0, err
	}
	if lastUpdated == nil {
		return int(count), 0, nil
	}
	return int(count), lastUpdated.UnixMilli(), nil
}

// ProjectStoredCounts returns retained totals for lifecycle administration.
// Callers must enforce site administration before exposing them.
func (s *Store) ProjectStoredCounts(ctx context.Context, workspaceID, projectID string) (int, int, error) {
	var issues, boards int64
	err := s.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM issues i WHERE i.project_id=p.id),
		       (SELECT count(*) FROM boards b WHERE b.project_id=p.id)
		FROM projects p WHERE p.workspace_id=$1 AND p.id=$2`, workspaceID, projectID).Scan(&issues, &boards)
	return int(issues), int(boards), err
}
