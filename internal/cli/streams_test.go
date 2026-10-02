package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func streamServer(t *testing.T, h func(*websocket.Conn, *http.Request)) string {
	t.Helper()
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" || r.URL.Query().Has("token") {
			t.Error("stream token must use bearer header")
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		h(conn, r)
	})
	return p
}

func TestFollowLogs(t *testing.T) {
	p := streamServer(t, func(c *websocket.Conn, r *http.Request) {
		if r.URL.Path != "/api/servers/s1/containers/c1/logs/stream" || r.URL.Query().Get("follow") != "true" || r.URL.Query().Get("tail") != "10" {
			t.Errorf("log request %s", r.URL)
		}
		c.WriteMessage(websocket.TextMessage, []byte(`{"lines":[{"timestampUnixNano":1,"stream":"stdout","message":"hello"}]}`))
		c.WriteMessage(websocket.TextMessage, []byte(`{"done":true}`))
	})
	out, err := execute(t, p, "", "containers", "logs", "c1", "--server", "s1", "--follow", "--tail", "10")
	if err != nil || !strings.Contains(out, "[stdout] hello") {
		t.Fatalf("logs: %q %v", out, err)
	}
	out, err = execute(t, p, "", "containers", "logs", "c1", "--server", "s1", "--follow", "--tail", "10", "--json")
	if err != nil || !strings.Contains(out, `"message":"hello"`) {
		t.Fatalf("JSON logs: %q %v", out, err)
	}
}

func TestFollowLogError(t *testing.T) {
	p := streamServer(t, func(c *websocket.Conn, r *http.Request) {
		c.WriteMessage(websocket.TextMessage, []byte(`{"done":true,"error":"container vanished"}`))
	})
	_, err := execute(t, p, "", "containers", "logs", "c1", "--server", "s1", "--follow")
	if err == nil || !strings.Contains(err.Error(), "container vanished") {
		t.Fatalf("error: %v", err)
	}
}

func TestStreamCancel(t *testing.T) {
	connected := make(chan struct{})
	disconnected := make(chan struct{})
	p := streamServer(t, func(c *websocket.Conn, r *http.Request) {
		close(connected)
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				close(disconnected)
				return
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := NewCommand(strings.NewReader(""), io.Discard, io.Discard)
	cmd.SetArgs([]string{"--config", p, "containers", "logs", "c1", "--server", "s1", "--follow"})
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("connection timeout")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not unblock stream")
	}
	select {
	case <-disconnected:
	case <-time.After(3 * time.Second):
		t.Fatal("server connection was not closed")
	}
}

func TestExecInputOutputAndExitStatus(t *testing.T) {
	for _, exit := range []int{0, 7} {
		p := streamServer(t, func(c *websocket.Conn, r *http.Request) {
			if r.URL.Path != "/api/servers/s1/containers/c1/exec" || !reflect.DeepEqual(r.URL.Query()["cmd"], []string{"/bin/sh", "-c", "echo 'two words'"}) {
				t.Errorf("command arguments: %s", r.URL)
			}
			var input bytes.Buffer
			for {
				kind, b, err := c.ReadMessage()
				if err != nil {
					t.Error(err)
					return
				}
				if kind != websocket.BinaryMessage {
					t.Errorf("input frame type: %d", kind)
				}
				if bytes.Equal(b, []byte{4}) {
					break
				}
				input.Write(b)
			}
			if input.String() != "hello\n" {
				t.Errorf("stdin=%q", input.String())
			}
			c.WriteMessage(websocket.BinaryMessage, []byte("remote output\r\n"))
			c.WriteJSON(map[string]any{"done": true, "exitCode": exit})
		})
		out, err := execute(t, p, "hello\n", "containers", "exec", "c1", "--server", "s1", "--", "/bin/sh", "-c", "echo 'two words'")
		if out != "remote output\r\n" {
			t.Errorf("output=%q", out)
		}
		if exit == 0 && err != nil {
			t.Fatal(err)
		}
		if exit != 0 {
			var remote *ExitError
			if !errors.As(err, &remote) || remote.Code != exit {
				t.Fatalf("exit: %v", err)
			}
		}
	}
}

func TestExecDisconnectAndTTYValidation(t *testing.T) {
	p := streamServer(t, func(c *websocket.Conn, r *http.Request) {})
	_, err := execute(t, p, "", "containers", "exec", "c1", "--server", "s1", "--", "/bin/true")
	if err == nil {
		t.Fatal("disconnect without remote status was considered success")
	}
	_, err = execute(t, p, "", "containers", "exec", "c1", "--server", "s1", "--tty", "--", "/bin/sh")
	if err == nil || !strings.Contains(err.Error(), "requires a terminal") {
		t.Fatalf("TTY validation: %v", err)
	}
}

func TestStreamHandshakeFailure(t *testing.T) {
	p, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "forbidden", 403) })
	_, err := execute(t, p, "", "containers", "exec", "c1", "--server", "s1")
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("handshake: %v", err)
	}
}

// Non-interactive exec must treat /dev/null (unpollable on macOS) and a
// closed stdin as end of input, not as a terminal failure.
func TestExecWithoutStdin(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	closed, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	for name, in := range map[string]*os.File{"dev null": devNull, "closed": closed} {
		t.Run(name, func(t *testing.T) {
			p := streamServer(t, func(c *websocket.Conn, r *http.Request) {
				if _, b, err := c.ReadMessage(); err != nil || !bytes.Equal(b, []byte{4}) {
					t.Errorf("want EOF (Ctrl+D), got %q, %v", b, err)
				}
				c.WriteMessage(websocket.BinaryMessage, []byte("ok"))
				c.WriteJSON(map[string]any{"done": true, "exitCode": 0})
			})
			var out, stderr bytes.Buffer
			cmd := NewCommand(in, &out, &stderr)
			cmd.SetArgs([]string{"--config", p, "containers", "exec", "c1", "--server", "s1", "--", "true"})
			if err := cmd.Execute(); err != nil || out.String() != "ok" {
				t.Fatalf("output=%q err=%v", out.String(), err)
			}
		})
	}
}
