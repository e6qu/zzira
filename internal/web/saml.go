package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/saml"
	"github.com/e6qu/zzira/internal/store"
)

// SAML sign-in: this site sends the person to the provider, the provider
// posts an assertion back, and the site reads it. Everything that makes the
// assertion worth reading -- the signature, who it is from, who it is for,
// when it holds and which sign-in it answers -- is checked in internal/saml;
// what is here is the session it turns into.

// samlServiceProvider is this site as a provider sees it: what it calls
// itself and where the provider posts its answer.
//
// The addresses come from what this server was configured with, never from
// the request: a provider is configured with them once, and an assertion is
// only for this site because the audience in it says so. Taking them from a
// Host header would let whoever sends one decide what the site calls itself.
func (h *Handler) samlServiceProvider(providerKey string) (saml.ServiceProvider, error) {
	base := strings.TrimSuffix(h.IdentityExternalURL, "/")
	if base == "" {
		base = strings.TrimSuffix(h.BaseURL, "/")
	}
	if base == "" {
		return saml.ServiceProvider{}, errors.New("this site has no external address, so it cannot sign in through SAML: set ZZIRA_EXTERNAL_URL")
	}
	if !samlProviderKey.MatchString(providerKey) {
		return saml.ServiceProvider{}, errors.New("that is not an identity provider of this site")
	}
	return saml.ServiceProvider{
		EntityID: base + "/saml/metadata",
		ACSURL:   base + "/saml/" + providerKey + "/acs",
	}, nil
}

// samlWorkspace is the site a SAML sign-in belongs to, which is this
// server's one workspace; the slug names it, the store keys on its id.
func (h *Handler) samlWorkspace(r *http.Request) (string, error) {
	return h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
}

// samlProvider reads a site's configured provider and the certificates it
// signs with.
func (h *Handler) samlProvider(r *http.Request, providerKey string) (saml.Provider, store.SAMLProvider, error) {
	workspaceID, err := h.samlWorkspace(r)
	if err != nil {
		return saml.Provider{}, store.SAMLProvider{}, err
	}
	stored, err := h.Store.SAMLProvider(r.Context(), workspaceID, providerKey)
	if err != nil {
		return saml.Provider{}, store.SAMLProvider{}, err
	}
	if !stored.Enabled {
		return saml.Provider{}, stored, errors.New("that identity provider is turned off")
	}
	certificates, err := saml.ParseCertificates(stored.Certificates)
	if err != nil {
		return saml.Provider{}, stored, err
	}
	return saml.Provider{
		EntityID: stored.EntityID, SSOURL: stored.SSOURL, Certificates: certificates,
		EmailAttribute: stored.EmailAttribute, NameAttribute: stored.NameAttribute,
	}, stored, nil
}

