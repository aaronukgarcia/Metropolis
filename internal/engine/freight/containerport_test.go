package freight

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// --- fixtures -------------------------------------------------------------

// containerPortFixtureDir writes a temp data directory with the real
// market.json, logistics.json, freight.json and a data/containerport.json
// mutated by mutate (or the committed one when mutate is nil).
func containerPortFixtureDir(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	repo := repoDataDir(t)
	dir := t.TempDir()
	for _, f := range []string{"market.json", "logistics.json", "freight.json", "containerport.json"} {
		b, err := os.ReadFile(filepath.Join(repo, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if f == "containerport.json" && mutate != nil {
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("unmarshal containerport.json: %v", err)
			}
			mutate(m)
			b, err = json.MarshalIndent(m, "", "  ")
			if err != nil {
				t.Fatalf("marshal containerport.json: %v", err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return dir
}

func loadContainerPortFixture(t *testing.T, mutate func(map[string]any)) *ContainerPort {
	t.Helper()
	dir := containerPortFixtureDir(t, mutate)
	f, err := Load(dir, "cp-test-correlation")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cp, err := LoadContainerPort(dir, "cp-test-correlation", f)
	if err != nil {
		t.Fatalf("LoadContainerPort: %v", err)
	}
	return cp
}

// loadContainerPortErr loads with a containerport.json mutation and returns
// the (expected non-nil) load error.
func loadContainerPortErr(t *testing.T, mutate func(map[string]any)) error {
	t.Helper()
	dir := containerPortFixtureDir(t, mutate)
	f, err := Load(dir, "cp-test-correlation")
	if err != nil {
		return err
	}
	_, err = LoadContainerPort(dir, "cp-test-correlation", f)
	return err
}

// containerPortTier returns one tier entry from the decoded containerport.json
// map (for mutation).
func containerPortTier(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	tiers, ok := m["tiers"].([]any)
	if !ok {
		t.Fatalf("tiers is not a list")
	}
	for _, e := range tiers {
		tm, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if tm["key"] == key {
			return tm
		}
	}
	t.Fatalf("tier %q not found", key)
	return nil
}

// --- seams (test stubs for the unbuilt FEAT-053/FEAT-054/MOD-060 edges) ---

type stubPermit struct {
	grant bool
}

func (s *stubPermit) PermitGranted(tierKey string, milestone int) (bool, error) {
	return s.grant, nil
}

type stubDecom struct {
	mu          sync.Mutex
	liabilities map[string]int64
}

func (s *stubDecom) RegisterLiability(facilityKey string, costMicropounds int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.liabilities == nil {
		s.liabilities = map[string]int64{}
	}
	s.liabilities[facilityKey] = costMicropounds
	return nil
}

// stubRail is a thread-safe, conserving RailIntermodal seam stand-in for tests
// that cannot import internal/engine/rail (internal test files importing a
// package that imports freight would be an import cycle). The real engine.rail
// stub is exercised by containerport_rail_test.go.
type stubRail struct {
	mu    sync.Mutex
	in    map[Mode]int64
	out   map[Mode]int64
	dwell map[Mode]int64
}

func newStubRail() *stubRail {
	return &stubRail{in: map[Mode]int64{}, out: map[Mode]int64{}, dwell: map[Mode]int64{}}
}

func (s *stubRail) IntermodalTransfer(from, to Mode, tonnes int64) (IntermodalTransferResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tonnes <= 0 {
		return IntermodalTransferResult{}, errs.New(ErrContainerPortBuildRejected, "stub", map[string]any{"reason": "non-positive tonnes"})
	}
	s.in[from] = num.SatAdd(s.in[from], tonnes)
	s.out[to] = num.SatAdd(s.out[to], tonnes)
	return IntermodalTransferResult{Accepted: tonnes, Delivered: tonnes, Dwell: 0}, nil
}

func (s *stubRail) IntermodalAccount() IntermodalAccount {
	s.mu.Lock()
	defer s.mu.Unlock()
	return IntermodalAccount{
		InTonnes:    copyStubModeMap(s.in),
		OutTonnes:   copyStubModeMap(s.out),
		DwellTonnes: copyStubModeMap(s.dwell),
	}
}

func copyStubModeMap(m map[Mode]int64) map[Mode]int64 {
	out := make(map[Mode]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// --- AC-2: the port-tier ladder (deep-sea strictly above container_terminal) --

func TestPortTierLadder(t *testing.T) {
	cp := loadContainerPortFixture(t, nil)

	if _, err := cp.Tier("cargo_port_small"); err != nil {
		t.Fatalf("cargo_port_small: %v", err)
	}
	if _, err := cp.Tier("container_terminal"); err != nil {
		t.Fatalf("container_terminal: %v", err)
	}
	deep, err := cp.Tier("deep_sea_terminal")
	if err != nil {
		t.Fatalf("deep_sea_terminal: %v", err)
	}

	// Tiers() is the ascending (milestone, cost) ladder — the deep-sea tier
	// must sort strictly above container_terminal and cargo_port_small.
	idx := map[string]int{}
	for i, tr := range cp.Tiers() {
		idx[tr.Key] = i
	}
	if idx["deep_sea_terminal"] <= idx["container_terminal"] {
		t.Fatalf("deep_sea_terminal must sort strictly above container_terminal (got order %v)", cp.Tiers())
	}
	if idx["container_terminal"] <= idx["cargo_port_small"] {
		t.Fatalf("container_terminal must sort above cargo_port_small (got order %v)", cp.Tiers())
	}

	// The deep-sea tier is a real capacity step, not a renamed container_terminal.
	cont, _ := cp.Tier("container_terminal")
	if deep.CraneRateTonnesPerHour <= cont.CraneRateTonnesPerHour ||
		deep.Berths <= cont.Berths ||
		deep.ShipTonnage <= cont.ShipTonnage {
		t.Fatalf("deep-sea tier must out-thrust container_terminal: deep %+v vs container %+v", deep, cont)
	}

	deepCap, err := cp.TierPhysicalCapacity("deep_sea_terminal")
	if err != nil {
		t.Fatalf("TierPhysicalCapacity: %v", err)
	}
	contCap, err := cp.TierPhysicalCapacity("container_terminal")
	if err != nil {
		t.Fatalf("TierPhysicalCapacity: %v", err)
	}
	if deepCap.TonnesPerDay <= contCap.TonnesPerDay {
		t.Fatalf("deep-sea capacity %d must exceed container_terminal %d", deepCap.TonnesPerDay, contCap.TonnesPerDay)
	}
}

// --- AC-3: capacity figures are data-driven (mutate one tier, only it moves) --

func TestDeepSeaCapacityDataDriven(t *testing.T) {
	cp := loadContainerPortFixture(t, nil)
	baseDeep, err := cp.TierPhysicalCapacity("deep_sea_terminal")
	if err != nil {
		t.Fatalf("TierPhysicalCapacity: %v", err)
	}
	baseCont, err := cp.TierPhysicalCapacity("container_terminal")
	if err != nil {
		t.Fatalf("TierPhysicalCapacity: %v", err)
	}

	// Double ONLY the deep-sea tier's crane rate in the loaded fixture.
	cp2 := loadContainerPortFixture(t, func(m map[string]any) {
		deep := containerPortTier(t, m, "deep_sea_terminal")
		deep["craneRateTonnesPerHour"] = deep["craneRateTonnesPerHour"].(float64) * 2
	})
	newDeep, err := cp2.TierPhysicalCapacity("deep_sea_terminal")
	if err != nil {
		t.Fatalf("TierPhysicalCapacity: %v", err)
	}
	newCont, err := cp2.TierPhysicalCapacity("container_terminal")
	if err != nil {
		t.Fatalf("TierPhysicalCapacity: %v", err)
	}

	if newDeep.TonnesPerDay != baseDeep.TonnesPerDay*2 {
		t.Fatalf("doubling deep-sea crane rate: got %d, want %d", newDeep.TonnesPerDay, baseDeep.TonnesPerDay*2)
	}
	if newCont.TonnesPerDay != baseCont.TonnesPerDay {
		t.Fatalf("container_terminal capacity changed when only deep-sea was mutated: %d -> %d", baseCont.TonnesPerDay, newCont.TonnesPerDay)
	}
}

// --- AC-5: customs throughput separate from physical; smuggling risk rises ---

func TestCustomsSeparateFromPhysical(t *testing.T) {
	// Tiny deep-sea customs capacity so a small export saturates customs while
	// the physical berth/crane throughput is untouched.
	cp := loadContainerPortFixture(t, func(m map[string]any) {
		deep := containerPortTier(t, m, "deep_sea_terminal")
		deep["customsCapacityTonnesPerDay"] = float64(10)
	})
	mustAdvance(t, cp.freight) // produce stock

	beforeRisk, err := cp.SmugglingRisk()
	if err != nil {
		t.Fatalf("SmugglingRisk: %v", err)
	}
	if beforeRisk != 0 {
		t.Fatalf("expected zero smuggling risk before customs demand, got %v", beforeRisk)
	}
	beforePhys, err := cp.PhysicalCapacity()
	if err != nil {
		t.Fatalf("PhysicalCapacity: %v", err)
	}

	// Export 15t (road) — customs demand 15 > 10.
	if _, err := cp.Export("concrete", 15, ModeRoad); err != nil {
		t.Fatalf("Export: %v", err)
	}

	afterPhys, err := cp.PhysicalCapacity()
	if err != nil {
		t.Fatalf("PhysicalCapacity: %v", err)
	}
	if afterPhys.TonnesPerDay != beforePhys.TonnesPerDay {
		t.Fatalf("physical throughput changed by customs demand: %d -> %d", beforePhys.TonnesPerDay, afterPhys.TonnesPerDay)
	}

	sat, err := cp.CustomsSaturation()
	if err != nil {
		t.Fatalf("CustomsSaturation: %v", err)
	}
	if sat.Saturation <= 1 {
		t.Fatalf("expected deep-sea customs saturated (> 1), got %v (demand %d, capacity %d)", sat.Saturation, sat.Demanded, sat.Capacity)
	}
	risk, err := cp.SmugglingRisk()
	if err != nil {
		t.Fatalf("SmugglingRisk: %v", err)
	}
	if risk <= beforeRisk {
		t.Fatalf("smuggling risk did not rise as deep-sea customs saturated: %v -> %v", beforeRisk, risk)
	}
}

// --- AC-6: the importer→exporter flip reads two independently-sourced totals --

func TestImporterExporterFlip(t *testing.T) {
	cp := loadContainerPortFixture(t, nil)
	mustAdvance(t, cp.freight) // produce stock

	// Import first → imports above exports.
	impRes, err := cp.Import("concrete", 10, ModeRoad)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	imp := cp.BalanceOfTrade().Imports.TotalTonnes
	exp := cp.BalanceOfTrade().Exports.TotalTonnes
	if imp != impRes.Moved {
		t.Fatalf("imports total %d != moved %d", imp, impRes.Moved)
	}
	if exp != 0 {
		t.Fatalf("expected zero exports before any export, got %d", exp)
	}

	// Export more than the import → exports crosses imports.
	if _, err := cp.Export("concrete", 25, ModeRoad); err != nil {
		t.Fatalf("Export: %v", err)
	}
	imp2 := cp.BalanceOfTrade().Imports.TotalTonnes
	exp2 := cp.BalanceOfTrade().Exports.TotalTonnes
	if exp2 <= imp2 {
		t.Fatalf("exports did not cross imports: exports %d vs imports %d", exp2, imp2)
	}
	if imp2 != imp {
		t.Fatalf("imports perturbed by an export: %d -> %d", imp, imp2)
	}
	if exp2 != 25 {
		t.Fatalf("exports figure after exporting 25t = %d, want 25", exp2)
	}
}

// --- AC-8: registry-sourced build rejections, no state mutation ------------

func TestNoPermit(t *testing.T) {
	// No permit authority wired at all — permit-gated, never silently buildable.
	cp := loadContainerPortFixture(t, nil)
	err := cp.Build("deep_sea_terminal", 9)
	if !errors.Is(err, &errs.E{Code: ErrContainerPortBuildRejected}) {
		t.Fatalf("Build without permit authority: want ErrContainerPortBuildRejected, got %v", err)
	}
	if cp.ActiveTier() != "" {
		t.Fatalf("state mutated on a rejected build: active tier %q", cp.ActiveTier())
	}

	// Below the milestone gate.
	cp.WirePermit(&stubPermit{grant: true})
	cp.WireDecommission(&stubDecom{})
	err = cp.Build("deep_sea_terminal", 5)
	if !errors.Is(err, &errs.E{Code: ErrContainerPortBuildRejected}) {
		t.Fatalf("Build below milestone: want ErrContainerPortBuildRejected, got %v", err)
	}
	if cp.ActiveTier() != "" {
		t.Fatalf("state mutated on a below-milestone build: %q", cp.ActiveTier())
	}

	// Permit authority wired but denies.
	cp2 := loadContainerPortFixture(t, nil)
	cp2.WirePermit(&stubPermit{grant: false})
	cp2.WireDecommission(&stubDecom{})
	err = cp2.Build("deep_sea_terminal", 9)
	if !errors.Is(err, &errs.E{Code: ErrContainerPortBuildRejected}) {
		t.Fatalf("Build with denied permit: want ErrContainerPortBuildRejected, got %v", err)
	}
	if cp2.ActiveTier() != "" {
		t.Fatalf("state mutated on a denied-permit build: %q", cp2.ActiveTier())
	}
}

func TestBuildSuccess(t *testing.T) {
	cp := loadContainerPortFixture(t, nil)
	cp.WirePermit(&stubPermit{grant: true})
	decom := &stubDecom{}
	cp.WireDecommission(decom)

	if err := cp.Build("deep_sea_terminal", 9); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cp.ActiveTier() != "deep_sea_terminal" {
		t.Fatalf("active tier = %q, want deep_sea_terminal", cp.ActiveTier())
	}
	decom.mu.Lock()
	liability := decom.liabilities["deep_sea_terminal"]
	decom.mu.Unlock()
	if liability <= 0 {
		t.Fatalf("day-one decommission liability was not registered, got %d", liability)
	}
}

func TestUnknownTier(t *testing.T) {
	cp := loadContainerPortFixture(t, nil)
	if _, err := cp.Tier("noSuchTier"); !errors.Is(err, &errs.E{Code: ErrContainerPortUnknownTier}) {
		t.Fatalf("Tier(unknown): want ErrContainerPortUnknownTier, got %v", err)
	}
	if err := cp.Build("noSuchTier", 9); !errors.Is(err, &errs.E{Code: ErrContainerPortUnknownTier}) {
		t.Fatalf("Build(unknown tier): want ErrContainerPortUnknownTier, got %v", err)
	}
	if cp.ActiveTier() != "" {
		t.Fatalf("state mutated on an unknown-tier build: %q", cp.ActiveTier())
	}
}

// --- Defect 4 (upgrade guard): a non-upgrade Build is rejected, no state change --

func TestBuildRejectsDowngrade(t *testing.T) {
	cp := loadContainerPortFixture(t, nil)
	cp.WirePermit(&stubPermit{grant: true})
	decom := &stubDecom{}
	cp.WireDecommission(decom)

	if err := cp.Build("deep_sea_terminal", 9); err != nil {
		t.Fatalf("Build deep_sea_terminal: %v", err)
	}
	if cp.ActiveTier() != "deep_sea_terminal" {
		t.Fatalf("active tier = %q, want deep_sea_terminal", cp.ActiveTier())
	}

	// A downgrade to container_terminal must be rejected with no state change
	// and no second decommission liability.
	if err := cp.Build("container_terminal", 9); !errors.Is(err, &errs.E{Code: ErrContainerPortBuildRejected}) {
		t.Fatalf("downgrade build: want ErrContainerPortBuildRejected, got %v", err)
	}
	if cp.ActiveTier() != "deep_sea_terminal" {
		t.Fatalf("active tier downgraded to %q, want deep_sea_terminal", cp.ActiveTier())
	}

	// A repeat of the same tier is also rejected (not a strict upgrade).
	if err := cp.Build("deep_sea_terminal", 9); !errors.Is(err, &errs.E{Code: ErrContainerPortBuildRejected}) {
		t.Fatalf("repeat build: want ErrContainerPortBuildRejected, got %v", err)
	}

	decom.mu.Lock()
	defer decom.mu.Unlock()
	if len(decom.liabilities) != 1 {
		t.Fatalf("expected exactly 1 decommission liability, got %d: %v", len(decom.liabilities), decom.liabilities)
	}
	if decom.liabilities["deep_sea_terminal"] <= 0 {
		t.Fatalf("deep_sea_terminal liability missing or non-positive: %v", decom.liabilities)
	}
}

func TestBuildAllowsUpgrade(t *testing.T) {
	cp := loadContainerPortFixture(t, nil)
	cp.WirePermit(&stubPermit{grant: true})
	decom := &stubDecom{}
	cp.WireDecommission(decom)

	if err := cp.Build("container_terminal", 9); err != nil {
		t.Fatalf("Build container_terminal: %v", err)
	}
	// deep_sea_terminal is a strict upgrade on (milestone, cost) — allowed.
	if err := cp.Build("deep_sea_terminal", 9); err != nil {
		t.Fatalf("Build deep_sea_terminal (upgrade): %v", err)
	}
	if cp.ActiveTier() != "deep_sea_terminal" {
		t.Fatalf("active tier = %q, want deep_sea_terminal", cp.ActiveTier())
	}
	decom.mu.Lock()
	defer decom.mu.Unlock()
	if len(decom.liabilities) != 2 {
		t.Fatalf("expected 2 liabilities after an upgrade, got %d", len(decom.liabilities))
	}
}

// --- Defect 2 (ladder inversion) + Defect 3 (berths <= 0 / positivity) -------

func TestLadderInversionRejected(t *testing.T) {
	// deep_sea_terminal priced below container_terminal at equal milestone
	// must be rejected at load time — never silently sorted inverted (AC-2).
	err := loadContainerPortErr(t, func(m map[string]any) {
		deep := containerPortTier(t, m, "deep_sea_terminal")
		deep["costMillions"] = float64(100) // below container_terminal's 150
	})
	if !errors.Is(err, &errs.E{Code: ErrContainerPortDataInvalid}) {
		t.Fatalf("deep-sea priced below container: want ErrContainerPortDataInvalid, got %v", err)
	}
}

// TestLadderInversionRejectedMissingMiddleRung is the Destructive-REJECTED
// gap-crossing probe (FEAT-099): when the middle rung container_terminal is
// absent, the pair cargo_port_small vs deep_sea_terminal must STILL be
// compared. Pre-fix, validatePortLadder skipped a missing rung with `continue`,
// so a data file omitting container_terminal AND pricing deep_sea_terminal
// below cargo_port_small loaded with no error — the exact ladder inversion the
// check exists to catch.
func TestLadderInversionRejectedMissingMiddleRung(t *testing.T) {
	err := loadContainerPortErr(t, func(m map[string]any) {
		// Drop the middle rung so only cargo_port_small and deep_sea_terminal
		// remain; the ordering invariant must hold across the gap.
		raw := m["tiers"].([]any)
		kept := make([]any, 0, len(raw))
		for _, e := range raw {
			tm, ok := e.(map[string]any)
			if ok && tm["key"] == "container_terminal" {
				continue
			}
			kept = append(kept, e)
		}
		m["tiers"] = kept

		// Price deep_sea_terminal (M0, £1) below cargo_port_small (M7, £40).
		deep := containerPortTier(t, m, "deep_sea_terminal")
		deep["milestone"] = float64(0)
		deep["costMillions"] = float64(1)
	})
	if !errors.Is(err, &errs.E{Code: ErrContainerPortDataInvalid}) {
		t.Fatalf("deep-sea below cargo_port_small with container_terminal absent: want ErrContainerPortDataInvalid, got %v", err)
	}
}

func TestZeroBerthsRejected(t *testing.T) {
	// berths == 0 (previously only < 0 was rejected) skips the tier's
	// craneRate/hours/customs positivity checks and lets a negative-capacity
	// tier load — now rejected.
	err := loadContainerPortErr(t, func(m map[string]any) {
		deep := containerPortTier(t, m, "deep_sea_terminal")
		deep["berths"] = float64(0)
	})
	if !errors.Is(err, &errs.E{Code: ErrContainerPortDataInvalid}) {
		t.Fatalf("berths == 0: want ErrContainerPortDataInvalid, got %v", err)
	}

	// The Destructive probe: berths 0 + negative craneRate/hours/customs loads
	// with no error (and CustomsCapacity() reports negative). Now rejected.
	err = loadContainerPortErr(t, func(m map[string]any) {
		deep := containerPortTier(t, m, "deep_sea_terminal")
		deep["berths"] = float64(0)
		deep["craneRateTonnesPerHour"] = float64(-5)
		deep["operatingHoursPerDay"] = float64(-10)
		deep["customsCapacityTonnesPerDay"] = float64(-100)
	})
	if !errors.Is(err, &errs.E{Code: ErrContainerPortDataInvalid}) {
		t.Fatalf("berths 0 + negative craneRate/hours/customs: want ErrContainerPortDataInvalid, got %v", err)
	}

	// Positivity is enforced unconditionally: a positive-berths tier with a
	// negative customs capacity must never load (customs must never go negative).
	err = loadContainerPortErr(t, func(m map[string]any) {
		deep := containerPortTier(t, m, "deep_sea_terminal")
		deep["customsCapacityTonnesPerDay"] = float64(-100)
	})
	if !errors.Is(err, &errs.E{Code: ErrContainerPortDataInvalid}) {
		t.Fatalf("negative customs capacity: want ErrContainerPortDataInvalid, got %v", err)
	}
}

// --- AC-9: malformed data/containerport.json → distinct load-time error -----

func TestMalformedContainerport(t *testing.T) {
	err := loadContainerPortErr(t, func(m map[string]any) {
		deep := containerPortTier(t, m, "deep_sea_terminal")
		deep["berths"] = float64(-1)
	})
	if !errors.Is(err, &errs.E{Code: ErrContainerPortDataInvalid}) {
		t.Fatalf("negative berth count: want ErrContainerPortDataInvalid, got %v", err)
	}
}

func TestInvalidContainerport(t *testing.T) {
	// A tier missing its crane rate (berths > 0 but crane rate 0).
	err := loadContainerPortErr(t, func(m map[string]any) {
		deep := containerPortTier(t, m, "deep_sea_terminal")
		deep["craneRateTonnesPerHour"] = float64(0)
	})
	if !errors.Is(err, &errs.E{Code: ErrContainerPortDataInvalid}) {
		t.Fatalf("missing crane rate: want ErrContainerPortDataInvalid, got %v", err)
	}

	// A customs capacity absent (berths > 0 but customs 0).
	err = loadContainerPortErr(t, func(m map[string]any) {
		deep := containerPortTier(t, m, "deep_sea_terminal")
		deep["customsCapacityTonnesPerDay"] = float64(0)
	})
	if !errors.Is(err, &errs.E{Code: ErrContainerPortDataInvalid}) {
		t.Fatalf("absent customs capacity: want ErrContainerPortDataInvalid, got %v", err)
	}
}

// --- AC-10: determinism -----------------------------------------------------

func TestContainerPortDeterminism(t *testing.T) {
	run := func() string {
		cp := loadContainerPortFixture(t, nil)
		rail := newStubRail()
		cp.WireRail(rail)
		cp.WirePermit(&stubPermit{grant: true})
		cp.WireDecommission(&stubDecom{})

		mustAdvance(t, cp.freight)
		_, _ = cp.Import("concrete", 5, ModeRoad)
		_, _ = cp.Export("concrete", 10, ModeRoad)
		_, _ = cp.IntermodalTransfer(ModeSea, ModeRail, 100)
		_, _ = cp.IntermodalTransfer(ModeRail, ModeRoad, 100)
		_ = cp.Build("deep_sea_terminal", 9)

		phys, _ := cp.PhysicalCapacity()
		customs, _ := cp.CustomsCapacity()
		sat, _ := cp.CustomsSaturation()
		risk, _ := cp.SmugglingRisk()
		acct, _ := cp.IntermodalAccount()
		b, _ := json.Marshal(struct {
			Tiers      []PortTier
			ActiveTier string
			Phys       PortCapacity
			Customs    CustomsCapacity
			Saturation CustomsSaturation
			Risk       float64
			Trade      BalanceOfTrade
			Account    IntermodalAccount
		}{
			Tiers:      cp.Tiers(),
			ActiveTier: cp.ActiveTier(),
			Phys:       phys,
			Customs:    customs,
			Saturation: sat,
			Risk:       risk,
			Trade:      cp.BalanceOfTrade(),
			Account:    acct,
		})
		return string(b)
	}
	if a, b := run(), run(); a != b {
		t.Fatalf("determinism violated:\n%s\n--- vs ---\n%s", a, b)
	}
}

// TestTiersDeterministicEqualRanks is the Destructive-REJECTED GR#21 probe:
// two tiers sharing an equal (milestone, cost) rank must still yield a
// deterministic Tiers() order. The pre-fix sort.Slice was unstable AND seeded
// from map iteration, so a valid data file carrying a peer tier at the same
// rank produced distinct Tiers() orderings across loads. The fix breaks the
// tie on the tier key, so the order is total and load-independent.
func TestTiersDeterministicEqualRanks(t *testing.T) {
	// Append a peer tier with the SAME (milestone=9, cost=400) rank as
	// deep_sea_terminal. It is not part of the documented ladder, so
	// validatePortLadder skips it and the equal rank loads silently — the exact
	// shape the verdict used to expose the non-determinism.
	mutate := func(m map[string]any) {
		deep := containerPortTier(t, m, "deep_sea_terminal")
		peer := map[string]any{}
		for k, v := range deep {
			peer[k] = v
		}
		peer["key"] = "deep_sea_terminal_peer"
		peer["name"] = "Deep-sea terminal (peer)"
		tiers := m["tiers"].([]any)
		m["tiers"] = append(tiers, peer)
	}

	var want string
	for i := 0; i < 200; i++ {
		cp := loadContainerPortFixture(t, mutate)
		var order []string
		for _, tr := range cp.Tiers() {
			order = append(order, tr.Key)
		}
		got := strings.Join(order, ",")
		if want == "" {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("Tiers() order non-deterministic across loads: load 1 %q vs load %d %q", want, i+1, got)
		}
	}
	// The tie-break is the tier key, so the peer sorts after deep_sea_terminal
	// (its key is a strict suffix). Locks the direction, not just stability.
	if want != "cargo_port_small,container_terminal,deep_sea_terminal,deep_sea_terminal_peer" {
		t.Fatalf("Tiers() order = %q, want the (milestone, cost, key)-sorted ladder", want)
	}
}

// --- AC-12: concurrency (race-checked) --------------------------------------

func TestContainerPortRace(t *testing.T) {
	cp := loadContainerPortFixture(t, nil)
	cp.WireRail(newStubRail())
	mustAdvance(t, cp.freight)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = cp.PhysicalCapacity()
				_, _ = cp.CustomsCapacity()
				_, _ = cp.CustomsSaturation()
				_, _ = cp.SmugglingRisk()
				_ = cp.Tiers()
				_ = cp.BalanceOfTrade()
				_, _ = cp.IntermodalTransfer(ModeSea, ModeRail, 1)
			}
		}()
	}
	wg.Wait()
}
