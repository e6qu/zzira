package api3

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

// planError answers a plan failure with Jira's ErrorCollection and status.
func planError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal error"
	switch {
	case errors.Is(err, store.ErrPlanValidation):
		status, message = http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrPlanValidation.Error()+": ")
	case errors.Is(err, store.ErrPlanNotFound):
		status, message = http.StatusNotFound, "The plan or team was not found."
	case errors.Is(err, store.ErrPlanNotActive):
		status, message = http.StatusConflict, "The plan is not active."
	default:
		log.Printf("api3: plans: %v", err)
	}
	writeJSON(w, status, map[string]any{"errorMessages": []string{message}, "errors": map[string]string{}, "status": status})
}

func planBadRequest(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"errorMessages": []string{message}, "errors": map[string]string{}, "status": http.StatusBadRequest})
}

// planCursor pages by the last id returned, encoded opaquely.
func planCursor(w http.ResponseWriter, r *http.Request) (int64, int, bool) {
	query := r.URL.Query()
	after := int64(0)
	if raw := query.Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err == nil {
			after, err = strconv.ParseInt(string(decoded), 10, 64)
		}
		if err != nil || after < 0 {
			planBadRequest(w, "The cursor is not valid.")
			return 0, 0, false
		}
	}
	maxResults := 50
	if raw := query.Get("maxResults"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			planBadRequest(w, "maxResults must be a positive integer.")
			return 0, 0, false
		}
		maxResults = min(parsed, 50)
	}
	return after, maxResults, true
}

func encodePlanCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func cursorPage(r *http.Request, values any, size, total int, lastID int64, more bool) map[string]any {
	page := map[string]any{"cursor": r.URL.Query().Get("cursor"), "last": !more, "size": size, "total": total, "values": values}
	if more {
		page["nextPageCursor"] = encodePlanCursor(lastID)
	}
	return page
}

func planQueryBool(w http.ResponseWriter, r *http.Request, name string) (bool, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return false, true
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		planBadRequest(w, name+" must be true or false.")
		return false, false
	}
	return value, true
}

