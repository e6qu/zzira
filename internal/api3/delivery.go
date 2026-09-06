package api3

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

type softwareAssociation struct {
	AssociationType string   `json:"associationType"`
	Values          []string `json:"values"`
}

type softwareBulkRequest struct {
	Properties  map[string]string `json:"properties"`
	Builds      []json.RawMessage `json:"builds"`
	Deployments []json.RawMessage `json:"deployments"`
}

type rejectedSoftwareItem struct {
	Key    map[string]any      `json:"key"`
	Errors []map[string]string `json:"errors"`
}

func (h *Handler) softwareDeliveryRoute(w http.ResponseWriter, r *http.Request, kind string) {
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	path := r.URL.Path
	directPrefix := "/rest/" + kind + "/0.1"
	centralPrefix := "/jira/" + kind + "/0.1/cloud/"
	if strings.HasPrefix(path, centralPrefix) {
		parts := strings.Split(strings.TrimPrefix(path, centralPrefix), "/")
		if len(parts) < 2 || !h.validDevelopmentCloudID(r, workspaceID, parts[0]) {
			jiraError(w, http.StatusNotFound, "Cloud site was not found.")
			return
		}
		path = "/" + strings.Join(parts[1:], "/")
	} else {
		path = strings.TrimPrefix(path, directPrefix)
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if kind == "builds" {
		h.buildRoute(w, r, workspaceID, parts)
		return
	}
	h.deploymentRoute(w, r, workspaceID, parts)
}

func (h *Handler) buildRoute(w http.ResponseWriter, r *http.Request, workspaceID string, parts []string) {
	switch {
	case len(parts) == 1 && parts[0] == "bulk" && r.Method == http.MethodPost:
		h.submitBuilds(w, r, workspaceID)
	case len(parts) == 1 && parts[0] == "bulkByProperties" && r.Method == http.MethodDelete:
		properties, sequence, err := softwarePropertiesFromQuery(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.Store.DeleteSoftwareBuildsByProperties(r.Context(), workspaceID, properties, sequence); err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not delete builds.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case len(parts) == 4 && parts[0] == "pipelines" && parts[2] == "builds":
		buildNumber, err := softwareSequence(parts[3], "buildNumber")
		if err != nil || !validSoftwareKey(parts[1]) {
			jiraError(w, http.StatusBadRequest, "A valid pipelineId and buildNumber are required.")
			return
		}
		if r.Method == http.MethodGet {
			payload, err := h.Store.SoftwareBuild(r.Context(), workspaceID, parts[1], buildNumber)
			if err != nil {
				jiraError(w, http.StatusNotFound, "Build was not found.")
				return
			}
			writeRawJSON(w, payload)
			return
		}
		if r.Method == http.MethodDelete {
			sequence, err := softwareUpdateSequence(r)
			if err != nil {
				jiraError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := h.Store.DeleteSoftwareBuild(r.Context(), workspaceID, parts[1], buildNumber, sequence); err != nil {
				jiraError(w, http.StatusInternalServerError, "Could not delete build.")
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		fallthrough
	default:
		jiraError(w, http.StatusNotFound, "Build resource does not exist.")
	}
}

func (h *Handler) deploymentRoute(w http.ResponseWriter, r *http.Request, workspaceID string, parts []string) {
	switch {
	case len(parts) == 1 && parts[0] == "bulk" && r.Method == http.MethodPost:
		h.submitDeployments(w, r, workspaceID)
	case len(parts) == 1 && parts[0] == "bulkByProperties" && r.Method == http.MethodDelete:
		properties, sequence, err := softwarePropertiesFromQuery(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.Store.DeleteSoftwareDeploymentsByProperties(r.Context(), workspaceID, properties, sequence); err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not delete deployments.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case (len(parts) == 6 || len(parts) == 7) && parts[0] == "pipelines" && parts[2] == "environments" && parts[4] == "deployments":
		deploymentSequence, err := softwareSequence(parts[5], "deploymentSequenceNumber")
		if err != nil || !validSoftwareKey(parts[1]) || !validSoftwareKey(parts[3]) {
			jiraError(w, http.StatusBadRequest, "A valid pipelineId, environmentId, and deploymentSequenceNumber are required.")
			return
		}
		deployment, err := h.Store.SoftwareDeployment(r.Context(), workspaceID, parts[1], parts[3], deploymentSequence)
		if len(parts) == 7 && parts[6] == "gating-status" && r.Method == http.MethodGet {
			if err != nil {
				jiraError(w, http.StatusNotFound, "Deployment was not found.")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"deploymentSequenceNumber": deployment.DeploymentSequenceNumber,
				"pipelineId":               deployment.PipelineID, "environmentId": deployment.EnvironmentID,
				"updatedTimestamp": deployment.LastUpdated.UTC().Format(time.RFC3339),
				"gatingStatus":     "allowed", "details": []any{},
			})
			return
		}
		if len(parts) != 6 {
			jiraError(w, http.StatusNotFound, "Deployment resource does not exist.")
			return
		}
		if r.Method == http.MethodGet {
			if err != nil {
				jiraError(w, http.StatusNotFound, "Deployment was not found.")
				return
			}
			writeRawJSON(w, deployment.Payload)
			return
		}
		if r.Method == http.MethodDelete {
			sequence, parseErr := softwareUpdateSequence(r)
			if parseErr != nil {
				jiraError(w, http.StatusBadRequest, parseErr.Error())
				return
			}
			if err := h.Store.DeleteSoftwareDeployment(r.Context(), workspaceID, parts[1], parts[3], deploymentSequence, sequence); err != nil {
				jiraError(w, http.StatusInternalServerError, "Could not delete deployment.")
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		fallthrough
	default:
		jiraError(w, http.StatusNotFound, "Deployment resource does not exist.")
	}
}

func (h *Handler) submitBuilds(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var request softwareBulkRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20))
	if err := decoder.Decode(&request); err != nil || len(request.Builds) == 0 || len(request.Builds) > 100 {
		jiraError(w, http.StatusBadRequest, "Between one and 100 builds are required.")
		return
	}
	properties, err := validateDevelopmentProperties(request.Properties)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	accepted := make([]map[string]any, 0, len(request.Builds))
	rejected := make([]rejectedSoftwareItem, 0)
	builds := make([]models.SoftwareBuild, 0, len(request.Builds))
	unknownKeys, unknownRefs := map[string]bool{}, map[string]bool{}
	seen := map[string]bool{}
	for _, raw := range request.Builds {
		build, refs, parseErr := parseSoftwareBuild(raw)
		key := map[string]any{"pipelineId": build.PipelineID, "buildNumber": build.BuildNumber}
		composite := build.PipelineID + "\x00" + strconv.FormatInt(build.BuildNumber, 10)
		if parseErr == nil && seen[composite] {
			parseErr = &developmentInputError{"Build keys must be unique within a request."}
		}
		if parseErr != nil {
			rejected = append(rejected, rejectedSoftwareItem{Key: key, Errors: []map[string]string{{"message": parseErr.Error()}}})
			continue
		}
		seen[composite] = true
		build.IssueKeys, refs = h.resolveSoftwareIssueRefs(r, workspaceID, refs, unknownKeys, unknownRefs)
		if len(refs) > 0 && len(build.IssueKeys) == 0 {
			rejected = append(rejected, rejectedSoftwareItem{Key: key, Errors: []map[string]string{{"message": "Build is associated only with unknown work items."}}})
			continue
		}
		build.Properties = properties
		builds = append(builds, build)
		accepted = append(accepted, key)
	}
	if err := h.Store.UpsertSoftwareBuilds(r.Context(), workspaceID, builds); err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not store builds.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"acceptedBuilds": accepted, "rejectedBuilds": rejected,
		"unknownIssueKeys": sortedSoftwareKeys(unknownKeys), "unknownAssociations": unknownAssociations(unknownRefs),
	})
}

func (h *Handler) submitDeployments(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var request softwareBulkRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20))
	if err := decoder.Decode(&request); err != nil || len(request.Deployments) == 0 || len(request.Deployments) > 100 {
		jiraError(w, http.StatusBadRequest, "Between one and 100 deployments are required.")
		return
	}
	properties, err := validateDevelopmentProperties(request.Properties)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	accepted := make([]map[string]any, 0, len(request.Deployments))
	rejected := make([]rejectedSoftwareItem, 0)
	deployments := make([]models.SoftwareDeployment, 0, len(request.Deployments))
	unknownKeys, unknownRefs := map[string]bool{}, map[string]bool{}
	seen := map[string]bool{}
	for _, raw := range request.Deployments {
		deployment, refs, parseErr := parseSoftwareDeployment(raw)
		key := map[string]any{"pipelineId": deployment.PipelineID, "environmentId": deployment.EnvironmentID, "deploymentSequenceNumber": deployment.DeploymentSequenceNumber}
		composite := deployment.PipelineID + "\x00" + deployment.EnvironmentID + "\x00" + strconv.FormatInt(deployment.DeploymentSequenceNumber, 10)
		if parseErr == nil && seen[composite] {
			parseErr = &developmentInputError{"Deployment keys must be unique within a request."}
		}
		if parseErr != nil {
			rejected = append(rejected, rejectedSoftwareItem{Key: key, Errors: []map[string]string{{"message": parseErr.Error()}}})
			continue
		}
		seen[composite] = true
		deployment.IssueKeys, refs = h.resolveSoftwareIssueRefs(r, workspaceID, refs, unknownKeys, unknownRefs)
		if len(refs) > 0 && len(deployment.IssueKeys) == 0 {
			rejected = append(rejected, rejectedSoftwareItem{Key: key, Errors: []map[string]string{{"message": "Deployment is associated only with unknown work items."}}})
			continue
		}
		deployment.Properties = properties
		deployments = append(deployments, deployment)
		accepted = append(accepted, key)
	}
	if err := h.Store.UpsertSoftwareDeployments(r.Context(), workspaceID, deployments); err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not store deployments.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"acceptedDeployments": accepted, "rejectedDeployments": rejected,
		"unknownIssueKeys": sortedSoftwareKeys(unknownKeys), "unknownAssociations": unknownAssociations(unknownRefs),
	})
}

