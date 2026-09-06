package web

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type adminGroupRow struct {
	Group         *models.Group
	MemberIDs     map[string]bool
	ProductAccess map[string]bool
}

type adminPageData struct {
	Organization                      *models.Organization
	Site                              *models.Site
	Products                          []*models.Product
	Domains                           []*models.OrganizationDomain
	Policies                          []*models.OrganizationPolicy
	IdentityProviders                 []LoginProvider
	Directory                         *models.Directory
	Groups                            []adminGroupRow
	Users                             []*models.User
	Audit                             []*models.OrganizationAuditEvent
	AuditActions                      []adminAuditAction
	AuditQuery                        string
	AuditAction                       string
	Error                             string
	Saved                             string
	GroupName                         string
	GroupDescription                  string
	CurrentUserID                     string
	InvitationNotificationsConfigured bool
}

type adminAuditAction struct {
	Value string
	Name  string
}

var adminAuditActions = []adminAuditAction{
	{Value: "domain.created", Name: "Domain added"},
	{Value: "domain.deleted", Name: "Domain removed"},
	{Value: "domain.verified", Name: "Domain verified"},
	{Value: "group.created", Name: "Group created"},
	{Value: "group.deleted", Name: "Group deleted"},
	{Value: "group.member.added", Name: "Group member added"},
	{Value: "group.member.removed", Name: "Group member removed"},
	{Value: "identity.login", Name: "Provider sign-in"},
	{Value: "identity.linked", Name: "Provider connected"},
	{Value: "identity.unlinked", Name: "Provider disconnected"},
	{Value: "identity.provider.disabled", Name: "Provider disabled"},
	{Value: "identity.provider.enabled", Name: "Provider enabled"},
	{Value: "policy.created", Name: "Policy created"},
	{Value: "policy.deleted", Name: "Policy deleted"},
	{Value: "policy.resource.added", Name: "Policy resource added"},
	{Value: "policy.resource.removed", Name: "Policy resource removed"},
	{Value: "policy.resource.updated", Name: "Policy resource updated"},
	{Value: "policy.updated", Name: "Policy updated"},
	{Value: "role.assigned", Name: "Role assigned"},
	{Value: "role.revoked", Name: "Role revoked"},
	{Value: "user.invited", Name: "User invited"},
	{Value: "user.profile.updated", Name: "User profile updated"},
	{Value: "user.removed", Name: "User removed"},
	{Value: "user.restored", Name: "User restored"},
	{Value: "user.suspended", Name: "User suspended"},
}

func (h *Handler) adminData(r *http.Request, workspaceID, message string) (adminPageData, error) {
	organization, err := h.Store.OrganizationByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return adminPageData{}, err
	}
	site, err := h.Store.SiteByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return adminPageData{}, err
	}
	products, err := h.Store.ProductsBySite(r.Context(), site.ID)
	if err != nil {
		return adminPageData{}, err
	}
	domains, err := h.Store.OrganizationDomains(r.Context(), organization.ID)
	if err != nil {
		return adminPageData{}, err
	}
	policies, err := h.Store.OrganizationPolicies(r.Context(), organization.ID, "")
	if err != nil {
		return adminPageData{}, err
	}
	directories, err := h.Store.DirectoriesByOrganization(r.Context(), organization.ID)
	if err != nil {
		return adminPageData{}, err
	}
	data := adminPageData{
		Organization:                      organization,
		Site:                              site,
		Products:                          products,
		Domains:                           domains,
		Policies:                          policies,
		IdentityProviders:                 h.adminProviders(),
		Groups:                            []adminGroupRow{},
		Users:                             []*models.User{},
		Audit:                             []*models.OrganizationAuditEvent{},
		AuditActions:                      adminAuditActions,
		AuditQuery:                        strings.TrimSpace(r.URL.Query().Get("auditQuery")),
		AuditAction:                       r.URL.Query().Get("auditAction"),
		Error:                             message,
		Saved:                             r.URL.Query().Get("saved"),
		InvitationNotificationsConfigured: h.InvitationNotificationsConfigured,
	}
	if len(directories) == 0 {
		return data, nil
	}
	data.Directory = directories[0]
	data.Users, err = h.Store.DirectoryUsers(r.Context(), data.Directory.ID)
	if err != nil {
		return adminPageData{}, err
	}
	groups, err := h.Store.GroupsByDirectory(r.Context(), data.Directory.ID)
	if err != nil {
		return adminPageData{}, err
	}
	for _, group := range groups {
		memberIDs, err := h.Store.GroupMemberIDs(r.Context(), group.ID)
		if err != nil {
			return adminPageData{}, err
		}
		membership := make(map[string]bool, len(memberIDs))
		for _, userID := range memberIDs {
			membership[userID] = true
		}
		bindings, err := h.Store.RoleBindingsForPrincipal(r.Context(), workspaceID, "group", group.ID)
		if err != nil {
			return adminPageData{}, err
		}
		productAccess := make(map[string]bool)
		for _, binding := range bindings {
			if binding.ScopeType == "product" && binding.RoleKey == "atlassian/user" {
				productAccess[binding.ScopeID] = true
			}
		}
		data.Groups = append(data.Groups, adminGroupRow{Group: group, MemberIDs: membership, ProductAccess: productAccess})
	}
	data.Audit, _, err = h.Store.QueryOrganizationAuditEvents(r.Context(), organization.ID, store.OrganizationAuditFilter{
		Query: data.AuditQuery, Action: data.AuditAction, Limit: 20,
	})
	if err != nil {
		return adminPageData{}, err
	}
	return data, nil
}

