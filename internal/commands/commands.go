// Package commands is the single mutation layer. Both edges (web + REST) call
// into it; nothing else may write state or append actions.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

type Service struct {
	Store *store.Store
	Blobs attachments.Store
}

type CreateIssueInput struct {
	ActorID                   string
	ReporterID                string
	WorkspaceID               string
	ProjectIDOrKey            string
	Summary                   string
	Description               string // plain text in V0; stored as an ADF paragraph
	DescriptionADF            json.RawMessage
	IssueTypeID               string
	ParentIDOrKey             string
	PriorityID                string
	AssigneeID                string
	UseProjectDefaultAssignee bool
	SecurityLevelID           string
	Labels                    []string
	Fields                    map[string]json.RawMessage
	// OriginalEstimate and RemainingEstimate are time tracking estimates in
	// seconds; nil leaves one unset.
	OriginalEstimate  *int64
	RemainingEstimate *int64
}

// DeleteIssue removes the issue transactionally, then cleans up attachment
// blobs whose metadata was cascade-deleted with it.
func (s *Service) DeleteIssue(ctx context.Context, actorID, workspaceID, issueIDOrKey, reason string) (*models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	children, err := s.Store.ChildIssues(ctx, workspaceID, issue.ID)
	if err != nil {
		return nil, err
	}
	if len(children) > 0 {
		return nil, fmt.Errorf("move or delete sub-tasks before deleting their parent")
	}
	action, blobRefs, err := s.Store.DeleteIssue(ctx, actorID, workspaceID, issue.ID, reason)
	if err != nil {
		return nil, err
	}
	if s.Blobs == nil && len(blobRefs) > 0 {
		return action, fmt.Errorf("issue deleted but %d attachment blobs could not be cleaned up: storage not configured", len(blobRefs))
	}
	var cleanupErr error
	for _, ref := range blobRefs {
		if err := s.Blobs.Delete(ctx, ref); err != nil {
			if deferErr := s.Store.DeferAttachmentBlobDeletion(ctx, ref, err.Error(), 1); deferErr != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("defer attachment blob %q after %v: %w", ref, err, deferErr))
			}
			continue
		}
		if err := s.Store.CompleteAttachmentBlobDeletion(ctx, ref); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("complete attachment blob cleanup %q: %w", ref, err))
		}
	}
	return action, cleanupErr
}

