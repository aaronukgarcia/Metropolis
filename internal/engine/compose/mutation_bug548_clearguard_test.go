package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// TestMutation_PayrollShortfallClearsOnZeroPrivateBillCleanMonth is the
// round-requested mutation-coverage proof for the financeHook.ApplyEffect
// clear-guard fix: the shortfall-clear condition was widened from
// `privateWageBill > 0 && firmsPaidPrivate` to plain `firmsPaidPrivate`
// (compose.go) so a genuinely clean month — zero private wage bill owed —
// clears the PayrollShortfall surface, not just a month where a private
// bill existed and was successfully posted.
//
// Reaching a genuinely privateWageBill==0 month through the LIVE monthly
// tick (r2Month, 30 days) is NOT reliably reachable at baseline-one scale
// (measured: TestBUG548R2_ShortfallSurfaceClearsOnAnAllPublicCleanMonth and
// an earlier draft of this test both land byFirms > 0 on every attempt —
// churn within the SAME month's earlier daily ticks, e.g. a newly-arrived
// migrant or newly-adult resident financeHook's own markEmploymentAndCount
// call decides fresh this month, always reintroduces a small private
// headcount before financeHook ever runs). This test instead calls
// financeHook.ApplyEffect DIRECTLY, exactly once, with NO further ticking
// between forcing every Employed resident to citizens.SectorPublic and the
// call — so markEmploymentAndCount (invoked from inside ApplyEffect) sees
// no newly-undecided resident to freshly decide (any such resident from an
// earlier month was already decided by that month's own r2Month), and
// employedPrivate is deterministically 0. This is still the REAL
// production code path (the exact *financeHook the composition root
// registers), not a reimplementation — only the driving mechanism (direct
// call vs. a full month of ticks) differs.
//
// Verified by hand during development that reverting the clear condition
// from `else if firmsPaidPrivate` back to
// `else if privateWageBill > 0 && firmsPaidPrivate` REDS this exact test
// (privateWageBill == 0 never satisfies `privateWageBill > 0`, so
// RecordPayrollShortfall(month, 0) never runs and PayrollShortfall keeps
// reporting the earlier starved amount) — the mutation is not re-applied
// here since a passing suite must never carry a self-reverting edit, only
// the proof that one would fail.
func TestMutation_PayrollShortfallClearsOnZeroPrivateBillCleanMonth(t *testing.T) {
	e, comp := newTestEngine(t, 8181)
	st := comp.state
	f := st.finance

	// Grow the city until the employed headcount alone can carry a bill
	// above monthlyWagesFloor (mirrors TestBUG548R2_ShortfallSurfaceClearsOnAnAllPublicCleanMonth's
	// identical setup).
	needed := int(monthlyWagesFloor/monthlyWageGrossPerEmployedMicropounds) + 1
	grown := 0
	for month := 1; month <= 60; month++ {
		r2Month(t, e)
		grown = st.employedResidentCount()
		if grown >= needed {
			break
		}
	}
	if grown < needed {
		t.Skipf("city only reached %d employed in 60 months, need >= %d for a zero-private-bill month — cannot reach this shape at baseline-one scale", grown, needed)
	}

	// Starve one month so the surface is set to a real, non-zero value.
	r2ExhaustFirms(t, f, 1_000)
	r2Month(t, e)
	if _, amount := f.PayrollShortfall(); amount <= 0 {
		t.Fatalf("starved month did not set the shortfall surface (%d)", int64(amount))
	}

	// Force every currently-Employed resident to SectorPublic, then call
	// financeHook.ApplyEffect directly — no intervening tick, so no new
	// resident can be freshly decided into a private sector between the
	// force and the wage computation (see doc comment above).
	forced := r2ForceSector(t, st, citizens.SectorPublic)
	if forced < needed {
		t.Skipf("only %d employed residents to force public (need >= %d) — cannot reach this shape at baseline-one scale", forced, needed)
	}

	hook := &financeHook{st: st}
	hook.ApplyEffect(core.Effect{Sequence: 0, Payload: financeEffect{}})

	credited, byFirms, _, _ := r2WageLegs(f)
	if byFirms != 0 {
		t.Fatalf("fixture invalid: byFirms = %d, want 0 (every employed resident was just forced to SectorPublic with no intervening tick) — the fixture's own zero-private-bill precondition is not holding, not the production code under test", byFirms)
	}
	if credited <= monthlyWagesFloor {
		t.Fatalf("fixture invalid: credited = %d, want > floor = %d, so the clear site (not the floor-backstop branch) is the one being exercised", credited, int64(monthlyWagesFloor))
	}

	if _, amount := f.PayrollShortfall(); amount != 0 {
		t.Fatalf("a genuinely clean month (privateWageBill==0, byFirms=0, credited=%d > floor=%d) left PayrollShortfall at %d, want 0 — the clear-guard fix (financeHook.ApplyEffect, compose.go: `else if firmsPaidPrivate`) is not reachable; reverting it to `privateWageBill > 0 && firmsPaidPrivate` reproduces exactly this red",
			credited, int64(monthlyWagesFloor), int64(amount))
	}
}
