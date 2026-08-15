package freight

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- fixtures -----------------------------------------------------------

// repoDataDir resolves the repo's real data/ directory (market.json and
// logistics.json are copied verbatim so the registered engine.market /
// engine.logistics edges load against the committed, valid data).
func repoDataDir(t *testing.T) string {
	t.Helper()
	dir, err := data.ResolveDataDir("freight-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	return dir
}

// fixtureDir writes a temp data directory with the real market.json and
// logistics.json plus a data/freight.json mutated by mutate (or the
// committed one when mutate is nil).
func fixtureDir(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	repo := repoDataDir(t)
	dir := t.TempDir()
	for _, f := range []string{"market.json", "logistics.json"} {
		b, err := os.ReadFile(filepath.Join(repo, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(repo, "freight.json"))
	if err != nil {
		t.Fatalf("read freight.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal freight.json: %v", err)
	}
	if mutate != nil {
		mutate(m)
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal freight.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "freight.json"), out, 0o644); err != nil {
		t.Fatalf("write freight.json: %v", err)
	}
	return dir
}

func loadFixture(t *testing.T, mutate func(map[string]any)) *FreightAPI {
	t.Helper()
	f, err := Load(fixtureDir(t, mutate), "freight-test-correlation")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f
}

func mustAdvance(t *testing.T, f *FreightAPI) {
	t.Helper()
	if err := f.AdvanceTick(); err != nil {
		t.Fatalf("AdvanceTick: %v", err)
	}
}

// --- AC-2: port capacity is the three-factor product --------------------

func TestPortCapacity(t *testing.T) {
	f := loadFixture(t, nil)
	pc, err := f.PortCapacity()
	if err != nil {
		t.Fatalf("PortCapacity: %v", err)
	}
	want := pc.Berths * pc.CraneRateTonnesPerHour * pc.OperatingHoursPerDay
	if pc.TonnesPerDay != want {
		t.Fatalf("PortCapacity.TonnesPerDay = %d, want berths*crane*hours = %d", pc.TonnesPerDay, want)
	}
	if pc.Berths <= 0 || pc.CraneRateTonnesPerHour <= 0 || pc.OperatingHoursPerDay <= 0 {
		t.Fatalf("expected positive factors, got %+v", pc)
	}

	// Each factor scales capacity independently — a flat daily-tonnage
	// constant would pass "has capacity" but fail these three.
	doublings := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"berths", func(m map[string]any) { m["port"].(map[string]any)["berths"] = float64(pc.Berths * 2) }},
		{"craneRateTonnesPerHour", func(m map[string]any) {
			m["port"].(map[string]any)["craneRateTonnesPerHour"] = float64(pc.CraneRateTonnesPerHour * 2)
		}},
		{"operatingHoursPerDay", func(m map[string]any) {
			m["port"].(map[string]any)["operatingHoursPerDay"] = float64(pc.OperatingHoursPerDay * 2)
		}},
	}
	for _, d := range doublings {
		d := d
		t.Run(d.name, func(t *testing.T) {
			g := loadFixture(t, d.mutate)
			gc, err := g.PortCapacity()
			if err != nil {
				t.Fatalf("PortCapacity: %v", err)
			}
			if gc.TonnesPerDay != want*2 {
				t.Fatalf("doubling %s: got %d, want %d", d.name, gc.TonnesPerDay, want*2)
			}
		})
	}
}

// --- AC-3: customs saturation separate from port physical throughput -----

