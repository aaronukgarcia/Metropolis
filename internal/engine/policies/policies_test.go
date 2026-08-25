package policies

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// recordingProjections implements projectionSeam, recording every
// EnqueueDecision and serving a flat base curve so previews resolve without
// a real provider. It is the seam AC-4 uses to capture the exact payload.
type recordingProjections struct {
	mu        sync.Mutex
	decisions []projections.Decision
	horizon   int64
	base      float64

	// setCurrentMonth records every SetCurrentMonth call so a test can prove
	// a rejected preview never moves the seam's current month (GR#12).
	setCurrentMonth []int64
}

func (r *recordingProjections) EnqueueDecision(d projections.Decision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.decisions = append(r.decisions, d)
	return nil
}

func (r *recordingProjections) CancelDecision(string) error { return nil }

func (r *recordingProjections) Curve(key string, from, to int64) ([]projections.Point, error) {
	pts := make([]projections.Point, 0, to-from+1)
	for m := from; m <= to; m++ {
		pts = append(pts, projections.Point{Month: m, Value: r.base, Confidence: projections.ConfidenceComputed})
	}
	return pts, nil
}

func (r *recordingProjections) HorizonMonths() (int64, error) { return r.horizon, nil }

func (r *recordingProjections) SetCurrentMonth(m int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setCurrentMonth = append(r.setCurrentMonth, m)
	return nil
}

// deltasWithPrefix returns the (Key, Delta) payload of every recorded
// decision whose ID starts with prefix, in record order.
func (r *recordingProjections) deltasWithPrefix(prefix string) []CoefficientDelta {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []CoefficientDelta
	for _, d := range r.decisions {
		if len(d.ID) >= len(prefix) && d.ID[:len(prefix)] == prefix {
			out = append(out, CoefficientDelta{Key: d.CurveKey, Delta: d.Delta})
		}
	}
	return out
}

// recordingFinance implements financeSeam, recording every Post.
type recordingFinance struct {
	mu   sync.Mutex
	txns []finance.Transaction
}

func (r *recordingFinance) Post(tx finance.Transaction) (finance.TxID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.txns = append(r.txns, tx)
	return finance.TxID(len(r.txns)), nil
}

func (r *recordingFinance) debitTotal() finance.Money {
	r.mu.Lock()
	defer r.mu.Unlock()
	var total finance.Money
	for _, tx := range r.txns {
		for _, e := range tx.Entries {
			if e.Side == finance.SideDebit {
				total += e.Amount
			}
		}
	}
	return total
}

func (r *recordingFinance) postCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.txns)
}

// recordingTax implements taxSeam, recording every SetDistrictMultiplier and
// holding the applied multiplier per (district, instrument) so
// GetDistrictMultiplier reads back the real applied state — exactly what the
// production *tax.TaxAPI does. A test can also call SetDistrictMultiplier
// directly on it to simulate an out-of-band mutation of the tax state.
type recordingTax struct {
	mu    sync.Mutex
	calls []recordedTaxCall
	state map[recordedTaxKey]float64
}

type recordedTaxCall struct {
	district   tax.DistrictID
	instrument string
	multiplier float64
}

type recordedTaxKey struct {
	district   tax.DistrictID
	instrument string
}

func (r *recordingTax) SetDistrictMultiplier(d tax.DistrictID, inst string, m float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedTaxCall{district: d, instrument: inst, multiplier: m})
	if r.state == nil {
		r.state = make(map[recordedTaxKey]float64)
	}
	r.state[recordedTaxKey{district: d, instrument: inst}] = m
	return nil
}

func (r *recordingTax) GetDistrictMultiplier(d tax.DistrictID, inst string) (float64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.state[recordedTaxKey{district: d, instrument: inst}]; ok {
		return m, nil
	}
	return 1.0, nil // never touched: neutral
}

// mutableProvider is a stateful projections.CurveProvider whose value a test
// can change between enactment and checkpoint, to simulate the real world
// diverging from a stored preview (AC-7).
type mutableProvider struct {
	mu    sync.Mutex
	value float64
}

