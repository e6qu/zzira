package api3

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

type jqlFieldReference struct {
	Value       string   `json:"value"`
	DisplayName string   `json:"displayName"`
	Auto        string   `json:"auto"`
	Orderable   string   `json:"orderable"`
	Searchable  string   `json:"searchable"`
	Operators   []string `json:"operators"`
	Types       []string `json:"types"`
	CFID        string   `json:"cfid,omitempty"`
}

type jqlFunctionReference struct {
	Value                               string   `json:"value"`
	DisplayName                         string   `json:"displayName"`
	IsList                              string   `json:"isList"`
	SupportsListAndSingleValueOperators string   `json:"supportsListAndSingleValueOperators"`
	Types                               []string `json:"types"`
}

var jqlSystemFields = []jqlFieldReference{
	{Value: "affectedVersion", DisplayName: "Affected version", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"VERSION"}},
	{Value: "approvals", DisplayName: "Approvals", Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"=", "!="}, Types: []string{"APPROVAL"}},
	{Value: "assignee", DisplayName: "Assignee", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not", "was", "was in", "was not", "was not in", "changed"}, Types: []string{"USER"}},
	{Value: "component", DisplayName: "Component", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"COMPONENT"}},
	{Value: "created", DisplayName: "Created", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}, Types: []string{"DATE"}},
	{Value: "description", DisplayName: "Description", Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"~", "!~", "is", "is not", "changed"}, Types: []string{"TEXT"}},
	{Value: "due", DisplayName: "Due date", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}, Types: []string{"DATE"}},
	{Value: "environment", DisplayName: "Environment", Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"~", "!~", "is", "is not"}, Types: []string{"TEXT"}},
	{Value: "fixVersion", DisplayName: "Fix version", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"VERSION"}},
	{Value: "id", DisplayName: "Issue ID", Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in"}, Types: []string{"NUMBER"}},
	{Value: "issueType", DisplayName: "Issue type", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"ISSUETYPE"}},
	{Value: "key", DisplayName: "Key", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", ">", ">=", "<", "<="}, Types: []string{"ISSUE"}},
	{Value: "labels", DisplayName: "Labels", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "~", "!~", "is", "is not", "changed"}, Types: []string{"LABEL"}},
	{Value: "parent", DisplayName: "Parent", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not", "changed"}, Types: []string{"ISSUE"}},
	{Value: "priority", DisplayName: "Priority", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not", "was", "changed"}, Types: []string{"PRIORITY"}},
	{Value: "project", DisplayName: "Project", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in"}, Types: []string{"PROJECT"}},
	{Value: "reporter", DisplayName: "Reporter", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"USER"}},
	{Value: "resolution", DisplayName: "Resolution", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"RESOLUTION"}},
	{Value: "resolutionDate", DisplayName: "Resolved", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}, Types: []string{"DATE"}},
	{Value: "sprint", DisplayName: "Sprint", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"SPRINT"}},
	{Value: "status", DisplayName: "Status", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not", "was", "was in", "was not", "was not in", "changed"}, Types: []string{"STATUS"}},
	{Value: "statusCategory", DisplayName: "Status category", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in"}, Types: []string{"STATUS_CATEGORY"}},
	{Value: "summary", DisplayName: "Summary", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"~", "!~", "=", "!=", "is", "is not", "changed"}, Types: []string{"TEXT"}},
	{Value: "updated", DisplayName: "Updated", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}, Types: []string{"DATE"}},
}

