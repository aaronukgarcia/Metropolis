package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
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

// ---------------------------------------------------------------------------
// BUG-743 — unregisterDemolishedServiceBuildings: the demolition-side
// mirror of registerCompletedServiceBuildings, closing the gap BUG-734 left
// open. Driven the same way: direct []build.Demolition literals against a
// real *deathservices.DeathServicesAPI, no world/season/logistics/build
// fixture needed.
// ---------------------------------------------------------------------------

func TestUnregisterDemolishedServiceBuildings_UnregistersCemeteryAndCrematorium(t *testing.T) {
	ds := newDeathServicesForRegistryTest(t)
	completions := []build.BuildOrder{
		{ID: 1, BuildingID: "cemetery", Status: build.OrderComplete},
		{ID: 2, BuildingID: "crematorium", Status: build.OrderComplete},
	}
	if err := registerCompletedServiceBuildings(completions, ds, "corr"); err != nil {
		t.Fatalf("registerCompletedServiceBuildings: %v", err)
	}

	demolitions := []build.Demolition{
		{OrderID: 1, BuildingID: "cemetery", DemolitionSeq: 1},
		{OrderID: 2, BuildingID: "crematorium", DemolitionSeq: 2},
	}
	if err := unregisterDemolishedServiceBuildings(demolitions, ds, "corr"); err != nil {
		t.Fatalf("unregisterDemolishedServiceBuildings: %v", err)
	}

	if _, _, err := ds.CemeteryOccupancy(cemeteryInstanceID(1), "corr"); err == nil {
		t.Fatalf("CemeteryOccupancy(%s) succeeded after demolition, want ErrUnknownCemetery", cemeteryInstanceID(1))
	}
	if _, _, err := ds.Cremate(nil, crematoriumInstanceID(2), 1, "corr"); err == nil {
		t.Fatalf("Cremate against %s succeeded after demolition, want ErrUnknownCrematorium", crematoriumInstanceID(2))
	}
}

// TestUnregisterDemolishedServiceBuildings_NilDeathServicesIsNoOp mirrors
// registerCompletedServiceBuildings' own "not wired yet" degrade.
func TestUnregisterDemolishedServiceBuildings_NilDeathServicesIsNoOp(t *testing.T) {
	demolitions := []build.Demolition{{OrderID: 1, BuildingID: "cemetery", DemolitionSeq: 1}}
	if err := unregisterDemolishedServiceBuildings(demolitions, nil, "corr"); err != nil {
		t.Fatalf("unregisterDemolishedServiceBuildings(nil ds) = %v, want nil (no-op)", err)
	}
}

// TestUnregisterDemolishedServiceBuildings_EmptyIsNoOp is the trivial
// boundary — an empty/never-yet-advanced demolition cursor window.
func TestUnregisterDemolishedServiceBuildings_EmptyIsNoOp(t *testing.T) {
	ds := newDeathServicesForRegistryTest(t)
	if err := unregisterDemolishedServiceBuildings(nil, ds, "corr"); err != nil {
		t.Fatalf("unregisterDemolishedServiceBuildings(nil demolitions): %v", err)
	}
}

// TestUnregisterDemolishedServiceBuildings_NeverRegisteredIsLoggedNoOp
// proves the documented idempotency shape: a demolition naming a building
// this ds instance never actually registered (e.g. a demolition for an id
// that predates BUG-734/BUG-743, or a re-delivered window) is NOT an error
// — it is a logged, GR#17-observable no-op via
// [ErrDemolitionAlreadyDeregistered], the same errs.Recent() observability
// contract ErrUnknownDeathServiceBuildingKind already relies on.
func TestUnregisterDemolishedServiceBuildings_NeverRegisteredIsLoggedNoOp(t *testing.T) {
	ds := newDeathServicesForRegistryTest(t)
	const probeCorrelationID = "bug743-never-registered-probe"
	demolitions := []build.Demolition{
		{OrderID: 99, BuildingID: "cemetery", DemolitionSeq: 1},
	}
	if err := unregisterDemolishedServiceBuildings(demolitions, ds, probeCorrelationID); err != nil {
		t.Fatalf("unregisterDemolishedServiceBuildings: %v", err)
	}

	var found *errs.Entry
	for _, e := range errs.Recent() {
		if e.Code == ErrDemolitionAlreadyDeregistered && e.CorrelationID == probeCorrelationID {
			e := e
			found = &e
			break
		}
	}
	if found == nil {
		t.Fatalf("ErrDemolitionAlreadyDeregistered (correlation %q) not found in errs.Recent() — the discarded no-op is genuinely lost, GR#17 violated", probeCorrelationID)
	}
	if found.Ctx["orderID"] != uint64(99) {
		t.Errorf("logged entry ctx[orderID] = %v, want %d", found.Ctx["orderID"], uint64(99))
	}
}

