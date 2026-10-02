// Command agent is the PS-pocketEdge per-node agent.
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
	tlsEnabled := flag.Bool("tls", false, "use TLS even for a loopback control-plane address (remote addresses always require TLS)")
	tlsCAFile := flag.String("tls-ca-file", "", "optional PEM CA certificate for the control-plane gRPC server")
	allowBuilds := flag.Bool("allow-builds", false, "let the control plane build images from Git on this server's container runtime")
	buildDir := flag.String("build-dir", "", "where image builds clone repositories (default: a directory under the system temp dir)")
	containerRuntime := flag.String("runtime", "docker", "container runtime: docker or podman")
	containerHost := flag.String("container-host", "", "container API endpoint (defaults to DOCKER_HOST or the runtime socket)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting pspocketedge-agent", "version", version.String())

	serverAddr, enrollToken, statePathValue := *controlPlaneAddr, *token, *statePath
	useTLS, caFile := *tlsEnabled, *tlsCAFile
	builds, buildDirValue := *allowBuilds, *buildDir
	runtimeValue, hostValue := *containerRuntime, *containerHost
	explicitlySet := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { explicitlySet[f.Name] = true })
	if *configPath != "" {
		cfg, err := config.Load(*configPath)
		if err != nil {
			log.Error("failed to load config", "path", *configPath, "error", err)
			os.Exit(1)
		}
		if !explicitlySet["runtime"] && cfg.ContainerRuntime != "" {
			runtimeValue = cfg.ContainerRuntime
		}
		if !explicitlySet["container-host"] {
			hostValue = cfg.ContainerHost
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
		useTLS = useTLS || cfg.TLS
		if caFile == "" {
			caFile = cfg.TLSCAFile
		}
		builds = builds || cfg.AllowBuilds
		if buildDirValue == "" {
			buildDirValue = cfg.BuildDir
		}
	}

	if runtimeValue != "docker" && runtimeValue != "podman" {
		log.Error("unsupported container runtime", "runtime", runtimeValue)
		os.Exit(1)
	}

	log.Info("container runtime configured", "runtime", runtimeValue)

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runner := stream.New(log, serverAddr, enrollToken, hostname, runtime.GOOS, runtime.GOARCH, version.Version, statePathValue)
	runner.TLS = useTLS
	runner.TLSCAFile = caFile
	runner.AllowBuilds = builds
	runner.BuildDir = buildDirValue
	runner.ContainerRuntime = runtimeValue
	runner.ContainerHost = hostValue
	if builds {
		log.Info("image builds enabled on this server")
	}
	if err := runner.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}
