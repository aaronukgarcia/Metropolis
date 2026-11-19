package compose

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1243 options A, B and C and the BUG-1245 parks option are DATA switches
// in data/attract_terms.json. The shipped file is TODAY'S behaviour (both
// modifiers built, baseline 40, parks as read); Aaron chooses an option by a
// one-line edit:
//
//	A      built.modifiers.safety and .environment = false
//	B      A plus world.aWorld = 20 (or 0)
//	C      A plus built.whenUnbuilt.safety and .environment = "floorAtBaseline"
//	parks  leisure.floorAtBaseline = true
//
// The tests below select each option through bug1243DataVariant (a copy of
// the data directory); the shipped file is never changed.

// bug1243AfterOneMonth wires a city (data as currently set), builds 200
// dwellings and runs a month.
func bug1243AfterOneMonth(t *testing.T) *ev83 {
	t.Helper()
	r := ev83New(t)
	bug1243Build(t, r)
	r.advance(30)
	return r
}

// Option C arithmetic: above the baseline it contributes like a built
// modifier, below it exactly 0, never negative; built and neutral unchanged.
func TestBUG1243_ModifierTerm_FloorAtBaseline(t *testing.T) {
	const w, aw = 0.1, 40.0
	pts := func(lt attract.LeverTerm) float64 {
		if !lt.Built {
			return 0
		}
		return w * (lt.Value - aw)
	}
	if got, want := pts(modifierTerm(90, false, true, aw)), w*(90-aw); math.Abs(got-want) > 1e-12 || got <= 0 {
		t.Fatalf("floored, reading above baseline: %v points, want %v (like built)", got, want)
	}
	for _, reading := range []float64{39.999, 12.4, 0} {
		if got := pts(modifierTerm(reading, false, true, aw)); got != 0 {
			t.Fatalf("floored, reading %v below baseline: %v points, want exactly 0", reading, got)
		}
	}
	for r := 0.0; r <= 100; r += 2.5 {
		if got := pts(modifierTerm(r, false, true, aw)); got < 0 {
			t.Fatalf("floored contribution negative at reading %v: %v", r, got)
		}
	}
	if lt := modifierTerm(12.4, false, false, aw); lt.Built {
		t.Fatalf("neutral mode must publish not built, got %+v", lt)
	}
	if lt := modifierTerm(12.4, true, true, aw); !lt.Built || lt.Value != 12.4 {
		t.Fatalf("a built modifier passes its reading unchanged even below the baseline, got %+v", lt)
	}
}

// The shipped data is today's behaviour; bad values are refused.
func TestBUG1243_ShippedKeys_AreTodaysBehaviour_AndAreValidated(t *testing.T) {
	b, _, err := readAttractTermsFile("bug1243-keys")
	if err != nil {
		t.Fatal(err)
	}
	d, err := parseAttractTermsData(b, "bug1243-keys")
	if err != nil {
		t.Fatal(err)
	}
	if d.World.worldBaseline() != baselineOneAWorld || baselineOneAWorld != 40 {
		t.Fatalf("shipped baseline %v, want the code default 40", d.World.worldBaseline())
	}
	if !d.Built.safetyBuilt() || !d.Built.environmentBuilt() {
		t.Fatalf("shipped data must hold both modifiers built (today's behaviour): %+v", d.Built)
	}
	if d.Built.safetyFloored() || d.Built.environmentFloored() || d.Leisure.FloorAtBaseline {
		t.Fatalf("shipped data must have no floor switches on: %+v", d)
	}
	edit := func(f func(doc map[string]any)) error {
		var doc map[string]any
		_ = json.Unmarshal(b, &doc)
		f(doc)
		nb, _ := json.Marshal(doc)
		_, perr := parseAttractTermsData(nb, "bug1243-keys")
		return perr
	}
	if err := edit(func(doc map[string]any) {
		bug1243Sub(bug1243Sub(doc, "built"), "whenUnbuilt")["safety"] = "floorAtBaseline"
	}); err != nil {
		t.Fatalf("floorAtBaseline refused: %v", err)
	}
	if err := edit(func(doc map[string]any) { bug1243Sub(bug1243Sub(doc, "built"), "whenUnbuilt")["safety"] = "sometimes" }); err == nil {
		t.Fatalf("an unknown whenUnbuilt mode was accepted")
	}
	for _, bad := range []float64{-1, 100.5} {
		if err := edit(func(doc map[string]any) { bug1243Sub(doc, "world")["aWorld"] = bad }); err == nil {
			t.Fatalf("world.aWorld %v was accepted", bad)
		}
	}
}

