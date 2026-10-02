//go:build !windows

package cli

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Poll terminal/pipe input so exiting a stream does not leave a goroutine
// blocked in Read, or close the caller's standard input to interrupt it.
func copyInput(ctx context.Context, r io.Reader, send func([]byte) error) error {
	f, ok := r.(*os.File)
	if !ok {
		return copyReader(ctx, r, send)
	}
	// A closed *os.File reports fd -1, which poll silently ignores.
	if f.Fd() == ^uintptr(0) {
		return nil
	}
	fd := int(f.Fd())
	buf := make([]byte, 4096)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if poll[0].Revents&unix.POLLNVAL != 0 {
			// macOS cannot poll some devices such as /dev/null, and a closed
			// stdin is invalid too. Read directly: /dev/null reports EOF, and
			// a closed descriptor means there is no input to forward.
			err := copyReader(ctx, f, send)
			if errors.Is(err, unix.EBADF) || errors.Is(err, os.ErrClosed) {
				return nil
			}
			return err
		}
		n, err = unix.Read(fd, buf)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if err = send(buf[:n]); err != nil {
			return err
		}
	}
}
