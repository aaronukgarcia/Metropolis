package compose

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"regexp"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestBUG1231_LoadAt_WellbeingStatusSurvivesSave: the month-start wellbeing
// status (read by mortality, migration and firm output) is part of the save,
// so a mid-run save loaded into a fresh composition continues byte-identically
// for three months, saved at tick 0, mid-month and on a month boundary, at
// three seeds. Before the fix the load re-derived the status from the
// population at the save tick, and the first monthly migration step and
// mortality draw after the load differed from the uninterrupted run.
func TestBUG1231_LoadAt_WellbeingStatusSurvivesSave(t *testing.T) {
	const endTick = int64(90)
	for _, seed := range []uint64{roundTripSeed, 7, 424242} {
		for _, saveTick := range []int64{0, 3, 30} {
			mk := func() (*core.Engine, *Composition) {
				e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
				c, err := Wire(e, &Deps{Coastal: quietCoastal(t)})
				if err != nil {
					t.Fatalf("Wire: %v", err)
				}
				return e, c
			}
			eRef, cRef := mk()
			if saveTick > 0 {
				if err := eRef.AdvanceTicks(errs.NewCorrelationID(), saveTick); err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			if err := cRef.Save(dir); err != nil {
				t.Fatalf("Save: %v", err)
			}
			eB, cB := mk()
			if err := cB.LoadAt(dir, saveTick); err != nil {
				t.Fatalf("LoadAt: %v", err)
			}
			for tk := saveTick + 1; tk <= endTick; tk++ {
				if err := eRef.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
					t.Fatal(err)
				}
				if err := eB.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
					t.Fatal(err)
				}
				if cRef.StateDigest() != cB.StateDigest() || cRef.PopulationHash() != cB.PopulationHash() || cRef.Treasury() != cB.Treasury() {
					t.Fatalf("seed %d save@%d: diverged at tick %d (treasury %d vs %d, wellbeing ref %+v loaded %+v)",
						seed, saveTick, tk, cRef.Treasury(), cB.Treasury(), cRef.WellbeingStatus(), cB.WellbeingStatus())
				}
			}
		}
	}
}

// --- BUG-1231 r2: refusal of a damaged record, used compositions, absent shard ---

// b1231Mk builds a fresh composition on the given seed.
func b1231Mk(t *testing.T, seed uint64) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	c, err := Wire(e, &Deps{Coastal: quietCoastal(t)})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, c
}

// b1231Save runs a fresh composition to saveTick and saves it under a new root.
func b1231Save(t *testing.T, seed uint64, saveTick int64) string {
	t.Helper()
	e, c := b1231Mk(t, seed)
	if saveTick > 0 {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), saveTick); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	if err := c.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return root
}

// b1231Bundle returns the bundle directory under a Save root.
func b1231Bundle(t *testing.T, root string) string {
	t.Helper()
	summaries, _, err := save.List(root)
	if err != nil {
		t.Fatalf("save.List: %v", err)
	}
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			return s.Path
		}
	}
	t.Fatalf("no composition save under %q", root)
	return ""
}

