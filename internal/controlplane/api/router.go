// Package api implements the control plane's REST/JSON API consumed by the
// Flutter dashboard.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/ai"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/backup"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbops"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/livestate"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/vault"
)

// defaultMetricsWindow is how far back GET .../metrics looks when the
// client doesn't pass a ?since= duration.
const defaultMetricsWindow = 1 * time.Hour

const enrollmentTokenTTL = 1 * time.Hour

// NewRouter builds the HTTP handler for the REST API.
//
// publicURL is how agents reach this control plane's HTTP API (used to
// build the upload/download URLs embedded in BackupCommand/RestoreCommand
// — the control plane can't assume its own bind address is what a remote
// agent, possibly behind a different network path or reverse proxy,
// should actually dial).
//
// A permissive CORS policy is applied so the Flutter web build can call
// this API from its dev server origin during local development; this
// should be tightened together with publicURL before any non-local
// deployment.
func NewRouter(log *slog.Logger, st *store.Store, authMgr *auth.Manager, dispatcher *deploy.Dispatcher, events *deploy.EventBus, serverEvents *livestate.EventBus, blobs *backup.BlobStore, publicURL string, inspectWaiter *deploy.InspectWaiter, opWaiter *deploy.OpWaiter, imageListWaiter *deploy.ImageListWaiter, imageDetailWaiter *deploy.ImageDetailWaiter, imageOpWaiter *deploy.ImageOpWaiter, logStreamRelay *deploy.LogStreamRelay, eventListWaiter *deploy.EventListWaiter, execStreamRelay *deploy.ExecStreamRelay, networkListWaiter *deploy.NetworkListWaiter, networkOpWaiter *deploy.NetworkOpWaiter, volumeListWaiter *deploy.VolumeListWaiter, volumeDetailWaiter *deploy.VolumeDetailWaiter, volumeOpWaiter *deploy.VolumeOpWaiter, v *vault.Vault, ops *dbops.Ops) http.Handler {
	aiClient := ai.New()
	secrets := vaultSecretSource(log, v)
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/login", handleLogin(log, st, authMgr))
	mux.Handle("GET /api/auth/me", authMgr.RequireAuth(handleGetMe(log, st)))
	mux.Handle("POST /api/auth/change-password", authMgr.RequireAuth(handleChangePassword(log, st)))

	mux.Handle("GET /api/users", authMgr.RequireAdmin(handleListUsers(log, st)))
	mux.Handle("POST /api/users", authMgr.RequireAdmin(handleCreateUser(log, st)))
	mux.Handle("PATCH /api/users/{id}/role", authMgr.RequireAdmin(handleUpdateUserRole(log, st)))
	mux.Handle("POST /api/users/{id}/reset-password", authMgr.RequireAdmin(handleResetUserPassword(log, st)))
	mux.Handle("DELETE /api/users/{id}", authMgr.RequireAdmin(handleDeleteUser(log, st)))

	mux.Handle("GET /api/servers", authMgr.RequireAuth(handleListServers(log, st)))
	mux.Handle("POST /api/servers/enroll-token", authMgr.RequireAuth(handleCreateEnrollmentToken(log, st)))
	mux.Handle("DELETE /api/servers/{id}", authMgr.RequireAdmin(handleRemoveServer(log, st, dispatcher)))
	mux.Handle("GET /api/servers/{id}", authMgr.RequireAuth(handleGetServer(log, st)))
	mux.Handle("GET /api/servers/{id}/metrics", authMgr.RequireAuth(handleGetServerMetrics(log, st)))
	// Auth via ?token= query param, not the Authorization header — see
	// handleDeploymentStream's doc comment for why.
	mux.HandleFunc("GET /api/servers/{id}/stream", handleServerStream(log, st, authMgr, serverEvents))

	mux.Handle("GET /api/stacks", authMgr.RequireAuth(handleListStacks(log, st)))

	// Kubernetes is a separate target type from a Docker agent/server.
	k8s := &kubernetesAPI{log: log, st: st, vault: v}
	mux.Handle("GET /api/kubernetes/clusters", authMgr.RequireAuth(http.HandlerFunc(k8s.listClusters)))
	mux.Handle("POST /api/kubernetes/clusters", authMgr.RequireAdmin(http.HandlerFunc(k8s.createCluster)))
	mux.Handle("DELETE /api/kubernetes/clusters/{id}", authMgr.RequireAdmin(http.HandlerFunc(k8s.deleteCluster)))
	mux.Handle("GET /api/kubernetes/clusters/{id}/overview", authMgr.RequireAuth(http.HandlerFunc(k8s.overview)))
	mux.Handle("GET /api/kubernetes/clusters/{id}/pods/{namespace}/{pod}/logs", authMgr.RequireAuth(http.HandlerFunc(k8s.podLogs)))
	mux.Handle("POST /api/kubernetes/clusters/{id}/workloads", authMgr.RequireAdmin(http.HandlerFunc(k8s.workload)))
	mux.Handle("PUT /api/kubernetes/clusters/{id}/workloads/{namespace}/{name}", authMgr.RequireAdmin(http.HandlerFunc(k8s.workload)))
	mux.Handle("GET /api/kubernetes/clusters/{id}/workloads/{namespace}/{name}/revisions", authMgr.RequireAuth(http.HandlerFunc(k8s.revisions)))
	mux.Handle("POST /api/kubernetes/clusters/{id}/workloads/{namespace}/{name}/rollback", authMgr.RequireAdmin(http.HandlerFunc(k8s.rollbackWorkload)))
	mux.Handle("DELETE /api/kubernetes/clusters/{id}/workloads/{namespace}/{name}", authMgr.RequireAdmin(http.HandlerFunc(k8s.deleteWorkload)))
	mux.Handle("POST /api/kubernetes/clusters/{id}/ai/ollama", authMgr.RequireAdmin(http.HandlerFunc(k8s.deployOllama)))
	mux.Handle("GET /api/kubernetes/clusters/{id}/helm", authMgr.RequireAuth(http.HandlerFunc(k8s.listHelm)))
	mux.Handle("POST /api/kubernetes/clusters/{id}/helm", authMgr.RequireAdmin(http.HandlerFunc(k8s.helmInstallOrUpgrade)))
	mux.Handle("GET /api/kubernetes/clusters/{id}/helm/{namespace}/{name}/history", authMgr.RequireAuth(http.HandlerFunc(k8s.helmHistory)))
	mux.Handle("POST /api/kubernetes/clusters/{id}/helm/{namespace}/{name}/rollback", authMgr.RequireAdmin(http.HandlerFunc(k8s.helmRollback)))
	mux.Handle("DELETE /api/kubernetes/clusters/{id}/helm/{namespace}/{name}", authMgr.RequireAdmin(http.HandlerFunc(k8s.helmUninstall)))

	// Deployment Management > Docker Compose: user-authored Compose files,
	// distinct from the /api/stacks catalog above.
	mux.Handle("GET /api/compose-files", authMgr.RequireAuth(handleListComposeFiles(log, st)))
	mux.Handle("POST /api/compose-files", authMgr.RequireAuth(handleCreateComposeFile(log, st)))
	mux.Handle("POST /api/compose-files/parse", authMgr.RequireAuth(handleParseComposeYAML(log, st)))
	mux.Handle("POST /api/compose-files/render", authMgr.RequireAuth(handleRenderComposeYAML(log, st)))
	mux.Handle("GET /api/compose-files/{id}", authMgr.RequireAuth(handleGetComposeFile(log, st)))
	mux.Handle("PATCH /api/compose-files/{id}", authMgr.RequireAuth(handleUpdateComposeFile(log, st)))
	mux.Handle("DELETE /api/compose-files/{id}", authMgr.RequireAuth(handleDeleteComposeFile(log, st)))
	mux.Handle("GET /api/compose-files/{id}/versions", authMgr.RequireAuth(handleListComposeFileVersions(log, st)))
	mux.Handle("GET /api/compose-files/{id}/versions/{versionId}", authMgr.RequireAuth(handleGetComposeFileVersion(log, st)))
	mux.Handle("POST /api/compose-files/{id}/versions/{versionId}/restore", authMgr.RequireAuth(handleRestoreComposeFileVersion(log, st)))

	// Deployment Management > Docker Compose > Configuration management:
	// reusable, environment-tagged sets of env vars.
	mux.Handle("GET /api/env-var-groups", authMgr.RequireAuth(handleListEnvVarGroups(log, st)))
	mux.Handle("POST /api/env-var-groups", authMgr.RequireAuth(handleCreateEnvVarGroup(log, st)))
	mux.Handle("GET /api/env-var-groups/{id}", authMgr.RequireAuth(handleGetEnvVarGroup(log, st)))
	mux.Handle("PATCH /api/env-var-groups/{id}", authMgr.RequireAuth(handleUpdateEnvVarGroup(log, st)))
	mux.Handle("DELETE /api/env-var-groups/{id}", authMgr.RequireAuth(handleDeleteEnvVarGroup(log, st)))

	d := newDeployer(log, st, dispatcher, events, opWaiter)
	d.publicURL = publicURL
	mux.Handle("POST /api/deployments/preview", authMgr.RequireAuth(handlePreviewDeployment(log, st)))
	mux.Handle("GET /api/deployments", authMgr.RequireAuth(handleListDeployments(d)))
	mux.Handle("POST /api/deployments", authMgr.RequireAuth(handleCreateDeployment(d)))
	mux.Handle("GET /api/deployments/{id}", authMgr.RequireAuth(handleGetDeployment(d)))
	mux.Handle("PATCH /api/deployments/{id}/metadata", authMgr.RequireAuth(handleUpdateDeploymentMetadata(d)))
	mux.Handle("POST /api/deployments/{id}/redeploy", authMgr.RequireAuth(handleRedeployDeployment(d)))
	mux.Handle("POST /api/deployments/{id}/services/{service}/redeploy", authMgr.RequireAuth(handleRedeployService(d)))
	mux.Handle("POST /api/deployments/{id}/services/{service}/scale", authMgr.RequireAuth(handleScaleService(d)))
	mux.Handle("POST /api/deployments/{id}/rollback", authMgr.RequireAuth(handleRollbackDeployment(d)))
	mux.Handle("POST /api/deployments/{id}/promote", authMgr.RequireAuth(handlePromoteDeployment(d)))
	mux.Handle("POST /api/deployments/{id}/action", authMgr.RequireAuth(handleDeploymentAction(d)))
	mux.Handle("GET /api/deployments/{id}/revisions", authMgr.RequireAuth(handleListDeploymentRevisions(d)))
	mux.Handle("GET /api/deployments/{id}/revisions/{revision}", authMgr.RequireAuth(handleGetDeploymentRevision(d)))
	mux.Handle("GET /api/deployments/{id}/drift", authMgr.RequireAuth(handleDeploymentDrift(d)))

	// Governance: approval workflow, environment policies (approval,
	// maintenance windows, required metadata), and the audit trail.
	mux.Handle("GET /api/deployment-requests", authMgr.RequireAuth(handleListDeploymentRequests(d)))
	mux.Handle("POST /api/deployment-requests/{id}/approve", authMgr.RequireAdmin(handleApproveDeploymentRequest(d)))
	mux.Handle("POST /api/deployment-requests/{id}/reject", authMgr.RequireAdmin(handleRejectDeploymentRequest(d)))
	mux.Handle("POST /api/deployment-requests/{id}/cancel", authMgr.RequireAuth(handleCancelDeploymentRequest(d)))
	mux.Handle("GET /api/environment-policies", authMgr.RequireAuth(handleListEnvironmentPolicies(d)))
	mux.Handle("PUT /api/environment-policies/{environment}", authMgr.RequireAdmin(handleUpdateEnvironmentPolicy(d)))
	mux.Handle("GET /api/audit-events", authMgr.RequireAdmin(handleListAuditEvents(d)))

	// Database Marketplace: curated engine catalog, one-click deployment
	// wizard, deployed instances, and their vaulted credentials.
	dbs := &databaseAPI{log: log, st: st, d: d, vault: v, ops: ops, dispatcher: dispatcher, publicURL: publicURL}
	mux.Handle("GET /api/database-engines", authMgr.RequireAuth(dbs.handleListEngines()))
	mux.Handle("POST /api/databases/preview", authMgr.RequireAuth(dbs.handlePreview()))
	mux.Handle("GET /api/databases", authMgr.RequireAuth(dbs.handleList()))
	mux.Handle("POST /api/databases", authMgr.RequireAuth(dbs.handleCreate()))
	mux.Handle("GET /api/databases/{id}", authMgr.RequireAuth(dbs.handleGet()))
	mux.Handle("PATCH /api/databases/{id}/configuration", authMgr.RequireAuth(dbs.handleReconfigure()))
	mux.Handle("DELETE /api/databases/{id}", authMgr.RequireAuth(dbs.handleRemoveDatabase()))
	mux.Handle("PUT /api/databases/{id}/backup-policy", authMgr.RequireAuth(dbs.handleUpdateBackupPolicy()))
	mux.Handle("POST /api/databases/{id}/backups", authMgr.RequireAuth(dbs.handleCreateBackup()))
	mux.Handle("POST /api/databases/{id}/refresh-from-backup", authMgr.RequireAuth(dbs.handleRefreshFromBackup()))
	mux.Handle("POST /api/databases/{id}/logical-backups", authMgr.RequireAuth(dbs.handlePostgresLogicalBackup()))
	mux.Handle("POST /api/databases/{id}/migrate-from-backup", authMgr.RequireAuth(dbs.handlePostgresMigrate()))
	mux.Handle("GET /api/databases/{id}/credentials", authMgr.RequireAuth(dbs.handleListCredentials()))
	mux.Handle("POST /api/databases/{id}/temporary-credentials", authMgr.RequireAuth(dbs.handleCreateTemporary()))
	mux.Handle("GET /api/databases/{id}/postgres/overview", authMgr.RequireAuth(dbs.handlePostgresOverview()))
	mux.Handle("GET /api/databases/{id}/postgres/metrics", authMgr.RequireAuth(dbs.handlePostgresMetrics()))
	mux.Handle("GET /api/databases/{id}/postgres/alerts", authMgr.RequireAuth(dbs.handlePostgresAlerts()))
	mux.Handle("GET /api/databases/{id}/postgres/sessions", authMgr.RequireAuth(dbs.handlePostgresSessions()))
	mux.Handle("GET /api/databases/{id}/postgres/slow-queries", authMgr.RequireAuth(dbs.handlePostgresSlowQueries()))
	mux.Handle("POST /api/databases/{id}/postgres/query-insights/enable", authMgr.RequireAuth(dbs.handleEnablePostgresInsights()))
	mux.Handle("GET /api/databases/{id}/postgres/sizes", authMgr.RequireAuth(dbs.handlePostgresSizes()))
	mux.Handle("POST /api/databases/{id}/postgres/query", authMgr.RequireAuth(dbs.handlePostgresQuery()))
	mux.Handle("POST /api/databases/{id}/postgres/connection-test", authMgr.RequireAuth(dbs.handlePostgresConnectionTest()))
	mux.Handle("POST /api/databases/{id}/postgres/databases", authMgr.RequireAuth(dbs.handlePostgresCreateDatabase()))
	mux.Handle("DELETE /api/databases/{id}/postgres/databases/{name}", authMgr.RequireAuth(dbs.handlePostgresDeleteDatabase()))
	mux.Handle("POST /api/databases/{id}/postgres/users", authMgr.RequireAuth(dbs.handlePostgresCreateUser()))
	mux.Handle("PUT /api/databases/{id}/postgres/users/{name}/permissions", authMgr.RequireAuth(dbs.handlePostgresPermissions()))
	mux.Handle("POST /api/databases/{id}/postgres/sessions/{pid}/terminate", authMgr.RequireAuth(dbs.handlePostgresTerminateSession()))
	mux.Handle("PUT /api/databases/{id}/postgres/connection-limit", authMgr.RequireAuth(dbs.handlePostgresConnectionLimit()))
	mux.Handle("POST /api/secrets/{id}/reveal", authMgr.RequireAuth(dbs.handleReveal()))
	mux.Handle("POST /api/secrets/{id}/download", authMgr.RequireAuth(dbs.handleDownload()))
	mux.Handle("POST /api/secrets/{id}/rotate", authMgr.RequireAuth(dbs.handleRotate()))
	mux.Handle("POST /api/secrets/{id}/revoke", authMgr.RequireAuth(dbs.handleRevoke()))
	mux.Handle("GET /api/secrets/{id}/grants", authMgr.RequireAuth(dbs.handleListGrants()))
	mux.Handle("PUT /api/secrets/{id}/grants/{userId}", authMgr.RequireAuth(dbs.handlePutGrant()))
	mux.Handle("DELETE /api/secrets/{id}/grants/{userId}", authMgr.RequireAuth(dbs.handleDeleteGrant()))
	mux.Handle("GET /api/users/directory", authMgr.RequireAuth(handleUserDirectory(log, st)))

	// Git-based deployment.
	mux.Handle("GET /api/git-repositories", authMgr.RequireAuth(handleListGitRepositories(log, st)))
	mux.Handle("POST /api/git-repositories", authMgr.RequireAdmin(handleCreateGitRepository(log, st, publicURL)))
	mux.Handle("PATCH /api/git-repositories/{id}", authMgr.RequireAdmin(handleUpdateGitRepository(log, st)))
	mux.Handle("DELETE /api/git-repositories/{id}", authMgr.RequireAdmin(handleDeleteGitRepository(log, st)))
	mux.Handle("GET /api/git-repositories/{id}/webhook", authMgr.RequireAdmin(handleGetGitWebhook(log, st, publicURL)))
	mux.Handle("POST /api/git-repositories/{id}/webhook/rotate", authMgr.RequireAdmin(handleRotateGitWebhookSecret(log, st, publicURL)))
	mux.Handle("GET /api/git-repositories/{id}/refs", authMgr.RequireAuth(handleListGitRefs(log, st)))
	mux.Handle("GET /api/git-repositories/{id}/file", authMgr.RequireAuth(handlePreviewGitFile(log, st)))
	mux.Handle("POST /api/git-repositories/{id}/import", authMgr.RequireAuth(handleImportGitComposeFile(log, st)))
	mux.Handle("POST /api/compose-files/{id}/git/sync", authMgr.RequireAuth(handleSyncComposeFile(log, st)))
	mux.Handle("PUT /api/compose-files/{id}/git", authMgr.RequireAuth(handleSetComposeFileGitLink(log, st)))
	mux.Handle("GET /api/compose-files/{id}/git/commits", authMgr.RequireAuth(handleListComposeFileCommits(log, st)))
	// Unauthenticated: verified by the repository's webhook secret.
	mux.HandleFunc("POST /api/webhooks/git/{repoId}", handleGitWebhook(d))
	mux.HandleFunc("GET /api/deployments/{id}/stream", handleDeploymentStream(log, st, authMgr, events))

	mux.Handle("GET /api/containers", authMgr.RequireAuth(handleListContainers(log, st)))
	mux.Handle("POST /api/servers/{id}/containers/{containerId}/inspect", authMgr.RequireAuth(handleInspectContainer(log, dispatcher, inspectWaiter)))
	mux.Handle("POST /api/servers/{id}/containers", authMgr.RequireAuth(handleCreateContainer(log, st, dispatcher, opWaiter)))
	mux.Handle("POST /api/servers/{id}/containers/{containerId}/action", authMgr.RequireAuth(handleContainerAction(log, dispatcher, opWaiter)))
	mux.Handle("POST /api/containers/bulk-action", authMgr.RequireAuth(handleBulkContainerAction(log, dispatcher, opWaiter)))
	mux.Handle("POST /api/servers/{id}/containers/{containerId}/rename", authMgr.RequireAuth(handleRenameContainer(log, dispatcher, opWaiter)))
	mux.Handle("POST /api/servers/{id}/containers/{containerId}/clone", authMgr.RequireAuth(handleCloneContainer(log, dispatcher, opWaiter)))
	mux.Handle("POST /api/servers/{id}/containers/{containerId}/recreate", authMgr.RequireAuth(handleRecreateContainer(log, st, dispatcher, opWaiter)))
	mux.Handle("PATCH /api/servers/{id}/containers/{containerId}/restart-policy", authMgr.RequireAuth(handleUpdateRestartPolicy(log, dispatcher, opWaiter)))
	mux.Handle("PATCH /api/servers/{id}/containers/{containerId}/resources", authMgr.RequireAuth(handleUpdateResourceLimits(log, dispatcher, opWaiter)))
	mux.Handle("GET /api/servers/{id}/containers/{containerId}/metrics", authMgr.RequireAuth(handleGetContainerMetrics(log, st)))
	mux.Handle("GET /api/servers/{id}/containers/{containerId}/insights", authMgr.RequireAuth(handleContainerInsights(log, st)))
	mux.Handle("GET /api/container-alert-rules", authMgr.RequireAuth(handleListContainerAlertRules(log, st)))
	mux.Handle("POST /api/container-alert-rules", authMgr.RequireAuth(handleCreateContainerAlertRule(log, st)))
	mux.Handle("PUT /api/container-alert-rules/{id}", authMgr.RequireAuth(handleUpdateContainerAlertRule(log, st)))
	mux.Handle("DELETE /api/container-alert-rules/{id}", authMgr.RequireAuth(handleDeleteContainerAlertRule(log, st)))
	mux.Handle("GET /api/container-alerts", authMgr.RequireAuth(handleListContainerAlerts(log, st)))
	mux.Handle("POST /api/container-alerts/{id}/acknowledge", authMgr.RequireAuth(handleAcknowledgeContainerAlert(log, st)))
	mux.Handle("GET /api/servers/{id}/containers/{containerId}/rollback-history", authMgr.RequireAuth(handleListImageRollbackHistory(log, st)))
	mux.Handle("POST /api/servers/{id}/containers/{containerId}/rollback", authMgr.RequireAuth(handleRollbackContainer(log, st, dispatcher, inspectWaiter, opWaiter)))

	// Logs and troubleshooting. Auth via ?token= query param on the two WS
	// endpoints, not the Authorization header — same documented exception
	// as handleServerStream (browsers can't set custom headers on a
	// WebSocket handshake).
	mux.HandleFunc("GET /api/servers/{id}/containers/{containerId}/logs/stream", handleContainerLogsStream(log, authMgr, dispatcher, logStreamRelay, secrets))
	mux.Handle("GET /api/servers/{id}/containers/{containerId}/logs/download", authMgr.RequireAuth(handleDownloadContainerLogs(log, dispatcher, logStreamRelay, secrets)))
	mux.Handle("POST /api/servers/{id}/containers/{containerId}/logs/analyze", authMgr.RequireAuth(handleAnalyzeContainerLogs(log, st, dispatcher, logStreamRelay, aiClient, secrets)))
	mux.Handle("GET /api/servers/{id}/containers/{containerId}/events", authMgr.RequireAuth(handleListContainerEvents(log, dispatcher, eventListWaiter)))
	mux.HandleFunc("GET /api/servers/{id}/containers/{containerId}/exec", handleContainerExec(log, authMgr, dispatcher, execStreamRelay))
	mux.Handle("GET /api/ai/status", authMgr.RequireAuth(handleAIStatus(aiClient)))

	mux.Handle("GET /api/images", authMgr.RequireAuth(handleListImages(log, st, dispatcher, imageListWaiter)))
	mux.Handle("GET /api/images/newer", authMgr.RequireAuth(handleImageUpdateAvailable(log, st, dispatcher, imageDetailWaiter)))
	mux.Handle("POST /api/images/scan", authMgr.RequireAuth(handleScanImage(log, st)))
	mux.Handle("GET /api/images/scan", authMgr.RequireAuth(handleGetImageScan(log, st)))
	mux.Handle("GET /api/servers/{id}/images/{imageId}/inspect", authMgr.RequireAuth(handleInspectImage(log, dispatcher, imageDetailWaiter)))
	mux.Handle("POST /api/servers/{id}/images/pull", authMgr.RequireAuth(handlePullImage(log, st, dispatcher, imageOpWaiter)))
	mux.Handle("DELETE /api/servers/{id}/images/{imageId}", authMgr.RequireAuth(handleRemoveImage(log, dispatcher, imageOpWaiter)))
	mux.Handle("POST /api/servers/{id}/images/prune", authMgr.RequireAuth(handlePruneImages(log, dispatcher, imageOpWaiter)))

	mux.Handle("GET /api/networks", authMgr.RequireAuth(handleListNetworks(log, st, dispatcher, networkListWaiter)))
	mux.Handle("POST /api/servers/{id}/networks", authMgr.RequireAuth(handleCreateNetwork(log, dispatcher, networkOpWaiter)))
	mux.Handle("DELETE /api/servers/{id}/networks/{networkId}", authMgr.RequireAuth(handleRemoveNetwork(log, dispatcher, networkOpWaiter)))
	mux.Handle("POST /api/servers/{id}/networks/{networkId}/connect", authMgr.RequireAuth(handleConnectContainerToNetwork(log, dispatcher, networkOpWaiter)))
	mux.Handle("POST /api/servers/{id}/networks/{networkId}/disconnect", authMgr.RequireAuth(handleDisconnectContainerFromNetwork(log, dispatcher, networkOpWaiter)))

	mux.Handle("GET /api/volumes", authMgr.RequireAuth(handleListVolumes(log, st, dispatcher, volumeListWaiter)))
	mux.Handle("POST /api/servers/{id}/volumes", authMgr.RequireAuth(handleCreateVolume(log, dispatcher, volumeOpWaiter)))
	mux.Handle("GET /api/servers/{id}/volumes/{name}", authMgr.RequireAuth(handleInspectVolume(log, dispatcher, volumeDetailWaiter)))
	mux.Handle("DELETE /api/servers/{id}/volumes/{name}", authMgr.RequireAuth(handleRemoveVolume(log, dispatcher, volumeOpWaiter)))
	mux.Handle("GET /api/servers/{id}/ports/check", authMgr.RequireAuth(handleCheckPortConflict(log, st)))

	mux.Handle("GET /api/registries", authMgr.RequireAdmin(handleListRegistries(log, st)))
	mux.Handle("POST /api/registries", authMgr.RequireAdmin(handleCreateRegistry(log, st)))
	mux.Handle("DELETE /api/registries/{id}", authMgr.RequireAdmin(handleDeleteRegistry(log, st)))
	mux.Handle("GET /api/registries/search", authMgr.RequireAuth(handleSearchRegistries(log, st)))
	mux.Handle("GET /api/registries/tags", authMgr.RequireAuth(handleListImageTags(log, st)))

	mux.Handle("GET /api/image-policies", authMgr.RequireAdmin(handleListApprovedImages(log, st)))
	mux.Handle("POST /api/image-policies", authMgr.RequireAdmin(handleCreateApprovedImage(log, st)))
	mux.Handle("DELETE /api/image-policies/{id}", authMgr.RequireAdmin(handleDeleteApprovedImage(log, st)))
	mux.Handle("GET /api/image-policies/settings", authMgr.RequireAuth(handleGetImagePolicySettings(log, st)))
	mux.Handle("PATCH /api/image-policies/settings", authMgr.RequireAdmin(handleUpdateImagePolicySettings(log, st)))

	mux.Handle("GET /api/schedules", authMgr.RequireAuth(handleListSchedules(log, st)))
	mux.Handle("POST /api/schedules", authMgr.RequireAuth(handleCreateSchedule(log, st)))
	mux.Handle("PATCH /api/schedules/{id}", authMgr.RequireAuth(handleUpdateSchedule(log, st)))
	mux.Handle("DELETE /api/schedules/{id}", authMgr.RequireAuth(handleDeleteSchedule(log, st)))

	mux.Handle("POST /api/deployments/{id}/backups", authMgr.RequireAuth(handleCreateBackup(log, st, dispatcher, publicURL)))
	mux.Handle("GET /api/deployments/{id}/backups", authMgr.RequireAuth(handleListBackups(log, st)))
	mux.Handle("GET /api/backups/{id}", authMgr.RequireAuth(handleGetBackup(log, st)))
	mux.Handle("POST /api/backups/{id}/restore", authMgr.RequireAuth(handleRestoreBackup(log, st, dispatcher, events, publicURL, v)))
	// Agent-credential auth (bearer token hashed against the owning
	// server's agent_token_hash), not admin JWT — these two are called by
	// the agent itself, not the Flutter app. See handleUploadBackupBlob's
	// doc comment.
	mux.HandleFunc("PUT /api/agent/backups/{id}/blob", handleUploadBackupBlob(log, st, blobs))
	mux.HandleFunc("GET /api/agent/backups/{id}/blob", handleDownloadBackupBlob(log, st, blobs))

	return withCORS(mux)
}

