package api3

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// assetsPrefix is where Jira Cloud serves the Assets API, one path per Assets
// workspace. A site has one, and every schema in it belongs to a service desk.
const assetsPrefix = "/jsm/assets/workspace/"

// assetsRoute serves the Assets API. A schema is also its object type, so the
// objectschema and objecttype resources answer for the same ids.
func (h *Handler) assetsRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, assetsPrefix), "/")
	assetsWorkspace, rest, _ := strings.Cut(rest, "/")
	ids, err := h.Store.ServiceAssetsWorkspaceIDs(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load Assets workspaces.")
		return
	}
	if !slices.Contains(ids, assetsWorkspace) {
		jiraError(w, http.StatusNotFound, "That Assets workspace does not exist.")
		return
	}
	version, rest, _ := strings.Cut(rest, "/")
	if version != "v1" {
		jiraError(w, http.StatusNotFound, "That Assets resource does not exist.")
		return
	}
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) == 2 && parts[0] == "objectschema" && parts[1] == "list" && r.Method == http.MethodGet:
		h.assetSchemaList(w, r, workspaceID, actorID, assetsWorkspace)
	case len(parts) == 2 && parts[0] == "objectschema" && parts[1] == "create" && r.Method == http.MethodPost:
		h.assetSchemaCreate(w, r, workspaceID, actorID, assetsWorkspace)
	case len(parts) == 2 && parts[0] == "objectschema" && r.Method == http.MethodDelete:
		h.assetSchemaDelete(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 2 && parts[0] == "objectschema" && r.Method == http.MethodGet:
		h.assetSchema(w, r, workspaceID, actorID, assetsWorkspace, parts[1])
	case len(parts) == 4 && parts[0] == "objectschema" && parts[2] == "objecttypes" && parts[3] == "flat" && r.Method == http.MethodGet:
		h.assetObjectTypes(w, r, workspaceID, actorID, assetsWorkspace, parts[1])
	case len(parts) == 3 && parts[0] == "objectschema" && parts[2] == "import" && r.Method == http.MethodPost:
		h.assetImport(w, r, workspaceID, actorID, assetsWorkspace, parts[1])
	case len(parts) == 3 && parts[0] == "objecttype" && parts[2] == "attributes" && r.Method == http.MethodGet:
		h.assetObjectTypeAttributes(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 2 && parts[0] == "object" && parts[1] == "create" && r.Method == http.MethodPost:
		h.assetObjectWrite(w, r, workspaceID, actorID, assetsWorkspace, "")
	case len(parts) == 3 && parts[0] == "object" && parts[1] == "navlist" && parts[2] == "aql" && r.Method == http.MethodPost:
		h.assetObjectSearch(w, r, workspaceID, actorID, assetsWorkspace)
	case len(parts) == 2 && parts[0] == "object" && r.Method == http.MethodGet:
		h.assetObject(w, r, workspaceID, actorID, assetsWorkspace, parts[1])
	case len(parts) == 2 && parts[0] == "object" && r.Method == http.MethodPut:
		h.assetObjectWrite(w, r, workspaceID, actorID, assetsWorkspace, parts[1])
	case len(parts) == 2 && parts[0] == "object" && r.Method == http.MethodDelete:
		h.assetObjectDelete(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 3 && parts[0] == "object" && parts[2] == "referenceinfo" && r.Method == http.MethodGet:
		h.assetObjectReferences(w, r, workspaceID, actorID, assetsWorkspace, parts[1])
	case len(parts) == 3 && parts[0] == "object" && parts[2] == "connectedTickets" && r.Method == http.MethodGet:
		h.assetObjectTickets(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 3 && parts[0] == "object" && parts[2] == "history" && r.Method == http.MethodGet:
		h.assetObjectHistory(w, r, workspaceID, actorID, parts[1])
	default:
		jiraError(w, http.StatusNotFound, "That Assets resource does not exist.")
	}
}

// assetError says the same thing for an id in another desk as for one that was
// never there, so the API never confirms what the caller may not read.
func assetError(w http.ResponseWriter, err error, action string) {
	switch {
	case errors.Is(err, store.ErrAssetNotFound), errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusNotFound, "That Assets object does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, action)
	}
}

func (h *Handler) assetsBase(assetsWorkspace string) string {
	return h.BaseURL + assetsPrefix + assetsWorkspace + "/v1"
}

func (h *Handler) assetSchemaBean(schema models.ServiceAssetSchema, assetsWorkspace string, objects int) map[string]any {
	return map[string]any{
		"id":              schema.ID,
		"name":            schema.Name,
		"objectSchemaKey": schema.Key,
		"description":     schema.Description,
		"serviceDeskId":   schema.ServiceDeskID,
		"objectCount":     objects,
		"objectTypeCount": 1,
		"_links":          map[string]string{"self": h.assetsBase(assetsWorkspace) + "/objectschema/" + schema.ID},
	}
}

func assetAttributeBean(attribute models.ServiceAssetAttribute, schemaID string) map[string]any {
	bean := map[string]any{
		"id":             attribute.Key,
		"name":           attribute.Name,
		"type":           attribute.Type,
		"required":       attribute.Required,
		"objectTypeId":   schemaID,
		"objectSchemaId": schemaID,
	}
	if attribute.Type == "select" {
		bean["options"] = attribute.Options
	}
	return bean
}

func (h *Handler) assetObjectBean(object models.ServiceAssetObject, assetsWorkspace string) map[string]any {
	values := object.Values
	if values == nil {
		values = map[string]string{}
	}
	return map[string]any{
		"id":         object.ID,
		"objectKey":  object.Key,
		"label":      object.Label,
		"name":       object.Label,
		"objectType": map[string]any{"id": object.SchemaID, "name": object.SchemaName, "objectSchemaKey": object.SchemaKey},
		"attributes": values,
		"position":   map[string]int{"x": object.X, "y": object.Y},
		"_links":     map[string]string{"self": h.assetsBase(assetsWorkspace) + "/object/" + object.ID},
	}
}

func (h *Handler) assetSchemaList(w http.ResponseWriter, r *http.Request, workspaceID, actorID, assetsWorkspace string) {
	schemas, err := h.Store.ServiceAssetSchemas(r.Context(), workspaceID, actorID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load Assets schemas.")
		return
	}
	values := make([]map[string]any, 0, len(schemas))
	for _, schema := range schemas {
		objects, err := h.Store.SearchServiceAssetObjects(r.Context(), workspaceID, actorID, schema.ID, "", 0, 0)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load Assets schemas.")
			return
		}
		values = append(values, h.assetSchemaBean(schema, assetsWorkspace, objects.Total))
	}
	writeJSON(w, http.StatusOK, map[string]any{"startAt": 0, "maxResults": len(values), "total": len(values), "values": values, "objectschemas": values, "isLast": true})
}

func (h *Handler) assetSchema(w http.ResponseWriter, r *http.Request, workspaceID, actorID, assetsWorkspace, schemaID string) {
	schema, _, err := h.Store.ServiceAssetSchema(r.Context(), workspaceID, actorID, schemaID)
	if err != nil {
		assetError(w, err, "Could not load that Assets schema.")
		return
	}
	objects, err := h.Store.SearchServiceAssetObjects(r.Context(), workspaceID, actorID, schemaID, "", 0, 0)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load that Assets schema.")
		return
	}
	writeJSON(w, http.StatusOK, h.assetSchemaBean(*schema, assetsWorkspace, objects.Total))
}

