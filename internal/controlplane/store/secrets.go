package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Secret mirrors a row in the secrets table, minus the ciphertext — the
// sealed value is only ever read through GetSecretCiphertext /
// SecretCiphertexts, so a Secret can be serialized to the API without any
// risk of leaking it.
type Secret struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"`
	Username     string     `json:"username"`
	DatabaseID   *string    `json:"databaseId,omitempty"`
	Version      int        `json:"version"`
	OwnerID      *string    `json:"ownerId,omitempty"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	DownloadedAt *time.Time `json:"downloadedAt,omitempty"`
	RotatedAt    *time.Time `json:"rotatedAt,omitempty"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
}

// Active reports whether the secret is still usable: not revoked and not
// past its expiry.
func (s *Secret) Active(now time.Time) bool {
	return s.RevokedAt == nil && (s.ExpiresAt == nil || now.Before(*s.ExpiresAt))
}

// NewSecret is the input to CreateSecret.
type NewSecret struct {
	Name       string
	Kind       string
	Username   string
	Ciphertext []byte
	OwnerID    string
	DatabaseID string
	ExpiresAt  *time.Time
}

const secretColumns = `id, name, kind, username, database_id, version, owner_id, expires_at, downloaded_at, rotated_at, revoked_at, created_at`

func scanSecret(row rowScanner) (Secret, error) {
	var s Secret
	err := row.Scan(&s.ID, &s.Name, &s.Kind, &s.Username, &s.DatabaseID, &s.Version, &s.OwnerID, &s.ExpiresAt, &s.DownloadedAt, &s.RotatedAt, &s.RevokedAt, &s.CreatedAt)
	return s, err
}

func nullableUUID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// CreateSecret stores a sealed secret.
func (s *Store) CreateSecret(ctx context.Context, n NewSecret) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO secrets (name, kind, username, value_ciphertext, owner_id, database_id, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, n.Name, n.Kind, n.Username, n.Ciphertext, nullableUUID(n.OwnerID), nullableUUID(n.DatabaseID), n.ExpiresAt).Scan(&id)
	return id, err
}

