package worklife

import "testing"

// effect builds a WorkingWeekEffect from a loaded policy def, so the test's
// 996 figures are read from data/worklife.json, never hardcoded (AC-19).
func effect(p WorkingWeekPolicyDef) WorkingWeekEffect {
	return WorkingWeekEffect{
		HoursPerWeek:    p.HoursPerWeek,
		WageCoefficient: p.WageCoefficient,
		WellbeingWeight: p.WellbeingWeight,
	}
}

// AC-2: each pattern's hours and on-duty profile are loaded from
// data/worklife.json — mutating one pattern's hours in the loaded fixture
// changes the at-work profile, so the hours are never Go literals.
func TestAtWork_PatternsDataDriven(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	core := patternDef(t, f, PatternCoreHours)

	worker := Worker{ID: 1, Pattern: PatternCoreHours}
	atStart, err := api.AtWork(worker, core.StartHour) // Monday, at the data-driven start hour
	if err != nil {
		t.Fatalf("AtWork: %v", err)
	}
	if !atStart {
		t.Fatalf("expected at-work at data-driven core start hour %d", core.StartHour)
	}

	// Mutate the loaded fixture: shift core-hours start later by one hour.
	mutated := f
	for i := range mutated.Patterns {
		if PatternKind(mutated.Patterns[i].ID) == PatternCoreHours {
			mutated.Patterns[i].StartHour++
		}
	}
	mutAPI := newTestAPI(t, mutated)

	if at, _ := mutAPI.AtWork(worker, core.StartHour); at {
		t.Fatalf("after mutating startHour +1, old start hour %d should be off-duty", core.StartHour)
	}
	if at, _ := mutAPI.AtWork(worker, core.StartHour+1); !at {
		t.Fatalf("after mutating startHour +1, new start hour %d should be on-duty", core.StartHour+1)
	}
}

// AC-3 (core claim): a 24x7 Shift role's required headcount for the same
// per-hour on-duty demand strictly exceeds a CoreHours role's, because shift
// coverage spans all 24 hours while core-hours spans only the working day.
func TestCoverageRequirement_ScheduleNotHeadcount(t *testing.T) {
	api := newTestAPI(t, loadTestFile(t))
	const perHourOnDuty = 5

	core, err := api.CoverageRequirement(PatternCoreHours, perHourOnDuty)
	if err != nil {
		t.Fatalf("core CoverageRequirement: %v", err)
	}
	shift, err := api.CoverageRequirement(PatternShift, perHourOnDuty)
	if err != nil {
		t.Fatalf("shift CoverageRequirement: %v", err)
	}
	if shift <= core {
		t.Fatalf("shift headcount %d must strictly exceed core headcount %d for the same per-hour demand %d",
			shift, core, perHourOnDuty)
	}
}

// AC-4: AtWork is a deterministic function of pattern and tick — a
// CoreHours worker's on/off boundaries over a full week match the
// documented data-driven window exactly.
func TestAtWork_CoreHoursBoundaries(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	core := patternDef(t, f, PatternCoreHours)
	worker := Worker{ID: 7, Pattern: PatternCoreHours}

	for tick := int64(0); tick < 168; tick++ {
		at, err := api.AtWork(worker, tick)
		if err != nil {
			t.Fatalf("AtWork(%d): %v", tick, err)
		}
		h := tick % 24
		d := (tick / 24) % 7
		want := d < core.DaysPerWeek && h >= core.StartHour && h < core.EndHour
		if at != want {
			t.Fatalf("tick %d (hour %d day %d): at-work=%v, want %v", tick, h, d, at, want)
		}
	}
}

