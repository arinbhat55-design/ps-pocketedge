package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/storage/driver"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// kubernetesTestMux registers the Kubernetes handlers on the same patterns
// as NewRouter, without auth, so path values resolve as in production.
func kubernetesTestMux(k8s *kubernetesAPI) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/kubernetes/clusters", k8s.listClusters)
	mux.HandleFunc("POST /api/kubernetes/clusters", k8s.createCluster)
	mux.HandleFunc("DELETE /api/kubernetes/clusters/{id}", k8s.deleteCluster)
	mux.HandleFunc("GET /api/kubernetes/clusters/{id}/overview", k8s.overview)
	mux.HandleFunc("GET /api/kubernetes/clusters/{id}/pods/{namespace}/{pod}/logs", k8s.podLogs)
	mux.HandleFunc("POST /api/kubernetes/clusters/{id}/workloads", k8s.workload)
	mux.HandleFunc("PUT /api/kubernetes/clusters/{id}/workloads/{namespace}/{name}", k8s.workload)
	mux.HandleFunc("GET /api/kubernetes/clusters/{id}/workloads/{namespace}/{name}/revisions", k8s.revisions)
	mux.HandleFunc("POST /api/kubernetes/clusters/{id}/workloads/{namespace}/{name}/rollback", k8s.rollbackWorkload)
	mux.HandleFunc("DELETE /api/kubernetes/clusters/{id}/workloads/{namespace}/{name}", k8s.deleteWorkload)
	mux.HandleFunc("POST /api/kubernetes/clusters/{id}/ai/ollama", k8s.deployOllama)
	mux.HandleFunc("GET /api/kubernetes/clusters/{id}/helm", k8s.listHelm)
	mux.HandleFunc("POST /api/kubernetes/clusters/{id}/helm", k8s.helmInstallOrUpgrade)
	mux.HandleFunc("GET /api/kubernetes/clusters/{id}/helm/{namespace}/{name}/history", k8s.helmHistory)
	mux.HandleFunc("POST /api/kubernetes/clusters/{id}/helm/{namespace}/{name}/rollback", k8s.helmRollback)
	mux.HandleFunc("DELETE /api/kubernetes/clusters/{id}/helm/{namespace}/{name}", k8s.helmUninstall)
	return mux
}

