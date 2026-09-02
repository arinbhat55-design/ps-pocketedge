package docker

import (
	"bufio"
	"context"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// defaultLogTailLines bounds an unbounded ("tailLines == 0") request —
// without it, a StreamLogsCommand for a long-running container with
// follow=false would dump its entire history.
const defaultLogTailLines = 200

// LogLine is one demultiplexed line from a container's combined
// stdout/stderr, with the timestamp Docker prefixed it with (best-effort:
// Time is zero if a line couldn't be parsed).
type LogLine struct {
	Time    time.Time
	Stream  string // "stdout" or "stderr"
	Message string
}

// StreamLogs tails containerID's logs, invoking onLine for each line as it
// arrives. With follow=true it blocks until ctx is cancelled (the caller
// owns cancellation, e.g. on a StopStreamCommand); with follow=false it
// returns once the bounded dump is exhausted — the same call serves both
// the live tail view and the download/AI-analysis paths.
func StreamLogs(ctx context.Context, cli *client.Client, containerID string, follow bool, tailLines int, since, until time.Time, onLine func(LogLine)) error {
	tail := strconv.Itoa(defaultLogTailLines)
	switch {
	case tailLines > 0:
		tail = strconv.Itoa(tailLines)
	case tailLines < 0:
		tail = "all"
	}

	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tail,
		Timestamps: true,
	}
	if !since.IsZero() {
		opts.Since = since.Format(time.RFC3339Nano)
	}
	if !until.IsZero() {
		opts.Until = until.Format(time.RFC3339Nano)
	}

	reader, err := cli.ContainerLogs(ctx, containerID, opts)
	if err != nil {
		return err
	}
	defer reader.Close()

	// ContainerLogs multiplexes stdout/stderr into one stream (see its doc
	// comment) unless the container runs with a tty — demux via stdcopy
	// into two pipes, each scanned independently so a line on either
	// stream reaches onLine as soon as it arrives rather than waiting for
	// the other stream to also produce a line.
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()

	go func() {
		_, copyErr := stdcopy.StdCopy(outW, errW, reader)
		outW.CloseWithError(copyErr)
		errW.CloseWithError(copyErr)
	}()

	done := make(chan struct{}, 2)
	scan := func(r io.Reader, streamName string) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			onLine(parseLogLine(scanner.Text(), streamName))
		}
		done <- struct{}{}
	}
	go scan(outR, "stdout")
	go scan(errR, "stderr")

	<-done
	<-done
	return nil
}

// parseLogLine splits Docker's "<RFC3339Nano timestamp> <message>" format
// (requested via LogsOptions.Timestamps above) into its two parts. A line
// with no parseable timestamp (shouldn't happen given Timestamps:true, but
// defensive) is returned whole with a zero Time.
func parseLogLine(raw, stream string) LogLine {
	if idx := strings.IndexByte(raw, ' '); idx > 0 {
		if parsed, err := time.Parse(time.RFC3339Nano, raw[:idx]); err == nil {
			return LogLine{Time: parsed, Stream: stream, Message: raw[idx+1:]}
		}
	}
	return LogLine{Stream: stream, Message: raw}
}
