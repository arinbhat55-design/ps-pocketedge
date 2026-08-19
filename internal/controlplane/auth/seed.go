package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"log/slog"
)

// UserStore is the subset of store.Store needed to seed the initial admin
// account, kept as an interface here so this package doesn't import store
// (store already imports auth's token helpers, so a direct import would
// cycle).
type UserStore interface {
	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, email, passwordHash string) (string, error)
}

// SeedAdmin creates the initial admin user if no users exist yet. If
// password is empty, a random one is generated and logged — the
// self-hosted-homelab-friendly "check the logs for your first-run
// password" pattern, since there's no other channel to deliver it through
// on a brand-new install.
func SeedAdmin(ctx context.Context, log *slog.Logger, us UserStore, email, password string) error {
	count, err := us.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	if email == "" {
		email = "admin@pspocketedge.local"
	}
	generated := password == ""
	if generated {
		var err error
		password, err = randomPassword()
		if err != nil {
			return err
		}
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := us.CreateUser(ctx, email, hash); err != nil {
		return err
	}

	if generated {
		log.Warn("seeded initial admin user with a generated password — save this now, it will not be shown again",
			"email", email, "password", password)
	} else {
		log.Info("seeded initial admin user", "email", email)
	}
	return nil
}

func randomPassword() (string, error) {
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}