func parseSoftwareBuild(raw json.RawMessage) (models.SoftwareBuild, []string, error) {
	var value struct {
		PipelineID           string                `json:"pipelineId"`
		BuildNumber          *int64                `json:"buildNumber"`
		UpdateSequenceNumber *int64                `json:"updateSequenceNumber"`
		DisplayName          string                `json:"displayName"`
		URL                  string                `json:"url"`
		State                string                `json:"state"`
		LastUpdated          string                `json:"lastUpdated"`
		IssueKeys            []string              `json:"issueKeys"`
		Associations         []softwareAssociation `json:"associations"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return models.SoftwareBuild{}, nil, &developmentInputError{"Build data must be valid JSON."}
	}
	lastUpdated, timestampErr := time.Parse(time.RFC3339, value.LastUpdated)
	key := models.SoftwareBuild{PipelineID: value.PipelineID}
	if value.BuildNumber != nil {
		key.BuildNumber = *value.BuildNumber
	}
	if !validSoftwareKey(value.PipelineID) || value.BuildNumber == nil || value.UpdateSequenceNumber == nil || strings.TrimSpace(value.DisplayName) == "" || len(value.DisplayName) > 255 || !validDevelopmentURL(value.URL) || !softwareState(value.State, false) || timestampErr != nil {
		return key, nil, &developmentInputError{"Build key, displayName, URL, state, lastUpdated, and updateSequenceNumber are required."}
	}
	refs := softwareIssueRefs(value.IssueKeys, value.Associations)
	return models.SoftwareBuild{PipelineID: strings.TrimSpace(value.PipelineID), BuildNumber: *value.BuildNumber,
		UpdateSequenceNumber: *value.UpdateSequenceNumber, DisplayName: strings.TrimSpace(value.DisplayName),
		URL: strings.TrimSpace(value.URL), State: value.State, LastUpdated: lastUpdated.UTC(), Payload: bytes.Clone(raw)}, refs, nil
}

func parseSoftwareDeployment(raw json.RawMessage) (models.SoftwareDeployment, []string, error) {
	var value struct {
		DeploymentSequenceNumber *int64                `json:"deploymentSequenceNumber"`
		UpdateSequenceNumber     *int64                `json:"updateSequenceNumber"`
		DisplayName              string                `json:"displayName"`
		URL                      string                `json:"url"`
		Description              *string               `json:"description"`
		State                    string                `json:"state"`
		LastUpdated              string                `json:"lastUpdated"`
		IssueKeys                []string              `json:"issueKeys"`
		Associations             []softwareAssociation `json:"associations"`
		Pipeline                 struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			URL         string `json:"url"`
		} `json:"pipeline"`
		Environment struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			Type        string `json:"type"`
		} `json:"environment"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return models.SoftwareDeployment{}, nil, &developmentInputError{"Deployment data must be valid JSON."}
	}
	lastUpdated, timestampErr := time.Parse(time.RFC3339, value.LastUpdated)
	validPipeline := validSoftwareKey(value.Pipeline.ID) && strings.TrimSpace(value.Pipeline.DisplayName) != "" && len(value.Pipeline.DisplayName) <= 255 && validDevelopmentURL(value.Pipeline.URL)
	validEnvironment := validSoftwareKey(value.Environment.ID) && strings.TrimSpace(value.Environment.DisplayName) != "" && len(value.Environment.DisplayName) <= 255 && softwareEnvironmentType(value.Environment.Type)
	key := models.SoftwareDeployment{PipelineID: value.Pipeline.ID, EnvironmentID: value.Environment.ID}
	if value.DeploymentSequenceNumber != nil {
		key.DeploymentSequenceNumber = *value.DeploymentSequenceNumber
	}
	if value.DeploymentSequenceNumber == nil || value.UpdateSequenceNumber == nil || value.Description == nil || len(*value.Description) > 255 || strings.TrimSpace(value.DisplayName) == "" || len(value.DisplayName) > 255 || !validDevelopmentURL(value.URL) || !softwareState(value.State, true) || timestampErr != nil || !validPipeline || !validEnvironment {
		return key, nil, &developmentInputError{"Deployment key, pipeline, environment, description, displayName, URL, state, lastUpdated, and updateSequenceNumber are required."}
	}
	refs := softwareIssueRefs(value.IssueKeys, value.Associations)
	return models.SoftwareDeployment{PipelineID: strings.TrimSpace(value.Pipeline.ID), EnvironmentID: strings.TrimSpace(value.Environment.ID),
		DeploymentSequenceNumber: *value.DeploymentSequenceNumber, UpdateSequenceNumber: *value.UpdateSequenceNumber,
		DisplayName: strings.TrimSpace(value.DisplayName), URL: strings.TrimSpace(value.URL), State: value.State,
		EnvironmentName: strings.TrimSpace(value.Environment.DisplayName), EnvironmentType: value.Environment.Type,
		LastUpdated: lastUpdated.UTC(), Payload: bytes.Clone(raw)}, refs, nil
}

