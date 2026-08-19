package services

// Independent-destructive addition (GR#23): doc.go labels SVC-3 (coverage-
// map jump) and SVC-6 (Public Service Pie) "BLOCKED (tripwire)", mirroring
// the same prose-only-tripwire gap the F8 districts round found and fixed
// (internal/ui/screens/districts/tripwire_test.go, PR #46). This file
// makes SVC-6's block mechanical. SVC-3 is deliberately NOT given a
// tripwire here -- see the comment on TestTripwire_EngineFiscalNotYetAnOutboundEdge
// below and this package's own drill_map.go/drill_map_test.go for why, and
// the PR description for the full reasoning.
//
// # SVC-6: engine.fiscal registered outbound edge (mechanical, this file)
//
// doc.go's SVC-6 note says the Public Service Pie cannot be built because
// code.json's ui.screen.services module registers only engine.services and
// engine.dispatch as outbound calls -- engine.fiscal (§54's likely source
// for the Pie's per-1k-population benchmark ratios) is NOT a registered
// edge, and GR#25 forbids building against an unregistered dependency.
// That is a fact about code.json's graph, not about a Go package's
// presence/absence on disk, so this tripwire parses code.json directly
// (mirroring the F8 tripwire's os.Stat-on-the-blocker-path idea, adapted
// to a graph-edge blocker instead of a directory blocker) and fails loudly
// the moment engine.fiscal appears in ui.screen.services' outbound.calls --
// forcing a human back to doc.go's SVC-6 section and Screen.PublicServicePie
// instead of the wire-only stub silently staying unbuilt forever once the
// edge is registered.
//
// # SVC-3: NOT given a tripwire here (documented, not fabricated)
//
// SVC-3's blocker is ui.screen.map's own AC-3 (the per-service coverage
// overlay), deferred at FEAT-005's Sprint 1 dispatch (map/doc.go's Scope
// section) and still unbuilt. Unlike engine.policies (F8's blocker,
// entirely absent as a package) or engine.fiscal (SVC-6's blocker, a
// missing code.json edge against an EXISTING module), AC-3's landing has
// no stable, already-agreed detection point to probe:
//   - internal/ui/screens/map already exists as a package (a package-
//     presence check like F8's would misfire immediately -- it is already
//     there).
//   - No overlay/coverage-marking symbol exists anywhere in that package
//     today (grep for "Overlay"/"Mark" across internal/ui/screens/map/*.go
//     turns up nothing but a render.go glyph *comment* -- no exported
//     type, constant, or function whose future existence AC-3's landing
//     is guaranteed to introduce under a name this test could reliably
//     name in advance).
//   - code.json's ui.screen.map module has no dedicated AC-3 sub-edge to
//     probe either -- AC-3 is acceptance-criteria prose inside
//     ui.screen.map.md, not a graph node.
//
// Writing a tripwire against a guessed future symbol name (e.g. an
// invented "OverlayCoverage" type) would be exactly the fabricated-
// tripwire trap this round exists to avoid: a check that either never
// fires (wrong guess) or fires on an unrelated rename, training humans to
// ignore it. This package's existing drill_map_test.go
// (TestCoverageJumpTarget_DoesNotYetResolve) already documents SVC-3's
// current BLOCKED behaviour against a fresh dash.MapResolver and asks a
// human to revisit it "if this test ever starts failing" -- that is the
// honest mechanical ceiling available for SVC-3 today, and this file adds
// no further, less-reliable probe on top of it.
//
// # ApplyResult / ASM-1482 gating note: not a "BLOCKED (tripwire)" AC
//
// doc.go's "Gating Notes & Architecture Seams" section (SVC-8/ApplyResult)
// is NOT one of the two ACs doc.go marks "BLOCKED (tripwire)" -- SVC-8
// itself is built and tested; only the wider ui-side wiring that would
// make ApplyResult *reachable* at runtime is pending (ASM-1482,
// docs/planning/icd/ui.result-routing.md). internal/ui/router (BOW
// MOD-115) now exists on this tree and closes the transport-owning-seam
// half of ASM-1482, but by its own doc.go it "never imports any concrete
// screen package" and nothing anywhere in the repo yet calls
// RegisterResultHandler/BindSubscription for this screen (verified: no
// hit for either symbol outside internal/ui/router itself) -- the missing
// piece is a not-yet-named ui-side composition-root package (ICD Open
// Decision 1), so there is no stable path or symbol this test could probe
// for either, for the same reason SVC-3 gets none. This mirrors F8: its
// identical AC-9/ASM-1482 gating note (districts/screen.go) also has no
// tripwire in tripwire_test.go -- only the two explicitly BLOCKED ACs
// (there: AC-2/3/4/5/8 via engine.policies) got one.

import (
	"encoding/json"
	"os"
	"testing"
)

// codeJSONModule is the minimal shape this test needs from a single
// code.json module entry -- deliberately not the full schema, so this
// test does not need to track every unrelated field code.json carries.
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

// TestTripwire_EngineFiscalNotYetAnOutboundEdge is SVC-6's mechanical
// tripwire (see file header). Path arithmetic: this package lives at
// internal/ui/screens/services/, so four levels up
// ("../../../../code.json") is the repository root's code.json --
// verified the same way F8's tripwire verified its own path arithmetic,
// against this package's own known location.
func TestTripwire_EngineFiscalNotYetAnOutboundEdge(t *testing.T) {
	const codeJSONPath = "../../../../code.json"

	raw, err := os.ReadFile(codeJSONPath)
	if err != nil {
		t.Fatalf("could not read %s (path arithmetic wrong, or code.json moved -- fix this test's path, do not delete it): %v", codeJSONPath, err)
	}

	var doc codeJSONDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("could not parse %s as JSON: %v", codeJSONPath, err)
	}

	var svc *codeJSONModule
	for i := range doc.Modules {
		if doc.Modules[i].Key == "ui.screen.services" {
			svc = &doc.Modules[i]
			break
		}
	}
	if svc == nil {
		t.Fatalf("code.json has no module keyed \"ui.screen.services\" -- the registry entry this tripwire depends on is gone; revisit this test alongside whatever renamed/removed it")
	}

	for _, call := range svc.Outbound.Calls {
		if call.Key == "engine.fiscal" {
			t.Fatalf(
				"TRIPWIRE FIRED: code.json's ui.screen.services module now " +
					"lists engine.fiscal as a registered outbound edge -- SVC-6 " +
					"(Public Service Pie) is no longer correctly BLOCKED. " +
					"docs/planning/acceptance/ui.screen.services.md's SVC-6 and " +
					"this package's doc.go (\"# SVC-6: Public Service Pie\" " +
					"section), Screen.PublicServicePie() and " +
					"RenderPublicServicePie must be revisited to build against " +
					"the real, now-registered field instead of always reporting " +
					"have=false.",
			)
		}
	}
	// Expected today: engine.fiscal is not a registered outbound edge for
	// ui.screen.services (confirmed at dispatch by reading code.json
	// directly). SVC-6 remains correctly BLOCKED.
}
