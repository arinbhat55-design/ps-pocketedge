//go:build linux || darwin

// Package hostexec opens interactive shells as the agent's OS user.
package hostexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/shirou/gopsutil/v4/process"
)

// Session owns the terminal and its process tree until Close is called.
type Session struct {
	Conn      io.ReadWriteCloser
	cmd       *exec.Cmd
	file      *os.File
	done      chan struct{}
	waitErr   error
	closeOnce sync.Once
}

// Start launches a command in a controlling PTY. An empty command opens
// an interactive login shell; no Docker or Podman daemon is required.
func Start(ctx context.Context, args []string, cols, rows uint) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		args = []string{defaultShell(), "-il"}
	}
	if args[0] == "" {
		return nil, errors.New("host terminal requires an executable")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "TERM=dumb")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	file, err := pty.StartWithSize(cmd, dimensions(cols, rows))
	if err != nil {
		return nil, fmt.Errorf("start host terminal: %w", err)
	}
	// Construct a new os.File after setting O_NONBLOCK so Go registers
	// the PTY with its poller. Changing the original descriptor's flags
	// alone makes reads return EAGAIN instead of waiting on macOS.
	fd, err := syscall.Dup(int(file.Fd()))
	if err == nil {
		syscall.CloseOnExec(fd)
		err = syscall.SetNonblock(fd, true)
	}
	if err != nil {
		if fd >= 0 {
			_ = syscall.Close(fd)
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = file.Close()
		_ = cmd.Wait()
		return nil, err
	}
	name := file.Name()
	_ = file.Close()
	file = os.NewFile(uintptr(fd), name)
	s := &Session{cmd: cmd, file: file, done: make(chan struct{})}
	s.Conn = s
	go func() { s.waitErr = cmd.Wait(); close(s.done) }()
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.done:
		}
	}()
	return s, nil
}

func defaultShell() string {
	candidates := []string{os.Getenv("SHELL"), "/bin/bash", "/bin/sh"}
	if runtime.GOOS == "darwin" {
		candidates = []string{os.Getenv("SHELL"), "/bin/zsh", "/bin/sh"}
	}
	for _, path := range candidates {
		if !filepath.IsAbs(path) {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path
		}
	}
	return "/bin/sh"
}

func dimensions(cols, rows uint) *pty.Winsize {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	return &pty.Winsize{Cols: uint16(min(cols, 65535)), Rows: uint16(min(rows, 65535))}
}

func (s *Session) Read(b []byte) (int, error) {
	n, err := s.file.Read(b)
	// Linux reports EIO when the last slave handle closes; that is EOF.
	if errors.Is(err, syscall.EIO) {
		err = io.EOF
	}
	return n, err
}
func (s *Session) Write(b []byte) (int, error) { return s.file.Write(b) }
func (s *Session) Resize(ctx context.Context, cols, rows uint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return pty.Setsize(s.file, dimensions(cols, rows))
}
func (s *Session) ExitCode(ctx context.Context) (int, error) {
	select {
	case <-s.done:
		if s.waitErr != nil {
			var exit *exec.ExitError
			if !errors.As(s.waitErr, &exit) {
				return 0, s.waitErr
			}
		}
		return s.cmd.ProcessState.ExitCode(), nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (s *Session) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		// Interactive shells give jobs their own process groups. Kill their
		// descendants too, so leaving the page does not leave commands running.
		select {
		case <-s.done:
		default:
			if root, err := process.NewProcess(int32(s.cmd.Process.Pid)); err == nil {
				killChildren(root)
			}
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		}
		closeErr = s.file.Close()
	})
	return closeErr
}

func killChildren(parent *process.Process) {
	children, err := parent.Children()
	if err != nil {
		return
	}
	for _, child := range children {
		killChildren(child)
		_ = child.Kill()
	}
}
