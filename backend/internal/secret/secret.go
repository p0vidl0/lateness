// Package secret holds random tokens, token hashing and symmetric encryption of stored secrets.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

func Hex(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// keyVersion prefixes every sealed value so the key can be rotated later.
const keyVersion byte = 1

var ErrSealed = errors.New("sealed value is malformed or was sealed with another key")

// Cipher seals values with AES-256-GCM. Sealed layout: version byte, nonce, ciphertext.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher takes a base64-encoded 32-byte key.
func NewCipher(keyB64 string) (*Cipher, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("secret key must be 32 bytes in base64")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("random: %w", err)
	}
	out := append([]byte{keyVersion}, nonce...)
	return c.aead.Seal(out, nonce, plain, []byte{keyVersion}), nil
}

func (c *Cipher) Open(sealed []byte) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(sealed) < 1+n || sealed[0] != keyVersion {
		return nil, ErrSealed
	}
	plain, err := c.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte{keyVersion})
	if err != nil {
		return nil, ErrSealed
	}
	return plain, nil
}
