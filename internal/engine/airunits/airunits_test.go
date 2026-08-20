package airunits

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestMain points the error registry at the package's own fixture so the
// AC-11 GR#7 code assertions resolve against the codes this package defines,
// without editing the shared data/errors.json (which this build is scoped not
// to touch). Once the lead registers the MET-G49xx codes in data/errors.json,
// this fixture is redundant and the same assertions hold against the real
// registry.
func TestMain(m *testing.M) {
	abs, err := filepath.Abs(filepath.Join("testdata", "errors.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve testdata/errors.json:", err)
		os.Exit(1)
	}
	if err := os.Setenv("METROPOLIS_ERRORS_PATH", abs); err != nil {
		fmt.Fprintln(os.Stderr, "set METROPOLIS_ERRORS_PATH:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// --- fixtures ---

// defaultData is the balanced test fixture every data-derived test loads.
// These are INPUTS; expectations are always computed back from the loaded
// config, never a hardcoded constant (AC-15).
func defaultData() HelicoptersData {
	return HelicoptersData{
		Version: 1,
		Units: map[string]UnitData{
			"police": {
				PurchaseCostMicroPounds: 5000, UnlockMilestone: 2,
				FuelCostMicroPounds: 10, HangarCostMicroPounds: 20,
				InsuranceCostMicroPounds: 30, CrewCostMicroPounds: 40,
				Effect:     EffectData{CoverageRadiusExtension: 100},
				Disclosure: "placeholder",
			},
			"fire": {
				PurchaseCostMicroPounds: 6000, UnlockMilestone: 3,
				FuelCostMicroPounds: 12, HangarCostMicroPounds: 22,
				InsuranceCostMicroPounds: 32, CrewCostMicroPounds: 42,
				Effect:     EffectData{RemoteFireReachBonus: 200},
				Disclosure: "placeholder",
			},
			"ambulance": {
				PurchaseCostMicroPounds: 4500, UnlockMilestone: 3,
				FuelCostMicroPounds: 11, HangarCostMicroPounds: 21,
				InsuranceCostMicroPounds: 31, CrewCostMicroPounds: 41,
				Effect:     EffectData{HospitalLandingReductionMinutes: 15},
				Disclosure: "placeholder",
			},
			"vip": {
				PurchaseCostMicroPounds: 8000, UnlockMilestone: 4,
				FuelCostMicroPounds: 13, HangarCostMicroPounds: 23,
				InsuranceCostMicroPounds: 33, CrewCostMicroPounds: 43,
				Effect:     EffectData{CommercialRevenuePerMonth: 1000},
				Disclosure: "placeholder",
			},
		},
		Maintenance: MaintenanceData{WearPerFlightCycle: 1, OutOfServiceWearThreshold: 5, EngineerHoursPerWearPoint: 2, Disclosure: "placeholder"},
		Approval:    ApprovalData{ApprovalWeightPerActiveChopper: 3, Disclosure: "placeholder"},
		Weather:     WeatherData{GroundingWindKnots: 20, Disclosure: "placeholder"},
		Travel:      TravelData{AirSpeedMinutesPerUnit: 2, Disclosure: "placeholder"},
	}
}

// writeAndLoad marshals d to a temp data dir and Loads an API from it, so a
// test can mutate the fixture and observe the change (AC-3/AC-8/AC-9/AC-12).
func writeAndLoad(t *testing.T, d HelicoptersData) *AirUnitsAPI {
	t.Helper()
	dir := t.TempDir()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), b, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	a, err := Load(dir, "test")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return a
}

// newAPIWithSeed builds an API directly from a config with an explicit seed
// (determinism tests).
func newAPIWithSeed(t *testing.T, seed uint64, d HelicoptersData) *AirUnitsAPI {
	t.Helper()
	a, err := New(seed, d.config(), "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// --- seam stubs ---

type financeStub struct {
	mu            sync.Mutex
	capital       []det.Micropounds
	opex          []det.Micropounds
	rejectCapital error
}

func (f *financeStub) SettleCapital(amount det.Micropounds) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rejectCapital != nil {
		return f.rejectCapital
	}
	f.capital = append(f.capital, amount)
	return nil
}

func (f *financeStub) SettleOpex(amount det.Micropounds) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opex = append(f.opex, amount)
	return nil
}

func (f *financeStub) capitalTotal() det.Micropounds {
	f.mu.Lock()
	defer f.mu.Unlock()
	var t det.Micropounds
	for _, a := range f.capital {
		t += a
	}
	return t
}

func (f *financeStub) opexTotal() det.Micropounds {
	f.mu.Lock()
	defer f.mu.Unlock()
	var t det.Micropounds
	for _, a := range f.opex {
		t += a
	}
	return t
}

type staffingStub struct {
	qualified map[PilotID]bool
}

func (s *staffingStub) PilotQualified(p PilotID) (bool, error) {
	return s.qualified[p], nil
}

type maintenanceStub struct {
	mu             sync.Mutex
	demands        map[UnitID]int64
	serviceCleared int64
}

func (m *maintenanceStub) ReportDemand(id UnitID, hours int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.demands[id] = hours
	return nil
}

func (m *maintenanceStub) Service(id UnitID, hours int64) (int64, error) {
	return m.serviceCleared, nil
}

type dispatchStub struct {
	mu            sync.Mutex
	contributions []RoleEffect
}

func (d *dispatchStub) ReportContribution(id UnitID, role UnitType, effect RoleEffect) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.contributions = append(d.contributions, effect)
	return nil
}

type weatherStub struct {
	w Weather
}

func (w *weatherStub) CurrentWeather() Weather { return w.w }

const (
	pilotQualified   PilotID = 1000
	pilotQualified2  PilotID = 1001
	pilotUnqualified PilotID = 999
)

type testEnv struct {
	a     *AirUnitsAPI
	fin   *financeStub
	staff *staffingStub
	maint *maintenanceStub
	disp  *dispatchStub
	wx    *weatherStub
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	a := writeAndLoad(t, defaultData())
	fin := &financeStub{}
	staff := &staffingStub{qualified: map[PilotID]bool{pilotQualified: true, pilotQualified2: true}}
	maint := &maintenanceStub{demands: map[UnitID]int64{}, serviceCleared: 5}
	disp := &dispatchStub{}
	wx := &weatherStub{}
	mustSet(t, a.SetFinance(fin))
	mustSet(t, a.SetStaffing(staff))
	mustSet(t, a.SetMaintenance(maint))
	mustSet(t, a.SetDispatch(disp))
	mustSet(t, a.SetWorld(wx))
	return &testEnv{a: a, fin: fin, staff: staff, maint: maint, disp: disp, wx: wx}
}

func mustSet(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("seam wiring failed: %v", err)
	}
}