func softwareIssueRefs(issueKeys []string, associations []softwareAssociation) []string {
	refs := append([]string(nil), issueKeys...)
	for _, association := range associations {
		if association.AssociationType == "issueIdOrKeys" {
			refs = append(refs, association.Values...)
		}
	}
	return normalizeDevelopmentIssueKeys(refs)
}

func (h *Handler) resolveSoftwareIssueRefs(r *http.Request, workspaceID string, refs []string, unknownKeys, unknownRefs map[string]bool) ([]string, []string) {
	resolved := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, ref)
		if err != nil {
			if developmentIssueKey.MatchString(strings.ToUpper(ref)) {
				unknownKeys[strings.ToUpper(ref)] = true
			} else {
				unknownRefs[ref] = true
			}
			continue
		}
		if !seen[issue.Key] {
			seen[issue.Key] = true
			resolved = append(resolved, issue.Key)
		}
	}
	return resolved, refs
}

func validSoftwareKey(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 255
}

func softwareState(value string, deployment bool) bool {
	states := map[string]bool{"pending": true, "in_progress": true, "successful": true, "failed": true, "cancelled": true, "unknown": true}
	if deployment {
		states["rolled_back"] = true
	}
	return states[value]
}

func softwareEnvironmentType(value string) bool {
	return map[string]bool{"unmapped": true, "development": true, "testing": true, "staging": true, "production": true}[value]
}

