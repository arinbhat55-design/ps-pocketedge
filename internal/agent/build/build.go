// Package build builds Docker images from Git repositories on the agent's
// own Docker daemon: clone the repository at an exact commit, send the
// build context to the daemon (honoring .dockerignore), and tag the result
// so a deploy on this server can run it without any registry.
package build

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/shirou/gopsutil/v4/disk"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

const (
	// DefaultTimeout bounds a build when the command doesn't say.
	DefaultTimeout = 30 * time.Minute
	// maxTimeout caps whatever the command asks for.
	maxTimeout = 3 * time.Hour
	// DefaultMaxContextBytes caps the build context sent to the daemon.
	DefaultMaxContextBytes = 1 << 30
	// minFreeDiskBytes is the free space a build needs to start: the
	// clone, the context, and the image layers all land on this host.
	minFreeDiskBytes = 1 << 30
)

// ContentKeyLabel labels each built image with what it was built from:
// the build settings and the Git tree of the build context.
const ContentKeyLabel = "pspocketedge.content-key"

// Options configures Run beyond what the command carries.
type Options struct {
	// WorkDir is where repositories are cloned; each build uses its own
	// temporary directory under it, removed when the build ends.
	WorkDir         string
	MaxContextBytes int64
}

// Result is a successful build.
type Result struct {
	ImageID string
	// Reused means the tag already existed, so nothing was built.
	Reused bool
}

// PhaseFunc reports a phase change with a human-readable message.
type PhaseFunc func(phase agentv1.BuildPhase, message string)