// buy dispatches a qualified chopper and returns its id.
func (e *testEnv) buyAndFly(t *testing.T, typ UnitType, milestone int64) UnitID {
	t.Helper()
	id, err := e.a.Purchase(typ, milestone)
	if err != nil {
		t.Fatalf("Purchase(%v): %v", typ, err)
	}
	if err := e.a.AssignPilot(id, pilotQualified); err != nil {
		t.Fatalf("AssignPilot: %v", err)
	}
	if err := e.a.Dispatch(id); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	return id
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want code %s", code)
	}
	if !errors.Is(err, &errs.E{Code: code}) {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

// assertPilotOn asserts one chopper's assigned pilot snapshot.
func assertPilotOn(t *testing.T, a *AirUnitsAPI, id UnitID, want PilotID) {
	t.Helper()
	st, ok, err := a.UnitStatus(id)
	if err != nil || !ok {
		t.Fatalf("UnitStatus(%d): ok=%v err=%v", id, ok, err)
	}
	if st.Pilot != want {
		t.Fatalf("unit %d pilot = %d, want %d", id, st.Pilot, want)
	}
}

// --- AC-1: four distinct unit types ---

func TestFourRoleTypes(t *testing.T) {
	if len(UnitTypes) != 4 {
		t.Fatalf("want exactly four resolvable unit types, got %d", len(UnitTypes))
	}
	seenKeys := map[string]bool{}
	seenEffects := map[EffectKind]bool{}
	for _, typ := range UnitTypes {
		key := typ.String()
		if seenKeys[key] {
			t.Fatalf("duplicate type key %q", key)
		}
		seenKeys[key] = true
		if typ != UnitPolice && typ != UnitFire && typ != UnitAmbulance && typ != UnitVIP {
			t.Fatalf("unknown type key %q", key)
		}
		k := effectKindFor(typ)
		if seenEffects[k] {
			t.Fatalf("two roles resolve to the same effect path %v", k)
		}
		seenEffects[k] = true
	}
	if len(seenEffects) != 4 {
		t.Fatalf("want four distinct effect paths, got %d", len(seenEffects))
	}
}

// --- AC-3: purchase is data-driven, milestone-gated CAPEX ---

func TestPurchasePostsDataDrivenCapex(t *testing.T) {
	// Load one fixture, purchase, and confirm the CAPEX post equals the
	// fixture's figure (never a Go literal).
	e := newTestEnv(t)
	id, err := e.a.Purchase(UnitPolice, 10)
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}
	want := e.a.cfg.Units[UnitPolice].PurchaseCost
	if got := e.fin.capitalTotal(); got != want {
		t.Fatalf("CAPEX posted %d, want %d (from loaded fixture)", got, want)
	}
	if e.a.TotalChoppers() != 1 {
		t.Fatalf("want 1 chopper after purchase, got %d", e.a.TotalChoppers())
	}
	_ = id

	// Mutate the fixture's purchase cost and confirm the changed figure is the
	// one posted — the value lives in the data file, not in code.
	d := defaultData()
	u := d.Units["fire"]
	u.PurchaseCostMicroPounds = 424242
	d.Units["fire"] = u
	a2 := writeAndLoad(t, d)
	f2 := &financeStub{}
	mustSet(t, a2.SetFinance(f2))
	if _, err := a2.Purchase(UnitFire, 10); err != nil {
		t.Fatalf("Purchase(mutated): %v", err)
	}
	if got := f2.capitalTotal(); got != det.Micropounds(424242) {
		t.Fatalf("CAPEX posted %d, want 424242 (mutated fixture)", got)
	}
}

