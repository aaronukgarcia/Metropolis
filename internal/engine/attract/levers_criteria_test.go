package attract

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/households"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// Criteria tests for the ten-lever migration formula (FEAT-9995385616,
// docs/planning/acceptance/reason-panel-criteria.md C2-C9, C19). Names carry
// the criterion number so a red run says which criterion broke.

// neutralTI is every lever, modifier and reinforcer not built, with the
// given Value written into every slot (to prove an unbuilt value is ignored).
func neutralTI(ignored float64) TermInputs {
	var in TermInputs
	for i := 0; i < LeverCount; i++ {
		in.Levers[i] = LeverTerm{Value: ignored}
	}
	for i := 0; i < ModifierCount; i++ {
		in.Modifiers[i] = LeverTerm{Value: ignored}
	}
	for i := 0; i < ReinforcerCount; i++ {
		in.Reinforcers[i] = LeverTerm{Value: ignored}
	}
	return in
}

// splitmix is a tiny local deterministic generator (no math/rand).
type splitmix struct{ s uint64 }

func (g *splitmix) next() uint64 {
	g.s += 0x9E3779B97F4A7C15
	z := g.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// unit returns a float in [0,1).
func (g *splitmix) unit() float64 { return float64(g.next()>>11) / float64(1<<53) }

// TestC2_NeutralCityIsExactlyNeutral: all ten levers and both modifiers not
// built, reputation 0, factor 1: A == A_world exactly, Net == 0, nobody
// moves, every line is exactly zero.
func TestC2_NeutralCityIsExactlyNeutral(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())
	if err := a.SetTermInputs(neutralTI(0)); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}
	aWorld := a.AWorld()
	got, err := a.A()
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	if got != aWorld {
		t.Fatalf("neutral city A = %v, want exactly A_world %v", got, aWorld)
	}
	res, err := a.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: 100, JunctionThroughput: 100})
	if err != nil {
		t.Fatalf("ApplyMigration: %v", err)
	}
	if res.A != aWorld || res.Net != 0 || res.Inflow != 0 || res.Outflow != 0 || res.Reputation != 0 {
		t.Fatalf("neutral city moved: A=%v Net=%v Inflow=%d Outflow=%d Rep=%v", res.A, res.Net, res.Inflow, res.Outflow, res.Reputation)
	}
	for i := 0; i < LeverCount; i++ {
		if res.Levers[i] != (LeverResult{}) {
			t.Fatalf("lever %s not exactly zero/not built: %+v", LeverID(i), res.Levers[i])
		}
	}
	for i := 0; i < ModifierCount; i++ {
		if res.Modifiers[i] != (LeverResult{}) {
			t.Fatalf("modifier %s not exactly zero/not built: %+v", ModifierID(i), res.Modifiers[i])
		}
	}
	if res.ReputationPoints != 0 || res.WellbeingPoints != 0 || res.WellbeingFactor != 1 {
		t.Fatalf("neutral city has non-zero lines: %+v", res)
	}
}

// TestC3_UnbuiltInputCannotMoveMigration: for every lever, modifier and
// reinforcer slot, the same month with (Built=false, Value=0) and
// (Built=false, Value=100) gives an identical MigrationResult, in a city
// where some other lever IS built and moving people.
func TestC3_UnbuiltInputCannotMoveMigration(t *testing.T) {
	run := func(group string, idx int, v float64) MigrationResult {
		a, _, _, _ := newAPI(t, validConfig())
		in := neutralTI(0)
		// A built anchor lever so the month actually admits people.
		anchor := LeverParks
		if group == "lever" && idx == int(LeverParks) {
			anchor = LeverShops
		}
		in.Levers[anchor] = LeverTerm{Value: 90, Built: true}
		switch group {
		case "lever":
			in.Levers[idx] = LeverTerm{Value: v}
		case "modifier":
			in.Modifiers[idx] = LeverTerm{Value: v}
		case "reinforcer":
			in.Reinforcers[idx] = LeverTerm{Value: v}
		}
		if err := a.SetTermInputs(in); err != nil {
			t.Fatalf("SetTermInputs: %v", err)
		}
		res, err := a.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: 100, JunctionThroughput: 100})
		if err != nil {
			t.Fatalf("ApplyMigration: %v", err)
		}
		if res.Inflow <= 0 {
			t.Fatalf("anchor city admitted nobody (Net=%v): the test would prove nothing", res.Net)
		}
		return res
	}
	check := func(group, name string, idx int) {
		lo, hi := run(group, idx, 0), run(group, idx, 100)
		if lo != hi {
			t.Errorf("%s %s: unbuilt value moved the result:\n lo=%+v\n hi=%+v", group, name, lo, hi)
		}
	}
	for i := 0; i < LeverCount; i++ {
		check("lever", LeverID(i).String(), i)
	}
	for i := 0; i < ModifierCount; i++ {
		check("modifier", ModifierID(i).String(), i)
	}
	for i := 0; i < ReinforcerCount; i++ {
		check("reinforcer", ReinforcerID(i).String(), i)
	}
}

