package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

// backupHelperImage is a small, always-available image used purely to
// give the Docker daemon something to mount volumes onto — it is never
// started, so its actual contents don't matter.
const backupHelperImage = "busybox:latest"
const backupMountRoot = "/backup"

// BackupVolumes snapshots every named volume labeled with deploymentID
// into a single tar stream. It works via a throwaway helper container
// (created but never started) with each volume mounted read-only, then
// CopyFromContainer to read them out — no commands run inside the
// container, and no arbitrary compose-file command is trusted, since the
// only thing being read is the Docker-managed volume storage itself.
//
// The returned ReadCloser must be closed by the caller (this removes the
// helper container as a side effect of Close, via an internal wrapper) —
// callers should not skip closing it even on an error path partway
// through reading.
func BackupVolumes(ctx context.Context, cli *client.Client, deploymentID string) (io.ReadCloser, error) {
	volumeNames, err := listDeploymentVolumes(ctx, cli, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("failed to list volumes: %w", err)
	}

	if len(volumeNames) == 0 {
		// A stateless stack (e.g. nginx-hello) has nothing to snapshot —
		// that's a valid, successful backup of zero bytes of state, not
		// an error.
		return io.NopCloser(emptyTarReader()), nil
	}

	if err := pullImage(ctx, cli, backupHelperImage); err != nil {
		return nil, fmt.Errorf("failed to pull backup helper image: %w", err)
	}

	mounts := make([]mount.Mount, 0, len(volumeNames))
	for _, name := range volumeNames {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeVolume,
			Source:   name,
			Target:   backupMountRoot + "/" + name,
			ReadOnly: true,
		})
	}

	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image:  backupHelperImage,
		Labels: map[string]string{labelDeploymentID: deploymentID},
	}, &container.HostConfig{Mounts: mounts}, nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create backup helper container: %w", err)
	}
	removeHelper := func() {
		_ = cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
	}

	reader, _, err := cli.CopyFromContainer(ctx, resp.ID, backupMountRoot)
	if err != nil {
		removeHelper()
		return nil, fmt.Errorf("failed to read volumes from backup helper: %w", err)
	}

	return &closeFunc{ReadCloser: reader, fn: removeHelper}, nil
}

// closeFunc runs an extra cleanup func when the wrapped ReadCloser closes.
type closeFunc struct {
	io.ReadCloser
	fn func()
}

func (c *closeFunc) Close() error {
	err := c.ReadCloser.Close()
	c.fn()
	return err
}

func listDeploymentVolumes(ctx context.Context, cli *client.Client, deploymentID string) ([]string, error) {
	vols, err := cli.VolumeList(ctx, volume.ListOptions{
		Filters: filters.NewArgs(filters.Arg("label", labelDeploymentID+"="+deploymentID)),
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(vols.Volumes))
	for _, v := range vols.Volumes {
		names = append(names, v.Name)
	}
	return names, nil
}

func emptyTarReader() *bytes.Reader {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.Close()
	return bytes.NewReader(buf.Bytes())
}
