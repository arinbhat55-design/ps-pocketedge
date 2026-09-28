package api

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// schedulePreview estimates placement using the same node allocatable and
// existing Pod requests that the scheduler sees. It is intentionally a
// preflight estimate: admission webhooks, affinity, topology, and concurrent
// changes remain the Kubernetes scheduler's decision.
type schedulePreview struct {
	Fits       bool           `json:"fits"`
	Placements map[string]int `json:"placements"`
	Reasons    []string       `json:"reasons"`
}

func podRequests(pod corev1.Pod) corev1.ResourceList {
	requests := corev1.ResourceList{}
	for _, container := range pod.Spec.Containers {
		addResources(requests, container.Resources.Requests)
	}
	// Init containers run one at a time, so their maximum request competes
	// with the total request of regular containers.
	for _, container := range pod.Spec.InitContainers {
		for name, want := range container.Resources.Requests {
			if have, ok := requests[name]; !ok || want.Cmp(have) > 0 {
				requests[name] = want.DeepCopy()
			}
		}
	}
	addResources(requests, pod.Spec.Overhead)
	return requests
}

func addResources(dst, src corev1.ResourceList) {
	for name, quantity := range src {
		if existing, ok := dst[name]; ok {
			existing.Add(quantity)
			dst[name] = existing
		} else {
			dst[name] = quantity.DeepCopy()
		}
	}
}

func subtractResources(dst, src corev1.ResourceList) {
	for name, quantity := range src {
		if existing, ok := dst[name]; ok {
			existing.Sub(quantity)
			dst[name] = existing
		}
	}
}

func fitsResources(available, requested corev1.ResourceList) bool {
	for name, want := range requested {
		have, ok := available[name]
		if !ok || have.Cmp(want) < 0 {
			return false
		}
	}
	return true
}