// AC-4: a full Shift roster leaves no hour unstaffed (the rotations tile
// the day), and a single shift worker is at-work on exactly one rotation.
func TestShiftRoster_NoUnstaffedHour(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	shift := patternDef(t, f, PatternShift)

	// The rotations' windows must tile [0, 24) with no gap — so a full
	// roster (one worker per rotation) leaves no hour unstaffed.
	var covered [24]bool
	for _, r := range shift.Rotations {
		for h := r.StartHour; h < r.EndHour; h++ {
			covered[h] = true
		}
	}
	for h := int64(0); h < 24; h++ {
		if !covered[h] {
			t.Fatalf("hour %d is not covered by any shift rotation (full roster would leave it unstaffed)", h)
		}
	}

	// Each shift worker is at-work on exactly one rotation's window.
	for _, worker := range []Worker{{ID: 1, Pattern: PatternShift}, {ID: 2, Pattern: PatternShift}, {ID: 3, Pattern: PatternShift}} {
		on := 0
		for _, r := range shift.Rotations {
			mid := r.StartHour + (r.EndHour-r.StartHour)/2 // Monday, mid-rotation
			at, err := api.AtWork(worker, mid)
			if err != nil {
				t.Fatalf("AtWork(%d, %d): %v", worker.ID, mid, err)
			}
			if at {
				on++
			}
		}
		if on != 1 {
			t.Fatalf("worker %d at-work on %d rotations, want exactly 1", worker.ID, on)
		}
	}
}

// AC-6: every worker commutes aligned to their pattern's start/end — the
// per-hour arrival distribution's mode sits at core-hours start, and shift
// workers spread their arrivals across the day (no flat daily average).
func TestCommuteDemand_Rush(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	core := patternDef(t, f, PatternCoreHours)

	var workers []Worker
	for i := 0; i < 40; i++ {
		workers = append(workers, Worker{ID: uint64(1000 + i), Pattern: PatternCoreHours})
	}
	for i := 0; i < 30; i++ {
		workers = append(workers, Worker{ID: uint64(2000 + i), Pattern: PatternShift})
	}

	demand, err := api.CommuteDemandByHour(workers, 0, 24) // Monday only
	if err != nil {
		t.Fatalf("CommuteDemandByHour: %v", err)
	}

	modeHour, modeCount := int64(-1), int64(-1)
	distinctArrivalHours := map[int64]bool{}
	for _, d := range demand {
		if d.Arrivals > modeCount {
			modeCount = d.Arrivals
			modeHour = d.Hour
		}
		if d.Arrivals > 0 {
			distinctArrivalHours[d.Hour] = true
		}
	}
	if modeHour != core.StartHour {
		t.Fatalf("arrival mode hour = %d, want core-hours start %d", modeHour, core.StartHour)
	}
	if len(distinctArrivalHours) < 2 {
		t.Fatalf("expected arrivals to spread across >1 hour (shift workers spread the rush), got %d distinct arrival hours", len(distinctArrivalHours))
	}
}

// AC-8: the working week is consumed from the PoliciesAPI seam, never a
// worklife-local hour table — enacting the policy via the seam changes the
// computed hours.
func TestWorkingHours_PolicyConsumed(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	policy := policyDef(t, f, "996")
	core := patternDef(t, f, PatternCoreHours)
	p := &fakePolicies{}
	if err := api.SetPolicies(p); err != nil {
		t.Fatalf("SetPolicies: %v", err)
	}

	def, err := api.WorkingHours(PatternCoreHours, "corr")
	if err != nil {
		t.Fatalf("default WorkingHours: %v", err)
	}
	if def != core.HoursPerDay*core.DaysPerWeek {
		t.Fatalf("default working hours = %d, want data-driven %d", def, core.HoursPerDay*core.DaysPerWeek)
	}

	p.set(effect(policy), true)
	got, err := api.WorkingHours(PatternCoreHours, "corr")
	if err != nil {
		t.Fatalf("996 WorkingHours: %v", err)
	}
	if got != policy.HoursPerWeek {
		t.Fatalf("996 working hours = %d, want policy hours %d", got, policy.HoursPerWeek)
	}
}

