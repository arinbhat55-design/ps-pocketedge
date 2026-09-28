package main

import (
	"errors"
	"fmt"
	"net"
)

func validateListeners(grpcAddr, httpAddr, certFile, keyFile string) error {
	if (certFile == "") != (keyFile == "") {
		return errors.New("both TLS certificate and key files are required")
	}
	if certFile != "" {
		return nil
	}
	for _, addr := range []string{grpcAddr, httpAddr} {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return fmt.Errorf("invalid listener address %q: %w", addr, err)
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("listener %q requires TLS certificate and key; plaintext is limited to loopback", addr)
		}
	}
	return nil
}