var jqlFunctions = []jqlFunctionReference{
	{Value: "approved()", DisplayName: "approved()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "approver()", DisplayName: "approver(users...)", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "breached()", DisplayName: "breached()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "closedSprints()", DisplayName: "closedSprints()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"SPRINT"}},
	{Value: "completed()", DisplayName: "completed()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "componentsLeadByUser()", DisplayName: "componentsLeadByUser([user])", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"COMPONENT"}},
	{Value: "currentLogin()", DisplayName: "currentLogin()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
	{Value: "currentUser()", DisplayName: "currentUser()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"USER"}},
	{Value: "everBreached()", DisplayName: "everBreached()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "futureSprints()", DisplayName: "futureSprints()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"SPRINT"}},
	{Value: "linkedIssues()", DisplayName: "linkedIssues(issueKey[, linkTypes...])", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUE"}},
	{Value: "linkedWorkItems()", DisplayName: "linkedWorkItems(workItemKey[, linkTypes...])", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUE"}},
	{Value: "lastLogin()", DisplayName: "lastLogin()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
	{Value: "membersOf()", DisplayName: "membersOf(group)", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"USER"}},
	{Value: "myApproval()", DisplayName: "myApproval()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "myPending()", DisplayName: "myPending()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "myPendingApproval()", DisplayName: "myPendingApproval()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "now()", DisplayName: "now()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
	{Value: "openSprints()", DisplayName: "openSprints()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"SPRINT"}},
	{Value: "standardIssueTypes()", DisplayName: "standardIssueTypes()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUETYPE"}},
	{Value: "standardWorkTypes()", DisplayName: "standardWorkTypes()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUETYPE"}},
	{Value: "subtaskIssueTypes()", DisplayName: "subtaskIssueTypes()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUETYPE"}},
	{Value: "subtaskWorkTypes()", DisplayName: "subtaskWorkTypes()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUETYPE"}},
	{Value: "releasedVersions()", DisplayName: "releasedVersions([project])", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"VERSION"}},
	{Value: "unreleasedVersions()", DisplayName: "unreleasedVersions([project])", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"VERSION"}},
	{Value: "latestReleasedVersion()", DisplayName: "latestReleasedVersion(project)", IsList: "false", SupportsListAndSingleValueOperators: "true", Types: []string{"VERSION"}},
	{Value: "earliestUnreleasedVersion()", DisplayName: "earliestUnreleasedVersion(project)", IsList: "false", SupportsListAndSingleValueOperators: "true", Types: []string{"VERSION"}},
	{Value: "projectsLeadByUser()", DisplayName: "projectsLeadByUser([user])", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"PROJECT"}},
	{Value: "spacesLeadByUser()", DisplayName: "spacesLeadByUser([user])", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"PROJECT"}},
	{Value: "projectsWhereUserHasRole()", DisplayName: "projectsWhereUserHasRole(role)", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"PROJECT"}},
	{Value: "pending()", DisplayName: "pending()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "pendingApprovalBy()", DisplayName: "pendingApprovalBy(users...)", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "pendingBy()", DisplayName: "pendingBy(users...)", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "paused()", DisplayName: "paused()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "remaining()", DisplayName: "remaining([duration])", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "running()", DisplayName: "running()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "spacesWhereUserHasRole()", DisplayName: "spacesWhereUserHasRole(role)", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"PROJECT"}},
	{Value: "updatedBy()", DisplayName: "updatedBy(user[, from[, to]])", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUE"}},
	{Value: "votedIssues()", DisplayName: "votedIssues()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUE"}},
	{Value: "votedWorkItems()", DisplayName: "votedWorkItems()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUE"}},
	{Value: "watchedIssues()", DisplayName: "watchedIssues()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUE"}},
	{Value: "watchedWorkItems()", DisplayName: "watchedWorkItems()", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"ISSUE"}},
	{Value: "withinCalendarHours()", DisplayName: "withinCalendarHours()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "startOfDay()", DisplayName: "startOfDay([increment])", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
	{Value: "endOfDay()", DisplayName: "endOfDay([increment])", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
	{Value: "startOfWeek()", DisplayName: "startOfWeek([increment])", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
	{Value: "endOfWeek()", DisplayName: "endOfWeek([increment])", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
	{Value: "startOfMonth()", DisplayName: "startOfMonth([increment])", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
	{Value: "endOfMonth()", DisplayName: "endOfMonth([increment])", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
	{Value: "startOfYear()", DisplayName: "startOfYear([increment])", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
	{Value: "endOfYear()", DisplayName: "endOfYear([increment])", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"DATE"}},
}

var jqlReservedWords = []string{"and", "asc", "by", "changed", "desc", "during", "empty", "from", "in", "is", "not", "null", "or", "order", "to", "was"}

func decodeJQLBody(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid JQL request: "+err.Error())
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		jiraError(w, http.StatusBadRequest, "Expected one JSON object.")
		return false
	}
	return true
}

func (h *Handler) jqlAutoCompleteData(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method == http.MethodPost {
		var request struct {
			IncludeCollapsedFields bool    `json:"includeCollapsedFields"`
			ProjectIDs             []int64 `json:"projectIds"`
		}
		if !decodeJQLBody(w, r, &request) {
			return
		}
		if len(request.ProjectIDs) > 1000 {
			jiraError(w, http.StatusBadRequest, "No more than 1000 project IDs may be supplied.")
			return
		}
	}
	fields := append([]jqlFieldReference{}, jqlSystemFields...)
	functions := append([]jqlFunctionReference{}, jqlFunctions...)
	customFields, err := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load JQL fields.")
		return
	}
	for _, field := range customFields {
		types := []string{"TEXT"}
		operators := []string{"=", "!=", "~", "!~", "in", "not in", "is", "is not"}
		if string(field.Type) == "number" {
			types = []string{"NUMBER"}
			operators = []string{"=", "!=", ">", ">=", "<", "<=", "in", "not in", "is", "is not"}
		} else if string(field.Type) == "datetime" {
			types = []string{"DATE"}
			operators = []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}
		}
		fields = append(fields, jqlFieldReference{Value: field.ID, CFID: strings.TrimPrefix(field.ID, "customfield_"), DisplayName: field.Name + " - cf[" + strings.TrimPrefix(field.ID, "customfield_") + "]", Auto: "false", Orderable: "false", Searchable: "true", Operators: operators, Types: types})
	}
	slaMetrics, err := h.Store.ServiceSLAMetricsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load JQL SLA fields.")
		return
	}
	seenSLAs := make(map[string]bool)
	for _, metric := range slaMetrics {
		key := strings.ToLower(metric.Name)
		if seenSLAs[key] {
			continue
		}
		seenSLAs[key] = true
		fields = append(fields, jqlFieldReference{Value: metric.Name, CFID: metric.ID, DisplayName: metric.Name, Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<="}, Types: []string{"SLA"}})
	}
	appFunctions, err := h.Store.ActiveAppJQLFunctions(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load JQL functions.")
		return
	}
	for _, function := range appFunctions {
		arguments := make([]string, 0, len(function.Arguments))
		for _, argument := range function.Arguments {
			name := argument.Name
			if !argument.Required {
				name = "[" + name + "]"
			}
			arguments = append(arguments, name)
		}
		types := make([]string, 0, len(function.Types))
		for _, value := range function.Types {
			types = append(types, strings.ToUpper(value))
		}
		isList := "false"
		for _, operator := range function.Operators {
			if operator == "in" || operator == "not_in" {
				isList = "true"
			}
		}
		display := function.Name + "(" + strings.Join(arguments, ", ") + ")"
		functions = append(functions, jqlFunctionReference{Value: function.Name + "()", DisplayName: display, IsList: isList, SupportsListAndSingleValueOperators: "true", Types: types})
	}
	sort.Slice(fields, func(i, k int) bool {
		return strings.ToLower(fields[i].DisplayName) < strings.ToLower(fields[k].DisplayName)
	})
	sort.Slice(functions, func(i, k int) bool { return strings.ToLower(functions[i].Value) < strings.ToLower(functions[k].Value) })
	writeJSON(w, http.StatusOK, map[string]any{"jqlReservedWords": jqlReservedWords, "visibleFieldNames": fields, "visibleFunctionNames": functions})
}

type jqlSuggestion struct {
	DisplayName string `json:"displayName"`
	Value       string `json:"value"`
}

func (h *Handler) jqlSuggestions(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	field := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("fieldName")))
	needle := strings.TrimSpace(r.URL.Query().Get("fieldValue"))
	values := []jqlSuggestion{}
	add := func(value, label string) {
		if value == "" || needle != "" && !strings.Contains(strings.ToLower(value+" "+label), strings.ToLower(needle)) {
			return
		}
		values = append(values, jqlSuggestion{Value: jqlQuote(value), DisplayName: highlightJQLSuggestion(label, needle)})
	}
	switch field {
	case "project":
		projects, err := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, 500, "Could not load JQL suggestions.")
			return
		}
		for _, project := range projects {
			add(project.Key, project.Name+" ("+project.Key+")")
		}
	case "status", "statuscategory":
		statuses, err := h.Store.StatusesForWorkspace(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, 500, "Could not load JQL suggestions.")
			return
		}
		seen := map[string]bool{}
		for _, status := range statuses {
			value, label := status.Name, status.Name
			if field == "statuscategory" {
				value, label = status.Category, status.Category
			}
			if !seen[strings.ToLower(value)] {
				add(value, label)
				seen[strings.ToLower(value)] = true
			}
		}
	case "priority":
		priorities, err := h.Store.Priorities(r.Context())
		if err != nil {
			jiraError(w, 500, "Could not load JQL suggestions.")
			return
		}
		for _, priority := range priorities {
			add(priority.Name, priority.Name)
		}
	case "issuetype":
		types, err := h.Store.IssueTypes(r.Context())
		if err != nil {
			jiraError(w, 500, "Could not load JQL suggestions.")
			return
		}
		for _, issueType := range types {
			add(issueType.Name, issueType.Name)
		}
	case "assignee", "reporter", "creator":
		members, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, 500, "Could not load JQL suggestions.")
			return
		}
		for _, member := range members {
			add(member.ID, member.DisplayName)
		}
	case "labels", "component", "sprint", "resolution", "fixversion", "affectedversion":
		stored, err := h.Store.JQLFieldSuggestions(r.Context(), workspaceID, userID, field, needle, 50)
		if err != nil {
			jiraError(w, 500, "Could not load JQL suggestions.")
			return
		}
		for _, value := range stored {
			add(value, value)
		}
	default:
		jiraError(w, http.StatusBadRequest, "The field does not provide autocomplete suggestions.")
		return
	}
	if len(values) > 50 {
		values = values[:50]
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": values})
}

