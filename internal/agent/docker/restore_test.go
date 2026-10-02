package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

func TestRemapBackupVolumeNames(t *testing.T) {
	var source bytes.Buffer
	tw := tar.NewWriter(&source)
	content := []byte("database contents")
	if err := tw.WriteHeader(&tar.Header{Name: "backup/pe-source-pgdata/base/1", Mode: 0600, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var target bytes.Buffer
	if err := remapBackupVolumeNames(&source, &target, "source", "target"); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&target)
	h, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != "backup/pe-target-pgdata/base/1" {
		t.Fatalf("name = %q", h.Name)
	}
	got, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content = %q", got)
	}
}

func TestRestoreVolumesRemapIntegration(t *testing.T) {
	if os.Getenv("RUN_DOCKER_INTEGRATION") != "1" {
		t.Skip("requires local Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	id := fmt.Sprintf("remap-%d", time.Now().UnixNano())
	name := "pe-" + id + "-pgdata"
	if _, err := cli.VolumeCreate(ctx, volume.CreateOptions{Name: name, Labels: map[string]string{labelDeploymentID: id}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = cli.VolumeRemove(c, name, true)
	})
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	data := []byte("probe")
	if err := tw.WriteHeader(&tar.Header{Name: "backup/pe-source-pgdata/probe", Mode: 0644, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  db:\n    image: busybox:latest\n    volumes:\n      - pgdata:/data\nvolumes:\n  pgdata: {}\n"
	if err := RestoreVolumes(ctx, cli, &agentv1.RestoreCommand{DeploymentId: id, SourceDeploymentId: "source", StackName: "integration", ComposeYaml: compose}, &archive); err != nil {
		t.Fatal(err)
	}
}
