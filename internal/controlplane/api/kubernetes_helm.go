package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type helmRESTGetter struct {
	config *rest.Config
	raw    clientcmdapi.Config
}

var _ genericclioptions.RESTClientGetter = (*helmRESTGetter)(nil)

func (g *helmRESTGetter) ToRESTConfig() (*rest.Config, error) { return rest.CopyConfig(g.config), nil }
func (g *helmRESTGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	client, err := discovery.NewDiscoveryClientForConfig(g.config)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(client), nil
}
func (g *helmRESTGetter) ToRESTMapper() (meta.RESTMapper, error) {
	client, err := g.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(client), nil
}
func (g *helmRESTGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return clientcmd.NewDefaultClientConfig(g.raw, &clientcmd.ConfigOverrides{})
}

func (api *kubernetesAPI) helmConfig(ctx context.Context, clusterID, namespace string) (*action.Configuration, error) {
	_, sealed, err := api.st.GetKubernetesClusterConfig(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	raw, err := api.vault.OpenValue(sealed)
	if err != nil {
		return nil, err
	}
	config, err := clientcmd.RESTConfigFromKubeConfig([]byte(raw))
	if err != nil {
		return nil, err
	}
	config.Timeout = 5 * time.Minute
	parsed, err := clientcmd.Load([]byte(raw))
	if err != nil {
		return nil, err
	}
	getter := &helmRESTGetter{config: config, raw: *parsed}
	client := new(action.Configuration)
	if err := client.Init(getter, namespace, "secrets", func(format string, args ...interface{}) { api.log.Debug(fmt.Sprintf(format, args...)) }); err != nil {
		return nil, err
	}
	return client, nil
}

type helmChartRequest struct {
	Namespace   string                 `json:"namespace"`
	Name        string                 `json:"name"`
	ChartBase64 string                 `json:"chartBase64"`
	Values      map[string]interface{} `json:"values"`
}

func validateHelmTarget(namespace, name string) error {
	if len(validation.IsDNS1123Label(namespace)) > 0 || len(validation.IsDNS1123Label(name)) > 0 {
		return errors.New("namespace and release name must be Kubernetes DNS labels")
	}
	return nil
}

func helmReleaseSummary(name, namespace, chartName, chartVersion, status string, revision int) map[string]any {
	return map[string]any{"name": name, "namespace": namespace, "chart": chartName, "chartVersion": chartVersion, "status": status, "revision": revision}
}

func (api *kubernetesAPI) listHelm(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}
	if len(validation.IsDNS1123Label(namespace)) > 0 {
		http.Error(w, "invalid namespace", 400)
		return
	}
	config, err := api.helmConfig(r.Context(), r.PathValue("id"), namespace)
	if err != nil {
		api.error(w, err)
		return
	}
	list := action.NewList(config)
	list.All = true
	list.Limit = 200
	releases, err := list.Run()
	if err != nil {
		api.error(w, err)
		return
	}
	out := make([]map[string]any, 0, len(releases))
	for _, release := range releases {
		chartName, chartVersion := "", ""
		if release.Chart != nil && release.Chart.Metadata != nil {
			chartName, chartVersion = release.Chart.Metadata.Name, release.Chart.Metadata.Version
		}
		out = append(out, helmReleaseSummary(release.Name, release.Namespace, chartName, chartVersion, string(release.Info.Status), release.Version))
	}
	writeJSON(w, 200, out)
}

