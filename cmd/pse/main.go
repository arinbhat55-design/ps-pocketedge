package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.NewCommand(os.Stdin, os.Stdout, os.Stderr).ExecuteContext(ctx); err != nil {
		// Ctrl+C is a normal way to end live logs; exit quietly with 130.
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "pse:", err)
		var remote *cli.ExitError
		if errors.As(err, &remote) && remote.Code > 0 && remote.Code < 256 {
			os.Exit(remote.Code)
		}
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		os.Exit(1)
	}
}
