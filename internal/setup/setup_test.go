package setup

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMandatoryRefusalBeforeAnyInstallation(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, component := range []string{"runtime", "database"} {
		t.Run(component, func(t *testing.T) {
			p := DefaultPlan(home)
			if component == "runtime" {
				p.Runtime = "decline"
			} else {
				p.Database = "decline"
			}
			called := false
			var events []Event
			e := Engine{Run: func(context.Context, string, ...string) (string, error) { called = true; return "", nil }, Emit: func(v Event) { events = append(events, v) }}
			if err := e.Install(context.Background(), p); err == nil {
				t.Fatal("accepted a declined requirement")
			}
			if called {
				t.Fatal("executed commands after refusal")
			}
			if len(events) != 1 || !events[0].Done || events[0].OK {
				t.Fatalf("missing refusal checklist: %+v", events)
			}
		})
	}
}
func TestRemoteModeHasNoLocalDependencies(t *testing.T) {
	home, _ := os.UserHomeDir()
	p := DefaultPlan(home)
	p.Mode = "remote"
	p.APIURL = "https://control.example"
	p.Runtime = "decline"
	p.Database = "decline"
	p.AllowHomebrew = false
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestRejectUnsafePathsAndURLs(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, modify := range []func(*Plan){func(p *Plan) { p.DataPath = home }, func(p *Plan) { p.DataPath = "/" }, func(p *Plan) { p.AppPath = "/Applications/Other.app" }, func(p *Plan) { p.APIURL = "http://u:secret@host" }, func(p *Plan) { p.AppPath = "/Applications/../Other/PS-pocketEdge.app" }, func(p *Plan) { p.Runtime = "existing-docker"; p.Socket = "tcp://host:2375" }} {
		p := DefaultPlan(home)
		modify(&p)
		if p.Validate() == nil {
			t.Fatalf("accepted unsafe plan: %+v", p)
		}
	}
}
func TestProtectExistingDataAndSymlinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "existing")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "database"), []byte("keep me"), 0600)
	if prepareRoot(root) == nil {
		t.Fatal("claimed unowned data")
	}
	b, _ := os.ReadFile(filepath.Join(root, "database"))
	if string(b) != "keep me" {
		t.Fatal("changed existing data")
	}
	link := filepath.Join(base, "link")
	os.Symlink(root, link)
	if prepareRoot(link) == nil {
		t.Fatal("accepted symlink")
	}
	fresh := filepath.Join(base, "fresh")
	if err = prepareRoot(fresh); err != nil {
		t.Fatal(err)
	}
	if err = prepareRoot(fresh); err != nil {
		t.Fatal("cannot resume", err)
	}
}
func TestCredentialsPersistAndRemainPrivate(t *testing.T) {
	root := t.TempDir()
	a, err := loadSecrets(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadSecrets(root)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || len(a.JWTSecret) != 64 || a.JWTSecret == a.AdminPassword {
		t.Fatal("unstable or weak credentials")
	}
	st, _ := os.Stat(filepath.Join(root, "credentials.json"))
	if st.Mode().Perm() != 0600 {
		t.Fatal("credentials not private")
	}
}
func TestLaunchPlistEscapesPathsAndStartupChoice(t *testing.T) {
	raw := launchPlist("com.pspocketedge.setup.agent", []string{"/Users/A & B/bin/agent", "--config=/path/<file>"}, map[string]string{"DATABASE_URL": "postgres://a:p&x@host/db"}, "/Users/A & B/data", false, true)
	decoder := xml.NewDecoder(strings.NewReader(string(raw)))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(string(raw), "<true/>") || !strings.Contains(string(raw), "A &amp; B") {
		t.Fatal("incorrect startup policy or escaping")
	}
}
func TestErrorsDoNotRevealDatabaseCredentials(t *testing.T) {
	p := Plan{DatabaseURL: "postgres://user:s3cret@host/db"}
	s := safeError(errors.New("connection postgres://user:s3cret@host/db failed with password s3cret"), p)
	if strings.Contains(s, "s3cret") {
		t.Fatal("secret leaked")
	}
}
func TestRuntimeSocketRequiresCompatibleAPI(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "pspe-socket-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	root, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "runtime.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			t.Error(r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]string{"Version": "5.0", "ApiVersion": "1.40"})
	})}
	go server.Serve(listener)
	defer server.Close()
	version, err := socketVersion(context.Background(), "unix://"+socket)
	if err != nil || version != "5.0" {
		t.Fatal(version, err)
	}
	if _, err = socketVersion(context.Background(), "unix://"+filepath.Join(root, "missing")); err == nil {
		t.Fatal("accepted missing runtime")
	}
}
func TestPreflightRejectsUnhealthyAPI(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer s.Close()
	var out struct{ Service string }
	if apiRequest(context.Background(), s.URL, "/api/health", "", &out) == nil {
		t.Fatal("accepted unhealthy API")
	}
}
func TestStepStopsOnFailureAndReportsIt(t *testing.T) {
	var events []Event
	e := Engine{Emit: func(v Event) { events = append(events, v) }}
	err := e.step(Plan{}, "PostgreSQL", "/data", func() (string, string, error) { return "", "", errors.New("database failed") })
	if err == nil || len(events) != 2 || events[1].Status != "Failed" {
		t.Fatalf("failure not surfaced: %+v %v", events, err)
	}
}

