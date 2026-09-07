package confluence

import (
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func (h *Handler) attachmentBean(a *models.WikiAttachment) map[string]any {
	download := "/wiki/download/attachments/" + a.PageID + "/" + a.ID + "/" + url.PathEscape(a.Filename)
	return map[string]any{"id": a.ID, "status": a.Status, "title": a.Filename, "createdAt": a.CreatedAt, "pageId": a.PageID, "mediaType": a.MediaType, "mediaTypeDescription": a.MediaType, "comment": a.Comment, "fileId": a.FileID, "fileSize": a.Size, "webuiLink": "/wiki/spaces/pages/" + a.PageID, "downloadLink": download, "version": a.Version, "_links": map[string]string{"webui": "/pages/" + a.PageID, "download": download, "base": h.BaseURL + "/wiki"}}
}

func (h *Handler) attachments(w http.ResponseWriter, r *http.Request, ws, actor, pageID string) {
	if !supportedQuery(w, r, "sort", "cursor", "status", "mediaType", "filename", "limit") {
		return
	}
	status := r.URL.Query().Get("status")
	if strings.Contains(status, ",") {
		failure(w, 400, "Only one attachment status is currently supported.")
		return
	}
	if status == "" {
		status = "current"
	}
	items, err := h.Store.WikiAttachments(r.Context(), ws, actor, pageID, r.URL.Query().Get("mediaType"), r.URL.Query().Get("filename"), status)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(items))
	for _, a := range items {
		values = append(values, h.attachmentBean(a))
	}
	h.list(w, r, values)
}

func (h *Handler) attachment(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "version", "include-labels", "include-properties", "include-operations", "include-versions", "include-version", "include-collaborators") {
		return
	}
	a, err := h.Store.WikiAttachment(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if raw := r.URL.Query().Get("version"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 {
			failure(w, 400, "version must be positive.")
			return
		}
		v, e := h.Store.WikiAttachmentVersion(r.Context(), ws, actor, id, n)
		if e != nil {
			writeError(w, e)
			return
		}
		a.Filename = v.Filename
		a.MediaType = v.MediaType
		a.Comment = v.Comment
		a.Size = v.Size
		a.Version = v.WikiVersion
	}
	bean := h.attachmentBean(a)
	if r.URL.Query().Get("include-operations") == "true" {
		bean["operations"] = map[string]any{"results": h.attachmentOperationValues(r, ws, actor, a)}
	}
	if r.URL.Query().Get("include-versions") == "true" {
		vs, _ := h.Store.WikiAttachmentVersions(r.Context(), ws, actor, id)
		bean["versions"] = map[string]any{"results": vs}
	}
	respond(w, 200, bean)
}

func (h *Handler) deleteAttachment(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "purge") {
		return
	}
	if err := h.Commands.DeleteWikiAttachment(r.Context(), ws, actor, id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) attachmentOperationValues(r *http.Request, ws, actor string, a *models.WikiAttachment) []any {
	ops := []any{map[string]string{"operation": "read", "targetType": "attachment"}}
	if ok, _ := h.Store.CanUpdateWikiPage(r.Context(), ws, actor, a.PageID); ok {
		ops = append(ops, map[string]string{"operation": "update", "targetType": "attachment"}, map[string]string{"operation": "delete", "targetType": "attachment"})
	}
	return ops
}
func (h *Handler) attachmentOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	a, err := h.Store.WikiAttachment(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{"operations": h.attachmentOperationValues(r, ws, actor, a)})
}
func (h *Handler) attachmentVersions(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "cursor", "limit", "sort") {
		return
	}
	vs, err := h.Store.WikiAttachmentVersions(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, len(vs))
	for i := range vs {
		values[i] = vs[i]
	}
	h.list(w, r, values)
}
func (h *Handler) attachmentVersion(w http.ResponseWriter, r *http.Request, ws, actor, id, raw string) {
	if !supportedQuery(w, r) {
		return
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		failure(w, 400, "version-number must be positive.")
		return
	}
	v, err := h.Store.WikiAttachmentVersion(r.Context(), ws, actor, id, n)
	if err != nil {
		writeError(w, err)
		return
	}
	bean := map[string]any{"number": v.Number, "authorId": v.AuthorID, "message": v.Message, "createdAt": v.CreatedAt, "minorEdit": v.MinorEdit, "contentTypeModified": false}
	if v.Number > 1 {
		bean["prevVersion"] = v.Number - 1
	}
	if current, _ := h.Store.WikiAttachment(r.Context(), ws, actor, id); current != nil && v.Number < current.Version.Number {
		bean["nextVersion"] = v.Number + 1
	}
	respond(w, 200, bean)
}

func parseWikiMultipart(w http.ResponseWriter, r *http.Request) ([]*models.WikiAttachment, []string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, (100<<20)+1)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		failure(w, 400, "Invalid multipart attachment request.")
		return nil, nil, false
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		failure(w, 400, "At least one file is required.")
		return nil, nil, false
	}
	comments := r.MultipartForm.Value["comment"]
	return make([]*models.WikiAttachment, 0, len(files)), comments, true
}

