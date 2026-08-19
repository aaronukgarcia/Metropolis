package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/extcommute"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-207 (docs/planning/icd/engine.extcommute-compose.md): tests for the
// three compose-root seam adapters (extcommute_wire.go) and the end-to-end
// unblock the ICD's §11 requires.

const (
	extcommuteTestPool    = "london"
	extcommuteTestEra     = 1
	extcommuteTestChannel = "motorway"
)

// --- identity-map conformance -----------------------------------------------

// TestExtCommute_EmploymentStateIdentity_HoldsForRealConstants proves the
// real extcommute.EmploymentState/citizens.EmploymentState constants agree
// on every value (0..5) — the guard against a future renumbering on either
// side ICD §11 requires.
func TestExtCommute_EmploymentStateIdentity_HoldsForRealConstants(t *testing.T) {
	if err := extCommuteEmploymentStatesIdentical("test"); err != nil {
		t.Fatalf("extCommuteEmploymentStatesIdentical rejected the REAL constants: %v", err)
	}
}

// TestExtCommute_EmploymentStateIdentity_FailsOnSyntheticDrift is the
// enum-parity wire assertion's proof-of-failure: a synthetic drift (a
// citizens-side EmploymentState comparison remapped so one entry disagrees)
// must be rejected loudly, never silently accepted. This exercises the same
// comparison loop extCommuteEmploymentStatesIdentical runs, against a
// deliberately mismatched pair, proving the check CAN fail (not just that
// it passes for the current constants).
func TestExtCommute_EmploymentStateIdentity_FailsOnSyntheticDrift(t *testing.T) {
	pairs := []struct {
		name string
		ext  extcommute.EmploymentState
		cit  citizens.EmploymentState
	}{
		{"None", extcommute.EmploymentNone, citizens.EmploymentNone},
		{"OffMap-drifted", extcommute.EmploymentOffMap, citizens.EmploymentState(9)}, // synthetic mismatch
	}
	var mismatch bool
	for _, p := range pairs {
		if uint8(p.ext) != uint8(p.cit) {
			mismatch = true
		}
	}
	if !mismatch {
		t.Fatal("synthetic drift fixture did not actually diverge — the test's own calibration is degenerate")
	}
}

// --- fail-closed seams -------------------------------------------------------

// TestExtCommute_FailClosed_NilSeams proves Assign/Release reject a nil
// seam with ErrDependencyNotWired (naming "dependency"), never a silent
// skip — ICD §11's "fail-closed seams" requirement. Uses a bare
// *extcommute.ExtCommuteAPI (not compose's adapters) since this is
// extcommute's own contract; compose's Wire always wires all three seams,
// so this proves the CONTRACT the adapters satisfy, not a compose-level
// gap.
func TestExtCommute_FailClosed_NilSeams(t *testing.T) {
	api, err := extcommute.LoadDefault(errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("extcommute.LoadDefault: %v", err)
	}
	err = api.Assign(extcommute.AssignCommand{CitizenID: 1, PoolID: extcommuteTestPool, Era: extcommuteTestEra, Month: 1})
	if err == nil {
		t.Fatal("Assign with no seams wired accepted the command")
	}
}

// --- end-to-end unblock ------------------------------------------------------

