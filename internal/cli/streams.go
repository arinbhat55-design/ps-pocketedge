package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ExitError preserves the process's remote exit status for shell scripts.
type ExitError struct{ Code int }

func (e *ExitError) Error() string {
	return fmt.Sprintf("remote command exited with status %d", e.Code)
}

func (c *client) socket(ctx context.Context, path string) (*websocket.Conn, error) {
	u, err := url.Parse(c.URL + path)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	t := c.http.Transport.(*http.Transport)
	d := websocket.Dialer{Proxy: t.Proxy, NetDialContext: t.DialContext, TLSClientConfig: t.TLSClientConfig, HandshakeTimeout: 30 * time.Second}
	conn, resp, err := d.DialContext(ctx, u.String(), http.Header{"Authorization": {"Bearer " + c.Token}})
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			return nil, fmt.Errorf("stream API %s: %s", resp.Status, strings.TrimSpace(string(b)))
		}
		return nil, fmt.Errorf("connect stream: %w", err)
	}
	conn.SetReadLimit(8 << 20)
	return conn, nil
}

// Close the socket on cancellation so a blocked read returns immediately.
func closeOnCancel(ctx context.Context, conn *websocket.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	return func() { close(done); conn.Close() }
}

func followLogs(cmd *cobra.Command, c *client, path string, tail int, asJSON bool) error {
	path += "/logs/stream?" + url.Values{"tail": {fmt.Sprint(tail)}, "follow": {"true"}}.Encode()
	conn, err := c.socket(cmd.Context(), path)
	if err != nil {
		return err
	}
	defer closeOnCancel(cmd.Context(), conn)()
	for {
		var chunk struct {
			ContainerID string `json:"containerId"`
			Lines       []struct {
				ContainerID string `json:"containerId"`
				Timestamp   int64  `json:"timestampUnixNano"`
				Stream      string `json:"stream"`
				Message     string `json:"message"`
			} `json:"lines"`
			Done  bool   `json:"done"`
			Error string `json:"error"`
		}
		if err := conn.ReadJSON(&chunk); err != nil {
			if cmd.Context().Err() != nil {
				return cmd.Context().Err()
			}
			return fmt.Errorf("log stream disconnected before completion: %w", err)
		}
		for _, line := range chunk.Lines {
			if asJSON {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(line); err != nil {
					return err
				}
			} else {
				ts := "?"
				if line.Timestamp > 0 {
					ts = time.Unix(0, line.Timestamp).Format(time.RFC3339Nano)
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s [%s] %s\n", ts, line.Stream, line.Message); err != nil {
					return err
				}
			}
		}
		if chunk.Error != "" {
			return fmt.Errorf("log stream: %s", chunk.Error)
		}
		if chunk.Done {
			return nil
		}
	}
}

func execCommand(o *options) *cobra.Command {
	var server string
	var tty bool
	c := &cobra.Command{Use: "exec <container-id> [--tty] -- [command args...]", Short: "Run a command in the container PTY (default: /bin/sh)", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 1 && cmd.ArgsLenAtDash() != 1 {
			return fmt.Errorf("put the command and its arguments after --")
		}
		if !validSegment(args[0]) {
			return fmt.Errorf("invalid container ID")
		}
		var fd int
		var inputFile *os.File
		if tty {
			var ok bool
			inputFile, ok = cmd.InOrStdin().(*os.File)
			if !ok || !term.IsTerminal(int(inputFile.Fd())) {
				return fmt.Errorf("--tty requires a terminal on standard input")
			}
			fd = int(inputFile.Fd())
		}
		c, err := o.client(true)
		if err != nil {
			return err
		}
		if server == "" {
			server, err = resolveServer(cmd, c, args[0])
			if err != nil {
				return err
			}
		}
		p, err := route("/api/servers", server, "containers", args[0], "exec")
		if err != nil {
			return err
		}
		cols, rows := 80, 24
		if tty {
			if w, h, err := term.GetSize(fd); err == nil {
				cols, rows = w, h
			}
		}
		q := url.Values{"cols": {fmt.Sprint(cols)}, "rows": {fmt.Sprint(rows)}}
		for _, arg := range args[1:] {
			q.Add("cmd", arg)
		}
		conn, err := c.socket(cmd.Context(), p+"?"+q.Encode())
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		defer closeOnCancel(ctx, conn)()
		if tty {
			state, err := term.MakeRaw(fd)
			if err != nil {
				return err
			}
			defer term.Restore(fd, state)
		}
		return runExec(ctx, cmd, conn, tty, fd, cols, rows)
	}}
	c.Flags().StringVar(&server, "server", "", "Server ID (inferred from inventory when omitted)")
	c.Flags().BoolVarP(&tty, "tty", "t", false, "Use raw local terminal input and forward window resizing")
	return c
}

func runExec(ctx context.Context, cmd *cobra.Command, conn *websocket.Conn, tty bool, fd, cols, rows int) error {
	ctx, cancel := context.WithCancel(ctx)
	inputDone := make(chan struct{})
	defer func() {
		cancel()
		conn.Close()
		// Unix terminal reads poll cancellation. Give the reader time to
		// finish before restoring cooked mode in the caller. A Windows
		// console read may wait until the next key or process exit.
		select {
		case <-inputDone:
		case <-time.After(200 * time.Millisecond):
		}
	}()
	var mu sync.Mutex
	write := func(kind int, b []byte) error {
		mu.Lock()
		defer mu.Unlock()
		if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		return conn.WriteMessage(kind, b)
	}
	inputErr := make(chan error, 1)
	go func() {
		defer close(inputDone)
		err := copyInput(ctx, cmd.InOrStdin(), func(b []byte) error { return write(websocket.BinaryMessage, b) })
		if err == nil && !tty {
			err = write(websocket.BinaryMessage, []byte{4})
		}
		if err != nil && ctx.Err() == nil {
			inputErr <- err
			conn.Close()
		}
	}()
	if tty {
		go func() {
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					w, h, err := term.GetSize(fd)
					if err == nil && w > 0 && h > 0 && (w != cols || h != rows) {
						cols, rows = w, h
						if err := write(websocket.TextMessage, []byte(fmt.Sprintf("resize:%dx%d", w, h))); err != nil {
							conn.Close()
							return
						}
					}
				}
			}
		}()
	}
	for {
		kind, b, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case err := <-inputErr:
				return fmt.Errorf("terminal input: %w", err)
			default:
			}
			return fmt.Errorf("exec stream disconnected before exit status: %w", err)
		}
		if kind == websocket.BinaryMessage {
			if _, err := cmd.OutOrStdout().Write(b); err != nil {
				return err
			}
			continue
		}
		if kind != websocket.TextMessage {
			continue
		}
		var end struct {
			Done     bool   `json:"done"`
			ExitCode int    `json:"exitCode"`
			Error    string `json:"error"`
		}
		if err := json.Unmarshal(b, &end); err != nil {
			return fmt.Errorf("invalid exec completion: %w", err)
		}
		if !end.Done {
			continue
		}
		if end.Error != "" {
			return fmt.Errorf("exec: %s", end.Error)
		}
		if end.ExitCode != 0 {
			return &ExitError{Code: end.ExitCode}
		}
		return nil
	}
}
