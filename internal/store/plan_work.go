package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// PlanWork is what a plan shows: its work in rank order with each epic's
// child work under it, the names of the fields its dates come from, and its
// teams.
type PlanWork struct {
	Items          []models.TimelineItem
	StartFieldName string
	EndFieldName   string
	Teams          []PlanTeam
}

// PlanAccess reports whether the user may view the plan and edit it: site
// administrators and the plan lead may do both, and the plan's permissions
// grant view or edit to people and groups.
func (s *Store) PlanAccess(ctx context.Context, workspaceID, userID string, plan Plan) (bool, bool, error) {
	admin, err := s.IsAdmin(ctx, workspaceID, userID)
	if err != nil {
		return false, false, err
	}
	if admin || plan.LeadAccountID == userID {
		return true, true, nil
	}
	groups := map[string]bool{}
	if memberships, err := s.UserGroups(ctx, workspaceID, userID); err == nil {
		for _, group := range memberships {
			groups[group.ID] = true
		}
	}
	view, edit := false, false
	for _, permission := range plan.Permissions {
		holds := (permission.HolderType == "AccountId" && permission.Holder == userID) || (permission.HolderType == "Group" && groups[permission.Holder])
		if holds {
			view = true
			edit = edit || permission.Type == "Edit"
		}
	}
	return view, edit, nil
}

// planDateField resolves where a plan reads one of its dates: the due date,
// the site's Target start or Target end field, or a chosen date field.
func (s *Store) planDateField(ctx context.Context, workspaceID string, field PlanDateField) (string, string, error) {
	switch field.Type {
	case "DueDate":
		return "duedate", "Due date", nil
	case "TargetStartDate", "TargetEndDate":
		name := "Target start"
		if field.Type == "TargetEndDate" {
			name = "Target end"
		}
		id, err := s.siteDateFieldID(ctx, workspaceID, name)
		return id, name, err
	case "DateCustomField":
		if field.DateCustomFieldID == nil {
			return "", "", nil
		}
		id := "customfield_" + strconv.FormatInt(*field.DateCustomFieldID, 10)
		var name string
		if err := s.Pool.QueryRow(ctx, `SELECT name FROM custom_fields WHERE id=$1`, id).Scan(&name); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", "", err
		}
		return id, name, nil
	}
	return "", "", nil
}

