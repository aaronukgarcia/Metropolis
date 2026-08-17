package chemicals

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

const (
	seedA uint64 = 0xaaaa
	seedB uint64 = 0xbbbb
)

// ---- seam stubs (race-safe, so the concurrent test exercises them too) ----

type stubFreight struct {
	mu    sync.Mutex
	capT  int64 // < 0 means "pass the requested tonnage through"
	calls int
	err   error
}

func (s *stubFreight) CrudeLanding(tonnes int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return 0, s.err
	}
	if s.capT >= 0 && tonnes > s.capT {
		return s.capT, nil
	}
	return tonnes, nil
}

type stubFuel struct {
	mu       sync.Mutex
	supplied int64
	err      error
}

func (s *stubFuel) Supply(tonnes int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.supplied = tonnes
	return nil
}

func (s *stubFuel) SupplyTonnes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.supplied
}

type stubDispatch struct {
	mu         sync.Mutex
	categories []string
	severities []int
	err        error
}

func (s *stubDispatch) ReportIncident(category string, severity int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.categories = append(s.categories, category)
	s.severities = append(s.severities, severity)
	return nil
}

func (s *stubDispatch) snapshot() ([]string, []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.categories...), append([]int(nil), s.severities...)
}

type stubPermit struct {
	mu      sync.Mutex
	granted bool
	err     error
}

func (s *stubPermit) PermitGranted(string, int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.granted, s.err
}

type stubDecom struct {
	mu          sync.Mutex
	liabilities map[string]int64
	err         error
}

func (s *stubDecom) RegisterLiability(key string, cost int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.liabilities == nil {
		s.liabilities = map[string]int64{}
	}
	s.liabilities[key] = cost
	return nil
}

func (s *stubDecom) liability(key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.liabilities[key]
}

// ---- helpers ----

func realDataDir(t *testing.T) string {
	t.Helper()
	dir, err := data.ResolveDataDir(errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("resolve data dir: %v", err)
	}
	return dir
}

func loadRealRefinery(t *testing.T, seed uint64) *Refinery {
	t.Helper()
	r, err := LoadRefinery(realDataDir(t), errs.NewCorrelationID(), seed)
	if err != nil {
		t.Fatalf("load real refinery data: %v", err)
	}
	return r
}

// writeRefineryFixture writes a mutated copy of the real data/refinery.json to
// a temp dir and returns that dir, so a test can exercise the loader against a
// specific data shape (AC-2's data-driven proof, AC-9's malformed cases).
func writeRefineryFixture(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(realDataDir(t), "refinery.json"))
	if err != nil {
		t.Fatalf("read refinery.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal refinery.json: %v", err)
	}
	mutate(m)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "refinery.json"), out, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

func buildRefinery(t *testing.T, r *Refinery, permit *stubPermit, decom *stubDecom) {
	t.Helper()
	if err := r.WirePermit(permit); err != nil {
		t.Fatalf("wire permit: %v", err)
	}
	if err := r.WireDecommission(decom); err != nil {
		t.Fatalf("wire decommission: %v", err)
	}
	if err := r.Build(9); err != nil {
		t.Fatalf("build: %v", err)
	}
}

func wireOperate(t *testing.T, r *Refinery, freight *stubFreight, fuel *stubFuel, dispatch *stubDispatch) {
	t.Helper()
	if err := r.WireFreight(freight); err != nil {
		t.Fatalf("wire freight: %v", err)
	}
	if err := r.WireFuel(fuel); err != nil {
		t.Fatalf("wire fuel: %v", err)
	}
	if err := r.WireDispatch(dispatch); err != nil {
		t.Fatalf("wire dispatch: %v", err)
	}
}

// ---- AC-1: distinct facility profiles ----