// TestUnregisterDemolishedServiceBuildings_RedeliveredDemolitionIsLoggedNoOp
// proves the SAME redelivery-tolerance for a REAL prior registration: unregister
// once (succeeds), then unregister the identical demolition record again
// (the shape a caller re-delivers when its persisted cursor has not
// advanced past this demolition yet) — the second call must also be a
// logged no-op, never a hard error.
func TestUnregisterDemolishedServiceBuildings_RedeliveredDemolitionIsLoggedNoOp(t *testing.T) {
	ds := newDeathServicesForRegistryTest(t)
	if err := registerCompletedServiceBuildings([]build.BuildOrder{{ID: 5, BuildingID: "cemetery", Status: build.OrderComplete}}, ds, "corr"); err != nil {
		t.Fatalf("registerCompletedServiceBuildings: %v", err)
	}
	demolitions := []build.Demolition{{OrderID: 5, BuildingID: "cemetery", DemolitionSeq: 1}}
	if err := unregisterDemolishedServiceBuildings(demolitions, ds, "corr"); err != nil {
		t.Fatalf("first unregister: %v", err)
	}
	// Redeliver the SAME demolition record.
	if err := unregisterDemolishedServiceBuildings(demolitions, ds, "corr-redelivered"); err != nil {
		t.Fatalf("redelivered unregister: %v", err)
	}
}

// TestUnregisterDemolishedServiceBuildings_UnknownKindLogged mirrors
// registerCompletedServiceBuildings' own unknown-BuildingID observability
// (F3), on the demolition side.
func TestUnregisterDemolishedServiceBuildings_UnknownKindLogged(t *testing.T) {
	ds := newDeathServicesForRegistryTest(t)
	const probeCorrelationID = "bug743-unknown-kind-probe"
	demolitions := []build.Demolition{
		{OrderID: 7, BuildingID: "graveyard", DemolitionSeq: 1}, // unknown synonym, mirrors the F3 register-side test
	}
	if err := unregisterDemolishedServiceBuildings(demolitions, ds, probeCorrelationID); err != nil {
		t.Fatalf("unregisterDemolishedServiceBuildings: %v", err)
	}
	var found bool
	for _, e := range errs.Recent() {
		if e.Code == ErrUnknownDeathServiceBuildingKind && e.CorrelationID == probeCorrelationID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ErrUnknownDeathServiceBuildingKind (correlation %q) not found in errs.Recent()", probeCorrelationID)
	}
}

// TestUnregisterDemolishedServiceBuildings_BulldozeMidBacklog is BUG-743's
// sharpest teeth: registers two cemeteries and two crematoria, runs a real
// death backlog through Intake/Bury/Cremate (some buried, some cremated,
// some left AWAITING — a genuine mid-backlog city), then bulldozes ONE
// cemetery and ONE crematorium mid-backlog and asserts:
//
//   - bodies are conserved across the demolition (AC-14's identity —
//     intake == buried + cremated + dispensed + awaiting(+en-route) — holds
//     identically before and after, since UnregisterCemetery/
//     UnregisterCrematorium never touch a Body record, only the
//     registration bookkeeping);
//   - disposal capacity ([DeathServicesAPI.MonthlyDrainCapacity]) drops in
//     THIS SAME call, reflecting one fewer cemetery/crematorium
//     immediately, not on some later tick;
//   - the SURVIVING cemetery/crematorium are completely untouched — still
//     queryable, still live.
func TestUnregisterDemolishedServiceBuildings_BulldozeMidBacklog(t *testing.T) {
	ds := newDeathServicesForRegistryTest(t)
	completions := []build.BuildOrder{
		{ID: 1, BuildingID: "cemetery", Status: build.OrderComplete},
		{ID: 2, BuildingID: "cemetery", Status: build.OrderComplete},
		{ID: 3, BuildingID: "crematorium", Status: build.OrderComplete},
		{ID: 4, BuildingID: "crematorium", Status: build.OrderComplete},
	}
	if err := registerCompletedServiceBuildings(completions, ds, "corr"); err != nil {
		t.Fatalf("registerCompletedServiceBuildings: %v", err)
	}

	// A real backlog: 6 deaths intaken, 2 buried, 2 cremated, 2 left
	// AWAITING (the "mid-backlog" shape).
	deaths := make([]citizens.RealisedDeath, 6)
	for i := range deaths {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
	}
	if _, err := ds.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if err := ds.Bury(1, cemeteryInstanceID(1), 1, "corr"); err != nil {
		t.Fatalf("Bury(1): %v", err)
	}
	if err := ds.Bury(2, cemeteryInstanceID(2), 1, "corr"); err != nil {
		t.Fatalf("Bury(2): %v", err)
	}
	if _, _, err := ds.Cremate([]uint64{3, 4}, crematoriumInstanceID(3), 1, "corr"); err != nil {
		t.Fatalf("Cremate: %v", err)
	}
	// Citizens 5 and 6 stay AWAITING — the backlog.

	before, err := ds.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot (before): %v", err)
	}
	if before.Sum() != before.BodiesReleased {
		t.Fatalf("setup: conservation identity does not hold BEFORE demolition: %+v (sum=%d)", before, before.Sum())
	}
	capBefore := ds.MonthlyDrainCapacity(1)

	// Bulldoze cemetery #1 and crematorium #3 mid-backlog.
	demolitions := []build.Demolition{
		{OrderID: 1, BuildingID: "cemetery", DemolitionSeq: 1},
		{OrderID: 3, BuildingID: "crematorium", DemolitionSeq: 2},
	}
	if err := unregisterDemolishedServiceBuildings(demolitions, ds, "corr"); err != nil {
		t.Fatalf("unregisterDemolishedServiceBuildings: %v", err)
	}

	// Conservation holds IDENTICALLY across the demolition — no body was
	// touched, only registration bookkeeping.
	after, err := ds.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot (after): %v", err)
	}
	if after != before {
		t.Fatalf("conservation snapshot changed across a demolition that touches no bodies: before=%+v after=%+v", before, after)
	}
	if after.Sum() != after.BodiesReleased {
		t.Fatalf("conservation identity broken after demolition: %+v (sum=%d)", after, after.Sum())
	}

	// Capacity drops in THIS SAME call (one fewer cemetery/crematorium).
	capAfter := ds.MonthlyDrainCapacity(1)
	if capAfter >= capBefore {
		t.Fatalf("MonthlyDrainCapacity did not drop after bulldozing a cemetery+crematorium: before=%d after=%d", capBefore, capAfter)
	}

	// The demolished ids are gone.
	if _, _, err := ds.CemeteryOccupancy(cemeteryInstanceID(1), "corr"); err == nil {
		t.Fatalf("CemeteryOccupancy(demolished cemetery) succeeded, want ErrUnknownCemetery")
	}
	if _, _, err := ds.Cremate(nil, crematoriumInstanceID(3), 1, "corr"); err == nil {
		t.Fatalf("Cremate(demolished crematorium) succeeded, want ErrUnknownCrematorium")
	}

	// The SURVIVING cemetery #2 and crematorium #4 are untouched.
	occ2, cap2, err := ds.CemeteryOccupancy(cemeteryInstanceID(2), "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy(surviving cemetery): %v", err)
	}
	if occ2 != 1 || cap2 <= 0 {
		t.Fatalf("surviving cemetery occupancy/capacity = (%d, %d), want (1, >0) — its own buried body must still be counted", occ2, cap2)
	}
	if _, _, err := ds.Cremate(nil, crematoriumInstanceID(4), 1, "corr"); err != nil {
		t.Fatalf("Cremate(surviving crematorium) failed: %v", err)
	}
}