func jqlQuote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n(),\"'") {
		return value
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func highlightJQLSuggestion(value, needle string) string {
	escaped := html.EscapeString(value)
	if needle == "" {
		return escaped
	}
	lower, match := strings.ToLower(value), strings.ToLower(needle)
	start := strings.Index(lower, match)
	if start < 0 {
		return escaped
	}
	return html.EscapeString(value[:start]) + "<b>" + html.EscapeString(value[start:start+len(needle)]) + "</b>" + html.EscapeString(value[start+len(needle):])
}

func (h *Handler) jqlParse(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	validation := r.URL.Query().Get("validation")
	if validation == "" {
		validation = "strict"
	}
	if validation != "strict" && validation != "warn" && validation != "none" {
		jiraError(w, 400, "validation must be strict, warn, or none.")
		return
	}
	var request struct {
		Queries []string `json:"queries"`
	}
	if !decodeJQLBody(w, r, &request) {
		return
	}
	if len(request.Queries) < 1 || len(request.Queries) > 100 {
		jiraError(w, 400, "queries must contain between 1 and 100 JQL queries.")
		return
	}
	results := make([]map[string]any, 0, len(request.Queries))
	for _, raw := range request.Queries {
		result := map[string]any{"query": raw, "errors": []string{}, "warnings": []string{}}
		parsed, err := jql.Parse(raw)
		if err != nil {
			result["errors"] = []string{err.Error()}
			results = append(results, result)
			continue
		}
		if validation != "none" {
			if _, compileErr := h.compileJQL(r.Context(), workspaceID, raw, userID); compileErr != nil {
				if validation == "warn" {
					result["warnings"] = []string{compileErr.message}
				} else {
					result["errors"] = []string{compileErr.message}
					results = append(results, result)
					continue
				}
			}
		}
		result["structure"] = jqlQueryStructure(parsed)
		results = append(results, result)
	}
	writeJSON(w, 200, map[string]any{"queries": results})
}

func jqlQueryStructure(query *jql.Query) map[string]any {
	structure := map[string]any{"where": jqlNodeStructure(query.Root)}
	orders := query.Orders
	if len(orders) == 0 && query.OrderBy != nil {
		orders = []jql.Order{*query.OrderBy}
	}
	if len(orders) > 0 {
		fields := make([]map[string]any, 0, len(orders))
		for _, order := range orders {
			direction := "asc"
			if order.Desc {
				direction = "desc"
			}
			fields = append(fields, map[string]any{"field": map[string]any{"name": order.Field}, "direction": direction})
		}
		structure["orderBy"] = map[string]any{"fields": fields}
	}
	return structure
}

func jqlNodeStructure(node jql.Node) map[string]any {
	switch value := node.(type) {
	case jql.And:
		return jqlCompoundStructure("and", value.Terms)
	case jql.Or:
		return jqlCompoundStructure("or", value.Terms)
	case jql.Not:
		return jqlCompoundStructure("not", []jql.Node{value.Inner})
	case jql.Text:
		return map[string]any{"field": map[string]any{"name": "text"}, "operator": "~", "operand": jqlOperand(value.Value)}
	case jql.Clause:
		op := value.Op
		operand := jqlOperands(value.Values, value.Op == "in" || value.Op == "notin")
		if op == "notin" {
			op = "not in"
		} else if op == "empty" {
			op = "is"
			operand = jqlOperand("empty")
		} else if op == "notempty" {
			op = "is not"
			operand = jqlOperand("empty")
		}
		return map[string]any{"field": map[string]any{"name": value.Field}, "operator": op, "operand": operand}
	case jql.HistoryClause:
		op := strings.ReplaceAll(value.Op, "notin", "not in")
		op = strings.ReplaceAll(op, "wasin", "was in")
		op = strings.ReplaceAll(op, "wasnot", "was not")
		predicates := make([]map[string]any, 0, len(value.Predicates))
		for _, predicate := range value.Predicates {
			predicates = append(predicates, map[string]any{"operator": predicate.Kind, "operand": jqlOperands(predicate.Values, len(predicate.Values) > 1)})
		}
		out := map[string]any{"field": map[string]any{"name": value.Field}, "operator": op, "predicates": predicates}
		if value.Op != "changed" {
			out["operand"] = jqlOperands(value.Values, strings.Contains(value.Op, "in"))
		}
		return out
	default:
		return map[string]any{}
	}
}

func jqlCompoundStructure(operator string, nodes []jql.Node) map[string]any {
	clauses := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		clauses = append(clauses, jqlNodeStructure(node))
	}
	return map[string]any{"operator": operator, "clauses": clauses}
}

