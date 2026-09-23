package web

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
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
	history, err := h.Store.ServiceAssetInventoryHistory(r.Context(), workspaceID, user.ID, deskID)
	if err != nil {
		http.Error(w, "Could not load what has happened to these objects.", http.StatusInternalServerError)
		return
	}
	comments, err := h.Store.ServiceAssetInventoryComments(r.Context(), workspaceID, user.ID, deskID)
	if err != nil {
		http.Error(w, "Could not load what people have said about these objects.", http.StatusInternalServerError)
		return
	}
	files, err := h.Store.ServiceAssetInventoryAttachments(r.Context(), workspaceID, user.ID, deskID)
	if err != nil {
		http.Error(w, "Could not load the files kept with these objects.", http.StatusInternalServerError)
		return
	}
	// An object type says what it sits under by name, which the page has only
	// as an id.
	schemaNames := make(map[string]string, len(inventory.Schemas))
	for _, schema := range inventory.Schemas {
		schemaNames[schema.ID] = schema.Name
	}
	data := servicePageData{Desk: desk, AssetInventory: inventory, CanAgent: true, CanAdmin: admin, AssetHistory: history, AssetComments: comments, AssetFiles: files, AssetSchemaNames: schemaNames}
	// An import redirects back here with what it wrote, so the inventory the
	// page shows is the one the import left behind.
	data.AssetImportError = r.URL.Query().Get("importError")
	created, createdErr := strconv.Atoi(r.URL.Query().Get("imported"))
	updated, updatedErr := strconv.Atoi(r.URL.Query().Get("reimported"))
	deleted, _ := strconv.Atoi(r.URL.Query().Get("unimported"))
	if createdErr == nil && updatedErr == nil {
		data.AssetImport = &models.ServiceAssetImport{Created: created, Updated: updated, Deleted: deleted}
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
	r.Body = http.MaxBytesReader(w, r.Body, serviceAssetImportLimit+(1<<20))
	if err := r.ParseMultipartForm(serviceAssetImportLimit); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
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
	imported, err := h.Commands.ImportServiceAssetObjects(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("schemaId"), text, r.PostFormValue("reconcile") == "true")
	if err != nil {
		redirectLocal(w, r, back+"?importError="+url.QueryEscape(err.Error())+"#import")
		return
	}
	redirectLocal(w, r, back+"?imported="+strconv.Itoa(imported.Created)+"&reimported="+strconv.Itoa(imported.Updated)+"&unimported="+strconv.Itoa(imported.Deleted)+"#import")
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
	switch {
	case r.PostFormValue("action") == "delete":
		err = h.Commands.DeleteServiceAssetSchema(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("schemaId"))
	case r.PostFormValue("action") == "parent":
		err = h.Commands.SetServiceAssetSchemaParent(r.Context(), user.ID, workspaceID, deskID, r.PostFormValue("schemaId"), r.PostFormValue("parent"))
	default:
		var attributes []models.ServiceAssetAttribute
		attributes, err = parseServiceAssetAttributes(r.PostFormValue("attributes"))
		if err == nil {
			_, err = h.Commands.CreateServiceAssetSchema(r.Context(), user.ID, workspaceID, deskID, models.ServiceAssetSchema{Key: r.PostFormValue("key"), Name: r.PostFormValue("name"), Description: r.PostFormValue("description"), ParentID: r.PostFormValue("parent"), Attributes: attributes})
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

// ServiceAssetObjectComment says something about an object, or takes a
// comment away. Agents of the desk may say something; whoever wrote a comment,
// and any site administrator, may remove it.
func (h *Handler) ServiceAssetObjectComment(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	deskID, objectID := r.PathValue("desk"), r.PostFormValue("objectId")
	var err error
	if r.PostFormValue("action") == "delete" {
		err = h.Store.DeleteServiceAssetObjectComment(r.Context(), workspaceID, user.ID, objectID, r.PostFormValue("commentId"))
	} else {
		_, err = h.Store.CreateServiceAssetObjectComment(r.Context(), workspaceID, user.ID, objectID, r.PostFormValue("body"))
	}
	if err != nil {
		http.Error(w, "Could not update the comments on this object: "+err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, "/service/agent/"+deskID+"/assets#objects")
}

// serviceAssetFileLimit is the largest file kept on an Assets object.
const serviceAssetFileLimit = 32 << 20

// ServiceAssetObjectFile keeps a file with an object, or takes one away.
// Agents of the desk keep files; whoever put one there, and any site
// administrator, removes it.
func (h *Handler) ServiceAssetObjectFile(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	deskID := r.PathValue("desk")
	back := "/service/agent/" + deskID + "/assets#objects"
	r.Body = http.MaxBytesReader(w, r.Body, serviceAssetFileLimit+(1<<20))
	if err := r.ParseMultipartForm(serviceAssetFileLimit); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
		http.Error(w, "Could not read the file.", http.StatusBadRequest)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	objectID := r.PostFormValue("objectId")
	if r.PostFormValue("action") == "delete" {
		blobRef, err := h.Store.DeleteServiceAssetObjectAttachment(r.Context(), workspaceID, user.ID, objectID, r.PostFormValue("fileId"))
		if err != nil {
			http.Error(w, "Could not remove that file: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := h.Commands.Blobs.Delete(r.Context(), blobRef); err != nil {
			log.Printf("remove asset file blob %s: %s", strconv.Quote(blobRef), strconv.Quote(err.Error()))
		}
		redirectLocal(w, r, back)
		return
	}
	upload, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Choose a file to keep with this object.", http.StatusBadRequest)
		return
	}
	defer func() { _ = upload.Close() }()
	ref := store.NewID("blob")
	size, err := h.Commands.Blobs.Put(r.Context(), ref, io.LimitReader(upload, serviceAssetFileLimit+1))
	if err != nil {
		http.Error(w, "Could not store that file.", http.StatusInternalServerError)
		return
	}
	if size > serviceAssetFileLimit {
		if err := h.Commands.Blobs.Delete(r.Context(), ref); err != nil {
			log.Printf("remove oversized asset file blob %s: %s", strconv.Quote(ref), strconv.Quote(err.Error()))
		}
		http.Error(w, "That file is larger than 32 MB.", http.StatusBadRequest)
		return
	}
	if _, err := h.Store.SaveServiceAssetObjectAttachment(r.Context(), workspaceID, user.ID, objectID,
		header.Filename, header.Header.Get("Content-Type"), size, ref); err != nil {
		if deleteErr := h.Commands.Blobs.Delete(r.Context(), ref); deleteErr != nil {
			log.Printf("remove unrecorded asset file blob %s: %s", strconv.Quote(ref), strconv.Quote(deleteErr.Error()))
		}
		http.Error(w, "Could not keep that file: "+err.Error(), http.StatusBadRequest)
		return
	}
	redirectLocal(w, r, back)
}

// ServiceAssetObjectFileDownload serves a file kept with an object to the
// agents of its desk.
func (h *Handler) ServiceAssetObjectFileDownload(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	file, err := h.Store.ServiceAssetObjectAttachment(r.Context(), workspaceID, user.ID, r.PathValue("object"), r.PathValue("file"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	reader, _, err := h.Commands.Blobs.Get(r.Context(), file.BlobRef)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = reader.Close() }()
	w.Header().Set("Content-Type", file.MediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(file.Filename))
	if _, err := io.Copy(w, reader); err != nil {
		log.Printf("serve asset file %s: %s", strconv.Quote(file.ID), strconv.Quote(err.Error()))
	}
}
