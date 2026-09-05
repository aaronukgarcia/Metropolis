package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ---------------------------------------------------------------------------
// BUG-734 — registerCompletedServiceBuildings: the compose-side registration
// helper this lane adds WITHOUT touching compose.go (BUG-720's lane owns
// that file). Driven directly against []build.BuildOrder literals — no
// composition/world/season/logistics fixture is needed since this helper
// only reads BuildOrder.ID/BuildingID and calls the deathservices API.
// ---------------------------------------------------------------------------

func newDeathServicesForRegistryTest(t *testing.T) *deathservices.DeathServicesAPI {
	t.Helper()
	d, err := deathservices.LoadDefault("bug734-test")
	if err != nil {
		t.Fatalf("deathservices.LoadDefault: %v", err)
	}
	return d
}

func TestRegisterCompletedServiceBuildings_RegistersCemeteryAndCrematorium(t *testing.T) {
	ds := newDeathServicesForRegistryTest(t)
	completions := []build.BuildOrder{
		{ID: 1, BuildingID: "cemetery", Status: build.OrderComplete},
		{ID: 2, BuildingID: "crematorium", Status: build.OrderComplete},
		{ID: 3, BuildingID: "corner_shop", Status: build.OrderComplete}, // non-deathservices building — skipped
	}
	if err := registerCompletedServiceBuildings(completions, ds, "corr"); err != nil {
		t.Fatalf("registerCompletedServiceBuildings: %v", err)
	}

	occ, capVal, err := ds.CemeteryOccupancy(cemeteryInstanceID(1), "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy(%s): %v", cemeteryInstanceID(1), err)
	}
	if occ != 0 || capVal <= 0 {
		t.Fatalf("CemeteryOccupancy(%s) = (%d, %d), want (0, >0)", cemeteryInstanceID(1), occ, capVal)
	}

	throughput, err := ds.DailyThroughput("corr")
	if err != nil {
		t.Fatalf("DailyThroughput: %v", err)
	}
	if throughput <= 0 {
		t.Fatalf("DailyThroughput = %d, want > 0 (crematorium registration should not change the data-sourced figure, just prove the id is live)", throughput)
	}
	// Prove the crematorium id itself is live: Cremate against an unknown id
	// is rejected, but this one was just registered, so a Cremate call must
	// not fail with ErrUnknownCrematorium (any other outcome — e.g. no
	// bodies to cremate — is fine and expected here).
	if _, _, err := ds.Cremate(nil, crematoriumInstanceID(2), 1, "corr"); err != nil {
		t.Fatalf("Cremate(no bodies) against the freshly-registered crematorium failed: %v", err)
	}

	// The skipped non-deathservices building never registered anything under
	// its own order id.
	if _, _, err := ds.CemeteryOccupancy(cemeteryInstanceID(3), "corr"); err == nil {
		t.Fatalf("CemeteryOccupancy(order 3's derived id) succeeded, want ErrUnknownCemetery (order 3 was not a cemetery)")
	}
}

// TestRegisterCompletedServiceBuildings_IdempotentOnReplay proves calling
// the helper twice with an OVERLAPPING (here: identical) completions slice —
// the shape a replay or an un-advanced cursor produces — registers each
// building exactly once, never erroring on the repeat (RegisterCemetery/
// RegisterCrematorium's own idempotent-registration contract).
func TestRegisterCompletedServiceBuildings_IdempotentOnReplay(t *testing.T) {
	ds := newDeathServicesForRegistryTest(t)
	completions := []build.BuildOrder{
		{ID: 10, BuildingID: "cemetery", Status: build.OrderComplete},
		{ID: 11, BuildingID: "crematorium", Status: build.OrderComplete},
	}
	if err := registerCompletedServiceBuildings(completions, ds, "corr"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	occBefore, capBefore, err := ds.CemeteryOccupancy(cemeteryInstanceID(10), "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy after first call: %v", err)
	}

	// Replay the SAME slice again.
	if err := registerCompletedServiceBuildings(completions, ds, "corr"); err != nil {
		t.Fatalf("second (replay) call: %v", err)
	}
	occAfter, capAfter, err := ds.CemeteryOccupancy(cemeteryInstanceID(10), "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy after replay: %v", err)
	}
	if occAfter != occBefore || capAfter != capBefore {
		t.Fatalf("replay changed cemetery state: before=(%d,%d) after=(%d,%d) — registration is not idempotent", occBefore, capBefore, occAfter, capAfter)
	}
}

