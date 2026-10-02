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
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/registryclient"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// imageListTimeout/imageDetailTimeout mirror inspectTimeout (containers.go)
// — a single Docker Engine API round trip relayed over an already-open
// gRPC stream.
const (
	imageListTimeout   = 10 * time.Second
	imageDetailTimeout = 10 * time.Second
	// imagePullTimeout is far larger than containerOpTimeout: a cold pull
	// of a multi-GB image over a slow edge connection is expected to take
	// minutes, not seconds. No progress streaming in this pass (matches
	// DeployStatus's existing phase-only, not percentage, precedent) — the
	// client just waits on this one request.
	imagePullTimeout = 10 * time.Minute
	// imageOpTimeout covers remove/prune, both fast local Docker Engine
	// calls once dispatched.
	imageOpTimeout = 60 * time.Second
)

// imageSummaryResponse is one image in a list response, with the owning
// server attached so a fleet-wide (no ?serverId filter) list can show
// where each image lives.
type imageSummaryResponse struct {
	ServerID        string   `json:"serverId"`
	ServerName      string   `json:"serverName"`
	ID              string   `json:"id"`
	RepoTags        []string `json:"repoTags"`
	RepoDigests     []string `json:"repoDigests"`
	SizeBytes       int64    `json:"sizeBytes"`
	CreatedUnix     int64    `json:"createdUnix"`
	Dangling        bool     `json:"dangling"`
	ContainersCount int64    `json:"containersCount"`
}

func imageSummaryToResponse(serverID, serverName string, img *agentv1.ImageSummary) imageSummaryResponse {
	return imageSummaryResponse{
		ServerID:        serverID,
		ServerName:      serverName,
		ID:              img.GetId(),
		RepoTags:        img.GetRepoTags(),
		RepoDigests:     img.GetRepoDigests(),
		SizeBytes:       img.GetSizeBytes(),
		CreatedUnix:     img.GetCreatedUnix(),
		Dangling:        img.GetDangling(),
		ContainersCount: img.GetContainersCount(),
	}
}

// handleListImages serves the image inventory for one server (?serverId=)
// or, if omitted, fans out ListImagesCommand to every server concurrently
// (same sync.WaitGroup pattern as handleBulkContainerAction) and merges the
// results. Images aren't part of the heartbeat (see ListImagesCommand's
// proto doc comment), so this is always a live round trip, not a DB read —
// a server that's currently offline is simply omitted rather than erroring
// the whole request.
func handleListImages(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, waiter *deploy.ImageListWaiter) http.HandlerFunc {
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

		results := make([][]imageSummaryResponse, len(targets))
		var wg sync.WaitGroup
		for i, srv := range targets {
			wg.Add(1)
			go func(i int, srv store.Server) {
				defer wg.Done()
				images, err := fetchServerImages(dispatcher, waiter, srv.ID)
				if err != nil {
					return
				}
				out := make([]imageSummaryResponse, len(images))
				for j, img := range images {
					out[j] = imageSummaryToResponse(srv.ID, srv.Name, img)
				}
				results[i] = out
			}(i, srv)
		}
		wg.Wait()

		merged := []imageSummaryResponse{}
		for _, r := range results {
			merged = append(merged, r...)
		}
		writeJSON(w, http.StatusOK, merged)
	}
}

func fetchServerImages(dispatcher *deploy.Dispatcher, waiter *deploy.ImageListWaiter, serverID string) ([]*agentv1.ImageSummary, error) {
	requestID, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}

	ch, cleanup := waiter.Await(requestID)
	defer cleanup()

	cmd := &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_ListImages{
			ListImages: &agentv1.ListImagesCommand{RequestId: requestID, ServerId: serverID},
		},
	}
	if err := dispatcher.Send(serverID, cmd); err != nil {
		return nil, err
	}

	select {
	case result := <-ch:
		return result.GetImages(), nil
	case <-time.After(imageListTimeout):
		return nil, errOpTimeout
	}
}

// imageDetailResponse is the JSON shape for a successful image inspect.
type imageDetailResponse struct {
	ID           string            `json:"id"`
	RepoTags     []string          `json:"repoTags"`
	RepoDigests  []string          `json:"repoDigests"`
	SizeBytes    int64             `json:"sizeBytes"`
	CreatedUnix  int64             `json:"createdUnix"`
	Architecture string            `json:"architecture"`
	OS           string            `json:"os"`
	Layers       []imageLayerJSON  `json:"layers"`
	Env          []string          `json:"env"`
	Labels       map[string]string `json:"labels"`
}