func (m *mutableProvider) Value(int64) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.value, nil
}

func (m *mutableProvider) set(v float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.value = v
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func testAPI(t *testing.T) *PoliciesAPI {
	t.Helper()
	a := NewPoliciesAPI("test-correlation")
	a.meta = policiesMeta{
		Combination:  combinationMultiplicative,
		PreviewDrift: previewDrift{Tolerance: 0.10, CheckpointMonths: 3},
	}
	return a
}

func addPolicy(t *testing.T, a *PoliciesAPI, def *policyDef) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.library[def.ID] = def
}

func simplePolicy(id PolicyID, scope ScopeKind, key string, delta float64) *policyDef {
	return &policyDef{
		ID:        id,
		Name:      string(id),
		Category:  "economy",
		Scope:     scope,
		Mechanism: []CoefficientDelta{{Key: key, Delta: delta}},
		Cost:      CostDef{},
		Conflicts: nil,
	}
}

func mustEnact(t *testing.T, a *PoliciesAPI, id PolicyID, scope Scope) EnactmentID {
	t.Helper()
	eid, err := a.Enact(id, scope)
	if err != nil {
		t.Fatalf("Enact(%q): %v", id, err)
	}
	return eid
}

func cells(rows ...int) []CellRef {
	out := make([]CellRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, CellRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: r, Col: 0}})
	}
	return out
}

