package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// proxyHeaders are set by reverse proxies and tunnels. A loopback peer
// carrying any of them is relaying someone else's request, so it is not a
// local user even though the connection itself comes from 127.0.0.1.
var proxyHeaders = []string{
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
	"X-Real-Ip",
	"Via",
	"Cf-Connecting-Ip",
	"True-Client-Ip",
	"X-Client-Ip",
	"Fastly-Client-Ip",
}

// SetDashboardOrigins sets the browser origins allowed to use local access.
// Requests without an Origin header (the native app, curl) are unaffected.
// Configure it before serving requests.
func (m *Manager) SetDashboardOrigins(origins []string) error {
	allowed := make(map[string]bool, len(origins))
	for _, raw := range origins {
		origin, err := normalizeOrigin(raw)
		if err != nil {
			return fmt.Errorf("dashboard origin %q: %w", raw, err)
		}
		u, _ := url.Parse(origin)
		if !IsLoopbackHost(u.Hostname()) {
			return fmt.Errorf("dashboard origin %q: local access only accepts loopback origins", raw)
		}
		allowed[origin] = true
	}
	m.dashboardOrigins = allowed
	return nil
}

// LocalRequestRefusal returns why r cannot use local access, or "" when it
// comes from a user on this machine: a loopback peer, addressed to a
// loopback host, not relayed by a proxy, and (for browsers) from an allowed
// dashboard origin. The Host and Origin checks stop DNS rebinding and other
// sites in the user's browser.
func (m *Manager) LocalRequestRefusal(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !IsLoopbackHost(peer) {
		return "request does not come from this computer"
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if !IsLoopbackHost(host) {
		return "request is not addressed to localhost"
	}
	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return "request was relayed by a proxy"
		}
	}
	if raw := r.Header.Get("Origin"); raw != "" {
		origin, err := normalizeOrigin(raw)
		if err != nil || !m.dashboardOrigins[origin] {
			return fmt.Sprintf("browser origin %s is not an allowed dashboard origin (set DASHBOARD_ORIGINS)", raw)
		}
	}
	return ""
}

func (m *Manager) IsLocalRequest(r *http.Request) bool {
	return m.LocalRequestRefusal(r) == ""
}

func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// normalizeOrigin reduces an origin to scheme://host:port with the default
// port made explicit, so "http://localhost" and "http://localhost:80" match.
func normalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("must be a bare http(s) origin")
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), nil
}