// Wire refuses bad option data with the registered data error and installs
// nothing (no hook is registered, no composition is returned).
//
// Unknown keys are refused on purpose: a switch spelt almost right
// (floorAtBaselin, aWorl) would be ignored and the game would silently stay on
// today's behaviour, so the one-line data edit Aaron makes would appear to
// work and do nothing.
func TestBUG1243_Wire_RefusesBadOptionData(t *testing.T) {
	replace := func(old, repl string) func([]byte) []byte {
		return func(b []byte) []byte {
			s := string(b)
			if !strings.Contains(s, old) {
				t.Fatalf("shipped file no longer contains %q: update the test", old)
			}
			return []byte(strings.Replace(s, old, repl, 1))
		}
	}
	cases := []struct {
		name string
		edit func([]byte) []byte
	}{
		{"aWorld NaN string", replace(`"aWorld": 40`, `"aWorld": "NaN"`)},
		{"aWorld null", replace(`"aWorld": 40`, `"aWorld": null`)},
		{"aWorld bool", replace(`"aWorld": 40`, `"aWorld": true`)},
		{"aWorld above range", replace(`"aWorld": 40`, `"aWorld": 100.5`)},
		{"aWorld below range", replace(`"aWorld": 40`, `"aWorld": -0.5`)},
		{"empty file", func([]byte) []byte { return nil }},
		{"built not an object (number)", replace(`"built": {`, `"built": 5, "x": {`)},
		{"built not an object (array)", replace(`"built": {`, `"built": [], "x": {`)},
		{"modifiers flag not a bool", replace(`"safety": true, "environment": true`, `"safety": "yes", "environment": true`)},
		{"unknown key aWorl", replace(`"aWorld": 40`, `"aWorl": 20`)},
		{"unknown key floorAtBaselin", replace(`"floorAtBaseline": false`, `"floorAtBaselin": true`)},
		{"unknown key whenUnbuilt.saftey", replace(`"safety": "neutral"`, `"saftey": "floorAtBaseline"`)},
		{"unknown top-level block", replace(`"world": {`, `"wrld": {"aWorld": 20}, "world": {`)},
		{"unknown mode", replace(`"safety": "neutral"`, `"safety": "floorAtBaselin"`)},
	}
	// One copy of the data directory; each case rewrites only attract_terms.json.
	var shipped []byte
	bug1243RawVariant(t, func(b []byte) []byte { shipped = b; return b })
	file := filepath.Join(os.Getenv("METROPOLIS_DATA_DIR"), attractTermsFile)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(file, tc.edit(shipped), 0o644); err != nil {
				t.Fatal(err)
			}
			e := core.NewEngine()
			comp, err := Wire(e, &Deps{CorrelationID: errs.NewCorrelationID()})
			if err == nil {
				t.Fatalf("Wire accepted the bad data (%s)", tc.name)
			}
			var ee *errs.E
			if !errors.As(err, &ee) || ee.Code != ErrModuleFailed {
				t.Fatalf("error = %v, want the registered ErrModuleFailed", err)
			}
			if comp != nil || e.HookCount() != 0 {
				t.Fatalf("Wire installed something despite the error: comp=%v hooks=%d", comp, e.HookCount())
			}
		})
	}
}

