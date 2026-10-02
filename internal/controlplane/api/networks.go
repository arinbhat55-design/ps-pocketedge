package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// networkListTimeout/networkOpTimeout mirror imageListTimeout/imageOpTimeout
// (images.go) — a single Docker Engine API round trip relayed over an
// already-open gRPC stream.
const (
	networkListTimeout = 10 * time.Second
	networkOpTimeout   = 30 * time.Second
)

// networkSummaryResponse is one network in a list response, with the owning
// server attached so a fleet-wide (no ?serverId filter) list can show where
// each network lives — same shape as imageSummaryResponse.
type networkSummaryResponse struct {
	ServerID     string            `json:"serverId"`
	ServerName   string            `json:"serverName"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Driver       string            `json:"driver"`
	Scope        string            `json:"scope"`
	Internal     bool              `json:"internal"`
	Labels       map[string]string `json:"labels,omitempty"`
	ContainerIDs []string          `json:"containerIds,omitempty"`
}

func networkSummaryToResponse(serverID, serverName string, n *agentv1.NetworkSummary) networkSummaryResponse {
	return networkSummaryResponse{
		ServerID:     serverID,
		ServerName:   serverName,
		ID:           n.GetId(),
		Name:         n.GetName(),
		Driver:       n.GetDriver(),
		Scope:        n.GetScope(),
		Internal:     n.GetInternal(),
		Labels:       n.GetLabels(),
		ContainerIDs: n.GetContainerIds(),
	}
}

// handleListNetworks serves the network inventory for one server
// (?serverId=) or, if omitted, fans out ListNetworksCommand to every server
// concurrently (same sync.WaitGroup pattern as handleListImages) and merges
// the results. Not part of the heartbeat, so this is always a live round
// trip — a server that's currently offline is simply omitted rather than
// erroring the whole request.
func handleListNetworks(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, waiter *deploy.NetworkListWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.URL.Query().Get("serverId")

		var targets []store.Server
		if serverID != "" {
			srv, err := st.GetServer(r.Context(), serverID)
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "server not found", http.StatusNotFound)
				return
			}
			if err != nil {
				log.Error("failed to load server", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			targets = []store.Server{*srv}
		} else {
			all, err := st.ListServers(r.Context())
			if err != nil {
				log.Error("failed to list servers", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			targets = all
		}

		results := make([][]networkSummaryResponse, len(targets))
		var wg sync.WaitGroup
		for i, srv := range targets {
			wg.Add(1)
			go func(i int, srv store.Server) {
				defer wg.Done()
				networks, err := fetchServerNetworks(dispatcher, waiter, srv.ID)
				if err != nil {
					return
				}
				out := make([]networkSummaryResponse, len(networks))
				for j, n := range networks {
					out[j] = networkSummaryToResponse(srv.ID, srv.Name, n)
				}
				results[i] = out
			}(i, srv)
		}
		wg.Wait()

		merged := []networkSummaryResponse{}
		for _, r := range results {
			merged = append(merged, r...)
		}
		writeJSON(w, http.StatusOK, merged)
	}
}

func fetchServerNetworks(dispatcher *deploy.Dispatcher, waiter *deploy.NetworkListWaiter, serverID string) ([]*agentv1.NetworkSummary, error) {
	requestID, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}

	ch, cleanup := waiter.Await(requestID)
	defer cleanup()

	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_ListNetworks{
			ListNetworks: &agentv1.ListNetworksCommand{RequestId: requestID, ServerId: serverID},
		},
	}
	if err := dispatcher.Send(serverID, cmd); err != nil {
		return nil, err
	}

	select {
	case result := <-ch:
		return result.GetNetworks(), nil
	case <-time.After(networkListTimeout):
		return nil, errOpTimeout
	}
}

// networkOpResponse is the JSON shape for a create/remove/connect/
// disconnect result.
type networkOpResponse struct {
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	NetworkID string `json:"networkId,omitempty"`
}

// sendAndAwaitNetworkOp mirrors sendAndAwaitImageOp.
func sendAndAwaitNetworkOp(dispatcher *deploy.Dispatcher, waiter *deploy.NetworkOpWaiter, serverID, requestID string, cmd *agentv1.ControlMessage, timeout time.Duration) (*agentv1.NetworkOpResult, error) {
	ch, cleanup := waiter.Await(requestID)
	defer cleanup()

	if err := dispatcher.Send(serverID, cmd); err != nil {
		return nil, err
	}

	select {
	case result := <-ch:
		return result, nil
	case <-time.After(timeout):
		return nil, errOpTimeout
	}
}

func writeNetworkOpResult(w http.ResponseWriter, dispatcher *deploy.Dispatcher, waiter *deploy.NetworkOpWaiter, serverID, requestID string, cmd *agentv1.ControlMessage, timeout time.Duration, successStatus int) {
	result, err := sendAndAwaitNetworkOp(dispatcher, waiter, serverID, requestID, cmd, timeout)
	if err != nil {
		if errors.Is(err, deploy.ErrAgentNotConnected) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
			return
		}
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
		return
	}

	status := http.StatusOK
	if result.GetSuccess() {
		status = successStatus
	}
	writeJSON(w, status, networkOpResponse{
		Success:   result.GetSuccess(),
		Error:     result.GetErrorMessage(),
		NetworkID: result.GetNetworkId(),
	})
}

// handleCreateNetwork creates a new user-defined network on serverID.
func handleCreateNetwork(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.NetworkOpWaiter) http.HandlerFunc {
	type request struct {
		Name     string            `json:"name"`
		Driver   string            `json:"driver,omitempty"`
		Internal bool              `json:"internal,omitempty"`
		Labels   map[string]string `json:"labels,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_CreateNetwork{
				CreateNetwork: &agentv1.CreateNetworkCommand{
					RequestId: requestID,
					ServerId:  serverID,
					Name:      req.Name,
					Driver:    req.Driver,
					Internal:  req.Internal,
					Labels:    req.Labels,
				},
			},
		}
		writeNetworkOpResult(w, dispatcher, waiter, serverID, requestID, cmd, networkOpTimeout, http.StatusCreated)
	}
}

