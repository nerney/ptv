package config

// Encryption layer: argon2id(password, salt=master_key) → 32-byte derived key,
// used with ChaCha20-Poly1305 to seal/open the JSON config blob.
//
// On-disk format (config.enc): JSON object with version + IV + ciphertext.
// The Poly1305 auth tag is appended to the ciphertext by the AEAD seal,
// so authenticated-decrypt failure IS the wrong-password signal.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	vaultDir       = ".vault"
	keyFilename    = "key"
	masterKeySize  = 32
	derivedKeySize = chacha20poly1305.KeySize // 32

	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 2

	blobVersion = 1
)

type encryptedBlob struct {
	Version int    `json:"version"`
	IV      string `json:"iv"`   // base64 (12 bytes)
	Data    string `json:"data"` // base64 (ciphertext || tag)
}

// loadOrCreateMasterKey reads /<dir>/.vault/key, generating a 32-byte
// random master key on first run. Returns the key bytes.
func loadOrCreateMasterKey(dir string) ([]byte, error) {
	vaultPath := filepath.Join(dir, vaultDir)
	keyPath := filepath.Join(vaultPath, keyFilename)

	if data, err := os.ReadFile(keyPath); err == nil {
		if len(data) != masterKeySize {
			return nil, fmt.Errorf("master key wrong size: got %d want %d", len(data), masterKeySize)
		}
		return data, nil
	}

	if err := os.MkdirAll(vaultPath, 0700); err != nil {
		return nil, fmt.Errorf("mkdir vault: %w", err)
	}

	key := make([]byte, masterKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("rand master key: %w", err)
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return key, nil
}

// deriveKey returns argon2id(password, salt=master_key). The master key
// IS the salt — exact match to the spec's "Argon2(password, salt=master_key)".
func deriveKey(password string, masterKey []byte) []byte {
	return argon2.IDKey(
		[]byte(password),
		masterKey,
		argonTime,
		argonMemory,
		argonThreads,
		derivedKeySize,
	)
}

// seal encrypts plaintext with the derived key, returning a JSON-encoded blob.
func seal(derivedKey, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(derivedKey)
	if err != nil {
		return nil, fmt.Errorf("aead init: %w", err)
	}
	iv := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("rand iv: %w", err)
	}
	ct := aead.Seal(nil, iv, plaintext, nil)

	blob := encryptedBlob{
		Version: blobVersion,
		IV:      base64.StdEncoding.EncodeToString(iv),
		Data:    base64.StdEncoding.EncodeToString(ct),
	}
	return json.Marshal(blob)
}

// open decrypts a JSON-encoded blob with the derived key. An auth-tag
// failure (wrong password) returns errBadPassword.
var errBadPassword = errors.New("decrypt failed: wrong password or corrupted data")

func open(derivedKey, data []byte) ([]byte, error) {
	var blob encryptedBlob
	if err := json.Unmarshal(data, &blob); err != nil {
		return nil, fmt.Errorf("blob parse: %w", err)
	}
	if blob.Version != blobVersion {
		return nil, fmt.Errorf("unsupported blob version: %d", blob.Version)
	}

	iv, err := base64.StdEncoding.DecodeString(blob.IV)
	if err != nil {
		return nil, fmt.Errorf("iv decode: %w", err)
	}
	ct, err := base64.StdEncoding.DecodeString(blob.Data)
	if err != nil {
		return nil, fmt.Errorf("data decode: %w", err)
	}

	aead, err := chacha20poly1305.New(derivedKey)
	if err != nil {
		return nil, fmt.Errorf("aead init: %w", err)
	}

	pt, err := aead.Open(nil, iv, ct, nil)
	if err != nil {
		return nil, errBadPassword
	}
	return pt, nil
}

// zero wipes a byte slice in place — used when clearing derived keys
// from memory on logout. Note: Go's GC may have already moved the bytes,
// so this is best-effort, not a strong guarantee.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
