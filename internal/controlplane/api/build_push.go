package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

var registryRepositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

// registryPushRef builds a tag inside the configured registry. The caller
// only supplies the repository path; it cannot choose a different host for
// the registry's credentials.
func registryPushRef(registryURL, repository, sourceTag string) (string, error) {
	if !registryRepositoryPattern.MatchString(repository) {
		return "", errors.New("repository must be a lowercase image path")
	}
	_, tag, ok := strings.Cut(sourceTag, ":")
	if !ok || tag == "" {
		return "", errors.New("built image has no immutable tag")
	}
	address := registryURL
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("invalid configured registry URL")
	}
	prefix := strings.Trim(u.Path, "/")
	if prefix != "" && !registryRepositoryPattern.MatchString(prefix) {
		return "", errors.New("invalid registry repository prefix")
	}
	if prefix != "" {
		repository = prefix + "/" + repository
	}
	return fmt.Sprintf("%s/%s:%s", u.Host, repository, tag), nil
}

// handlePushBuild pushes a successful build's exact image to a configured
// registry. The deployment keeps using its local image; the published tag
// can later be used by other servers or Kubernetes.
func handlePushBuild(log *slog.Logger, st *store.Store, dispatcher *deploy.Dispatcher, waiter *deploy.ImageOpWaiter) http.HandlerFunc {
	type request struct {
		RegistryID string `json:"registryId"`
		Repository string `json:"repository"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RegistryID == "" || req.Repository == "" {
			http.Error(w, "registryId and repository are required", http.StatusBadRequest)
			return
		}
		b, err := st.GetImageBuild(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "build not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		if b.Status != store.BuildSucceeded {
			http.Error(w, "only successful builds can be pushed", http.StatusConflict)
			return
		}
		reg, err := st.GetRegistry(r.Context(), req.RegistryID)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "registry not found", http.StatusNotFound)
			return
		}
		if err != nil {
			writeActionError(w, log, err)
			return
		}
		ref, err := registryPushRef(reg.URL, req.Repository, b.ImageTag)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requestID, ok := newRequestID(w, log)
		if !ok {
			return
		}
		cmd := &agentv1.ControlMessage{Payload: &agentv1.ControlMessage_PushImage{PushImage: &agentv1.PushImageCommand{
			RequestId: requestID, ServerId: b.ServerID, SourceTag: b.ImageTag, TargetRef: ref,
			Auth: &agentv1.RegistryAuth{Username: reg.Username, Password: reg.Password},
		}}}
		result, err := sendAndAwaitImageOp(dispatcher, waiter, b.ServerID, requestID, cmd, 30*time.Minute)
		if err != nil {
			writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": err.Error()})
			return
		}
		if !result.GetSuccess() {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": result.GetErrorMessage()})
			return
		}
		recordAudit(r, log, st, "build.push", "deployment", b.DeploymentID, "pushed built image "+ref, map[string]any{"buildId": b.ID, "registryId": req.RegistryID})
		writeJSON(w, http.StatusOK, map[string]string{"imageRef": ref})
	}
}
