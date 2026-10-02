package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Engine struct {
	Payload string
	Emit    func(Event)
	// Run is injectable so failure paths can be tested without installing tools.
	Run    func(context.Context, string, ...string) (string, error)
	mu     sync.Mutex
	events []Event
	log    *os.File
}

func (e *Engine) command(ctx context.Context, name string, args ...string) (string, error) {
	if e.Run != nil {
		return e.Run(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 3 * time.Second
	cmd.Env = append(os.Environ(), "PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin", "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_INSTALL_UPGRADE=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s failed: %w", filepath.Base(name), err)
	}
	return strings.TrimSpace(string(output)), nil
}
func (e *Engine) event(v Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if v.Component != "" {
		e.events = append(e.events, v)
	}
	if e.log != nil {
		json.NewEncoder(e.log).Encode(v)
	}
	if e.Emit != nil {
		e.Emit(v)
	}
}
func (e *Engine) step(p Plan, name, path string, fn func() (string, string, error)) error {
	e.event(Event{Component: name, Status: "Installing", Path: path})
	status, version, err := fn()
	if err != nil {
		e.event(Event{Component: name, Status: "Failed", Message: safeError(err, p), Path: path})
		return err
	}
	e.event(Event{Component: name, Status: status, Path: path, Version: version, Message: "Verified"})
	return nil
}
func brewPath() string {
	for _, p := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
		if st, err := os.Stat(p); err == nil && st.Mode()&0111 != 0 {
			return p
		}
	}
	return ""
}
func randomSecret() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, b, 0600)
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".setup-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}
func safeDirectory(path string) error {
	for p := path; p != "/"; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return fmt.Errorf("directory must not be a symlink or file: %s", p)
		}
	}
	return nil
}
func prepareRoot(root string) error {
	if err := safeDirectory(root); err != nil {
		return err
	}
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		if b, err := os.ReadFile(filepath.Join(root, ".pspe-setup")); err != nil || string(b) != "PS-pocketEdge setup v1\n" {
			return errors.New("data folder contains existing files not owned by setup; choose an empty folder")
		}
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if err := os.Chmod(root, 0700); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(root, ".pspe-setup"), []byte("PS-pocketEdge setup v1\n"), 0600)
}
func socketVersion(ctx context.Context, socket string) (string, error) {
	path := strings.TrimPrefix(socket, "unix://")
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost/version", nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("container API socket is unavailable; start the selected runtime and retry")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("container runtime did not return a compatible API version")
	}
	var result struct {
		Version    string
		APIVersion string
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&result); err != nil || result.Version == "" || result.APIVersion == "" {
		return "", errors.New("socket is not a Docker-compatible container API")
	}
	parts := strings.Split(result.APIVersion, ".")
	if len(parts) != 2 {
		return "", errors.New("container API version is not supported")
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	if major < 1 || (major == 1 && minor < 40) {
		return "", errors.New("container API 1.40 or newer is required")
	}
	return result.Version, nil
}
func apiRequest(ctx context.Context, base, path, token string, result any) error {
	return apiRequestBody(ctx, base, path, token, nil, result)
}

