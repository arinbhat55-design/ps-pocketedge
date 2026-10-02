package gitsource

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// lookupHost resolves a host name; a variable so tests can stub DNS.
var lookupHost = net.DefaultResolver.LookupNetIP

// checkRemote runs CheckURL before every remote operation, so a host that
// resolved to a public address when the repository was added can't later
// point at this machine. Only http(s) URLs reach the network this way;
// the API refuses any other repository URL when it's added (CheckURL), so
// the rest are on-disk repositories in tests.
func checkRemote(ctx context.Context, rawURL string) error {
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return nil
	}
	return CheckURL(ctx, rawURL)
}

// CheckURL reports why the control plane refuses to contact rawURL, or nil.
// Only http(s) URLs are allowed, and not ones whose host is (or resolves
// to) this machine, a link-local address such as the cloud metadata
// service at 169.254.169.254, or an unspecified/multicast address — a
// repository URL is admin input, but it must not turn the control plane
// into a way to reach its own internal endpoints. Private LAN addresses
// are allowed, for self-hosted Git servers.
func CheckURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid repository URL: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("repository URL must use https:// or http://")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("repository URL has no host")
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return fmt.Errorf("repository URL can't point at the control plane's own machine (%s)", host)
	}
	var addrs []netip.Addr
	if addr, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{addr}
	} else {
		addrs, err = lookupHost(ctx, "ip", host)
		if err != nil {
			return fmt.Errorf("couldn't resolve %s: %w", host, err)
		}
	}
	for _, addr := range addrs {
		addr = addr.Unmap()
		switch {
		case addr.IsLoopback():
			return fmt.Errorf("repository URL can't point at the control plane's own machine (%s resolves to %s)", host, addr)
		case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast():
			return fmt.Errorf("repository URL can't point at a link-local address (%s resolves to %s)", host, addr)
		case addr.IsUnspecified(), addr.IsMulticast():
			return fmt.Errorf("repository URL can't point at %s (%s)", addr, host)
		}
	}
	return nil
}

// FileNotFoundError is returned when a path doesn't exist at a commit.
type FileNotFoundError struct {
	Path   string
	Commit string
}

func (e *FileNotFoundError) Error() string {
	return fmt.Sprintf("file %q not found at commit %s", e.Path, e.Commit)
}
