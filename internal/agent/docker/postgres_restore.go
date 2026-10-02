package docker

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/docker/docker/client"
)

var postgresRestoreIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// SyncPostgresPassword makes a refreshed instance's vaulted credential
// valid after its physical data files, including pg_authid, came from a
// different instance. It runs over PostgreSQL's trusted local socket.
func SyncPostgresPassword(ctx context.Context, cli *client.Client, deploymentID, username, database, password string) error {
	if !postgresRestoreIdentifier.MatchString(username) || !postgresRestoreIdentifier.MatchString(database) || password == "" {
		return fmt.Errorf("invalid PostgreSQL refresh credential")
	}
	sql := fmt.Sprintf(`ALTER ROLE "%s" WITH PASSWORD '%s'`, username, strings.ReplaceAll(password, "'", "''"))
	containerName := "pe-" + deploymentID + "-db"
	session, err := StartExec(ctx, cli, containerName, []string{"psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", username, "-d", database, "-c", sql}, 80, 24)
	if err != nil {
		return fmt.Errorf("could not start PostgreSQL credential sync: %w", err)
	}
	defer session.Conn.Close()
	if _, err := io.Copy(io.Discard, session.Conn); err != nil {
		return fmt.Errorf("PostgreSQL credential sync output failed: %w", err)
	}
	code, err := session.ExitCode(ctx)
	if err != nil {
		return fmt.Errorf("could not inspect PostgreSQL credential sync: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("PostgreSQL rejected the target credential")
	}
	return nil
}
