package saml

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"strings"
	"testing"
	"time"
)

// signer is an identity provider of the test's own, for asking what a
// response says rather than whether a signature holds -- which the fixture
// signed by somebody else answers.
type signer struct {
	key          *rsa.PrivateKey
	certificate  *x509.Certificate
	certificates []*x509.Certificate
}

func newSigner(t *testing.T) *signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "identity provider"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &signer{key: key, certificate: certificate, certificates: []*x509.Certificate{certificate}}
}

// sign writes the enveloped signature over the element the id names, exactly
// as an identity provider does, and answers the whole document base64 as it
// would be posted.
func (s *signer) sign(t *testing.T, document, id string) string {
	t.Helper()
	parsed, err := Parse([]byte(document))
	if err != nil {
		t.Fatalf("the test's own document: %v", err)
	}
	element, err := parsed.elementByID(id)
	if err != nil {
		t.Fatal(err)
	}
	digest := hashBytes(crypto.SHA256, canonical(element, canonicalOptions{exclusive: true}))
	body := `<ds:CanonicalizationMethod Algorithm="` + exclusiveC14N + `"></ds:CanonicalizationMethod>` +
		`<ds:SignatureMethod Algorithm="` + rsaSHA256 + `"></ds:SignatureMethod>` +
		`<ds:Reference URI="#` + id + `"><ds:Transforms>` +
		`<ds:Transform Algorithm="` + envelopedSignature + `"></ds:Transform>` +
		`<ds:Transform Algorithm="` + exclusiveC14N + `"></ds:Transform>` +
		`</ds:Transforms><ds:DigestMethod Algorithm="` + digestSHA256 + `"></ds:DigestMethod>` +
		`<ds:DigestValue>` + base64.StdEncoding.EncodeToString(digest) + `</ds:DigestValue></ds:Reference>`
	// What is signed is the SignedInfo as exclusive canonical XML, which is
	// the same whether the ds prefix is declared on it or on the signature
	// around it, because that is what exclusive canonicalization is for.
	standalone, err := Parse([]byte(`<ds:SignedInfo xmlns:ds="` + dsigNamespace + `">` + body + `</ds:SignedInfo>`))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256,
		hashBytes(crypto.SHA256, canonical(standalone, canonicalOptions{exclusive: true})))
	if err != nil {
		t.Fatal(err)
	}
	block := `<ds:Signature xmlns:ds="` + dsigNamespace + `"><ds:SignedInfo>` + body + `</ds:SignedInfo>` +
		`<ds:SignatureValue>` + base64.StdEncoding.EncodeToString(signature) + `</ds:SignatureValue></ds:Signature>`
	// The signature goes after the element's Issuer, where a provider puts it.
	marker := `<saml:Issuer>` + issuerEntityID + `</saml:Issuer>`
	at := strings.Index(document, `ID="`+id+`"`)
	if at < 0 {
		t.Fatalf("the document does not carry %q", id)
	}
	after := strings.Index(document[at:], marker)
	if after < 0 {
		t.Fatal("the element carries no issuer to put the signature after")
	}
	insert := at + after + len(marker)
	return base64.StdEncoding.EncodeToString([]byte(document[:insert] + block + document[insert:]))
}

const (
	issuerEntityID = "https://idp.example.test/metadata"
	siteEntityID   = "https://zzira.test/saml/metadata"
	siteACS        = "https://zzira.test/saml/acs"
)

// responseDocument is what an identity provider posts, with the pieces a test
// changes one at a time.
type responseDocument struct {
	requestID, audience, destination, recipient string
	notBefore, notOnOrAfter                     string
	// conditionsEnd is when the assertion itself stops holding, when that is
	// not when its confirmation does.
	conditionsEnd string
	status        string
	email, name   string
	assertionID   string
}

func (d responseDocument) xml() string {
	if d.assertionID == "" {
		d.assertionID = "assertion-1"
	}
	if d.status == "" {
		d.status = "urn:oasis:names:tc:SAML:2.0:status:Success"
	}
	return `<samlp:Response xmlns:samlp="` + protocolNamespace + `" xmlns:saml="` + assertionNamespace + `"` +
		` ID="response-1" Version="2.0" IssueInstant="2026-09-23T10:00:00Z" Destination="` + d.destination + `" InResponseTo="` + d.requestID + `">` +
		`<saml:Issuer>` + issuerEntityID + `</saml:Issuer>` +
		`<samlp:Status><samlp:StatusCode Value="` + d.status + `"></samlp:StatusCode></samlp:Status>` +
		`<saml:Assertion ID="` + d.assertionID + `" Version="2.0" IssueInstant="2026-09-23T10:00:00Z">` +
		`<saml:Issuer>` + issuerEntityID + `</saml:Issuer>` +
		`<saml:Subject><saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">` + d.email + `</saml:NameID>` +
		`<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer">` +
		`<saml:SubjectConfirmationData InResponseTo="` + d.requestID + `" NotOnOrAfter="` + d.notOnOrAfter + `" Recipient="` + d.recipient + `"></saml:SubjectConfirmationData>` +
		`</saml:SubjectConfirmation></saml:Subject>` +
		`<saml:Conditions NotBefore="` + d.notBefore + `" NotOnOrAfter="` + conditionsEndOf(d) + `">` +
		`<saml:AudienceRestriction><saml:Audience>` + d.audience + `</saml:Audience></saml:AudienceRestriction></saml:Conditions>` +
		`<saml:AuthnStatement AuthnInstant="2026-09-23T10:00:00Z" SessionIndex="session-9">` +
		`<saml:AuthnContext><saml:AuthnContextClassRef>urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport</saml:AuthnContextClassRef></saml:AuthnContext></saml:AuthnStatement>` +
		`<saml:AttributeStatement>` +
		`<saml:Attribute Name="http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name"><saml:AttributeValue>` + d.name + `</saml:AttributeValue></saml:Attribute>` +
		`<saml:Attribute Name="groups"><saml:AttributeValue>platform</saml:AttributeValue><saml:AttributeValue>oncall</saml:AttributeValue></saml:Attribute>` +
		`</saml:AttributeStatement>` +
		`</saml:Assertion></samlp:Response>`
}

