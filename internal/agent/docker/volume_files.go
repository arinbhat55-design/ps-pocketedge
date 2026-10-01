package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

const (
	VolumeFileMaxBytes    = 1 << 20
	volumeFileMaxEntries  = 200
	volumeFileOutputLimit = 128 << 10
	volumeFilesMount      = "/data"
)

type VolumeFileEntry struct {
	Name        string
	IsDirectory bool
	IsSymlink   bool
	SizeBytes   int64
}

// volumeRelativePath rejects absolute, parent, and platform-specific paths.
func volumeRelativePath(raw string, allowRoot bool) (string, error) {
	if strings.ContainsAny(raw, "\\\x00") || strings.HasPrefix(raw, "/") || strings.Contains(raw, ":") {
		return "", errors.New("volume path must be relative")
	}
	clean := path.Clean(raw)
	if clean == "." {
		if allowRoot {
			return ".", nil
		}
		return "", errors.New("a file path is required")
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("volume path escapes its root")
	}
	return clean, nil
}

func volumeFileContainer(ctx context.Context, cli *client.Client, name string, readonly bool, start bool) (string, func(), error) {
	if _, err := cli.VolumeInspect(ctx, name); err != nil {
		return "", nil, fmt.Errorf("volume %q: %w", name, err)
	}
	if err := pullImage(ctx, cli, backupHelperImage); err != nil {
		return "", nil, fmt.Errorf("volume helper image: %w", err)
	}
	config := &container.Config{Image: backupHelperImage, Cmd: []string{"sleep", "120"}, WorkingDir: volumeFilesMount}
	host := &container.HostConfig{
		Mounts:      []mount.Mount{{Type: mount.TypeVolume, Source: name, Target: volumeFilesMount, ReadOnly: readonly}},
		NetworkMode: "none", ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"},
	}
	resp, err := cli.ContainerCreate(ctx, config, host, nil, nil, "")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true}) }
	if start {
		if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return resp.ID, cleanup, nil
}

// checkVolumePath checks every existing component for symlinks. For a new
// file, only the leaf may be absent. Writers also require the volume not be
// mounted by another container, avoiding concurrent changes during writes.
func checkVolumePath(ctx context.Context, cli *client.Client, containerID, relative string, newLeaf bool) (container.PathStat, error) {
	var stat container.PathStat
	current := volumeFilesMount
	parts := strings.Split(relative, "/")
	if relative == "." {
		parts = nil
	}
	for i, part := range parts {
		current = path.Join(current, part)
		var err error
		stat, err = cli.ContainerStatPath(ctx, containerID, current)
		if newLeaf && i == len(parts)-1 && errdefs.IsNotFound(err) {
			return container.PathStat{}, nil
		}
		if err != nil {
			return container.PathStat{}, err
		}
		if stat.Mode&os.ModeSymlink != 0 || stat.LinkTarget != "" {
			return container.PathStat{}, errors.New("symlink paths are not allowed")
		}
		if i < len(parts)-1 && !stat.Mode.IsDir() {
			return container.PathStat{}, errors.New("parent path is not a directory")
		}
	}
	if relative == "." {
		return cli.ContainerStatPath(ctx, containerID, volumeFilesMount)
	}
	return stat, nil
}

func volumeUnused(ctx context.Context, cli *client.Client, name string) error {
	usage, err := volumeUsage(ctx, cli)
	if err != nil {
		return err
	}
	if len(usage[name]) != 0 {
		return fmt.Errorf("volume %q is in use; stop its containers before editing or cloning", name)
	}
	return nil
}

// ListVolumeFiles lists immediate children. It starts a network-isolated
// helper briefly so find can enumerate names without reading file contents.
func ListVolumeFiles(ctx context.Context, cli *client.Client, volumeName, rawPath string) ([]VolumeFileEntry, error) {
	relative, err := volumeRelativePath(rawPath, true)
	if err != nil {
		return nil, err
	}
	id, cleanup, err := volumeFileContainer(ctx, cli, volumeName, true, true)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	stat, err := checkVolumePath(ctx, cli, id, relative, false)
	if err != nil {
		return nil, err
	}
	if !stat.Mode.IsDir() {
		return nil, errors.New("path is not a directory")
	}
	base := path.Join(volumeFilesMount, relative)
	execResp, err := cli.ContainerExecCreate(ctx, id, container.ExecOptions{Cmd: []string{"find", base, "-mindepth", "1", "-maxdepth", "1", "-print0"}, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return nil, err
	}
	attached, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	_, copyErr := stdcopy.StdCopy(&stdout, &stderr, io.LimitReader(attached.Reader, volumeFileOutputLimit+1))
	attached.Close()
	if copyErr != nil || stdout.Len()+stderr.Len() > volumeFileOutputLimit {
		return nil, errors.New("directory listing is too large")
	}
	inspected, err := cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return nil, err
	}
	if inspected.ExitCode != 0 {
		return nil, fmt.Errorf("could not list volume directory: %s", strings.TrimSpace(stderr.String()))
	}
	paths := bytes.Split(bytes.TrimSuffix(stdout.Bytes(), []byte{0}), []byte{0})
	if len(stdout.Bytes()) == 0 {
		return []VolumeFileEntry{}, nil
	}
	if len(paths) > volumeFileMaxEntries {
		return nil, fmt.Errorf("directory has more than %d entries", volumeFileMaxEntries)
	}
	entries := make([]VolumeFileEntry, 0, len(paths))
	for _, full := range paths {
		name := path.Base(string(full))
		if name == "." || name == ".." {
			continue
		}
		stat, err := cli.ContainerStatPath(ctx, id, string(full))
		if err != nil {
			return nil, err
		}
		entries = append(entries, VolumeFileEntry{Name: name, IsDirectory: stat.Mode.IsDir(), IsSymlink: stat.Mode&os.ModeSymlink != 0, SizeBytes: stat.Size})
	}
	return entries, nil
}