func (s *Service) CreateIssue(ctx context.Context, in CreateIssueInput) (*models.Issue, *models.Action, error) {
	if in.ActorID == "" {
		return nil, nil, fmt.Errorf("no actor")
	}
	in.Summary = strings.TrimSpace(in.Summary)
	if len(in.Summary) == 0 || len(in.Summary) > 255 {
		return nil, nil, fmt.Errorf("summary is required (max 255 chars)")
	}
	if len(in.Description) > 1<<20 {
		return nil, nil, fmt.Errorf("description must be at most 1 MiB")
	}
	if in.IssueTypeID == "" {
		return nil, nil, fmt.Errorf("issue type is required")
	}
	project, err := s.Store.ProjectByIDOrKey(ctx, in.WorkspaceID, in.ProjectIDOrKey)
	if err != nil {
		return nil, nil, fmt.Errorf("project %q not found in workspace", in.ProjectIDOrKey)
	}
	issueType, err := s.Store.IssueTypeByIDOrName(ctx, in.WorkspaceID, in.IssueTypeID)
	if err != nil {
		return nil, nil, fmt.Errorf("issue type %q not found", in.IssueTypeID)
	}
	configuration, err := s.jiraSiteConfiguration(ctx, in.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	if issueType.Subtask && !configuration.SubTasksEnabled {
		return nil, nil, fmt.Errorf("subtasks are disabled for this site")
	}
	// Jira accepts an option as its id, {"id"} or {"value"}; every check below
	// sees the option ids a work item stores.
	if err = s.normalizeOptionFields(ctx, in.WorkspaceID, project.ID, issueType.ID, in.Fields); err != nil {
		return nil, nil, err
	}
	if err = s.enforceFieldConfiguration(ctx, in, project.ID, issueType.ID); err != nil {
		return nil, nil, err
	}
	if err = s.enforceCustomFieldContexts(ctx, in.WorkspaceID, project.ID, issueType.ID, in.Fields); err != nil {
		return nil, nil, err
	}
	parentID := ""
	if issueType.Subtask {
		if strings.TrimSpace(in.ParentIDOrKey) == "" {
			return nil, nil, fmt.Errorf("parent is required for a sub-task")
		}
		parent, err := s.visibleIssue(ctx, in.ActorID, in.WorkspaceID, in.ParentIDOrKey)
		if err != nil || parent.ProjectID != project.ID {
			return nil, nil, fmt.Errorf("parent must be a visible work item in this project")
		}
		if parent.IssueType.Subtask {
			return nil, nil, fmt.Errorf("a sub-task cannot be the parent of another sub-task")
		}
		parentID = parent.ID
	} else if strings.TrimSpace(in.ParentIDOrKey) != "" {
		parent, err := s.epicParent(ctx, in.ActorID, in.WorkspaceID, issueType.HierarchyLevel, in.ParentIDOrKey)
		if err != nil {
			return nil, nil, err
		}
		parentID = parent.ID
	}
	priorityID := ""
	if in.PriorityID != "" {
		priority, err := s.Store.PriorityByIDOrName(ctx, in.WorkspaceID, in.PriorityID)
		if err != nil {
			return nil, nil, fmt.Errorf("priority %q not found", in.PriorityID)
		}
		priorityID = priority.ID
	}
	if in.UseProjectDefaultAssignee && in.AssigneeID == "-1" {
		in.AssigneeID = ""
	}
	if in.UseProjectDefaultAssignee && in.Fields != nil {
		if raw, provided := in.Fields["components"]; provided {
			componentAssignee, selected, err := s.Store.ComponentDefaultAssignee(ctx, in.WorkspaceID, project.ID, raw)
			if err != nil {
				return nil, nil, err
			}
			if selected {
				in.AssigneeID = componentAssignee
				in.UseProjectDefaultAssignee = false
			}
		}
	}
	if in.AssigneeID != "" {
		if _, err := s.Store.MemberByID(ctx, in.WorkspaceID, in.AssigneeID); err != nil {
			return nil, nil, fmt.Errorf("assignee is not an active workspace member")
		}
	}
	if in.UseProjectDefaultAssignee && project.AssigneeType == "PROJECT_LEAD" {
		if _, err := s.Store.MemberByID(ctx, in.WorkspaceID, project.LeadAccountID); err != nil {
			return nil, nil, fmt.Errorf("project lead is not an active workspace member")
		}
		in.AssigneeID = project.LeadAccountID
	}
	if in.AssigneeID == "" && !configuration.UnassignedIssuesAllowed {
		return nil, nil, fmt.Errorf("unassigned work items are disabled for this site")
	}
	if in.SecurityLevelID != "" {
		scheme, err := s.Store.SecuritySchemeForProject(ctx, project.ID)
		if err != nil {
			return nil, nil, err
		}
		valid := false
		if scheme != nil {
			for _, level := range scheme.Levels {
				if level.ID == in.SecurityLevelID {
					valid = true
					break
				}
			}
		}
		if !valid {
			return nil, nil, fmt.Errorf("security level is not available for this project")
		}
		visible, err := authz.CanSeeIssue(ctx, s.Store, in.WorkspaceID, project.ID, in.ActorID, "", in.SecurityLevelID)
		if err != nil {
			return nil, nil, err
		}
		if !visible {
			return nil, nil, fmt.Errorf("security level would hide this issue from you")
		}
	}
	if err := s.validateCustomFields(ctx, project.ID, in.Fields); err != nil {
		return nil, nil, err
	}
	labels, err := normalizeLabels(in.Labels)
	if err != nil {
		return nil, nil, err
	}
	description := plainTextToADF(in.Description)
	if in.DescriptionADF != nil {
		if len(in.DescriptionADF) > 1<<20 {
			return nil, nil, fmt.Errorf("description must be at most 1 MiB")
		}
		if string(in.DescriptionADF) != "null" {
			var doc struct {
				Type    string            `json:"type"`
				Version int               `json:"version"`
				Content []json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(in.DescriptionADF, &doc); err != nil || doc.Type != "doc" || doc.Version != 1 {
				return nil, nil, fmt.Errorf("description must be an ADF document with type doc and version 1")
			}
		}
		description = in.DescriptionADF
	}
	if (in.OriginalEstimate != nil || in.RemainingEstimate != nil) && !configuration.TimeTrackingEnabled {
		return nil, nil, fmt.Errorf("time tracking is disabled for this site")
	}
	// Work starts where the workflow's initial transition leads, after that
	// transition's validators and post functions run on the new work item.
	wf, err := s.Store.WorkflowForProjectAndIssueType(ctx, project.ID, issueType.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		wf, err = workflow.Default(), nil
	}
	if err != nil {
		return nil, nil, err
	}
	eventID, notificationKind := int64(1), "issue_created"
	var triggers store.IssueUpdate
	if initial := wf.Initial(); initial != nil {
		created, runErr := s.runInitialTransition(ctx, in, *initial, parentID, description, priorityID, labels)
		if runErr != nil {
			return nil, nil, runErr
		}
		description, priorityID, labels = created.description, created.priorityID, created.labels
		in.Summary, in.AssigneeID, in.Fields = created.summary, created.assigneeID, created.fields
		triggers = created.triggers
		if custom, parseErr := strconv.ParseInt(initial.CustomIssueEventID, 10, 64); parseErr == nil && custom > 0 {
			eventID, notificationKind = custom, "issue_event"
		}
	}
	issue, action, err := s.Store.CreateEstimatedIssueForReporter(ctx, in.ActorID, in.ReporterID, project.ID, in.Summary,
		description, wf.InitialStatus(), issueType.ID, priorityID, in.AssigneeID, labels, in.Fields, in.SecurityLevelID, parentID,
		store.IssueEstimates{Original: in.OriginalEstimate, Remaining: in.RemainingEstimate})
	if err != nil {
		return nil, nil, err
	}
	if err = s.deliverIssueEvent(ctx, in.WorkspaceID, in.ActorID, issue, action, eventID, notificationKind, "created"); err != nil {
		return issue, action, err
	}
	if len(triggers.TriggeredWebhookIDs) > 0 || len(triggers.TriggeredAgents) > 0 {
		triggers.SuppressChangelog, triggers.SuppressEvents = true, true
		if _, _, err = s.Store.UpdateIssue(ctx, in.ActorID, in.WorkspaceID, issue.ID, triggers); err != nil {
			return issue, action, err
		}
	}
	return issue, action, nil
}

func (s *Service) deliverIssueEvent(ctx context.Context, workspaceID, actorID string, issue *models.Issue, action *models.Action, eventID int64, kind, verb string) error {
	if issue == nil || action == nil {
		return nil
	}
	return s.Store.DeliverIssueNotification(ctx, workspaceID, actorID, issue.ID, action.Seq, eventID, kind, verb+" "+issue.Key)
}

// plainTextToADF wraps plain text into the minimal ADF document. Replaced by the
// real ADF edge in V1/V3.
func plainTextToADF(text string) json.RawMessage {
	doc := map[string]any{
		"type":    "doc",
		"version": 1,
	}
	if text == "" {
		b, _ := json.Marshal(doc)
		return b
	}
	doc["content"] = []map[string]any{{
		"type": "paragraph",
		"content": []map[string]any{{
			"type": "text",
			"text": text,
		}},
	}}
	b, _ := json.Marshal(doc)
	return b
}

// enforceFieldConfiguration applies the project's field configuration to a
// create request, so the command path rejects exactly what the form advertises
// rather than leaving required and hidden as cosmetic labels.
func (s *Service) enforceFieldConfiguration(ctx context.Context, in CreateIssueInput, projectID, issueTypeID string) error {
	rules, err := s.Store.ResolveFieldBehaviour(ctx, in.WorkspaceID, projectID, issueTypeID)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	supplied := map[string]bool{
		"summary":     strings.TrimSpace(in.Summary) != "",
		"description": strings.TrimSpace(in.Description) != "" || len(in.DescriptionADF) > 0,
		"assignee":    in.AssigneeID != "" && in.AssigneeID != "-1",
		"priority":    in.PriorityID != "",
		"parent":      strings.TrimSpace(in.ParentIDOrKey) != "",
		"security":    in.SecurityLevelID != "",
		// The browser create form posts an empty labels input, and
		// strings.Split("", ",") yields one empty entry, so count real values.
		"labels": hasNonEmptyValue(in.Labels),
	}
	for field, raw := range in.Fields {
		supplied[field] = supplied[field] || suppliedFieldValue(raw)
	}
	for field, rule := range rules {
		// Summary and the context fields are required by the command path
		// itself, so a configuration can neither relax nor hide them.
		if field == "summary" {
			continue
		}
		if rule.IsRequired && !supplied[field] {
			return fmt.Errorf("%s is required by the field configuration for this work type", field)
		}
		if rule.IsHidden && supplied[field] {
			return fmt.Errorf("%s is hidden by the field configuration for this work type", field)
		}
	}
	return nil
}

func hasNonEmptyValue(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func suppliedFieldValue(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	switch value {
	case "", "null", `""`, "[]", "{}":
		return false
	}
	return true
}

// fieldWriteIntent describes what one update does to a field: whether it is
// touched at all, and whether it leaves a value behind afterwards.
type fieldWriteIntent struct{ touched, hasValue bool }

func stringWriteIntent(value *string, cleared ...string) fieldWriteIntent {
	if value == nil {
		return fieldWriteIntent{}
	}
	trimmed := strings.TrimSpace(*value)
	has := trimmed != ""
	for _, empty := range cleared {
		if trimmed == empty {
			has = false
		}
	}
	return fieldWriteIntent{touched: true, hasValue: has}
}

// issueWriteIntents maps an update onto the field IDs a field configuration
// speaks about, so edits and transitions are judged by the same rules the
// create form advertises.
func issueWriteIntents(update store.IssueUpdate) map[string]fieldWriteIntent {
	intents := map[string]fieldWriteIntent{
		"summary":  stringWriteIntent(update.Summary),
		"priority": stringWriteIntent(update.PriorityID),
		"assignee": stringWriteIntent(update.AssigneeID, "-1"),
		"parent":   stringWriteIntent(update.ParentID),
		"security": stringWriteIntent(update.SecurityLevelID),
	}
	if update.Description != nil {
		intents["description"] = fieldWriteIntent{touched: true, hasValue: suppliedFieldValue(update.Description)}
	}
	if update.Labels != nil {
		intents["labels"] = fieldWriteIntent{touched: true, hasValue: hasNonEmptyValue(*update.Labels)}
	}
	for field, raw := range update.Fields {
		intents[field] = fieldWriteIntent{touched: true, hasValue: suppliedFieldValue(raw)}
	}
	for field, operations := range update.VersionOperations {
		intents[field] = fieldWriteIntent{touched: true, hasValue: len(operations) > 0}
	}
	return intents
}

// enforceFieldConfigurationWrite applies the project's field configuration to an
// edit or transition. A required field may not be cleared and a hidden field may
// not be given a value; an update that leaves a field alone is never rejected,
// so the rules do not block edits to unrelated fields.
func (s *Service) enforceFieldConfigurationWrite(ctx context.Context, workspaceID string, issue *models.Issue, update store.IssueUpdate) error {
	rules, err := s.Store.ResolveFieldBehaviour(ctx, workspaceID, issue.ProjectID, issue.IssueType.ID)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	intents := issueWriteIntents(update)
	for field, rule := range rules {
		// Summary is required by the command path itself, which already
		// rejects an empty one, and the context fields cannot be edited.
		if field == "summary" {
			continue
		}
		intent := intents[field]
		if !intent.touched {
			continue
		}
		if rule.IsHidden && intent.hasValue {
			return fmt.Errorf("%s is hidden by the field configuration for this work type", field)
		}
		if rule.IsRequired && !intent.hasValue {
			return fmt.Errorf("%s is required by the field configuration for this work type", field)
		}
	}
	return nil
}

// enforceCustomFieldContexts rejects a write that sets a custom field the
// project and work type's context does not reach. Without it a context would
// govern which fields a form offers while REST clients wrote past it, which is
// the gap field configurations already close for hidden fields.
func (s *Service) enforceCustomFieldContexts(ctx context.Context, workspaceID, projectID, issueTypeID string, fields map[string]json.RawMessage) error {
	touched := []string{}
	for field, raw := range fields {
		if suppliedFieldValue(raw) {
			touched = append(touched, field)
		}
	}
	if len(touched) == 0 {
		return nil
	}
	known, applicable, err := s.Store.CustomFieldWriteScope(ctx, workspaceID, projectID, issueTypeID)
	if err != nil {
		return err
	}
	sort.Strings(touched)
	for _, field := range touched {
		if known[field] && !applicable[field] {
			return fmt.Errorf("%s is not available for this project and work type", field)
		}
	}
	options, err := s.Store.CustomFieldOptionScope(ctx, workspaceID, projectID, issueTypeID)
	if err != nil {
		return err
	}
	for _, field := range touched {
		choices, isSelect := options[field]
		if !isSelect {
			continue
		}
		var values []string
		var value string
		var cascade struct {
			Parent string `json:"parent"`
			Child  string `json:"child"`
		}
		if err := json.Unmarshal(fields[field], &value); err == nil {
			values = []string{value}
		} else if err := json.Unmarshal(fields[field], &values); err == nil {
		} else if err := json.Unmarshal(fields[field], &cascade); err == nil && cascade.Parent != "" {
			values = []string{cascade.Parent}
			if cascade.Child != "" {
				values = append(values, cascade.Child)
			}
		} else {
			return fmt.Errorf("%s must be an option id", field)
		}
		for _, value := range values {
			if !choices[value] {
				return fmt.Errorf("%s does not offer option %q here", field, value)
			}
		}
	}
	return nil
}

// normalizeOptionFields turns the value forms Jira accepts for custom fields
// into what a work item stores: option ids for select and multi-select fields
// (an id, {"id"} or {"value"}), {"parent","child"} option ids for a cascading
// select, account ids for user pickers ({"accountId"}), group ids for group
// pickers ({"groupId"} or {"name"}), and lists for the multi-value fields, where
// a single value is taken as a list of one.
func (s *Service) normalizeOptionFields(ctx context.Context, workspaceID, projectID, issueTypeID string, fields map[string]json.RawMessage) error {
	if len(fields) == 0 {
		return nil
	}
	definitions, err := s.Store.CustomFieldsForProject(ctx, projectID)
	if err != nil {
		return err
	}
	types := map[string]string{}
	for _, definition := range definitions {
		types[definition.ID] = definition.Type
	}
	var catalog map[string]store.OptionCatalog
	loadCatalog := func() (map[string]store.OptionCatalog, error) {
		if catalog == nil {
			loaded, err := s.Store.CustomFieldOptionCatalog(ctx, workspaceID, projectID, issueTypeID)
			if err != nil {
				return nil, err
			}
			catalog = loaded
		}
		return catalog, nil
	}
	list := func(raw json.RawMessage) []json.RawMessage {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			items = []json.RawMessage{raw}
		}
		return items
	}
	scalar := func(raw json.RawMessage, keys ...string) (string, string, bool) {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return text, "", true
		}
		var number json.Number
		if json.Unmarshal(raw, &number) == nil {
			return number.String(), "", true
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil {
			return "", "", false
		}
		for _, key := range keys {
			if value, ok := object[key]; ok && string(value) != "null" {
				return strings.Trim(string(value), `"`), key, true
			}
		}
		return "", "", false
	}
	for field, raw := range fields {
		fieldType, known := types[field]
		if !known || !suppliedFieldValue(raw) {
			continue
		}
		switch fieldType {
		case models.CustomFieldSelect, models.CustomFieldMultiSelect:
			catalog, err := loadCatalog()
			if err != nil {
				return err
			}
			options := catalog[field]
			resolve := func(item json.RawMessage) (string, error) {
				value, key, ok := scalar(item, "id", "value")
				if !ok {
					return "", fmt.Errorf("%s must be an option id, {\"id\"} or {\"value\"}", field)
				}
				if key != "value" {
					return value, nil
				}
				if id, found := options.ByValue[value]; found {
					return id, nil
				}
				return "", fmt.Errorf("%s does not offer the option %q here", field, value)
			}
			if fieldType == models.CustomFieldSelect {
				id, err := resolve(raw)
				if err != nil {
					return err
				}
				fields[field], _ = json.Marshal(id)
				continue
			}
			ids := []string{}
			for _, item := range list(raw) {
				id, err := resolve(item)
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			fields[field], _ = json.Marshal(ids)
		case models.CustomFieldCascadingSelect:
			catalog, err := loadCatalog()
			if err != nil {
				return err
			}
			options := catalog[field]
			var object struct {
				Parent json.RawMessage `json:"parent"`
				ID     json.RawMessage `json:"id"`
				Value  *string         `json:"value"`
				Child  json.RawMessage `json:"child"`
			}
			if json.Unmarshal(raw, &object) != nil {
				return fmt.Errorf("%s must be an option with an optional child option", field)
			}
			parent := strings.Trim(string(object.Parent), `"`)
			if parent == "" {
				parent = strings.Trim(string(object.ID), `"`)
			}
			if parent == "" && object.Value != nil {
				parent = options.ByValue[*object.Value]
				if parent == "" {
					return fmt.Errorf("%s does not offer the option %q here", field, *object.Value)
				}
			}
			if parent == "" {
				return fmt.Errorf("%s must name its option by id or value", field)
			}
			stored := map[string]string{"parent": parent}
			if len(object.Child) > 0 && string(object.Child) != "null" {
				child, key, ok := scalar(object.Child, "id", "value")
				if !ok {
					return fmt.Errorf("%s has a child option that is not an id, {\"id\"} or {\"value\"}", field)
				}
				if key == "value" {
					child = options.Children[parent][child]
					if child == "" {
						return fmt.Errorf("%s does not offer that child option under the chosen option", field)
					}
				}
				stored["child"] = child
			}
			fields[field], _ = json.Marshal(stored)
		case models.CustomFieldUser, models.CustomFieldMultiUser:
			resolve := func(item json.RawMessage) (string, error) {
				accountID, _, ok := scalar(item, "accountId")
				if !ok || accountID == "" {
					return "", fmt.Errorf("%s must name a user by accountId", field)
				}
				if _, err := s.Store.MemberByID(ctx, workspaceID, accountID); err != nil {
					return "", fmt.Errorf("%s names %q, who is not a user of this site", field, accountID)
				}
				return accountID, nil
			}
			if fieldType == models.CustomFieldUser {
				accountID, err := resolve(raw)
				if err != nil {
					return err
				}
				fields[field], _ = json.Marshal(accountID)
				continue
			}
			ids := []string{}
			for _, item := range list(raw) {
				accountID, err := resolve(item)
				if err != nil {
					return err
				}
				ids = append(ids, accountID)
			}
			fields[field], _ = json.Marshal(ids)
		case models.CustomFieldGroup, models.CustomFieldMultiGroup:
			resolve := func(item json.RawMessage) (string, error) {
				value, key, ok := scalar(item, "groupId", "name")
				if !ok || value == "" {
					return "", fmt.Errorf("%s must name a group by groupId or name", field)
				}
				groupID, groupName := value, ""
				if key == "name" {
					groupID, groupName = "", value
				}
				group, err := s.Store.SiteGroupByIDOrName(ctx, workspaceID, groupID, groupName)
				if err != nil && key == "" {
					group, err = s.Store.SiteGroupByIDOrName(ctx, workspaceID, "", value)
				}
				if err != nil {
					return "", fmt.Errorf("%s names the group %q, which does not exist", field, value)
				}
				return group.ID, nil
			}
			if fieldType == models.CustomFieldGroup {
				groupID, err := resolve(raw)
				if err != nil {
					return err
				}
				fields[field], _ = json.Marshal(groupID)
				continue
			}
			ids := []string{}
			for _, item := range list(raw) {
				groupID, err := resolve(item)
				if err != nil {
					return err
				}
				ids = append(ids, groupID)
			}
			fields[field], _ = json.Marshal(ids)
		case models.CustomFieldProject:
			ref, _, ok := scalar(raw, "id", "key")
			if !ok || ref == "" {
				return fmt.Errorf("%s must name a project by id or key", field)
			}
			project, err := s.Store.ProjectByIDOrKey(ctx, workspaceID, ref)
			if err != nil {
				return fmt.Errorf("%s names the project %q, which does not exist", field, ref)
			}
			fields[field], _ = json.Marshal(project.ID)
		case models.CustomFieldVersion, models.CustomFieldMultiVersion:
			var versions []*models.Version
			resolve := func(item json.RawMessage) (string, error) {
				ref, key, ok := scalar(item, "id", "name")
				if !ok || ref == "" {
					return "", fmt.Errorf("%s must name a version by id or name", field)
				}
				if versions == nil {
					loaded, err := s.Store.ProjectVersions(ctx, projectID)
					if err != nil {
						return "", err
					}
					versions = loaded
				}
				for _, version := range versions {
					if (key == "name" && version.Name == ref) || (key != "name" && version.ID == ref) {
						return version.ID, nil
					}
				}
				return "", fmt.Errorf("%s names the version %q, which is not a version of this project", field, ref)
			}
			if fieldType == models.CustomFieldVersion {
				id, err := resolve(raw)
				if err != nil {
					return err
				}
				fields[field], _ = json.Marshal(id)
				continue
			}
			ids := []string{}
			for _, item := range list(raw) {
				id, err := resolve(item)
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			fields[field], _ = json.Marshal(ids)
		case models.CustomFieldLabels:
			labels := []string{}
			for _, item := range list(raw) {
				var label string
				if json.Unmarshal(item, &label) != nil {
					return fmt.Errorf("%s must be a list of labels", field)
				}
				labels = append(labels, label)
			}
			fields[field], _ = json.Marshal(labels)
		}
	}
	return nil
}

// createdFromInitialTransition is a new work item's values once the initial
// transition's post functions have run.
type createdFromInitialTransition struct {
	summary, assigneeID, priorityID string
	description                     json.RawMessage
	labels                          []string
	fields                          map[string]json.RawMessage
	triggers                        store.IssueUpdate
}

// runInitialTransition applies the workflow's initial transition to a work
// item about to be created: its validators see the submitted values, and its
// assignee, field update and field copy post functions change them, as Jira's
// Create transition does. Webhook and agent triggers are returned to run once
// the work item exists.
func (s *Service) runInitialTransition(ctx context.Context, in CreateIssueInput, initial workflow.Transition, parentID string, description json.RawMessage, priorityID string, labels []string) (createdFromInitialTransition, error) {
	created := createdFromInitialTransition{summary: in.Summary, assigneeID: in.AssigneeID, priorityID: priorityID, description: description, labels: labels, fields: in.Fields}
	draft := &models.Issue{Summary: in.Summary, Description: description, Labels: labels, Fields: in.Fields}
	if in.AssigneeID != "" {
		draft.Assignee = &models.User{ID: in.AssigneeID}
	}
	reporterID := in.ReporterID
	if reporterID == "" {
		reporterID = in.ActorID
	}
	draft.Reporter = &models.User{ID: reporterID}
	if priorityID != "" {
		draft.Priority = &models.Priority{ID: priorityID}
	}
	context := workflow.ContextForIssue(in.ActorID, draft)
	permissions, err := authz.JiraPermissions(ctx, s.Store, in.WorkspaceID, in.ActorID)
	if err != nil {
		return created, err
	}
	context.Permissions = permissions
	if err = initial.ValidateRules(context); err != nil {
		return created, err
	}
	var update store.IssueUpdate
	assigneeID, changeAssignee, err := initial.AssigneeEffect(context)
	if err != nil {
		return created, err
	}
	if changeAssignee {
		update.AssigneeID = &assigneeID
	}
	effects, err := initial.FieldUpdateEffects()
	if err != nil {
		return created, err
	}
	for _, effect := range effects {
		switch {
		case effect.SourceField == "":
			err = applyWorkflowFieldUpdate(draft, &update, effect)
		case effect.IssueSource == "PARENT":
			if parentID == "" {
				return created, fmt.Errorf("copy-field parent source requires a parent issue")
			}
			parent, parentErr := s.Store.IssueByIDOrKey(ctx, in.WorkspaceID, parentID)
			if parentErr != nil {
				return created, fmt.Errorf("copy-field parent source is unavailable")
			}
			err = applyWorkflowFieldCopyFrom(parent, &store.IssueUpdate{}, &update, effect.SourceField, effect.Field)
		default:
			err = applyWorkflowFieldCopyFrom(draft, &update, &update, effect.SourceField, effect.Field)
		}
		if err != nil {
			return created, err
		}
	}
	if update.AssigneeID != nil {
		if *update.AssigneeID != "" {
			if _, err := s.Store.MemberByID(ctx, in.WorkspaceID, *update.AssigneeID); err != nil {
				return created, fmt.Errorf("workflow assignee is not an active workspace member")
			}
		}
		created.assigneeID = *update.AssigneeID
	}
	if update.Summary != nil {
		if len(*update.Summary) == 0 || len(*update.Summary) > 255 {
			return created, fmt.Errorf("workflow summary update must be between 1 and 255 characters")
		}
		created.summary = *update.Summary
	}
	if update.Description != nil {
		created.description = update.Description
	}
	if update.Labels != nil {
		created.labels = *update.Labels
	}
	if update.PriorityID != nil {
		created.priorityID = *update.PriorityID
	}
	if len(update.Fields) > 0 {
		fields := make(map[string]json.RawMessage, len(created.fields)+len(update.Fields))
		for field, value := range created.fields {
			fields[field] = value
		}
		for field, value := range update.Fields {
			fields[field] = value
		}
		created.fields = fields
	}
	if created.triggers.TriggeredWebhookIDs, err = initial.TriggerWebhookIDs(); err != nil {
		return created, err
	}
	for _, agent := range initial.AgentTriggers() {
		created.triggers.TriggeredAgents = append(created.triggers.TriggeredAgents, models.WorkflowAgentTrigger{AgentID: agent.AgentID, Prompt: agent.Prompt})
	}
	return created, nil
}
