package saml

import (
	"bytes"
	"compress/flate"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"
)

// A sign-in begins with an AuthnRequest: this site asks the provider to say
// who somebody is, and remembers the request's id so that the answer can be
// held to it.

// AuthnRequest is what this site sends and what it must remember.
type AuthnRequest struct {
	// ID is what the answer must name as the request it belongs to.
	ID string
	// Redirect is where the person is sent to sign in, which carries the
	// request and the state this site keeps.
	Redirect string
}

// NewAuthnRequest writes the request that starts a sign-in, ready to be sent
// through the HTTP-Redirect binding: deflated, base64 and in the query, as
// the binding says.
func NewAuthnRequest(provider Provider, site ServiceProvider, relayState string, now time.Time) (AuthnRequest, error) {
	if strings.TrimSpace(provider.SSOURL) == "" {
		return AuthnRequest{}, errors.New("the identity provider has no sign-in address")
	}
	endpoint, err := url.Parse(provider.SSOURL)
	if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.Host == "" {
		return AuthnRequest{}, errors.New("the identity provider's sign-in address is not a URL")
	}
	id, err := newRequestID()
	if err != nil {
		return AuthnRequest{}, err
	}
	document := `<samlp:AuthnRequest xmlns:samlp="` + protocolNamespace + `" xmlns:saml="` + assertionNamespace + `"` +
		` ID="` + id + `" Version="2.0" IssueInstant="` + now.UTC().Format(time.RFC3339) + `"` +
		` Destination="` + html.EscapeString(provider.SSOURL) + `"` +
		` ProtocolBinding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST"` +
		` AssertionConsumerServiceURL="` + html.EscapeString(site.ACSURL) + `">` +
		`<saml:Issuer>` + html.EscapeString(site.EntityID) + `</saml:Issuer>` +
		`<samlp:NameIDPolicy Format="urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified" AllowCreate="true"/>` +
		`</samlp:AuthnRequest>`

	var deflated bytes.Buffer
	writer, err := flate.NewWriter(&deflated, flate.DefaultCompression)
	if err != nil {
		return AuthnRequest{}, err
	}
	if _, err := writer.Write([]byte(document)); err != nil {
		return AuthnRequest{}, err
	}
	if err := writer.Close(); err != nil {
		return AuthnRequest{}, err
	}
	query := endpoint.Query()
	query.Set("SAMLRequest", base64.StdEncoding.EncodeToString(deflated.Bytes()))
	if relayState != "" {
		query.Set("RelayState", relayState)
	}
	endpoint.RawQuery = query.Encode()
	return AuthnRequest{ID: id, Redirect: endpoint.String()}, nil
}

// newRequestID is an id an answer can be held to: random, and beginning with
// a letter, because a SAML id is an XML id.
func newRequestID() (string, error) {
	buffer := make([]byte, 20)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("could not start a sign-in: %w", err)
	}
	return "id" + hex.EncodeToString(buffer), nil
}

// Metadata describes this site to an identity provider: who it is and where
// it receives assertions.
func Metadata(site ServiceProvider) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="` + html.EscapeString(site.EntityID) + `">` +
		`<md:SPSSODescriptor AuthnRequestsSigned="false" WantAssertionsSigned="true" protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">` +
		`<md:NameIDFormat>urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress</md:NameIDFormat>` +
		`<md:AssertionConsumerService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location="` + html.EscapeString(site.ACSURL) + `" index="0" isDefault="true"/>` +
		`</md:SPSSODescriptor></md:EntityDescriptor>`)
}
