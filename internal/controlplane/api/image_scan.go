package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/scan"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// handleScanImage runs a Trivy scan against imageRef (pulled from the
// registry by Trivy itself, not from any agent — see internal/
// controlplane/scan's doc comment) and stores the summarized result,
// replacing any previous scan of the same ref.
func handleScanImage(log *slog.Logger, st *store.Store) http.HandlerFunc {
	type request struct {
		ImageRef string `json:"imageRef"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ImageRef == "" {
			http.Error(w, "imageRef is required", http.StatusBadRequest)
			return
		}

		result, err := scan.Run(r.Context(), req.ImageRef)
		if errors.Is(err, scan.ErrScannerUnavailable) {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": err.Error()})
			return
		}
		if err != nil {
			log.Warn("vulnerability scan failed", "image_ref", req.ImageRef, "error", err)
			scanErr := store.ImageScan{ImageRef: req.ImageRef, Error: err.Error()}
			if saveErr := st.UpsertImageScan(r.Context(), scanErr); saveErr != nil {
				log.Error("failed to save failed scan result", "error", saveErr)
			}
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}

		raw, err := json.Marshal(result)
		if err != nil {
			log.Error("failed to marshal scan result", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		record := store.ImageScan{
			ImageRef:      req.ImageRef,
			CriticalCount: result.CriticalCount,
			HighCount:     result.HighCount,
			MediumCount:   result.MediumCount,
			LowCount:      result.LowCount,
			UnknownCount:  result.UnknownCount,
			RawResult:     raw,
		}
		if err := st.UpsertImageScan(r.Context(), record); err != nil {
			log.Error("failed to save scan result", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		writeJSON(w, http.StatusOK, result)
	}
}

// handleGetImageScan returns the last stored scan result for ?ref=.
func handleGetImageScan(log *slog.Logger, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref := r.URL.Query().Get("ref")
		if ref == "" {
			http.Error(w, "ref is required", http.StatusBadRequest)
			return
		}

		result, err := st.GetImageScan(r.Context(), ref)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "no scan recorded for this image", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Error("failed to load scan result", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
