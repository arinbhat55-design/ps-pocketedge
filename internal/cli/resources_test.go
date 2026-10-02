package cli

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceContracts(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, input, response string
		args                                []string
		body                                map[string]any
		query                               map[string]string
		status                              int
	}{
		{name: "database list", method: "GET", path: "/api/databases", response: `[{"id":"d1","phase":"running"}]`, args: []string{"databases", "list", "--json"}},
		{name: "database create", method: "POST", path: "/api/databases", args: []string{"databases", "create", "--engine", "postgresql", "--name", "appdb", "--server", "s1"}, body: map[string]any{"engine": "postgresql", "name": "appdb", "serverId": "s1"}},
		{name: "database advanced preview", method: "POST", path: "/api/databases/preview", input: `{"engine":"postgresql","name":"old","serverId":"s1","memoryMb":512,"backup":{"consistent":false}}`, args: []string{"databases", "preview", "--file", "-", "--name", "new"}, body: map[string]any{"engine": "postgresql", "name": "new", "serverId": "s1", "memoryMb": float64(512), "backup": map[string]any{"consistent": false}}},
		{name: "database configure", method: "PATCH", path: "/api/databases/d1/configuration", input: `{"memoryMb":1024}`, args: []string{"databases", "configure", "d1", "--file", "-"}, body: map[string]any{"memoryMb": float64(1024)}},
		{name: "database remove", method: "DELETE", path: "/api/databases/d1", args: []string{"databases", "remove", "d1"}, status: 204},
		{name: "database physical backup", method: "POST", path: "/api/databases/d1/backups", args: []string{"databases", "backup", "d1"}, body: map[string]any{}},
		{name: "database logical backup", method: "POST", path: "/api/databases/d1/logical-backups", args: []string{"databases", "logical-backup", "d1"}},
		{name: "database migrate", method: "POST", path: "/api/databases/d1/migrate-from-backup", args: []string{"databases", "migrate", "d1", "--backup", "b1"}, body: map[string]any{"backupId": "b1"}},
		{name: "database credentials", method: "GET", path: "/api/databases/d1/credentials", args: []string{"databases", "credentials", "d1"}},
		{name: "postgres query", method: "POST", path: "/api/databases/d1/postgres/query", input: `{"sql":"select 1"}`, args: []string{"databases", "postgres", "query", "d1", "--file", "-"}, body: map[string]any{"sql": "select 1"}},
		{name: "secret rotate", method: "POST", path: "/api/secrets/sec1/rotate", args: []string{"databases", "secrets", "rotate", "sec1"}},
		{name: "backup policy", method: "PUT", path: "/api/databases/d1/backup-policy", input: `{"cron":"0 2 * * *","retentionDays":7}`, args: []string{"databases", "backup-policy", "d1", "--file", "-"}, body: map[string]any{"cron": "0 2 * * *", "retentionDays": float64(7)}},
		{name: "backup list", method: "GET", path: "/api/deployments/dep1/backups", response: `[]`, args: []string{"backups", "list", "--deployment", "dep1", "--json"}},
		{name: "backup create default", method: "POST", path: "/api/deployments/dep1/backups", args: []string{"backups", "create", "--deployment", "dep1"}, body: map[string]any{}},
		{name: "backup create explicit false", method: "POST", path: "/api/deployments/dep1/backups", args: []string{"backups", "create", "--deployment", "dep1", "--consistent=false"}, body: map[string]any{"consistent": false}},
		{name: "backup restore", method: "POST", path: "/api/backups/b1/restore", args: []string{"backups", "restore", "b1"}},
		{name: "images list", method: "GET", path: "/api/images", response: `[]`, args: []string{"images", "list", "--server", "s1"}, query: map[string]string{"serverId": "s1"}},
		{name: "image pull", method: "POST", path: "/api/servers/s1/images/pull", args: []string{"images", "pull", "nginx:alpine", "--server", "s1", "--registry", "r1"}, body: map[string]any{"imageRef": "nginx:alpine", "registryId": "r1"}},
		{name: "image inspect", method: "GET", path: "/api/servers/s1/images/sha256:abc/inspect", args: []string{"images", "inspect", "sha256:abc", "--server", "s1"}},
		{name: "image remove", method: "DELETE", path: "/api/servers/s1/images/sha256:abc", args: []string{"images", "remove", "sha256:abc", "--server", "s1", "--force"}, query: map[string]string{"force": "true"}},
		{name: "image prune", method: "POST", path: "/api/servers/s1/images/prune", args: []string{"images", "prune", "--server", "s1", "--all"}, body: map[string]any{"all": true}},
		{name: "cluster list", method: "GET", path: "/api/kubernetes/clusters", response: `[]`, args: []string{"kubernetes", "clusters", "list"}},
		{name: "cluster register", method: "POST", path: "/api/kubernetes/clusters", input: "apiVersion: v1\n", args: []string{"kubernetes", "clusters", "add", "--name", "dev", "--kubeconfig", "-"}, body: map[string]any{"name": "dev", "kubeconfig": "apiVersion: v1\n"}},
		{name: "cluster local", method: "POST", path: "/api/kubernetes/local-clusters", args: []string{"kubernetes", "clusters", "create-local", "--name", "dev"}, body: map[string]any{"name": "dev"}},
		{name: "cluster disconnect", method: "DELETE", path: "/api/kubernetes/clusters/k1", args: []string{"kubernetes", "clusters", "remove", "k1"}, status: 204},
		{name: "cluster overview all", method: "GET", path: "/api/kubernetes/clusters/k1/overview", args: []string{"kubernetes", "overview", "k1", "--namespace", "all"}, query: map[string]string{"namespace": ""}},
		{name: "workload create", method: "POST", path: "/api/kubernetes/clusters/k1/workloads", input: `{"namespace":"default","name":"web","image":"nginx","replicas":1}`, args: []string{"kubernetes", "workloads", "create", "--cluster", "k1", "--file", "-", "--dry-run"}, query: map[string]string{"dryRun": "true"}, body: map[string]any{"namespace": "default", "name": "web", "image": "nginx", "replicas": float64(1)}},
		{name: "workload rollback", method: "POST", path: "/api/kubernetes/clusters/k1/workloads/default/web/rollback", args: []string{"kubernetes", "workloads", "rollback", "web", "--cluster", "k1", "--revision", "2"}, body: map[string]any{"revision": float64(2)}},
		{name: "helm list", method: "GET", path: "/api/kubernetes/clusters/k1/helm", args: []string{"kubernetes", "helm", "list", "--cluster", "k1"}, query: map[string]string{"namespace": "default"}},
		{name: "helm rollback", method: "POST", path: "/api/kubernetes/clusters/k1/helm/default/web/rollback", args: []string{"kubernetes", "helm", "rollback", "web", "--cluster", "k1", "--revision", "2"}, body: map[string]any{"revision": float64(2)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("request: %s %s, want %s %s", r.Method, r.URL.Path, tc.method, tc.path)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing authentication")
				}
				for k, v := range tc.query {
					if r.URL.Query().Get(k) != v {
						t.Errorf("query %s=%s want %s", k, r.URL.Query().Get(k), v)
					}
				}
				if tc.body != nil {
					var b map[string]any
					if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
						t.Error(err)
					}
					got, _ := json.Marshal(b)
					want, _ := json.Marshal(tc.body)
					if string(got) != string(want) {
						t.Errorf("body: %s want %s", got, want)
					}
				}
				if tc.status == 204 {
					w.WriteHeader(204)
					return
				}
				response := tc.response
				if response == "" {
					response = `{"success":true}`
				}
				io.WriteString(w, response)
			})
			if _, err := execute(t, p, tc.input, tc.args...); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestResourceFailuresAndInput(t *testing.T) {
	for _, tc := range []struct {
		response string
		status   int
		want     string
	}{{`{"success":false,"error":"image in use"}`, 200, "image in use"}, {"forbidden", 403, "403"}} {
		p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.response) })
		_, err := execute(t, p, "", "images", "remove", "sha256:abc", "--server", "s1")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("failure: %v", err)
		}
	}
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid command reached API") })
	for _, args := range [][]string{{"backups", "create"}, {"images", "pull", "nginx"}, {"databases", "create"}, {"kubernetes", "workloads", "rollback", "web", "--cluster", "k1"}, {"databases", "configure", "d1", "--file", "-"}, {"backups", "restore", "../bad"}} {
		if _, err := execute(t, p, `[]`, args...); err == nil {
			t.Errorf("invalid input accepted: %v", args)
		}
	}
}

func TestHelmArchive(t *testing.T) {
	chart := filepath.Join(t.TempDir(), "chart.tgz")
	os.WriteFile(chart, []byte("archive"), 0600)
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path != "/api/kubernetes/clusters/k1/helm" || b["chartBase64"] != base64.StdEncoding.EncodeToString([]byte("archive")) || b["name"] != "web" {
			t.Errorf("chart request: %v", b)
		}
		io.WriteString(w, `{"status":"applied"}`)
	})
	if _, err := execute(t, p, "", "kubernetes", "helm", "install", "web", "--cluster", "k1", "--chart", chart); err != nil {
		t.Fatal(err)
	}
}
