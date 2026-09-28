package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/backup"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbcatalog"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbops"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/vault"
)

// databaseAPI serves the Database Marketplace: the engine catalog, the
// one-click deployment wizard, deployed instances, and their credentials.
type databaseAPI struct {
	log        *slog.Logger
	st         *store.Store
	d          *deployer
	vault      *vault.Vault
	ops        *dbops.Ops
	dispatcher *deploy.Dispatcher
	publicURL  string
}

// ---- Catalog --------------------------------------------------------------

func (api *databaseAPI) handleListEngines() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, dbcatalog.All())
	}
}

// ---- Wizard ---------------------------------------------------------------

// databaseRequest is the deployment wizard's submission.
type databaseRequest struct {
	gateOptions
	Engine           string  `json:"engine"`
	CloneBackupID    string  `json:"cloneBackupId"`
	Version          string  `json:"version"`
	Name             string  `json:"name"`
	ServerID         string  `json:"serverId"`
	DatabaseName     string  `json:"databaseName"`
	Username         string  `json:"username"`
	Port             int     `json:"port"`
	Access           string  `json:"access"`
	StorageGB        int     `json:"storageGb"`
	MemoryMB         int     `json:"memoryMb"`
	CPUs             float64 `json:"cpus"`
	HighAvailability bool    `json:"highAvailability"`
	Profile          string  `json:"profile"`
	Edition          string  `json:"edition"`
	AcceptLicense    bool    `json:"acceptLicense"`
	Backup           struct {
		Cron           string `json:"cron"`
		Consistent     *bool  `json:"consistent"`
		RetentionDays  int    `json:"retentionDays"`
		RetentionCount int    `json:"retentionCount"`
	} `json:"backup"`
	// Governance metadata a production environment policy may require.
	ChangeRequest string `json:"changeRequest"`
	RollbackPlan  string `json:"rollbackPlan"`
	Notes         string `json:"notes"`
}

func (req *databaseRequest) options() dbcatalog.Options {
	return dbcatalog.Options{
		Name: req.Name, Version: req.Version, DatabaseName: req.DatabaseName, Username: req.Username,
		Port: req.Port, Access: req.Access, StorageGB: req.StorageGB, MemoryMB: req.MemoryMB, CPUs: req.CPUs,
		HighAvailability: req.HighAvailability, Profile: req.Profile, Edition: req.Edition, AcceptLicense: req.AcceptLicense,
	}
}

// backupPolicy validates the request's backup settings.
func backupPolicyFrom(cronExpr string, consistent *bool, retentionDays, retentionCount int, persistent bool, now time.Time) (store.BackupPolicy, error) {
	p := store.BackupPolicy{Consistent: true, RetentionDays: retentionDays, RetentionCount: retentionCount}
	if consistent != nil {
		p.Consistent = *consistent
	}
	if retentionDays < 0 || retentionDays > 3650 || retentionCount < 0 || retentionCount > 1000 {
		return p, newActionError(http.StatusBadRequest, "retention must be 0-3650 days and 0-1000 backups")
	}
	cronExpr = strings.TrimSpace(cronExpr)
	if cronExpr == "" {
		return p, nil
	}
	if !persistent {
		return p, newActionError(http.StatusBadRequest, "this engine keeps no data on disk, so there is nothing to back up")
	}
	schedule, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return p, newActionError(http.StatusBadRequest, "invalid backup schedule: %v", err)
	}
	// Schedules are UTC (the client converts local times); robfig
	// evaluates in the location of the time it's given.
	next := schedule.Next(now.UTC())
	// Guard against a schedule that would back up every minute.
	if schedule.Next(next).Sub(next) < 15*time.Minute {
		return p, newActionError(http.StatusBadRequest, "backups can run at most every 15 minutes")
	}
	p.Cron, p.NextRunAt = &cronExpr, &next
	return p, nil
}

// planned is a validated, rendered wizard submission.
type planned struct {
	engine *dbcatalog.Engine
	opts   dbcatalog.Options
	plan   *dbcatalog.Plan
	server *store.Server
	policy store.BackupPolicy
}

