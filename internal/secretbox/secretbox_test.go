package secretbox

import (
	"bytes"
	"testing"
)

func TestAuthenticatedEncryptionBindsProviderContext(t *testing.T) {
	box, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte("client-secret"), "ws_default/google")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := box.Open(sealed, "ws_default/google")
	if err != nil || string(opened) != "client-secret" {
		t.Fatalf("open = %q, %v", opened, err)
	}
	if bytes.Contains(sealed, []byte("client-secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	if _, err := box.Open(sealed, "ws_other/google"); err == nil {
		t.Fatal("ciphertext opened under another provider context")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := box.Open(sealed, "ws_default/google"); err == nil {
		t.Fatal("tampered ciphertext opened")
	}
}
