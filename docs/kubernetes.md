# Kubernetes integration

PS-pocketEdge connects directly to a standard Kubernetes API server. The
Kubernetes module is separate from Docker agents and Compose deployments.
Apply migration `0025_kubernetes_clusters.up.sql` before starting the updated
control plane.

## Connect a cluster

In **Kubernetes → Connect cluster**, paste a self-contained kubeconfig with a
current context. Its API server must use HTTPS. Inline bearer tokens or inline
client certificate/key data are supported. File paths, `exec` credential
plugins, auth-provider plugins, impersonation, and insecure TLS are rejected:
those would read files or run programs on the control-plane host. The
kubeconfig is encrypted with the existing vault key and is never returned by
the API. Back up the vault key along with the database.

The control plane must be able to reach the Kubernetes API server. The
connection step calls API discovery to verify the credential before saving.

## Kubernetes permissions

The connected credential needs these read permissions for the current UI:

| API group | Resources | Verbs |
| --- | --- | --- |
| core | `namespaces`, `nodes`, `pods`, `events` | `get`, `list` |
| core | `resourcequotas` | `list` |
| core | `pods/log` | `get` |
| apps | `deployments`, `replicasets` | `get`, `list` |
| metrics.k8s.io (optional) | `nodes` | `list` |

For workload management, also grant `create`, `update`, and `delete` on
`apps/deployments`. The Ollama template additionally needs `create` and
`delete` on core `persistentvolumeclaims` and `services`. Helm needs access to
the objects in each chart and `get`, `list`, `create`, `update`, and `delete`
on core `secrets` for release storage. Bind namespace-scoped permissions where
possible; the all-namespace overview and placement estimate need cluster-wide
list permissions. PS-pocketEdge restricts cluster connection and changes to
its admins. A read-only installation can use a read-only credential.

## Workload behavior

The Deploy form creates a Kubernetes Deployment with CPU and memory requests
and limits. It can request a GPU resource advertised by a device plugin. Its
preflight estimates placement of all requested replicas using node allocatable
resources, existing Pod requests, node selectors, and taints. It checks
namespace CPU and memory ResourceQuota on new workloads. Kubernetes then
validates the create or update with server-side dry-run. The review dialog
shows estimated placement. Kubernetes remains the scheduling authority: the
estimate does not model affinity, topology spread, volume placement, admission
changes, or concurrent changes. Updates use a conservative estimate that
reserves capacity for all requested replicas in addition to current Pods.

Only Deployments created by PS-pocketEdge can be updated, rolled back, or
deleted from the UI. Revision history comes from retained ReplicaSets;
rollback is available only while the desired revision remains in Kubernetes.
Deleting a Deployment removes its Pods, while separately created storage is
retained.

## AI model template

**Deploy Ollama** creates one Deployment, ClusterIP Service, and persistent
volume claim. The container pulls the selected model into persistent storage
on startup; readiness succeeds after the model is available. The form accepts
CPU, memory, optional GPU resource, and storage size. The review step checks
placement, CPU and memory quota, and Kubernetes admission through dry-run.
The Service is reachable inside the cluster at the address shown after
deployment. A StorageClass capable of provisioning a ReadWriteOnce volume is
required. Model download size and memory use depend on the selected model and
are not estimated yet. Deleting the Deployment retains the Service and volume
claim, which an administrator can manage directly in Kubernetes.

## Helm releases

Open **Helm** from the cluster page to list releases by namespace, upload a
`.tgz` chart up to 8 MB, and enter values as JSON. The control plane uses the
Helm Go SDK with the stored kubeconfig; no local `helm` executable is needed.
It performs a server dry-run before presenting the review dialog. Existing
release names are upgraded. Release history, rollback, and uninstall are
available in the release menu. Chart templates execute with the connected
Kubernetes credential, so administrators should review charts and grant that
credential only the permissions it needs. Helm operations can take up to five
minutes. Helm itself may retain persistent volumes or resources annotated to
be kept when a release is uninstalled.

The overview shows the first 500 objects of each kind, with a notice when a
list is truncated. Node usage appears when the cluster exposes the optional
Metrics API. Pod logs show the most recent 200 lines, capped at 1 MB.