// prepare validates a submission against the catalog and the target
// server and renders it. Shared by preview and create, so a preview that
// succeeds means create will accept the same input.
func (api *databaseAPI) prepare(ctx context.Context, req *databaseRequest) (*planned, error) {
	engine, ok := dbcatalog.Get(req.Engine)
	if !ok {
		return nil, newActionError(http.StatusBadRequest, "unknown database engine %q", req.Engine)
	}
	opts, err := engine.Normalize(req.options())
	if err != nil {
		return nil, newActionError(http.StatusBadRequest, "%v", err)
	}
	plan, err := engine.Render(opts)
	if err != nil {
		return nil, newActionError(http.StatusBadRequest, "%v", err)
	}
	policy, err := backupPolicyFrom(req.Backup.Cron, req.Backup.Consistent, req.Backup.RetentionDays, req.Backup.RetentionCount, engine.Persistent, time.Now())
	if err != nil {
		return nil, err
	}
	if req.ServerID == "" {
		return nil, newActionError(http.StatusBadRequest, "serverId is required")
	}
	server, err := api.st.GetServer(ctx, req.ServerID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && server.Status == "removed") {
		return nil, newActionError(http.StatusNotFound, "server not found")
	}
	if err != nil {
		return nil, err
	}
	if !engine.SupportsArch(server.Arch) {
		return nil, newActionError(http.StatusBadRequest, "%s images are not published for %s servers (supported: %s)", engine.Name, server.Arch, strings.Join(engine.Architectures, ", "))
	}
	if err := checkServerCapacity(server, engine, opts); err != nil {
		return nil, err
	}

	if taken, err := api.st.DatabaseNameTaken(ctx, opts.Name); err != nil {
		return nil, err
	} else if taken {
		return nil, newActionError(http.StatusConflict, "a database named %q already exists", opts.Name)
	}
	ports := []int{opts.Port}
	for _, p := range engine.ExtraHostPorts(opts.Port) {
		ports = append(ports, p.Host)
	}
	for _, port := range ports {
		if name, taken, err := api.st.DatabasePortTaken(ctx, server.ID, port); err != nil {
			return nil, err
		} else if taken {
			return nil, newActionError(http.StatusConflict, "port %d on this server is already assigned to database %q", port, name)
		}
		if containerID, found, err := api.st.FindPortConflict(ctx, server.ID, uint16(port), "tcp"); err != nil {
			return nil, err
		} else if found {
			return nil, newActionError(http.StatusConflict, "port %d is already published by container %s on this server", port, shortID(containerID))
		}
	}
	return &planned{engine: engine, opts: opts, plan: plan, server: server, policy: policy}, nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// checkServerCapacity rejects a request that can't fit the server at all.
// Capacity is only known from agents new enough to report it; unknown
// passes.
func checkServerCapacity(server *store.Server, engine *dbcatalog.Engine, opts dbcatalog.Options) error {
	if len(server.LastResources) == 0 {
		return nil
	}
	var res store.ResourceSnapshot
	if err := json.Unmarshal(server.LastResources, &res); err != nil {
		return nil
	}
	replicas := 1
	if opts.HighAvailability {
		replicas = 2
	}
	if res.TotalMemoryBytes > 0 {
		want := uint64(opts.MemoryMB*replicas) << 20
		if want > res.TotalMemoryBytes {
			return newActionError(http.StatusBadRequest, "%d MB of memory requested but %s only has %d MB", opts.MemoryMB*replicas, server.Name, res.TotalMemoryBytes>>20)
		}
	}
	if res.NumCPUs > 0 && opts.CPUs > float64(res.NumCPUs) {
		return newActionError(http.StatusBadRequest, "%.2g CPUs requested but %s only has %d", opts.CPUs, server.Name, res.NumCPUs)
	}
	if engine.Persistent && res.TotalDiskBytes > 0 {
		free := float64(res.TotalDiskBytes) * (1 - res.DiskPercent/100)
		want := float64(uint64(opts.StorageGB*replicas) << 30)
		if want > free {
			return newActionError(http.StatusBadRequest, "%d GB of storage requested but %s has about %.0f GB free", opts.StorageGB*replicas, server.Name, free/(1<<30))
		}
	}
	return nil
}

type databasePreview struct {
	ComposeYAML      string               `json:"composeYaml"`
	ExtraPorts       []dbcatalog.HostPort `json:"extraPorts"`
	Warnings         []string             `json:"warnings"`
	ConnectionString string               `json:"connectionString"`
	Username         string               `json:"username"`
	Credentials      []string             `json:"credentials"`
	PrimaryService   string               `json:"primaryService"`
}

func (api *databaseAPI) handlePreview() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req databaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		p, err := api.prepare(r.Context(), &req)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		creds := make([]string, 0, len(p.plan.Secrets))
		for _, s := range p.plan.Secrets {
			creds = append(creds, s.Name)
		}
		writeJSON(w, http.StatusOK, databasePreview{
			ComposeYAML:      p.plan.ComposeYAML,
			ExtraPorts:       p.engine.ExtraHostPorts(p.opts.Port),
			Warnings:         nonNil(p.plan.Warnings),
			ConnectionString: p.engine.ConnectionString(connectionHost(p.server, p.opts.Access), p.opts.Port, p.opts.Username, p.opts.DatabaseName),
			Username:         p.opts.Username,
			Credentials:      creds,
			PrimaryService:   p.plan.Service,
		})
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// connectionHost is the host a client uses: the server's hostname for
// remote access, loopback (i.e. from the server itself, or through an SSH
// tunnel) for local.
func connectionHost(server *store.Server, access string) string {
	if access == "remote" && server.Hostname != "" {
		return server.Hostname
	}
	return "127.0.0.1"
}

// handleCreate is the one-click deployment: render, generate and vault
// the credentials, save the Compose file, and deploy it through the same
// governance path as any deployment.
func (api *databaseAPI) handleCreate() http.HandlerFunc {
	type response struct {
		Database   databaseView   `json:"database"`
		Deployment *actionOutcome `json:"deployment,omitempty"`
		Warnings   []string       `json:"warnings"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		var req databaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		p, err := api.prepare(ctx, &req)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if req.CloneBackupID != "" {
			if p.engine.ID != "postgresql" {
				writeActionError(w, api.log, newActionError(400, "cloning from a backup currently supports PostgreSQL"))
				return
			}
			b, err := api.st.GetBackup(ctx, req.CloneBackupID)
			if err != nil || b.Status != "completed" || b.Format != "volumes" || !b.Quiesced {
				writeActionError(w, api.log, newActionError(409, "clone requires a completed consistent volume backup"))
				return
			}
			source, err := api.st.GetDatabaseInstanceByDeployment(ctx, b.DeploymentID)
			if err != nil || !canManageInstance(a, source) {
				writeActionError(w, api.log, newActionError(403, "access to the clone source is required"))
				return
			}
			sourceMajor, _ := postgresMajor(source.Version)
			targetMajor, _ := postgresMajor(p.opts.Version)
			if source.Engine != "postgresql" || sourceMajor != targetMajor || source.AdminUsername != p.opts.Username || source.DatabaseName != p.opts.DatabaseName {
				writeActionError(w, api.log, newActionError(409, "clone source and target must have the same PostgreSQL major version, administrator, and primary database name"))
				return
			}
		}

		// Credentials first: the deployment's env refers to them.
		env := map[string]string{}
		for k, v := range p.plan.Env {
			env[k] = v
		}
		var secretIDs []string
		var adminSecretID string
		cleanup := func() {
			if len(secretIDs) > 0 {
				_ = api.st.DeleteSecrets(context.WithoutCancel(ctx), secretIDs)
			}
		}
		for _, spec := range p.plan.Secrets {
			value, err := vault.GeneratePassword(32)
			if err != nil {
				cleanup()
				writeActionError(w, api.log, err)
				return
			}
			id, err := api.vault.Create(ctx, vault.NewSecret{Name: spec.Name, Kind: spec.Kind, Username: spec.Username, Value: value, OwnerID: a.ID})
			if err != nil {
				cleanup()
				writeActionError(w, api.log, err)
				return
			}
			secretIDs = append(secretIDs, id)
			if spec.Kind == "admin" {
				adminSecretID = id
			}
			env[spec.EnvVar] = vault.Reference(id)
		}

		fileID, err := api.st.CreateComposeFile(ctx, "db-"+p.opts.Name, p.plan.ComposeYAML, a.ID)
		if err != nil {
			cleanup()
			if strings.Contains(err.Error(), "idx_compose_files_name") {
				writeActionError(w, api.log, newActionError(http.StatusConflict, "a Compose file named db-%s already exists", p.opts.Name))
				return
			}
			writeActionError(w, api.log, err)
			return
		}

		environment := p.opts.Profile
		n := store.NewDeployment{
			ComposeFileID: &fileID,
			ServerID:      p.server.ID,
			Env:           env,
			CreatedBy:     a.ID,
			Environment:   &environment,
			Tags:          []string{"database", p.engine.ID},
			ChangeRequest: req.ChangeRequest,
			RollbackPlan:  req.RollbackPlan,
			Notes:         req.Notes,
		}
		summary := fmt.Sprintf("deployment of %s %s database %s created", p.engine.Name, p.opts.Version, p.opts.Name)
		outcome, deploymentID, deployErr := api.d.create(ctx, a, n, req.gateOptions, summary)
		if deploymentID == "" {
			// Nothing was created that references the credentials or the
			// file; don't leave them behind.
			cleanup()
			_ = api.st.DeleteComposeFile(context.WithoutCancel(ctx), fileID)
			writeActionError(w, api.log, deployErr)
			return
		}

		instanceID, err := api.st.InsertDatabaseInstance(ctx, store.NewDatabaseInstance{
			Name: p.opts.Name, Engine: p.engine.ID, Version: p.opts.Version,
			DeploymentID: deploymentID, ComposeFileID: fileID, ServerID: p.server.ID,
			PrimaryService: p.plan.Service, DatabaseName: p.opts.DatabaseName, AdminUsername: p.opts.Username,
			Port: p.opts.Port, Access: p.opts.Access, Profile: p.opts.Profile,
			StorageGB: p.opts.StorageGB, MemoryMB: p.opts.MemoryMB, CPUs: p.opts.CPUs,
			HighAvailability: p.opts.HighAvailability, AdminSecretID: adminSecretID,
			BackupPolicy: p.policy, CreatedBy: a.ID,
		})
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if err := api.st.LinkSecretsToDatabase(ctx, instanceID, secretIDs); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if req.CloneBackupID != "" {
			if err := api.st.CreateDatabaseCloneJob(ctx, instanceID, req.CloneBackupID); err != nil {
				writeActionError(w, api.log, err)
				return
			}
		}
		api.d.audit(ctx, a, "database.create", "database", instanceID, summary, map[string]any{
			"engine": p.engine.ID, "version": p.opts.Version, "serverId": p.server.ID,
			"access": p.opts.Access, "profile": p.opts.Profile, "highAvailability": p.opts.HighAvailability,
		})

		inst, err := api.st.GetDatabaseInstance(ctx, instanceID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		view := api.view(inst)
		if deployErr != nil {
			var ae *actionError
			if errors.As(deployErr, &ae) {
				if ae.extra == nil {
					ae.extra = map[string]any{}
				}
				ae.extra["deploymentId"] = deploymentID
				ae.extra["databaseId"] = instanceID
			}
			writeActionError(w, api.log, deployErr)
			return
		}
		writeJSON(w, http.StatusAccepted, response{Database: view, Deployment: outcome, Warnings: nonNil(p.plan.Warnings)})
	}
}

// ---- Instances ------------------------------------------------------------

// databaseView is an instance as the API presents it.
type databaseView struct {
	store.DatabaseInstance
	EngineName       string               `json:"engineName"`
	Category         string               `json:"category"`
	ConnectionString string               `json:"connectionString"`
	Host             string               `json:"host"`
	Rotation         string               `json:"rotation"`
	TemporaryUsers   bool                 `json:"temporaryUsers"`
	Persistent       bool                 `json:"persistent"`
	ExtraPorts       []dbcatalog.HostPort `json:"extraPorts"`
}

func (api *databaseAPI) view(inst *store.DatabaseInstance) databaseView {
	v := databaseView{DatabaseInstance: *inst, EngineName: inst.Engine}
	server := &store.Server{Hostname: inst.ServerHostname}
	v.Host = connectionHost(server, inst.Access)
	if e, ok := dbcatalog.Get(inst.Engine); ok {
		v.EngineName, v.Category = e.Name, string(e.Category)
		v.ConnectionString = e.ConnectionString(v.Host, inst.Port, inst.AdminUsername, inst.DatabaseName)
		v.Rotation, v.TemporaryUsers, v.Persistent = string(e.Rotation), e.TemporaryUsers, e.Persistent
		v.ExtraPorts = e.ExtraHostPorts(inst.Port)
	}
	return v
}

func (api *databaseAPI) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := api.st.ListDatabaseInstances(r.Context())
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		out := make([]databaseView, 0, len(list))
		for i := range list {
			out = append(out, api.view(&list[i]))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (api *databaseAPI) loadInstance(w http.ResponseWriter, r *http.Request) (*store.DatabaseInstance, bool) {
	inst, err := api.st.GetDatabaseInstance(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "database not found", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		writeActionError(w, api.log, err)
		return nil, false
	}
	return inst, true
}

func (api *databaseAPI) handleGet() http.HandlerFunc {
	type response struct {
		databaseView
		// Not "engine": that key is the instance's engine ID.
		Engine    *dbcatalog.Engine       `json:"engineInfo,omitempty"`
		Backups   []store.Backup          `json:"backups"`
		Clone     *store.DatabaseCloneJob `json:"clone,omitempty"`
		CanManage bool                    `json:"canManage"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.loadInstance(w, r)
		if !ok {
			return
		}
		backups, err := api.st.ListBackupsForDeployment(r.Context(), inst.DeploymentID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		engine, _ := dbcatalog.Get(inst.Engine)
		clone, err := api.st.GetDatabaseCloneJob(r.Context(), inst.ID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusOK, response{databaseView: api.view(inst), Engine: engine, Backups: backups, Clone: clone, CanManage: canManageInstance(actorFromRequest(r), inst)})
	}
}

// canManageInstance: the instance's creator, or an admin.
func canManageInstance(a actor, inst *store.DatabaseInstance) bool {
	return a.IsAdmin || (inst.CreatedBy != nil && *inst.CreatedBy == a.ID)
}

func (api *databaseAPI) handleUpdateBackupPolicy() http.HandlerFunc {
	type request struct {
		Cron           string `json:"cron"`
		Consistent     *bool  `json:"consistent"`
		RetentionDays  int    `json:"retentionDays"`
		RetentionCount int    `json:"retentionCount"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		inst, ok := api.loadInstance(w, r)
		if !ok {
			return
		}
		if !canManageInstance(a, inst) {
			http.Error(w, "only the database's owner or an admin can change its backup policy", http.StatusForbidden)
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		engine, _ := dbcatalog.Get(inst.Engine)
		persistent := engine == nil || engine.Persistent
		policy, err := backupPolicyFrom(req.Cron, req.Consistent, req.RetentionDays, req.RetentionCount, persistent, time.Now())
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if err := api.st.UpdateDatabaseBackupPolicy(r.Context(), inst.ID, policy); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), a, "database.backup_policy", "database", inst.ID, "backup policy of database "+inst.Name+" updated", map[string]any{
			"cron": policy.Cron, "consistent": policy.Consistent, "retentionDays": policy.RetentionDays, "retentionCount": policy.RetentionCount,
		})
		updated, err := api.st.GetDatabaseInstance(r.Context(), inst.ID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusOK, api.view(updated))
	}
}

func (api *databaseAPI) handleCreateBackup() http.HandlerFunc {
	type request struct {
		Consistent *bool `json:"consistent"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		inst, ok := api.loadInstance(w, r)
		if !ok {
			return
		}
		if engine, ok := dbcatalog.Get(inst.Engine); ok && !engine.Persistent {
			http.Error(w, "this engine keeps no data on disk, so there is nothing to back up", http.StatusBadRequest)
			return
		}
		quiesce := inst.BackupConsistent
		if r.ContentLength > 0 {
			var req request
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
				http.Error(w, "invalid backup request", http.StatusBadRequest)
				return
			}
			if req.Consistent != nil {
				if !canManageInstance(a, inst) {
					http.Error(w, "only the database owner or an admin can override backup consistency", http.StatusForbidden)
					return
				}
				quiesce = *req.Consistent
			}
		}
		dep, err := api.st.GetDeployment(r.Context(), inst.DeploymentID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		backupID, err := backup.Start(r.Context(), api.log, api.st, api.dispatcher, api.publicURL, backup.Request{
			Deployment: dep, CreatedBy: a.ID, Origin: "manual", Quiesce: quiesce,
		})
		var notConnected *backup.ErrServerNotConnected
		if errors.As(err, &notConnected) {
			writeJSON(w, http.StatusConflict, map[string]string{"backupId": backupID, "error": "server not connected"})
			return
		}
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"backupId": backupID})
	}
}