func TestPurchaseMilestoneGate(t *testing.T) {
	e := newTestEnv(t)
	// Police unlocks at milestone 2; purchasing at milestone 1 must fail
	// closed and create no chopper.
	if _, err := e.a.Purchase(UnitPolice, 1); err != nil {
		wantCode(t, err, ErrMilestoneLocked)
	} else {
		t.Fatal("Purchase below unlock milestone must return ErrMilestoneLocked")
	}
	if e.a.TotalChoppers() != 0 {
		t.Fatal("a failed purchase must not create a chopper")
	}
	if e.fin.capitalTotal() != 0 {
		t.Fatal("a failed purchase must not post CAPEX")
	}
}

// --- AC-4: running cost is OPEX, composed of named components ---

func TestRunningCostFuelComponent(t *testing.T) {
	e := newTestEnv(t)
	id, err := e.a.Purchase(UnitPolice, 10)
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}
	rc, err := e.a.RunningCostFor(UnitPolice)
	if err != nil {
		t.Fatalf("RunningCostFor: %v", err)
	}

	// Grounded month: standing components only, no fuel.
	if err := e.a.AdvanceMonth(1); err != nil {
		t.Fatalf("AdvanceMonth(grounded): %v", err)
	}
	grounded := e.fin.opexTotal()
	if want := rc.GroundedTotal(); grounded != want {
		t.Fatalf("grounded OPEX = %d, want %d (hangar+insurance+crew)", grounded, want)
	}
	if grounded != rc.Hangar+rc.Insurance+rc.Crew {
		t.Fatal("grounded month must post exactly hangar+insurance+crew")
	}

	// Flying month: fuel additionally, so flying > grounded by the fuel
	// component.
	e.fin.opex = e.fin.opex[:0]
	if err := e.a.AssignPilot(id, pilotQualified); err != nil {
		t.Fatalf("AssignPilot: %v", err)
	}
	if err := e.a.Dispatch(id); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if err := e.a.AdvanceMonth(2); err != nil {
		t.Fatalf("AdvanceMonth(flying): %v", err)
	}
	flying := e.fin.opexTotal()
	if want := rc.Total(); flying != want {
		t.Fatalf("flying OPEX = %d, want %d (fuel+hangar+insurance+crew)", flying, want)
	}
	if delta := flying - grounded; delta != rc.Fuel {
		t.Fatalf("flying - grounded = %d, want fuel component %d", delta, rc.Fuel)
	}
}

func TestGroundedVersusFlyingCost(t *testing.T) {
	e := newTestEnv(t)
	id, err := e.a.Purchase(UnitAmbulance, 10)
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}
	rc, _ := e.a.RunningCostFor(UnitAmbulance)
	if err := e.a.AdvanceMonth(1); err != nil {
		t.Fatal(err)
	}
	grounded := e.fin.opexTotal()

	e.fin.opex = e.fin.opex[:0]
	mustSet(t, e.a.AssignPilot(id, pilotQualified))
	mustSet(t, e.a.Dispatch(id))
	mustSet(t, e.a.AdvanceMonth(2))
	flying := e.fin.opexTotal()

	if grounded != rc.Hangar+rc.Insurance+rc.Crew {
		t.Fatalf("grounded total %d != standing components %d", grounded, rc.Hangar+rc.Insurance+rc.Crew)
	}
	if flying <= grounded {
		t.Fatalf("flying total %d must exceed grounded total %d", flying, grounded)
	}
}

// --- AC-5: no pilot, no flight ---