func jqlOperands(values []string, list bool) map[string]any {
	if !list && len(values) == 1 {
		return jqlOperand(values[0])
	}
	items := make([]map[string]any, 0, len(values))
	for _, value := range values {
		items = append(items, jqlOperand(value))
	}
	return map[string]any{"values": items}
}

func jqlOperand(value string) map[string]any {
	if strings.EqualFold(value, "empty") || strings.EqualFold(value, "null") {
		return map[string]any{"keyword": "empty"}
	}
	if open := strings.IndexByte(value, '('); open > 0 && strings.HasSuffix(value, ")") {
		arguments := []string{}
		if body := value[open+1 : len(value)-1]; body != "" {
			arguments = strings.Split(body, ",")
		}
		return map[string]any{"function": value[:open], "arguments": arguments}
	}
	return map[string]any{"value": value}
}

func (h *Handler) jqlMatch(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		IssueIDs []int64  `json:"issueIds"`
		JQLs     []string `json:"jqls"`
	}
	if !decodeJQLBody(w, r, &request) {
		return
	}
	if len(request.IssueIDs) > 1000 || len(request.JQLs) < 1 || len(request.JQLs) > 20 {
		jiraError(w, 400, "issueIds accepts at most 1000 values and jqls must contain 1 to 20 queries.")
		return
	}
	requested := map[int64]bool{}
	requestedOrder := make([]int64, 0, len(request.IssueIDs))
	for _, id := range request.IssueIDs {
		requested[id] = true
		requestedOrder = append(requestedOrder, id)
	}
	matches := make([]map[string]any, 0, len(request.JQLs))
	for _, raw := range request.JQLs {
		compiled, compileErr := h.compileJQL(r.Context(), workspaceID, raw, userID)
		if compileErr != nil {
			matches = append(matches, map[string]any{"matchedIssues": []any{}, "errors": []string{compileErr.message}})
			continue
		}
		matchedIDs, err := h.Store.MatchIssueIDs(r.Context(), workspaceID, userID, compiled, requestedOrder)
		if err != nil {
			matches = append(matches, map[string]any{"matchedIssues": []any{}, "errors": []string{err.Error()}})
			continue
		}
		matchedSet := map[int64]bool{}
		for _, id := range matchedIDs {
			matchedSet[id] = true
		}
		matched := []int64{}
		for _, id := range requestedOrder {
			if requested[id] && matchedSet[id] {
				matched = append(matched, id)
			}
		}
		matches = append(matches, map[string]any{"matchedIssues": matched, "errors": []string{}})
	}
	writeJSON(w, 200, map[string]any{"matches": matches})
}