func serve(mux http.Handler, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func TestKubernetesErrorMapping(t *testing.T) {
	api := &kubernetesAPI{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	gr := schema.GroupResource{Group: "apps", Resource: "deployments"}
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"missing cluster":       {store.ErrNotFound, http.StatusNotFound},
		"missing helm release":  {fmt.Errorf("history: %w", driver.ErrReleaseNotFound), http.StatusNotFound},
		"missing k8s object":    {apierrors.NewNotFound(gr, "web"), http.StatusNotFound},
		"forbidden":             {apierrors.NewForbidden(gr, "web", fmt.Errorf("rbac")), http.StatusForbidden},
		"unauthorized":          {apierrors.NewUnauthorized("expired"), http.StatusForbidden},
		"already exists":        {apierrors.NewAlreadyExists(gr, "web"), http.StatusConflict},
		"conflict":              {apierrors.NewConflict(gr, "web", fmt.Errorf("stale")), http.StatusConflict},
		"bad request":           {apierrors.NewBadRequest("bad"), http.StatusBadRequest},
		"unreachable apiserver": {fmt.Errorf("dial tcp: connection refused"), http.StatusBadGateway},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			api.error(rec, tc.err)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

// Every mutating handler must reject malformed input before it reads the
// store or dials the cluster; a nil store would panic if it did.
func TestKubernetesHandlersRejectInvalidInputBeforeContactingCluster(t *testing.T) {
	mux := kubernetesTestMux(&kubernetesAPI{log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	const base = "/api/kubernetes/clusters/c1"
	validWorkload := `{"namespace":"default","name":"web","image":"nginx:1","replicas":1,"cpu":"100m","memory":"64Mi"}`
	for name, tc := range map[string]struct{ method, target, body string }{
		"cluster: malformed json":       {"POST", "/api/kubernetes/clusters", `{`},
		"cluster: blank name":           {"POST", "/api/kubernetes/clusters", `{"name":"  ","kubeconfig":"x"}`},
		"cluster: long name":            {"POST", "/api/kubernetes/clusters", `{"name":"` + strings.Repeat("a", 101) + `","kubeconfig":"x"}`},
		"cluster: invalid kubeconfig":   {"POST", "/api/kubernetes/clusters", `{"name":"lab","kubeconfig":"not: [yaml"}`},
		"workload: malformed json":      {"POST", base + "/workloads", `nope`},
		"workload: zero replicas":       {"POST", base + "/workloads", strings.Replace(validWorkload, `"replicas":1`, `"replicas":0`, 1)},
		"workload: uppercase name":      {"POST", base + "/workloads", strings.Replace(validWorkload, `"web"`, `"Web"`, 1)},
		"workload: missing memory":      {"POST", base + "/workloads", strings.Replace(validWorkload, `"64Mi"`, `""`, 1)},
		"workload: update bad replicas": {"PUT", base + "/workloads/default/web", strings.Replace(validWorkload, `"replicas":1`, `"replicas":99`, 1)},
		"rollback: missing revision":    {"POST", base + "/workloads/default/web/rollback", `{}`},
		"rollback: negative revision":   {"POST", base + "/workloads/default/web/rollback", `{"revision":-1}`},
		"ollama: unsafe model":          {"POST", base + "/ai/ollama", `{"namespace":"default","name":"llm","model":"x;rm -rf /","cpu":"1","memory":"1Gi","storageGi":10}`},
		"ollama: tiny storage":          {"POST", base + "/ai/ollama", `{"namespace":"default","name":"llm","model":"llama3","cpu":"1","memory":"1Gi","storageGi":1}`},
		"helm list: bad namespace":      {"GET", base + "/helm?namespace=Bad_NS", ``},
		"helm apply: bad release name":  {"POST", base + "/helm", `{"namespace":"default","name":"Bad_Name","chartBase64":"AA=="}`},
		"helm apply: invalid base64":    {"POST", base + "/helm", `{"namespace":"default","name":"web","chartBase64":"%%%"}`},
		"helm apply: empty chart":       {"POST", base + "/helm", `{"namespace":"default","name":"web","chartBase64":""}`},
		"helm apply: not a chart":       {"POST", base + "/helm", `{"namespace":"default","name":"web","chartBase64":"aGVsbG8="}`},
		"helm rollback: no revision":    {"POST", base + "/helm/default/web/rollback", `{"revision":0}`},
		"helm rollback: bad namespace":  {"POST", base + "/helm/Bad_NS/web/rollback", `{"revision":1}`},
		"helm history: bad name":        {"GET", base + "/helm/default/Bad_Name/history", ``},
		"helm uninstall: bad name":      {"DELETE", base + "/helm/default/Bad_Name", ``},
	} {
		t.Run(name, func(t *testing.T) {
			rec := serve(mux, tc.method, tc.target, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
			}
		})
	}
}

func TestValidKubeconfigRejectsUnsafeOrIncompleteConfigs(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(testKubeconfig, old, new, 1) }
	for name, raw := range map[string]string{
		"empty":                ``,
		"oversized":            strings.Repeat("#", 1<<20+1),
		"no current context":   replace("current-context: test", "current-context: \"\""),
		"unknown context":      replace("current-context: test", "current-context: other"),
		"unknown user":         replace("    user: test\ncurrent", "    user: nobody\ncurrent"),
		"skip tls verify":      replace("    certificate-authority-data: Y2E=", "    insecure-skip-tls-verify: true"),
		"ca file path":         replace("    certificate-authority-data: Y2E=", "    certificate-authority: /etc/ca.pem"),
		"client cert path":     replace("    token: test-token", "    client-certificate: /etc/cert.pem\n    client-key: /etc/key.pem"),
		"auth provider":        replace("    token: test-token", "    auth-provider:\n      name: gcp"),
		"impersonation":        replace("    token: test-token", "    token: test-token\n    as: admin"),
		"impersonation groups": replace("    token: test-token", "    token: test-token\n    as-groups: [\"system:masters\"]"),
		"no credentials":       replace("    token: test-token", "    username: only-user"),
		"server credentials":   replace("server: https://example.invalid", "server: https://u:p@example.invalid"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validKubeconfig(raw); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	basic := replace("    token: test-token", "    username: admin\n    password: secret")
	if _, err := validKubeconfig(basic); err != nil {
		t.Fatalf("inline basic auth rejected: %v", err)
	}
}

func TestValidateWorkloadBounds(t *testing.T) {
	valid := workloadRequest{Namespace: "default", Name: "web", Image: "nginx:1", Replicas: 1, CPU: "100m", Memory: "64Mi"}
	resources, err := validateWorkload(valid)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 {
		t.Fatalf("CPU-only workload should request exactly CPU and memory, got %v", resources)
	}
	for name, mutate := range map[string]func(*workloadRequest){
		"bad namespace":       func(r *workloadRequest) { r.Namespace = "Default" },
		"empty image":         func(r *workloadRequest) { r.Image = "" },
		"image with space":    func(r *workloadRequest) { r.Image = "nginx 1" },
		"too many replicas":   func(r *workloadRequest) { r.Replicas = 51 },
		"missing cpu":         func(r *workloadRequest) { r.CPU = "" },
		"negative cpu":        func(r *workloadRequest) { r.CPU = "-1" },
		"zero memory":         func(r *workloadRequest) { r.Memory = "0" },
		"garbage memory":      func(r *workloadRequest) { r.Memory = "lots" },
		"too many gpus":       func(r *workloadRequest) { r.GPUCount, r.GPUResource = 9, "nvidia.com/gpu" },
		"negative gpus":       func(r *workloadRequest) { r.GPUCount = -1 },
		"gpu without type":    func(r *workloadRequest) { r.GPUCount = 1 },
		"non-gpu as gpu type": func(r *workloadRequest) { r.GPUCount, r.GPUResource = 1, "example.com/fpga" },
	} {
		t.Run(name, func(t *testing.T) {
			req := valid
			mutate(&req)
			if _, err := validateWorkload(req); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestDeploymentForSelectorMatchesTemplate(t *testing.T) {
	req := workloadRequest{Namespace: "team", Name: "api", Image: "example/api:2", Replicas: 3, CPU: "250m", Memory: "128Mi"}
	resources, err := validateWorkload(req)
	if err != nil {
		t.Fatal(err)
	}
	d := deploymentFor(req, resources)
	if !managedDeployment(d) || d.Namespace != "team" || *d.Spec.Replicas != 3 {
		t.Fatalf("unexpected deployment metadata: %+v", d.ObjectMeta)
	}
	for k, v := range d.Spec.Selector.MatchLabels {
		if d.Spec.Template.Labels[k] != v {
			t.Fatalf("selector label %s=%s missing from pod template", k, v)
		}
	}
	c := d.Spec.Template.Spec.Containers[0]
	if c.Image != "example/api:2" || !c.Resources.Limits.Cpu().Equal(resource.MustParse("250m")) {
		t.Fatalf("container not built from request: %+v", c)
	}
	cpu, memory, gpuResource, gpuCount := workloadResources(d.Spec.Template.Spec.Containers)
	if cpu != "250m" || memory != "128Mi" || gpuResource != "" || gpuCount != 0 {
		t.Fatalf("workloadResources round-trip = %s %s %s %d", cpu, memory, gpuResource, gpuCount)
	}
}

func readyNode(name string, cpu, memory string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"zone": name}}, Status: corev1.NodeStatus{
		Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory)},
		Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
	}}
}

func cpuPod(cpu string) corev1.PodSpec {
	return corev1.PodSpec{Containers: []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)}}}}}
}