func TestNoPilotNoFlight(t *testing.T) {
	e := newTestEnv(t)
	id, err := e.a.Purchase(UnitPolice, 10)
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}
	// No pilot yet: dispatch must be rejected.
	if err := e.a.Dispatch(id); err != nil {
		wantCode(t, err, ErrNoPilot)
	} else {
		t.Fatal("Dispatch without a pilot must return ErrNoPilot")
	}

	// Assign a qualified pilot: now dispatchable.
	mustSet(t, e.a.AssignPilot(id, pilotQualified))
	if err := e.a.Dispatch(id); err != nil {
		t.Fatalf("Dispatch with pilot: %v", err)
	}

	// Removing the pilot grounds the chopper.
	mustSet(t, e.a.ArriveOnScene(id))
	mustSet(t, e.a.RemovePilot(id))
	if err := e.a.Dispatch(id); err != nil {
		wantCode(t, err, ErrGroundedDispatch)
	} else {
		t.Fatal("Dispatch after pilot removal must return ErrGroundedDispatch")
	}
}

func TestUnqualifiedPilotRejected(t *testing.T) {
	e := newTestEnv(t)
	id, err := e.a.Purchase(UnitPolice, 10)
	if err != nil {
		t.Fatalf("Purchase: %v", err)
	}
	if err := e.a.AssignPilot(id, pilotUnqualified); err != nil {
		wantCode(t, err, ErrUnqualifiedPilot)
	} else {
		t.Fatal("AssignPilot(unqualified) must return ErrUnqualifiedPilot")
	}
	// No state change: pilot still unassigned, chopper still not dispatchable.
	st, ok, err := e.a.UnitStatus(id)
	if err != nil || !ok {
		t.Fatalf("UnitStatus: %v %v", st, err)
	}
	if st.Pilot != 0 {
		t.Fatalf("unqualified assignment must not set a pilot (got %d)", st.Pilot)
	}
	if err := e.a.Dispatch(id); err != nil {
		wantCode(t, err, ErrNoPilot)
	} else {
		t.Fatal("Dispatch after rejected pilot assignment must still return ErrNoPilot")
	}
}

// TestAssignPilotRejectsDoubleAssignment covers MOD-074 r1: one pilot may
// never be live on two choppers at once. AssignPilot(P, unit B) while P is
// still assigned to a DIFFERENT unit A is rejected fail-closed (ErrPilot-
// AlreadyAssigned), P stays only on A, and reassignment succeeds after
// RemovePilot releases the prior slot. Same-unit reassignment stays a
// harmless idempotent no-op.
func TestAssignPilotRejectsDoubleAssignment(t *testing.T) {
	e := newTestEnv(t)
	a, err := e.a.Purchase(UnitPolice, 10)
	if err != nil {
		t.Fatalf("Purchase A: %v", err)
	}
	b, err := e.a.Purchase(UnitFire, 10)
	if err != nil {
		t.Fatalf("Purchase B: %v", err)
	}

	// P is assigned to unit A.
	mustSet(t, e.a.AssignPilot(a, pilotQualified))
	assertPilotOn(t, e.a, a, pilotQualified)

	// Same-unit reassignment is idempotent, not a rejection.
	mustSet(t, e.a.AssignPilot(a, pilotQualified))
	assertPilotOn(t, e.a, a, pilotQualified)

	// Reassigning P to a DIFFERENT unit B is rejected fail-closed.
	if err := e.a.AssignPilot(b, pilotQualified); err != nil {
		wantCode(t, err, ErrPilotAlreadyAssigned)
	} else {
		t.Fatal("AssignPilot to a second unit must return ErrPilotAlreadyAssigned")
	}
	// P is never on two units at once: still only A, B has no pilot.
	assertPilotOn(t, e.a, a, pilotQualified)
	assertPilotOn(t, e.a, b, 0)

	// Release P from A, then the reassignment to B succeeds.
	mustSet(t, e.a.RemovePilot(a))
	mustSet(t, e.a.AssignPilot(b, pilotQualified))
	assertPilotOn(t, e.a, a, 0)
	assertPilotOn(t, e.a, b, pilotQualified)
}

// --- AC-6: maintenance-heavy ---

