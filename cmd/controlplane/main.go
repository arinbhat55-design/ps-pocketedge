// Command controlplane is the PSpocketEdge control-plane server.
package main

import (
	"flag"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/grpcserver"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
)

func main() {
	grpcAddr := flag.String("grpc-addr", ":8443", "address for the agent gRPC service to listen on")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	log.Info("starting pspocketedge-controlplane", "version", version.String())

	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Error("failed to listen", "addr", *grpcAddr, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	agentv1.RegisterAgentSessionServer(grpcServer, grpcserver.New(log))

	log.Info("agent gRPC service listening", "addr", *grpcAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Error("grpc server stopped", "error", err)
		os.Exit(1)
	}
}
