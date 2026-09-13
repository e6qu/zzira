package store

import (
	"context"
	"strings"

	"github.com/e6qu/zzira/internal/jql"
)

// normalizeJQLWireIDs lets a query name statuses, projects and sprints by the
// numeric ids clients see, as Jira's JQL does: each such id becomes the name,
// key or stored id the compiler matches on. Values that are not a known id of
// that kind are left as they are.
func (s *Store) normalizeJQLWireIDs(ctx context.Context, workspaceID string, query *jql.Query) error {
	if query == nil || query.Root == nil {
		return nil
	}
	fields := map[string]bool{}
	collectJQLFields(query.Root, fields)
	if !fields["status"] && !fields["project"] && !fields["sprint"] {
		return nil
	}
	replacements := map[string]map[string]string{"status": {}, "project": {}, "sprint": {}}
	lookups := []struct {
		field, sql string
	}{
		{"status", `SELECT jira_id::text, name FROM statuses WHERE workspace_id IS NULL OR workspace_id=$1`},
		{"project", `SELECT id, key FROM projects WHERE workspace_id=$1 AND id ~ '^[0-9]+$'`},
		{"sprint", `SELECT s.jira_id::text, s.id FROM sprints s JOIN boards b ON b.id=s.board_id JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1`},
	}
	for _, lookup := range lookups {
		if !fields[lookup.field] {
			continue
		}
		rows, err := s.Pool.Query(ctx, lookup.sql, workspaceID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var wire, value string
			if err = rows.Scan(&wire, &value); err != nil {
				rows.Close()
				return err
			}
			replacements[lookup.field][wire] = value
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
	}
	query.Root = rewriteJQLWireIDs(query.Root, replacements)
	return nil
}

func collectJQLFields(node jql.Node, fields map[string]bool) {
	switch n := node.(type) {
	case jql.Or:
		for _, term := range n.Terms {
			collectJQLFields(term, fields)
		}
	case jql.And:
		for _, term := range n.Terms {
			collectJQLFields(term, fields)
		}
	case jql.Not:
		collectJQLFields(n.Inner, fields)
	case jql.Clause:
		fields[strings.ToLower(n.Field)] = true
	case jql.HistoryClause:
		fields[strings.ToLower(n.Field)] = true
	}
}

func rewriteJQLValues(field string, values []string, replacements map[string]map[string]string) []string {
	byID := replacements[strings.ToLower(field)]
	if len(byID) == 0 {
		return values
	}
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = value
		if replacement, ok := byID[strings.Trim(strings.TrimSpace(value), `"'`)]; ok {
			out[index] = replacement
		}
	}
	return out
}

func rewriteJQLWireIDs(node jql.Node, replacements map[string]map[string]string) jql.Node {
	switch n := node.(type) {
	case jql.Or:
		terms := make([]jql.Node, len(n.Terms))
		for index, term := range n.Terms {
			terms[index] = rewriteJQLWireIDs(term, replacements)
		}
		return jql.Or{Terms: terms}
	case jql.And:
		terms := make([]jql.Node, len(n.Terms))
		for index, term := range n.Terms {
			terms[index] = rewriteJQLWireIDs(term, replacements)
		}
		return jql.And{Terms: terms}
	case jql.Not:
		return jql.Not{Inner: rewriteJQLWireIDs(n.Inner, replacements)}
	case jql.Clause:
		n.Values = rewriteJQLValues(n.Field, n.Values, replacements)
		return n
	case jql.HistoryClause:
		n.Values = rewriteJQLValues(n.Field, n.Values, replacements)
		for index := range n.Predicates {
			n.Predicates[index].Values = rewriteJQLValues(n.Field, n.Predicates[index].Values, replacements)
		}
		return n
	}
	return node
}
