package saml

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Provider is one identity provider this site signs people in through: who it
// says it is, where it answers, and the certificates it signs with.
type Provider struct {
	// EntityID is what the provider calls itself, and what an assertion must
	// say it came from.
	EntityID string
	// SSOURL is where a person is sent to sign in.
	SSOURL string
	// Certificates are the certificates the provider signs with; a provider
	// rolling a key over is configured with both.
	Certificates []*x509.Certificate
	// EmailAttribute and NameAttribute are the attributes an assertion
	// carries the person's address and name in, when it does not use the
	// usual ones.
	EmailAttribute, NameAttribute string
}

// ServiceProvider is this site as the identity provider sees it.
type ServiceProvider struct {
	// EntityID is what this site calls itself, which an assertion's audience
	// must name.
	EntityID string
	// ACSURL is where the provider posts its answer.
	ACSURL string
}

// Identity is who an assertion says signed in.
type Identity struct {
	// Issuer is the provider that said so, Subject the name it knows the
	// person by, and SessionIndex the session it opened, which a logout
	// message names. AssertionID is what the assertion calls itself, which
	// is how a site reads one once.
	Issuer, Subject, SessionIndex, AssertionID string
	Email, DisplayName                         string
	// Attributes are every attribute the assertion carried, by name, so a
	// site can read what it configured its provider to send.
	Attributes map[string][]string
	// NotOnOrAfter is when the provider says the session it opened ends,
	// zero when it says nothing.
	NotOnOrAfter time.Time
}

// responseMaxBytes caps the answer a provider may post. A signed assertion is
// a few kilobytes; anything far larger is not one.
const responseMaxBytes = 512 << 10

// clockSkew is how far apart this site's clock and a provider's may be before
// an assertion is refused for arriving too early or too late.
const clockSkew = 2 * time.Minute

// ReadResponse reads what an identity provider posted back: it checks the
// signature, then everything the signature makes worth checking -- who it is
// from, who it is for, when it holds, and that it answers the request this
// site sent.
//
// requestID is the AuthnRequest this site sent, empty when the provider
// started the sign-in itself, which is refused unless the site allows it.
func ReadResponse(encoded string, provider Provider, site ServiceProvider, requestID string, now time.Time) (*Identity, error) {
	if len(encoded) > responseMaxBytes {
		return nil, errors.New("that answer is too large to be an assertion")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(encoded), ""))
	if err != nil {
		return nil, errors.New("the answer is not base64")
	}
	document, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if document.Namespace() != protocolNamespace || document.Local != "Response" {
		return nil, errors.New("that is not a SAML response")
	}
	if issuer := textOf(document.Child(assertionNamespace, "Issuer")); issuer != "" && issuer != provider.EntityID {
		return nil, fmt.Errorf("the answer says it is from %q, and this site signs in through %q", issuer, provider.EntityID)
	}
	if destination, ok := document.Attr("Destination"); ok && destination != "" && destination != site.ACSURL {
		return nil, fmt.Errorf("the answer was addressed to %q rather than to this site", destination)
	}
	if inResponseTo, ok := document.Attr("InResponseTo"); ok && inResponseTo != "" && inResponseTo != requestID {
		return nil, errors.New("the answer belongs to another sign-in")
	} else if requestID != "" && (!ok || inResponseTo == "") {
		return nil, errors.New("the answer does not say which sign-in it belongs to")
	}
	if err := readStatus(document); err != nil {
		return nil, err
	}

	// A response may be signed, an assertion may be signed, and only a signed
	// assertion is worth reading: the whole point of the exercise is that
	// nobody but the provider could have written it.
	assertions := document.ChildrenNamed(assertionNamespace, "Assertion")
	if len(assertions) != 1 {
		return nil, fmt.Errorf("an answer carries one assertion, not %d", len(assertions))
	}
	assertion := assertions[0]
	responseSigned := len(document.ChildrenNamed(dsigNamespace, "Signature")) == 1
	assertionSigned := len(assertion.ChildrenNamed(dsigNamespace, "Signature")) == 1
	if !responseSigned && !assertionSigned {
		return nil, errors.New("the answer carries no signature")
	}
	if responseSigned {
		if err := verifySignature(document, provider.Certificates); err != nil {
			return nil, fmt.Errorf("the answer's signature: %w", err)
		}
	}
	if assertionSigned {
		if err := verifySignature(assertion, provider.Certificates); err != nil {
			return nil, fmt.Errorf("the assertion's signature: %w", err)
		}
	} else if !responseSigned {
		return nil, errors.New("the assertion is not signed")
	}
	if textOf(assertion.Child(assertionNamespace, "Issuer")) != provider.EntityID {
		return nil, errors.New("the assertion says it is from somebody else")
	}
	if encrypted := document.Child(assertionNamespace, "EncryptedAssertion"); encrypted != nil {
		return nil, errors.New("an encrypted assertion is not read here")
	}

	identity, err := readAssertion(assertion, site, requestID, now)
	if err != nil {
		return nil, err
	}
	identity.Issuer = provider.EntityID
	identity.AssertionID, _ = assertion.Attr("ID")
	if provider.EmailAttribute != "" {
		if values := identity.Attributes[provider.EmailAttribute]; len(values) > 0 {
			identity.Email = strings.TrimSpace(values[0])
		}
	}
	if provider.NameAttribute != "" {
		if values := identity.Attributes[provider.NameAttribute]; len(values) > 0 {
			identity.DisplayName = strings.TrimSpace(values[0])
		}
	}
	if identity.Email == "" {
		return nil, errors.New("the assertion says nothing this site can use as an email address")
	}
	return identity, nil
}

