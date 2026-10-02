package docker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

// VolumeSummary is a snapshot of one locally-present named volume, from the
// same VolumeList call `docker volume ls` uses, plus InUseBy which is
// computed by cross-referencing every container's mounts in the same pass
// (see volumeUsage below) — an empty InUseBy means the volume is orphaned.
type VolumeSummary struct {
	Name        string
	Driver      string
	Mountpoint  string
	Labels      map[string]string
	SizeBytes   int64
	InUseBy     []string
	CreatedUnix int64
}

// VolumeDetail is the on-demand detail for one volume (metadata-browse
// view), same shape as VolumeSummary since Docker's VolumeInspect doesn't
// offer anything richer than VolumeList already does for a single volume.
type VolumeDetail struct {
	Found        bool
	ErrorMessage string
	Volume       VolumeSummary
}

// ListVolumes returns every volume present on the host, each annotated with
// the containers currently mounting it — for the on-demand Volumes tab and
// for orphan detection (deliberately not part of the heartbeat, same
// reasoning as ListImages/ListNetworks).
func ListVolumes(ctx context.Context, cli *client.Client) ([]VolumeSummary, error) {
	resp, err := cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, err
	}

	usage, err := volumeUsage(ctx, cli)
	if err != nil {
		return nil, err
	}

	summaries := make([]VolumeSummary, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		if v == nil {
			continue
		}
		summaries = append(summaries, summarizeVolume(*v, usage[v.Name]))
	}
	return summaries, nil
}

// volumeUsage builds a map from volume name to the IDs of every container
// currently mounting it, from one ContainerList(All: true) call — the same
// single-pass approach ListContainers uses, shared here so ListVolumes
// doesn't need a second, more expensive per-volume lookup.
func volumeUsage(ctx context.Context, cli *client.Client) (map[string][]string, error) {
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	usage := make(map[string][]string)
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type != mount.TypeVolume || m.Name == "" {
				continue
			}
			usage[m.Name] = append(usage[m.Name], c.ID)
		}
	}
	return usage, nil
}

func summarizeVolume(v volume.Volume, inUseBy []string) VolumeSummary {
	var sizeBytes int64
	if v.UsageData != nil && v.UsageData.Size >= 0 {
		sizeBytes = v.UsageData.Size
	}
	return VolumeSummary{
		Name:        v.Name,
		Driver:      v.Driver,
		Mountpoint:  v.Mountpoint,
		Labels:      v.Labels,
		SizeBytes:   sizeBytes,
		InUseBy:     inUseBy,
		CreatedUnix: parseVolumeCreated(v.CreatedAt),
	}
}

// parseVolumeCreated converts VolumeInspect/VolumeList's RFC3339 CreatedAt
// string into a Unix timestamp — same "empty/unparseable means unknown (0)"
// convention as images.go's parseImageCreated.
func parseVolumeCreated(createdAt string) int64 {
	if createdAt == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// CreateVolume creates a new named volume, erroring if one by that name
// already exists — unlike ensureStandaloneVolume's idempotent
// create-if-missing (used internally by container create), this is a
// user-driven create that should surface "already exists" rather than
// silently succeed.
func CreateVolume(ctx context.Context, cli *client.Client, name, driver string, labels map[string]string) error {
	if _, err := cli.VolumeInspect(ctx, name); err == nil {
		return fmt.Errorf("volume %q already exists", name)
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	_, err := cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Driver: driver,
		Labels: labels,
	})
	return err
}

// RemoveVolume removes name, refusing (with a clear error) if it's
// currently in use by any container and force is not set — a courtesy
// check ahead of Docker's own engine-level refusal, which VolumeRemove
// still enforces regardless (force here only bypasses this agent-side
// check, not Docker's).
func RemoveVolume(ctx context.Context, cli *client.Client, name string, force bool) error {
	if !force {
		usage, err := volumeUsage(ctx, cli)
		if err != nil {
			return err
		}
		if inUseBy := usage[name]; len(inUseBy) > 0 {
			return fmt.Errorf("volume %q is in use by container(s) %v", name, inUseBy)
		}
	}
	err := cli.VolumeRemove(ctx, name, force)
	if err != nil && errdefs.IsNotFound(err) {
		return errors.New("no such volume: " + name)
	}
	return err
}

// InspectVolume returns name's full detail. A not-found or otherwise failed
// inspect is reported via Found/ErrorMessage rather than a returned error,
// matching InspectImage's convention.
func InspectVolume(ctx context.Context, cli *client.Client, name string) VolumeDetail {
	v, err := cli.VolumeInspect(ctx, name)
	if err != nil {
		return VolumeDetail{Found: false, ErrorMessage: err.Error()}
	}

	usage, err := volumeUsage(ctx, cli)
	if err != nil {
		return VolumeDetail{Found: false, ErrorMessage: err.Error()}
	}

	return VolumeDetail{Found: true, Volume: summarizeVolume(v, usage[name])}
}
