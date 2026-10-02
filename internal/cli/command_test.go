package cli

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func execute(t *testing.T, path, input string, args ...string) (string, error) {
	t.Helper()
	var out, stderr bytes.Buffer
	cmd := NewCommand(strings.NewReader(input), &out, &stderr)
	cmd.SetArgs(append([]string{"--config", path}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func setup(t *testing.T, h http.HandlerFunc) (string, *httptest.Server) {
	t.Helper()
	t.Setenv("PSE_URL", "")
	t.Setenv("PSE_TOKEN", "")
	t.Setenv("PSE_CONFIG", "")
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	p := filepath.Join(t.TempDir(), "cli.json")
	if err := (&options{configPath: p}).save(config{URL: s.URL, Token: "test-token"}); err != nil {
		t.Fatal(err)
	}
	return p, s
}

func TestLoginPersistenceAndLogout(t *testing.T) {
	p, s := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/auth/login" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected login request: %s %s", r.Method, r.URL)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["email"] != "admin@example.com" || body["password"] != "secret" {
			t.Errorf("incorrect credentials: %v", body)
		}
		io.WriteString(w, `{"token":"new-token"}`)
	})
	out, err := execute(t, p, "secret\n", "login", "--url", s.URL, "--email", "admin@example.com", "--password-stdin")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "new-token") || strings.Contains(out, "secret") {
		t.Fatal("credentials leaked in output")
	}
	o := &options{configPath: p}
	c, err := o.load()
	if err != nil || c.Token != "new-token" || c.URL != s.URL {
		t.Fatalf("saved config: %+v, %v", c, err)
	}
	b, _ := os.ReadFile(p)
	if bytes.Contains(b, []byte("secret")) {
		t.Fatal("password persisted")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != 0600 {
			t.Fatalf("config mode: %v", st.Mode())
		}
	}
	if _, err = execute(t, p, "", "logout"); err != nil {
		t.Fatal(err)
	}
	c, _ = o.load()
	if c.Token != "" || c.URL != s.URL {
		t.Fatal("logout did not clear only saved token")
	}
}

