# PS-pocketEdge CLI

`pse` manages PS-pocketEdge through its control-plane REST API from a terminal.
It works on Linux servers without a desktop, as well as macOS and Windows.
The control plane and enrolled agents must already be running. It uses the
same user accounts, role permissions, and deployment approval rules as the app.

## Build and run

From the repository root with Go and Make installed:

```sh
make build-cli
./bin/pse --help
```

On Linux/macOS, install the binary on your command path:

```sh
sudo install -m 0755 bin/pse /usr/local/bin/pse
```

The release configuration also builds `pse` archives for Linux, macOS, and
Windows on amd64 and arm64. These become available when a release is published.

## Login

```sh
pse login --url https://control.example.com:8080 --email admin@example.com
```

The password prompt hides your input. For scripts, use `--password-stdin`
with password input supplied by your secret manager. There is no password
command-line argument. For a local control plane, the default URL is
`http://127.0.0.1:8080`. Remote URLs require HTTPS. For a private CA, add
`--ca-file /path/to/ca.pem`; the certificate must match the URL hostname.

Login saves the URL, session token, and optional CA path in `cli.json` under
the OS user configuration directory's `pspocketedge` folder. New files use
owner-only permissions on Unix. Override the path with `--config` or
`PSE_CONFIG`. `--url` or `PSE_URL` overrides the saved URL; switching URLs
requires a new login. `PSE_TOKEN` supplies a token without saving it.

```sh
pse logout
```

Logout removes the saved token. It does not revoke issued tokens or clear
an externally supplied `PSE_TOKEN`. Expired sessions require another login.

## Servers and containers

```sh
pse servers list
pse containers list
pse containers list --server SERVER_ID
pse containers list --json
pse containers start CONTAINER_ID
pse containers stop CONTAINER_ID
pse containers restart CONTAINER_ID
pse containers inspect CONTAINER_ID
pse containers logs CONTAINER_ID --tail 100
pse containers logs CONTAINER_ID --follow
pse containers exec CONTAINER_ID --tty -- /bin/sh
```

Use full IDs from the list output. Commands infer the server from inventory
when the container ID is unique. Add `--server SERVER_ID` to choose a server
explicitly, or to address a container not yet present in inventory. Logs print
a snapshot of recent logs; `--tail 0` requests all available lines. Add
`--follow` (or `-f`) for live logs; Ctrl+C stops the stream. With `--json`,
live logs print one JSON object per line. Inspect and action results print
JSON; list commands print tables unless `--json` is supplied. API failures,
including a container operation returning `success: false`, exit with code 1.

Exec uses the agent's existing PTY protocol, with stdout and stderr combined.
`--tty` (or `-t`) requires terminal stdin, enables raw local input, forwards
window resizing, and restores the terminal on completion or cancellation.
Ctrl+C in raw mode reaches the container process. Closing the connection
stops the remote session. Non-interactive commands can omit `--tty`:

```sh
pse containers exec CONTAINER_ID -- /bin/sh -c 'echo hello'
```

Put the executable and all its arguments after `--`. Omitting a command
starts `/bin/sh`. Input is forwarded to the remote PTY; non-interactive
stdin EOF sends Ctrl+D. Exec returns the remote exit code (1–255); transport
errors return 1, and local cancellation returns 130. This API always creates
a remote PTY, so it combines output streams and may use CRLF line endings.

Live logs and exec require the updated control plane, which supports bearer
headers on these WebSockets. Existing browser clients continue to work with
query tokens. Older agents already support the exec command field.

## Compose deployments

```sh
pse deploy compose.yaml --server SERVER_ID --name my-service
pse deployments list
pse deploy --compose-id SAVED_COMPOSE_ID --server SERVER_ID
```

Deploying a local file first creates a saved Compose file, then submits a
deployment. Names must be unique; reuse `--compose-id` for later deployments
of an existing file. Uploaded files support prebuilt images; `build:` files
must use the existing Git import workflow in the app. Uploads are limited to
4 MiB. The API validates Compose and enforces its image and deployment policies.

Submission prints the API response, including the deployment's status. An
accepted submission may be queued for approval or a maintenance window; it
does not mean the containers are already running. Use `pse deployments list`
to check status. If submission fails after upload, the saved file remains;
the error provides its ID for retrying with `--compose-id`.

## Database management

```sh
pse databases engines
pse databases list
pse databases inspect DATABASE_ID
pse databases preview --engine postgresql --name appdb --server SERVER_ID
pse databases create --engine postgresql --name appdb --server SERVER_ID
pse databases configure DATABASE_ID --file settings.json
pse databases credentials DATABASE_ID
pse databases temporary-credentials DATABASE_ID --file temporary.json
pse databases secrets reveal SECRET_ID
pse databases secrets rotate SECRET_ID
pse databases secrets revoke SECRET_ID
pse databases backup-policy DATABASE_ID --file policy.json
pse databases backup DATABASE_ID --consistent
pse databases logical-backup DATABASE_ID
pse databases refresh TARGET_DATABASE_ID --backup PHYSICAL_BACKUP_ID
pse databases migrate TARGET_DATABASE_ID --backup LOGICAL_BACKUP_ID
pse databases remove DATABASE_ID
```

Create and preview accept `--file` for advanced API settings; explicit
`--engine`, `--name`, and `--server` flags override the corresponding file
fields. Example creation settings:

