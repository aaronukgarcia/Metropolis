package chemicals

import (
	"errors"
	"math"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// refineryByteCopy performs SEC-020's attack — a plain Refinery struct copy —
// via a raw byte-for-byte memcpy through unsafe.Pointer, the sanctioned
// TEST-ONLY mechanism (mirroring internal/foundation/errs/copyguard_test.go's
// loggerByteCopy): a literal `c := *r` is legal Go but go vet's copylocks check
// statically flags it, and this package must pass `go vet ./...`. The byte copy
// produces identical runtime semantics (self's pointer bytes copied unchanged)
// without a statically-flaggable copy expression.
func refineryByteCopy(r *Refinery) *Refinery {
	c := new(Refinery)
	*(*[unsafe.Sizeof(Refinery{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Refinery{})]byte)(unsafe.Pointer(r))
	return c
}

// chemAPIByteCopy is the ChemAPI equivalent of refineryByteCopy.
func chemAPIByteCopy(c *ChemAPI) *ChemAPI {
	out := new(ChemAPI)
	*(*[unsafe.Sizeof(ChemAPI{})]byte)(unsafe.Pointer(out)) = *(*[unsafe.Sizeof(ChemAPI{})]byte)(unsafe.Pointer(c))
	return out
}

// This file is the Destructive-REJECT regression suite for engine.chemicals
// (MOD-063): the SEC-132..SEC-137 weakness classes, fixed as classes not
// instances. Every test here is demonstrated to fail against the pre-fix code
// (see the delivery note for the baseline used); the stable-signature tests
// fail at runtime, the signature-change tests fail because the pre-fix API had
// no error channel to observe the rejection at all.

// ---- SEC-132: the exported setter enforces the same domain the loader does ----

func TestSetImportMarginRejectsInvalid(t *testing.T) {
	chem := NewChemAPI(errs.NewCorrelationID())

	for _, bad := range []struct {
		name   string
		comm   string
		margin int64
	}{
		{"negative margin", commodityRefinedProduct, -100},
		{"zero margin", commodityRefinedProduct, 0},
		{"empty commodity", "", 100},
	} {
		t.Run(bad.name, func(t *testing.T) {
			if err := chem.SetImportMargin(bad.comm, bad.margin); !errors.Is(err, &errs.E{Code: ErrRefineryDataInvalid}) {
				t.Fatalf("expected ErrRefineryDataInvalid, got %v", err)
			}
		})
	}

	// A rejected margin is never stored (no partial state).
	if _, ok, err := chem.ImportMargin(commodityRefinedProduct); err != nil || ok {
		t.Fatalf("rejected margin was stored: ok=%v err=%v", ok, err)
	}

	// A valid margin is stored and read back.
	if err := chem.SetImportMargin(commodityRefinedProduct, 200000000); err != nil {
		t.Fatalf("valid margin rejected: %v", err)
	}
	if m, ok, err := chem.ImportMargin(commodityRefinedProduct); err != nil || !ok || m != 200000000 {
		t.Fatalf("valid margin not stored: m=%d ok=%v err=%v", m, ok, err)
	}
}

// ---- SEC-132/137: ImportRefined rejects a non-positive margin and overflow ----
// Margins are injected white-box so these assertions run against the pre-fix
// code too (ImportRefined's signature is unchanged).

func TestImportRefinedRejectsNonPositiveMargin(t *testing.T) {
	chem := NewChemAPI(errs.NewCorrelationID())
	for _, margin := range []int64{-100, 0} {
		chem.importMargin[commodityRefinedProduct] = margin
		if cost, err := chem.ImportRefined(commodityRefinedProduct, 5000); !errors.Is(err, &errs.E{Code: ErrRefineryDataInvalid}) {
			t.Fatalf("margin %d: expected ErrRefineryDataInvalid, got cost=%d err=%v", margin, cost, err)
		}
	}
}

func TestImportRefinedRejectsOverflow(t *testing.T) {
	chem := NewChemAPI(errs.NewCorrelationID())
	chem.importMargin[commodityRefinedProduct] = 200000000 // £200/tonne
	if cost, err := chem.ImportRefined(commodityRefinedProduct, math.MaxInt64); !errors.Is(err, &errs.E{Code: ErrRefineryDataInvalid}) {
		t.Fatalf("expected overflow rejection, got cost=%d err=%v", cost, err)
	}
}

// ---- SEC-133: a duplicate output commodity is rejected, not silently diverged ----

func TestDuplicateOutputCommodityRejected(t *testing.T) {
	dir := writeRefineryFixture(t, func(m map[string]any) {
		ref := m["facilities"].(map[string]any)["refinery"].(map[string]any)
		outs := ref["outputs"].([]any)
		// fuel=10000 then fuel=1: two readers would disagree (first vs last).
		ref["outputs"] = append(outs, map[string]any{"commodity": "fuel", "tonnesPerDay": float64(1)})
	})
	r, err := LoadRefinery(dir, errs.NewCorrelationID(), seedA)
	if !errors.Is(err, &errs.E{Code: ErrRefineryDataInvalid}) {
		t.Fatalf("expected ErrRefineryDataInvalid for duplicate output, got %v", err)
	}
	if r != nil {
		t.Fatal("duplicate output must not produce a facility (no partial state)")
	}
}

// ---- SEC-134: throughput is a cap, not merely a scaling denominator ----

func TestOperateClampsLandedToThroughput(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	refinery, _ := r.Facility("refinery")
	fuelRate, _ := refinery.Output(commodityFuel)

	// Pass-through freight: whatever Operate requests is "landed".
	wireOperate(t, r, &stubFreight{capT: -1}, &stubFuel{}, &stubDispatch{})
	res, err := r.Operate(0, refinery.ThroughputTonnesPerDay*1000)
	if err != nil {
		t.Fatalf("operate: %v", err)
	}
	if res.FuelOutput != fuelRate {
		t.Fatalf("fuel output %d != rated %d (throughput must cap output)", res.FuelOutput, fuelRate)
	}
	if res.CrudeLanded != refinery.ThroughputTonnesPerDay {
		t.Fatalf("crude landed %d != throughput cap %d", res.CrudeLanded, refinery.ThroughputTonnesPerDay)
	}
}

// ---- SEC-135: upstream = registered-before, never every-other-stage ----

func TestStageInputCannotDrawFromDownstream(t *testing.T) {
	chem := NewChemAPI(errs.NewCorrelationID())
	// A consumer registered FIRST has no upstream stage.
	if err := chem.RegisterStage("consumer", map[string]int64{"feedstock": 150}, map[string]int64{}); err != nil {
		t.Fatalf("register consumer: %v", err)
	}
	// Two stages registered AFTER it each output feedstock.
	if err := chem.RegisterStage("a", map[string]int64{}, map[string]int64{"feedstock": 100}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := chem.RegisterStage("b", map[string]int64{}, map[string]int64{"feedstock": 200}); err != nil {
		t.Fatalf("register b: %v", err)
	}

	in, err := chem.StageInput("consumer")
	if err != nil {
		t.Fatalf("stage input: %v", err)
	}
	if in["feedstock"] != 0 {
		t.Fatalf("consumer drew %d feedstock from downstream stages; want 0 (no upstream)", in["feedstock"])
	}
}

func TestStageInputCycleCannotManufactureTonnage(t *testing.T) {
	chem := NewChemAPI(errs.NewCorrelationID())
	// A draws X and outputs Y; B draws Y and outputs X — a mutual-dependency pair.
	if err := chem.RegisterStage("a", map[string]int64{"x": 100}, map[string]int64{"y": 50}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := chem.RegisterStage("b", map[string]int64{"y": 100}, map[string]int64{"x": 50}); err != nil {
		t.Fatalf("register b: %v", err)
	}

	// A is registered first: it has no upstream, so it cannot draw B's X.
	inA, err := chem.StageInput("a")
	if err != nil {
		t.Fatalf("stage input a: %v", err)
	}
	if inA["x"] != 0 {
		t.Fatalf("A drew %d X from downstream B; want 0 (tonnage manufactured from nothing)", inA["x"])
	}

	// B may draw A's Y, bounded by A's routed output.
	inB, err := chem.StageInput("b")
	if err != nil {
		t.Fatalf("stage input b: %v", err)
	}
	if inB["y"] != 50 {
		t.Fatalf("B drew %d Y; want 50 (A's routed output)", inB["y"])
	}
}

// ---- SEC-136: copy-guard getters return the error, not a sentinel ----

func TestCopiedRefineryGettersReturnError(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	copied := refineryByteCopy(r)

	if _, err := copied.Facilities(); !errors.Is(err, &errs.E{Code: ErrRefineryCopied}) {
		t.Fatalf("copied.Facilities: expected ErrRefineryCopied, got %v", err)
	}
	if _, err := copied.Built(); !errors.Is(err, &errs.E{Code: ErrRefineryCopied}) {
		t.Fatalf("copied.Built: expected ErrRefineryCopied, got %v", err)
	}
	// Sanity: the pointer's own getters still work.
	if _, err := r.Facilities(); err != nil {
		t.Fatalf("original.Facilities: unexpected error %v", err)
	}
}

func TestCopiedChemAPIGettersReturnError(t *testing.T) {
	chem := NewChemAPI(errs.NewCorrelationID())
	copied := chemAPIByteCopy(chem)

	if _, _, err := copied.ImportMargin(commodityRefinedProduct); !errors.Is(err, &errs.E{Code: ErrRefineryCopied}) {
		t.Fatalf("copied.ImportMargin: expected ErrRefineryCopied, got %v", err)
	}
	if err := copied.SetImportMargin(commodityRefinedProduct, 100); !errors.Is(err, &errs.E{Code: ErrRefineryCopied}) {
		t.Fatalf("copied.SetImportMargin: expected ErrRefineryCopied, got %v", err)
	}
}

// ---- SEC-160: Wire* setters reject a struct-copied Refinery, not silently wire a dead copy ----

func TestCopiedRefineryWireReturnsError(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	copied := refineryByteCopy(r)

	// Each Wire* method on a byte-copied Refinery must surface ErrRefineryCopied
	// rather than silently wiring the copy's own seam (SEC-160 impact 1: silent
	// dead wiring).
	if err := copied.WireFreight(&stubFreight{capT: -1}); !errors.Is(err, &errs.E{Code: ErrRefineryCopied}) {
		t.Fatalf("copied.WireFreight: expected ErrRefineryCopied, got %v", err)
	}
	if err := copied.WireFuel(&stubFuel{}); !errors.Is(err, &errs.E{Code: ErrRefineryCopied}) {
		t.Fatalf("copied.WireFuel: expected ErrRefineryCopied, got %v", err)
	}
	if err := copied.WireDispatch(&stubDispatch{}); !errors.Is(err, &errs.E{Code: ErrRefineryCopied}) {
		t.Fatalf("copied.WireDispatch: expected ErrRefineryCopied, got %v", err)
	}
	if err := copied.WirePermit(&stubPermit{granted: true}); !errors.Is(err, &errs.E{Code: ErrRefineryCopied}) {
		t.Fatalf("copied.WirePermit: expected ErrRefineryCopied, got %v", err)
	}
	if err := copied.WireDecommission(&stubDecom{liabilities: map[string]int64{}}); !errors.Is(err, &errs.E{Code: ErrRefineryCopied}) {
		t.Fatalf("copied.WireDecommission: expected ErrRefineryCopied, got %v", err)
	}

	// The rejection must leave the copy's seams unwired — checkNotCopied runs
	// before the lock, so no write happens on a rejected call. This is what
	// distinguishes "rejected" from "wired then rejected" (no side effect on the
	// copy).
	if copied.freight != nil || copied.fuel != nil || copied.dispatch != nil || copied.permit != nil || copied.decom != nil {
		t.Fatalf("rejected Wire* left a seam wired on the copy: freight=%v fuel=%v dispatch=%v permit=%v decom=%v",
			copied.freight, copied.fuel, copied.dispatch, copied.permit, copied.decom)
	}

	// Sanity: the original's Wire* still wires — the guard rejects the copy, not
	// legitimate wiring.
	if err := r.WireFreight(&stubFreight{capT: -1}); err != nil {
		t.Fatalf("original.WireFreight: unexpected error %v", err)
	}
	if err := r.WireFuel(&stubFuel{}); err != nil {
		t.Fatalf("original.WireFuel: unexpected error %v", err)
	}
	if err := r.WireDispatch(&stubDispatch{}); err != nil {
		t.Fatalf("original.WireDispatch: unexpected error %v", err)
	}
	if err := r.WirePermit(&stubPermit{granted: true}); err != nil {
		t.Fatalf("original.WirePermit: unexpected error %v", err)
	}
	if err := r.WireDecommission(&stubDecom{liabilities: map[string]int64{}}); err != nil {
		t.Fatalf("original.WireDecommission: unexpected error %v", err)
	}
}

// ---- SEC-137 (scaleTonnes site): overflow is reported, not discarded ----

func TestScaleTonnesReportsOverflow(t *testing.T) {
	if out, overflow := scaleTonnes(1<<62, 1<<62, 1); !overflow {
		t.Fatalf("expected overflow for huge product, got out=%d overflow=%v", out, overflow)
	}
	if out, overflow := scaleTonnes(100, 10, 200); overflow || out != 5 {
		t.Fatalf("expected 5 without overflow, got out=%d overflow=%v", out, overflow)
	}
	if out, overflow := scaleTonnes(0, 10, 200); overflow || out != 0 {
		t.Fatalf("expected 0 (no input) without overflow, got out=%d overflow=%v", out, overflow)
	}
}

// ---- SEC-164: Chem() must not hand a struct-copied Refinery the original's live chain ----

// TestCopiedRefineryChemReturnsError is the SEC-164 regression: a byte-copied
// Refinery's Chem() must be rejected with ErrRefineryCopied, never hand out the
// original's live chain. The original's own Chem() still returns its live chain.
func TestCopiedRefineryChemReturnsError(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	copied := refineryByteCopy(r)

	if chem, err := copied.Chem(); !errors.Is(err, &errs.E{Code: ErrRefineryCopied}) {
		t.Fatalf("copied.Chem: expected ErrRefineryCopied, got chem=%v err=%v", chem, err)
	} else if chem != nil {
		t.Fatalf("copied.Chem must not return a chain alongside the rejection, got %v", chem)
	}

	// The original's own accessor still returns the live, shared chain.
	chem, err := r.Chem()
	if err != nil {
		t.Fatalf("original.Chem: unexpected error %v", err)
	}
	if chem != r.chem {
		t.Fatal("original.Chem did not return the facility's live chain")
	}
}

// ---- SEC-165: Operate rejects negative crude tonnage at the seam boundary ----

func TestOperateRejectsNegativeCrude(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	wireOperate(t, r, &stubFreight{capT: -1}, &stubFuel{}, &stubDispatch{})

	res, err := r.Operate(0, -100)
	if !errors.Is(err, &errs.E{Code: ErrRefineryNegativeCrude}) {
		t.Fatalf("expected ErrRefineryNegativeCrude for negative crude, got res=%+v err=%v", res, err)
	}
}

// TestOperateRejectsScaledOutputOverflow exercises the Operate integration of
// the scaleTonnes overflow: a data file whose output rate would overflow the
// int64 product is rejected, not silently saturated into a bogus supply feed.
func TestOperateRejectsScaledOutputOverflow(t *testing.T) {
	dir := writeRefineryFixture(t, func(m map[string]any) {
		outs := m["facilities"].(map[string]any)["refinery"].(map[string]any)["outputs"].([]any)
		outs[0].(map[string]any)["tonnesPerDay"] = float64(500000000000000) // huge fuel rate
	})
	r, err := LoadRefinery(dir, errs.NewCorrelationID(), seedA)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	refinery, _ := r.Facility("refinery")
	wireOperate(t, r, &stubFreight{capT: -1}, &stubFuel{}, &stubDispatch{})

	if _, err := r.Operate(0, refinery.ThroughputTonnesPerDay); !errors.Is(err, &errs.E{Code: ErrRefineryDataInvalid}) {
		t.Fatalf("expected ErrRefineryDataInvalid for scaled output overflow, got %v", err)
	}
}

// ---- SEC-166: a freight seam returning a negative landing is rejected ----

// negativeFreight is a FreightAPI seam that returns a negative landing for any
// request, the SEC-166 attack shape (a seam violating its own no-negative
// contract).
type negativeFreight struct{}

func (negativeFreight) CrudeLanding(int64) (int64, error) { return -50, nil }

func TestOperateRejectsNegativeLanding(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	if err := r.WireFreight(negativeFreight{}); err != nil {
		t.Fatalf("wire freight: %v", err)
	}
	if err := r.WireFuel(&stubFuel{}); err != nil {
		t.Fatalf("wire fuel: %v", err)
	}
	if err := r.WireDispatch(&stubDispatch{}); err != nil {
		t.Fatalf("wire dispatch: %v", err)
	}
	refinery, _ := r.Facility("refinery")

	res, err := r.Operate(0, refinery.ThroughputTonnesPerDay)
	if !errors.Is(err, &errs.E{Code: ErrRefineryNegativeCrude}) {
		t.Fatalf("expected ErrRefineryNegativeCrude for a negative landing, got res=%+v err=%v", res, err)
	}
}

// ---- SEC-168: a refinery facility omitting a structural output is rejected ----

// TestRefineryFacilityWithoutStructuralOutputRejected proves the load-time
// rejection: a schema-valid data file whose refinery outputs omit fuel (or the
// feedstock sibling) must fail to load, never silently feed zero (SEC-168).
func TestRefineryFacilityWithoutStructuralOutputRejected(t *testing.T) {
	cases := []struct {
		name string
		drop string
	}{
		{"missing fuel output", commodityFuel},
		{"missing feedstock output", commodityFeedstock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeRefineryFixture(t, func(m map[string]any) {
				ref := m["facilities"].(map[string]any)["refinery"].(map[string]any)
				outs := ref["outputs"].([]any)
				kept := make([]any, 0, len(outs))
				for _, o := range outs {
					if o.(map[string]any)["commodity"] != tc.drop {
						kept = append(kept, o)
					}
				}
				ref["outputs"] = kept
			})
			r, err := LoadRefinery(dir, errs.NewCorrelationID(), seedA)
			if !errors.Is(err, &errs.E{Code: ErrRefineryDataInvalid}) {
				t.Fatalf("expected ErrRefineryDataInvalid for a refinery missing %q, got %v", tc.drop, err)
			}
			if r != nil {
				t.Fatal("a refinery without a structural output must not produce a facility (no partial state)")
			}
		})
	}
}

// TestOperateRejectsProfileWithoutStructuralOutput exercises Operate's own
// defense-in-depth check: the load-time check cannot be bypassed through the
// public API, so this white-box strips the fuel output from the loaded profile
// and proves Operate still rejects rather than silently feeding zero fuel
// (SEC-132's loader-plus-setter same-domain precedent).
func TestOperateRejectsProfileWithoutStructuralOutput(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	buildRefinery(t, r, &stubPermit{granted: true}, &stubDecom{liabilities: map[string]int64{}})
	wireOperate(t, r, &stubFreight{capT: -1}, &stubFuel{}, &stubDispatch{})
	refinery, _ := r.Facility("refinery")

	feedRate, _ := refinery.Output(commodityFeedstock)
	r.mu.Lock()
	p := r.byKey[facilityRefinery]
	p.Outputs = []ChainOutput{{Commodity: commodityFeedstock, TonnesPerDay: feedRate}}
	r.byKey[facilityRefinery] = p
	r.mu.Unlock()

	res, err := r.Operate(0, refinery.ThroughputTonnesPerDay)
	if !errors.Is(err, &errs.E{Code: ErrRefineryDataInvalid}) {
		t.Fatalf("expected ErrRefineryDataInvalid for a profile missing fuel, got res=%+v err=%v", res, err)
	}
}

// ---- SEC-169: RegisterStage rejects non-positive tonnage in inputs/outputs ----
//
// SEC-169 is the SEC-132 loader-plus-setter gap re-opened for stage tonnage:
// the loader (buildFacilityProfile) enforces a strictly positive t/day for both
// the input throughput and every output rate, but the exported RegisterStage
// accepted any int64, so a negative tonnage was stored and StageInput surfaced
// it as negative "available input" with a nil error. This test is the class
// matrix: negative and zero tonnage in either map, plus the empty-commodity
// sibling the loader also rejects — each rejected with ErrRefineryDataInvalid
// and no partial state, so StageInput/StageOutput can never surface a negative.

func TestRegisterStageRejectsInvalidTonnage(t *testing.T) {
	chem := NewChemAPI(errs.NewCorrelationID())

	cases := []struct {
		name    string
		inputs  map[string]int64
		outputs map[string]int64
	}{
		{"negative input tonnage", map[string]int64{"crude_oil": -100}, map[string]int64{"fuel": 50}},
		{"zero input tonnage", map[string]int64{"crude_oil": 0}, map[string]int64{"fuel": 50}},
		{"negative output tonnage", map[string]int64{"crude_oil": 100}, map[string]int64{"fuel": -50}},
		{"zero output tonnage", map[string]int64{"crude_oil": 100}, map[string]int64{"fuel": 0}},
		{"empty input commodity", map[string]int64{"": 100}, map[string]int64{"fuel": 50}},
		{"empty output commodity", map[string]int64{"crude_oil": 100}, map[string]int64{"": 50}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := chem.RegisterStage("s", tc.inputs, tc.outputs); !errors.Is(err, &errs.E{Code: ErrRefineryDataInvalid}) {
				t.Fatalf("expected ErrRefineryDataInvalid, got %v", err)
			}
			// Rejection leaves no partial state: the stage is not stored, so
			// StageInput/StageOutput cannot surface the invalid figure later.
			if _, err := chem.StageInput("s"); !errors.Is(err, &errs.E{Code: ErrUnregisteredStage}) {
				t.Fatalf("rejected stage was stored (StageInput err=%v)", err)
			}
			if _, err := chem.StageOutput("s"); !errors.Is(err, &errs.E{Code: ErrUnregisteredStage}) {
				t.Fatalf("rejected stage was stored (StageOutput err=%v)", err)
			}
		})
	}

	// Positive control: a valid stage (a sink with no inputs, one positive
	// output) still registers, and StageOutput surfaces the positive figure.
	if err := chem.RegisterStage("ok", map[string]int64{}, map[string]int64{"fuel": 50}); err != nil {
		t.Fatalf("valid stage rejected: %v", err)
	}
	out, err := chem.StageOutput("ok")
	if err != nil {
		t.Fatalf("stage output: %v", err)
	}
	if out["fuel"] != 50 {
		t.Fatalf("stage output fuel = %d, want 50", out["fuel"])
	}
}

// TestRegisterStageRejectsNegativeTonnageAtRegistration reproduces the exact
// SEC-169 shape end-to-end: an upstream stage with a negative output, followed
// by a downstream stage demanding that commodity, must be rejected at the
// registration boundary (before either is stored), so a downstream consumer can
// never observe a negative available-input figure.
func TestRegisterStageRejectsNegativeTonnageAtRegistration(t *testing.T) {
	chem := NewChemAPI(errs.NewCorrelationID())
	if err := chem.RegisterStage("upstream", map[string]int64{}, map[string]int64{"feedstock": -100}); !errors.Is(err, &errs.E{Code: ErrRefineryDataInvalid}) {
		t.Fatalf("upstream negative output: expected ErrRefineryDataInvalid, got %v", err)
	}
	// The downstream stage never gets the chance to draw from a negative output.
	if err := chem.RegisterStage("downstream", map[string]int64{"feedstock": 50}, map[string]int64{}); err != nil {
		t.Fatalf("downstream register: %v", err)
	}
	in, err := chem.StageInput("downstream")
	if err != nil {
		t.Fatalf("stage input: %v", err)
	}
	if in["feedstock"] < 0 {
		t.Fatalf("StageInput surfaced negative available input %d", in["feedstock"])
	}
}

// ---- SEC-170: Build must not hold r.mu across the permit/decommission seams ----
//
// SEC-170 is the "a lock held across an external seam/callback" deadlock class
// (the same class the airport module's SEC-119 closed). Build held r.mu — the
// single RWMutex guarding all Refinery state — across PermitGranted and
// RegisterLiability, so a seam that calls back into any Refinery read method
// (Built, Facility, Facilities, Operate) re-entered r.mu and wedged the whole
// facility surface permanently. The regression below wires a re-entrant seam
// whose callback invokes Built() and asserts Build still returns. The deadlock
// is deterministic (a non-reentrant RWMutex blocks a nested RLock 100% of the
// time), so a fail-fast timeout guard is the correct failure signal, never a
// timing race.

// reentrantPermit is the SEC-170 permit seam attack shape: PermitGranted calls
// back into the Refinery surface (Built) while Build is mid-flight.
type reentrantPermit struct {
	r       *Refinery
	granted bool
}

func (p *reentrantPermit) PermitGranted(string, int) (bool, error) {
	if _, err := p.r.Built(); err != nil {
		return false, err
	}
	return p.granted, nil
}

// reentrantDecom is the SEC-170 decommission seam attack shape:
// RegisterLiability calls back into Built() while Build is mid-flight.
type reentrantDecom struct {
	r *Refinery
}

func (d *reentrantDecom) RegisterLiability(string, int64) error {
	if _, err := d.r.Built(); err != nil {
		return err
	}
	return nil
}

func TestBuildDoesNotDeadlockOnReentrantPermit(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	if err := r.WirePermit(&reentrantPermit{r: r, granted: true}); err != nil {
		t.Fatalf("wire permit: %v", err)
	}
	if err := r.WireDecommission(&stubDecom{liabilities: map[string]int64{}}); err != nil {
		t.Fatalf("wire decommission: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- r.Build(9) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Build deadlocked: a re-entrant permit seam calling Built() never returned (r.mu held across PermitGranted)")
	}

	if built, err := r.Built(); err != nil {
		t.Fatalf("Built: %v", err)
	} else if !built {
		t.Fatal("refinery not built after a successful Build")
	}
}

func TestBuildDoesNotDeadlockOnReentrantDecommission(t *testing.T) {
	r := loadRealRefinery(t, seedA)
	if err := r.WirePermit(&stubPermit{granted: true}); err != nil {
		t.Fatalf("wire permit: %v", err)
	}
	if err := r.WireDecommission(&reentrantDecom{r: r}); err != nil {
		t.Fatalf("wire decommission: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- r.Build(9) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Build deadlocked: a re-entrant decommission seam calling Built() never returned (r.mu held across RegisterLiability)")
	}

	if built, err := r.Built(); err != nil {
		t.Fatalf("Built: %v", err)
	} else if !built {
		t.Fatal("refinery not built after a successful Build")
	}
}
