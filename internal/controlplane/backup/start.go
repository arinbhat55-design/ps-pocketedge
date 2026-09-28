package backup

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// ErrServerNotConnected is returned by Start when the backup row was
// created but its command couldn't reach the agent; the row is already
// marked failed.
type ErrServerNotConnected struct {
	BackupID string
	Err      error
}

func (e *ErrServerNotConnected) Error() string {
	return fmt.Sprintf("server not connected: %v", e.Err)
}

// Request describes one backup to take.
type Request struct {
	Deployment *store.Deployment
	// CreatedBy is empty for a scheduled backup.
	CreatedBy string
	// Origin is "manual" or "scheduled".
	Origin string
	// Quiesce stops the deployment's containers during the snapshot (see
	// agentv1.BackupCommand.quiesce).
	Quiesce          bool
	PostgresLogical  bool
	PostgresUsername string
	PostgresDatabase string
}

// Start records a pending backup and dispatches its BackupCommand. The
// agent reports progress over the session stream and uploads the blob to
// publicURL; completion is recorded by the upload handler. Shared by the
// REST endpoint and the backup scheduler.
func Start(ctx context.Context, log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, publicURL string, req Request) (string, error) {
	dep := req.Deployment
	format := "volumes"
	if req.PostgresLogical {
		format = "postgres_custom"
	}
	backupID, err := st.CreateBackup(ctx, dep.ID, dep.ServerID, req.CreatedBy, req.Origin, req.Quiesce, format)
	if err != nil {
		return "", err
	}
	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_Backup{
			Backup: &agentv1.BackupCommand{
				BackupId:         backupID,
				DeploymentId:     dep.ID,
				UploadUrl:        publicURL + "/api/agent/backups/" + backupID + "/blob",
				Quiesce:          req.Quiesce,
				PostgresLogical:  req.PostgresLogical,
				PostgresUsername: req.PostgresUsername,
				PostgresDatabase: req.PostgresDatabase,
			},
		},
	}
	if err := dispatcher.Send(dep.ServerID, cmd); err != nil {
		log.Warn("failed to dispatch backup command", "backup_id", backupID, "error", err)
		_ = st.UpdateBackupStatus(ctx, backupID, "failed", "server not connected: "+err.Error())
		return backupID, &ErrServerNotConnected{BackupID: backupID, Err: err}
	}
	return backupID, nil
}
