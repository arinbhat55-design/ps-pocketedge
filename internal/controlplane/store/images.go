package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/jackc/pgx/v5"
)

// Registry is one admin-configured private registry's credentials, used
// both to authenticate PullImageCommand and to authenticate registry
// search/tag-listing/digest lookups from internal/controlplane/registryclient.
// Password is stored in plaintext (see the migration's doc comment) —
// consciously matching this codebase's existing security posture rather
// than an oversight.
type Registry struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Username  string    `json:"username"`
	Password  string    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) ListRegistries(ctx context.Context) ([]Registry, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, url, username, password, created_at FROM registries ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	registries := []Registry{}
	for rows.Next() {
		var r Registry
		if err := rows.Scan(&r.ID, &r.Name, &r.URL, &r.Username, &r.Password, &r.CreatedAt); err != nil {
			return nil, err
		}
		registries = append(registries, r)
	}
	return registries, rows.Err()
}

func (s *Store) GetRegistry(ctx context.Context, id string) (*Registry, error) {
	var r Registry
	err := s.pool.QueryRow(ctx, `SELECT id, name, url, username, password, created_at FROM registries WHERE id = $1`, id).
		Scan(&r.ID, &r.Name, &r.URL, &r.Username, &r.Password, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) CreateRegistry(ctx context.Context, name, url, username, password string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO registries (name, url, username, password) VALUES ($1, $2, $3, $4) RETURNING id
	`, name, url, username, password).Scan(&id)
	return id, err
}

func (s *Store) DeleteRegistry(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM registries WHERE id = $1`, id)
	return err
}

// ApprovedImage is one allowed pattern in the deploy-time allowlist (see
// IsImageApproved).
type ApprovedImage struct {
	ID        string    `json:"id"`
	Pattern   string    `json:"pattern"`
	Note      string    `json:"note"`
	CreatedBy *string   `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) ListApprovedImages(ctx context.Context) ([]ApprovedImage, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, pattern, note, created_by, created_at FROM approved_images ORDER BY pattern ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	patterns := []ApprovedImage{}
	for rows.Next() {
		var a ApprovedImage
		if err := rows.Scan(&a.ID, &a.Pattern, &a.Note, &a.CreatedBy, &a.CreatedAt); err != nil {
			return nil, err
		}
		patterns = append(patterns, a)
	}
	return patterns, rows.Err()
}

func (s *Store) CreateApprovedImage(ctx context.Context, pattern, note, createdBy string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO approved_images (pattern, note, created_by) VALUES ($1, $2, $3) RETURNING id
	`, pattern, note, createdBy).Scan(&id)
	return id, err
}

func (s *Store) DeleteApprovedImage(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM approved_images WHERE id = $1`, id)
	return err
}

// GetImagePolicyEnabled reports whether the approved-image allowlist is
// currently enforced.
func (s *Store) GetImagePolicyEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx, `SELECT enabled FROM image_policy_settings WHERE id = 1`).Scan(&enabled)
	return enabled, err
}

func (s *Store) SetImagePolicyEnabled(ctx context.Context, enabled bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE image_policy_settings SET enabled = $1 WHERE id = 1`, enabled)
	return err
}

