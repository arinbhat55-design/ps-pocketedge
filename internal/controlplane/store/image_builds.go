package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// maxBuildLogBytes caps the build output kept per build; older output is
// dropped first.
const maxBuildLogBytes = 1 << 20

// Build statuses. queued/cloning/building are active; the rest are final.
const (
	BuildQueued     = "queued"
	BuildCloning    = "cloning"
	BuildBuilding   = "building"
	BuildSucceeded  = "succeeded"
	BuildFailed     = "failed"
	BuildCancelled  = "cancelled"
	BuildSuperseded = "superseded"
)

// IsFinalBuildStatus reports whether a build with this status is over.
func IsFinalBuildStatus(status string) bool {
	switch status {
	case BuildSucceeded, BuildFailed, BuildCancelled, BuildSuperseded:
		return true
	}
	return false
}

// ImageBuild is one image built (or found already built) on one server
// for one service of a deployment's revision.
type ImageBuild struct {
	ID              string            `json:"id"`
	DeploymentID    string            `json:"deploymentId"`
	Revision        int               `json:"revision"`
	ServerID        string            `json:"serverId"`
	Service         string            `json:"service"`
	ImageTag        string            `json:"imageTag"`
	GitRepositoryID *string           `json:"gitRepositoryId,omitempty"`
	GitRef          string            `json:"gitRef"`
	GitCommit       string            `json:"gitCommit"`
	ContextPath     string            `json:"contextPath"`
	Dockerfile      string            `json:"dockerfile"`
	Target          string            `json:"target,omitempty"`
	BuildArgs       map[string]string `json:"-"`
	BuildArgNames   []string          `json:"buildArgNames"`
	Labels          map[string]string `json:"labels,omitempty"`
	NoCache         bool              `json:"noCache"`
	Status          string            `json:"status"`
	StatusMessage   string            `json:"statusMessage,omitempty"`
	Reused          bool              `json:"reused"`
	ImageID         string            `json:"imageId,omitempty"`
	// Log is only loaded by GetImageBuild.
	Log            string     `json:"log,omitempty"`
	LogSeq         int64      `json:"logSeq"`
	CreatedBy      *string    `json:"createdBy,omitempty"`
	CreatedByEmail *string    `json:"createdByEmail,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
}

// NewImageBuild is what InsertImageBuild stores.
type NewImageBuild struct {
	DeploymentID    string
	Revision        int
	ServerID        string
	Service         string
	ImageTag        string
	GitRepositoryID string
	GitRef          string
	GitCommit       string
	ContextPath     string
	Dockerfile      string
	Target          string
	BuildArgs       map[string]string
	Labels          map[string]string
	NoCache         bool
	CreatedBy       string
}

func (s *Store) InsertImageBuild(ctx context.Context, n NewImageBuild) (string, error) {
	if n.BuildArgs == nil {
		n.BuildArgs = map[string]string{}
	}
	if n.Labels == nil {
		n.Labels = map[string]string{}
	}
	args, err := json.Marshal(n.BuildArgs)
	if err != nil {
		return "", err
	}
	labels, err := json.Marshal(n.Labels)
	if err != nil {
		return "", err
	}
	var repoID, createdBy any
	if n.GitRepositoryID != "" {
		repoID = n.GitRepositoryID
	}
	if n.CreatedBy != "" {
		createdBy = n.CreatedBy
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO image_builds (deployment_id, revision, server_id, service, image_tag, git_repository_id, git_ref, git_commit,
			context_path, dockerfile, target, build_args, labels, no_cache, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id
	`, n.DeploymentID, n.Revision, n.ServerID, n.Service, n.ImageTag, repoID, n.GitRef, n.GitCommit,
		n.ContextPath, n.Dockerfile, n.Target, args, labels, n.NoCache, createdBy).Scan(&id)
	return id, err
}

// BuildUpdate is one status report for a build: its new status, and any
// build output that came with it.
type BuildUpdate struct {
	Status  string
	Message string
	Log     string
	LogSeq  int64
	ImageID string
	Reused  bool
}

// UpdateImageBuild applies a status report. A build already final (for
// example superseded by a newer rollout) keeps its status, but still
// records any output that arrives after. Log batches are applied once, in
// order: a batch at or below the stored log_seq is a duplicate.
func (s *Store) UpdateImageBuild(ctx context.Context, id string, u BuildUpdate) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE image_builds SET
			status = CASE WHEN status IN ('succeeded', 'failed', 'cancelled', 'superseded') OR $2 = '' THEN status ELSE $2 END,
			status_message = CASE WHEN status IN ('succeeded', 'failed', 'cancelled', 'superseded') OR $3 = '' THEN status_message ELSE $3 END,
			log = CASE WHEN $4 <> '' AND $5 > log_seq THEN right(log || $4, $8) ELSE log END,
			log_seq = GREATEST(log_seq, $5),
			image_id = CASE WHEN $6 <> '' THEN $6 ELSE image_id END,
			reused = reused OR $7,
			started_at = CASE WHEN started_at IS NULL AND $2 IN ('cloning', 'building') THEN now() ELSE started_at END,
			finished_at = CASE WHEN finished_at IS NULL AND $2 IN ('succeeded', 'failed', 'cancelled', 'superseded') THEN now() ELSE finished_at END
		WHERE id = $1
	`, id, u.Status, u.Message, u.Log, u.LogSeq, u.ImageID, u.Reused, maxBuildLogBytes)
	return err
}

// FinishImageBuild sets a build's final status from the control plane's
// side (a timeout, a cancel, the server disconnecting), unless it's
// already final. Reports whether it changed anything.
func (s *Store) FinishImageBuild(ctx context.Context, id, status, message string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE image_builds SET status = $2, status_message = $3, finished_at = now()
		WHERE id = $1 AND status NOT IN ('succeeded', 'failed', 'cancelled', 'superseded')
	`, id, status, message)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// BuildState is a build's status without its settings or output.
