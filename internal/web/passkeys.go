package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/webauthn"
)

// A security key or passkey is the other way to answer a sign-in this site
// holds: the browser asks the key to sign the challenge issued here, and the
// site checks the signature against the key the account registered.

// passkeyRelyingParty is the site a key is registered against: the host by
// itself, and the address a page must be at.
func (h *Handler) passkeyRelyingParty(r *http.Request) (rpID, origin string, err error) {
	base := strings.TrimSuffix(h.IdentityExternalURL, "/")
	if base == "" {
		base = strings.TrimSuffix(h.BaseURL, "/")
	}
	if base == "" {
		return "", "", errors.New("this site has no external address, so a security key cannot be registered against it: set ZZIRA_EXTERNAL_URL")
	}
	parsed, parseErr := url.Parse(base)
	if parseErr != nil || parsed.Host == "" {
		return "", "", errors.New("this site's external address is not a URL")
	}
	return parsed.Hostname(), parsed.Scheme + "://" + parsed.Host, nil
}

// passkeyJSON writes an answer a browser's credentials API can be handed.
func passkeyJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// PasskeyRegistrationOptions answers with what the browser needs to ask a key
// to register: the challenge this site issued, who the account is, and the
// keys it already has, so the same key is not registered twice.
func (h *Handler) PasskeyRegistrationOptions(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	rpID, _, err := h.passkeyRelyingParty(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	challenge, err := h.Store.NewWebAuthnChallenge(r.Context(), user.ID, "register")
	if err != nil {
		http.Error(w, "could not begin registering a key", http.StatusInternalServerError)
		return
	}
	existing, err := h.Store.WebAuthnCredentials(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "could not read the keys on this account", http.StatusInternalServerError)
		return
	}
	exclude := make([]map[string]any, 0, len(existing))
	for _, credential := range existing {
		exclude = append(exclude, map[string]any{"type": "public-key", "id": base64.RawURLEncoding.EncodeToString(credential.CredentialID)})
	}
	parameters := make([]map[string]any, 0, 2)
	for _, algorithm := range webauthn.Algorithms() {
		parameters = append(parameters, map[string]any{"type": "public-key", "alg": algorithm})
	}
	passkeyJSON(w, http.StatusOK, map[string]any{
		"challenge": base64.RawURLEncoding.EncodeToString(challenge),
		"rp":        map[string]any{"id": rpID, "name": "ZZIRA"},
		"user": map[string]any{
			"id":          base64.RawURLEncoding.EncodeToString([]byte(user.ID)),
			"name":        user.Email,
			"displayName": user.DisplayName,
		},
		"pubKeyCredParams":       parameters,
		"timeout":                int(store.WebAuthnChallengeTTL.Milliseconds()),
		"attestation":            "none",
		"excludeCredentials":     exclude,
		"authenticatorSelection": map[string]any{"userVerification": "preferred", "residentKey": "preferred"},
	})
}

// passkeyRegistration is what the page posts back after a key has been made.
type passkeyRegistration struct {
	RawID    string `json:"rawId"`
	Label    string `json:"label"`
	Response struct {
		ClientDataJSON    string `json:"clientDataJSON"`
		AttestationObject string `json:"attestationObject"`
	} `json:"response"`
}

// passkeyBodyLimit caps what a ceremony may post.
const passkeyBodyLimit = 256 << 10

