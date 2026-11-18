package compose

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-1143: save/load continuity of employment. The item's recipe (seed 4103,
// 3 months, Save, Load, advance) is only valid when the save is loaded into a
// FRESH composition with LoadAt (which seeds the clock) and compared against an
// UNINTERRUPTED control at the same tick (BUG-1201 was closed as the invalid
// harness). This pins employed/unemployed counts, PopulationHash and the
// StateDigest equal every month after load, saving at a month boundary and
// mid-month.

func employmentCounts(st *simState) (employed, unemployed int64) {
	st.gatherLiveResidents(func(_ uint64, cit citizens.Citizen, ok bool) {
		if !ok {
			return
		}
		switch cit.Employment.State {
		case citizens.EmploymentUnemployed:
			unemployed++
		case citizens.EmploymentEmployed:
			employed++
		}
	})
	return
}

func employmentContinuityRun(t *testing.T, seed uint64, extraTicks int64) {
	t.Helper()
	month := int64(core.DailyTicksPerMonth)
	eCtl, ctl := newTestEngine(t, seed)
	eSrc, src := newTestEngine(t, seed)
	fiscalR1Endow(t, ctl, 500_000_000_000)
	fiscalR1Endow(t, src, 500_000_000_000)
	for _, e := range []*core.Engine{eCtl, eSrc} {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), 3*month+extraTicks); err != nil {
			t.Fatalf("advance to save point: %v", err)
		}
	}
	dir := t.TempDir()
	if err := src.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clk, err := eSrc.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	eLoad, loaded := newTestEngine(t, seed)
	if err := loaded.LoadAt(dir, clk.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	compare := func(label string) {
		t.Helper()
		ce, cu := employmentCounts(ctl.state)
		le, lu := employmentCounts(loaded.state)
		if ce != le || cu != lu {
			t.Fatalf("seed %d +%d %s: employed/unemployed control %d/%d, loaded %d/%d", seed, extraTicks, label, ce, cu, le, lu)
		}
		if ctl.PopulationHash() != loaded.PopulationHash() {
			t.Fatalf("seed %d +%d %s: PopulationHash differs", seed, extraTicks, label)
		}
		if ctl.StateDigest() != loaded.StateDigest() {
			t.Fatalf("seed %d +%d %s: StateDigest differs", seed, extraTicks, label)
		}
	}
	ce, cu := employmentCounts(ctl.state)
	if ce == 0 {
		t.Fatalf("fixture: no employed residents at the save point (employed %d, unemployed %d)", ce, cu)
	}
	t.Logf("seed %d +%d save point employed=%d unemployed=%d", seed, extraTicks, ce, cu)
	compare("at load")
	for m := 1; m <= 5; m++ {
		for _, e := range []*core.Engine{eCtl, eLoad} {
			if err := e.AdvanceTicks(errs.NewCorrelationID(), month); err != nil {
				t.Fatalf("advance month %d: %v", m, err)
			}
		}
		compare(fmt.Sprintf("after +%d months", m))
	}
}

func TestBUG1143_SaveLoadEmploymentContinuity(t *testing.T) {
	for _, seed := range []uint64{4103, 4104, 4207} {
		for _, extra := range []int64{0, 11} {
			seed, extra := seed, extra
			t.Run(fmt.Sprintf("seed%d_extra%d", seed, extra), func(t *testing.T) {
				employmentContinuityRun(t, seed, extra)
			})
		}
	}
}