func TestWearAccumulatesToOutOfService(t *testing.T) {
	e := newTestEnv(t)
	id := e.buyAndFly(t, UnitPolice, 10)

	threshold := e.a.cfg.Maintenance.OutOfServiceWearThreshold
	perCycle := e.a.cfg.Maintenance.WearPerFlightCycle
	cycles := threshold / perCycle

	var prev int64
	for i := int64(0); i < cycles; i++ {
		if err := e.a.AdvanceMonth(i + 1); err != nil {
			t.Fatalf("AdvanceMonth(%d): %v", i+1, err)
		}
		st, _, _ := e.a.UnitStatus(id)
		if st.Wear <= prev {
			t.Fatalf("wear must increase monotonically (cycle %d: %d -> %d)", i+1, prev, st.Wear)
		}
		prev = st.Wear
	}
	st, _, _ := e.a.UnitStatus(id)
	if st.State != StateOutOfService {
		t.Fatalf("un-serviced wear at threshold must transition to out-of-service, got %v", st.State)
	}
	// The engineer-hour burden surfaced through the maintenance seam (never a
	// local ledger).
	if e.maint.demands[id] <= 0 {
		t.Fatal("maintenance seam must record a positive engineer-hour demand")
	}
}

func TestServiceClearsWearAndRestores(t *testing.T) {
	e := newTestEnv(t)
	id := e.buyAndFly(t, UnitPolice, 10)
	threshold := e.a.cfg.Maintenance.OutOfServiceWearThreshold
	perCycle := e.a.cfg.Maintenance.WearPerFlightCycle
	for i := int64(0); i < threshold/perCycle; i++ {
		mustSet(t, e.a.AdvanceMonth(i+1))
	}
	if st, _, _ := e.a.UnitStatus(id); st.State != StateOutOfService {
		t.Fatalf("want out-of-service before service, got %v", st.State)
	}
	mustSet(t, e.a.Service(id, 100))
	st, _, _ := e.a.UnitStatus(id)
	if st.State != StateAvailable {
		t.Fatalf("serviced chopper should return to available, got %v", st.State)
	}
	if st.Wear >= threshold {
		t.Fatalf("service should clear wear below threshold (wear=%d, threshold=%d)", st.Wear, threshold)
	}
}

// --- AC-7: traffic-immune, weather-limited ---

// groundReferenceTravelTime is a deliberately congestion-sensitive ground-unit
// reference (used only to prove the chopper's air path is congestion-immune).
func groundReferenceTravelTime(distance, congestionPercent int64) int64 {
	return distance*2 + distance*2*congestionPercent/100
}

func TestTrafficImmuneCongestion(t *testing.T) {
	e := newTestEnv(t)
	dist := int64(50)

	clear, err := e.a.AirTravelTimeMinutes(dist)
	if err != nil {
		t.Fatalf("AirTravelTimeMinutes: %v", err)
	}
	// Congestion is not even a parameter of the air path: recomputing at any
	// "congestion" leaves the chopper's travel time identical.
	for _, congestion := range []int64{0, 50, 100} {
		again, _ := e.a.AirTravelTimeMinutes(dist)
		if again != clear {
			t.Fatalf("air travel time changed with congestion %d%%: %d -> %d", congestion, clear, again)
		}
		_ = congestion
	}
	// A ground unit on the same origin-destination is measurably slowed.
	if g0, g100 := groundReferenceTravelTime(dist, 0), groundReferenceTravelTime(dist, 100); g100 <= g0 {
		t.Fatalf("ground reference must slow under congestion (%d -> %d)", g0, g100)
	}
	if want := dist * e.a.cfg.Travel.AirSpeedMinutesPerUnit; clear != want {
		t.Fatalf("air travel time = %d, want %d (distance × data air speed)", clear, want)
	}
}

func TestWeatherGateGroundsDispatch(t *testing.T) {
	e := newTestEnv(t)
	id := e.buyAndFly(t, UnitFire, 10)
	mustSet(t, e.a.ArriveOnScene(id))
	mustSet(t, e.a.ReleaseFromScene(id)) // back at base, available

	// Adverse weather grounds dispatch.
	e.wx.w = Weather{WindKnots: 30}
	if e.a.WeatherGrounded(Weather{WindKnots: 30}) != true {
		t.Fatal("WeatherGrounded must report adverse wind at/above threshold")
	}
	if err := e.a.Dispatch(id); err != nil {
		wantCode(t, err, ErrWeatherGrounded)
	} else {
		t.Fatal("Dispatch in adverse weather must return ErrWeatherGrounded")
	}

	// Weather clears: dispatchable again.
	e.wx.w = Weather{}
	if err := e.a.Dispatch(id); err != nil {
		t.Fatalf("Dispatch in calm weather: %v", err)
	}
	mustSet(t, e.a.ArriveOnScene(id))
	mustSet(t, e.a.ReleaseFromScene(id))

	// A flying chopper is grounded when weather turns adverse mid-tick.
	mustSet(t, e.a.Dispatch(id))
	e.wx.w = Weather{WindKnots: 30}
	mustSet(t, e.a.AdvanceMonth(1))
	if st, _, _ := e.a.UnitStatus(id); st.State != StateOutOfService {
		t.Fatalf("flying chopper must ground under adverse weather, got %v", st.State)
	}
}