// Run builds cmd's image, writing build output to out and reporting
// CLONING and BUILDING through phase. The caller reports the outcome.
func Run(ctx context.Context, cli *client.Client, cmd *agentv1.BuildImageCommand, opts Options, phase PhaseFunc, out io.Writer) (*Result, error) {
	if err := validate(cmd); err != nil {
		return nil, err
	}
	timeout := DefaultTimeout
	if t := time.Duration(cmd.GetTimeoutSeconds()) * time.Second; t > 0 {
		timeout = min(t, maxTimeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tag := cmd.GetImageTag()
	if !cmd.GetNoCache() {
		if img, err := cli.ImageInspect(ctx, tag); err == nil {
			fmt.Fprintf(out, "Image %s already exists on this server (same commit and build settings) — reusing it.\n", tag)
			return &Result{ImageID: img.ID, Reused: true}, nil
		}
	}

	if err := os.MkdirAll(opts.WorkDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create build directory: %w", err)
	}
	if usage, err := disk.UsageWithContext(ctx, opts.WorkDir); err == nil && usage.Free < minFreeDiskBytes {
		return nil, fmt.Errorf("only %d MB of disk space free on this server — at least %d MB is needed to build", usage.Free>>20, minFreeDiskBytes>>20)
	}
	workDir, err := os.MkdirTemp(opts.WorkDir, "build-")
	if err != nil {
		return nil, fmt.Errorf("failed to create build directory: %w", err)
	}
	defer os.RemoveAll(workDir)
	repoDir := workDir + string(os.PathSeparator) + "src"

	phase(agentv1.BuildPhase_BUILD_PHASE_CLONING, fmt.Sprintf("cloning %s at %s", cmd.GetGitRef(), shortHash(cmd.GetGitCommit())))
	fmt.Fprintf(out, "Cloning %s (%s at %s)\n", cmd.GetRepoUrl(), cmd.GetGitRef(), shortHash(cmd.GetGitCommit()))
	commit, err := checkout(ctx, repoDir, source{
		URL: cmd.GetRepoUrl(), Ref: cmd.GetGitRef(), Commit: cmd.GetGitCommit(),
		Username: cmd.GetGitUsername(), Token: cmd.GetGitToken(),
	})
	if err != nil {
		return nil, contextError(ctx, timeout, err)
	}

	contextPath := cleanRel(cmd.GetContextPath())
	contextDir, err := resolveInside(repoDir, contextPath)
	if err != nil {
		return nil, fmt.Errorf("build context: %w", err)
	}
	dockerfile := cleanRel(cmd.GetDockerfile())
	if dockerfile == "." {
		dockerfile = "Dockerfile"
	}
	if _, err := resolveInside(contextDir, dockerfile); err != nil {
		return nil, fmt.Errorf("dockerfile: %w", err)
	}

	labels := make(map[string]string, len(cmd.GetLabels())+1)
	for k, v := range cmd.GetLabels() {
		labels[k] = v
	}
	if key := contentKey(cmd.GetSettingsKey(), commit, contextPath); key != "" {
		if !cmd.GetNoCache() {
			if id, ok := findBuiltImage(ctx, cli, key); ok {
				if err := cli.ImageTag(ctx, id, tag); err == nil {
					fmt.Fprintf(out, "Nothing in the build context or build settings changed since an earlier build — tagging that image as %s instead of rebuilding.\n", tag)
					return &Result{ImageID: id, Reused: true}, nil
				}
			}
		}
		labels[ContentKeyLabel] = key
	}

	phase(agentv1.BuildPhase_BUILD_PHASE_BUILDING, "building "+tag)
	fmt.Fprintf(out, "Building %s from %s (dockerfile %s)\n", tag, contextPath, dockerfile)

	maxBytes := opts.MaxContextBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxContextBytes
	}
	// Buildx uses BuildKit and reads the context directly from this checked
	// out repository. Check the same size limit as the SDK tar path first.
	if buildxAvailable(ctx) {
		if err := writeContextTar(io.Discard, contextDir, dockerfile, commit.Committer.When, maxBytes); err != nil {
			return nil, err
		}
		if err := buildWithBuildx(ctx, workDir, contextDir, dockerfile, tag, cmd, labels, out); err != nil {
			return nil, contextError(ctx, timeout, err)
		}
		img, err := cli.ImageInspect(ctx, tag)
		if err != nil {
			return nil, fmt.Errorf("BuildKit completed but image %s was not loaded into Docker: %w", tag, err)
		}
		return &Result{ImageID: img.ID}, nil
	}
	fmt.Fprintln(out, "Buildx is unavailable on this server; using Docker's classic builder.")
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(writeContextTar(pw, contextDir, dockerfile, commit.Committer.When, maxBytes))
	}()
	defer pr.Close()

	args := make(map[string]*string, len(cmd.GetBuildArgs()))
	for k, v := range cmd.GetBuildArgs() {
		args[k] = &v
	}
	resp, err := cli.ImageBuild(ctx, pr, build.ImageBuildOptions{
		Tags:        []string{tag},
		Dockerfile:  dockerfile,
		BuildArgs:   args,
		Target:      cmd.GetTarget(),
		Labels:      labels,
		NoCache:     cmd.GetNoCache(),
		Remove:      true,
		ForceRemove: true,
		// The classic builder takes the context as a plain tar upload;
		// BuildKit needs an interactive session the Engine SDK doesn't
		// provide on its own.
		Version: build.BuilderV1,
	})
	if err != nil {
		return nil, contextError(ctx, timeout, fmt.Errorf("failed to start the build: %w", err))
	}
	defer resp.Body.Close()

	imageID, err := readBuildOutput(resp.Body, out)
	if err != nil {
		return nil, contextError(ctx, timeout, err)
	}
	if img, err := cli.ImageInspect(ctx, tag); err == nil {
		imageID = img.ID
	} else if imageID == "" {
		return nil, fmt.Errorf("the build finished but image %s wasn't found: %w", tag, err)
	}
	return &Result{ImageID: imageID}, nil
}

// buildxAvailable keeps Docker Engine-only agents working. Buildx is an
// optional Docker CLI plugin; the Engine SDK cannot open the session it
// needs for a BuildKit build by itself.
func buildxAvailable(ctx context.Context) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(probe, "docker", "buildx", "version").Run() == nil
}

