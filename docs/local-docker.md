# Manage local Docker and Podman

PS-pocketEdge manages Docker Engine or Podman through its agent. The dashboard alone cannot run containers: a container runtime API must be available on the machine you want to manage. The agent connects outbound to the control plane's gRPC address, normally port 8443.

## Dashboard access

When the control plane REST API listens on `127.0.0.1`, `::1`, or `localhost` (the default), the dashboard obtains a local administrator session automatically. You can view, start, stop, create, and edit containers without an app login, like using Docker Desktop on your own machine. If the dashboard was opened before the control plane started, choose **Use this computer without signing in** on the login screen.

A local session manages every connected server, not only this computer, so it is granted only to requests that:

- come from loopback and are addressed to a loopback host;
- were not relayed by a reverse proxy or tunnel (requests carrying `Forwarded`, `X-Forwarded-*`, `X-Real-IP`, `Via`, or similar headers are refused);
- in a browser, come from an allowed dashboard origin. Other web pages running on localhost cannot obtain a session.

The native desktop app needs no configuration. The web dashboard must run on an allowed origin, by default `http://localhost:8090` or `http://127.0.0.1:8090`:

```sh
flutter run -d chrome --web-port 8090
```

Set `DASHBOARD_ORIGINS` (or `-dashboard-origins`) to a comma-separated list of loopback origins to use other ports. When a local session is refused, the login screen and the control plane log say why.

Local session tokens are accepted only on requests that pass the same checks, so a copied token does not work from another machine. Audit entries and deployment events made in a local session show the administrator account followed by `(local session)`.

An administrator can turn on **Settings → Require login on this computer** after confirming their password. This revokes automatic local sessions immediately; the setting persists across restarts. If the password is unknown, reset it under **Users** before enabling login. Turn it on if other people use this computer.

When the REST API listens on a network address, local sessions are disabled and the existing app login is required. The local dashboard still needs a running agent and Docker Engine to manage containers.

## Roles

Administrators can manage users, settings, servers, containers, images, networks, volumes, Compose files, deployments, databases, and Kubernetes resources. Viewers can inspect inventories, status, metrics, and logs. They cannot create, change, delete, start, stop, pause, execute commands, reveal database credentials, or read volume file contents. Configuration sources and environment variable groups are restricted to administrators; container environment variable values and deployment environment values are hidden from viewers. The API enforces these permissions; the dashboard also hides unavailable actions. A viewer can change their own password.

An administrator assigns roles under **Users**. New users default to **Viewer**. Role changes and account deletion take effect on the next API request, including requests made with an existing token. When local login is off, the automatic local session is an administrator session, so anyone with access to that computer's dashboard can manage resources. Enable **Settings → Require login on this computer** if people sharing the computer need separate roles.

## Linux