// --- AC-8: four role-specific, data-driven effects ---

func TestRoleEffectsDistinctAndDataDriven(t *testing.T) {
	e := newTestEnv(t)
	effects := map[UnitType]RoleEffect{}
	values := map[EffectKind]int64{}
	for _, typ := range UnitTypes {
		re, err := e.a.RoleEffect(typ)
		if err != nil {
			t.Fatalf("RoleEffect(%v): %v", typ, err)
		}
		effects[typ] = re
		values[re.Kind] = re.Value()
	}
	if len(values) != 4 {
		t.Fatalf("four roles must resolve to four distinct effects, got %d", len(values))
	}

	// The four named accessors return the data-loaded figure for their role.
	pol, _ := e.a.PoliceEffect()
	fir, _ := e.a.FireEffect()
	amb, _ := e.a.AmbulanceEffect()
	vip, _ := e.a.VIPEffect()
	if pol != e.a.cfg.Units[UnitPolice].Effect.CoverageRadiusExtension ||
		fir != e.a.cfg.Units[UnitFire].Effect.RemoteFireReachBonus ||
		amb != e.a.cfg.Units[UnitAmbulance].Effect.HospitalLandingReductionMinutes ||
		vip != e.a.cfg.Units[UnitVIP].Effect.CommercialRevenuePerMonth {
		t.Fatal("role effect accessors must return their data-loaded figure")
	}

	// Mutating one role's effect parameter changes only that role.
	d := defaultData()
	u := d.Units["ambulance"]
	u.Effect = EffectData{HospitalLandingReductionMinutes: 77}
	d.Units["ambulance"] = u
	a2 := writeAndLoad(t, d)
	if got, _ := a2.AmbulanceEffect(); got != 77 {
		t.Fatalf("ambulance effect = %d, want 77 (mutated fixture)", got)
	}
	if got, _ := a2.PoliceEffect(); got != e.a.cfg.Units[UnitPolice].Effect.CoverageRadiusExtension {
		t.Fatalf("police effect changed to %d after mutating ambulance; must be unchanged", got)
	}
}

func TestHospitalLandingEffect(t *testing.T) {
	e := newTestEnv(t)
	got, err := e.a.AmbulanceEffect()
	if err != nil {
		t.Fatal(err)
	}
	if want := e.a.cfg.Units[UnitAmbulance].Effect.HospitalLandingReductionMinutes; got != want {
		t.Fatalf("hospital landing reduction = %d, want %d", got, want)
	}
}

func TestRemoteFireEffect(t *testing.T) {
	e := newTestEnv(t)
	got, err := e.a.FireEffect()
	if err != nil {
		t.Fatal(err)
	}
	if want := e.a.cfg.Units[UnitFire].Effect.RemoteFireReachBonus; got != want {
		t.Fatalf("remote fire reach bonus = %d, want %d", got, want)
	}
}

// --- AC-9: approval via response-time improvement ---

func TestApprovalWeight(t *testing.T) {
	e := newTestEnv(t)
	id, err := e.a.Purchase(UnitAmbulance, 10)
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, e.a.AssignPilot(id, pilotQualified))
	if e.a.ApprovalWeight() != 0 {
		t.Fatal("a grounded (not flying) chopper must contribute zero approval")
	}
	mustSet(t, e.a.Dispatch(id))
	want := e.a.cfg.Approval.ApprovalWeightPerActiveChopper
	if got := e.a.ApprovalWeight(); got != want {
		t.Fatalf("approval weight = %d, want %d (one active chopper × data weight)", got, want)
	}

	// Mutating the fixture weight moves the approval contribution.
	d := defaultData()
	d.Approval = ApprovalData{ApprovalWeightPerActiveChopper: 9, Disclosure: "placeholder"}
	a2 := writeAndLoad(t, d)
	mustSet(t, a2.SetFinance(&financeStub{}))
	mustSet(t, a2.SetStaffing(&staffingStub{qualified: map[PilotID]bool{pilotQualified: true}}))
	id2, err := a2.Purchase(UnitAmbulance, 10)
	if err != nil {
		t.Fatalf("Purchase(mutated): %v", err)
	}
	mustSet(t, a2.AssignPilot(id2, pilotQualified))
	mustSet(t, a2.Dispatch(id2))
	if got := a2.ApprovalWeight(); got != 9 {
		t.Fatalf("approval weight = %d, want 9 (mutated fixture)", got)
	}
}

