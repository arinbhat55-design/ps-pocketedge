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

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/config"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/agent/stream"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
)

func main() {
	configPath := flag.String("config", "", "path to agent YAML config (written by scripts/install-agent.sh); CLI flags below override it")
	controlPlaneAddr := flag.String("server", "localhost:8443", "control-plane gRPC address")
	token := flag.String("token", "", "enrollment token (single-use; only needed on first run)")
	statePath := flag.String("state-path", "", "where to persist the agent's identity across restarts (empty = re-enroll every run, for local dev)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting pspocketedge-agent", "version", version.String())

	serverAddr, enrollToken, statePathValue := *controlPlaneAddr, *token, *statePath
	if *configPath != "" {
		cfg, err := config.Load(*configPath)
		if err != nil {
			log.Error("failed to load config", "path", *configPath, "error", err)
			os.Exit(1)
		}
		if cfg.Server != "" {
			serverAddr = cfg.Server
		}
		if cfg.Token != "" {
			enrollToken = cfg.Token
		}
		if cfg.StatePath != "" {
			statePathValue = cfg.StatePath
		}
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runner := stream.New(log, serverAddr, enrollToken, hostname, runtime.GOOS, runtime.GOARCH, version.Version, statePathValue)
	if err := runner.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}