```json
{
  "engine": "postgresql",
  "name": "appdb",
  "serverId": "YOUR_SERVER_UUID",
  "memoryMb": 512,
  "cpus": 1,
  "storageGb": 10,
  "backup": {"cron": "0 2 * * *", "consistent": true, "retentionDays": 7}
}
```

Configure accepts fields such as `version`, `memoryMb`, `cpus`, and
`storageGb`. Backup-policy JSON uses `cron`, `consistent`, `retentionDays`,
and `retentionCount`. Temporary credentials use `{"ttlMinutes":60}`.
`credentials` lists masked values; `secrets reveal` explicitly prints a
secret. Credential grants are available through `secrets grants`, `grant`
(`--file` with optional `expiresAt`), and `ungrant`.

PostgreSQL administration and monitoring use the `databases postgres` group:

```sh
pse databases postgres overview DATABASE_ID
pse databases postgres metrics DATABASE_ID
pse databases postgres sessions DATABASE_ID
pse databases postgres slow-queries DATABASE_ID
pse databases postgres connection-test DATABASE_ID
pse databases postgres query DATABASE_ID --file query.json
```

Query JSON uses `{"sql":"select current_database()","database":"appdb"}`;
results print as CSV.
The group also provides `alerts`, `sizes`, `enable-insights`, `create-database`,
`remove-database`, `create-user`, `permissions`, `connection-limit`, and
`terminate-session`. Commands with structured inputs accept JSON `--file`
options matching the API (e.g. `{"name":"analytics"}` for create-database;
`{"username":"analyst","database":"appdb","permission":"read"}` for
create-user). The API enforces ownership, role, engine, and license requirements.

## Backups

```sh
pse backups create --deployment DEPLOYMENT_ID
pse backups create --deployment DEPLOYMENT_ID --consistent
pse backups list --deployment DEPLOYMENT_ID
pse backups inspect BACKUP_ID
pse backups restore BACKUP_ID
```

Omitting `--consistent` preserves the API's configured backup behavior;
`--consistent=false` explicitly requests a live physical snapshot. Consistent
physical backups stop the deployment's containers during the snapshot.
Creation is asynchronous; inspect the returned backup ID for completion.
Restore replaces data in the backup's original deployment. Logical PostgreSQL
archives use `databases migrate` into a target instance. The existing API
limits blob transfer to agents, so this CLI does not download backup blobs.

## Images

```sh
pse images list
pse images list --server SERVER_ID --json
pse images pull nginx:alpine --server SERVER_ID
pse images pull PRIVATE_IMAGE --server SERVER_ID --registry REGISTRY_ID
pse images inspect IMAGE_ID --server SERVER_ID
pse images remove IMAGE_ID --server SERVER_ID
pse images prune --server SERVER_ID
pse images prune --server SERVER_ID --all
pse images scan --file scan.json
```

Scan JSON uses `{"imageRef":"nginx:alpine"}` and requires the control-plane
scanner. Prune defaults to dangling images; `--all` removes all unused images.
Remove accepts `--force`. Fleet inventory may omit offline servers, following
the existing API behavior.

## Kubernetes

```sh
pse kubernetes clusters list
pse kubernetes clusters add --name production --kubeconfig kubeconfig.yaml
pse kubernetes clusters create-local --name development
pse kubernetes overview CLUSTER_ID --namespace all
pse kubernetes pods logs POD_NAME --cluster CLUSTER_ID --namespace default --tail 100
pse kubernetes workloads create --cluster CLUSTER_ID --file workload.json --dry-run
pse kubernetes workloads create --cluster CLUSTER_ID --file workload.json
pse kubernetes workloads update web --cluster CLUSTER_ID --file workload.json
pse kubernetes workloads revisions web --cluster CLUSTER_ID
pse kubernetes workloads rollback web --cluster CLUSTER_ID --revision 1
pse kubernetes workloads remove web --cluster CLUSTER_ID
pse kubernetes helm list --cluster CLUSTER_ID
pse kubernetes helm install web --cluster CLUSTER_ID --chart chart.tgz --values values.json
pse kubernetes helm history web --cluster CLUSTER_ID
pse kubernetes helm rollback web --cluster CLUSTER_ID --revision 1
pse kubernetes helm remove web --cluster CLUSTER_ID
pse kubernetes clusters remove CLUSTER_ID
pse kubernetes clusters delete-local LOCAL_CLUSTER_ID
```

`k8s` is an alias for `kubernetes`. Workload JSON is the app's deployment
request format, rather than an arbitrary Kubernetes YAML manifest:

```json
{"namespace":"default","name":"web","image":"nginx:alpine","replicas":1,"cpu":"250m","memory":"256Mi"}
```

Workloads also accept `gpuResource` and `gpuCount`; updates must match the
name and namespace in the URL. Use `--namespace` (or `-n`) for workload and
Helm targets outside `default`. Pod logs are bounded snapshots, matching
the API; container `--follow` applies to Docker containers. Helm install
also upgrades an existing release and accepts a packaged chart up to 4 MiB.
`kubernetes ollama CLUSTER_ID --file ollama.json` exposes the app's AI
deployment settings.

Removing a registered cluster disconnects it; `delete-local` deletes the
managed kind cluster on the control-plane host. Local creation requires
kind and Docker on that host. Rollback and removal follow the API's managed
workload restrictions. No Kubernetes tools are required on the CLI machine.

Structured request files are JSON objects limited to 4 MiB; `--file -`
reads from stdin. Single results print JSON and list commands offer
`--json`. HTTP calls default to a 10-minute timeout to accommodate image
pulls and local cluster creation; use `--timeout 15m` to override it.
