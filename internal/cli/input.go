package cli

import (
	"context"
	"io"
)

func copyReader(ctx context.Context, r io.Reader, send func([]byte) error) error {
	buf := make([]byte, 4096)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(buf)
		if n > 0 {
			if e := send(buf[:n]); e != nil {
				return e
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
