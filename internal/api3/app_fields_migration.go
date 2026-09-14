package api3

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// ---- app custom field configuration and values ----

// appFieldRoute serves /rest/api/3/app/field.
func (h *Handler) appFieldRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	installation, isApp := apps.InstallationFromContext(r.Context())
	rest := strings.TrimPrefix(path, "/app/field/")
	switch {
	case rest == "value" && r.Method == http.MethodPost:
		h.updateAppFieldValues(w, r, workspaceID, installation, isApp, "")
	case rest == "context/configuration/list" && r.Method == http.MethodPost:
		h.listAppFieldConfigurations(w, r, workspaceID, userID, installation, isApp, nil)
	case strings.HasSuffix(rest, "/context/configuration"):
		fieldRef := strings.TrimSuffix(rest, "/context/configuration")
		switch r.Method {
		case http.MethodGet:
			h.listAppFieldConfigurations(w, r, workspaceID, userID, installation, isApp, []string{fieldRef})
		case http.MethodPut:
			h.updateAppFieldConfigurations(w, r, workspaceID, userID, installation, isApp, fieldRef)
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case strings.HasSuffix(rest, "/value") && r.Method == http.MethodPut:
		h.updateAppFieldValues(w, r, workspaceID, installation, isApp, strings.TrimSuffix(rest, "/value"))
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

// appFieldAccess allows a site administrator or the app that provides the
// field.
func (h *Handler) appFieldAccess(w http.ResponseWriter, r *http.Request, workspaceID, userID string, installation *models.AppInstallation, isApp bool, ref string) (store.AppField, bool) {
	field, err := h.Store.AppCustomField(r.Context(), workspaceID, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		jiraError(w, http.StatusNotFound, "The custom field "+ref+" was not found.")
		return store.AppField{}, false
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return store.AppField{}, false
	}
	if isApp && installation.ID == field.InstallationID {
		return field, true
	}
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Store, workspaceID, userID)
	if err != nil || !admin {
		jiraError(w, http.StatusForbidden, "Only a Jira administrator or the app that provides the field can use this operation.")
		return store.AppField{}, false
	}
	return field, true
}

func int64Values(w http.ResponseWriter, r *http.Request, name string) ([]int64, bool) {
	values := []int64{}
	for _, raw := range r.URL.Query()[name] {
		for _, part := range strings.Split(raw, ",") {
			value, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil {
				jiraError(w, http.StatusBadRequest, name+" must be a list of numbers.")
				return nil, false
			}
			values = append(values, value)
		}
	}
	return values, true
}

func (h *Handler) listAppFieldConfigurations(w http.ResponseWriter, r *http.Request, workspaceID, userID string, installation *models.AppInstallation, isApp bool, refs []string) {
	bulk := refs == nil
	if bulk {
		var request struct {
			FieldIDsOrKeys []string `json:"fieldIdsOrKeys"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || len(request.FieldIDsOrKeys) == 0 {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"fieldIdsOrKeys": "The list of custom fields is required."})
			return
		}
		refs = request.FieldIDsOrKeys
	}
	ids, ok := int64Values(w, r, "id")
	if !ok {
		return
	}
	contextIDs, ok := int64Values(w, r, "fieldContextId")
	if !ok {
		return
	}
	query := r.URL.Query()
	issueRef, projectRef, issueTypeRef := query.Get("issueId"), query.Get("projectKeyOrId"), query.Get("issueTypeId")
	filters := 0
	for _, used := range []bool{len(ids) > 0, len(contextIDs) > 0, issueRef != "", projectRef != "" || issueTypeRef != ""} {
		if used {
			filters++
		}
	}
	if filters > 1 || (projectRef == "") != (issueTypeRef == "") {
		jiraError(w, http.StatusBadRequest, "Use only one of id, fieldContextId, issueId, or projectKeyOrId with issueTypeId.")
		return
	}
	startAt, maxResults, ok := pageRequest(w, r, 100, 1000)
	if !ok {
		return
	}
	projectID, issueTypeID, noMatch := "", "", false
	if issueRef != "" {
		issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, issueRef)
		if err != nil {
			noMatch = true
		} else {
			projectID, issueTypeID = issue.ProjectID, issue.IssueType.ID
		}
	}
	if projectRef != "" {
		project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectRef)
		issueType, typeErr := h.Store.IssueTypeByIDOrName(r.Context(), workspaceID, issueTypeRef)
		if err != nil || typeErr != nil {
			noMatch = true
		} else {
			projectID, issueTypeID = project.ID, issueType.ID
		}
	}
	values := []map[string]any{}
	for _, ref := range refs {
		field, ok := h.appFieldAccess(w, r, workspaceID, userID, installation, isApp, ref)
		if !ok {
			return
		}
		configurations, err := h.Store.AppFieldConfigurations(r.Context(), workspaceID, field.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		applicable := ""
		if projectID != "" {
			if context, err := h.Store.ApplicableCustomFieldContext(r.Context(), field.ID, projectID, issueTypeID); err == nil && context != nil {
				applicable = context.ID
			}
		}
		for _, configuration := range configurations {
			switch {
			case noMatch:
				continue
			case len(ids) > 0 && !containsInt64(ids, configuration.ID):
				continue
			case len(contextIDs) > 0 && !containsInt64(contextIDs, configuration.ContextID):
				continue
			case projectID != "" && strconv.FormatInt(configuration.ContextID, 10) != applicable:
				continue
			}
			bean := map[string]any{"id": strconv.FormatInt(configuration.ID, 10), "fieldContextId": strconv.FormatInt(configuration.ContextID, 10)}
			if len(configuration.Configuration) > 0 {
				bean["configuration"] = configuration.Configuration
			}
			if len(configuration.Schema) > 0 {
				bean["schema"] = configuration.Schema
			}
			if bulk {
				bean["customFieldId"] = field.ID
			}
			values = append(values, bean)
		}
	}
	start := min(startAt, len(values))
	end := min(start+maxResults, len(values))
	writeJSON(w, http.StatusOK, h.pageBean(r, startAt, maxResults, len(values), values[start:end], end-start))
}

func containsInt64(values []int64, value int64) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (h *Handler) updateAppFieldConfigurations(w http.ResponseWriter, r *http.Request, workspaceID, userID string, installation *models.AppInstallation, isApp bool, ref string) {
	field, ok := h.appFieldAccess(w, r, workspaceID, userID, installation, isApp, ref)
	if !ok {
		return
	}
	var request struct {
		Configurations []struct {
			ID             string          `json:"id"`
			FieldContextID string          `json:"fieldContextId"`
			Configuration  json.RawMessage `json:"configuration"`
			Schema         json.RawMessage `json:"schema"`
		} `json:"configurations"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&request); err != nil || request.Configurations == nil || len(request.Configurations) > 1000 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"configurations": "Give a list of at most 1000 configurations."})
		return
	}
	configurations := []store.AppFieldConfiguration{}
	for _, item := range request.Configurations {
		id, idErr := strconv.ParseInt(item.ID, 10, 64)
		contextID, contextErr := strconv.ParseInt(item.FieldContextID, 10, 64)
		if idErr != nil || contextErr != nil {
			jiraError(w, http.StatusBadRequest, "Each configuration needs its id and fieldContextId.")
			return
		}
		configurations = append(configurations, store.AppFieldConfiguration{ID: id, ContextID: contextID, Configuration: item.Configuration, Schema: item.Schema})
	}
	err := h.Store.SetAppFieldConfigurations(r.Context(), workspaceID, field.ID, configurations)
	if errors.Is(err, store.ErrAppFieldConfigurationValidation) {
		jiraError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrAppFieldConfigurationValidation.Error()+": "))
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// updateAppFieldValues writes the values of fields the calling app provides.
func (h *Handler) updateAppFieldValues(w http.ResponseWriter, r *http.Request, workspaceID string, installation *models.AppInstallation, isApp bool, fieldRef string) {
	for _, flag := range []string{"generateChangelog", "generateAppEvents"} {
		if raw := r.URL.Query().Get(flag); raw != "" {
			if _, err := strconv.ParseBool(raw); err != nil {
				jiraError(w, http.StatusBadRequest, flag+" must be true or false.")
				return
			}
		}
	}
	type update struct {
		CustomField string          `json:"customField"`
		IssueIDs    []int64         `json:"issueIds"`
		Value       json.RawMessage `json:"value"`
	}
	var request struct {
		Updates []update `json:"updates"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&request); err != nil || len(request.Updates) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"updates": "At least one update is required."})
		return
	}
	type write struct {
		field store.AppField
		issue *models.Issue
		value json.RawMessage
	}
	writes := []write{}
	seen := map[string]bool{}
	for _, item := range request.Updates {
		ref := fieldRef
		if ref == "" {
			ref = item.CustomField
		}
		if ref == "" || len(item.IssueIDs) == 0 || len(item.Value) == 0 {
			jiraError(w, http.StatusBadRequest, "Each update needs a custom field, issue ids and a value.")
			return
		}
		field, err := h.Store.AppCustomField(r.Context(), workspaceID, ref)
		if errors.Is(err, pgx.ErrNoRows) {
			jiraError(w, http.StatusNotFound, "The custom field "+ref+" was not found.")
			return
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !isApp || installation.ID != field.InstallationID {
			jiraError(w, http.StatusForbidden, "Only the app that provides the field can update its values.")
			return
		}
		for _, issueID := range item.IssueIDs {
			key := field.ID + "/" + strconv.FormatInt(issueID, 10)
			if seen[key] {
				jiraError(w, http.StatusBadRequest, "Each combination of custom field and issue can be updated once.")
				return
			}
			seen[key] = true
			issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, strconv.FormatInt(issueID, 10))
			if err != nil {
				jiraError(w, http.StatusBadRequest, fmt.Sprintf("The issue %d does not exist.", issueID))
				return
			}
			writes = append(writes, write{field: field, issue: issue, value: item.Value})
		}
	}
	for _, item := range writes {
		if _, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{
			ActorID: installation.PrincipalID, WorkspaceID: workspaceID, IssueIDOrKey: item.issue.ID, Fields: map[string]json.RawMessage{item.field.ID: item.value},
		}); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Connect app migration ----

var transferIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// connectMigrationRoute serves /rest/atlassian-connect/1/migration.
func (h *Handler) connectMigrationRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	installation, isApp := apps.InstallationFromContext(r.Context())
	rest := strings.TrimPrefix(r.URL.Path, "/rest/atlassian-connect/1/migration/")
	parts := strings.Split(rest, "/")
	if len(parts) == 3 && parts[2] == "task" {
		h.connectFieldMigrationTask(w, r, workspaceID, installation, isApp, parts[0], parts[1])
		return
	}
	if !isApp || installation.Format != "connect" {
		operationMessage(w, http.StatusForbidden, "Only Connect apps can make this request.")
		return
	}
	transferID := strings.TrimSpace(r.Header.Get("Atlassian-Transfer-Id"))
	if !transferIDPattern.MatchString(transferID) {
		operationMessage(w, http.StatusBadRequest, "The Atlassian-Transfer-Id header must be a transfer id.")
		return
	}
	valid, err := h.Store.ValidAppMigrationTransfer(r.Context(), installation.ID, transferID)
	if err != nil {
		operationMessage(w, http.StatusInternalServerError, "The transfer could not be checked.")
		return
	}
	if !valid {
		operationMessage(w, http.StatusForbidden, "The transfer ID was not found.")
		return
	}
	switch {
	case rest == "field" && r.Method == http.MethodPut:
		h.migrateConnectFieldValues(w, r, workspaceID, installation)
	case len(parts) == 2 && parts[0] == "properties" && r.Method == http.MethodPut:
		h.migrateEntityProperties(w, r, workspaceID, installation, parts[1])
	case rest == "workflow/rule/search" && r.Method == http.MethodPost:
		h.searchMigratedWorkflowRules(w, r, workspaceID, installation)
	default:
		operationMessage(w, http.StatusNotFound, "No resource found.")
	}
}

func (h *Handler) migrateConnectFieldValues(w http.ResponseWriter, r *http.Request, workspaceID string, installation *models.AppInstallation) {
	var request struct {
		UpdateValueList []struct {
			Type     string   `json:"_type"`
			FieldID  *int64   `json:"fieldID"`
			IssueID  *int64   `json:"issueID"`
			Number   *float64 `json:"number"`
			OptionID *string  `json:"optionID"`
			RichText *string  `json:"richText"`
			String   *string  `json:"string"`
			Text     *string  `json:"text"`
		} `json:"updateValueList"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&request); err != nil || len(request.UpdateValueList) == 0 {
		operationMessage(w, http.StatusBadRequest, "The list of field values is required.")
		return
	}
	fields := map[string]bool{}
	type write struct {
		issueID, fieldID string
		value            json.RawMessage
	}
	writes := []write{}
	for _, item := range request.UpdateValueList {
		if item.FieldID == nil || item.IssueID == nil {
			operationMessage(w, http.StatusBadRequest, "Each value needs _type, fieldID and issueID.")
			return
		}
		fieldID := "customfield_" + strconv.FormatInt(*item.FieldID, 10)
		field, err := h.Store.AppCustomField(r.Context(), workspaceID, fieldID)
		if err != nil || field.InstallationID != installation.ID {
			operationMessage(w, http.StatusBadRequest, "The field "+fieldID+" is not a field of this app.")
			return
		}
		fields[fieldID] = true
		if len(fields) > 200 {
			operationMessage(w, http.StatusBadRequest, "The values of at most 200 custom fields can be updated.")
			return
		}
		var value any
		switch item.Type {
		case "StringIssueField":
			value = item.String
		case "TextIssueField":
			value = item.Text
		case "RichTextIssueField":
			value = item.RichText
		case "NumberIssueField":
			value = item.Number
		case "SingleSelectIssueField":
			value = item.OptionID
		case "MultiSelectIssueField":
			operationMessage(w, http.StatusBadRequest, "Multi-select issue fields are not supported.")
			return
		default:
			operationMessage(w, http.StatusBadRequest, "The _type "+item.Type+" is not supported.")
			return
		}
		raw, _ := json.Marshal(value)
		issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, strconv.FormatInt(*item.IssueID, 10))
		if err != nil {
			operationMessage(w, http.StatusBadRequest, fmt.Sprintf("The issue %d does not exist.", *item.IssueID))
			return
		}
		writes = append(writes, write{issueID: issue.ID, fieldID: fieldID, value: raw})
	}
	for _, item := range writes {
		if _, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{
			ActorID: installation.PrincipalID, WorkspaceID: workspaceID, IssueIDOrKey: item.issueID, Fields: map[string]json.RawMessage{item.fieldID: item.value},
		}); err != nil {
			operationMessage(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) migrateEntityProperties(w http.ResponseWriter, r *http.Request, workspaceID string, installation *models.AppInstallation, entityType string) {
	var request []struct {
		EntityID *json.Number `json:"entityId"`
		Key      string       `json:"key"`
		Value    *string      `json:"value"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil {
		operationMessage(w, http.StatusBadRequest, "The request must be a list of entity properties.")
		return
	}
	properties := []store.MigrationEntityProperty{}
	for _, item := range request {
		if item.EntityID == nil || item.Value == nil {
			operationMessage(w, http.StatusBadRequest, "Each property needs entityId, key and value.")
			return
		}
		entityID, err := item.EntityID.Int64()
		if err != nil {
			operationMessage(w, http.StatusBadRequest, "The entityId must be a whole number.")
			return
		}
		value := json.RawMessage(*item.Value)
		if !json.Valid(value) {
			value, _ = json.Marshal(*item.Value)
		}
		properties = append(properties, store.MigrationEntityProperty{EntityID: entityID, Key: item.Key, Value: value})
	}
	err := h.Store.SetMigrationEntityProperties(r.Context(), workspaceID, installation.PrincipalID, entityType, properties)
	if errors.Is(err, store.ErrMigrationValidation) {
		operationMessage(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrMigrationValidation.Error()+": "))
		return
	}
	if err != nil {
		operationMessage(w, http.StatusInternalServerError, "The properties could not be saved.")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) searchMigratedWorkflowRules(w http.ResponseWriter, r *http.Request, workspaceID string, installation *models.AppInstallation) {
	var request struct {
		WorkflowEntityID string   `json:"workflowEntityId"`
		RuleIDs          []string `json:"ruleIds"`
		Expand           string   `json:"expand"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil || !transferIDPattern.MatchString(request.WorkflowEntityID) || len(request.RuleIDs) == 0 || len(request.RuleIDs) > 10 {
		operationMessage(w, http.StatusBadRequest, "A workflowEntityId and between 1 and 10 ruleIds are required.")
		return
	}
	workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
	if err != nil {
		operationMessage(w, http.StatusInternalServerError, "The workflows could not be read.")
		return
	}
	valid := []map[string]any{}
	invalid := []string{}
	for index := range workflows {
		if !strings.EqualFold(workflows[index].EntityID, request.WorkflowEntityID) {
			continue
		}
		byID := map[string]appRuleRef{}
		for _, ref := range appRules(&workflows[index], installation.Key) {
			byID[ref.rule.ID] = ref
		}
		lists := map[string][]map[string]any{"postfunction": {}, "condition": {}, "validator": {}}
		for _, id := range request.RuleIDs {
			ref, ok := byID[id]
			if !ok {
				invalid = append(invalid, id)
				continue
			}
			lists[ref.kind] = append(lists[ref.kind], appRuleBean(ref, request.Expand == "transition"))
		}
		if len(invalid) < len(request.RuleIDs) {
			valid = append(valid, map[string]any{
				"workflowId": map[string]any{"name": workflows[index].Name, "draft": false}, "postFunctions": lists["postfunction"],
				"conditions": lists["condition"], "validators": lists["validator"],
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"workflowEntityId": request.WorkflowEntityID, "validRules": valid, "invalidRules": invalid})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflowEntityId": request.WorkflowEntityID, "validRules": valid, "invalidRules": request.RuleIDs})
}

func (h *Handler) connectFieldMigrationTask(w http.ResponseWriter, r *http.Request, workspaceID string, installation *models.AppInstallation, isApp bool, connectKey, moduleKey string) {
	if !isApp {
		operationMessage(w, http.StatusUnauthorized, "Only Connect and Forge apps can make this request.")
		return
	}
	field, err := h.Store.AppCustomField(r.Context(), workspaceID, connectKey+"__"+moduleKey)
	if err != nil || installation.Key != connectKey || field.InstallationID != installation.ID {
		operationMessage(w, http.StatusNotFound, "No migrated Forge module with the given key was found.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		task, err := h.Store.ConnectFieldMigrationTask(r.Context(), workspaceID, installation.ID, moduleKey)
		if errors.Is(err, pgx.ErrNoRows) {
			operationMessage(w, http.StatusNotFound, "No migration task exists for the field.")
			return
		}
		if err != nil {
			operationMessage(w, http.StatusInternalServerError, "The migration task could not be read.")
			return
		}
		writeJSON(w, http.StatusOK, h.apiTaskBean(task))
	case http.MethodPost:
		retrigger := false
		if raw := r.URL.Query().Get("retriggerCompletedMigration"); raw != "" {
			parsed, err := strconv.ParseBool(raw)
			if err != nil {
				operationMessage(w, http.StatusBadRequest, "retriggerCompletedMigration must be true or false.")
				return
			}
			retrigger = parsed
		}
		_, err := h.Store.SubmitConnectFieldMigration(r.Context(), workspaceID, installation.PrincipalID, field, retrigger)
		if errors.Is(err, store.ErrConnectFieldMigrationRunning) {
			operationMessage(w, http.StatusConflict, "A migration task is already in progress for the field.")
			return
		}
		if err != nil {
			operationMessage(w, http.StatusInternalServerError, "The migration task could not be submitted.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		operationMessage(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

// ---- service registry ----

func (h *Handler) serviceRegistry(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if installation, ok := apps.InstallationFromContext(r.Context()); !ok || installation.Format != "connect" {
		jiraError(w, http.StatusForbidden, "Only Connect apps can make this request.")
		return
	}
	ids := []string{}
	for _, raw := range r.URL.Query()["serviceIds"] {
		for _, id := range strings.Split(raw, ",") {
			id = strings.TrimSpace(id)
			if encoded, ok := strings.CutPrefix(id, "b:"); ok {
				decoded, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					jiraError(w, http.StatusBadRequest, "The service id "+id+" is not valid Base64.")
					return
				}
				id = string(decoded)
			}
			if id != "" {
				ids = append(ids, strings.ToLower(id))
			}
		}
	}
	if len(ids) == 0 || len(ids) > 20 {
		jiraError(w, http.StatusBadRequest, "Give between 1 and 20 serviceIds.")
		return
	}
	services, err := h.Store.ServiceRegistryServices(r.Context(), workspaceID, ids)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	beans := make([]map[string]any, 0, len(services))
	for _, service := range services {
		beans = append(beans, map[string]any{
			"id": service.ID, "name": service.Name, "description": service.Description, "organizationId": service.OrganizationID, "revision": service.Revision,
			"serviceTier": map[string]any{"id": service.Tier.ID, "level": service.Tier.Level, "name": service.Tier.Name, "nameKey": service.Tier.NameKey, "description": service.Tier.Description},
		})
	}
	writeJSON(w, http.StatusOK, beans)
}