func (h *Handler) jqlPersonalDataMigration(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		QueryStrings []string `json:"queryStrings"`
	}
	if !decodeJQLBody(w, r, &request) {
		return
	}
	if len(request.QueryStrings) > 100 {
		jiraError(w, 400, "No more than 100 queries may be converted.")
		return
	}
	members, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, 500, "Could not load workspace users.")
		return
	}
	converted := append([]string{}, request.QueryStrings...)
	for index, query := range converted {
		converted[index] = migrateJQLPersonalData(query, members)
	}
	writeJSON(w, 200, map[string]any{"queryStrings": converted, "queriesWithUnknownUsers": []any{}})
}

func migrateJQLPersonalData(query string, members []*models.User) string {
	for _, member := range members {
		for _, identity := range []string{member.Email, member.DisplayName} {
			if identity != "" {
				pattern := `(?i)(\b(?:assignee|reporter|creator)\s*(?:=|!=)\s*)(?:"` + regexp.QuoteMeta(identity) + `"|'` + regexp.QuoteMeta(identity) + `'|` + regexp.QuoteMeta(identity) + `\b)`
				query = regexp.MustCompile(pattern).ReplaceAllString(query, `${1}`+jqlQuote(member.ID))
			}
		}
	}
	return query
}

func (h *Handler) jqlSanitize(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Queries []struct {
			AccountID *string `json:"accountId"`
			Query     string  `json:"query"`
		} `json:"queries"`
	}
	if !decodeJQLBody(w, r, &request) {
		return
	}
	if len(request.Queries) > 20 {
		jiraError(w, 400, "No more than 20 queries may be sanitized.")
		return
	}
	results := make([]map[string]any, 0, len(request.Queries))
	for _, item := range request.Queries {
		viewer := userID
		if item.AccountID != nil && *item.AccountID != "" {
			viewer = *item.AccountID
		}
		result := map[string]any{"accountId": item.AccountID, "initialQuery": item.Query}
		if _, compileErr := h.compileJQL(r.Context(), workspaceID, item.Query, viewer); compileErr != nil {
			result["sanitizedQuery"] = nil
			result["errors"] = map[string]any{"errorMessages": []string{compileErr.message}, "errors": map[string]string{}}
		} else {
			result["sanitizedQuery"] = item.Query
			result["errors"] = map[string]any{"errorMessages": []string{}, "errors": map[string]string{}}
		}
		results = append(results, result)
	}
	writeJSON(w, 200, map[string]any{"queries": results})
}
