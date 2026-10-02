// The setup helper's stdout is NDJSON, consumed by the native macOS wizard.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/setup"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) == 2 && os.Args[1] == "defaults" {
		json.NewEncoder(os.Stdout).Encode(setup.SuggestedPlan(context.Background(), home))
		return
	}
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: pe-setup-helper defaults | preflight/install/open PAYLOAD (JSON plan on stdin)")
		os.Exit(2)
	}
	var p setup.Plan
	if err = json.NewDecoder(os.Stdin).Decode(&p); err != nil {
		fmt.Fprintln(os.Stderr, "invalid setup plan")
		os.Exit(2)
	}
	payload, err := filepath.Abs(os.Args[2])
	if err != nil {
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	encoder := json.NewEncoder(os.Stdout)
	engine := setup.Engine{Payload: payload, Emit: func(e setup.Event) { encoder.Encode(e) }}
	switch os.Args[1] {
	case "preflight":
		err = engine.Preflight(ctx, p)
		if err != nil {
			encoder.Encode(setup.Event{Component: "Preflight", Status: "Failed", Message: err.Error(), Done: true})
		} else {
			encoder.Encode(setup.Event{Component: "Preflight", Status: "Passed", Done: true, OK: true})
		}
	case "install":
		err = engine.Install(ctx, p)
	case "open":
		err = engine.Open(ctx, p)
	default:
		fmt.Fprintln(os.Stderr, "unknown operation")
		os.Exit(2)
	}
	if err != nil {
		if os.Args[1] != "preflight" {
			encoder.Encode(setup.Event{Component: "Setup", Status: "Failed", Message: setup.ErrorMessage(err, p), Done: true})
		}
		os.Exit(1)
	}
}
