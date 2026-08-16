package spaceport

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// testConfig returns a Config whose magnitudes are small so the mechanisms
// are fast to exercise (GR#15's test-fixture latitude — the real magnitudes
// live in data/spaceport.json and are placeholders; a unit test may inject a
// fixture).
func testConfig() Config {
	return Config{
		CatalogueAnchor:         "space_launch_complex",
		BlightClass:             "medium",
		BuildMonths:             5,
		LaunchCadenceMonths:     2,
		ExportPerLaunch:         1000,
		PrestigePerLaunch:       100,
		FdiDrawAmount:           50,
		TourismDrawAmount:       200,
		ExclusionRadius:         3,
		ExclusionFactorPerMille: 700,
		ExpertThreshold:         100,
	}
}

// fakeEducation is a test double for the shared expert gate seam. Its
// research output is settable so a test can flip the threshold verdict.
type fakeEducation struct {
	mu sync.Mutex
	rp int64
}

func (f *fakeEducation) ResearchPoints() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rp
}

func (f *fakeEducation) set(v int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rp = v
}

// fakePermit is a test double for the §7 permit seam.
type fakePermit struct {
	mu   sync.Mutex
	held bool
}

func (f *fakePermit) PermitHeld(string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held, nil
}

// fakeDecommission is a test double for the §7 decommission seam; it records
// which facility keys accrued a liability.
type fakeDecommission struct {
	mu      sync.Mutex
	accrued []string
}

func (f *fakeDecommission) Accrue(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accrued = append(f.accrued, key)
	return nil
}

// fakeFdi is a test double for the engine.fdi demand-injection seam.
type fakeFdi struct {
	mu    sync.Mutex
	total int64
}

func (f *fakeFdi) AddProspectDemand(amount int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.total += amount
	return nil
}

func (f *fakeFdi) Total() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total
}

// fakeTourism is a test double for the engine.tourism demand-injection seam.
type fakeTourism struct {
	mu    sync.Mutex
	total int64
}

func (f *fakeTourism) AddVisitorDraw(amount int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.total += amount
	return nil
}

func (f *fakeTourism) Total() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total
}

// newSpaceport constructs a SpaceportAPI over testConfig and wires every
// seam with a fake: the education gate at gateRP, the permit held per
// permitHeld, and recording decommission/FDI/tourism doubles.
func newSpaceport(t *testing.T, gateRP int64, permitHeld bool) (*SpaceportAPI, *fakeEducation, *fakePermit, *fakeDecommission, *fakeFdi, *fakeTourism) {
	t.Helper()
	a, err := New(testConfig(), 42, "test")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	edu := &fakeEducation{rp: gateRP}
	perm := &fakePermit{held: permitHeld}
	dec := &fakeDecommission{}
	fdi := &fakeFdi{}
	tou := &fakeTourism{}
	if err := a.SetEducationGate(edu); err != nil {
		t.Fatalf("set education: %v", err)
	}
	if err := a.SetPermitGate(perm); err != nil {
		t.Fatalf("set permit: %v", err)
	}
	if err := a.SetDecommissionLiability(dec); err != nil {
		t.Fatalf("set decommission: %v", err)
	}
	if err := a.SetFdiDraw(fdi); err != nil {
		t.Fatalf("set fdi: %v", err)
	}
	if err := a.SetTourismDraw(tou); err != nil {
		t.Fatalf("set tourism: %v", err)
	}
	return a, edu, perm, dec, fdi, tou
}

// buildComplete starts the build and advances the documented build duration
// one month per Tick (AC-4: never completable by a single command).
func buildComplete(t *testing.T, a *SpaceportAPI) {
	t.Helper()
	if err := a.StartBuild(BuildCommand{FacilityKey: "space_launch_complex", SiteX: 0, SiteY: 0}); err != nil {
		t.Fatalf("start build: %v", err)
	}
	_, total := a.BuildProgress()
	for m := int64(1); m <= total; m++ {
		if err := a.Tick(m); err != nil {
			t.Fatalf("tick %d: %v", m, err)
		}
	}
	if !a.IsBuilt() {
		t.Fatal("expected built after buildMonths ticks")
	}
}

// assertCode asserts err is a registry *errs.E carrying the wanted code.
func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != want {
		t.Fatalf("expected code %s, got %s", want, e.Code)
	}
}

// assertNoPartialState asserts a rejected build left no facility record, no
// launch, and no export/prestige credit (AC-10).
func assertNoPartialState(t *testing.T, a *SpaceportAPI) {
	t.Helper()
	rem, total := a.BuildProgress()
	if a.IsBuilt() || len(a.Launches()) != 0 || a.Prestige() != 0 || a.ExportTotal() != 0 || rem != 0 || total != 0 {
		t.Fatal("rejection left partial state")
	}
}