func (api *kubernetesAPI) helmInstallOrUpgrade(w http.ResponseWriter, r *http.Request) {
	var req helmChartRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid or oversized chart request", 400)
		return
	}
	if err := validateHelmTarget(req.Namespace, req.Name); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if len(req.ChartBase64) > 11<<20 {
		http.Error(w, "chart archive is too large", 400)
		return
	}
	archive, err := base64.StdEncoding.DecodeString(req.ChartBase64)
	if err != nil || len(archive) == 0 || len(archive) > 8<<20 {
		http.Error(w, "invalid or oversized chart archive", 400)
		return
	}
	chart, err := loader.LoadArchive(bytes.NewReader(archive))
	if err != nil {
		http.Error(w, "invalid Helm chart archive", 400)
		return
	}
	if chart.Metadata == nil || chart.Metadata.Name == "" {
		http.Error(w, "chart metadata is required", 400)
		return
	}
	config, err := api.helmConfig(r.Context(), r.PathValue("id"), req.Namespace)
	if err != nil {
		api.error(w, err)
		return
	}
	_, priorErr := config.Releases.Last(req.Name)
	dry := r.URL.Query().Get("dryRun") == "true"
	values := req.Values
	if values == nil {
		values = map[string]interface{}{}
	}
	var revision int
	if errors.Is(priorErr, driver.ErrReleaseNotFound) {
		install := action.NewInstall(config)
		install.ReleaseName = req.Name
		install.Namespace = req.Namespace
		install.DryRun = dry
		if dry {
			install.DryRunOption = "server"
			install.HideSecret = true
		}
		install.Atomic = !dry
		install.Wait = !dry
		install.Timeout = 5 * time.Minute
		release, err := install.RunWithContext(r.Context(), chart, values)
		if err != nil {
			api.error(w, err)
			return
		}
		revision = release.Version
	} else if priorErr == nil {
		upgrade := action.NewUpgrade(config)
		upgrade.Namespace = req.Namespace
		upgrade.DryRun = dry
		if dry {
			upgrade.DryRunOption = "server"
			upgrade.HideSecret = true
		}
		upgrade.Atomic = !dry
		upgrade.Wait = !dry
		upgrade.Timeout = 5 * time.Minute
		release, err := upgrade.RunWithContext(r.Context(), req.Name, chart, values)
		if err != nil {
			api.error(w, err)
			return
		}
		revision = release.Version
	} else {
		api.error(w, priorErr)
		return
	}
	if !dry {
		api.audit(r, "kubernetes.helm.apply", "helm_release", req.Namespace+"/"+req.Name, "applied Helm release "+req.Name)
	}
	writeJSON(w, 200, helmReleaseSummary(req.Name, req.Namespace, chart.Metadata.Name, chart.Metadata.Version, map[bool]string{true: "dry-run", false: "applied"}[dry], revision))
}

func (api *kubernetesAPI) helmRollback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Revision int `json:"revision"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil || req.Revision < 1 {
		http.Error(w, "valid revision is required", 400)
		return
	}
	if err := validateHelmTarget(r.PathValue("namespace"), r.PathValue("name")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	config, err := api.helmConfig(r.Context(), r.PathValue("id"), r.PathValue("namespace"))
	if err != nil {
		api.error(w, err)
		return
	}
	rollback := action.NewRollback(config)
	rollback.Version = req.Revision
	rollback.Wait = true
	rollback.Timeout = 5 * time.Minute
	rollback.CleanupOnFail = true
	if err := rollback.Run(r.PathValue("name")); err != nil {
		api.error(w, err)
		return
	}
	api.audit(r, "kubernetes.helm.rollback", "helm_release", r.PathValue("namespace")+"/"+r.PathValue("name"), "rolled back Helm release")
	w.WriteHeader(http.StatusNoContent)
}

func (api *kubernetesAPI) helmUninstall(w http.ResponseWriter, r *http.Request) {
	if err := validateHelmTarget(r.PathValue("namespace"), r.PathValue("name")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	config, err := api.helmConfig(r.Context(), r.PathValue("id"), r.PathValue("namespace"))
	if err != nil {
		api.error(w, err)
		return
	}
	uninstall := action.NewUninstall(config)
	uninstall.Wait = true
	uninstall.Timeout = 5 * time.Minute
	if _, err := uninstall.Run(r.PathValue("name")); err != nil {
		api.error(w, err)
		return
	}
	api.audit(r, "kubernetes.helm.uninstall", "helm_release", r.PathValue("namespace")+"/"+r.PathValue("name"), "uninstalled Helm release")
	w.WriteHeader(http.StatusNoContent)
}

func (api *kubernetesAPI) helmHistory(w http.ResponseWriter, r *http.Request) {
	if err := validateHelmTarget(r.PathValue("namespace"), r.PathValue("name")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	config, err := api.helmConfig(r.Context(), r.PathValue("id"), r.PathValue("namespace"))
	if err != nil {
		api.error(w, err)
		return
	}
	history := action.NewHistory(config)
	history.Max = 30
	releases, err := history.Run(r.PathValue("name"))
	if err != nil {
		api.error(w, err)
		return
	}
	out := make([]map[string]any, 0, len(releases))
	for _, release := range releases {
		chartName, chartVersion := "", ""
		if release.Chart != nil && release.Chart.Metadata != nil {
			chartName, chartVersion = release.Chart.Metadata.Name, release.Chart.Metadata.Version
		}
		out = append(out, helmReleaseSummary(release.Name, release.Namespace, chartName, chartVersion, string(release.Info.Status), release.Version))
	}
	writeJSON(w, 200, out)
}

// Helm chart archives are uploaded by the administrator. The control plane
// never resolves a URL or executes a local Helm binary on behalf of a user.
