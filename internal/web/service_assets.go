package web

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func (h *Handler) ServiceAssetsPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	deskID := r.PathValue("desk")
	allowed, err := h.Store.IsServiceAgent(r.Context(), workspaceID, deskID, user.ID)
	if err != nil || !allowed {
		http.Error(w, "Service agent access is required.", http.StatusForbidden)
		return
	}
	desk, err := h.Store.ServiceDesk(r.Context(), workspaceID, deskID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	inventory, err := h.Store.ServiceAssetInventory(r.Context(), workspaceID, user.ID, deskID)
	if err != nil {
		http.Error(w, "Could not load service assets.", http.StatusInternalServerError)
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	if err != nil {
		http.Error(w, "Could not authorize service asset administration.", http.StatusInternalServerError)
		return
	}
	data := servicePageData{Desk: desk, AssetInventory: inventory, CanAgent: true, CanAdmin: admin}
	// An import redirects back here with what it wrote, so the inventory the
	// page shows is the one the import left behind.
	data.AssetImportError = r.URL.Query().Get("importError")
	created, createdErr := strconv.Atoi(r.URL.Query().Get("imported"))
	updated, updatedErr := strconv.Atoi(r.URL.Query().Get("reimported"))
	if createdErr == nil && updatedErr == nil {
		data.AssetImport = &models.ServiceAssetImport{Created: created, Updated: updated}
	}
	h.writeWorkspacePage(w, r, "page_service_assets", user, workspaceID, data, "service-agent", desk.ProjectID)
}

// serviceAssetImportLimit is the largest file an import reads. A thousand
// objects of typed attributes are far smaller than this.
const serviceAssetImportLimit = 4 << 20

// ServiceAssetImport reads a comma separated file of objects for one schema.
func (h *Handler) ServiceAssetImport(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	deskID := r.PathValue("desk")
	back := "/service/agent/" + deskID + "/assets"
	if err := r.ParseMultipartForm(serviceAssetImportLimit); err != nil {
		redirectLocal(w, r, back+"?importError="+url.QueryEscape("that file could not be read")+"#import")
		return
	}
	file, header, err := r.FormFile("file")
	text := r.PostFormValue("objects")
	if err == nil {
		defer func() { _ = file.Close() }()
		if header.Size > serviceAssetImportLimit {
			redirectLocal(w, r, back+"?importError="+url.QueryEscape("that file is larger than 4 MB")+"#import")
			return
		}
		body, readErr := io.ReadAll(io.LimitReader(file, serviceAssetImportLimit+1))
		if readErr != nil {
			redirectLocal(w, r, back+"?importError="+url.QueryEscape("that file could not be read")+"#import")
			return
		}
		text = string(body)
	}
	if strings.TrimSpace(text) == "" {
		redirectLocal(w, r, back+"?importError="+url.QueryEscape("choose a file or paste the objects to import")+"#import")
		return
	}
	imported, err := h.Commands.ImportServiceAssetObjects(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("schemaId"), text)
	if err != nil {
		redirectLocal(w, r, back+"?importError="+url.QueryEscape(err.Error())+"#import")
		return
	}
	redirectLocal(w, r, back+"?imported="+strconv.Itoa(imported.Created)+"&reimported="+strconv.Itoa(imported.Updated)+"#import")
}

func parseServiceAssetAttributes(value string) ([]models.ServiceAssetAttribute, error) {
	attributes := []models.ServiceAssetAttribute{}
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 3 || len(parts) > 5 {
			return nil, fmt.Errorf("each attribute line needs key, name, and type")
		}
		attribute := models.ServiceAssetAttribute{Key: strings.TrimSpace(parts[0]), Name: strings.TrimSpace(parts[1]), Type: strings.TrimSpace(parts[2])}
		if len(parts) >= 4 {
			requirement := strings.TrimSpace(parts[3])
			if requirement != "" && !strings.EqualFold(requirement, "required") {
				return nil, fmt.Errorf("the fourth attribute value may only be required")
			}
			attribute.Required = strings.EqualFold(requirement, "required")
		}
		if len(parts) == 5 {
			for _, option := range strings.Split(parts[4], ",") {
				attribute.Options = append(attribute.Options, strings.TrimSpace(option))
			}
		}
		attributes = append(attributes, attribute)
	}
	return attributes, nil
}

func (h *Handler) ServiceAssetSchemaSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	var err error
	if r.PostFormValue("action") == "delete" {
		err = h.Commands.DeleteServiceAssetSchema(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("schemaId"))
	} else {
		var attributes []models.ServiceAssetAttribute
		attributes, err = parseServiceAssetAttributes(r.PostFormValue("attributes"))
		if err == nil {
			_, err = h.Commands.CreateServiceAssetSchema(r.Context(), user.ID, workspaceID, deskID, models.ServiceAssetSchema{Key: r.PostFormValue("key"), Name: r.PostFormValue("name"), Description: r.PostFormValue("description"), Attributes: attributes})
		}
	}
	if err != nil {
		http.Error(w, "Could not update asset schema: "+err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"/assets#schemas")
}

func (h *Handler) ServiceAssetObjectSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	if r.PostFormValue("action") == "delete" {
		if err := h.Commands.DeleteServiceAssetObject(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("objectId")); err != nil {
			http.Error(w, "Could not delete asset object: "+err.Error(), http.StatusBadRequest)
			return
		}
		redirectLocal(w, r, "/service/agent/"+deskID+"/assets#objects")
		return
	}
	x, xErr := strconv.Atoi(r.PostFormValue("x"))
	y, yErr := strconv.Atoi(r.PostFormValue("y"))
	if xErr != nil || yErr != nil {
		http.Error(w, "Topology coordinates must be numbers.", http.StatusBadRequest)
		return
	}
	values := map[string]string{}
	for key, entries := range r.PostForm {
		if strings.HasPrefix(key, "value_") && len(entries) > 0 {
			values[strings.TrimPrefix(key, "value_")] = entries[0]
		}
	}
	_, err := h.Commands.SaveServiceAssetObject(r.Context(), user.ID, workspaceID, deskID, models.ServiceAssetObject{ID: r.PostFormValue("objectId"), SchemaID: r.PostFormValue("schemaId"), Key: r.PostFormValue("key"), Label: r.PostFormValue("label"), Values: values, X: x, Y: y})
	if err != nil {
		http.Error(w, "Could not save asset object: "+err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"/assets#objects")
}

func (h *Handler) ServiceAssetRelationshipSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	deskID := r.PathValue("desk")
	var err error
	if r.PostFormValue("action") == "delete" {
		err = h.Commands.DeleteServiceAssetRelationship(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("relationshipId"))
	} else {
		_, err = h.Commands.CreateServiceAssetRelationship(r.Context(), user.ID, workspaceID, deskID, models.ServiceAssetRelationship{Relationship: r.PostFormValue("relationship"), From: models.ServiceAssetObject{ID: r.PostFormValue("fromObject")}, To: models.ServiceAssetObject{ID: r.PostFormValue("toObject")}})
	}
	if err != nil {
		http.Error(w, "Could not update asset relationship: "+err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"/assets#topology")
}

func (h *Handler) ServiceRequestAssetSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	linked := r.PostFormValue("action") != "unlink"
	if err := h.Commands.SetServiceRequestAsset(r.Context(), user.ID, workspaceID, r.PathValue("key"), r.PostFormValue("objectId"), r.PostFormValue("role"), linked); err != nil {
		http.Error(w, "Could not update request assets: "+err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/requests/"+r.PathValue("key")+"#asset-impact")
}
