package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// handleRefreshFromBackup restores a quiesced physical PostgreSQL backup
// into another existing instance. Both must use the same major version and
// cluster identity; the agent remaps the Docker volume names, then rotates
// the restored administrator role to the target instance's vaulted password.
func (api *databaseAPI) handleRefreshFromBackup() http.HandlerFunc {
	type request struct {
		BackupID string `json:"backupId"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		target, ok := api.loadInstance(w, r)
		if !ok {
			return
		}
		a := actorFromRequest(r)
		if !canManageInstance(a, target) {
			http.Error(w, "only the database owner or an admin can refresh it", 403)
			return
		}
		if target.Engine != "postgresql" {
			http.Error(w, "refresh currently supports PostgreSQL", 501)
			return
		}
		var req request
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.BackupID == "" {
			http.Error(w, "backupId is required", 400)
			return
		}
		backup, err := api.st.GetBackup(r.Context(), req.BackupID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "backup not found", 404)
			return
		}
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if backup.Status != "completed" || backup.Format != "volumes" || !backup.Quiesced {
			http.Error(w, "refresh requires a completed, consistent backup", 409)
			return
		}
		if backup.DeploymentID == target.DeploymentID {
			http.Error(w, "use restore for a backup of this same instance", 400)
			return
		}
		source, err := api.st.GetDatabaseInstanceByDeployment(r.Context(), backup.DeploymentID)
		if err != nil {
			writeActionError(w, api.log, newActionError(404, "source database is unavailable"))
			return
		}
		if !canManageInstance(a, source) {
			http.Error(w, "access to the source database is required", 403)
			return
		}
		sourceMajor, _ := postgresMajor(source.Version)
		targetMajor, _ := postgresMajor(target.Version)
		if source.Engine != "postgresql" || sourceMajor != targetMajor || source.AdminUsername != target.AdminUsername || source.DatabaseName != target.DatabaseName {
			http.Error(w, "source and target must have the same PostgreSQL major version, administrator, and primary database name", 409)
			return
		}
		dep, err := api.st.GetDeployment(r.Context(), target.DeploymentID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		name, composeYAML, err := api.st.ResolveDeploymentSource(r.Context(), dep)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		env, err := api.vault.ResolveEnv(r.Context(), dep.Env)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if env["DB_PASSWORD"] == "" {
			http.Error(w, "target has no administrator password", 409)
			return
		}
		downloadURL, err := api.backupDownloadURL(r, backup, target.ServerID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		cmd := &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_Restore{Restore: &agentv1.RestoreCommand{
			BackupId: backup.ID, DeploymentId: target.DeploymentID, DownloadUrl: downloadURL,
			StackName: name, ComposeYaml: composeYAML, Env: env, SourceDeploymentId: source.DeploymentID,
			SyncPostgresPassword: true, PostgresUsername: target.AdminUsername, PostgresDatabase: target.DatabaseName,
		}}}
		if err := api.dispatcher.Send(target.ServerID, cmd); err != nil {
			writeActionError(w, api.log, newActionError(409, "target server not connected"))
			return
		}
		api.d.event(r.Context(), target.DeploymentID, "pending", "refresh requested from backup "+backup.ID, a.ID)
		api.d.audit(r.Context(), a, "database.refresh", "database", target.ID, "database refresh requested", map[string]any{"sourceDatabaseId": source.ID, "backupId": backup.ID})
		writeJSON(w, http.StatusAccepted, map[string]string{"deploymentId": target.DeploymentID, "backupId": backup.ID})
	}
}

func (api *databaseAPI) backupDownloadURL(r *http.Request, backup *store.Backup, targetServerID string) (string, error) {
	url := api.publicURL + "/api/agent/backups/" + backup.ID + "/blob"
	if backup.ServerID == targetServerID {
		return url, nil
	}
	if err := api.st.GrantBackupRestore(r.Context(), backup.ID, targetServerID, time.Now().Add(24*time.Hour)); err != nil {
		return "", err
	}
	return url + "?serverId=" + targetServerID, nil
}
