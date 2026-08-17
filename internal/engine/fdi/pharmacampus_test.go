package fdi

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/firms"
	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This test file is the feat.pharmacampus (FEAT-101) regression suite. Test
// names are chosen to match the acceptance doc's own grep patterns (AC-1
// through AC-9) AND to all contain "Pharma" so `-run Pharma` (AC-11's scoped
// race suite) selects them.

// fakeEducation is the contract-first test double for the EducationEdge seam
// (ASM-698). It records what the feature emitted so a test can prove demand
// actually reached the edge, and can simulate an unavailable output.
type fakeEducation struct {
	output      int64
	available   bool
	demandErr   error
	removeErr   error
	demands     int64 // count of AddGraduateDemand calls (never decremented)
	demandTotal int64 // net outstanding demand (add minus remove)
	lastDemand  int64
}

func (f *fakeEducation) GraduateOutput() (int64, bool) { return f.output, f.available }

func (f *fakeEducation) AddGraduateDemand(amount int64) error {
	f.demands++
	f.demandTotal += amount
	f.lastDemand = amount
	return f.demandErr
}

func (f *fakeEducation) RemoveGraduateDemand(amount int64) error {
	f.demandTotal -= amount
	return f.removeErr
}

// fakeTrade is the contract-first test double for the TradeEdge seam (AC-6).
// It records what the feature routed so a test can prove exports reached the
// registered trade surface, and can simulate a rejected flow.
type fakeTrade struct {
	exports   int64
	rejectErr error
	removeErr error
}

func (f *fakeTrade) AddExports(tonnes int64) error {
	if f.rejectErr != nil {
		return f.rejectErr
	}
	f.exports += tonnes
	return nil
}

func (f *fakeTrade) RemoveExports(tonnes int64) error {
	f.exports -= tonnes
	return f.removeErr
}

// fakeFirms is the contract-first test double for the FirmsEdge seam (AC-8).
// It models the real *firms.FirmsAPI churn ledger faithfully so a test can
// observe exactly what accounting a rollback leaves behind (SEC-140):
// RegisterFirm increments founded and emits EventFounded; Fail is the §32
// insolvency path (failedCount++, EventFailed); RemoveFirm is the compensating
// inverse (deletes the firm, decrements founded, emits nothing). failOnCall
// lets a test make the Nth RegisterFirm call fail so a supply-chain
// registration failure is reachable.
type fakeFirms struct {
	registered []firms.FirmID
	nextID     uint64
	failOnCall int // 0 = never fail; N = fail on the Nth RegisterFirm call
	calls      int
	failErr    error

	founded int64                  // firms founded (RegisterFirm success count)
	failed  int64                  // firms failed (Fail insolvency count)
	events  []firms.LifecycleEvent // lifecycle events emitted, in order
}

func (f *fakeFirms) RegisterFirm(name string, staff int64, premises string) (freight.Firm, error) {
	f.calls++
	if f.failOnCall != 0 && f.calls == f.failOnCall {
		return freight.Firm{}, f.failErr
	}
	f.nextID++
	id := firms.FirmID(f.nextID)
	f.registered = append(f.registered, id)
	f.founded++
	f.events = append(f.events, firms.LifecycleEvent{Kind: firms.EventFounded, FirmID: id})
	return freight.Firm{ID: uint64(id), Staff: staff, Premises: premises}, nil
}

// RemoveFirm implements the FirmsEdge compensating inverse (SEC-140): it
// deletes the firm, decrements the founded count, and retracts the EventFounded
// RegisterFirm emitted — the exact inverse of RegisterFirm, with no EventFailed
// and no failedCount++.
func (f *fakeFirms) RemoveFirm(id firms.FirmID) error {
	for i, r := range f.registered {
		if r == id {
			f.registered = append(f.registered[:i], f.registered[i+1:]...)
			break
		}
	}
	f.founded--
	f.removeFoundedEvent(id)
	return nil
}

// removeFoundedEvent retracts the EventFounded lifecycle event RegisterFirm
// emitted for id — the inverse of its emit. A true compensating inverse must
// undo the event too, not just the count (SEC-140's "no EventFounded events").
func (f *fakeFirms) removeFoundedEvent(id firms.FirmID) {
	for i, e := range f.events {
		if e.Kind == firms.EventFounded && e.FirmID == id {
			f.events = append(f.events[:i], f.events[i+1:]...)
			return
		}
	}
}