// conditionsEndOf is when the assertion stops holding, which is when its
// confirmation does unless a test says otherwise.
func conditionsEndOf(d responseDocument) string {
	if d.conditionsEnd != "" {
		return d.conditionsEnd
	}
	return d.notOnOrAfter
}

func goodDocument() responseDocument {
	return responseDocument{
		requestID: "id-request-1", audience: siteEntityID, destination: siteACS, recipient: siteACS,
		notBefore: "2026-09-23T09:55:00Z", notOnOrAfter: "2026-09-23T10:05:00Z",
		email: "ana@example.test", name: "Ana Soursop",
	}
}

func readingTime() time.Time { return time.Date(2026, time.September, 23, 10, 1, 0, 0, time.UTC) }

// An assertion that is signed, for this site, about this sign-in and in date
// says who signed in.
func TestASignedAssertionSaysWhoSignedIn(t *testing.T) {
	idp := newSigner(t)
	document := goodDocument()
	posted := idp.sign(t, document.xml(), document.assertionIDOrDefault())
	identity, err := ReadResponse(posted, Provider{EntityID: issuerEntityID, Certificates: idp.certificates},
		ServiceProvider{EntityID: siteEntityID, ACSURL: siteACS}, document.requestID, readingTime())
	if err != nil {
		t.Fatalf("a good assertion: %v", err)
	}
	if identity.Email != "ana@example.test" || identity.DisplayName != "Ana Soursop" {
		t.Fatalf("identity = %+v", identity)
	}
	if identity.SessionIndex != "session-9" || identity.Issuer != issuerEntityID {
		t.Fatalf("identity = %+v", identity)
	}
	if groups := identity.Attributes["groups"]; len(groups) != 2 || groups[0] != "platform" {
		t.Fatalf("groups = %v", groups)
	}
}

// What an answer must be for this site, about this sign-in, and in date.
func TestAnAssertionIsHeldToWhatItSays(t *testing.T) {
	idp := newSigner(t)
	site := ServiceProvider{EntityID: siteEntityID, ACSURL: siteACS}
	provider := Provider{EntityID: issuerEntityID, Certificates: idp.certificates}
	for _, tc := range []struct {
		name      string
		change    func(*responseDocument)
		requestID string
		says      string
	}{
		{"another site's audience", func(d *responseDocument) { d.audience = "https://elsewhere.test/saml" }, "id-request-1", "not for this site"},
		{"another address", func(d *responseDocument) { d.destination = "https://elsewhere.test/acs" }, "id-request-1", "addressed to"},
		{"another recipient", func(d *responseDocument) { d.recipient = "https://elsewhere.test/acs" }, "id-request-1", "not confirmed"},
		{"another sign-in", func(d *responseDocument) { d.requestID = "id-someone-else" }, "id-request-1", "belongs to another sign-in"},
		{"no sign-in at all", func(d *responseDocument) { d.requestID = "" }, "id-request-1", "does not say which sign-in"},
		{"a confirmation that has run out", func(d *responseDocument) { d.notOnOrAfter = "2026-09-23T09:56:00Z" }, "id-request-1", "not confirmed"},
		{"expired", func(d *responseDocument) { d.conditionsEnd = "2026-09-23T09:56:00Z" }, "id-request-1", "expired"},
		{"not yet", func(d *responseDocument) { d.notBefore = "2026-09-23T10:30:00Z" }, "id-request-1", "does not hold yet"},
		{"refused", func(d *responseDocument) { d.status = "urn:oasis:names:tc:SAML:2.0:status:Responder" }, "id-request-1", "refused the sign-in"},
		{"nobody", func(d *responseDocument) { d.email = "" }, "id-request-1", "names nobody"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := goodDocument()
			tc.change(&document)
			posted := idp.sign(t, document.xml(), document.assertionIDOrDefault())
			_, err := ReadResponse(posted, provider, site, tc.requestID, readingTime())
			if err == nil {
				t.Fatal("the assertion was read")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("error %q does not say %q", err, tc.says)
			}
		})
	}
}

