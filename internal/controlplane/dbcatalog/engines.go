package dbcatalog

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	archAll   = []string{"amd64", "arm64", "arm"}
	archAMD64 = []string{"amd64"}
	arch64    = []string{"amd64", "arm64"}
)

// engines is the curated catalog. Versions are listed recommended-first
// and were checked against the registries' published tags; the wizard can
// only deploy a listed tag.
var engines = []Engine{
	// ---- Relational -----------------------------------------------------
	{
		ID: "postgresql", Name: "PostgreSQL", Category: CategoryRelational,
		Description: "The advanced open-source relational database. Tuned shared_buffers and effective_cache_size from the memory limit, data checksums in production.",
		Image:       "postgres",
		Versions: []Version{
			{Tag: "18", Label: "18 (latest)", dataPath: "/var/lib/postgresql"},
			{Tag: "17", Label: "17"},
			{Tag: "16", Label: "16"},
			{Tag: "15", Label: "15"},
		},
		Port: Port{5432, "PostgreSQL"}, Auth: AuthPassword,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		DatabaseNameAllowed: true, DatabaseNameLabel: "Database name",
		Architectures: archAll, Persistent: true,
		MinMemoryMB: 256, DefaultMemoryMB: 1024, DefaultCPUs: 1, DefaultStorageGB: 10,
		Rotation: RotationExec, TemporaryUsers: true,
		License: "PostgreSQL License (permissive open source)", ConnectionScheme: "postgresql",
		dataPath: "/var/lib/postgresql/data",
		build:    buildPostgres,
		commands: postgresCommands,
	},
	{
		ID: "mysql", Name: "MySQL", Category: CategoryRelational,
		Description: "Oracle's widely deployed open-source relational database. InnoDB buffer pool sized from the memory limit.",
		Image:       "mysql",
		Versions: []Version{
			{Tag: "8.4", Label: "8.4 LTS"},
			{Tag: "9.7", Label: "9.7 Innovation"},
		},
		Port: Port{3306, "MySQL"}, Auth: AuthPassword,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		DatabaseNameAllowed: true, DatabaseNameLabel: "Database name",
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 512, DefaultMemoryMB: 1024, DefaultCPUs: 1, DefaultStorageGB: 10,
		Rotation: RotationExec, TemporaryUsers: true,
		License: "GPLv2 (Community Edition)", ConnectionScheme: "mysql",
		Notes:    []string{"The administrator gets full privileges on the named database; root shares the administrator's password and is rotated with it."},
		dataPath: "/var/lib/mysql",
		build:    buildMySQL("mysql"),
		commands: mysqlCommands("mysql"),
	},
	{
		ID: "mariadb", Name: "MariaDB", Category: CategoryRelational,
		Description: "Community-developed MySQL fork with a drop-in compatible protocol. Uses the image's built-in healthcheck.sh.",
		Image:       "mariadb",
		Versions: []Version{
			{Tag: "11.8", Label: "11.8 LTS"},
			{Tag: "11.4", Label: "11.4 LTS"},
			{Tag: "10.11", Label: "10.11 LTS"},
		},
		Port: Port{3306, "MariaDB"}, Auth: AuthPassword,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		DatabaseNameAllowed: true, DatabaseNameLabel: "Database name",
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 256, DefaultMemoryMB: 1024, DefaultCPUs: 1, DefaultStorageGB: 10,
		Rotation: RotationExec, TemporaryUsers: true,
		License: "GPLv2", ConnectionScheme: "mariadb",
		Notes:    []string{"The administrator gets full privileges on the named database; root shares the administrator's password and is rotated with it."},
		dataPath: "/var/lib/mysql",
		build:    buildMySQL("mariadb"),
		commands: mysqlCommands("mariadb"),
	},
	{
		ID: "mssql", Name: "Microsoft SQL Server", Category: CategoryRelational,
		Description: "Microsoft's relational database on Linux. x86-64 hosts only; requires accepting the SQL Server license terms.",
		Image:       "mcr.microsoft.com/mssql/server",
		Versions: []Version{
			{Tag: "2022-latest", Label: "2022"},
			{Tag: "2025-latest", Label: "2025"},
			{Tag: "2019-latest", Label: "2019"},
		},
		Port: Port{1433, "TDS"}, Auth: AuthPassword,
		FixedUsername: "sa", UsernameAllowed: true,
		Architectures: archAMD64, Persistent: true,
		MinMemoryMB: 2048, DefaultMemoryMB: 4096, DefaultCPUs: 2, DefaultStorageGB: 20,
		Rotation:                  RotationExec,
		License:                   "Proprietary (Microsoft SQL Server EULA)",
		RequiresLicenseAcceptance: true,
		Editions:                  []string{"Developer", "Express", "Standard", "Enterprise"},
		Notes: []string{
			"Developer edition is free but licensed for development and testing only; Express is free for production with size limits; Standard and Enterprise require a license you already hold.",
			"Images are published for x86-64 only — ARM servers (Raspberry Pi, Apple silicon) are not supported.",
			"Create databases after deployment (SQL Server has no create-on-start setting).",
		},
		ConnectionScheme: "sqlserver",
		dataPath:         "/var/opt/mssql",
		build:            buildMSSQL,
		commands:         mssqlCommands,
	},
	{
		ID: "cockroachdb", Name: "CockroachDB", Category: CategoryRelational,
		Description: "Distributed, PostgreSQL-compatible SQL database. Runs as a secure single node with auto-generated TLS certificates and a web console on port 8080.",
		Image:       "cockroachdb/cockroach",
		Versions: []Version{
			{Tag: "latest-v26.2", Label: "v26.2"},
			{Tag: "latest-v25.4", Label: "v25.4"},
		},
		Port: Port{26257, "SQL"}, ExtraPorts: []Port{{8080, "DB Console"}}, Auth: AuthPassword,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		DatabaseNameAllowed: true, DatabaseNameLabel: "Database name",
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 1024, DefaultMemoryMB: 2048, DefaultCPUs: 2, DefaultStorageGB: 10,
		Rotation: RotationExec,
		License:  "CockroachDB Software License (free for most uses; see cockroachlabs.com/pricing)",
		Notes: []string{
			"Runs a single node. Multi-node clusters need nodes on separate servers, which this marketplace does not orchestrate yet.",
			"Connect with sslmode=require — the node's certificate is self-signed.",
		},
		ConnectionScheme: "postgresql",
		dataPath:         "/cockroach/cockroach-data",
		build:            buildCockroach,
		commands:         cockroachCommands,
	},

	// ---- NoSQL ----------------------------------------------------------
	{
		ID: "mongodb", Name: "MongoDB", Category: CategoryNoSQL,
		Description: "Document database. Root user created at first start; WiredTiger cache sized from the memory limit.",
		Image:       "mongo",
		Versions: []Version{
			{Tag: "8.0", Label: "8.0"},
			{Tag: "7.0", Label: "7.0"},
		},
		Port: Port{27017, "MongoDB"}, Auth: AuthPassword,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		DatabaseNameAllowed: true, DatabaseNameLabel: "Initial database",
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 512, DefaultMemoryMB: 1024, DefaultCPUs: 1, DefaultStorageGB: 10,
		Rotation: RotationExec, TemporaryUsers: true,
		License: "Server Side Public License (SSPL)", ConnectionScheme: "mongodb",
		Notes:    []string{"ARM hosts need ARMv8.2-A (Raspberry Pi 5 works; Raspberry Pi 4 does not). x86-64 hosts need AVX."},
		dataPath: "/data/db",
		build:    buildMongo,
		commands: mongoCommands,
	},
	{
		ID: "couchdb", Name: "CouchDB", Category: CategoryNoSQL,
		Description: "Document database with an HTTP/JSON API and multi-master replication. Configured as a single node with system databases created automatically.",
		Image:       "couchdb",
		Versions: []Version{
			{Tag: "3.5", Label: "3.5"},
			{Tag: "3.4", Label: "3.4"},
		},
		Port: Port{5984, "HTTP API"}, Auth: AuthPassword,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 256, DefaultMemoryMB: 512, DefaultCPUs: 1, DefaultStorageGB: 10,
		Rotation: RotationExec,
		License:  "Apache 2.0", ConnectionScheme: "http",
		Notes:    []string{"Databases are created through the HTTP API after deployment. The web UI is at /_utils."},
		dataPath: "/opt/couchdb/data",
		build:    buildCouch,
		commands: couchCommands,
	},
	{
		ID: "cassandra", Name: "Apache Cassandra", Category: CategoryNoSQL,
		Description: "Wide-column store built for write-heavy workloads. JVM heap sized from the memory limit.",
		Image:       "cassandra",
		Versions: []Version{
			{Tag: "5.0", Label: "5.0"},
			{Tag: "4.1", Label: "4.1"},
		},
		Port: Port{9042, "CQL"}, Auth: AuthNone,
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 2048, DefaultMemoryMB: 4096, DefaultCPUs: 2, DefaultStorageGB: 20,
		HighAvailability: "Adds a second Cassandra node on the same server that joins the cluster, so a keyspace with replication_factor 2 survives one node process failing. Both nodes get the memory and CPU limits you choose. It does not protect against the server itself going down.",
		Rotation:         RotationNone,
		License:          "Apache 2.0", ConnectionScheme: "cassandra",
		Notes: []string{
			"The official image starts with authentication disabled (AllowAllAuthenticator) — keep access local, or enable PasswordAuthenticator yourself before exposing it.",
			"Create keyspaces with CQL after deployment.",
		},
		dataPath: "/var/lib/cassandra",
		build:    buildCassandra,
	},

	// ---- Cache and key-value -------------------------------------------
	{
		ID: "redis", Name: "Redis", Category: CategoryCache,
		Description: "In-memory data store. Password-protected, append-only persistence, maxmemory set from the memory limit with the noeviction policy so data is never silently dropped.",
		Image:       "redis",
		Versions: []Version{
			{Tag: "8.8", Label: "8.8"},
			{Tag: "8.6", Label: "8.6"},
			{Tag: "7.4", Label: "7.4 (BSD-licensed line)"},
		},
		Port: Port{6379, "RESP"}, Auth: AuthPassword,
		FixedUsername: "default", UsernameAllowed: true,
		Architectures: archAll, Persistent: true,
		MinMemoryMB: 64, DefaultMemoryMB: 512, DefaultCPUs: 1, DefaultStorageGB: 5,
		HighAvailability: "Adds a replica that continuously replicates from the primary and can serve reads. There is no automatic failover (no Sentinel); promote the replica manually if the primary fails.",
		Rotation:         RotationRedeploy,
		License:          "Redis 8: tri-licensed RSALv2 / SSPLv1 / AGPLv3. Redis 7.4: RSALv2 / SSPLv1.", ConnectionScheme: "redis",
		dataPath: "/data",
		build:    buildRedis("redis"),
	},
	{
		ID: "valkey", Name: "Valkey", Category: CategoryCache,
		Description: "Linux Foundation fork of Redis, BSD-licensed and protocol-compatible. Same hardening as the Redis template.",
		Image:       "valkey/valkey",
		Versions: []Version{
			{Tag: "9.1", Label: "9.1"},
			{Tag: "9.0", Label: "9.0"},
			{Tag: "8.1", Label: "8.1"},
		},
		Port: Port{6379, "RESP"}, Auth: AuthPassword,
		FixedUsername: "default", UsernameAllowed: true,
		Architectures: archAll, Persistent: true,
		MinMemoryMB: 64, DefaultMemoryMB: 512, DefaultCPUs: 1, DefaultStorageGB: 5,
		HighAvailability: "Adds a replica that continuously replicates from the primary and can serve reads. There is no automatic failover (no Sentinel); promote the replica manually if the primary fails.",
		Rotation:         RotationRedeploy,
		License:          "BSD 3-Clause", ConnectionScheme: "redis",
		dataPath: "/data",
		build:    buildRedis("valkey"),
	},
	{
		ID: "memcached", Name: "Memcached", Category: CategoryCache,
		Description: "Simple, fast in-memory object cache. Cache size set from the memory limit. Nothing is persisted.",
		Image:       "memcached",
		Versions: []Version{
			{Tag: "1.6", Label: "1.6"},
		},
		Port: Port{11211, "Memcached"}, Auth: AuthNone,
		Architectures: archAll, Persistent: false,
		MinMemoryMB: 64, DefaultMemoryMB: 256, DefaultCPUs: 1,
		Rotation: RotationNone,
		License:  "BSD 3-Clause", ConnectionScheme: "memcached",
		Notes: []string{"Memcached has no authentication in the official image — keep access local."},
		build: buildMemcached,
	},

	// ---- Analytical and data engineering -------------------------------
	{
		ID: "clickhouse", Name: "ClickHouse", Category: CategoryAnalytics,
		Description: "Column-oriented OLAP database for real-time analytics. HTTP interface on the chosen port, native protocol on 9000.",
		Image:       "clickhouse/clickhouse-server",
		Versions: []Version{
			{Tag: "26.3", Label: "26.3 LTS"},
			{Tag: "25.8", Label: "25.8 LTS"},
		},
		Port: Port{8123, "HTTP"}, ExtraPorts: []Port{{9000, "Native"}}, Auth: AuthPassword,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		DatabaseNameAllowed: true, DatabaseNameLabel: "Database name",
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 1024, DefaultMemoryMB: 2048, DefaultCPUs: 2, DefaultStorageGB: 20,
		Rotation: RotationRedeploy, TemporaryUsers: true,
		License: "Apache 2.0", ConnectionScheme: "http",
		dataPath: "/var/lib/clickhouse",
		build:    buildClickHouse,
		commands: clickhouseCommands,
	},
	{
		ID: "duckdb", Name: "DuckDB (GigAPI)", Category: CategoryAnalytics,
		Description: "DuckDB as a network service: GigAPI runs DuckDB over Parquet storage with an HTTP query/ingest API, FlightSQL, and a query UI, protected by HTTP basic auth.",
		Image:       "ghcr.io/gigapi/gigapi",
		Versions: []Version{
			{Tag: "v2.0.44", Label: "2.0.44"},
		},
		Port: Port{7971, "HTTP"}, Auth: AuthPassword,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 512, DefaultMemoryMB: 2048, DefaultCPUs: 2, DefaultStorageGB: 20,
		Rotation: RotationRedeploy,
		License:  "AGPLv3 (GigAPI); DuckDB itself is MIT", ConnectionScheme: "http",
		Notes: []string{
			"DuckDB is an embedded engine with no server of its own; GigAPI is the community project that serves it. GigAPI is an open beta — expect changes.",
			"DuckDB's memory limit is set to 75% of the container limit.",
		},
		dataPath: "/data",
		build:    buildGigAPI,
	},
	{
		ID: "timescaledb", Name: "TimescaleDB", Category: CategoryAnalytics,
		Description: "PostgreSQL extended for time-series: hypertables, continuous aggregates, compression. Same tuning and credential handling as the PostgreSQL template.",
		Image:       "timescale/timescaledb",
		Versions: []Version{
			{Tag: "latest-pg17", Label: "PostgreSQL 17"},
			{Tag: "latest-pg16", Label: "PostgreSQL 16"},
		},
		Port: Port{5432, "PostgreSQL"}, Auth: AuthPassword,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		DatabaseNameAllowed: true, DatabaseNameLabel: "Database name",
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 512, DefaultMemoryMB: 2048, DefaultCPUs: 2, DefaultStorageGB: 20,
		Rotation: RotationExec, TemporaryUsers: true,
		License: "Timescale License (TSL) for the community edition; Apache 2.0 core", ConnectionScheme: "postgresql",
		dataPath: "/var/lib/postgresql/data",
		build:    buildPostgres,
		commands: postgresCommands,
	},
	{
		ID: "influxdb", Name: "InfluxDB", Category: CategoryAnalytics,
		Description: "Time-series database. Initial user, organization, bucket and an admin API token are set up on first start.",
		Image:       "influxdb",
		Versions: []Version{
			{Tag: "2.8", Label: "2.8"},
			{Tag: "2.9", Label: "2.9"},
		},
		Port: Port{8086, "HTTP API"}, Auth: AuthPassword,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		DatabaseNameAllowed: true, DatabaseNameLabel: "Bucket",
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 512, DefaultMemoryMB: 1024, DefaultCPUs: 1, DefaultStorageGB: 20,
		Rotation: RotationExec,
		License:  "MIT", ConnectionScheme: "http",
		Notes:    []string{"The organization is named after the instance. The admin API token is stored in the vault alongside the password."},
		dataPath: "/var/lib/influxdb2",
		build:    buildInflux,
		commands: influxCommands,
	},

	// ---- Vector ---------------------------------------------------------
	{
		ID: "qdrant", Name: "Qdrant", Category: CategoryVector,
		Description: "Vector similarity search engine. REST on the chosen port, gRPC on 6334, protected by a generated API key.",
		Image:       "qdrant/qdrant",
		Versions: []Version{
			{Tag: "v1.19.1", Label: "1.19.1"},
		},
		Port: Port{6333, "REST"}, ExtraPorts: []Port{{6334, "gRPC"}}, Auth: AuthToken,
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 256, DefaultMemoryMB: 1024, DefaultCPUs: 1, DefaultStorageGB: 10,
		Rotation: RotationRedeploy,
		License:  "Apache 2.0", ConnectionScheme: "http",
		Notes:    []string{"Send the API key in the api-key header."},
		dataPath: "/qdrant/storage",
		build:    buildQdrant,
	},
	{
		ID: "milvus", Name: "Milvus", Category: CategoryVector,
		Description: "Cloud-native vector database, deployed standalone with its etcd metadata store and MinIO object store on a private network.",
		Image:       "milvusdb/milvus",
		Versions: []Version{
			{Tag: "v2.6.24", Label: "2.6.24"},
		},
		Port: Port{19530, "gRPC"}, ExtraPorts: []Port{{9091, "Health / metrics"}}, Auth: AuthNone,
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 4096, DefaultMemoryMB: 8192, DefaultCPUs: 2, DefaultStorageGB: 50,
		Rotation: RotationNone,
		License:  "Apache 2.0", ConnectionScheme: "milvus",
		Notes: []string{
			"Milvus user authentication is off by default and is enabled through milvus.yaml, which this template doesn't manage — keep access local.",
			"etcd and MinIO are only reachable from Milvus itself; MinIO keeps its default credentials on that private network.",
			"etcd and MinIO get 512 MB each on top of the memory you choose for Milvus.",
		},
		dataPath: "/var/lib/milvus",
		build:    buildMilvus,
	},
	{
		ID: "weaviate", Name: "Weaviate", Category: CategoryVector,
		Description: "Vector database with hybrid search. Anonymous access disabled; a generated API key is required. REST on the chosen port, gRPC on 50051.",
		Image:       "semitechnologies/weaviate",
		Versions: []Version{
			{Tag: "1.39.6", Label: "1.39.6"},
			{Tag: "1.38.17", Label: "1.38.17"},
		},
		Port: Port{8080, "REST"}, ExtraPorts: []Port{{50051, "gRPC"}}, Auth: AuthToken,
		UsernameAllowed: true, DefaultUsername: "dbadmin",
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 1024, DefaultMemoryMB: 2048, DefaultCPUs: 2, DefaultStorageGB: 20,
		Rotation: RotationRedeploy,
		License:  "BSD 3-Clause", ConnectionScheme: "http",
		Notes:    []string{"Vectorizer modules are disabled — bring your own vectors, or add a module and its API keys later. Send the API key as a Bearer token."},
		dataPath: "/var/lib/weaviate",
		build:    buildWeaviate,
	},
	{
		ID: "chroma", Name: "Chroma", Category: CategoryVector,
		Description: "Lightweight open-source embedding database, popular for RAG prototypes.",
		Image:       "chromadb/chroma",
		Versions: []Version{
			{Tag: "1.5.9", Label: "1.5.9"},
			{Tag: "1.4.1", Label: "1.4.1"},
		},
		Port: Port{8000, "HTTP"}, Auth: AuthNone,
		Architectures: arch64, Persistent: true,
		MinMemoryMB: 512, DefaultMemoryMB: 1024, DefaultCPUs: 1, DefaultStorageGB: 10,
		Rotation: RotationNone,
		License:  "Apache 2.0", ConnectionScheme: "http",
		Notes:    []string{"The Chroma 1.x server has no built-in authentication — keep access local or put an authenticating proxy in front of it."},
		dataPath: "/data",
		build:    buildChroma,
	},
}