// ---- Credentials ----------------------------------------------------------

// credentialView is a secret's metadata plus what the caller may do with
// it. The value itself is never part of it.
type credentialView struct {
	store.Secret
	Masked    string `json:"masked"`
	Active    bool   `json:"active"`
	CanReveal bool   `json:"canReveal"`
	CanManage bool   `json:"canManage"`
}

const maskedValue = "••••••••••••"

// canManageSecret: the secret's owner, or an admin.
func canManageSecret(a actor, sec *store.Secret) bool {
	return a.IsAdmin || (sec.OwnerID != nil && *sec.OwnerID == a.ID)
}

// canAccessSecret: managers, plus users holding an unexpired grant.
func (api *databaseAPI) canAccessSecret(ctx context.Context, a actor, sec *store.Secret) (bool, error) {
	if canManageSecret(a, sec) {
		return true, nil
	}
	return api.st.HasActiveSecretGrant(ctx, sec.ID, a.ID, time.Now())
}

func (api *databaseAPI) credentialView(ctx context.Context, a actor, sec store.Secret) (credentialView, error) {
	v := credentialView{Secret: sec, Masked: maskedValue, Active: sec.Active(time.Now()), CanManage: canManageSecret(a, &sec)}
	ok, err := api.canAccessSecret(ctx, a, &sec)
	v.CanReveal = ok && v.Active
	return v, err
}

