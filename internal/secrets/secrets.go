// Package secrets seals values at rest (spec §10.4) with AES-256-GCM under a key kept
// in a separate 0600 file, outside the database.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

const keySize = 32

// LoadOrCreateKey returns the key at path, creating it (0600) on first use. A key file
// readable by others or of the wrong size is refused.
func LoadOrCreateKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return createKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: read key: %w", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secrets: key file %s must be 0600, it is %#o", path, st.Mode().Perm())
	}
	if len(b) != keySize {
		return nil, fmt.Errorf("secrets: key file %s holds %d bytes, want %d", path, len(b), keySize)
	}
	return b, nil
}

func createKey(path string) ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("secrets: create key: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	return key, f.Close()
}

// Box seals and opens values; the value's name is authenticated with it, so a sealed
// value cannot be moved to another name.
type Box struct{ aead cipher.AEAD }

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
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

// Seal encrypts plain for name: a random nonce followed by the ciphertext.
func (b *Box) Seal(name string, plain []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plain, []byte(name)), nil
}

// Open decrypts a value sealed for name.
func (b *Box) Open(name string, sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("secrets: sealed value too short")
	}
	return b.aead.Open(nil, sealed[:n], sealed[n:], []byte(name))
}

// Vault keeps named secrets sealed in the store.
type Vault struct {
	box   *Box
	store *store.Store
}

const (
	checkMeta  = "secrets_key_check"
	checkName  = "ghrm/key-check"
	checkValue = "ok"
)

// OpenVault loads (or creates) the key at keyPath and checks that it is the key that
// sealed this database's secrets; a different key is refused rather than leaving every
// secret unreadable.
func OpenVault(ctx context.Context, s *store.Store, keyPath string) (*Vault, error) {
	key, err := LoadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	box, err := New(key)
	if err != nil {
		return nil, err
	}
	check, err := s.GetMeta(ctx, checkMeta)
	switch {
	case errors.Is(err, store.ErrNotFound):
		sealed, err := box.Seal(checkName, []byte(checkValue))
		if err != nil {
			return nil, err
		}
		if err := s.PutMeta(ctx, checkMeta, base64.StdEncoding.EncodeToString(sealed)); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		sealed, err := base64.StdEncoding.DecodeString(check)
		if err == nil {
			var plain []byte
			plain, err = box.Open(checkName, sealed)
			if err == nil && string(plain) != checkValue {
				err = errors.New("unexpected check value")
			}
		}
		if err != nil {
			return nil, fmt.Errorf("secrets: %s does not match the key that sealed this database's secrets (restore that key file)", keyPath)
		}
	}
	return &Vault{box: box, store: s}, nil
}

// Get returns a secret and whether it exists.
func (v *Vault) Get(ctx context.Context, name string) (string, bool, error) {
	sealed, err := v.store.GetSecret(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	plain, err := v.box.Open(name, sealed)
	if err != nil {
		return "", false, fmt.Errorf("secrets: open %s: %w", name, err)
	}
	return string(plain), true, nil
}

// Set seals and stores a secret.
func (v *Vault) Set(ctx context.Context, name, value string) error {
	sealed, err := v.box.Seal(name, []byte(value))
	if err != nil {
		return err
	}
	return v.store.PutSecret(ctx, name, sealed)
}

// Delete removes a secret.
func (v *Vault) Delete(ctx context.Context, name string) error {
	return v.store.DeleteSecret(ctx, name)
}

// Names lists the stored secrets.
func (v *Vault) Names(ctx context.Context) ([]string, error) { return v.store.ListSecretNames(ctx) }
