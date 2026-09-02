package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
)

// ImageSummary is a snapshot of one locally-present image, from the same
// ImageList call as `docker images` — cheap enough for the on-demand list,
// unlike ImageDetail's per-image ImageInspect below.
type ImageSummary struct {
	ID              string
	RepoTags        []string
	RepoDigests     []string
	SizeBytes       int64
	CreatedUnix     int64
	Dangling        bool
	ContainersCount int64
}

// ImageLayer is one layer of an image's root filesystem.
type ImageLayer struct {
	Digest    string
	SizeBytes int64
}

// ImageDetail is the expensive, on-demand detail for one image (layers,
// env, labels, architecture) — fetched only when a user opens an image's
// detail view, same split as ContainerDetail/InspectContainer.
type ImageDetail struct {
	Found        bool
	ErrorMessage string
	ID           string
	RepoTags     []string
	RepoDigests  []string
	SizeBytes    int64
	CreatedUnix  int64
	Architecture string
	OS           string
	Layers       []ImageLayer
	Env          []string
	Labels       map[string]string
}

// RegistryAuth carries credentials for a registry pull; a zero value means
// an anonymous/public pull.
type RegistryAuth struct {
	Username string
	Password string
}

// ListImages returns every image present on the host, for the on-demand
// Images tab (deliberately not part of the heartbeat — see
// ListImagesCommand's doc comment in agent.proto).
func ListImages(ctx context.Context, cli *client.Client) ([]ImageSummary, error) {
	images, err := cli.ImageList(ctx, image.ListOptions{All: false})
	if err != nil {
		return nil, err
	}

	summaries := make([]ImageSummary, 0, len(images))
	for _, img := range images {
		summaries = append(summaries, ImageSummary{
			ID:              img.ID,
			RepoTags:        cleanRepoList(img.RepoTags),
			RepoDigests:     cleanRepoList(img.RepoDigests),
			SizeBytes:       img.Size,
			CreatedUnix:     img.Created,
			Dangling:        isDangling(img.RepoTags),
			ContainersCount: img.Containers,
		})
	}
	return summaries, nil
}

// cleanRepoList drops Docker's "<none>:<none>" / "<none>@<none>"
// placeholders, which ImageList reports for a dangling image instead of an
// empty slice.
func cleanRepoList(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if strings.Contains(t, "<none>") {
			continue
		}
		out = append(out, t)
	}
	return out
}

func isDangling(repoTags []string) bool {
	return len(cleanRepoList(repoTags)) == 0
}

// InspectImage runs a live ImageInspect for imageID and returns its full
// detail. A not-found or otherwise failed inspect is reported via
// Found/ErrorMessage rather than a returned error, matching
// InspectContainer's convention.
func InspectImage(ctx context.Context, cli *client.Client, imageID string) ImageDetail {
	inspect, err := cli.ImageInspect(ctx, imageID)
	if err != nil {
		return ImageDetail{Found: false, ErrorMessage: err.Error()}
	}

	layers := make([]ImageLayer, 0, len(inspect.RootFS.Layers))
	for _, digest := range inspect.RootFS.Layers {
		layers = append(layers, ImageLayer{Digest: digest})
	}

	var env []string
	var labels map[string]string
	if inspect.Config != nil {
		env = inspect.Config.Env
		labels = inspect.Config.Labels
	}

	return ImageDetail{
		Found:        true,
		ID:           inspect.ID,
		RepoTags:     cleanRepoList(inspect.RepoTags),
		RepoDigests:  cleanRepoList(inspect.RepoDigests),
		SizeBytes:    inspect.Size,
		Architecture: inspect.Architecture,
		OS:           inspect.Os,
		Layers:       layers,
		Env:          env,
		Labels:       labels,
	}
}

// PullImage pulls ref from its registry, authenticating with auth if
// non-nil. Reuses the same error-draining approach as deploy.go's
// pullImageOnce — Docker reports registry-side pull failures (auth,
// manifest not found, rate limit) inside the streamed JSON messages, not as
// a request-level error, so a bare io.Copy would silently swallow them.
func PullImage(ctx context.Context, cli *client.Client, ref string, auth *RegistryAuth) error {
	opts := image.PullOptions{}
	if auth != nil && (auth.Username != "" || auth.Password != "") {
		encoded, err := encodeRegistryAuth(*auth)
		if err != nil {
			return err
		}
		opts.RegistryAuth = encoded
	}

	reader, err := cli.ImagePull(ctx, ref, opts)
	if err != nil {
		return err
	}
	defer reader.Close()

	decoder := json.NewDecoder(reader)
	for {
		var msg jsonmessage.JSONMessage
		if err := decoder.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if msg.Error != nil {
			return msg.Error
		}
	}

	if _, err := cli.ImageInspect(ctx, ref); err != nil {
		return errors.New("pull reported success but image is not present locally: " + err.Error())
	}
	return nil
}

func encodeRegistryAuth(auth RegistryAuth) (string, error) {
	authConfig := struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{Username: auth.Username, Password: auth.Password}
	encoded, err := json.Marshal(authConfig)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(encoded), nil
}

// RemoveImage removes idOrRef, force-removing (deleting even if referenced
// by a stopped container / used by multiple tags) when force is set.
func RemoveImage(ctx context.Context, cli *client.Client, idOrRef string, force bool) error {
	_, err := cli.ImageRemove(ctx, idOrRef, image.RemoveOptions{Force: force})
	if err != nil && errdefs.IsNotFound(err) {
		return errors.New("no such image: " + idOrRef)
	}
	return err
}

// PruneImages removes dangling images (all=false) or every image not
// referenced by any container (all=true, matching `docker image prune -a`),
// returning the total bytes reclaimed.
func PruneImages(ctx context.Context, cli *client.Client, all bool) (int64, error) {
	args := filters.NewArgs()
	if !all {
		args.Add("dangling", "true")
	}
	report, err := cli.ImagesPrune(ctx, args)
	if err != nil {
		return 0, err
	}
	return int64(report.SpaceReclaimed), nil
}
