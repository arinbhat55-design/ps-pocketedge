package vault

import (
	"context"
	"fmt"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// Vault is the credential store: the store's secrets table plus the
// cipher that seals it.
type Vault struct {
	st     *store.Store
	cipher *Cipher
}

// New builds a Vault.
func New(st *store.Store, cipher *Cipher) *Vault {
	return &Vault{st: st, cipher: cipher}
}

// NewSecret describes a secret to store; Value is sealed before it
// reaches the database.
type NewSecret struct {
	Name       string
	Kind       string
	Username   string
	Value      string
	OwnerID    string
	DatabaseID string
	ExpiresAt  *time.Time
}

// Create seals and stores a secret, returning its ID.
func (v *Vault) Create(ctx context.Context, n NewSecret) (string, error) {
	sealed, err := v.cipher.Seal(n.Value)
	if err != nil {
		return "", err
	}
	return v.st.CreateSecret(ctx, store.NewSecret{
		Name: n.Name, Kind: n.Kind, Username: n.Username, Ciphertext: sealed,
		OwnerID: n.OwnerID, DatabaseID: n.DatabaseID, ExpiresAt: n.ExpiresAt,
	})
}

// Open returns a secret's metadata and plaintext.
func (v *Vault) Open(ctx context.Context, id string) (*store.Secret, string, error) {
	sec, sealed, err := v.st.GetSecretCiphertext(ctx, id)
	if err != nil {
		return nil, "", err
	}
	value, err := v.cipher.Open(sealed)
	if err != nil {
		return nil, "", err
	}
	return sec, value, nil
}

// Replace stores a new value for an existing secret (a rotation).
func (v *Vault) Replace(ctx context.Context, id, value string) error {
	sealed, err := v.cipher.Seal(value)
	if err != nil {
		return err
	}
	return v.st.ReplaceSecretValue(ctx, id, sealed)
}

// ResolveEnv returns env with every vault reference replaced by its
// secret's plaintext, or env itself if it holds no references. A
// reference to a missing secret is an error: deploying with the literal
// "vault:..." string as a password would be worse than not deploying.
func (v *Vault) ResolveEnv(ctx context.Context, env map[string]string) (map[string]string, error) {
	var ids []string
	for _, value := range env {
		if id, ok := ParseReference(value); ok {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return env, nil
	}
	sealed, err := v.st.SecretCiphertexts(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		id, ok := ParseReference(value)
		if !ok {
			out[key] = value
			continue
		}
		ct, found := sealed[id]
		if !found {
			return nil, fmt.Errorf("env %s refers to a secret that no longer exists", key)
		}
		plain, err := v.cipher.Open(ct)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", key, err)
		}
		out[key] = plain
	}
	return out, nil
}

// EnvResolver adapts ResolveEnv to deploy.EnvResolver's context-free
// signature.
func (v *Vault) EnvResolver() func(map[string]string) (map[string]string, error) {
	return func(env map[string]string) (map[string]string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return v.ResolveEnv(ctx, env)
	}
}

// ServerSecrets returns the plaintext of every live database credential on
// serverID, for redacting that server's container logs.
func (v *Vault) ServerSecrets(ctx context.Context, serverID string) ([]string, error) {
	sealed, err := v.st.ServerSecretCiphertexts(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(sealed))
	for _, ct := range sealed {
		if plain, err := v.cipher.Open(ct); err == nil {
			out = append(out, plain)
		}
	}
	return out, nil
}
