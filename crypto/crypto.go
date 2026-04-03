package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

func Encrypt(key [32]byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func Decrypt(key [32]byte, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce := ciphertext[:gcm.NonceSize()]
	return gcm.Open(nil, nonce, ciphertext[gcm.NonceSize():], nil)
}

func LoadOrCreateKey(configDir string) ([32]byte, error) {
	if secret := os.Getenv("NEVINHO_SECRET"); secret != "" {
		return sha256.Sum256([]byte(secret)), nil
	}

	keyFile := filepath.Join(configDir, "secret.key")
	data, err := os.ReadFile(keyFile)
	if err == nil && len(data) == 32 {
		var key [32]byte
		copy(key[:], data)
		return key, nil
	}

	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return key, fmt.Errorf("generate key: %w", err)
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return key, err
	}
	if err := os.WriteFile(keyFile, key[:], 0600); err != nil {
		return key, err
	}
	return key, nil
}
