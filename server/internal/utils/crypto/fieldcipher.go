package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// FieldCipher encrypts 🔒 columns at rest with AES-256-GCM (spec C5). The master
// key arrives from the environment (OUROPASS_FIELD_KEY); it never lives in the DB.
type FieldCipher struct {
	aead cipher.AEAD
}

// NewFieldCipher builds a cipher from a 32-byte key (AES-256).
func NewFieldCipher(key []byte) (*FieldCipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("field key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &FieldCipher{aead: aead}, nil
}

// NewFieldCipherHex decodes a hex-encoded 32-byte key (the env form).
func NewFieldCipherHex(keyHex string) (*FieldCipher, error) {
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("decode field key hex: %w", err)
	}
	return NewFieldCipher(key)
}

// Encrypt returns nonce||ciphertext||tag. A fresh random nonce is prepended so
// the same plaintext yields different blobs.
func (c *FieldCipher) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt, authenticating the tag.
func (c *FieldCipher) Decrypt(blob []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	return c.aead.Open(nil, nonce, ct, nil)
}
