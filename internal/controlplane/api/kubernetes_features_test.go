package api

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestEstimateScheduleAccountsForPodsAndTaints(t *testing.T) {
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-1"}, Status: corev1.NodeStatus{
		Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi"), "nvidia.com/gpu": resource.MustParse("1")},
		Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
	}}
	template := corev1.PodSpec{Containers: []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("3Gi"), "nvidia.com/gpu": resource.MustParse("1"),
	}}}}}
	if !estimateSchedule([]corev1.Node{node}, nil, template, 1).Fits {
		t.Fatal("empty node should fit")
	}
	occupied := corev1.Pod{Spec: corev1.PodSpec{NodeName: "gpu-1", Containers: []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{"nvidia.com/gpu": resource.MustParse("1")}}}}}}
	if estimateSchedule([]corev1.Node{node}, []corev1.Pod{occupied}, template, 1).Fits {
		t.Fatal("allocated GPU must block placement")
	}
	node.Spec.Taints = []corev1.Taint{{Key: "dedicated", Value: "other", Effect: corev1.TaintEffectNoSchedule}}
	if estimateSchedule([]corev1.Node{node}, nil, template, 1).Fits {
		t.Fatal("untolerated taint must block placement")
	}
	template.Tolerations = []corev1.Toleration{{Key: "dedicated", Value: "other", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule}}
	if !estimateSchedule([]corev1.Node{node}, nil, template, 1).Fits {
		t.Fatal("tolerated taint should fit")
	}
}

func TestQuotaCheckRejectsExcessRequests(t *testing.T) {
	quota := corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "team"}, Status: corev1.ResourceQuotaStatus{
		Hard: corev1.ResourceList{corev1.ResourceRequestsMemory: resource.MustParse("4Gi")},
		Used: corev1.ResourceList{corev1.ResourceRequestsMemory: resource.MustParse("3Gi")},
	}}
	warnings := quotaCheck([]corev1.ResourceQuota{quota}, corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")}, 2)
	if len(warnings) == 0 {
		t.Fatal("expected quota warning")
	}
}

func TestOwnedRevisionsExcludeOtherDeployments(t *testing.T) {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{UID: types.UID("ours"), Annotations: map[string]string{deploymentRevisionAnnotation: "2"}}}
	sets := []appsv1.ReplicaSet{
		{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{UID: types.UID("ours"), Kind: "Deployment"}}, Annotations: map[string]string{deploymentRevisionAnnotation: "2"}}},
		{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{UID: types.UID("other"), Kind: "Deployment"}}, Annotations: map[string]string{deploymentRevisionAnnotation: "1"}}},
	}
	revisions, byNumber := ownedRevisions(deployment, sets)
	if len(revisions) != 1 || !revisions[0].Current || byNumber[1] != nil {
		t.Fatalf("unexpected revisions: %#v", revisions)
	}
}

func TestOllamaTemplatePersistsModelAndWaitsForPull(t *testing.T) {
	pvc, service, deployment, err := ollamaObjects(ollamaRequest{Namespace: "default", Name: "assistant", Model: "llama3.2:1b", CPU: "2", Memory: "4Gi", StorageGi: 20, GPUResource: "nvidia.com/gpu", GPUCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if pvc.Name != "assistant-models" || service.Spec.Ports[0].Port != 11434 {
		t.Fatal("missing persistent storage or service port")
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if container.Command[len(container.Command)-1] != "llama3.2:1b" || container.ReadinessProbe.Exec == nil || container.VolumeMounts[0].MountPath != "/root/.ollama" {
		t.Fatal("model pull or readiness/persistence missing")
	}
	gpu := container.Resources.Requests["nvidia.com/gpu"]
	if gpu.Value() != 1 {
		t.Fatal("GPU request missing")
	}
	if _, _, _, err := ollamaObjects(ollamaRequest{Namespace: "default", Name: "assistant", Model: "bad;rm", CPU: "2", Memory: "4Gi", StorageGi: 20}); err == nil {
		t.Fatal("unsafe model name accepted")
	}
}
