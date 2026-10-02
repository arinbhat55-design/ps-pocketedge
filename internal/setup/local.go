package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/config"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/state"
	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

type secrets struct {
	DatabasePassword string `json:"databasePassword"`
	JWTSecret        string `json:"jwtSecret"`
	AdminPassword    string `json:"adminPassword"`
}

func loadSecrets(root string) (secrets, error) {
	var s secrets
	b, err := os.ReadFile(filepath.Join(root, "credentials.json"))
	if err == nil {
		err = json.Unmarshal(b, &s)
		if err == nil && (s.DatabasePassword == "" || s.JWTSecret == "" || s.AdminPassword == "") {
			err = errors.New("saved credentials are incomplete; restore credentials.json before retrying")
		}
		return s, err
	}
	if !os.IsNotExist(err) {
		return s, err
	}
	if s.DatabasePassword, err = randomSecret(); err != nil {
		return s, err
	}
	if s.JWTSecret, err = randomSecret(); err != nil {
		return s, err
	}
	if s.AdminPassword, err = randomSecret(); err != nil {
		return s, err
	}
	err = writeJSON(filepath.Join(root, "credentials.json"), s)
	return s, err
}

func (e *Engine) ensureBrew(ctx context.Context, p Plan) (string, error) {
	if brew := brewPath(); brew != "" {
		version, err := e.command(ctx, brew, "--version")
		if err != nil {
			return "", errors.New("existing Homebrew could not run; repair it before retrying setup")
		}
		e.event(Event{Component: "Homebrew", Status: "Already available", Path: brew, Version: strings.Split(version, "\n")[0]})
		return brew, nil
	}
	if !p.AllowHomebrew {
		return "", required("Homebrew")
	}
	e.event(Event{Component: "Homebrew", Status: "Installing", Message: "Complete the official Homebrew installer in the Terminal window. It may request your administrator password."})
	script := filepath.Join(p.DataPath, "install-homebrew.sh")
	if _, err := e.command(ctx, "/usr/bin/curl", "--fail", "--location", "--proto", "=https", "--tlsv1.2", "https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh", "--output", script); err != nil {
		return "", err
	}
	if err := os.Chmod(script, 0700); err != nil {
		return "", err
	}
	command := "/bin/bash " + shellQuote(script)
	if _, err := e.command(ctx, "/usr/bin/osascript", "-e", `tell application "Terminal" to activate`, "-e", `tell application "Terminal" to do script `+appleQuote(command)); err != nil {
		return "", err
	}
	deadline := time.NewTimer(20 * time.Minute)
	defer deadline.Stop()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if brew := brewPath(); brew != "" {
			if version, err := e.command(ctx, brew, "--version"); err == nil {
				e.event(Event{Component: "Homebrew", Status: "Installed", Path: brew, Version: strings.Split(version, "\n")[0]})
				return brew, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", errors.New("Homebrew setup did not finish; complete it in Terminal and retry")
		case <-tick.C:
		}
	}
}