func (h *Handler) adminProviders() []LoginProvider {
	if h.IdentityProviders != nil {
		return h.IdentityProviders.AdminProviders()
	}
	return h.loginProviders()
}

func (h *Handler) UpdateAdminIdentityProvider(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	providerKey := r.PathValue("provider")
	provider := h.IdentityProviders.Provider(providerKey)
	if provider == nil && h.IdentityProviders != nil {
		provider = h.IdentityProviders.ProviderByKeyAny(providerKey)
	}
	if provider == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	action := r.FormValue("action")
	if action != "enable" && action != "disable" {
		http.Error(w, "action must be enable or disable", http.StatusBadRequest)
		return
	}
	enabled := action == "enable"
	if err := h.Store.SetIdentityProviderEnabled(r.Context(), workspaceID, user.ID, providerKey, provider.issuer, enabled); err != nil {
		http.Error(w, "update identity provider", http.StatusInternalServerError)
		return
	}
	h.IdentityProviders.SetEnabled(providerKey, enabled)
	message := provider.displayName + " enabled"
	if !enabled {
		message = provider.displayName + " disabled"
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape(message), http.StatusSeeOther)
}

func (h *Handler) CreateAdminDomain(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	_, err := h.Store.CreateOrganizationDomain(r.Context(), workspaceID, user.ID, r.FormValue("name"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrAdminValidation) {
			status = http.StatusBadRequest
		} else if errors.Is(err, store.ErrAdminConflict) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape("Domain added"), http.StatusSeeOther)
}

func (h *Handler) UpdateAdminDomain(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	action := r.FormValue("action")
	if action == "delete" {
		if err := h.Store.DeleteOrganizationDomain(r.Context(), workspaceID, user.ID, r.PathValue("domainId")); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrAdminNotFound) {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		http.Redirect(w, r, "/admin?saved="+url.QueryEscape("Domain removed"), http.StatusSeeOther)
		return
	}
	if action != "verify" {
		http.Error(w, "action must be verify or delete", http.StatusBadRequest)
		return
	}
	organization, err := h.Store.OrganizationByWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "load organization", http.StatusInternalServerError)
		return
	}
	domain, err := h.Store.OrganizationDomain(r.Context(), organization.ID, r.PathValue("domainId"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, pgx.ErrNoRows) {
			status = http.StatusNotFound
		}
		http.Error(w, "load domain", status)
		return
	}
	lookup := h.DomainTXTLookup
	if lookup == nil {
		lookup = net.DefaultResolver.LookupTXT
	}
	records, err := lookup(r.Context(), "_zzira-challenge."+domain.Name)
	expected := "zzira-domain-verification=" + domain.VerificationToken
	verified := false
	for _, record := range records {
		verified = verified || strings.TrimSpace(record) == expected
	}
	if err != nil || !verified {
		http.Error(w, "DNS verification record was not found: "+expected, http.StatusConflict)
		return
	}
	if err := h.Store.VerifyOrganizationDomain(r.Context(), workspaceID, user.ID, domain.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape("Domain verified"), http.StatusSeeOther)
}

