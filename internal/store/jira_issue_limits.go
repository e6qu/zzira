package store

import (
	"context"
	"strconv"
)

// IssueEntityLimits are Jira's per-issue limits on how many of each item an issue holds.
var IssueEntityLimits = map[string]int{
	"comment":          5000,
	"worklog":          10000,
	"attachment":       2000,
	"issuelinks":       2000,
	"remoteIssueLinks": 2000,
}

// IssueADFSizeLimit is the most bytes one rich-text value may hold.
const IssueADFSizeLimit = 32767

// IssueADFFieldTypes are the rich-text values the ADF limit report covers.
var IssueADFFieldTypes = []string{"comment_adf", "worklog_adf", "customfield_adf", "description_adf", "environment_adf"}

// issueLimitApproachingShare is the share of a limit at which an issue is reported as approaching it.
const issueLimitApproachingShare = 0.8

// IssueLimitReport lists the issues at or near a limit, keyed by field and then
// by issue id or key, and, for the ADF report, the entities breaching it.
type IssueLimitReport struct {
	Limits      map[string]int
	Breaching   map[string]map[string]int
	Approaching map[string]map[string]int
	Entities    map[string]map[string][]string
}

func newIssueLimitReport(limits map[string]int) IssueLimitReport {
	report := IssueLimitReport{Limits: limits, Breaching: map[string]map[string]int{}, Approaching: map[string]map[string]int{}, Entities: map[string]map[string][]string{}}
	for field := range limits {
		report.Breaching[field] = map[string]int{}
		report.Approaching[field] = map[string]int{}
	}
	return report
}

func (report IssueLimitReport) add(field, issueRef string, value int) {
	limit := report.Limits[field]
	switch {
	case value >= limit:
		report.Breaching[field][issueRef] = value
	case float64(value) >= float64(limit)*issueLimitApproachingShare:
		report.Approaching[field][issueRef] = value
	}
}

// IssueLimitCounts reports the issues a person can see that breach or approach
// Jira's per-issue item limits.
func (s *Store) IssueLimitCounts(ctx context.Context, workspaceID, userID string, byKey bool) (IssueLimitReport, error) {
	report := newIssueLimitReport(IssueEntityLimits)
	queries := map[string]string{
		"comment":          `SELECT issue_id, count(*) FROM comments GROUP BY issue_id`,
		"worklog":          `SELECT issue_id, count(*) FROM worklogs GROUP BY issue_id`,
		"attachment":       `SELECT issue_id, count(*) FROM attachments GROUP BY issue_id`,
		"remoteIssueLinks": `SELECT issue_id, count(*) FROM remote_issue_links GROUP BY issue_id`,
		"issuelinks":       `SELECT issue_id, count(*) FROM (SELECT inward_id AS issue_id FROM issue_links UNION ALL SELECT outward_id FROM issue_links) l GROUP BY issue_id`,
	}
	for field, counts := range queries {
		threshold := int(float64(IssueEntityLimits[field]) * issueLimitApproachingShare)
		rows, err := s.Pool.Query(ctx, `SELECT i.jira_id, i.key, c.count FROM (`+counts+`) c(issue_id, count)
			JOIN issues i ON i.id=c.issue_id
			WHERE i.workspace_id=$1 AND c.count >= $3 AND `+VisibleIssuePredicate("i", "$2"), workspaceID, userID, threshold)
		if err != nil {
			return report, err
		}
		for rows.Next() {
			var jiraID int64
			var key string
			var count int
			if err = rows.Scan(&jiraID, &key, &count); err != nil {
				rows.Close()
				return report, err
			}
			report.add(field, issueLimitRef(jiraID, key, byKey), count)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return report, err
		}
	}
	return report, nil
}

// IssueADFSizes reports the issues a person can see whose rich-text values
// breach or approach the ADF size limit, with the entities that breach it.
func (s *Store) IssueADFSizes(ctx context.Context, workspaceID, userID string, byKey bool, fieldTypes []string) (IssueLimitReport, error) {
	limits := map[string]int{}
	for _, fieldType := range fieldTypes {
		limits[fieldType] = IssueADFSizeLimit
	}
	report := newIssueLimitReport(limits)
	// Each query yields the issue, the entity holding the value, and its size.
	queries := map[string]string{
		"comment_adf":     `SELECT c.issue_id, c.jira_id::text, octet_length(c.body::text) FROM comments c`,
		"worklog_adf":     `SELECT w.issue_id, w.id, octet_length(COALESCE(w.comment::text,'')) FROM worklogs w`,
		"description_adf": `SELECT i.id, i.jira_id::text, octet_length(COALESCE(i.description::text,'')) FROM issues i`,
		"environment_adf": `SELECT i.id, i.jira_id::text, octet_length(CASE WHEN jsonb_typeof(i.fields)='object' THEN COALESCE((i.fields->'environment')::text,'') ELSE '' END) FROM issues i`,
		"customfield_adf": `SELECT i.id, f.key, octet_length(f.value::text) FROM issues i, jsonb_each(CASE WHEN jsonb_typeof(i.fields)='object' THEN i.fields ELSE '{}'::jsonb END) f
			WHERE f.key LIKE 'customfield\_%' AND jsonb_typeof(f.value)='object' AND f.value->>'type'='doc'`,
	}
	sizeLimit := IssueADFSizeLimit
	threshold := int(float64(sizeLimit) * issueLimitApproachingShare)
	for _, fieldType := range fieldTypes {
		sizes, ok := queries[fieldType]
		if !ok {
			continue
		}
		rows, err := s.Pool.Query(ctx, `SELECT i.jira_id, i.key, v.entity, v.size FROM (`+sizes+`) v(issue_id, entity, size)
			JOIN issues i ON i.id=v.issue_id
			WHERE i.workspace_id=$1 AND v.size >= $3 AND `+VisibleIssuePredicate("i", "$2")+`
			ORDER BY i.jira_id, v.entity`, workspaceID, userID, threshold)
		if err != nil {
			return report, err
		}
		for rows.Next() {
			var jiraID int64
			var key, entity string
			var size int
			if err = rows.Scan(&jiraID, &key, &entity, &size); err != nil {
				rows.Close()
				return report, err
			}
			ref := issueLimitRef(jiraID, key, byKey)
			if size > report.Breaching[fieldType][ref] && size >= IssueADFSizeLimit {
				report.Breaching[fieldType][ref] = size
			} else if size < IssueADFSizeLimit && size > report.Approaching[fieldType][ref] {
				report.Approaching[fieldType][ref] = size
			}
			if size >= IssueADFSizeLimit {
				if report.Entities[fieldType] == nil {
					report.Entities[fieldType] = map[string][]string{}
				}
				report.Entities[fieldType][ref] = append(report.Entities[fieldType][ref], entity)
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return report, err
		}
	}
	return report, nil
}

func issueLimitRef(jiraID int64, key string, byKey bool) string {
	if byKey {
		return key
	}
	return strconv.FormatInt(jiraID, 10)
}