// AC-9: the default working week sums to ~40h (data-driven hoursPerDay ×
// daysPerWeek) and the 996 policy is strictly more hours.
func TestWorkingWeekHours_DefaultVs996(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	policy := policyDef(t, f, "996")
	core := patternDef(t, f, PatternCoreHours)
	p := &fakePolicies{}
	if err := api.SetPolicies(p); err != nil {
		t.Fatalf("SetPolicies: %v", err)
	}

	def, err := api.WorkingHours(PatternCoreHours, "corr")
	if err != nil {
		t.Fatalf("default WorkingHours: %v", err)
	}
	// The ~40h default shape is the data file's own hoursPerDay×daysPerWeek
	// (8×5), read here — not a pinned figure invented by the test.
	if def != core.HoursPerDay*core.DaysPerWeek {
		t.Fatalf("default = %d, want %d×%d = %d (data-driven)", def, core.HoursPerDay, core.DaysPerWeek, core.HoursPerDay*core.DaysPerWeek)
	}

	p.set(effect(policy), true)
	got, err := api.WorkingHours(PatternCoreHours, "corr")
	if err != nil {
		t.Fatalf("996 WorkingHours: %v", err)
	}
	if got <= def {
		t.Fatalf("996 hours %d must strictly exceed default %d", got, def)
	}
}

// AC-10: toggling the working-week policy off reverts hours/wage/wellbeing
// to the default week, and toggling it back on re-applies 996 — no residue.
func TestWorkingWeekToggle_ReversibleClean(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	policy := policyDef(t, f, "996")
	p := &fakePolicies{}
	wb := &fakeWellbeing{}
	if err := api.SetPolicies(p); err != nil {
		t.Fatalf("SetPolicies: %v", err)
	}
	if err := api.SetWellbeing(wb); err != nil {
		t.Fatalf("SetWellbeing: %v", err)
	}
	const base = int64(1_000_000)
	worker := Worker{ID: 42, Pattern: PatternCoreHours}

	defHours, _ := api.WorkingHours(PatternCoreHours, "corr")
	defWage, _ := api.WeeklyWage(base, "corr")
	defBal, _ := api.OverworkWellbeingInput(worker, "corr")

	// Enact 996.
	p.set(effect(policy), true)
	onHours, _ := api.WorkingHours(PatternCoreHours, "corr")
	onWage, _ := api.WeeklyWage(base, "corr")
	onBal, _ := api.OverworkWellbeingInput(worker, "corr")
	if onHours <= defHours || onWage <= defWage || onBal >= defBal {
		t.Fatalf("996 must raise hours/wage and lower balance: hours %d>%d wage %d>%d bal %v<%v",
			onHours, defHours, onWage, defWage, onBal, defBal)
	}

	// Repeal -> default values restored (no residue).
	p.set(WorkingWeekEffect{}, false)
	offHours, _ := api.WorkingHours(PatternCoreHours, "corr")
	offWage, _ := api.WeeklyWage(base, "corr")
	offBal, _ := api.OverworkWellbeingInput(worker, "corr")
	if offHours != defHours || offWage != defWage || offBal != defBal {
		t.Fatalf("after repeal: hours %d!=%d wage %d!=%d bal %v!=%v (residue)",
			offHours, defHours, offWage, defWage, offBal, defBal)
	}

	// Re-enact -> 996 again.
	p.set(effect(policy), true)
	againHours, _ := api.WorkingHours(PatternCoreHours, "corr")
	againWage, _ := api.WeeklyWage(base, "corr")
	againBal, _ := api.OverworkWellbeingInput(worker, "corr")
	if againHours != onHours || againWage != onWage || againBal != onBal {
		t.Fatalf("re-enact did not reproduce 996: hours %d!=%d wage %d!=%d bal %v!=%v",
			againHours, onHours, againWage, onWage, againBal, onBal)
	}
}

