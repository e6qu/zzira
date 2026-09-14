package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/store"
)

// Jira Software's DevOps provider modules: operations (incidents and
// post-incident reviews), security (vulnerabilities), DevOps components,
// feature flags and remote links. Each accepts provider data in bulk, keeps
// the newest update of every entity and answers reads and deletes by id or by
// the properties the provider tagged the data with.

const providerRequestLimit = 5 << 20

var (
	providerContainerPattern = regexp.MustCompile(`^[a-zA-Z0-9\-_.~@:{}=]+(/[a-zA-Z0-9\-_.~@:{}=]+)*$`)
	providerIssueKeyPattern  = regexp.MustCompile(`^\w{1,255}-\d{1,255}$`)
	providerIssueRefPattern  = regexp.MustCompile(`^(\w{1,255}-\d{1,255}|\d{1,255})$`)
)

// providerModule describes one DevOps provider module.
type providerModule struct {
	name     string
	prefix   string
	linkable bool
	// collections are the request arrays the bulk submission accepts.
	collections []providerCollection
}

type providerCollection struct {
	requestKey  string // e.g. "flags"
	entityType  string // stored entity type
	pathSegment string // e.g. "flag" in /flag/{id}
	sequenceKey string // updateSequenceId or updateSequenceNumber
	acceptedKey string
	failedKey   string
	unknownKey  string // unknownIssueKeys, unknownAssociations or unknownProjectKeys
	validate    func(map[string]any) []string
	// issueRefs are the issue keys or ids an entity is associated with.
	issueRefs func(map[string]any) []string
}

var providerModules = []providerModule{
	{name: "featureflags", prefix: "/rest/featureflags/0.1", collections: []providerCollection{{
		requestKey: "flags", entityType: "flag", pathSegment: "flag", sequenceKey: "updateSequenceId",
		acceptedKey: "acceptedFeatureFlags", failedKey: "failedFeatureFlags", unknownKey: "unknownIssueKeys",
		validate: validateFeatureFlag, issueRefs: associationIssueRefs,
	}}},
	{name: "remotelinks", prefix: "/rest/remotelinks/1.0", collections: []providerCollection{{
		requestKey: "remoteLinks", entityType: "remotelink", pathSegment: "remotelink", sequenceKey: "updateSequenceNumber",
		acceptedKey: "acceptedRemoteLinks", failedKey: "rejectedRemoteLinks", unknownKey: "unknownAssociations",
		validate: validateRemoteLink, issueRefs: associationIssueRefs,
	}}},
	{name: "security", prefix: "/rest/security/1.0", linkable: true, collections: []providerCollection{{
		requestKey: "vulnerabilities", entityType: "vulnerability", pathSegment: "vulnerability", sequenceKey: "updateSequenceNumber",
		acceptedKey: "acceptedVulnerabilities", failedKey: "failedVulnerabilities", unknownKey: "unknownAssociations",
		validate: validateVulnerability, issueRefs: vulnerabilityIssueRefs,
	}}},
	{name: "operations", prefix: "/rest/operations/1.0", linkable: true, collections: []providerCollection{
		{
			requestKey: "incidents", entityType: "incident", pathSegment: "incidents", sequenceKey: "updateSequenceNumber",
			acceptedKey: "acceptedIncidents", failedKey: "failedIncidents", unknownKey: "unknownProjectKeys",
			validate: validateIncident, issueRefs: associationIssueRefs,
		},
		{
			requestKey: "reviews", entityType: "review", pathSegment: "post-incident-reviews", sequenceKey: "updateSequenceNumber",
			acceptedKey: "acceptedIncidents", failedKey: "failedIncidents", unknownKey: "unknownProjectKeys",
			validate: validateReview, issueRefs: associationIssueRefs,
		},
	}},
	{name: "devopscomponents", prefix: "/rest/devopscomponents/1.0", collections: []providerCollection{{
		requestKey: "devopsComponents", entityType: "component", pathSegment: "devopscomponents", sequenceKey: "updateSequenceNumber",
		acceptedKey: "acceptedComponents", failedKey: "failedComponents", unknownKey: "unknownProjectKeys",
		validate: validateDevOpsComponent,
	}}},
}