func productResourceID(product *models.Product) string {
	return "ari:cloud:" + product.Key + "::site/" + product.SiteID
}

func policyRuleValues(policy *models.OrganizationPolicy) []string {
	values, ok := policy.Rule["in"].([]any)
	if !ok {
		if texts, ok := policy.Rule["in"].([]string); ok {
			return texts
		}
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func policyResourceInputs(policy *models.OrganizationPolicy) []store.PolicyResourceInput {
	resources := make([]store.PolicyResourceInput, 0, len(policy.Resources))
	for _, resource := range policy.Resources {
		resources = append(resources, store.PolicyResourceInput{ID: resource.ID, Meta: resource.Meta, Links: resource.Links})
	}
	return resources
}

func (h *Handler) CreateAdminPolicy(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	values := strings.FieldsFunc(r.FormValue("values"), func(character rune) bool { return character == ',' || character == '\n' || character == '\r' })
	input := store.PolicyInput{Type: r.FormValue("type"), Name: r.FormValue("name"), Status: "disabled", Values: values}
	if r.FormValue("enabled") == "true" {
		input.Status = "enabled"
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil {
		http.Error(w, "load administration", http.StatusInternalServerError)
		return
	}
	available := map[string]bool{}
	for _, product := range data.Products {
		available[productResourceID(product)] = true
	}
	for _, resourceID := range r.Form["resourceId"] {
		if !available[resourceID] {
			http.Error(w, "policy product was not found", http.StatusNotFound)
			return
		}
		input.Resources = append(input.Resources, store.PolicyResourceInput{ID: resourceID})
	}
	if _, err := h.Store.CreateOrganizationPolicy(r.Context(), workspaceID, user.ID, input); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrAdminValidation) {
			status = http.StatusBadRequest
		} else if errors.Is(err, store.ErrAdminConflict) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape("Policy created"), http.StatusSeeOther)
}

func (h *Handler) UpdateAdminPolicy(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	action := r.FormValue("action")
	if action == "delete" {
		if err := h.Store.DeleteOrganizationPolicy(r.Context(), workspaceID, user.ID, r.PathValue("policyId")); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrAdminNotFound) {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		http.Redirect(w, r, "/admin?saved="+url.QueryEscape("Policy deleted"), http.StatusSeeOther)
		return
	}
	if action != "enable" && action != "disable" {
		http.Error(w, "action must be enable, disable, or delete", http.StatusBadRequest)
		return
	}
	organization, err := h.Store.OrganizationByWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "load organization", http.StatusInternalServerError)
		return
	}
	policy, err := h.Store.OrganizationPolicy(r.Context(), organization.ID, r.PathValue("policyId"))
	if err != nil {
		http.Error(w, "load policy", http.StatusNotFound)
		return
	}
	status := "enabled"
	if action == "disable" {
		status = "disabled"
	}
	_, err = h.Store.UpdateOrganizationPolicy(r.Context(), workspaceID, user.ID, policy.ID, store.PolicyInput{
		Type: policy.Type, Name: policy.Name, Status: status, Values: policyRuleValues(policy), Resources: policyResourceInputs(policy),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	message := "Policy enabled"
	if action == "disable" {
		message = "Policy disabled"
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape(message), http.StatusSeeOther)
}

func (h *Handler) AdminPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil {
		http.Error(w, "load administration", http.StatusInternalServerError)
		return
	}
	data.CurrentUserID = user.ID
	h.writeWorkspacePage(w, r, "page_admin", user, workspaceID, data, "admin", "")
}

