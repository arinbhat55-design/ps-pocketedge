package api

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const testKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://example.invalid
    certificate-authority-data: Y2E=
users:
- name: test
  user:
    token: test-token
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
`

func TestValidKubeconfigRejectsHostFilesAndPlugins(t *testing.T) {
	if server, err := validKubeconfig(testKubeconfig); err != nil || server != "https://example.invalid" {
		t.Fatalf("valid inline kubeconfig: server=%q err=%v", server, err)
	}
	for name, raw := range map[string]string{
		"file":     strings.Replace(testKubeconfig, "    token: test-token", "    tokenFile: /etc/shadow", 1),
		"exec":     strings.Replace(testKubeconfig, "    token: test-token", "    exec:\n      command: /bin/sh", 1),
		"insecure": strings.Replace(testKubeconfig, "    server: https://example.invalid", "    server: http://example.invalid", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validKubeconfig(raw); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestWorkloadValidationAndNodeCapacity(t *testing.T) {
	req := workloadRequest{Namespace: "default", Name: "model", Image: "example/model:v1", Replicas: 1, CPU: "500m", Memory: "1Gi", GPUResource: "nvidia.com/gpu", GPUCount: 1}
	resources, err := validateWorkload(req)
	if err != nil {
		t.Fatal(err)
	}
	node := corev1.Node{Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("2"),
		corev1.ResourceMemory: resource.MustParse("4Gi"),
		"nvidia.com/gpu":      resource.MustParse("1"),
	}, Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
	if !anyNodeFits([]corev1.Node{node}, resources) {
		t.Fatal("expected node to fit")
	}
	node.Spec.Unschedulable = true
	if anyNodeFits([]corev1.Node{node}, resources) {
		t.Fatal("unschedulable node must not fit")
	}
	node.Spec.Unschedulable = false
	node.Status.Allocatable["nvidia.com/gpu"] = resource.MustParse("0")
	if anyNodeFits([]corev1.Node{node}, resources) {
		t.Fatal("GPU shortage must not fit")
	}
	req.Name = "Invalid_Name"
	if _, err := validateWorkload(req); err == nil {
		t.Fatal("invalid Kubernetes name accepted")
	}
}