Install [Docker Engine](https://docs.docker.com/engine/install/) and confirm `sudo docker info` works. In **Add server**, select Linux, replace `<control-plane-host>` with the control plane's reachable gRPC host, and run the shown command. The installer creates a systemd service. Add `--allow-builds` to opt into building Dockerfiles on this machine.

## macOS

Install the Docker CLI and [Colima](https://github.com/abiosoft/colima), for example with Homebrew:

```sh
brew install docker colima docker-buildx
colima start
docker info
```

In **Add server**, select macOS and run the shown command in Terminal as your normal user. The installer checks that Docker is reachable, installs the macOS agent, and registers it in your user launchd session. It sets `DOCKER_HOST` to Colima's default socket so the agent and Buildx use the same Engine. If you use a different Docker-compatible runtime, set `DOCKER_HOST` to its socket before running the installer. Add `--allow-builds` if this Mac should build repository Dockerfiles.

Colima and the user agent need to be running after login. Start Colima before relying on the agent; if Colima is stopped, container and build operations cannot work. Agent logs are in `~/Library/Application Support/PSpocketEdge/agent.log`.

## Windows

Install WSL2 with an Ubuntu distribution, [enable systemd in WSL](https://learn.microsoft.com/en-us/windows/wsl/systemd), then install Docker Engine **inside that distribution**. Confirm `sudo docker info` works inside WSL. In **Add server**, select Windows and run its Linux installer command inside the Ubuntu shell. The agent manages the Docker Engine in that WSL distribution; Windows containers are not supported by this path.

## Connections and security

The generated enrollment token works once and expires after one hour. The quick-start command puts it in shell history; for a shared machine, pass it in `PE_ENROLL_TOKEN` instead of `--token`. The agent needs to reach the control plane's gRPC port. `--allow-builds` grants repository Dockerfiles access to that machine's Docker daemon, so use it only on machines where trusted admins may build.

The current installers download `pe-agent` from a GitHub release. For testing a locally compiled agent, replace the download with `--local-binary=/path/to/pe-agent`.

## Podman

Select **Podman** in **Add server**. The generated installer command includes
`--runtime=podman`; Docker remains the default for existing agents.
Podman uses its [Docker-compatible API](https://docs.podman.io/en/latest/markdown/podman-system-service.1.html),
so container, image, network, volume, and Compose operations share the agent's
existing API path. This is compatibility support, not Podman-native pod management.

On Linux, install Podman before running the generated command. The installer
runs as root and enables `podman.socket` at `/run/podman/podman.sock`; it manages
rootful containers. The Windows command follows the same path inside WSL2
with systemd enabled.

On macOS, install Podman and create/start its machine:

```sh
brew install podman
podman machine init
podman machine start
```

Run the macOS installer as your normal user with `--runtime=podman`. It discovers
the forwarded API socket using `podman machine inspect`, checks that the socket
responds, and persists it in launchd. It requires neither Docker CLI nor Colima.
For a different machine/socket, pass `--container-host=unix:///path/to/podman.sock`.
Keep the Podman machine running; if its socket path changes, rerun the installer
with the new path.

To manage **rootless** Linux containers, run the agent as the same user as Podman,
rather than using the rootful installer:

```sh
systemctl --user enable --now podman.socket
pe-agent --runtime=podman --server=<control-plane-host>:8443 --token=<token> \
  --state-path="$HOME/.local/share/pspocketedge/state.json"
```

The agent uses `$XDG_RUNTIME_DIR/podman/podman.sock` (or
`/run/user/<uid>/podman/podman.sock`). Set up a user service if the agent should
persist across logins. Rootless networking, resource limits, and privileged
operations follow Podman's host permissions.

Agent YAML also supports `container_runtime: podman` and
`container_host: unix:///path/to/podman.sock`. The `--runtime` and
`--container-host` flags override those fields. When no explicit host is set,
`DOCKER_HOST` takes precedence over the runtime's default socket.

Add `--allow-builds` to enable Git image builds through Podman's API. Docker
Buildx is skipped for Podman. Docker-specific build extensions and API behavior
may differ; full on-device parity still needs validation against your Podman version.

## Bottom resource bar

The dashboard's bottom bar shows the selected agent host's CPU usage, RAM used
and total RAM, and disk space used and total capacity. The server menu switches
which host is measured; values refresh every ten seconds and remain visible
while navigating detail pages. The status strip is a single 32-pixel row. On phones, its metrics scroll
horizontally above the navigation bar.

Disk capacity is the host's root filesystem (`/`), rather than a per-container
storage quota. RAM and disk byte usage require an updated agent and control
plane. Older agents continue to show their reported usage percentages; unknown
capacity displays as a dash. Offline or unavailable readings are marked as last
reported.

**Terminal** opens an interactive shell on the selected agent host. It runs as
that agent's OS user (root for the Linux system installer; your user for the
macOS installer or a rootless Linux agent). Administrators have access; viewers
and disconnected hosts cannot open it. The agent and control plane must be
updated to support host terminals. Docker/Podman and running containers are not
required for host shells. Leaving the terminal closes the session and terminates
its active commands. The terminal uses a plain command input and scrollback view;
full-screen terminal applications are not supported.
