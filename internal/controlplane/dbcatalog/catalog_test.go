package dbcatalog

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"
	"gopkg.in/yaml.v3"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/compose"
)

// load parses a rendered plan the way the agent does (docker.parseCompose).
func load(t *testing.T, p *Plan, secrets map[string]string) *types.Project {
	t.Helper()
	env := map[string]string{}
	for k, v := range p.Env {
		env[k] = v
	}
	for k, v := range secrets {
		env[k] = v
	}
	project, err := loader.LoadWithContext(context.Background(), types.ConfigDetails{
		ConfigFiles: []types.ConfigFile{{Filename: "compose.yaml", Content: []byte(p.ComposeYAML)}},
		Environment: env,
	}, func(o *loader.Options) {
		o.SetProjectName("test", true)
		o.SkipConsistencyCheck = true
	})
	if err != nil {
		t.Fatalf("compose load failed: %v\n%s", err, p.ComposeYAML)
	}
	return project
}

func TestEveryEngineRendersLoadableCompose(t *testing.T) {
	for _, e := range All() {
		for _, profile := range []string{"development", "production"} {
			for _, access := range []string{"local", "remote"} {
				for _, ha := range []bool{false, true} {
					if ha && e.HighAvailability == "" {
						continue
					}
					name := e.ID + "/" + profile + "/" + access
					if ha {
						name += "/ha"
					}
					t.Run(name, func(t *testing.T) {
						opts := Options{Name: "demo-db", Profile: profile, Access: access, HighAvailability: ha, AcceptLicense: true}
						if e.ID == "mssql" && profile == "production" {
							opts.Edition = "Express"
						}
						eng, _ := Get(e.ID)
						opts, err := eng.Normalize(opts)
						if err != nil {
							t.Fatalf("Normalize: %v", err)
						}
						plan, err := eng.Render(opts)
						if err != nil {
							t.Fatalf("Render: %v", err)
						}

						secretValues := map[string]string{}
						for _, s := range plan.Secrets {
							secretValues[s.EnvVar] = "Sup3rSecretValue" + s.EnvVar
						}
						// Credentials must be interpolations, never literals.
						for _, v := range secretValues {
							if strings.Contains(plan.ComposeYAML, v) {
								t.Fatal("secret value leaked into compose content")
							}
						}
						if e.Auth != AuthNone && len(plan.Secrets) == 0 {
							t.Fatalf("%s authenticates but the plan generates no credential", e.ID)
						}

						project := load(t, plan, secretValues)
						svc, ok := project.Services[plan.Service]
						if !ok {
							t.Fatalf("primary service %q missing", plan.Service)
						}
						if !strings.HasPrefix(svc.Image, e.Image+":") {
							t.Errorf("image = %s", svc.Image)
						}
						if len(svc.Ports) == 0 {
							t.Fatal("primary service publishes no port")
						}
						wantIP := map[string]string{"local": "127.0.0.1", "remote": "0.0.0.0"}[access]
						for _, s := range project.Services {
							for _, p := range s.Ports {
								if p.HostIP != wantIP {
									t.Errorf("service %s port %s bound to %q, want %q", s.Name, p.Published, p.HostIP, wantIP)
								}
							}
							if s.Deploy == nil || s.Deploy.Resources.Limits == nil || s.Deploy.Resources.Limits.MemoryBytes == 0 {
								t.Errorf("service %s has no memory limit", s.Name)
							}
						}
						if svc.Deploy.Resources.Limits.MemoryBytes != types.UnitBytes(opts.MemoryMB)*1024*1024 {
							t.Errorf("memory limit = %d, want %d MB", svc.Deploy.Resources.Limits.MemoryBytes, opts.MemoryMB)
						}
						if e.Persistent && len(project.Volumes) == 0 {
							t.Error("persistent engine declares no volume")
						}
						if ha && len(project.Services) < 2 {
							t.Error("HA option added no service")
						}
						wantRestart := map[string]string{"development": "unless-stopped", "production": "always"}[profile]
						if svc.Restart != wantRestart {
							t.Errorf("restart = %q, want %q", svc.Restart, wantRestart)
						}

						// The deploy engine must support every key rendered.
						var doc yaml.Node
						if err := yaml.Unmarshal([]byte(plan.ComposeYAML), &doc); err != nil {
							t.Fatal(err)
						}
						errs, warnings := compose.Validate(doc.Content[0])
						if len(errs) > 0 || len(warnings) > 0 {
							t.Errorf("Validate: errors=%v warnings=%v", errs, warnings)
						}
					})
				}
			}
		}
	}
}