func TestEstimateScheduleSpreadsReplicasAndReportsShortfall(t *testing.T) {
	nodes := []corev1.Node{readyNode("a", "2", "4Gi"), readyNode("b", "2", "4Gi")}
	preview := estimateSchedule(nodes, nil, cpuPod("1"), 4)
	if !preview.Fits || preview.Placements["a"] != 2 || preview.Placements["b"] != 2 {
		t.Fatalf("4 replicas should fill both nodes: %+v", preview)
	}
	preview = estimateSchedule(nodes, nil, cpuPod("1"), 5)
	if preview.Fits || len(preview.Reasons) == 0 || !strings.Contains(preview.Reasons[0], "only 4 of 5") {
		t.Fatalf("5th replica must not fit: %+v", preview)
	}
	// Finished pods free their requests; running ones do not.
	done := corev1.Pod{Spec: cpuPod("2"), Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
	done.Spec.NodeName = "a"
	running := corev1.Pod{Spec: cpuPod("2"), Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	running.Spec.NodeName = "b"
	preview = estimateSchedule(nodes, []corev1.Pod{done, running}, cpuPod("1"), 2)
	if !preview.Fits || preview.Placements["a"] != 2 || preview.Placements["b"] != 0 {
		t.Fatalf("placement should skip the full node: %+v", preview)
	}
}

func TestEstimateScheduleHonoursNodeEligibility(t *testing.T) {
	notReady := readyNode("down", "8", "8Gi")
	notReady.Status.Conditions[0].Status = corev1.ConditionFalse
	if estimateSchedule([]corev1.Node{notReady}, nil, cpuPod("1"), 1).Fits {
		t.Fatal("NotReady node must not receive pods")
	}
	spec := cpuPod("1")
	spec.NodeSelector = map[string]string{"zone": "b"}
	preview := estimateSchedule([]corev1.Node{readyNode("a", "8", "8Gi"), readyNode("b", "8", "8Gi")}, nil, spec, 1)
	if !preview.Fits || preview.Placements["b"] != 1 {
		t.Fatalf("node selector ignored: %+v", preview)
	}
	spec.NodeSelector = map[string]string{"zone": "c"}
	if estimateSchedule([]corev1.Node{readyNode("a", "8", "8Gi")}, nil, spec, 1).Fits {
		t.Fatal("no node matches the selector")
	}
}

func TestPodRequestsCountsInitContainersAndOverhead(t *testing.T) {
	pod := corev1.Pod{Spec: corev1.PodSpec{
		Containers:     []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")}}}, {Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")}}}},
		InitContainers: []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("1Gi")}}}},
		Overhead:       corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("100Mi")},
	}}
	got := podRequests(pod)
	if !got.Cpu().Equal(resource.MustParse("2")) {
		t.Fatalf("CPU should be max(init, sum(containers)) = 2, got %s", got.Cpu())
	}
	want := resource.MustParse("1Gi")
	want.Add(resource.MustParse("100Mi"))
	if !got.Memory().Equal(want) {
		t.Fatalf("memory should include overhead: got %s want %s", got.Memory(), &want)
	}
}

