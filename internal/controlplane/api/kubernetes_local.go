package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

const localKindTimeout = 6 * time.Minute

func newLocalKindName() (string, error) {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return "pspe-" + hex.EncodeToString(suffix[:]), nil
}

func kindCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "kind", args...)
	cmd.Env = append(os.Environ(), "KIND_EXPERIMENTAL_PROVIDER=docker")
	return cmd.CombinedOutput()
}

// createLocalCluster provisions a kind cluster on the control-plane host.
// It only works when that host has kind, the Docker CLI, and a reachable
// local Docker Engine. The generated kubeconfig stays in a private temp
// directory until encrypted in the existing cluster store.
func (api *kubernetesAPI) createLocalCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		http.Error(w, "name must be 1–100 characters", http.StatusBadRequest)
		return
	}
	if _, err := exec.LookPath("kind"); err != nil {
		http.Error(w, "kind is not installed on the control-plane host", http.StatusConflict)
		return
	}
	if _, err := exec.LookPath("docker"); err != nil {
		http.Error(w, "Docker CLI is not installed on the control-plane host", http.StatusConflict)
		return
	}
	kindName, err := newLocalKindName()
	if err != nil {
		api.error(w, err)
		return
	}
	workDir, err := os.MkdirTemp("", "pspe-kind-")
	if err != nil {
		api.error(w, err)
		return
	}
	defer os.RemoveAll(workDir)
	kubeconfigPath := filepath.Join(workDir, "kubeconfig")
	ctx, cancel := context.WithTimeout(r.Context(), localKindTimeout)
	defer cancel()
	persisted := false
	defer func() {
		if persisted {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if output, err := kindCommand(cleanupCtx, "delete", "cluster", "--name", kindName); err != nil {
			api.log.Warn("could not clean up local kind cluster", "name", kindName, "error", err, "output", string(output))
		}
	}()
	output, err := kindCommand(ctx, "create", "cluster", "--name", kindName, "--kubeconfig", kubeconfigPath, "--wait", "4m")
	if err != nil {
		api.log.Warn("local kind cluster creation failed", "name", kindName, "error", err, "output", string(output))
		http.Error(w, "kind could not create the cluster; check the control-plane Docker Engine and logs", http.StatusBadGateway)
		return
	}
	raw, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		api.error(w, fmt.Errorf("read generated kubeconfig: %w", err))
		return
	}
	server, err := validKubeconfig(string(raw))
	if err != nil {
		api.error(w, fmt.Errorf("kind returned an unusable kubeconfig: %w", err))
		return
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(raw)
	if err != nil {
		api.error(w, err)
		return
	}
	config.Timeout = 15 * time.Second
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		api.error(w, err)
		return
	}
	if _, err := client.Discovery().ServerVersion(); err != nil {
		api.error(w, fmt.Errorf("local cluster API is not reachable from the control plane: %w", err))
		return
	}
	sealed, err := api.vault.Seal(string(raw))
	if err != nil {
		api.error(w, err)
		return
	}
	cluster, err := api.st.CreateLocalKubernetesCluster(ctx, req.Name, server, sealed, kindName)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "cluster name already exists", http.StatusConflict)
			return
		}
		api.error(w, err)
		return
	}
	persisted = true
	api.audit(r, "kubernetes.cluster.create_local", "kubernetes_cluster", cluster.ID, "created local kind cluster "+cluster.Name)
	writeJSON(w, http.StatusCreated, cluster)
}

func (api *kubernetesAPI) deleteLocalCluster(w http.ResponseWriter, r *http.Request) {
	cluster, _, err := api.st.GetKubernetesClusterConfig(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}
	if err != nil {
		api.error(w, err)
		return
	}
	if cluster.LocalKindName == "" {
		http.Error(w, "cluster is not managed locally", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	output, err := kindCommand(ctx, "delete", "cluster", "--name", cluster.LocalKindName)
	if err != nil {
		api.log.Warn("local kind cluster deletion failed", "name", cluster.LocalKindName, "error", err, "output", string(output))
		http.Error(w, "kind could not delete the local cluster", http.StatusBadGateway)
		return
	}
	if _, err := api.st.DeleteKubernetesCluster(r.Context(), cluster.ID); err != nil {
		api.error(w, err)
		return
	}
	api.audit(r, "kubernetes.cluster.delete_local", "kubernetes_cluster", cluster.ID, "deleted local kind cluster "+cluster.Name)
	w.WriteHeader(http.StatusNoContent)
}
