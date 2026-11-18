package compose

// BUG-1220 r2: the player-facing housing figures on chrome.topbar are pinned to
// the engine's own vacancy rule, and a Load drops them until the next month
// close. Everything here reads the real published patch (buildChromeTopBarPatch).

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// published decodes the housing block of the live chrome.topbar patch.
func published(t *testing.T, st *simState) chromeHousing {
	t.Helper()
	raw, err := st.buildChromeTopBarPatch()
	if err != nil {
		t.Fatalf("buildChromeTopBarPatch: %v", err)
	}
	var p chromeTopBarWirePatch
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	return p.Figures.Housing
}

// independentStock sums the stock WITHOUT housingStockFor: the seed constant
// plus every standing structure's positive resident capacity.
func independentStock(st *simState) int64 {
	total := baselineOneSeedHomeCapacityPeople
	for _, s := range st.buildAPI.StandingCapacities() {
		if s.ResidentCapacity > 0 {
			total += s.ResidentCapacity
		}
	}
	return total
}

func checkHousingAgainstRule(t *testing.T, label string, st *simState) chromeHousing {
	t.Helper()
	h := published(t, st)
	if !h.Known {
		t.Fatalf("%s: housing figures unknown after a month close", label)
	}
	if want := independentStock(st); h.Places != want {
		t.Fatalf("%s: places %d, want seed %d + standing capacity = %d", label, h.Places, baselineOneSeedHomeCapacityPeople, want)
	}
	if want := housingStockFor(st.buildAPI, st.seedHomeCapacityPeople); h.Places != want {
		t.Fatalf("%s: places %d differs from the vacancy rule's stock %d", label, h.Places, want)
	}
	vac := housingVacancyFor(st.buildAPI, st.citizens, st.cid, st.seedHomeCapacityPeople)
	if h.Full != (vac == 0) {
		t.Fatalf("%s: cue Full=%v but the vacancy rule reads %d units", label, h.Full, vac)
	}
	if free := h.Places - h.Lived; free > 0 && h.Free != free || free <= 0 && h.Free != 0 {
		t.Fatalf("%s: free %d does not match places %d - lived %d", label, h.Free, h.Places, h.Lived)
	}
	return h
}

func TestBUG1220_HousingPub_PlacesAndFullBindToVacancyRule(t *testing.T) {
	for _, dwellings := range []int{0, 6} {
		r := ev83New(t)
		st := r.comp.state
		if h := published(t, st); h.Known {
			t.Fatalf("dwellings=%d: figures known before any month closed: %+v", dwellings, h)
		}
		if dwellings > 0 {
			r.buildDwellings(dwellings)
		}
		var sawFull, sawNotFull bool
		var last chromeHousing
		for m := 1; m <= 10; m++ {
			r.advance(30)
			last = checkHousingAgainstRule(t, fmt.Sprintf("month %d", m), st)
			if last.Full {
				sawFull = true
			} else {
				sawNotFull = true
			}
		}
		if dwellings == 0 {
			if last.Places != 120 {
				t.Fatalf("control places %d, want the 120 seed homes", last.Places)
			}
			if !sawFull {
				t.Fatalf("control city never full in 10 months; the Full agreement was never exercised")
			}
		} else {
			if last.Places <= 120 {
				t.Fatalf("6 dwellings built but places stayed %d", last.Places)
			}
			if !sawNotFull {
				t.Fatalf("built city was never non-full; the Full agreement was never exercised both ways")
			}
		}
	}
}

func TestBUG1220_HousingPub_LoadDropsFiguresUntilNextMonthClose(t *testing.T) {
	month := int64(core.DailyTicksPerMonth)
	eA, compA := newTestEngine(t, 4103)
	if err := eA.AdvanceTicks(errs.NewCorrelationID(), 3*month); err != nil {
		t.Fatalf("advance A: %v", err)
	}
	if h := published(t, compA.state); !h.Known {
		t.Fatalf("precondition: figures must be known after three months, got %+v", h)
	}
	clk, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	eB, compB := newTestEngine(t, 4103)
	if err := compB.LoadAt(dir, clk.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if h := published(t, compB.state); h.Known {
		t.Fatalf("figures still known right after a load (stale pre-load numbers): %+v", h)
	}
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), month); err != nil {
		t.Fatalf("advance B: %v", err)
	}
	checkHousingAgainstRule(t, "first close after load", compB.state)
}
