package saml

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// The namespaces a signed assertion is written in.
const (
	dsigNamespace      = "http://www.w3.org/2000/09/xmldsig#"
	assertionNamespace = "urn:oasis:names:tc:SAML:2.0:assertion"
	protocolNamespace  = "urn:oasis:names:tc:SAML:2.0:protocol"
)

// The algorithms a signature may be written with. SHA-1 is not among them:
// a signature nobody can forge is the only reason to believe an assertion,
// and SHA-1 collisions have been bought for thousands of dollars since 2017.
const (
	exclusiveC14N          = "http://www.w3.org/2001/10/xml-exc-c14n#"
	exclusiveC14NComments  = "http://www.w3.org/2001/10/xml-exc-c14n#WithComments"
	inclusiveC14N          = "http://www.w3.org/TR/2001/REC-xml-c14n-20010315"
	envelopedSignature     = "http://www.w3.org/2000/09/xmldsig#enveloped-signature"
	rsaSHA256              = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"
	rsaSHA384              = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha384"
	rsaSHA512              = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha512"
	digestSHA256           = "http://www.w3.org/2001/04/xmlenc#sha256"
	digestSHA384           = "http://www.w3.org/2001/04/xmldsig-more#sha384"
	digestSHA512           = "http://www.w3.org/2001/04/xmlenc#sha512"
	signatureValueMaxBytes = 4096
)

// signatureHashes maps each signature and digest algorithm to the hash it is
// written with. Anything else is refused by name.
var signatureHashes = map[string]crypto.Hash{
	rsaSHA256: crypto.SHA256, rsaSHA384: crypto.SHA384, rsaSHA512: crypto.SHA512,
	digestSHA256: crypto.SHA256, digestSHA384: crypto.SHA384, digestSHA512: crypto.SHA512,
}

// verifySignature checks the signature that envelopes one element: that it
// covers that element and nothing else, that the digest over the element is
// the one the signature says, and that the signature over the SignedInfo was
// made by one of the certificates the site was configured with.
//
// The certificate inside the document is never trusted. A signature verified
// with the key it carries says only that whoever wrote the document also
// signed it.
func verifySignature(signed *Node, certificates []*x509.Certificate) error {
	if len(certificates) == 0 {
		return errors.New("no certificate is configured for this identity provider")
	}
	signatures := signed.ChildrenNamed(dsigNamespace, "Signature")
	if len(signatures) != 1 {
		return fmt.Errorf("a signed %s carries one signature, not %d", signed.Local, len(signatures))
	}
	signature := signatures[0]
	signedInfo := signature.Child(dsigNamespace, "SignedInfo")
	if signedInfo == nil {
		return errors.New("the signature says nothing about what it signs")
	}
	method := algorithmOf(signedInfo.Child(dsigNamespace, "CanonicalizationMethod"))
	if method != exclusiveC14N && method != inclusiveC14N {
		return fmt.Errorf("a signature is read from exclusive canonical XML, not %q", method)
	}
	signatureAlgorithm := algorithmOf(signedInfo.Child(dsigNamespace, "SignatureMethod"))
	signatureHash, ok := signatureHashes[signatureAlgorithm]
	if !ok {
		return fmt.Errorf("a signature is made with RSA and SHA-256 or better, not %q", signatureAlgorithm)
	}

	references := signedInfo.ChildrenNamed(dsigNamespace, "Reference")
	if len(references) != 1 {
		return fmt.Errorf("a signature covers one element, not %d", len(references))
	}
	if err := verifyReference(signed, signature, references[0]); err != nil {
		return err
	}

	// What was signed is the SignedInfo, canonical as the signature says.
	canonicalSignedInfo := canonical(signedInfo, canonicalOptions{
		exclusive: method == exclusiveC14N,
		prefixes:  inclusivePrefixes(signedInfo.Child(dsigNamespace, "CanonicalizationMethod")),
	})
	raw := strings.TrimSpace(textOf(signature.Child(dsigNamespace, "SignatureValue")))
	if raw == "" || len(raw) > signatureValueMaxBytes {
		return errors.New("the signature carries no value")
	}
	signatureBytes, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(raw), ""))
	if err != nil {
		return errors.New("the signature value is not base64")
	}
	digest := hashBytes(signatureHash, canonicalSignedInfo)
	for _, certificate := range certificates {
		public, ok := certificate.PublicKey.(*rsa.PublicKey)
		if !ok {
			continue
		}
		if err := rsa.VerifyPKCS1v15(public, signatureHash, digest, signatureBytes); err == nil {
			return nil
		}
	}
	return errors.New("the signature was not made by a certificate this site trusts")
}