func planDateValue(issue *models.Issue, fieldID string) string {
	if fieldID == "duedate" {
		return issue.DueDate
	}
	var value string
	if raw, ok := issue.Fields[fieldID]; ok && fieldID != "" {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

// planSourceClause turns one issue source into a search condition, or an empty
// clause when the source no longer exists.
func (s *Store) planSourceClause(ctx context.Context, workspaceID, userID string, source PlanIssueSource, resolver jql.FieldResolver, args *[]any) (string, error) {
	compile := func(query *jql.Query) (string, error) {
		if err := s.ExpandAppJQL(ctx, workspaceID, query); err != nil {
			return "", err
		}
		compiled := jql.CompileAt(query, userID, resolver, len(*args)+1)
		if compiled.Err != nil {
			return "", compiled.Err
		}
		*args = append(*args, compiled.Args...)
		if strings.TrimSpace(compiled.Where) == "" {
			return "TRUE", nil
		}
		return compiled.Where, nil
	}
	switch source.Type {
	case "Board":
		var boardID string
		err := s.Pool.QueryRow(ctx, `SELECT b.id FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1 AND b.jira_id=$2`, workspaceID, source.Value).Scan(&boardID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		board, err := s.BoardByID(ctx, boardID)
		if err != nil {
			return "", err
		}
		query, err := boardFilterQuery(board, nil, "")
		if err != nil {
			return "", err
		}
		*args = append(*args, board.ProjectID)
		project := len(*args)
		where, err := compile(query)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(i.project_id=$%d AND (%s))", project, where), nil
	case "Project":
		*args = append(*args, strconv.FormatInt(source.Value, 10))
		return fmt.Sprintf("i.project_id=$%d", len(*args)), nil
	case "Filter":
		var filterJQL string
		err := s.Pool.QueryRow(ctx, `SELECT jql FROM filters WHERE workspace_id=$1 AND jira_id=$2`, workspaceID, source.Value).Scan(&filterJQL)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		query, err := jql.Parse(filterJQL)
		if err != nil {
			return "", nil
		}
		where, err := compile(query)
		if err != nil {
			return "", err
		}
		return "(" + where + ")", nil
	}
	return "", nil
}

// PlanWork gathers the work the plan's sources give the user, leaves out what
// its exclusion rules name, and reads each item's dates from the plan's
// scheduling fields.
func (s *Store) PlanWork(ctx context.Context, workspaceID, userID string, plan Plan, now time.Time) (PlanWork, error) {
	work := PlanWork{Items: []models.TimelineItem{}}
	startID, startName, err := s.planDateField(ctx, workspaceID, plan.Scheduling.StartDate)
	if err != nil {
		return work, err
	}
	endID, endName, err := s.planDateField(ctx, workspaceID, plan.Scheduling.EndDate)
	if err != nil {
		return work, err
	}
	work.StartFieldName, work.EndFieldName = startName, endName
	if work.Teams, _, err = s.PlanTeams(ctx, workspaceID, plan.ID, 0, 100); err != nil {
		return work, err
	}
	resolver, err := s.JQLResolver(ctx, workspaceID)
	if err != nil {
		return work, err
	}
	args := []any{workspaceID}
	clauses := []string{}
	for _, source := range plan.IssueSources {
		clause, err := s.planSourceClause(ctx, workspaceID, userID, source, resolver, &args)
		if err != nil {
			return work, err
		}
		if clause != "" {
			clauses = append(clauses, clause)
		}
	}
	if len(clauses) == 0 {
		return work, nil
	}
	args = append(args, userID)
	rows, err := s.Pool.Query(ctx, searchSelect+" "+issueJoinTables()+`
		WHERE i.workspace_id=$1 AND (`+strings.Join(clauses, " OR ")+`) AND `+VisibleIssuePredicate("i", fmt.Sprintf("$%d", len(args)))+`
		ORDER BY i.rank, i.key`, args...)
	if err != nil {
		return work, err
	}
	defer rows.Close()

	rules := plan.ExclusionRules
	ids := func(values []int64) map[int64]bool {
		out := map[int64]bool{}
		for _, value := range values {
			out[value] = true
		}
		return out
	}
	excludedIssues, excludedTypes, excludedStatuses := ids(rules.IssueIDs), ids(rules.IssueTypeIDs), ids(rules.WorkStatusIDs)
	categories := map[int64]string{2: "new", 4: "indeterminate", 3: "done"}
	excludedCategories := map[string]bool{}
	for _, id := range rules.WorkStatusCategoryIDs {
		excludedCategories[categories[id]] = true
	}
	excludedReleases := map[string]bool{}
	for _, id := range rules.ReleaseIDs {
		excludedReleases[strconv.FormatInt(id, 10)] = true
	}
	completedSince := now.UTC().AddDate(0, 0, -rules.NumberOfDaysToShowCompletedIssues)
	excluded := func(issue *models.Issue) bool {
		if issue.IssueType.Subtask || excludedIssues[issue.JiraID] || excludedTypes[issue.IssueType.JiraID] || excludedStatuses[issue.Status.JiraID] || excludedCategories[issue.Status.Category] {
			return true
		}
		var versions []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(issue.Fields["fixVersions"], &versions) == nil {
			for _, version := range versions {
				if excludedReleases[version.ID] {
					return true
				}
			}
		}
		if issue.Status.Category == "done" {
			completed := issue.ResolvedAt
			if completed == "" {
				completed = issue.UpdatedAt
			}
			if at, err := time.Parse(time.RFC3339, completed); err == nil && at.Before(completedSince) {
				return true
			}
		}
		return false
	}
	type entry struct {
		issue *models.Issue
		item  models.TimelineItem
	}
	included := []entry{}
	epics := map[string]int{}
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return work, err
		}
		if excluded(issue) {
			continue
		}
		item := models.TimelineItem{Issue: issue, StartDate: planDateValue(issue, startID), DueDate: planDateValue(issue, endID)}
		if issue.IssueType.HierarchyLevel == 1 {
			epics[issue.ID] = len(included)
		}
		included = append(included, entry{issue: issue, item: item})
	}
	if err := rows.Err(); err != nil {
		return work, err
	}
	// Child work sits under its epic when the plan includes the epic.
	placed := map[int]bool{}
	for index, candidate := range included {
		if parent := candidate.issue.Parent; parent != nil {
			if epicIndex, ok := epics[parent.ID]; ok && epicIndex != index {
				included[epicIndex].item.Children = append(included[epicIndex].item.Children, candidate.item)
				placed[index] = true
			}
		}
	}
	for index, candidate := range included {
		if !placed[index] {
			work.Items = append(work.Items, candidate.item)
		}
	}
	return work, nil
}
