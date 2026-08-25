package debug

import (
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// monthlyPhaseOrder is the six monthly phase names in pipeline order
// (AC-8), derived from internal/protocol — the shared Engine<->UI name
// vocabulary (BUG-382). It is NOT a literal local copy any more: GR#20
// forbids internal/ui from importing internal/engine in non-test code,
// which is why the names live in the neutral protocol package both
// domains already depend on. A phase rename or reorder in protocol now
// breaks THIS package's build at compile time instead of surfacing as a
// red CI drift test. determinism_test.go still imports engine.core (the
// sanctioned test-only exemption) to assert the snapshot wiring stays
// aligned with the engine's real MonthlyPhaseOrder().
var monthlyPhaseOrder = protocol.MonthlyPhaseOrderNames()
