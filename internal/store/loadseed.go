package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// SeedLoadWorkspace builds a synthetic workspace for load measurement:
// one project, an admin user with an API token, and n issues (one action
// each, 1 comment per 10 issues). Seeded with COPY for speed.
// Returns the actor's API token (plain).
func (s *Store) SeedLoadWorkspace(ctx context.Context, slug string, n int) (token, email string, err error) {
	wsID := "ws_" + slug
	projectID := "prj_" + slug
	actorID := "usr_" + slug

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO workspaces (id, slug, name, seq) VALUES ($1,$2,'Load Test',0)
		ON CONFLICT (id) DO NOTHING`, wsID, slug); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, display_name)
		VALUES ($1,$2,'x','Load User') ON CONFLICT (id) DO NOTHING`,
		actorID, slug+"@zzira.dev"); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1,$2,'admin')
		ON CONFLICT DO NOTHING`, wsID, actorID); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO projects (id, workspace_id, key, name, issue_seq)
		VALUES ($1,$3,'LOAD','Load Test',$2) ON CONFLICT (id) DO NOTHING`,
		projectID, n, wsID); err != nil {
		return "", "", err
	}

	// Build rows in Go (payloads need real JSON snapshots), COPY into tables.
	type actionRow struct {
		Seq        int64
		EntityType string
		EntityID   string
		Op         string
		SchemaV    int
		Payload    []byte
		ActorID    string
	}
	now := time.Now().UTC().Format(time.RFC3339)
	issues := make([][]any, 0, n)
	actions := make([][]any, 0, n)
	statuses := []string{"st_todo", "st_inprogress", "st_done"}
	seq := int64(0)
	for i := 1; i <= n; i++ {
		seq++
		issueID := fmt.Sprintf("iss_%s_%d", slug, i)
		issue := models.Issue{
			ID: issueID, WorkspaceID: wsID, ProjectID: projectID,
			Key:         fmt.Sprintf("LOAD-%d", i),
			Summary:     fmt.Sprintf("load issue %d", i),
			Description: json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"seeded"}]}]}`),
			Status:      models.Status{ID: statuses[i%3], Name: statusNameFor(statuses[i%3]), Category: statusCategoryFor(statuses[i%3])},
			IssueType:   models.IssueType{ID: "it_task", Name: "Task"},
			Rank:        fmt.Sprintf("u%06d", i),
			UpdatedSeq:  seq,
			UpdatedAt:   now,
		}
		payload, err := json.Marshal(models.IssueUpsertPayload{Issue: issue})
		if err != nil {
			return "", "", err
		}
		issues = append(issues, []any{
			issue.ID, wsID, projectID, issue.Key, issue.Summary, issue.Description,
			issue.Status.ID, issue.IssueType.ID, issue.Rank, seq, time.Now(),
		})
		actions = append(actions, []any{
			wsID, seq, models.EntityIssue, issue.ID, models.OpUpsert,
			models.SchemaVersion, payload, actorID, time.Now(),
		})
	}

	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"issues"},
		[]string{"id", "workspace_id", "project_id", "key", "summary", "description",
			"status_id", "issuetype_id", "rank", "updated_seq", "updated_at"},
		pgx.CopyFromRows(issues)); err != nil {
		return "", "", err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"actions"},
		[]string{"workspace_id", "seq", "entity_type", "entity_id", "op", "schema_v", "payload", "actor_id", "created_at"},
		pgx.CopyFromRows(actions)); err != nil {
		return "", "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE workspaces SET seq=$2 WHERE id=$1`, wsID, seq); err != nil {
		return "", "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}

	// API token for the load actor (after the user commit).
	tokenPlain := NewID("ltok") + NewID("ltok")
	if err := s.CreateAPIToken(ctx, NewID("tok"), actorID, HashToken(tokenPlain), "load"); err != nil {
		return "", "", err
	}
	return tokenPlain, slug + "@zzira.dev", nil
}

func statusNameFor(id string) string {
	switch id {
	case "st_todo":
		return "To Do"
	case "st_inprogress":
		return "In Progress"
	}
	return "Done"
}

func statusCategoryFor(id string) string {
	switch id {
	case "st_todo":
		return "new"
	case "st_inprogress":
		return "indeterminate"
	}
	return "done"
}
