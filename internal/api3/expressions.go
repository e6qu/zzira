package api3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/jexpr"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

// Jira expressions: POST /rest/api/3/expression/analyse, /eval and the
// enhanced-search /evaluate.

const (
	expressionDefaultIssues = 1000
	expressionMaximumIssues = 1000
)

func (h *Handler) analyseExpressions(w http.ResponseWriter, r *http.Request) {
	check := r.URL.Query().Get("check")
	if check == "" {
		check = "syntax"
	}
	if check != "syntax" && check != "type" && check != "complexity" {
		jiraError(w, http.StatusBadRequest, "The check must be syntax, type or complexity.")
		return
	}
	var request struct {
		Expressions      []string          `json:"expressions"`
		ContextVariables map[string]string `json:"contextVariables"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	if len(request.Expressions) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"expressions": "At least one expression is required."})
		return
	}
	results := make([]map[string]any, 0, len(request.Expressions))
	for _, expression := range request.Expressions {
		analysis := jexpr.Analyse(expression, check, request.ContextVariables)
		result := map[string]any{"expression": expression, "valid": analysis.Valid}
		if len(analysis.Errors) > 0 {
			problems := make([]map[string]any, 0, len(analysis.Errors))
			for _, problem := range analysis.Errors {
				bean := map[string]any{"message": problem.Message, "type": problem.Type}
				if problem.Line > 0 {
					bean["line"], bean["column"] = problem.Line, problem.Column
				}
				if problem.Expression != "" {
					bean["expression"] = problem.Expression
				}
				problems = append(problems, bean)
			}
			result["errors"] = problems
		}
		if analysis.Type != "" {
			result["type"] = analysis.Type
		}
		if analysis.Complexity != nil {
			complexity := map[string]any{"expensiveOperations": analysis.Complexity.ExpensiveOperations}
			if len(analysis.Complexity.Variables) > 0 {
				complexity["variables"] = analysis.Complexity.Variables
			}
			result["complexity"] = complexity
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

type expressionIDOrKey struct {
	ID  *int64 `json:"id"`
	Key string `json:"key"`
}

func (ref *expressionIDOrKey) value() (string, bool) {
	if ref.ID != nil && ref.Key != "" {
		return "", false
	}
	if ref.ID != nil {
		return strconv.FormatInt(*ref.ID, 10), true
	}
	return ref.Key, ref.Key != ""
}

type expressionContext struct {
	Issue           *expressionIDOrKey `json:"issue"`
	Project         *expressionIDOrKey `json:"project"`
	Sprint          *int64             `json:"sprint"`
	Board           *int64             `json:"board"`
	ServiceDesk     *int64             `json:"serviceDesk"`
	CustomerRequest *int64             `json:"customerRequest"`
	Issues          *struct {
		JQL *struct {
			Query         string `json:"query"`
			StartAt       *int64 `json:"startAt"`
			MaxResults    *int   `json:"maxResults"`
			NextPageToken string `json:"nextPageToken"`
			Validation    string `json:"validation"`
		} `json:"jql"`
	} `json:"issues"`
	Custom []json.RawMessage `json:"custom"`
}

// expressionIdentity resolves the site and the caller; expressions can be
// evaluated anonymously, with user set to null.
func (h *Handler) expressionIdentity(r *http.Request) (string, string, *jerr) {
	if r.Header.Get("Authorization") != "" {
		return h.authWorkspace(r)
	}
	if _, err := authn.Identify(r.Context(), h.Store, r); err == nil {
		return h.authWorkspace(r)
	}
	workspaceID, err := h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		return "", "", &jerr{http.StatusInternalServerError, "no workspace configured", nil}
	}
	return workspaceID, "", nil
}

func (h *Handler) evaluateExpression(w http.ResponseWriter, r *http.Request, enhanced bool) {
	workspaceID, userID, authErr := h.expressionIdentity(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Expression string          `json:"expression"`
		Context    json.RawMessage `json:"context"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	if strings.TrimSpace(request.Expression) == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"expression": "The expression must not be empty."})
		return
	}
	var context expressionContext
	if len(request.Context) > 0 && string(request.Context) != "null" {
		contextDecoder := json.NewDecoder(strings.NewReader(string(request.Context)))
		contextDecoder.DisallowUnknownFields()
		if err := contextDecoder.Decode(&context); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid expression context: "+err.Error())
			return
		}
	}
	loader := &expressionLoader{h: h, ctx: r.Context(), workspaceID: workspaceID, userID: userID}
	variables := map[string]jexpr.Value{"user": nil, "app": nil}
	if userID != "" {
		if user, err := h.Store.UserByID(r.Context(), userID); err == nil {
			variables["user"] = loader.userRecord(user)
		}
	}
	notFound := func(message string) {
		jiraError(w, http.StatusNotFound, message)
	}
	if context.Issue != nil {
		ref, ok := context.Issue.value()
		if !ok {
			jiraError(w, http.StatusBadRequest, "Specify either the issue id or the issue key.")
			return
		}
		issue, err := loader.Issue(r.Context(), ref)
		if err != nil {
			notFound(err.Error())
			return
		}
		variables["issue"] = issue
	}
	if context.Project != nil {
		ref, ok := context.Project.value()
		if !ok {
			jiraError(w, http.StatusBadRequest, "Specify either the project id or the project key.")
			return
		}
		project, err := loader.Project(r.Context(), ref)
		if err != nil {
			notFound(err.Error())
			return
		}
		variables["project"] = project
	}
	if context.Sprint != nil {
		sprint, err := h.Store.SprintByIDInWorkspace(r.Context(), workspaceID, strconv.FormatInt(*context.Sprint, 10))
		if err != nil || userID == "" {
			notFound("The sprint does not exist or you do not have permission to see it.")
			return
		}
		variables["sprint"] = loader.sprintRecord(sprint)
	}
	if context.Board != nil {
		board, err := h.Store.BoardByIDInWorkspace(r.Context(), workspaceID, strconv.FormatInt(*context.Board, 10))
		if err != nil || userID == "" {
			notFound("The board does not exist or you do not have permission to see it.")
			return
		}
		variables["board"] = loader.boardRecord(board)
	}
	if context.ServiceDesk != nil {
		serviceDesk, err := h.Store.ServiceDesk(r.Context(), workspaceID, strconv.FormatInt(*context.ServiceDesk, 10))
		if err != nil || userID == "" {
			notFound("The service desk does not exist or you do not have permission to see it.")
			return
		}
		variables["serviceDesk"] = loader.serviceDeskRecord(serviceDesk)
	}
	if context.CustomerRequest != nil {
		issue, err := loader.visibleIssue(strconv.FormatInt(*context.CustomerRequest, 10))
		if err != nil {
			notFound("The customer request does not exist or you do not have permission to see it.")
			return
		}
		variables["customerRequest"] = loader.customerRequestRecord(issue)
	}
	for _, raw := range context.Custom {
		name, value, err := loader.customVariable(raw)
		if errors.Is(err, errExpressionNotFound) {
			notFound(strings.TrimPrefix(err.Error(), errExpressionNotFound.Error()+": "))
			return
		}
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		variables[name] = value
	}
	meta := map[string]any{}
	if context.Issues != nil && context.Issues.JQL != nil {
		jqlContext := context.Issues.JQL
		maxResults := expressionDefaultIssues
		if jqlContext.MaxResults != nil {
			if *jqlContext.MaxResults < 1 {
				jiraError(w, http.StatusBadRequest, "maxResults must be a positive number.")
				return
			}
			maxResults = min(*jqlContext.MaxResults, expressionMaximumIssues)
		}
		offset := 0
		if enhanced {
			if jqlContext.StartAt != nil || jqlContext.Validation != "" {
				jiraError(w, http.StatusBadRequest, "The evaluate resource pages issues with nextPageToken.")
				return
			}
			if jqlContext.NextPageToken != "" {
				decoded, err := base64.RawURLEncoding.DecodeString(jqlContext.NextPageToken)
				value, found := strings.CutPrefix(string(decoded), "offset:")
				parsed, parseErr := strconv.Atoi(value)
				if err != nil || !found || parseErr != nil || parsed < 0 {
					jiraError(w, http.StatusBadRequest, "Invalid nextPageToken.")
					return
				}
				offset = parsed
			}
		} else if jqlContext.StartAt != nil {
			if *jqlContext.StartAt < 0 {
				jiraError(w, http.StatusBadRequest, "startAt must not be negative.")
				return
			}
			offset = int(*jqlContext.StartAt)
		}
		validation := jqlContext.Validation
		if validation == "" {
			validation = "strict"
		}
		if validation != "strict" && validation != "warn" && validation != "none" {
			jiraError(w, http.StatusBadRequest, "validation must be strict, warn or none.")
			return
		}
		issues := &jexpr.List{Items: []jexpr.Value{}}
		total := 0
		warnings := []string{}
		compiled, compileErr := h.compileJQL(r.Context(), workspaceID, jqlContext.Query, userID)
		switch {
		case compileErr != nil && validation == "strict":
			jiraError(w, http.StatusBadRequest, compileErr.message)
			return
		case compileErr != nil:
			warnings = append(warnings, compileErr.message)
		case userID != "":
			found, count, err := h.Store.Search(r.Context(), workspaceID, userID, compiled, maxResults, offset)
			if err != nil {
				jiraError(w, http.StatusBadRequest, jql.QueryMessage(err.Error()))
				return
			}
			for _, issue := range found {
				issues.Items = append(issues.Items, loader.issueRecord(issue))
			}
			total = count
		}
		variables["issues"] = issues
		if enhanced {
			jqlMeta := map[string]any{"isLast": offset+len(issues.Items) >= total}
			if offset+len(issues.Items) < total {
				jqlMeta["nextPageToken"] = base64.RawURLEncoding.EncodeToString([]byte("offset:" + strconv.Itoa(offset+len(issues.Items))))
			}
			meta["issues"] = map[string]any{"jql": jqlMeta}
		} else {
			jqlMeta := map[string]any{"startAt": offset, "maxResults": maxResults, "count": len(issues.Items), "totalCount": total}
			if validation == "warn" && len(warnings) > 0 {
				jqlMeta["validationWarnings"] = warnings
			}
			meta["issues"] = map[string]any{"jql": jqlMeta}
		}
	}
	evaluation := &jexpr.Context{Ctx: r.Context(), Variables: variables, Loader: loader, Limits: jexpr.DefaultLimits}
	value, err := evaluation.Evaluate(request.Expression)
	if err == nil {
		var encoded any
		if encoded, err = evaluation.ToJSON(value); err == nil {
			response := map[string]any{"value": encoded}
			if strings.Contains(r.URL.Query().Get("expand"), "meta.complexity") {
				used := evaluation.Used()
				limits := jexpr.DefaultLimits
				meta["complexity"] = map[string]any{
					"steps":               map[string]int{"value": used.Steps, "limit": limits.Steps},
					"expensiveOperations": map[string]int{"value": used.ExpensiveOperations, "limit": limits.ExpensiveOperations},
					"beans":               map[string]int{"value": used.Beans, "limit": limits.Beans},
					"primitiveValues":     map[string]int{"value": used.PrimitiveValues, "limit": limits.PrimitiveValues},
				}
			}
			if len(meta) > 0 {
				response["meta"] = meta
			}
			writeJSON(w, http.StatusOK, response)
			return
		}
	}
	var syntaxErr *jexpr.SyntaxError
	if errors.As(err, &syntaxErr) {
		jiraError(w, http.StatusBadRequest, fmt.Sprintf("Evaluation failed: Jira expression syntax error at line %d, column %d: %s", syntaxErr.Pos.Line, syntaxErr.Pos.Column, syntaxErr.Message))
		return
	}
	jiraError(w, http.StatusBadRequest, err.Error())
}

