package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"helm.sh/helm/v3/pkg/storage/driver"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/vault"
)

// Kubernetes uses its own workload model. Docker deployments remain tied to
// server agents and Compose; no implicit Compose-to-Kubernetes conversion.
type kubernetesAPI struct {
	log   *slog.Logger
	st    *store.Store
	vault *vault.Vault
}

func (api *kubernetesAPI) audit(r *http.Request, action, entityType, entityID, summary string) {
	a := actorFromRequest(r)
	if err := api.st.RecordAudit(r.Context(), a.ID, a.Local, action, entityType, entityID, summary, nil); err != nil {
		api.log.Error("failed to record Kubernetes audit event", "action", action, "error", err)
	}
}

func validKubeconfig(raw string) (string, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return "", errors.New("kubeconfig must be between 1 byte and 1 MB")
	}
	cfg, err := clientcmd.Load([]byte(raw))
	if err != nil {
		return "", fmt.Errorf("invalid kubeconfig: %w", err)
	}
	if cfg.CurrentContext == "" {
		return "", errors.New("kubeconfig must have a current context")
	}
	ctx := cfg.Contexts[cfg.CurrentContext]
	if ctx == nil {
		return "", errors.New("current context was not found")
	}
	cluster := cfg.Clusters[ctx.Cluster]
	user := cfg.AuthInfos[ctx.AuthInfo]
	if cluster == nil || user == nil {
		return "", errors.New("current context must select a cluster and user")
	}
	parsed, err := url.Parse(cluster.Server)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("cluster API server must be an HTTPS URL")
	}
	if cluster.InsecureSkipTLSVerify {
		return "", errors.New("insecure TLS verification is not allowed")
	}
	// Uploaded configs must be self-contained. File references and credential
	// plugins would execute or read files on the control-plane host.
	if cluster.CertificateAuthority != "" || user.ClientCertificate != "" || user.ClientKey != "" || user.TokenFile != "" || user.Exec != nil || user.AuthProvider != nil {
		return "", errors.New("use inline certificate data or token credentials; file paths and authentication plugins are not allowed")
	}
	if user.Impersonate != "" || len(user.ImpersonateGroups) > 0 {
		return "", errors.New("impersonation is not allowed")
	}
	if user.Token == "" && (len(user.ClientCertificateData) == 0 || len(user.ClientKeyData) == 0) && (user.Username == "" || user.Password == "") {
		return "", errors.New("current user needs an inline token, client certificate, or username and password")
	}
	if _, err := clientcmd.RESTConfigFromKubeConfig([]byte(raw)); err != nil {
		return "", fmt.Errorf("invalid kubeconfig: %w", err)
	}
	return cluster.Server, nil
}

func (api *kubernetesAPI) client(ctx context.Context, id string) (kubernetes.Interface, *store.KubernetesCluster, error) {
	cluster, sealed, err := api.st.GetKubernetesClusterConfig(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	raw, err := api.vault.OpenValue(sealed)
	if err != nil {
		return nil, nil, err
	}
	config, err := clientcmd.RESTConfigFromKubeConfig([]byte(raw))
	if err != nil {
		return nil, nil, err
	}
	config.Timeout = 12 * time.Second
	config.QPS = 10
	config.Burst = 20
	client, err := kubernetes.NewForConfig(config)
	return client, cluster, err
}

func (api *kubernetesAPI) error(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, driver.ErrReleaseNotFound) || apierrors.IsNotFound(err) {
		http.Error(w, "cluster or resource not found", http.StatusNotFound)
		return
	}
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		http.Error(w, "Kubernetes credentials lack permission", http.StatusForbidden)
		return
	}
	if apierrors.IsAlreadyExists(err) || apierrors.IsConflict(err) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.log.Warn("Kubernetes request failed", "error", err)
	http.Error(w, "Kubernetes request failed: "+err.Error(), http.StatusBadGateway)
}

