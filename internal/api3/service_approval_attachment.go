package api3

import (
	"encoding/json"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
)

func (h *Handler) serviceApprovalBean(request *models.ServiceRequest, approval models.ServiceApproval, actorID string) map[string]any {
	approvers := make([]map[string]any, 0, len(approval.Approvers))
	canAnswer := false
	for _, approver := range approval.Approvers {
		approvers = append(approvers, map[string]any{"approver": h.serviceUserBean(approver.User), "approverDecision": approver.Decision})
		if approver.User.ID == actorID && approver.Decision == "pending" && approval.FinalDecision == "pending" {
			canAnswer = true
		}
	}
	bean := map[string]any{"id": approval.ID, "name": approval.Name, "finalDecision": approval.FinalDecision, "canAnswerApproval": canAnswer, "approvers": approvers, "createdDate": serviceDate(approval.CreatedAt), "_links": map[string]string{"self": h.BaseURL + "/rest/servicedeskapi/request/" + request.Issue.Key + "/approval/" + approval.ID}}
	if approval.CompletedAt != nil {
		bean["completedDate"] = serviceDate(*approval.CompletedAt)
	}
	return bean
}

func (h *Handler) serviceRequestApprovals(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey, approvalID string) {
	request, _, actorID, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	if approvalID == "" {
		values, err := h.Store.ServiceApprovals(r.Context(), request.Issue.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load approvals.")
			return
		}
		beans := make([]map[string]any, 0, len(values))
		for _, value := range values {
			beans = append(beans, h.serviceApprovalBean(request, value, actorID))
		}
		h.writeServicePage(w, r, beans)
		return
	}
	if r.Method == http.MethodPost {
		var input struct {
			Decision string `json:"decision"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
			jiraError(w, http.StatusBadRequest, "Request body is invalid.")
			return
		}
		approval, err := h.Commands.AnswerServiceApproval(r.Context(), actorID, workspaceID, request.Issue.ID, approvalID, input.Decision)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, h.serviceApprovalBean(request, *approval, actorID))
		return
	}
	approval, err := h.Store.ServiceApproval(r.Context(), request.Issue.ID, approvalID)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Approval was not found.")
		return
	}
	writeJSON(w, http.StatusOK, h.serviceApprovalBean(request, *approval, actorID))
}

func (h *Handler) serviceAttachmentBean(request *models.ServiceRequest, attachment models.Attachment) map[string]any {
	content := h.BaseURL + "/rest/servicedeskapi/request/" + request.Issue.Key + "/attachment/" + attachment.ID
	return map[string]any{
		"filename": attachment.Filename, "mimeType": attachment.MimeType, "size": attachment.Size,
		"author":  h.serviceUserBean(&models.User{ID: attachment.AuthorID, DisplayName: attachment.AuthorName, Active: true, AccountType: "atlassian"}),
		"created": serviceDate(parseServiceDate(attachment.Created)),
		"_links":  map[string]string{"self": content, "content": content, "thumbnail": content + "/thumbnail", "jiraRest": h.BaseURL + "/rest/api/3/attachment/" + attachment.ID},
	}
}

func (h *Handler) attachServiceTemporaryFiles(w http.ResponseWriter, r *http.Request, workspaceID, serviceDeskID string) {
	_, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Header.Get("X-Atlassian-Token") != "no-check" {
		w.Header().Set("X-Atlassian-Token", "no-check")
		jiraError(w, http.StatusForbidden, "XSRF check failed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
		jiraError(w, http.StatusBadRequest, "multipart/form-data body required")
		return
	}
	defer cleanupMultipart(r)
	files := r.MultipartForm.File["file"]
	if len(files) > 60 {
		jiraError(w, http.StatusRequestEntityTooLarge, "A maximum of 60 files can be uploaded.")
		return
	}
	values := make([]map[string]any, 0, len(files))
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			jiraError(w, http.StatusBadRequest, "Could not read an uploaded file.")
			return
		}
		value, createErr := h.Commands.CreateServiceTemporaryAttachment(r.Context(), actorID, workspaceID, serviceDeskID, header.Filename, header.Header.Get("Content-Type"), file)
		closeErr := file.Close()
		if createErr != nil {
			jiraError(w, http.StatusBadRequest, createErr.Error())
			return
		}
		if closeErr != nil {
			jiraError(w, http.StatusInternalServerError, "Could not close an uploaded file.")
			return
		}
		values = append(values, map[string]any{"temporaryAttachmentId": value.ID, "fileName": value.Filename})
	}
	if len(values) == 0 {
		jiraError(w, http.StatusBadRequest, "No attachments were uploaded.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"temporaryAttachments": values})
}

func (h *Handler) serviceRequestAttachments(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey string) {
	request, canManage, actorID, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	if r.Method == http.MethodPost {
		var input struct {
			TemporaryAttachmentIDs []string `json:"temporaryAttachmentIds"`
			Public                 *bool    `json:"public"`
			AdditionalComment      struct {
				Body string `json:"body"`
			} `json:"additionalComment"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
			jiraError(w, http.StatusBadRequest, "Request body is invalid.")
			return
		}
		public := true
		if input.Public != nil {
			public = *input.Public
		}
		attachments, comment, err := h.Commands.CreateServiceAttachmentComment(r.Context(), actorID, workspaceID, request.Issue.ID, input.TemporaryAttachmentIDs, input.AdditionalComment.Body, public)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		beans := make([]map[string]any, 0, len(attachments))
		for _, attachment := range attachments {
			beans = append(beans, h.serviceAttachmentBean(request, attachment.Attachment))
		}
		writeJSON(w, http.StatusCreated, map[string]any{"attachments": map[string]any{"start": 0, "limit": 50, "size": len(beans), "isLastPage": true, "values": beans}, "comment": h.serviceCommentBean(request, *comment)})
		return
	}
	values, err := h.Store.ServiceRequestAttachments(r.Context(), request.Issue.ID, canManage)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load attachments.")
		return
	}
	beans := make([]map[string]any, 0, len(values))
	for _, value := range values {
		beans = append(beans, h.serviceAttachmentBean(request, value.Attachment))
	}
	h.writeServicePage(w, r, beans)
}