// ---- entities ----

var errExpressionNotFound = errors.New("not found")

type expressionLoader struct {
	h           *Handler
	ctx         context.Context
	workspaceID string
	userID      string
}

func (l *expressionLoader) visibleIssue(ref string) (*models.Issue, error) {
	message := fmt.Errorf("Issue %s does not exist or you do not have permission to see it.", ref)
	if l.userID == "" {
		return nil, message
	}
	issue, err := l.h.Store.IssueByIDOrKey(l.ctx, l.workspaceID, ref)
	if err != nil {
		return nil, message
	}
	visible, err := authz.CanSeeIssue(l.ctx, l.h.Store, l.workspaceID, issue.ProjectID, l.userID, issue.ID, issue.SecurityLevelID)
	if err != nil || !visible {
		return nil, message
	}
	return issue, nil
}

func (l *expressionLoader) Issue(_ context.Context, ref string) (jexpr.Value, error) {
	issue, err := l.visibleIssue(ref)
	if err != nil {
		return nil, err
	}
	return l.issueRecord(issue), nil
}

func (l *expressionLoader) User(_ context.Context, accountID string) (jexpr.Value, error) {
	user, err := l.h.Store.UserByID(l.ctx, accountID)
	if err != nil || l.userID == "" {
		return nil, fmt.Errorf("User %s does not exist.", accountID)
	}
	return l.userRecord(user), nil
}

