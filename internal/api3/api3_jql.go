package api3

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"sort"
	"strconv"
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
	{Value: "attachments", DisplayName: "Attachments", Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"is", "is not"}, Types: []string{"ATTACHMENT"}},
	{Value: "category", DisplayName: "Project category", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"CATEGORY"}},
	{Value: "comment", DisplayName: "Comment", Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"~", "!~"}, Types: []string{"TEXT"}},
	{Value: "component", DisplayName: "Component", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"COMPONENT"}},
	{Value: "created", DisplayName: "Created", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}, Types: []string{"DATE"}},
	{Value: "description", DisplayName: "Description", Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"~", "!~", "is", "is not", "changed"}, Types: []string{"TEXT"}},
	{Value: "due", DisplayName: "Due date", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}, Types: []string{"DATE"}},
	{Value: "environment", DisplayName: "Environment", Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"~", "!~", "is", "is not"}, Types: []string{"TEXT"}},
	{Value: "fixVersion", DisplayName: "Fix version", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"VERSION"}},
	{Value: "hierarchyLevel", DisplayName: "Hierarchy level", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "in", "not in"}, Types: []string{"NUMBER"}},
	{Value: "id", DisplayName: "Issue ID", Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in"}, Types: []string{"NUMBER"}},
	{Value: "issueLinkType", DisplayName: "Issue link type", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"ISSUE_LINK_TYPE"}},
	{Value: "issueType", DisplayName: "Issue type", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"ISSUETYPE"}},
	{Value: "key", DisplayName: "Key", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", ">", ">=", "<", "<="}, Types: []string{"ISSUE"}},
	{Value: "labels", DisplayName: "Labels", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "~", "!~", "is", "is not", "changed"}, Types: []string{"LABEL"}},
	{Value: "level", DisplayName: "Security level", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"SECURITY_LEVEL"}},
	{Value: "parent", DisplayName: "Parent", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not", "changed"}, Types: []string{"ISSUE"}},
	{Value: "priority", DisplayName: "Priority", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not", "was", "changed"}, Types: []string{"PRIORITY"}},
	{Value: "project", DisplayName: "Project", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in"}, Types: []string{"PROJECT"}},
	{Value: "reporter", DisplayName: "Reporter", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"USER"}},
	{Value: "Request participants", DisplayName: "Request participants", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"USER"}},
	{Value: "request-channel-type", DisplayName: "Request channel type", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"STRING"}},
	{Value: "resolution", DisplayName: "Resolution", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"RESOLUTION"}},
	{Value: "resolutionDate", DisplayName: "Resolved", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}, Types: []string{"DATE"}},
	{Value: "sprint", DisplayName: "Sprint", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"SPRINT"}},
	{Value: "status", DisplayName: "Status", Auto: "true", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not", "was", "was in", "was not", "was not in", "changed"}, Types: []string{"STATUS"}},
	{Value: "statusCategory", DisplayName: "Status category", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in"}, Types: []string{"STATUS_CATEGORY"}},
	{Value: "statusCategoryChangedDate", DisplayName: "Status category changed", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}, Types: []string{"DATE"}},
	{Value: "summary", DisplayName: "Summary", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"~", "!~", "=", "!=", "is", "is not", "changed"}, Types: []string{"TEXT"}},
	{Value: "text", DisplayName: "Text", Auto: "false", Orderable: "false", Searchable: "true", Operators: []string{"~", "!~"}, Types: []string{"TEXT"}},
	{Value: "updated", DisplayName: "Updated", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}, Types: []string{"DATE"}},
	{Value: "voter", DisplayName: "Voter", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"USER"}},
	{Value: "votes", DisplayName: "Votes", Auto: "false", Orderable: "true", Searchable: "true", Operators: []string{"=", "!=", ">", ">=", "<", "<=", "in", "not in"}, Types: []string{"NUMBER"}},
	{Value: "watcher", DisplayName: "Watcher", Auto: "true", Orderable: "false", Searchable: "true", Operators: []string{"=", "!=", "in", "not in", "is", "is not"}, Types: []string{"USER"}},
}