// AC-11: a worker under 996 has a strictly higher wage than under the
// default week, via the policy's data-driven wage coefficient.
func TestWage996_EarnsMore(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	policy := policyDef(t, f, "996")
	p := &fakePolicies{}
	if err := api.SetPolicies(p); err != nil {
		t.Fatalf("SetPolicies: %v", err)
	}
	const base = int64(1_000_000)

	defWage, err := api.WeeklyWage(base, "corr")
	if err != nil {
		t.Fatalf("default WeeklyWage: %v", err)
	}
	p.set(effect(policy), true)
	wage, err := api.WeeklyWage(base, "corr")
	if err != nil {
		t.Fatalf("996 WeeklyWage: %v", err)
	}
	if wage <= defWage {
		t.Fatalf("996 wage %d must strictly exceed default wage %d", wage, defWage)
	}
}

// AC-12: a 996 worker and a default-week worker (identical otherwise) fed
// through the same path yield a strictly lower wellbeing input for the 996
// worker, and the input is pushed through the WellbeingAPI seam.
func TestWellbeing996_LessHappy(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	policy := policyDef(t, f, "996")
	p := &fakePolicies{}
	wb := &fakeWellbeing{}
	if err := api.SetPolicies(p); err != nil {
		t.Fatalf("SetPolicies: %v", err)
	}
	if err := api.SetWellbeing(wb); err != nil {
		t.Fatalf("SetWellbeing: %v", err)
	}
	worker := Worker{ID: 42, Pattern: PatternCoreHours}

	defBal, err := api.OverworkWellbeingInput(worker, "corr")
	if err != nil {
		t.Fatalf("default OverworkWellbeingInput: %v", err)
	}
	p.set(effect(policy), true)
	bal, err := api.OverworkWellbeingInput(worker, "corr")
	if err != nil {
		t.Fatalf("996 OverworkWellbeingInput: %v", err)
	}
	if bal >= defBal {
		t.Fatalf("996 wellbeing input %v must be strictly lower than default %v", bal, defBal)
	}
	// The value was actually pushed through the seam, not just returned.
	if wb.balance(worker.ID) != bal {
		t.Fatalf("balance %v not pushed through the WellbeingAPI seam (recorded %v)", bal, wb.balance(worker.ID))
	}
}

// AC-13: the overwork trade is genuine — both the wage gain and the
// wellbeing cost fire from the same policy effect, so there is no
// configuration with a wage gain and zero wellbeing cost.
func TestOverworkTrade_NoFreeProductivity(t *testing.T) {
	f := loadTestFile(t)
	api := newTestAPI(t, f)
	policy := policyDef(t, f, "996")
	p := &fakePolicies{}
	wb := &fakeWellbeing{}
	if err := api.SetPolicies(p); err != nil {
		t.Fatalf("SetPolicies: %v", err)
	}
	if err := api.SetWellbeing(wb); err != nil {
		t.Fatalf("SetWellbeing: %v", err)
	}
	const base = int64(1_000_000)
	worker := Worker{ID: 7, Pattern: PatternCoreHours}

	defWage, _ := api.WeeklyWage(base, "corr")
	defBal, _ := api.OverworkWellbeingInput(worker, "corr")

	p.set(effect(policy), true)
	wage, _ := api.WeeklyWage(base, "corr")
	bal, _ := api.OverworkWellbeingInput(worker, "corr")

	wageGain := wage - defWage
	wellbeingCost := defBal - bal
	if wageGain <= 0 {
		t.Fatalf("expected a positive wage gain under 996, got %d", wageGain)
	}
	if wellbeingCost <= 0 {
		t.Fatalf("expected a positive wellbeing cost under 996, got %v", wellbeingCost)
	}
	// The two are inseparable: a policy that raises the wage (coefficient
	// > 1) must also carry the hours increase that lowers the balance, and
	// the data declares a non-zero overwork weight whenever it declares a
	// wage gain.
	if policy.WageCoefficient > 1 && policy.WellbeingWeight <= 0 {
		t.Fatalf("data inconsistency: 996 wage gain (coefficient %v) with a non-positive wellbeing weight %v is not representable",
			policy.WageCoefficient, policy.WellbeingWeight)
	}
}