func ReadVolumeFile(ctx context.Context, cli *client.Client, volumeName, rawPath string) ([]byte, error) {
	relative, err := volumeRelativePath(rawPath, false)
	if err != nil {
		return nil, err
	}
	id, cleanup, err := volumeFileContainer(ctx, cli, volumeName, true, false)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	stat, err := checkVolumePath(ctx, cli, id, relative, false)
	if err != nil {
		return nil, err
	}
	if !stat.Mode.IsRegular() || stat.Size > VolumeFileMaxBytes {
		return nil, errors.New("file must be regular and at most 1 MiB")
	}
	archive, _, err := cli.CopyFromContainer(ctx, id, path.Join(volumeFilesMount, relative))
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	tr := tar.NewReader(archive)
	header, err := tr.Next()
	if err != nil {
		return nil, err
	}
	if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
		return nil, errors.New("file is not regular")
	}
	data, err := io.ReadAll(io.LimitReader(tr, VolumeFileMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > VolumeFileMaxBytes {
		return nil, errors.New("file exceeds 1 MiB")
	}
	return data, nil
}

func WriteVolumeFile(ctx context.Context, cli *client.Client, volumeName, rawPath string, content []byte) error {
	if len(content) > VolumeFileMaxBytes {
		return errors.New("file exceeds 1 MiB")
	}
	relative, err := volumeRelativePath(rawPath, false)
	if err != nil {
		return err
	}
	if err := volumeUnused(ctx, cli, volumeName); err != nil {
		return err
	}
	id, cleanup, err := volumeFileContainer(ctx, cli, volumeName, false, false)
	if err != nil {
		return err
	}
	defer cleanup()
	parent := path.Dir(relative)
	parentStat, err := checkVolumePath(ctx, cli, id, parent, false)
	if err != nil {
		return err
	}
	if !parentStat.Mode.IsDir() {
		return errors.New("parent path is not a directory")
	}
	stat, err := checkVolumePath(ctx, cli, id, relative, true)
	if err != nil {
		return err
	}
	mode := int64(0644)
	uid, gid := 0, 0
	if stat.Name != "" {
		if !stat.Mode.IsRegular() {
			return errors.New("target is not a regular file")
		}
		mode = int64(stat.Mode.Perm())
		archive, _, err := cli.CopyFromContainer(ctx, id, path.Join(volumeFilesMount, relative))
		if err != nil {
			return err
		}
		header, headerErr := tar.NewReader(archive).Next()
		_ = archive.Close()
		if headerErr != nil {
			return headerErr
		}
		uid, gid = header.Uid, header.Gid
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: path.Base(relative), Mode: mode, Uid: uid, Gid: gid, Size: int64(len(content)), ModTime: time.Now()}); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return cli.CopyToContainer(ctx, id, path.Join(volumeFilesMount, parent), &buf, container.CopyToContainerOptions{})
}

// CloneVolume copies an unused volume into a new Docker volume as a tar
// stream through the agent; the data is not loaded into memory or sent to
// the control plane. The new volume is removed if the copy fails.
func CloneVolume(ctx context.Context, cli *client.Client, source, target string) error {
	if source == "" || target == "" || source == target {
		return errors.New("source and a different target volume are required")
	}
	if err := volumeUnused(ctx, cli, source); err != nil {
		return err
	}
	if _, err := cli.VolumeInspect(ctx, source); err != nil {
		return err
	}
	if _, err := cli.VolumeInspect(ctx, target); err == nil {
		return fmt.Errorf("target volume %q already exists", target)
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	if err := CreateVolume(ctx, cli, target, "", map[string]string{"pspocketedge.cloned_from": source}); err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			_ = cli.VolumeRemove(context.Background(), target, true)
		}
	}()
	sourceID, sourceCleanup, err := volumeFileContainer(ctx, cli, source, true, false)
	if err != nil {
		return err
	}
	defer sourceCleanup()
	targetID, targetCleanup, err := volumeFileContainer(ctx, cli, target, false, false)
	if err != nil {
		return err
	}
	defer targetCleanup()
	archive, _, err := cli.CopyFromContainer(ctx, sourceID, volumeFilesMount+"/.")
	if err != nil {
		return err
	}
	defer archive.Close()
	if err := cli.CopyToContainer(ctx, targetID, volumeFilesMount, archive, container.CopyToContainerOptions{}); err != nil {
		return err
	}
	completed = true
	return nil
}