func TestGroundedApprovalZero(t *testing.T) {
	e := newTestEnv(t)
	id := e.buyAndFly(t, UnitPolice, 10)
	if e.a.ApprovalWeight() == 0 {
		t.Fatal("a flying chopper must contribute positive approval")
	}
	mustSet(t, e.a.RemovePilot(id)) // grounds it
	if e.a.ApprovalWeight() != 0 {
		t.Fatal("a grounded chopper must contribute zero approval")
	}
}

// --- AC-10: fleet conservation ---

func TestFleetConservation(t *testing.T) {
	e := newTestEnv(t)
	assertConserved := func() {
		t.Helper()
		c := e.a.FleetCounts()
		if !c.Conserved() {
			t.Fatalf("fleet conservation broken: %+v", c)
		}
	}

	p1, err := e.a.Purchase(UnitPolice, 10)
	mustSet(t, err)
	p2, err := e.a.Purchase(UnitFire, 10)
	mustSet(t, err)
	p3, err := e.a.Purchase(UnitAmbulance, 10)
	mustSet(t, err)
	assertConserved()

	mustSet(t, e.a.AssignPilot(p1, pilotQualified))
	mustSet(t, e.a.AssignPilot(p2, pilotQualified2))
	mustSet(t, e.a.Dispatch(p1))      // EnRoute
	mustSet(t, e.a.ArriveOnScene(p1)) // OnScene
	mustSet(t, e.a.RemovePilot(p3))   // OutOfService
	assertConserved()

	for i := int64(1); i <= 3; i++ {
		mustSet(t, e.a.AdvanceMonth(i))
		assertConserved()
	}

	mustSet(t, e.a.ReleaseFromScene(p1)) // back to Available
	assertConserved()

	if e.a.TotalChoppers() != 3 {
		t.Fatalf("TotalChoppers = %d, want 3", e.a.TotalChoppers())
	}
}

// --- AC-11: registry-sourced errors, no state mutation ---

func TestInsufficientFunds(t *testing.T) {
	e := newTestEnv(t)
	e.fin.rejectCapital = errors.New("treasury empty")
	if _, err := e.a.Purchase(UnitPolice, 10); err != nil {
		wantCode(t, err, ErrInsufficientFunds)
	} else {
		t.Fatal("Purchase with insufficient funds must return ErrInsufficientFunds")
	}
	if e.a.TotalChoppers() != 0 {
		t.Fatal("insufficient-funds purchase must not create a chopper")
	}
}

func TestGroundedDispatch(t *testing.T) {
	e := newTestEnv(t)
	id, _ := e.a.Purchase(UnitPolice, 10)
	mustSet(t, e.a.AssignPilot(id, pilotQualified))
	mustSet(t, e.a.RemovePilot(id)) // grounds it
	if err := e.a.Dispatch(id); err != nil {
		wantCode(t, err, ErrGroundedDispatch)
	} else {
		t.Fatal("dispatch of a grounded chopper must return ErrGroundedDispatch")
	}
}

func TestUnknownUnitTypeAndUnit(t *testing.T) {
	e := newTestEnv(t)
	if _, err := e.a.RoleEffect(UnitType(99)); err != nil {
		wantCode(t, err, ErrUnknownUnitType)
	} else {
		t.Fatal("RoleEffect with an unknown type must return ErrUnknownUnitType")
	}
	if _, err := e.a.Purchase(UnitType(99), 10); err != nil {
		wantCode(t, err, ErrUnknownUnitType)
	} else {
		t.Fatal("Purchase with an unknown type must return ErrUnknownUnitType")
	}
	if err := e.a.AssignPilot(UnitID(999), pilotQualified); err != nil {
		wantCode(t, err, ErrUnknownUnit)
	} else {
		t.Fatal("AssignPilot to a nonexistent chopper must return ErrUnknownUnit")
	}
	if err := e.a.Dispatch(UnitID(999)); err != nil {
		wantCode(t, err, ErrUnknownUnit)
	} else {
		t.Fatal("Dispatch of a nonexistent chopper must return ErrUnknownUnit")
	}
}

// --- AC-12: malformed data rejected at load time ---