func TestExistingServerInstallDoesNotInstallLocalServices(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS installation preflight")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"service": "pspocketedge", "version": "1.0.0"})
	}))
	defer server.Close()
	payload := filepath.Join(home, "payload")
	os.MkdirAll(filepath.Join(payload, "PS-pocketEdge.app"), 0700)
	p := DefaultPlan(home)
	p.Mode = "remote"
	p.APIURL = server.URL
	p.AppPath = filepath.Join(home, "Applications/PS-pocketEdge.app")
	p.AllowHomebrew = false
	e := Engine{Payload: payload, Run: func(_ context.Context, command string, args ...string) (string, error) {
		if strings.Contains(command, "brew") || strings.Contains(command, "launchctl") {
			t.Fatal("installed a local dependency in remote mode")
		}
		if strings.HasSuffix(command, "pgrep") {
			return "", errors.New("not running")
		}
		if strings.HasSuffix(command, "PlistBuddy") {
			return "1.0.0", nil
		}
		return "", nil
	}}
	if err = e.Install(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(p.DataPath, "checklist.json"))
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	json.Unmarshal(raw, &events)
	skipped := 0
	passed := false
	for _, event := range events {
		if event.Status == "Skipped" {
			skipped++
		}
		if event.Component == "Connection checks" && event.Status == "Passed" {
			passed = true
		}
	}
	if skipped != 5 || !passed {
		t.Fatalf("incorrect final checklist: %+v", events)
	}
}

func TestExistingAdministratorLoginAndViewerRefusal(t *testing.T) {
	for _, role := range []string{"admin", "viewer"} {
		t.Run(role, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/auth/local-session":
					w.WriteHeader(http.StatusForbidden)
				case "/api/auth/login":
					if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
						t.Fatal("invalid login request")
					}
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if body["email"] != "admin@local" || body["password"] != "private" {
						t.Error("wrong login credentials")
					}
					json.NewEncoder(w).Encode(map[string]string{"token": "session-token"})
				case "/api/auth/me":
					if r.Header.Get("Authorization") != "Bearer session-token" {
						t.Error("missing authorization")
					}
					json.NewEncoder(w).Encode(map[string]string{"role": role})
				default:
					t.Error(r.URL.Path)
				}
			}))
			defer s.Close()
			token, err := administratorSession(context.Background(), Plan{APIURL: s.URL, AdminEmail: "admin@local", AdminPassword: "private"})
			if role == "admin" && (err != nil || token != "session-token") {
				t.Fatal(token, err)
			}
			if role == "viewer" && err == nil {
				t.Fatal("accepted viewer credentials")
			}
		})
	}
}
