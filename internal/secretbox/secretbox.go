// Package secretbox encrypts application credentials before persistence.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const envelopeVersion byte = 1

type Box struct {
	aead cipher.AEAD
}

// FromEnv loads a 32-byte base64 key. An absent key disables database-backed
// credentials; malformed configured keys fail startup instead of weakening
// encryption or silently generating an unrecoverable key.
func FromEnv(name string) (*Box, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%s must be a base64-encoded 32-byte key", name)
	}
	return New(key)
}

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("secretbox key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plaintext []byte, associatedData string) ([]byte, error) {
	if b == nil {
		return nil, errors.New("credential encryption is not configured")
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	envelope := append([]byte{envelopeVersion}, nonce...)
	return b.aead.Seal(envelope, nonce, plaintext, []byte(associatedData)), nil
}

func (b *Box) Open(envelope []byte, associatedData string) ([]byte, error) {
	if b == nil {
		return nil, errors.New("credential encryption is not configured")
	}
	nonceSize := b.aead.NonceSize()
	if len(envelope) <= 1+nonceSize || envelope[0] != envelopeVersion {
		return nil, errors.New("invalid credential envelope")
	}
	nonce := envelope[1 : 1+nonceSize]
	plaintext, err := b.aead.Open(nil, nonce, envelope[1+nonceSize:], []byte(associatedData))
	if err != nil {
		return nil, errors.New("credential envelope authentication failed")
	}
	return plaintext, nil
}