// TestC4_IsolationAndWeightPerLever: with everything else fixed and
// reputation anchored at 0, raising one built lever (or modifier) by 10
// moves the score by exactly weight x 10 = 1.0 and moves no other line.
func TestC4_IsolationAndWeightPerLever(t *testing.T) {
	const delta = 10.0
	base := func() TermInputs {
		in := neutralTI(0)
		for i := 0; i < LeverCount; i++ {
			if LeverID(i) == LeverHousing {
				continue // housing is computed inside attract; covered below
			}
			in.Levers[i] = LeverTerm{Value: 50, Built: true}
		}
		for i := 0; i < ModifierCount; i++ {
			in.Modifiers[i] = LeverTerm{Value: 50, Built: true}
		}
		return in
	}
	month := func(in TermInputs) MigrationResult {
		a, _, _, _ := newAPI(t, validConfig())
		if err := a.SetTermInputs(in); err != nil {
			t.Fatalf("SetTermInputs: %v", err)
		}
		res, err := a.ApplyMigration(MigrationCommand{Month: 0})
		if err != nil {
			t.Fatalf("ApplyMigration: %v", err)
		}
		return res
	}
	ref := month(base())
	for i := 0; i < LeverCount+ModifierCount; i++ {
		if i == int(LeverHousing) {
			continue
		}
		in := base()
		var name string
		if i < LeverCount {
			in.Levers[i].Value += delta
			name = LeverID(i).String()
		} else {
			in.Modifiers[i-LeverCount].Value += delta
			name = ModifierID(i - LeverCount).String()
		}
		got := month(in)
		if d := got.A - ref.A; math.Abs(d-1.0) > 1e-9 {
			t.Errorf("%s: score moved by %v, want exactly 1.0 (weight 0.1 x 10)", name, d)
		}
		for j := 0; j < LeverCount; j++ {
			want := ref.Levers[j].Points
			if j == i {
				want += 1.0
			}
			if math.Abs(got.Levers[j].Points-want) > 1e-9 {
				t.Errorf("%s: lever %s points = %v, want %v", name, LeverID(j), got.Levers[j].Points, want)
			}
		}
		for j := 0; j < ModifierCount; j++ {
			want := ref.Modifiers[j].Points
			if i >= LeverCount && j == i-LeverCount {
				want += 1.0
			}
			if math.Abs(got.Modifiers[j].Points-want) > 1e-9 {
				t.Errorf("%s: modifier %s points = %v, want %v", name, ModifierID(j), got.Modifiers[j].Points, want)
			}
		}
		if got.ReputationPoints != ref.ReputationPoints || got.WellbeingPoints != ref.WellbeingPoints {
			t.Errorf("%s: reputation/wellbeing line moved", name)
		}
	}
}

// TestC4_HousingLeverWeight: the housing value is computed inside attract
// (the caller supplies only Built), and its line is weight x (value -
// A_world) at the value attract itself reports.
func TestC4_HousingLeverWeight(t *testing.T) {
	a, ca, h, f := newAPI(t, validConfig())
	hid := partnerCouple(t, ca, mkResident(1, 60), mkResident(2, 60))
	for _, typ := range []string{"terrace", "bungalow"} {
		if err := h.ReportStock(households.StockCommand{TypologyID: typ, Count: 10}); err != nil {
			t.Fatalf("ReportStock: %v", err)
		}
	}
	in := neutralTI(0)
	in.Levers[LeverHousing] = LeverTerm{Value: 7, Built: true} // 7 must be ignored
	in.HouseholdIDs = []uint64{hid}
	in.MonthlyRentMicroPounds = 1000
	if err := a.SetTermInputs(in); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}
	lowV, err := a.HousingAffordability() // zero income: 0
	if err != nil {
		t.Fatalf("HousingAffordability: %v", err)
	}
	lowA, _ := a.A()
	seedTreasury(t, f, 1_000_000)
	if _, err := f.PostWages(10000); err != nil {
		t.Fatalf("PostWages: %v", err)
	}
	highV, _ := a.HousingAffordability()
	highA, _ := a.A()
	if lowV == highV {
		t.Fatalf("test did not move the housing value: %v == %v", lowV, highV)
	}
	if d, want := highA-lowA, 0.1*(highV-lowV); math.Abs(d-want) > 1e-9 {
		t.Fatalf("housing moved the score by %v, want 0.1 x %v = %v", d, highV-lowV, want)
	}
	res, err := a.ApplyMigration(MigrationCommand{Month: 0})
	if err != nil {
		t.Fatalf("ApplyMigration: %v", err)
	}
	hl := res.Levers[LeverHousing]
	if !hl.Built || hl.Value != highV || math.Abs(hl.Points-0.1*(highV-a.AWorld())) > 1e-9 {
		t.Fatalf("housing line = %+v, want built, value %v, points %v", hl, highV, 0.1*(highV-a.AWorld()))
	}
}