type imageLayerJSON struct {
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"sizeBytes"`
}

// handleInspectImage dispatches an InspectImageCommand to the target agent
// and blocks up to imageDetailTimeout for its ImageDetail reply — same
// dispatch+correlate+timeout shape as handleInspectContainer.
func handleInspectImage(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.ImageDetailWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		imageID := r.PathValue("imageId")

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		ch, cleanup := waiter.Await(requestID)
		defer cleanup()

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_InspectImage{
				InspectImage: &agentv1.InspectImageCommand{RequestId: requestID, ServerId: serverID, ImageId: imageID},
			},
		}
		if err := dispatcher.Send(serverID, cmd); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
			return
		}

		select {
		case detail := <-ch:
			if !detail.GetFound() {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": detail.GetErrorMessage()})
				return
			}
			layers := make([]imageLayerJSON, len(detail.GetLayers()))
			for i, l := range detail.GetLayers() {
				layers[i] = imageLayerJSON{Digest: l.GetDigest(), SizeBytes: l.GetSizeBytes()}
			}
			writeJSON(w, http.StatusOK, imageDetailResponse{
				ID:           detail.GetId(),
				RepoTags:     detail.GetRepoTags(),
				RepoDigests:  detail.GetRepoDigests(),
				SizeBytes:    detail.GetSizeBytes(),
				CreatedUnix:  detail.GetCreatedUnix(),
				Architecture: detail.GetArchitecture(),
				OS:           detail.GetOs(),
				Layers:       layers,
				Env:          detail.GetEnv(),
				Labels:       detail.GetLabels(),
			})
		case <-time.After(imageDetailTimeout):
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for agent response"})
		case <-r.Context().Done():
		}
	}
}

// imageOpResponse is the JSON shape for a pull/remove/prune result.
type imageOpResponse struct {
	Success        bool   `json:"success"`
	Error          string `json:"error,omitempty"`
	ImageID        string `json:"imageId,omitempty"`
	ReclaimedBytes int64  `json:"reclaimedBytes,omitempty"`
}

// sendAndAwaitImageOp mirrors sendAndAwaitContainerOp, parameterized on
// timeout since pull needs far longer than remove/prune.
func sendAndAwaitImageOp(dispatcher *deploy.Dispatcher, waiter *deploy.ImageOpWaiter, serverID, requestID string, cmd *agentv1.ControlMessage, timeout time.Duration) (*agentv1.ImageOpResult, error) {
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

func writeImageOpResult(w http.ResponseWriter, dispatcher *deploy.Dispatcher, waiter *deploy.ImageOpWaiter, serverID, requestID string, cmd *agentv1.ControlMessage, timeout time.Duration, successStatus int) {
	result, err := sendAndAwaitImageOp(dispatcher, waiter, serverID, requestID, cmd, timeout)
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
	writeJSON(w, status, imageOpResponse{
		Success:        result.GetSuccess(),
		Error:          result.GetErrorMessage(),
		ImageID:        result.GetImageId(),
		ReclaimedBytes: result.GetReclaimedBytes(),
	})
}

// handlePullImage pulls an image onto one server, optionally authenticating
// against a configured private registry (?registryId, matched by id, its
// stored credentials attached to the PullImageCommand).
func handlePullImage(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, waiter *deploy.ImageOpWaiter) http.HandlerFunc {
	type request struct {
		ImageRef   string `json:"imageRef"`
		RegistryID string `json:"registryId,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")

		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ImageRef == "" {
			http.Error(w, "imageRef is required", http.StatusBadRequest)
			return
		}

		if err := enforceImagePolicy(r.Context(), st, []string{req.ImageRef}); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}

		var auth *agentv1.RegistryAuth
		if req.RegistryID != "" {
			reg, err := st.GetRegistry(r.Context(), req.RegistryID)
			if errors.Is(err, store.ErrNotFound) {
				http.Error(w, "registry not found", http.StatusNotFound)
				return
			}
			if err != nil {
				log.Error("failed to load registry", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			auth = &agentv1.RegistryAuth{Username: reg.Username, Password: reg.Password}
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_PullImage{
				PullImage: &agentv1.PullImageCommand{
					RequestId: requestID,
					ServerId:  serverID,
					ImageRef:  req.ImageRef,
					Auth:      auth,
				},
			},
		}
		writeImageOpResult(w, dispatcher, waiter, serverID, requestID, cmd, imagePullTimeout, http.StatusOK)
	}
}