func TestNormalizeRejectsBadInput(t *testing.T) {
	pg, _ := Get("postgresql")
	cases := map[string]Options{
		"bad name":          {Name: "Bad Name"},
		"unknown version":   {Name: "pg-one", Version: "9.6"},
		"privileged port":   {Name: "pg-one", Port: 80},
		"injection in user": {Name: "pg-one", Username: `x"; DROP TABLE t; --`},
		"reserved prefix":   {Name: "pg-one", Username: "pg_admin"},
		"bad database":      {Name: "pg-one", DatabaseName: "app-db"},
		"too little memory": {Name: "pg-one", MemoryMB: 64},
		"ha unsupported":    {Name: "pg-one", HighAvailability: true},
		"bad access":        {Name: "pg-one", Access: "public"},
	}
	for name, opts := range cases {
		if _, err := pg.Normalize(opts); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	ms, _ := Get("mssql")
	if _, err := ms.Normalize(Options{Name: "sql-one"}); err == nil {
		t.Error("mssql accepted without license acceptance")
	}
	opts, err := ms.Normalize(Options{Name: "sql-one", AcceptLicense: true, Profile: "production"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Username != "sa" {
		t.Errorf("mssql username = %q, want sa", opts.Username)
	}
	if _, err := ms.Render(opts); err == nil {
		t.Error("Developer edition rendered for a production profile")
	}
}

func TestPostgresVersionDataPath(t *testing.T) {
	pg, _ := Get("postgresql")
	for tag, want := range map[string]string{"18": "pgdata:/var/lib/postgresql\n", "17": "pgdata:/var/lib/postgresql/data\n"} {
		opts, _ := pg.Normalize(Options{Name: "pg-one", Version: tag})
		plan, err := pg.Render(opts)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan.ComposeYAML, want) {
			t.Errorf("version %s: missing %q in\n%s", tag, want, plan.ComposeYAML)
		}
	}
}

func TestCommands(t *testing.T) {
	c := CommandContext{Username: "dbadmin", Database: "app", Password: "OldPassw0rd", NewPassword: "NewPassw0rd"}
	expires := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

	pg, _ := Get("postgresql")
	cmd, err := pg.RotateCommand(c)
	if err != nil || !slices.Contains(cmd, `ALTER ROLE "dbadmin" WITH PASSWORD 'NewPassw0rd'`) {
		t.Errorf("postgres rotate = %v, %v", cmd, err)
	}
	cmd, err = pg.CreateTemporaryUserCommand(c, "tmp_abc", "TmpPassw0rd", expires)
	if err != nil || !slices.Contains(cmd, `CREATE ROLE "tmp_abc" LOGIN PASSWORD 'TmpPassw0rd' VALID UNTIL '2030-01-02T03:04:05Z'`) {
		t.Errorf("postgres temp = %v, %v", cmd, err)
	}
	if _, err := pg.CreateTemporaryUserCommand(c, "bad-name", "x", expires); err == nil {
		t.Error("accepted an invalid temporary username")
	}

	my, _ := Get("mysql")
	cmd, _ = my.RotateCommand(c)
	// The current password is a positional parameter, not part of the
	// script.
	if cmd[0] != "sh" || strings.Contains(cmd[2], "OldPassw0rd") || cmd[4] != "OldPassw0rd" {
		t.Errorf("mysql rotate = %v", cmd)
	}

	redis, _ := Get("redis")
	if _, err := redis.RotateCommand(c); err != ErrUnsupported {
		t.Errorf("redis rotates by redeploy, got %v", err)
	}
	cassandra, _ := Get("cassandra")
	if _, err := cassandra.CreateTemporaryUserCommand(c, "tmp_abc", "x", expires); err != ErrUnsupported {
		t.Errorf("cassandra temp users = %v", err)
	}
}

func TestCatalogConsistency(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range All() {
		if seen[e.ID] {
			t.Errorf("duplicate engine id %s", e.ID)
		}
		seen[e.ID] = true
		if len(e.Versions) == 0 || e.build == nil {
			t.Errorf("%s: incomplete definition", e.ID)
		}
		if e.Rotation == RotationExec && (e.commands == nil || e.commands.rotate == nil) {
			t.Errorf("%s: exec rotation without a rotate command", e.ID)
		}
		if e.TemporaryUsers && (e.commands == nil || e.commands.createTemp == nil || e.commands.dropTemp == nil) {
			t.Errorf("%s: temporary users without commands", e.ID)
		}
		if (e.Auth == AuthNone) != (e.Rotation == RotationNone) {
			t.Errorf("%s: auth %s but rotation %s", e.ID, e.Auth, e.Rotation)
		}
		if e.DefaultMemoryMB < e.MinMemoryMB {
			t.Errorf("%s: default memory below minimum", e.ID)
		}
	}
	for _, id := range []string{"postgresql", "mysql", "mariadb", "mssql", "cockroachdb", "mongodb", "couchdb", "cassandra", "redis", "valkey", "memcached", "clickhouse", "duckdb", "timescaledb", "influxdb", "qdrant", "milvus", "weaviate", "chroma"} {
		if !seen[id] {
			t.Errorf("catalog is missing %s", id)
		}
	}
}

func TestExtraPortsShiftWithPrimary(t *testing.T) {
	crdb, _ := Get("cockroachdb")
	if got := crdb.ExtraHostPorts(26257); got[0].Host != 8080 {
		t.Errorf("default primary port: console at %d, want 8080", got[0].Host)
	}
	if got := crdb.ExtraHostPorts(26258); got[0].Host != 8081 {
		t.Errorf("primary 26258: console at %d, want 8081", got[0].Host)
	}
	opts, err := crdb.Normalize(Options{Name: "crdb-two", Port: 26258})
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := crdb.Render(opts)
	if !strings.Contains(plan.ComposeYAML, "127.0.0.1:8081:8080") {
		t.Errorf("console not published at 8081:\n%s", plan.ComposeYAML)
	}
	// 1100 - 26257 shifts the console below 1024.
	if _, err := crdb.Normalize(Options{Name: "crdb-two", Port: 1100}); err == nil {
		t.Error("accepted a primary port that pushes an extra port out of range")
	}
}