// assetObjectTypes reports the schema itself: this site has no type hierarchy
// below a schema, so the flat list of object types holds exactly one.
func (h *Handler) assetObjectTypes(w http.ResponseWriter, r *http.Request, workspaceID, actorID, assetsWorkspace, schemaID string) {
	schema, _, err := h.Store.ServiceAssetSchema(r.Context(), workspaceID, actorID, schemaID)
	if err != nil {
		assetError(w, err, "Could not load that Assets schema.")
		return
	}
	attributes := make([]map[string]any, 0, len(schema.Attributes))
	for _, attribute := range schema.Attributes {
		attributes = append(attributes, assetAttributeBean(attribute, schema.ID))
	}
	writeJSON(w, http.StatusOK, []map[string]any{{
		"id":                 schema.ID,
		"name":               schema.Name,
		"objectSchemaId":     schema.ID,
		"parentObjectTypeId": nil,
		"attributes":         attributes,
		"_links":             map[string]string{"self": h.assetsBase(assetsWorkspace) + "/objecttype/" + schema.ID},
	}})
}

func (h *Handler) assetObjectTypeAttributes(w http.ResponseWriter, r *http.Request, workspaceID, actorID, schemaID string) {
	schema, _, err := h.Store.ServiceAssetSchema(r.Context(), workspaceID, actorID, schemaID)
	if err != nil {
		assetError(w, err, "Could not load that Assets object type.")
		return
	}
	attributes := make([]map[string]any, 0, len(schema.Attributes))
	for _, attribute := range schema.Attributes {
		attributes = append(attributes, assetAttributeBean(attribute, schema.ID))
	}
	writeJSON(w, http.StatusOK, attributes)
}