func (api *databaseAPI) handleListCredentials() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		inst, ok := api.loadInstance(w, r)
		if !ok {
			return
		}
		secrets, err := api.st.ListSecretsForDatabase(r.Context(), inst.ID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		out := make([]credentialView, 0, len(secrets))
		for _, s := range secrets {
			v, err := api.credentialView(r.Context(), a, s)
			if err != nil {
				writeActionError(w, api.log, err)
				return
			}
			out = append(out, v)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// loadSecret loads the path's secret and checks the caller may at least
// access it.
func (api *databaseAPI) loadSecret(w http.ResponseWriter, r *http.Request, a actor) (*store.Secret, bool) {
	sec, err := api.st.GetSecret(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "credential not found", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		writeActionError(w, api.log, err)
		return nil, false
	}
	ok, err := api.canAccessSecret(r.Context(), a, sec)
	if err != nil {
		writeActionError(w, api.log, err)
		return nil, false
	}
	if !ok {
		// Same response as a missing secret: don't confirm it exists.
		http.Error(w, "credential not found", http.StatusNotFound)
		return nil, false
	}
	return sec, true
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

func (api *databaseAPI) handleReveal() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		sec, ok := api.loadSecret(w, r, a)
		if !ok {
			return
		}
		if !sec.Active(time.Now()) {
			http.Error(w, "this credential has expired or been revoked", http.StatusGone)
			return
		}
		_, value, err := api.vault.Open(r.Context(), sec.ID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), a, "secret.reveal", "secret", sec.ID, "credential "+sec.Name+" revealed", map[string]any{"databaseId": sec.DatabaseID, "version": sec.Version})
		noStore(w)
		writeJSON(w, http.StatusOK, map[string]any{"username": sec.Username, "value": value, "version": sec.Version})
	}
}