type BuildState struct {
	Status  string
	Message string
	Reused  bool
}

// GetImageBuildState returns just a build's status — cheap enough to poll
// while waiting on it.
func (s *Store) GetImageBuildState(ctx context.Context, id string) (BuildState, error) {
	var b BuildState
	err := s.pool.QueryRow(ctx, `SELECT status, status_message, reused FROM image_builds WHERE id = $1`, id).Scan(&b.Status, &b.Message, &b.Reused)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// ActiveBuild is a build still in progress.
type ActiveBuild struct {
	ID       string
	ServerID string
}

// SupersedeActiveBuilds marks a deployment's in-progress builds from
// revisions before `before` as superseded and returns them, so the caller
// can stop them on their agents: a newer rollout replaces them.
func (s *Store) SupersedeActiveBuilds(ctx context.Context, deploymentID string, before int, message string) ([]ActiveBuild, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE image_builds SET status = 'superseded', status_message = $3, finished_at = now()
		WHERE deployment_id = $1 AND revision < $2 AND status IN ('queued', 'cloning', 'building')
		RETURNING id, server_id
	`, deploymentID, before, message)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActiveBuild
	for rows.Next() {
		var b ActiveBuild
		if err := rows.Scan(&b.ID, &b.ServerID); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

const imageBuildColumns = `b.id, b.deployment_id, b.revision, b.server_id, b.service, b.image_tag, b.git_repository_id, b.git_ref, b.git_commit,
	b.context_path, b.dockerfile, b.target, b.build_args, b.labels, b.no_cache, b.status, b.status_message, b.reused, b.image_id,
	b.log_seq, b.created_by, u.email, b.created_at, b.started_at, b.finished_at`

func scanImageBuild(row pgx.Row, withLog bool) (*ImageBuild, error) {
	var b ImageBuild
	var args, labels []byte
	dest := []any{&b.ID, &b.DeploymentID, &b.Revision, &b.ServerID, &b.Service, &b.ImageTag, &b.GitRepositoryID, &b.GitRef, &b.GitCommit,
		&b.ContextPath, &b.Dockerfile, &b.Target, &args, &labels, &b.NoCache, &b.Status, &b.StatusMessage, &b.Reused, &b.ImageID,
		&b.LogSeq, &b.CreatedBy, &b.CreatedByEmail, &b.CreatedAt, &b.StartedAt, &b.FinishedAt}
	if withLog {
		dest = append(dest, &b.Log)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(args, &b.BuildArgs); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(labels, &b.Labels); err != nil {
		return nil, err
	}
	b.BuildArgNames = sortedMapKeys(b.BuildArgs)
	return &b, nil
}

// GetImageBuild returns one build, including its output. Returns
// ErrNotFound if it doesn't exist.
func (s *Store) GetImageBuild(ctx context.Context, id string) (*ImageBuild, error) {
	b, err := scanImageBuild(s.pool.QueryRow(ctx, `SELECT `+imageBuildColumns+`, b.log FROM image_builds b
		LEFT JOIN users u ON u.id = b.created_by WHERE b.id = $1`, id), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

// ListDeploymentBuilds returns a deployment's most recent builds, newest
// first, without their output.
func (s *Store) ListDeploymentBuilds(ctx context.Context, deploymentID string, limit int) ([]ImageBuild, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+imageBuildColumns+` FROM image_builds b
		LEFT JOIN users u ON u.id = b.created_by
		WHERE b.deployment_id = $1 ORDER BY b.created_at DESC LIMIT $2`, deploymentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImageBuild{}
	for rows.Next() {
		b, err := scanImageBuild(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// LatestBuildForTag returns the most recent successful build that produced
// tag — its recorded settings are how to build the same image again.
// Returns ErrNotFound if there's none.
func (s *Store) LatestBuildForTag(ctx context.Context, tag string) (*ImageBuild, error) {
	b, err := scanImageBuild(s.pool.QueryRow(ctx, `SELECT `+imageBuildColumns+` FROM image_builds b
		LEFT JOIN users u ON u.id = b.created_by
		WHERE b.image_tag = $1 AND b.status = 'succeeded'
		ORDER BY b.created_at DESC LIMIT 1`, tag), false)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