// TestC5_IdentityOver200SeededInputs: sum(lever points) + sum(modifier
// points) + reputation points + wellbeing points == Net / migrationRate,
// for 200 seeded random inputs over two months (so reputation is non-zero),
// with a random wellbeing factor and migration rate.
func TestC5_IdentityOver200SeededInputs(t *testing.T) {
	g := &splitmix{s: 20261118}
	rates := [3]float64{0.5, 1, 2}
	for n := 0; n < 200; n++ {
		cfg := validConfig()
		cfg.MigrationRate = rates[n%3]
		a, _, _, _ := newAPI(t, cfg)
		factor := 0.5 + g.unit()
		if err := a.SetWellbeingModifiers(func() (float64, float64) { return factor, 1 }); err != nil {
			t.Fatalf("SetWellbeingModifiers: %v", err)
		}
		for m := int64(0); m < 2; m++ {
			var in TermInputs
			for i := 0; i < LeverCount; i++ {
				in.Levers[i] = LeverTerm{Value: g.unit() * 100, Built: g.next()%2 == 0}
			}
			for i := 0; i < ModifierCount; i++ {
				in.Modifiers[i] = LeverTerm{Value: g.unit() * 100, Built: g.next()%2 == 0}
			}
			if err := a.SetTermInputs(in); err != nil {
				t.Fatalf("SetTermInputs: %v", err)
			}
			res, err := a.ApplyMigration(MigrationCommand{Month: m})
			if err != nil {
				t.Fatalf("ApplyMigration: %v", err)
			}
			var sum float64
			for i := 0; i < LeverCount; i++ {
				sum += res.Levers[i].Points
			}
			for i := 0; i < ModifierCount; i++ {
				sum += res.Modifiers[i].Points
			}
			sum += res.ReputationPoints + res.WellbeingPoints
			if want := res.Net / cfg.MigrationRate; math.Abs(sum-want) > 1e-9 {
				t.Fatalf("input %d month %d: points sum %v != net/rate %v (diff %g)", n, m, sum, want, sum-want)
			}
		}
	}
}

// TestC6_InvalidInputRefusedEvenWhenUnbuilt: NaN, infinities, below 0 and
// above 100 are refused with ErrInvalidTermInput for every lever, modifier
// and reinforcer slot, built or not, and leave the stored inputs unchanged.
func TestC6_InvalidInputRefusedEvenWhenUnbuilt(t *testing.T) {
	bad := []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.0001, 100.0001}
	a, _, _, _ := newAPI(t, validConfig())
	good := uniformTI(60, nil, 0)
	if err := a.SetTermInputs(good); err != nil {
		t.Fatalf("SetTermInputs(good): %v", err)
	}
	before, _ := a.A()
	try := func(name string, mut func(*TermInputs, float64), built bool) {
		for _, v := range bad {
			in := uniformTI(60, nil, 0)
			mut(&in, v)
			_ = built
			err := a.SetTermInputs(in)
			if err == nil {
				t.Errorf("%s value %v accepted", name, v)
				continue
			}
			isErr(t, err, ErrInvalidTermInput)
		}
	}
	for _, built := range []bool{false, true} {
		for i := 0; i < LeverCount; i++ {
			i, built := i, built
			try("lever "+LeverID(i).String(), func(in *TermInputs, v float64) { in.Levers[i] = LeverTerm{Value: v, Built: built} }, built)
		}
		for i := 0; i < ModifierCount; i++ {
			i, built := i, built
			try("modifier "+ModifierID(i).String(), func(in *TermInputs, v float64) { in.Modifiers[i] = LeverTerm{Value: v, Built: built} }, built)
		}
		for i := 0; i < ReinforcerCount; i++ {
			i, built := i, built
			try("reinforcer "+ReinforcerID(i).String(), func(in *TermInputs, v float64) { in.Reinforcers[i] = LeverTerm{Value: v, Built: built} }, built)
		}
	}
	after, _ := a.A()
	if before != after {
		t.Fatalf("a refused input changed state: A %v -> %v", before, after)
	}
}

