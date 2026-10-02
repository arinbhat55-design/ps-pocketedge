package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/vault"
)

// fakeKubeAPI serves just enough of the Kubernetes REST API over TLS for the
// cluster handlers: one ready node with a GPU, one managed deployment and its
// pod, one event, and a "default" namespace. Every request is recorded.
type fakeKubeAPI struct {
	*httptest.Server
	mu    sync.Mutex
	calls []string
}

func (f *fakeKubeAPI) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func newFakeKubeAPI(t *testing.T) *fakeKubeAPI {
	t.Helper()
	typeMeta := func(kind, apiVersion string) metav1.TypeMeta {
		return metav1.TypeMeta{Kind: kind, APIVersion: apiVersion}
	}
	labels := map[string]string{"app": "web", "app.kubernetes.io/managed-by": "pspocketedge"}
	replicas := int32(2)
	container := corev1.Container{Name: "app", Image: "nginx:1.27", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("256Mi"),
	}}}
	namespace := corev1.Namespace{TypeMeta: typeMeta("Namespace", "v1"), ObjectMeta: metav1.ObjectMeta{Name: "default"}}
	namespaces := corev1.NamespaceList{TypeMeta: typeMeta("NamespaceList", "v1"), Items: []corev1.Namespace{namespace}}
	nodes := corev1.NodeList{TypeMeta: typeMeta("NodeList", "v1"), Items: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}, Status: corev1.NodeStatus{
		Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi"), "nvidia.com/gpu": resource.MustParse("1")},
		Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
	}}}}
	deployments := appsv1.DeploymentList{TypeMeta: typeMeta("DeploymentList", "apps/v1"), Items: []appsv1.Deployment{{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", Labels: labels, Generation: 1},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: labels}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{Containers: []corev1.Container{container}}}},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, ReadyReplicas: 2, AvailableReplicas: 2},
	}}}
	pods := corev1.PodList{TypeMeta: typeMeta("PodList", "v1"), Items: []corev1.Pod{{
		ObjectMeta: metav1.ObjectMeta{Name: "web-abc", Namespace: "default", Labels: labels},
		Spec:       corev1.PodSpec{NodeName: "node-1", Containers: []corev1.Container{container}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}}}
	events := corev1.EventList{TypeMeta: typeMeta("EventList", "v1"), Items: []corev1.Event{{
		ObjectMeta: metav1.ObjectMeta{Name: "web-abc.1", Namespace: "default"}, Reason: "Scheduled", Message: "assigned to node-1", Type: "Normal",
		InvolvedObject: corev1.ObjectReference{Name: "web-abc"},
	}}}
	quotas := corev1.ResourceQuotaList{TypeMeta: typeMeta("ResourceQuotaList", "v1")}
	routes := map[string]any{
		"/version":                                     map[string]string{"major": "1", "minor": "30", "gitVersion": "v1.30.0"},
		"/api/v1/namespaces":                           namespaces,
		"/api/v1/namespaces/default":                   namespace,
		"/api/v1/nodes":                                nodes,
		"/apis/apps/v1/deployments":                    deployments,
		"/apis/apps/v1/namespaces/default/deployments": deployments,
		"/api/v1/pods":                                 pods,
		"/api/v1/namespaces/default/pods":              pods,
		"/api/v1/events":                               events,
		"/api/v1/namespaces/default/events":            events,
		"/api/v1/namespaces/default/resourcequotas":    quotas,
	}
	f := &fakeKubeAPI{}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fake-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Unauthorized","code":401}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/apis/apps/v1/namespaces/default/deployments" {
			// client-go may send the body as protobuf; answer with a canned
			// object rather than decoding it.
			d := appsv1.Deployment{TypeMeta: typeMeta("Deployment", "apps/v1"), ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"}}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(d)
			return
		}
		body, ok := routes[r.URL.Path]
		if r.Method != http.MethodGet || !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeKubeAPI) kubeconfig() string {
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.Certificate().Raw})
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster:
    server: %s
    certificate-authority-data: %s
users:
- name: fake
  user:
    token: fake-token
contexts:
- name: fake
  context:
    cluster: fake
    user: fake
