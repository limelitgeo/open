// Package secrets encrypts the provider credentials a user pastes into the
// dashboard, so the SQLite file is not a plaintext key store.
//
// The key comes from LIMELIT_SECRET when it is set. Otherwise one is
// generated on first use and written next to the database with 0600. That
// second path matters: a self-hoster who never reads the documentation still
// gets encryption at rest, and a user who wants to manage the key themselves
// can export the variable and the file is ignored.
//
// This is not protection against someone who already has the machine, who can
// read both files. It is protection against the ordinary ways a database file
// travels: a backup, a support attachment, a copied volume.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoKey is returned when no key is available and one could not be created.
var ErrNoKey = errors.New("secrets: no encryption key")

// Keyring holds the derived key.
type Keyring struct {
	aead cipher.AEAD
}

// Open loads the key: LIMELIT_SECRET when set, otherwise the file at
// dir/secret.key, creating it if it does not exist.
func Open(dir string) (*Keyring, error) {
	material, err := keyMaterial(dir)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(material)
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Keyring{aead: aead}, nil
}

func keyMaterial(dir string) ([]byte, error) {
	if env := strings.TrimSpace(os.Getenv("LIMELIT_SECRET")); env != "" {
		return []byte(env), nil
	}
	path := filepath.Join(dir, "secret.key")
	switch data, err := os.ReadFile(path); {
	case err == nil && len(data) > 0:
		return data, nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("%w: read %s: %v", ErrNoKey, path, err)
	}

	generated := make([]byte, 32)
	if _, err := rand.Read(generated); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoKey, err)
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(generated))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoKey, err)
	}
	// 0600: the key is only ever read by this process, as this user.
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return nil, fmt.Errorf("%w: write %s: %v", ErrNoKey, path, err)
	}
	return encoded, nil
}

// Seal encrypts a credential for storage. The nonce is prefixed, so one
// stored string carries everything needed to open it.
func (k *Keyring) Seal(plaintext string) (string, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := k.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Unseal decrypts a stored credential.
func (k *Keyring) Unseal(stored string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return "", fmt.Errorf("secrets: stored value is not base64: %w", err)
	}
	size := k.aead.NonceSize()
	if len(raw) < size {
		return "", errors.New("secrets: stored value is too short to contain a nonce")
	}
	plain, err := k.aead.Open(nil, raw[:size], raw[size:], nil)
	if err != nil {
		// Nearly always a key change rather than corruption, so say so: the
		// alternative error message sends people looking at their database.
		return "", fmt.Errorf("secrets: cannot decrypt, the key changed or the value is corrupt: %w", err)
	}
	return string(plain), nil
}