func TestToleratesTaints(t *testing.T) {
	node := corev1.Node{Spec: corev1.NodeSpec{Taints: []corev1.Taint{{Key: "gpu", Value: "true", Effect: corev1.TaintEffectNoSchedule}}}}
	for name, tc := range map[string]struct {
		tolerations []corev1.Toleration
		want        bool
	}{
		"none":             {nil, false},
		"exists any key":   {[]corev1.Toleration{{Operator: corev1.TolerationOpExists}}, true},
		"exists same key":  {[]corev1.Toleration{{Key: "gpu", Operator: corev1.TolerationOpExists}}, true},
		"exists other key": {[]corev1.Toleration{{Key: "ssd", Operator: corev1.TolerationOpExists}}, false},
		"equal wrong val":  {[]corev1.Toleration{{Key: "gpu", Value: "false"}}, false},
		"wrong effect":     {[]corev1.Toleration{{Key: "gpu", Value: "true", Effect: corev1.TaintEffectNoExecute}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := toleratesTaints(corev1.PodSpec{Tolerations: tc.tolerations}, node); got != tc.want {
				t.Fatalf("toleratesTaints = %v, want %v", got, tc.want)
			}
		})
	}
	soft := corev1.Node{Spec: corev1.NodeSpec{Taints: []corev1.Taint{{Key: "x", Effect: corev1.TaintEffectPreferNoSchedule}}}}
	if !toleratesTaints(corev1.PodSpec{}, soft) {
		t.Fatal("PreferNoSchedule must not block placement")
	}
}

func TestQuotaCheckAllowsRequestsWithinQuota(t *testing.T) {
	quota := corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "team"}, Status: corev1.ResourceQuotaStatus{
		Hard: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("4"), corev1.ResourceLimitsMemory: resource.MustParse("4Gi")},
		Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("1")},
	}}
	requested := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")}
	if warnings := quotaCheck([]corev1.ResourceQuota{quota}, requested, 3); len(warnings) != 0 {
		t.Fatalf("exactly-at-quota request rejected: %v", warnings)
	}
	if warnings := quotaCheck([]corev1.ResourceQuota{quota}, requested, 4); len(warnings) != 1 || !strings.Contains(warnings[0], "requests.cpu") {
		t.Fatalf("over-quota CPU not reported: %v", warnings)
	}
}

func TestGPUResourcesListsOnlyAdvertisedGPUs(t *testing.T) {
	got := gpuResources(corev1.ResourceList{
		"nvidia.com/gpu":    resource.MustParse("2"),
		"amd.com/gpu":       resource.MustParse("0"),
		corev1.ResourceCPU:  resource.MustParse("8"),
		"example.com/fpgas": resource.MustParse("1"),
	})
	if len(got) != 1 || got[0] != "nvidia.com/gpu" {
		t.Fatalf("gpuResources = %v", got)
	}
}

