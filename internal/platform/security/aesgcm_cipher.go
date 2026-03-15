package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

type AESGCMCipher struct {
	aead cipher.AEAD
}

func NewAESGCMCipher(passphrase string) (*AESGCMCipher, error) {
	secret := strings.TrimSpace(passphrase)
	if secret == "" {
		return nil, errors.New("merchant secret passphrase is empty")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create aes block: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create aes-gcm: %w", err)
	}
	return &AESGCMCipher{aead: aead}, nil
}

func (c *AESGCMCipher) Encrypt(plain string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	encrypted := c.aead.Seal(nil, nonce, []byte(plain), nil)
	raw := append(nonce, encrypted...)
	return "v1." + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (c *AESGCMCipher) Decrypt(ciphertext string) (string, error) {
	if c == nil {
		return "", errors.New("cipher is nil")
	}
	payload := strings.TrimSpace(ciphertext)
	if !strings.HasPrefix(payload, "v1.") {
		return "", errors.New("unsupported ciphertext version")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(payload, "v1."))
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce, encrypted := raw[:nonceSize], raw[nonceSize:]
	plain, err := c.aead.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt ciphertext: %w", err)
	}
	return string(plain), nil
}