// handleRemoveImage removes one image on serverID.
func handleRemoveImage(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.ImageOpWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")
		imageID := r.PathValue("imageId")
		force := r.URL.Query().Get("force") == "true"

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_RemoveImage{
				RemoveImage: &agentv1.RemoveImageCommand{RequestId: requestID, ServerId: serverID, ImageId: imageID, Force: force},
			},
		}
		writeImageOpResult(w, dispatcher, waiter, serverID, requestID, cmd, imageOpTimeout, http.StatusOK)
	}
}

// handlePruneImages removes dangling (or, with all:true, every unused)
// image on serverID and reports bytes reclaimed.
func handlePruneImages(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.ImageOpWaiter) http.HandlerFunc {
	type request struct {
		All bool `json:"all,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.PathValue("id")

		var req request
		_ = json.NewDecoder(r.Body).Decode(&req)

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}

		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_PruneImages{
				PruneImages: &agentv1.PruneImagesCommand{RequestId: requestID, ServerId: serverID, All: req.All},
			},
		}
		writeImageOpResult(w, dispatcher, waiter, serverID, requestID, cmd, imageOpTimeout, http.StatusOK)
	}
}

// handleImageUpdateAvailable compares a local image's digest (fetched live
// via InspectImageCommand) against the registry's current digest for the
// same tag, to answer "is a newer image available".
func handleImageUpdateAvailable(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, detailWaiter *deploy.ImageDetailWaiter) http.HandlerFunc {
	type response struct {
		UpToDate     bool   `json:"upToDate"`
		LocalDigest  string `json:"localDigest,omitempty"`
		RemoteDigest string `json:"remoteDigest,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		serverID := r.URL.Query().Get("serverId")
		imageID := r.URL.Query().Get("imageId")
		ref := r.URL.Query().Get("ref")
		if serverID == "" || imageID == "" || ref == "" {
			http.Error(w, "serverId, imageId, and ref are required", http.StatusBadRequest)
			return
		}

		creds, err := resolveCreds(r, st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		remoteDigest, err := registryclient.ResolveDigest(r.Context(), ref, creds)
		if err != nil {
			log.Warn("failed to resolve remote digest", "ref", ref, "error", err)
			http.Error(w, "failed to resolve remote digest: "+err.Error(), http.StatusBadGateway)
			return
		}

		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}
		ch, cleanup := detailWaiter.Await(requestID)
		defer cleanup()
		cmd := &agentv1.ControlMessage{
			Payload: &agentv1.ControlMessage_InspectImage{
				InspectImage: &agentv1.InspectImageCommand{RequestId: requestID, ServerId: serverID, ImageId: imageID},
			},
		}
		if err := dispatcher.Send(serverID, cmd); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
			return
		}

		var localDigest string
		select {
		case detail := <-ch:
			for _, d := range detail.GetRepoDigests() {
				localDigest = d
				break
			}
		case <-time.After(imageDetailTimeout):
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "timed out waiting for agent response"})
			return
		}

		writeJSON(w, http.StatusOK, response{
			UpToDate:     localDigest != "" && containsDigest(localDigest, remoteDigest),
			LocalDigest:  localDigest,
			RemoteDigest: remoteDigest,
		})
	}
}

// containsDigest reports whether repoDigest (e.g. "nginx@sha256:abc...")
// carries remoteDigest (e.g. "sha256:abc...") as its digest component.
func containsDigest(repoDigest, remoteDigest string) bool {
	return len(repoDigest) >= len(remoteDigest) && repoDigest[len(repoDigest)-len(remoteDigest):] == remoteDigest
}

// resolveCreds loads registry credentials from ?registryId, or returns
// anonymous Credentials if unset.
func resolveCreds(r *http.Request, st *store.Store) (registryclient.Credentials, error) {
	registryID := r.URL.Query().Get("registryId")
	if registryID == "" {
		return registryclient.Credentials{}, nil
	}
	reg, err := st.GetRegistry(r.Context(), registryID)
	if err != nil {
		return registryclient.Credentials{}, errors.New("registry not found")
	}
	return registryclient.Credentials{Username: reg.Username, Password: reg.Password}, nil
}