// TestRegisterCompletedServiceBuildings_NilDeathServicesIsNoOp proves the
// helper degrades gracefully (not an error) when deathservices is not yet
// wired — mirroring engine.build's own SetServices/registerServiceLocked
// optionality rather than hard-failing a tick over an unwired dependency.
func TestRegisterCompletedServiceBuildings_NilDeathServicesIsNoOp(t *testing.T) {
	completions := []build.BuildOrder{{ID: 1, BuildingID: "cemetery", Status: build.OrderComplete}}
	if err := registerCompletedServiceBuildings(completions, nil, "corr"); err != nil {
		t.Fatalf("registerCompletedServiceBuildings(nil ds) = %v, want nil (no-op)", err)
	}
}

// TestRegisterCompletedServiceBuildings_EmptyCompletionsIsNoOp is the
// trivial boundary — an empty/never-yet-advanced cursor window.
func TestRegisterCompletedServiceBuildings_EmptyCompletionsIsNoOp(t *testing.T) {
	ds := newDeathServicesForRegistryTest(t)
	if err := registerCompletedServiceBuildings(nil, ds, "corr"); err != nil {
		t.Fatalf("registerCompletedServiceBuildings(nil completions): %v", err)
	}
}

// TestRegisterCompletedServiceBuildings_UnknownKindIsObservableViaErrsRecent
// (BUG-734 round follow-up: "confirm the F3 registry record is actually
// persisted/observable") proves the discarded `_ = errs.New(...)` call this
// helper makes for an unrecognised BuildingID is NOT lost: [errs.New]/
// construct's own logEntry choke point (internal/foundation/errs/log.go)
// unconditionally persists every constructed *E to the configured sink, or
// — the common no-sink case a plain test runs under — the package's
// in-memory ring buffer, retrievable via [errs.Recent] (the SAME primitive
// the F12 debug info panel's error tail reads). GR#17's "every monitoring
// failure also writes a registry error" is therefore satisfied by the
// EXISTING error-log infrastructure this codebase already relies on for
// every other discarded-diagnostic-aid call (deathservices'
// ErrNegativeBudget/ErrCorruptHandoffCursor precedent) — no `[]error` skip
// list return value is needed on top of it; adding one would duplicate an
// observability channel that already exists rather than close a real gap.
func TestRegisterCompletedServiceBuildings_UnknownKindIsObservableViaErrsRecent(t *testing.T) {
	ds := newDeathServicesForRegistryTest(t)
	const probeCorrelationID = "bug734-f3-observability-probe"
	completions := []build.BuildOrder{
		{ID: 42, BuildingID: "graveyard", Status: build.OrderComplete}, // the spec's own synonym, unknown to this helper
	}
	if err := registerCompletedServiceBuildings(completions, ds, probeCorrelationID); err != nil {
		t.Fatalf("registerCompletedServiceBuildings: %v", err)
	}

	var found *errs.Entry
	for _, e := range errs.Recent() {
		if e.Code == ErrUnknownDeathServiceBuildingKind && e.CorrelationID == probeCorrelationID {
			e := e
			found = &e
			break
		}
	}
	if found == nil {
		t.Fatalf("ErrUnknownDeathServiceBuildingKind (correlation %q) not found in errs.Recent() — the discarded errs.New call IS genuinely lost, GR#17 violated", probeCorrelationID)
	}
	if found.Ctx["buildingID"] != "graveyard" {
		t.Errorf("logged entry ctx[buildingID] = %v, want %q", found.Ctx["buildingID"], "graveyard")
	}
}
