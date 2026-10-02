package main

import "testing"

func TestValidateListeners(t *testing.T) {
	if err := validateListeners("127.0.0.1:8443", "localhost:8080", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateListeners(":8443", "127.0.0.1:8080", "", ""); err == nil {
		t.Fatal("public plaintext gRPC accepted")
	}
	if err := validateListeners("127.0.0.1:8443", "0.0.0.0:8080", "", ""); err == nil {
		t.Fatal("public plaintext HTTP accepted")
	}
	if err := validateListeners(":8443", ":8080", "cert.pem", "key.pem"); err != nil {
		t.Fatal(err)
	}
	if err := validateListeners("127.0.0.1:8443", "127.0.0.1:8080", "cert.pem", ""); err == nil {
		t.Fatal("incomplete TLS config accepted")
	}
}

func TestLocalAccessOnlyOnLoopbackListener(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080"} {
		if !isLoopbackListener(addr) {
			t.Errorf("local access disabled for %s", addr)
		}
	}
	for _, addr := range []string{"0.0.0.0:8080", ":8080", "192.0.2.1:8080"} {
		if isLoopbackListener(addr) {
			t.Errorf("local access enabled for %s", addr)
		}
	}
}
