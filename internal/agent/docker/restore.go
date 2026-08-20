package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// RestoreVolumes extracts a tar stream (as produced by BackupVolumes) back
// into cmd's deployment's named volumes. It uses the same mount-layout
// convention BackupVolumes wrote the archive with (each volume at
// /backup/<docker-volume-name>), via another disposable helper container
// (created but never started — the same reasoning as ensureVolumes' doc
// comment on not trusting/running anything from the compose file).
//
// Two things this function does deliberately, both found by testing this
// against a live Postgres deployment rather than assumed correct:
//
//  1. It stops and removes the deployment's current containers *before*
//     extracting, not after. A first version extracted first and let the
//     caller's later redeploy stop the old containers — but that stop is a
//     graceful shutdown, and a graceful Postgres shutdown checkpoints and
//     overwrites exactly the files just restored, silently undoing the
//     restore. Nothing may still have the volume open while it's rewritten.
//  2. It wipes each volume before extracting rather than overlaying the
//     tar onto whatever's already there. Extraction only adds/overwrites
//     paths present in the archive — it never deletes paths that aren't,
//     so leftover files (e.g. newer WAL segments Postgres wrote after the
//     backup was taken) would otherwise survive and get replayed forward
//     on the next start, again silently reverting the restore.
//
// This does not start any service containers itself: the caller (see
// stream.Runner.handleRestore) redeploys afterward, using the same
// compose/env, to bring the deployment back up against the restored data.
func RestoreVolumes(ctx context.Context, cli *client.Client, cmd *agentv1.RestoreCommand, tarStream io.Reader) error {
	project, err := parseCompose(ctx, cmd.GetComposeYaml(), cmd.GetEnv(), cmd.GetStackName())
	if err != nil {
		return fmt.Errorf("failed to parse compose file: %w", err)
	}

	if err := removeExisting(ctx, cli, cmd.GetDeploymentId()); err != nil {
		return fmt.Errorf("failed to stop existing containers before restore: %w", err)
	}

	volumeNames, err := recreateVolumesClean(ctx, cli, cmd.GetDeploymentId(), cmd.GetStackName(), project)
	if err != nil {
		return fmt.Errorf("failed to prepare volumes: %w", err)
	}

	if len(volumeNames) == 0 {
		// Nothing to restore into (a stateless stack) — still drain the
		// stream so the HTTP response is read to completion.
		_, err := io.Copy(io.Discard, tarStream)
		return err
	}

	if err := pullImage(ctx, cli, backupHelperImage); err != nil {
		return fmt.Errorf("failed to pull backup helper image: %w", err)
	}

	mounts := make([]mount.Mount, 0, len(volumeNames))
	for _, dockerName := range volumeNames {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeVolume,
			Source: dockerName,
			Target: backupMountRoot + "/" + dockerName,
		})
	}

	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image:  backupHelperImage,
		Labels: map[string]string{labelDeploymentID: cmd.GetDeploymentId()},
	}, &container.HostConfig{Mounts: mounts}, nil, nil, "")
	if err != nil {
		return fmt.Errorf("failed to create restore helper container: %w", err)
	}
	defer func() {
		_ = cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
	}()

	if err := cli.CopyToContainer(ctx, resp.ID, "/", tarStream, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("failed to extract backup into volumes: %w", err)
	}
	return nil
}

// recreateVolumesClean returns the same compose-volume-name -> Docker-
// volume-name mapping ensureVolumes does, but removes and recreates each
// volume empty first — see RestoreVolumes' doc comment on why a restore
// must not just overlay onto whatever's already there.
func recreateVolumesClean(ctx context.Context, cli *client.Client, deploymentID, stackName string, project *types.Project) (map[string]string, error) {
	resolved := make(map[string]string, len(project.Volumes))
	for name := range project.Volumes {
		dockerName := "pe-" + deploymentID + "-" + name

		if _, err := cli.VolumeInspect(ctx, dockerName); err == nil {
			if err := cli.VolumeRemove(ctx, dockerName, true); err != nil {
				return nil, fmt.Errorf("failed to remove existing volume %q for a clean restore: %w", dockerName, err)
			}
		} else if !errdefs.IsNotFound(err) {
			return nil, err
		}

		if _, err := cli.VolumeCreate(ctx, volume.CreateOptions{
			Name: dockerName,
			Labels: map[string]string{
				labelDeploymentID: deploymentID,
				labelStack:        stackName,
			},
		}); err != nil {
			return nil, err
		}
		resolved[name] = dockerName
	}
	return resolved, nil
}