func apiRequestBody(ctx context.Context, base, path, token string, body io.Reader, result any) error {
	method := "GET"
	if strings.Contains(path, "local-session") || strings.Contains(path, "enroll-token") || strings.Contains(path, "/auth/login") {
		method = "POST"
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return errors.New("control plane is unavailable at the selected address")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("control plane returned HTTP %d; check access settings and logs", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result)
}
func waitFor(ctx context.Context, fn func() error) error {
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var last error
	for {
		last = fn()
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return last
		case <-tick.C:
		}
	}
}
func (e *Engine) Preflight(ctx context.Context, p Plan) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("this setup is for macOS")
	}
	if os.Geteuid() == 0 {
		return errors.New("run Setup as your normal macOS user; administrator permission is requested only for the app copy")
	}
	if err := safeDirectory(p.DataPath); err != nil {
		return err
	}
	if err := safeDirectory(filepath.Dir(p.AppPath)); err != nil {
		return err
	}
	if existing, readErr := os.ReadDir(p.DataPath); readErr == nil && len(existing) > 0 {
		if marker, markerErr := os.ReadFile(filepath.Join(p.DataPath, ".pspe-setup")); markerErr != nil || string(marker) != "PS-pocketEdge setup v1\n" {
			return errors.New("data folder contains existing files not owned by setup; choose an empty folder")
		}
	}
	if _, runningErr := e.command(ctx, "/usr/bin/pgrep", "-x", "PS-pocketEdge"); runningErr == nil {
		return errors.New("quit PS-pocketEdge before installing or updating it")
	}
	if _, err := os.Stat(filepath.Join(e.Payload, "PS-pocketEdge.app")); err != nil {
		return errors.New("installer payload is missing the desktop app")
	}
	if _, err := e.command(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", filepath.Join(e.Payload, "PS-pocketEdge.app")); err != nil {
		return errors.New("desktop app signature verification failed")
	}
	var disk syscall.Statfs_t
	home, _ := os.UserHomeDir()
	if err := syscall.Statfs(home, &disk); err != nil {
		return err
	}
	minimum := uint64(8 << 30)
	if p.Mode == "remote" {
		minimum = 512 << 20
	}
	if uint64(disk.Bavail)*uint64(disk.Bsize) < minimum {
		return errors.New("insufficient free disk space: local setup needs 8 GB, existing-server setup needs 512 MB")
	}
	if p.Mode == "remote" {
		var result struct{ Service string }
		if err := apiRequest(ctx, p.APIURL, "/api/health", "", &result); err != nil {
			return err
		}
		if result.Service != "pspocketedge" {
			return errors.New("address is not a PS-pocketEdge control plane")
		}
		return nil
	}
	macVersion, versionErr := e.command(ctx, "/usr/bin/sw_vers", "-productVersion")
	if versionErr != nil {
		return versionErr
	}
	major, _ := strconv.Atoi(strings.Split(macVersion, ".")[0])
	if major < 14 {
		return errors.New("local setup requires macOS 14 or newer")
	}
	if strings.HasPrefix(p.Runtime, "install-") || p.Database == "install" {
		if _, networkErr := e.command(ctx, "/usr/bin/curl", "--fail", "--head", "--location", "--connect-timeout", "10", "--max-time", "20", "--proto", "=https", "https://formulae.brew.sh/api/formula.json"); networkErr != nil {
			return errors.New("package downloads are unreachable; check your internet connection and retry")
		}
	}
	// Only services with our saved ownership marker may be reused or restarted.
	for _, port := range []int{8080, 8443, 55433} {
		if port == 55433 && p.Database != "install" {
			continue
		}
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			listener.Close()
			continue
		}
		label := map[int]string{8080: "controlplane", 8443: "controlplane", 55433: "postgres"}[port]
		if _, err := os.Stat(filepath.Join(p.DataPath, label+".plist")); err != nil {
			return fmt.Errorf("port %d is already in use; stop that service or choose existing-server/database mode", port)
		}
		job, jobErr := e.command(ctx, "/bin/launchctl", "print", fmt.Sprintf("gui/%d/com.pspocketedge.setup.%s", os.Getuid(), label))
		match := regexp.MustCompile(`(?m)^\s*pid = ([0-9]+)`).FindStringSubmatch(job)
		owners, ownerErr := e.command(ctx, "/usr/sbin/lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t")
		if jobErr != nil || ownerErr != nil || len(match) != 2 || len(strings.Fields(owners)) == 0 {
			return fmt.Errorf("cannot verify ownership of occupied port %d; stop the conflicting service and retry", port)
		}
		for _, owner := range strings.Fields(owners) {
			if owner != match[1] {
				return fmt.Errorf("port %d belongs to another service; setup will not stop it", port)
			}
		}
	}
	if strings.HasPrefix(p.Runtime, "existing-") {
		if _, err := socketVersion(ctx, p.Socket); err != nil {
			return err
		}
	}
	if !p.AllowHomebrew && brewPath() == "" && (strings.HasPrefix(p.Runtime, "install-") || p.Database == "install") {
		return required("Homebrew")
	}
	if strings.HasPrefix(p.Runtime, "install-") && p.CPUs > runtime.NumCPU() {
		return errors.New("selected CPU count exceeds this computer's available CPUs")
	}
	mem, err := e.command(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize")
	if err == nil && strings.HasPrefix(p.Runtime, "install-") {
		total, _ := strconv.ParseUint(strings.TrimSpace(mem), 10, 64)
		if uint64(p.MemoryGB)<<30 > total/2 {
			return errors.New("VM RAM must leave at least half of this computer's memory available")
		}
	}
	return nil
}

