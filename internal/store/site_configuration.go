package store

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var defaultJiraSiteConfiguration = models.JiraSiteConfiguration{
	Announcement:       models.AnnouncementBanner{Visibility: "public"},
	AttachmentsEnabled: true, IssueLinkingEnabled: true, SubTasksEnabled: true,
	TimeTrackingEnabled: true, UnassignedIssuesAllowed: true, VotingEnabled: true, WatchingEnabled: true,
	TimeTrackingProvider:  "Jira",
	TimeTracking:          models.TimeTrackingConfiguration{DefaultUnit: "minute", TimeFormat: "pretty", WorkingDaysPerWeek: 5, WorkingHoursPerDay: 8},
	NavigatorColumns:      []string{"issuekey", "summary", "priority", "status", "assignee", "updated"},
	ApplicationProperties: map[string]string{},
}

func announcementHash(b models.AnnouncementBanner) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%t\x00%t\x00%s\x00%s", b.IsEnabled, b.IsDismissible, b.Visibility, b.Message)))
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:20])
}

func (s *Store) JiraSiteConfiguration(ctx context.Context, workspaceID string) (*models.JiraSiteConfiguration, error) {
	cfg := defaultJiraSiteConfiguration
	cfg.NavigatorColumns = append([]string(nil), cfg.NavigatorColumns...)
	cfg.ApplicationProperties = map[string]string{}
	var properties []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT announcement_message,announcement_enabled,announcement_dismissible,announcement_visibility,
		 attachments_enabled,issue_linking_enabled,subtasks_enabled,time_tracking_enabled,
		 unassigned_issues_allowed,voting_enabled,watching_enabled,time_tracking_provider,
		 working_hours_per_day,working_days_per_week,time_format,default_unit,navigator_columns,application_properties
		FROM jira_site_configuration WHERE workspace_id=$1`, workspaceID).Scan(
		&cfg.Announcement.Message, &cfg.Announcement.IsEnabled, &cfg.Announcement.IsDismissible, &cfg.Announcement.Visibility,
		&cfg.AttachmentsEnabled, &cfg.IssueLinkingEnabled, &cfg.SubTasksEnabled, &cfg.TimeTrackingEnabled,
		&cfg.UnassignedIssuesAllowed, &cfg.VotingEnabled, &cfg.WatchingEnabled, &cfg.TimeTrackingProvider,
		&cfg.TimeTracking.WorkingHoursPerDay, &cfg.TimeTracking.WorkingDaysPerWeek, &cfg.TimeTracking.TimeFormat,
		&cfg.TimeTracking.DefaultUnit, &cfg.NavigatorColumns, &properties)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if len(properties) > 0 {
		if err := json.Unmarshal(properties, &cfg.ApplicationProperties); err != nil {
			return nil, err
		}
	}
	cfg.Announcement.HashID = announcementHash(cfg.Announcement)
	return &cfg, nil
}

func ensureJiraSiteConfiguration(ctx context.Context, tx pgx.Tx, workspaceID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO jira_site_configuration(workspace_id) VALUES($1) ON CONFLICT DO NOTHING`, workspaceID)
	return err
}