// Fail models the real FirmsAPI's §32 insolvency path — the one Win's rollback
// must NOT reuse (SEC-140): it deletes the firm, increments failed and emits
// EventFailed. It exists on the fake so a test can pin the distinction between
// insolvency and compensating removal; the FirmsEdge seam itself has no Fail.
func (f *fakeFirms) Fail(id firms.FirmID) (firms.Insolvency, error) {
	for i, r := range f.registered {
		if r == id {
			f.registered = append(f.registered[:i], f.registered[i+1:]...)
			break
		}
	}
	f.failed++
	f.events = append(f.events, firms.LifecycleEvent{Kind: firms.EventFailed, FirmID: id})
	return firms.Insolvency{FirmID: id}, nil
}

func (f *fakeFirms) count() int { return len(f.registered) }

// realFirmsEdge adapts the real *firms.FirmsAPI to the FirmsEdge seam for the
// AC-5/AC-6 integration tests that must observe the anchor as a REAL,
// independently retrievable and closeable firm. RegisterFirm and RemoveFirm
// both delegate directly to the real *firms.FirmsAPI — RemoveFirm to the
// genuine compensating inverse (SEC-159), never the §32 insolvency path. The
// failOnCall/failErr fields let a rollback-reproduction test fail the Nth
// RegisterFirm call (a supply-chain registration) while every other call and
// the compensating removal hit the real API.
type realFirmsEdge struct {
	api        *firms.FirmsAPI
	failOnCall int // 0 = never fail; N = fail the Nth RegisterFirm call
	calls      int
	failErr    error
}

func (r *realFirmsEdge) RegisterFirm(name string, staff int64, premises string) (freight.Firm, error) {
	r.calls++
	if r.failOnCall != 0 && r.calls == r.failOnCall {
		return freight.Firm{}, r.failErr
	}
	return r.api.RegisterFirm(name, staff, premises)
}

func (r *realFirmsEdge) RemoveFirm(id firms.FirmID) error {
	return r.api.RemoveFirm(id)
}

// realPharmaPath walks upward from the test cwd to the repo root's
// data/pharmacampus.json (the same resolution idea mining's suite uses).
func realPharmaPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, "data", "pharmacampus.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("data/pharmacampus.json not found walking upward from %s", dir)
		}
		dir = parent
	}
}

// realPharmaCatalogue loads the committed data/pharmacampus.json, proving the
// shipped file is well-formed and giving every test the real, data-sourced
// catalogue (GR#15 — tests never hardcode balance numbers).
func realPharmaCatalogue(t *testing.T) Catalogue {
	t.Helper()
	c, err := LoadPharmaCampus(realPharmaPath(t), cid())
	if err != nil {
		t.Fatalf("load real data/pharmacampus.json: %v", err)
	}
	return c
}

// loadPharmaParams resolves the pharma_r_d_campus profile from the shipped
// data file.
func loadPharmaParams(t *testing.T) PharmaCampusParams {
	t.Helper()
	p, err := realPharmaCatalogue(t).Resolve("pharma_r_d_campus")
	if err != nil {
		t.Fatalf("resolve pharma_r_d_campus: %v", err)
	}
	return p
}

// writeMutatedPharma loads the real data file, lets mutate edit its decoded
// JSON shape, and writes the result to a temp file whose path it returns.
// Used to prove a parameter is actually read (AC-2) and that malformed data
// is rejected with no partial state (AC-8).
func writeMutatedPharma(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	b, err := os.ReadFile(realPharmaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	mutate(m)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "pharmacampus.json")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func cid() string { return errs.NewCorrelationID() }

func assertErrCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", want)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected registry-sourced *errs.E, got %T", err)
	}
	if e.Code != want {
		t.Fatalf("expected error code %s, got %s", want, e.Code)
	}
}

// winningEducationOutput returns an education-output scalar that, given the
// data-sourced bid curve, always clears the off-map competing floor even at
// the worst seeded jitter. It is derived from the data file, never a pinned
// magnitude (AC-3's "directional, not a pinned value").
func winningEducationOutput(params PharmaCampusParams) int64 {
	need := params.Bid.CompetingFloor + params.Bid.JitterMax - params.Bid.QualityBase
	if need < 0 {
		need = 0
	}
	return need/params.Bid.EducationTermPerGraduate + 1
}

// --- AC-1: distinct modelled facility -------------------------------------