// verifyReference checks that the signature covers the element being read and
// that the digest over it holds. A signature that covers something else --
// another assertion, a copy of one moved elsewhere in the document -- is
// refused here, which is what signature wrapping tries.
func verifyReference(signed, signature, reference *Node) error {
	uri, _ := reference.Attr("URI")
	id, hasID := signed.Attr("ID")
	if uri == "" || !strings.HasPrefix(uri, "#") {
		return errors.New("a signature says which element it covers by id")
	}
	if !hasID || subtle.ConstantTimeCompare([]byte(uri[1:]), []byte(id)) != 1 {
		return fmt.Errorf("the signature covers %q rather than the %s being read", uri, signed.Local)
	}
	// The id must name this element and no other, so that what the signature
	// covers cannot be a copy somewhere else in the document.
	root := signed
	for root.Parent != nil {
		root = root.Parent
	}
	named, err := root.elementByID(id)
	if err != nil {
		return err
	}
	if named != signed {
		return errors.New("the signature covers another element with the same id")
	}

	transforms := reference.Child(dsigNamespace, "Transforms")
	if transforms == nil {
		return errors.New("a signature says how what it covers was written")
	}
	seenEnveloped, c14nMethod, prefixes := false, "", []string(nil)
	for _, transform := range transforms.ChildrenNamed(dsigNamespace, "Transform") {
		switch algorithm := algorithmOf(transform); algorithm {
		case envelopedSignature:
			seenEnveloped = true
		case exclusiveC14N, inclusiveC14N:
			c14nMethod, prefixes = algorithm, inclusivePrefixes(transform)
		case exclusiveC14NComments:
			return errors.New("a signature that keeps comments is not read here")
		default:
			return fmt.Errorf("a signature is read through enveloped-signature and canonical XML, not %q", algorithm)
		}
	}
	if !seenEnveloped {
		return errors.New("an enveloped signature leaves itself out of what it covers")
	}
	if c14nMethod == "" {
		c14nMethod = inclusiveC14N
	}
	digestAlgorithm := algorithmOf(reference.Child(dsigNamespace, "DigestMethod"))
	digestHash, ok := signatureHashes[digestAlgorithm]
	if !ok {
		return fmt.Errorf("a digest is SHA-256 or better, not %q", digestAlgorithm)
	}
	expected, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(textOf(reference.Child(dsigNamespace, "DigestValue"))), ""))
	if err != nil {
		return errors.New("the digest is not base64")
	}
	written := canonical(signed, canonicalOptions{exclusive: c14nMethod == exclusiveC14N, prefixes: prefixes, omit: signature})
	if subtle.ConstantTimeCompare(hashBytes(digestHash, written), expected) != 1 {
		return errors.New("the signed element has been changed since it was signed")
	}
	return nil
}

// inclusivePrefixes are the prefixes a transform asks to be treated as an
// inclusive canonicalization treats them.
func inclusivePrefixes(method *Node) []string {
	if method == nil {
		return nil
	}
	for _, child := range method.Children() {
		if child.Local != "InclusiveNamespaces" {
			continue
		}
		list, _ := child.Attr("PrefixList")
		return strings.Fields(list)
	}
	return nil
}

// algorithmOf reads a method element's Algorithm.
func algorithmOf(method *Node) string {
	if method == nil {
		return ""
	}
	algorithm, _ := method.Attr("Algorithm")
	return algorithm
}

// textOf is an element's text, or empty when the element is not there.
func textOf(node *Node) string {
	if node == nil {
		return ""
	}
	return node.Text()
}

// hashBytes is the digest of some bytes under one of the hashes above.
func hashBytes(hash crypto.Hash, value []byte) []byte {
	switch hash {
	case crypto.SHA384:
		sum := sha512.Sum384(value)
		return sum[:]
	case crypto.SHA512:
		sum := sha512.Sum512(value)
		return sum[:]
	default:
		sum := sha256.Sum256(value)
		return sum[:]
	}
}

// ParseCertificates reads the certificates a site trusts for one identity
// provider: PEM, or the bare base64 an identity provider's metadata carries.
func ParseCertificates(raw string) ([]*x509.Certificate, error) {
	certificates := []*x509.Certificate{}
	for _, block := range splitCertificates(raw) {
		der, err := base64.StdEncoding.DecodeString(block)
		if err != nil {
			return nil, errors.New("a certificate is base64 between BEGIN and END lines, or on its own")
		}
		certificate, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("read the certificate: %w", err)
		}
		if _, ok := certificate.PublicKey.(*rsa.PublicKey); !ok {
			return nil, errors.New("a signing certificate carries an RSA key")
		}
		certificates = append(certificates, certificate)
	}
	if len(certificates) == 0 {
		return nil, errors.New("no certificate was given")
	}
	return certificates, nil
}

// splitCertificates reads the base64 of each certificate out of PEM blocks,
// or takes the whole text as one certificate when it carries no PEM lines.
func splitCertificates(raw string) []string {
	blocks := []string{}
	current := strings.Builder{}
	inside := false
	sawBegin := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "-----BEGIN"):
			inside, sawBegin = true, true
			current.Reset()
		case strings.HasPrefix(line, "-----END"):
			if inside && current.Len() > 0 {
				blocks = append(blocks, current.String())
			}
			inside = false
		case inside:
			current.WriteString(line)
		}
	}
	if !sawBegin {
		if bare := strings.Join(strings.Fields(raw), ""); bare != "" {
			blocks = append(blocks, bare)
		}
	}
	return blocks
}
