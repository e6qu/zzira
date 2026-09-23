package webauthn

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// Credential is a security key or passkey an account has registered.
type Credential struct {
	// ID is what the authenticator calls this credential, and what a sign-in
	// names to use it.
	ID []byte
	// PublicKey is the COSE key the authenticator wrote, kept as it arrived
	// so the site verifies with exactly what was registered.
	PublicKey []byte
	// SignCount is what the authenticator's counter said. An authenticator
	// that counts must count upwards; one that does not stays at zero.
	SignCount uint32
	// UserVerified says the person proved they were there with more than a
	// touch -- a PIN, a fingerprint -- when they registered it.
	UserVerified bool
	// AAGUID says what kind of authenticator it is, as the authenticator
	// reports itself.
	AAGUID []byte
}

// Expectation is what a ceremony must answer to: the challenge this site
// issued, where the page was, and which site the key is for.
type Expectation struct {
	Challenge []byte
	Origin    string
	RPID      string
	// RequireUserVerification asks for more than presence: a PIN or a
	// fingerprint, which is what a second factor is for.
	RequireUserVerification bool
}

// The flags an authenticator sets in the data it signs.
const (
	flagUserPresent  = 1 << 0
	flagUserVerified = 1 << 2
	flagAttestedData = 1 << 6
)

// The COSE algorithms this site registers keys for.
const (
	algorithmES256 = -7
	algorithmRS256 = -257
)

// Algorithms are the algorithms this site asks a browser for, in the order it
// prefers them.
func Algorithms() []int { return []int{algorithmES256, algorithmRS256} }

// clientData is what the browser says the ceremony was.
type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// Register reads what a browser handed back when a key was registered, and
// answers with the credential to keep.
func Register(clientDataJSON, attestationObject []byte, expect Expectation) (Credential, error) {
	if err := checkClientData(clientDataJSON, "webauthn.create", expect); err != nil {
		return Credential{}, err
	}
	attestation, rest, err := decodeCBOR(attestationObject)
	if err != nil {
		return Credential{}, fmt.Errorf("read what the key sent: %w", err)
	}
	if len(rest) != 0 || attestation.kind != cborMap {
		return Credential{}, errors.New("what the key sent is not an attestation object")
	}
	authDataValue, ok := attestation.value("authData")
	if !ok || authDataValue.kind != cborBytes {
		return Credential{}, errors.New("the key sent no authenticator data")
	}
	authenticator, err := readAuthenticatorData(authDataValue.bytes)
	if err != nil {
		return Credential{}, err
	}
	if err := checkAuthenticator(authenticator, expect); err != nil {
		return Credential{}, err
	}
	if authenticator.credentialID == nil || authenticator.publicKey == nil {
		return Credential{}, errors.New("the key sent no credential to keep")
	}
	// The key is read now rather than at the first sign-in, so a key this
	// site cannot verify with is refused while somebody is watching.
	if _, err := publicKeyFrom(authenticator.publicKey); err != nil {
		return Credential{}, err
	}
	return Credential{
		ID: authenticator.credentialID, PublicKey: authenticator.publicKey,
		SignCount: authenticator.signCount, UserVerified: authenticator.userVerified,
		AAGUID: authenticator.aaguid,
	}, nil
}

// Verify reads what a browser handed back when a key answered a sign-in, and
// answers with the counter to keep.
func Verify(credential Credential, clientDataJSON, authenticatorData, signature []byte, expect Expectation) (uint32, error) {
	if err := checkClientData(clientDataJSON, "webauthn.get", expect); err != nil {
		return 0, err
	}
	authenticator, err := readAuthenticatorData(authenticatorData)
	if err != nil {
		return 0, err
	}
	if err := checkAuthenticator(authenticator, expect); err != nil {
		return 0, err
	}
	public, err := publicKeyFrom(credential.PublicKey)
	if err != nil {
		return 0, err
	}
	// What the key signed is the data it wrote, with the hash of what the
	// browser said the ceremony was.
	clientHash := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authenticatorData...), clientHash[:]...)
	digest := sha256.Sum256(signed)
	switch key := public.(type) {
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(key, digest[:], signature) {
			return 0, errors.New("that key did not sign this sign-in")
		}
	case *rsa.PublicKey:
		if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
			return 0, errors.New("that key did not sign this sign-in")
		}
	default:
		return 0, errors.New("this site does not read that kind of key")
	}
	// An authenticator that counts must count upwards: a counter that went
	// backwards is a copy of the key answering.
	if credential.SignCount > 0 || authenticator.signCount > 0 {
		if authenticator.signCount <= credential.SignCount {
			return 0, errors.New("that key has been used elsewhere: its counter went backwards")
		}
	}
	return authenticator.signCount, nil
}

// checkClientData holds the browser's account of the ceremony to what this
// site asked for: the right ceremony, this site's challenge, and this site's
// address.
func checkClientData(raw []byte, ceremony string, expect Expectation) error {
	var data clientData
	if err := json.Unmarshal(raw, &data); err != nil {
		return errors.New("the browser's account of the sign-in cannot be read")
	}
	if data.Type != ceremony {
		return fmt.Errorf("that answer is to %q rather than to %q", data.Type, ceremony)
	}
	challenge, err := base64.RawURLEncoding.DecodeString(data.Challenge)
	if err != nil {
		return errors.New("the challenge in the answer is not base64url")
	}
	if len(expect.Challenge) == 0 || subtle.ConstantTimeCompare(challenge, expect.Challenge) != 1 {
		return errors.New("that answer is to another challenge")
	}
	if data.Origin != expect.Origin {
		return fmt.Errorf("that answer was given at %q rather than at this site", data.Origin)
	}
	if data.CrossOrigin {
		return errors.New("that answer was given inside another site's page")
	}
	return nil
}

