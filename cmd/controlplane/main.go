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

	"google.golang.org/grpc"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/api"
	authpkg "github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/grpcserver"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
)

func main() {
	grpcAddr := flag.String("grpc-addr", ":8443", "address for the agent gRPC service to listen on")
	httpAddr := flag.String("http-addr", ":8080", "address for the REST API to listen on")
	databaseURL := flag.String("database-url", "postgres://pspocketedge:pspocketedge@localhost:55432/pspocketedge?sslmode=disable", "Postgres connection string")
	jwtSecret := flag.String("jwt-secret", os.Getenv("JWT_SECRET"), "secret used to sign admin session JWTs (env JWT_SECRET); a random one is generated per-process if unset, which invalidates sessions on restart")
	adminEmail := flag.String("admin-email", os.Getenv("ADMIN_EMAIL"), "email for the seeded admin user, only used if no users exist yet (env ADMIN_EMAIL)")
	adminPassword := flag.String("admin-password", os.Getenv("ADMIN_PASSWORD"), "password for the seeded admin user, only used if no users exist yet; generated and logged if unset (env ADMIN_PASSWORD)")
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

	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Error("failed to listen", "addr", *grpcAddr, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	agentv1.RegisterAgentSessionServer(grpcServer, grpcserver.New(log, st))

	httpServer := &http.Server{
		Addr:    *httpAddr,
		Handler: api.NewRouter(log, st, authMgr),
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