// IsImageApproved reports whether ref matches one of the configured
// approved_images patterns (see matchesImagePattern for the syntax).
// Callers should only enforce this when GetImagePolicyEnabled is true.
func (s *Store) IsImageApproved(ctx context.Context, ref string) (bool, error) {
	patterns, err := s.ListApprovedImages(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range patterns {
		if matchesImagePattern(ref, p.Pattern) {
			return true, nil
		}
	}
	return false, nil
}

// matchesImagePattern reports whether image ref matches an approved-image
// pattern. Both sides are normalized the way Docker does, so "nginx",
// "library/nginx" and "docker.io/library/nginx" are the same repository.
// Patterns:
//
//   - a repository ("nginx", "ghcr.io/acme/app") matches it at any tag or
//     digest — but not a different repository sharing the prefix, such as
//     "nginx-evil/miner";
//   - a repository with a tag ("nginx:1.27") matches only that tag;
//   - "*" matches any run of characters ("nginx:1.*", "ghcr.io/acme/*");
//   - a trailing "/" ("myregistry.com/team/") is shorthand for
//     "myregistry.com/team/*".
//
// Factored out of IsImageApproved so the matching rule itself is
// unit-testable without a database.
func matchesImagePattern(ref, pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		pattern += "*"
	}
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		// Not a valid image reference; only an identical pattern matches.
		return ref == pattern
	}
	repo := named.Name()

	if strings.Contains(pattern, "*") {
		full := repo
		if tagged, ok := named.(reference.Tagged); ok {
			full += ":" + tagged.Tag()
		} else if _, ok := named.(reference.Digested); !ok {
			full += ":latest"
		}
		if digested, ok := named.(reference.Digested); ok {
			full += "@" + digested.Digest().String()
		}
		return globMatch(normalizeImagePatternName(pattern), full)
	}

	p, err := reference.ParseNormalizedNamed(pattern)
	if err != nil || p.Name() != repo {
		return false
	}
	if pt, ok := p.(reference.Tagged); ok {
		rt, ok := named.(reference.Tagged)
		if !ok || rt.Tag() != pt.Tag() {
			return false
		}
	}
	if pd, ok := p.(reference.Digested); ok {
		rd, ok := named.(reference.Digested)
		if !ok || rd.Digest() != pd.Digest() {
			return false
		}
	}
	return true
}

// ValidateImagePattern reports why pattern can't be used as an approved-
// image pattern, or nil when it can (see matchesImagePattern).
func ValidateImagePattern(pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return errors.New("pattern is required")
	}
	if strings.HasSuffix(pattern, "/") || strings.Contains(pattern, "*") {
		probe := strings.ReplaceAll(strings.TrimSuffix(pattern, "/"), "*", "x")
		if strings.HasSuffix(pattern, "/") {
			probe += "/x"
		}
		if _, err := reference.ParseNormalizedNamed(probe); err != nil {
			return fmt.Errorf("%q is not a valid image pattern: %v", pattern, err)
		}
		return nil
	}
	if _, err := reference.ParseNormalizedNamed(pattern); err != nil {
		return fmt.Errorf("%q is not a valid image reference: %v", pattern, err)
	}
	return nil
}

// normalizeImagePatternName applies Docker's name normalization to a glob
// pattern, which reference.ParseNormalizedNamed rejects because of its
// "*": a first component without a "." or ":" (and not "localhost") is a
// Docker Hub name, and a single-component Hub name is under "library/".
func normalizeImagePatternName(pattern string) string {
	if pattern == "*" {
		return pattern
	}
	first, rest, hasSlash := strings.Cut(pattern, "/")
	if hasSlash && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return pattern
	}
	if !hasSlash {
		if first == "*" || strings.HasPrefix(first, "*") {
			return "docker.io/" + pattern
		}
		return "docker.io/library/" + pattern
	}
	return "docker.io/" + first + "/" + rest
}

