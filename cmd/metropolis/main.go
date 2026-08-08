// Command metropolis is the game's entrypoint: the TUI process that hosts
// the UI process-domain and (in-process, v1) the engine domain (M0-ENG
// §1.1 process & thread topology).
//
// This is the M0 skeleton only: it prints the build identity and exits.
// The real UI/engine boot sequence lands with harness.stub, ui.core and
// engine.core (BOW).
//
// Module key: foundation.repo (see code.json)
// Spec ref:   M0-ENG §5; A8; M0-ENG §3 (build info)
package main

import (
	"fmt"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
)

func main() {
	fmt.Println(buildinfo.String())
	os.Exit(0)
}
