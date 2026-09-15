package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// ActivityEntry is one event in the activity stream gadget: work created, its
// fields changed, or a comment added.
type ActivityEntry struct {
	Kind     string
	At       time.Time
	Actor    string
	IssueKey string
	Summary  string
	Changes  []models.ChangeItem
	Comment  json.RawMessage
}

const activityBatch = 100

// activityStream lists the latest events on the work a gadget's query matches
// that someone can see, newest first. A comment restricted to a group or
// project role appears only to its members, as on the work item.
func (s *Store) activityStream(ctx context.Context, ws, user, where string, args []any, limit int) ([]ActivityEntry, error) {
	query := `WITH scoped AS (SELECT i.id, i.key, i.summary, i.project_id ` + searchJoin + ` WHERE ` + where + `)
		SELECT kind, at, actor, key, summary, detail, visibility_type, visibility_value, project_id FROM (
			SELECT CASE WHEN act.payload ? 'diff' THEN 'changed' ELSE 'created' END AS kind, act.created_at AS at,
			       COALESCE(actor.display_name,'') AS actor, sc.key, sc.summary, COALESCE(act.payload->'diff','{}'::jsonb) AS detail,
			       NULL::text AS visibility_type, NULL::text AS visibility_value, sc.project_id, act.seq AS seq
			FROM actions act JOIN scoped sc ON sc.id=act.entity_id LEFT JOIN users actor ON actor.id=act.actor_id
			WHERE act.workspace_id=$1 AND act.entity_type='issue' AND act.op='upsert' AND (
				(act.payload ? 'diff' AND act.schema_v >= 2 AND act.payload->'diff' <> '{}'::jsonb AND NOT COALESCE((act.payload->>'suppressChangelog')::boolean, false))
				OR act.seq = (SELECT min(earliest.seq) FROM actions earliest WHERE earliest.workspace_id=$1 AND earliest.entity_type='issue' AND earliest.entity_id=act.entity_id))
			UNION ALL
			SELECT 'commented', c.created_at, COALESCE(author.display_name,''), sc.key, sc.summary, c.body,
			       c.visibility_type, c.visibility_value, sc.project_id, 0
			FROM comments c JOIN scoped sc ON sc.id=c.issue_id LEFT JOIN users author ON author.id=c.author_id
		) stream ORDER BY at DESC, seq DESC LIMIT ` + strconv.Itoa(activityBatch) + ` OFFSET `
	type candidate struct {
		entry                           ActivityEntry
		detail                          []byte
		visibilityType, visibilityValue *string
		projectID                       string
	}
	out := []ActivityEntry{}
	for offset := 0; len(out) < limit; offset += activityBatch {
		rows, err := s.Pool.Query(ctx, query+strconv.Itoa(offset), args...)
		if err != nil {
			return nil, err
		}
		batch := []candidate{}
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.entry.Kind, &c.entry.At, &c.entry.Actor, &c.entry.IssueKey, &c.entry.Summary, &c.detail, &c.visibilityType, &c.visibilityValue, &c.projectID); err != nil {
				rows.Close()
				return nil, err
			}
			batch = append(batch, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, c := range batch {
			if len(out) == limit {
				break
			}
			if c.entry.Kind == "commented" {
				if c.visibilityType != nil && c.visibilityValue != nil {
					visible, err := s.CommentVisibleTo(ctx, ws, c.projectID, user, &models.Comment{VisibilityType: *c.visibilityType, VisibilityValue: *c.visibilityValue})
					if err != nil {
						return nil, err
					}
					if !visible {
						continue
					}
				}
				c.entry.Comment = c.detail
			} else if c.entry.Kind == "changed" {
				diff := map[string]models.ChangeItem{}
				if json.Unmarshal(c.detail, &diff) != nil {
					continue
				}
				c.entry.Changes = models.SortedDiffItems(diff)
			}
			out = append(out, c.entry)
		}
		if len(batch) < activityBatch {
			break
		}
	}
	return out, nil
}

// CalendarEntry is work due, or a version released, on a calendar day.
type CalendarEntry struct {
	Date                              string
	IssueKey, Summary, StatusCategory string
	ProjectKey, VersionName           string
	Released                          bool
}

// GadgetCalendar is a month of the calendar gadget.
type GadgetCalendar struct {
	Month            time.Time
	Issues, Versions []CalendarEntry
	// More counts due work beyond what the month shows.
	More int
}

const calendarLimit = 200