// b1231EditLedger rewrites the compose-ledger shard through edit (or deletes
// the shard and its header entry when edit is nil) and repairs the header's
// size and checksum, so the bundle still validates: this models a damaged or
// hand-edited save, not a corrupt file.
func b1231EditLedger(t *testing.T, root string, edit func(plain string) string) {
	t.Helper()
	dir := b1231Bundle(t, root)
	hdr, err := serialize.ReadHeader(dir)
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	idx := -1
	for i, sm := range hdr.ShardIndex {
		if sm.Kind == kindComposeLedger {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("no compose-ledger shard in bundle")
	}
	path, err := serialize.ShardPath(dir, hdr.ShardIndex[idx])
	if err != nil {
		t.Fatal(err)
	}
	if edit == nil {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		hdr.ShardIndex = append(hdr.ShardIndex[:idx], hdr.ShardIndex[idx+1:]...)
		if err := serialize.WriteHeader(dir, hdr); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	_ = zr.Close()
	changed := edit(string(plain))
	if changed == string(plain) {
		t.Fatal("ledger edit changed nothing (no matching wellbeing field in the record?)")
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(changed)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	hdr.ShardIndex[idx].ByteSize = int64(buf.Len())
	hdr.ShardIndex[idx].SHA256 = hex.EncodeToString(sum[:])
	if err := serialize.WriteHeader(dir, hdr); err != nil {
		t.Fatal(err)
	}
}

var b1231WellbeingRe = regexp.MustCompile(`,"wellbeing":\{[^}]*\}`)

// b1231StripWellbeing turns a new-format save into an old-format one (the
// compose-ledger record without its wellbeing field).
func b1231StripWellbeing(t *testing.T, root string) {
	t.Helper()
	b1231EditLedger(t, root, func(p string) string { return b1231WellbeingRe.ReplaceAllString(p, "") })
}

// b1231SetField rewrites one field inside the saved wellbeing object to the
// given raw JSON text.
func b1231SetField(t *testing.T, root, field, raw string) {
	t.Helper()
	re := regexp.MustCompile(`("wellbeing":\{[^}]*"` + field + `":)[^,}]+`)
	b1231EditLedger(t, root, func(p string) string { return re.ReplaceAllString(p, "${1}"+raw) })
}

type b1231Snap struct {
	Digest, Pop string
	Treasury    int64
	Status      WellbeingStatus
	Saved       *WellbeingStatus
	Tick        int64
}

func b1231Take(t *testing.T, e *core.Engine, c *Composition) b1231Snap {
	t.Helper()
	clock, err := e.Clock()
	if err != nil {
		t.Fatal(err)
	}
	s := b1231Snap{Digest: fmt.Sprintf("%x", c.StateDigest()), Pop: fmt.Sprint(c.PopulationHash()), Treasury: c.Treasury(), Status: c.WellbeingStatus(), Tick: clock.Tick()}
	if c.state.wellbeingSaved != nil {
		v := *c.state.wellbeingSaved
		s.Saved = &v
	}
	return s
}

// TestBUG1231_R2_DamagedWellbeingRecordRefusesWholeLoad: ruling (1). Every
// field of the saved record is validated before any state is installed; a
// value the wellbeing module cannot produce refuses the whole load with the
// registered error and the composition behaves exactly as before the attempt.
func TestBUG1231_R2_DamagedWellbeingRecordRefusesWholeLoad(t *testing.T) {
	const seed = roundTripSeed
	cases := []struct{ name, field, raw string }{
		{"negative mortality modifier", "mortalityModifier", "-5"},
		{"huge mortality modifier", "mortalityModifier", "1e300"},
		{"huge satisfaction modifier", "satisfactionModifier", "1e300"},
		{"negative productivity modifier", "productivityModifier", "-5"},
		{"huge emigration modifier", "emigrationModifier", "1e300"},
		{"huge physical mean", "meanPhysical", "1e300"},
		{"negative mental mean", "meanMental", "-1e9"},
		{"negative sample size", "sampleSize", "-3"},
		{"sample size over the cap", "sampleSize", "100000"},
		{"noData contradicts sampleSize", "noData", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := b1231Save(t, seed, 31)
			b1231SetField(t, root, tc.field, tc.raw)
			// A composition that has run past the save point, so a half-installed load would show.
			e, c := b1231Mk(t, seed)
			if err := e.AdvanceTicks(errs.NewCorrelationID(), 45); err != nil {
				t.Fatal(err)
			}
			before := b1231Take(t, e, c)
			err := c.LoadAt(root, 31)
			if err == nil {
				t.Fatal("damaged wellbeing record was accepted")
			}
			if !errors.Is(err, &errs.E{Code: ErrSavedWellbeingInvalid}) {
				t.Fatalf("want %s, got %v", ErrSavedWellbeingInvalid, err)
			}
			if after := b1231Take(t, e, c); !reflect.DeepEqual(before, after) {
				t.Fatalf("a refused load changed the composition:\n before %+v\n after  %+v", before, after)
			}
		})
	}
}

// A NaN or an infinity cannot be written into a JSON save (the decoder refuses
// the number), so a hand-edited file carrying one is refused at decode time;
// the validator is also called directly so the guard does not rest on the
// decoder alone.
func TestBUG1231_R2_NonFiniteWellbeingRefused(t *testing.T) {
	const seed = roundTripSeed
	_, c := b1231Mk(t, seed)
	w0 := c.state.wellbeingAPI
	good := wellbeingStatusWire{SampleSize: 10, MeanPhysical: 60, MeanMental: 60, MortalityModifier: w0.MortalityModifier(60, 60), ProductivityModifier: w0.ProductivityModifier(60, 60), SatisfactionModifier: w0.SatisfactionModifier(60, 60), EmigrationModifier: w0.EmigrationModifier(60, 60)}
	if err := validateSavedWellbeing(c.state.wellbeingAPI, "cid", good); err != nil {
		t.Fatalf("control record refused: %v", err)
	}
	for name, mut := range map[string]func(w *wellbeingStatusWire){
		"NaN mortality": func(w *wellbeingStatusWire) { w.MortalityModifier = math.NaN() },
		"+Inf emigrate": func(w *wellbeingStatusWire) { w.EmigrationModifier = math.Inf(1) },
		"-Inf satisf":   func(w *wellbeingStatusWire) { w.SatisfactionModifier = math.Inf(-1) },
		"NaN physical":  func(w *wellbeingStatusWire) { w.MeanPhysical = math.NaN() },
		"+Inf mental":   func(w *wellbeingStatusWire) { w.MeanMental = math.Inf(1) },
		"sampleSize -3": func(w *wellbeingStatusWire) { w.SampleSize = -3 },
		"noData w/size": func(w *wellbeingStatusWire) { w.NoData = true },
	} {
		w := good
		mut(&w)
		if err := validateSavedWellbeing(c.state.wellbeingAPI, "cid", w); !errors.Is(err, &errs.E{Code: ErrSavedWellbeingInvalid}) {
			t.Errorf("%s: want %s, got %v", name, ErrSavedWellbeingInvalid, err)
		}
	}
	// And through a real file: text NaN / 1e999 is refused (decode failure), nothing installed.
	for _, raw := range []string{"NaN", "1e999"} {
		root := b1231Save(t, seed, 31)
		b1231SetField(t, root, "mortalityModifier", raw)
		e, c := b1231Mk(t, seed)
		if err := e.AdvanceTicks(errs.NewCorrelationID(), 45); err != nil {
			t.Fatal(err)
		}
		before := b1231Take(t, e, c)
		if err := c.LoadAt(root, 31); err == nil {
			t.Fatalf("file carrying %s was accepted", raw)
		}
		if after := b1231Take(t, e, c); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: a refused load changed the composition", raw)
		}
	}
}

// A record the engine itself wrote must still load (the validator is not so
// tight that it refuses what the engine produces).
func TestBUG1231_R2_GoodRecordStillLoads(t *testing.T) {
	root := b1231Save(t, roundTripSeed, 31)
	_, c := b1231Mk(t, roundTripSeed)
	if err := c.LoadAt(root, 31); err != nil {
		t.Fatalf("a record the engine wrote was refused: %v", err)
	}
}

// b1231LockStep advances two compositions together and fails on the first divergence.
func b1231LockStep(t *testing.T, label string, eA *core.Engine, cA *Composition, eB *core.Engine, cB *Composition, ticks int) {
	t.Helper()
	for i := 0; i < ticks; i++ {
		if err := eA.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
			t.Fatal(err)
		}
		if err := eB.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
			t.Fatal(err)
		}
		if cA.StateDigest() != cB.StateDigest() || cA.PopulationHash() != cB.PopulationHash() || cA.Treasury() != cB.Treasury() {
			t.Fatalf("%s: diverged %d ticks after the load (treasury %d vs %d, wellbeing %+v vs %+v)", label, i+1, cA.Treasury(), cB.Treasury(), cA.WellbeingStatus(), cB.WellbeingStatus())
		}
	}
}