func (h *Handler) CreateAdminGroup(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil {
		http.Error(w, "load administration", http.StatusInternalServerError)
		return
	}
	if data.Directory == nil {
		http.Error(w, "organization has no active directory", http.StatusConflict)
		return
	}
	_, err = h.Store.CreateDirectoryGroup(r.Context(), workspaceID, user.ID, data.Directory.ID, r.FormValue("name"), r.FormValue("description"))
	if err != nil {
		status := http.StatusInternalServerError
		message := "Group creation failed."
		if errors.Is(err, store.ErrAdminValidation) {
			status = http.StatusBadRequest
			message = strings.TrimSpace(strings.TrimPrefix(err.Error(), store.ErrAdminValidation.Error()+":"))
		} else if errors.Is(err, store.ErrAdminConflict) {
			status = http.StatusConflict
			message = strings.TrimSpace(strings.TrimPrefix(err.Error(), store.ErrAdminConflict.Error()+":"))
		}
		data, dataErr := h.adminData(r, workspaceID, message)
		if dataErr != nil {
			http.Error(w, "load administration", http.StatusInternalServerError)
			return
		}
		data.GroupName = r.FormValue("name")
		data.GroupDescription = r.FormValue("description")
		data.CurrentUserID = user.ID
		h.writeWorkspacePageStatus(w, r, "page_admin", user, workspaceID, data, "admin", "", status)
		return
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape("Group created"), http.StatusSeeOther)
}

func (h *Handler) UpdateAdminGroupMember(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil {
		http.Error(w, "load administration", http.StatusInternalServerError)
		return
	}
	if data.Directory == nil {
		http.Error(w, "organization has no active directory", http.StatusConflict)
		return
	}
	action := r.FormValue("action")
	if action != "add" && action != "remove" {
		http.Error(w, "action must be add or remove", http.StatusBadRequest)
		return
	}
	err = h.Store.SetGroupMember(r.Context(), workspaceID, user.ID, data.Directory.ID, r.PathValue("groupId"), r.FormValue("accountId"), action == "add")
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, store.ErrAdminValidation):
			status = http.StatusBadRequest
		case errors.Is(err, store.ErrAdminConflict):
			status = http.StatusConflict
		case errors.Is(err, store.ErrAdminNotFound), errors.Is(err, pgx.ErrNoRows):
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	message := "Member added"
	if action == "remove" {
		message = "Member removed"
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape(message), http.StatusSeeOther)
}

func (h *Handler) UpdateAdminGroupRole(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	action := r.FormValue("action")
	if action != "assign" && action != "revoke" {
		http.Error(w, "action must be assign or revoke", http.StatusBadRequest)
		return
	}
	err := h.Store.SetRoleBinding(r.Context(), workspaceID, user.ID, "group", r.PathValue("groupId"), "product", r.FormValue("productId"), "atlassian/user", action == "assign")
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, store.ErrAdminValidation):
			status = http.StatusBadRequest
		case errors.Is(err, store.ErrAdminNotFound), errors.Is(err, pgx.ErrNoRows):
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	message := "Product access granted"
	if action == "revoke" {
		message = "Product access revoked"
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape(message), http.StatusSeeOther)
}

func (h *Handler) DeleteAdminGroup(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil || data.Directory == nil {
		http.Error(w, "load directory", http.StatusInternalServerError)
		return
	}
	err = h.Store.DeleteDirectoryGroup(r.Context(), workspaceID, user.ID, data.Directory.ID, r.PathValue("groupId"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, pgx.ErrNoRows) {
			status = http.StatusNotFound
		}
		http.Error(w, "delete group", status)
		return
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape("Group deleted"), http.StatusSeeOther)
}

