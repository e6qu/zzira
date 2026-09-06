package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

func (h *Handler) domainModel(domain *models.OrganizationDomain) map[string]any {
	self := strings.TrimRight(h.BaseURL, "/") + "/admin/v1/orgs/" + url.PathEscape(domain.OrganizationID) + "/domains/" + url.PathEscape(domain.ID)
	return map[string]any{
		"id": domain.ID, "type": "domains",
		"attributes": map[string]any{
			"name":  domain.Name,
			"claim": map[string]string{"type": domain.ClaimType, "status": domain.ClaimStatus},
		},
		"links": map[string]string{"self": self},
	}
}

func (h *Handler) Domains(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := rejectUnknownQuery(r.URL.Query(), "cursor"); err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	organization, ok := h.organizationForRequest(w, r, workspaceID)
	if !ok {
		return
	}
	offset, limit, err := parsePage(r.URL.Query())
	if err != nil {
		failure(w, http.StatusBadRequest, err.Error())
		return
	}
	domains, err := h.Store.OrganizationDomains(r.Context(), organization.ID)
	if err != nil {
		failure(w, http.StatusInternalServerError, "Domain lookup failed.")
		return
	}
	page, next := pageSlice(domains, offset, limit)
	data := make([]map[string]any, 0, len(page))
	for _, domain := range page {
		data = append(data, h.domainModel(domain))
	}
	links := map[string]string{"self": strings.TrimRight(h.BaseURL, "/") + r.URL.RequestURI()}
	if next != "" {
		links["next"] = strings.TrimRight(h.BaseURL, "/") + r.URL.Path + "?cursor=" + url.QueryEscape(next)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "links": links})
}

func (h *Handler) DomainDetails(w http.ResponseWriter, r *http.Request) {
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
	domain, err := h.Store.OrganizationDomain(r.Context(), organization.ID, r.PathValue("domainId"))
	if errors.Is(err, pgx.ErrNoRows) {
		failure(w, http.StatusNotFound, "Domain was not found.")
		return
	}
	if err != nil {
		failure(w, http.StatusInternalServerError, "Domain lookup failed.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": h.domainModel(domain)})
}
