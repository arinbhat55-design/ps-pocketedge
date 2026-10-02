# Guided macOS setup

Build the installer on macOS with Go, Flutter, and Xcode:

```sh
bash scripts/package-macos-setup.sh
```

The output is `dist/PS-pocketEdge-Setup-<version>-<build>-macos-universal.dmg`
plus a SHA-256 checksum. The disk image contains **PS-pocketEdge Setup.app** and
installation notes. The Setup app contains universal Intel/Apple Silicon helper,
control-plane, and agent binaries, and the release Flutter app. It does not need
Go, Flutter, Xcode, or Python on the user's computer. The setup wizard requires
macOS 14 or newer. Missing runtime/database packages require internet access.

## Wizard

1. Choose **Quick install** or **Custom install**, and local setup or an existing
   server. Quick setup uses reviewed defaults and detects a running Colima
   socket or restores a previous setup's choices.
2. Custom local setup asks for Docker Engine through Colima, Podman, or an
   existing compatible runtime socket. Docker Desktop is never installed.
3. Choose a dedicated PostgreSQL 16 installation or provide an existing
   PostgreSQL 16+ database URL. Use a dedicated app database and a role with
   schema migration permissions. If that app database disables automatic local
   access, provide an existing app administrator email and password so setup can
   enroll the agent without changing the access policy.
4. Review application/data paths, resource limits, Homebrew installation
   permission, and start-at-login behavior. Data folders must be inside the
   current user's home, empty or owned by a previous setup. Quick defaults:

   | Setting | Default |
   |---|---|
   | App | `/Applications/PS-pocketEdge.app` |
   | Data | `~/Library/Application Support/PSpocketEdge/LocalSetup` |
   | Runtime | Dedicated Colima profile `pspocketedge`, or running existing Colima |
   | VM resources | 2 CPUs, 4 GB RAM, 60 GB disk limit, adjusted down for small hosts |
   | Database | Dedicated PostgreSQL 16 cluster, `127.0.0.1:55433` |
   | Control plane | HTTP `127.0.0.1:8080`, gRPC `127.0.0.1:8443` |
   | Startup | Per-user services at login |

5. Click **Install**. Setup checks signatures, macOS, free space, CPU/RAM limits,
   occupied ports, connectivity, and selected dependencies before provisioning.
   Local setup requires 8 GB free space; existing-server mode requires 512 MB.
   Growing databases, container images, and VM use need additional space.
6. Review the checklist: Installed, Already available, Skipped, Failed, or Passed,
   with reported versions and locations. **Finish and open app** is available
   only after every mandatory step and connection check succeeds.

Declining a required runtime, database, or missing Homebrew prompts **Go back**
or **Exit setup**. An existing verified component satisfies that requirement.
Existing-server mode installs only the client; local dependencies are skipped.
The selected server must provide the `/api/health` readiness endpoint introduced
with this installer. Upgrade older control planes before using this mode.

Quit PS-pocketEdge before installing/upgrading. Setup opens the installed app
with its selected API URL and the native app saves it in sandboxed preferences
for subsequent launches. The native setup helper never runs inside the
dashboard's sandbox.

## Permissions and existing installations

Homebrew installs missing formulae and verifies the resulting runtime/database.
Existing installed formulae are reused; automatic Homebrew updates and implicit
formula upgrades are disabled. When Homebrew is absent and its installation is
allowed, the official Homebrew installer opens in Terminal. Complete its prompts,
including any administrator password request, then setup continues. macOS may
ask permission for Setup to control Terminal. Downloads are performed by
Homebrew and Colima/Podman; total download sizes depend on cached dependencies.

Copying the app into `/Applications` requests administrator authorization.
Installing into `~/Applications` avoids that step. Existing app bundles must have
the PS-pocketEdge bundle identifier; other apps are never overwritten. The
previous app is retained beside the new app as `PS-pocketEdge-previous-*.app`.

Runtime setup uses a dedicated `pspocketedge` profile/machine. Existing runtime
mode verifies its socket without changing that runtime's startup or VM settings.
PostgreSQL uses a private cluster independent of Docker/Podman, never resets an
existing cluster, and applies transactional forward migrations with an advisory
lock. Unknown existing app schemas and newer schemas are refused. Existing
golang-migrate history is recognized; retries do not apply migrations twice.

Generated database/admin passwords and a stable JWT secret are kept in
`credentials.json` with mode 0600. The app data directory is mode 0700. The
initial local admin email is `admin@localhost`; the generated password is in that
file if needed. Enrollment tokens are removed from agent configuration after
the durable identity is saved. Database URLs and credentials are never printed
in the checklist. Keep credentials, database data, and `vault.key` backed up.

## Failure, retry, and services

Installation progress is displayed per component. **Stop setup** cancels further
work, preserves installed components and existing data, and shows the partial
checklist. A separately opened Homebrew installation in Terminal must be stopped
there if desired. Setup does not uninstall shared packages on failure.

**Retry** verifies components again and reuses database data, credentials, and
agent identity. Resume using the same setup mode, database, and data directory;
setup refuses changes that could attach an existing identity to another database.
Checklist and structured progress logs are saved as `checklist.json` and
`setup.log` in the data directory. Service logs are `postgres.log`,
`controlplane.log`, `agent.log`, and `runtime.log`.

Service plists are mode 0600 under `~/Library/LaunchAgents/` with labels
`com.pspocketedge.setup.postgres`, `.controlplane`, `.agent`, and `.runtime`.
Setup only manages these labels and refuses labels owned by another data folder.
It never stops unrelated PostgreSQL, Docker/Podman, or development services.
If default ports are occupied by unrelated services, stop them yourself or
choose existing-server/database mode.

When start-at-login is disabled, services run for the current session but do not
start automatically after the next login. Start the selected runtime first and
use `launchctl kickstart gui/$(id -u)/com.pspocketedge.setup.postgres`, followed by
the controlplane and agent labels, to start them manually. Omit postgres when
using an existing database. For an installer-managed runtime, kickstart the
runtime label first. An uninstall UI is not included: removing the app preserves
all data and background services. To stop/remove setup-owned services, boot out
their labels and remove their matching plists; explicitly choose whether to keep
database/VM data. Never delete shared runtime profiles or PostgreSQL clusters.

## Distribution and validation

Local artifacts use ad hoc signing. Public distribution still requires Developer
ID signing and notarization of the Setup app, nested executable payloads, and
DMG. See [macOS distribution guidance](macos-installer.md#public-distribution).

The automated checks cover refusal before mutations, path/symlink protection,
private stable credentials, runtime API verification, remote-mode dependency
skipping, readiness errors, app API configuration, and fresh-schema/retry
migrations. Full dependency downloads, administrator prompts, and reboot/login
startup should also be tested on a clean Mac before public release.
