// Command controlplane is the PSpocketEdge control-plane server.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/api"
	authpkg "github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/backup"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/grpcserver"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/livestate"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
)

// metricSampleRetention bounds how long server_metric_samples history is
// kept. No downsampling/rollup in this MVP slice — old samples are pruned
// outright by a periodic background sweep, not aggregated.
const metricSampleRetention = 24 * time.Hour

func main() {
	grpcAddr := flag.String("grpc-addr", ":8443", "address for the agent gRPC service to listen on")
	httpAddr := flag.String("http-addr", ":8080", "address for the REST API to listen on")
	databaseURL := flag.String("database-url", "postgres://pspocketedge:pspocketedge@localhost:55432/pspocketedge?sslmode=disable", "Postgres connection string")
	jwtSecret := flag.String("jwt-secret", os.Getenv("JWT_SECRET"), "secret used to sign admin session JWTs (env JWT_SECRET); a random one is generated per-process if unset, which invalidates sessions on restart")
	adminEmail := flag.String("admin-email", os.Getenv("ADMIN_EMAIL"), "email for the seeded admin user, only used if no users exist yet (env ADMIN_EMAIL)")
	adminPassword := flag.String("admin-password", os.Getenv("ADMIN_PASSWORD"), "password for the seeded admin user, only used if no users exist yet; generated and logged if unset (env ADMIN_PASSWORD)")
	publicURL := flag.String("public-url", envOr("PUBLIC_URL", "http://localhost:8080"), "URL agents use to reach this control plane's REST API, for backup/restore blob transfer (env PUBLIC_URL); must be reachable from every enrolled agent, not just localhost, once agents run on other machines")
	backupDir := flag.String("backup-dir", envOr("BACKUP_DIR", "./data/backups"), "local directory to store backup blobs in (env BACKUP_DIR)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting pspocketedge-controlplane", "version", version.String())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, *databaseURL)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	secret := *jwtSecret
	if secret == "" {
		generated, err := authpkg.RandomToken()
		if err != nil {
			log.Error("failed to generate JWT secret", "error", err)
			os.Exit(1)
		}
		secret = generated
		log.Warn("no JWT_SECRET set, generated a random one for this process — existing sessions will be invalidated on every restart; set JWT_SECRET for a stable one")
	}
	authMgr := authpkg.NewManager([]byte(secret))

	if err := authpkg.SeedAdmin(ctx, log, st, *adminEmail, *adminPassword); err != nil {
		log.Error("failed to seed admin user", "error", err)
		os.Exit(1)
	}

	if err := seedCatalog(ctx, log, st); err != nil {
		log.Error("failed to seed catalog", "error", err)
		os.Exit(1)
	}

	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Error("failed to listen", "addr", *grpcAddr, "error", err)
		os.Exit(1)
	}

	dispatcher := deploy.NewDispatcher()
	events := deploy.NewEventBus()
	serverEvents := livestate.NewEventBus()
	inspectWaiter := deploy.NewInspectWaiter()

	blobs, err := backup.NewBlobStore(*backupDir)
	if err != nil {
		log.Error("failed to initialize backup storage", "dir", *backupDir, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	agentv1.RegisterAgentSessionServer(grpcServer, grpcserver.New(log, st, dispatcher, events, serverEvents, inspectWaiter))

	httpServer := &http.Server{
		Addr:    *httpAddr,
		Handler: api.NewRouter(log, st, authMgr, dispatcher, events, serverEvents, blobs, *publicURL, inspectWaiter),
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("agent gRPC service listening", "addr", *grpcAddr)
		errCh <- grpcServer.Serve(lis)
	}()
	go func() {
		log.Info("REST API listening", "addr", *httpAddr)
		errCh <- httpServer.ListenAndServe()
	}()
	go pruneMetricsLoop(ctx, log, st)

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		grpcServer.GracefulStop()
		_ = httpServer.Shutdown(context.Background())
	case err := <-errCh:
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// pruneMetricsLoop periodically deletes server_metric_samples older than
// metricSampleRetention, so the table doesn't grow unbounded — a heartbeat
// every ~20s per server adds up over days/weeks of uptime. Runs hourly
// rather than continuously since retention is measured in a day, not
// seconds.
func pruneMetricsLoop(ctx context.Context, log *slog.Logger, st *store.Store) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-metricSampleRetention)
			if err := st.PruneMetricSamplesOlderThan(ctx, cutoff); err != nil {
				log.Error("failed to prune old metric samples", "error", err)
			}
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