func (h *V1Handler) v1SaveAttachments(w http.ResponseWriter, r *http.Request, ws, actor, pageID string) {
	if r.Method != "POST" && r.Method != "PUT" {
		failure(w, 405, "Method not allowed.")
		return
	}
	if !supportedQuery(w, r, "status") {
		return
	}
	if status := r.URL.Query().Get("status"); status != "" && status != "current" {
		failure(w, 400, "Only current page attachments are supported.")
		return
	}
	out, comments, ok := parseWikiMultipart(w, r)
	if !ok {
		return
	}
	for i, header := range r.MultipartForm.File["file"] {
		file, err := header.Open()
		if err != nil {
			writeError(w, err)
			return
		}
		comment := ""
		if i < len(comments) {
			comment = comments[i]
		}
		attachmentID := ""
		if r.Method == "PUT" {
			if old, e := h.Store.WikiAttachmentByFilename(r.Context(), ws, actor, pageID, header.Filename); e == nil {
				attachmentID = old.ID
			}
		}
		a, e := h.Commands.SaveWikiAttachment(r.Context(), ws, actor, pageID, attachmentID, header.Filename, header.Header.Get("Content-Type"), comment, "", r.FormValue("minorEdit") == "true", file)
		closeErr := file.Close()
		if e != nil {
			writeError(w, e)
			return
		}
		if closeErr != nil {
			writeError(w, closeErr)
			return
		}
		out = append(out, a)
	}
	results := make([]any, len(out))
	for i, a := range out {
		results[i] = h.v1AttachmentBean(a)
	}
	respond(w, 200, map[string]any{"results": results, "start": 0, "limit": len(results), "size": len(results), "_links": map[string]string{"base": h.BaseURL + "/wiki"}})
}

func (h *V1Handler) v1AttachmentBean(a *models.WikiAttachment) map[string]any {
	return map[string]any{"id": a.ID, "type": "attachment", "status": a.Status, "title": a.Filename, "container": map[string]any{"id": a.PageID, "type": "page"}, "metadata": map[string]any{"mediaType": a.MediaType, "comment": a.Comment}, "extensions": map[string]any{"mediaType": a.MediaType, "fileSize": a.Size, "fileId": a.FileID}, "version": a.Version, "_links": map[string]string{"download": "/download/attachments/" + a.PageID + "/" + a.ID + "/" + url.PathEscape(a.Filename), "base": h.BaseURL + "/wiki"}}
}

func (h *V1Handler) v1UpdateAttachmentData(w http.ResponseWriter, r *http.Request, ws, actor, pageID, id string) {
	_, comments, ok := parseWikiMultipart(w, r)
	if !ok {
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) != 1 {
		failure(w, 400, "Exactly one file is required.")
		return
	}
	file, err := files[0].Open()
	if err != nil {
		writeError(w, err)
		return
	}
	defer file.Close()
	comment := ""
	if len(comments) > 0 {
		comment = comments[0]
	}
	a, err := h.Commands.SaveWikiAttachment(r.Context(), ws, actor, pageID, id, files[0].Filename, files[0].Header.Get("Content-Type"), comment, "", r.FormValue("minorEdit") == "true", file)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.v1AttachmentBean(a))
}

func (h *V1Handler) v1UpdateAttachmentProperties(w http.ResponseWriter, r *http.Request, ws, actor, pageID, id string) {
	var input struct {
		ID, Type, Title string
		Metadata        struct {
			MediaType string `json:"mediaType"`
		}
		Version struct {
			Number    int
			Message   string
			MinorEdit bool
		}
	}
	if !decode(w, r, &input) {
		return
	}
	a, err := h.Store.UpdateWikiAttachmentProperties(r.Context(), ws, actor, pageID, id, input.Title, input.Metadata.MediaType, "", input.Version.Message, input.Version.MinorEdit, input.Version.Number)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.v1AttachmentBean(a))
}
func (h *V1Handler) v1DownloadAttachment(w http.ResponseWriter, r *http.Request, ws, actor, pageID, id string) {
	if !supportedQuery(w, r, "version", "status") {
		return
	}
	a, err := h.Store.WikiAttachment(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if a.PageID != pageID {
		writeError(w, pgx.ErrNoRows)
		return
	}
	target := "/wiki/download/attachments/" + pageID + "/" + id + "/" + url.PathEscape(a.Filename)
	if version := r.URL.Query().Get("version"); version != "" {
		target += "?version=" + url.QueryEscape(version)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

type DownloadHandler struct{ *Handler }

func (h *DownloadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	actor, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		failure(w, 401, "Authentication required.")
		return
	}
	ws, err := h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		writeError(w, err)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/wiki/download/attachments/"), "/"), "/")
	if len(parts) < 2 {
		failure(w, 404, "Attachment not found.")
		return
	}
	version := 0
	if raw := r.URL.Query().Get("version"); raw != "" {
		version, _ = strconv.Atoi(raw)
	}
	a, err := h.Store.WikiAttachment(r.Context(), ws, actor, parts[1])
	if err != nil || a.PageID != parts[0] {
		writeError(w, pgx.ErrNoRows)
		return
	}
	ref, name, mediaType, err := h.Store.WikiAttachmentBlob(r.Context(), ws, actor, parts[1], version)
	if err != nil {
		writeError(w, err)
		return
	}
	reader, size, err := h.Blobs.Get(r.Context(), ref)
	if err != nil {
		writeError(w, err)
		return
	}
	defer func() {
		if err := reader.Close(); err != nil {
			log.Printf("wiki attachment close: %v", err)
		}
	}()
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name}); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	if _, err := io.Copy(w, reader); err != nil {
		log.Printf("wiki attachment stream: %v", err)
	}
}