func (h *Handler) InviteAdminUser(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil || data.Directory == nil {
		http.Error(w, "load directory", http.StatusInternalServerError)
		return
	}
	groupIDs := r.Form["groupId"]
	availableGroups := map[string]bool{}
	for _, row := range data.Groups {
		availableGroups[row.Group.ID] = true
	}
	for _, groupID := range groupIDs {
		if !availableGroups[groupID] {
			http.Error(w, "invitation group was not found", http.StatusNotFound)
			return
		}
	}
	availableProducts := map[string]*models.Product{}
	for _, product := range data.Products {
		availableProducts[product.ID] = product
	}
	assignments := make([]store.InviteRoleAssignment, 0, len(r.Form["productId"]))
	for _, productID := range r.Form["productId"] {
		product := availableProducts[productID]
		if product == nil || !product.Enabled {
			http.Error(w, "invitation product was not found", http.StatusNotFound)
			return
		}
		assignments = append(assignments, store.InviteRoleAssignment{
			ScopeType: "product", ScopeID: product.ID, Role: "atlassian/user",
			Resource: "ari:cloud:" + product.Key + "::site/" + product.SiteID,
		})
	}
	sendNotification := r.FormValue("sendNotification") == "true"
	notificationText := r.FormValue("notificationText")
	if sendNotification && !h.InvitationNotificationsConfigured {
		http.Error(w, "invitation email delivery is not configured", http.StatusServiceUnavailable)
		return
	}
	if !sendNotification && strings.TrimSpace(notificationText) != "" {
		http.Error(w, "personal message requires invitation email", http.StatusBadRequest)
		return
	}
	if len(notificationText) > 4000 {
		http.Error(w, "personal message is too long", http.StatusBadRequest)
		return
	}
	options := store.InviteOptions{GroupIDs: groupIDs, Roles: assignments}
	if sendNotification {
		organizationName := strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(data.Organization.Name))
		options.EmailSubject = "Invitation to " + organizationName
		options.EmailBody = webInvitationEmailBody(organizationName, h.BaseURL, notificationText)
	}
	passwordHash, err := authn.UnusablePasswordHash()
	if err == nil {
		_, err = h.Store.InviteDirectoryUserWithAccess(r.Context(), workspaceID, user.ID, data.Directory.ID, r.FormValue("email"), r.FormValue("displayName"), passwordHash, options)
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrAdminValidation) {
			status = http.StatusBadRequest
		} else if errors.Is(err, store.ErrAdminConflict) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape("User invited"), http.StatusSeeOther)
}

func webInvitationEmailBody(organizationName, baseURL, custom string) string {
	lines := []string{"You have been invited to " + organizationName + " on ZZIRA.", "", "Sign in at " + strings.TrimRight(baseURL, "/") + "/login"}
	if custom = strings.TrimSpace(custom); custom != "" {
		lines = append(lines, "", custom)
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) UpdateAdminUserStatus(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil || data.Directory == nil {
		http.Error(w, "load directory", http.StatusInternalServerError)
		return
	}
	action := r.FormValue("action")
	if action != "suspend" && action != "restore" && action != "remove" {
		http.Error(w, "unsupported user action", http.StatusBadRequest)
		return
	}
	accountID := r.PathValue("accountId")
	if action == "remove" {
		err = h.Store.RemoveDirectoryUser(r.Context(), workspaceID, user.ID, data.Directory.ID, accountID)
	} else {
		err = h.Store.SetDirectoryUserActive(r.Context(), workspaceID, user.ID, data.Directory.ID, accountID, action == "restore")
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrAdminValidation) {
			status = http.StatusBadRequest
		} else if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrAdminNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	messages := map[string]string{"suspend": "User suspended", "restore": "User restored", "remove": "User removed"}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape(messages[action]), http.StatusSeeOther)
}

func (h *Handler) UpdateAdminUserProfile(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil || data.Directory == nil {
		http.Error(w, "load directory", http.StatusInternalServerError)
		return
	}
	err = h.Store.UpdateDirectoryUserProfile(r.Context(), workspaceID, user.ID, data.Directory.ID, r.PathValue("accountId"), store.ManagedProfileUpdate{
		DisplayName: r.FormValue("displayName"), Nickname: r.FormValue("nickname"),
		JobTitle: r.FormValue("jobTitle"), Department: r.FormValue("department"),
		OrganizationName: r.FormValue("organization"), Location: r.FormValue("location"), TimeZone: r.FormValue("timeZone"),
	})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrAdminValidation) {
			status = http.StatusBadRequest
		} else if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrAdminNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape("Profile updated"), http.StatusSeeOther)
}
