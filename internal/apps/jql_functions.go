package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

const maxAppJQLFunctionDepth = 4

// JQLFunctionEvaluator resolves installed-app functions through Jira-compatible
// precomputations. Cache misses call the descriptor endpoint synchronously;
// every returned fragment is parsed as JQL before it can affect SQL.
type JQLFunctionEvaluator struct {
	Store   *store.Store
	Secrets *secretbox.Box
	Client  *http.Client
	Now     func() time.Time
}

func (e *JQLFunctionEvaluator) Expand(ctx context.Context, workspaceID string, query *jql.Query) error {
	if e == nil || e.Store == nil {
		return nil
	}
	return e.expand(ctx, workspaceID, query, 0)
}

func (e *JQLFunctionEvaluator) expand(ctx context.Context, workspaceID string, query *jql.Query, depth int) error {
	if depth > maxAppJQLFunctionDepth {
		return fmt.Errorf("custom JQL function expansion exceeds %d levels", maxAppJQLFunctionDepth)
	}
	return jql.TransformClauseFunctions(query, func(invocation jql.FunctionInvocation) (jql.Node, bool, error) {
		function, err := e.Store.ActiveAppJQLFunction(ctx, workspaceID, invocation.Name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, true, err
		}
		field, fieldType, err := e.fieldContext(ctx, workspaceID, invocation.Field)
		if err != nil {
			return nil, true, err
		}
		operator := appJQLOperator(invocation.Operator)
		if !containsFold(function.Types, fieldType) {
			return nil, true, fmt.Errorf("function %s() does not support field %s", function.Name, invocation.Field)
		}
		if !containsFold(function.Operators, operator) {
			return nil, true, fmt.Errorf("function %s() does not support operator %s", function.Name, strings.ReplaceAll(operator, "_", " "))
		}
		if err := validateJQLFunctionArguments(*function, invocation.Arguments); err != nil {
			return nil, true, err
		}
		now := time.Now().UTC()
		if e.Now != nil {
			now = e.Now().UTC()
		}
		functionKey := function.AppKey + "__" + function.Key
		precomputation, err := e.Store.EnsureJQLFunctionPrecomputation(ctx, function.InstallationID, functionKey, function.Name, field, strings.ReplaceAll(operator, "_", " "), invocation.Arguments, now)
		if err != nil {
			return nil, true, err
		}
		fragment := precomputation.Value
		if precomputation.Error != nil {
			return nil, true, errors.New(*precomputation.Error)
		}
		if fragment == nil {
			fragment, err = e.evaluate(ctx, workspaceID, *function, *precomputation, field, fieldType, strings.ReplaceAll(operator, "_", " "), invocation.Arguments, now)
			if err != nil {
				return nil, true, err
			}
		}
		replacement, err := jql.Parse(*fragment)
		if err != nil {
			return nil, true, fmt.Errorf("function %s() returned invalid JQL: %w", function.Name, err)
		}
		if replacement.OrderBy != nil {
			return nil, true, fmt.Errorf("function %s() returned JQL with ORDER BY", function.Name)
		}
		if err := e.expand(ctx, workspaceID, replacement, depth+1); err != nil {
			return nil, true, err
		}
		return replacement.Root, true, nil
	})
}

func (e *JQLFunctionEvaluator) fieldContext(ctx context.Context, workspaceID, raw string) (string, string, error) {
	field := strings.ToLower(strings.TrimSpace(raw))
	switch field {
	case "issue", "issuekey", "key":
		return "key", "issue", nil
	case "parent":
		return "parent", "issue", nil
	case "id":
		return "id", "number", nil
	case "project":
		return "project", "project", nil
	case "assignee", "reporter", "creator":
		return field, "user", nil
	case "created", "updated", "due", "resolutiondate":
		return field, "date", nil
	case "fixversion", "affectedversion":
		return field, "version", nil
	case "component":
		return field, "component", nil
	case "priority":
		return field, "priority", nil
	case "resolution":
		return field, "resolution", nil
	case "issuetype":
		return field, "issue_type", nil
	case "status":
		return field, "status", nil
	case "statuscategory":
		return field, "status_category", nil
	case "labels":
		return field, "label", nil
	case "summary", "description", "environment", "sprint":
		return field, "text", nil
	}
	fields, err := e.Store.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil {
		return "", "", err
	}
	for _, candidate := range fields {
		if strings.EqualFold(field, candidate.ID) || strings.EqualFold(field, candidate.Name) || candidate.AppKey != "" && strings.EqualFold(field, candidate.AppKey+"__"+candidate.AppModuleKey) {
			if candidate.Type == models.CustomFieldNumber {
				return candidate.ID, "number", nil
			}
			return candidate.ID, "text", nil
		}
	}
	return "", "", fmt.Errorf("field does not exist or is not searchable: %s", raw)
}