func (h *Handler) serviceCommentAttachments(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey, commentID string) {
	request, canManage, _, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	if _, err := h.Store.ServiceRequestComment(r.Context(), request.Issue.ID, commentID, canManage); err != nil {
		jiraError(w, http.StatusNotFound, "Comment does not exist or is not visible.")
		return
	}
	values, err := h.Store.ServiceCommentAttachments(r.Context(), request.Issue.ID, commentID, canManage)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load attachments.")
		return
	}
	beans := make([]map[string]any, 0, len(values))
	for _, value := range values {
		beans = append(beans, h.serviceAttachmentBean(request, value.Attachment))
	}
	h.writeServicePage(w, r, beans)
}

func (h *Handler) serviceRequestAttachmentContent(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey, attachmentID string, thumbnail bool) {
	request, canManage, _, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	if _, err := h.Store.ServiceRequestAttachment(r.Context(), request.Issue.ID, attachmentID, canManage); err != nil {
		jiraError(w, http.StatusNotFound, "Attachment does not exist or is not visible.")
		return
	}
	if h.Blobs == nil {
		jiraError(w, http.StatusNotFound, "Attachment content is unavailable.")
		return
	}
	blobRef, filename, mimeType, err := h.Store.AttachmentBlobRef(r.Context(), workspaceID, attachmentID)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Attachment does not exist.")
		return
	}
	reader, size, err := h.Blobs.Get(r.Context(), blobRef)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Attachment does not exist.")
		return
	}
	defer func() {
		if err := reader.Close(); err != nil {
			log.Printf("service attachment close: %v", err)
		}
	}()
	w.Header().Set("Content-Type", mimeType)
	if !thumbnail {
		if disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); disposition != "" {
			w.Header().Set("Content-Disposition", disposition)
		}
	}
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if _, err := io.Copy(w, reader); err != nil {
		log.Printf("service attachment stream: %v", err)
	}
}