func TestCustomsSeparateFromPort(t *testing.T) {
	// Tiny customs capacity so a small export saturates customs while the
	// physical berth/crane throughput is untouched.
	f := loadFixture(t, func(m map[string]any) {
		m["port"].(map[string]any)["customsCapacityTonnesPerDay"] = float64(10)
	})
	mustAdvance(t, f) // produce stock

	beforeRisk, err := f.SmugglingRisk()
	if err != nil {
		t.Fatalf("SmugglingRisk: %v", err)
	}
	if beforeRisk != 0 {
		t.Fatalf("expected zero smuggling risk before customs demand, got %v", beforeRisk)
	}
	beforePort, err := f.PortCapacity()
	if err != nil {
		t.Fatalf("PortCapacity: %v", err)
	}

	// Export 15t of concrete by road (cap 25t) — customs demand 15 > 10.
	if _, err := f.Export("concrete", 15, ModeRoad); err != nil {
		t.Fatalf("Export: %v", err)
	}

	afterPort, err := f.PortCapacity()
	if err != nil {
		t.Fatalf("PortCapacity: %v", err)
	}
	if afterPort.TonnesPerDay != beforePort.TonnesPerDay {
		t.Fatalf("port physical throughput changed by customs demand: %d -> %d", beforePort.TonnesPerDay, afterPort.TonnesPerDay)
	}

	sat, err := f.CustomsSaturation()
	if err != nil {
		t.Fatalf("CustomsSaturation: %v", err)
	}
	if sat.Saturation <= 1 {
		t.Fatalf("expected customs saturated (saturation > 1), got %v (demand %d, capacity %d)", sat.Saturation, sat.Demanded, sat.Capacity)
	}
	risk, err := f.SmugglingRisk()
	if err != nil {
		t.Fatalf("SmugglingRisk: %v", err)
	}
	if risk <= beforeRisk {
		t.Fatalf("smuggling risk did not rise as customs saturated: %v -> %v", beforeRisk, risk)
	}
}

// --- AC-4: stages register as firms through the FirmRegistrar seam -------

type stubRegistrar struct {
	next  uint64
	names map[string]bool
}

func (r *stubRegistrar) RegisterFirm(name string, staff int64, premises string) (Firm, error) {
	if r.names == nil {
		r.names = map[string]bool{}
	}
	r.next++
	r.names[name] = true
	return Firm{ID: r.next, Staff: staff, Premises: premises}, nil
}

func TestStageIsFirm(t *testing.T) {
	f := loadFixture(t, nil)

	s, err := f.Stage("cementPlant")
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if s.Jobs <= 0 {
		t.Fatalf("stage must expose jobs (staff), got %d", s.Jobs)
	}
	if s.Firm.ID != 0 {
		t.Fatalf("expected zero Firm before registration (blocked on MOD-058), got %+v", s.Firm)
	}

	reg := &stubRegistrar{}
	if err := f.RegisterFirms(reg); err != nil {
		t.Fatalf("RegisterFirms: %v", err)
	}

	s2, err := f.Stage("cementPlant")
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if s2.Firm.ID == 0 {
		t.Fatal("stage firm was not registered through the seam")
	}
	if s2.Firm.Staff != s2.Jobs {
		t.Fatalf("firm staff %d != stage jobs %d", s2.Firm.Staff, s2.Jobs)
	}

	seen := map[uint64]bool{}
	for _, st := range f.Stages() {
		if st.Firm.ID == 0 {
			t.Fatalf("stage %s has no registered firm", st.ID)
		}
		if seen[st.Firm.ID] {
			t.Fatalf("firm ID %d assigned to more than one stage", st.Firm.ID)
		}
		seen[st.Firm.ID] = true
	}
}

// --- AC-5: output bounded by input availability -------------------------

func TestStageThroughput(t *testing.T) {
	f := loadFixture(t, nil)
	st, err := f.Stage("cementPlant")
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if len(st.Inputs) != 1 || len(st.Outputs) != 1 {
		t.Fatalf("cementPlant should have exactly 1 input and 1 output, got %d/%d", len(st.Inputs), len(st.Outputs))
	}
	inRate := st.Inputs[0].TonnesPerDay
	inComm := st.Inputs[0].Commodity
	outRate := st.Outputs[0].TonnesPerDay
	outComm := st.Outputs[0].Commodity

	full, err := f.StageThroughput("cementPlant", map[Commodity]int64{inComm: inRate})
	if err != nil {
		t.Fatalf("StageThroughput: %v", err)
	}
	if full.Outputs[outComm] != outRate {
		t.Fatalf("full supply: output %d, want documented %d", full.Outputs[outComm], outRate)
	}

	// Half the documented input → output falls proportionally (the lazy
	// "unconditional output regardless of supply" would still return
	// outRate here and fail).
	half, err := f.StageThroughput("cementPlant", map[Commodity]int64{inComm: inRate / 2})
	if err != nil {
		t.Fatalf("StageThroughput: %v", err)
	}
	if half.Outputs[outComm] != outRate/2 {
		t.Fatalf("half supply: output %d, want %d", half.Outputs[outComm], outRate/2)
	}
	if half.Consumed[inComm] != inRate/2 {
		t.Fatalf("half supply: consumed %d, want %d", half.Consumed[inComm], inRate/2)
	}

	// Zero input → zero output.
	zero, err := f.StageThroughput("cementPlant", map[Commodity]int64{inComm: 0})
	if err != nil {
		t.Fatalf("StageThroughput: %v", err)
	}
	if zero.Outputs[outComm] != 0 {
		t.Fatalf("zero supply: output %d, want 0", zero.Outputs[outComm])
	}
}

