package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbops"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/vault"
)

// These commands use the PostgreSQL client inside the primary container. The
// local socket uses the image's local authentication for connection. This is intentionally specific to
// PostgreSQL; other catalog engines have different administration semantics.
const postgresCommandTimeout = 30 * time.Second

var pgIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

func pgIdent(s string) (string, error) {
	if !pgIdentifier.MatchString(s) || strings.HasPrefix(strings.ToLower(s), "pg_") {
		return "", newActionError(http.StatusBadRequest, "invalid PostgreSQL identifier %q", s)
	}
	return `"` + s + `"`, nil
}

func pgLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (api *databaseAPI) postgresInstance(w http.ResponseWriter, r *http.Request, manage bool) (*store.DatabaseInstance, bool) {
	inst, ok := api.loadInstance(w, r)
	if !ok {
		return nil, false
	}
	if inst.Engine != "postgresql" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "this operation currently supports PostgreSQL instances"})
		return nil, false
	}
	if manage && !canManageInstance(actorFromRequest(r), inst) {
		http.Error(w, "only the database owner or an admin can manage it", http.StatusForbidden)
		return nil, false
	}
	if inst.Phase != "running" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "database must be running"})
		return nil, false
	}
	return inst, true
}

func (api *databaseAPI) pgCommand(r *http.Request, inst *store.DatabaseInstance, database, sql string, csv bool) (string, error) {
	if database == "" {
		database = inst.DatabaseName
	}
	if database == "" {
		database = "postgres"
	}
	argv := []string{"psql", "-X", "-v", "ON_ERROR_STOP=1", "-P", "pager=off", "-U", inst.AdminUsername, "-d", database}
	if csv {
		argv = append(argv, "--csv", "-P", "footer=off")
	} else {
		argv = append(argv, "-A", "-t")
	}
	argv = append(argv, "-c", sql)
	limit := 1 << 20
	if csv {
		limit = 8 << 20
	}
	result, err := deploy.RunCommandWithLimit(r.Context(), api.dispatcher, api.ops.Relay(), inst.ServerID, dbops.PrimaryContainer(inst), argv, postgresCommandTimeout, limit)
	if err != nil {
		return "", newActionError(http.StatusBadGateway, "database command could not run: %v", err)
	}
	if result.ExitCode != 0 {
		return "", newActionError(http.StatusBadRequest, "PostgreSQL rejected the command: %s", strings.TrimSpace(result.Output))
	}
	if result.Truncated {
		return "", newActionError(http.StatusRequestEntityTooLarge, "result exceeds the %d MB download limit; narrow the query", limit>>20)
	}
	return strings.TrimSpace(strings.ReplaceAll(result.Output, "\r", "")), nil
}

func (api *databaseAPI) pgJSON(w http.ResponseWriter, r *http.Request, inst *store.DatabaseInstance, sql string) {
	out, err := api.pgCommand(r, inst, "", sql, false)
	if err != nil {
		writeActionError(w, api.log, err)
		return
	}
	var value any
	if err := json.Unmarshal([]byte(out), &value); err != nil {
		writeActionError(w, api.log, fmt.Errorf("invalid PostgreSQL response: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (api *databaseAPI) handlePostgresConnectionTest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, false)
		if !ok {
			return
		}
		started := time.Now()
		out, err := api.pgCommand(r, inst, "", "SELECT jsonb_build_object('connected',true,'serverVersion',current_setting('server_version'),'database',current_database())", false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		result["latencyMs"] = time.Since(started).Milliseconds()
		writeJSON(w, http.StatusOK, result)
	}
}

func (api *databaseAPI) handlePostgresOverview() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, false)
		if !ok {
			return
		}
		overview, err := api.ops.PostgresOverview(r.Context(), inst)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusOK, overview)
	}
}

func (api *databaseAPI) handlePostgresMetrics() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, false)
		if !ok {
			return
		}
		samples, err := api.st.ListDatabaseMetricSamples(r.Context(), inst.ID, time.Now().Add(-24*time.Hour))
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		var rate float64
		var growth int64
		if len(samples) > 1 {
			last, prior := samples[len(samples)-1], samples[len(samples)-2]
			seconds := last.RecordedAt.Sub(prior.RecordedAt).Seconds()
			if seconds > 0 && last.TransactionsTotal >= prior.TransactionsTotal {
				rate = float64(last.TransactionsTotal-prior.TransactionsTotal) / seconds
			}
			growth = last.DatabaseBytes - prior.DatabaseBytes
		}
		writeJSON(w, http.StatusOK, map[string]any{"samples": samples, "transactionRate": rate, "storageGrowthBytes": growth})
	}
}

