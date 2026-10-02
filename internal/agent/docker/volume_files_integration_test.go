package docker

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/client"
)

func TestVolumeFilesIntegration(t *testing.T) {
	if os.Getenv("RUN_VOLUME_INTEGRATION") != "1" {
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
	source, target := "pe-volume-files-"+suffix, "pe-volume-clone-"+suffix
	if err := CreateVolume(ctx, cli, source, "", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		_ = cli.VolumeRemove(cleanupCtx, target, true)
		_ = cli.VolumeRemove(cleanupCtx, source, true)
	})
	if err := WriteVolumeFile(ctx, cli, source, "hello.txt", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	entries, err := ListVolumeFiles(ctx, cli, source, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "hello.txt" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	data, err := ReadVolumeFile(ctx, cli, source, "hello.txt")
	if err != nil || string(data) != "hello" {
		t.Fatalf("read: %q, %v", data, err)
	}
	if err := CloneVolume(ctx, cli, source, target); err != nil {
		t.Fatal(err)
	}
	data, err = ReadVolumeFile(ctx, cli, target, "hello.txt")
	if err != nil || string(data) != "hello" {
		t.Fatalf("clone: %q, %v", data, err)
	}
}