// --- AC-6: storage type matching ----------------------------------------

func TestMismatchedStorage(t *testing.T) {
	f := loadFixture(t, nil)

	// grain → silo is the documented match (no error).
	if _, err := f.Store("grain", 10, SiteSilo); err != nil {
		t.Fatalf("grain→silo should be accepted, got %v", err)
	}
	// grain → tank farm is a mismatch, rejected (not silently accepted).
	if _, err := f.Store("grain", 10, SiteTankFarm); !errors.Is(err, &errs.E{Code: ErrStorageTypeMismatch}) {
		t.Fatalf("grain→tankFarm: want ErrStorageTypeMismatch, got %v", err)
	}
	// fuel → silo is a mismatch too.
	if _, err := f.Store("fuel", 10, SiteSilo); !errors.Is(err, &errs.E{Code: ErrStorageTypeMismatch}) {
		t.Fatalf("fuel→silo: want ErrStorageTypeMismatch, got %v", err)
	}
}

// --- AC-7/AC-13: modal caps and negative tonnage -------------------------

func TestModalCapsMatchSpec(t *testing.T) {
	f := loadFixture(t, nil)
	road, _ := f.ModalCap(ModeRoad)
	rail, _ := f.ModalCap(ModeRail)
	sea, _ := f.ModalCap(ModeSea)
	if road.MaxTonnesPerMovement != 25 {
		t.Fatalf("road max = %d, want §33/§8's 25", road.MaxTonnesPerMovement)
	}
	if rail.MaxTonnesPerMovement != 1000 {
		t.Fatalf("rail max = %d, want 1000", rail.MaxTonnesPerMovement)
	}
	if sea.MinTonnesPerMovement != 3000 || sea.MaxTonnesPerMovement != 40000 {
		t.Fatalf("sea = [%d,%d], want [3000,40000]", sea.MinTonnesPerMovement, sea.MaxTonnesPerMovement)
	}
}

func TestOverCapRejected(t *testing.T) {
	f := loadFixture(t, nil)
	mustAdvance(t, f) // produce stock so Export has tonnes available

	cases := []struct {
		name   string
		tonnes int64
		mode   Mode
		want   string
	}{
		{"roadOverCap", 26, ModeRoad, ErrModalCapExceeded},
		{"railOverCap", 1001, ModeRail, ErrModalCapExceeded},
		{"seaBelowMin", 100, ModeSea, ErrModalCapExceeded},
		{"seaOverCap", 40001, ModeSea, ErrModalCapExceeded},
		{"negative", -5, ModeRoad, ErrNegativeTonnage},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, err := f.Export("concrete", c.tonnes, c.mode)
			if !errors.Is(err, &errs.E{Code: c.want}) {
				t.Fatalf("Export(%d, %s): want %s, got %v", c.tonnes, c.mode, c.want, err)
			}
		})
	}
}

// --- AC-9: import/export figures independently sourced ------------------

func TestImportExportIndependent(t *testing.T) {
	f := loadFixture(t, nil)
	mustAdvance(t, f) // produce stock

	beforeExp := f.Exports().TotalTonnes
	beforeImp := f.Imports().TotalTonnes

	// An export changes only the exports figure.
	if _, err := f.Export("concrete", 20, ModeRoad); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if got := f.Exports().TotalTonnes; got != beforeExp+20 {
		t.Fatalf("exports after export = %d, want %d", got, beforeExp+20)
	}
	if got := f.Imports().TotalTonnes; got != beforeImp {
		t.Fatalf("imports perturbed by an export: %d -> %d", beforeImp, got)
	}

	// An import changes only the imports figure.
	res, err := f.Import("concrete", 10, ModeRoad)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := f.Imports().TotalTonnes; got != beforeImp+res.Moved {
		t.Fatalf("imports after import = %d, want %d", got, beforeImp+res.Moved)
	}
	if got := f.Exports().TotalTonnes; got != beforeExp+20 {
		t.Fatalf("exports perturbed by an import: %d -> %d", beforeExp+20, got)
	}
}

