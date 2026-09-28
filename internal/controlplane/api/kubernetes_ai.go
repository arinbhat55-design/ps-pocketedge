package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
)

const ollamaImage = "ollama/ollama:0.34.1"

var modelNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,119}$`)

type ollamaRequest struct {
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	Model       string `json:"model"`
	CPU         string `json:"cpu"`
	Memory      string `json:"memory"`
	StorageGi   int64  `json:"storageGi"`
	GPUResource string `json:"gpuResource"`
	GPUCount    int64  `json:"gpuCount"`
}

func ollamaObjects(req ollamaRequest) (*corev1.PersistentVolumeClaim, *corev1.Service, *appsv1.Deployment, error) {
	if len(validation.IsDNS1123Label(req.Name)) > 0 || len(validation.IsDNS1123Label(req.Namespace)) > 0 {
		return nil, nil, nil, fmt.Errorf("name and namespace must be valid Kubernetes DNS labels")
	}
	if !modelNamePattern.MatchString(req.Model) {
		return nil, nil, nil, fmt.Errorf("invalid model name")
	}
	if req.StorageGi < 5 || req.StorageGi > 1000 {
		return nil, nil, nil, fmt.Errorf("storage must be between 5 and 1000 GiB")
	}
	limits, err := validateWorkload(workloadRequest{Namespace: req.Namespace, Name: req.Name, Image: ollamaImage, Replicas: 1, CPU: req.CPU, Memory: req.Memory, GPUResource: req.GPUResource, GPUCount: req.GPUCount})
	if err != nil {
		return nil, nil, nil, err
	}
	labels := map[string]string{"app": req.Name, "app.kubernetes.io/managed-by": "pspocketedge", "app.kubernetes.io/name": "ollama"}
	storage := *resource.NewQuantity(req.StorageGi<<30, resource.BinarySI)
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: req.Name + "-models", Namespace: req.Namespace, Labels: labels}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: storage}}}}
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: req.Namespace, Labels: labels}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: labels, Ports: []corev1.ServicePort{{Name: "http", Port: 11434, Protocol: corev1.ProtocolTCP}}}}
	// The model name is passed as a separate shell argument, never interpolated
	// into the shell program. Readiness stays false until the pull completes.
	script := `ollama serve & server=$!; until ollama list >/dev/null 2>&1; do sleep 2; done; ollama pull "$1" || exit 1; wait "$server"`
	replicas := int32(1)
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: req.Namespace, Labels: labels, Annotations: map[string]string{"pspocketedge.ai/model": req.Model}}, Spec: appsv1.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: labels}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "ollama", Image: ollamaImage, Command: []string{"sh", "-c", script, "--", req.Model},
		Ports:          []corev1.ContainerPort{{Name: "http", ContainerPort: 11434}},
		Resources:      corev1.ResourceRequirements{Requests: limits, Limits: limits},
		VolumeMounts:   []corev1.VolumeMount{{Name: "models", MountPath: "/root/.ollama"}},
		ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"ollama", "show", req.Model}}}, PeriodSeconds: 15, FailureThreshold: 3},
		LivenessProbe:  &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/api/version", Port: intstr.FromInt(11434)}}, PeriodSeconds: 30, FailureThreshold: 5},
	}}, Volumes: []corev1.Volume{{Name: "models", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvc.Name}}}}}}}}
	return pvc, service, deployment, nil
}

func (api *kubernetesAPI) deployOllama(w http.ResponseWriter, r *http.Request) {
	var req ollamaRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", 400)
		return
	}
	pvc, service, deployment, err := ollamaObjects(req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	client, _, err := api.client(r.Context(), r.PathValue("id"))
	if err != nil {
		api.error(w, err)
		return
	}
	ns := req.Namespace
	if _, err := client.CoreV1().Namespaces().Get(r.Context(), ns, metav1.GetOptions{}); err != nil {
		api.error(w, err)
		return
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
	placement := estimateSchedule(nodes.Items, pods.Items, deployment.Spec.Template.Spec, 1)
	if !placement.Fits {
		writeJSON(w, 422, map[string]any{"error": "model service does not currently fit the cluster", "schedule": placement})
		return
	}
	quotas, err := client.CoreV1().ResourceQuotas(ns).List(r.Context(), metav1.ListOptions{})
	if err != nil {
		api.error(w, err)
		return
	}
	if warnings := quotaCheck(quotas.Items, deployment.Spec.Template.Spec.Containers[0].Resources.Requests, 1); len(warnings) > 0 {
		http.Error(w, strings.Join(warnings, "; "), 422)
		return
	}
	// First verify all three objects with admission dry-run. A name conflict
	// aborts before creating any persistent data.
	if _, err = client.CoreV1().PersistentVolumeClaims(ns).Create(r.Context(), pvc, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}); err != nil {
		api.error(w, err)
		return
	}
	if _, err = client.CoreV1().Services(ns).Create(r.Context(), service, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}); err != nil {
		api.error(w, err)
		return
	}
	if _, err = client.AppsV1().Deployments(ns).Create(r.Context(), deployment, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}); err != nil {
		api.error(w, err)
		return
	}
	if r.URL.Query().Get("dryRun") == "true" {
		writeJSON(w, 200, map[string]any{"name": req.Name, "model": req.Model, "service": req.Name + "." + ns + ".svc.cluster.local:11434", "schedule": placement, "dryRun": true})
		return
	}
	createdPVC, err := client.CoreV1().PersistentVolumeClaims(ns).Create(r.Context(), pvc, metav1.CreateOptions{})
	if err != nil {
		api.error(w, err)
		return
	}
	createdService, err := client.CoreV1().Services(ns).Create(r.Context(), service, metav1.CreateOptions{})
	if err != nil {
		_ = client.CoreV1().PersistentVolumeClaims(ns).Delete(r.Context(), createdPVC.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &createdPVC.UID}})
		api.error(w, err)
		return
	}
	if _, err = client.AppsV1().Deployments(ns).Create(r.Context(), deployment, metav1.CreateOptions{}); err != nil {
		_ = client.CoreV1().Services(ns).Delete(r.Context(), createdService.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &createdService.UID}})
		_ = client.CoreV1().PersistentVolumeClaims(ns).Delete(r.Context(), createdPVC.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &createdPVC.UID}})
		api.error(w, err)
		return
	}
	api.audit(r, "kubernetes.ai.ollama.deploy", "kubernetes_deployment", ns+"/"+req.Name, "deployed Ollama model "+req.Model)
	writeJSON(w, 201, map[string]any{"name": req.Name, "model": req.Model, "service": req.Name + "." + ns + ".svc.cluster.local:11434", "schedule": placement, "dryRun": false})
}