// GetSecret returns a secret's metadata. Returns ErrNotFound if it
// doesn't exist.
func (s *Store) GetSecret(ctx context.Context, id string) (*Secret, error) {
	sec, err := scanSecret(s.pool.QueryRow(ctx, `SELECT `+secretColumns+` FROM secrets WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sec, nil
}

// GetSecretCiphertext returns a secret's metadata and sealed value.
func (s *Store) GetSecretCiphertext(ctx context.Context, id string) (*Secret, []byte, error) {
	var ciphertext []byte
	row := s.pool.QueryRow(ctx, `SELECT `+secretColumns+`, value_ciphertext FROM secrets WHERE id = $1`, id)
	var sec Secret
	err := row.Scan(&sec.ID, &sec.Name, &sec.Kind, &sec.Username, &sec.DatabaseID, &sec.Version, &sec.OwnerID, &sec.ExpiresAt, &sec.DownloadedAt, &sec.RotatedAt, &sec.RevokedAt, &sec.CreatedAt, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return &sec, ciphertext, nil
}

// SecretCiphertexts returns the sealed values of the given secrets, keyed
// by ID. IDs that don't exist are simply absent from the result.
func (s *Store) SecretCiphertexts(ctx context.Context, ids []string) (map[string][]byte, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, value_ciphertext FROM secrets WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]byte, len(ids))
	for rows.Next() {
		var id string
		var ct []byte
		if err := rows.Scan(&id, &ct); err != nil {
			return nil, err
		}
		out[id] = ct
	}
	return out, rows.Err()
}

// ServerSecretCiphertexts returns the sealed values of every unrevoked
// secret belonging to a database instance on serverID — the set the log
// endpoints redact from that server's container output.
func (s *Store) ServerSecretCiphertexts(ctx context.Context, serverID string) ([][]byte, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.value_ciphertext FROM secrets s
		JOIN database_instances d ON d.id = s.database_id
		WHERE d.server_id = $1 AND s.revoked_at IS NULL
	`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var ct []byte
		if err := rows.Scan(&ct); err != nil {
			return nil, err
		}
		out = append(out, ct)
	}
	return out, rows.Err()
}

// ListSecretsForDatabase returns a database instance's secrets, admin
// credential first, then newest first.
func (s *Store) ListSecretsForDatabase(ctx context.Context, databaseID string) ([]Secret, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+secretColumns+` FROM secrets WHERE database_id = $1
		ORDER BY (kind = 'admin') DESC, (kind = 'token') DESC, created_at DESC
	`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Secret{}
	for rows.Next() {
		sec, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// LinkSecretsToDatabase sets database_id on secrets created before their
// instance row existed (the instance needs the deployment, which needs
// the secrets' references).
func (s *Store) LinkSecretsToDatabase(ctx context.Context, databaseID string, ids []string) error {
	_, err := s.pool.Exec(ctx, `UPDATE secrets SET database_id = $1 WHERE id = ANY($2::uuid[])`, databaseID, ids)
	return err
}

// DeleteSecrets removes secrets outright — only used to clean up after a
// database creation that failed before anything referenced them.
func (s *Store) DeleteSecrets(ctx context.Context, ids []string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM secrets WHERE id = ANY($1::uuid[])`, ids)
	return err
}

// ReplaceSecretValue stores a rotated value and bumps the version. The
// one-time download is re-armed, since the file downloaded earlier no
// longer holds a working credential.
func (s *Store) ReplaceSecretValue(ctx context.Context, id string, ciphertext []byte) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE secrets SET value_ciphertext = $2, version = version + 1, rotated_at = now(), downloaded_at = NULL
		WHERE id = $1
	`, id, ciphertext)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ClaimSecretDownload marks a secret's one-time download as used. It
// returns false if it had already been claimed, atomically, so two
// concurrent downloads can't both succeed.
func (s *Store) ClaimSecretDownload(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE secrets SET downloaded_at = now() WHERE id = $1 AND downloaded_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// RevokeSecret marks a secret revoked.
func (s *Store) RevokeSecret(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE secrets SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

// ExpiredTemporarySecrets returns temporary credentials whose expiry has
// passed but that haven't been revoked yet — the scheduler drops their
// database users and revokes them.
func (s *Store) ExpiredTemporarySecrets(ctx context.Context, now time.Time) ([]Secret, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+secretColumns+` FROM secrets
		WHERE kind = 'temporary' AND revoked_at IS NULL AND expires_at <= $1
	`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Secret{}
	for rows.Next() {
		sec, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// SecretGrant is one user's explicit access to a secret.
type SecretGrant struct {
	SecretID  string     `json:"secretId"`
	UserID    string     `json:"userId"`
	Email     string     `json:"email"`
	GrantedBy *string    `json:"grantedBy,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// ListSecretGrants returns a secret's grants, including expired ones (the
// UI shows them as expired).
func (s *Store) ListSecretGrants(ctx context.Context, secretID string) ([]SecretGrant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.secret_id, g.user_id, u.email, g.granted_by, g.expires_at, g.created_at
		FROM secret_grants g JOIN users u ON u.id = g.user_id
		WHERE g.secret_id = $1 ORDER BY u.email
	`, secretID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecretGrant{}
	for rows.Next() {
		var g SecretGrant
		if err := rows.Scan(&g.SecretID, &g.UserID, &g.Email, &g.GrantedBy, &g.ExpiresAt, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpsertSecretGrant grants (or re-grants, replacing the expiry) userID
// access to a secret.
func (s *Store) UpsertSecretGrant(ctx context.Context, secretID, userID, grantedBy string, expiresAt *time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO secret_grants (secret_id, user_id, granted_by, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (secret_id, user_id) DO UPDATE SET granted_by = $3, expires_at = $4, created_at = now()
	`, secretID, userID, nullableUUID(grantedBy), expiresAt)
	return err
}

// DeleteSecretGrant revokes userID's grant on a secret.
func (s *Store) DeleteSecretGrant(ctx context.Context, secretID, userID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM secret_grants WHERE secret_id = $1 AND user_id = $2`, secretID, userID)
	return err
}

// HasActiveSecretGrant reports whether userID holds an unexpired grant on
// secretID.
func (s *Store) HasActiveSecretGrant(ctx context.Context, secretID, userID string, now time.Time) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM secret_grants
			WHERE secret_id = $1 AND user_id = $2 AND (expires_at IS NULL OR expires_at > $3)
		)
	`, secretID, userID, now).Scan(&ok)
	return ok, err
}
