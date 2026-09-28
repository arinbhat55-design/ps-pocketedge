# Cloud connectivity

PSpocketEdge can run its control plane on a cloud VM and manage Docker hosts
that run the PSpocketEdge agent. Agents initiate the gRPC connection to the
control plane; Docker Engine does not need a public TCP port.

## TLS setup

For remote access, provide a certificate and private key whose DNS names
cover the address agents use. The control plane serves TLS on both its REST
and gRPC listeners when these options are set:

```sh
pe-controlplane \
  --grpc-addr=:8443 --http-addr=:8080 \
  --tls-cert=/etc/pspocketedge/tls.crt \
  --tls-key=/etc/pspocketedge/tls.key \
  --public-url=https://control.example.com:8080
```

The certificate must be trusted by the agent and by users of the REST API.
For a private CA, copy its PEM certificate to each host and install the agent
with `--ca-file=/path/to/ca.pem`. For a publicly trusted certificate, omit
`--ca-file`. The installer enables TLS and uses the same CA for gRPC and
backup transfers. The server name in `--server` must match a certificate DNS
name. Do not set `PUBLIC_URL` to an HTTP URL for remote agents: authenticated
backup transfers require HTTPS.

```sh
PE_ENROLL_TOKEN=<one-time-token> sh scripts/install-agent.sh \
  --server=control.example.com:8443 \
  --ca-file=/path/to/ca.pem
```

Keep ports 8443 and 8080 reachable only from the required agents and users,
respectively. Use a private network or firewall rules. The control plane's
plain HTTP and gRPC modes now bind only to loopback addresses for local
development. Existing deployments that listen on public addresses must add
the TLS certificate and key before upgrading.

The database, backup blobs, vault key, and JWT signing secret still require
durable storage and a tested recovery procedure. The current blob store is a
local directory (`BACKUP_DIR`); object storage and automated control-plane
recovery are future work.
