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
