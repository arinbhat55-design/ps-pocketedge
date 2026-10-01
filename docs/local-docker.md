# Manage local Docker without Docker Desktop

PSpocketEdge manages a Docker Engine through its agent. The dashboard alone cannot run containers: a Docker daemon must be running on the machine you want to manage. The agent connects outbound to the control plane's gRPC address, normally port 8443.

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
