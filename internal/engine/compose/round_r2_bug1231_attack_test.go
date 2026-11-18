package compose

// BUG-1231 round r2 attack tests (attacker opus-round-r2-bug1231). Kept as
// regressions: every record the engine writes must pass its own validation
// across seeds and a full year of seasons; the envelope edges behave; a
// refused plain Load (not only LoadAt) and a refused snapshot restore leave
// nothing behind; and a LoadAt at the month edges continues identically.

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Every record a running engine would write passes validation, at every tick
// over thirteen months, at four seeds; the saves taken each month load.
func TestRoundR2BUG1231_EveryLegitRecordValidates(t *testing.T) {
	const ticks = 400
	for _, seed := range []uint64{roundTripSeed, 7, 424242, 99} {
		e, c := b1231Mk(t, seed)
		withData := 0
		pLo, pHi, mLo, mHi := c.state.wellbeingAPI.TrackEnvelope()
		minMargin := math.Inf(1)
		for tk := int64(1); tk <= ticks; tk++ {
			if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
				t.Fatal(err)
			}
			w := composeLedgerToWire(c.state).Wellbeing
			if err := validateSavedWellbeing(c.state.wellbeingAPI, "cid", *w); err != nil {
				t.Fatalf("seed %d tick %d: a record the engine produced was refused: %v (%+v)", seed, tk, err, *w)
			}
			if !w.NoData {
				withData++
				for _, m := range []float64{w.MeanPhysical - pLo, pHi - w.MeanPhysical, w.MeanMental - mLo, mHi - w.MeanMental} {
					minMargin = math.Min(minMargin, m)
				}
			}
			if tk%30 == 0 || tk == 1 {
				root := t.TempDir()
				if err := c.Save(root); err != nil {
					t.Fatalf("Save: %v", err)
				}
				_, cL := b1231Mk(t, seed)
				if err := cL.LoadAt(root, tk); err != nil {
					t.Fatalf("seed %d: the engine's own save at tick %d was refused: %v", seed, tk, err)
				}
			}
		}
		if withData == 0 {
			t.Fatalf("VACUOUS: seed %d never produced a record with data", seed)
		}
		t.Logf("seed %d: %d/%d ticks with data, smallest mean margin to the envelope %.3f (envelope phys [%v,%v] ment [%v,%v])", seed, withData, ticks, minMargin, pLo, pHi, mLo, mHi)
	}
}

