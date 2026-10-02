package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestKubernetesClusterRoundTrip(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	name := "store-test-" + t.Name()
	_, _ = st.pool.Exec(ctx, `DELETE FROM kubernetes_clusters WHERE name=$1`, name)

	created, err := st.CreateKubernetesCluster(ctx, name, "https://k8s.example:6443", []byte("sealed"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _, _ = st.DeleteKubernetesCluster(context.Background(), created.ID) })
	if created.ID == "" || created.Name != name || created.APIServer != "https://k8s.example:6443" || created.CreatedAt.IsZero() {
		t.Fatalf("created = %+v", created)
	}

	var pgErr *pgconn.PgError
	if _, err := st.CreateKubernetesCluster(ctx, name, "https://other:6443", []byte("x")); !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate name error = %v, want unique violation", err)
	}

	clusters, err := st.ListKubernetesClusters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range clusters {
		found = found || c.ID == created.ID
	}
	if !found {
		t.Fatal("created cluster missing from list")
	}

	got, sealed, err := st.GetKubernetesClusterConfig(ctx, created.ID)
	if err != nil || got.Name != name || string(sealed) != "sealed" {
		t.Fatalf("get = %+v %q %v", got, sealed, err)
	}

	deleted, err := st.DeleteKubernetesCluster(ctx, created.ID)
	if err != nil || !deleted {
		t.Fatalf("delete = %v %v", deleted, err)
	}
	if _, _, err := st.GetKubernetesClusterConfig(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
	if deleted, err := st.DeleteKubernetesCluster(ctx, created.ID); err != nil || deleted {
		t.Fatalf("second delete = %v %v", deleted, err)
	}
}

func TestKubernetesClusterMalformedIDIsNotFound(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, _, err := st.GetKubernetesClusterConfig(ctx, "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get malformed id = %v, want ErrNotFound", err)
	}
	if deleted, err := st.DeleteKubernetesCluster(ctx, "not-a-uuid"); err != nil || deleted {
		t.Fatalf("delete malformed id = %v %v, want false, nil", deleted, err)
	}
}