// AC-1: the spaceport resolves to exactly one existing catalogue entry
// (shape (a) — the spaceport IS space_launch_complex, enriched in place).
func TestCatalogueReconciliation(t *testing.T) {
	a, err := LoadDefault("test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if a.Anchor() != "space_launch_complex" {
		t.Fatalf("anchor = %q, want space_launch_complex", a.Anchor())
	}
	if a.BlightClass() != "medium" {
		t.Fatalf("blightClass = %q, want medium", a.BlightClass())
	}

	// Independently verify exactly one buildings.json entry resolves (GR#3:
	// no silent second launch site).
	dir, err := data.ResolveDataDir("test")
	if err != nil {
		t.Fatalf("resolve data dir: %v", err)
	}
	b, err := data.LoadBuildings(dir, "test")
	if err != nil {
		t.Fatalf("load buildings: %v", err)
	}
	count := 0
	var anchor data.BuildingEntry
	for _, e := range b.Entries {
		if e.ID == "space_launch_complex" {
			count++
			anchor = e
		}
	}
	if count != 1 {
		t.Fatalf("space_launch_complex resolves to %d entries, want exactly 1", count)
	}
	if anchor.Unlock.Milestone != "M11" || anchor.CostRaw != "3B" {
		t.Fatalf("anchor is not the existing M11/3B entry: milestone=%q cost=%q", anchor.Unlock.Milestone, anchor.CostRaw)
	}
}

// AC-3: the expert gate is the "money alone cannot buy it" mechanic — ample
// money/DP/milestone does not substitute; raising ONLY the education output
// flips the verdict. (This package models no funds/milestone gate at all, so
// the education output is the only build blocker and the flip is mechanical.)
func TestExpertGateMoneyCannotBuyThresholdFlips(t *testing.T) {
	cfg := testConfig()
	a, edu, _, _, _, _ := newSpaceport(t, cfg.ExpertThreshold-1, true)

	cmd := BuildCommand{FacilityKey: "space_launch_complex", SiteX: 0, SiteY: 0}
	err := a.StartBuild(cmd)
	assertCode(t, err, ErrExpertGateUnmet)
	assertNoPartialState(t, a)

	// Raise ONLY the education output above the threshold; everything else
	// unchanged. The verdict flips to accepted.
	edu.set(cfg.ExpertThreshold)
	if err := a.StartBuild(cmd); err != nil {
		t.Fatalf("start build after raising education output: %v", err)
	}
	rem, total := a.BuildProgress()
	if rem == 0 && total == 0 {
		// StartBuild succeeded: the facility is now sited/building, so
		// BuildProgress is non-zero — proving the gate flipped.
		t.Fatal("start build reported success but left no build in flight")
	}
}

// AC-4: multi-year build, zero launches before completion, a deterministic
// cadence-spaced schedule, and per-launch export/prestige.
func TestMultiYearBuildLaunchScheduleExport(t *testing.T) {
	cfg := testConfig()
	a, _, _, _, _, _ := newSpaceport(t, cfg.ExpertThreshold, true)

	if err := a.StartBuild(BuildCommand{FacilityKey: "space_launch_complex", SiteX: 0, SiteY: 0}); err != nil {
		t.Fatalf("start build: %v", err)
	}
	// (a) zero launch events before completion, no export/prestige credited.
	for m := int64(1); m < cfg.BuildMonths; m++ {
		if err := a.Tick(m); err != nil {
			t.Fatalf("tick %d: %v", m, err)
		}
		if len(a.Launches()) != 0 || a.Prestige() != 0 || a.ExportTotal() != 0 {
			t.Fatalf("launch/export/prestige produced before completion at month %d", m)
		}
	}
	// Complete the build (one month per Tick — not a single command).
	if err := a.Tick(cfg.BuildMonths); err != nil {
		t.Fatalf("complete build: %v", err)
	}
	if !a.IsBuilt() {
		t.Fatal("not built after buildMonths ticks")
	}

	// (b) a deterministic launch schedule matching the data-file cadence.
	sched, err := a.LaunchSchedule(4)
	if err != nil {
		t.Fatalf("launch schedule: %v", err)
	}
	if len(sched) != 4 {
		t.Fatalf("schedule length = %d, want 4", len(sched))
	}
	for i := 1; i < len(sched); i++ {
		if gap := sched[i].Month - sched[i-1].Month; gap != cfg.LaunchCadenceMonths {
			t.Fatalf("schedule spacing = %d, want cadence %d", gap, cfg.LaunchCadenceMonths)
		}
	}

	// (c) each fired launch increments export/prestige by the documented
	// per-launch amount.
	for i := int64(0); i < cfg.LaunchCadenceMonths*3; i++ {
		if err := a.Tick(cfg.BuildMonths + i + 1); err != nil {
			t.Fatalf("tick post-build %d: %v", i, err)
		}
	}
	launches := a.Launches()
	if len(launches) != 3 {
		t.Fatalf("launches fired = %d, want 3", len(launches))
	}
	if got := a.ExportTotal(); got != cfg.ExportPerLaunch*3 {
		t.Fatalf("export total = %d, want %d", got, cfg.ExportPerLaunch*3)
	}
	if got := a.Prestige(); got != cfg.PrestigePerLaunch*3 {
		t.Fatalf("prestige = %d, want %d", got, cfg.PrestigePerLaunch*3)
	}
	for _, l := range launches {
		if l.Export != cfg.ExportPerLaunch || l.Prestige != cfg.PrestigePerLaunch {
			t.Fatalf("launch event %+v does not carry the per-launch amounts", l)
		}
	}
}