func handleLogin(log *slog.Logger, st *store.Store, authMgr *auth.Manager) http.HandlerFunc {
	type request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	type response struct {
		Token string `json:"token"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		user, err := st.GetUserByEmail(r.Context(), req.Email)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "invalid email or password", http.StatusUnauthorized)
			return
		}
		if err != nil {
			log.Error("login lookup failed", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if !auth.CheckPassword(user.PasswordHash, req.Password) {
			http.Error(w, "invalid email or password", http.StatusUnauthorized)
			return
		}

		token, err := authMgr.IssueToken(user.ID, user.Email, user.Role)
		if err != nil {
			log.Error("failed to issue token", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, response{Token: token})
	}
}

func handleListServers(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		servers, err := st.ListServers(r.Context())
		if err != nil {
			log.Error("failed to list servers", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, servers)
	}
}

func handleGetServer(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type response struct {
		store.Server
		Containers []store.ContainerState `json:"containers"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		server, err := st.GetServer(r.Context(), id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "server not found", http.StatusNotFound)
				return
			}
			log.Error("failed to load server", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		containers, err := st.ListContainers(r.Context(), id)
		if err != nil {
			log.Error("failed to list containers", "server_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, response{Server: *server, Containers: containers})
	}
}

func handleGetServerMetrics(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		window := defaultMetricsWindow
		if raw := r.URL.Query().Get("since"); raw != "" {
			parsed, err := time.ParseDuration(raw)
			if err != nil {
				http.Error(w, "invalid since duration", http.StatusBadRequest)
				return
			}
			window = parsed
		}

		samples, err := st.ListMetricSamples(r.Context(), id, time.Now().Add(-window))
		if err != nil {
			log.Error("failed to list metric samples", "server_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, samples)
	}
}

// handleGetContainerMetrics serves one container's resource-usage history —
// the per-container counterpart to handleGetServerMetrics above, same
// ?since= duration query param and default window.
func handleGetContainerMetrics(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		containerID := r.PathValue("containerId")

		window := defaultMetricsWindow
		if raw := r.URL.Query().Get("since"); raw != "" {
			parsed, err := time.ParseDuration(raw)
			if err != nil {
				http.Error(w, "invalid since duration", http.StatusBadRequest)
				return
			}
			window = parsed
		}

		samples, err := st.ListContainerMetricSamples(r.Context(), serverID, containerID, time.Now().Add(-window))
		if err != nil {
			log.Error("failed to list container metric samples", "server_id", serverID, "container_id", containerID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, samples)
	}
}

func handleCreateEnrollmentToken(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type response struct {
		Token       string    `json:"token"`
		ExpiresAt   time.Time `json:"expiresAt"`
		InstallHint string    `json:"installHint"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		token, err := auth.RandomToken()
		if err != nil {
			log.Error("failed to generate enrollment token", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		expiresAt := time.Now().Add(enrollmentTokenTTL)
		if _, err := st.CreateEnrollmentToken(r.Context(), auth.HashToken(token), claims.UserID, expiresAt); err != nil {
			log.Error("failed to persist enrollment token", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, response{
			Token:       token,
			ExpiresAt:   expiresAt,
			InstallHint: "curl -sSL https://<control-plane>/install.sh | sh -s -- --token=" + token,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