// The envelope edges: a record at a corner, or one ulp beyond it, is
// accepted; a clear step beyond is refused, for each mean and modifier.
func TestRoundR2BUG1231_EnvelopeEdges(t *testing.T) {
	_, c := b1231Mk(t, roundTripSeed)
	w := c.state.wellbeingAPI
	pLo, pHi, mLo, mHi := w.TrackEnvelope()
	mk := func(p, m float64) wellbeingStatusWire {
		return wellbeingStatusWire{SampleSize: 500, MeanPhysical: p, MeanMental: m,
			MortalityModifier: w.MortalityModifier(p, m), ProductivityModifier: w.ProductivityModifier(p, m),
			SatisfactionModifier: w.SatisfactionModifier(p, m), EmigrationModifier: w.EmigrationModifier(p, m)}
	}
	accept := map[string]wellbeingStatusWire{
		"low corner":            mk(pLo, mLo),
		"high corner":           mk(pHi, mHi),
		"low corner -1ulp":      mk(math.Nextafter(pLo, math.Inf(-1)), math.Nextafter(mLo, math.Inf(-1))),
		"high corner +1ulp":     mk(math.Nextafter(pHi, math.Inf(1)), math.Nextafter(mHi, math.Inf(1))),
		"mixed corner lo/hi":    mk(pLo, mHi),
		"sampleSize 1 at cap":   func() wellbeingStatusWire { r := mk(pHi, mHi); r.SampleSize = 1; return r }(),
		"sampleSize at the cap": func() wellbeingStatusWire { r := mk(60, 60); r.SampleSize = wellbeingSampleCap; return r }(),
		"noData neutral": {SampleSize: 0, NoData: true, MortalityModifier: 1, ProductivityModifier: 1,
			SatisfactionModifier: 1, EmigrationModifier: 1},
	}
	// The mean of 500 citizens all at the high corner, summed in order.
	sum := 0.0
	for i := 0; i < wellbeingSampleCap; i++ {
		sum += pHi
	}
	accept["summed mean at the high corner"] = mk(sum/float64(wellbeingSampleCap), mHi)
	for name, r := range accept {
		if err := validateSavedWellbeing(w, "cid", r); err != nil {
			t.Errorf("%s: refused a value the engine could produce: %v", name, err)
		}
	}
	step := func(v, dir float64) float64 { return v + dir*1e-6*(1+math.Abs(v)) }
	refuse := map[string]func(r *wellbeingStatusWire){
		"physical below": func(r *wellbeingStatusWire) { r.MeanPhysical = step(pLo, -1) },
		"physical above": func(r *wellbeingStatusWire) { r.MeanPhysical = step(pHi, 1) },
		"mental below":   func(r *wellbeingStatusWire) { r.MeanMental = step(mLo, -1) },
		"mental above":   func(r *wellbeingStatusWire) { r.MeanMental = step(mHi, 1) },
		"mortality above": func(r *wellbeingStatusWire) {
			r.MortalityModifier = step(math.Max(w.MortalityModifier(pLo, mLo), w.MortalityModifier(pHi, mHi)), 1)
		},
		"mortality below": func(r *wellbeingStatusWire) {
			r.MortalityModifier = step(math.Min(w.MortalityModifier(pLo, mLo), w.MortalityModifier(pHi, mHi)), -1)
		},
		"productivity above": func(r *wellbeingStatusWire) {
			r.ProductivityModifier = step(math.Max(w.ProductivityModifier(pLo, mLo), w.ProductivityModifier(pHi, mHi)), 1)
		},
		"satisfaction below": func(r *wellbeingStatusWire) {
			r.SatisfactionModifier = step(math.Min(w.SatisfactionModifier(pLo, mLo), w.SatisfactionModifier(pHi, mHi)), -1)
		},
		"emigration above": func(r *wellbeingStatusWire) {
			r.EmigrationModifier = step(math.Max(w.EmigrationModifier(pLo, mLo), w.EmigrationModifier(pHi, mHi)), 1)
		},
		"noData with a non-zero mean": func(r *wellbeingStatusWire) {
			*r = wellbeingStatusWire{NoData: true, MeanPhysical: 1, MortalityModifier: 1, ProductivityModifier: 1, SatisfactionModifier: 1, EmigrationModifier: 1}
		},
		"noData with a non-neutral modifier": func(r *wellbeingStatusWire) {
			*r = wellbeingStatusWire{NoData: true, MortalityModifier: 1.01, ProductivityModifier: 1, SatisfactionModifier: 1, EmigrationModifier: 1}
		},
		"sampleSize zero without noData": func(r *wellbeingStatusWire) { r.SampleSize = 0 },
		"sampleSize cap+1":               func(r *wellbeingStatusWire) { r.SampleSize = wellbeingSampleCap + 1 },
	}
	for name, mut := range refuse {
		r := mk(60, 60)
		mut(&r)
		if err := validateSavedWellbeing(w, "cid", r); !errors.Is(err, &errs.E{Code: ErrSavedWellbeingInvalid}) {
			t.Errorf("%s: want %s, got %v", name, ErrSavedWellbeingInvalid, err)
		}
	}
}