// ---- Builders -----------------------------------------------------------

func buildPostgres(b *builder) error {
	o := b.opts
	cmd := []string{
		"postgres",
		"-c", fmt.Sprintf("shared_buffers=%dMB", b.memoryMB(0.25, 32)),
		"-c", fmt.Sprintf("effective_cache_size=%dMB", b.memoryMB(0.75, 64)),
		"-c", "max_connections=" + map[bool]string{true: "200", false: "100"}[o.Profile == "production"],
		"-c", "shared_preload_libraries=pg_stat_statements",
		"-c", "pg_stat_statements.track=all",
		"-c", "track_io_timing=on",
	}
	env := map[string]string{
		"POSTGRES_USER":     o.Username,
		"POSTGRES_PASSWORD": b.adminPassword(),
		"POSTGRES_DB":       o.DatabaseName,
	}
	if o.Profile == "production" {
		// Default from PostgreSQL 18 on; explicit for older versions.
		env["POSTGRES_INITDB_ARGS"] = "--data-checksums"
		cmd = append(cmd, "-c", "log_min_duration_statement=1000", "-c", "log_checkpoints=on")
	}
	b.primary("db", &service{
		Command:     cmd,
		Environment: env,
		Healthcheck: b.health("30s", "CMD-SHELL", fmt.Sprintf("pg_isready -U %s -d %s", o.Username, o.DatabaseName)),
	}, map[string]string{"pgdata": b.dataPath})
	return nil
}