func (api *kubernetesAPI) createCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		Kubeconfig string `json:"kubeconfig"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20+4096)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", 400)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		http.Error(w, "name must be 1–100 characters", 400)
		return
	}
	server, err := validKubeconfig(req.Kubeconfig)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	config, _ := clientcmd.RESTConfigFromKubeConfig([]byte(req.Kubeconfig))
	config.Timeout = 8 * time.Second
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		http.Error(w, "invalid Kubernetes client configuration", 400)
		return
	}
	if _, err := client.Discovery().ServerVersion(); err != nil {
		api.error(w, err)
		return
	}
	sealed, err := api.vault.Seal(req.Kubeconfig)
	if err != nil {
		api.error(w, err)
		return
	}
	cluster, err := api.st.CreateKubernetesCluster(r.Context(), req.Name, server, sealed)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "cluster name already exists", 409)
			return
		}
		api.error(w, err)
		return
	}
	api.audit(r, "kubernetes.cluster.connect", "kubernetes_cluster", cluster.ID, "connected Kubernetes cluster "+cluster.Name)
	writeJSON(w, http.StatusCreated, cluster)
}

func (api *kubernetesAPI) listClusters(w http.ResponseWriter, r *http.Request) {
	clusters, err := api.st.ListKubernetesClusters(r.Context())
	if err != nil {
		api.error(w, err)
		return
	}
	writeJSON(w, 200, clusters)
}

func (api *kubernetesAPI) deleteCluster(w http.ResponseWriter, r *http.Request) {
	cluster, _, err := api.st.GetKubernetesClusterConfig(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}
	if err != nil {
		api.error(w, err)
		return
	}
	if cluster.LocalKindName != "" {
		http.Error(w, "use the local-cluster delete action to remove the kind cluster and its record", http.StatusConflict)
		return
	}
	deleted, err := api.st.DeleteKubernetesCluster(r.Context(), r.PathValue("id"))
	if err != nil {
		api.error(w, err)
		return
	}
	if !deleted {
		http.Error(w, "cluster not found", 404)
		return
	}
	api.audit(r, "kubernetes.cluster.disconnect", "kubernetes_cluster", r.PathValue("id"), "disconnected Kubernetes cluster")
	w.WriteHeader(http.StatusNoContent)
}

func namespaceParam(r *http.Request) string {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		return metav1.NamespaceAll
	}
	return ns
}

type kubernetesNode struct {
	Name   string            `json:"name"`
	Ready  bool              `json:"ready"`
	CPU    string            `json:"cpu"`
	Memory string            `json:"memory"`
	GPU    map[string]string `json:"gpu"`
	Usage  map[string]string `json:"usage,omitempty"`
}

func (api *kubernetesAPI) overview(w http.ResponseWriter, r *http.Request) {
	client, _, err := api.client(r.Context(), r.PathValue("id"))
	if err != nil {
		api.error(w, err)
		return
	}
	ctx := r.Context()
	ns := namespaceParam(r)
	namespaces, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		api.error(w, err)
		return
	}
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		api.error(w, err)
		return
	}
	deployments, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		api.error(w, err)
		return
	}
	pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		api.error(w, err)
		return
	}
	events, err := client.CoreV1().Events(ns).List(ctx, metav1.ListOptions{Limit: 500})
	if err != nil {
		api.error(w, err)
		return
	}
	nsList := make([]string, 0, len(namespaces.Items))
	for _, item := range namespaces.Items {
		nsList = append(nsList, item.Name)
	}
	nodeList := make([]kubernetesNode, 0, len(nodes.Items))
	metricUsage := map[string]map[string]string{}
	if raw, err := client.Discovery().RESTClient().Get().AbsPath("/apis/metrics.k8s.io/v1beta1/nodes").DoRaw(ctx); err == nil {
		var metrics struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Usage map[string]string `json:"usage"`
			} `json:"items"`
		}
		if json.Unmarshal(raw, &metrics) == nil {
			for _, item := range metrics.Items {
				metricUsage[item.Metadata.Name] = item.Usage
			}
		}
	}
	for _, item := range nodes.Items {
		gpu := map[string]string{}
		for key, quantity := range item.Status.Allocatable {
			if strings.Contains(string(key), "gpu") {
				gpu[string(key)] = quantity.String()
			}
		}
		ready := false
		for _, condition := range item.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		nodeList = append(nodeList, kubernetesNode{Name: item.Name, Ready: ready, CPU: item.Status.Allocatable.Cpu().String(), Memory: item.Status.Allocatable.Memory().String(), GPU: gpu, Usage: metricUsage[item.Name]})
	}
	workloads := make([]map[string]any, 0, len(deployments.Items))
	for _, item := range deployments.Items {
		cpu, memory, gpuResource, gpuCount := workloadResources(item.Spec.Template.Spec.Containers)
		desired := int32(1)
		if item.Spec.Replicas != nil {
			desired = *item.Spec.Replicas
		}
		status := "Progressing"
		if item.Status.AvailableReplicas >= desired && item.Status.ObservedGeneration >= item.Generation {
			status = "Available"
		}
		for _, condition := range item.Status.Conditions {
			if condition.Type == appsv1.DeploymentProgressing && condition.Status == corev1.ConditionFalse {
				status = condition.Reason
			}
		}
		workloads = append(workloads, map[string]any{"name": item.Name, "namespace": item.Namespace, "ready": item.Status.ReadyReplicas, "replicas": desired, "image": firstImage(item.Spec.Template.Spec.Containers), "cpu": cpu, "memory": memory, "gpuResource": gpuResource, "gpuCount": gpuCount, "status": status, "managed": item.Labels["app.kubernetes.io/managed-by"] == "pspocketedge"})
	}
	podList := make([]map[string]any, 0, len(pods.Items))
	for _, item := range pods.Items {
		var reason string
		for _, state := range item.Status.ContainerStatuses {
			if state.State.Waiting != nil {
				reason = state.State.Waiting.Reason
				break
			}
		}
		podList = append(podList, map[string]any{"name": item.Name, "namespace": item.Namespace, "phase": item.Status.Phase, "reason": reason, "node": item.Spec.NodeName})
	}
	eventList := make([]map[string]any, 0, len(events.Items))
	for _, item := range events.Items {
		eventList = append(eventList, map[string]any{"namespace": item.Namespace, "reason": item.Reason, "message": item.Message, "object": item.InvolvedObject.Name, "type": item.Type, "time": item.LastTimestamp})
	}
	writeJSON(w, 200, map[string]any{"namespaces": nsList, "nodes": nodeList, "deployments": workloads, "pods": podList, "events": eventList, "truncated": namespaces.Continue != "" || nodes.Continue != "" || deployments.Continue != "" || pods.Continue != "" || events.Continue != ""})
}

func firstImage(containers []corev1.Container) string {
	if len(containers) == 0 {
		return ""
	}
	return containers[0].Image
}

func workloadResources(containers []corev1.Container) (cpu, memory, gpuResource string, gpuCount int64) {
	if len(containers) == 0 {
		return
	}
	for name, quantity := range containers[0].Resources.Requests {
		switch name {
		case corev1.ResourceCPU:
			cpu = quantity.String()
		case corev1.ResourceMemory:
			memory = quantity.String()
		default:
			if strings.Contains(string(name), "gpu") {
				gpuResource = string(name)
				gpuCount = quantity.Value()
			}
		}
	}
	return
}

func (api *kubernetesAPI) podLogs(w http.ResponseWriter, r *http.Request) {
	client, _, err := api.client(r.Context(), r.PathValue("id"))
	if err != nil {
		api.error(w, err)
		return
	}
	lines := int64(200)
	if value := r.URL.Query().Get("lines"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 1 || n > 1000 {
			http.Error(w, "lines must be between 1 and 1000", 400)
			return
		}
		lines = n
	}
	stream, err := client.CoreV1().Pods(r.PathValue("namespace")).GetLogs(r.PathValue("pod"), &corev1.PodLogOptions{TailLines: &lines, Container: r.URL.Query().Get("container")}).Stream(r.Context())
	if err != nil {
		api.error(w, err)
		return
	}
	defer stream.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.Copy(w, io.LimitReader(stream, 1<<20))
}

type workloadRequest struct {
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	Image       string `json:"image"`
	Replicas    int32  `json:"replicas"`
	CPU         string `json:"cpu"`
	Memory      string `json:"memory"`
	GPUResource string `json:"gpuResource"`
	GPUCount    int64  `json:"gpuCount"`
}

func validateWorkload(req workloadRequest) (corev1.ResourceList, error) {
	if len(validation.IsDNS1123Label(req.Name)) > 0 || len(validation.IsDNS1123Label(req.Namespace)) > 0 {
		return nil, errors.New("name and namespace must be valid Kubernetes DNS labels")
	}
	if len(req.Image) == 0 || len(req.Image) > 512 || strings.ContainsAny(req.Image, " \n\t") {
		return nil, errors.New("valid image reference is required")
	}
	if req.Replicas < 1 || req.Replicas > 50 {
		return nil, errors.New("replicas must be between 1 and 50")
	}
	resources := corev1.ResourceList{}
	if req.CPU == "" || req.Memory == "" {
		return nil, errors.New("CPU and memory requests are required")
	}
	if req.CPU != "" {
		q, err := resource.ParseQuantity(req.CPU)
		if err != nil || q.Sign() <= 0 {
			return nil, errors.New("invalid CPU request")
		}
		resources[corev1.ResourceCPU] = q
	}
	if req.Memory != "" {
		q, err := resource.ParseQuantity(req.Memory)
		if err != nil || q.Sign() <= 0 {
			return nil, errors.New("invalid memory request")
		}
		resources[corev1.ResourceMemory] = q
	}
	if req.GPUCount < 0 || req.GPUCount > 8 {
		return nil, errors.New("GPU count must be between 0 and 8")
	}
	if req.GPUCount > 0 {
		if req.GPUResource == "" || !strings.Contains(req.GPUResource, "/") || !strings.Contains(req.GPUResource, "gpu") {
			return nil, errors.New("select an advertised GPU resource")
		}
		resources[corev1.ResourceName(req.GPUResource)] = *resource.NewQuantity(req.GPUCount, resource.DecimalSI)
	}
	return resources, nil
}

func deploymentFor(req workloadRequest, resources corev1.ResourceList) *appsv1.Deployment {
	labels := map[string]string{"app": req.Name, "app.kubernetes.io/managed-by": "pspocketedge"}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: req.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &req.Replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name: "app", Image: req.Image,
					Resources: corev1.ResourceRequirements{Requests: resources, Limits: resources},
				}}},
			},
		},
	}
}

func (api *kubernetesAPI) workload(w http.ResponseWriter, r *http.Request) {
	var req workloadRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", 400)
		return
	}
	resources, err := validateWorkload(req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	client, _, err := api.client(r.Context(), r.PathValue("id"))
	if err != nil {
		api.error(w, err)
		return
	}
	if _, err := client.CoreV1().Namespaces().Get(r.Context(), req.Namespace, metav1.GetOptions{}); err != nil {
		api.error(w, err)
		return
	}
	deployment := deploymentFor(req, resources)
	var result, existing *appsv1.Deployment
	if r.Method == http.MethodPut {
		if req.Namespace != r.PathValue("namespace") || req.Name != r.PathValue("name") {
			http.Error(w, "workload path and body must match", 400)
			return
		}
		existing, err = client.AppsV1().Deployments(req.Namespace).Get(r.Context(), req.Name, metav1.GetOptions{})
		if err != nil {
			api.error(w, err)
			return
		}
		if existing.Labels["app.kubernetes.io/managed-by"] != "pspocketedge" {
			http.Error(w, "only PS-pocketEdge-managed deployments can be updated", 403)
			return
		}
		if len(existing.Spec.Template.Spec.Containers) == 0 {
			http.Error(w, "deployment has no container to update", 409)
			return
		}
		deployment = existing.DeepCopy()
		deployment.Spec.Replicas = &req.Replicas
		deployment.Spec.Template.Spec.Containers[0].Image = req.Image
		deployment.Spec.Template.Spec.Containers[0].Resources.Requests = resources
		deployment.Spec.Template.Spec.Containers[0].Resources.Limits = resources
	}
	nodes, err := client.CoreV1().Nodes().List(r.Context(), metav1.ListOptions{})
	if err != nil {
		api.error(w, err)
		return
	}
	pods, err := client.CoreV1().Pods(metav1.NamespaceAll).List(r.Context(), metav1.ListOptions{})
	if err != nil {
		api.error(w, err)
		return
	}
	var placement schedulePreview
	if existing != nil {
		placement = estimateUpdate(nodes.Items, pods.Items, existing, deployment, req.Replicas)
	} else {
		placement = estimateSchedule(nodes.Items, pods.Items, deployment.Spec.Template.Spec, req.Replicas)
	}
	if !placement.Fits {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "workload does not currently fit the cluster", "schedule": placement})
		return
	}
	if r.Method == http.MethodPost {
		quotas, err := client.CoreV1().ResourceQuotas(req.Namespace).List(r.Context(), metav1.ListOptions{})
		if err != nil {
			api.error(w, err)
			return
		}
		if warnings := quotaCheck(quotas.Items, resources, req.Replicas); len(warnings) > 0 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": strings.Join(warnings, "; "), "schedule": placement})
			return
		}
	}
	if r.Method == http.MethodPut {
		result, err = client.AppsV1().Deployments(req.Namespace).Update(r.Context(), deployment, metav1.UpdateOptions{DryRun: dryRun(r)})
	} else {
		result, err = client.AppsV1().Deployments(req.Namespace).Create(r.Context(), deployment, metav1.CreateOptions{DryRun: dryRun(r)})
	}
	if err != nil {
		api.error(w, err)
		return
	}
	status := http.StatusCreated
	if r.Method == http.MethodPut {
		status = 200
	}
	if r.URL.Query().Get("dryRun") == "true" {
		status = 200
	} else {
		action := "kubernetes.workload.create"
		if r.Method == http.MethodPut {
			action = "kubernetes.workload.update"
		}
		api.audit(r, action, "kubernetes_deployment", result.Namespace+"/"+result.Name, action+" on cluster "+r.PathValue("id"))
	}
	writeJSON(w, status, map[string]any{"name": result.Name, "namespace": result.Namespace, "image": req.Image, "replicas": req.Replicas, "dryRun": r.URL.Query().Get("dryRun") == "true", "schedule": placement})
}

func anyNodeFits(nodes []corev1.Node, requested corev1.ResourceList) bool {
	for _, node := range nodes {
		if node.Spec.Unschedulable {
			continue
		}
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				ready = true
				break
			}
		}
		if !ready {
			continue
		}
		fits := true
		for name, want := range requested {
			have, ok := node.Status.Allocatable[name]
			if !ok || have.Cmp(want) < 0 {
				fits = false
				break
			}
		}
		if fits {
			return true
		}
	}
	return false
}

func dryRun(r *http.Request) []string {
	if r.URL.Query().Get("dryRun") == "true" {
		return []string{metav1.DryRunAll}
	}
	return nil
}
