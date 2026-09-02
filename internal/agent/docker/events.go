package docker

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// ContainerEvent is one Docker daemon event (container.Type events only —
// start/stop/die/health_status/oom, etc.), scoped for the troubleshooting
// "what happened here" view.
type ContainerEvent struct {
	Time          time.Time
	Type          string
	Action        string
	ContainerID   string
	ContainerName string
}

// ListEvents returns Docker events between since/until (until defaults to
// now, since defaults to unbounded), optionally scoped to one container —
// containerID == "" lists every container's events on this host. This is
// a bounded historical fetch, not a live subscription: cli.Events streams
// until ctx is cancelled or Until is reached, and a concrete Until here
// (rather than the zero value, which streams forever) is what makes the
// call return once caught up.
func ListEvents(ctx context.Context, cli *client.Client, containerID string, since, until time.Time) ([]ContainerEvent, error) {
	if until.IsZero() {
		until = time.Now()
	}

	f := filters.NewArgs(filters.Arg("type", "container"))
	if containerID != "" {
		f.Add("container", containerID)
	}

	opts := events.ListOptions{Filters: f, Until: until.Format(time.RFC3339Nano)}
	if !since.IsZero() {
		opts.Since = since.Format(time.RFC3339Nano)
	}

	msgCh, errCh := cli.Events(ctx, opts)
	var out []ContainerEvent
	for {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				return out, nil
			}
			out = append(out, ContainerEvent{
				Time:          time.Unix(0, msg.TimeNano),
				Type:          string(msg.Type),
				Action:        string(msg.Action),
				ContainerID:   msg.Actor.ID,
				ContainerName: msg.Actor.Attributes["name"],
			})
		case err, ok := <-errCh:
			// io.EOF signals "caught up to Until", the normal completion
			// path for a bounded fetch — not an actual failure.
			if !ok || err == nil || errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
}