var jqlFunctions = []jqlFunctionReference{
	{Value: "approved()", DisplayName: "approved()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "approver()", DisplayName: "approver(users...)", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "breached()", DisplayName: "breached()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "cascadeOption()", DisplayName: "cascadeOption(parentOption, childOption)", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"OPTION"}},
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
	{Value: "projectsWhereUserHasPermission()", DisplayName: "projectsWhereUserHasPermission(permission)", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"PROJECT"}},
	{Value: "projectsWhereUserHasRole()", DisplayName: "projectsWhereUserHasRole(role)", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"PROJECT"}},
	{Value: "pending()", DisplayName: "pending()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "pendingApprovalBy()", DisplayName: "pendingApprovalBy(users...)", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "pendingBy()", DisplayName: "pendingBy(users...)", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"APPROVAL"}},
	{Value: "paused()", DisplayName: "paused()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "remaining()", DisplayName: "remaining([duration])", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "running()", DisplayName: "running()", IsList: "false", SupportsListAndSingleValueOperators: "false", Types: []string{"SLA"}},
	{Value: "spacesWhereUserHasPermission()", DisplayName: "spacesWhereUserHasPermission(permission)", IsList: "true", SupportsListAndSingleValueOperators: "true", Types: []string{"PROJECT"}},
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
	var request struct {
		IncludeCollapsedFields bool    `json:"includeCollapsedFields"`
		ProjectIDs             []int64 `json:"projectIds"`
	}
	if r.Method == http.MethodPost {
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
	// Project IDs narrow the custom fields to those a context applies in; system
	// fields always appear and invalid project IDs are ignored.
	if len(request.ProjectIDs) > 0 {
		projects, projectErr := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
		if projectErr != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load JQL fields.")
			return
		}
		known := map[string]bool{}
		for _, project := range projects {
			known[project.ID] = true
		}
		selected := map[string]bool{}
		for _, id := range request.ProjectIDs {
			if key := strconv.FormatInt(id, 10); known[key] {
				selected[key] = true
			}
		}
		if len(selected) > 0 {
			applicable := customFields[:0]
			for _, field := range customFields {
				contexts, contextErr := h.Store.CustomFieldContexts(r.Context(), workspaceID, field.ID, nil)
				if contextErr != nil {
					jiraError(w, http.StatusInternalServerError, "Could not load JQL fields.")
					return
				}
				if customFieldAppliesToProjects(contexts, selected) {
					applicable = append(applicable, field)
				}
			}
			customFields = applicable
		}
	}
	nameUses := map[string]int{}
	collapsed := map[string][]*models.CustomField{}
	for _, field := range customFields {
		nameUses[strings.ToLower(field.Name)]++
		alias := strings.ToLower(models.CollapsedFieldName(field.Name, field.Type))
		collapsed[alias] = append(collapsed[alias], field)
	}
	for _, field := range customFields {
		reference := jqlCustomFieldReference(field)
		if nameUses[strings.ToLower(field.Name)] == 1 {
			reference.Value = field.Name
		}
		fields = append(fields, reference)
	}
	if request.IncludeCollapsedFields {
		for _, members := range collapsed {
			if len(members) < 2 {
				continue
			}
			reference := jqlCustomFieldReference(members[0])
			name := models.CollapsedFieldName(members[0].Name, members[0].Type)
			reference.Value, reference.DisplayName, reference.CFID, reference.Orderable = jqlQuoteAlways(name), members[0].Name+" - "+name, "", "false"
			fields = append(fields, reference)
		}
	}
	// Issue property values apps index are searchable by their JQL name and by
	// the alias an app gives them.
	indexes, err := h.Store.EntityPropertyIndexes(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load JQL entity property fields.")
		return
	}
	resolver := jql.WithEntityProperties(jql.WithCustomFields(jql.DefaultResolver(), customFields), indexes)
	for _, index := range indexes {
		if index.EntityType != "issue" {
			continue
		}
		name := jql.EntityPropertyFieldName(index.PropertyKey, index.ObjectName)
		reference := jqlEntityPropertyReference(name, index)
		fields = append(fields, reference)
		if alias := strings.ToLower(index.Alias); alias != "" {
			if field, ok := resolver.EntityProperties[alias]; ok && field.PropertyKey == index.PropertyKey && strings.Join(field.Path, ".") == index.ObjectName {
				aliasReference := jqlEntityPropertyReference(index.Alias, index)
				aliasReference.DisplayName = index.Alias + " - " + name
				fields = append(fields, aliasReference)
			}
		}
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

// jqlCustomFieldReference describes a custom field the way Jira's JQL
// reference data does: its cf[N] id, the operators and value type of its field
// type, and whether values are suggested.
func jqlCustomFieldReference(field *models.CustomField) jqlFieldReference {
	cfid := "cf[" + strings.TrimPrefix(field.ID, "customfield_") + "]"
	reference := jqlFieldReference{Value: cfid, CFID: cfid, DisplayName: field.Name + " - " + cfid, Auto: "false", Orderable: "true", Searchable: "true"}
	listOperators := []string{"=", "!=", "in", "not in", "is", "is not"}
	switch field.Type {
	case models.CustomFieldSelect, models.CustomFieldMultiSelect, models.CustomFieldCascadingSelect:
		reference.Types, reference.Operators, reference.Auto = []string{"OPTION"}, listOperators, "true"
	case models.CustomFieldUser, models.CustomFieldMultiUser:
		reference.Types, reference.Operators, reference.Auto = []string{"USER"}, listOperators, "true"
	case models.CustomFieldGroup, models.CustomFieldMultiGroup:
		reference.Types, reference.Operators, reference.Auto = []string{"GROUP"}, listOperators, "true"
	case models.CustomFieldLabels:
		reference.Types, reference.Operators, reference.Auto = []string{"LABEL"}, listOperators, "true"
	case models.CustomFieldProject:
		reference.Types, reference.Operators, reference.Auto = []string{"PROJECT"}, listOperators, "true"
	case models.CustomFieldTeam:
		reference.Types, reference.Operators, reference.Auto = []string{"com.atlassian.teams.api.team.Team"}, listOperators, "false"
	case models.CustomFieldVersion, models.CustomFieldMultiVersion:
		reference.Types, reference.Operators, reference.Auto = []string{"VERSION"}, append(append([]string{}, listOperators...), ">", ">=", "<", "<="), "true"
	case models.CustomFieldNumber:
		reference.Types, reference.Operators = []string{"NUMBER"}, []string{"=", "!=", ">", ">=", "<", "<=", "in", "not in", "is", "is not"}
	case models.CustomFieldDate, models.CustomFieldDatetime:
		reference.Types, reference.Operators = []string{"DATE"}, []string{"=", "!=", ">", ">=", "<", "<=", "is", "is not"}
	default:
		reference.Types, reference.Operators, reference.Orderable = []string{"TEXT"}, []string{"~", "!~", "is", "is not"}, "false"
	}
	// A searcher narrows the operators to those it supports.
	if operators := models.SearcherOperators(field.SearcherKey); operators != nil {
		spelled := map[string]string{"notin": "not in", "empty": "is", "notempty": "is not"}
		reference.Operators = make([]string, 0, len(operators))
		for _, operator := range operators {
			if name, ok := spelled[operator]; ok {
				operator = name
			}
			reference.Operators = append(reference.Operators, operator)
		}
	}
	return reference
}

// customFieldAppliesToProjects reports whether a global context, or one naming
// a selected project, makes the field searchable there.
func customFieldAppliesToProjects(contexts []*models.CustomFieldContext, projects map[string]bool) bool {
	for _, context := range contexts {
		if context.AllProjects {
			return true
		}
		for _, projectID := range context.ProjectIDs {
			if projects[projectID] {
				return true
			}
		}
	}
	return false
}

// jqlQuoteAlways writes a value as a double-quoted JQL string.
func jqlQuoteAlways(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// jqlSuggestionCustomField finds the custom field a suggestion request names by
// cf[N], customfield_N, its name or its collapsed name.
func (h *Handler) jqlSuggestionCustomField(r *http.Request, workspaceID, name string) (*models.CustomField, error) {
	fields, err := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	name = strings.Trim(name, `"`)
	if strings.HasPrefix(name, "cf[") && strings.HasSuffix(name, "]") {
		name = "customfield_" + name[3:len(name)-1]
	}
	for _, field := range fields {
		if strings.EqualFold(field.ID, name) || strings.EqualFold(field.Name, name) || strings.EqualFold(models.CollapsedFieldName(field.Name, field.Type), name) {
			return field, nil
		}
	}
	return nil, nil
}

// customFieldOptionValues lists the distinct option values every context of a
// select field offers.
func (h *Handler) customFieldOptionValues(r *http.Request, workspaceID, fieldID string) ([]string, error) {
	contexts, err := h.Store.CustomFieldContexts(r.Context(), workspaceID, fieldID, nil)
	if err != nil {
		return nil, err
	}
	seen, values := map[string]bool{}, []string{}
	for _, context := range contexts {
		options, optionErr := h.Store.CustomFieldOptions(r.Context(), workspaceID, fieldID, context.ID)
		if optionErr != nil {
			return nil, optionErr
		}
		for _, option := range options {
			if option.ParentID == "" && !seen[strings.ToLower(option.Value)] {
				seen[strings.ToLower(option.Value)] = true
				values = append(values, option.Value)
			}
		}
	}
	return values, nil
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
	// CHANGED predicates: BY names people, FROM and TO name the field's values.
	switch predicate := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("predicateName"))); predicate {
	case "":
	case "by", "from", "to":
		needle = strings.TrimSpace(r.URL.Query().Get("predicateValue"))
		if predicate == "by" {
			field = "assignee"
		}
	default:
		jiraError(w, http.StatusBadRequest, "The predicate must be by, from or to.")
		return
	}
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
		customField, err := h.jqlSuggestionCustomField(r, workspaceID, field)
		if err != nil {
			jiraError(w, 500, "Could not load JQL suggestions.")
			return
		}
		if customField == nil {
			jiraError(w, http.StatusBadRequest, "The field does not provide autocomplete suggestions.")
			return
		}
		switch customField.Type {
		case models.CustomFieldSelect, models.CustomFieldMultiSelect, models.CustomFieldCascadingSelect:
			options, optionErr := h.customFieldOptionValues(r, workspaceID, customField.ID)
			if optionErr != nil {
				jiraError(w, 500, "Could not load JQL suggestions.")
				return
			}
			for _, option := range options {
				add(option, option)
			}
		case models.CustomFieldUser, models.CustomFieldMultiUser:
			if h.browsesUsers(r, workspaceID, userID) {
				members, memberErr := h.Store.MembersByWorkspace(r.Context(), workspaceID)
				if memberErr != nil {
					jiraError(w, 500, "Could not load JQL suggestions.")
					return
				}
				for _, member := range members {
					add(member.ID, member.DisplayName)
				}
			}
		case models.CustomFieldGroup, models.CustomFieldMultiGroup:
			groups, groupErr := h.Store.GroupsByWorkspace(r.Context(), workspaceID)
			if groupErr != nil {
				jiraError(w, 500, "Could not load JQL suggestions.")
				return
			}
			for _, group := range groups {
				add(group.Name, group.Name)
			}
		case models.CustomFieldProject:
			projects, projectErr := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
			if projectErr != nil {
				jiraError(w, 500, "Could not load JQL suggestions.")
				return
			}
			for _, project := range projects {
				if allowed, browseErr := h.canBrowseProject(r, workspaceID, userID, project.ID); browseErr == nil && allowed {
					add(project.Key, project.Name+" ("+project.Key+")")
				}
			}
		case models.CustomFieldVersion, models.CustomFieldMultiVersion, models.CustomFieldLabels:
			source := "fixversion"
			if customField.Type == models.CustomFieldLabels {
				source = "labels"
			}
			stored, storeErr := h.Store.JQLFieldSuggestions(r.Context(), workspaceID, userID, source, needle, 50)
			if storeErr != nil {
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
			_, warnings, validationErrors, compileErr := h.compileJQLValidated(r.Context(), workspaceID, raw, userID, validation)
			switch {
			case compileErr != nil:
				result["errors"] = []string{compileErr.message}
				results = append(results, result)
				continue
			case len(validationErrors) > 0:
				result["errors"] = validationErrors
				results = append(results, result)
				continue
			case len(warnings) > 0:
				result["warnings"] = warnings
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
		switch op {
		case "notin":
			op = "not in"
		case "empty":
			op = "is"
			operand = jqlOperand("empty")
		case "notempty":
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

// jqlEntityPropertyReference describes an indexed issue property value for
// JQL autocomplete, with the operators its extraction type supports.
func jqlEntityPropertyReference(value string, index models.AppEntityPropertyIndex) jqlFieldReference {
	reference := jqlFieldReference{Value: value, DisplayName: value, Auto: "false", Orderable: "true", Searchable: "true"}
	switch index.Type {
	case "number":
		reference.Types, reference.Operators = []string{"NUMBER"}, []string{"=", "!=", ">", ">=", "<", "<=", "in", "not in", "is", "is not"}
	case "date":
		reference.Types, reference.Operators = []string{"DATE"}, []string{"=", "!=", ">", ">=", "<", "<=", "in", "not in", "is", "is not"}
	case "user":
		reference.Types, reference.Operators = []string{"USER"}, []string{"=", "!=", "in", "not in", "is", "is not"}
	case "text":
		reference.Types, reference.Operators = []string{"TEXT"}, []string{"~", "!~", "is", "is not"}
	default:
		reference.Types, reference.Operators = []string{"STRING"}, []string{"=", "!=", "in", "not in", "is", "is not"}
	}
	return reference
}
