package gitsource

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestCheckURL(t *testing.T) {
	original := lookupHost
	t.Cleanup(func() { lookupHost = original })
	lookupHost = func(_ context.Context, _, host string) ([]netip.Addr, error) {
		switch host {
		case "github.com":
			return []netip.Addr{netip.MustParseAddr("140.82.112.3")}, nil
		case "gitea.lan":
			return []netip.Addr{netip.MustParseAddr("192.168.1.20")}, nil
		case "rebind.example":
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return nil, errors.New("no such host")
	}

	for _, ok := range []string{"https://github.com/docker/awesome-compose.git", "http://gitea.lan:3000/team/app.git", "https://10.0.0.5/x.git"} {
		if err := CheckURL(context.Background(), ok); err != nil {
			t.Errorf("CheckURL(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"file:///etc", "ssh://git@github.com/x.git", "https:///x.git",
		"http://127.0.0.1:18080/x.git", "http://localhost/x.git", "http://[::1]/x.git",
		"http://169.254.169.254/latest/meta-data", "http://0.0.0.0/x.git", "https://rebind.example/x.git",
		"https://unresolvable.invalid/x.git",
	} {
		if err := CheckURL(context.Background(), bad); err == nil {
			t.Errorf("CheckURL(%q) = nil, want an error", bad)
		}
	}
}