// SAMLMetadata describes this site to an identity provider, which is what an
// administrator gives the provider rather than typing addresses into it.
func (h *Handler) SAMLMetadata(w http.ResponseWriter, r *http.Request) {
	providerKey := r.PathValue("provider")
	if providerKey == "" {
		providerKey = "saml"
	}
	site, err := h.samlServiceProvider(providerKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// #nosec G705 -- the only part of this document that comes from the
	// request is the provider key, which samlServiceProvider refuses unless
	// it is lower-case letters, numbers and hyphens; the rest is what this
	// server was configured with. It is served as SAML metadata, not as HTML,
	// and with nosniff.
	_, _ = w.Write(saml.Metadata(site))
}

// SAMLLogin sends somebody to their identity provider to sign in.
func (h *Handler) SAMLLogin(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	providerKey := r.PathValue("provider")
	provider, _, err := h.samlProvider(r, providerKey)
	if err != nil {
		if errors.Is(err, store.ErrSAMLProviderNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "this site cannot sign in through that identity provider", http.StatusBadRequest)
		return
	}
	site, err := h.samlServiceProvider(providerKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	request, err := saml.NewAuthnRequest(provider, site, "", time.Now().UTC())
	if err != nil {
		http.Error(w, "could not begin sign-in", http.StatusInternalServerError)
		return
	}
	workspaceID, err := h.samlWorkspace(r)
	if err != nil {
		http.Error(w, "could not begin sign-in", http.StatusInternalServerError)
		return
	}
	if err := h.Store.StartSAMLSignIn(r.Context(), workspaceID, providerKey, request.ID, "", ""); err != nil {
		http.Error(w, "could not begin sign-in", http.StatusInternalServerError)
		return
	}
	// #nosec G710 -- the address is the site's own configured identity
	// provider, and being sent to it is the whole point of the flow.
	http.Redirect(w, r, request.Redirect, http.StatusFound)
}

// samlResponseLimit caps what a provider may post back.
const samlResponseLimit = 1 << 20

// SAMLAssertion reads what the identity provider posted and signs the person
// in as the assertion says.
func (h *Handler) SAMLAssertion(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	providerKey := r.PathValue("provider")
	r.Body = http.MaxBytesReader(w, r.Body, samlResponseLimit)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "that is not an answer this site can read", http.StatusBadRequest)
		return
	}
	provider, stored, err := h.samlProvider(r, providerKey)
	if err != nil {
		if errors.Is(err, store.ErrSAMLProviderNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "this site cannot sign in through that identity provider", http.StatusBadRequest)
		return
	}
	posted := r.PostFormValue("SAMLResponse")
	if strings.TrimSpace(posted) == "" {
		http.Error(w, "the identity provider sent no assertion", http.StatusBadRequest)
		return
	}
	// The answer names the sign-in it belongs to, and that sign-in is read
	// once: an answer somebody kept cannot be posted again.
	requestID := saml.InResponseTo(posted)
	if requestID == "" {
		http.Error(w, "this site reads only the answers to sign-ins it began", http.StatusBadRequest)
		return
	}
	workspaceID, err := h.samlWorkspace(r)
	if err != nil {
		http.Error(w, "could not complete sign-in", http.StatusInternalServerError)
		return
	}
	signIn, err := h.Store.ConsumeSAMLSignIn(r.Context(), workspaceID, requestID)
	if err != nil || signIn.ProviderKey != providerKey {
		http.Error(w, "that sign-in is not one this site began", http.StatusBadRequest)
		return
	}
	site, err := h.samlServiceProvider(providerKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	identity, err := saml.ReadResponse(posted, provider, site, requestID, time.Now().UTC())
	if err != nil {
		log.Printf("saml sign-in refused: %v", err)
		http.Error(w, "sign-in could not be completed", http.StatusUnauthorized)
		return
	}
	// An assertion is read once, whatever else it says.
	expires := identity.NotOnOrAfter
	if expires.IsZero() {
		expires = time.Now().Add(12 * time.Hour)
	}
	if err := h.Store.RememberSAMLAssertion(r.Context(), identity.AssertionID, expires); err != nil {
		http.Error(w, "sign-in could not be completed", http.StatusUnauthorized)
		return
	}

	displayName := strings.TrimSpace(identity.DisplayName)
	if displayName == "" {
		displayName, _, _ = strings.Cut(identity.Email, "@")
	}
	userID := signIn.LinkUserID
	if userID == "" {
		userID, err = h.Store.ResolveOIDCUser(r.Context(), identity.Issuer, identity.Subject, identity.Email, displayName, authn.UnusablePasswordHash)
	} else {
		err = h.Store.LinkOIDCIdentity(r.Context(), userID, identity.Issuer, identity.Subject, identity.Email)
	}
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInactiveUser):
			http.Error(w, "this account is inactive", http.StatusForbidden)
		case errors.Is(err, store.ErrAdminConflict):
			http.Error(w, "this sign-in account is already connected elsewhere", http.StatusConflict)
		default:
			http.Error(w, "could not create sign-in session", http.StatusInternalServerError)
		}
		return
	}
	session, ttl, err := authn.LoginIdentityProvider(r.Context(), h.Store, userID, "", identity.Issuer, identity.Subject, identity.SessionIndex, "saml:"+stored.ProviderKey)
	if err != nil {
		http.Error(w, "could not create sign-in session", http.StatusInternalServerError)
		return
	}
	authn.SetSessionCookieFor(w, session, ttl)
	if signIn.LinkUserID != "" {
		http.Redirect(w, r, "/people/"+url.PathEscape(userID)+"?saved="+url.QueryEscape(stored.DisplayName+" connected"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// samlProviderView is one configured SAML provider as the admin page shows
// it, with the addresses its identity provider needs.
type samlProviderView struct {
	store.SAMLProvider
	MetadataURL, ACSURL, LoginURL string
	Certificates                  int
	CertificateError              string
}

// samlProviderViews are the site's SAML providers for the admin page.
func (h *Handler) samlProviderViews(r *http.Request) ([]samlProviderView, error) {
	workspaceID, err := h.samlWorkspace(r)
	if err != nil {
		return nil, err
	}
	providers, err := h.Store.SAMLProviders(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	views := make([]samlProviderView, 0, len(providers))
	for _, provider := range providers {
		site, err := h.samlServiceProvider(provider.ProviderKey)
		if err != nil {
			return nil, err
		}
		view := samlProviderView{SAMLProvider: provider, MetadataURL: site.EntityID, ACSURL: site.ACSURL,
			LoginURL: "/saml/" + url.PathEscape(provider.ProviderKey) + "/login"}
		certificates, err := saml.ParseCertificates(provider.Certificates)
		if err != nil {
			view.CertificateError = err.Error()
		}
		view.Certificates = len(certificates)
		views = append(views, view)
	}
	return views, nil
}

// SAMLProviderSettings adds, changes or removes a SAML identity provider.
func (h *Handler) SAMLProviderSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	back := func(message string) {
		http.Redirect(w, r, "/admin?saved="+url.QueryEscape(message)+"#admin-saml", http.StatusSeeOther)
	}
	providerKey := strings.TrimSpace(r.PostFormValue("key"))
	switch r.PostFormValue("action") {
	case "delete":
		if err := h.Store.DeleteSAMLProvider(r.Context(), workspaceID, user.ID, providerKey); err != nil {
			http.Error(w, "That identity provider is not configured here.", http.StatusNotFound)
			return
		}
		back("SAML provider removed")
		return
	case "enable", "disable":
		provider, err := h.Store.SAMLProvider(r.Context(), workspaceID, providerKey)
		if err != nil {
			http.Error(w, "That identity provider is not configured here.", http.StatusNotFound)
			return
		}
		provider.Enabled = r.PostFormValue("action") == "enable"
		if err := h.Store.SaveSAMLProvider(r.Context(), workspaceID, user.ID, provider); err != nil {
			http.Error(w, "Could not save that identity provider.", http.StatusInternalServerError)
			return
		}
		back("SAML provider saved")
		return
	}
	provider := store.SAMLProvider{
		ProviderKey:    providerKey,
		DisplayName:    strings.TrimSpace(r.PostFormValue("displayName")),
		EntityID:       strings.TrimSpace(r.PostFormValue("entityId")),
		SSOURL:         strings.TrimSpace(r.PostFormValue("ssoUrl")),
		Certificates:   strings.TrimSpace(r.PostFormValue("certificates")),
		EmailAttribute: strings.TrimSpace(r.PostFormValue("emailAttribute")),
		NameAttribute:  strings.TrimSpace(r.PostFormValue("nameAttribute")),
		Enabled:        true,
	}
	if !samlProviderKey.MatchString(provider.ProviderKey) {
		http.Error(w, "A provider key is lower-case letters, numbers and hyphens.", http.StatusBadRequest)
		return
	}
	if provider.DisplayName == "" || provider.EntityID == "" {
		http.Error(w, "A provider needs a name and the entity ID it calls itself.", http.StatusBadRequest)
		return
	}
	endpoint, err := url.Parse(provider.SSOURL)
	if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.Host == "" {
		http.Error(w, "The sign-in address is a URL the identity provider answers at.", http.StatusBadRequest)
		return
	}
	if _, err := saml.ParseCertificates(provider.Certificates); err != nil {
		http.Error(w, "The signing certificate could not be read: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.Store.SaveSAMLProvider(r.Context(), workspaceID, user.ID, provider); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	back(provider.DisplayName + " saved")
}

// samlProviderKey is what a provider may be called in an address.
var samlProviderKey = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