func TestRefineryDistinctProfiles(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	refinery, err := r.Facility("refinery")
	if err != nil {
		t.Fatalf("resolve refinery: %v", err)
	}
	works, err := r.Facility("petrochemical_works")
	if err != nil {
		t.Fatalf("resolve petrochemical_works: %v", err)
	}

	// Distinct keys (different chain stages, not one shared row).
	if refinery.Key == works.Key {
		t.Fatalf("refinery and petrochemical works share key %q", refinery.Key)
	}
	if refinery.ThroughputTonnesPerDay == works.ThroughputTonnesPerDay {
		t.Fatalf("refinery throughput (%d) must differ from works throughput (%d)",
			refinery.ThroughputTonnesPerDay, works.ThroughputTonnesPerDay)
	}
	if refinery.Jobs == works.Jobs {
		t.Fatalf("refinery jobs (%d) must differ from works jobs (%d)", refinery.Jobs, works.Jobs)
	}
	// The per-type behavioural fields exist (footprint/throughput/jobs/utility
	// draw) and are populated from the data file, not defaulted.
	for name, p := range map[string]FacilityProfile{"refinery": refinery, "petrochemical_works": works} {
		if p.FootprintCells <= 0 || p.ThroughputTonnesPerDay <= 0 || p.Jobs < 0 {
			t.Fatalf("%s profile has a zero/negative footprint/throughput/jobs field: %+v", name, p)
		}
		if p.PowerKWhPerDay < 0 || p.WaterLitresPerDay < 0 {
			t.Fatalf("%s profile has a negative utility draw: %+v", name, p)
		}
	}
}

// ---- AC-2: data-driven balance figures (GR#15) ----

func TestRefineryDataDrivenThroughput(t *testing.T) {
	dirA := writeRefineryFixture(t, func(m map[string]any) {
		m["facilities"].(map[string]any)["refinery"].(map[string]any)["throughputTonnesPerDay"] = float64(20000)
	})
	dirB := writeRefineryFixture(t, func(m map[string]any) {
		m["facilities"].(map[string]any)["refinery"].(map[string]any)["throughputTonnesPerDay"] = float64(40000)
	})

	a, err := LoadRefinery(dirA, errs.NewCorrelationID(), seedA)
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	b, err := LoadRefinery(dirB, errs.NewCorrelationID(), seedA)
	if err != nil {
		t.Fatalf("load B: %v", err)
	}

	ta, _ := a.Facility("refinery")
	tb, _ := b.Facility("refinery")
	if ta.ThroughputTonnesPerDay == tb.ThroughputTonnesPerDay {
		t.Fatalf("editing the throughput figure did not change the derived throughput (%d vs %d) — the parameter is not actually read",
			ta.ThroughputTonnesPerDay, tb.ThroughputTonnesPerDay)
	}
	if ta.ThroughputTonnesPerDay != 20000 || tb.ThroughputTonnesPerDay != 40000 {
		t.Fatalf("derived throughputs %d/%d do not match the edited figures 20000/40000", ta.ThroughputTonnesPerDay, tb.ThroughputTonnesPerDay)
	}
}

// ---- AC-3: make-vs-buy — both directions reachable ----

func TestRefineryMakeVsBuyBothDirectionsReachable(t *testing.T) {
	// (a) A city with no refinery satisfies demand via import at the margin.
	unbuilt := loadRealRefinery(t, seedA)
	margin, err := unbuilt.ImportUnitCost()
	if err != nil {
		t.Fatalf("import unit cost: %v", err)
	}
	cost, err := unbuilt.ImportRefined(commodityRefinedProduct, 5000)
	if err != nil {
		t.Fatalf("import (no refinery) failed: %v", err)
	}
	if cost != margin*5000 {
		t.Fatalf("import cost %d != margin*tonnes %d", cost, margin*5000)
	}

	// (b) Build the refinery and compare domestic vs import unit cost at scale.
	r := loadRealRefinery(t, seedA)
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	refinery, _ := r.Facility("refinery")

	importCost, err := r.ImportUnitCost()
	if err != nil {
		t.Fatalf("import unit cost: %v", err)
	}
	domHigh, err := r.DomesticUnitCost(refinery.ThroughputTonnesPerDay)
	if err != nil {
		t.Fatalf("domestic unit cost at scale: %v", err)
	}
	if domHigh >= importCost {
		t.Fatalf("build must win at scale: domestic %d >= import %d", domHigh, importCost)
	}

	domLow, err := r.DomesticUnitCost(refinery.ThroughputTonnesPerDay / 20)
	if err != nil {
		t.Fatalf("domestic unit cost at low scale: %v", err)
	}
	if domLow <= importCost {
		t.Fatalf("import must stay rational at small scale: domestic %d <= import %d", domLow, importCost)
	}
}

