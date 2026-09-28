package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// invalidClusterID reports a malformed UUID path parameter, which callers
// treat the same as an unknown cluster rather than a database failure.
func invalidClusterID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

type KubernetesCluster struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	APIServer string    `json:"apiServer"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Store) CreateKubernetesCluster(ctx context.Context, name, apiServer string, ciphertext []byte) (*KubernetesCluster, error) {
	var c KubernetesCluster
	err := s.pool.QueryRow(ctx, `INSERT INTO kubernetes_clusters (name, api_server, kubeconfig_ciphertext) VALUES ($1,$2,$3) RETURNING id,name,api_server,created_at`, name, apiServer, ciphertext).
		Scan(&c.ID, &c.Name, &c.APIServer, &c.CreatedAt)
	return &c, err
}

func (s *Store) ListKubernetesClusters(ctx context.Context) ([]KubernetesCluster, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,name,api_server,created_at FROM kubernetes_clusters ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	clusters := []KubernetesCluster{}
	for rows.Next() {
		var c KubernetesCluster
		if err := rows.Scan(&c.ID, &c.Name, &c.APIServer, &c.CreatedAt); err != nil {
			return nil, err
		}
		clusters = append(clusters, c)
	}
	return clusters, rows.Err()
}

func (s *Store) GetKubernetesClusterConfig(ctx context.Context, id string) (*KubernetesCluster, []byte, error) {
	var c KubernetesCluster
	var ciphertext []byte
	err := s.pool.QueryRow(ctx, `SELECT id,name,api_server,created_at,kubeconfig_ciphertext FROM kubernetes_clusters WHERE id=$1`, id).
		Scan(&c.ID, &c.Name, &c.APIServer, &c.CreatedAt, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) || invalidClusterID(err) {
		return nil, nil, ErrNotFound
	}
	return &c, ciphertext, err
}

func (s *Store) DeleteKubernetesCluster(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM kubernetes_clusters WHERE id=$1`, id)
	if invalidClusterID(err) {
		return false, nil
	}
	return tag.RowsAffected() > 0, err
}