// TestC7_NeutralCityKeepsReputationAtRest: 36 months with every lever
// unbuilt leave reputation exactly 0.
func TestC7_NeutralCityKeepsReputationAtRest(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())
	for m := int64(0); m < 36; m++ {
		if err := a.SetTermInputs(neutralTI(float64(m % 101))); err != nil {
			t.Fatalf("SetTermInputs: %v", err)
		}
		res, err := a.ApplyMigration(MigrationCommand{Month: m})
		if err != nil {
			t.Fatalf("ApplyMigration: %v", err)
		}
		if res.Reputation != 0 || res.ReputationPoints != 0 || a.Reputation() != 0 {
			t.Fatalf("month %d: reputation %v / points %v, want exactly 0", m, res.Reputation, res.ReputationPoints)
		}
	}
}

// TestC7_ReputationUsesMeanOfTenEffectiveValues: one lever going from not
// built to 100 moves the momentum's target by (100 - A_world)/10, because
// the other nine count as A_world; the mean of built levers only would move
// it ten times as far.
func TestC7_ReputationUsesMeanOfTenEffectiveValues(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())
	if err := a.SetTermInputs(neutralTI(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyMigration(MigrationCommand{Month: 0}); err != nil {
		t.Fatal(err)
	}
	in := neutralTI(0)
	in.Levers[LeverShops] = LeverTerm{Value: 100, Built: true}
	if err := a.SetTermInputs(in); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyMigration(MigrationCommand{Month: 1}); err != nil {
		t.Fatal(err)
	}
	// dev = (100-50)/10 = 5; rise rate 0.2 from 0 -> 1.0.
	if got := a.Reputation(); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("reputation after one built lever at 100 = %v, want 1.0", got)
	}
}

// TestC8_CapWantedRoomLimitedBy: Wanted is the whole-person net before the
// cap, Room is min(throughput, vacancy x 2), LimitedBy names the binding
// cap (homes on a tie), and Inflow is min(Wanted, Room) in whole households.
func TestC8_CapWantedRoomLimitedBy(t *testing.T) {
	cases := []struct {
		name                 string
		vacancy, throughput  int64
		wantRoom, wantInflow int64
		wantLimit            string
	}{
		{"homes bind", 3, 100, 6, 6, LimitHomes},
		{"transport binds", 100, 10, 10, 10, LimitTransport},
		{"tie goes to homes", 5, 10, 10, 10, LimitHomes},
		{"no vacancy", 0, 100, 0, 0, LimitHomes},
		{"wanted binds, whole households", 60, 200, 120, -1, LimitHomes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, ca, _, _ := newAPI(t, validConfig())
			in := neutralTI(0)
			for i := 0; i < LeverCount; i++ {
				if LeverID(i) != LeverHousing {
					in.Levers[i] = LeverTerm{Value: 100, Built: true}
				}
			}
			if err := a.SetTermInputs(in); err != nil {
				t.Fatal(err)
			}
			res, err := a.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: tc.vacancy, JunctionThroughput: tc.throughput})
			if err != nil {
				t.Fatal(err)
			}
			if want := int64(res.Net); res.Wanted != want || res.Wanted <= 0 {
				t.Fatalf("Wanted = %d, want %d (positive, whole-person net before the cap)", res.Wanted, want)
			}
			if res.Room != tc.wantRoom || res.LimitedBy != tc.wantLimit {
				t.Fatalf("Room=%d LimitedBy=%q, want %d %q", res.Room, res.LimitedBy, tc.wantRoom, tc.wantLimit)
			}
			wantInflow := tc.wantInflow
			if wantInflow < 0 { // min(Wanted, Room) rounded down to households of 2
				wantInflow = (minI64(res.Wanted, res.Room) / 2) * 2
			}
			if res.Inflow != wantInflow {
				t.Fatalf("Inflow = %d, want %d", res.Inflow, wantInflow)
			}
			if got := int64(ca.TotalPopulation("corr-attract")); got != wantInflow {
				t.Fatalf("population %d != Inflow %d", got, wantInflow)
			}
		})
	}
	// Negative wanted: people want to leave; Room is still reported.
	a, _, _, _ := newAPI(t, validConfig())
	in := neutralTI(0)
	in.Levers[LeverJobs] = LeverTerm{Value: 0, Built: true}
	if err := a.SetTermInputs(in); err != nil {
		t.Fatal(err)
	}
	res, err := a.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: 4, JunctionThroughput: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Wanted >= 0 || res.Inflow != 0 || res.Room != 8 {
		t.Fatalf("leaving month: Wanted=%d Inflow=%d Room=%d, want Wanted<0 Inflow=0 Room=8", res.Wanted, res.Inflow, res.Room)
	}
}

