package cryptokit

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const keySize = 32

// Encrypt seals plaintext with AES-256-GCM. The returned blob is nonce||ciphertext.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens a blob produced by Encrypt (nonce||ciphertext).
func Decrypt(key, blob []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(blob) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := blob[:nonceSize], blob[nonceSize:]
	pt, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return pt, nil
}

// ParseKey accepts a 32-byte raw key or a 64-char hex encoding of 32 bytes.
func ParseKey(raw string) ([]byte, error) {
	if len(raw) == hex.EncodedLen(keySize) {
		key, err := hex.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("decode hex key: %w", err)
		}
		if len(key) != keySize {
			return nil, errors.New("key must be 32 bytes")
		}
		return key, nil
	}
	if len(raw) == keySize {
		return []byte(raw), nil
	}
	return nil, errors.New("key must be 32 raw bytes or 64 hex characters")
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, errors.New("key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return gcm, nil
}
