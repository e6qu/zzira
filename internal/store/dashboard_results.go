package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	// Grid is two dimensional statistics' counts.
	Grid *GadgetGrid
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
	// A report gadget draws its project or board report, not a query.
	if models.ReportGadget(g.ModuleKey) {
		return out, nil
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
	// List gadgets such as Assigned to me add their own JQL for the viewer.
	scope := models.GadgetScopeJQL(g.ModuleKey)
	if strings.TrimSpace(query) == "" {
		query, out.JQL = scope, scope
		scope = ""
	}
	compiled := jql.Compiled{OrderSQL: "i.updated_at DESC"}
	if strings.TrimSpace(query) != "" {
		q, e := jql.Parse(query)
		if e != nil {
			return out, e
		}
		if scope != "" {
			scoped, e := jql.Parse(scope)
			if e != nil {
				return out, e
			}
			if q.Root == nil {
				q.Root = scoped.Root
			} else {
				q.Root = jql.And{Terms: []jql.Node{q.Root, scoped.Root}}
			}
		}
		if e := s.ExpandAppJQL(ctx, ws, q); e != nil {
			return out, e
		}
		resolver, e := s.JQLResolver(ctx, ws)
		if e != nil {
			return out, e
		}
		compiled = jql.CompileAt(q, user, resolver, 2)
	}
	if compiled.Err != nil {
		return out, compiled.Err
	}
	if models.ListGadget(g.ModuleKey) {
		out.Issues, out.Total, err = s.Search(ctx, ws, user, compiled, out.Config.Limit, 0)
		return out, err
	}
	where := "i.workspace_id=$1"
	args := []any{ws}
	if compiled.Where != "" {
		where += " AND (" + compiled.Where + ")"
		args = append(args, compiled.Args...)
	}
	args = append(args, user)
	where += " AND " + VisibleIssuePredicate("i", fmt.Sprintf("$%d", len(args)))
	// The total counts each work item once, even when it carries several labels.
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) `+searchJoin+` WHERE `+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	x := gadgetGroupSQL[out.Config.GroupBy]
	from := searchJoin
	if out.Config.GroupBy == "labels" || (g.ModuleKey == "com.zzira:two-dimensional-statistics" && out.Config.YGroupBy == "labels") {
		from += gadgetLabelJoin
	}
	if g.ModuleKey == "com.zzira:two-dimensional-statistics" {
		out.Grid, err = s.gadgetGrid(ctx, `SELECT `+x+`,`+gadgetGroupSQL[out.Config.YGroupBy]+`,count(*) `+from+` WHERE `+where+` GROUP BY 1,2`, args, out.Config.Limit)
		return out, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+x+`,count(*) `+from+` WHERE `+where+` GROUP BY 1 ORDER BY 2 DESC,1`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var count GadgetCount
		if err = rows.Scan(&count.Name, &count.Count); err != nil {
			return out, err
		}
		out.Counts = append(out.Counts, count)
	}
	return out, rows.Err()
}

// gadgetGroupSQL is what each chart grouping counts work by, with the names
// people see for work that has no value.
var gadgetGroupSQL = map[string]string{
	"status":     "st.name",
	"priority":   "COALESCE(pro.name,pr2.name,'None')",
	"issuetype":  "COALESCE(ito.name,it.name)",
	"assignee":   "COALESCE(a.display_name,'Unassigned')",
	"reporter":   "COALESCE(r.display_name,'Anonymous')",
	"resolution": "COALESCE(reso.name,res.name,'Unresolved')",
	"project":    "pr.name",
	"labels":     "COALESCE(gadget_label.name,'None')",
}

// gadgetLabelJoin counts work once under each of its labels.
const gadgetLabelJoin = ` LEFT JOIN LATERAL unnest(i.labels) AS gadget_label(name) ON true`

// GadgetGrid is two dimensional statistics: counts of work for each row value
// across each column value, largest first.
type GadgetGrid struct {
	Columns      []string
	ColumnTotals []int
	Rows         []GadgetGridRow
	Total        int
	// HiddenRows is how many smaller rows fall beyond the result limit.
	HiddenRows int
}

// GadgetGridRow is one row of two dimensional statistics.
type GadgetGridRow struct {
	Name   string
	Counts []int
	Total  int
}

func (s *Store) gadgetGrid(ctx context.Context, query string, args []any, limit int) (*GadgetGrid, error) {
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cells := map[string]map[string]int{}
	columnTotals, rowTotals := map[string]int{}, map[string]int{}
	for rows.Next() {
		var column, row string
		var count int
		if err = rows.Scan(&column, &row, &count); err != nil {
			return nil, err
		}
		if cells[row] == nil {
			cells[row] = map[string]int{}
		}
		cells[row][column] += count
		columnTotals[column] += count
		rowTotals[row] += count
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	largest := func(totals map[string]int) []string {
		names := make([]string, 0, len(totals))
		for name := range totals {
			names = append(names, name)
		}
		sort.Slice(names, func(i, j int) bool {
			if totals[names[i]] != totals[names[j]] {
				return totals[names[i]] > totals[names[j]]
			}
			return names[i] < names[j]
		})
		return names
	}
	grid := &GadgetGrid{Columns: largest(columnTotals)}
	for _, column := range grid.Columns {
		grid.ColumnTotals = append(grid.ColumnTotals, columnTotals[column])
		grid.Total += columnTotals[column]
	}
	for index, name := range largest(rowTotals) {
		if index == limit {
			grid.HiddenRows = len(rowTotals) - limit
			break
		}
		row := GadgetGridRow{Name: name, Total: rowTotals[name]}
		for _, column := range grid.Columns {
			row.Counts = append(row.Counts, cells[name][column])
		}
		grid.Rows = append(grid.Rows, row)
	}
	return grid, nil
}