func (l *expressionLoader) Project(_ context.Context, ref string) (jexpr.Value, error) {
	project, err := l.h.Store.ProjectByIDOrKey(l.ctx, l.workspaceID, ref)
	if err != nil || l.userID == "" {
		return nil, fmt.Errorf("Project %s does not exist or you do not have permission to see it.", ref)
	}
	return l.projectRecord(project), nil
}

func (l *expressionLoader) customVariable(raw json.RawMessage) (string, jexpr.Value, error) {
	var variable struct {
		Type      string          `json:"type"`
		Key       string          `json:"key"`
		ID        *int64          `json:"id"`
		AccountID string          `json:"accountId"`
		Value     json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &variable); err != nil {
		return "", nil, fmt.Errorf("Invalid custom context variable.")
	}
	if variable.Key == "" {
		return "", nil, fmt.Errorf("Custom context variables need a key.")
	}
	value, err := l.customValue(variable.Type, variable.ID, variable.AccountID, variable.Value)
	return variable.Key, value, err
}

func (l *expressionLoader) customValue(kind string, id *int64, accountID string, raw json.RawMessage) (jexpr.Value, error) {
	switch kind {
	case "user":
		user, err := l.User(l.ctx, accountID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errExpressionNotFound, err)
		}
		return user, nil
	case "issue":
		if id == nil {
			return nil, fmt.Errorf("An issue custom context variable needs the issue id.")
		}
		issue, err := l.Issue(l.ctx, strconv.FormatInt(*id, 10))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errExpressionNotFound, err)
		}
		return issue, nil
	case "json":
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("A json custom context variable needs a JSON value.")
		}
		return jexpr.FromJSON(decoded), nil
	case "list":
		var items []struct {
			Type      string          `json:"type"`
			ID        *int64          `json:"id"`
			AccountID string          `json:"accountId"`
			Value     json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("A list custom context variable needs a list of variables.")
		}
		list := &jexpr.List{Items: []jexpr.Value{}}
		for _, item := range items {
			if item.Type == "list" {
				return nil, fmt.Errorf("A list custom context variable cannot contain lists.")
			}
			value, err := l.customValue(item.Type, item.ID, item.AccountID, item.Value)
			if err != nil {
				return nil, err
			}
			list.Items = append(list.Items, value)
		}
		return list, nil
	}
	return nil, fmt.Errorf("Unsupported custom context variable type %q.", kind)
}

