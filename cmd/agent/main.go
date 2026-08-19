// Command agent is the PSpocketEdge per-node agent.
package main

import (
	"fmt"
	"os"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
)

func main() {
	fmt.Fprintf(os.Stdout, "pspocketedge-agent %s\n", version.String())
}