// ---- AC-4: crude arrives by tanker as freight tonnage; output is input-bound ----

func TestRefineryCrudeTankerInputBound(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	refinery, _ := r.Facility("refinery")
	fuelRate, _ := refinery.Output(commodityFuel)
	fullCrude := refinery.ThroughputTonnesPerDay

	wireOperate(t, r, &stubFreight{capT: -1}, &stubFuel{}, &stubDispatch{})
	full, err := r.Operate(0, fullCrude)
	if err != nil {
		t.Fatalf("operate (unconstrained): %v", err)
	}
	if full.FuelOutput != fuelRate {
		t.Fatalf("unconstrained fuel output %d != fuel rate %d", full.FuelOutput, fuelRate)
	}

	// Constrain crude through the freight edge (a half-capacity tanker landing).
	wireOperate(t, r, &stubFreight{capT: fullCrude / 2}, &stubFuel{}, &stubDispatch{})
	half, err := r.Operate(0, fullCrude)
	if err != nil {
		t.Fatalf("operate (constrained): %v", err)
	}
	if half.FuelOutput >= full.FuelOutput {
		t.Fatalf("constrained fuel output %d did not fall below unconstrained %d", half.FuelOutput, full.FuelOutput)
	}
	if half.FuelOutput != full.FuelOutput/2 {
		t.Fatalf("fuel output %d did not fall proportionally (expected %d)", half.FuelOutput, full.FuelOutput/2)
	}
}

// ---- AC-5: the refinery is the head of the registered ChemAPI chain ----

func TestRefineryChainHeadFeedstockPetrochemical(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	chem, err := r.Chem()
	if err != nil {
		t.Fatalf("chem: %v", err)
	}

	refOut, err := chem.StageOutput("refinery")
	if err != nil {
		t.Fatalf("refinery stage output: %v", err)
	}
	worksIn, err := chem.StageInput("petrochemical_works")
	if err != nil {
		t.Fatalf("works stage input: %v", err)
	}

	// The works' feedstock input is queryable through ChemAPI's own surface and
	// is conserved: input <= upstream routed output.
	if worksIn[commodityFeedstock] > refOut[commodityFeedstock] {
		t.Fatalf("conservation violated: works feedstock input %d > refinery feedstock output %d",
			worksIn[commodityFeedstock], refOut[commodityFeedstock])
	}
	if refOut[commodityFeedstock] <= 0 {
		t.Fatalf("refinery declares no feedstock output")
	}

	// Prove the bound is real: a works demanding MORE feedstock than the
	// refinery routes gets capped at the refinery's output (never exceeded).
	if err := chem.RegisterStage("petrochemical_works",
		map[string]int64{commodityFeedstock: refOut[commodityFeedstock] + 1000},
		map[string]int64{commodityPlastics: 5000}); err != nil {
		t.Fatalf("re-register works: %v", err)
	}
	capped, err := chem.StageInput("petrochemical_works")
	if err != nil {
		t.Fatalf("works input after re-register: %v", err)
	}
	if capped[commodityFeedstock] != refOut[commodityFeedstock] {
		t.Fatalf("works input %d was not capped at upstream output %d",
			capped[commodityFeedstock], refOut[commodityFeedstock])
	}
}

// ---- AC-6: fuel output feeds the fuel system; cutting crude degrades supply ----

func TestRefineryFuelSupplyFeed(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	refinery, _ := r.Facility("refinery")
	fullCrude := refinery.ThroughputTonnesPerDay

	fuel := &stubFuel{}
	wireOperate(t, r, &stubFreight{capT: -1}, fuel, &stubDispatch{})

	if _, err := r.Operate(0, fullCrude); err != nil {
		t.Fatalf("operate (full crude): %v", err)
	}
	fullSupply := fuel.SupplyTonnes()
	if fullSupply <= 0 {
		t.Fatalf("fuel system supply was not fed (got %d)", fullSupply)
	}

	// Cut crude via the freight edge; the fuel system's own supply figure
	// (read back through FuelAPI) must degrade.
	wireOperate(t, r, &stubFreight{capT: fullCrude / 2}, fuel, &stubDispatch{})
	if _, err := r.Operate(1, fullCrude); err != nil {
		t.Fatalf("operate (constrained crude): %v", err)
	}
	halfSupply := fuel.SupplyTonnes()
	if halfSupply >= fullSupply {
		t.Fatalf("fuel supply %d did not degrade below %d when crude was constrained", halfSupply, fullSupply)
	}
}