// PasskeyRegister keeps the key the browser has just made.
func (h *Handler) PasskeyRegister(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	rpID, origin, err := h.passkeyRelyingParty(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	var body passkeyRegistration
	r.Body = http.MaxBytesReader(w, r.Body, passkeyBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "that is not a key this site can read", http.StatusBadRequest)
		return
	}
	clientDataJSON, attestation, err := passkeyBytes(body.Response.ClientDataJSON, body.Response.AttestationObject)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	challenge, err := passkeyChallengeOf(clientDataJSON)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.Store.ConsumeWebAuthnChallenge(r.Context(), challenge, user.ID, "register"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	credential, err := webauthn.Register(clientDataJSON, attestation, webauthn.Expectation{
		Challenge: challenge, Origin: origin, RPID: rpID,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.Store.SaveWebAuthnCredential(r.Context(), user.ID, store.WebAuthnCredential{
		CredentialID: credential.ID, PublicKey: credential.PublicKey, Label: body.Label,
		SignCount: credential.SignCount, UserVerified: credential.UserVerified,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	passkeyJSON(w, http.StatusCreated, map[string]any{"registered": true})
}

// PasskeyDelete takes a key off an account.
func (h *Handler) PasskeyDelete(w http.ResponseWriter, r *http.Request) {
	user, _, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	if err := h.Store.DeleteWebAuthnCredential(r.Context(), user.ID, r.PathValue("credential")); err != nil {
		http.Error(w, "That key is not on this account.", http.StatusNotFound)
		return
	}
	redirectLocal(w, r, "/people/"+url.PathEscape(user.ID)+"?saved="+url.QueryEscape("Security key removed")+"#two-step")
}

// PasskeySignInOptions answers with what the browser needs to ask a key to
// answer the sign-in this site is holding.
func (h *Handler) PasskeySignInOptions(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	challenge, ok := h.waitingSignIn(r)
	if !ok {
		http.Error(w, "no sign-in is waiting", http.StatusBadRequest)
		return
	}
	userID, err := h.Store.SignInChallengeUser(r.Context(), authn.SessionHash(challenge))
	if err != nil {
		http.Error(w, "no sign-in is waiting", http.StatusBadRequest)
		return
	}
	rpID, _, err := h.passkeyRelyingParty(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	credentials, err := h.Store.WebAuthnCredentials(r.Context(), userID)
	if err != nil || len(credentials) == 0 {
		http.Error(w, "this account has no security key", http.StatusBadRequest)
		return
	}
	keyChallenge, err := h.Store.NewWebAuthnChallenge(r.Context(), userID, "sign-in")
	if err != nil {
		http.Error(w, "could not begin the sign-in", http.StatusInternalServerError)
		return
	}
	allowed := make([]map[string]any, 0, len(credentials))
	for _, credential := range credentials {
		allowed = append(allowed, map[string]any{"type": "public-key", "id": base64.RawURLEncoding.EncodeToString(credential.CredentialID)})
	}
	passkeyJSON(w, http.StatusOK, map[string]any{
		"challenge":        base64.RawURLEncoding.EncodeToString(keyChallenge),
		"rpId":             rpID,
		"timeout":          int(store.WebAuthnChallengeTTL.Milliseconds()),
		"userVerification": "preferred",
		"allowCredentials": allowed,
	})
}

// passkeyAssertion is what the page posts back after a key has answered.
type passkeyAssertion struct {
	RawID    string `json:"rawId"`
	Response struct {
		ClientDataJSON    string `json:"clientDataJSON"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
	} `json:"response"`
}

// PasskeySignIn finishes a waiting sign-in with the key that answered it.
func (h *Handler) PasskeySignIn(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	signInChallenge, ok := h.waitingSignIn(r)
	if !ok {
		http.Error(w, "no sign-in is waiting", http.StatusBadRequest)
		return
	}
	userID, err := h.Store.SignInChallengeUser(r.Context(), authn.SessionHash(signInChallenge))
	if err != nil {
		http.Error(w, "no sign-in is waiting", http.StatusBadRequest)
		return
	}
	rpID, origin, err := h.passkeyRelyingParty(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	var body passkeyAssertion
	r.Body = http.MaxBytesReader(w, r.Body, passkeyBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "that is not an answer this site can read", http.StatusBadRequest)
		return
	}
	credentialID, err := base64.RawURLEncoding.DecodeString(body.RawID)
	if err != nil || len(credentialID) == 0 {
		http.Error(w, "that answer names no key", http.StatusBadRequest)
		return
	}
	credential, owner, err := h.Store.WebAuthnCredentialByID(r.Context(), credentialID)
	if err != nil || owner != userID {
		http.Error(w, "that key is not on this account", http.StatusUnauthorized)
		return
	}
	clientDataJSON, authenticatorData, err := passkeyBytes(body.Response.ClientDataJSON, body.Response.AuthenticatorData)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	signature, err := base64.RawURLEncoding.DecodeString(body.Response.Signature)
	if err != nil || len(signature) == 0 {
		http.Error(w, "the answer carries no signature", http.StatusBadRequest)
		return
	}
	keyChallenge, err := passkeyChallengeOf(clientDataJSON)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.Store.ConsumeWebAuthnChallenge(r.Context(), keyChallenge, userID, "sign-in"); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	count, err := webauthn.Verify(webauthn.Credential{
		ID: credential.CredentialID, PublicKey: credential.PublicKey, SignCount: credential.SignCount,
	}, clientDataJSON, authenticatorData, signature, webauthn.Expectation{
		Challenge: keyChallenge, Origin: origin, RPID: rpID,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if err := h.Store.WebAuthnCredentialUsed(r.Context(), credential.CredentialID, count); err != nil {
		http.Error(w, "could not finish the sign-in", http.StatusInternalServerError)
		return
	}
	// The second step has been answered, so the sign-in this site was holding
	// becomes a session.
	signIn, err := authn.CompleteEnrolledSignIn(r.Context(), h.Store, signInChallenge)
	if err != nil {
		http.Error(w, "could not finish the sign-in", http.StatusInternalServerError)
		return
	}
	clearSignInChallenge(w)
	authn.SetSessionCookieFor(w, signIn.Session, signIn.TTL)
	passkeyJSON(w, http.StatusOK, map[string]any{"signedIn": true})
}

// passkeyBytes reads the two base64url values a ceremony carries.
func passkeyBytes(first, second string) ([]byte, []byte, error) {
	one, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(one) == 0 {
		return nil, nil, errors.New("the browser's account of the ceremony is not base64url")
	}
	two, err := base64.RawURLEncoding.DecodeString(second)
	if err != nil || len(two) == 0 {
		return nil, nil, errors.New("what the key sent is not base64url")
	}
	return one, two, nil
}

// passkeyChallengeOf is the challenge the browser says the ceremony answered,
// which the site looks up before it reads anything else.
func passkeyChallengeOf(clientDataJSON []byte) ([]byte, error) {
	var data struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(clientDataJSON, &data); err != nil {
		return nil, errors.New("the browser's account of the ceremony cannot be read")
	}
	challenge, err := base64.RawURLEncoding.DecodeString(data.Challenge)
	if err != nil || len(challenge) == 0 {
		return nil, errors.New("that answer names no challenge")
	}
	return challenge, nil
}
