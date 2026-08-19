// Command controlplane is the PSpocketEdge control-plane server.
package main

import (
	"fmt"
	"os"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
)

func main() {
	fmt.Fprintf(os.Stdout, "pspocketedge-controlplane %s\n", version.String())
}
