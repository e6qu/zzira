package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ServiceOperationsSettings(ctx context.Context, workspaceID, deskID string) (*models.ServiceOperationsSettings, error) {
	v := &models.ServiceOperationsSettings{ServiceDeskID: deskID, WorkspaceID: workspaceID}
	if err := s.Pool.QueryRow(ctx, `SELECT o.cab_risk_threshold,o.review_due_days FROM service_operations_settings o JOIN service_desks d ON d.id=o.service_desk_id WHERE d.workspace_id=$1 AND d.id=$2`, workspaceID, deskID).Scan(&v.CABRiskThreshold, &v.ReviewDueDays); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT u.id,u.email,u.display_name,u.time_zone FROM service_cab_members c JOIN service_desks sd ON sd.id=c.service_desk_id JOIN users u ON u.id=c.user_id AND u.active JOIN memberships m ON m.workspace_id=sd.workspace_id AND m.user_id=u.id WHERE sd.workspace_id=$1 AND c.service_desk_id=$2 AND EXISTS(SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active JOIN directory_users du ON du.directory_id=d.id AND du.user_id=u.id AND du.active WHERE si.workspace_id=sd.workspace_id) ORDER BY lower(u.display_name),u.id`, workspaceID, deskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		user := &models.User{Active: true, AccountType: "atlassian"}
		if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.TimeZone); err != nil {
			rows.Close()
			return nil, err
		}
		v.CABMembers = append(v.CABMembers, user)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	shiftRows, err := s.Pool.Query(ctx, `SELECT sh.id,sh.user_id,u.display_name,sh.label,sh.starts_at,sh.ends_at FROM service_on_call_shifts sh JOIN users u ON u.id=sh.user_id JOIN service_desks d ON d.id=sh.service_desk_id WHERE d.workspace_id=$1 AND d.id=$2 ORDER BY sh.starts_at,sh.id::bigint`, workspaceID, deskID)
	if err != nil {
		return nil, err
	}
	defer shiftRows.Close()
	for shiftRows.Next() {
		var sh models.ServiceOnCallShift
		sh.ServiceDeskID = deskID
		if err := shiftRows.Scan(&sh.ID, &sh.UserID, &sh.UserName, &sh.Label, &sh.StartsAt, &sh.EndsAt); err != nil {
			return nil, err
		}
		sh.StartsAt = sh.StartsAt.UTC()
		sh.EndsAt = sh.EndsAt.UTC()
		v.OnCallShifts = append(v.OnCallShifts, sh)
	}
	return v, shiftRows.Err()
}

func (s *Store) UpdateServiceOperationsSettings(ctx context.Context, workspaceID, actorID, deskID string, threshold, reviewDays int, cabIDs []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	if tag, err := tx.Exec(ctx, `UPDATE service_operations_settings o SET cab_risk_threshold=$3,review_due_days=$4,updated_at=now() FROM service_desks d WHERE d.id=o.service_desk_id AND d.workspace_id=$1 AND d.id=$2`, workspaceID, deskID, threshold, reviewDays); err != nil {
		return err
	} else if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `DELETE FROM service_cab_members WHERE service_desk_id=$1`, deskID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, id := range cabIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if tag, err := tx.Exec(ctx, `INSERT INTO service_cab_members(service_desk_id,user_id) SELECT $1,m.user_id FROM memberships m JOIN users u ON u.id=m.user_id AND u.active WHERE m.workspace_id=$2 AND m.user_id=$3 AND EXISTS(SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active JOIN directory_users du ON du.directory_id=d.id AND du.user_id=m.user_id AND du.active WHERE si.workspace_id=m.workspace_id) ON CONFLICT DO NOTHING`, deskID, workspaceID, id); err != nil {
			return err
		} else if tag.RowsAffected() == 0 {
			return fmt.Errorf("CAB member is not an active workspace member")
		}
	}
	detail, _ := json.Marshal(map[string]any{"cabRiskThreshold": threshold, "reviewDueDays": reviewDays, "cabMembers": cabIDs})
	if _, err = tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,'service_operations_settings_updated','service_desk',$3,$4::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, deskID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateServiceOnCallShift(ctx context.Context, workspaceID, actorID, deskID, userID, label string, startsAt, endsAt time.Time) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	var shiftID string
	if err := tx.QueryRow(ctx, `INSERT INTO service_on_call_shifts(service_desk_id,user_id,label,starts_at,ends_at) SELECT sd.id,m.user_id,$4,$5,$6 FROM service_desks sd JOIN memberships m ON m.workspace_id=sd.workspace_id AND m.user_id=$3 JOIN users u ON u.id=m.user_id AND u.active WHERE sd.workspace_id=$1 AND sd.id=$2 AND EXISTS(SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active JOIN directory_users du ON du.directory_id=d.id AND du.user_id=m.user_id AND du.active WHERE si.workspace_id=m.workspace_id) RETURNING id`, workspaceID, deskID, userID, label, startsAt, endsAt).Scan(&shiftID); err == pgx.ErrNoRows {
		return fmt.Errorf("on-call owner is not an active workspace member")
	} else if err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"serviceDeskId": deskID, "userId": userID, "label": label, "startsAt": startsAt, "endsAt": endsAt})
	if _, err = tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,'service_on_call_shift_created','service_on_call_shift',$3,$4::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, shiftID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteServiceOnCallShift(ctx context.Context, workspaceID, actorID, deskID, shiftID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	var userID, label string
	var startsAt, endsAt time.Time
	if err := tx.QueryRow(ctx, `DELETE FROM service_on_call_shifts sh USING service_desks d WHERE sh.id=$3 AND sh.service_desk_id=d.id AND d.workspace_id=$1 AND d.id=$2 RETURNING sh.user_id,sh.label,sh.starts_at,sh.ends_at`, workspaceID, deskID, shiftID).Scan(&userID, &label, &startsAt, &endsAt); err == pgx.ErrNoRows {
		return pgx.ErrNoRows
	} else if err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"serviceDeskId": deskID, "userId": userID, "label": label, "startsAt": startsAt, "endsAt": endsAt})
	if _, err = tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,'service_on_call_shift_deleted','service_on_call_shift',$3,$4::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, shiftID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateServiceOperationsProfile(ctx context.Context, workspaceID, actorID, issueID, deskID, kind string) error {
	var onCall *string
	var dueDays int
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT sh.user_id FROM service_on_call_shifts sh JOIN users u ON u.id=sh.user_id AND u.active JOIN memberships m ON m.workspace_id=$1 AND m.user_id=sh.user_id WHERE sh.service_desk_id=$2 AND sh.starts_at<=now() AND sh.ends_at>now() AND EXISTS(SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active JOIN directory_users du ON du.directory_id=d.id AND du.user_id=sh.user_id AND du.active WHERE si.workspace_id=$1) ORDER BY sh.starts_at DESC LIMIT 1),o.review_due_days FROM service_operations_settings o JOIN service_desks d ON d.id=o.service_desk_id WHERE d.workspace_id=$1 AND o.service_desk_id=$2`, workspaceID, deskID).Scan(&onCall, &dueDays); err != nil {
		return err
	}
	review := kind == "incident"
	status := "not_required"
	var due *time.Time
	if review {
		status = "pending"
		value := time.Now().UTC().AddDate(0, 0, dueDays)
		due = &value
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO service_request_operations(request_issue_id,kind,on_call_user_id,review_required,review_due_at,review_status,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7)`, issueID, kind, onCall, review, due, status, actorID)
	return err
}

func (s *Store) ServiceOperationsProfile(ctx context.Context, workspaceID, issueID string) (*models.ServiceOperationsProfile, error) {
	v := &models.ServiceOperationsProfile{RequestIssueID: issueID}
	var onCall *string
	var onCallEmail, onCallName, onCallTimeZone string
	var onCallActive bool
	err := s.Pool.QueryRow(ctx, `SELECT o.kind,o.impact,o.likelihood,o.impact*o.likelihood,o.change_type,o.planned_start,o.planned_end,o.rollback_plan,o.on_call_user_id,COALESCE(u.email,''),COALESCE(u.display_name,'Former user'),COALESCE(u.time_zone,'UTC'),COALESCE(u.active,false),o.review_required,o.review_due_at,o.review_status,o.review_summary,o.updated_at FROM service_request_operations o JOIN service_requests r ON r.issue_id=o.request_issue_id LEFT JOIN users u ON u.id=o.on_call_user_id WHERE r.workspace_id=$1 AND o.request_issue_id=$2`, workspaceID, issueID).Scan(&v.Kind, &v.Impact, &v.Likelihood, &v.RiskScore, &v.ChangeType, &v.PlannedStart, &v.PlannedEnd, &v.RollbackPlan, &onCall, &onCallEmail, &onCallName, &onCallTimeZone, &onCallActive, &v.ReviewRequired, &v.ReviewDueAt, &v.ReviewStatus, &v.ReviewSummary, &v.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if v.PlannedStart != nil {
		value := v.PlannedStart.UTC()
		v.PlannedStart = &value
	}
	if v.PlannedEnd != nil {
		value := v.PlannedEnd.UTC()
		v.PlannedEnd = &value
	}
	if v.ReviewDueAt != nil {
		value := v.ReviewDueAt.UTC()
		v.ReviewDueAt = &value
	}
	if onCall != nil {
		v.OnCallUserID = *onCall
		v.OnCallUser = &models.User{ID: *onCall, Email: onCallEmail, DisplayName: onCallName, TimeZone: onCallTimeZone, Active: onCallActive, AccountType: "atlassian"}
	}
	return v, nil
}

func scanServiceChangeWindow(row pgx.Row) (models.ServiceChangeWindow, error) {
	var v models.ServiceChangeWindow
	err := row.Scan(&v.IssueID, &v.IssueKey, &v.Summary, &v.StatusName, &v.StatusCategory, &v.PlannedStart, &v.PlannedEnd, &v.RiskScore, &v.ConflictCount)
	if err != nil {
		return v, err
	}
	v.PlannedStart = v.PlannedStart.UTC()
	v.PlannedEnd = v.PlannedEnd.UTC()
	v.RiskLevel = (models.ServiceOperationsProfile{RiskScore: v.RiskScore}).RiskLevel()
	return v, nil
}

// ServiceChangeCalendar returns active change windows visible to an agent for one desk.
// ConflictCount is calculated from the same non-completed desk schedule.
func (s *Store) ServiceChangeCalendar(ctx context.Context, workspaceID, actorID, deskID string, from, until time.Time) ([]models.ServiceChangeWindow, error) {
	if !until.After(from) {
		return nil, fmt.Errorf("change calendar end must be after start")
	}
	agent, err := s.IsServiceAgent(ctx, workspaceID, deskID, actorID)
	if err != nil {
		return nil, err
	}
	if !agent {
		return nil, ErrProjectPermission
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT i.id,i.key,i.summary,st.name,st.category,o.planned_start,o.planned_end,o.impact*o.likelihood,
		  (SELECT count(*)::int FROM service_request_operations other
		   JOIN service_requests other_request ON other_request.issue_id=other.request_issue_id
		   JOIN issues other_issue ON other_issue.id=other.request_issue_id
		   JOIN statuses other_status ON other_status.id=other_issue.status_id
		   WHERE other_request.service_desk_id=r.service_desk_id AND other.kind='change'
		     AND other.request_issue_id<>o.request_issue_id AND other_status.category<>'done'
		     AND other.planned_start IS NOT NULL AND other.planned_end IS NOT NULL
		     AND other.planned_start<o.planned_end AND other.planned_end>o.planned_start)
		FROM service_request_operations o
		JOIN service_requests r ON r.issue_id=o.request_issue_id
		JOIN issues i ON i.id=o.request_issue_id
		JOIN statuses st ON st.id=i.status_id
		WHERE r.workspace_id=$1 AND r.service_desk_id=$2 AND o.kind='change'
		  AND st.category<>'done' AND o.planned_start IS NOT NULL AND o.planned_end IS NOT NULL
		  AND o.planned_start<$4 AND o.planned_end>$3
		ORDER BY o.planned_start,i.key`, workspaceID, deskID, from, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.ServiceChangeWindow{}
	for rows.Next() {
		value, err := scanServiceChangeWindow(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) ServiceChangeConflicts(ctx context.Context, workspaceID, actorID, issueID string) ([]models.ServiceChangeWindow, error) {
	var deskID string
	var startsAt, endsAt *time.Time
	err := s.Pool.QueryRow(ctx, `SELECT r.service_desk_id,o.planned_start,o.planned_end FROM service_request_operations o JOIN service_requests r ON r.issue_id=o.request_issue_id WHERE r.workspace_id=$1 AND o.request_issue_id=$2 AND o.kind='change'`, workspaceID, issueID).Scan(&deskID, &startsAt, &endsAt)
	if err != nil {
		return nil, err
	}
	agent, err := s.IsServiceAgent(ctx, workspaceID, deskID, actorID)
	if err != nil {
		return nil, err
	}
	if !agent {
		return nil, ErrProjectPermission
	}
	if startsAt == nil || endsAt == nil {
		return []models.ServiceChangeWindow{}, nil
	}
	values, err := s.ServiceChangeCalendar(ctx, workspaceID, actorID, deskID, *startsAt, *endsAt)
	if err != nil {
		return nil, err
	}
	conflicts := values[:0]
	for _, value := range values {
		if value.IssueID != issueID {
			conflicts = append(conflicts, value)
		}
	}
	return conflicts, nil
}

// ServiceDependencyLinks returns links touching operations work in one desk.
// Callers must still permission-filter linked Jira issues outside the desk.
func (s *Store) ServiceDependencyLinks(ctx context.Context, workspaceID, actorID, deskID string) ([]*models.IssueLink, error) {
	agent, err := s.IsServiceAgent(ctx, workspaceID, deskID, actorID)
	if err != nil {
		return nil, err
	}
	if !agent {
		return nil, ErrProjectPermission
	}
	rows, err := s.Pool.Query(ctx, linkJoin+`
		WHERE l.workspace_id=$1 AND EXISTS(
			SELECT 1 FROM service_requests r
			JOIN service_request_operations o ON o.request_issue_id=r.issue_id
			WHERE r.service_desk_id=$2 AND r.workspace_id=$1
			  AND r.issue_id IN (l.inward_id,l.outward_id))
		ORDER BY l.created_at,l.id`, workspaceID, deskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []*models.IssueLink{}
	for rows.Next() {
		link, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

func (s *Store) UpdateServiceOperationsProfile(ctx context.Context, workspaceID, actorID, issueID string, v models.ServiceOperationsProfile) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE service_request_operations o SET impact=$4,likelihood=$5,change_type=$6,planned_start=$7,planned_end=$8,rollback_plan=$9,on_call_user_id=NULLIF($10,''),review_required=$11,review_due_at=$12,review_status=$13,review_summary=$14,updated_by=$3,updated_at=now() FROM service_requests r WHERE r.issue_id=o.request_issue_id AND r.workspace_id=$1 AND o.request_issue_id=$2`, workspaceID, issueID, actorID, v.Impact, v.Likelihood, v.ChangeType, v.PlannedStart, v.PlannedEnd, v.RollbackPlan, v.OnCallUserID, v.ReviewRequired, v.ReviewDueAt, v.ReviewStatus, v.ReviewSummary)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	detail, _ := json.Marshal(map[string]any{"impact": v.Impact, "likelihood": v.Likelihood, "riskScore": v.Impact * v.Likelihood, "changeType": v.ChangeType, "onCallUserId": v.OnCallUserID, "reviewRequired": v.ReviewRequired, "reviewStatus": v.ReviewStatus})
	if _, err = tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,'service_operations_assessed','service_request',$3,$4::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, issueID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
