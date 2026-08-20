// Package backup stores backup blobs (tar snapshots of a deployment's
// volumes, uploaded by the agent) on local disk.
//
// This is deliberately local-disk-only for now, not S3 — the plan's
// "Future Phases" note calls out S3 as a later addition, and adding real
// object-storage integration (credentials, bucket config, multipart
// upload) is a meaningfully bigger feature than this pass. Flagged
// explicitly so it isn't mistaken for a finished requirement.
package backup

import (
	"io"
	"os"
	"path/filepath"
)

type BlobStore struct {
	dir string
}

func NewBlobStore(dir string) (*BlobStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &BlobStore{dir: dir}, nil
}

func (s *BlobStore) path(backupID string) string {
	return filepath.Join(s.dir, backupID+".tar")
}

// Save streams r to disk under backupID and returns the path written and
// the number of bytes copied.
func (s *BlobStore) Save(backupID string, r io.Reader) (path string, size int64, err error) {
	path = s.path(backupID)
	f, err := os.Create(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	n, err := io.Copy(f, r)
	if err != nil {
		return "", 0, err
	}
	return path, n, nil
}

// Open opens a previously saved blob for reading (e.g. to serve a
// restore's download).
func (s *BlobStore) Open(path string) (*os.File, error) {
	return os.Open(path)
}
