package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/power"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// FEAT-1972079851 (pylon catalogue + map power layer, trio slice 1 of 3):
// the composition-root wiring for engine.power's placement store —
// mirroring traffic_wire.go's per-integration bridge-file pattern. The
// store is constructed from data/pylons.json (catalogue) and bounded to
// the start tile's local cell domain (the same coordinate space
// buildViewportPatch and the gameplay command seam already use), then
// published through "f1.viewport"'s powerLines field (omitempty).
//
// Deliberate seam: nothing places pylons yet (no Build command variant,
// no network solving) — this slice proves catalogue → placement →
// publish end to end; placement callers arrive with later trio slices.

// loadDefaultPower constructs a *power.PowerAPI over data/pylons.json
// (GR#15), bounded to the start tile's cell domain. This is
// Deps.LoadPower's default — Wire calls it unless a caller injects an
// override.
func loadDefaultPower(correlationID string) (*power.PowerAPI, error) {
	cat, err := power.LoadDefault(correlationID)
	if err != nil {
		// Already a registry-sourced *errs.E (MET-G5200); Wire's
		// ErrModuleFailed wrap adds the module attribution.
		return nil, err
	}
	return power.New(cat, power.Bounds{Width: world.TileSizeCells, Height: world.TileSizeCells}), nil
}