// providerModuleFor returns the module a request path belongs to.
func providerModuleFor(path string) (providerModule, bool) {
	for _, module := range providerModules {
		if strings.HasPrefix(path, module.prefix+"/") {
			return module, true
		}
	}
	return providerModule{}, false
}

// providerError writes the DevOps APIs' error shape: a list of messages.
func providerError(w http.ResponseWriter, status int, messages ...string) {
	body := make([]map[string]string, 0, len(messages))
	for _, message := range messages {
		body = append(body, map[string]string{"message": message})
	}
	writeJSON(w, status, body)
}

func (h *Handler) softwareProviderRoute(w http.ResponseWriter, r *http.Request, module providerModule) {
	workspaceID, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		if authErr.status == http.StatusUnauthorized {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		providerError(w, authErr.status, authErr.message)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, module.prefix), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "bulk" && r.Method == http.MethodPost:
		if h.providerRateLimited(w, r, workspaceID, actorID, module.name, true) {
			return
		}
		h.submitProviderData(w, r, workspaceID, module)
	case len(parts) == 1 && parts[0] == "bulkByProperties" && r.Method == http.MethodDelete:
		properties := providerPropertiesFromQuery(r.URL.Query())
		if len(properties) == 0 {
			providerError(w, http.StatusBadRequest, "At least one property must be given to delete by.")
			return
		}
		if err := h.Store.DeleteProviderEntitiesByProperties(r.Context(), workspaceID, module.name, properties); err != nil {
			providerError(w, http.StatusInternalServerError, "The data could not be deleted.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case module.linkable && len(parts) >= 1 && parts[0] == "linkedWorkspaces":
		h.providerLinkedWorkspaces(w, r, workspaceID, module, parts[1:])
	case len(parts) == 2:
		collection, ok := module.collectionForPath(parts[0])
		if !ok {
			providerError(w, http.StatusNotFound, "No resource found.")
			return
		}
		switch r.Method {
		case http.MethodGet:
			entity, err := h.Store.ProviderEntityRecord(r.Context(), workspaceID, module.name, collection.entityType, parts[1])
			if errors.Is(err, store.ErrProviderEntityNotFound) {
				providerError(w, http.StatusNotFound, "No data found for the given ID.")
				return
			}
			if err != nil {
				providerError(w, http.StatusInternalServerError, "The data could not be read.")
				return
			}
			writeRawJSON(w, entity.Payload)
		case http.MethodDelete:
			if err := h.Store.DeleteProviderEntity(r.Context(), workspaceID, module.name, collection.entityType, parts[1]); err != nil {
				providerError(w, http.StatusInternalServerError, "The data could not be deleted.")
				return
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			providerError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		}
	default:
		providerError(w, http.StatusNotFound, "No resource found.")
	}
}

func (module providerModule) collectionForPath(segment string) (providerCollection, bool) {
	for _, collection := range module.collections {
		if collection.pathSegment == segment {
			return collection, true
		}
	}
	return providerCollection{}, false
}

// providerPropertiesFromQuery reads the properties a bulk delete matches; the
// retired update sequence parameters are ignored.
func providerPropertiesFromQuery(query url.Values) map[string]string {
	properties := map[string]string{}
	for key, values := range query {
		if key == "_updateSequenceId" || key == "_updateSequenceNumber" || len(values) == 0 {
			continue
		}
		properties[key] = values[0]
	}
	return properties
}

func (h *Handler) submitProviderData(w http.ResponseWriter, r *http.Request, workspaceID string, module providerModule) {
	body := http.MaxBytesReader(w, r.Body, providerRequestLimit)
	var request map[string]json.RawMessage
	if err := json.NewDecoder(body).Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			providerError(w, http.StatusRequestEntityTooLarge, "Data is too large. Submit fewer entities in each payload.")
			return
		}
		providerError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	properties := map[string]string{}
	if raw, ok := request["properties"]; ok {
		if err := json.Unmarshal(raw, &properties); err != nil || len(properties) > 5 {
			providerError(w, http.StatusBadRequest, "properties must be an object of at most 5 string values.")
			return
		}
		for key, value := range properties {
			if len(key) > 255 || len(value) > 255 {
				providerError(w, http.StatusBadRequest, "Property keys and values must be at most 255 characters.")
				return
			}
		}
	}
	if raw, ok := request["operationType"]; ok {
		var operationType string
		if json.Unmarshal(raw, &operationType) != nil || (operationType != "NORMAL" && operationType != "SCAN" && operationType != "BACKFILL") {
			providerError(w, http.StatusBadRequest, "operationType must be NORMAL, SCAN or BACKFILL.")
			return
		}
	}
	allowed := map[string]bool{"properties": true, "providerMetadata": true, "preventTransitions": true}
	if module.name == "security" {
		allowed["operationType"] = true
	}
	submitted := false
	for _, collection := range module.collections {
		allowed[collection.requestKey] = true
		if _, ok := request[collection.requestKey]; ok {
			submitted = true
		}
	}
	for key := range request {
		if !allowed[key] {
			providerError(w, http.StatusBadRequest, "Unrecognized field "+strconv.Quote(key)+".")
			return
		}
	}
	if !submitted {
		providerError(w, http.StatusBadRequest, "The request must contain "+module.collections[0].requestKey+".")
		return
	}
	response := map[string]any{}
	unknown := map[string]bool{}
	var entities []store.ProviderEntity
	for _, collection := range module.collections {
		raw, ok := request[collection.requestKey]
		if !ok {
			continue
		}
		var items []map[string]any
		if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
			providerError(w, http.StatusBadRequest, collection.requestKey+" must be a non-empty array.")
			return
		}
		if len(items) > 1000 {
			providerError(w, http.StatusRequestEntityTooLarge, "Submit at most 1000 entities in each payload.")
			return
		}
		accepted, _ := response[collection.acceptedKey].([]string)
		if accepted == nil {
			accepted = []string{}
		}
		failed, _ := response[collection.failedKey].(map[string][]map[string]string)
		if failed == nil {
			failed = map[string][]map[string]string{}
		}
		for index, item := range items {
			id, _ := item["id"].(string)
			failureKey := id
			if failureKey == "" {
				failureKey = "index-" + strconv.Itoa(index)
			}
			if problems := collection.validate(item); len(problems) > 0 {
				for _, problem := range problems {
					failed[failureKey] = append(failed[failureKey], map[string]string{"message": problem})
				}
				continue
			}
			issueIDs := []string{}
			knownRefs := 0
			refs := []string{}
			if collection.issueRefs != nil {
				refs = collection.issueRefs(item)
			}
			for _, ref := range refs {
				issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, ref)
				if errors.Is(err, pgx.ErrNoRows) || err != nil {
					unknown[ref] = true
					continue
				}
				knownRefs++
				issueIDs = append(issueIDs, issue.ID)
			}
			if len(refs) > 0 && knownRefs == 0 {
				failed[failureKey] = append(failed[failureKey], map[string]string{"message": "The entity is only associated with unknown issues."})
				continue
			}
			if collection.entityType == "vulnerability" {
				issueIDs = h.mergeVulnerabilityIssues(r, workspaceID, id, item, issueIDs)
			}
			payload, err := json.Marshal(item)
			if err != nil {
				providerError(w, http.StatusBadRequest, "The entity could not be encoded.")
				return
			}
			sequence, _ := item[collection.sequenceKey].(float64)
			entities = append(entities, store.ProviderEntity{
				Type: collection.entityType, ID: id, UpdateSequence: int64(sequence),
				Properties: properties, IssueIDs: issueIDs, Payload: payload,
			})
			accepted = append(accepted, id)
		}
		response[collection.acceptedKey] = accepted
		if len(failed) > 0 {
			response[collection.failedKey] = failed
		}
	}
	if err := h.Store.UpsertProviderEntities(r.Context(), workspaceID, module.name, entities); err != nil {
		providerError(w, http.StatusInternalServerError, "The data could not be stored.")
		return
	}
	unknownRefs := make([]string, 0, len(unknown))
	for ref := range unknown {
		unknownRefs = append(unknownRefs, ref)
	}
	sort.Strings(unknownRefs)
	switch unknownKey := module.collections[0].unknownKey; unknownKey {
	case "unknownIssueKeys":
		response["unknownIssueKeys"] = unknownRefs
		response["unknownAssociations"] = issueAssociationList(unknownRefs)
	case "unknownAssociations":
		response["unknownAssociations"] = issueAssociationList(unknownRefs)
	case "unknownProjectKeys":
		response["unknownProjectKeys"] = []string{}
	}
	writeJSON(w, http.StatusAccepted, response)
}