// floatApprox is a tolerance-aware float comparison for assertions about
// values derived through floating-point arithmetic (multiplicative
// combination, divergence), where exact equality would be over-precise.
func floatApprox(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// ---------------------------------------------------------------------------
// AC-1 — library / enact / scope-resolve surfaces
// ---------------------------------------------------------------------------

func TestPolicyLibrary(t *testing.T) {
	a := testAPI(t)
	addPolicy(t, a, simplePolicy("freeport", ScopeDistrict, "tax.businessRates", -1.0))
	addPolicy(t, a, simplePolicy("cycling", ScopeCitywide, "movement.cycling.share", 0.15))

	all := a.Policies()
	if len(all) != 2 {
		t.Fatalf("want 2 policies, got %d", len(all))
	}
	// Deterministic sorted order.
	if all[0].ID != "cycling" || all[1].ID != "freeport" {
		t.Fatalf("Policies() not sorted: %v %v", all[0].ID, all[1].ID)
	}
	info, err := a.Policy("freeport")
	if err != nil {
		t.Fatalf("Policy: %v", err)
	}
	if info.Name != "freeport" || len(info.Mechanism) != 1 || info.Mechanism[0].Key != "tax.businessRates" {
		t.Fatalf("unexpected PolicyInfo: %+v", info)
	}
}

func TestEnactAndRepeal(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	addPolicy(t, a, simplePolicy("cycling", ScopeCitywide, "movement.cycling.share", 0.15))

	eid := mustEnact(t, a, "cycling", Scope{Kind: ScopeCitywide})
	if len(a.CoefficientState()) != 1 {
		t.Fatalf("want 1 active coefficient, got %d", len(a.CoefficientState()))
	}
	if err := a.Repeal(eid); err != nil {
		t.Fatalf("Repeal: %v", err)
	}
	if len(a.CoefficientState()) != 0 {
		t.Fatalf("want 0 active coefficients after repeal")
	}
	if err := a.Repeal(eid); err == nil {
		t.Fatal("Repeal of unknown enactment must error")
	} else if !errors.Is(err, &errs.E{Code: ErrEnactmentNotFound}) {
		t.Fatalf("want ErrEnactmentNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// AC-3 — single-field mechanism-vs-mutation diff
// ---------------------------------------------------------------------------

func TestMechanismDiffSingleField(t *testing.T) {
	a := testAPI(t)
	rec := &recordingProjections{horizon: 72}
	a.projections = rec
	addPolicy(t, a, simplePolicy("single", ScopeCitywide, "wellbeing.parkAccess", 0.20))

	before := a.CoefficientState()
	if len(before) != 0 {
		t.Fatalf("pre-enactment state should be empty, got %+v", before)
	}

	mustEnact(t, a, "single", Scope{Kind: ScopeCitywide})

	after := a.CoefficientState()
	if len(after) != 1 {
		t.Fatalf("field-by-field diff must show exactly one changed coefficient, got %d: %+v", len(after), after)
	}
	if after[0].Key != "wellbeing.parkAccess" || !floatApprox(after[0].Delta, 0.20) {
		t.Fatalf("only the declared coefficient must change, got %+v", after[0])
	}
	// The projections payload must touch only the declared key too.
	perm := rec.deltasWithPrefix("enactment-")
	if len(perm) != 1 || perm[0].Key != "wellbeing.parkAccess" {
		t.Fatalf("projections must receive exactly the declared coefficient, got %+v", perm)
	}
}

// ---------------------------------------------------------------------------
// AC-4 — the preview and the enactment feed the identical payload
// ---------------------------------------------------------------------------

func TestPreviewMatchesEnact(t *testing.T) {
	def := simplePolicy("freeport", ScopeCitywide, "tax.businessRates", -1.0)
	def.Mechanism = []CoefficientDelta{
		{Key: "tax.businessRates", Delta: -1.0},
		{Key: "economy.smuggling", Delta: 0.05},
	}
	sort.Slice(def.Mechanism, func(i, j int) bool { return def.Mechanism[i].Key < def.Mechanism[j].Key })

	previewAPI := testAPI(t)
	previewRec := &recordingProjections{horizon: 72}
	previewAPI.projections = previewRec
	addPolicy(t, previewAPI, def)
	if _, err := previewAPI.PreviewImpact("freeport", Scope{Kind: ScopeCitywide}); err != nil {
		t.Fatalf("PreviewImpact: %v", err)
	}
	previewPayload := previewRec.deltasWithPrefix("preview:")

	enactAPI := testAPI(t)
	enactRec := &recordingProjections{horizon: 72}
	enactAPI.projections = enactRec
	addPolicy(t, enactAPI, def)
	mustEnact(t, enactAPI, "freeport", Scope{Kind: ScopeCitywide})
	enactPayload := enactRec.deltasWithPrefix("enactment-")

	if len(previewPayload) != len(enactPayload) {
		t.Fatalf("payload lengths differ: preview=%d enact=%d", len(previewPayload), len(enactPayload))
	}
	for i := range previewPayload {
		if previewPayload[i] != enactPayload[i] {
			t.Fatalf("payload[%d] differs: preview=%+v enact=%+v", i, previewPayload[i], enactPayload[i])
		}
	}
}

// ---------------------------------------------------------------------------
// AC-5 — beyond-horizon preview is never tagged Computed
// ---------------------------------------------------------------------------

func TestBeyondHorizonPreview(t *testing.T) {
	proj := projections.NewProjectionsAPI(projections.WithCorrelationID("t"))
	if err := proj.RegisterCurveProvider("movement.cycling.share", projections.CurveProviderFunc(func(int64) (float64, error) { return 0.3, nil })); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	if err := proj.SetCurrentMonth(0); err != nil {
		t.Fatalf("SetCurrentMonth: %v", err)
	}
	horizon, err := proj.HorizonMonths()
	if err != nil {
		t.Fatalf("HorizonMonths: %v", err)
	}

	a := testAPI(t)
	a.projections = proj
	addPolicy(t, a, simplePolicy("cycling", ScopeCitywide, "movement.cycling.share", 0.15))

	preview, err := a.PreviewImpactRange("cycling", Scope{Kind: ScopeCitywide}, horizon+5)
	if err != nil {
		t.Fatalf("PreviewImpactRange: %v", err)
	}
	if len(preview.Series) != 1 {
		t.Fatalf("want 1 series, got %d", len(preview.Series))
	}
	pts := preview.Series[0].Points
	for _, p := range pts {
		if p.Month > horizon && p.Confidence == projections.ConfidenceComputed {
			t.Fatalf("point at month %d (beyond horizon %d) must not be Computed", p.Month, horizon)
		}
	}
}

// ---------------------------------------------------------------------------
// AC-7 — PreviewDrift fires (and stays silent within tolerance)
// ---------------------------------------------------------------------------

func TestPreviewDriftFires(t *testing.T) {
	proj := projections.NewProjectionsAPI(projections.WithCorrelationID("t"))
	base := &mutableProvider{value: 100}
	if err := proj.RegisterCurveProvider("wellbeing.parkAccess", projections.CurveProviderFunc(base.Value)); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	if err := proj.SetCurrentMonth(0); err != nil {
		t.Fatalf("SetCurrentMonth: %v", err)
	}

	a := testAPI(t)
	a.projections = proj
	addPolicy(t, a, simplePolicy("parkAccess", ScopeCitywide, "wellbeing.parkAccess", 10))

	mustEnact(t, a, "parkAccess", Scope{Kind: ScopeCitywide})

	// The world diverges: the provider moves from 100 to 200, so the
	// observed value (200+10) is ~91% off the stored preview (100+10).
	base.set(200)

	events, err := a.Checkpoint(3)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected a PreviewDrift event to fire")
	}
	ev := events[0]
	if ev.PolicyID != "parkAccess" || ev.Coefficient != "wellbeing.parkAccess" || ev.Checkpoint != 3 {
		t.Fatalf("event missing policy/coefficient/checkpoint fields: %+v", ev)
	}
	if ev.Previewed != 110 || ev.Actual != 210 {
		t.Fatalf("unexpected previewed/actual: %+v", ev)
	}
	if ev.Magnitude <= a.meta.PreviewDrift.Tolerance {
		t.Fatalf("magnitude must exceed tolerance: %+v", ev)
	}
	// Queryable, accumulated.
	if got := a.PreviewDriftEvents(); len(got) != 1 {
		t.Fatalf("PreviewDriftEvents should accumulate the raised event, got %d", len(got))
	}
}

func TestPreviewDriftWithinToleranceNoEvent(t *testing.T) {
	proj := projections.NewProjectionsAPI(projections.WithCorrelationID("t"))
	if err := proj.RegisterCurveProvider("wellbeing.parkAccess", projections.CurveProviderFunc(func(int64) (float64, error) { return 100, nil })); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	if err := proj.SetCurrentMonth(0); err != nil {
		t.Fatalf("SetCurrentMonth: %v", err)
	}

	a := testAPI(t)
	a.projections = proj
	addPolicy(t, a, simplePolicy("parkAccess", ScopeCitywide, "wellbeing.parkAccess", 10))

	mustEnact(t, a, "parkAccess", Scope{Kind: ScopeCitywide})

	events, err := a.Checkpoint(3)
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no drift event within tolerance, got %+v", events)
	}
}

// ---------------------------------------------------------------------------
// AC-8 / AC-12 — districts: create, rename, name query
// ---------------------------------------------------------------------------

func TestCreateDistrictAndRename(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	id, err := a.CreateDistrict("CBD", cells(1, 2, 3))
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}
	// Enact a district-scoped policy on it before renaming.
	addPolicy(t, a, simplePolicy("parkAccess", ScopeDistrict, "wellbeing.parkAccess", 0.20))
	mustEnact(t, a, "parkAccess", Scope{Kind: ScopeDistrict, District: id})

	if err := a.RenameDistrict(id, "Central Business District"); err != nil {
		t.Fatalf("RenameDistrict: %v", err)
	}
	info, err := a.District(id)
	if err != nil {
		t.Fatalf("District: %v", err)
	}
	if info.ID != id {
		t.Fatalf("rename must preserve DistrictID: got %q want %q", info.ID, id)
	}
	if len(info.Cells) != 3 {
		t.Fatalf("rename must preserve cell set: got %d cells", len(info.Cells))
	}
	// The renamed district's scoped policy must still resolve against the
	// same DistrictID (scoping keys on the ID, never the name).
	res, err := a.ResolveScope("parkAccess", Scope{Kind: ScopeDistrict, District: id})
	if err != nil {
		t.Fatalf("ResolveScope after rename: %v", err)
	}
	if len(res.Cells) != 3 {
		t.Fatalf("rename must preserve the district's scoped policy resolution: got %d cells", len(res.Cells))
	}
}

func TestDistrictName(t *testing.T) {
	a := testAPI(t)
	id, err := a.CreateDistrict("Old Name", cells(1))
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}
	if err := a.RenameDistrict(id, "New Name"); err != nil {
		t.Fatalf("RenameDistrict: %v", err)
	}
	info, err := a.District(id)
	if err != nil {
		t.Fatalf("District: %v", err)
	}
	if info.Name != "New Name" {
		t.Fatalf("renamed district's Name must be immediately visible, got %q", info.Name)
	}
}