// A refused PLAIN Load into a composition that already holds a loaded city
// changes nothing, and the city then runs exactly as one that never saw the
// refused file.
func TestRoundR2BUG1231_RefusedPlainLoadLeavesUsedCompositionIntact(t *testing.T) {
	const seed = roundTripSeed
	good := b1231Save(t, seed, 31)
	bad := b1231Save(t, seed, 61)
	b1231SetField(t, bad, "emigrationModifier", "7")

	eU, cU := b1231Mk(t, seed)
	if err := cU.LoadAt(good, 31); err != nil {
		t.Fatal(err)
	}
	eR, cR := b1231Mk(t, seed)
	if err := cR.LoadAt(good, 31); err != nil {
		t.Fatal(err)
	}
	before := b1231Take(t, eU, cU)
	if before.Saved == nil {
		t.Fatal("VACUOUS: no saved status installed")
	}
	err := cU.Load(bad)
	if !errors.Is(err, &errs.E{Code: ErrSavedWellbeingInvalid}) {
		t.Fatalf("want %s, got %v", ErrSavedWellbeingInvalid, err)
	}
	if after := b1231Take(t, eU, cU); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused plain Load changed the composition:\n before %+v\n after  %+v", before, after)
	}
	b1231LockStep(t, "after refused plain Load", eU, cU, eR, cR, 90)
}

// A refused load whose wellbeing record is fine but whose bundle the save
// manager refuses (a save of another world seed) keeps the previous load's
// saved status (save_wire.go prevSaved restore).
func TestRoundR2BUG1231_ManagerRefusalKeepsPreviousSavedStatus(t *testing.T) {
	good := b1231Save(t, roundTripSeed, 31)
	other := b1231Save(t, 7, 31)
	eU, cU := b1231Mk(t, roundTripSeed)
	if err := cU.LoadAt(good, 31); err != nil {
		t.Fatal(err)
	}
	before := b1231Take(t, eU, cU)
	if before.Saved == nil {
		t.Fatal("VACUOUS: no saved status installed")
	}
	if err := cU.Load(other); err == nil {
		t.Fatal("a save of another world seed was accepted")
	}
	if after := b1231Take(t, eU, cU); !reflect.DeepEqual(before, after) {
		t.Fatalf("a manager-refused load changed the composition:\n before %+v\n after  %+v", before, after)
	}
}

// The snapshot restore path (metroserve's rehydrate goes through it) refuses a
// damaged record too: restoreFromSnapshotBytes reaches Load's pre-validation.
func TestRoundR2BUG1231_SnapshotRestoreRefusesDamagedRecord(t *testing.T) {
	bad := b1231Save(t, roundTripSeed, 31)
	b1231SetField(t, bad, "mortalityModifier", "-5")
	data, err := zipDir(bad)
	if err != nil {
		t.Fatal(err)
	}
	_, c := b1231Mk(t, roundTripSeed)
	if _, err := c.restoreFromSnapshotBytes(data); !errors.Is(err, &errs.E{Code: ErrSavedWellbeingInvalid}) {
		t.Fatalf("snapshot restore: want %s, got %v", ErrSavedWellbeingInvalid, err)
	}
}

// The original defect: LoadAt at the month edges continues identically for
// 180 ticks at two seeds; an old-format save at the same ticks still loads.
func TestRoundR2BUG1231_ContinuityAtMonthEdges(t *testing.T) {
	for _, seed := range []uint64{roundTripSeed, 424242} {
		for _, saveTick := range []int64{0, 1, 29, 30, 31, 59, 60, 90} {
			eRef, cRef := b1231Mk(t, seed)
			if saveTick > 0 {
				if err := eRef.AdvanceTicks(errs.NewCorrelationID(), saveTick); err != nil {
					t.Fatal(err)
				}
			}
			root := t.TempDir()
			if err := cRef.Save(root); err != nil {
				t.Fatal(err)
			}
			old := t.TempDir()
			if err := cRef.Save(old); err != nil {
				t.Fatal(err)
			}
			b1231StripWellbeing(t, old)
			eB, cB := b1231Mk(t, seed)
			if err := cB.LoadAt(root, saveTick); err != nil {
				t.Fatalf("seed %d LoadAt %d: %v", seed, saveTick, err)
			}
			_, cO := b1231Mk(t, seed)
			if err := cO.LoadAt(old, saveTick); err != nil {
				t.Fatalf("seed %d old-format LoadAt %d: %v", seed, saveTick, err)
			}
			if cO.state.wellbeingSaved != nil {
				t.Fatal("old-format save installed a saved status")
			}
			b1231LockStep(t, "continuity", eRef, cRef, eB, cB, 180)
		}
	}
}