// --- AC-10: the mass-conservation identity -------------------------------

func TestMassConservation(t *testing.T) {
	f := loadFixture(t, nil)

	for i := 0; i < 5; i++ {
		switch i {
		case 1:
			if _, err := f.Export("concrete", 20, ModeRoad); err != nil {
				t.Fatalf("Export: %v", err)
			}
		case 2:
			if _, err := f.Import("concrete", 5, ModeRoad); err != nil {
				t.Fatalf("Import: %v", err)
			}
		case 3:
			if _, err := f.Ship("grain", 30, SiteSilo, ModeRail); err != nil {
				t.Fatalf("Ship: %v", err)
			}
		}

		mustAdvance(t, f)

		if v := f.VerifyConservation(); v != "" {
			t.Fatalf("tick %d: conservation violated for commodity %q", f.Tick(), v)
		}

		// AC-10's exact identity, independently summed from the account's
		// own term maps (never a remainder).
		acct := f.ConservationAccount()
		if !acct.IsBalanced(f.tonneCommodities()) {
			t.Fatalf("tick %d: Produced == Consumed + Exported + StorageDelta + InTransitDelta violated:\n%+v", f.Tick(), acct)
		}
	}
}

// --- AC-12: registry-sourced errors, no zero-value entries ---------------

// SEC-086: a chain stage whose inputs list the same commodity twice would
// let runStagesLocked over-draw the pool negative and route the deficit into
// storage as a negative StorageDelta — a silent tonnage leak the invariant
// would miss. The config must reject a duplicate-input stage at load time.
func TestDuplicateInputRejected(t *testing.T) {
	_, err := Load(fixtureDir(t, func(m map[string]any) {
		stages := m["chains"].(map[string]any)["construction"].(map[string]any)["stages"].([]any)
		cement := stages[1].(map[string]any) // cementPlant, inputs = [{chalk,100}]
		inputs := cement["inputs"].([]any)
		cement["inputs"] = append(inputs, map[string]any{"commodity": "chalk", "tonnesPerDay": float64(50)})
	}), "freight-test-correlation")
	if err == nil {
		t.Fatal("duplicate input commodity must be rejected at Load, but it was accepted")
	}
	if !errors.Is(err, &errs.E{Code: ErrFreightDataInvalid}) {
		t.Fatalf("duplicate input: want ErrFreightDataInvalid, got %v", err)
	}
}

func TestUnregisteredStage(t *testing.T) {
	f := loadFixture(t, nil)
	_, err := f.Stage("noSuchStage")
	if !errors.Is(err, &errs.E{Code: ErrUnknownStage}) {
		t.Fatalf("Stage(unknown): want ErrUnknownStage, got %v", err)
	}
	for _, st := range f.Stages() {
		if st.ID == "noSuchStage" {
			t.Fatal("unknown stage created a zero-value entry")
		}
	}
}

func TestUnknownStorage(t *testing.T) {
	f := loadFixture(t, nil)
	if _, err := f.StorageSite("bogus"); !errors.Is(err, &errs.E{Code: ErrUnknownStorageSite}) {
		t.Fatalf("StorageSite(bogus): want ErrUnknownStorageSite, got %v", err)
	}
	if _, err := f.Store("concrete", 1, "bogus"); !errors.Is(err, &errs.E{Code: ErrUnknownStorageSite}) {
		t.Fatalf("Store(bogus site): want ErrUnknownStorageSite, got %v", err)
	}
	if _, err := f.Store("bogus", 1, SiteSilo); !errors.Is(err, &errs.E{Code: ErrUnknownCommodity}) {
		t.Fatalf("Store(bogus commodity): want ErrUnknownCommodity, got %v", err)
	}
}

