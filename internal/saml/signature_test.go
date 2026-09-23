package saml

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

// selfSignedCertificate is a certificate that signed nothing this package
// reads, for asking what happens when the wrong one is configured.
func selfSignedCertificate(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "somebody else"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

// signedFixture is a real assertion, signed by Shibboleth with RSA-SHA256
// over exclusive canonical XML and an InclusiveNamespaces prefix list. It is
// the one the gosaml2 toolkit (Apache-2.0) keeps as testdata/saml.xml, with
// the certificate that signed it beside it. Reading it is how this package's
// canonicalization is checked against somebody else's signer rather than
// against itself.
func signedFixture(t *testing.T) *Node {
	t.Helper()
	raw, err := os.ReadFile("testdata/shibboleth_assertion.xml")
	if err != nil {
		t.Fatal(err)
	}
	document, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

// fixtureCertificates are what the site would have been configured with: the
// certificate of the provider that signed the fixture.
func fixtureCertificates(t *testing.T) []*x509.Certificate {
	t.Helper()
	pem, err := os.ReadFile("testdata/shibboleth_idp.crt")
	if err != nil {
		t.Fatal(err)
	}
	certificates, err := ParseCertificates(string(pem))
	if err != nil {
		t.Fatal(err)
	}
	return certificates
}

// certificateIn reads the certificate a document carries, which a test
// trusts because a test says so; nothing else does.
func certificateIn(t *testing.T, node *Node) string {
	t.Helper()
	signature := node.Child(dsigNamespace, "Signature")
	if signature == nil {
		t.Fatal("the element carries no signature")
	}
	keyInfo := signature.Child(dsigNamespace, "KeyInfo")
	if keyInfo == nil {
		t.Fatal("the signature carries no key")
	}
	data := keyInfo.Child(dsigNamespace, "X509Data")
	if data == nil {
		t.Fatal("the signature carries no certificate")
	}
	return data.Child(dsigNamespace, "X509Certificate").Text()
}

func TestASignatureFromAnotherImplementationVerifies(t *testing.T) {
	assertion := signedFixture(t)
	if err := verifySignature(assertion, fixtureCertificates(t)); err != nil {
		t.Fatalf("an assertion signed by somebody else: %v", err)
	}
	// What the assertion carries inside it is not signed on its own, and
	// saying it is is refused rather than ignored.
	subject := assertion.Child(assertionNamespace, "Subject")
	if subject == nil {
		t.Fatal("the assertion carries no subject")
	}
	if err := verifySignature(subject, fixtureCertificates(t)); err == nil {
		t.Fatal("an unsigned element verified")
	}
}

// A signature holds only while the bytes it covers do: a changed attribute, a
// changed word, or a signature that covers something else is refused.
func TestAChangedAssertionIsRefused(t *testing.T) {
	raw, err := os.ReadFile("testdata/shibboleth_assertion.xml")
	if err != nil {
		t.Fatal(err)
	}
	original := string(raw)
	for _, change := range []struct{ name, from, to string }{
		{"the audience", "https://sp.astuart.co/sp", "https://attacker.example.com/sp"},
		{"an attribute value", ">pcollegeadmin<", ">siteadmin<"},
		{"the assertion id", `ID="_c0c84b03960d4845b3931e7e429fe7fa"`, `ID="_c0c84b03960d4845b3931e7e429fe7fb"`},
		{"the subject", "NameQualifier=", "nameQualifier="},
	} {
		t.Run(change.name, func(t *testing.T) {
			if !strings.Contains(original, change.from) {
				t.Skipf("the fixture no longer carries %q", change.from)
			}
			assertion, err := Parse([]byte(strings.Replace(original, change.from, change.to, 1)))
			if err != nil {
				t.Fatal(err)
			}
			if err := verifySignature(assertion, fixtureCertificates(t)); err == nil {
				t.Fatal("a changed assertion verified")
			}
		})
	}
}

// A certificate that did not sign the document does not verify it, however
// well formed the signature is.
func TestAnotherCertificateDoesNotVerify(t *testing.T) {
	assertion := signedFixture(t)
	// A certificate of the test's own making, which signed nothing here.
	other := selfSignedCertificate(t)
	if err := verifySignature(assertion, []*x509.Certificate{other}); err == nil {
		t.Fatal("the assertion verified with a certificate that did not sign it")
	}
}

// A certificate is read as PEM or as the bare base64 an identity provider's
// metadata carries, and nothing else.
func TestCertificatesAreRead(t *testing.T) {
	bare := strings.Join(strings.Fields(certificateIn(t, signedFixture(t))), "")
	if _, err := ParseCertificates(bare); err != nil {
		t.Fatalf("bare base64: %v", err)
	}
	pem := "-----BEGIN CERTIFICATE-----\n" + bare + "\n-----END CERTIFICATE-----\n"
	if _, err := ParseCertificates(pem); err != nil {
		t.Fatalf("PEM: %v", err)
	}
	if _, err := ParseCertificates(pem + pem); err != nil {
		t.Fatalf("two certificates: %v", err)
	}
	if _, err := ParseCertificates("not a certificate"); err == nil {
		t.Fatal("text that is not a certificate was read as one")
	}
	if _, err := ParseCertificates(base64.StdEncoding.EncodeToString([]byte("not DER"))); err == nil {
		t.Fatal("base64 that is not a certificate was read as one")
	}
}