func TestMalformedDataRejected(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*HelicoptersData)
	}{
		{"missingCost", func(d *HelicoptersData) {
			u := d.Units["police"]
			u.PurchaseCostMicroPounds = 0
			d.Units["police"] = u
		}},
		{"negativeComponent", func(d *HelicoptersData) {
			u := d.Units["fire"]
			u.FuelCostMicroPounds = -5
			d.Units["fire"] = u
		}},
		{"unknownTypeKey", func(d *HelicoptersData) {
			d.Units["rocket"] = UnitData{
				PurchaseCostMicroPounds: 1, UnlockMilestone: 1,
				Effect: EffectData{CoverageRadiusExtension: 1},
			}
		}},
		{"roleWithoutEffect", func(d *HelicoptersData) {
			u := d.Units["ambulance"]
			u.Effect = EffectData{}
			d.Units["ambulance"] = u
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := defaultData()
			tc.mut(&d)
			dir := t.TempDir()
			b, _ := json.Marshal(d)
			if err := os.WriteFile(filepath.Join(dir, fileName), b, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(dir, "test"); err != nil {
				wantCode(t, err, ErrAirunitsDataInvalid)
			} else {
				t.Fatal("malformed data must be rejected at load time, never silently defaulted")
			}
		})
	}
}

// --- AC-13: determinism ---

func TestDeterminism(t *testing.T) {
	run := func(seed uint64) ([]UnitStatus, det.Micropounds) {
		a := newAPIWithSeed(t, seed, defaultData())
		mustSet(t, a.SetFinance(&financeStub{}))
		mustSet(t, a.SetStaffing(&staffingStub{qualified: map[PilotID]bool{pilotQualified: true}}))
		id, err := a.Purchase(UnitVIP, 10)
		if err != nil {
			t.Fatalf("Purchase: %v", err)
		}
		mustSet(t, a.AssignPilot(id, pilotQualified))
		mustSet(t, a.Dispatch(id))
		for i := int64(1); i <= 12; i++ {
			mustSet(t, a.AdvanceMonth(i))
		}
		return a.Fleet(), a.CommercialRevenue()
	}
	f1, r1 := run(42)
	f2, r2 := run(42)
	if fmt.Sprint(f1) != fmt.Sprint(f2) || r1 != r2 {
		t.Fatalf("same seed must produce byte-identical state:\nfleet1=%v revenue1=%d\nfleet2=%v revenue2=%d", f1, r1, f2, r2)
	}
}

// --- AC-14: saturating arithmetic ---

func TestSaturatingWear(t *testing.T) {
	e := newTestEnv(t)
	id := e.buyAndFly(t, UnitPolice, 10)
	ch := e.a.fleet[id]
	ch.wear = math.MaxInt64 - 1
	mustSet(t, e.a.AdvanceMonth(1))
	if ch.wear != math.MaxInt64 {
		t.Fatalf("wear must saturate at MaxInt64, got %d (wrapped?)", ch.wear)
	}
}

// --- AC-15: data-derived counts + concurrency ---

func TestDataDerivedCounts(t *testing.T) {
	e := newTestEnv(t)
	// Expected purchase cost read back from the loaded fixture, never a literal.
	want := e.a.cfg.Units[UnitPolice].PurchaseCost
	if _, err := e.a.Purchase(UnitPolice, 10); err != nil {
		t.Fatal(err)
	}
	if got := e.fin.capitalTotal(); got != want {
		t.Fatalf("CAPEX = %d, want %d (data-derived)", got, want)
	}
	if c := e.a.FleetCounts(); c.Total != 1 || c.Available != 1 {
		t.Fatalf("fleet counts after one purchase = %+v, want Total=1 Available=1", c)
	}
}

func TestConcurrentDispatchGrounding(t *testing.T) {
	e := newTestEnv(t)
	const n = 20
	ids := make([]UnitID, n)
	for i := range ids {
		id, err := e.a.Purchase(UnitPolice, 10)
		if err != nil {
			t.Fatal(err)
		}
		// MOD-074 r1: each unit needs its own pilot — one pilot may never be
		// live on two units at once.
		p := pilotQualified + PilotID(i)
		e.staff.qualified[p] = true
		mustSet(t, e.a.AssignPilot(id, p))
		ids[i] = id
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.a.Dispatch(id)
			_ = e.a.ArriveOnScene(id)
			_ = e.a.ReleaseFromScene(id)
			_ = e.a.RemovePilot(id)
		}()
	}
	wg.Wait()

	if c := e.a.FleetCounts(); !c.Conserved() {
		t.Fatalf("conservation broken after concurrent dispatch/grounding: %+v", c)
	}
}

// Value returns the single meaningful effect magnitude for the role's effect
// kind (test helper for the AC-8 distinctness check).
func (re RoleEffect) Value() int64 {
	switch re.Kind {
	case EffectPoliceCoverage:
		return re.CoverageRadiusExtension
	case EffectFireReach:
		return re.RemoteFireReachBonus
	case EffectAmbulanceLanding:
		return re.HospitalLandingReductionMinutes
	case EffectVIPCommercial:
		return re.CommercialRevenuePerMonth
	default:
		return 0
	}
}