// ---------------------------------------------------------------------------
// AC-9 — scope resolution
// ---------------------------------------------------------------------------

func TestResolveScope(t *testing.T) {
	a := testAPI(t)
	districtID, err := a.CreateDistrict("CBD", cells(1, 2))
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}
	roadEdges := []EdgeRef{
		{From: CellRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 1, Col: 0}}, To: CellRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 2, Col: 0}}},
	}
	if err := a.RegisterRoad("high.street", roadEdges); err != nil {
		t.Fatalf("RegisterRoad: %v", err)
	}

	addPolicy(t, a, simplePolicy("citywide", ScopeCitywide, "k", 0.1))
	addPolicy(t, a, simplePolicy("dist", ScopeDistrict, "k", 0.1))
	addPolicy(t, a, simplePolicy("road", ScopeRoad, "k", 0.1))

	city, err := a.ResolveScope("citywide", Scope{Kind: ScopeCitywide})
	if err != nil {
		t.Fatalf("ResolveScope citywide: %v", err)
	}
	if !city.Citywide {
		t.Fatal("citywide scope must resolve to the whole city")
	}

	dist, err := a.ResolveScope("dist", Scope{Kind: ScopeDistrict, District: districtID})
	if err != nil {
		t.Fatalf("ResolveScope district: %v", err)
	}
	if dist.District != districtID || len(dist.Cells) != 2 {
		t.Fatalf("district scope must resolve to the district's cell set: %+v", dist)
	}

	road, err := a.ResolveScope("road", Scope{Kind: ScopeRoad, Road: "high.street"})
	if err != nil {
		t.Fatalf("ResolveScope road: %v", err)
	}
	if road.Road != "high.street" || len(road.Edges) != 1 {
		t.Fatalf("road scope must resolve to the road's edge set: %+v", road)
	}
}