var postgresCommands = &commandSet{
	reservedPrefix: "pg_",
	rotate: func(c CommandContext) []string {
		return psql(c, fmt.Sprintf(`ALTER ROLE "%s" WITH PASSWORD '%s'`, c.Username, c.NewPassword))
	},
	createTemp: func(c CommandContext, user, password string, expires time.Time) []string {
		return psql(c,
			fmt.Sprintf(`CREATE ROLE "%s" LOGIN PASSWORD '%s' VALID UNTIL '%s'`, user, password, expires.UTC().Format(time.RFC3339)),
			fmt.Sprintf(`GRANT pg_read_all_data TO "%s"`, user),
		)
	},
	dropTemp: func(c CommandContext, user string) []string {
		return psql(c,
			fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = '%s'`, user),
			fmt.Sprintf(`DROP ROLE IF EXISTS "%s"`, user),
		)
	},
}

// psql runs statements over the container's local socket, which the
// official image trusts — no password needed on the command line.
func psql(c CommandContext, statements ...string) []string {
	cmd := []string{"psql", "-v", "ON_ERROR_STOP=1", "-U", c.Username, "-d", c.Database}
	for _, s := range statements {
		cmd = append(cmd, "-c", s)
	}
	return cmd
}

func buildMySQL(flavor string) func(b *builder) error {
	return func(b *builder) error {
		o := b.opts
		pw := b.adminPassword()
		prefix, server, health := "MYSQL", "mysqld", []string{"CMD-SHELL", "mysqladmin ping -h 127.0.0.1 --silent"}
		if flavor == "mariadb" {
			prefix, server, health = "MARIADB", "mariadbd", []string{"CMD", "healthcheck.sh", "--connect", "--innodb_initialized"}
		}
		env := map[string]string{
			prefix + "_ROOT_PASSWORD": pw,
			prefix + "_DATABASE":      o.DatabaseName,
		}
		if !strings.EqualFold(o.Username, "root") {
			env[prefix+"_USER"] = o.Username
			env[prefix+"_PASSWORD"] = pw
		}
		maxConn := "151"
		if o.Profile == "production" {
			maxConn = "300"
		}
		b.primary("db", &service{
			Command: []string{
				server,
				fmt.Sprintf("--innodb-buffer-pool-size=%dM", b.memoryMB(0.5, 64)),
				"--max-connections=" + maxConn,
			},
			Environment: env,
			Healthcheck: b.health("60s", health...),
		}, map[string]string{"dbdata": b.dataPath})
		return nil
	}
}

// mysqlCommands runs SQL as root through the image's client. The current
// password travels in MYSQL_PWD (set from a positional parameter), not on
// the client's command line.
func mysqlCommands(client string) *commandSet {
	run := func(c CommandContext, sql string) []string {
		return []string{"sh", "-c", `MYSQL_PWD="$1" exec ` + client + ` -uroot -e "$2"`, "sh", c.Password, sql}
	}
	return &commandSet{
		reservedUsernames: []string{"mysql.sys", "mysql.session", "mysql.infoschema", "mariadb.sys"},
		rotate: func(c CommandContext) []string {
			sql := fmt.Sprintf(`ALTER USER IF EXISTS 'root'@'localhost' IDENTIFIED BY '%[1]s'; ALTER USER IF EXISTS 'root'@'%%' IDENTIFIED BY '%[1]s';`, c.NewPassword)
			if !strings.EqualFold(c.Username, "root") {
				sql += fmt.Sprintf(` ALTER USER IF EXISTS '%s'@'%%' IDENTIFIED BY '%s';`, c.Username, c.NewPassword)
			}
			return run(c, sql)
		},
		createTemp: func(c CommandContext, user, password string, _ time.Time) []string {
			return run(c, fmt.Sprintf("CREATE USER '%[1]s'@'%%' IDENTIFIED BY '%[2]s'; GRANT SELECT, SHOW VIEW ON `%[3]s`.* TO '%[1]s'@'%%';", user, password, c.Database))
		},
		dropTemp: func(c CommandContext, user string) []string {
			// Kill the user's sessions first — DROP USER alone leaves
			// them connected.
			script := `export MYSQL_PWD="$1"
for id in $(` + client + ` -uroot -N -e "SELECT id FROM information_schema.processlist WHERE user = '$2'"); do
  ` + client + ` -uroot -e "KILL $id" || true
done
exec ` + client + ` -uroot -e "DROP USER IF EXISTS '$2'@'%'"`
			return []string{"sh", "-c", script, "sh", c.Password, user}
		},
	}
}

func buildMSSQL(b *builder) error {
	o := b.opts
	if o.Profile == "production" && o.Edition == "Developer" {
		return fmt.Errorf("the Developer edition is licensed for development and testing only; choose Express, Standard or Enterprise for a production profile")
	}
	b.primary("db", &service{
		Environment: map[string]string{
			"ACCEPT_EULA":           "Y",
			"MSSQL_SA_PASSWORD":     b.adminPassword(),
			"MSSQL_PID":             o.Edition,
			"MSSQL_MEMORY_LIMIT_MB": strconv.Itoa(b.memoryMB(0.8, 1536)),
		},
		Healthcheck: b.health("60s", "CMD-SHELL", sqlcmdPrelude+`$S -b -S localhost -U sa -P "$$MSSQL_SA_PASSWORD" -Q "SELECT 1" -o /dev/null`),
	}, map[string]string{"mssqldata": b.dataPath})
	return nil
}

// sqlcmdPrelude picks the image's sqlcmd: newer images ship
// mssql-tools18 (which needs -C to trust the self-signed certificate),
// older ones only mssql-tools.
const sqlcmdPrelude = `if [ -x /opt/mssql-tools18/bin/sqlcmd ]; then S="/opt/mssql-tools18/bin/sqlcmd -C"; else S=/opt/mssql-tools/bin/sqlcmd; fi; `

var mssqlCommands = &commandSet{
	rotate: func(c CommandContext) []string {
		script := sqlcmdPrelude + `exec $S -b -S localhost -U sa -P "$1" -Q "ALTER LOGIN [sa] WITH PASSWORD = '$2' OLD_PASSWORD = '$1'"`
		return []string{"sh", "-c", script, "sh", c.Password, c.NewPassword}
	},
}

func buildCockroach(b *builder) error {
	o := b.opts
	b.primary("db", &service{
		Command: []string{"start-single-node", "--http-addr=0.0.0.0:8080", "--cache=.25", "--max-sql-memory=.25"},
		Environment: map[string]string{
			"COCKROACH_USER":     o.Username,
			"COCKROACH_PASSWORD": b.adminPassword(),
			"COCKROACH_DATABASE": o.DatabaseName,
		},
		// The image's generated node certificate is only valid for
		// 127.0.0.1, and the CLI would otherwise default to
		// COCKROACH_USER from the environment.
		Healthcheck: b.health("60s", "CMD", "cockroach", "node", "status", "--certs-dir=/cockroach/certs", "--host=127.0.0.1:26257", "--user=root"),
	}, map[string]string{"crdata": b.dataPath, "crcerts": "/cockroach/certs"})
	return nil
}

var cockroachCommands = &commandSet{
	reservedUsernames: []string{"root", "admin", "public", "node"},
	rotate: func(c CommandContext) []string {
		return []string{"cockroach", "sql", "--certs-dir=/cockroach/certs", "--host=127.0.0.1:26257", "--user=root",
			"-e", fmt.Sprintf(`ALTER USER "%s" WITH PASSWORD '%s'`, c.Username, c.NewPassword)}
	},
}

func buildMongo(b *builder) error {
	o := b.opts
	// MongoDB's own default: 50% of (RAM - 1 GB), at least 0.25 GB.
	cacheGB := float64(o.MemoryMB-1024) / 2 / 1024
	if cacheGB < 0.25 {
		cacheGB = 0.25
	}
	b.primary("db", &service{
		Command: []string{"mongod", "--wiredTigerCacheSizeGB", strconv.FormatFloat(cacheGB, 'f', 2, 64)},
		Environment: map[string]string{
			"MONGO_INITDB_ROOT_USERNAME": o.Username,
			"MONGO_INITDB_ROOT_PASSWORD": b.adminPassword(),
			"MONGO_INITDB_DATABASE":      o.DatabaseName,
		},
		Healthcheck: b.health("30s", "CMD", "mongosh", "--quiet", "--eval", "db.adminCommand('ping').ok"),
	}, map[string]string{"mongodata": b.dataPath, "mongoconfig": "/data/configdb"})
	return nil
}

var mongoCommands = &commandSet{
	reservedUsernames: []string{"__system"},
	rotate: func(c CommandContext) []string {
		return mongosh(c, fmt.Sprintf(`db.getSiblingDB("admin").changeUserPassword("%s", "%s")`, c.Username, c.NewPassword))
	},
	createTemp: func(c CommandContext, user, password string, _ time.Time) []string {
		return mongosh(c, fmt.Sprintf(`db.getSiblingDB("admin").createUser({user: "%s", pwd: "%s", roles: [{role: "read", db: "%s"}]})`, user, password, c.Database))
	},
	dropTemp: func(c CommandContext, user string) []string {
		return mongosh(c, fmt.Sprintf(`db.getSiblingDB("admin").dropUser("%s")`, user))
	},
}

func mongosh(c CommandContext, eval string) []string {
	return []string{"mongosh", "--quiet", "-u", c.Username, "-p", c.Password, "--authenticationDatabase", "admin", "--eval", eval}
}

func buildCouch(b *builder) error {
	o := b.opts
	// Write a single-node config (so CouchDB creates its system databases
	// itself) and hand over to the image's entrypoint, which sets up the
	// admin and drops root.
	b.primary("db", &service{
		Command: []string{"sh", "-c",
			`printf '[couchdb]\nsingle_node = true\n' > /opt/couchdb/etc/local.d/10-single-node.ini && exec /docker-entrypoint.sh /opt/couchdb/bin/couchdb`},
		Environment: map[string]string{
			"COUCHDB_USER":     o.Username,
			"COUCHDB_PASSWORD": b.adminPassword(),
		},
		Healthcheck: b.health("30s", "CMD-SHELL", "curl -fs http://127.0.0.1:5984/_up"),
	}, map[string]string{"couchdata": b.dataPath})
	return nil
}

var couchCommands = &commandSet{
	rotate: func(c CommandContext) []string {
		script := `exec curl -fsS -u "$1:$2" -X PUT "http://127.0.0.1:5984/_node/_local/_config/admins/$1" -H 'Content-Type: application/json' -d "\"$3\""`
		return []string{"sh", "-c", script, "sh", c.Username, c.Password, c.NewPassword}
	},
}

func buildCassandra(b *builder) error {
	o := b.opts
	heap := b.memoryMB(0.5, 1024)
	if heap > 8192 {
		heap = 8192
	}
	env := func() map[string]string {
		return map[string]string{
			"CASSANDRA_CLUSTER_NAME":    o.Name,
			"CASSANDRA_SEEDS":           "cassandra",
			"CASSANDRA_ENDPOINT_SNITCH": "GossipingPropertyFileSnitch",
			"CASSANDRA_DC":              "dc1",
			"MAX_HEAP_SIZE":             fmt.Sprintf("%dM", heap),
			"HEAP_NEWSIZE":              fmt.Sprintf("%dM", heap/4),
		}
	}
	health := b.health("180s", "CMD-SHELL", "nodetool status 2>/dev/null | grep -q '^UN'")
	b.primary("cassandra", &service{Environment: env(), Healthcheck: health}, map[string]string{"cassandra1": b.dataPath})
	if o.HighAvailability {
		b.addService("cassandra-2", &service{
			Environment: env(),
			Healthcheck: health,
			DependsOn:   map[string]dependsOn{"cassandra": {Condition: "service_healthy"}},
		}, map[string]string{"cassandra2": b.dataPath})
	}
	return nil
}

func buildRedis(flavor string) func(b *builder) error {
	return func(b *builder) error {
		o := b.opts
		server, cli, envVar := flavor+"-server", flavor+"-cli", strings.ToUpper(flavor)+"_PASSWORD"
		maxmemory := fmt.Sprintf("%dmb", b.memoryMB(0.8, 32))
		// Hand off to the image's entrypoint with the server as its
		// first argument, so it still drops root; the password comes
		// from the environment rather than sitting in the container's
		// command line.
		serverCmd := func(extra string) []string {
			return []string{"sh", "-c", fmt.Sprintf(
				`exec docker-entrypoint.sh %s --requirepass "$$%s" --appendonly yes --maxmemory %s --maxmemory-policy noeviction%s`,
				server, envVar, maxmemory, extra)}
		}
		env := map[string]string{envVar: b.secret("DB_PASSWORD", "admin", "Password", o.Username)}
		health := b.health("10s", "CMD-SHELL", fmt.Sprintf(`%s -a "$$%s" --no-auth-warning ping | grep -q PONG`, cli, envVar))
		b.primary(flavor, &service{Command: serverCmd(""), Environment: env, Healthcheck: health}, map[string]string{flavor + "data": b.dataPath})
		if o.HighAvailability {
			b.addService(flavor+"-replica", &service{
				Command:     serverCmd(fmt.Sprintf(` --replicaof %s 6379 --masterauth "$$%s" --replica-read-only yes`, flavor, envVar)),
				Environment: map[string]string{envVar: env[envVar]},
				Healthcheck: health,
				DependsOn:   map[string]dependsOn{flavor: {Condition: "service_healthy"}},
			}, map[string]string{flavor + "replica": b.dataPath})
		}
		return nil
	}
}

func buildMemcached(b *builder) error {
	threads := int(b.opts.CPUs * 2)
	if threads < 1 {
		threads = 1
	}
	b.primary("memcached", &service{
		Command: []string{"memcached", "-m", strconv.Itoa(b.memoryMB(0.9, 32)), "-c", "4096", "-t", strconv.Itoa(threads)},
	}, nil)
	return nil
}

func buildClickHouse(b *builder) error {
	o := b.opts
	b.primary("clickhouse", &service{
		Environment: map[string]string{
			"CLICKHOUSE_DB":                        o.DatabaseName,
			"CLICKHOUSE_USER":                      o.Username,
			"CLICKHOUSE_PASSWORD":                  b.adminPassword(),
			"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1",
		},
		Healthcheck: b.health("30s", "CMD-SHELL", `clickhouse-client --user "$$CLICKHOUSE_USER" --password "$$CLICKHOUSE_PASSWORD" -q "SELECT 1"`),
	}, map[string]string{"chdata": b.dataPath, "chlogs": "/var/log/clickhouse-server"})
	return nil
}

var clickhouseCommands = &commandSet{
	reservedUsernames: []string{"default"},
	createTemp: func(c CommandContext, user, password string, expires time.Time) []string {
		return clickhouseClient(c, fmt.Sprintf("CREATE USER %[1]s IDENTIFIED WITH sha256_password BY '%[2]s' VALID UNTIL '%[3]s'; GRANT SELECT ON %[4]s.* TO %[1]s",
			user, password, expires.UTC().Format("2006-01-02 15:04:05"), c.Database))
	},
	dropTemp: func(c CommandContext, user string) []string {
		return clickhouseClient(c, fmt.Sprintf("KILL QUERY WHERE user = '%[1]s' ASYNC; DROP USER IF EXISTS %[1]s", user))
	},
}

func clickhouseClient(c CommandContext, sql string) []string {
	return []string{"sh", "-c", `exec clickhouse-client --user "$1" --password "$2" --multiquery -q "$3"`, "sh", c.Username, c.Password, sql}
}

func buildGigAPI(b *builder) error {
	o := b.opts
	b.primary("gigapi", &service{
		Environment: map[string]string{
			"GIGAPI_ROOT":              b.dataPath,
			"GIGAPI_LAYERS_0_NAME":     "default",
			"GIGAPI_LAYERS_0_TYPE":     "fs",
			"GIGAPI_LAYERS_0_URL":      "file://" + b.dataPath,
			"HTTP_PORT":                "7971",
			"HTTP_BASIC_AUTH_USERNAME": o.Username,
			"HTTP_BASIC_AUTH_PASSWORD": b.adminPassword(),
			"DUCKDB_MEM_LIMIT":         fmt.Sprintf("%dMB", b.memoryMB(0.75, 256)),
			"DUCKDB_THREAD_LIMIT":      strconv.Itoa(max(1, int(o.CPUs))),
		},
	}, map[string]string{"duckdata": b.dataPath})
	return nil
}

func buildInflux(b *builder) error {
	o := b.opts
	b.primary("influxdb", &service{
		Environment: map[string]string{
			"DOCKER_INFLUXDB_INIT_MODE":        "setup",
			"DOCKER_INFLUXDB_INIT_USERNAME":    o.Username,
			"DOCKER_INFLUXDB_INIT_PASSWORD":    b.adminPassword(),
			"DOCKER_INFLUXDB_INIT_ORG":         o.Name,
			"DOCKER_INFLUXDB_INIT_BUCKET":      o.DatabaseName,
			"DOCKER_INFLUXDB_INIT_ADMIN_TOKEN": b.secret("DB_TOKEN", "token", "Admin API token", ""),
		},
		Healthcheck: b.health("30s", "CMD", "influx", "ping"),
	}, map[string]string{"influxdata": b.dataPath, "influxconfig": "/etc/influxdb2"})
	return nil
}

var influxCommands = &commandSet{
	rotate: func(c CommandContext) []string {
		return []string{"influx", "user", "password", "--name", c.Username, "--password", c.NewPassword, "--token", c.Token}
	},
}

func buildQdrant(b *builder) error {
	b.primary("qdrant", &service{
		Environment: map[string]string{
			"QDRANT__SERVICE__API_KEY":   b.secret("DB_TOKEN", "admin", "API key", ""),
			"QDRANT__TELEMETRY_DISABLED": "true",
		},
	}, map[string]string{"qdrantdata": b.dataPath})
	return nil
}

func buildMilvus(b *builder) error {
	aux := b.resourceLimits(512, 0.5)
	b.addService("etcd", &service{
		Image:   "quay.io/coreos/etcd:v3.5.25",
		Command: []string{"etcd", "-advertise-client-urls=http://etcd:2379", "-listen-client-urls=http://0.0.0.0:2379", "--data-dir=/etcd"},
		Environment: map[string]string{
			"ETCD_AUTO_COMPACTION_MODE":      "revision",
			"ETCD_AUTO_COMPACTION_RETENTION": "1000",
			"ETCD_QUOTA_BACKEND_BYTES":       "4294967296",
			"ETCD_SNAPSHOT_COUNT":            "50000",
		},
		Healthcheck: b.health("30s", "CMD", "etcdctl", "endpoint", "health"),
		Deploy:      aux,
	}, map[string]string{"etcddata": "/etcd"})
	b.addService("minio", &service{
		Image:       "minio/minio:RELEASE.2024-05-28T17-19-04Z",
		Command:     []string{"minio", "server", "/minio_data"},
		Healthcheck: b.health("30s", "CMD", "curl", "-f", "http://localhost:9000/minio/health/live"),
		Deploy:      b.resourceLimits(512, 0.5),
	}, map[string]string{"miniodata": "/minio_data"})
	b.primary("milvus", &service{
		Command: []string{"milvus", "run", "standalone"},
		Environment: map[string]string{
			"ETCD_ENDPOINTS": "etcd:2379",
			"MINIO_ADDRESS":  "minio:9000",
			"MINIO_REGION":   "us-east-1",
		},
		Healthcheck: b.health("90s", "CMD", "curl", "-f", "http://localhost:9091/healthz"),
		DependsOn: map[string]dependsOn{
			"etcd":  {Condition: "service_healthy"},
			"minio": {Condition: "service_healthy"},
		},
	}, map[string]string{"milvusdata": b.dataPath})
	return nil
}

func buildWeaviate(b *builder) error {
	o := b.opts
	b.primary("weaviate", &service{
		Command: []string{"--host", "0.0.0.0", "--port", "8080", "--scheme", "http"},
		Environment: map[string]string{
			"AUTHENTICATION_ANONYMOUS_ACCESS_ENABLED": "false",
			"AUTHENTICATION_APIKEY_ENABLED":           "true",
			"AUTHENTICATION_APIKEY_ALLOWED_KEYS":      b.secret("DB_TOKEN", "admin", "API key", o.Username),
			"AUTHENTICATION_APIKEY_USERS":             o.Username,
			"PERSISTENCE_DATA_PATH":                   b.dataPath,
			"DEFAULT_VECTORIZER_MODULE":               "none",
			"CLUSTER_HOSTNAME":                        "node1",
			"LIMIT_RESOURCES":                         "true",
			"GOMEMLIMIT":                              fmt.Sprintf("%dMiB", b.memoryMB(0.8, 256)),
		},
		Healthcheck: b.health("30s", "CMD-SHELL", "wget -q --spider http://localhost:8080/v1/.well-known/ready"),
	}, map[string]string{"weaviatedata": b.dataPath})
	return nil
}

func buildChroma(b *builder) error {
	b.primary("chroma", &service{
		Environment: map[string]string{"ANONYMIZED_TELEMETRY": "FALSE"},
	}, map[string]string{"chromadata": b.dataPath})
	return nil
}
