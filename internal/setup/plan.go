// Package setup implements the macOS setup helper used by the native wizard.
package setup

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Plan struct {
	Mode          string `json:"mode"`
	Runtime       string `json:"runtime"`
	Socket        string `json:"socket"`
	Database      string `json:"database"`
	DatabaseURL   string `json:"databaseURL"`
	APIURL        string `json:"apiURL"`
	AppPath       string `json:"appPath"`
	DataPath      string `json:"dataPath"`
	CPUs          int    `json:"cpus"`
	MemoryGB      int    `json:"memoryGB"`
	DiskGB        int    `json:"diskGB"`
	AutoStart     bool   `json:"autoStart"`
	AllowHomebrew bool   `json:"allowHomebrew"`
	AdminEmail    string `json:"adminEmail"`
	AdminPassword string `json:"adminPassword"`
}

func DefaultPlan(home string) Plan {
	return Plan{Mode: "local", Runtime: "install-docker", Database: "install", APIURL: "http://localhost:8080", AppPath: "/Applications/PS-pocketEdge.app", DataPath: filepath.Join(home, "Library/Application Support/PSpocketEdge/LocalSetup"), CPUs: 2, MemoryGB: 4, DiskGB: 60, AutoStart: true, AllowHomebrew: true}
}

// Validate runs before any write or installation. Declining a mandatory
// component cannot leave a partially installed system.
func (p Plan) Validate() error {
	if p.Mode != "local" && p.Mode != "remote" {
		return errors.New("choose local setup or an existing server")
	}
	for _, path := range []string{p.AppPath, p.DataPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\r\n\x00") {
			return errors.New("installation paths must be absolute, clean paths")
		}
	}
	if filepath.Base(p.AppPath) != "PS-pocketEdge.app" {
		return errors.New("app location must end in PS-pocketEdge.app")
	}
	home, _ := os.UserHomeDir()
	if p.DataPath == "/" || p.DataPath == home || !strings.HasPrefix(p.DataPath, home+string(os.PathSeparator)) {
		return errors.New("choose an app data folder inside your home directory")
	}
	if strings.HasPrefix(p.DataPath, p.AppPath+"/") || strings.HasPrefix(p.AppPath, p.DataPath+"/") {
		return errors.New("app and data folders must be separate")
	}
	u, err := url.Parse(p.APIURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("provide a valid HTTP(S) control-plane URL without credentials or query parameters")
	}
	if p.Mode == "remote" {
		return nil
	}
	if p.APIURL != "http://localhost:8080" {
		return errors.New("local setup uses http://localhost:8080; choose existing-server mode for another address")
	}
	switch p.Runtime {
	case "install-docker", "install-podman", "existing-docker", "existing-podman":
	default:
		return errors.New("a container runtime is mandatory; declining it exits setup")
	}
	switch p.Database {
	case "install":
	case "existing":
		db, err := url.Parse(p.DatabaseURL)
		if err != nil || (db.Scheme != "postgres" && db.Scheme != "postgresql") || db.Hostname() == "" || strings.Trim(db.Path, "/") == "" {
			return errors.New("provide an existing PostgreSQL database URL including a dedicated database name")
		}
	default:
		return errors.New("PostgreSQL is mandatory; declining it exits setup")
	}
	if strings.HasPrefix(p.Runtime, "existing-") && (!strings.HasPrefix(p.Socket, "unix:///") || strings.ContainsAny(p.Socket, "\r\n\x00")) {
		return errors.New("choose an existing runtime's absolute unix:// socket path")
	}
	if p.CPUs < 1 || p.CPUs > 128 || p.MemoryGB < 2 || p.MemoryGB > 512 || p.DiskGB < 10 || p.DiskGB > 4096 {
		return errors.New("resource limits are outside the supported range")
	}
	return nil
}

type Event struct {
	Component string `json:"component"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Done      bool   `json:"done,omitempty"`
	OK        bool   `json:"ok,omitempty"`
}

func safeError(err error, p Plan) string {
	s := err.Error()
	if p.AdminPassword != "" {
		s = strings.ReplaceAll(s, p.AdminPassword, "[redacted]")
	}
	if p.DatabaseURL != "" {
		s = strings.ReplaceAll(s, p.DatabaseURL, "[database connection]")
		if u, e := url.Parse(p.DatabaseURL); e == nil && u.User != nil {
			if password, ok := u.User.Password(); ok && password != "" {
				s = strings.ReplaceAll(s, password, "[redacted]")
			}
		}
	}
	return s
}

// ErrorMessage removes database credentials before displaying a failure.
func ErrorMessage(err error, p Plan) string { return safeError(err, p) }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`).Replace(s) + `"`
}
func required(name string) error {
	return fmt.Errorf("%s is required. Go back to allow installation or choose a compatible existing installation", name)
}