// gadgetCalendar finds the month's due work a gadget's query matches that
// someone can see, and the release dates of versions in those projects.
func (s *Store) gadgetCalendar(ctx context.Context, where string, args []any, now time.Time) (*GadgetCalendar, error) {
	now = now.UTC()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	calendar := &GadgetCalendar{Month: first}
	monthArgs := append(append([]any{}, args...), first, first.AddDate(0, 1, 0))
	from, until := fmt.Sprintf("$%d::date", len(args)+1), fmt.Sprintf("$%d::date", len(args)+2)
	rows, err := s.Pool.Query(ctx, `SELECT i.due_date::text, i.key, i.summary, st.category, count(*) OVER () `+searchJoin+` WHERE `+where+` AND i.due_date >= `+from+` AND i.due_date < `+until+` ORDER BY i.due_date, i.key LIMIT `+strconv.Itoa(calendarLimit), monthArgs...)
	if err != nil {
		return nil, err
	}
	total := 0
	for rows.Next() {
		var entry CalendarEntry
		if err := rows.Scan(&entry.Date, &entry.IssueKey, &entry.Summary, &entry.StatusCategory, &total); err != nil {
			rows.Close()
			return nil, err
		}
		calendar.Issues = append(calendar.Issues, entry)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	calendar.More = total - len(calendar.Issues)
	rows, err = s.Pool.Query(ctx, `WITH scoped AS (SELECT DISTINCT i.project_id `+searchJoin+` WHERE `+where+`)
		SELECT v.release_date::text, p.key, v.name, v.released FROM project_versions v JOIN scoped sc ON sc.project_id=v.project_id JOIN projects p ON p.id=v.project_id
		WHERE NOT v.archived AND v.release_date >= `+from+` AND v.release_date < `+until+`
		ORDER BY v.release_date, p.key, v.position LIMIT 100`, monthArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry CalendarEntry
		if err := rows.Scan(&entry.Date, &entry.ProjectKey, &entry.VersionName, &entry.Released); err != nil {
			return nil, err
		}
		calendar.Versions = append(calendar.Versions, entry)
	}
	return calendar, rows.Err()
}

// RoadMapVersion is an unreleased version on the road map gadget, with the
// progress of its fix-version work that someone can see.
type RoadMapVersion struct {
	Version  *models.Version
	Progress models.VersionProgress
	Total    int
	Overdue  bool
}

// RoadMap lists a project's unreleased, unarchived versions due within the
// window or already overdue, soonest first, as Jira's road map gadget does.
func (s *Store) RoadMap(ctx context.Context, workspaceID, userID, projectID string, days int, now time.Time) ([]RoadMapVersion, error) {
	_, today := windowDays(1, now)
	rows, err := s.Pool.Query(ctx, versionSelect+` WHERE v.project_id=$1 AND NOT v.released AND NOT v.archived AND v.release_date IS NOT NULL AND v.release_date <= $2::date ORDER BY v.release_date, v.position, v.id LIMIT 20`, projectID, today.AddDate(0, 0, days))
	if err != nil {
		return nil, err
	}
	versions := []*models.Version{}
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		versions = append(versions, version)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]RoadMapVersion, 0, len(versions))
	for _, version := range versions {
		issues, err := s.VersionIssues(ctx, workspaceID, userID, projectID, version.ID, "fixVersions")
		if err != nil {
			return nil, err
		}
		out = append(out, RoadMapVersion{Version: version, Progress: VersionProgress(issues), Total: len(issues), Overdue: version.ReleaseDate < today.Format("2006-01-02")})
	}
	return out, nil
}

// BubbleIssue is work on the bubble chart gadget: how many days ago it was
// last updated, how many people took part in it and how many voted for it.
type BubbleIssue struct {
	Key, Summary        string
	UpdatedDays         int
	Participants, Votes int
}

// bubbleChart reads the most recently updated work a gadget's query matches
// that someone can see. Participants are the reporter, the assignee and
// everyone who commented, as in Jira.
func (s *Store) bubbleChart(ctx context.Context, where string, args []any, limit int) ([]BubbleIssue, error) {
	rows, err := s.Pool.Query(ctx, `SELECT i.key, i.summary, GREATEST(0, floor(EXTRACT(EPOCH FROM now() - i.updated_at) / 86400))::int,
		       (SELECT count(DISTINCT person) FROM (SELECT i.reporter_id AS person UNION SELECT i.assignee_id UNION SELECT c.author_id FROM comments c WHERE c.issue_id=i.id) people WHERE person IS NOT NULL),
		       (SELECT count(*) FROM issue_votes v WHERE v.issue_id=i.id) `+searchJoin+` WHERE `+where+` ORDER BY i.updated_at DESC, i.key LIMIT `+strconv.Itoa(limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BubbleIssue{}
	for rows.Next() {
		var bubble BubbleIssue
		if err := rows.Scan(&bubble.Key, &bubble.Summary, &bubble.UpdatedDays, &bubble.Participants, &bubble.Votes); err != nil {
			return nil, err
		}
		out = append(out, bubble)
	}
	return out, rows.Err()
}
