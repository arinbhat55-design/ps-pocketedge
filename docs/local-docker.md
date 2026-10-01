# Manage local Docker without Docker Desktop

PSpocketEdge manages a Docker Engine through its agent. The dashboard alone cannot run containers: a Docker daemon must be running on the machine you want to manage. The agent connects outbound to the control plane's gRPC address, normally port 8443.

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
