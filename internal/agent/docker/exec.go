package docker

import (
	"context"
	"io"
	"net"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// ExecSession is a live, attached `docker exec` process running with a
// pty (so a shell's colors/cursor movement/line-editing work as expected)
// — one per interactive container terminal.
type ExecSession struct {
	Conn io.ReadWriteCloser

	execID string
	cli    *client.Client
}

// Resize changes the exec process's pty dimensions — called when the
// Flutter terminal widget's viewport size changes.
func (s *ExecSession) Resize(ctx context.Context, cols, rows uint) error {
	return s.cli.ContainerExecResize(ctx, s.execID, container.ResizeOptions{Width: cols, Height: rows})
}

// ExitCode blocks until the exec process has exited (poll-free: the caller
// only calls this after Conn.Read returns EOF, at which point the process
// has already exited) and returns its exit code.
func (s *ExecSession) ExitCode(ctx context.Context) (int, error) {
	inspect, err := s.cli.ContainerExecInspect(ctx, s.execID)
	if err != nil {
		return 0, err
	}
	return inspect.ExitCode, nil
}

// StartExec opens an interactive exec session inside containerID. cmd
// empty defaults to a plain shell, matching what a "container terminal"
// button implies without the caller needing to guess the image's shell.
func StartExec(ctx context.Context, cli *client.Client, containerID string, cmd []string, cols, rows uint) (*ExecSession, error) {
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh"}
	}

	created, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, err
	}

	attached, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return nil, err
	}

	if cols > 0 && rows > 0 {
		_ = cli.ContainerExecResize(ctx, created.ID, container.ResizeOptions{Width: cols, Height: rows})
	}

	return &ExecSession{
		Conn:   execConn{r: attached.Reader, c: attached.Conn},
		execID: created.ID,
		cli:    cli,
	}, nil
}

// execConn adapts types.HijackedResponse (a buffered Reader plus the
// underlying net.Conn for writing/closing) into a plain
// io.ReadWriteCloser — reads must go through Reader, not Conn directly,
// since HijackedResponse's initial handshake bytes may already sit in
// Reader's buffer.
type execConn struct {
	r io.Reader
	c net.Conn
}

func (e execConn) Read(p []byte) (int, error)  { return e.r.Read(p) }
func (e execConn) Write(p []byte) (int, error) { return e.c.Write(p) }
func (e execConn) Close() error                { return e.c.Close() }
