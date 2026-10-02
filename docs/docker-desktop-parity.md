# Docker Desktop parity work

PS-pocketEdge manages Docker and Podman containers. Docker Engine or a Podman API service is required on every machine where containers run. See [runtime setup](local-docker.md).

| Priority | Capability | Current state |
| --- | --- | --- |
| High | Local use without Docker Desktop | Linux Engine supported; macOS agent release and Colima installer added; Windows WSL2 setup documented. Podman selection and API support added for Linux, macOS, and WSL2; runtime setup and Podman parity still need on-device validation. |
| High | Deploy from repository | Dockerfile wizard creates a Git-linked Compose file, checks the Dockerfile, and deploys to the chosen server. |
| High | Build experience | Build state, live logs, cancel, copy log, follow output, Buildx when present, and manual registry push. |
| High | Automatic registry publishing | Pending: per-deployment destination and publish step after successful build. |
| High | Build secrets | Pending: secure secret references in Compose and Buildx, with no secret material in logs or revisions. |
| High | Multi-platform images | Pending: Buildx push workflow and registry-backed pull for target servers. |
| Medium | Volume file management | Browse, upload, download, edit, and clone implemented. Individual file transfers are capped at 1 MiB; whole-volume export/import remains pending. |
| Medium | Local Kubernetes provisioning | Implemented for a kind cluster on the control plane host, with create, connection verification, and delete. Requires kind and Docker on that host; on-device validation remains. |
| Low | Extension system | Pending. |
| Low | Model and MCP management | Pending. |

The user moved medium-priority work ahead of the remaining high-priority items. See [local-docker.md](local-docker.md), [local-kubernetes.md](local-kubernetes.md), [volume-files.md](volume-files.md), and [image-builds.md](image-builds.md) for current setup and behavior.