// beanRecord makes an expression record from a REST bean, converting numeric
// ids into numbers.
func beanRecord(typeName string, bean map[string]any, names map[string]string) *jexpr.Record {
	fields := map[string]jexpr.Value{}
	for member, beanKey := range names {
		value, present := bean[beanKey]
		if !present {
			fields[member] = nil
			continue
		}
		switch typed := value.(type) {
		case string:
			if member == "id" {
				if number, err := strconv.ParseFloat(typed, 64); err == nil {
					fields[member] = number
					continue
				}
			}
			fields[member] = typed
		case int:
			fields[member] = float64(typed)
		case int64:
			fields[member] = float64(typed)
		case float64, bool, nil:
			fields[member] = typed
		default:
			fields[member] = jexpr.FromJSON(typed)
		}
	}
	return &jexpr.Record{Type: typeName, Fields: fields, Bean: func(*jexpr.Context) (any, error) { return bean, nil }}
}

// adfPlainText flattens a document into its text, one line per block.
func adfPlainText(raw json.RawMessage) string {
	var document any
	if len(raw) == 0 || json.Unmarshal(raw, &document) != nil {
		return ""
	}
	if text, ok := document.(string); ok {
		return text
	}
	var b strings.Builder
	var walk func(node any)
	walk = func(node any) {
		object, ok := node.(map[string]any)
		if !ok {
			return
		}
		if text, ok := object["text"].(string); ok {
			b.WriteString(text)
		}
		if object["type"] == "hardBreak" {
			b.WriteString("\n")
		}
		children, _ := object["content"].([]any)
		for _, child := range children {
			walk(child)
		}
		switch object["type"] {
		case "paragraph", "heading", "codeBlock", "blockquote", "listItem":
			b.WriteString("\n")
		}
	}
	walk(document)
	return strings.TrimRight(b.String(), "\n")
}

func richText(raw json.RawMessage) jexpr.Value {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var document any
	_ = json.Unmarshal(raw, &document)
	return &jexpr.Record{
		Type:   "RichText",
		Fields: map[string]jexpr.Value{"plainText": adfPlainText(raw)},
		Bean:   func(*jexpr.Context) (any, error) { return document, nil },
	}
}

func (l *expressionLoader) userRecord(user *models.User) jexpr.Value {
	if user == nil {
		return nil
	}
	return &jexpr.Record{
		Type:   "User",
		Fields: map[string]jexpr.Value{"accountId": user.ID, "displayName": user.DisplayName, "timeZone": user.TimeZone},
		Bean:   func(*jexpr.Context) (any, error) { return l.h.userBeanFor(l.ctx, user), nil },
	}
}