func (h *Handler) assetObject(w http.ResponseWriter, r *http.Request, workspaceID, actorID, assetsWorkspace, objectID string) {
	object, _, err := h.Store.ServiceAssetObject(r.Context(), workspaceID, actorID, objectID)
	if err != nil {
		assetError(w, err, "Could not load that Assets object.")
		return
	}
	writeJSON(w, http.StatusOK, h.assetObjectBean(*object, assetsWorkspace))
}

type assetObjectBody struct {
	ObjectTypeID string            `json:"objectTypeId"`
	ObjectKey    string            `json:"objectKey"`
	Label        string            `json:"label"`
	Attributes   map[string]string `json:"attributes"`
	Position     *struct {
		X int `json:"x"`
		Y int `json:"y"`
	} `json:"position"`
}

func (h *Handler) assetObjectWrite(w http.ResponseWriter, r *http.Request, workspaceID, actorID, assetsWorkspace, objectID string) {
	var body assetObjectBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		jiraError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	object := models.ServiceAssetObject{ID: objectID, SchemaID: body.ObjectTypeID, Key: body.ObjectKey, Label: body.Label, Values: body.Attributes}
	if objectID != "" {
		// An update keeps whatever the object already is where the body is
		// silent, so a caller may move an object or rename it alone.
		existing, _, err := h.Store.ServiceAssetObject(r.Context(), workspaceID, actorID, objectID)
		if err != nil {
			assetError(w, err, "Could not load that Assets object.")
			return
		}
		if object.SchemaID == "" {
			object.SchemaID = existing.SchemaID
		}
		if object.Key == "" {
			object.Key = existing.Key
		}
		if object.Label == "" {
			object.Label = existing.Label
		}
		if object.Values == nil {
			object.Values = existing.Values
		}
		object.X, object.Y = existing.X, existing.Y
	}
	if body.Position != nil {
		object.X, object.Y = body.Position.X, body.Position.Y
	}
	schema, deskID, err := h.Store.ServiceAssetSchema(r.Context(), workspaceID, actorID, object.SchemaID)
	if err != nil {
		assetError(w, err, "Could not load that Assets object type.")
		return
	}
	written, err := h.Commands.SaveServiceAssetObject(r.Context(), actorID, workspaceID, deskID, object)
	if err != nil {
		if errors.Is(err, store.ErrProjectPermission) {
			jiraError(w, http.StatusForbidden, "Only site administrators change Assets objects.")
			return
		}
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := http.StatusCreated
	if objectID != "" {
		status = http.StatusOK
	}
	written.SchemaKey, written.SchemaName = schema.Key, schema.Name
	writeJSON(w, status, h.assetObjectBean(*written, assetsWorkspace))
}

func (h *Handler) assetObjectDelete(w http.ResponseWriter, r *http.Request, workspaceID, actorID, objectID string) {
	_, deskID, err := h.Store.ServiceAssetObject(r.Context(), workspaceID, actorID, objectID)
	if err != nil {
		assetError(w, err, "Could not load that Assets object.")
		return
	}
	if err := h.Commands.DeleteServiceAssetObject(r.Context(), actorID, workspaceID, deskID, objectID); err != nil {
		if errors.Is(err, store.ErrProjectPermission) {
			jiraError(w, http.StatusForbidden, "Only site administrators delete Assets objects.")
			return
		}
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type assetSearchBody struct {
	QLQuery      string `json:"qlQuery"`
	ObjectTypeID string `json:"objectTypeId"`
	StartAt      int    `json:"startAt"`
	MaxResults   int    `json:"maxResults"`
}

func (h *Handler) assetObjectSearch(w http.ResponseWriter, r *http.Request, workspaceID, actorID, assetsWorkspace string) {
	var body assetSearchBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		jiraError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	if body.MaxResults <= 0 || body.MaxResults > 200 {
		body.MaxResults = 50
	}
	found, err := h.Store.SearchServiceAssetObjects(r.Context(), workspaceID, actorID, body.ObjectTypeID, body.QLQuery, body.StartAt, body.MaxResults)
	if err != nil {
		if errors.Is(err, store.ErrAssetNotFound) {
			jiraError(w, http.StatusNotFound, "That Assets object type does not exist.")
			return
		}
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	entries := make([]map[string]any, 0, len(found.Objects))
	for _, object := range found.Objects {
		entries = append(entries, h.assetObjectBean(object, assetsWorkspace))
	}
	writeJSON(w, http.StatusOK, map[string]any{"startAt": body.StartAt, "maxResults": body.MaxResults, "total": found.Total, "objectEntries": entries, "isLast": body.StartAt+len(entries) >= found.Total})
}

func (h *Handler) assetObjectReferences(w http.ResponseWriter, r *http.Request, workspaceID, actorID, assetsWorkspace, objectID string) {
	references, err := h.Store.ServiceAssetObjectReferences(r.Context(), workspaceID, actorID, objectID)
	if err != nil {
		assetError(w, err, "Could not load that Assets object.")
		return
	}
	values := make([]map[string]any, 0, len(references))
	for _, reference := range references {
		values = append(values, map[string]any{
			"id":           reference.ID,
			"relationship": reference.Relationship,
			"direction":    map[bool]string{true: "outbound", false: "inbound"}[reference.From.ID == objectID],
			"object":       h.assetObjectBean(map[bool]models.ServiceAssetObject{true: reference.To, false: reference.From}[reference.From.ID == objectID], assetsWorkspace),
		})
	}
	writeJSON(w, http.StatusOK, values)
}

func (h *Handler) assetObjectTickets(w http.ResponseWriter, r *http.Request, workspaceID, actorID, objectID string) {
	requests, err := h.Store.ServiceAssetObjectRequests(r.Context(), workspaceID, actorID, objectID)
	if err != nil {
		assetError(w, err, "Could not load that Assets object.")
		return
	}
	tickets := make([]map[string]any, 0, len(requests))
	for _, request := range requests {
		tickets = append(tickets, map[string]any{"key": request.Key, "summary": request.Summary, "role": request.Role, "self": h.BaseURL + "/rest/servicedeskapi/request/" + request.Key})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(tickets), "tickets": tickets})
}

type assetImportBody struct {
	File string `json:"file"`
	// Reconcile deletes the objects of the schema that the file leaves out,
	// so an import can be the whole inventory rather than an addition to it.
	Reconcile bool `json:"reconcile"`
}

// assetSchemaBody is a schema and the attributes it describes. An attribute
// is named, typed, optionally required, and a select carries its options.
type assetSchemaBody struct {
	Name            string `json:"name"`
	ObjectSchemaKey string `json:"objectSchemaKey"`
	Description     string `json:"description"`
	ServiceDeskID   string `json:"serviceDeskId"`
	Attributes      []struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Type     string   `json:"type"`
		Required bool     `json:"required"`
		Options  []string `json:"options"`
	} `json:"attributes"`
}

// assetSchemaCreate makes a schema in one desk's inventory. A site has one
// Assets workspace and each schema belongs to a service desk, so the body
// names which desk it is for.
func (h *Handler) assetSchemaCreate(w http.ResponseWriter, r *http.Request, workspaceID, actorID, assetsWorkspace string) {
	var body assetSchemaBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		jiraError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	if strings.TrimSpace(body.ServiceDeskID) == "" {
		jiraError(w, http.StatusBadRequest, "Name the service desk the schema belongs to.")
		return
	}
	agent, err := h.Store.IsServiceAgent(r.Context(), workspaceID, body.ServiceDeskID, actorID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load that service desk.")
		return
	}
	if !agent {
		jiraError(w, http.StatusNotFound, "That service desk does not exist.")
		return
	}
	schema := models.ServiceAssetSchema{Key: body.ObjectSchemaKey, Name: body.Name, Description: body.Description}
	for _, attribute := range body.Attributes {
		key := attribute.ID
		if key == "" {
			key = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(attribute.Name), " ", "_"))
		}
		// An attribute with no type is a line of text, which is what most of
		// them are and what the Assets page writes when nothing is chosen.
		kind := attribute.Type
		if kind == "" {
			kind = "text"
		}
		schema.Attributes = append(schema.Attributes, models.ServiceAssetAttribute{
			Key: key, Name: attribute.Name, Type: kind, Required: attribute.Required, Options: attribute.Options,
		})
	}
	created, err := h.Commands.CreateServiceAssetSchema(r.Context(), actorID, workspaceID, body.ServiceDeskID, schema)
	if err != nil {
		if errors.Is(err, store.ErrProjectPermission) {
			jiraError(w, http.StatusForbidden, "Only site administrators create Assets schemas.")
			return
		}
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, h.assetSchemaBean(*created, assetsWorkspace, 0))
}