// A data file with NO new blocks at all (the file as it was before BUG-1243)
// behaves exactly as today: same score, same wanted migration as the shipped
// file, which spells today's behaviour out.
func TestBUG1243_DataFileWithNoNewBlocks_BehavesAsToday(t *testing.T) {
	run := func() (float64, float64) {
		r := ev83New(t)
		bug1243BuildRange(t, r, 0, 40)
		r.advance(30)
		pre, _ := r.comp.state.attract.A()
		return pre, bug1243Wanted(r.comp)
	}
	preS, wS := run()
	bug1243DataVariant(t, func(doc map[string]any) {
		delete(doc, "built")
		delete(doc, "world")
		delete(bug1243Sub(doc, "leisure"), "floorAtBaseline")
	})
	preO, wO := run()
	if preS != preO || wS != wO {
		t.Fatalf("a file without the new blocks differs from the shipped file: score %v vs %v, wanted %v vs %v", preO, preS, wO, wS)
	}
}

// Option B: the baseline is a data value.
func TestBUG1243_WorldBaseline_IsData(t *testing.T) {
	r := ev83New(t)
	if got := r.comp.state.attract.AWorld(); got != 40 {
		t.Fatalf("default baseline %v, want 40", got)
	}
	bug1243DataVariant(t, func(doc map[string]any) { bug1243Sub(doc, "world")["aWorld"] = 31.5 })
	r2 := ev83New(t)
	if got := r2.comp.state.attract.AWorld(); got != 31.5 {
		t.Fatalf("baseline from data %v, want 31.5", got)
	}
}

// Option C wired: with both readings above the baseline the floored city
// scores exactly like the built city and differs from the neutral one; with
// the environment reading far below the baseline its published value is the
// baseline (zero points) and only safety counts.
func TestBUG1243_FloorAtBaseline_Wired(t *testing.T) {
	score := func(edit func(doc map[string]any)) (*attract.AttractAPI, float64) {
		if edit != nil {
			bug1243DataVariant(t, edit)
		}
		r := bug1243AfterOneMonth(t)
		a := r.comp.state.attract
		pre, err := a.A()
		if err != nil {
			t.Fatalf("A: %v", err)
		}
		return a, pre
	}
	_, preBuilt := score(nil) // shipped = built
	_, preNeutral := score(bug1243Neutral)
	aFloor, preFloor := score(bug1243Floor)
	if preFloor != preBuilt || preFloor == preNeutral {
		t.Fatalf("readings above baseline: floor %v must equal built %v and differ from neutral %v", preFloor, preBuilt, preNeutral)
	}
	for i := 0; i < attract.ModifierCount; i++ {
		if _, built := aFloor.ModifierValue(attract.ModifierID(i)); !built {
			t.Fatalf("floored modifier %d must publish as built (the panel has no other counted state)", i)
		}
	}
	// Environment far below the baseline: a tiny half-saturation point makes
	// the refuse backlog saturate it.
	lowEnv := func(doc map[string]any) {
		bug1243Floor(doc)
		bug1243Sub(doc, "environment")["pollutionHalfSaturationKg"] = 0.001
	}
	aLow, preLow := score(lowEnv)
	envVal, _ := aLow.ModifierValue(attract.ModifierEnvironment)
	if envVal != aLow.AWorld() {
		t.Fatalf("environment floored value %v, want the baseline %v (its reading is below it)", envVal, aLow.AWorld())
	}
	if preLow > preFloor {
		t.Fatalf("a floored environment below the baseline raised the score: %v > %v", preLow, preFloor)
	}
	if preLow < preNeutral {
		t.Fatalf("a floored modifier lowered the score below the neutral city: %v < %v", preLow, preNeutral)
	}
}