func (l *expressionLoader) userByID(accountID, displayName string) jexpr.Value {
	if accountID == "" {
		return nil
	}
	if user, err := l.h.Store.UserByID(l.ctx, accountID); err == nil {
		return l.userRecord(user)
	}
	return l.userRecord(&models.User{ID: accountID, DisplayName: displayName})
}

func (l *expressionLoader) projectRecord(project *models.Project) jexpr.Value {
	if project == nil {
		return nil
	}
	id, _ := strconv.ParseFloat(project.ID, 64)
	return &jexpr.Record{
		Type:   "Project",
		Fields: map[string]jexpr.Value{"id": id, "key": project.Key, "name": project.Name, "projectTypeKey": project.ProjectTypeKey},
		Deferred: map[string]func(*jexpr.Context) (jexpr.Value, error){
			"lead": func(*jexpr.Context) (jexpr.Value, error) { return l.userByID(project.LeadAccountID, ""), nil },
		},
		Bean: func(*jexpr.Context) (any, error) { return l.h.projectBean(project), nil },
	}
}

func (l *expressionLoader) sprintRecord(sprint *models.Sprint) jexpr.Value {
	bean := map[string]any{
		"id": sprint.JiraID, "name": sprint.Name, "state": sprint.State, "goal": sprint.Goal, "originBoardId": sprint.BoardJiraID,
		"self": l.h.BaseURL + "/rest/agile/1.0/sprint/" + strconv.FormatInt(sprint.JiraID, 10),
	}
	if sprint.StartDate != "" {
		bean["startDate"] = sprint.StartDate
	}
	if sprint.EndDate != "" {
		bean["endDate"] = sprint.EndDate
	}
	return &jexpr.Record{
		Type: "Sprint",
		Fields: map[string]jexpr.Value{
			"id": float64(sprint.JiraID), "name": sprint.Name, "state": sprint.State, "goal": sprint.Goal,
			"startDate": jexpr.ParseDate(sprint.StartDate), "endDate": jexpr.ParseDate(sprint.EndDate), "originBoardId": float64(sprint.BoardJiraID),
		},
		Bean: func(*jexpr.Context) (any, error) { return bean, nil },
	}
}

func (l *expressionLoader) boardRecord(board *models.Board) jexpr.Value {
	bean := map[string]any{"id": board.JiraID, "name": board.Name, "type": board.Type, "self": l.h.BaseURL + "/rest/agile/1.0/board/" + strconv.FormatInt(board.JiraID, 10)}
	return &jexpr.Record{
		Type: "Board",
		Fields: map[string]jexpr.Value{
			"id": float64(board.JiraID), "name": board.Name, "type": board.Type, "hasBacklog": true, "hasSprints": board.Type == "scrum",
		},
		Bean: func(*jexpr.Context) (any, error) { return bean, nil },
	}
}

func (l *expressionLoader) serviceDeskRecord(serviceDesk *models.ServiceDesk) jexpr.Value {
	id, _ := strconv.ParseFloat(serviceDesk.ID, 64)
	return &jexpr.Record{
		Type:   "ServiceDesk",
		Fields: map[string]jexpr.Value{"id": id},
		Lazy: map[string]func(*jexpr.Context) (jexpr.Value, error){
			"project": func(*jexpr.Context) (jexpr.Value, error) {
				project, err := l.h.Store.ProjectByIDOrKey(l.ctx, l.workspaceID, serviceDesk.ProjectID)
				if err != nil {
					return nil, nil
				}
				return l.projectRecord(project), nil
			},
		},
		Bean: func(*jexpr.Context) (any, error) {
			return map[string]any{"id": serviceDesk.ID, "projectId": serviceDesk.ProjectID, "projectKey": serviceDesk.ProjectKey, "projectName": serviceDesk.ProjectName}, nil
		},
	}
}

func (l *expressionLoader) customerRequestRecord(issue *models.Issue) jexpr.Value {
	return &jexpr.Record{
		Type: "CustomerRequest",
		Fields: map[string]jexpr.Value{
			"id": float64(issue.JiraID), "key": issue.Key, "issue": l.issueRecord(issue), "reporter": l.userRecord(issue.Reporter),
		},
		Bean: func(*jexpr.Context) (any, error) {
			return map[string]any{"issueId": strconv.FormatInt(issue.JiraID, 10), "issueKey": issue.Key}, nil
		},
	}
}

