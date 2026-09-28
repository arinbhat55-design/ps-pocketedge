package deploy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
)

// maxCommandOutput caps how much of a command's output RunCommand keeps.
const maxCommandOutput = 64 << 10

// CommandResult is a finished one-shot command.
type CommandResult struct {
	// Output is stdout and stderr combined (the agent runs exec sessions
	// on a pty), truncated to maxCommandOutput.
	Output    string
	ExitCode  int64
	Truncated bool
}

// RunCommand runs argv to completion inside containerID (a container ID
// or name) on serverID, over the same exec relay interactive terminals
// use, and returns its output and exit code. A non-zero exit is not an
// error — callers decide what it means — but failing to start, the agent
// being unreachable, and timing out are.
func RunCommand(ctx context.Context, dispatcher *Dispatcher, relay *ExecStreamRelay, serverID, containerID string, argv []string, timeout time.Duration) (*CommandResult, error) {
	return RunCommandWithLimit(ctx, dispatcher, relay, serverID, containerID, argv, timeout, maxCommandOutput)
}

// RunCommandWithLimit permits a larger bounded result for query downloads.
// The caller must reject Truncated if it needs a complete result.
func RunCommandWithLimit(ctx context.Context, dispatcher *Dispatcher, relay *ExecStreamRelay, serverID, containerID string, argv []string, timeout time.Duration, outputLimit int) (*CommandResult, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	if outputLimit <= 0 || outputLimit > 8<<20 {
		return nil, errors.New("output limit must be between 1 byte and 8 MB")
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	requestID := hex.EncodeToString(idBytes)

	// Subscribe before dispatching so no early output is missed.
	ch, unsubscribe := relay.Subscribe(requestID)
	defer unsubscribe()

	err := dispatcher.Send(serverID, &agentv1.ControlMessage{
		Payload: &agentv1.ControlMessage_ExecStart{
			ExecStart: &agentv1.ExecStartCommand{
				RequestId:   requestID,
				ServerId:    serverID,
				ContainerId: containerID,
				Cmd:         argv,
				// Wide enough that tools which wrap to the terminal
				// width don't mangle their output.
				Cols: 250,
				Rows: 50,
			},
		},
	})
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	truncated := false
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case chunk := <-ch:
			data := chunk.GetData()
			remaining := outputLimit - out.Len()
			if len(data) > remaining {
				truncated = true
				data = data[:remaining]
			}
			out.Write(data)
			if chunk.GetDone() {
				if msg := chunk.GetErrorMessage(); msg != "" && chunk.GetExitCode() == 0 {
					return nil, fmt.Errorf("command failed to run: %s", msg)
				}
				text := out.String()
				return &CommandResult{Output: text, ExitCode: chunk.GetExitCode(), Truncated: truncated}, nil
			}
		case <-deadline.C:
			// Ask the agent to close the session so the process doesn't
			// linger.
			_ = dispatcher.Send(serverID, &agentv1.ControlMessage{
				Payload: &agentv1.ControlMessage_StopStream{StopStream: &agentv1.StopStreamCommand{RequestId: requestID}},
			})
			return nil, fmt.Errorf("command timed out after %s", timeout)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