// AC-5: the launch-exclusion contour is a real, queryable blight — a cell
// inside the radius is degraded relative to a cell just outside, with the
// radius read from data/spaceport.json, not hardcoded.
func TestExclusionContourBlightRadius(t *testing.T) {
	cfg := testConfig()
	a, _, _, _, _, _ := newSpaceport(t, cfg.ExpertThreshold, true)

	if a.BlightFactor(0, 0) != 1000 {
		t.Fatal("expected no blight before the site is chosen")
	}
	if err := a.StartBuild(BuildCommand{FacilityKey: "space_launch_complex", SiteX: 0, SiteY: 0}); err != nil {
		t.Fatalf("start build: %v", err)
	}

	r := a.ExclusionRadius()
	if r != cfg.ExclusionRadius {
		t.Fatalf("radius = %d, want data-sourced %d", r, cfg.ExclusionRadius)
	}
	inside := a.BlightFactor(0, 0)
	outside := a.BlightFactor(r+1, 0)
	if inside >= outside {
		t.Fatalf("inside factor %d is not worse than outside factor %d", inside, outside)
	}
	if outside != 1000 {
		t.Fatalf("outside factor = %d, want 1000 (no degradation)", outside)
	}
	if inside != cfg.ExclusionFactorPerMille {
		t.Fatalf("inside factor = %d, want data-sourced %d", inside, cfg.ExclusionFactorPerMille)
	}
}

// AC-6: the spaceport injects a measurable, built-conditional demand into
// the FDI and tourism seams.
func TestFdiTourismDraw(t *testing.T) {
	cfg := testConfig()
	a, _, _, _, fdi, tou := newSpaceport(t, cfg.ExpertThreshold, true)

	// Not built: zero draw, nothing injected.
	if a.FdiDrawAmount() != 0 || a.TourismDrawAmount() != 0 {
		t.Fatal("draw nonzero before build")
	}
	if err := a.InjectDraws(); err != nil {
		t.Fatalf("inject draws pre-build: %v", err)
	}
	if fdi.Total() != 0 || tou.Total() != 0 {
		t.Fatal("injected draw before build")
	}

	buildComplete(t, a)
	if a.FdiDrawAmount() != cfg.FdiDrawAmount {
		t.Fatalf("fdi draw = %d, want %d", a.FdiDrawAmount(), cfg.FdiDrawAmount)
	}
	if a.TourismDrawAmount() != cfg.TourismDrawAmount {
		t.Fatalf("tourism draw = %d, want %d", a.TourismDrawAmount(), cfg.TourismDrawAmount)
	}
	if err := a.InjectDraws(); err != nil {
		t.Fatalf("inject draws post-build: %v", err)
	}
	if fdi.Total() != cfg.FdiDrawAmount {
		t.Fatalf("fdi injected = %d, want %d", fdi.Total(), cfg.FdiDrawAmount)
	}
	if tou.Total() != cfg.TourismDrawAmount {
		t.Fatalf("tourism injected = %d, want %d", tou.Total(), cfg.TourismDrawAmount)
	}
}

// AC-7: prestige is this facility's own output derived from launch history —
// zero before the first launch, non-decreasing across launches.
func TestPrestigeAccumulatesPerLaunch(t *testing.T) {
	cfg := testConfig()
	a, _, _, _, _, _ := newSpaceport(t, cfg.ExpertThreshold, true)

	if a.Prestige() != 0 {
		t.Fatal("prestige nonzero before first launch")
	}
	buildComplete(t, a)
	if a.Prestige() != 0 {
		t.Fatal("prestige nonzero post-build but pre-launch")
	}
	prev := int64(0)
	for i := int64(0); i < cfg.LaunchCadenceMonths*2; i++ {
		if err := a.Tick(cfg.BuildMonths + i + 1); err != nil {
			t.Fatalf("tick: %v", err)
		}
		if p := a.Prestige(); p < prev {
			t.Fatalf("prestige decreased from %d to %d", prev, p)
		} else {
			prev = p
		}
	}
	if got := a.Prestige(); got != cfg.PrestigePerLaunch*2 {
		t.Fatalf("prestige = %d, want %d", got, cfg.PrestigePerLaunch*2)
	}
}