func TestPharmaAnchorProfilesDistinct(t *testing.T) {
	cat := realPharmaCatalogue(t)
	pharma, err := cat.Resolve("pharma_r_d_campus")
	if err != nil {
		t.Fatal(err)
	}
	chem, err := cat.Resolve("chemicals_complex")
	if err != nil {
		t.Fatal(err)
	}

	if pharma.JobsCharacter == chem.JobsCharacter {
		t.Fatalf("pharma and chemicals_complex resolve to the same jobs character %s", pharma.JobsCharacter)
	}
	if pharma.UtilityPowerKW == chem.UtilityPowerKW && pharma.UtilityWaterLPD == chem.UtilityWaterLPD {
		t.Fatalf("pharma and chemicals_complex resolve to identical utility draws (no distinct behavioural profile)")
	}
	if pharma.Footprint == chem.Footprint && pharma.Jobs == chem.Jobs && pharma.OutputRate == chem.OutputRate {
		t.Fatalf("pharma and chemicals_complex resolve to identical behavioural profiles")
	}
}

// --- AC-2: data-driven placeholders (GR#15) --------------------------------

func TestPharmaDataDrivenJobs(t *testing.T) {
	before, err := LoadPharmaCampus(realPharmaPath(t), cid())
	if err != nil {
		t.Fatal(err)
	}
	p, err := before.Resolve("pharma_r_d_campus")
	if err != nil {
		t.Fatal(err)
	}

	mutated := writeMutatedPharma(t, func(m map[string]any) {
		anchors := m["anchors"].([]any)
		entry := anchors[0].(map[string]any)
		entry["jobs"] = float64(p.Jobs + 1)
	})
	after, err := LoadPharmaCampus(mutated, cid())
	if err != nil {
		t.Fatal(err)
	}
	q, err := after.Resolve("pharma_r_d_campus")
	if err != nil {
		t.Fatal(err)
	}
	if q.Jobs != p.Jobs+1 {
		t.Fatalf("jobs figure not actually read from data file: before=%d after=%d", p.Jobs, q.Jobs)
	}
}

// --- AC-3: first leg — education output improves the bid, lose reachable ----

func TestPharmaEducationBidFirstLeg(t *testing.T) {
	params := loadPharmaParams(t)
	seed := uint64(7)

	low, err := NewPharma(params, &fakeEducation{available: true, output: 0}, nil, nil, seed)
	if err != nil {
		t.Fatal(err)
	}
	high, err := NewPharma(params, &fakeEducation{available: true, output: winningEducationOutput(params)}, nil, nil, seed)
	if err != nil {
		t.Fatal(err)
	}

	lowOut, err := low.ResolveBid(cid())
	if err != nil {
		t.Fatal(err)
	}
	highOut, err := high.ResolveBid(cid())
	if err != nil {
		t.Fatal(err)
	}

	if highOut.Quality <= lowOut.Quality {
		t.Fatalf("higher education output did not yield a strictly better bid: high=%d low=%d", highOut.Quality, lowOut.Quality)
	}
	if lowOut.Won {
		t.Fatalf("zero-education city won the pharma prospect (lose branch unreachable)")
	}
	if !highOut.Won {
		t.Fatalf("well-educated city lost the pharma prospect")
	}
}

// --- AC-4: second leg — won campus feeds graduate/research demand ----------

