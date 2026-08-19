// Command agent is the PSpocketEdge per-node agent.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/stream"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
)

func main() {
	controlPlaneAddr := flag.String("server", "localhost:8443", "control-plane gRPC address")
	token := flag.String("token", "", "enrollment token")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting pspocketedge-agent", "version", version.String())

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runner := stream.New(log, *controlPlaneAddr, *token, hostname, runtime.GOOS, runtime.GOARCH, version.Version)
	if err := runner.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}