// ---- AC-7: hazmat-fire category feeds the dispatch edge ----

func TestRefineryHazmatDispatch(t *testing.T) {
	dir := writeRefineryFixture(t, func(m map[string]any) {
		m["facilities"].(map[string]any)["refinery"].(map[string]any)["hazmatFirePeriodDays"] = float64(1)
	})
	r, err := LoadRefinery(dir, errs.NewCorrelationID(), seedA)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	refinery, _ := r.Facility("refinery")

	dispatch := &stubDispatch{}
	wireOperate(t, r, &stubFreight{capT: -1}, &stubFuel{}, dispatch)

	if _, err := r.Operate(0, refinery.ThroughputTonnesPerDay); err != nil {
		t.Fatalf("operate: %v", err)
	}
	categories, severities := dispatch.snapshot()
	if len(categories) != 1 || categories[0] != hazmatFireCategory {
		t.Fatalf("expected one %q incident, got %v", hazmatFireCategory, categories)
	}
	if severities[0] != refinery.HazmatSeverity {
		t.Fatalf("hazmat severity %d != profile severity %d", severities[0], refinery.HazmatSeverity)
	}
}

// ---- AC-8: permit-gated + decommission liability, inherited not reimplemented ----

func TestRefineryPermitDecommissionInheritance(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	refinery, _ := r.Facility("refinery")

	permit := &stubPermit{granted: false}
	decom := &stubDecom{liabilities: map[string]int64{}}
	if err := r.WirePermit(permit); err != nil {
		t.Fatalf("wire permit: %v", err)
	}
	if err := r.WireDecommission(decom); err != nil {
		t.Fatalf("wire decommission: %v", err)
	}

	// Permit denied: no build, no liability.
	if err := r.Build(9); !errors.Is(err, &errs.E{Code: ErrRefineryBuildRejected}) {
		t.Fatalf("expected build rejection, got %v", err)
	}
	if built, err := r.Built(); err != nil {
		t.Fatalf("built (denied): %v", err)
	} else if built {
		t.Fatal("refinery built despite a denied permit")
	}
	if decom.liability("refinery") != 0 {
		t.Fatalf("decommission liability recorded despite a denied permit: %d", decom.liability("refinery"))
	}

	// Permit granted: build records the day-one decommission liability (capex).
	permit.granted = true
	if err := r.Build(9); err != nil {
		t.Fatalf("build (granted): %v", err)
	}
	if built, err := r.Built(); err != nil {
		t.Fatalf("built (granted): %v", err)
	} else if !built {
		t.Fatal("refinery not built despite a granted permit")
	}
	if got := decom.liability("refinery"); got != refinery.CapexMicropounds {
		t.Fatalf("decommission liability %d != capex %d", got, refinery.CapexMicropounds)
	}
}

func TestRefineryPermitDecommissionRequiresSeams(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	// No permit seam wired: build is rejected as not-wired, no partial state.
	if err := r.Build(9); !errors.Is(err, &errs.E{Code: ErrRefineryNotWired}) {
		t.Fatalf("expected not-wired (permit), got %v", err)
	}
	if built, err := r.Built(); err != nil {
		t.Fatalf("built (no permit): %v", err)
	} else if built {
		t.Fatal("refinery built with no permit seam")
	}

	// Permit wired, decommission missing: still rejected, still not built.
	if err := r.WirePermit(&stubPermit{granted: true}); err != nil {
		t.Fatalf("wire permit: %v", err)
	}
	if err := r.Build(9); !errors.Is(err, &errs.E{Code: ErrRefineryNotWired}) {
		t.Fatalf("expected not-wired (decommission), got %v", err)
	}
	if built, err := r.Built(); err != nil {
		t.Fatalf("built (no decommission): %v", err)
	} else if built {
		t.Fatal("refinery built with no decommission seam")
	}
}

// ---- AC-9: registry-sourced errors and no partial state ----