func (api *databaseAPI) handlePostgresAlerts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, false)
		if !ok {
			return
		}
		alerts, err := api.st.ListDatabaseAlerts(r.Context(), inst.ID, false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		writeJSON(w, http.StatusOK, alerts)
	}
}

const pgSessionsSQL = `SELECT COALESCE(jsonb_agg(row ORDER BY (row->>'durationSeconds')::numeric DESC), '[]'::jsonb) FROM (
 SELECT jsonb_build_object('pid',pid,'user',usename,'database',datname,'state',state,
 'waitEventType',wait_event_type,'waitEvent',wait_event,'query',left(query,2000),
 'durationSeconds',round(extract(epoch FROM now()-COALESCE(query_start,backend_start))::numeric,1),
 'transactionSeconds',CASE WHEN xact_start IS NULL THEN NULL ELSE round(extract(epoch FROM now()-xact_start)::numeric,1) END,
 'application',application_name,'client',client_addr::text) AS row
 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() LIMIT 100
) s`

func (api *databaseAPI) handlePostgresSessions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		api.pgJSON(w, r, inst, pgSessionsSQL)
	}
}

const pgSlowQueriesSQL = `SELECT COALESCE(jsonb_agg(entry ORDER BY (entry->>'meanMs')::numeric DESC), '[]'::jsonb) FROM (
 SELECT jsonb_build_object('query',left(query,2000),'calls',calls,'meanMs',round(mean_exec_time::numeric,2),
 'maxMs',round(max_exec_time::numeric,2),'totalMs',round(total_exec_time::numeric,2),'rows',rows) AS entry
 FROM pg_stat_statements WHERE dbid=(SELECT oid FROM pg_database WHERE datname=current_database())
 ORDER BY mean_exec_time DESC LIMIT 50
) s`

// pgInsightsStateSQL reports whether pg_stat_statements can be queried:
// "missing" (extension not created), "not_preloaded" (created, but the
// server wasn't started with it, so reads fail), or "ok".
const pgInsightsStateSQL = `SELECT CASE
 WHEN NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname='pg_stat_statements') THEN 'missing'
 WHEN NOT ('pg_stat_statements' = ANY(string_to_array(replace(current_setting('shared_preload_libraries'),' ',''),','))) THEN 'not_preloaded'
 ELSE 'ok' END`

func (api *databaseAPI) handlePostgresSlowQueries() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		state, err := api.pgCommand(r, inst, "", pgInsightsStateSQL, false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		switch state {
		case "missing":
			writeJSON(w, http.StatusConflict, map[string]any{"error": "Query insights are not enabled for this database yet. Use Enable query insights to start recording.", "insightsEnabled": false})
			return
		case "not_preloaded":
			writeJSON(w, http.StatusConflict, map[string]any{"error": "Query insights need the current PostgreSQL template. Apply it from Version & resources, then retry.", "insightsEnabled": false})
			return
		}
		api.pgJSON(w, r, inst, pgSlowQueriesSQL)
	}
}

func (api *databaseAPI) handleEnablePostgresInsights() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		preload, err := api.pgCommand(r, inst, "", "SHOW shared_preload_libraries", false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if !strings.Contains(preload, "pg_stat_statements") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "pg_stat_statements is not preloaded; apply the current PostgreSQL template in Version & resources, then retry"})
			return
		}
		_, err = api.pgCommand(r, inst, "", "CREATE EXTENSION IF NOT EXISTS pg_stat_statements", false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), actorFromRequest(r), "database.query_insights", "database", inst.ID, "PostgreSQL query statistics enabled", nil)
		w.WriteHeader(http.StatusNoContent)
	}
}

const pgSizesSQL = `SELECT jsonb_build_object(
 'databases',(SELECT COALESCE(jsonb_agg(jsonb_build_object('name',datname,'bytes',pg_database_size(oid)) ORDER BY datname),'[]'::jsonb) FROM pg_database WHERE datallowconn),
 'tables',(SELECT COALESCE(jsonb_agg(jsonb_build_object('schema',schemaname,'name',relname,'tableBytes',pg_relation_size(relid),'indexBytes',pg_indexes_size(relid),'totalBytes',pg_total_relation_size(relid)) ORDER BY pg_total_relation_size(relid) DESC),'[]'::jsonb) FROM (SELECT * FROM pg_stat_user_tables ORDER BY pg_total_relation_size(relid) DESC LIMIT 100) t)
)`

func (api *databaseAPI) handlePostgresSizes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, false)
		if !ok {
			return
		}
		api.pgJSON(w, r, inst, pgSizesSQL)
	}
}