func softwareSequence(value, name string) (int64, error) {
	sequence, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, &developmentInputError{name + " must be an integer."}
	}
	return sequence, nil
}

func softwareUpdateSequence(r *http.Request) (*int64, error) {
	value := r.URL.Query().Get("_updateSequenceNumber")
	if value == "" {
		return nil, nil
	}
	sequence, err := softwareSequence(value, "_updateSequenceNumber")
	if err != nil {
		return nil, err
	}
	return &sequence, nil
}

func softwarePropertiesFromQuery(r *http.Request) (json.RawMessage, *int64, error) {
	sequence, err := softwareUpdateSequence(r)
	if err != nil {
		return nil, nil, err
	}
	values := make(map[string]string)
	for key, candidates := range r.URL.Query() {
		if key == "_updateSequenceNumber" {
			continue
		}
		if len(candidates) != 1 {
			return nil, nil, &developmentInputError{"Each property must have exactly one value."}
		}
		values[key] = candidates[0]
	}
	if len(values) == 0 {
		return nil, nil, &developmentInputError{"At least one property is required."}
	}
	properties, err := validateDevelopmentProperties(values)
	return properties, sequence, err
}

func sortedSoftwareKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func unknownAssociations(values map[string]bool) []softwareAssociation {
	refs := sortedSoftwareKeys(values)
	if len(refs) == 0 {
		return []softwareAssociation{}
	}
	return []softwareAssociation{{AssociationType: "issueIdOrKeys", Values: refs}}
}

func writeRawJSON(w http.ResponseWriter, payload json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
