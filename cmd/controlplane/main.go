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
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/grpcserver"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
)

func main() {
	grpcAddr := flag.String("grpc-addr", ":8443", "address for the agent gRPC service to listen on")
	httpAddr := flag.String("http-addr", ":8080", "address for the REST API to listen on")
	databaseURL := flag.String("database-url", "postgres://pspocketedge:pspocketedge@localhost:55432/pspocketedge?sslmode=disable", "Postgres connection string")
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

	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Error("failed to listen", "addr", *grpcAddr, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	agentv1.RegisterAgentSessionServer(grpcServer, grpcserver.New(log, st))

	httpServer := &http.Server{
		Addr:    *httpAddr,
		Handler: api.NewRouter(log, st),
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