// ---------------------------------------------------------------------------
// AC-10 — compounding
// ---------------------------------------------------------------------------

func TestCompoundEffect(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	pa := simplePolicy("a", ScopeCitywide, "economy.wage.level", 0.10)
	pb := simplePolicy("b", ScopeCitywide, "economy.wage.level", 0.20)
	addPolicy(t, a, pa)
	addPolicy(t, a, pb)

	mustEnact(t, a, "a", Scope{Kind: ScopeCitywide})
	mustEnact(t, a, "b", Scope{Kind: ScopeCitywide})

	combined, err := a.CombinedEffect("economy.wage.level", Scope{Kind: ScopeCitywide})
	if err != nil {
		t.Fatalf("CombinedEffect: %v", err)
	}
	naiveSum := 0.10 + 0.20
	if combined == naiveSum {
		t.Fatalf("combined effect must differ from the naive sum (%v)", naiveSum)
	}
	// Multiplicative: (1.1 * 1.2) - 1 = 0.32.
	want := 0.32
	if !floatApprox(combined, want) {
		t.Fatalf("combined = %v, want %v", combined, want)
	}
}

// writeClampFixture writes a minimal valid data/policies.json with the given
// data-declared maxCombinedAbsDelta bound and a single citywide policy whose
// delta produces a raw multiplicative product of (1+delta)−1 = delta, so a
// delta magnitude above the bound exercises the clamp, returning the temp dir.
func writeClampFixture(t *testing.T, bound, delta float64) string {
	t.Helper()
	dir := t.TempDir()
	body := fmt.Sprintf(`{
  "version": 1,
  "meta": {
    "categories": ["economy"],
    "combination": "multiplicative",
    "maxCombinedAbsDelta": %v,
    "previewDrift": {"tolerance": 0.1, "checkpointMonths": 3}
  },
  "entries": [
    {"key": "boostA", "name": "Boost A", "category": "economy", "scope": "citywide",
     "mechanism": {"coefficients": [{"key": "economy.wage.level", "delta": %v}]},
     "cost": {"enactmentMicroPounds": 0, "opexMonthlyMicroPounds": 0},
     "conflictsWith": [], "disclosure": "clamp test fixture"}
  ]
}`, bound, delta)
	if err := os.WriteFile(filepath.Join(dir, data.FilePolicies), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

// loadClampFixture loads the fixture written by [writeClampFixture] and wires
// the projections seam so Enact/CombinedEffect run.
func loadClampFixture(t *testing.T, dir string) *PoliciesAPI {
	t.Helper()
	a, err := Load(dir, "clamp-test")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a.projections = &recordingProjections{horizon: 72}
	return a
}

// TestCombinedEffectClamped (regression, routed finding): a raw multiplicative
// product that exceeds the data-declared meta.maxCombinedAbsDelta bound is
// CLAMPED to exactly ±bound — never the unbounded raw product, never a Go
// literal (GR#15).
func TestCombinedEffectClamped(t *testing.T) {
	// Positive side: single +3.0 delta, raw product +3.0, bound 2.0 → +2.0.
	a := loadClampFixture(t, writeClampFixture(t, 2.0, 3.0))
	mustEnact(t, a, "boostA", Scope{Kind: ScopeCitywide})
	combined, err := a.CombinedEffect("economy.wage.level", Scope{Kind: ScopeCitywide})
	if err != nil {
		t.Fatalf("CombinedEffect: %v", err)
	}
	if combined != 2.0 {
		t.Fatalf("combined must clamp to exactly +bound 2.0, got %v", combined)
	}
	if floatApprox(combined, 3.0) {
		t.Fatalf("clamp must not return the raw unbounded product 3.0")
	}

	// Negative side: single −3.0 delta, raw product −3.0, bound 2.0 → −2.0.
	b := loadClampFixture(t, writeClampFixture(t, 2.0, -3.0))
	mustEnact(t, b, "boostA", Scope{Kind: ScopeCitywide})
	combinedNeg, err := b.CombinedEffect("economy.wage.level", Scope{Kind: ScopeCitywide})
	if err != nil {
		t.Fatalf("CombinedEffect (negative): %v", err)
	}
	if combinedNeg != -2.0 {
		t.Fatalf("combined must clamp to exactly −bound −2.0, got %v", combinedNeg)
	}
	if floatApprox(combinedNeg, -3.0) {
		t.Fatalf("clamp must not return the raw unbounded product −3.0")
	}
}

// TestCombinedEffectClampMovesWithData (data-driven proof, GR#15): the clamp
// bound is read from the loaded meta, so changing the data-declared
// maxCombinedAbsDelta moves the clamp — the engine never hardcodes the bound.
func TestCombinedEffectClampMovesWithData(t *testing.T) {
	// Same delta (+3.0) under two different data-declared bounds: the clamp
	// must follow the data, not a literal.
	a := loadClampFixture(t, writeClampFixture(t, 0.5, 3.0))
	mustEnact(t, a, "boostA", Scope{Kind: ScopeCitywide})
	combined, err := a.CombinedEffect("economy.wage.level", Scope{Kind: ScopeCitywide})
	if err != nil {
		t.Fatalf("CombinedEffect: %v", err)
	}
	if combined != 0.5 {
		t.Fatalf("clamp must follow the loaded bound 0.5, got %v", combined)
	}

	b := loadClampFixture(t, writeClampFixture(t, 1.5, 3.0))
	mustEnact(t, b, "boostA", Scope{Kind: ScopeCitywide})
	combined2, err := b.CombinedEffect("economy.wage.level", Scope{Kind: ScopeCitywide})
	if err != nil {
		t.Fatalf("CombinedEffect (bound 1.5): %v", err)
	}
	if combined2 != 1.5 {
		t.Fatalf("clamp must move with the data to bound 1.5, got %v", combined2)
	}
}

// ---------------------------------------------------------------------------
// AC-11 — conflict warnings are queryable and non-blocking
// ---------------------------------------------------------------------------

func TestConflictWarn(t *testing.T) {
	api := testAPI(t)
	api.projections = &recordingProjections{horizon: 72}

	first := simplePolicy("congestionCharge", ScopeCitywide, "movement.car.share", -0.10)
	second := simplePolicy("lowEmissionZone", ScopeCitywide, "movement.car.share", -0.20)
	second.Conflicts = []PolicyID{"congestionCharge"}
	addPolicy(t, api, first)
	addPolicy(t, api, second)

	if _, err := api.Enact("congestionCharge", Scope{Kind: ScopeCitywide}); err != nil {
		t.Fatalf("Enact first: %v", err)
	}
	eid, err := api.Enact("lowEmissionZone", Scope{Kind: ScopeCitywide})
	if err != nil {
		t.Fatalf("Enact conflicting second: %v", err)
	}

	// The second policy is active, not rejected (AC-11).
	if len(api.CoefficientState()) == 0 {
		t.Fatal("second policy must still be active")
	}
	_ = eid

	warnings := api.Conflicts()
	if len(warnings) != 1 {
		t.Fatalf("want 1 conflict warning, got %d", len(warnings))
	}
	if warnings[0].EnactedPolicy != "lowEmissionZone" || warnings[0].ConflictWith != "congestionCharge" {
		t.Fatalf("unexpected warning: %+v", warnings[0])
	}
}

// ---------------------------------------------------------------------------
// AC-13 — registry-sourced errors, never a silent no-op / empty-set success
// ---------------------------------------------------------------------------

func TestEnactAlreadyActiveErrors(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	addPolicy(t, a, simplePolicy("cycling", ScopeCitywide, "movement.cycling.share", 0.15))

	mustEnact(t, a, "cycling", Scope{Kind: ScopeCitywide})
	_, err := a.Enact("cycling", Scope{Kind: ScopeCitywide})
	if err == nil {
		t.Fatal("re-enacting an identical-scope policy must error")
	}
	if !errors.Is(err, &errs.E{Code: ErrPolicyAlreadyActive}) {
		t.Fatalf("want ErrPolicyAlreadyActive, got %v", err)
	}
	if len(a.CoefficientState()) != 1 {
		t.Fatalf("re-enactment must be a no-op on state: got %d coefficients", len(a.CoefficientState()))
	}
}

func TestUnknownScopeErrors(t *testing.T) {
	a := testAPI(t)
	addPolicy(t, a, simplePolicy("dist", ScopeDistrict, "k", 0.1))

	_, err := a.ResolveScope("dist", Scope{Kind: ScopeDistrict, District: "does-not-exist"})
	if err == nil {
		t.Fatal("resolving an unknown district must error, not resolve to an empty set")
	}
	if !errors.Is(err, &errs.E{Code: ErrUnknownScope}) {
		t.Fatalf("want ErrUnknownScope, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// AC-14 — deterministic evaluation order
// ---------------------------------------------------------------------------

func TestDeterministicEvaluationOrder(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	addPolicy(t, a, simplePolicy("z", ScopeCitywide, "shared", 0.10))
	addPolicy(t, a, simplePolicy("a", ScopeCitywide, "shared", 0.20))
	addPolicy(t, a, simplePolicy("m", ScopeCitywide, "shared", 0.05))

	for _, id := range []PolicyID{"z", "a", "m"} {
		mustEnact(t, a, id, Scope{Kind: ScopeCitywide})
	}

	first, err := a.CombinedEffect("shared", Scope{Kind: ScopeCitywide})
	if err != nil {
		t.Fatalf("CombinedEffect: %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := a.CombinedEffect("shared", Scope{Kind: ScopeCitywide})
		if err != nil {
			t.Fatalf("CombinedEffect: %v", err)
		}
		if got != first {
			t.Fatalf("non-deterministic combined effect: %v != %v", got, first)
		}
	}
}

// ---------------------------------------------------------------------------
// AC-19 — enactment cost and recurring enforcement opex through finance
// ---------------------------------------------------------------------------

func TestEnactmentCost(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	fin := &recordingFinance{}
	a.finance = fin

	def := simplePolicy("freeport", ScopeCitywide, "tax.businessRates", -1.0)
	def.Cost = CostDef{EnactmentMicroPounds: 5_000_000}
	addPolicy(t, a, def)

	mustEnact(t, a, "freeport", Scope{Kind: ScopeCitywide})

	if got := fin.debitTotal(); got != finance.Money(5_000_000) {
		t.Fatalf("enactment must debit exactly the declared cost, got %d", got)
	}
}

func TestEnforcementOpex(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	fin := &recordingFinance{}
	a.finance = fin

	def := simplePolicy("freeport", ScopeCitywide, "tax.businessRates", -1.0)
	def.Cost = CostDef{OpexMonthlyMicroPounds: 50_000}
	addPolicy(t, a, def)

	mustEnact(t, a, "freeport", Scope{Kind: ScopeCitywide})
	before := fin.postCount()

	if _, err := a.AdvanceMonth(1); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	if fin.postCount() != before+1 {
		t.Fatalf("enforcement opex must post once per month, not once ever")
	}
	if got := fin.debitTotal(); got != finance.Money(50_000) {
		t.Fatalf("recurring opex must debit the declared monthly amount, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Tax routing — a freeport suppresses its district's business rates via
// engine.tax's TaxAPI (data-declared, never a second tax implementation)
// ---------------------------------------------------------------------------

func TestTaxMoveRoutesThroughTaxAPI(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	rec := &recordingTax{}
	a.tax = rec

	districtID, err := a.CreateDistrict("Harbour", cells(1))
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}
	def := &policyDef{
		ID:        "freeport",
		Name:      "Tax-Free Harbour",
		Category:  "economy",
		Scope:     ScopeDistrict,
		Mechanism: []CoefficientDelta{{Key: "tax.businessRates.districtMultiplier", Delta: -1.0, Tax: &TaxMove{Instrument: "business-rates", Mode: taxMoveDistrictMultiplier}}},
	}
	addPolicy(t, a, def)

	mustEnact(t, a, "freeport", Scope{Kind: ScopeDistrict, District: districtID})

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) != 1 {
		t.Fatalf("want 1 tax move, got %d", len(rec.calls))
	}
	c := rec.calls[0]
	if c.district != tax.DistrictID(districtID) || c.instrument != "business-rates" || c.multiplier != 0.0 {
		t.Fatalf("unexpected tax move: %+v", c)
	}
}

// ---------------------------------------------------------------------------
// The real data/policies.json loads and covers all four categories
// ---------------------------------------------------------------------------

func TestLibraryLoadsFromData(t *testing.T) {
	a, err := LoadDefault("test-correlation")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	categories := map[string]bool{}
	seen := map[PolicyID]bool{}
	for _, p := range a.Policies() {
		categories[p.Category] = true
		seen[p.ID] = true
	}
	for _, want := range []string{"movement", "layout & wellbeing", "economy", "social"} {
		if !categories[want] {
			t.Fatalf("category %q not represented in data/policies.json", want)
		}
	}
	for _, want := range []PolicyID{"cyclePriorityNetwork", "freeportTaxFreeHarbour"} {
		if !seen[want] {
			t.Fatalf("expected policy %q in the data library", want)
		}
	}
}