func TestPortCapacityBeforeBerths(t *testing.T) {
	f := loadFixture(t, func(m map[string]any) {
		m["port"].(map[string]any)["berths"] = float64(0)
	})
	if _, err := f.PortCapacity(); !errors.Is(err, &errs.E{Code: ErrNoBerthsConfigured}) {
		t.Fatalf("PortCapacity with zero berths: want ErrNoBerthsConfigured, got %v", err)
	}
	if _, err := f.CustomsCapacity(); !errors.Is(err, &errs.E{Code: ErrNoBerthsConfigured}) {
		t.Fatalf("CustomsCapacity with zero berths: want ErrNoBerthsConfigured, got %v", err)
	}
}

// --- AC-14: determinism ---------------------------------------------------

func stateSnapshot(f *FreightAPI) string {
	type snap struct {
		Tick       int64
		Trade      BalanceOfTrade
		Account    ConservationAccount
		Sites      []StorageSite
		Movements  []Movement
		StageCount int
	}
	b, _ := json.Marshal(snap{
		Tick:       f.Tick(),
		Trade:      f.BalanceOfTrade(),
		Account:    f.ConservationAccount(),
		Sites:      f.StorageSites(),
		Movements:  f.Movements(),
		StageCount: len(f.Stages()),
	})
	return string(b)
}

func TestDeterminism(t *testing.T) {
	run := func() string {
		f := loadFixture(t, nil)
		mustAdvance(t, f)
		_, _ = f.Export("concrete", 10, ModeRoad)
		_, _ = f.Import("concrete", 5, ModeRoad)
		_, _ = f.Ship("grain", 30, SiteSilo, ModeRail)
		mustAdvance(t, f)
		mustAdvance(t, f)
		mustAdvance(t, f)
		return stateSnapshot(f)
	}
	a := run()
	b := run()
	if a != b {
		t.Fatalf("determinism violated:\n%s\n--- vs ---\n%s", a, b)
	}
}

// --- AC-16: concurrency (deterministic assertions, race-checked) ---------

func TestConcurrentStageResolution(t *testing.T) {
	f := loadFixture(t, nil)
	mustAdvance(t, f) // produce stock for the export half

	stages := f.Stages()
	if len(stages) == 0 {
		t.Fatal("no stages loaded")
	}

	// Concurrent reads: every stage resolved from many goroutines, plus
	// port-capacity reads. Results are asserted, never timing-dependent.
	var wg sync.WaitGroup
	for _, st := range stages {
		st := st
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				got, err := f.Stage(st.ID)
				if err != nil {
					t.Errorf("Stage(%s): %v", st.ID, err)
					return
				}
				if got.Name == "" || got.ID != st.ID {
					t.Errorf("Stage(%s) returned wrong stage: %+v", st.ID, got)
					return
				}
				if _, err := f.PortCapacity(); err != nil {
					t.Errorf("PortCapacity: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// Concurrent writes to DISTINCT commodities — the final per-commodity
	// totals are deterministic (10 exports of 1t each).
	comms := []Commodity{"concrete", "cement", "steel", "bread", "wine"}
	var wg2 sync.WaitGroup
	for _, c := range comms {
		c := c
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			for j := 0; j < 10; j++ {
				if _, err := f.Export(c, 1, ModeRoad); err != nil {
					t.Errorf("Export(%s): %v", c, err)
					return
				}
			}
		}()
	}
	wg2.Wait()

	exp := f.Exports().ByCommodity
	for _, c := range comms {
		if exp[c].Tonnes != 10 {
			t.Fatalf("%s exported %d, want 10", c, exp[c].Tonnes)
		}
	}
}

// --- AC-16b: struct-copy guard (SEC-020 class) ---------------------------

// freightCopy takes a same-package value copy of *FreightAPI via an unsafe
// byte-copy, so go vet's copylocks check does not flag the literal
// assignment at the call site (mirrors engine.world's w2Copy convention) —
// a plain `cp := *f` is legal Go producing the identical attack shape, but
// would fail this package's own `go vet` baseline gate.
func freightCopy(f *FreightAPI) *FreightAPI {
	c := new(FreightAPI)
	*(*[unsafe.Sizeof(FreightAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(FreightAPI{})]byte)(unsafe.Pointer(f))
	return c
}

func TestCopyGuard(t *testing.T) {
	f := loadFixture(t, nil)
	cp := freightCopy(f) // a struct copy aliases the maps but gets its own mu
	if _, err := cp.PortCapacity(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copy.PortCapacity: want ErrCopiedValue, got %v", err)
	}
}
