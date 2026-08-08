// Command metctl is the operator/CLI counterpart to metropolis: headless
// control, fixture/save export-import, and (later) H-HEADLESS scenario
// scripting (M0-ENG §2 harness strategy). Its export/import surface is the
// CLI consumer named in int.serializer ("metctl export").
//
// This is the M0 skeleton only: it prints the build identity and exits.
//
// Module key: foundation.repo (see code.json)
// Spec ref:   M0-ENG §5; A8; int.serializer
package main

import (
	"fmt"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
)

func main() {
	fmt.Println("metctl", buildinfo.String())
	os.Exit(0)
}
