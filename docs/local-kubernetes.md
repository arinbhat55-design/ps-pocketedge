# Local Kubernetes with kind

PSpocketEdge can create a local Kubernetes cluster on the **control plane host**. Install [kind](https://kind.sigs.k8s.io/docs/user/quick-start/) and a working Docker CLI/Engine there first. If the control plane runs in a container, that container also needs access to the Docker daemon and the kind binary, and it must be able to reach the kind API server through its loopback address.

In **Kubernetes → Add cluster → Create local cluster**, enter a display name. PSpocketEdge creates a uniquely named kind cluster, waits for it to become ready, checks that its API is reachable, and stores its kubeconfig encrypted in the existing Kubernetes cluster store. Creation can take several minutes and may download kind's node image. A failed creation attempts to remove its kind cluster.

Use **Delete local cluster** to remove the kind containers, their data, and the saved connection. This deletes workloads inside the cluster. Connected external clusters retain their existing **Disconnect** action, which only removes the saved connection.

The local cluster is on the control plane host, not on a remote PSpocketEdge agent. For a remote or pre-existing Kubernetes cluster, use **Connect existing cluster** with a self-contained kubeconfig.