func toleratesTaints(pod corev1.PodSpec, node corev1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		matched := false
		for _, tolerance := range pod.Tolerations {
			if tolerance.Effect != "" && tolerance.Effect != taint.Effect {
				continue
			}
			if tolerance.Operator == corev1.TolerationOpExists {
				if tolerance.Key == "" || tolerance.Key == taint.Key {
					matched = true
					break
				}
			} else if tolerance.Key == taint.Key && tolerance.Value == taint.Value {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func eligibleNode(pod corev1.PodSpec, node corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	ready := false
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			ready = true
			break
		}
	}
	if !ready || !toleratesTaints(pod, node) {
		return false
	}
	if pod.NodeName != "" && pod.NodeName != node.Name {
		return false
	}
	for key, value := range pod.NodeSelector {
		if node.Labels[key] != value {
			return false
		}
	}
	return true
}

func estimateSchedule(nodes []corev1.Node, pods []corev1.Pod, template corev1.PodSpec, replicas int32) schedulePreview {
	result := schedulePreview{Placements: map[string]int{}, Reasons: []string{}}
	remaining := map[string]corev1.ResourceList{}
	for _, node := range nodes {
		if !eligibleNode(template, node) {
			continue
		}
		capacity := corev1.ResourceList{}
		addResources(capacity, node.Status.Allocatable)
		remaining[node.Name] = capacity
	}
	if len(remaining) == 0 {
		result.Reasons = append(result.Reasons, "no ready, schedulable node matches the workload's node selectors and taints")
		return result
	}
	for _, pod := range pods {
		if pod.Spec.NodeName == "" || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		if capacity, ok := remaining[pod.Spec.NodeName]; ok {
			subtractResources(capacity, podRequests(pod))
		}
	}
	want := podRequests(corev1.Pod{Spec: template})
	for replica := int32(0); replica < replicas; replica++ {
		chosen := ""
		for _, node := range nodes {
			capacity, ok := remaining[node.Name]
			if ok && fitsResources(capacity, want) {
				chosen = node.Name
				break
			}
		}
		if chosen == "" {
			result.Reasons = append(result.Reasons, fmt.Sprintf("only %d of %d replicas fit current node requests and allocatable resources", replica, replicas))
			return result
		}
		subtractResources(remaining[chosen], want)
		result.Placements[chosen]++
	}
	result.Fits = true
	result.Reasons = append(result.Reasons, "estimate excludes admission changes, affinity, topology, and concurrent scheduling")
	return result
}

// estimateUpdate previews a change to an existing deployment. Its own Pods
// are being replaced, so they don't count against the steady-state estimate.
// When the Pod template changes and the rollout cannot take a replica down
// first (maxUnavailable rounds to 0), one surge Pod must also fit beside the
// current Pods, or the rollout would stall.
func estimateUpdate(nodes []corev1.Node, pods []corev1.Pod, existing, updated *appsv1.Deployment, replicas int32) schedulePreview {
	others := pods
	if selector, err := metav1.LabelSelectorAsSelector(existing.Spec.Selector); err == nil {
		others = make([]corev1.Pod, 0, len(pods))
		for _, pod := range pods {
			if pod.Namespace == existing.Namespace && selector.Matches(labels.Set(pod.Labels)) {
				continue
			}
			others = append(others, pod)
		}
	}
	preview := estimateSchedule(nodes, others, updated.Spec.Template.Spec, replicas)
	if !preview.Fits || apiequality.Semantic.DeepEqual(existing.Spec.Template, updated.Spec.Template) || canReplaceInPlace(existing, replicas) {
		return preview
	}
	if !estimateSchedule(nodes, pods, updated.Spec.Template.Spec, 1).Fits {
		preview.Fits = false
		preview.Reasons = append([]string{"no room for a rolling-update surge Pod beside the current Pods; free capacity, raise the deployment's maxUnavailable, or use the Recreate strategy"}, preview.Reasons...)
	}
	return preview
}

// canReplaceInPlace reports whether a rollout may stop an old Pod before its
// replacement is scheduled, using Kubernetes' defaults (RollingUpdate, 25%).
func canReplaceInPlace(d *appsv1.Deployment, replicas int32) bool {
	if d.Spec.Strategy.Type == appsv1.RecreateDeploymentStrategyType {
		return true
	}
	maxUnavailable := intstr.FromString("25%")
	if ru := d.Spec.Strategy.RollingUpdate; ru != nil && ru.MaxUnavailable != nil {
		maxUnavailable = *ru.MaxUnavailable
	}
	n, err := intstr.GetScaledValueFromIntOrPercent(&maxUnavailable, int(replicas), false)
	return err == nil && n > 0
}

func quotaCheck(quotas []corev1.ResourceQuota, requested corev1.ResourceList, replicas int32) []string {
	warnings := []string{}
	for _, quota := range quotas {
		for _, check := range []struct {
			quotaName    corev1.ResourceName
			resourceName corev1.ResourceName
		}{
			{corev1.ResourceRequestsCPU, corev1.ResourceCPU},
			{corev1.ResourceRequestsMemory, corev1.ResourceMemory},
			{corev1.ResourceLimitsCPU, corev1.ResourceCPU},
			{corev1.ResourceLimitsMemory, corev1.ResourceMemory},
		} {
			hard, ok := quota.Status.Hard[check.quotaName]
			if !ok {
				continue
			}
			used := quota.Status.Used[check.quotaName]
			want, ok := requested[check.resourceName]
			if !ok {
				continue
			}
			want = want.DeepCopy()
			want.Mul(int64(replicas))
			used.Add(want)
			if used.Cmp(hard) > 0 {
				warnings = append(warnings, fmt.Sprintf("quota %s: %s would exceed %s", quota.Name, check.quotaName, hard.String()))
			}
		}
	}
	return warnings
}

func gpuResources(resources corev1.ResourceList) []string {
	out := []string{}
	for name, q := range resources {
		if strings.Contains(string(name), "gpu") && q.Cmp(resource.MustParse("0")) > 0 {
			out = append(out, string(name))
		}
	}
	return out
}
