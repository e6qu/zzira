package admin

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type policyRuleRequest struct {
	In []string `json:"in"`
}

type policyResourceRequest struct {
	ID    string         `json:"id"`
	Meta  map[string]any `json:"meta"`
	Links map[string]any `json:"links"`
}

type policyAttributesRequest struct {
	Type      string                  `json:"type"`
	Name      string                  `json:"name"`
	Status    string                  `json:"status"`
	Rule      policyRuleRequest       `json:"rule"`
	Resources []policyResourceRequest `json:"resources"`
}

type policyDataRequest struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Attributes policyAttributesRequest `json:"attributes"`
}

type policyRequest struct {
	Data policyDataRequest `json:"data"`
}

type policyResourceUpdateRequest struct {
	Meta  map[string]any `json:"meta"`
	Links map[string]any `json:"links"`
}

func policyStoreInput(input policyDataRequest) (store.PolicyInput, error) {
	if input.Type != "policy" {
		return store.PolicyInput{}, errors.New("data.type must be policy")
	}
	resources := make([]store.PolicyResourceInput, 0, len(input.Attributes.Resources))
	for _, resource := range input.Attributes.Resources {
		resources = append(resources, store.PolicyResourceInput{ID: resource.ID, Meta: resource.Meta, Links: resource.Links})
	}
	return store.PolicyInput{
		Type: input.Attributes.Type, Name: input.Attributes.Name, Status: input.Attributes.Status,
		Values: input.Attributes.Rule.In, Resources: resources,
	}, nil
}

func (h *Handler) policyModel(policy *models.OrganizationPolicy) map[string]any {
	resources := make([]map[string]any, 0, len(policy.Resources))
	for _, resource := range policy.Resources {
		resources = append(resources, map[string]any{
			"id": resource.ID, "applicationStatus": resource.ApplicationStatus,
			"meta": resource.Meta, "links": resource.Links,
		})
	}
	return map[string]any{
		"id": policy.ID, "type": "policy",
		"attributes": map[string]any{
			"type": policy.Type, "name": policy.Name, "status": policy.Status,
			"rule": policy.Rule, "resources": resources,
		},
	}
}

func policyFailure(w http.ResponseWriter, err error, operation string) {
	switch {
	case errors.Is(err, store.ErrAdminValidation):
		failure(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrAdminConflict):
		failure(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrAdminNotFound), errors.Is(err, pgx.ErrNoRows):
		failure(w, http.StatusNotFound, operation+" was not found.")
	default:
		failure(w, http.StatusInternalServerError, operation+" failed.")
	}
}

func (h *Handler) Policies(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	organization, ok := h.organizationForRequest(w, r, workspaceID)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if err := rejectUnknownQuery(r.URL.Query()); err != nil {
			failure(w, http.StatusBadRequest, err.Error())
			return
		}
		var request policyRequest
		if err := decodeJSONBody(r, &request); err != nil {
			failure(w, http.StatusBadRequest, "Policy body is invalid.")
			return
		}
		input, err := policyStoreInput(request.Data)
		if err != nil {
			failure(w, http.StatusBadRequest, err.Error())
			return
		}
		policy, err := h.Store.CreateOrganizationPolicy(r.Context(), workspaceID, actorID, input)
		if err != nil {
			policyFailure(w, err, "Policy creation")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"data": h.policyModel(policy)})
		return
	}
	if err := rejectUnknownQuery(r.URL.Query(), "cursor", "type"); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	policyType := r.URL.Query().Get("type")
	if policyType != "" && policyType != "ip-allowlist" && policyType != "data-residency" && policyType != "data-security" {
		failure(w, http.StatusBadRequest, "Policy type is invalid.")
		return
	}
	offset, limit, err := parsePage(r.URL.Query())
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	policies, err := h.Store.OrganizationPolicies(r.Context(), organization.ID, policyType)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Policy lookup failed.")
		return
	}
	page, next := pageSlice(policies, offset, limit)
	data := make([]map[string]any, 0, len(page))
	for _, policy := range page {
		data = append(data, h.policyModel(policy))
	}
	meta := map[string]any{"next": nil, "page_size": len(data)}
	links := map[string]string{"self": strings.TrimRight(h.BaseURL, "/") + r.URL.RequestURI()}
	if next != "" {
		meta["next"] = next
		query := r.URL.Query()
		query.Set("cursor", next)
		links["next"] = strings.TrimRight(h.BaseURL, "/") + r.URL.Path + "?" + query.Encode()
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "meta": meta, "links": links})
}

