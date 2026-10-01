// Command controlplane is the PSpocketEdge control-plane server.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/api"
	authpkg "github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/backup"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/dbops"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/grpcserver"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/insights"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/livestate"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/schedule"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	vaultpkg "github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/vault"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
)

// metricSampleRetention bounds how long server_metric_samples history is
// kept. No downsampling/rollup in this MVP slice — old samples are pruned
// outright by a periodic background sweep, not aggregated.
const metricSampleRetention = 24 * time.Hour

// resolvedAlertRetention bounds how long resolved container alerts are
// kept as history; open alerts are never pruned.
const resolvedAlertRetention = 30 * 24 * time.Hour

func main() {
	grpcAddr := flag.String("grpc-addr", "127.0.0.1:8443", "address for the agent gRPC service to listen on")
	httpAddr := flag.String("http-addr", "127.0.0.1:8080", "address for the REST API to listen on")
	tlsCert := flag.String("tls-cert", envOr("TLS_CERT_FILE", ""), "PEM certificate for HTTPS and agent gRPC (env TLS_CERT_FILE)")
	tlsKey := flag.String("tls-key", envOr("TLS_KEY_FILE", ""), "PEM private key for HTTPS and agent gRPC (env TLS_KEY_FILE)")
	databaseURL := flag.String("database-url", "postgres://pspocketedge:pspocketedge@localhost:55432/pspocketedge?sslmode=disable", "Postgres connection string")
	jwtSecret := flag.String("jwt-secret", os.Getenv("JWT_SECRET"), "secret used to sign admin session JWTs (env JWT_SECRET); a random one is generated per-process if unset, which invalidates sessions on restart")
	adminEmail := flag.String("admin-email", os.Getenv("ADMIN_EMAIL"), "email for the seeded admin user, only used if no users exist yet (env ADMIN_EMAIL)")
	adminPassword := flag.String("admin-password", os.Getenv("ADMIN_PASSWORD"), "password for the seeded admin user, only used if no users exist yet; generated and logged if unset (env ADMIN_PASSWORD)")
	publicURL := flag.String("public-url", envOr("PUBLIC_URL", "http://localhost:8080"), "URL agents use to reach this control plane's REST API, for backup/restore blob transfer (env PUBLIC_URL); must be reachable from every enrolled agent, not just localhost, once agents run on other machines")
	backupDir := flag.String("backup-dir", envOr("BACKUP_DIR", "./data/backups"), "local directory to store backup blobs in (env BACKUP_DIR)")
	vaultKeyFile := flag.String("vault-key-file", envOr("VAULT_KEY_FILE", "./data/vault.key"), "file holding the key that encrypts stored database credentials, generated on first start if missing (env VAULT_KEY_FILE); ignored when VAULT_KEY is set. Back it up: without it, stored credentials cannot be decrypted")
	dashboardOrigins := flag.String("dashboard-origins", envOr("DASHBOARD_ORIGINS", "http://localhost:8090,http://127.0.0.1:8090"), "comma-separated browser origins of the web dashboard allowed to use local access without login (env DASHBOARD_ORIGINS); run the web app on one of them, e.g. flutter run -d chrome --web-port 8090. The native desktop app is not affected")
	flag.Parse()
	if err := validateListeners(*grpcAddr, *httpAddr, *tlsCert, *tlsKey); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

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
	authMgr.SetRoleLookup(func(ctx context.Context, userID string) (string, error) {
		user, err := st.GetUserByID(ctx, userID)
		if errors.Is(err, store.ErrNotFound) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		return user.Role, nil
	})
	if err := authMgr.SetDashboardOrigins(splitList(*dashboardOrigins)); err != nil {
		log.Error("invalid -dashboard-origins", "error", err)
		os.Exit(2)
	}
	requireLocalLogin, err := st.RequireLocalLogin(ctx)
	if err != nil {
		log.Error("failed to load access settings; run database migrations", "error", err)
		os.Exit(1)
	}
	authMgr.SetLocalSessionsAllowed(isLoopbackListener(*httpAddr) && !requireLocalLogin)

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
	opWaiter := deploy.NewOpWaiter()
	imageListWaiter := deploy.NewImageListWaiter()
	imageDetailWaiter := deploy.NewImageDetailWaiter()
	imageOpWaiter := deploy.NewImageOpWaiter()
	logStreamRelay := deploy.NewLogStreamRelay()
	eventListWaiter := deploy.NewEventListWaiter()
	execStreamRelay := deploy.NewExecStreamRelay()
	networkListWaiter := deploy.NewNetworkListWaiter()
	networkOpWaiter := deploy.NewNetworkOpWaiter()
	volumeListWaiter := deploy.NewVolumeListWaiter()
	volumeDetailWaiter := deploy.NewVolumeDetailWaiter()
	volumeOpWaiter := deploy.NewVolumeOpWaiter()
	volumeFileWaiter := deploy.NewVolumeFileWaiter()
	buildBus := deploy.NewBuildBus()

	vaultKey, keyCreated, err := vaultpkg.LoadKey(os.Getenv("VAULT_KEY"), *vaultKeyFile)
	if err != nil {
		log.Error("failed to load vault key", "error", err)
		os.Exit(1)
	}
	if keyCreated {
		log.Warn("generated a new credential vault key — back this file up; stored database credentials cannot be decrypted without it", "file", *vaultKeyFile)
	}
	cipher, err := vaultpkg.NewCipher(vaultKey)
	if err != nil {
		log.Error("failed to initialize vault", "error", err)
		os.Exit(1)
	}
	credentialVault := vaultpkg.New(st, cipher)
	// Vault references in deployment env are resolved here, at the last
	// hop before an agent, and nowhere else.
	dispatcher.SetEnvResolver(credentialVault.EnvResolver())
	databaseOps := dbops.New(log, st, credentialVault, dispatcher, execStreamRelay)

	blobs, err := backup.NewBlobStore(*backupDir)
	if err != nil {
		log.Error("failed to initialize backup storage", "dir", *backupDir, "error", err)
		os.Exit(1)
	}

	grpcOptions := []grpc.ServerOption{}
	if *tlsCert != "" {
		pair, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
		if err != nil {
			log.Error("failed to load TLS certificate", "error", err)
			os.Exit(1)
		}
		grpcOptions = append(grpcOptions, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12})))
	}
	grpcServer := grpc.NewServer(grpcOptions...)
	agentv1.RegisterAgentSessionServer(grpcServer, grpcserver.New(log, st, dispatcher, events, serverEvents, inspectWaiter, opWaiter, imageListWaiter, imageDetailWaiter, imageOpWaiter, logStreamRelay, eventListWaiter, execStreamRelay, networkListWaiter, networkOpWaiter, volumeListWaiter, volumeDetailWaiter, volumeOpWaiter, volumeFileWaiter, buildBus))

	httpServer := &http.Server{
		Addr:    *httpAddr,
		Handler: api.NewRouter(log, st, authMgr, dispatcher, events, serverEvents, blobs, *publicURL, inspectWaiter, opWaiter, imageListWaiter, imageDetailWaiter, imageOpWaiter, logStreamRelay, eventListWaiter, execStreamRelay, networkListWaiter, networkOpWaiter, volumeListWaiter, volumeDetailWaiter, volumeOpWaiter, volumeFileWaiter, credentialVault, databaseOps, buildBus, isLoopbackListener(*httpAddr)),
	}

	scheduler := schedule.New(log, st, dispatcher, opWaiter)
	databaseScheduler := schedule.NewDatabaseScheduler(log, st, dispatcher, databaseOps, blobs, *publicURL, credentialVault)

	errCh := make(chan error, 2)
	go func() {
		log.Info("agent gRPC service listening", "addr", *grpcAddr)
		errCh <- grpcServer.Serve(lis)
	}()
	go func() {
		log.Info("REST API listening", "addr", *httpAddr)
		if *tlsCert != "" {
			errCh <- httpServer.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			errCh <- httpServer.ListenAndServe()
		}
	}()
	go pruneMetricsLoop(ctx, log, st)
	go scheduler.Run(ctx)
	go databaseScheduler.Run(ctx)
	go insights.NewEvaluator(log, st).Run(ctx)
	go api.RunGovernanceWorker(ctx, log, st, dispatcher, events, opWaiter, buildBus, *publicURL)

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
			if err := st.PruneContainerMetricSamplesOlderThan(ctx, cutoff); err != nil {
				log.Error("failed to prune old container metric samples", "error", err)
			}
			if err := st.PruneDatabaseMetricSamplesOlderThan(ctx, cutoff); err != nil {
				log.Error("failed to prune old database metric samples", "error", err)
			}
			if err := st.PruneResolvedContainerAlertsOlderThan(ctx, time.Now().Add(-resolvedAlertRetention)); err != nil {
				log.Error("failed to prune old resolved container alerts", "error", err)
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

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