// handleDownload serves the credential as an .env file, once per value:
// a second download is refused until the credential is rotated.
func (api *databaseAPI) handleDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		sec, ok := api.loadSecret(w, r, a)
		if !ok {
			return
		}
		if !sec.Active(time.Now()) {
			http.Error(w, "this credential has expired or been revoked", http.StatusGone)
			return
		}
		if sec.DatabaseID == nil {
			http.Error(w, "credential is not attached to a database", http.StatusBadRequest)
			return
		}
		inst, err := api.st.GetDatabaseInstance(r.Context(), *sec.DatabaseID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		_, value, err := api.vault.Open(r.Context(), sec.ID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		claimed, err := api.st.ClaimSecretDownload(r.Context(), sec.ID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if !claimed {
			writeJSON(w, http.StatusGone, map[string]string{"error": "these credentials were already downloaded; reveal them, or rotate to download a new copy"})
			return
		}
		api.d.audit(r.Context(), a, "secret.download", "secret", sec.ID, "credential "+sec.Name+" downloaded", map[string]any{"databaseId": inst.ID, "version": sec.Version})

		view := api.view(inst)
		username := sec.Username
		if username == "" {
			username = inst.AdminUsername
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# %s credentials for %s (%s %s)\n", sec.Name, inst.Name, view.EngineName, inst.Version)
		fmt.Fprintf(&b, "# Downloaded %s. This file can only be downloaded once per credential version —\n", time.Now().UTC().Format(time.RFC3339))
		fmt.Fprintf(&b, "# store it in a password manager and delete this copy.\n")
		if sec.ExpiresAt != nil {
			fmt.Fprintf(&b, "# Expires %s.\n", sec.ExpiresAt.UTC().Format(time.RFC3339))
		}
		fmt.Fprintf(&b, "DB_HOST=%s\nDB_PORT=%d\n", view.Host, inst.Port)
		if username != "" {
			fmt.Fprintf(&b, "DB_USERNAME=%s\n", username)
		}
		if inst.DatabaseName != "" {
			fmt.Fprintf(&b, "DB_NAME=%s\n", inst.DatabaseName)
		}
		switch sec.Kind {
		case "token":
			fmt.Fprintf(&b, "DB_TOKEN=%s\n", value)
		default:
			if engine, ok := dbcatalog.Get(inst.Engine); ok && engine.Auth == dbcatalog.AuthToken {
				fmt.Fprintf(&b, "DB_API_KEY=%s\n", value)
			} else {
				fmt.Fprintf(&b, "DB_PASSWORD=%s\n", value)
				if view.ConnectionString != "" {
					url := strings.Replace(view.ConnectionString, "<password>", value, 1)
					if sec.Kind == "temporary" {
						url = strings.Replace(url, inst.AdminUsername+":", username+":", 1)
					}
					fmt.Fprintf(&b, "DB_URL=%s\n", url)
				}
			}
		}
		noStore(w)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.env"`, inst.Name, strings.ReplaceAll(strings.ToLower(sec.Kind), " ", "-")))
		_, _ = w.Write([]byte(b.String()))
	}
}

// handleRotate replaces a database's admin credential. Engines that can
// change it in place do so without a restart; engines that read it from
// the environment at startup get the new value on a redeploy, which goes
// through the deployment's governance gate like any other.
func (api *databaseAPI) handleRotate() http.HandlerFunc {
	type response struct {
		Credential credentialView `json:"credential"`
		Method     string         `json:"method"`
		Deployment *actionOutcome `json:"deployment,omitempty"`
		Message    string         `json:"message"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		sec, ok := api.loadSecret(w, r, a)
		if !ok {
			return
		}
		if !canManageSecret(a, sec) {
			http.Error(w, "only the credential's owner or an admin can rotate it", http.StatusForbidden)
			return
		}
		if sec.Kind != "admin" || sec.DatabaseID == nil {
			http.Error(w, "only a database's administrator credential can be rotated; issue a new temporary credential instead", http.StatusBadRequest)
			return
		}
		var gate gateOptions
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&gate); err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
		}
		ctx := r.Context()
		inst, err := api.st.GetDatabaseInstance(ctx, *sec.DatabaseID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		engine, ok := dbcatalog.Get(inst.Engine)
		if !ok {
			http.Error(w, "unknown engine", http.StatusInternalServerError)
			return
		}

		resp := response{Method: string(engine.Rotation)}
		switch engine.Rotation {
		case dbcatalog.RotationExec:
			unsaved, err := api.ops.RotateInPlace(ctx, inst, engine)
			if unsaved != "" {
				noStore(w)
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error":       err.Error(),
					"newPassword": unsaved,
				})
				return
			}
			if err != nil {
				writeRotationError(w, api.log, err)
				return
			}
			resp.Message = "Password changed in the running database. Existing connections stay open; new connections need the new password."

		case dbcatalog.RotationRedeploy:
			_, oldValue, err := api.vault.Open(ctx, sec.ID)
			if err != nil {
				writeActionError(w, api.log, err)
				return
			}
			newValue, err := vault.GeneratePassword(32)
			if err != nil {
				writeActionError(w, api.log, err)
				return
			}
			if err := api.vault.Replace(ctx, sec.ID, newValue); err != nil {
				writeActionError(w, api.log, err)
				return
			}
			dep, err := api.st.GetDeployment(ctx, inst.DeploymentID)
			if err == nil {
				resp.Deployment, err = api.d.submit(ctx, dep, actionRedeploy, actionParams{}, a, gate, false)
			}
			if err != nil {
				// The running database still uses the old value; put it
				// back so the vault keeps matching reality.
				if rerr := api.vault.Replace(context.WithoutCancel(ctx), sec.ID, oldValue); rerr != nil {
					api.log.Error("failed to restore credential after a failed rotation", "secret_id", sec.ID, "error", rerr)
				}
				writeActionError(w, api.log, err)
				return
			}
			switch resp.Deployment.Status {
			case "dispatched", "completed":
				resp.Message = "New credential stored; the database is being redeployed to apply it (a brief restart)."
			default:
				resp.Message = "New credential stored. It takes effect when the redeploy runs (" + strings.ReplaceAll(resp.Deployment.Status, "_", " ") + "); until then the database still accepts the previous one."
			}

		default:
			http.Error(w, engine.Name+" has no credential to rotate", http.StatusBadRequest)
			return
		}

		api.d.audit(ctx, a, "secret.rotate", "secret", sec.ID, "credential "+sec.Name+" of database "+inst.Name+" rotated", map[string]any{"databaseId": inst.ID, "method": resp.Method})
		updated, err := api.st.GetSecret(ctx, sec.ID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		resp.Credential, err = api.credentialView(ctx, a, *updated)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// writeRotationError maps credential-operation failures to responses.
func writeRotationError(w http.ResponseWriter, log *slog.Logger, err error) {
	var cmdErr *dbops.CommandError
	switch {
	case errors.Is(err, deploy.ErrAgentNotConnected):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
	case errors.Is(err, dbcatalog.ErrUnsupported):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.As(err, &cmdErr):
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the database rejected the change: " + cmdErr.Error()})
	default:
		var ae *actionError
		if errors.As(err, &ae) {
			writeActionError(w, log, err)
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
}

func (api *databaseAPI) handleCreateTemporary() http.HandlerFunc {
	type request struct {
		TTLMinutes int `json:"ttlMinutes"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		inst, ok := api.loadInstance(w, r)
		if !ok {
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		engine, ok := dbcatalog.Get(inst.Engine)
		if !ok || !engine.TemporaryUsers {
			http.Error(w, "temporary credentials are not supported for this engine", http.StatusBadRequest)
			return
		}
		// Issuing a login is as sensitive as holding the admin credential.
		if inst.AdminSecretID == nil {
			http.Error(w, "database has no administrator credential", http.StatusBadRequest)
			return
		}
		admin, err := api.st.GetSecret(r.Context(), *inst.AdminSecretID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if ok, err := api.canAccessSecret(r.Context(), a, admin); err != nil {
			writeActionError(w, api.log, err)
			return
		} else if !ok {
			http.Error(w, "you need access to this database's administrator credential to issue temporary ones", http.StatusForbidden)
			return
		}

		ttl := time.Duration(req.TTLMinutes) * time.Minute
		id, err := api.ops.CreateTemporaryUser(r.Context(), inst, engine, a.ID, ttl)
		if err != nil {
			if strings.HasPrefix(err.Error(), "lifetime must be") {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeRotationError(w, api.log, err)
			return
		}
		sec, err := api.st.GetSecret(r.Context(), id)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), a, "secret.issue_temporary", "secret", id, "temporary credential "+sec.Username+" issued for database "+inst.Name, map[string]any{"databaseId": inst.ID, "expiresAt": sec.ExpiresAt})
		view, err := api.credentialView(r.Context(), a, *sec)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusCreated, view)
	}
}

func (api *databaseAPI) handleRevoke() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		sec, ok := api.loadSecret(w, r, a)
		if !ok {
			return
		}
		if !canManageSecret(a, sec) {
			http.Error(w, "only the credential's owner or an admin can revoke it", http.StatusForbidden)
			return
		}
		if sec.Kind != "temporary" || sec.DatabaseID == nil {
			http.Error(w, "only temporary credentials can be revoked; rotate an administrator credential instead", http.StatusBadRequest)
			return
		}
		if sec.RevokedAt != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		inst, err := api.st.GetDatabaseInstance(r.Context(), *sec.DatabaseID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		engine, _ := dbcatalog.Get(inst.Engine)
		if engine == nil {
			http.Error(w, "unknown engine", http.StatusInternalServerError)
			return
		}
		if err := api.ops.DropTemporaryUser(r.Context(), inst, engine, sec); err != nil {
			writeRotationError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), a, "secret.revoke", "secret", sec.ID, "temporary credential "+sec.Username+" revoked", map[string]any{"databaseId": inst.ID})
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- Sharing --------------------------------------------------------------

func (api *databaseAPI) handleListGrants() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		sec, ok := api.loadSecret(w, r, a)
		if !ok {
			return
		}
		if !canManageSecret(a, sec) {
			http.Error(w, "only the credential's owner or an admin can see who it is shared with", http.StatusForbidden)
			return
		}
		grants, err := api.st.ListSecretGrants(r.Context(), sec.ID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusOK, grants)
	}
}

