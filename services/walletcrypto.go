package services

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/tayyebi/gig/config"
)

// WalletCrypto encrypts/decrypts seller wallet addresses at rest with
// AES-256-GCM, keyed from config.WalletEncryptionKey. Stdlib only
// (crypto/aes, crypto/cipher), matching the project's no-SDK constraint.
type WalletCrypto struct {
	key []byte
}

// NewWalletCrypto builds a WalletCrypto from the configured base64 key. It
// returns an error if the key is missing or not exactly 32 bytes (AES-256),
// so misconfiguration fails at startup rather than silently at first use.
func NewWalletCrypto(cfg *config.Config) (*WalletCrypto, error) {
	key, err := config.DecodeWalletKey(cfg.WalletEncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decode wallet encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("wallet encryption key must be 32 bytes, got %d", len(key))
	}
	return &WalletCrypto{key: key}, nil
}

// Encrypt returns nonce||ciphertext for the given plaintext wallet address.
func (wc *WalletCrypto) Encrypt(plaintext string) ([]byte, error) {
	block, err := aes.NewCipher(wc.key)
	if err != nil {
		return nil, fmt.Errorf("wallet crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("wallet crypto: new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("wallet crypto: generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt is the inverse of Encrypt.
func (wc *WalletCrypto) Decrypt(data []byte) (string, error) {
	block, err := aes.NewCipher(wc.key)
	if err != nil {
		return "", fmt.Errorf("wallet crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("wallet crypto: new gcm: %w", err)
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("wallet crypto: ciphertext too short")
	}
	nonce, ciphertext := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("wallet crypto: decrypt: %w", err)
	}
	return string(plaintext), nil
}

// WalletAddressFingerprint returns a stable, non-reversible fingerprint of a
// wallet address for duplicate detection without decrypting stored rows.
func WalletAddressFingerprint(address string) string {
	sum := sha256.Sum256([]byte(address))
	return fmt.Sprintf("%x", sum[:])
}