// Install validates before writing, serializes concurrent installers, and saves
// a checklist on both success and failure. Retrying rechecks each component.
func (e *Engine) Install(ctx context.Context, p Plan) (err error) {
	if err = p.Validate(); err != nil {
		e.event(Event{Component: "Requirements", Status: "Failed", Message: err.Error(), Done: true})
		return err
	}
	if err = e.Preflight(ctx, p); err != nil {
		e.event(Event{Component: "Preflight", Status: "Failed", Message: safeError(err, p), Done: true})
		return err
	}
	if err = prepareRoot(p.DataPath); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	lockDir := filepath.Join(home, "Library/Caches/com.pspocketedge.setup")
	if err = safeDirectory(lockDir); err != nil {
		return err
	}
	if err = os.MkdirAll(lockDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(lockDir, "setup.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another setup is already running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if b, readErr := os.ReadFile(filepath.Join(p.DataPath, "plan.json")); readErr == nil {
		var previous Plan
		if json.Unmarshal(b, &previous) != nil {
			return errors.New("saved setup plan is invalid; restore plan.json before retrying")
		}
		if previous.Mode != p.Mode || previous.Database != p.Database || previous.DatabaseURL != p.DatabaseURL || previous.APIURL != p.APIURL {
			return errors.New("an existing setup cannot switch database or server mode in the same data folder; keep its original choices")
		}
	}
	e.log, err = os.OpenFile(filepath.Join(p.DataPath, "setup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer e.log.Close()
	defer func() {
		e.event(Event{Done: true, OK: err == nil, Message: func() string {
			if err != nil {
				return safeError(err, p)
			}
			return "All mandatory checks passed"
		}()})
		writeJSON(filepath.Join(p.DataPath, "checklist.json"), e.events)
	}()
	if err = writeJSON(filepath.Join(p.DataPath, "plan.json"), p); err != nil {
		return err
	}
	if p.Mode == "remote" {
		for _, name := range []string{"Container runtime", "PostgreSQL", "Control plane", "Agent", "Start at login"} {
			e.event(Event{Component: name, Status: "Skipped", Message: "Provided by the existing server"})
		}
	} else {
		if err = e.local(ctx, p); err != nil {
			return err
		}
	}
	if err = e.step(p, "PS-pocketEdge app", p.AppPath, func() (string, string, error) { return e.installApp(ctx, p) }); err != nil {
		return err
	}
	if err = e.step(p, "Connection checks", p.APIURL, func() (string, string, error) {
		var result struct {
			Service string
			Version string
		}
		err := apiRequest(ctx, p.APIURL, "/api/health", "", &result)
		if err == nil && result.Service != "pspocketedge" {
			err = errors.New("unexpected control-plane response")
		}
		return "Passed", result.Version, err
	}); err != nil {
		return err
	}
	return nil
}

func (e *Engine) Open(ctx context.Context, p Plan) error {
	if err := p.Validate(); err != nil {
		return err
	}
	returnErr := error(nil)
	_, returnErr = e.command(ctx, "/usr/bin/open", "-n", p.AppPath, "--args", "--control-plane-url", p.APIURL)
	return returnErr
}
