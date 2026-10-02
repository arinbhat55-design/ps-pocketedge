package stream

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// transportCredentials permits plaintext only to a local development server.
// Remote agents always verify the server's certificate before enrollment.
func transportCredentials(addr string, forceTLS bool, caFile string) (credentials.TransportCredentials, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return nil, fmt.Errorf("server must be a host:port address: %q", addr)
	}
	isLoopback := strings.EqualFold(host, "localhost")
	if ip := net.ParseIP(host); ip != nil {
		isLoopback = ip.IsLoopback()
	}
	if !forceTLS && caFile == "" && isLoopback {
		return insecure.NewCredentials(), nil
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		roots, err := rootCAs(caFile)
		if err != nil {
			return nil, err
		}
		config.RootCAs = roots
	}
	return credentials.NewTLS(config), nil
}

func rootCAs(caFile string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read TLS CA file: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("TLS CA file contains no certificates")
	}
	return roots, nil
}

func backupHTTPClient(caFile string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		roots, err := rootCAs(caFile)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

func validateBackupURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return errors.New("invalid backup transfer URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (strings.EqualFold(u.Hostname(), "localhost") || ip != nil && ip.IsLoopback()) {
		return nil
	}
	return errors.New("remote backup transfer URL must use HTTPS")
}
