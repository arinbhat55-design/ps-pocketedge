package stream

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestTransportCredentialsRequireTLSRemotely(t *testing.T) {
	local, err := transportCredentials("localhost:8443", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if local.Info().SecurityProtocol != "insecure" {
		t.Fatal("loopback development connection should remain plaintext")
	}
	remote, err := transportCredentials("control.example:8443", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if remote.Info().SecurityProtocol != "tls" {
		t.Fatal("remote connection must use TLS")
	}
	if _, err := transportCredentials(":8443", false, ""); err == nil {
		t.Fatal("missing server host accepted")
	}
	if _, err := transportCredentials("localhost:8443", true, "/missing/ca.pem"); err == nil {
		t.Fatal("invalid CA file accepted")
	}
}

func TestBackupURLRequiresHTTPSRemotely(t *testing.T) {
	for _, raw := range []string{"http://control.example:8080/blob", "file:///tmp/blob", "https://user:pass@control.example/blob"} {
		if err := validateBackupURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"https://control.example/blob", "http://127.0.0.1:8080/blob"} {
		if err := validateBackupURL(raw); err != nil {
			t.Errorf("rejected %q: %v", raw, err)
		}
	}
}

func TestBackupHTTPClientTrustsConfiguredCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ready")
	}))
	defer server.Close()
	cert := server.Certificate()
	if cert == nil {
		t.Fatal("missing test certificate")
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := backupHTTPClient(path)
	if err != nil {
		t.Fatal(err)
	}
	response, err := downloadBlob(context.Background(), client, server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Close()
	data, err := io.ReadAll(response)
	if err != nil || string(data) != "ready" {
		t.Fatalf("response = %q, %v", data, err)
	}
	if _, err := backupHTTPClient(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing CA file accepted")
	}
}