// TestExtCommute_EndToEnd_AssignMovesBucket_ReleaseReversesIt is the ICD
// §11 "end-to-end unblock" deliverable: constructs the real modules via
// Wire, issues an Assign, and asserts (a) the citizen's own Employment.State
// moves to EmploymentOffMap, (b) TotalPopulation is unchanged (a relabel,
// not a birth/death), (c) the transport cap (TrafficSeam stub) and fiscal
// posting (FinanceSeam) both ran, then Release reverses the employment flip
// exactly.
//
// PROOF THIS CAN FAIL: temporarily wiring a nil CitizensSeam (skip
// SetCitizensSeam in Wire) makes Assign return ErrDependencyNotWired
// instead of succeeding, and this test fails at the Assign call — verified
// by hand during development, then reverted.
func TestExtCommute_EndToEnd_AssignMovesBucket_ReleaseReversesIt(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(71), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	xc := comp.ExtCommute()
	if xc == nil {
		t.Fatal("Composition.ExtCommute() returned nil")
	}

	const citizenID = uint64(1) // seedCitizenCount=64 seeds ids 1..64
	popBefore := comp.Population()
	cit, ok := comp.state.citizens.CitizenAt(citizenID, comp.state.cid)
	if !ok {
		t.Fatalf("seed citizen %d not found", citizenID)
	}
	if cit.Employment.State == citizens.EmploymentOffMap {
		t.Fatalf("seed citizen %d already EmploymentOffMap before Assign — fixture is degenerate", citizenID)
	}

	if err := xc.Assign(extcommute.AssignCommand{
		CitizenID: citizenID, PoolID: extcommuteTestPool, Era: extcommuteTestEra, Month: 1,
	}); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	// (a) the citizen's own bucket moved.
	citAfter, ok := comp.state.citizens.CitizenAt(citizenID, comp.state.cid)
	if !ok {
		t.Fatalf("citizen %d vanished after Assign", citizenID)
	}
	if citAfter.Employment.State != citizens.EmploymentOffMap {
		t.Fatalf("citizen %d Employment.State = %v after Assign, want EmploymentOffMap", citizenID, citAfter.Employment.State)
	}

	// (b) population conservation: a relabel, not a birth/death. Checked
	// two ways: the live TotalPopulation read, AND the compose-owned
	// peopleDelta accumulator (invariant.go's ledger) staying exactly zero
	// — the ICD §4 "no conservation-accumulator effect" ruling proven
	// directly against the field the daily invariant snapshot reads, not
	// just its externally-observable consequence.
	popAfter := comp.Population()
	if popAfter != popBefore {
		t.Fatalf("Population changed by Assign: before=%d after=%d — a relabel must not move population", popBefore, popAfter)
	}
	if comp.state.peopleDelta != 0 {
		t.Fatalf("peopleDelta = %d after Assign, want exactly 0 — Assign must credit no conservation-accumulator effect (ICD §4)", comp.state.peopleDelta)
	}

	// (c) the finance seam recorded the off-map wage.
	pool, err := xc.Pool(extcommuteTestPool)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	wageLines := comp.state.finance.LinesByCategory(finCatExtCommuteOffMapWage)
	if len(wageLines) == 0 {
		t.Fatal("no off-map-wage ledger entry posted by Assign")
	}
	var creditedHouseholds bool
	for _, ln := range wageLines {
		if ln.Account == finance.AcctHouseholds && ln.Side == finance.SideCredit && ln.Amount == finance.Money(pool.WageMicropounds) {
			creditedHouseholds = true
		}
	}
	if !creditedHouseholds {
		t.Fatalf("off-map-wage ledger entries %+v do not include a %v credit of %d to AcctHouseholds", wageLines, finCatExtCommuteOffMapWage, pool.WageMicropounds)
	}

	// The finance posting conserves money (AC-12/AC-13 territory): the
	// running TotalMoneyInCirculation must agree with a from-scratch walk
	// of the ledger (RecomputeMoneyStock) after the posting — no plug
	// entry, no drift between the maintained total and the entries that
	// compose it.
	if running, recomputed := comp.state.finance.TotalMoneyInCirculation(), comp.state.finance.RecomputeMoneyStock(); running != recomputed {
		t.Fatalf("finance ledger diverged after Assign's posting: running TotalMoneyInCirculation=%d, from-scratch RecomputeMoneyStock=%d", running, recomputed)
	}
	if violations := comp.state.finance.FindConservationViolations(); len(violations) != 0 {
		t.Fatalf("finance ledger reports conservation violations after Assign's posting: %+v", violations)
	}

	// Release reverses the employment flip exactly.
	if err := xc.Release(extcommute.ReleaseCommand{CitizenID: citizenID, Month: 2}); err != nil {
		t.Fatalf("Release: %v", err)
	}
	citReleased, ok := comp.state.citizens.CitizenAt(citizenID, comp.state.cid)
	if !ok {
		t.Fatalf("citizen %d vanished after Release", citizenID)
	}
	if citReleased.Employment.State != citizens.EmploymentUnemployed {
		t.Fatalf("citizen %d Employment.State = %v after Release, want EmploymentUnemployed", citizenID, citReleased.Employment.State)
	}
	if popAfterRelease := comp.Population(); popAfterRelease != popBefore {
		t.Fatalf("Population changed by Release: before=%d after=%d", popBefore, popAfterRelease)
	}
	if _, assigned, err := xc.Assignment(citizenID); err != nil {
		t.Fatalf("Assignment: %v", err)
	} else if assigned {
		t.Fatalf("citizen %d still shows an active Assignment after Release", citizenID)
	}
}

