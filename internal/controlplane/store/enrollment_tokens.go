package store

import (
	"context"
	"errors"
	"time"
)

// ErrTokenInvalid covers an enrollment token that doesn't exist, has
// already been consumed, or has expired — deliberately not distinguished
// so we don't leak which case it was to the caller.
var ErrTokenInvalid = errors.New("enrollment token invalid or expired")

// CreateEnrollmentToken records a one-time enrollment token (already
// hashed by the caller) with the given expiry.
func (s *Store) CreateEnrollmentToken(ctx context.Context, tokenHash string, createdBy string, expiresAt time.Time) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO enrollment_tokens (token_hash, created_by, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id
	`, tokenHash, createdBy, expiresAt).Scan(&id)
	return id, err
}

// ConsumeEnrollmentToken atomically checks that tokenHash is unexpired and
// unconsumed, and marks it consumed. Returns ErrTokenInvalid if not.
func (s *Store) ConsumeEnrollmentToken(ctx context.Context, tokenHash string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE enrollment_tokens
		SET consumed_at = now()
		WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > now()
	`, tokenHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTokenInvalid
	}
	return nil
}

// LinkEnrollmentTokenToServer records which server a consumed token
// enrolled, for audit purposes.
func (s *Store) LinkEnrollmentTokenToServer(ctx context.Context, tokenHash, serverID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE enrollment_tokens SET server_id = $2 WHERE token_hash = $1
	`, tokenHash, serverID)
	return err
}
