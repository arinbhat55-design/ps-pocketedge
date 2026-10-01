package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func localRequest(remote, host, origin string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8080/api/auth/local-session", nil)
	r.RemoteAddr, r.Host = remote, host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestLocalRequestPolicy(t *testing.T) {
	m := NewManager([]byte("test-secret"))
	if err := m.SetDashboardOrigins([]string{"http://localhost:8090", "http://127.0.0.1:8090"}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, remote, host, origin string
		headers                    map[string]string
		want                       bool
	}{
		{"native app", "127.0.0.1:1234", "localhost:8080", "", nil, true},
		{"dashboard origin", "[::1]:1234", "127.0.0.1:8080", "http://localhost:8090", nil, true},
		{"dashboard origin, other spelling", "127.0.0.1:1234", "localhost:8080", "http://LOCALHOST:8090", nil, true},
		{"other localhost app", "127.0.0.1:1234", "localhost:8080", "http://localhost:3000", nil, false},
		{"remote peer", "192.0.2.1:1234", "localhost:8080", "", nil, false},
		{"external host", "127.0.0.1:1234", "evil.example:8080", "", nil, false},
		{"external origin", "127.0.0.1:1234", "localhost:8080", "https://evil.example", nil, false},
		{"spoofed origin", "127.0.0.1:1234", "localhost:8080", "http://localhost.evil.example:8090", nil, false},
		{"nginx default", "127.0.0.1:1234", "127.0.0.1:8080", "", map[string]string{"X-Real-IP": "203.0.113.9"}, false},
		{"forwarded", "127.0.0.1:1234", "localhost:8080", "", map[string]string{"Forwarded": "for=203.0.113.9"}, false},
		{"cloudflare tunnel", "127.0.0.1:1234", "localhost:8080", "", map[string]string{"Cf-Connecting-Ip": "203.0.113.9"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := localRequest(tt.remote, tt.host, tt.origin, tt.headers)
			if got := m.IsLocalRequest(r); got != tt.want {
				t.Fatalf("IsLocalRequest() = %v, want %v (refusal %q)", got, tt.want, m.LocalRequestRefusal(r))
			}
		})
	}
}

func TestDashboardOriginsMustBeLoopback(t *testing.T) {
	m := NewManager([]byte("test-secret"))
	for _, bad := range []string{"https://dashboard.example", "localhost:8090", "http://localhost:8090/app"} {
		if err := m.SetDashboardOrigins([]string{bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestLocalTokenOnlyWorksOnLocalRequests(t *testing.T) {
	m := NewManager([]byte("test-secret"))
	m.SetLocalSessionsAllowed(true)
	token, err := m.IssueLocalToken("u1", "admin@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AuthenticateRequest(localRequest("127.0.0.1:1234", "localhost:8080", "", nil), token); err != nil {
		t.Fatalf("local request rejected: %v", err)
	}
	for name, r := range map[string]*http.Request{
		"remote":       localRequest("192.0.2.1:1234", "localhost:8080", "", nil),
		"via proxy":    localRequest("127.0.0.1:1234", "localhost:8080", "", map[string]string{"X-Forwarded-For": "203.0.113.9"}),
		"other origin": localRequest("127.0.0.1:1234", "localhost:8080", "http://localhost:3000", nil),
	} {
		if _, err := m.AuthenticateRequest(r, token); err == nil {
			t.Errorf("%s: local token accepted", name)
		}
	}
	login, err := m.IssueToken("u1", "admin@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AuthenticateRequest(localRequest("192.0.2.1:1234", "cp.example:8080", "", nil), login); err != nil {
		t.Fatalf("login token rejected remotely: %v", err)
	}
}
