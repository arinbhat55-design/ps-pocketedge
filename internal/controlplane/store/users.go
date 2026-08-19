package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("not found")

type User struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// CountUsers returns how many admin users exist, used to decide whether to
// seed the initial admin account on startup.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count)
	return count, err
}

// CreateUser inserts a new admin user with an already-hashed password.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash)
		VALUES ($1, $2)
		RETURNING id
	`, email, passwordHash).Scan(&id)
	return id, err
}

// GetUserByEmail looks up a user for login. Returns ErrNotFound if no user
// has that email.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, password_hash, created_at FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}