func (s *Store) UpdateGlobalJiraConfiguration(ctx context.Context, workspaceID, actorID string, value models.JiraSiteConfiguration) error {
	detail := map[string]bool{
		"attachmentsEnabled": value.AttachmentsEnabled, "issueLinkingEnabled": value.IssueLinkingEnabled,
		"subTasksEnabled": value.SubTasksEnabled, "timeTrackingEnabled": value.TimeTrackingEnabled,
		"unassignedIssuesAllowed": value.UnassignedIssuesAllowed, "votingEnabled": value.VotingEnabled,
		"watchingEnabled": value.WatchingEnabled,
	}
	return s.updateJiraSiteConfiguration(ctx, workspaceID, actorID, "jira.configuration.updated", "globalConfiguration", detail, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE jira_site_configuration SET attachments_enabled=$2,issue_linking_enabled=$3,subtasks_enabled=$4,time_tracking_enabled=$5,unassigned_issues_allowed=$6,voting_enabled=$7,watching_enabled=$8,updated_at=now() WHERE workspace_id=$1`, workspaceID, value.AttachmentsEnabled, value.IssueLinkingEnabled, value.SubTasksEnabled, value.TimeTrackingEnabled, value.UnassignedIssuesAllowed, value.VotingEnabled, value.WatchingEnabled)
		return err
	})
}

func siteConfigurationAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, targetID string, detail any) error {
	payload, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,$3,'jira_configuration',$4,$5::jsonb FROM sites WHERE workspace_id=$1`,
		workspaceID, actorID, action, targetID, payload)
	if err != nil {
		return err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: "jira_configuration", EntityID: targetID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID})
}

func (s *Store) updateJiraSiteConfiguration(ctx context.Context, workspaceID, actorID, action, targetID string, detail any, update func(pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = ensureJiraSiteConfiguration(ctx, tx, workspaceID); err != nil {
		return err
	}
	if err = update(tx); err != nil {
		return err
	}
	if err = siteConfigurationAudit(ctx, tx, workspaceID, actorID, action, targetID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateAnnouncementBanner(ctx context.Context, workspaceID, actorID string, banner models.AnnouncementBanner) error {
	return s.updateJiraSiteConfiguration(ctx, workspaceID, actorID, "jira.announcement.updated", "announcementBanner", banner, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE jira_site_configuration SET announcement_message=$2,announcement_enabled=$3,announcement_dismissible=$4,announcement_visibility=$5,updated_at=now() WHERE workspace_id=$1`, workspaceID, banner.Message, banner.IsEnabled, banner.IsDismissible, banner.Visibility)
		return err
	})
}

func (s *Store) UpdateTimeTrackingProvider(ctx context.Context, workspaceID, actorID, key string) error {
	return s.updateJiraSiteConfiguration(ctx, workspaceID, actorID, "jira.timetracking.provider.updated", "timeTrackingProvider", map[string]string{"key": key}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE jira_site_configuration SET time_tracking_provider=$2,time_tracking_enabled=TRUE,updated_at=now() WHERE workspace_id=$1`, workspaceID, key)
		return err
	})
}

func (s *Store) UpdateTimeTrackingOptions(ctx context.Context, workspaceID, actorID string, value models.TimeTrackingConfiguration) error {
	return s.updateJiraSiteConfiguration(ctx, workspaceID, actorID, "jira.timetracking.options.updated", "timeTrackingOptions", value, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE jira_site_configuration SET working_hours_per_day=$2,working_days_per_week=$3,time_format=$4,default_unit=$5,updated_at=now() WHERE workspace_id=$1`, workspaceID, value.WorkingHoursPerDay, value.WorkingDaysPerWeek, value.TimeFormat, value.DefaultUnit)
		return err
	})
}

func (s *Store) UpdateApplicationProperty(ctx context.Context, workspaceID, actorID, key, value string) error {
	return s.updateJiraSiteConfiguration(ctx, workspaceID, actorID, "jira.application.property.updated", key, map[string]string{"key": key, "value": value}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE jira_site_configuration SET application_properties=jsonb_set(application_properties,ARRAY[$2],to_jsonb($3::text),TRUE),updated_at=now() WHERE workspace_id=$1`, workspaceID, key, value)
		return err
	})
}

func (s *Store) UpdateNavigatorColumns(ctx context.Context, workspaceID, actorID string, columns []string) error {
	return s.updateJiraSiteConfiguration(ctx, workspaceID, actorID, "jira.navigator.columns.updated", "navigatorColumns", map[string]any{"columns": columns}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE jira_site_configuration SET navigator_columns=$2,updated_at=now() WHERE workspace_id=$1`, workspaceID, columns)
		return err
	})
}