// TestC9_DeterminismOver24Months: two APIs with the same config and seed and
// the same inputs for 24 months give byte-identical serialised results every
// month, and the run is not trivially static.
func TestC9_DeterminismOver24Months(t *testing.T) {
	run := func() [][]byte {
		a, _, _, _ := newAPI(t, validConfig())
		var out [][]byte
		for m := int64(0); m < 24; m++ {
			g := &splitmix{s: uint64(m) + 1}
			in := neutralTI(0)
			for i := 0; i < LeverCount; i++ {
				if LeverID(i) == LeverHousing {
					continue
				}
				in.Levers[i] = LeverTerm{Value: 30 + 60*g.unit(), Built: g.next()%3 != 0}
			}
			for i := 0; i < ModifierCount; i++ {
				in.Modifiers[i] = LeverTerm{Value: 100 * g.unit(), Built: true}
			}
			if err := a.SetTermInputs(in); err != nil {
				t.Fatal(err)
			}
			res, err := a.ApplyMigration(MigrationCommand{Month: m, HousingVacancy: 20, JunctionThroughput: 20})
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, b)
		}
		return out
	}
	r1, r2 := run(), run()
	distinct := map[string]bool{}
	for m := range r1 {
		if !bytes.Equal(r1[m], r2[m]) {
			t.Fatalf("month %d differs between two identical runs:\n%s\n%s", m, r1[m], r2[m])
		}
		distinct[string(r1[m])] = true
	}
	if len(distinct) < 12 {
		t.Fatalf("only %d distinct months in 24: the run is too static to prove determinism", len(distinct))
	}
}

// TestC19_OldSaveMarkerReanchorsReputationOnce: a record with no leverFormula
// marker (written under the seven-term formula) re-anchors reputation on
// load (no baseline, value 0) and the next month shows no jump; a record
// WITH the marker keeps its reputation exactly.
func TestC19_OldSaveMarkerReanchorsReputationOnce(t *testing.T) {
	load := func(raw string) *AttractAPI {
		a, _, _, _ := newAPI(t, validConfig())
		if err := NewSaveParticipant(a).Handler()(serialize.Record{Kind: recAttractMeta, Data: []byte(raw)}); err != nil {
			t.Fatalf("Handler: %v", err)
		}
		return a
	}
	old := load(`{"reputation":{"hasBaseline":true,"baseline":5,"value":6},"lastAdvancedMonth":9,"hasAdvanced":true,"nextMigrantID":0,"migrantAdmittedMonths":[]}`)
	old.mu.RLock()
	oldRep := old.reputation
	old.mu.RUnlock()
	if oldRep != (reputationState{}) {
		t.Fatalf("old record's reputation = %+v, want re-anchored zero state", oldRep)
	}
	if err := old.SetTermInputs(neutralTI(0)); err != nil {
		t.Fatal(err)
	}
	res, err := old.ApplyMigration(MigrationCommand{Month: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reputation != 0 || res.Net != 0 {
		t.Fatalf("re-anchored city jumped on its first month: reputation %v net %v", res.Reputation, res.Net)
	}

	modern := load(`{"reputation":{"hasBaseline":true,"baseline":5,"value":6},"lastAdvancedMonth":9,"hasAdvanced":true,"nextMigrantID":0,"migrantAdmittedMonths":[],"leverFormula":1}`)
	modern.mu.RLock()
	modernRep := modern.reputation
	modern.mu.RUnlock()
	if modernRep != (reputationState{hasBaseline: true, baseline: 5, value: 6}) {
		t.Fatalf("modern record's reputation = %+v, want it kept exactly", modernRep)
	}
}