// assetSchemaDelete removes a schema, and with it its objects, their
// relationships and the request links that named them.
func (h *Handler) assetSchemaDelete(w http.ResponseWriter, r *http.Request, workspaceID, actorID, schemaID string) {
	_, deskID, err := h.Store.ServiceAssetSchema(r.Context(), workspaceID, actorID, schemaID)
	if err != nil {
		assetError(w, err, "Could not load that Assets schema.")
		return
	}
	if err := h.Commands.DeleteServiceAssetSchema(r.Context(), actorID, workspaceID, deskID, schemaID); err != nil {
		if errors.Is(err, store.ErrProjectPermission) {
			jiraError(w, http.StatusForbidden, "Only site administrators delete Assets schemas.")
			return
		}
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// assetImport reads the same comma separated inventory the Assets page takes,
// either as a JSON body or as the request body itself.
func (h *Handler) assetImport(w http.ResponseWriter, r *http.Request, workspaceID, actorID, assetsWorkspace, schemaID string) {
	schema, deskID, err := h.Store.ServiceAssetSchema(r.Context(), workspaceID, actorID, schemaID)
	if err != nil {
		assetError(w, err, "Could not load that Assets schema.")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		jiraError(w, http.StatusBadRequest, "Could not read the request body.")
		return
	}
	file, reconcile := string(raw), r.URL.Query().Get("reconcile") == "true"
	if strings.HasPrefix(strings.TrimSpace(r.Header.Get("Content-Type")), "application/json") {
		var body assetImportBody
		if err := json.Unmarshal(raw, &body); err != nil {
			jiraError(w, http.StatusBadRequest, "The request body is not valid JSON.")
			return
		}
		file, reconcile = body.File, body.Reconcile || reconcile
	}
	imported, err := h.Commands.ImportServiceAssetObjects(r.Context(), actorID, workspaceID, deskID, schemaID, file, reconcile)
	if err != nil {
		if errors.Is(err, store.ErrProjectPermission) {
			jiraError(w, http.StatusForbidden, "Only site administrators import Assets objects.")
			return
		}
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	objects := make([]map[string]any, 0, len(imported.Objects))
	for _, object := range imported.Objects {
		object.SchemaKey, object.SchemaName = schema.Key, schema.Name
		objects = append(objects, h.assetObjectBean(object, assetsWorkspace))
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": imported.Created, "updated": imported.Updated, "deleted": imported.Deleted, "total": len(objects), "objectEntries": objects})
}

// assetObjectHistory is what has happened to one object, oldest first, read
// from the site's own action log.
func (h *Handler) assetObjectHistory(w http.ResponseWriter, r *http.Request, workspaceID, actorID, objectID string) {
	history, err := h.Store.ServiceAssetObjectHistory(r.Context(), workspaceID, actorID, objectID)
	if err != nil {
		assetError(w, err, "Could not load that Assets object.")
		return
	}
	entries := make([]map[string]any, 0, len(history))
	for _, change := range history {
		entries = append(entries, map[string]any{
			"id":      change.Seq,
			"created": change.At,
			"actor":   map[string]string{"id": change.ActorID, "displayName": change.ActorName},
			"type":    change.Operation,
			"changed": change.Changed,
			"label":   change.Object.Label,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": len(entries), "entries": entries})
}
