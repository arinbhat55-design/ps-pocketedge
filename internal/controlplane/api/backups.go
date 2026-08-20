package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/backup"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func handleCreateBackup(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, publicURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		deploymentID := r.PathValue("id")
		deployment, err := st.GetDeployment(r.Context(), deploymentID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "deployment not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		backupID, err := st.CreateBackup(r.Context(), deployment.ID, deployment.ServerID, claims.UserID)
		if err != nil {
			log.Error("failed to create backup", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_Backup{
				Backup: &agentv1.BackupCommand{
					BackupId:     backupID,
					DeploymentId: deployment.ID,
					UploadUrl:    publicURL + "/api/agent/backups/" + backupID + "/blob",
				},
			},
		}
		if err := dispatcher.Send(deployment.ServerID, cmd); err != nil {
			log.Warn("failed to dispatch backup command", "backup_id", backupID, "error", err)
			_ = st.UpdateBackupStatus(r.Context(), backupID, "failed", "server not connected: "+err.Error())
			writeJSON(w, http.StatusConflict, map[string]string{
				"backupId": backupID,
				"error":    "server not connected",
			})
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"backupId": backupID})
	}
}

func handleListBackups(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backups, err := st.ListBackupsForDeployment(r.Context(), r.PathValue("id"))
		if err != nil {
			log.Error("failed to list backups", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, backups)
	}
}

func handleGetBackup(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := st.GetBackup(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "backup not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load backup", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, b)
	}
}

// handleRestoreBackup dispatches a RestoreCommand for backup's deployment.
// Restore always targets the deployment the backup was taken from — there
// is no "restore into a new/different deployment" yet.
func handleRestoreBackup(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, events *deploy.EventBus, publicURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backupID := r.PathValue("id")
		b, err := st.GetBackup(r.Context(), backupID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "backup not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load backup", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if b.Status != "completed" {
			http.Error(w, "backup is not in a restorable state", http.StatusConflict)
			return
		}

		deployment, err := st.GetDeployment(r.Context(), b.DeploymentID)
		if err != nil {
			log.Error("failed to load deployment", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		stack, err := st.GetStack(r.Context(), deployment.StackID)
		if err != nil {
			log.Error("failed to load stack", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		if event, err := st.AddDeploymentEvent(r.Context(), deployment.ID, "pending", "restore requested from backup "+backupID); err == nil {
			events.Publish(deployment.ID, event)
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_Restore{
				Restore: &agentv1.RestoreCommand{
					BackupId:     backupID,
					DeploymentId: deployment.ID,
					DownloadUrl:  publicURL + "/api/agent/backups/" + backupID + "/blob",
					StackName:    stack.Name,
					ComposeYaml:  stack.ComposeYAML,
					Env:          deployment.Env,
				},
			},
		}
		if err := dispatcher.Send(deployment.ServerID, cmd); err != nil {
			log.Warn("failed to dispatch restore command", "backup_id", backupID, "error", err)
			if event, addErr := st.AddDeploymentEvent(r.Context(), deployment.ID, "failed", "server not connected: "+err.Error()); addErr == nil {
				events.Publish(deployment.ID, event)
			}
			writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"deploymentId": deployment.ID})
	}
}

// handleUploadBackupBlob receives the tar stream an agent PUTs after
// completing a BackupCommand. Authenticated with the agent's own bearer
// credential (validated against the hash stored for the backup's owning
// server), not an admin JWT — this endpoint is called by the agent, not
// the Flutter app, and the agent never has an admin session.
func handleUploadBackupBlob(log *slog.Logger, st *store.Store, blobs *backup.BlobStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backupID := r.PathValue("id")
		b, err := st.GetBackup(r.Context(), backupID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "backup not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load backup", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if !authenticateAgent(r, st, b.ServerID) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		path, size, err := blobs.Save(backupID, r.Body)
		if err != nil {
			log.Error("failed to save backup blob", "backup_id", backupID, "error", err)
			_ = st.UpdateBackupStatus(r.Context(), backupID, "failed", "failed to store blob: "+err.Error())
			http.Error(w, "failed to store blob", http.StatusInternalServerError)
			return
		}

		if err := st.CompleteBackupUpload(r.Context(), backupID, path, size); err != nil {
			log.Error("failed to record backup completion", "backup_id", backupID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDownloadBackupBlob serves a previously uploaded blob back to the
// agent for a restore. Same agent-credential auth as upload.
func handleDownloadBackupBlob(log *slog.Logger, st *store.Store, blobs *backup.BlobStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		backupID := r.PathValue("id")
		b, err := st.GetBackup(r.Context(), backupID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "backup not found", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load backup", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if !authenticateAgent(r, st, b.ServerID) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		path, err := st.GetBackupStoragePath(r.Context(), backupID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "backup has no stored blob", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to look up backup storage path", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		f, err := blobs.Open(path)
		if err != nil {
			log.Error("failed to open backup blob", "backup_id", backupID, "error", err)
			http.Error(w, "failed to open blob", http.StatusInternalServerError)
			return
		}
		defer f.Close()

		if stat, err := f.Stat(); err == nil {
			w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
		}
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = io.Copy(w, f)
	}
}

func authenticateAgent(r *http.Request, st *store.Store, serverID string) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return false
	}
	hash, err := st.GetAgentTokenHash(r.Context(), serverID)
	if err != nil {
		return false
	}
	return auth.HashToken(token) == hash
}