// TestUnregisterDemolishedServiceBuildings_SharedCrematoriumDeregisteredOnlyOnLast
// proves the shared CrematoriumServiceID (engine.services registration) is
// only removed once the LAST crematorium is bulldozed — demolishing the
// first of two must leave the shared registration alive (mirroring
// deathservices' own TestUnregisterCrematorium_SharedServiceIDOnlyDeregisteredWhenLastRemoved,
// exercised here through the compose-level helper this bug wires up).
func TestUnregisterDemolishedServiceBuildings_SharedCrematoriumDeregisteredOnlyOnLast(t *testing.T) {
	sv, err := services.LoadDefault("corr")
	if err != nil {
		t.Fatalf("services.LoadDefault: %v", err)
	}
	ds := newDeathServicesForRegistryTest(t)
	if err := ds.Wire(sv, nil, "corr"); err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := registerCompletedServiceBuildings([]build.BuildOrder{
		{ID: 1, BuildingID: "crematorium", Status: build.OrderComplete},
		{ID: 2, BuildingID: "crematorium", Status: build.OrderComplete},
	}, ds, "corr"); err != nil {
		t.Fatalf("registerCompletedServiceBuildings: %v", err)
	}
	if _, err := sv.Capacity(deathservices.CrematoriumServiceID); err != nil {
		t.Fatalf("setup: shared CrematoriumServiceID not registered: %v", err)
	}

	// Demolish the FIRST crematorium: the shared registration must survive.
	if err := unregisterDemolishedServiceBuildings([]build.Demolition{
		{OrderID: 1, BuildingID: "crematorium", DemolitionSeq: 1},
	}, ds, "corr"); err != nil {
		t.Fatalf("unregisterDemolishedServiceBuildings (first): %v", err)
	}
	if _, err := sv.Capacity(deathservices.CrematoriumServiceID); err != nil {
		t.Fatalf("shared CrematoriumServiceID deregistered after only ONE of two crematoria demolished: %v", err)
	}

	// Demolish the LAST crematorium: the shared registration must now be gone.
	if err := unregisterDemolishedServiceBuildings([]build.Demolition{
		{OrderID: 2, BuildingID: "crematorium", DemolitionSeq: 2},
	}, ds, "corr"); err != nil {
		t.Fatalf("unregisterDemolishedServiceBuildings (last): %v", err)
	}
	if _, err := sv.Capacity(deathservices.CrematoriumServiceID); err == nil {
		t.Fatalf("shared CrematoriumServiceID still registered after the LAST crematorium was demolished")
	}
}