func (api *databaseAPI) handlePostgresQuery() http.HandlerFunc {
	type request struct {
		SQL      string `json:"sql"`
		Database string `json:"database"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		var req request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "invalid query request", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.SQL) == "" {
			http.Error(w, "sql is required", http.StatusBadRequest)
			return
		}
		if req.Database != "" {
			if _, err := pgIdent(req.Database); err != nil {
				writeActionError(w, api.log, err)
				return
			}
		}
		out, err := api.pgCommand(r, inst, req.Database, req.SQL, true)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), actorFromRequest(r), "database.query", "database", inst.ID, "SQL query executed", map[string]any{"database": req.Database, "sqlLength": len(req.SQL)})
		noStore(w)
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="query-results.csv"`)
		_, _ = w.Write([]byte(out + "\n"))
	}
}

func (api *databaseAPI) handlePostgresCreateDatabase() http.HandlerFunc {
	type request struct {
		Name string `json:"name"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		var req request
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		name, err := pgIdent(req.Name)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		owner, err := pgIdent(inst.AdminUsername)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		_, err = api.pgCommand(r, inst, "", fmt.Sprintf("CREATE DATABASE %s OWNER %s", name, owner), false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), actorFromRequest(r), "database.logical_create", "database", inst.ID, "logical database created", map[string]any{"name": req.Name})
		writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
	}
}

func (api *databaseAPI) handlePostgresDeleteDatabase() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		name := r.PathValue("name")
		if name == inst.DatabaseName || name == "postgres" || name == "template1" || name == "template0" {
			http.Error(w, "cannot delete the instance's primary or system database", 400)
			return
		}
		ident, err := pgIdent(name)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		_, err = api.pgCommand(r, inst, "", fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", ident), false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), actorFromRequest(r), "database.logical_delete", "database", inst.ID, "logical database deleted", map[string]any{"name": name})
		w.WriteHeader(http.StatusNoContent)
	}
}

func (api *databaseAPI) handlePostgresCreateUser() http.HandlerFunc {
	type request struct {
		Username   string `json:"username"`
		Database   string `json:"database"`
		Permission string `json:"permission"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		var req request
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		user, err := pgIdent(req.Username)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if req.Database == "" {
			req.Database = inst.DatabaseName
		}
		if _, err = pgIdent(req.Database); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		// Validate before CREATE ROLE so a bad request never leaves a role
		// behind for the rollback below to clean up.
		if err := validatePostgresGrant(inst, req.Username, req.Permission); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		password, err := vault.GeneratePassword(32)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		_, err = api.pgCommand(r, inst, "", fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD %s", user, pgLiteral(password)), false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if err = api.setPostgresPermission(r, inst, req.Username, req.Database, req.Permission); err != nil {
			_ = api.setPostgresPermission(r, inst, req.Username, req.Database, "none")
			_, _ = api.pgCommand(r, inst, "", "DROP ROLE "+user, false)
			writeActionError(w, api.log, err)
			return
		}
		id, err := api.vault.Create(r.Context(), vault.NewSecret{Name: "Managed PostgreSQL user", Kind: "managed", Username: req.Username, Value: password, OwnerID: actorFromRequest(r).ID, DatabaseID: inst.ID})
		if err != nil {
			_ = api.setPostgresPermission(r, inst, req.Username, req.Database, "none")
			_, _ = api.pgCommand(r, inst, "", "DROP ROLE "+user, false)
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), actorFromRequest(r), "database.user_create", "database", inst.ID, "managed user created", map[string]any{"username": req.Username, "database": req.Database, "permission": req.Permission})
		writeJSON(w, http.StatusCreated, map[string]string{"username": req.Username, "secretId": id})
	}
}