// handleRemoveNetwork removes one network on serverID.
func handleRemoveNetwork(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.NetworkOpWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		networkID := r.PathValue("networkId")

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_RemoveNetwork{
				RemoveNetwork: &agentv1.RemoveNetworkCommand{RequestId: requestID, ServerId: serverID, NetworkId: networkID},
			},
		}
		writeNetworkOpResult(w, dispatcher, waiter, serverID, requestID, cmd, networkOpTimeout, http.StatusOK)
	}
}

// handleConnectContainerToNetwork attaches a container to a network.
func handleConnectContainerToNetwork(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.NetworkOpWaiter) http.HandlerFunc {
	type request struct {
		ContainerID string `json:"containerId"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		networkID := r.PathValue("networkId")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ContainerID == "" {
			http.Error(w, "containerId is required", http.StatusBadRequest)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_ConnectContainerToNetwork{
				ConnectContainerToNetwork: &agentv1.ConnectContainerToNetworkCommand{
					RequestId: requestID, ServerId: serverID, NetworkId: networkID, ContainerId: req.ContainerID,
				},
			},
		}
		writeNetworkOpResult(w, dispatcher, waiter, serverID, requestID, cmd, networkOpTimeout, http.StatusOK)
	}
}

// handleDisconnectContainerFromNetwork detaches a container from a network.
func handleDisconnectContainerFromNetwork(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.NetworkOpWaiter) http.HandlerFunc {
	type request struct {
		ContainerID string `json:"containerId"`
		Force       bool   `json:"force,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		networkID := r.PathValue("networkId")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ContainerID == "" {
			http.Error(w, "containerId is required", http.StatusBadRequest)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_DisconnectContainerFromNetwork{
				DisconnectContainerFromNetwork: &agentv1.DisconnectContainerFromNetworkCommand{
					RequestId: requestID, ServerId: serverID, NetworkId: networkID, ContainerId: req.ContainerID, Force: req.Force,
				},
			},
		}
		writeNetworkOpResult(w, dispatcher, waiter, serverID, requestID, cmd, networkOpTimeout, http.StatusOK)
	}
}
