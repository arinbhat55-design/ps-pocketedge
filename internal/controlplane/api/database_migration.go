package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/backup"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// A pg_dump custom archive is consistent while PostgreSQL stays online.
// It contains the instance's primary logical database. Ownership and ACLs
// are omitted so the target can use its own administrator credential.
func (api *databaseAPI) handlePostgresLogicalBackup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		dep, err := api.st.GetDeployment(r.Context(), inst.DeploymentID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		backupID, err := backup.Start(r.Context(), api.log, api.st, api.dispatcher, api.publicURL, backup.Request{
			Deployment: dep, CreatedBy: actorFromRequest(r).ID, Origin: "manual", PostgresLogical: true,
			PostgresUsername: inst.AdminUsername, PostgresDatabase: inst.DatabaseName,
		})
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), actorFromRequest(r), "database.logical_backup", "database", inst.ID, "logical PostgreSQL backup requested", map[string]any{"backupId": backupID})
		writeJSON(w, http.StatusAccepted, map[string]string{"backupId": backupID})
	}
}

func (api *databaseAPI) handlePostgresMigrate() http.HandlerFunc {
	type request struct {
		gateOptions
		BackupID string `json:"backupId"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		target, ok := api.postgresInstance(w, r, true)
		if !ok {
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
		if backup.Status != "completed" || backup.Format != "postgres_custom" {
			http.Error(w, "a completed PostgreSQL logical backup is required", 409)
			return
		}
		source, err := api.st.GetDatabaseInstanceByDeployment(r.Context(), backup.DeploymentID)
		if err != nil {
			http.Error(w, "source database is unavailable", 404)
			return
		}
		if !canManageInstance(actorFromRequest(r), source) {
			http.Error(w, "access to the source database is required", 403)
			return
		}
		sourceMajor, _ := postgresMajor(source.Version)
		targetMajor, _ := postgresMajor(target.Version)
		if source.Engine != "postgresql" || sourceMajor >= targetMajor {
			http.Error(w, "target must run a newer PostgreSQL major version", 409)
			return
		}
		// A logical restore cleans only objects present in the archive. Require
		// an empty target so unrelated tables cannot be silently retained.
		objects, err := api.pgCommand(r, target, "", `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND c.relkind IN ('r','p','v','m','S','f')`, false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if objects != "0" {
			http.Error(w, "migration target must be empty", 409)
			return
		}
		dep, err := api.st.GetDeployment(r.Context(), target.DeploymentID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		outcome, err := api.d.submit(r.Context(), dep, actionDatabaseMigration, actionParams{DatabaseMigration: &databaseMigrationParams{TargetID: target.ID, BackupID: backup.ID}}, actorFromRequest(r), req.gateOptions, false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusAccepted, outcome)
	}
}

func (d *deployer) executeDatabaseMigration(ctx context.Context, dep *store.Deployment, p actionParams, a actor) (*actionOutcome, error) {
	m := p.DatabaseMigration
	if m == nil {
		return nil, newActionError(400, "migration request is missing")
	}
	target, err := d.st.GetDatabaseInstance(ctx, m.TargetID)
	if err != nil {
		return nil, err
	}
	if target.DeploymentID != dep.ID || target.Engine != "postgresql" || target.Phase != "running" {
		return nil, newActionError(409, "migration target is not a running PostgreSQL instance")
	}
	b, err := d.st.GetBackup(ctx, m.BackupID)
	if err != nil {
		return nil, err
	}
	if b.Status != "completed" || b.Format != "postgres_custom" {
		return nil, newActionError(409, "migration backup is no longer available")
	}
	source, err := d.st.GetDatabaseInstanceByDeployment(ctx, b.DeploymentID)
	if err != nil {
		return nil, err
	}
	sourceMajor, _ := postgresMajor(source.Version)
	targetMajor, _ := postgresMajor(target.Version)
	if source.Engine != "postgresql" || sourceMajor >= targetMajor {
		return nil, newActionError(409, "migration requires a newer PostgreSQL target")
	}
	downloadURL := d.publicURL + "/api/agent/backups/" + b.ID + "/blob"
	if b.ServerID != target.ServerID {
		if err := d.st.GrantBackupRestore(ctx, b.ID, target.ServerID, time.Now().Add(24*time.Hour)); err != nil {
			return nil, err
		}
		downloadURL += "?serverId=" + target.ServerID
	}
	cmd := &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_Restore{Restore: &agentv1.RestoreCommand{
		BackupId: b.ID, DeploymentId: target.DeploymentID, DownloadUrl: downloadURL, PostgresLogical: true,
		PostgresUsername: target.AdminUsername, PostgresDatabase: target.DatabaseName,
	}}}
	if err := d.dispatcher.Send(target.ServerID, cmd); err != nil {
		return nil, newActionError(409, "target server not connected")
	}
	d.event(ctx, target.DeploymentID, "pending", "PostgreSQL major migration requested from backup "+b.ID, a.ID)
	d.audit(ctx, a, "database.major_migration", "database", target.ID, "PostgreSQL logical migration dispatched", map[string]any{"sourceDatabaseId": source.ID, "backupId": b.ID})
	return &actionOutcome{DeploymentID: target.DeploymentID, Status: "dispatched", Message: "PostgreSQL migration started"}, nil
}
