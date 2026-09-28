package docker

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// DumpPostgres streams a consistent pg_dump custom archive from the source
// container. Binary stdout is Docker-demultiplexed rather than using a PTY.
func DumpPostgres(ctx context.Context, cli *client.Client, deploymentID, username, database string) (io.ReadCloser, error) {
	if !postgresRestoreIdentifier.MatchString(username) || !postgresRestoreIdentifier.MatchString(database) {
		return nil, fmt.Errorf("invalid PostgreSQL dump identifiers")
	}
	name := "pe-" + deploymentID + "-db"
	created, err := cli.ContainerExecCreate(ctx, name, container.ExecOptions{Cmd: []string{"pg_dump", "-Fc", "--no-owner", "--no-acl", "-U", username, "-d", database}, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return nil, err
	}
	attached, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, err
	}
	reader, writer := io.Pipe()
	go func() {
		defer attached.Close()
		_, err := stdcopy.StdCopy(writer, io.Discard, attached.Reader)
		if err == nil {
			var inspect container.ExecInspect
			inspect, err = cli.ContainerExecInspect(ctx, created.ID)
			if err == nil && inspect.ExitCode != 0 {
				err = fmt.Errorf("pg_dump exited with status %d", inspect.ExitCode)
			}
		}
		writer.CloseWithError(err)
	}()
	return reader, nil
}

// RestorePostgresLogical replaces objects in an already running target
// database from a custom pg_dump archive. pg_restore runs in one transaction
// and its binary stdin is attached without a PTY.
func RestorePostgresLogical(ctx context.Context, cli *client.Client, deploymentID, username, database string, archive io.Reader) error {
	if !postgresRestoreIdentifier.MatchString(username) || !postgresRestoreIdentifier.MatchString(database) {
		return fmt.Errorf("invalid PostgreSQL restore identifiers")
	}
	name := "pe-" + deploymentID + "-db"
	created, err := cli.ContainerExecCreate(ctx, name, container.ExecOptions{Cmd: []string{
		"pg_restore", "--clean", "--if-exists", "--no-owner", "--no-acl", "--exit-on-error", "--single-transaction", "-U", username, "-d", database,
	}, AttachStdin: true, AttachStdout: false, AttachStderr: false})
	if err != nil {
		return err
	}
	attached, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return err
	}
	defer attached.Close()
	if _, err := io.Copy(attached.Conn, archive); err != nil {
		return fmt.Errorf("streaming PostgreSQL archive failed: %w", err)
	}
	if err := attached.CloseWrite(); err != nil {
		return fmt.Errorf("closing PostgreSQL archive stream failed: %w", err)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		inspect, err := cli.ContainerExecInspect(ctx, created.ID)
		if err != nil {
			return err
		}
		if !inspect.Running {
			if inspect.ExitCode != 0 {
				return fmt.Errorf("pg_restore exited with status %d", inspect.ExitCode)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
