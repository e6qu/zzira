package api3

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
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
			if allowed, browseErr := h.canBrowseProject(r, workspaceID, userID, project.ID); browseErr != nil || !allowed {
				continue
			}
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
		priorities, err := h.Store.Priorities(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, 500, "Could not load JQL suggestions.")
			return
		}
		for _, priority := range priorities {
			add(priority.Name, priority.Name)
		}
	case "issuetype":
		types, err := h.Store.IssueTypes(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, 500, "Could not load JQL suggestions.")
			return
		}
		for _, issueType := range types {
			add(issueType.Name, issueType.Name)
		}
	case "assignee", "reporter", "creator":
		if !h.browsesUsers(r, workspaceID, userID) {
			break
		}
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

// jqlPersonalDataMigration converts the people a query names by email or
// display name into account IDs, as Jira's personal data cleaner converts
// usernames and user keys. A person who cannot be found becomes "unknown" and
// the query is reported apart; a query that does not parse fails the request.
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
	users, err := h.Store.SiteUsers(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, 500, "Could not load site users.")
		return
	}
	userFields := map[string]bool{"assignee": true, "reporter": true, "creator": true, "watcher": true, "voter": true}
	if customFields, fieldErr := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID); fieldErr == nil {
		for _, field := range customFields {
			if field.Type == models.CustomFieldUser || field.Type == models.CustomFieldMultiUser {
				userFields[field.ID], userFields[strings.ToLower(field.Name)] = true, true
			}
		}
	}
	converted, unknown := []string{}, []map[string]any{}
	for _, query := range request.QueryStrings {
		operands, _, parseErr := jql.Operands(query)
		if parseErr != nil {
			jiraError(w, 400, "Error in the JQL Query: "+parseErr.Error())
			return
		}
		edits, hasUnknown := []jql.Edit{}, false
		for _, operand := range operands {
			person := operand.Role == "by" || (userFields[operand.Field] && (operand.Role == "value" || operand.Role == "from" || operand.Role == "to"))
			if !person || operand.Function || strings.EqualFold(operand.Value, "empty") || strings.EqualFold(operand.Value, "null") {
				continue
			}
			accountID, found := personalDataAccount(users, operand.Value)
			switch {
			case !found:
				edits, hasUnknown = append(edits, jql.Edit{Start: operand.Start, End: operand.End, Text: "unknown"}), true
			case accountID != operand.Value:
				edits = append(edits, jql.Edit{Start: operand.Start, End: operand.End, Text: jql.Quote(accountID)})
			}
		}
		result := jql.ApplyEdits(query, edits)
		if hasUnknown {
			unknown = append(unknown, map[string]any{"convertedQuery": result, "originalQuery": query})
			continue
		}
		converted = append(converted, result)
	}
	writeJSON(w, 200, map[string]any{"queryStrings": converted, "queriesWithUnknownUsers": unknown})
}

// personalDataAccount finds the one person a query value names: by account
// ID, by email address, or by a display name no one else shares.
func personalDataAccount(users []*models.User, value string) (string, bool) {
	matched := ""
	for _, user := range users {
		switch {
		case user.ID == value:
			return user.ID, true
		case user.Email != "" && strings.EqualFold(user.Email, value):
			return user.ID, true
		case strings.EqualFold(user.DisplayName, value):
			if matched != "" && matched != user.ID {
				return "", false
			}
			matched = user.ID
		}
	}
	return matched, matched != ""
}