func validateJQLFunctionArguments(function models.AppJQLFunction, arguments []string) error {
	required := 0
	for index, argument := range function.Arguments {
		if argument.Required {
			required = index + 1
		}
	}
	if len(arguments) < required || len(arguments) > len(function.Arguments) {
		return fmt.Errorf("function %s() expects %d to %d arguments", function.Name, required, len(function.Arguments))
	}
	return nil
}

func appJQLOperator(operator string) string {
	if operator == "notin" {
		return "not_in"
	} else if operator == "isnot" {
		return "is_not"
	}
	return strings.ToLower(operator)
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}

type jqlFunctionResponse struct {
	JQL                        *string `json:"jql"`
	Error                      *string `json:"error"`
	StoreErrorAsPrecomputation bool    `json:"storeErrorAsPrecomputation"`
}

func (e *JQLFunctionEvaluator) evaluate(ctx context.Context, workspaceID string, function models.AppJQLFunction, precomputation models.JQLFunctionPrecomputation, field, fieldType, operator string, arguments []string, now time.Time) (*string, error) {
	if e.Secrets == nil || e.Client == nil {
		return nil, fmt.Errorf("function %s() is awaiting app evaluation", function.Name)
	}
	payload, err := json.Marshal(map[string]any{
		"precomputationId": precomputation.ID,
		"clause":           map[string]any{"field": field, "type": []string{fieldType}, "operator": operator, "functionName": function.Name, "arguments": arguments},
	})
	if err != nil {
		return nil, err
	}
	target, err := appRelativeURL(function.BaseURL, function.Path)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	secret, err := e.Secrets.Open(function.SecretCiphertext, workspaceID+"/"+function.AppKey)
	if err != nil {
		return nil, fmt.Errorf("open app credentials: %w", err)
	}
	delivery := &models.AppOutboundDelivery{ID: precomputation.ID, AppKey: function.AppKey, Format: function.Format, Event: "jira:jql_function", Payload: payload}
	if err := signOutboundRequest(request, delivery, workspaceID, secret, now); err != nil {
		return nil, err
	}
	response, err := e.Client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("evaluate function %s(): %w", function.Name, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("function %s() returned HTTP %d", function.Name, response.StatusCode)
	}
	var result jqlFunctionResponse
	if err := json.Unmarshal(body, &result); err != nil || (result.JQL == nil) == (result.Error == nil) {
		return nil, fmt.Errorf("function %s() returned an invalid response", function.Name)
	}
	if result.Error != nil {
		if result.StoreErrorAsPrecomputation {
			_, err = e.Store.UpdateJQLFunctionPrecomputations(ctx, function.InstallationID, []models.JQLFunctionPrecomputationUpdate{{ID: precomputation.ID, Error: result.Error}}, false)
			if err != nil {
				return nil, err
			}
		}
		return nil, errors.New(*result.Error)
	}
	fragment := strings.TrimSpace(*result.JQL)
	parsed, parseErr := jql.Parse(fragment)
	if parseErr != nil || parsed.OrderBy != nil {
		return nil, fmt.Errorf("function %s() returned invalid JQL", function.Name)
	}
	_, err = e.Store.UpdateJQLFunctionPrecomputations(ctx, function.InstallationID, []models.JQLFunctionPrecomputationUpdate{{ID: precomputation.ID, Value: &fragment}}, false)
	if err != nil {
		return nil, err
	}
	return &fragment, nil
}