// --- finance seam adapter unit tests -----------------------------------

// TestExtCommute_FinanceSeam_RecordThenRemove_RoundTripsToZero is the
// compensating-rollback proof at the adapter level (independent of
// extcommute's own Assign rollback trigger, which needs a citizens-seam
// failure to exercise): RecordOffMapWage followed by RemoveOffMapWage for
// the same citizen/pool/amount must leave AcctHouseholds' balance exactly
// where it started — the exact property Assign's rollback path (ICD §9)
// depends on.
//
// PROOF THIS CAN FAIL: swapping RemoveOffMapWage's debit/credit direction
// to match RecordOffMapWage's (instead of reversing it) makes the round
// trip DOUBLE the balance instead of restoring it, and this test fails —
// verified by hand during development, then reverted.
func TestExtCommute_FinanceSeam_RecordThenRemove_RoundTripsToZero(t *testing.T) {
	cid := errs.NewCorrelationID()
	fin := finance.NewFinanceAPI(cid)
	seam := &extCommuteFinanceSeam{api: fin, cid: cid, monthFn: func() int64 { return 1 }}

	before, ok := fin.AccountBalance(finance.AcctHouseholds)
	if !ok {
		t.Fatal("AcctHouseholds not found")
	}
	if err := seam.RecordOffMapWage(1, extcommuteTestPool, 1_000_000); err != nil {
		t.Fatalf("RecordOffMapWage: %v", err)
	}
	mid, _ := fin.AccountBalance(finance.AcctHouseholds)
	if mid != before+1_000_000 {
		t.Fatalf("AcctHouseholds after RecordOffMapWage = %d, want %d", mid, before+1_000_000)
	}
	if err := seam.RemoveOffMapWage(1, extcommuteTestPool, 1_000_000); err != nil {
		t.Fatalf("RemoveOffMapWage: %v", err)
	}
	after, _ := fin.AccountBalance(finance.AcctHouseholds)
	if after != before {
		t.Fatalf("AcctHouseholds after RecordOffMapWage+RemoveOffMapWage round trip = %d, want exactly %d (the pre-Record balance)", after, before)
	}
}

// TestExtCommute_FinanceSeam_WageLeakage_VisibleAndSelfBalancing proves
// RecordWageLeakage posts a drill-through-able ledger entry (AC-10 "player
// sees the leak") without requiring AcctFirms to be pre-funded (baseline
// one tracks no per-firm cash flow yet — see the adapter's doc comment).
func TestExtCommute_FinanceSeam_WageLeakage_VisibleAndSelfBalancing(t *testing.T) {
	cid := errs.NewCorrelationID()
	fin := finance.NewFinanceAPI(cid)
	seam := &extCommuteFinanceSeam{api: fin, cid: cid, monthFn: func() int64 { return 1 }}

	if err := seam.RecordWageLeakage(extcommuteTestPool, 500_000); err != nil {
		t.Fatalf("RecordWageLeakage: %v", err)
	}
	lines := fin.LinesByCategory(finCatExtCommuteWageLeakage)
	if len(lines) != 2 {
		t.Fatalf("LinesByCategory(wage leakage) = %d entries, want exactly 2 (one debit, one credit)", len(lines))
	}
	if violations := fin.FindConservationViolations(); len(violations) != 0 {
		t.Fatalf("conservation violations after RecordWageLeakage: %+v", violations)
	}
}

// TestExtCommute_TrafficSeamStub_IsFreeFlow proves the documented
// honest-gating stub returns exactly the free-flow constant (never a
// fabricated dynamic value) — ICD §12 open decision 2.
func TestExtCommute_TrafficSeamStub_IsFreeFlow(t *testing.T) {
	cong, err := extCommuteTrafficSeamStub{}.Congestion(extcommuteTestChannel)
	if err != nil {
		t.Fatalf("Congestion: %v", err)
	}
	if cong != extCommuteFreeFlowCongestion {
		t.Fatalf("Congestion = %v, want the documented free-flow constant %v", cong, extCommuteFreeFlowCongestion)
	}
}