// authenticatorData is what the authenticator itself wrote and signed.
type authenticatorData struct {
	rpIDHash     []byte
	userPresent  bool
	userVerified bool
	signCount    uint32
	aaguid       []byte
	credentialID []byte
	publicKey    []byte
}

// readAuthenticatorData reads the bytes an authenticator writes: which site
// it is for, what the person did, its counter, and -- when a key has just
// been made -- the credential itself.
func readAuthenticatorData(data []byte) (authenticatorData, error) {
	if len(data) < 37 {
		return authenticatorData{}, errors.New("the authenticator data is too short to be one")
	}
	parsed := authenticatorData{
		rpIDHash:     data[:32],
		userPresent:  data[32]&flagUserPresent != 0,
		userVerified: data[32]&flagUserVerified != 0,
		signCount:    binary.BigEndian.Uint32(data[33:37]),
	}
	if data[32]&flagAttestedData == 0 {
		return parsed, nil
	}
	rest := data[37:]
	if len(rest) < 18 {
		return authenticatorData{}, errors.New("the authenticator data promises a credential it does not carry")
	}
	parsed.aaguid = rest[:16]
	length := int(binary.BigEndian.Uint16(rest[16:18]))
	rest = rest[18:]
	if length <= 0 || length > len(rest) || length > 1023 {
		return authenticatorData{}, errors.New("the credential id is not there")
	}
	parsed.credentialID = rest[:length]
	rest = rest[length:]
	key, remainder, err := decodeCBOR(rest)
	if err != nil || key.kind != cborMap {
		return authenticatorData{}, errors.New("the credential carries no key")
	}
	parsed.publicKey = rest[:len(rest)-len(remainder)]
	return parsed, nil
}

// checkAuthenticator holds what the authenticator wrote to what this site
// asked for: its own address, somebody there, and -- when the site asks --
// somebody who proved who they were.
func checkAuthenticator(data authenticatorData, expect Expectation) error {
	rpIDHash := sha256.Sum256([]byte(expect.RPID))
	if subtle.ConstantTimeCompare(data.rpIDHash, rpIDHash[:]) != 1 {
		return errors.New("that key answered for another site")
	}
	if !data.userPresent {
		return errors.New("nobody was there when the key answered")
	}
	if expect.RequireUserVerification && !data.userVerified {
		return errors.New("this site asks the key to check who is using it")
	}
	return nil
}

// publicKeyFrom reads the COSE key an authenticator wrote.
func publicKeyFrom(raw []byte) (crypto.PublicKey, error) {
	key, rest, err := decodeCBOR(raw)
	if err != nil || key.kind != cborMap || len(rest) != 0 {
		return nil, errors.New("the credential's key cannot be read")
	}
	algorithm, ok := key.label(3)
	if !ok {
		return nil, errors.New("the credential's key says nothing about its algorithm")
	}
	switch algorithm.number {
	case algorithmES256:
		x, okX := key.label(-2)
		y, okY := key.label(-3)
		curve, okCurve := key.label(-1)
		if !okX || !okY || !okCurve || x.kind != cborBytes || y.kind != cborBytes {
			return nil, errors.New("the credential's key is not a P-256 key")
		}
		if curve.number != 1 {
			return nil, errors.New("this site reads P-256 keys")
		}
		if len(x.bytes) != 32 || len(y.bytes) != 32 {
			return nil, errors.New("the credential's key is the wrong size for P-256")
		}
		public := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x.bytes), Y: new(big.Int).SetBytes(y.bytes)}
		if !public.Curve.IsOnCurve(public.X, public.Y) {
			return nil, errors.New("the credential's key is not a point on P-256")
		}
		return public, nil
	case algorithmRS256:
		modulus, okN := key.label(-1)
		exponent, okE := key.label(-2)
		if !okN || !okE || modulus.kind != cborBytes || exponent.kind != cborBytes {
			return nil, errors.New("the credential's key is not an RSA key")
		}
		if len(modulus.bytes) < 256 {
			return nil, errors.New("the credential's RSA key is shorter than 2048 bits")
		}
		public := &rsa.PublicKey{N: new(big.Int).SetBytes(modulus.bytes), E: int(new(big.Int).SetBytes(exponent.bytes).Int64())}
		if public.E < 3 {
			return nil, errors.New("the credential's RSA key has no usable exponent")
		}
		return public, nil
	}
	return nil, fmt.Errorf("this site registers keys that sign with ES256 or RS256, not %d", algorithm.number)
}

// PublicKeyDER is the credential's key in the form a person can compare with
// what their authenticator shows, which is what a test and an audit read.
func PublicKeyDER(raw []byte) ([]byte, error) {
	public, err := publicKeyFrom(raw)
	if err != nil {
		return nil, err
	}
	return x509.MarshalPKIXPublicKey(public)
}
