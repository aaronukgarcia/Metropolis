package policies

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
)

// ---------------------------------------------------------------------------
// AdvanceMonth idempotency and drift-event dedupe (MOD-064 REJECT r1). The
// r1 tax-mirror divergence detection was replaced in r2 by the getter-first
// tax read-back (engine.tax.GetDistrictMultiplier) plus the out-of-band
// regression test in regression_test.go.
// ---------------------------------------------------------------------------

// TestAdvanceMonthSameMonthIdempotent proves defect 1 is fixed: a second
// AdvanceMonth for the same month is a no-op — the month's recurring opex is
// posted once and its drift events are raised once, never a double debit and
// never a re-run checkpoint.
func TestAdvanceMonthSameMonthIdempotent(t *testing.T) {
	a := testAPI(t)
	proj := projections.NewProjectionsAPI(projections.WithCorrelationID("t"))
	base := &mutableProvider{value: 100}
	if err := proj.RegisterCurveProvider("wellbeing.parkAccess", projections.CurveProviderFunc(base.Value)); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	if err := proj.SetCurrentMonth(0); err != nil {
		t.Fatalf("SetCurrentMonth: %v", err)
	}
	a.projections = proj
	fin := &recordingFinance{}
	a.finance = fin

	def := simplePolicy("parkAccess", ScopeCitywide, "wellbeing.parkAccess", 10)
	def.Cost = CostDef{OpexMonthlyMicroPounds: 50_000}
	addPolicy(t, a, def)
	mustEnact(t, a, "parkAccess", Scope{Kind: ScopeCitywide})

	// The world diverges from the stored preview, so the quarter checkpoint
	// (month 3) raises a drift event.
	base.set(200)

	events, err := a.AdvanceMonth(3)
	if err != nil {
		t.Fatalf("AdvanceMonth(3): %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("first AdvanceMonth(3) must raise exactly one drift event, got %d", len(events))
	}
	if fin.postCount() != 1 {
		t.Fatalf("first AdvanceMonth(3) must post monthly opex exactly once, got %d posts", fin.postCount())
	}

	// A second advance of the SAME month is a no-op: no second opex debit, no
	// re-run checkpoint, no duplicate drift event.
	again, err := a.AdvanceMonth(3)
	if err != nil {
		t.Fatalf("second AdvanceMonth(3) must be a no-op, not an error: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second AdvanceMonth(3) must raise no events, got %d", len(again))
	}
	if got := fin.postCount(); got != 1 {
		t.Fatalf("second AdvanceMonth(3) must not double-post opex, got %d posts", got)
	}
	if got := fin.debitTotal(); got != finance.Money(50_000) {
		t.Fatalf("opex must be debited exactly once (50000), got %d", got)
	}
	if got := len(a.PreviewDriftEvents()); got != 1 {
		t.Fatalf("drift events must be recorded once, got %d", got)
	}
}

// TestDriftEventDedupe proves defect 2 is fixed: re-running the same
// checkpoint (same enactment + same coefficient + same month) does not
// accumulate duplicate drift events in the queryable log.
func TestDriftEventDedupe(t *testing.T) {
	a := testAPI(t)
	proj := projections.NewProjectionsAPI(projections.WithCorrelationID("t"))
	base := &mutableProvider{value: 100}
	if err := proj.RegisterCurveProvider("wellbeing.parkAccess", projections.CurveProviderFunc(base.Value)); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	if err := proj.SetCurrentMonth(0); err != nil {
		t.Fatalf("SetCurrentMonth: %v", err)
	}
	a.projections = proj
	addPolicy(t, a, simplePolicy("parkAccess", ScopeCitywide, "wellbeing.parkAccess", 10))
	mustEnact(t, a, "parkAccess", Scope{Kind: ScopeCitywide})
	base.set(200)

	if _, err := a.Checkpoint(3); err != nil {
		t.Fatalf("first Checkpoint(3): %v", err)
	}
	if _, err := a.Checkpoint(3); err != nil {
		t.Fatalf("second Checkpoint(3): %v", err)
	}

	got := a.PreviewDriftEvents()
	if len(got) != 1 {
		t.Fatalf("re-running the same checkpoint must not accumulate duplicate drift events, got %d", len(got))
	}
	if got[0].PolicyID != "parkAccess" || got[0].Coefficient != "wellbeing.parkAccess" || got[0].Checkpoint != 3 {
		t.Fatalf("deduped event lost its fields: %+v", got[0])
	}
}
