# PS-pocketEdge

Manage containers, servers, deployments, and databases across your machines from one dashboard — without Docker Desktop.

PS-pocketEdge has three parts:

| Part | Binary / folder | What it does |
| --- | --- | --- |
| **Control plane** | `pe-controlplane` (`cmd/controlplane`) | REST API (port 8080) for the app and CLI, gRPC service (port 8443) for agents, backed by PostgreSQL. |
| **Agent** | `pe-agent` (`cmd/agent`) | Runs on each machine with Docker Engine or Podman. Connects *outbound* to the control plane, so the Docker socket is never exposed. |
| **Clients** | Flutter app (`app/`), CLI `pse` (`cmd/pse`) | Desktop and web dashboard, and a terminal client that uses the same accounts and permissions. |

## Features

- **Containers, images, networks, volumes** on Docker Engine or Podman (Linux, macOS via Colima, Windows via WSL2)
- **Compose deployments** with versioned Compose files, environment variable groups, and approval workflow
- **Deploy from a Git repository**, including Dockerfile builds on the target server (BuildKit when available)
- **Database marketplace** with credential vault, monitoring, logical backups, restore, and cloning
- **Kubernetes**: connect existing clusters or create a local [kind](https://kind.sigs.k8s.io/) cluster
- **Volume file browser**: browse, download, edit, upload, and clone volumes
- **Roles and audit log**: Administrator and Viewer roles enforced by the API
- **macOS installer** that sets up everything without Docker Desktop

## Quick start (development)

Requirements: Go 1.26+, Docker (or Colima) for the dev database, [golang-migrate](https://github.com/golang-migrate/migrate), and Flutter for the app.

```sh
# 1. Configuration
cp .env.example .env            # fill in values; see comments in the file
set -a; source .env; set +a

# 2. Database (PostgreSQL 16 on localhost:55432)
make dev                         # leave running; use a second terminal below
make migrate

# 3. Build and start the control plane
make build
./bin/pe-controlplane --database-url="$DATABASE_URL"

# 4. Start the dashboard
cd app
flutter run -d chrome --web-port 8090    # or: flutter run -d macos
```

On first start, an admin user is created from `ADMIN_EMAIL`. If `ADMIN_PASSWORD` is empty, a password is generated and printed in the control plane log. When opened on the same computer, the dashboard can also sign you in automatically with a local admin session (see [local Docker docs](docs/local-docker.md#dashboard-access)).

### Add a server

In the dashboard, open **Add server**, choose the operating system, and run the generated command on that machine. It installs `pe-agent` and enrolls it with a one-time token. See [Manage local Docker and Podman](docs/local-docker.md) for Linux, macOS, Windows, and Podman setup.

## Make targets

| Command | Description |
| --- | --- |
| `make build` | Build `pe-agent`, `pe-controlplane`, and `pse` into `bin/` |
| `make test` / `make vet` | Run Go tests / `go vet` |
| `make dev` | Start the development PostgreSQL with Docker Compose |
| `make migrate` | Apply database migrations from `migrations/` |
| `make proto` | Regenerate gRPC code from `proto/` |
| `make release` | Build a snapshot release with GoReleaser |
| `make package-macos-setup` | Build the guided macOS installer DMG |

## Documentation

- [CLI (`pse`)](docs/cli.md)
- [Manage local Docker and Podman](docs/local-docker.md)
- [Cloud connectivity and TLS](docs/cloud-connectivity.md)
- [Git image builds](docs/image-builds.md)
- [Kubernetes integration](docs/kubernetes.md) · [Local Kubernetes with kind](docs/local-kubernetes.md)
- [Volume files](docs/volume-files.md)
- [Guided macOS setup](docs/guided-macos-setup.md) · [macOS client installer](docs/macos-installer.md)
- [Docker Desktop parity roadmap](docs/docker-desktop-parity.md)

## Repository layout

```
app/          Flutter dashboard (desktop and web)
cmd/          Entry points: agent, controlplane, pse (CLI), setup (macOS installer helper)
internal/     Go packages for agent, control plane, CLI, setup, and shared code
proto/        gRPC definitions between agent and control plane
migrations/   PostgreSQL schema migrations
deploy/       Dev Docker Compose file and agent systemd unit
installer/    macOS installer app
scripts/      Agent install and macOS packaging scripts
docs/         Feature and setup documentation
```

## License

Licensed under the [Apache License 2.0](LICENSE).