// InResponseTo is the sign-in an answer says it belongs to, read without
// checking anything: a site looks the sign-in up before it reads the answer,
// and reads nothing at all when it began no such sign-in.
func InResponseTo(encoded string) string {
	if len(encoded) > responseMaxBytes {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(encoded), ""))
	if err != nil {
		return ""
	}
	document, err := Parse(raw)
	if err != nil || document.Local != "Response" {
		return ""
	}
	id, _ := document.Attr("InResponseTo")
	return id
}

// readStatus refuses an answer that says the sign-in did not happen, with
// what the provider said about it.
func readStatus(document *Node) error {
	status := document.Child(protocolNamespace, "Status")
	if status == nil {
		return errors.New("the answer says nothing about whether the sign-in worked")
	}
	code := status.Child(protocolNamespace, "StatusCode")
	value := ""
	if code != nil {
		value, _ = code.Attr("Value")
	}
	if value == "urn:oasis:names:tc:SAML:2.0:status:Success" {
		return nil
	}
	said := strings.TrimSpace(textOf(status.Child(protocolNamespace, "StatusMessage")))
	if inner := code.Child(protocolNamespace, "StatusCode"); inner != nil && said == "" {
		said, _ = inner.Attr("Value")
	}
	if said == "" {
		said = value
	}
	return fmt.Errorf("the identity provider refused the sign-in: %s", said)
}