// Ruling (2): ONE composition loads a new-format save, then an old-format
// save; the second load must behave as a fresh load of the old save.
func TestBUG1231_R2_UsedCompositionOldSaveBehavesAsFreshLoad(t *testing.T) {
	const seed = roundTripSeed
	newFmt := b1231Save(t, seed, 31)
	oldFmt := b1231Save(t, seed, 3)
	b1231StripWellbeing(t, oldFmt)

	eU, cU := b1231Mk(t, seed)
	if err := cU.LoadAt(newFmt, 31); err != nil {
		t.Fatalf("load new-format: %v", err)
	}
	if cU.state.wellbeingSaved == nil {
		t.Fatal("VACUOUS: the new-format load installed no saved status")
	}
	if err := cU.LoadAt(oldFmt, 3); err != nil {
		t.Fatalf("load old-format into used composition: %v", err)
	}
	eF, cF := b1231Mk(t, seed)
	if err := cF.LoadAt(oldFmt, 3); err != nil {
		t.Fatalf("fresh load of old-format: %v", err)
	}
	if cU.state.wellbeingSaved != nil {
		t.Fatal("old-format save kept the previous load's saved status")
	}
	if !reflect.DeepEqual(cU.WellbeingStatus(), cF.WellbeingStatus()) {
		t.Fatalf("status after the second load differs from a fresh load: %+v vs %+v", cU.WellbeingStatus(), cF.WellbeingStatus())
	}
	b1231LockStep(t, "used vs fresh (old-format)", eU, cU, eF, cF, 100)
}

// Ruling (3): a bundle with no compose-ledger shard at all must not keep the
// status a previous load installed.
func TestBUG1231_R2_ShardlessBundleDoesNotKeepPreviousStatus(t *testing.T) {
	const seed = roundTripSeed
	newFmt := b1231Save(t, seed, 31)
	bare := b1231Save(t, seed, 3)
	b1231EditLedger(t, bare, nil)

	_, cU := b1231Mk(t, seed)
	if err := cU.LoadAt(newFmt, 31); err != nil {
		t.Fatalf("load new-format: %v", err)
	}
	if cU.state.wellbeingSaved == nil {
		t.Fatal("VACUOUS: the new-format load installed no saved status")
	}
	if err := cU.LoadAt(bare, 3); err != nil {
		t.Fatalf("shardless load: %v", err)
	}
	if cU.state.wellbeingSaved != nil {
		t.Fatal("a bundle with no compose-ledger shard kept the previous load's saved status")
	}
	_, cF := b1231Mk(t, seed)
	if err := cF.LoadAt(bare, 3); err != nil {
		t.Fatalf("fresh shardless load: %v", err)
	}
	if !reflect.DeepEqual(cU.WellbeingStatus(), cF.WellbeingStatus()) {
		t.Fatalf("status after a shardless load differs from a fresh shardless load: %+v vs %+v", cU.WellbeingStatus(), cF.WellbeingStatus())
	}
}