func buildWithBuildx(ctx context.Context, workDir, contextDir, dockerfile, tag string, cmd *agentv1.BuildImageCommand, labels map[string]string, out io.Writer) error {
	args := []string{"buildx", "build", "--load", "--progress=plain", "--tag", tag, "--file", filepath.Join(contextDir, filepath.FromSlash(dockerfile))}
	if target := cmd.GetTarget(); target != "" {
		args = append(args, "--target", target)
	}
	if cmd.GetNoCache() {
		args = append(args, "--no-cache")
	}
	buildEnv := os.Environ()
	for _, key := range sortedKeys(cmd.GetBuildArgs()) {
		if !validBuildArgName(key) {
			return fmt.Errorf("invalid build argument name %q", key)
		}
		// Buildx reads a bare --build-arg NAME from its environment. The
		// value stays out of the agent's process command line.
		args = append(args, "--build-arg", key)
		buildEnv = append(buildEnv, key+"="+cmd.GetBuildArgs()[key])
	}
	for _, key := range sortedKeys(labels) {
		args = append(args, "--label", key+"="+labels[key])
	}
	args = append(args, contextDir)
	buildCmd := exec.CommandContext(ctx, "docker", args...)
	buildCmd.Dir = workDir
	buildCmd.Env = buildEnv
	buildCmd.Stdout, buildCmd.Stderr = out, out
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("BuildKit build failed: %w", err)
	}
	return nil
}

func validBuildArgName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func validate(cmd *agentv1.BuildImageCommand) error {
	switch {
	case cmd.GetImageTag() == "":
		return errors.New("no image tag given")
	case cmd.GetRepoUrl() == "":
		return errors.New("no repository given")
	}
	for _, p := range []string{cmd.GetContextPath(), cmd.GetDockerfile()} {
		if strings.HasPrefix(p, "/") || cleanRel(p) == ".." || strings.HasPrefix(cleanRel(p), "../") {
			return fmt.Errorf("path %q must be relative and inside the repository", p)
		}
	}
	return nil
}

// contentKey identifies everything an image is built from: the build
// settings (settingsKey, from the control plane) and the build context's
// Git tree. Empty when either is unknown.
func contentKey(settingsKey string, commit *object.Commit, contextPath string) string {
	if settingsKey == "" {
		return ""
	}
	tree, err := contextTree(commit, contextPath)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(settingsKey + "\n" + tree))
	return hex.EncodeToString(sum[:])
}

// findBuiltImage looks for an image already built from contentKey.
func findBuiltImage(ctx context.Context, cli *client.Client, key string) (string, bool) {
	images, err := cli.ImageList(ctx, image.ListOptions{Filters: filters.NewArgs(filters.Arg("label", ContentKeyLabel+"="+key))})
	if err != nil || len(images) == 0 {
		return "", false
	}
	return images[0].ID, true
}

// cleanRel normalizes a slash-separated relative path ("" becomes ".").
func cleanRel(p string) string {
	return path.Clean("./" + strings.TrimSpace(p))
}

// contextError explains a failure caused by the build's own timeout.
func contextError(ctx context.Context, timeout time.Duration, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("build timed out after %s", timeout)
	}
	return err
}

// buildMessage is one line of the daemon's JSON build output.
type buildMessage struct {
	Stream      string `json:"stream"`
	Status      string `json:"status"`
	ID          string `json:"id"`
	Progress    string `json:"progress"`
	Error       string `json:"error"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	Aux json.RawMessage `json:"aux"`
}

// readBuildOutput copies the daemon's build output to out as plain text
// and returns the built image's ID, or the error the build failed with.
// Pull-progress bars are dropped: they're redrawn in place on a terminal
// and would be thousands of lines in a log.
func readBuildOutput(r io.Reader, out io.Writer) (string, error) {
	dec := json.NewDecoder(bufio.NewReader(r))
	var imageID string
	for {
		var m buildMessage
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return imageID, nil
			}
			return imageID, fmt.Errorf("failed to read build output: %w", err)
		}
		switch {
		case m.Error != "" || m.ErrorDetail != nil:
			msg := m.Error
			if msg == "" {
				msg = m.ErrorDetail.Message
			}
			fmt.Fprintln(out, msg)
			return "", fmt.Errorf("build failed: %s", strings.TrimSpace(msg))
		case m.Stream != "":
			io.WriteString(out, m.Stream)
		case m.Status != "" && m.Progress == "":
			if m.ID != "" {
				fmt.Fprintf(out, "%s: %s\n", m.ID, m.Status)
			} else {
				fmt.Fprintln(out, m.Status)
			}
		case len(m.Aux) > 0:
			var aux struct {
				ID string `json:"ID"`
			}
			if json.Unmarshal(m.Aux, &aux) == nil && aux.ID != "" {
				imageID = aux.ID
			}
		}
	}
}
