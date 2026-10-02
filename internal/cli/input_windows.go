//go:build windows

package cli

import (
	"context"
	"io"
)

func copyInput(ctx context.Context, r io.Reader, send func([]byte) error) error {
	return copyReader(ctx, r, send)
}