// AC-10: each rejection path raises its registry code and writes no state.
func TestUnmetGateRejectsWithNoPartialState(t *testing.T) {
	cfg := testConfig()
	a, _, _, _, _, _ := newSpaceport(t, cfg.ExpertThreshold-1, true)
	err := a.StartBuild(BuildCommand{FacilityKey: "space_launch_complex", SiteX: 0, SiteY: 0})
	assertCode(t, err, ErrExpertGateUnmet)
	assertNoPartialState(t, a)
}

func TestUnknownSpaceportKeyRejected(t *testing.T) {
	cfg := testConfig()
	a, _, _, _, _, _ := newSpaceport(t, cfg.ExpertThreshold, true)
	err := a.StartBuild(BuildCommand{FacilityKey: "spaceport_spacex", SiteX: 0, SiteY: 0})
	assertCode(t, err, ErrUnknownFacilityKey)
	assertNoPartialState(t, a)
}

func TestNoPermitRejected(t *testing.T) {
	cfg := testConfig()
	a, _, _, _, _, _ := newSpaceport(t, cfg.ExpertThreshold, false)
	err := a.StartBuild(BuildCommand{FacilityKey: "space_launch_complex", SiteX: 0, SiteY: 0})
	assertCode(t, err, ErrPermitMissing)
	assertNoPartialState(t, a)
}

func TestLaunchAgainstUnbuiltRejected(t *testing.T) {
	cfg := testConfig()
	a, _, _, _, _, _ := newSpaceport(t, cfg.ExpertThreshold, true)
	_, err := a.LaunchSchedule(3)
	assertCode(t, err, ErrLaunchUnbuilt)
}

// AC-11: identical build+launch sequences produce byte-identical launch
// schedules and prestige (no wall clock, no shared/global RNG).
func TestDeterminism(t *testing.T) {
	run := func() (string, int64) {
		cfg := testConfig()
		a, err := New(cfg, 12345, "test")
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		edu := &fakeEducation{rp: cfg.ExpertThreshold}
		perm := &fakePermit{held: true}
		dec := &fakeDecommission{}
		if err := a.SetEducationGate(edu); err != nil {
			t.Fatal(err)
		}
		if err := a.SetPermitGate(perm); err != nil {
			t.Fatal(err)
		}
		if err := a.SetDecommissionLiability(dec); err != nil {
			t.Fatal(err)
		}
		if err := a.StartBuild(BuildCommand{FacilityKey: "space_launch_complex", SiteX: 3, SiteY: 4}); err != nil {
			t.Fatalf("start build: %v", err)
		}
		for m := int64(1); m <= cfg.BuildMonths+cfg.LaunchCadenceMonths*4; m++ {
			if err := a.Tick(m); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		var sb strings.Builder
		for _, l := range a.Launches() {
			fmt.Fprintf(&sb, "%d:%d:%d;", l.Month, l.Export, l.Prestige)
		}
		return sb.String(), a.Prestige()
	}
	s1, p1 := run()
	s2, p2 := run()
	if s1 != s2 || p1 != p2 {
		t.Fatalf("non-deterministic: (%q,%d) vs (%q,%d)", s1, p1, s2, p2)
	}
	if s1 == "" {
		t.Fatal("no launches produced to compare")
	}
}

// AC-13: reading the launch schedule and exclusion contour concurrently with
// a tick firing a launch races nothing.
func TestConcurrentReadsNoRace(t *testing.T) {
	cfg := testConfig()
	a, _, _, _, _, _ := newSpaceport(t, cfg.ExpertThreshold, true)
	if err := a.StartBuild(BuildCommand{FacilityKey: "space_launch_complex", SiteX: 0, SiteY: 0}); err != nil {
		t.Fatalf("start build: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = a.Launches()
				_ = a.BlightFactor(0, 0)
				_ = a.Prestige()
				_ = a.IsBuilt()
			}
		}
	}()

	for m := int64(1); m <= cfg.BuildMonths+cfg.LaunchCadenceMonths*6; m++ {
		if err := a.Tick(m); err != nil {
			t.Fatalf("tick %d: %v", m, err)
		}
	}
	close(stop)
	wg.Wait()
	if len(a.Launches()) == 0 {
		t.Fatal("no launches fired")
	}
}
