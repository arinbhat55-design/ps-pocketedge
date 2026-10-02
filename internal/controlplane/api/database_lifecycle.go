package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbcatalog"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

func postgresMajor(version string) (int, error) {
	parts := strings.Split(version, ".")
	if len(parts) > 2 || len(parts) == 0 {
		return 0, fmt.Errorf("invalid PostgreSQL version")
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid PostgreSQL version")
	}
	return major, nil
}

func (api *databaseAPI) handleReconfigure() http.HandlerFunc {
	type request struct {
		gateOptions
		Version   string  `json:"version"`
		MemoryMB  int     `json:"memoryMb"`
		CPUs      float64 `json:"cpus"`
		StorageGB int     `json:"storageGb"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.loadInstance(w, r)
		if !ok {
			return
		}
		if !canManageInstance(actorFromRequest(r), inst) {
			http.Error(w, "only the database owner or an admin can change it", http.StatusForbidden)
			return
		}
		if inst.Phase != "running" {
			http.Error(w, "database must be running", http.StatusConflict)
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.Version == "" {
			req.Version = inst.Version
		}
		if req.MemoryMB == 0 {
			req.MemoryMB = inst.MemoryMB
		}
		if req.CPUs == 0 {
			req.CPUs = inst.CPUs
		}
		if req.StorageGB == 0 {
			req.StorageGB = inst.StorageGB
		}
		if inst.Engine == "postgresql" {
			oldMajor, err := postgresMajor(inst.Version)
			if err != nil {
				writeActionError(w, api.log, err)
				return
			}
			newMajor, err := postgresMajor(req.Version)
			if err != nil {
				writeActionError(w, api.log, newActionError(400, "%v", err))
				return
			}
			if oldMajor != newMajor {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "major-version changes require a controlled migration into a new instance; an in-place image swap is unsafe"})
				return
			}
		} else if req.Version != inst.Version {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "version changes for this engine require an engine-specific upgrade path"})
			return
		}
		if req.StorageGB < inst.StorageGB {
			http.Error(w, "storage allocation cannot be reduced", 400)
			return
		}
		if req.Version != inst.Version {
			backups, err := api.st.ListBackupsForDeployment(r.Context(), inst.DeploymentID)
			if err != nil {
				writeActionError(w, api.log, err)
				return
			}
			fresh := false
			for _, b := range backups {
				if b.Status == "completed" && b.Quiesced && b.Format == "volumes" && time.Since(b.CreatedAt) < 24*time.Hour {
					fresh = true
					break
				}
			}
			if !fresh {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "take a completed consistent volume backup within the past 24 hours before changing the PostgreSQL image"})
				return
			}
		}
		engine, _ := dbcatalog.Get(inst.Engine)
		opts, err := engine.Normalize(dbcatalog.Options{Name: inst.Name, Version: req.Version, DatabaseName: inst.DatabaseName, Username: inst.AdminUsername,
			Port: inst.Port, Access: inst.Access, StorageGB: req.StorageGB, MemoryMB: req.MemoryMB, CPUs: req.CPUs,
			HighAvailability: inst.HighAvailability, Profile: inst.Profile})
		if err != nil {
			writeActionError(w, api.log, newActionError(400, "%v", err))
			return
		}
		server, err := api.st.GetServer(r.Context(), inst.ServerID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if err := checkServerCapacity(server, engine, opts); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		plan, err := engine.Render(opts)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if inst.ComposeFileID == nil {
			http.Error(w, "database has no Compose source", 409)
			return
		}
		file, err := api.st.GetComposeFile(r.Context(), *inst.ComposeFileID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		dep, err := api.st.GetDeployment(r.Context(), inst.DeploymentID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		params := actionParams{DatabaseConfig: &databaseConfigParams{InstanceID: inst.ID, Version: opts.Version, MemoryMB: opts.MemoryMB, CPUs: opts.CPUs, StorageGB: opts.StorageGB,
			ComposeYAML: plan.ComposeYAML, ExpectedComposeVersion: file.Version}}
		outcome, err := api.d.submit(r.Context(), dep, actionDatabaseReconfigure, params, actorFromRequest(r), req.gateOptions, false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"deployment": outcome, "warnings": plan.Warnings})
	}
}

func (d *deployer) executeDatabaseReconfigure(ctx context.Context, dep *store.Deployment, p actionParams, a actor) (*actionOutcome, error) {
	c := p.DatabaseConfig
	if c == nil {
		return nil, newActionError(400, "database configuration is missing")
	}
	inst, err := d.st.GetDatabaseInstance(ctx, c.InstanceID)
	if err != nil {
		return nil, err
	}
	if inst.DeploymentID != dep.ID || inst.ComposeFileID == nil {
		return nil, newActionError(409, "database deployment changed while request was pending")
	}
	file, err := d.st.GetComposeFile(ctx, *inst.ComposeFileID)
	if err != nil {
		return nil, err
	}
	if file.Version != c.ExpectedComposeVersion {
		return nil, newActionError(409, "database Compose definition changed while request was pending; review and retry")
	}
	if err := d.st.UpdateComposeFile(ctx, file.ID, file.Name, c.ComposeYAML); err != nil {
		return nil, err
	}
	if err := d.st.UpdateDatabaseConfiguration(ctx, inst.ID, c.Version, c.MemoryMB, c.CPUs, c.StorageGB); err != nil {
		_ = d.st.UpdateComposeFile(ctx, file.ID, file.Name, file.Content)
		return nil, err
	}
	outcome, err := d.executeRollout(ctx, dep, actionDatabaseReconfigure, p, a)
	if err != nil {
		_ = d.st.UpdateComposeFile(ctx, file.ID, file.Name, file.Content)
		_ = d.st.UpdateDatabaseConfiguration(ctx, inst.ID, inst.Version, inst.MemoryMB, inst.CPUs, inst.StorageGB)
		return nil, err
	}
	d.audit(ctx, a, "database.reconfigure", "database", inst.ID, "database configuration changed", map[string]any{"version": c.Version, "memoryMb": c.MemoryMB, "cpus": c.CPUs, "storageGb": c.StorageGB})
	return outcome, nil
}

// Remove a database instance and release its marketplace name and port.
// Docker named volumes and deployment/backup history are retained.
func (api *databaseAPI) handleRemoveDatabase() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.loadInstance(w, r)
		if !ok {
			return
		}
		if !canManageInstance(actorFromRequest(r), inst) {
			http.Error(w, "only the database owner or an admin can remove it", 403)
			return
		}
		requestID, ok := newRequestID(w, api.log)
		if !ok {
			return
		}
		cmd := &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_Undeploy{Undeploy: &agentv1.UndeployCommand{
			RequestId: requestID, ServerId: inst.ServerID, DeploymentId: inst.DeploymentID}}}
		result, err := sendAndAwaitContainerOp(api.dispatcher, api.d.opWaiter, inst.ServerID, requestID, cmd)
		if err != nil {
			writeActionError(w, api.log, newActionError(502, "could not remove database stack: %v", err))
			return
		}
		if !result.GetSuccess() {
			writeActionError(w, api.log, newActionError(409, "could not remove database stack: %s", result.GetErrorMessage()))
			return
		}
		if err := api.st.UpdateDeploymentPhase(r.Context(), inst.DeploymentID, "removed"); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if err := api.st.DeleteDatabaseInstance(r.Context(), inst.ID); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.event(r.Context(), inst.DeploymentID, "removed", "database removed; volumes and backups retained", actorFromRequest(r).ID)
		api.d.audit(r.Context(), actorFromRequest(r), "database.remove", "database", inst.ID, "database instance removed; volumes and backups retained", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}