func issueAssociationList(refs []string) []map[string]any {
	if len(refs) == 0 {
		return []map[string]any{}
	}
	return []map[string]any{{"associationType": "issueIdOrKeys", "values": refs}}
}

// mergeVulnerabilityIssues applies addAssociations and removeAssociations to
// the issues a stored vulnerability is already associated with.
func (h *Handler) mergeVulnerabilityIssues(r *http.Request, workspaceID, id string, item map[string]any, added []string) []string {
	current := map[string]bool{}
	if stored, err := h.Store.ProviderEntityRecord(r.Context(), workspaceID, "security", "vulnerability", id); err == nil {
		for _, issueID := range stored.IssueIDs {
			current[issueID] = true
		}
	}
	for _, issueID := range added {
		current[issueID] = true
	}
	for _, ref := range associationValues(item["removeAssociations"], "issueIdOrKeys") {
		if issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, ref); err == nil {
			delete(current, issue.ID)
		}
	}
	merged := make([]string, 0, len(current))
	for issueID := range current {
		merged = append(merged, issueID)
	}
	sort.Strings(merged)
	return merged
}

func (h *Handler) providerLinkedWorkspaces(w http.ResponseWriter, r *http.Request, workspaceID string, module providerModule, parts []string) {
	switch {
	case len(parts) == 1 && parts[0] == "bulk" && r.Method == http.MethodPost:
		var request struct {
			WorkspaceIDs []string `json:"workspaceIds"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, providerRequestLimit)).Decode(&request); err != nil || len(request.WorkspaceIDs) == 0 || len(request.WorkspaceIDs) > 100 {
			providerError(w, http.StatusBadRequest, "workspaceIds must list between 1 and 100 workspace IDs.")
			return
		}
		for _, id := range request.WorkspaceIDs {
			if len(id) > 255 || !providerContainerPattern.MatchString(id) {
				providerError(w, http.StatusBadRequest, "Invalid workspace ID "+strconv.Quote(id)+".")
				return
			}
		}
		if err := h.Store.LinkProviderWorkspaces(r.Context(), workspaceID, module.name, request.WorkspaceIDs); err != nil {
			providerError(w, http.StatusInternalServerError, "The workspaces could not be linked.")
			return
		}
		if module.name == "operations" {
			writeJSON(w, http.StatusAccepted, map[string]any{"acceptedWorkspaceIds": request.WorkspaceIDs})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case len(parts) == 1 && parts[0] == "bulk" && r.Method == http.MethodDelete:
		ids := []string{}
		for _, value := range r.URL.Query()["workspaceIds"] {
			for _, id := range strings.Split(value, ",") {
				if id = strings.TrimSpace(id); id != "" {
					ids = append(ids, id)
				}
			}
		}
		if len(ids) == 0 {
			providerError(w, http.StatusBadRequest, "workspaceIds must list the workspaces to unlink.")
			return
		}
		if err := h.Store.UnlinkProviderWorkspaces(r.Context(), workspaceID, module.name, ids); err != nil {
			providerError(w, http.StatusInternalServerError, "The workspaces could not be unlinked.")
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case len(parts) == 0 && r.Method == http.MethodGet:
		linked, err := h.Store.LinkedProviderWorkspaces(r.Context(), workspaceID, module.name)
		if err != nil {
			providerError(w, http.StatusInternalServerError, "The workspaces could not be read.")
			return
		}
		ids := make([]string, 0, len(linked))
		for _, workspace := range linked {
			ids = append(ids, workspace.ID)
		}
		if requested := r.URL.Query().Get("workspaceId"); requested != "" {
			filtered := []string{}
			for _, id := range ids {
				if id == requested {
					filtered = append(filtered, id)
				}
			}
			ids = filtered
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspaceIds": ids})
	case len(parts) == 1 && module.name == "security" && r.Method == http.MethodGet:
		linked, err := h.Store.LinkedProviderWorkspaces(r.Context(), workspaceID, module.name)
		if err != nil {
			providerError(w, http.StatusInternalServerError, "The workspaces could not be read.")
			return
		}
		for _, workspace := range linked {
			if workspace.ID == parts[0] {
				writeJSON(w, http.StatusOK, map[string]any{"workspaceId": workspace.ID, "updatedAt": workspace.UpdatedAt.UTC().Format(time.RFC3339)})
				return
			}
		}
		providerError(w, http.StatusNotFound, "No Security Workspace found for the given ID.")
	default:
		providerError(w, http.StatusNotFound, "No resource found.")
	}
}

// ---- validation ----

type providerChecks struct {
	item     map[string]any
	problems []string
}

func (c *providerChecks) fail(message string) { c.problems = append(c.problems, message) }

func (c *providerChecks) allowOnly(fields ...string) {
	allowed := map[string]bool{}
	for _, field := range fields {
		allowed[field] = true
	}
	keys := make([]string, 0, len(c.item))
	for key := range c.item {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !allowed[key] {
			c.fail("Unrecognized field " + strconv.Quote(key) + ".")
		}
	}
}

func (c *providerChecks) str(field string, required bool, maxLength int) string {
	value, present := c.item[field]
	if !present || value == nil {
		if required {
			c.fail(field + " is required.")
		}
		return ""
	}
	text, ok := value.(string)
	if !ok {
		c.fail(field + " must be a string.")
		return ""
	}
	if maxLength > 0 && len(text) > maxLength {
		c.fail(field + " must be at most " + strconv.Itoa(maxLength) + " characters.")
	}
	return text
}

func (c *providerChecks) enum(field string, required bool, values ...string) {
	text := c.str(field, required, 0)
	if text == "" {
		return
	}
	for _, value := range values {
		if text == value {
			return
		}
	}
	c.fail(field + " must be one of " + strings.Join(values, ", ") + ".")
}

func (c *providerChecks) uri(field string, required bool) {
	text := c.str(field, required, 2000)
	if text == "" {
		return
	}
	if parsed, err := url.Parse(text); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		c.fail(field + " must be an absolute URL.")
	}
}

func (c *providerChecks) dateTime(field string, required bool) {
	text := c.str(field, required, 0)
	if text == "" {
		return
	}
	if _, err := time.Parse(time.RFC3339, text); err != nil {
		c.fail(field + " must be an RFC 3339 date-time.")
	}
}

func (c *providerChecks) sequence(field string) {
	value, present := c.item[field]
	number, ok := value.(float64)
	if !present || !ok || number != float64(int64(number)) {
		c.fail(field + " is required and must be an integer.")
	}
}

func (c *providerChecks) schemaVersion(required bool) {
	c.enum("schemaVersion", required, "1.0")
}

func (c *providerChecks) object(field string, required bool) map[string]any {
	value, present := c.item[field]
	if !present || value == nil {
		if required {
			c.fail(field + " is required.")
		}
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		c.fail(field + " must be an object.")
		return nil
	}
	return object
}

func (c *providerChecks) array(field string, required bool, minItems, maxItems int) []any {
	value, present := c.item[field]
	if !present || value == nil {
		if required {
			c.fail(field + " is required.")
		}
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		c.fail(field + " must be an array.")
		return nil
	}
	if len(items) < minItems || (maxItems > 0 && len(items) > maxItems) {
		c.fail(field + " must have between " + strconv.Itoa(minItems) + " and " + strconv.Itoa(maxItems) + " items.")
	}
	return items
}

func (c *providerChecks) nested(field string, object map[string]any, check func(*providerChecks)) {
	if object == nil {
		return
	}
	inner := &providerChecks{item: object}
	check(inner)
	for _, problem := range inner.problems {
		c.fail(field + "." + problem)
	}
}

func (c *providerChecks) associations(field string, required bool, maxItems int, types ...string) {
	items := c.array(field, required, 1, maxItems)
	allowed := map[string]bool{}
	for _, associationType := range types {
		allowed[associationType] = true
	}
	total := 0
	for _, raw := range items {
		association, ok := raw.(map[string]any)
		if !ok {
			c.fail(field + " must contain association objects.")
			continue
		}
		associationType, _ := association["associationType"].(string)
		if !allowed[associationType] {
			c.fail(field + ".associationType must be one of " + strings.Join(types, ", ") + ".")
		}
		values, ok := association["values"].([]any)
		if !ok || len(values) == 0 {
			c.fail(field + ".values must list at least one value.")
			continue
		}
		total += len(values)
		for _, value := range values {
			text, ok := value.(string)
			if !ok || len(text) > 255 {
				c.fail(field + ".values must be strings of at most 255 characters.")
				continue
			}
			if associationType == "issueKeys" && !providerIssueKeyPattern.MatchString(text) {
				c.fail(field + ".values must be Jira issue keys.")
			}
			if associationType == "issueIdOrKeys" && !providerIssueRefPattern.MatchString(text) {
				c.fail(field + ".values must be Jira issue keys or ids.")
			}
		}
	}
	if total > 500 {
		c.fail(field + " must not associate more than 500 values.")
	}
}

func associationValues(raw any, types ...string) []string {
	allowed := map[string]bool{}
	for _, associationType := range types {
		allowed[associationType] = true
	}
	refs := []string{}
	items, _ := raw.([]any)
	for _, rawAssociation := range items {
		association, _ := rawAssociation.(map[string]any)
		associationType, _ := association["associationType"].(string)
		if !allowed[associationType] {
			continue
		}
		values, _ := association["values"].([]any)
		for _, value := range values {
			if text, ok := value.(string); ok {
				refs = append(refs, text)
			}
		}
	}
	return refs
}

// associationIssueRefs returns the issue keys and ids an entity names, from
// its associations and the deprecated issueKeys list.
func associationIssueRefs(item map[string]any) []string {
	refs := associationValues(item["associations"], "issueKeys", "issueIdOrKeys")
	if keys, ok := item["issueKeys"].([]any); ok {
		for _, key := range keys {
			if text, ok := key.(string); ok {
				refs = append(refs, text)
			}
		}
	}
	return uniqueStrings(refs)
}

func vulnerabilityIssueRefs(item map[string]any) []string {
	return uniqueStrings(associationValues(item["addAssociations"], "issueIdOrKeys"))
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func featureFlagStatus(c *providerChecks) {
	if enabled, ok := c.item["enabled"]; !ok {
		c.fail("enabled is required.")
	} else if _, isBool := enabled.(bool); !isBool {
		c.fail("enabled must be a boolean.")
	}
	c.str("defaultValue", false, 255)
	c.nested("rollout", c.object("rollout", false), func(rollout *providerChecks) {
		if percentage, ok := rollout.item["percentage"]; ok {
			if number, isNumber := percentage.(float64); !isNumber || number < 0 || number > 100 {
				rollout.fail("percentage must be a number from 0 to 100.")
			}
		}
		rollout.str("text", false, 255)
		if rules, ok := rollout.item["rules"]; ok {
			if number, isNumber := rules.(float64); !isNumber || number < 0 || number != float64(int64(number)) {
				rollout.fail("rules must be a non-negative integer.")
			}
		}
	})
}

func validateFeatureFlag(item map[string]any) []string {
	c := &providerChecks{item: item}
	c.allowOnly("schemaVersion", "id", "key", "updateSequenceId", "displayName", "issueKeys", "associations", "summary", "details")
	c.schemaVersion(false)
	c.str("id", true, 255)
	c.str("key", true, 255)
	c.sequence("updateSequenceId")
	c.str("displayName", false, 255)
	if keys := c.array("issueKeys", false, 1, 100); keys != nil {
		for _, key := range keys {
			if text, ok := key.(string); !ok || !providerIssueKeyPattern.MatchString(text) {
				c.fail("issueKeys must be Jira issue keys.")
			}
		}
	}
	if _, ok := item["associations"]; ok {
		c.associations("associations", false, 0, "issueKeys", "issueIdOrKeys")
	}
	c.nested("summary", c.object("summary", true), func(summary *providerChecks) {
		summary.uri("url", false)
		summary.dateTime("lastUpdated", true)
		summary.nested("status", summary.object("status", true), featureFlagStatus)
	})
	for _, raw := range c.array("details", true, 0, 0) {
		detail, ok := raw.(map[string]any)
		if !ok {
			c.fail("details must contain objects.")
			continue
		}
		c.nested("details", detail, func(details *providerChecks) {
			details.uri("url", true)
			details.dateTime("lastUpdated", true)
			details.nested("environment", details.object("environment", true), func(environment *providerChecks) {
				environment.str("name", true, 255)
				environment.enum("type", false, "development", "testing", "staging", "production")
			})
			details.nested("status", details.object("status", true), featureFlagStatus)
		})
	}
	return c.problems
}

func validateRemoteLink(item map[string]any) []string {
	c := &providerChecks{item: item}
	c.allowOnly("schemaVersion", "id", "updateSequenceNumber", "displayName", "url", "type", "description", "lastUpdated", "associations", "status", "actionIds", "attributeMap")
	c.schemaVersion(false)
	c.str("id", true, 255)
	c.sequence("updateSequenceNumber")
	c.str("displayName", true, 255)
	c.uri("url", true)
	c.enum("type", true, "document", "alert", "test", "security", "logFile", "prototype", "coverage", "bugReport", "other")
	c.str("description", false, 255)
	c.dateTime("lastUpdated", true)
	if _, ok := item["associations"]; ok {
		c.associations("associations", false, 2, "issueKeys", "issueIdOrKeys", "serviceIdOrKeys")
	}
	c.nested("status", c.object("status", false), func(status *providerChecks) {
		status.enum("appearance", true, "default", "inprogress", "moved", "new", "removed", "prototype", "success")
		status.str("label", true, 255)
	})
	c.array("actionIds", false, 0, 10)
	if attributes := c.object("attributeMap", false); attributes != nil {
		for _, value := range attributes {
			if _, ok := value.(string); !ok {
				c.fail("attributeMap values must be strings.")
				break
			}
		}
	}
	return c.problems
}

func validateVulnerability(item map[string]any) []string {
	c := &providerChecks{item: item}
	c.allowOnly("schemaVersion", "id", "updateSequenceNumber", "containerId", "displayName", "description", "url", "type",
		"introducedDate", "lastUpdated", "severity", "identifiers", "status", "additionalInfo", "addAssociations",
		"removeAssociations", "associationsLastUpdated", "associationsUpdateSequenceNumber")
	c.schemaVersion(true)
	c.str("id", true, 255)
	c.sequence("updateSequenceNumber")
	if container := c.str("containerId", true, 255); container != "" && !providerContainerPattern.MatchString(container) {
		c.fail("containerId has an invalid format.")
	}
	c.str("displayName", true, 255)
	c.str("description", true, 5000)
	c.uri("url", true)
	c.enum("type", true, "sca", "sast", "dast", "unknown")
	c.dateTime("introducedDate", true)
	c.dateTime("lastUpdated", true)
	c.nested("severity", c.object("severity", true), func(severity *providerChecks) {
		severity.enum("level", true, "critical", "high", "medium", "low", "unknown")
	})
	for _, raw := range c.array("identifiers", false, 1, 100) {
		identifier, ok := raw.(map[string]any)
		if !ok {
			c.fail("identifiers must contain objects.")
			continue
		}
		c.nested("identifiers", identifier, func(identifiers *providerChecks) {
			identifiers.str("displayName", true, 255)
			identifiers.uri("url", true)
		})
	}
	c.enum("status", true, "open", "closed", "ignored", "unknown")
	c.nested("additionalInfo", c.object("additionalInfo", false), func(info *providerChecks) {
		info.str("content", true, 255)
		info.uri("url", false)
	})
	for _, field := range []string{"addAssociations", "removeAssociations"} {
		if _, ok := item[field]; ok {
			c.associations(field, false, 1, "issueIdOrKeys")
		}
	}
	c.dateTime("associationsLastUpdated", false)
	if _, ok := item["associationsUpdateSequenceNumber"]; ok {
		c.sequence("associationsUpdateSequenceNumber")
	}
	return c.problems
}

func operationsEntity(c *providerChecks, statuses ...string) {
	c.schemaVersion(true)
	c.str("id", true, 255)
	c.sequence("updateSequenceNumber")
	c.str("summary", true, 255)
	c.str("description", true, 5000)
	c.uri("url", true)
	c.dateTime("createdDate", true)
	c.dateTime("lastUpdated", true)
	c.enum("status", true, statuses...)
	if _, ok := c.item["associations"]; ok {
		c.associations("associations", false, 100, "issueIdOrKeys", "serviceIdOrKeys", "ati:cloud:compass:event-source")
	}
}

func validateIncident(item map[string]any) []string {
	c := &providerChecks{item: item}
	c.allowOnly("schemaVersion", "id", "updateSequenceNumber", "affectedComponents", "summary", "description", "url", "createdDate", "lastUpdated", "severity", "status", "associations")
	operationsEntity(c, "open", "resolved", "unknown")
	for _, component := range c.array("affectedComponents", true, 1, 100) {
		if _, ok := component.(string); !ok {
			c.fail("affectedComponents must contain component IDs.")
			break
		}
	}
	c.nested("severity", c.object("severity", false), func(severity *providerChecks) {
		severity.enum("level", true, "P1", "P2", "P3", "P4", "P5", "unknown")
	})
	return c.problems
}

func validateReview(item map[string]any) []string {
	c := &providerChecks{item: item}
	c.allowOnly("schemaVersion", "id", "updateSequenceNumber", "reviews", "summary", "description", "url", "createdDate", "lastUpdated", "status", "associations")
	operationsEntity(c, "in progress", "outstanding actions", "completed", "unknown")
	for _, incident := range c.array("reviews", true, 1, 100) {
		if _, ok := incident.(string); !ok {
			c.fail("reviews must contain incident IDs.")
			break
		}
	}
	return c.problems
}

func validateDevOpsComponent(item map[string]any) []string {
	c := &providerChecks{item: item}
	c.allowOnly("schemaVersion", "id", "updateSequenceNumber", "name", "providerName", "description", "url", "avatarUrl", "tier", "componentType", "lastUpdated")
	c.schemaVersion(true)
	c.str("id", true, 255)
	c.sequence("updateSequenceNumber")
	c.str("name", true, 255)
	c.str("providerName", false, 255)
	c.str("description", true, 5000)
	c.uri("url", true)
	c.uri("avatarUrl", true)
	c.enum("tier", true, "Tier 1", "Tier 2", "Tier 3", "Tier 4")
	c.enum("componentType", true, "Service", "Application", "Library", "Capability", "Cloud resource", "Data pipeline", "Machine learning model", "UI element", "Website", "Other")
	c.dateTime("lastUpdated", true)
	return c.problems
}
