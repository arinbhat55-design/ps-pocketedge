//go:build !windows

package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Run the command in a child process so Python can attach a real PTY and
// inspect its settings before and after command completion/cancellation.
func TestExecPTYHelper(t *testing.T) {
	if os.Getenv("PSE_PTY_HELPER") != "1" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := NewCommand(os.Stdin, os.Stdout, os.Stderr)
	cmd.SetArgs([]string{"--config", os.Getenv("PSE_PTY_CONFIG"), "containers", "exec", "c1", "--server", "s1", "--tty", "--", "/bin/sh"})
	err := cmd.ExecuteContext(ctx)
	code := 0
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
		var remote *ExitError
		if errors.As(err, &remote) {
			code = remote.Code
		}
		if errors.Is(err, context.Canceled) {
			code = 130
		}
	}
	os.Exit(code)
}

func TestRealPTYRestoreResizeAndCancel(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python is needed to create and inspect the test PTY")
	}
	for _, mode := range []string{"complete", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			p := streamServer(t, func(c *websocket.Conn, r *http.Request) {
				c.SetReadDeadline(time.Now().Add(10 * time.Second))
				if r.URL.Query().Get("cols") != "90" || r.URL.Query().Get("rows") != "30" {
					t.Errorf("initial terminal dimensions: %s", r.URL)
				}
				if mode == "cancel" {
					for {
						if _, _, err := c.ReadMessage(); err != nil {
							return
						}
					}
				}
				input, resize := false, false
				for !input || !resize {
					kind, b, err := c.ReadMessage()
					if err != nil {
						t.Error(err)
						return
					}
					if kind == websocket.BinaryMessage {
						if string(b) != "hello\n" {
							t.Errorf("raw input %q", b)
						}
						input = true
					}
					if kind == websocket.TextMessage && string(b) == "resize:130x45" {
						resize = true
					}
				}
				c.WriteMessage(websocket.BinaryMessage, []byte("done\r\n"))
				c.WriteJSON(map[string]any{"done": true, "exitCode": 7})
			})
			const script = `
import os, sys, pty, termios, fcntl, struct, subprocess, time, signal
master, slave = pty.openpty()
before = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 30, 90, 0, 0))
env = dict(os.environ, PSE_PTY_HELPER='1', PSE_PTY_CONFIG=sys.argv[2])
process = subprocess.Popen([sys.argv[1], '-test.run=^TestExecPTYHelper$'], stdin=slave, stdout=slave, stderr=slave, env=env)
try:
    deadline = time.monotonic() + 10
    while termios.tcgetattr(slave)[3] & termios.ICANON:
        if process.poll() is not None or time.monotonic() > deadline:
            raise AssertionError('CLI did not enter raw terminal mode')
        time.sleep(0.01)
    if sys.argv[3] == 'cancel':
        process.send_signal(signal.SIGTERM)
        expected = 130
    else:
        os.write(master, b'hello\n')
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 45, 130, 0, 0))
        expected = 7
    code = process.wait(timeout=10)
    assert code == expected, ('exit status', code, expected)
    after = termios.tcgetattr(slave)
    # The kernel may set PENDIN when restoring the line discipline.
    # Compare persistent settings, excluding this transient input flag.
    before[3] &= ~getattr(termios, 'PENDIN', 0)
    after[3] &= ~getattr(termios, 'PENDIN', 0)
    assert after == before, ('terminal state was not restored', before, after)
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
`
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			proc := exec.CommandContext(ctx, python, "-c", script, os.Args[0], p, mode)
			output, err := proc.CombinedOutput()
			if err != nil {
				t.Fatalf("PTY check: %v\n%s", err, strings.TrimSpace(string(output)))
			}
		})
	}
}