// readAssertion reads a signed assertion: who it is about, who it is for, and
// when it holds.
func readAssertion(assertion *Node, site ServiceProvider, requestID string, now time.Time) (*Identity, error) {
	identity := &Identity{Attributes: map[string][]string{}}
	subject := assertion.Child(assertionNamespace, "Subject")
	if subject == nil {
		return nil, errors.New("the assertion says nothing about who signed in")
	}
	nameID := subject.Child(assertionNamespace, "NameID")
	if nameID == nil {
		return nil, errors.New("the assertion names nobody")
	}
	identity.Subject = strings.TrimSpace(nameID.Text())
	if identity.Subject == "" {
		return nil, errors.New("the assertion names nobody")
	}
	if format, _ := nameID.Attr("Format"); strings.HasSuffix(format, "emailAddress") {
		identity.Email = identity.Subject
	}

	// The assertion must be for this site, now, and about this sign-in.
	confirmed := false
	for _, confirmation := range subject.ChildrenNamed(assertionNamespace, "SubjectConfirmation") {
		method, _ := confirmation.Attr("Method")
		if method != "urn:oasis:names:tc:SAML:2.0:cm:bearer" {
			continue
		}
		data := confirmation.Child(assertionNamespace, "SubjectConfirmationData")
		if data == nil {
			continue
		}
		if recipient, ok := data.Attr("Recipient"); ok && recipient != "" && recipient != site.ACSURL {
			continue
		}
		if inResponseTo, ok := data.Attr("InResponseTo"); ok && inResponseTo != "" && inResponseTo != requestID {
			continue
		}
		if requestID != "" {
			if inResponseTo, ok := data.Attr("InResponseTo"); !ok || inResponseTo != requestID {
				continue
			}
		}
		if raw, ok := data.Attr("NotOnOrAfter"); ok {
			deadline, err := time.Parse(time.RFC3339, raw)
			if err != nil || !now.Add(-clockSkew).Before(deadline) {
				continue
			}
		}
		if raw, ok := data.Attr("NotBefore"); ok {
			from, err := time.Parse(time.RFC3339, raw)
			if err != nil || now.Add(clockSkew).Before(from) {
				continue
			}
		}
		confirmed = true
	}
	if !confirmed {
		return nil, errors.New("the assertion is not confirmed for this site and this sign-in")
	}

	if conditions := assertion.Child(assertionNamespace, "Conditions"); conditions != nil {
		if raw, ok := conditions.Attr("NotBefore"); ok {
			from, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return nil, errors.New("the assertion says when it starts in a way this site cannot read")
			}
			if now.Add(clockSkew).Before(from) {
				return nil, errors.New("the assertion does not hold yet")
			}
		}
		if raw, ok := conditions.Attr("NotOnOrAfter"); ok {
			deadline, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return nil, errors.New("the assertion says when it ends in a way this site cannot read")
			}
			if !now.Add(-clockSkew).Before(deadline) {
				return nil, errors.New("the assertion has expired")
			}
		}
		audiences := []string{}
		for _, restriction := range conditions.ChildrenNamed(assertionNamespace, "AudienceRestriction") {
			for _, audience := range restriction.ChildrenNamed(assertionNamespace, "Audience") {
				audiences = append(audiences, strings.TrimSpace(audience.Text()))
			}
		}
		if len(audiences) > 0 {
			named := false
			for _, audience := range audiences {
				named = named || audience == site.EntityID
			}
			if !named {
				return nil, fmt.Errorf("the assertion is for %s, not for this site", strings.Join(audiences, ", "))
			}
		}
	}

	for _, statement := range assertion.ChildrenNamed(assertionNamespace, "AuthnStatement") {
		if index, ok := statement.Attr("SessionIndex"); ok {
			identity.SessionIndex = index
		}
		if raw, ok := statement.Attr("SessionNotOnOrAfter"); ok {
			if deadline, err := time.Parse(time.RFC3339, raw); err == nil {
				identity.NotOnOrAfter = deadline
			}
		}
	}

	for _, statement := range assertion.ChildrenNamed(assertionNamespace, "AttributeStatement") {
		for _, attribute := range statement.ChildrenNamed(assertionNamespace, "Attribute") {
			name, _ := attribute.Attr("Name")
			friendly, _ := attribute.Attr("FriendlyName")
			values := []string{}
			for _, value := range attribute.ChildrenNamed(assertionNamespace, "AttributeValue") {
				values = append(values, strings.TrimSpace(value.Text()))
			}
			if name != "" {
				identity.Attributes[name] = append(identity.Attributes[name], values...)
			}
			if friendly != "" {
				identity.Attributes[friendly] = append(identity.Attributes[friendly], values...)
			}
		}
	}
	// The usual names an identity provider sends an address and a name under.
	if identity.Email == "" {
		identity.Email = firstAttribute(identity.Attributes,
			"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress",
			"urn:oid:0.9.2342.19200300.100.1.3", "email", "mail", "Email", "EmailAddress")
	}
	if identity.DisplayName == "" {
		identity.DisplayName = firstAttribute(identity.Attributes,
			"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name",
			"http://schemas.microsoft.com/identity/claims/displayname",
			"urn:oid:2.16.840.1.113730.3.1.241", "displayName", "name", "cn", "DisplayName")
	}
	identity.Email = strings.ToLower(strings.TrimSpace(identity.Email))
	return identity, nil
}

// firstAttribute is the first of these attributes the assertion carried.
func firstAttribute(attributes map[string][]string, names ...string) string {
	for _, name := range names {
		if values := attributes[name]; len(values) > 0 && strings.TrimSpace(values[0]) != "" {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}
