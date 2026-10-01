package build

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildArgName(t *testing.T) {
	for _, name := range []string{"VERSION", "BUILD_123", "_FLAG", "lower"} {
		if !validBuildArgName(name) {
			t.Errorf("rejected %q", name)
		}
	}
	for _, name := range []string{"", "1FLAG", "A=B", "A-B"} {
		if validBuildArgName(name) {
			t.Errorf("accepted %q", name)
		}
	}
}

func tarNames(t *testing.T, contextDir, dockerfile string) []string {
	t.Helper()
	var buf bytes.Buffer
	if err := writeContextTar(&buf, contextDir, dockerfile, time.Unix(0, 0), 0); err != nil {
		t.Fatalf("writeContextTar: %v", err)
	}
	var names []string
	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag != tar.TypeDir {
			names = append(names, hdr.Name)
		}
	}
	sort.Strings(names)
	return names
}

func TestContextTarHonorsDockerignore(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"Dockerfile":          "FROM scratch\n",
		".dockerignore":       "node_modules\n*.log\nsecrets/\n!keep.log\nDockerfile\n.dockerignore\n",
		"app.js":              "x",
		"debug.log":           "x",
		"keep.log":            "x",
		"node_modules/a/b.js": "x",
		"secrets/key":         "x",
		"src/main.go":         "x",
	})
	got := strings.Join(tarNames(t, dir, "Dockerfile"), ",")
	want := ".dockerignore,Dockerfile,app.js,keep.log,src/main.go"
	if got != want {
		t.Fatalf("archived %s, want %s", got, want)
	}
}

func TestContextTarKeepsDockerfileInIgnoredDir(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		".dockerignore":     "docker\n",
		"docker/Dockerfile": "FROM scratch\n",
		"docker/other":      "x",
		"main.go":           "x",
	})
	got := strings.Join(tarNames(t, dir, "docker/Dockerfile"), ",")
	if want := ".dockerignore,docker/Dockerfile,main.go"; got != want {
		t.Fatalf("archived %s, want %s", got, want)
	}
}

func TestContextTarArchivesSymlinksWithoutFollowing(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"Dockerfile": "FROM scratch\n"})
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "passwd")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeContextTar(&buf, dir, "Dockerfile", time.Unix(0, 0), 0); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			t.Fatal("symlink not archived")
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == "passwd" {
			if hdr.Typeflag != tar.TypeSymlink || hdr.Linkname != "/etc/passwd" || hdr.Size != 0 {
				t.Fatalf("symlink archived as %+v", hdr)
			}
			return
		}
	}
}

func TestContextTarSizeLimit(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"Dockerfile": "FROM scratch\n", "big": strings.Repeat("x", 2048)})
	err := writeContextTar(io.Discard, dir, "Dockerfile", time.Unix(0, 0), 1024)
	if !errors.Is(err, errContextTooLarge) {
		t.Fatalf("expected errContextTooLarge, got %v", err)
	}
}

func TestResolveInsideRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"app/Dockerfile": "FROM scratch\n"})
	if err := os.Symlink(os.TempDir(), filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveInside(dir, "app"); err != nil {
		t.Fatalf("app: %v", err)
	}
	for _, rel := range []string{"out", "../", "missing"} {
		if _, err := resolveInside(dir, rel); err == nil {
			t.Fatalf("%q: expected an error", rel)
		}
	}
}

func TestValidateRejectsAbsoluteAndParentPaths(t *testing.T) {
	for _, p := range []string{"/etc", "..", "../x", "a/../../x"} {
		cmd := &agentv1.BuildImageCommand{ImageTag: "t", RepoUrl: "u", ContextPath: p}
		if err := validate(cmd); err == nil {
			t.Fatalf("context %q: expected an error", p)
		}
	}
	if err := validate(&agentv1.BuildImageCommand{ImageTag: "t", RepoUrl: "u", ContextPath: "services/api"}); err != nil {
		t.Fatalf("valid context rejected: %v", err)
	}
}

func TestReadBuildOutput(t *testing.T) {
	stream := `{"stream":"Step 1/2 : FROM alpine\n"}
{"status":"Downloading","progress":"[==>   ]","id":"abc"}
{"status":"Pull complete","id":"abc"}
{"aux":{"ID":"sha256:123"}}
{"stream":"Successfully built 123\n"}
`
	var out bytes.Buffer
	id, err := readBuildOutput(strings.NewReader(stream), &out)
	if err != nil || id != "sha256:123" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if strings.Contains(out.String(), "Downloading") || !strings.Contains(out.String(), "abc: Pull complete") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}

	_, err = readBuildOutput(strings.NewReader(`{"errorDetail":{"message":"RUN false: exit 1"},"error":"RUN false: exit 1"}`), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("expected build failure, got %v", err)
	}
}

// commitAll creates or extends a Git repository at dir with files and
// returns the new commit's hash.
func commitAll(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		repo, err = git.PlainInit(dir, false)
	}
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, files)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	hash, err := wt.Commit("test", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	return hash.String()
}