func (e *Engine) local(ctx context.Context, p Plan) error {
	var brew string
	var err error
	if strings.HasPrefix(p.Runtime, "install-") || p.Database == "install" {
		brew, err = e.ensureBrew(ctx, p)
		if err != nil {
			return err
		}
	} else {
		e.event(Event{Component: "Homebrew", Status: "Skipped", Message: "Using existing components"})
	}
	s, err := loadSecrets(p.DataPath)
	if err != nil {
		return err
	}
	socket := p.Socket
	if err = e.step(p, "Container runtime", func() string {
		if strings.HasPrefix(p.Runtime, "existing-") {
			return socket
		}
		home, _ := os.UserHomeDir()
		if p.Runtime == "install-docker" {
			return filepath.Join(home, ".colima/pspocketedge")
		}
		return "Podman machine pspocketedge"
	}(), func() (string, string, error) {
		status := "Already available"
		if strings.HasPrefix(p.Runtime, "install-") {
			status = "Installed"
			if p.Runtime == "install-docker" {
				if _, err := e.command(ctx, brew, "install", "colima", "docker", "docker-compose", "docker-buildx"); err != nil {
					return "", "", err
				}
				prefix, err := e.command(ctx, brew, "--prefix")
				if err != nil {
					return "", "", err
				}
				colima := filepath.Join(prefix, "bin/colima")
				if _, err = e.command(ctx, colima, "start", "--profile", "pspocketedge", "--runtime", "docker", "--cpu", strconv.Itoa(p.CPUs), "--memory", strconv.Itoa(p.MemoryGB), "--disk", strconv.Itoa(p.DiskGB)); err != nil {
					return "", "", err
				}
				home, _ := os.UserHomeDir()
				socket = "unix://" + filepath.Join(home, ".colima/pspocketedge/docker.sock")
				if err = e.job(ctx, p, "runtime", []string{colima, "start", "--profile", "pspocketedge"}, nil, false); err != nil {
					return "", "", err
				}
			} else {
				if _, err := e.command(ctx, brew, "install", "podman"); err != nil {
					return "", "", err
				}
				prefix, err := e.command(ctx, brew, "--prefix")
				if err != nil {
					return "", "", err
				}
				podman := filepath.Join(prefix, "bin/podman")
				if _, err = e.command(ctx, podman, "machine", "inspect", "pspocketedge"); err != nil {
					if _, err = e.command(ctx, podman, "machine", "init", "--cpus", strconv.Itoa(p.CPUs), "--memory", strconv.Itoa(p.MemoryGB*1024), "--disk-size", strconv.Itoa(p.DiskGB), "pspocketedge"); err != nil {
						return "", "", err
					}
				}
				b, err := e.command(ctx, podman, "machine", "inspect", "pspocketedge")
				if err != nil {
					return "", "", err
				}
				var machines []struct {
					State          string
					ConnectionInfo struct{ PodmanSocket struct{ Path string } }
				}
				if err = json.Unmarshal([]byte(b), &machines); err != nil || len(machines) != 1 {
					return "", "", errors.New("could not inspect the dedicated Podman machine")
				}
				if machines[0].State != "running" {
					if _, err = e.command(ctx, podman, "machine", "start", "pspocketedge"); err != nil {
						return "", "", err
					}
				}
				b, err = e.command(ctx, podman, "machine", "inspect", "pspocketedge")
				if err != nil {
					return "", "", err
				}
				if err = json.Unmarshal([]byte(b), &machines); err != nil || len(machines) != 1 {
					return "", "", errors.New("could not discover Podman's socket")
				}
				socket = "unix://" + machines[0].ConnectionInfo.PodmanSocket.Path
				if err = e.job(ctx, p, "runtime", []string{podman, "machine", "start", "pspocketedge"}, nil, false); err != nil {
					return "", "", err
				}
			}
		}
		version, err := socketVersion(ctx, socket)
		return status, version, err
	}); err != nil {
		return err
	}
	databaseURL := p.DatabaseURL
	if err = e.step(p, "PostgreSQL", func() string {
		if p.Database == "install" {
			return filepath.Join(p.DataPath, "postgres")
		}
		u, _ := url.Parse(p.DatabaseURL)
		return u.Host + u.Path
	}(), func() (string, string, error) {
		status := "Already available"
		version := ""
		if p.Database == "install" {
			status = "Installed"
			if _, err := e.command(ctx, brew, "install", "postgresql@16"); err != nil {
				return "", "", err
			}
			prefix, err := e.command(ctx, brew, "--prefix", "postgresql@16")
			if err != nil {
				return "", "", err
			}
			pgbin := filepath.Join(prefix, "bin")
			pgroot := filepath.Join(p.DataPath, "postgres")
			data := filepath.Join(pgroot, "data")
			if err = os.MkdirAll(pgroot, 0700); err != nil {
				return "", "", err
			}
			if _, err = os.Stat(filepath.Join(data, "PG_VERSION")); os.IsNotExist(err) {
				if entries, readErr := os.ReadDir(data); readErr == nil && len(entries) > 0 {
					return "", "", errors.New("PostgreSQL data directory is not empty; setup will not overwrite it")
				}
				passwordFile := filepath.Join(pgroot, ".init-password")
				if err = atomicWrite(passwordFile, []byte(s.DatabasePassword+"\n"), 0600); err != nil {
					return "", "", err
				}
				_, err = e.command(ctx, filepath.Join(pgbin, "initdb"), "-D", data, "--username=pspocketedge", "--auth-host=scram-sha-256", "--auth-local=scram-sha-256", "--pwfile="+passwordFile)
				os.Remove(passwordFile)
				if err != nil {
					return "", "", err
				}
			} else if err != nil {
				return "", "", err
			}
			pgargs := []string{filepath.Join(pgbin, "postgres"), "-D", data, "-p", "55433", "-h", "127.0.0.1", "-k", ""}
			if err = e.job(ctx, p, "postgres", pgargs, nil, true); err != nil {
				return "", "", err
			}
			base := url.URL{Scheme: "postgres", User: url.UserPassword("pspocketedge", s.DatabasePassword), Host: "127.0.0.1:55433", Path: "/postgres", RawQuery: "sslmode=disable"}
			var conn *pgx.Conn
			err = waitFor(ctx, func() error {
				var connectErr error
				conn, connectErr = pgx.Connect(ctx, base.String())
				return connectErr
			})
			if err != nil {
				return "", "", errors.New("dedicated PostgreSQL did not become ready; inspect postgres.log")
			}
			defer conn.Close(context.Background())
			var exists bool
			if err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname='pspocketedge')").Scan(&exists); err != nil {
				return "", "", err
			}
			if !exists {
				if _, err = conn.Exec(ctx, "CREATE DATABASE pspocketedge"); err != nil {
					return "", "", err
				}
			}
			base.Path = "/pspocketedge"
			databaseURL = base.String()
			version, _ = e.command(ctx, filepath.Join(pgbin, "postgres"), "--version")
		} else {
			conn, err := pgx.Connect(ctx, databaseURL)
			if err != nil {
				return "", "", err
			}
			defer conn.Close(context.Background())
			if err = conn.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
				return "", "", err
			}
			major, _ := strconv.Atoi(strings.Split(version, ".")[0])
			if major < 16 {
				return "", "", errors.New("PostgreSQL 16 or later is required")
			}
		}
		if err := migrate(ctx, databaseURL); err != nil {
			return "", "", err
		}
		return status, version, nil
	}); err != nil {
		return err
	}
	bin := filepath.Join(p.DataPath, "bin")
	if err = os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	payloadVersionBytes, _ := os.ReadFile(filepath.Join(e.Payload, "version.txt"))
	payloadVersion := strings.TrimSpace(string(payloadVersionBytes))
	if err = e.step(p, "Control plane", bin, func() (string, string, error) {
		if err := e.copyBinary("pe-controlplane", bin); err != nil {
			return "", "", err
		}
		env := map[string]string{"DATABASE_URL": databaseURL, "JWT_SECRET": s.JWTSecret, "ADMIN_EMAIL": "admin@localhost", "ADMIN_PASSWORD": s.AdminPassword, "PUBLIC_URL": p.APIURL, "BACKUP_DIR": filepath.Join(p.DataPath, "backups"), "VAULT_KEY_FILE": filepath.Join(p.DataPath, "vault.key")}
		err := e.job(ctx, p, "controlplane", []string{filepath.Join(bin, "pe-controlplane"), "--http-addr=127.0.0.1:8080", "--grpc-addr=127.0.0.1:8443"}, env, true)
		if err == nil {
			err = waitFor(ctx, func() error {
				var r struct{ Service string }
				err := apiRequest(ctx, p.APIURL, "/api/health", "", &r)
				if err == nil && r.Service != "pspocketedge" {
					err = errors.New("unexpected API service")
				}
				return err
			})
		}
		return "Installed", payloadVersion, err
	}); err != nil {
		return err
	}
	if err = e.step(p, "Agent", bin, func() (string, string, error) {
		if err := e.copyBinary("pe-agent", bin); err != nil {
			return "", "", err
		}
		statePath := filepath.Join(p.DataPath, "agent-state.json")
		id, err := state.Load(statePath)
		if err != nil {
			return "", "", err
		}
		cfg := config.Config{Server: "127.0.0.1:8443", StatePath: statePath, ContainerRuntime: "docker", ContainerHost: socket, BuildDir: filepath.Join(p.DataPath, "builds")}
		if strings.Contains(p.Runtime, "podman") {
			cfg.ContainerRuntime = "podman"
		}
		token, err := administratorSession(ctx, p)
		if err != nil {
			return "", "", err
		}
		if id == nil {
			var enrollment struct{ Token string }
			if err = apiRequest(ctx, p.APIURL, "/api/servers/enroll-token", token, &enrollment); err != nil {
				return "", "", err
			}
			cfg.Token = enrollment.Token
		}
		cfgFile := filepath.Join(p.DataPath, "agent.yaml")
		b, err := yaml.Marshal(cfg)
		if err != nil {
			return "", "", err
		}
		if err = atomicWrite(cfgFile, b, 0600); err != nil {
			return "", "", err
		}
		if err = e.job(ctx, p, "agent", []string{filepath.Join(bin, "pe-agent"), "--config=" + cfgFile}, nil, true); err != nil {
			return "", "", err
		}
		err = waitFor(ctx, func() error {
			id, err := state.Load(statePath)
			if err != nil || id == nil {
				return errors.New("waiting for agent enrollment")
			}
			var servers []struct {
				ID     string
				Status string
			}
			if err = apiRequest(ctx, p.APIURL, "/api/servers", token, &servers); err != nil {
				return err
			}
			for _, srv := range servers {
				if srv.ID == id.ServerID && srv.Status == "online" {
					return nil
				}
			}
			return errors.New("waiting for the agent's first heartbeat")
		})
		// The durable identity replaces the short-lived enrollment token.
		if err == nil {
			cfg.Token = ""
			b, _ = yaml.Marshal(cfg)
			err = atomicWrite(cfgFile, b, 0600)
		}
		return "Installed", payloadVersion, err
	}); err != nil {
		return err
	}
	status := "Installed"
	if !p.AutoStart {
		status = "Skipped"
	}
	e.event(Event{Component: "Start at login", Status: status, Message: func() string {
		if p.AutoStart {
			return "Per-user services will start at login"
		}
		return "Services run now; start them manually after your next login"
	}()})
	return nil
}

func (e *Engine) copyBinary(name, dir string) error {
	b, err := os.ReadFile(filepath.Join(e.Payload, name))
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, name), b, 0700)
}

func administratorSession(ctx context.Context, p Plan) (string, error) {
	var session struct{ Token string }
	if err := apiRequest(ctx, p.APIURL, "/api/auth/local-session", "", &session); err != nil {
		if p.AdminEmail == "" || p.AdminPassword == "" {
			return "", errors.New("local administrator access is disabled; provide existing administrator credentials in Custom setup to enroll the agent")
		}
		body, _ := json.Marshal(map[string]string{"email": p.AdminEmail, "password": p.AdminPassword})
		if err = apiRequestBody(ctx, p.APIURL, "/api/auth/login", "", bytes.NewReader(body), &session); err != nil {
			return "", errors.New("administrator login failed; check the credentials in Custom setup")
		}
	}
	if session.Token == "" {
		return "", errors.New("administrator session was not issued")
	}
	var user struct{ Role string }
	if err := apiRequest(ctx, p.APIURL, "/api/auth/me", session.Token, &user); err != nil {
		return "", err
	}
	if user.Role != "admin" {
		return "", errors.New("an administrator account is required to enroll the local agent")
	}
	return session.Token, nil
}