func TestUnknownRefineryFacilityRejected(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	_, err := r.Facility("nonexistent")
	if !errors.Is(err, &errs.E{Code: ErrUnknownRefineryFacility}) {
		t.Fatalf("expected ErrUnknownRefineryFacility, got %v", err)
	}
}

func TestMalformedRefineryDataRejected(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "missing throughput",
			mutate: func(m map[string]any) {
				delete(m["facilities"].(map[string]any)["refinery"].(map[string]any), "throughputTonnesPerDay")
			},
		},
		{
			name: "negative utility draw",
			mutate: func(m map[string]any) {
				m["facilities"].(map[string]any)["refinery"].(map[string]any)["waterLitresPerDay"] = float64(-1)
			},
		},
		{
			name: "unrecognised stage name",
			mutate: func(m map[string]any) {
				f := m["facilities"].(map[string]any)
				f["cracker"] = f["refinery"]
				delete(f, "refinery")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeRefineryFixture(t, tc.mutate)
			r, err := LoadRefinery(dir, errs.NewCorrelationID(), seedA)
			if !errors.Is(err, &errs.E{Code: ErrRefineryDataInvalid}) {
				t.Fatalf("expected ErrRefineryDataInvalid, got %v", err)
			}
			if r != nil {
				t.Fatal("a malformed data file must not produce a facility (no partial state)")
			}
		})
	}
}

func TestRefineryRejectsOperationBeforeBuild(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	fuel := &stubFuel{}
	wireOperate(t, r, &stubFreight{capT: -1}, fuel, &stubDispatch{})

	_, err := r.Operate(0, 1000)
	if !errors.Is(err, &errs.E{Code: ErrRefineryNotBuilt}) {
		t.Fatalf("expected ErrRefineryNotBuilt, got %v", err)
	}
	if fuel.SupplyTonnes() != 0 {
		t.Fatalf("unbuilt refinery emitted fuel: %d", fuel.SupplyTonnes())
	}
}

func TestRefineryRejectsUnregisteredStage(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	chem, err := r.Chem()
	if err != nil {
		t.Fatalf("chem: %v", err)
	}
	_, err = chem.StageInput("nonexistent_stage")
	if !errors.Is(err, &errs.E{Code: ErrUnregisteredStage}) {
		t.Fatalf("expected ErrUnregisteredStage, got %v", err)
	}
}

// ---- AC-10: determinism ----

func runSequence(t *testing.T, seed uint64) []OperateResult {
	t.Helper()
	r := loadRealRefinery(t, seed)
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	wireOperate(t, r, &stubFreight{capT: -1}, &stubFuel{}, &stubDispatch{})
	refinery, _ := r.Facility("refinery")

	out := make([]OperateResult, 0, 32)
	for tick := int64(0); tick < 32; tick++ {
		res, err := r.Operate(tick, refinery.ThroughputTonnesPerDay)
		if err != nil {
			t.Fatalf("operate tick %d: %v", tick, err)
		}
		out = append(out, res)
	}
	return out
}

func TestRefineryDeterminism(t *testing.T) {
	// Identical seed => byte-identical stage/tonnage/fuel/risk state.
	a := runSequence(t, seedA)
	b := runSequence(t, seedA)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("identical seed produced different state:\n%+v\nvs\n%+v", a, b)
	}

	// Different seed => measurably different hazard-risk outcome set.
	ra := loadRealRefinery(t, seedA)
	rb := loadRealRefinery(t, seedB)
	different := false
	for tick := int64(0); tick < 64; tick++ {
		if ra.HazmatRisk(tick) != rb.HazmatRisk(tick) {
			different = true
			break
		}
	}
	if !different {
		t.Fatal("different seeds produced identical hazard-risk draws")
	}
}

// ---- AC-12: concurrent access is race-free (verified by -race) ----

func TestRefineryConcurrentOperate(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	wireOperate(t, r, &stubFreight{capT: -1}, &stubFuel{}, &stubDispatch{})
	refinery, _ := r.Facility("refinery")

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := int64(0); i < 32; i++ {
				_, _ = r.Operate(i, refinery.ThroughputTonnesPerDay)
				_, _ = r.Facilities()
				_, _ = r.Built()
				_ = r.HazmatRisk(i)
			}
		}()
	}
	wg.Wait()
}