func TestListAndAction(t *testing.T) {
	var actionCalls int
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer token")
		}
		switch r.URL.Path {
		case "/api/servers":
			io.WriteString(w, `[{"id":"s1","name":"host","status":"online"}]`)
		case "/api/containers":
			if r.URL.Query().Get("serverId") != "" && r.URL.Query().Get("serverId") != "s1" {
				t.Error("incorrect server filter")
			}
			io.WriteString(w, `[{"containerId":"c1","serverId":"s1","name":"web","state":"running"}]`)
		case "/api/servers/s1/containers/c1/action":
			actionCalls++
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if r.Method != "POST" || b["action"] != "start" {
				t.Errorf("bad action: %v", b)
			}
			io.WriteString(w, `{"success":true}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	out, err := execute(t, p, "", "servers", "list")
	if err != nil || !strings.Contains(out, "online") {
		t.Fatalf("list: %s, %v", out, err)
	}
	out, err = execute(t, p, "", "containers", "list", "--server", "s1", "--json")
	if err != nil || !json.Valid([]byte(out)) {
		t.Fatalf("JSON: %s, %v", out, err)
	}
	if _, err = execute(t, p, "", "containers", "start", "c1"); err != nil {
		t.Fatal(err)
	}
	if actionCalls != 1 {
		t.Fatalf("action calls: %d", actionCalls)
	}
}

func TestFailuresAndAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name, response, want string
		status               int
		args                 []string
	}{
		{"agent failure", `{"success":false,"error":"cannot start"}`, "cannot start", 200, []string{"containers", "start", "c1", "--server", "s1"}},
		{"expired session", "unauthorized", "renew your session", 401, []string{"servers", "list"}},
		{"viewer denied", "forbidden", "403", 403, []string{"containers", "start", "c1", "--server", "s1"}},
		{"ambiguous", `[{"containerId":"c1","serverId":"s1"},{"containerId":"c1","serverId":"s2"}]`, "multiple servers", 200, []string{"containers", "start", "c1"}},
		{"missing", `[]`, "not found", 200, []string{"containers", "start", "c1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.response) })
			_, err := execute(t, p, "", tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error: %v", err)
			}
		})
	}
}

func TestLogsAndInspect(t *testing.T) {
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/servers/s1/containers/c1/logs/download":
			if r.URL.Query().Get("tail") != "25" || r.Method != "GET" {
				t.Error("invalid log request")
			}
			io.WriteString(w, "[stdout] hello\n")
		case "/api/servers/s1/containers/c1/inspect":
			if r.Method != "POST" {
				t.Error("invalid inspect method")
			}
			io.WriteString(w, `{"containerId":"c1"}`)
		default:
			t.Error("unexpected request")
			w.WriteHeader(404)
		}
	})
	out, err := execute(t, p, "", "containers", "logs", "c1", "--server", "s1", "--tail", "25")
	if err != nil || out != "[stdout] hello\n" {
		t.Fatalf("logs: %q %v", out, err)
	}
	out, err = execute(t, p, "", "containers", "inspect", "c1", "--server", "s1")
	if err != nil || !json.Valid([]byte(out)) {
		t.Fatalf("inspect: %q %v", out, err)
	}
}

func TestDeployUploadAndRetryID(t *testing.T) {
	var calls int
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		switch r.URL.Path {
		case "/api/compose-files":
			if b["name"] != "web" || b["content"] != "services: {}\n" {
				t.Errorf("upload: %v", b)
			}
			w.WriteHeader(201)
			io.WriteString(w, `{"id":"file1"}`)
		case "/api/deployments":
			if b["composeFileId"] != "file1" || b["serverId"] != "s1" {
				t.Errorf("deploy: %v", b)
			}
			w.WriteHeader(202)
			io.WriteString(w, `{"id":"d1","status":"pending_approval"}`)
		default:
			t.Error("unexpected path")
			w.WriteHeader(404)
		}
	})
	f := filepath.Join(t.TempDir(), "compose.yaml")
	os.WriteFile(f, []byte("services: {}\n"), 0600)
	out, err := execute(t, p, "", "deploy", f, "--name", "web", "--server", "s1")
	if err != nil || !strings.Contains(out, "pending_approval") || calls != 2 {
		t.Fatalf("deploy: %q %v calls=%d", out, err, calls)
	}
	if _, err = execute(t, p, "", "deploy", "--compose-id", "file1", "--server", "s1"); err != nil || calls != 3 {
		t.Fatalf("existing file: %v", err)
	}
}

func TestURLSecurityAndTokenIsolation(t *testing.T) {
	for _, raw := range []string{"http://remote.example.com", "ftp://localhost", "https://user:pass@example.com", "https://example.com/api", "https://example.com?token=x", "https://example.com/#x"} {
		if _, err := validateURL(raw); err == nil {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
	for _, raw := range []string{"https://example.com:8080", "http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost:8080/"} {
		if _, err := validateURL(raw); err != nil {
			t.Errorf("rejected URL %q: %v", raw, err)
		}
	}
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("should not request server") })
	_, err := execute(t, p, "", "servers", "list", "--url", "https://other.example.com")
	if err == nil || !strings.Contains(err.Error(), "login required") {
		t.Fatalf("URL change reused session: %v", err)
	}
}

func TestPrivateCA(t *testing.T) {
	t.Setenv("PSE_TOKEN", "")
	t.Setenv("PSE_URL", "")
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `[]`) }))
	defer s.Close()
	p := filepath.Join(t.TempDir(), "cli.json")
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0600)
	o := &options{configPath: p}
	if err := o.save(config{URL: s.URL, Token: "token", CAFile: ca}); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, p, "", "servers", "list"); err != nil {
		t.Fatal(err)
	}
}

func TestHelpAndValidation(t *testing.T) {
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected API request") })
	out, err := execute(t, p, "", "--help")
	if err != nil || !strings.Contains(out, "containers") {
		t.Fatalf("help: %s %v", out, err)
	}
	for _, args := range [][]string{{"containers", "start"}, {"containers", "logs", "c1", "--tail", "-1"}, {"deploy", "compose.yaml"}, {"login"}, {"containers", "start", "../bad", "--server", "s1"}} {
		if _, err := execute(t, p, "", args...); err == nil {
			t.Errorf("expected validation error for %v", args)
		}
	}
}

func TestTableCellsAndEmptyResults(t *testing.T) {
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/images":
			io.WriteString(w, `[{"id":"sha256:a","repoTags":["nginx:alpine","nginx:latest"],"sizeBytes":377314550,"serverId":"s1"}]`)
		case "/api/deployments":
			io.WriteString(w, `[{"id":"d1","sourceName":"web","serverId":"s1","phase":"running","healthStatus":"healthy"}]`)
		case "/api/databases/d1":
			w.WriteHeader(204)
		case "/api/databases/d1/postgres/query":
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			io.WriteString(w, "current_database,two\nappdb,2\n")
		case "/api/auth/login":
			w.WriteHeader(401)
			io.WriteString(w, "invalid email or password")
		}
	})
	out, err := execute(t, p, "", "images", "list")
	if err != nil || !strings.Contains(out, "377314550") || strings.Contains(out, "e+") || !strings.Contains(out, "nginx:alpine,nginx:latest") {
		t.Fatalf("images: %q, %v", out, err)
	}
	out, err = execute(t, p, "", "deployments", "list")
	if err != nil || !strings.Contains(out, "PHASE") || !strings.Contains(out, "running") || !strings.Contains(out, "healthy") {
		t.Fatalf("deployments: %q, %v", out, err)
	}
	out, err = execute(t, p, `{"sql":"select 1"}`, "databases", "postgres", "query", "d1", "--file", "-")
	if err != nil || out != "current_database,two\nappdb,2\n" {
		t.Fatalf("query CSV: %q, %v", out, err)
	}
	var stdout, stderr bytes.Buffer
	cmd := NewCommand(strings.NewReader(""), &stdout, &stderr)
	cmd.SetArgs([]string{"--config", p, "databases", "remove", "d1"})
	if err = cmd.Execute(); err != nil || stdout.Len() != 0 || stderr.String() != "Done.\n" {
		t.Fatalf("empty result: stdout=%q stderr=%q err=%v", stdout.String(), stderr.String(), err)
	}
	_, err = execute(t, p, "wrong\n", "login", "--email", "a@example.com", "--password-stdin")
	if err == nil || strings.Contains(err.Error(), "renew") {
		t.Fatalf("wrong password error: %v", err)
	}
}