// planRequest is the create, and after a patch the full, plan document.
type planRequest struct {
	Name                 string                    `json:"name"`
	LeadAccountID        string                    `json:"leadAccountId"`
	Scheduling           *store.PlanScheduling     `json:"scheduling"`
	ExclusionRules       *store.PlanExclusionRules `json:"exclusionRules"`
	IssueSources         []store.PlanIssueSource   `json:"issueSources"`
	CustomFields         []store.PlanCustomField   `json:"customFields"`
	CrossProjectReleases []store.PlanRelease       `json:"crossProjectReleases"`
	Permissions          []struct {
		Type   string `json:"type"`
		Holder struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"holder"`
	} `json:"permissions"`
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// planFromRequest resolves group holders by name, or by id with useGroupId.
func (h *Handler) planFromRequest(r *http.Request, workspaceID string, request planRequest, useGroupID bool) (store.Plan, error) {
	if request.Scheduling == nil || request.IssueSources == nil {
		return store.Plan{}, fmt.Errorf("%w: The name, issueSources and scheduling are required.", store.ErrPlanValidation)
	}
	plan := store.Plan{
		Name: request.Name, LeadAccountID: request.LeadAccountID, Scheduling: *request.Scheduling, IssueSources: request.IssueSources,
		CustomFields: request.CustomFields, CrossProjectReleases: request.CrossProjectReleases,
	}
	if request.ExclusionRules != nil {
		plan.ExclusionRules = *request.ExclusionRules
	} else {
		plan.ExclusionRules.NumberOfDaysToShowCompletedIssues = 30
	}
	for _, permission := range request.Permissions {
		holder := permission.Holder.Value
		if permission.Holder.Type == "Group" {
			groupID, groupName := "", holder
			if useGroupID {
				groupID, groupName = holder, ""
			}
			group, err := h.Store.SiteGroupByIDOrName(r.Context(), workspaceID, groupID, groupName)
			if err != nil {
				return store.Plan{}, fmt.Errorf("%w: The group %s does not exist.", store.ErrPlanValidation, holder)
			}
			holder = group.ID
		}
		plan.Permissions = append(plan.Permissions, store.PlanPermission{Type: permission.Type, HolderType: permission.Holder.Type, Holder: holder})
	}
	return plan, nil
}

func (h *Handler) planDocument(r *http.Request, workspaceID string, plan store.Plan, useGroupID bool) map[string]any {
	sources := make([]map[string]any, 0, len(plan.IssueSources))
	for _, source := range plan.IssueSources {
		sources = append(sources, map[string]any{"type": source.Type, "value": source.Value})
	}
	names := map[string]string{}
	if groups, err := h.Store.SiteGroups(r.Context(), workspaceID); err == nil {
		for _, group := range groups {
			names[group.ID] = group.Name
		}
	}
	permissions := make([]map[string]any, 0, len(plan.Permissions))
	for _, permission := range plan.Permissions {
		value := permission.Holder
		if permission.HolderType == "Group" && !useGroupID {
			value = names[permission.Holder]
		}
		permissions = append(permissions, map[string]any{"type": permission.Type, "holder": map[string]string{"type": permission.HolderType, "value": value}})
	}
	document := map[string]any{
		"id": plan.ID, "name": plan.Name, "status": plan.Status, "scheduling": plan.Scheduling, "exclusionRules": plan.ExclusionRules,
		"issueSources": sources, "customFields": plan.CustomFields, "crossProjectReleases": plan.CrossProjectReleases, "permissions": permissions,
		"lastSaved": plan.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if plan.LeadAccountID != "" {
		document["leadAccountId"] = plan.LeadAccountID
	}
	return document
}

// plansRoute serves /rest/api/3/plans/plan and everything below it.
func (h *Handler) plansRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, userID, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJSON(w, e.status, map[string]any{"errorMessages": []string{e.message}, "errors": map[string]string{}, "status": e.status})
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/plans/plan"), "/"), "/")
	if parts[0] == "" {
		parts = nil
	}
	useGroupID, ok := planQueryBool(w, r, "useGroupId")
	if !ok {
		return
	}
	if len(parts) == 0 {
		switch r.Method {
		case http.MethodGet:
			h.listPlans(w, r, workspaceID)
		case http.MethodPost:
			h.createPlan(w, r, workspaceID, userID, useGroupID)
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	planID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		planError(w, store.ErrPlanNotFound)
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		plan, err := h.Store.Plan(r.Context(), workspaceID, planID)
		if err != nil {
			planError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.planDocument(r, workspaceID, plan, useGroupID))
	case len(parts) == 1 && r.Method == http.MethodPut:
		h.patchPlan(w, r, workspaceID, planID, useGroupID)
	case len(parts) == 2 && (parts[1] == "archive" || parts[1] == "trash") && r.Method == http.MethodPut:
		status := "Archived"
		if parts[1] == "trash" {
			status = "Trashed"
		}
		if err := h.Store.SetPlanStatus(r.Context(), workspaceID, planID, status); err != nil {
			planError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 2 && parts[1] == "duplicate" && r.Method == http.MethodPost:
		var request struct {
			Name string `json:"name"`
		}
		body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if decodeStrict(body, &request) != nil {
			planBadRequest(w, "The request body is not valid.")
			return
		}
		id, err := h.Store.DuplicatePlan(r.Context(), workspaceID, userID, planID, request.Name)
		if err != nil {
			planError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, id)
	case len(parts) >= 2 && parts[1] == "team":
		h.planTeamsRoute(w, r, workspaceID, planID, parts[2:])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) listPlans(w http.ResponseWriter, r *http.Request, workspaceID string) {
	includeTrashed, ok := planQueryBool(w, r, "includeTrashed")
	if !ok {
		return
	}
	includeArchived, ok := planQueryBool(w, r, "includeArchived")
	if !ok {
		return
	}
	after, maxResults, ok := planCursor(w, r)
	if !ok {
		return
	}
	plans, total, err := h.Store.Plans(r.Context(), workspaceID, includeTrashed, includeArchived, after, maxResults+1)
	if err != nil {
		planError(w, err)
		return
	}
	more := len(plans) > maxResults
	if more {
		plans = plans[:maxResults]
	}
	values := make([]map[string]any, 0, len(plans))
	lastID := int64(0)
	for _, plan := range plans {
		sources := make([]map[string]any, 0, len(plan.IssueSources))
		for _, source := range plan.IssueSources {
			sources = append(sources, map[string]any{"type": source.Type, "value": source.Value})
		}
		values = append(values, map[string]any{"id": strconv.FormatInt(plan.ID, 10), "name": plan.Name, "status": plan.Status, "scenarioId": strconv.FormatInt(plan.ScenarioID, 10), "issueSources": sources})
		lastID = plan.ID
	}
	writeJSON(w, http.StatusOK, cursorPage(r, values, len(values), total, lastID, more))
}

func (h *Handler) createPlan(w http.ResponseWriter, r *http.Request, workspaceID, userID string, useGroupID bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	var request planRequest
	if err != nil || decodeStrict(body, &request) != nil {
		planBadRequest(w, "The request body is not valid.")
		return
	}
	plan, err := h.planFromRequest(r, workspaceID, request, useGroupID)
	if err != nil {
		planError(w, err)
		return
	}
	id, err := h.Store.CreatePlan(r.Context(), workspaceID, userID, plan)
	if err != nil {
		planError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, id)
}

// patchPlan applies a JSON Patch to the plan's document and saves the result
// as a whole, so every patched value is validated like a new plan.
func (h *Handler) patchPlan(w http.ResponseWriter, r *http.Request, workspaceID string, planID int64, useGroupID bool) {
	current, err := h.Store.Plan(r.Context(), workspaceID, planID)
	if err != nil {
		planError(w, err)
		return
	}
	if current.Status != "Active" {
		planError(w, store.ErrPlanNotActive)
		return
	}
	patch, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		planBadRequest(w, "The request body is not valid.")
		return
	}
	document := h.planDocument(r, workspaceID, current, useGroupID)
	for _, readOnly := range []string{"id", "status", "lastSaved"} {
		delete(document, readOnly)
	}
	raw, _ := json.Marshal(document)
	patched, err := applyJSONPatch(raw, patch)
	if err != nil {
		planBadRequest(w, err.Error())
		return
	}
	var request planRequest
	if err := decodeStrict(patched, &request); err != nil {
		planBadRequest(w, "The patched plan is not valid: "+err.Error())
		return
	}
	plan, err := h.planFromRequest(r, workspaceID, request, useGroupID)
	if err != nil {
		planError(w, err)
		return
	}
	plan.ID = planID
	if err := h.Store.UpdatePlan(r.Context(), workspaceID, plan); err != nil {
		planError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- plan teams ----

type planTeamRequest struct {
	ID               string   `json:"id,omitempty"`
	Name             string   `json:"name,omitempty"`
	PlanningStyle    string   `json:"planningStyle"`
	IssueSourceID    *int64   `json:"issueSourceId,omitempty"`
	SprintLength     *int64   `json:"sprintLength,omitempty"`
	Capacity         *float64 `json:"capacity,omitempty"`
	MemberAccountIDs []string `json:"memberAccountIds,omitempty"`
}

func planTeamDocument(team store.PlanTeam) map[string]any {
	document := map[string]any{"planningStyle": team.PlanningStyle}
	if team.AtlassianTeamID != "" {
		document["id"] = team.AtlassianTeamID
	} else {
		document["id"] = team.ID
		document["name"] = team.Name
		members := team.MemberIDs
		if members == nil {
			members = []string{}
		}
		document["memberAccountIds"] = members
	}
	if team.IssueSourceID != nil {
		document["issueSourceId"] = *team.IssueSourceID
	}
	if team.SprintLength != nil {
		document["sprintLength"] = *team.SprintLength
	}
	if team.Capacity != nil {
		document["capacity"] = *team.Capacity
	}
	return document
}

func (h *Handler) planTeamsRoute(w http.ResponseWriter, r *http.Request, workspaceID string, planID int64, parts []string) {
	if len(parts) == 0 {
		if r.Method != http.MethodGet {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		after, maxResults, ok := planCursor(w, r)
		if !ok {
			return
		}
		teams, total, err := h.Store.PlanTeams(r.Context(), workspaceID, planID, after, maxResults+1)
		if err != nil {
			planError(w, err)
			return
		}
		more := len(teams) > maxResults
		if more {
			teams = teams[:maxResults]
		}
		values := make([]map[string]any, 0, len(teams))
		lastID := int64(0)
		for _, team := range teams {
			if team.AtlassianTeamID != "" {
				values = append(values, map[string]any{"id": team.AtlassianTeamID, "type": "Atlassian"})
			} else {
				values = append(values, map[string]any{"id": strconv.FormatInt(team.ID, 10), "type": "PlanOnly", "name": team.Name})
			}
			lastID = team.ID
		}
		writeJSON(w, http.StatusOK, cursorPage(r, values, len(values), total, lastID, more))
		return
	}
	kind := parts[0]
	if kind != "atlassian" && kind != "planonly" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodPost {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		var request planTeamRequest
		if err != nil || decodeStrict(body, &request) != nil {
			planBadRequest(w, "The request body is not valid.")
			return
		}
		team := store.PlanTeam{PlanningStyle: request.PlanningStyle, IssueSourceID: request.IssueSourceID, SprintLength: request.SprintLength, Capacity: request.Capacity}
		if kind == "atlassian" {
			if request.ID == "" || request.Name != "" || request.MemberAccountIDs != nil {
				planBadRequest(w, "An Atlassian team is added by its id, without a name or members.")
				return
			}
			team.AtlassianTeamID = request.ID
		} else {
			if request.ID != "" {
				planBadRequest(w, "A plan-only team is given a name, not an id.")
				return
			}
			team.Name, team.MemberIDs = request.Name, request.MemberAccountIDs
		}
		id, err := h.Store.SavePlanTeam(r.Context(), workspaceID, planID, team, true)
		if err != nil {
			planError(w, err)
			return
		}
		if kind == "atlassian" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusCreated, id)
		return
	}
	atlassianTeamID, planOnlyID := "", int64(0)
	if kind == "atlassian" {
		atlassianTeamID = parts[1]
	} else {
		parsed, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			planError(w, store.ErrPlanNotFound)
			return
		}
		planOnlyID = parsed
	}
	if len(parts) != 2 {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	team, err := h.Store.PlanTeam(r.Context(), workspaceID, planID, atlassianTeamID, planOnlyID)
	if err != nil {
		planError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, planTeamDocument(team))
	case http.MethodDelete:
		if err := h.Store.DeletePlanTeam(r.Context(), workspaceID, planID, team.ID); err != nil {
			planError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPut:
		patch, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			planBadRequest(w, "The request body is not valid.")
			return
		}
		document := planTeamDocument(team)
		delete(document, "id")
		raw, _ := json.Marshal(document)
		patched, err := applyJSONPatch(raw, patch)
		if err != nil {
			planBadRequest(w, err.Error())
			return
		}
		var request planTeamRequest
		if err := decodeStrict(patched, &request); err != nil || request.ID != "" || (kind == "atlassian" && (request.Name != "" || request.MemberAccountIDs != nil)) {
			planBadRequest(w, "Only the documented planning settings of the team can be changed.")
			return
		}
		team.PlanningStyle, team.IssueSourceID, team.SprintLength, team.Capacity = request.PlanningStyle, request.IssueSourceID, request.SprintLength, request.Capacity
		if kind == "planonly" {
			team.Name, team.MemberIDs = request.Name, request.MemberAccountIDs
		}
		if _, err := h.Store.SavePlanTeam(r.Context(), workspaceID, planID, team, false); err != nil {
			planError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
