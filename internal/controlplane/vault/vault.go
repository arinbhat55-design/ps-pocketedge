// Package vault encrypts credentials at rest and generates new ones.
//
// Secrets are sealed with AES-256-GCM under a single control-plane key
// (VAULT_KEY, or a generated key file — see LoadKey). Everything outside the secrets table — deployment env,
// Compose content, revisions, audit details — only ever holds a
// Reference ("vault:<secret id>"), which the dispatcher resolves to the
// plaintext at the last moment, when it builds the command it sends to an
// agent. That keeps credentials out of the database in the clear, out of
// API responses, and out of the control plane's own logs.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

// ReferencePrefix marks an env value as a vault reference rather than a
// literal.
const ReferencePrefix = "vault:"

// Reference returns the env value that stands in for secretID.
func Reference(secretID string) string { return ReferencePrefix + secretID }

// ParseReference returns the secret ID value refers to, if it is a
// reference.
func ParseReference(value string) (string, bool) {
	id, ok := strings.CutPrefix(value, ReferencePrefix)
	return id, ok && id != ""
}

// Cipher seals and opens secret values.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("vault key must be 32 bytes, got %d", len(key))
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

// LoadKey returns the vault key: VAULT_KEY (base64 of 32 bytes) when set,
// otherwise the key stored in keyFile, generating and saving a random one
// there on first start (created reports that, so the caller can tell the
// operator to back it up). Losing the key makes every stored credential
// unreadable, which is why it is never derived from anything that
// changes — such as a per-process JWT secret.
func LoadKey(vaultKey, keyFile string) (key []byte, created bool, err error) {
	if vaultKey != "" {
		key, err = decodeKey(vaultKey)
		if err != nil {
			return nil, false, fmt.Errorf("VAULT_KEY: %w", err)
		}
		return key, false, nil
	}
	if keyFile == "" {
		return nil, false, errors.New("either VAULT_KEY or a vault key file is required")
	}
	if data, err := os.ReadFile(keyFile); err == nil {
		key, err = decodeKey(strings.TrimSpace(string(data)))
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", keyFile, err)
		}
		return key, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return nil, false, err
	}
	// O_EXCL: never overwrite a key another process just wrote.
	f, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
		return nil, false, err
	}
	return key, true, f.Sync()
}

func decodeKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
}

// Seal encrypts plaintext; the nonce is prepended to the ciphertext.
func (c *Cipher) Seal(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Open decrypts a value produced by Seal.
func (c *Cipher) Open(sealed []byte) (string, error) {
	n := c.aead.NonceSize()
	if len(sealed) < n {
		return "", errors.New("sealed value too short")
	}
	plain, err := c.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return "", errors.New("failed to decrypt secret (wrong vault key?)")
	}
	return string(plain), nil
}

const (
	lower  = "abcdefghijkmnopqrstuvwxyz"
	upper  = "ABCDEFGHJKLMNPQRSTUVWXYZ"
	digits = "23456789"
)

// GeneratePassword returns a random password of length n (minimum 16)
// drawn from letters and digits, guaranteed to contain at least one of
// each class. Symbols are left out on purpose: the password is embedded in
// SQL literals, shell arguments, and connection URLs by the rotation and
// temporary-credential commands, and an alphanumeric 32-character password
// already carries ~185 bits of entropy. Look-alike characters (0/O, 1/l/I)
// are excluded so a password read off a screen can be typed back.
func GeneratePassword(n int) (string, error) {
	if n < 16 {
		n = 16
	}
	all := lower + upper + digits
	out := make([]byte, n)
	for i := range out {
		set := all
		switch i {
		case 0:
			set = lower
		case 1:
			set = upper
		case 2:
			set = digits
		}
		c, err := randomChar(set)
		if err != nil {
			return "", err
		}
		out[i] = c
	}
	// Shuffle so the guaranteed classes aren't always the first three.
	for i := len(out) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		out[i], out[j.Int64()] = out[j.Int64()], out[i]
	}
	return string(out), nil
}

func randomChar(set string) (byte, error) {
	i, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
	if err != nil {
		return 0, err
	}
	return set[i.Int64()], nil
}

// Redact replaces every occurrence of each secret in text with a mask.
// Secrets shorter than 6 characters are skipped — masking a short value
// would blank out unrelated text and they aren't generated by this
// package anyway.
func Redact(text string, secrets []string) string {
	for _, s := range secrets {
		if len(s) < 6 {
			continue
		}
		text = strings.ReplaceAll(text, s, "••••••••")
	}
	return text
}
