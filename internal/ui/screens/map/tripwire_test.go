package mapscreen

// Independent-destructive posture (GR#23), FEAT-031 dispatch: this
// package's own doc.go (Overlay cycle section) and overlay_data.go's
// overlayBlockedReason label three of AC-3's ten overlays
// (traffic, per-service coverage, parking occupancy) "BLOCKED" because
// their candidate engine module (internal/engine/traffic,
// internal/engine/services, internal/engine/parking respectively) exists
// and is even tick-wired in compose.Wire, but ui.screen.map has no
// registered outbound edge to it in code.json (GR#25) and — investigated
// directly at this item's dispatch — no Subscribe/Delta path carries any
// of their output to any protocol view today regardless. Mirroring
// ui.screen.services' SVC-6 tripwire (internal/ui/screens/services/
// tripwire_test.go), this file makes the code.json half of that
// blocker mechanical for all three: each test parses code.json directly
// and fails loudly the moment its named engine module appears as a
// registered ui.screen.map outbound edge, forcing a human back to this
// package's doc.go / overlay_data.go to build the real overlay instead
// of it silently staying BLOCKED forever once the edge is registered.
//
// The other seven overlays (ownership, land value, zoning, utilities,
// pollution, decay, vitality) get NO tripwire here, deliberately, for
// the exact reason ui.screen.services' SVC-3 note gives for the same
// gap: no engine module or code.json node exists for any of them today,
// so there is no stable, already-agreed detection point to probe. A
// tripwire against a guessed future module/edge name would be exactly
// the fabricated-tripwire trap GR#23's independent round exists to
// catch — see overlay_data.go's overlayBlockedReason for each one's
// honest "no stable detection point" documentation instead.
//
// Path arithmetic: this package lives at internal/ui/screens/map/, so
// four levels up ("../../../../code.json") is the repository root's
// code.json — the same arithmetic services/tripwire_test.go uses from
// its sibling directory internal/ui/screens/services/.

import (
	"encoding/json"
	"os"
	"testing"
)

// codeJSONModule mirrors services/tripwire_test.go's minimal shape:
// deliberately not the full code.json schema, just what this test needs.
type codeJSONModule struct {
	Key      string `json:"key"`
	Outbound struct {
		Calls []struct {
			Key string `json:"key"`
		} `json:"calls"`
	} `json:"outbound"`
}

type codeJSONDoc struct {
	Modules []codeJSONModule `json:"modules"`
}

// findMapModule reads and parses code.json, returning the ui.screen.map
// module entry. Fails the test (not silently) if code.json is missing,
// unparsable, or no longer carries a ui.screen.map entry — those are all
// "the tripwire's own foundation moved" conditions that need a human,
// exactly services/tripwire_test.go's posture.
func findMapModule(t *testing.T) *codeJSONModule {
	t.Helper()
	const codeJSONPath = "../../../../code.json"

	raw, err := os.ReadFile(codeJSONPath)
	if err != nil {
		t.Fatalf("could not read %s (path arithmetic wrong, or code.json moved -- fix this test's path, do not delete it): %v", codeJSONPath, err)
	}

	var doc codeJSONDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("could not parse %s as JSON: %v", codeJSONPath, err)
	}

	for i := range doc.Modules {
		if doc.Modules[i].Key == "ui.screen.map" {
			return &doc.Modules[i]
		}
	}
	t.Fatalf("code.json has no module keyed \"ui.screen.map\" -- the registry entry this tripwire depends on is gone; revisit this test alongside whatever renamed/removed it")
	return nil
}

// tripwireBlockedEdge fails loudly if wantEdge appears in mod's
// registered outbound calls, naming which overlay is no longer correctly
// BLOCKED and what to revisit.
func tripwireBlockedEdge(t *testing.T, mod *codeJSONModule, wantEdge, overlayName string) {
	t.Helper()
	for _, call := range mod.Outbound.Calls {
		if call.Key == wantEdge {
			t.Fatalf(
				"TRIPWIRE FIRED: code.json's ui.screen.map module now lists "+
					"%s as a registered outbound edge -- the %q overlay "+
					"(AC-3, docs/planning/acceptance/ui.screen.map.md) is no "+
					"longer correctly BLOCKED. overlay_data.go's "+
					"overlayLiveValue/overlayBlockedReason and this package's "+
					"doc.go must be revisited to wire the real per-cell "+
					"metric via a real Subscribe/Delta path (confirm one now "+
					"exists -- at FEAT-031 dispatch it did not, even with the "+
					"engine module tick-wired) instead of continuing to "+
					"report have=false for every cell.",
				wantEdge, overlayName,
			)
		}
	}
	// Expected today: wantEdge is not a registered outbound edge for
	// ui.screen.map (confirmed at dispatch by reading code.json directly).
	// The named overlay remains correctly BLOCKED.
}

func TestTripwire_EngineTrafficNotYetAnOutboundEdge(t *testing.T) {
	tripwireBlockedEdge(t, findMapModule(t), "engine.traffic", OverlayTraffic.String())
}

func TestTripwire_EngineServicesNotYetAnOutboundEdge(t *testing.T) {
	tripwireBlockedEdge(t, findMapModule(t), "engine.services", OverlayServiceCoverage.String())
}

func TestTripwire_EngineParkingNotYetAnOutboundEdge(t *testing.T) {
	tripwireBlockedEdge(t, findMapModule(t), "engine.parking", OverlayParkingOccupancy.String())
}
