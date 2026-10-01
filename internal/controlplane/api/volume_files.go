package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

const volumeFileMaxBytes = 1 << 20

func volumeFileCommand(w http.ResponseWriter, r *http.Request, log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.VolumeFileWaiter, operation, target string, content []byte) *agentv1.VolumeFileResult {
	requestID, ok := newRequestID(w, log)
	if !ok {
		return nil
	}
	serverID := r.PathValue("id")
	cmd := &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_VolumeFile{VolumeFile: &agentv1.VolumeFileCommand{
		RequestId: requestID, ServerId: serverID, VolumeName: r.PathValue("name"), Operation: operation,
		Path: r.URL.Query().Get("path"), Content: content, TargetVolume: target,
	}}}
	ch, cleanup := waiter.Await(requestID)
	defer cleanup()
	if err := dispatcher.Send(serverID, cmd); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "server not connected"})
		return nil
	}
	timeout := 2 * time.Minute
	if operation == "clone" {
		timeout = 15 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-ch:
		if !result.GetSuccess() {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": result.GetErrorMessage()})
			return nil
		}
		return result
	case <-timer.C:
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "volume operation timed out"})
	case <-r.Context().Done():
	}
	return nil
}

func handleListVolumeFiles(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.VolumeFileWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := volumeFileCommand(w, r, log, dispatcher, waiter, "list", "", nil)
		if result == nil {
			return
		}
		type entry struct {
			Name        string `json:"name"`
			IsDirectory bool   `json:"isDirectory"`
			IsSymlink   bool   `json:"isSymlink"`
			SizeBytes   int64  `json:"sizeBytes"`
		}
		entries := make([]entry, 0, len(result.GetEntries()))
		for _, e := range result.GetEntries() {
			entries = append(entries, entry{Name: e.GetName(), IsDirectory: e.GetIsDirectory(), IsSymlink: e.GetIsSymlink(), SizeBytes: e.GetSizeBytes()})
		}
		writeJSON(w, http.StatusOK, entries)
	}
}

func handleReadVolumeFile(log *slog.Logger, dispatcher *deploy.Dispatcher, waiter *deploy.VolumeFileWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := volumeFileCommand(w, r, log, dispatcher, waiter, "read", "", nil)
		if result == nil {
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(result.GetContent())
	}
}

func handleWriteVolumeFile(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, waiter *deploy.VolumeFileWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		content, err := io.ReadAll(io.LimitReader(r.Body, volumeFileMaxBytes+1))
		if err != nil || len(content) > volumeFileMaxBytes {
			http.Error(w, "file must be at most 1 MiB", http.StatusRequestEntityTooLarge)
			return
		}
		result := volumeFileCommand(w, r, log, dispatcher, waiter, "write", "", content)
		if result == nil {
			return
		}
		recordAudit(r, log, st, "volume.file.write", "volume", r.PathValue("name"), "wrote volume file", map[string]any{"serverId": r.PathValue("id"), "path": r.URL.Query().Get("path"), "bytes": len(content)})
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleCloneVolume(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, waiter *deploy.VolumeFileWaiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Target string `json:"target"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.Target == "" {
			http.Error(w, "target volume name is required", http.StatusBadRequest)
			return
		}
		result := volumeFileCommand(w, r, log, dispatcher, waiter, "clone", req.Target, nil)
		if result == nil {
			return
		}
		recordAudit(r, log, st, "volume.clone", "volume", req.Target, "cloned volume", map[string]any{"serverId": r.PathValue("id"), "source": r.PathValue("name")})
		writeJSON(w, http.StatusCreated, map[string]string{"name": req.Target})
	}
}
