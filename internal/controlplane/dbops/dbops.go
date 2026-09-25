// Package dbops performs credential operations on running database
// instances — rotating the admin password in place, and issuing and
// dropping temporary users — by running the catalog's engine-specific
// commands inside the instance's primary container. Shared by the REST
// API and the scheduler (which drops expired temporary users).
package dbops

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbcatalog"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/vault"
)

// commandTimeout bounds one in-container command. Generous: a database
// under load, or a JVM-based client, can take a while to connect.
const commandTimeout = 60 * time.Second

// MaxTemporaryTTL is the longest a temporary credential may live.
const MaxTemporaryTTL = 30 * 24 * time.Hour

// Ops runs credential operations.
type Ops struct {
	log        *slog.Logger
	st         *store.Store
	vault      *vault.Vault
	dispatcher *deploy.Dispatcher
	relay      *deploy.ExecStreamRelay
}

// New builds an Ops.
func New(log *slog.Logger, st *store.Store, v *vault.Vault, dispatcher *deploy.Dispatcher, relay *deploy.ExecStreamRelay) *Ops {
	return &Ops{log: log, st: st, vault: v, dispatcher: dispatcher, relay: relay}
}

// CommandError is an in-container command that ran but failed. Output has
// every credential involved already masked.
type CommandError struct {
	ExitCode int64
	Output   string
}

func (e *CommandError) Error() string {
	out := strings.TrimSpace(e.Output)
	if len(out) > 500 {
		out = out[len(out)-500:]
	}
	if out == "" {
		return fmt.Sprintf("command exited with status %d", e.ExitCode)
	}
	return fmt.Sprintf("command exited with status %d: %s", e.ExitCode, out)
}

// PrimaryContainer is the name of the first replica of an instance's
// primary service (see agent docker.containerName).
func PrimaryContainer(inst *store.DatabaseInstance) string {
	return "pe-" + inst.DeploymentID + "-" + inst.PrimaryService
}

// run executes argv in the instance's primary container. Every value in
// secrets is masked out of the returned output and error.
func (o *Ops) run(ctx context.Context, inst *store.DatabaseInstance, argv []string, secrets []string) error {
	res, err := deploy.RunCommand(ctx, o.dispatcher, o.relay, inst.ServerID, PrimaryContainer(inst), argv, commandTimeout)
	if err != nil {
		if errors.Is(err, deploy.ErrAgentNotConnected) {
			return err
		}
		return errors.New(vault.Redact(err.Error(), secrets))
	}
	if res.ExitCode != 0 {
		return &CommandError{ExitCode: res.ExitCode, Output: vault.Redact(res.Output, secrets)}
	}
	return nil
}

// Context loads what the engine's commands need: the admin username,
// database, current admin password, and token.
func (o *Ops) Context(ctx context.Context, inst *store.DatabaseInstance) (dbcatalog.CommandContext, error) {
	c := dbcatalog.CommandContext{Username: inst.AdminUsername, Database: inst.DatabaseName}
	if inst.AdminSecretID == nil {
		return c, errors.New("instance has no administrator credential")
	}
	_, pw, err := o.vault.Open(ctx, *inst.AdminSecretID)
	if err != nil {
		return c, err
	}
	c.Password = pw
	secrets, err := o.st.ListSecretsForDatabase(ctx, inst.ID)
	if err != nil {
		return c, err
	}
	for _, s := range secrets {
		if s.Kind == "token" && s.RevokedAt == nil {
			if _, tok, err := o.vault.Open(ctx, s.ID); err == nil {
				c.Token = tok
			}
			break
		}
	}
	return c, nil
}

// RotateInPlace changes the admin password inside the running database
// and then stores it. The database is changed first: if that fails
// nothing has changed anywhere. If the database accepted the new password
// but saving it fails, the new password is returned alongside the error
// so the caller can hand it to the user rather than lose it.
func (o *Ops) RotateInPlace(ctx context.Context, inst *store.DatabaseInstance, e *dbcatalog.Engine) (unsaved string, err error) {
	c, err := o.Context(ctx, inst)
	if err != nil {
		return "", err
	}
	c.NewPassword, err = vault.GeneratePassword(32)
	if err != nil {
		return "", err
	}
	argv, err := e.RotateCommand(c)
	if err != nil {
		return "", err
	}
	if err := o.run(ctx, inst, argv, []string{c.Password, c.NewPassword, c.Token}); err != nil {
		return "", err
	}
	var saveErr error
	for attempt := 0; attempt < 3; attempt++ {
		if saveErr = o.vault.Replace(ctx, *inst.AdminSecretID, c.NewPassword); saveErr == nil {
			return "", nil
		}
		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}
	o.log.Error("database password rotated but the new value could not be stored", "database_id", inst.ID, "error", saveErr)
	return c.NewPassword, fmt.Errorf("the database now uses a new password, but storing it failed: %w", saveErr)
}

// CreateTemporaryUser creates a read-only login that expires after ttl,
// and stores its credential (kind "temporary") owned by ownerID.
func (o *Ops) CreateTemporaryUser(ctx context.Context, inst *store.DatabaseInstance, e *dbcatalog.Engine, ownerID string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > MaxTemporaryTTL {
		return "", fmt.Errorf("lifetime must be between 1 minute and %d days", int(MaxTemporaryTTL.Hours()/24))
	}
	c, err := o.Context(ctx, inst)
	if err != nil {
		return "", err
	}
	user, err := temporaryUsername()
	if err != nil {
		return "", err
	}
	password, err := vault.GeneratePassword(32)
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(ttl).UTC()
	argv, err := e.CreateTemporaryUserCommand(c, user, password, expires)
	if err != nil {
		return "", err
	}
	if err := o.run(ctx, inst, argv, []string{c.Password, password}); err != nil {
		return "", err
	}
	id, err := o.vault.Create(ctx, vault.NewSecret{
		Name: "Temporary read-only login", Kind: "temporary", Username: user, Value: password,
		OwnerID: ownerID, DatabaseID: inst.ID, ExpiresAt: &expires,
	})
	if err != nil {
		// Don't leave a login behind that nobody has the password for.
		if dropArgv, derr := e.DropTemporaryUserCommand(c, user); derr == nil {
			_ = o.run(ctx, inst, dropArgv, []string{c.Password})
		}
		return "", err
	}
	return id, nil
}

// DropTemporaryUser removes a temporary login from the database and
// revokes its credential.
func (o *Ops) DropTemporaryUser(ctx context.Context, inst *store.DatabaseInstance, e *dbcatalog.Engine, sec *store.Secret) error {
	if sec.Kind != "temporary" {
		return errors.New("only temporary credentials can be revoked")
	}
	c, err := o.Context(ctx, inst)
	if err != nil {
		return err
	}
	argv, err := e.DropTemporaryUserCommand(c, sec.Username)
	if err != nil {
		return err
	}
	if err := o.run(ctx, inst, argv, []string{c.Password}); err != nil {
		return err
	}
	return o.st.RevokeSecret(ctx, sec.ID)
}

// temporaryUsername returns "tmp_" plus 10 random lowercase letters and
// digits — a valid identifier on every engine.
func temporaryUsername() (string, error) {
	const set = "abcdefghijkmnopqrstuvwxyz23456789"
	b := make([]byte, 10)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		if err != nil {
			return "", err
		}
		b[i] = set[n.Int64()]
	}
	return "tmp_" + string(b), nil
}
