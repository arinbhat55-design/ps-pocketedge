package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const deploymentRevisionAnnotation = "deployment.kubernetes.io/revision"

func managedDeployment(d *appsv1.Deployment) bool {
	return d.Labels["app.kubernetes.io/managed-by"] == "pspocketedge"
}

type kubernetesRevision struct {
	Revision  int64  `json:"revision"`
	Image     string `json:"image"`
	CreatedAt string `json:"createdAt"`
	Current   bool   `json:"current"`
}

func ownedRevisions(deployment *appsv1.Deployment, replicas []appsv1.ReplicaSet) ([]kubernetesRevision, map[int64]*appsv1.ReplicaSet) {
	out := []kubernetesRevision{}
	byNumber := map[int64]*appsv1.ReplicaSet{}
	current, _ := strconv.ParseInt(deployment.Annotations[deploymentRevisionAnnotation], 10, 64)
	for i := range replicas {
		rs := &replicas[i]
		owned := false
		for _, owner := range rs.OwnerReferences {
			if owner.UID == deployment.UID && owner.Kind == "Deployment" {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		version, err := strconv.ParseInt(rs.Annotations[deploymentRevisionAnnotation], 10, 64)
		if err != nil || version < 1 {
			continue
		}
		byNumber[version] = rs
		out = append(out, kubernetesRevision{Revision: version, Image: firstImage(rs.Spec.Template.Spec.Containers), CreatedAt: rs.CreationTimestamp.UTC().Format("2006-01-02T15:04:05Z"), Current: version == current})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision > out[j].Revision })
	return out, byNumber
}

func (api *kubernetesAPI) revisions(w http.ResponseWriter, r *http.Request) {
	client, _, err := api.client(r.Context(), r.PathValue("id"))
	if err != nil {
		api.error(w, err)
		return
	}
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	deployment, err := client.AppsV1().Deployments(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		api.error(w, err)
		return
	}
	if !managedDeployment(deployment) {
		http.Error(w, "only PS-pocketEdge-managed deployments have a managed history", 403)
		return
	}
	sets, err := client.AppsV1().ReplicaSets(ns).List(r.Context(), metav1.ListOptions{})
	if err != nil {
		api.error(w, err)
		return
	}
	revisions, _ := ownedRevisions(deployment, sets.Items)
	writeJSON(w, 200, revisions)
}

func (api *kubernetesAPI) rollbackWorkload(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Revision int64 `json:"revision"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil || request.Revision < 1 {
		http.Error(w, "valid revision is required", 400)
		return
	}
	client, _, err := api.client(r.Context(), r.PathValue("id"))
	if err != nil {
		api.error(w, err)
		return
	}
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	deployment, err := client.AppsV1().Deployments(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		api.error(w, err)
		return
	}
	if !managedDeployment(deployment) {
		http.Error(w, "only PS-pocketEdge-managed deployments can be rolled back", 403)
		return
	}
	sets, err := client.AppsV1().ReplicaSets(ns).List(r.Context(), metav1.ListOptions{})
	if err != nil {
		api.error(w, err)
		return
	}
	_, history := ownedRevisions(deployment, sets.Items)
	target := history[request.Revision]
	if target == nil {
		http.Error(w, "revision not found", 404)
		return
	}
	current := deployment.Annotations[deploymentRevisionAnnotation]
	if current == strconv.FormatInt(request.Revision, 10) {
		http.Error(w, "revision is already current", 409)
		return
	}
	copy := deployment.DeepCopy()
	copy.Spec.Template = *target.Spec.Template.DeepCopy()
	// Deployment's selector is immutable. Refuse a retained ReplicaSet with
	// incompatible labels instead of issuing a permanently invalid update.
	for key, value := range deployment.Spec.Selector.MatchLabels {
		if copy.Spec.Template.Labels[key] != value {
			http.Error(w, "revision selector no longer matches deployment", 409)
			return
		}
	}
	updated, err := client.AppsV1().Deployments(ns).Update(r.Context(), copy, metav1.UpdateOptions{DryRun: dryRun(r)})
	if err != nil {
		api.error(w, err)
		return
	}
	if r.URL.Query().Get("dryRun") != "true" {
		api.audit(r, "kubernetes.workload.rollback", "kubernetes_deployment", ns+"/"+name, "rolled back Kubernetes deployment to revision "+strconv.FormatInt(request.Revision, 10))
	}
	writeJSON(w, 200, map[string]any{"name": updated.Name, "namespace": updated.Namespace, "targetRevision": request.Revision, "image": firstImage(updated.Spec.Template.Spec.Containers), "dryRun": r.URL.Query().Get("dryRun") == "true"})
}

func (api *kubernetesAPI) deleteWorkload(w http.ResponseWriter, r *http.Request) {
	client, _, err := api.client(r.Context(), r.PathValue("id"))
	if err != nil {
		api.error(w, err)
		return
	}
	ns, name := r.PathValue("namespace"), r.PathValue("name")
	deployment, err := client.AppsV1().Deployments(ns).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		api.error(w, err)
		return
	}
	if !managedDeployment(deployment) {
		http.Error(w, "only PS-pocketEdge-managed deployments can be deleted", 403)
		return
	}
	policy := metav1.DeletePropagationBackground
	err = client.AppsV1().Deployments(ns).Delete(r.Context(), name, metav1.DeleteOptions{PropagationPolicy: &policy})
	if apierrors.IsNotFound(err) {
		http.Error(w, "deployment not found", 404)
		return
	}
	if err != nil {
		api.error(w, err)
		return
	}
	api.audit(r, "kubernetes.workload.delete", "kubernetes_deployment", ns+"/"+name, "deleted Kubernetes deployment")
	w.WriteHeader(http.StatusNoContent)
}
