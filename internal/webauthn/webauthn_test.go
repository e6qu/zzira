package webauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

// authenticator is a security key of the test's own: it writes the bytes a
// real one writes and signs them the same way, so this package is read
// against what it will be given rather than against itself.
type authenticator struct {
	key       *ecdsa.PrivateKey
	id        []byte
	signCount uint32
	verified  bool
}

func newAuthenticator(t *testing.T) *authenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &authenticator{key: key, id: []byte("credential-id-0123456789"), verified: true}
}

// coseKey is the key as an authenticator writes it: a CBOR map of the
// algorithm, the curve and the point.
func (a *authenticator) coseKey() []byte {
	x, y := a.key.PublicKey.X.Bytes(), a.key.PublicKey.Y.Bytes()
	x = append(make([]byte, 32-len(x)), x...)
	y = append(make([]byte, 32-len(y)), y...)
	out := []byte{0xa5}
	out = append(out, 0x01, 0x02)       // kty: EC2
	out = append(out, 0x03, 0x26)       // alg: ES256 (-7)
	out = append(out, 0x20, 0x01)       // crv: P-256
	out = append(out, 0x21, 0x58, 0x20) // x: 32 bytes
	out = append(out, x...)
	out = append(out, 0x22, 0x58, 0x20) // y: 32 bytes
	out = append(out, y...)
	return out
}

// authData is what the authenticator writes and signs.
func (a *authenticator) authData(rpID string, attested bool) []byte {
	hash := sha256.Sum256([]byte(rpID))
	flags := byte(flagUserPresent)
	if a.verified {
		flags |= flagUserVerified
	}
	if attested {
		flags |= flagAttestedData
	}
	data := append([]byte{}, hash[:]...)
	data = append(data, flags)
	counter := make([]byte, 4)
	binary.BigEndian.PutUint32(counter, a.signCount)
	data = append(data, counter...)
	if !attested {
		return data
	}
	data = append(data, make([]byte, 16)...) // AAGUID
	length := make([]byte, 2)
	binary.BigEndian.PutUint16(length, uint16(len(a.id)))
	data = append(data, length...)
	data = append(data, a.id...)
	return append(data, a.coseKey()...)
}

// attestationObject is the CBOR map a browser hands back on registration,
// with no attestation statement, which is what a second factor asks for.
func (a *authenticator) attestationObject(rpID string) []byte {
	authData := a.authData(rpID, true)
	out := []byte{0xa3}
	out = append(out, 0x63, 'f', 'm', 't')      // "fmt"
	out = append(out, 0x64, 'n', 'o', 'n', 'e') // "none"
	out = append(out, 0x67, 'a', 't', 't', 'S', 't', 'm', 't')
	out = append(out, 0xa0) // {}
	out = append(out, 0x68, 'a', 'u', 't', 'h', 'D', 'a', 't', 'a')
	out = append(out, cborByteString(authData)...)
	return out
}

// cborByteString writes bytes as CBOR.
func cborByteString(value []byte) []byte {
	switch {
	case len(value) < 24:
		return append([]byte{byte(0x40 + len(value))}, value...)
	case len(value) < 256:
		return append([]byte{0x58, byte(len(value))}, value...)
	default:
		header := []byte{0x59, byte(len(value) >> 8), byte(len(value))}
		return append(header, value...)
	}
}