func (api *databaseAPI) handlePutGrant() http.HandlerFunc {
	type request struct {
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		sec, ok := api.loadSecret(w, r, a)
		if !ok {
			return
		}
		if !canManageSecret(a, sec) {
			http.Error(w, "only the credential's owner or an admin can share it", http.StatusForbidden)
			return
		}
		var req request
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
		}
		if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
			http.Error(w, "expiresAt must be in the future", http.StatusBadRequest)
			return
		}
		userID := r.PathValue("userId")
		user, err := api.st.GetUserByID(r.Context(), userID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if sec.OwnerID != nil && *sec.OwnerID == user.ID {
			http.Error(w, "that user already owns this credential", http.StatusBadRequest)
			return
		}
		if err := api.st.UpsertSecretGrant(r.Context(), sec.ID, user.ID, a.ID, req.ExpiresAt); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), a, "secret.share", "secret", sec.ID, "credential "+sec.Name+" shared with "+user.Email, map[string]any{"userId": user.ID, "expiresAt": req.ExpiresAt})
		grants, err := api.st.ListSecretGrants(r.Context(), sec.ID)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusOK, grants)
	}
}

func (api *databaseAPI) handleDeleteGrant() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := actorFromRequest(r)
		sec, ok := api.loadSecret(w, r, a)
		if !ok {
			return
		}
		if !canManageSecret(a, sec) {
			http.Error(w, "only the credential's owner or an admin can stop sharing it", http.StatusForbidden)
			return
		}
		userID := r.PathValue("userId")
		if err := api.st.DeleteSecretGrant(r.Context(), sec.ID, userID); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), a, "secret.unshare", "secret", sec.ID, "credential "+sec.Name+" no longer shared with user "+userID, map[string]any{"userId": userID})
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleUserDirectory lists users' IDs and emails for the share picker —
// the admin-only /api/users carries roles and more.
func handleUserDirectory(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type entry struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := st.ListUsers(r.Context())
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		out := make([]entry, 0, len(users))
		for _, u := range users {
			out = append(out, entry{ID: u.ID, Email: u.Email})
		}
		writeJSON(w, http.StatusOK, out)
	}
}