// TestRunBuildsFromGit builds a real image on the local Docker daemon from
// a local repository, at an older commit than the branch tip, then checks
// that building the same tag again reuses it.
func TestRunBuildsFromGit(t *testing.T) {
	if testing.Short() {
		t.Skip("needs a Docker daemon")
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("no Docker client: %v", err)
	}
	ctx := context.Background()
	if _, err := cli.Ping(ctx); err != nil {
		t.Skipf("Docker daemon not reachable: %v", err)
	}

	repoDir := t.TempDir()
	first := commitAll(t, repoDir, map[string]string{
		"app/Dockerfile":    "FROM busybox:1.36\nARG GREETING\nCOPY message.txt /message.txt\nRUN echo \"$GREETING\" > /greeting.txt\n",
		"app/message.txt":   "first\n",
		"app/.dockerignore": "*.tmp\n",
		"app/junk.tmp":      "x",
	})
	commitAll(t, repoDir, map[string]string{"app/message.txt": "second\n"})
	// Changes nothing in the build context.
	third := commitAll(t, repoDir, map[string]string{"README.md": "docs\n"})

	tag := "pspe-build/test-agent-build:" + first[:12]
	tag3 := "pspe-build/test-agent-build:" + third[:12]
	tagSecond := "pspe-build/test-agent-build:second"
	t.Cleanup(func() {
		for _, tg := range []string{tag, tag3, tagSecond} {
			_, _ = cli.ImageRemove(context.Background(), tg, image.RemoveOptions{Force: true})
		}
	})

	cmd := &agentv1.BuildImageCommand{
		BuildId: "b1", ImageTag: tag, RepoUrl: repoDir, GitRef: "master", GitCommit: first,
		ContextPath: "app", BuildArgs: map[string]string{"GREETING": "hello"},
		Labels: map[string]string{"pspocketedge.build": "b1"}, SettingsKey: "settings-v1",
	}
	var phases []agentv1.BuildPhase
	phase := func(p agentv1.BuildPhase, _ string) { phases = append(phases, p) }
	var out bytes.Buffer
	res, err := Run(ctx, cli, cmd, Options{WorkDir: t.TempDir()}, phase, &out)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if buildxAvailable(ctx) && strings.Contains(out.String(), "using Docker's classic builder") {
		t.Fatalf("Buildx is available but the build fell back to the classic builder:\n%s", out.String())
	}
	if res.Reused || res.ImageID == "" {
		t.Fatalf("unexpected result %+v", res)
	}
	if len(phases) != 2 || phases[0] != agentv1.BuildPhase_BUILD_PHASE_CLONING || phases[1] != agentv1.BuildPhase_BUILD_PHASE_BUILDING {
		t.Fatalf("phases = %v", phases)
	}
	img, err := cli.ImageInspect(ctx, tag)
	if err != nil {
		t.Fatal(err)
	}
	if img.Config.Labels["pspocketedge.build"] != "b1" {
		t.Fatalf("labels = %v", img.Config.Labels)
	}

	res, err = Run(ctx, cli, cmd, Options{WorkDir: t.TempDir()}, phase, io.Discard)
	if err != nil || !res.Reused || res.ImageID != img.ID {
		t.Fatalf("second build: %+v, %v", res, err)
	}

	// The context changed since the first commit: a real build.
	cmd3 := &agentv1.BuildImageCommand{
		BuildId: "b3", ImageTag: tag3, RepoUrl: repoDir, GitRef: "master", GitCommit: third,
		ContextPath: "app", BuildArgs: map[string]string{"GREETING": "hello"}, SettingsKey: "settings-v1",
	}
	res3, err := Run(ctx, cli, cmd3, Options{WorkDir: t.TempDir()}, phase, io.Discard)
	if err != nil || res3.Reused || res3.ImageID == img.ID {
		t.Fatalf("build after a context change: %+v, %v", res3, err)
	}
	// The second commit's context is identical to the third's (only a file
	// outside it changed in between): that image is reused under a new tag.
	var second string
	if repo, err := git.PlainOpen(repoDir); err == nil {
		head, _ := repo.Head()
		c, _ := repo.CommitObject(head.Hash())
		p, _ := c.Parent(0)
		second = p.Hash.String()
	}
	cmd2 := &agentv1.BuildImageCommand{
		BuildId: "b2", ImageTag: tagSecond, RepoUrl: repoDir, GitRef: "master", GitCommit: second,
		ContextPath: "app", BuildArgs: map[string]string{"GREETING": "hello"}, SettingsKey: "settings-v1",
	}
	var out2 bytes.Buffer
	res2, err := Run(ctx, cli, cmd2, Options{WorkDir: t.TempDir()}, phase, &out2)
	if err != nil || !res2.Reused || res2.ImageID != res3.ImageID {
		t.Fatalf("unchanged context: %+v, %v\n%s", res2, err, out2.String())
	}
	if _, err := cli.ImageInspect(ctx, tagSecond); err != nil {
		t.Fatalf("reused image not tagged: %v", err)
	}

	failing := &agentv1.BuildImageCommand{ImageTag: tag + "-missing", RepoUrl: repoDir, GitRef: "master", GitCommit: first, ContextPath: "nope"}
	if _, err := Run(ctx, cli, failing, Options{WorkDir: t.TempDir()}, phase, io.Discard); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing context: %v", err)
	}
}