// An unsigned assertion is worth nothing, and neither is one signed by
// somebody else.
func TestAnUnsignedOrForeignAssertionIsRefused(t *testing.T) {
	idp, other := newSigner(t), newSigner(t)
	site := ServiceProvider{EntityID: siteEntityID, ACSURL: siteACS}
	document := goodDocument()
	unsigned := base64.StdEncoding.EncodeToString([]byte(document.xml()))
	if _, err := ReadResponse(unsigned, Provider{EntityID: issuerEntityID, Certificates: idp.certificates}, site, document.requestID, readingTime()); err == nil {
		t.Fatal("an unsigned response was read")
	}
	posted := idp.sign(t, document.xml(), document.assertionIDOrDefault())
	if _, err := ReadResponse(posted, Provider{EntityID: issuerEntityID, Certificates: other.certificates}, site, document.requestID, readingTime()); err == nil {
		t.Fatal("an assertion signed by somebody else was read")
	}
	// An answer from a provider this site does not sign in through.
	if _, err := ReadResponse(posted, Provider{EntityID: "https://attacker.test/metadata", Certificates: idp.certificates}, site, document.requestID, readingTime()); err == nil {
		t.Fatal("an answer from another provider was read")
	}
}

// A provider that answers without saying how the sign-in went is refused in
// words, not by falling over: the answer is read before anyone has signed in,
// so nothing an unsigned answer holds may take the reading down.
func TestAStatusWithoutACodeIsRefusedRatherThanFatal(t *testing.T) {
	site := ServiceProvider{EntityID: siteEntityID, ACSURL: siteACS}
	idp := newSigner(t)
	document := `<samlp:Response xmlns:samlp="` + protocolNamespace + `" xmlns:saml="` + assertionNamespace + `"` +
		` ID="response-1" Version="2.0" IssueInstant="2026-09-23T10:00:00Z" InResponseTo="id-request-1">` +
		`<samlp:Status><samlp:StatusMessage>later</samlp:StatusMessage></samlp:Status></samlp:Response>`
	posted := base64.StdEncoding.EncodeToString([]byte(document))
	_, err := ReadResponse(posted, Provider{EntityID: issuerEntityID, Certificates: idp.certificates}, site, "id-request-1", readingTime())
	if err == nil {
		t.Fatal("the answer was read")
	}
	if !strings.Contains(err.Error(), "refused the sign-in") {
		t.Fatalf("error %q does not say the sign-in was refused", err)
	}
}

// Signature wrapping: a second assertion smuggled in beside the signed one,
// hoping the reader takes the wrong one.
func TestASmuggledAssertionIsRefused(t *testing.T) {
	idp := newSigner(t)
	document := goodDocument()
	posted := idp.sign(t, document.xml(), document.assertionIDOrDefault())
	raw, err := base64.StdEncoding.DecodeString(posted)
	if err != nil {
		t.Fatal(err)
	}
	signed := string(raw)
	forged := strings.Replace(goodDocument().xml(), "ana@example.test", "attacker@example.test", 1)
	assertion := forged[strings.Index(forged, "<saml:Assertion"):strings.Index(forged, "</samlp:Response>")]
	smuggled := strings.Replace(signed, "<saml:Assertion", assertion+"<saml:Assertion", 1)
	if _, err := ReadResponse(base64.StdEncoding.EncodeToString([]byte(smuggled)),
		Provider{EntityID: issuerEntityID, Certificates: idp.certificates},
		ServiceProvider{EntityID: siteEntityID, ACSURL: siteACS}, document.requestID, readingTime()); err == nil {
		t.Fatal("a response carrying a forged assertion beside the signed one was read")
	}
}

// A site can say which attributes carry the address and the name, for a
// provider that sends them under names of its own.
func TestAProviderSaysWhichAttributesToRead(t *testing.T) {
	idp := newSigner(t)
	document := goodDocument()
	document.email = "not-an-address"
	posted := idp.sign(t, strings.Replace(document.xml(),
		`<saml:Attribute Name="groups">`,
		`<saml:Attribute Name="work-email"><saml:AttributeValue>ana@work.test</saml:AttributeValue></saml:Attribute><saml:Attribute Name="groups">`, 1),
		document.assertionIDOrDefault())
	identity, err := ReadResponse(posted, Provider{
		EntityID: issuerEntityID, Certificates: idp.certificates, EmailAttribute: "work-email", NameAttribute: "groups",
	}, ServiceProvider{EntityID: siteEntityID, ACSURL: siteACS}, document.requestID, readingTime())
	if err != nil {
		t.Fatalf("reading the attributes a provider was configured with: %v", err)
	}
	if identity.Email != "ana@work.test" || identity.DisplayName != "platform" {
		t.Fatalf("identity = %+v", identity)
	}
}

// assertionIDOrDefault is the id the document's assertion carries.
func (d responseDocument) assertionIDOrDefault() string {
	if d.assertionID == "" {
		return "assertion-1"
	}
	return d.assertionID
}