// jqlSanitize rewrites what a viewer may not see into IDs: a project, a
// component or version of a project they cannot browse, and a custom field
// shown in none of their projects. With no account ID the viewer is Jira's
// anonymous user.
func (h *Handler) jqlSanitize(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if admin, err := h.Store.IsAdmin(r.Context(), workspaceID, userID); err != nil || !admin {
		jiraError(w, http.StatusForbidden, "You are not authorized to perform this action. Administrator privileges are required.")
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
	if len(request.Queries) == 0 {
		jiraError(w, 400, "The queries has to be provided.")
		return
	}
	if len(request.Queries) > 20 {
		jiraError(w, 400, "No more than 20 queries may be sanitized.")
		return
	}
	seen := map[string]bool{}
	for _, item := range request.Queries {
		key := item.Query + "\x00"
		if item.AccountID != nil {
			key += *item.AccountID
		}
		if seen[key] {
			jiraError(w, 400, "The queries must be unique.")
			return
		}
		seen[key] = true
	}
	results := make([]map[string]any, 0, len(request.Queries))
	for _, item := range request.Queries {
		result := map[string]any{"initialQuery": item.Query}
		viewer := ""
		if item.AccountID != nil {
			result["accountId"] = *item.AccountID
			user, err := h.Store.SiteUser(r.Context(), workspaceID, *item.AccountID)
			if err != nil || user == nil {
				result["errors"] = map[string]any{"errorMessages": []string{"The account ID " + *item.AccountID + " does not identify a user."}, "errors": map[string]string{}}
				results = append(results, result)
				continue
			}
			viewer = user.ID
		}
		sanitized, err := h.sanitizeJQL(r, workspaceID, viewer, item.Query)
		if err != nil {
			result["errors"] = map[string]any{"errorMessages": []string{"Error in the JQL Query: " + err.Error()}, "errors": map[string]string{}}
		} else {
			result["sanitizedQuery"] = sanitized
		}
		results = append(results, result)
	}
	writeJSON(w, 200, map[string]any{"queries": results})
}

func (h *Handler) sanitizeJQL(r *http.Request, workspaceID, viewer, query string) (string, error) {
	operands, fields, err := jql.Operands(query)
	if err != nil {
		return "", err
	}
	ctx := r.Context()
	browsableProjects, err := h.Store.ProjectsWithPermissions(ctx, workspaceID, viewer, []string{"BROWSE_PROJECTS"})
	if err != nil {
		return "", err
	}
	browsable, browsableIDs := map[string]bool{}, []string{}
	for _, project := range browsableProjects {
		browsable[project.ID] = true
		browsableIDs = append(browsableIDs, project.ID)
	}
	projects, err := h.Store.ProjectsByWorkspace(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	edits := []jql.Edit{}
	for _, operand := range operands {
		if operand.Role != "value" || operand.Function {
			continue
		}
		var entities []store.NamedProjectEntity
		switch operand.Field {
		case "project":
			for _, project := range projects {
				if project.ID == operand.Value {
					break
				}
				if strings.EqualFold(project.Key, operand.Value) || strings.EqualFold(project.Name, operand.Value) {
					entities = []store.NamedProjectEntity{{ID: project.ID, ProjectID: project.ID}}
					break
				}
			}
		case "component":
			entities, err = h.Store.ComponentsNamed(ctx, workspaceID, operand.Value)
		case "fixversion", "affectedversion":
			entities, err = h.Store.VersionsNamed(ctx, workspaceID, operand.Value)
		}
		if err != nil {
			return "", err
		}
		hidden := false
		ids := make([]string, 0, len(entities))
		for _, entity := range entities {
			hidden = hidden || !browsable[entity.ProjectID]
			ids = append(ids, entity.ID)
		}
		if !hidden || (len(ids) == 1 && ids[0] == operand.Value) {
			continue
		}
		replacement := strings.Join(ids, ", ")
		if len(ids) > 1 && !operand.InParenthesized {
			// One name standing for several ids becomes a list.
			replacement = "(" + replacement + ")"
			switch operand.Operator {
			case "=":
				edits = append(edits, jql.Edit{Start: operand.OperatorStart, End: operand.OperatorEnd, Text: "in"})
			case "!=":
				edits = append(edits, jql.Edit{Start: operand.OperatorStart, End: operand.OperatorEnd, Text: "not in"})
			}
		}
		edits = append(edits, jql.Edit{Start: operand.Start, End: operand.End, Text: replacement})
	}
	customFields, err := h.Store.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	byName := map[string]*models.CustomField{}
	for _, field := range customFields {
		byName[strings.ToLower(field.Name)] = field
	}
	shown := map[string]bool{}
	for _, reference := range fields {
		field := byName[reference.Name]
		if field == nil || !strings.HasPrefix(field.ID, "customfield_") {
			continue
		}
		visible, known := shown[field.ID]
		if !known {
			if visible, err = h.Store.CustomFieldVisibleInProjects(ctx, workspaceID, field.ID, browsableIDs); err != nil {
				return "", err
			}
			shown[field.ID] = visible
		}
		if !visible {
			edits = append(edits, jql.Edit{Start: reference.Start, End: reference.End, Text: "cf[" + strings.TrimPrefix(field.ID, "customfield_") + "]"})
		}
	}
	return jql.ApplyEdits(query, edits), nil
}
