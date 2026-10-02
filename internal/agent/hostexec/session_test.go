//go:build linux || darwin

package hostexec

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHostPTYOutputAndExitCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := Start(ctx, []string{"/bin/sh", "-c", "test -t 0 && printf host-pty-ok; exit 7"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	output, err := io.ReadAll(session.Conn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "host-pty-ok") {
		t.Fatalf("output: %q", output)
	}
	code, err := session.ExitCode(ctx)
	if err != nil || code != 7 {
		t.Fatalf("exit=%d err=%v", code, err)
	}
}

func TestHostPTYInputAndResize(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := Start(ctx, []string{"/bin/sh", "-c", "read value; stty size; printf 'received:%s' \"$value\""}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Resize(ctx, 120, 40); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Conn.Write([]byte("hello host\n")); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(session.Conn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "40 120") || !strings.Contains(string(output), "received:hello host") {
		t.Fatalf("output: %q", output)
	}
}

func TestCancelStopsHostAndChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := Start(ctx, []string{"/bin/sh", "-c", "sleep 30 & echo $!; wait"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	var childPID int
	// The shell prints its child PID before waiting; this is a deterministic
	// cancellation test rather than relying on a startup sleep.
	if _, err := fmt.Fscanln(session.Conn, &childPID); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	if _, err := session.ExitCode(waitCtx); err != nil {
		t.Fatal(err)
	}
	child, err := os.FindProcess(childPID)
	if err != nil {
		t.Fatal(err)
	}
	// gopsutil's PID lookup treats exited/zombie children as non-running.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := child.Signal(syscall.Signal(0)); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child %d survived terminal cancellation", childPID)
}

func TestInvalidCommandAndCancelledContext(t *testing.T) {
	if _, err := Start(context.Background(), []string{"/missing/host-shell"}, 80, 24); err == nil {
		t.Fatal("missing executable accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Start(ctx, nil, 80, 24); err == nil {
		t.Fatal("cancelled context started a shell")
	}
}

func TestHostPTYCtrlC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := Start(ctx, []string{"/bin/sh", "-c", "trap 'echo interrupted; exit 0' INT; echo ready; while :; do sleep 1; done"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	var ready string
	if _, err := fmt.Fscanln(session.Conn, &ready); err != nil || ready != "ready" {
		t.Fatalf("not ready: %q %v", ready, err)
	}
	if _, err := session.Conn.Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(session.Conn)
	if err != nil || !strings.Contains(string(output), "interrupted") {
		t.Fatalf("Ctrl+C output: %q %v", output, err)
	}
}