func TestPharmaCampusDemandSecondLeg(t *testing.T) {
	params := loadPharmaParams(t)

	// demand is non-decreasing in employment (the compounding leg scales)
	if params.GraduateDemandFor(params.Jobs-1) > params.GraduateDemandFor(params.Jobs) {
		t.Fatalf("graduate demand decreased with employment: %d -> %d",
			params.GraduateDemandFor(params.Jobs-1), params.GraduateDemandFor(params.Jobs))
	}

	edu := &fakeEducation{available: true}
	f, err := firms.LoadDefault(3, cid())
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPharma(params, edu, &realFirmsEdge{api: f}, &fakeTrade{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	win, err := p.Win(cid())
	if err != nil {
		t.Fatal(err)
	}
	if win.Demand == 0 {
		t.Fatal("won campus emitted zero graduate/research demand")
	}
	if edu.lastDemand != win.Demand {
		t.Fatalf("demand emitted to education edge %d != reported %d", edu.lastDemand, win.Demand)
	}
	if win.Employment != params.Jobs {
		t.Fatalf("employment %d != jobs %d", win.Employment, params.Jobs)
	}
}

// --- AC-5: supply-chain spawning via the firms edge ------------------------

func TestPharmaSupplyChainSpawn(t *testing.T) {
	params := loadPharmaParams(t)

	if params.SupplyChainCountFor(params.Jobs-1) > params.SupplyChainCountFor(params.Jobs) {
		t.Fatalf("supply-chain spawn decreased with employment: %d -> %d",
			params.SupplyChainCountFor(params.Jobs-1), params.SupplyChainCountFor(params.Jobs))
	}

	edu := &fakeEducation{available: true}
	f, err := firms.LoadDefault(5, cid())
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPharma(params, edu, &realFirmsEdge{api: f}, &fakeTrade{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	win, err := p.Win(cid())
	if err != nil {
		t.Fatal(err)
	}
	if win.Spawned != params.SupplyChainCountFor(params.Jobs) {
		t.Fatalf("spawned %d != expected %d", win.Spawned, params.SupplyChainCountFor(params.Jobs))
	}
	// the spawned supply-chain firms are real FirmsAPI firms, not a
	// pharma-local registry — anchor + spawn count are all queryable.
	if int64(len(f.Firms())) < win.Spawned+1 {
		t.Fatalf("FirmsAPI holds %d firms, want at least anchor+%d supply-chain", len(f.Firms()), win.Spawned)
	}
}

// --- AC-6: real firm + exports + closure via FirmsAPI ----------------------

func TestPharmaFirmAndClosure(t *testing.T) {
	params := loadPharmaParams(t)
	edu := &fakeEducation{available: true}
	f, err := firms.LoadDefault(9, cid())
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPharma(params, edu, &realFirmsEdge{api: f}, &fakeTrade{}, 9)
	if err != nil {
		t.Fatal(err)
	}
	win, err := p.Win(cid())
	if err != nil {
		t.Fatal(err)
	}

	firm, err := f.Firm(win.FirmID)
	if err != nil {
		t.Fatalf("anchor firm not independently retrievable through FirmsAPI: %v", err)
	}
	if firm.InputRequired == 0 || firm.InputRequired != win.Employment {
		t.Fatalf("anchor firm employment %d != win employment %d (or zero)", firm.InputRequired, win.Employment)
	}
	if win.Exports == 0 {
		t.Fatal("export flow is zero (not a real queryable export)")
	}

	// closure is reachable through FirmsAPI's own path (§32 shock), not a
	// pharma-local scripted event.
	if _, err := f.Fail(win.FirmID); err != nil {
		t.Fatalf("anchor firm not closeable through FirmsAPI: %v", err)
	}
	if _, err := f.Firm(win.FirmID); err == nil {
		t.Fatal("anchor firm still retrievable after FirmsAPI closure")
	}
}

func TestPharmaExportFlow(t *testing.T) {
	params := loadPharmaParams(t)
	edu := &fakeEducation{available: true}
	trade := &fakeTrade{}
	f, err := firms.LoadDefault(10, cid())
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPharma(params, edu, &realFirmsEdge{api: f}, trade, 10)
	if err != nil {
		t.Fatal(err)
	}
	win, err := p.Win(cid())
	if err != nil {
		t.Fatal(err)
	}

	// The export figure routes through the registered trade surface — never a
	// pharma-local counter read straight off the data file (AC-6).
	if trade.exports != params.ExportsPerDay {
		t.Fatalf("trade surface received %d t/day, want %d (exports did not route through the registered trade edge)", trade.exports, params.ExportsPerDay)
	}
	if win.Exports != params.ExportsPerDay {
		t.Fatalf("win exports %d != data-sourced %d", win.Exports, params.ExportsPerDay)
	}
	if p.Exports() != params.ExportsPerDay {
		t.Fatalf("queryable exports %d != data-sourced %d", p.Exports(), params.ExportsPerDay)
	}
}

// --- AC-8: registry-sourced errors + no partial state ----------------------

func TestUnknownPharmaKey(t *testing.T) {
	cat := realPharmaCatalogue(t)
	_, err := cat.Resolve("no_such_anchor")
	assertErrCode(t, err, ErrUnknownAnchor)
}

func TestMalformedPharmaData(t *testing.T) {
	negDraw := writeMutatedPharma(t, func(m map[string]any) {
		anchors := m["anchors"].([]any)
		entry := anchors[0].(map[string]any)
		entry["utilityPowerKW"] = float64(-1)
	})
	c, err := LoadPharmaCampus(negDraw, cid())
	assertErrCode(t, err, ErrPharmaDataInvalid)
	if c.Len() != 0 {
		t.Fatalf("malformed data produced a partial catalogue of %d entries", c.Len())
	}

	badArchetype := writeMutatedPharma(t, func(m map[string]any) {
		anchors := m["anchors"].([]any)
		entry := anchors[0].(map[string]any)
		entry["jobsCharacter"] = "wizard"
	})
	if _, err := LoadPharmaCampus(badArchetype, cid()); err == nil {
		t.Fatal("unrecognised jobsCharacter accepted")
	} else {
		assertErrCode(t, err, ErrPharmaDataInvalid)
	}
}

func TestPharmaRejectsUnavailableEducation(t *testing.T) {
	params := loadPharmaParams(t)
	edu := &fakeEducation{available: false} // unregistered education output
	f, err := firms.LoadDefault(11, cid())
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPharma(params, edu, &realFirmsEdge{api: f}, &fakeTrade{}, 11)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.ResolveBid(cid()); err == nil {
		t.Fatal("bid with unavailable education output accepted")
	} else {
		assertErrCode(t, err, ErrEducationOutputUnavailable)
	}

	if len(f.Firms()) != 0 {
		t.Fatalf("unavailable education left %d firms registered", len(f.Firms()))
	}
	if edu.demands != 0 || edu.lastDemand != 0 {
		t.Fatal("unavailable education still emitted graduate demand")
	}
	if p.Won() {
		t.Fatal("unavailable education still marked the campus won")
	}
}

func TestPharmaWinRejectsDemandWithoutFirm(t *testing.T) {
	params := loadPharmaParams(t)
	edu := &fakeEducation{available: true, demandErr: errors.New("rejected")}
	trade := &fakeTrade{}
	f, err := firms.LoadDefault(14, cid())
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPharma(params, edu, &realFirmsEdge{api: f}, trade, 14)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Win(cid()); err == nil {
		t.Fatal("win with a rejected education demand accepted")
	} else {
		assertErrCode(t, err, ErrEducationDemandRejected)
	}

	if len(f.Firms()) != 0 {
		t.Fatalf("rejected demand left %d firms registered", len(f.Firms()))
	}
	if trade.exports != 0 {
		t.Fatalf("rejected demand still routed %d t/day of exports", trade.exports)
	}
	if p.Won() {
		t.Fatal("rejected demand still marked the campus won")
	}
}

func TestPharmaWinRollsBackSupplyChainFailure(t *testing.T) {
	params := loadPharmaParams(t)
	edu := &fakeEducation{available: true}
	trade := &fakeTrade{}
	// The anchor registers (call 1) and the first supply-chain registration
	// (call 2) fails, so the rollback must compensate the demand, exports and
	// the already-registered anchor firm (SEC-121 — not just the firms).
	ff := &fakeFirms{failOnCall: 2, failErr: errors.New("register fail")}
	p, err := NewPharma(params, edu, ff, trade, 17)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Win(cid()); err == nil {
		t.Fatal("win with a failing supply-chain registration accepted")
	} else {
		assertErrCode(t, err, ErrPharmaFirmRegistrationFailed)
	}

	if ff.count() != 0 {
		t.Fatalf("supply-chain registration failure left %d firms registered", ff.count())
	}
	if edu.demandTotal != 0 {
		t.Fatalf("supply-chain registration failure leaked %d graduate demand", edu.demandTotal)
	}
	if trade.exports != 0 {
		t.Fatalf("supply-chain registration failure leaked %d t/day of exports", trade.exports)
	}
	if p.Won() {
		t.Fatal("supply-chain failure still marked the campus won")
	}
}

// --- SEC-121: every side effect compensated on a late failure --------------

func TestPharmaWinRollsBackAnchorFailure(t *testing.T) {
	params := loadPharmaParams(t)
	edu := &fakeEducation{available: true}
	trade := &fakeTrade{}
	// The anchor registration (call 1) fails after demand and exports were
	// applied, so both must be compensated back (SEC-121).
	ff := &fakeFirms{failOnCall: 1, failErr: errors.New("anchor register fail")}
	p, err := NewPharma(params, edu, ff, trade, 18)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Win(cid()); err == nil {
		t.Fatal("win with a failing anchor registration accepted")
	} else {
		assertErrCode(t, err, ErrPharmaFirmRegistrationFailed)
	}

	if ff.count() != 0 {
		t.Fatalf("anchor registration failure left %d firms registered", ff.count())
	}
	if edu.demandTotal != 0 {
		t.Fatalf("anchor registration failure leaked %d graduate demand", edu.demandTotal)
	}
	if trade.exports != 0 {
		t.Fatalf("anchor registration failure leaked %d t/day of exports", trade.exports)
	}
	if p.Won() {
		t.Fatal("anchor failure still marked the campus won")
	}
}

func TestPharmaWinRollsBackExportFailure(t *testing.T) {
	params := loadPharmaParams(t)
	edu := &fakeEducation{available: true}
	trade := &fakeTrade{rejectErr: errors.New("export rejected")}
	ff := &fakeFirms{}
	p, err := NewPharma(params, edu, ff, trade, 19)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Win(cid()); err == nil {
		t.Fatal("win with a rejected export accepted")
	} else {
		assertErrCode(t, err, ErrPharmaExportRejected)
	}

	if ff.count() != 0 {
		t.Fatalf("export rejection left %d firms registered", ff.count())
	}
	if edu.demandTotal != 0 {
		t.Fatalf("export rejection leaked %d graduate demand", edu.demandTotal)
	}
	if trade.exports != 0 {
		t.Fatalf("export rejection left %d t/day of exports", trade.exports)
	}
	if p.Won() {
		t.Fatal("export rejection still marked the campus won")
	}
}

func TestPharmaWinSurfacesRollbackFailure(t *testing.T) {
	params := loadPharmaParams(t)
	reject := errors.New("export reject")
	removeFail := errors.New("demand remove fail")
	edu := &fakeEducation{available: true, removeErr: removeFail}
	trade := &fakeTrade{rejectErr: reject}
	ff := &fakeFirms{}
	p, err := NewPharma(params, edu, ff, trade, 20)
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.Win(cid())
	if err == nil {
		t.Fatal("win accepted despite a rejected export")
	}
	assertErrCode(t, err, ErrPharmaExportRejected)
	// A rollback failure must be surfaced alongside the primary cause (GR#1),
	// never silently dropped — otherwise the phantom demand reads as a clean
	// rejection.
	if !errors.Is(err, reject) {
		t.Fatalf("primary export-rejection cause not preserved: %v", err)
	}
	if !errors.Is(err, removeFail) {
		t.Fatalf("compensating demand-removal failure not surfaced: %v", err)
	}
}

// --- SEC-140: refused win leaves the FirmsAPI churn ledger untouched --------

// TestPharmaWinRollbackLeavesCleanChurnLedger is the SEC-140 regression: a
// refused win (here the first supply-chain registration fails after the anchor
// registered) must roll the anchor firm back with a COMPENSATING removal, not
// FirmsAPI's §32 insolvency path. Pre-fix, rollback called Fail, which left the
// real FirmsAPI at founded=1 failed=1 with two lifecycle events for a firm that
// should never have existed; post-fix it must leave founded==0, failed==0 and
// NO EventFounded/EventFailed events.
func TestPharmaWinRollbackLeavesCleanChurnLedger(t *testing.T) {
	params := loadPharmaParams(t)
	edu := &fakeEducation{available: true}
	trade := &fakeTrade{}
	ff := &fakeFirms{failOnCall: 2, failErr: errors.New("supply-chain register fail")}
	p, err := NewPharma(params, edu, ff, trade, 22)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Win(cid()); err == nil {
		t.Fatal("win with a failing supply-chain registration accepted")
	} else {
		assertErrCode(t, err, ErrPharmaFirmRegistrationFailed)
	}

	if ff.founded != 0 {
		t.Fatalf("refused win left founded=%d, want 0 (rollback reused insolvency semantics, not a true inverse)", ff.founded)
	}
	if ff.failed != 0 {
		t.Fatalf("refused win left failed=%d, want 0 (rollback emitted a §32 failure for a firm that never existed)", ff.failed)
	}
	if len(ff.events) != 0 {
		t.Fatalf("refused win emitted %d lifecycle events, want 0 (EventFounded/EventFailed pollution)", len(ff.events))
	}
	if ff.count() != 0 {
		t.Fatalf("refused win left %d registered firms, want 0", ff.count())
	}
}

// TestPharmaWinRollbackAgainstRealFirmsAPI is the SEC-159 regression. SEC-140's
// clean-ledger guarantee held only on the fakeFirms double: the real
// *firms.FirmsAPI had no RemoveFirm, so realFirmsEdge.RemoveFirm hard-errored and
// a refused win against the real API leaked the anchor firm (len(Firms)=1,
// FoundedCount=1, Events=[EventFounded]). Post-fix, realFirmsEdge delegates
// RemoveFirm to the real FirmsAPI's genuine compensating inverse, so a refused
// win against the real API leaves foundedCount==0, failedCount==0, no
// EventFounded, and no registered firms.
func TestPharmaWinRollbackAgainstRealFirmsAPI(t *testing.T) {
	params := loadPharmaParams(t)
	edu := &fakeEducation{available: true}
	trade := &fakeTrade{}
	f, err := firms.LoadDefault(42, cid())
	if err != nil {
		t.Fatal(err)
	}
	// The anchor registers (call 1) and the first supply-chain registration
	// (call 2) fails, so the rollback must compensate the already-registered
	// anchor through the REAL FirmsAPI's RemoveFirm — the exact pre-fix leak.
	edge := &realFirmsEdge{api: f, failOnCall: 2, failErr: errors.New("supply-chain register fail")}
	p, err := NewPharma(params, edu, edge, trade, 42)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Win(cid()); err == nil {
		t.Fatal("win with a failing supply-chain registration accepted")
	} else {
		assertErrCode(t, err, ErrPharmaFirmRegistrationFailed)
	}

	if got := f.FoundedCount(); got != 0 {
		t.Fatalf("refused win left foundedCount=%d, want 0 (RemoveFirm not wired on the real FirmsAPI)", got)
	}
	if got := f.FailedCount(); got != 0 {
		t.Fatalf("refused win left failedCount=%d, want 0", got)
	}
	if got := len(f.Firms()); got != 0 {
		t.Fatalf("refused win left %d firms registered, want 0 (anchor firm leaked)", got)
	}
	if got := len(f.Events()); got != 0 {
		t.Fatalf("refused win emitted %d lifecycle events, want 0 (EventFounded pollution)", got)
	}
	if p.Won() {
		t.Fatal("refused win still marked the campus won")
	}
}

// TestFirmsEdgeFailIsInsolvencyNotRollback pins the SEC-140 class distinction
// in the test double: Fail is the §32 insolvency path (failedCount++,
// EventFailed), RemoveFirm is the compensating inverse (founded--, retracts the
// EventFounded, touches neither the failed ledger nor other events). A future
// change that collapses the two — or that makes rollback call Fail again — is
// caught by TestPharmaWinRollbackLeavesCleanChurnLedger.
func TestFirmsEdgeFailIsInsolvencyNotRollback(t *testing.T) {
	f := &fakeFirms{}
	firm, err := f.RegisterFirm("anchor", 1, "SUP3")
	if err != nil {
		t.Fatal(err)
	}
	if f.founded != 1 || f.failed != 0 || len(f.events) != 1 || f.events[0].Kind != firms.EventFounded {
		t.Fatalf("register: founded=%d failed=%d events=%v, want 1/0/[EventFounded]", f.founded, f.failed, f.events)
	}

	// Fail is the §32 insolvency path, not the rollback inverse: it increments
	// failed and emits EventFailed, and it does NOT decrement founded (the firm
	// is a real failure, not an undone registration).
	if _, err := f.Fail(firms.FirmID(firm.ID)); err != nil {
		t.Fatal(err)
	}
	if f.failed != 1 || f.founded != 1 || len(f.events) != 2 || f.events[1].Kind != firms.EventFailed {
		t.Fatalf("fail: founded=%d failed=%d events=%v, want 1/1/[EventFounded,EventFailed]", f.founded, f.failed, f.events)
	}

	// RemoveFirm is the genuine inverse: it decrements founded and retracts the
	// EventFounded it emitted, touching neither the failed ledger nor the
	// remaining events.
	g, err := f.RegisterFirm("supply", 1, "SUP3")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveFirm(firms.FirmID(g.ID)); err != nil {
		t.Fatal(err)
	}
	if f.founded != 1 {
		t.Fatalf("RemoveFirm: founded=%d, want 1 (supply's founded decremented back)", f.founded)
	}
	if f.failed != 1 {
		t.Fatalf("RemoveFirm: failed=%d, want 1 (RemoveFirm must not touch the failed ledger)", f.failed)
	}
	if len(f.events) != 2 {
		t.Fatalf("RemoveFirm: events=%d, want 2 (supply's EventFounded retracted, nothing added)", len(f.events))
	}
}

// --- SEC-122: unbounded spawn loop rejected at both trust boundaries -------

func TestNewPharmaRejectsOversizedSupplyChainSpawn(t *testing.T) {
	base := loadPharmaParams(t)
	cases := []struct {
		name   string
		mutate func(*PharmaCampusParams)
	}{
		{"base-firms", func(p *PharmaCampusParams) { p.SupplyChainFirms = 1 << 40 }},
		{"per-worker-divisor", func(p *PharmaCampusParams) {
			p.SupplyChainFirms = 0
			p.SupplyChainPerWorkers = 1
			p.Jobs = 1 << 40
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.mutate(&p)
			if _, err := NewPharma(p, &fakeEducation{available: true}, &fakeFirms{}, &fakeTrade{}, 1); err == nil {
				t.Fatal("NewPharma accepted a supply-chain spawn beyond the validation ceiling")
			} else {
				assertErrCode(t, err, ErrPharmaDataInvalid)
			}
		})
	}
}

func TestLoadRejectsOversizedSupplyChainSpawn(t *testing.T) {
	mutated := writeMutatedPharma(t, func(m map[string]any) {
		anchors := m["anchors"].([]any)
		entry := anchors[0].(map[string]any)
		entry["supplyChainFirms"] = float64(1 << 40)
	})
	c, err := LoadPharmaCampus(mutated, cid())
	assertErrCode(t, err, ErrPharmaDataInvalid)
	if c.Len() != 0 {
		t.Fatalf("oversized spawn produced a partial catalogue of %d entries", c.Len())
	}
}

// --- SEC-123: jitter half-range bounded so the draw stays symmetric ---------

func TestNewPharmaRejectsOversizedJitter(t *testing.T) {
	params := loadPharmaParams(t)
	params.Bid.JitterMax = maxJitter + 1
	if _, err := NewPharma(params, &fakeEducation{available: true}, &fakeFirms{}, &fakeTrade{}, 1); err == nil {
		t.Fatal("NewPharma accepted a jitter half-range beyond the symmetric-draw ceiling")
	} else {
		assertErrCode(t, err, ErrPharmaDataInvalid)
	}
}

func TestLoadRejectsOversizedJitter(t *testing.T) {
	mutated := writeMutatedPharma(t, func(m map[string]any) {
		anchors := m["anchors"].([]any)
		entry := anchors[0].(map[string]any)
		bid := entry["bid"].(map[string]any)
		bid["jitterMax"] = float64(maxJitter + 1)
	})
	c, err := LoadPharmaCampus(mutated, cid())
	assertErrCode(t, err, ErrPharmaDataInvalid)
	if c.Len() != 0 {
		t.Fatalf("oversized jitter produced a partial catalogue of %d entries", c.Len())
	}
}

func TestPharmaBidJitterStaysSymmetricAtCeiling(t *testing.T) {
	// The draw must stay inside [-jitterMax, +jitterMax] right up to the
	// validation ceiling, where 2*jitterMax+1 is still exactly representable
	// (SEC-123 — the saturation that made it asymmetric only occurs past the
	// ceiling, which Validate now rejects).
	for _, jm := range []int64{0, 1, 8, maxJitter - 1, maxJitter} {
		for seed := uint64(0); seed < 100; seed++ {
			j := pharmaBidJitter(seed, jm)
			if j < -jm || j > jm {
				t.Fatalf("jitter %d outside [-%d,+%d] (seed %d)", j, jm, jm, seed)
			}
		}
	}
}

// --- SEC-124: education output value validated, not just the flag ----------

func TestPharmaRejectsNegativeEducationOutput(t *testing.T) {
	params := loadPharmaParams(t)
	edu := &fakeEducation{available: true, output: -5}
	p, err := NewPharma(params, edu, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.ResolveBid(cid()); err == nil {
		t.Fatal("bid with a negative education output accepted")
	} else {
		assertErrCode(t, err, ErrEducationOutputUnavailable)
	}
}

// --- AC-9: determinism (seed consumed, never wall clock / shared RNG) ------

func TestPharmaDeterminism(t *testing.T) {
	params := loadPharmaParams(t)
	seed := uint64(123)

	run := func() WinResult {
		edu := &fakeEducation{available: true, output: 4}
		f, err := firms.LoadDefault(seed, cid())
		if err != nil {
			t.Fatal(err)
		}
		p, err := NewPharma(params, edu, &realFirmsEdge{api: f}, &fakeTrade{}, seed)
		if err != nil {
			t.Fatal(err)
		}
		w, err := p.Win(cid())
		if err != nil {
			t.Fatal(err)
		}
		return w
	}

	a := run()
	b := run()
	if a != b {
		t.Fatalf("identical win sequence diverged: %+v vs %+v", a, b)
	}

	// a different seed is genuinely consumed — the bid jitter differs.
	term := params.EducationBidTerm(4)
	qa := ResolvePharmaBid(term, params.Bid.CompetingFloor, params.Bid.JitterMax, 1).Quality
	qb := ResolvePharmaBid(term, params.Bid.CompetingFloor, params.Bid.JitterMax, 2).Quality
	if qa == qb {
		t.Fatalf("different seeds produced identical bid outcome (seed not consumed)")
	}
}