// AC-14 (GR#7): an unknown pattern kind returns a registry-sourced
// ErrUnknownPattern and mutates no schedule/hours state.
func TestUnknownPattern_RegistryError(t *testing.T) {
	api := newTestAPI(t, loadTestFile(t))

	_, err := api.AtWork(Worker{ID: 1, Pattern: "night-owl"}, 0)
	if err == nil {
		t.Fatal("want error for unknown pattern kind")
	}
	assertCode(t, err, ErrUnknownPattern)

	// No schedule/hours state mutated on failure: a valid query still works.
	if _, err := api.CoverageRequirement(PatternCoreHours, 5); err != nil {
		t.Fatalf("valid query after unknown-pattern failure returned an error: %v", err)
	}
}

// AC-14 (GR#7): an out-of-domain hours input (negative, or > 24h/day) is
// rejected with a registry-sourced ErrInvalidHours, never clamped.
func TestHoursOutOfRange_RegistryError(t *testing.T) {
	api := newTestAPI(t, loadTestFile(t))

	if _, err := api.CoverageRequirement(PatternCoreHours, -1); err == nil {
		t.Fatal("want error for negative per-hour demand")
	} else {
		assertCode(t, err, ErrInvalidHours)
	}

	// > 24h/day equivalent: a policy claiming more than a week's worth of
	// hours is out of domain.
	p := &fakePolicies{}
	if err := api.SetPolicies(p); err != nil {
		t.Fatalf("SetPolicies: %v", err)
	}
	p.set(WorkingWeekEffect{HoursPerWeek: 200, WageCoefficient: 1, WellbeingWeight: 0}, true)
	if _, err := api.WorkingHours(PatternCoreHours, "corr"); err == nil {
		t.Fatal("want error for >24h/day (200h/week) policy hours")
	} else {
		assertCode(t, err, ErrInvalidHours)
	}
}

// AC-15 (GR#7): malformed data/worklife.json (a pattern missing its hours,
// a negative shift length, an unrecognised kind) produces a load-time
// registry-sourced ErrDataInvalid, never a silent default substitution.
func TestMalformedWorklifeData_RegistryError(t *testing.T) {
	// A pattern missing its hours (zero hoursPerDay).
	bad := WorklifeFile{
		Version: 1,
		Patterns: []PatternDef{
			{ID: "core-hours", HoursPerDay: 0, DaysPerWeek: 5, CoverageSpanHours: 8, StartHour: 9, EndHour: 17, Disclosure: "x"},
			{ID: "shift", HoursPerDay: 8, DaysPerWeek: 5, CoverageSpanHours: 24, Rotations: []RotationDef{{0, 8}, {8, 16}, {16, 24}}, Disclosure: "x"},
			{ID: "any-time", HoursPerDay: 8, DaysPerWeek: 5, CoverageSpanHours: 24, Disclosure: "x"},
		},
	}
	if _, err := New(bad, 1, "corr"); err == nil {
		t.Fatal("want load-time error for a pattern missing its hours")
	} else {
		assertCode(t, err, ErrDataInvalid)
	}

	// A negative shift-rotation length.
	bad2 := loadTestFile(t)
	for i := range bad2.Patterns {
		if PatternKind(bad2.Patterns[i].ID) == PatternShift {
			bad2.Patterns[i].Rotations[0].EndHour = bad2.Patterns[i].Rotations[0].StartHour - 1
		}
	}
	if _, err := New(bad2, 1, "corr"); err == nil {
		t.Fatal("want load-time error for a negative shift length")
	} else {
		assertCode(t, err, ErrDataInvalid)
	}

	// An unrecognised kind string.
	bad3 := loadTestFile(t)
	bad3.Patterns = append(bad3.Patterns, PatternDef{ID: "bogus", HoursPerDay: 8, DaysPerWeek: 5, CoverageSpanHours: 8, Disclosure: "x"})
	if _, err := New(bad3, 1, "corr"); err == nil {
		t.Fatal("want load-time error for an unrecognised kind string")
	} else {
		assertCode(t, err, ErrDataInvalid)
	}
}