func (h *Handler) PolicyDetails(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	organization, ok := h.organizationForRequest(w, r, workspaceID)
	if !ok {
		return
	}
	policyID := r.PathValue("policyId")
	if r.Method == http.MethodDelete {
		if err := h.Store.DeleteOrganizationPolicy(r.Context(), workspaceID, actorID, policyID); err != nil {
			policyFailure(w, err, "Policy")
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if r.Method == http.MethodPut {
		var request policyRequest
		if err := decodeJSONBody(r, &request); err != nil {
			failure(w, http.StatusBadRequest, "Policy body is invalid.")
			return
		}
		if request.Data.ID != "" && request.Data.ID != policyID {
			failure(w, http.StatusBadRequest, "data.id must match policyId.")
			return
		}
		input, err := policyStoreInput(request.Data)
		if err != nil {
			failure(w, http.StatusBadRequest, err.Error())
			return
		}
		policy, err := h.Store.UpdateOrganizationPolicy(r.Context(), workspaceID, actorID, policyID, input)
		if err != nil {
			policyFailure(w, err, "Policy")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"data": h.policyModel(policy)})
		return
	}
	policy, err := h.Store.OrganizationPolicy(r.Context(), organization.ID, policyID)
	if err != nil {
		policyFailure(w, err, "Policy")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": h.policyModel(policy)})
}

func (h *Handler) PolicyResources(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := h.organizationForRequest(w, r, workspaceID); !ok {
		return
	}
	var request policyResourceRequest
	if err := decodeJSONBody(r, &request); err != nil {
		failure(w, http.StatusBadRequest, "Policy resource body is invalid.")
		return
	}
	policy, err := h.Store.AddOrganizationPolicyResource(r.Context(), workspaceID, actorID, r.PathValue("policyId"), store.PolicyResourceInput{ID: request.ID, Meta: request.Meta, Links: request.Links})
	if err != nil {
		policyFailure(w, err, "Policy or resource")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"data": h.policyModel(policy)})
}

func (h *Handler) PolicyResourceDetails(w http.ResponseWriter, r *http.Request) {
	actorID, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := h.organizationForRequest(w, r, workspaceID); !ok {
		return
	}
	policyID, resourceID := r.PathValue("policyId"), r.PathValue("resourceId")
	if r.Method == http.MethodDelete {
		if err := h.Store.DeleteOrganizationPolicyResource(r.Context(), workspaceID, actorID, policyID, resourceID); err != nil {
			policyFailure(w, err, "Policy or resource")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var request policyResourceUpdateRequest
	if err := decodeJSONBody(r, &request); err != nil {
		failure(w, http.StatusBadRequest, "Policy resource body is invalid.")
		return
	}
	policy, err := h.Store.UpdateOrganizationPolicyResource(r.Context(), workspaceID, actorID, policyID, resourceID, store.PolicyResourceInput{ID: resourceID, Meta: request.Meta, Links: request.Links})
	if err != nil {
		policyFailure(w, err, "Policy or resource")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"data": h.policyModel(policy)})
}

func (h *Handler) ValidatePolicy(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query()); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	organization, ok := h.organizationForRequest(w, r, workspaceID)
	if !ok {
		return
	}
	if _, err := h.Store.OrganizationPolicy(r.Context(), organization.ID, r.PathValue("policyId")); err != nil {
		policyFailure(w, err, "Policy")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