func (api *databaseAPI) setPostgresPermission(r *http.Request, inst *store.DatabaseInstance, username, database, permission string) error {
	user, err := pgIdent(username)
	if err != nil {
		return err
	}
	db, err := pgIdent(database)
	if err != nil {
		return err
	}
	if err := validatePostgresGrant(inst, username, permission); err != nil {
		return err
	}
	// Database CONNECT is database-scoped. Table grants also need to run in
	// the target database; default privileges cover future tables owned by
	// this instance's administrator.
	_, err = api.pgCommand(r, inst, "", fmt.Sprintf("REVOKE ALL ON DATABASE %s FROM %s", db, user), false)
	if err != nil {
		return err
	}
	owner, _ := pgIdent(inst.AdminUsername)
	cleanup := []string{
		fmt.Sprintf("REVOKE ALL ON SCHEMA public FROM %s", user),
		fmt.Sprintf("REVOKE ALL ON ALL TABLES IN SCHEMA public FROM %s", user),
		fmt.Sprintf("REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM %s", user),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public REVOKE ALL ON TABLES FROM %s", owner, user),
		fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public REVOKE ALL ON SEQUENCES FROM %s", owner, user),
	}
	for _, sql := range cleanup {
		if _, err = api.pgCommand(r, inst, database, sql, false); err != nil {
			return err
		}
	}
	if permission == "none" {
		_, err = api.pgCommand(r, inst, "", "ALTER ROLE "+user+" NOLOGIN", false)
		if err != nil {
			return err
		}
		_, err = api.pgCommand(r, inst, "", fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename=%s AND pid<>pg_backend_pid()", pgLiteral(username)), false)
		return err
	}
	_, err = api.pgCommand(r, inst, "", fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s", db, user), false)
	if err != nil {
		return err
	}
	statements := []string{fmt.Sprintf("GRANT USAGE ON SCHEMA public TO %s", user), fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s", user), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT SELECT ON TABLES TO %s", owner, user)}
	if permission == "write" {
		statements = append(statements, fmt.Sprintf("GRANT INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO %s", user), fmt.Sprintf("GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO %s", user), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT INSERT,UPDATE,DELETE ON TABLES TO %s", owner, user), fmt.Sprintf("ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT USAGE,SELECT ON SEQUENCES TO %s", owner, user))
	}
	for _, sql := range statements {
		if _, err = api.pgCommand(r, inst, database, sql, false); err != nil {
			return err
		}
	}
	_, err = api.pgCommand(r, inst, "", "ALTER ROLE "+user+" LOGIN", false)
	return err
}

func validatePostgresGrant(inst *store.DatabaseInstance, username, permission string) error {
	if permission != "read" && permission != "write" && permission != "none" {
		return newActionError(400, "permission must be read, write, or none")
	}
	if strings.EqualFold(username, inst.AdminUsername) || strings.EqualFold(username, "postgres") {
		return newActionError(400, "cannot change the instance administrator's permissions")
	}
	return nil
}

func (api *databaseAPI) handlePostgresPermissions() http.HandlerFunc {
	type request struct {
		Database   string `json:"database"`
		Permission string `json:"permission"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		var req request
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.Database == "" {
			req.Database = inst.DatabaseName
		}
		if err := api.setPostgresPermission(r, inst, r.PathValue("name"), req.Database, req.Permission); err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), actorFromRequest(r), "database.permissions", "database", inst.ID, "user permissions updated", map[string]any{"username": r.PathValue("name"), "database": req.Database, "permission": req.Permission})
		w.WriteHeader(http.StatusNoContent)
	}
}

func (api *databaseAPI) handlePostgresTerminateSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		pid, err := strconv.Atoi(r.PathValue("pid"))
		if err != nil || pid <= 0 {
			http.Error(w, "invalid pid", 400)
			return
		}
		out, err := api.pgCommand(r, inst, "", fmt.Sprintf("SELECT pg_terminate_backend(%d) FROM pg_stat_activity WHERE pid=%d AND datname=current_database() AND pid<>pg_backend_pid()", pid, pid), false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		if out != "t" {
			http.Error(w, "session not found or not terminated", 404)
			return
		}
		api.d.audit(r.Context(), actorFromRequest(r), "database.session_terminate", "database", inst.ID, "session terminated", map[string]any{"pid": pid})
		w.WriteHeader(http.StatusNoContent)
	}
}

func (api *databaseAPI) handlePostgresConnectionLimit() http.HandlerFunc {
	type request struct {
		Database string `json:"database"`
		Limit    int    `json:"limit"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		inst, ok := api.postgresInstance(w, r, true)
		if !ok {
			return
		}
		var req request
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if req.Limit < -1 || req.Limit > 100000 {
			http.Error(w, "limit must be -1 (unlimited) or 0-100000", 400)
			return
		}
		if req.Database == "" {
			req.Database = inst.DatabaseName
		}
		db, err := pgIdent(req.Database)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		_, err = api.pgCommand(r, inst, "", fmt.Sprintf("ALTER DATABASE %s CONNECTION LIMIT %d", db, req.Limit), false)
		if err != nil {
			writeActionError(w, api.log, err)
			return
		}
		api.d.audit(r.Context(), actorFromRequest(r), "database.connection_limit", "database", inst.ID, "connection limit updated", map[string]any{"database": req.Database, "limit": req.Limit})
		w.WriteHeader(http.StatusNoContent)
	}
}