func TestOwnedRevisionsSortedNewestFirstAndSkipUnnumbered(t *testing.T) {
	uid := types.UID("ours")
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{UID: uid, Annotations: map[string]string{deploymentRevisionAnnotation: "3"}}}
	rs := func(rev string) appsv1.ReplicaSet {
		return appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{UID: uid, Kind: "Deployment"}}, Annotations: map[string]string{deploymentRevisionAnnotation: rev}}}
	}
	revisions, byNumber := ownedRevisions(d, []appsv1.ReplicaSet{rs("1"), rs("3"), rs("garbage"), rs("2")})
	if len(revisions) != 3 || revisions[0].Revision != 3 || revisions[2].Revision != 1 {
		t.Fatalf("revisions not sorted newest first: %+v", revisions)
	}
	if !revisions[0].Current || revisions[1].Current || len(byNumber) != 3 {
		t.Fatalf("current flag or lookup wrong: %+v %v", revisions, byNumber)
	}
}

func TestEstimateUpdateFreesOwnPodsAndChecksRolloutSurge(t *testing.T) {
	selector := map[string]string{"app": "web"}
	deployment := func(image string, replicas int32) *appsv1.Deployment {
		spec := cpuPod("1")
		spec.Containers[0].Image = image
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: selector},
				Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: selector}, Spec: spec}},
		}
	}
	running := func(namespace string, podLabels map[string]string) corev1.Pod {
		pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Labels: podLabels}, Spec: cpuPod("1"), Status: corev1.PodStatus{Phase: corev1.PodRunning}}
		pod.Spec.NodeName = "a"
		return pod
	}
	own := func(n int) []corev1.Pod {
		pods := []corev1.Pod{}
		for i := 0; i < n; i++ {
			pods = append(pods, running("default", selector))
		}
		return pods
	}

	// Scaling 2 -> 3 on a 4-CPU node that also runs one unrelated Pod: the
	// deployment's own 2 Pods are replaced, so 3 fit beside the other one.
	pods := append(own(2), running("default", map[string]string{"app": "other"}))
	existing := deployment("web:1", 2)
	if preview := estimateUpdate([]corev1.Node{readyNode("a", "4", "8Gi")}, pods, existing, deployment("web:1", 3), 3); !preview.Fits {
		t.Fatalf("scale-up with room for the extra replica rejected: %+v", preview)
	}
	if estimateSchedule([]corev1.Node{readyNode("a", "4", "8Gi")}, pods, existing.Spec.Template.Spec, 3).Fits {
		t.Fatal("precondition: the naive estimate double-counts the deployment's own Pods")
	}

	// A Pod in another namespace with the same labels still occupies capacity.
	pods = append(own(2), running("team-b", selector), running("team-b", selector))
	if estimateUpdate([]corev1.Node{readyNode("a", "4", "8Gi")}, pods, existing, deployment("web:1", 3), 3).Fits {
		t.Fatal("same-labelled Pods from another namespace were treated as the deployment's own")
	}

	// Changing the image on a full node: with 2 replicas the default 25%
	// maxUnavailable rounds to 0, so the rollout needs a surge Pod to fit.
	full := []corev1.Node{readyNode("a", "2", "8Gi")}
	preview := estimateUpdate(full, own(2), existing, deployment("web:2", 2), 2)
	if preview.Fits || !strings.Contains(preview.Reasons[0], "surge") {
		t.Fatalf("stalling rollout not reported: %+v", preview)
	}
	// Scaling alone doesn't roll Pods, so no surge is needed.
	if preview := estimateUpdate(full, own(2), existing, deployment("web:1", 2), 2); !preview.Fits {
		t.Fatalf("unchanged template needs no surge: %+v", preview)
	}
	// Recreate stops old Pods first.
	recreate := existing.DeepCopy()
	recreate.Spec.Strategy.Type = appsv1.RecreateDeploymentStrategyType
	if preview := estimateUpdate(full, own(2), recreate, deployment("web:2", 2), 2); !preview.Fits {
		t.Fatalf("Recreate strategy needs no surge: %+v", preview)
	}
	// With 4 replicas, 25% maxUnavailable allows one Pod down at a time.
	existing4 := deployment("web:1", 4)
	if preview := estimateUpdate([]corev1.Node{readyNode("a", "4", "8Gi")}, own(4), existing4, deployment("web:2", 4), 4); !preview.Fits {
		t.Fatalf("maxUnavailable=1 rollout rejected: %+v", preview)
	}
	// An explicit maxUnavailable of 0 needs the surge even at 4 replicas.
	strict := existing4.DeepCopy()
	zero := intstr.FromInt(0)
	strict.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{MaxUnavailable: &zero}
	if estimateUpdate([]corev1.Node{readyNode("a", "4", "8Gi")}, own(4), strict, deployment("web:2", 4), 4).Fits {
		t.Fatal("maxUnavailable=0 on a full node must be rejected")
	}
}