func clientDataJSON(t *testing.T, ceremony string, challenge []byte, origin string) []byte {
	t.Helper()
	raw, err := json.Marshal(clientData{
		Type: ceremony, Challenge: base64.RawURLEncoding.EncodeToString(challenge), Origin: origin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// sign is what the authenticator does when it answers a sign-in.
func (a *authenticator) sign(t *testing.T, authData, clientDataJSON []byte) []byte {
	t.Helper()
	clientHash := sha256.Sum256(clientDataJSON)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signature
}

const (
	testOrigin = "https://zzira.test"
	testRPID   = "zzira.test"
)

func expectation(challenge []byte) Expectation {
	return Expectation{Challenge: challenge, Origin: testOrigin, RPID: testRPID, RequireUserVerification: false}
}

// A key registers, and then answers a sign-in with the same key.
func TestAKeyRegistersAndAnswersASignIn(t *testing.T) {
	key := newAuthenticator(t)
	challenge := []byte("registration-challenge")
	credential, err := Register(clientDataJSON(t, "webauthn.create", challenge, testOrigin), key.attestationObject(testRPID), expectation(challenge))
	if err != nil {
		t.Fatalf("register a key: %v", err)
	}
	if string(credential.ID) != string(key.id) || !credential.UserVerified {
		t.Fatalf("credential = %+v", credential)
	}
	if _, err := PublicKeyDER(credential.PublicKey); err != nil {
		t.Fatalf("the key that was kept: %v", err)
	}

	signInChallenge := []byte("sign-in-challenge")
	key.signCount = 5
	authData := key.authData(testRPID, false)
	client := clientDataJSON(t, "webauthn.get", signInChallenge, testOrigin)
	count, err := Verify(credential, client, authData, key.sign(t, authData, client), expectation(signInChallenge))
	if err != nil {
		t.Fatalf("answer a sign-in: %v", err)
	}
	if count != 5 {
		t.Fatalf("counter = %d", count)
	}
}

// What a sign-in is held to: this site's challenge, this site's address, this
// site's key, somebody there, and a counter that has not gone backwards.
func TestASignInIsHeldToWhatItAnswers(t *testing.T) {
	key := newAuthenticator(t)
	challenge := []byte("registration-challenge")
	credential, err := Register(clientDataJSON(t, "webauthn.create", challenge, testOrigin), key.attestationObject(testRPID), expectation(challenge))
	if err != nil {
		t.Fatal(err)
	}
	signInChallenge := []byte("sign-in-challenge")
	key.signCount = 5
	good := func() ([]byte, []byte, []byte) {
		authData := key.authData(testRPID, false)
		client := clientDataJSON(t, "webauthn.get", signInChallenge, testOrigin)
		return client, authData, key.sign(t, authData, client)
	}
	for _, tc := range []struct {
		name string
		make func() ([]byte, []byte, []byte, Expectation)
		says string
	}{
		{"another challenge", func() ([]byte, []byte, []byte, Expectation) {
			client, authData, signature := good()
			return client, authData, signature, expectation([]byte("another-challenge"))
		}, "another challenge"},
		{"another site's page", func() ([]byte, []byte, []byte, Expectation) {
			authData := key.authData(testRPID, false)
			client := clientDataJSON(t, "webauthn.get", signInChallenge, "https://attacker.test")
			return client, authData, key.sign(t, authData, client), expectation(signInChallenge)
		}, "given at"},
		{"another site's key", func() ([]byte, []byte, []byte, Expectation) {
			authData := key.authData("attacker.test", false)
			client := clientDataJSON(t, "webauthn.get", signInChallenge, testOrigin)
			return client, authData, key.sign(t, authData, client), expectation(signInChallenge)
		}, "answered for another site"},
		{"a registration answering a sign-in", func() ([]byte, []byte, []byte, Expectation) {
			authData := key.authData(testRPID, false)
			client := clientDataJSON(t, "webauthn.create", signInChallenge, testOrigin)
			return client, authData, key.sign(t, authData, client), expectation(signInChallenge)
		}, "rather than to"},
		{"a signature from another key", func() ([]byte, []byte, []byte, Expectation) {
			other := newAuthenticator(t)
			authData := key.authData(testRPID, false)
			client := clientDataJSON(t, "webauthn.get", signInChallenge, testOrigin)
			return client, authData, other.sign(t, authData, client), expectation(signInChallenge)
		}, "did not sign"},
		{"a counter that went backwards", func() ([]byte, []byte, []byte, Expectation) {
			was := key.signCount
			key.signCount = 1
			defer func() { key.signCount = was }()
			authData := key.authData(testRPID, false)
			client := clientDataJSON(t, "webauthn.get", signInChallenge, testOrigin)
			return client, authData, key.sign(t, authData, client), expectation(signInChallenge)
		}, "used elsewhere"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, authData, signature, expect := tc.make()
			kept := credential
			kept.SignCount = 4
			_, err := Verify(kept, client, authData, signature, expect)
			if err == nil {
				t.Fatal("the sign-in was accepted")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("error %q does not say %q", err, tc.says)
			}
		})
	}
}

// A site that asks the key to check who is using it refuses one that only
// says somebody was there.
func TestUserVerificationIsAskedFor(t *testing.T) {
	key := newAuthenticator(t)
	key.verified = false
	challenge := []byte("registration-challenge")
	expect := expectation(challenge)
	expect.RequireUserVerification = true
	if _, err := Register(clientDataJSON(t, "webauthn.create", challenge, testOrigin), key.attestationObject(testRPID), expect); err == nil {
		t.Fatal("a key that checked nobody registered where verification is asked for")
	}
	key.verified = true
	if _, err := Register(clientDataJSON(t, "webauthn.create", challenge, testOrigin), key.attestationObject(testRPID), expect); err != nil {
		t.Fatalf("a key that checked the person: %v", err)
	}
}

// What is not a message says so rather than being read as one.
func TestNonsenseIsRefused(t *testing.T) {
	key := newAuthenticator(t)
	challenge := []byte("registration-challenge")
	client := clientDataJSON(t, "webauthn.create", challenge, testOrigin)
	for _, tc := range []struct {
		name   string
		object []byte
	}{
		{"nothing at all", nil},
		{"not CBOR", []byte{0xff, 0xff, 0xff}},
		{"a map with no authenticator data", []byte{0xa1, 0x63, 'f', 'm', 't', 0x64, 'n', 'o', 'n', 'e'}},
		{"authenticator data that is too short", append([]byte{0xa1, 0x68, 'a', 'u', 't', 'h', 'D', 'a', 't', 'a'}, cborByteString([]byte("short"))...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Register(client, tc.object, expectation(challenge)); err == nil {
				t.Fatal("nonsense was read as a registration")
			}
		})
	}
	if _, err := Register([]byte("not json"), key.attestationObject(testRPID), expectation(challenge)); err == nil {
		t.Fatal("nonsense was read as the browser's account of the ceremony")
	}
}

// A key of a kind this site does not register says so.
func TestOnlyKeysThisSiteReadsAreRegistered(t *testing.T) {
	// A COSE key that says it signs with an algorithm nobody asked for.
	key := []byte{0xa3, 0x01, 0x02, 0x03, 0x38, 0x24, 0x20, 0x01}
	if _, err := publicKeyFrom(key); err == nil {
		t.Fatal("a key with an unknown algorithm was read")
	}
	// A P-256 key whose point is not on the curve.
	bad := []byte{0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20}
	bad = append(bad, make([]byte, 32)...)
	bad = append(bad, 0x22, 0x58, 0x20)
	bad = append(bad, new(big.Int).SetInt64(1).FillBytes(make([]byte, 32))...)
	if _, err := publicKeyFrom(bad); err == nil {
		t.Fatal("a point that is not on P-256 was read as a key")
	}
}