// Each option is wired and moves the score the way the options table says,
// proven on a small city over three months (the multi-year table is behind
// METROPOLIS_MEASURE).
func TestBUG1243_EachOption_IsWired(t *testing.T) {
	wantedFor := func(edit func(doc map[string]any)) (float64, int) {
		if edit != nil {
			bug1243DataVariant(t, edit)
		}
		r := ev83New(t)
		bug1243BuildRange(t, r, 0, 40)
		for i := 0; i < 3; i++ {
			r.advance(30)
		}
		return bug1243Wanted(r.comp), r.comp.Population()
	}
	today, popToday := wantedFor(nil)
	a, popA := wantedFor(bug1243Neutral)
	b20, _ := wantedFor(bug1243Baseline(20))
	b0, _ := wantedFor(bug1243Baseline(0))
	c, popC := wantedFor(bug1243Floor)
	t.Logf("wanted after 3 months, 40 dwellings: today %.3f (pop %d), A %.3f (pop %d), B20 %.3f, B0 %.3f, C %.3f (pop %d)", today, popToday, a, popA, b20, b0, c, popC)
	if !(a < b20 && b20 < b0) {
		t.Fatalf("a lower baseline must raise wanted migration: A %.3f, B20 %.3f, B0 %.3f", a, b20, b0)
	}
	if !(a < today) {
		t.Fatalf("option A must be slower than today: A %.3f, today %.3f", a, today)
	}
	// Option C counts a reading only when it helps, today counts it even when it
	// hurts, so C is never below today (month 1, where both readings sit above
	// the baseline, is exactly equal: TestBUG1243_FloorAtBaseline_Wired).
	if c < today || popC < popToday {
		t.Fatalf("option C must never be below today: C %.3f/%d, today %.3f/%d", c, popC, today, popToday)
	}
}

// bug1243OpenVenue buys, zones and builds one entertainment cell and runs
// months until the venue registers, then one more.
func bug1243OpenVenue(t *testing.T, r *ev83, c protocol.CellRef) {
	t.Helper()
	before := len(r.comp.state.leisureVenuesRegistered)
	sendAndAwait(t, r.transport, protocol.KindBuy, protocol.BuyPayload{Cell: c})
	if res := sendAndAwait(t, r.transport, protocol.KindZone, protocol.ZonePayload{Cell: c, ZoneType: "entertainment"}); !res.Accepted {
		t.Fatalf("zone rejected: %+v", res.Error)
	}
	if res := sendAndAwait(t, r.transport, protocol.KindBuild, protocol.BuildPayload{Cell: c, BuildingType: "entertainment"}); !res.Accepted {
		t.Fatalf("build rejected: %+v", res.Error)
	}
	for i := 0; i < 4 && len(r.comp.state.leisureVenuesRegistered) == before; i++ {
		r.advance(30)
	}
	if len(r.comp.state.leisureVenuesRegistered) == before {
		t.Fatalf("venue at %+v never registered", c)
	}
	r.advance(30)
}

// BUG-1245 parks option (leisure.floorAtBaseline): the parks lever's value is
// never below the baseline, so before any venue it reads the baseline (zero
// points), and a venue can only raise it. Off (shipped), it reads the
// venue-less fit below the baseline, as today.
func TestBUG1243_ParksFloor_NeverBelowBaseline_AndVenuesOnlyRaiseIt(t *testing.T) {
	parks := func(r *ev83) float64 {
		v, built := r.comp.state.attract.LeverValue(attract.LeverParks)
		if !built {
			t.Fatalf("parks must stay a built lever")
		}
		return v
	}
	off := ev83New(t)
	bug1243BuildRange(t, off, 0, 10)
	off.advance(30)
	if got, aw := parks(off), off.comp.state.attract.AWorld(); got >= aw {
		t.Fatalf("shipped (option off) parks %v should sit below the baseline %v (venue-less fit)", got, aw)
	}
	bug1243DataVariant(t, bug1243ParksFloor)
	on := ev83New(t)
	bug1243BuildRange(t, on, 0, 10)
	on.advance(30)
	aw := on.comp.state.attract.AWorld()
	prev := parks(on)
	if prev != aw {
		t.Fatalf("option on, no venue: parks %v, want the baseline %v", prev, aw)
	}
	t.Logf("option on: before any venue %.3f", prev)
	for i := 0; i < 3; i++ {
		bug1243OpenVenue(t, on, protocol.CellRef{X: 30 + i, Y: 30})
		got := parks(on)
		t.Logf("option on: after %d venue(s) %.3f", i+1, got)
		if got < prev || got < aw {
			t.Fatalf("after venue %d parks %.3f is below the %.3f before it (or the baseline %.3f)", i+1, got, prev, aw)
		}
		prev = got
	}
}
