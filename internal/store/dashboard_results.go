package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

type GadgetCount struct {
	Name  string
	Count int
}
type GadgetResults struct {
	Config models.GadgetConfig
	Issues []*models.Issue
	Counts []GadgetCount
	Total  int
	JQL    string
}

func (s *Store) DashboardGadgetResults(ctx context.Context, ws, user, id string, g models.DashboardGadget) (GadgetResults, error) {
	out := GadgetResults{}
	props, err := s.DashboardProperties(ctx, ws, user, id, g.ID)
	if err != nil {
		return out, err
	}
	if raw := props["zzira.config"]; raw != nil {
		if err = json.Unmarshal(raw, &out.Config); err != nil {
			return out, err
		}
	}
	if err = NormalizeGadgetConfig(&out.Config); err != nil {
		return out, err
	}
	query := out.Config.JQL
	if out.Config.FilterID != "" {
		f, e := s.FilterByID(ctx, ws, user, out.Config.FilterID)
		if e != nil {
			return out, e
		}
		query = f.JQL
	}
	out.JQL = query
	compiled := jql.Compiled{OrderSQL: "i.updated_at DESC"}
	if strings.TrimSpace(query) != "" {
		q, e := jql.Parse(query)
		if e != nil {
			return out, e
		}
		if g.ModuleKey == "com.zzira:assigned-to-me" {
			q.Root = jql.And{Terms: []jql.Node{q.Root, jql.Clause{Field: "assignee", Op: "=", Values: []string{"currentUser()"}}}}
		}
		fields, e := s.CustomFields(ctx)
		if e != nil {
			return out, e
		}
		compiled = jql.CompileAt(q, user, jql.WithCustomFields(jql.DefaultResolver(), fields), 2)
	} else if g.ModuleKey == "com.zzira:assigned-to-me" {
		q, _ := jql.Parse("assignee = currentUser()")
		compiled = jql.CompileAt(q, user, jql.DefaultResolver(), 2)
		out.JQL = "assignee = currentUser()"
	}
	if compiled.Err != nil {
		return out, compiled.Err
	}
	if g.ModuleKey == "com.zzira:filter-results" || g.ModuleKey == "com.zzira:assigned-to-me" {
		out.Issues, out.Total, err = s.Search(ctx, ws, user, compiled, out.Config.Limit, 0)
		return out, err
	}
	group := map[string]string{"status": "st.name", "priority": "COALESCE(pr2.name,'None')", "issuetype": "it.name", "assignee": "COALESCE(a.display_name,'Unassigned')"}[out.Config.GroupBy]
	where := "i.workspace_id=$1"
	args := []any{ws}
	if compiled.Where != "" {
		where += " AND (" + compiled.Where + ")"
		args = append(args, compiled.Args...)
	}
	args = append(args, user)
	where += " AND " + VisibleIssuePredicate("i", fmt.Sprintf("$%d", len(args)))
	rows, err := s.Pool.Query(ctx, `SELECT `+group+`,count(*) `+searchJoin+` WHERE `+where+` GROUP BY `+group+` ORDER BY count(*) DESC,`+group, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var count GadgetCount
		if err = rows.Scan(&count.Name, &count.Count); err != nil {
			return out, err
		}
		out.Total += count.Count
		out.Counts = append(out.Counts, count)
	}
	return out, rows.Err()
}
