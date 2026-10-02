package build

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// errContextTooLarge means the build context exceeded the size limit.
var errContextTooLarge = errors.New("build context is too large")

// writeContextTar streams contextDir to w as the tar archive the Docker
// daemon's build endpoint takes, leaving out whatever
// contextDir/.dockerignore excludes — the same rules the docker CLI
// applies. The Dockerfile and .dockerignore are always sent, as the CLI
// does, even when .dockerignore matches them.
//
// Symlinks are archived as links, never followed, so a link in the
// repository pointing elsewhere on the host can't pull host files into the
// build. Ownership is reset to root and every modification time is set to
// modTime (the commit's time), so the same commit always produces the same
// archive.
func writeContextTar(w io.Writer, contextDir, dockerfile string, modTime time.Time, maxBytes int64) error {
	excludes, err := readDockerignore(contextDir)
	if err != nil {
		return err
	}
	var pm *patternmatcher.PatternMatcher
	if len(excludes) > 0 {
		if pm, err = patternmatcher.New(excludes); err != nil {
			return fmt.Errorf("invalid .dockerignore: %w", err)
		}
	}
	always := []string{".dockerignore", path.Clean(filepath.ToSlash(dockerfile))}

	tw := tar.NewWriter(w)
	var total int64
	err = filepath.WalkDir(contextDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(contextDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if pm != nil && !containsString(always, rel) {
			skip, err := pm.MatchesOrParentMatches(rel)
			if err != nil {
				return err
			}
			if skip {
				// A skipped directory can still hold files that an
				// exception ("!pattern") or the Dockerfile brings back,
				// so only prune the walk when neither can apply.
				if d.IsDir() && !pm.Exclusions() && !isParentOfAny(rel, always) {
					return filepath.SkipDir
				}
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		case info.IsDir(), info.Mode().IsRegular():
		default:
			// Sockets, devices and pipes have no place in a build context.
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		hdr.ModTime, hdr.AccessTime, hdr.ChangeTime = modTime, time.Time{}, time.Time{}
		hdr.Format = tar.FormatPAX

		if info.Mode().IsRegular() {
			total += info.Size()
			if maxBytes > 0 && total > maxBytes {
				return fmt.Errorf("%w (more than %d MB) — exclude what the build doesn't need with a .dockerignore", errContextTooLarge, maxBytes>>20)
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

func readDockerignore(contextDir string) ([]string, error) {
	f, err := os.Open(filepath.Join(contextDir, ".dockerignore"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	patterns, err := ignorefile.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read .dockerignore: %w", err)
	}
	return patterns, nil
}

// resolveInside joins rel onto root and checks that the result, with every
// symlink resolved, is still inside root — a repository can contain a
// symlink pointing anywhere on the host.
func resolveInside(root, rel string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(root, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%q not found in the repository", rel)
		}
		return "", err
	}
	if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("%q points outside the repository", rel)
	}
	return real, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func isParentOfAny(dir string, paths []string) bool {
	for _, p := range paths {
		if strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}