func (l *expressionLoader) visibleIssueRecords(issues []*models.Issue, keep func(*models.Issue) bool) jexpr.Value {
	list := &jexpr.List{Items: []jexpr.Value{}}
	for _, issue := range issues {
		if keep != nil && !keep(issue) {
			continue
		}
		if visible, err := authz.CanSeeIssue(l.ctx, l.h.Store, l.workspaceID, issue.ProjectID, l.userID, issue.ID, issue.SecurityLevelID); err == nil && visible {
			list.Items = append(list.Items, l.issueRecord(issue))
		}
	}
	return list
}

func (l *expressionLoader) issueRecord(issue *models.Issue) *jexpr.Record {
	h := l.h
	status := &jexpr.Record{
		Type: "Status",
		Fields: map[string]jexpr.Value{
			"id": float64(issue.Status.JiraID), "name": issue.Status.Name, "description": "",
			"category": beanRecord("StatusCategory", statusCategoryBean(issue.Status.Category), map[string]string{"id": "id", "key": "key", "name": "name", "colorName": "colorName"}),
		},
	}
	fields := map[string]jexpr.Value{
		"id": float64(issue.JiraID), "key": issue.Key, "summary": issue.Summary, "description": richText(issue.Description),
		"issueType": beanRecord("IssueType", h.issueTypeBean(issue.IssueType), map[string]string{
			"id": "id", "name": "name", "description": "description", "iconUrl": "iconUrl", "isSubtask": "subtask", "hierarchyLevel": "hierarchyLevel",
		}),
		"status": status, "priority": nil, "resolution": nil,
		"assignee": l.userRecord(issue.Assignee), "reporter": l.userRecord(issue.Reporter), "creator": l.userRecord(issue.Reporter),
		"created": jexpr.ParseDate(issue.CreatedAt), "updated": jexpr.ParseDate(issue.UpdatedAt), "resolutionDate": jexpr.ParseDate(issue.ResolvedAt),
		"isEpic": issue.IssueType.HierarchyLevel == 1,
	}
	labels := &jexpr.List{Items: []jexpr.Value{}}
	for _, label := range issue.Labels {
		labels.Items = append(labels.Items, label)
	}
	fields["labels"] = labels
	if issue.Priority != nil {
		fields["priority"] = beanRecord("Priority", h.priorityBean(*issue.Priority), map[string]string{"id": "id", "name": "name", "description": "description", "iconUrl": "iconUrl"})
	}
	if issue.Resolution != nil {
		fields["resolution"] = beanRecord("Resolution", h.resolutionBean(*issue.Resolution), map[string]string{"id": "id", "name": "name", "description": "description"})
	}
	for fieldID, raw := range issue.Fields {
		if !strings.HasPrefix(fieldID, "customfield_") {
			continue
		}
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if decoder.Decode(&decoded) == nil {
			fields[fieldID] = jexpr.FromJSON(decoded)
		}
	}
	ctx := l.ctx
	sprints := func() ([]*models.Sprint, error) {
		byIssue, err := h.Store.SprintsForIssues(ctx, []string{issue.ID})
		return byIssue[issue.ID], err
	}
	parentIssue := func() (*models.Issue, error) {
		if issue.Parent == nil {
			return nil, nil
		}
		parent, err := l.visibleIssue(issue.Parent.ID)
		if err != nil {
			return nil, nil
		}
		return parent, nil
	}
	lazy := map[string]func(*jexpr.Context) (jexpr.Value, error){
		"comments": func(*jexpr.Context) (jexpr.Value, error) {
			comments, err := h.Store.CommentsByIssue(ctx, issue.ID)
			if err != nil {
				return nil, err
			}
			list := &jexpr.List{Items: []jexpr.Value{}}
			for _, comment := range comments {
				if visible, visErr := h.Store.CommentVisibleTo(ctx, l.workspaceID, issue.ProjectID, l.userID, comment); visErr != nil || !visible {
					continue
				}
				list.Items = append(list.Items, &jexpr.Record{Type: "Comment", Fields: map[string]jexpr.Value{
					"id": float64(comment.JiraID), "body": richText(comment.Body), "author": l.userByID(comment.AuthorID, comment.AuthorName),
					"created": jexpr.ParseDate(comment.Created), "updated": jexpr.ParseDate(comment.Updated),
				}})
			}
			return list, nil
		},
		"attachments": func(*jexpr.Context) (jexpr.Value, error) {
			attachments, err := h.Store.AttachmentsByIssue(ctx, issue.ID)
			if err != nil {
				return nil, err
			}
			list := &jexpr.List{Items: []jexpr.Value{}}
			for _, attachment := range attachments {
				bean := h.attachmentBean(attachment)
				list.Items = append(list.Items, &jexpr.Record{Type: "Attachment", Fields: map[string]jexpr.Value{
					"id": float64(attachment.JiraID), "author": l.userByID(attachment.AuthorID, attachment.AuthorName), "filename": attachment.Filename,
					"size": float64(attachment.Size), "mimeType": attachment.MimeType, "created": jexpr.ParseDate(attachment.Created),
				}, Bean: func(*jexpr.Context) (any, error) { return bean, nil }})
			}
			return list, nil
		},
		"worklogs": func(*jexpr.Context) (jexpr.Value, error) {
			worklogs, err := h.Store.WorklogsByIssue(ctx, issue.ID)
			if err != nil {
				return nil, err
			}
			list := &jexpr.List{Items: []jexpr.Value{}}
			for _, worklog := range worklogs {
				bean := h.worklogBean(worklog)
				list.Items = append(list.Items, &jexpr.Record{Type: "Worklog", Fields: map[string]jexpr.Value{
					"id": float64(worklog.JiraID), "author": l.userByID(worklog.AuthorID, worklog.AuthorName), "timeSpentSeconds": float64(worklog.TimeSpentSeconds),
					"comment": richText(worklog.Comment), "created": jexpr.ParseDate(worklog.Created), "started": jexpr.ParseDate(worklog.Created),
				}, Bean: func(*jexpr.Context) (any, error) { return bean, nil }})
			}
			return list, nil
		},
		"changelogs": func(*jexpr.Context) (jexpr.Value, error) {
			entries, err := h.Store.IssueChangelog(ctx, l.workspaceID, issue.ID)
			if err != nil {
				return nil, err
			}
			list := &jexpr.List{Items: []jexpr.Value{}}
			for _, entry := range entries {
				items := &jexpr.List{Items: []jexpr.Value{}}
				for _, item := range entry.Items {
					items.Items = append(items.Items, &jexpr.Record{Type: "ChangelogItem", Fields: map[string]jexpr.Value{
						"field": item.Field, "fieldId": item.Field, "fieldtype": item.FieldType,
						"from": item.From, "fromString": item.FromString, "to": item.To, "toString": item.ToString,
					}})
				}
				list.Items = append(list.Items, &jexpr.Record{Type: "Changelog", Fields: map[string]jexpr.Value{
					"id": strconv.FormatInt(entry.Seq, 10), "author": l.userRecord(entry.Author), "created": jexpr.ParseDate(entry.Created), "items": items,
				}})
			}
			return list, nil
		},
		"links": func(*jexpr.Context) (jexpr.Value, error) {
			links, err := h.Store.LinksByIssue(ctx, issue.ID)
			if err != nil {
				return nil, err
			}
			list := &jexpr.List{Items: []jexpr.Value{}}
			for _, link := range links {
				direction, linkedID := "inward", link.InwardID
				if link.InwardID == issue.ID {
					direction, linkedID = "outward", link.OutwardID
				}
				linked, linkErr := l.visibleIssue(linkedID)
				if linkErr != nil {
					continue
				}
				list.Items = append(list.Items, &jexpr.Record{Type: "IssueLink", Fields: map[string]jexpr.Value{
					"id": float64(link.JiraID), "direction": direction, "linkedIssue": l.issueRecord(linked),
					"type": &jexpr.Record{Type: "IssueLinkType", Fields: map[string]jexpr.Value{
						"id": float64(link.TypeJiraID), "name": link.TypeName, "inward": link.Inward, "outward": link.Outward,
					}},
				}})
			}
			return list, nil
		},
		"subtasks": func(*jexpr.Context) (jexpr.Value, error) {
			children, err := h.Store.ChildIssues(ctx, l.workspaceID, issue.ID)
			if err != nil {
				return nil, err
			}
			return l.visibleIssueRecords(children, func(child *models.Issue) bool { return child.IssueType.Subtask }), nil
		},
		"stories": func(*jexpr.Context) (jexpr.Value, error) {
			if issue.IssueType.HierarchyLevel != 1 {
				return &jexpr.List{Items: []jexpr.Value{}}, nil
			}
			children, err := h.Store.ChildIssues(ctx, l.workspaceID, issue.ID)
			if err != nil {
				return nil, err
			}
			return l.visibleIssueRecords(children, func(child *models.Issue) bool { return child.IssueType.HierarchyLevel == 0 }), nil
		},
		"parent": func(*jexpr.Context) (jexpr.Value, error) {
			parent, err := parentIssue()
			if parent == nil || err != nil {
				return nil, err
			}
			return l.issueRecord(parent), nil
		},
		"epic": func(*jexpr.Context) (jexpr.Value, error) {
			parent, err := parentIssue()
			if parent == nil || err != nil || parent.IssueType.HierarchyLevel != 1 {
				return nil, err
			}
			return l.issueRecord(parent), nil
		},
		"properties": func(*jexpr.Context) (jexpr.Value, error) {
			properties, err := h.Store.IssueProperties(ctx, issue.ID)
			if err != nil {
				return nil, err
			}
			decoded := map[string]any{}
			for key, raw := range properties {
				var value any
				decoder := json.NewDecoder(strings.NewReader(string(raw)))
				decoder.UseNumber()
				if decoder.Decode(&value) == nil {
					decoded[key] = value
				}
			}
			return &jexpr.Record{
				Type: "EntityProperties",
				Methods: map[string]func(*jexpr.Context, []jexpr.Value) (jexpr.Value, error){
					"get": func(_ *jexpr.Context, args []jexpr.Value) (jexpr.Value, error) {
						if len(args) == 0 {
							return nil, fmt.Errorf("get needs a property key")
						}
						key, _ := args[0].(string)
						value, ok := decoded[key]
						if !ok {
							return nil, nil
						}
						return jexpr.FromJSON(value), nil
					},
					"keys": func(*jexpr.Context, []jexpr.Value) (jexpr.Value, error) {
						list := &jexpr.List{Items: []jexpr.Value{}}
						for key := range decoded {
							list.Items = append(list.Items, key)
						}
						return list, nil
					},
				},
				Bean: func(*jexpr.Context) (any, error) { return decoded, nil },
			}, nil
		},
		"sprint": func(*jexpr.Context) (jexpr.Value, error) {
			found, err := sprints()
			if err != nil {
				return nil, err
			}
			for _, sprint := range found {
				if sprint.State != "closed" {
					return l.sprintRecord(sprint), nil
				}
			}
			return nil, nil
		},
		"closedSprints": func(*jexpr.Context) (jexpr.Value, error) {
			found, err := sprints()
			if err != nil {
				return nil, err
			}
			list := &jexpr.List{Items: []jexpr.Value{}}
			for _, sprint := range found {
				if sprint.State == "closed" {
					list.Items = append(list.Items, l.sprintRecord(sprint))
				}
			}
			return list, nil
		},
		"flagged": func(*jexpr.Context) (jexpr.Value, error) { return false, nil },
		"votes": func(*jexpr.Context) (jexpr.Value, error) {
			voters, err := h.Store.VotersByIssue(ctx, issue.ID)
			return float64(len(voters)), err
		},
		"watches": func(*jexpr.Context) (jexpr.Value, error) {
			watchers, err := h.Store.WatchersByIssue(ctx, issue.ID)
			return float64(len(watchers)), err
		},
	}
	return &jexpr.Record{
		Type:   "Issue",
		Fields: fields,
		Lazy:   lazy,
		Deferred: map[string]func(*jexpr.Context) (jexpr.Value, error){
			"project": func(*jexpr.Context) (jexpr.Value, error) {
				project, err := h.Store.ProjectByIDOrKey(ctx, l.workspaceID, issue.ProjectID)
				if err != nil {
					return nil, nil
				}
				return l.projectRecord(project), nil
			},
		},
		Bean: func(*jexpr.Context) (any, error) { return h.IssueBean(issue), nil },
	}
}

// EvaluateIssueExpression computes a Jira expression with issue and user in
// context, as the bulk issue property update does for each work item.
func (h *Handler) EvaluateIssueExpression(ctx context.Context, workspaceID, userID, issueID, expression string) (json.RawMessage, error) {
	loader := &expressionLoader{h: h, ctx: ctx, workspaceID: workspaceID, userID: userID}
	issue, err := loader.Issue(ctx, issueID)
	if err != nil {
		return nil, err
	}
	variables := map[string]jexpr.Value{"issue": issue, "user": nil}
	if user, userErr := h.Store.UserByID(ctx, userID); userErr == nil {
		variables["user"] = loader.userRecord(user)
	}
	evaluation := &jexpr.Context{Ctx: ctx, Variables: variables, Loader: loader, Limits: jexpr.DefaultLimits}
	value, err := evaluation.Evaluate(expression)
	if err != nil {
		return nil, err
	}
	encoded, err := evaluation.ToJSON(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(encoded)
}