// globMatch matches s against pattern, where "*" matches any run of
// characters (including "/" and ":") and everything else is literal.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// RecordContainerRecreate updates image_rollback_history after fromID was
// recreated as toID: Docker gives a recreated container a new ID, so the
// history moves with it. consumedID, when set, is the entry a rollback just
// used and is dropped. previousImage, when set, becomes the newest entry.
// The history is then trimmed to the most recent keepLast entries — it
// exists purely to support "rollback to previous image", not a full audit
// trail.
func (s *Store) RecordContainerRecreate(ctx context.Context, serverID, fromID, toID, consumedID, previousImage string, keepLast int) error {
	if toID == "" {
		toID = fromID
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if consumedID != "" {
		if _, err := tx.Exec(ctx, `DELETE FROM image_rollback_history WHERE id = $1`, consumedID); err != nil {
			return err
		}
	}
	if fromID != toID {
		if _, err := tx.Exec(ctx, `
			UPDATE image_rollback_history SET container_id = $3
			WHERE server_id = $1 AND container_id = $2
		`, serverID, fromID, toID); err != nil {
			return err
		}
	}
	if previousImage != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO image_rollback_history (server_id, container_id, previous_image) VALUES ($1, $2, $3)
		`, serverID, toID, previousImage); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM image_rollback_history
		WHERE server_id = $1 AND container_id = $2
		  AND id NOT IN (
		    SELECT id FROM image_rollback_history
		    WHERE server_id = $1 AND container_id = $2
		    ORDER BY captured_at DESC LIMIT $3
		  )
	`, serverID, toID, keepLast); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ImageRollbackEntry is one recorded previous image for a container.
type ImageRollbackEntry struct {
	ID            string    `json:"id"`
	PreviousImage string    `json:"previousImage"`
	CapturedAt    time.Time `json:"capturedAt"`
}

func (s *Store) ListImageRollbackHistory(ctx context.Context, serverID, containerID string) ([]ImageRollbackEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, previous_image, captured_at FROM image_rollback_history
		WHERE server_id = $1 AND container_id = $2
		ORDER BY captured_at DESC
	`, serverID, containerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := []ImageRollbackEntry{}
	for rows.Next() {
		var e ImageRollbackEntry
		if err := rows.Scan(&e.ID, &e.PreviousImage, &e.CapturedAt); err != nil {
			return nil, err
		}
		history = append(history, e)
	}
	return history, rows.Err()
}

// LatestImageRollbackHistory returns the most recently captured previous
// image for containerID without removing it — a rollback only consumes it
// (via RecordContainerRecreate) once the recreate succeeded. Returns
// ErrNotFound if there's no history.
func (s *Store) LatestImageRollbackHistory(ctx context.Context, serverID, containerID string) (*ImageRollbackEntry, error) {
	var e ImageRollbackEntry
	err := s.pool.QueryRow(ctx, `
		SELECT id, previous_image, captured_at FROM image_rollback_history
		WHERE server_id = $1 AND container_id = $2
		ORDER BY captured_at DESC LIMIT 1
	`, serverID, containerID).Scan(&e.ID, &e.PreviousImage, &e.CapturedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ImageScan is a stored vulnerability-scan result (or failure) for one
// image reference. RawResult holds the summarized vulnerability list
// (capped) from internal/controlplane/scan, not Trivy's full raw output.
type ImageScan struct {
	ID            string          `json:"id"`
	ImageRef      string          `json:"imageRef"`
	ScannedAt     time.Time       `json:"scannedAt"`
	CriticalCount int             `json:"criticalCount"`
	HighCount     int             `json:"highCount"`
	MediumCount   int             `json:"mediumCount"`
	LowCount      int             `json:"lowCount"`
	UnknownCount  int             `json:"unknownCount"`
	RawResult     json.RawMessage `json:"rawResult,omitempty"`
	Error         string          `json:"error,omitempty"`
}

// UpsertImageScan records a scan result, replacing any previous scan of
// the same image_ref (see the migration's unique index) — history isn't
// kept, only the latest result.
func (s *Store) UpsertImageScan(ctx context.Context, scan ImageScan) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO image_scans (image_ref, scanned_at, critical_count, high_count, medium_count, low_count, unknown_count, raw_result, error)
		VALUES ($1, now(), $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (image_ref) DO UPDATE SET
		  scanned_at = now(), critical_count = $2, high_count = $3, medium_count = $4,
		  low_count = $5, unknown_count = $6, raw_result = $7, error = $8
	`, scan.ImageRef, scan.CriticalCount, scan.HighCount, scan.MediumCount, scan.LowCount, scan.UnknownCount, scan.RawResult, scan.Error)
	return err
}

func (s *Store) GetImageScan(ctx context.Context, imageRef string) (*ImageScan, error) {
	var sc ImageScan
	err := s.pool.QueryRow(ctx, `
		SELECT id, image_ref, scanned_at, critical_count, high_count, medium_count, low_count, unknown_count, raw_result, error
		FROM image_scans WHERE image_ref = $1
	`, imageRef).Scan(&sc.ID, &sc.ImageRef, &sc.ScannedAt, &sc.CriticalCount, &sc.HighCount, &sc.MediumCount, &sc.LowCount, &sc.UnknownCount, &sc.RawResult, &sc.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sc, nil
}
