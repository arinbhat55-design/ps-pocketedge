package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// Run explicitly with RUN_DOCKER_INTEGRATION=1. It verifies the binary
// pg_dump/pg_restore path against PostgreSQL 16 and 17 containers.
func TestPostgresLogicalMigrationIntegration(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("requires local Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	sourceID := "logical-source-" + suffix
	targetID := "logical-target-" + suffix
	start := func(id, image string) {
		t.Helper()
		name := "pe-" + id + "-db"
		created, err := cli.ContainerCreate(ctx, &container.Config{Image: image, Env: []string{"POSTGRES_USER=admin", "POSTGRES_PASSWORD=integrationpw", "POSTGRES_DB=app"}}, nil, nil, nil, name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = cli.ContainerRemove(cleanupCtx, created.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
		})
		if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := runPostgresTestSQL(ctx, cli, id, "SELECT 1"); err == nil {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatalf("%s did not become ready", name)
	}
	start(sourceID, "postgres:16")
	start(targetID, "postgres:17")
	if _, err := runPostgresTestSQL(ctx, cli, sourceID, "CREATE TABLE migration_probe (value integer); INSERT INTO migration_probe VALUES (42)"); err != nil {
		t.Fatal(err)
	}
	archive, err := DumpPostgres(ctx, cli, sourceID, "admin", "app")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if err := RestorePostgresLogical(ctx, cli, targetID, "admin", "app", archive); err != nil {
		t.Fatal(err)
	}
	out, err := runPostgresTestSQL(ctx, cli, targetID, "SELECT value FROM migration_probe")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "42") {
		t.Fatalf("restored row missing: %q", out)
	}
}

func runPostgresTestSQL(ctx context.Context, cli *client.Client, deploymentID, sql string) (string, error) {
	session, err := StartExec(ctx, cli, "pe-"+deploymentID+"-db", []string{"psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "admin", "-d", "app", "-At", "-c", sql}, 80, 24)
	if err != nil {
		return "", err
	}
	defer session.Conn.Close()
	out, err := io.ReadAll(session.Conn)
	if err != nil {
		return "", err
	}
	code, err := session.ExitCode(ctx)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("psql exited %d: %s", code, out)
	}
	return string(out), nil
}

// Covers the actual Docker tar layout, cross-deployment volume remapping,
// and the administrator password after refreshing from another instance.
func TestPostgresPhysicalRefreshIntegration(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("requires local Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	sourceID, targetID := "physical-source-"+suffix, "physical-target-"+suffix
	createVolume := func(id string) string {
		t.Helper()
		name := "pe-" + id + "-pgdata"
		if _, err := cli.VolumeCreate(ctx, volume.CreateOptions{Name: name, Labels: map[string]string{labelDeploymentID: id, labelStack: "integration"}}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
			defer done()
			_ = cli.VolumeRemove(cleanupCtx, name, true)
		})
		return name
	}
	sourceVolume, targetVolume := createVolume(sourceID), createVolume(targetID)
	start := func(id, volumeName, password string) {
		t.Helper()
		name := "pe-" + id + "-db"
		created, err := cli.ContainerCreate(ctx, &container.Config{Image: "postgres:16", Env: []string{"POSTGRES_USER=admin", "POSTGRES_PASSWORD=" + password, "POSTGRES_DB=app"}, Labels: map[string]string{labelDeploymentID: id}}, &container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: volumeName, Target: "/var/lib/postgresql/data"}}}, nil, nil, name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
			defer done()
			_ = cli.ContainerRemove(cleanupCtx, created.ID, container.RemoveOptions{Force: true})
		})
		if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := runPostgresTestSQL(ctx, cli, id, "SELECT 1"); err == nil {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatalf("%s did not become ready", name)
	}
	start(sourceID, sourceVolume, "sourcepw")
	start(targetID, targetVolume, "targetpw")
	if _, err := runPostgresTestSQL(ctx, cli, sourceID, "CREATE TABLE refresh_probe (value integer); INSERT INTO refresh_probe VALUES (73)"); err != nil {
		t.Fatal(err)
	}
	if _, err := runPostgresTestSQL(ctx, cli, targetID, "CREATE TABLE stale_probe (value integer)"); err != nil {
		t.Fatal(err)
	}
	if err := cli.ContainerStop(ctx, "pe-"+sourceID+"-db", container.StopOptions{}); err != nil {
		t.Fatal(err)
	}
	archive, err := BackupVolumes(ctx, cli, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	compose := "services:\n  db:\n    image: postgres:16\n    volumes:\n      - pgdata:/var/lib/postgresql/data\nvolumes:\n  pgdata: {}\n"
	if err := RestoreVolumes(ctx, cli, &agentv1.RestoreCommand{DeploymentId: targetID, SourceDeploymentId: sourceID, StackName: "integration", ComposeYaml: compose}, archive); err != nil {
		t.Fatal(err)
	}
	start(targetID, targetVolume, "targetpw")
	if err := SyncPostgresPassword(ctx, cli, targetID, "admin", "app", "targetpw"); err != nil {
		t.Fatal(err)
	}
	if out, err := runPostgresTestSQL(ctx, cli, targetID, "SELECT value FROM refresh_probe"); err != nil || !strings.Contains(out, "73") {
		t.Fatalf("restored data: %q, %v", out, err)
	}
	if _, err := runPostgresTestSQL(ctx, cli, targetID, "SELECT * FROM stale_probe"); err == nil {
		t.Fatal("target-only table survived refresh")
	}
	name := "pe-" + targetID + "-db"
	created, err := cli.ContainerExecCreate(ctx, name, container.ExecOptions{Cmd: []string{"psql", "-h", "127.0.0.1", "-U", "admin", "-d", "app", "-At", "-c", "SELECT 1"}, Env: []string{"PGPASSWORD=targetpw"}, AttachStdout: true, AttachStderr: true})
	if err != nil {
		t.Fatal(err)
	}
	attached, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, attached.Reader)
	attached.Close()
	result, err := cli.ContainerExecInspect(ctx, created.ID)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("target password rejected: exit %d, %v", result.ExitCode, err)
	}
}