current-context: fake
`, f.URL, base64.StdEncoding.EncodeToString(ca))
}

// kubernetesIntegrationAPI wires the handlers to a real Postgres (with
// migrations applied) and a real vault cipher. Skipped unless
// TEST_DATABASE_URL is set, like the store integration tests.
func kubernetesIntegrationAPI(t *testing.T) (*kubernetesAPI, *store.Store) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Kubernetes handler integration test")
	}
	st, err := store.Open(context.Background(), url)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(st.Close)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cipher, err := vault.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	return &kubernetesAPI{log: slog.New(slog.NewTextHandler(io.Discard, nil)), st: st, vault: vault.New(st, cipher)}, st
}

func TestKubernetesClusterLifecycleAgainstFakeAPIServer(t *testing.T) {
	k8s, st := kubernetesIntegrationAPI(t)
	fake := newFakeKubeAPI(t)
	mux := kubernetesTestMux(k8s)
	name := "it-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + fmt.Sprint(os.Getpid())
	body, _ := json.Marshal(map[string]string{"name": name, "kubeconfig": fake.kubeconfig()})

	// Connect: validates, probes /version, seals and stores the kubeconfig.
	rec := serve(mux, "POST", "/api/kubernetes/clusters", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect status = %d: %s", rec.Code, rec.Body)
	}
	var cluster store.KubernetesCluster
	if err := json.Unmarshal(rec.Body.Bytes(), &cluster); err != nil || cluster.ID == "" {
		t.Fatalf("connect response: %s (%v)", rec.Body, err)
	}
	t.Cleanup(func() { _, _ = st.DeleteKubernetesCluster(context.Background(), cluster.ID) })
	if cluster.APIServer != fake.URL || strings.Contains(rec.Body.String(), "fake-token") {
		t.Fatalf("connect response must carry the API server and never the credential: %s", rec.Body)
	}
	if !fake.called("GET /version") {
		t.Fatal("cluster was stored without a connectivity probe")
	}
	_, sealed, err := st.GetKubernetesClusterConfig(context.Background(), cluster.ID)
	if err != nil || strings.Contains(string(sealed), "fake-token") {
		t.Fatalf("kubeconfig must be stored sealed (err=%v)", err)
	}

	if rec := serve(mux, "POST", "/api/kubernetes/clusters", string(body)); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name status = %d, want 409", rec.Code)
	}

	rec = serve(mux, "GET", "/api/kubernetes/clusters", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"`+name+`"`) || strings.Contains(rec.Body.String(), "fake-token") {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}

	// Overview populates every section the Flutter screen renders.
	rec = serve(mux, "GET", "/api/kubernetes/clusters/"+cluster.ID+"/overview", "")
	if rec.Code != 200 {
		t.Fatalf("overview status = %d: %s", rec.Code, rec.Body)
	}
	var overview struct {
		Namespaces  []string         `json:"namespaces"`
		Nodes       []kubernetesNode `json:"nodes"`
		Deployments []map[string]any `json:"deployments"`
		Pods        []map[string]any `json:"pods"`
		Events      []map[string]any `json:"events"`
		Truncated   bool             `json:"truncated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if len(overview.Namespaces) != 1 || overview.Namespaces[0] != "default" || overview.Truncated {
		t.Fatalf("namespaces = %v truncated=%v", overview.Namespaces, overview.Truncated)
	}
	if len(overview.Nodes) != 1 || !overview.Nodes[0].Ready || overview.Nodes[0].GPU["nvidia.com/gpu"] != "1" || overview.Nodes[0].CPU != "4" {
		t.Fatalf("nodes = %+v", overview.Nodes)
	}
	if len(overview.Deployments) != 1 {
		t.Fatalf("deployments = %+v", overview.Deployments)
	}
	d := overview.Deployments[0]
	if d["name"] != "web" || d["managed"] != true || d["status"] != "Available" || d["image"] != "nginx:1.27" || d["cpu"] != "250m" || d["memory"] != "256Mi" || d["replicas"] != float64(2) || d["ready"] != float64(2) {
		t.Fatalf("deployment row = %+v", d)
	}
	if len(overview.Pods) != 1 || overview.Pods[0]["node"] != "node-1" || overview.Pods[0]["phase"] != "Running" {
		t.Fatalf("pods = %+v", overview.Pods)
	}
	if len(overview.Events) != 1 || overview.Events[0]["reason"] != "Scheduled" || overview.Events[0]["object"] != "web-abc" {
		t.Fatalf("events = %+v", overview.Events)
	}
	if rec := serve(mux, "GET", "/api/kubernetes/clusters/"+cluster.ID+"/overview?namespace=default", ""); rec.Code != 200 || !fake.called("GET /apis/apps/v1/namespaces/default/deployments") {
		t.Fatalf("namespace filter not applied: %d", rec.Code)
	}

	// Deploy preview: validated, scheduled, and sent to Kubernetes as a dry run.
	workload := `{"namespace":"default","name":"api","image":"example/api:1","replicas":2,"cpu":"500m","memory":"512Mi"}`
	rec = serve(mux, "POST", "/api/kubernetes/clusters/"+cluster.ID+"/workloads?dryRun=true", workload)
	if rec.Code != 200 {
		t.Fatalf("dry-run deploy status = %d: %s", rec.Code, rec.Body)
	}
	var preview struct {
		DryRun   bool            `json:"dryRun"`
		Schedule schedulePreview `json:"schedule"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &preview)
	if !preview.DryRun || !preview.Schedule.Fits || preview.Schedule.Placements["node-1"] != 2 {
		t.Fatalf("dry-run preview = %s", rec.Body)
	}
	if !fake.called("POST /apis/apps/v1/namespaces/default/deployments?dryRun=All") {
		t.Fatal("dry-run deploy must reach Kubernetes with dryRun=All")
	}

	gpuWorkload := strings.Replace(workload, `"memory":"512Mi"`, `"memory":"512Mi","gpuResource":"nvidia.com/gpu","gpuCount":2`, 1)
	if rec := serve(mux, "POST", "/api/kubernetes/clusters/"+cluster.ID+"/workloads?dryRun=true", gpuWorkload); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"fits":false`) {
		t.Fatalf("workload needing 2 GPUs on a 1-GPU cluster = %d %s", rec.Code, rec.Body)
	}
	missingNS := strings.Replace(workload, `"default"`, `"missing"`, 1)
	if rec := serve(mux, "POST", "/api/kubernetes/clusters/"+cluster.ID+"/workloads", missingNS); rec.Code != http.StatusNotFound {
		t.Fatalf("deploy to missing namespace = %d, want 404", rec.Code)
	}

	// Malformed and unknown cluster IDs are 404s, not database errors.
	for _, id := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		if rec := serve(mux, "GET", "/api/kubernetes/clusters/"+id+"/overview", ""); rec.Code != http.StatusNotFound {
			t.Fatalf("overview for %q = %d %s, want 404", id, rec.Code, rec.Body)
		}
		if rec := serve(mux, "DELETE", "/api/kubernetes/clusters/"+id, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("delete %q = %d %s, want 404", id, rec.Code, rec.Body)
		}
	}

	// Disconnect removes the stored credential; the cluster is then gone.
	if rec := serve(mux, "DELETE", "/api/kubernetes/clusters/"+cluster.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect = %d %s", rec.Code, rec.Body)
	}
	if rec := serve(mux, "DELETE", "/api/kubernetes/clusters/"+cluster.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("second disconnect = %d, want 404", rec.Code)
	}
	if rec := serve(mux, "GET", "/api/kubernetes/clusters/"+cluster.ID+"/overview", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("overview after disconnect = %d, want 404", rec.Code)
	}
}

func TestKubernetesConnectRejectsUnreachableOrUnauthorizedCluster(t *testing.T) {
	k8s, _ := kubernetesIntegrationAPI(t)
	fake := newFakeKubeAPI(t)
	mux := kubernetesTestMux(k8s)
	name := "it-reject-" + fmt.Sprint(os.Getpid())

	badToken := strings.Replace(fake.kubeconfig(), "token: fake-token", "token: wrong", 1)
	body, _ := json.Marshal(map[string]string{"name": name, "kubeconfig": badToken})
	if rec := serve(mux, "POST", "/api/kubernetes/clusters", string(body)); rec.Code != http.StatusForbidden {
		t.Fatalf("bad credentials = %d %s, want 403", rec.Code, rec.Body)
	}
	fake.Close()
	body, _ = json.Marshal(map[string]string{"name": name, "kubeconfig": fake.kubeconfig()})
	if rec := serve(mux, "POST", "/api/kubernetes/clusters", string(body)); rec.Code != http.StatusBadGateway {
		t.Fatalf("unreachable API server = %d %s, want 502", rec.Code, rec.Body)
	}
	rec := serve(mux, "GET", "/api/kubernetes/clusters", "")
	if strings.Contains(rec.Body.String(), name) {
		t.Fatal("a cluster that failed its connectivity probe was stored")
	}
}
