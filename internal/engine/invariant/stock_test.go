package invariant

import (
	"math"
	"reflect"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Multi-term fixtures use a refuse-mass-shaped stock (BUG-067's motivating
// example): generated + imported inflows against collected + composted +
// landfilled outflows. The numbers are chosen so every outflow is nonzero and
// distinct from the inflows — a check that drops an outflow term, adds it
// instead of subtracting it, or ignores outs entirely must fail the balanced
// fixture below (weakness pattern #1: the check must be a real arithmetic
// identity, not a one-scalar echo).
const (
	refuseOpening    = int64(1000)
	refuseGenerated  = int64(500)
	refuseImported   = int64(200)
	refuseCollected  = int64(300)
	refuseComposted  = int64(150)
	refuseLandfilled = int64(50)
)

// refuseTracked is the BUG-058 identity's right-hand side for the fixture:
// Σ(ins) − Σ(outs) = generated + imported − collected − composted − landfilled.
const refuseTracked = refuseGenerated + refuseImported - refuseCollected - refuseComposted - refuseLandfilled // 200

func refuseTermFuncs() (ins, outs map[string]TermFunc) {
	ins = map[string]TermFunc{
		"generated": func() int64 { return refuseGenerated },
		"imported":  func() int64 { return refuseImported },
	}
	outs = map[string]TermFunc{
		"collected":  func() int64 { return refuseCollected },
		"composted":  func() int64 { return refuseComposted },
		"landfilled": func() int64 { return refuseLandfilled },
	}
	return ins, outs
}

func registerRefuse(t *testing.T) *Registry {
	t.Helper()
	reg := NewRegistry()
	ins, outs := refuseTermFuncs()
	if err := RegisterStockWithTerms(reg, "refuse", StockName("refuse"), ins, outs); err != nil {
		t.Fatalf("RegisterStockWithTerms: %v", err)
	}
	return reg
}

func refuseSnapshot(closing int64) Snapshot {
	s := NewSnapshot(1)
	s.Readings[StockName("refuse")] = StockReading{Registered: true, Opening: refuseOpening, Closing: closing}
	return s
}

// TestRegisterStockWithTerms_Balanced proves the multi-term invariant does not
// cry wolf on a tick whose level delta exactly equals Σ(ins) − Σ(outs) — the
// REAL arithmetic identity, computed from the registered terms, not a scalar.
func TestRegisterStockWithTerms_Balanced(t *testing.T) {
	reg := registerRefuse(t)

	// Closing = opening + tracked keeps the identity balanced.
	got := RunSuite(reg, refuseSnapshot(refuseOpening+refuseTracked))

	if len(got.Outcomes) != 1 || !got.Outcomes[0].Ran {
		t.Fatalf("Outcomes = %+v, want exactly one Ran outcome", got.Outcomes)
	}
	if got.Outcomes[0].Violation.Detected {
		t.Fatalf("reported a Violation on a tick where Closing−Opening == Σ(ins)−Σ(outs): %+v", got.Outcomes[0].Violation)
	}
	if got.AnyViolation || !got.AllRan {
		t.Fatalf("AnyViolation=%v AllRan=%v, want false/true", got.AnyViolation, got.AllRan)
	}
}

// TestRegisterStockWithTerms_DetectsMismatch is the quality-bar test: a tick
// whose delta ≠ Σ(ins) − Σ(outs) MUST report a Violation with Expected ==
// Σ(ins) − Σ(outs), Actual == Closing − Opening, and the signed per-term
// breakdown. If this test can pass while the identity is wrong, the invariant
// is nominal — the exact failure BUG-067 is filed against.
func TestRegisterStockWithTerms_DetectsMismatch(t *testing.T) {
	reg := registerRefuse(t)

	// Actual delta = 150 (closing 1150 − opening 1000), tracked = 200.
	closing := refuseOpening + refuseTracked - 50 // 1150: 50 unexplained
	got := RunSuite(reg, refuseSnapshot(closing))

	if len(got.Outcomes) != 1 || !got.Outcomes[0].Ran {
		t.Fatalf("Outcomes = %+v, want exactly one Ran outcome", got.Outcomes)
	}
	v := got.Outcomes[0].Violation
	if !v.Detected {
		t.Fatal("did not detect a tick where delta ≠ Σ(ins) − Σ(outs) — the invariant cannot fail, which makes it worthless (BUG-067)")
	}
	if v.InvariantName != "refuse" {
		t.Errorf("InvariantName = %q, want %q", v.InvariantName, "refuse")
	}
	wantActual := closing - refuseOpening
	if v.Expected != refuseTracked || v.Actual != wantActual {
		t.Errorf("Expected/Actual = %d/%d, want %d/%d", v.Expected, v.Actual, refuseTracked, wantActual)
	}

	// The per-term breakdown must carry the named, signed contributions:
	// Σ(Terms) == Expected, ins positive, outs negative.
	wantTerms := map[string]int64{
		"generated":  refuseGenerated,
		"imported":   refuseImported,
		"collected":  -refuseCollected,
		"composted":  -refuseComposted,
		"landfilled": -refuseLandfilled,
	}
	if !reflect.DeepEqual(v.Terms, wantTerms) {
		t.Errorf("Violation.Terms = %v, want %v", v.Terms, wantTerms)
	}
	var sum int64
	for _, val := range v.Terms {
		sum += val
	}
	if sum != v.Expected {
		t.Errorf("Σ(Terms) = %d, want Expected %d — the breakdown must reconcile to the tracked delta", sum, v.Expected)
	}
}

// TestRegisterStockWithTerms_NilTermFunc proves GR#7's boundary rejection: a
// nil term function is refused at registration (ErrNilTermFunc), not deferred
// to a panic in the tick loop.
func TestRegisterStockWithTerms_NilTermFunc(t *testing.T) {
	reg := NewRegistry()

	err := RegisterStockWithTerms(reg, "refuse", StockName("refuse"),
		map[string]TermFunc{"generated": nil}, nil)
	if err == nil {
		t.Fatal("RegisterStockWithTerms with a nil ins term = nil error, want ErrNilTermFunc")
	}
	if e, ok := err.(*errs.E); !ok || e.Code != ErrNilTermFunc {
		t.Errorf("error = %v, want *errs.E with code %q", err, ErrNilTermFunc)
	}

	err = RegisterStockWithTerms(reg, "refuse", StockName("refuse"), nil,
		map[string]TermFunc{"collected": nil})
	if err == nil {
		t.Fatal("RegisterStockWithTerms with a nil outs term = nil error, want ErrNilTermFunc")
	}

	err = RegisterStock(reg, "refuse", StockName("refuse"), nil)
	if err == nil {
		t.Fatal("RegisterStock with a nil snapshot = nil error, want ErrNilTermFunc")
	}
}

// TestRegisterStock_SingleTermBackwardCompatible proves the plan's single-term
// RegisterStock(name, snapshot func() int64) shape still works: snapshot is
// the one pre-summed tracked-delta term, checked as Closing − Opening ==
// snapshot().
func TestRegisterStock_SingleTermBackwardCompatible(t *testing.T) {
	reg := NewRegistry()
	// A pre-summed people delta: births − deaths + immigration − emigration = 3.
	const tracked = int64(3)
	if err := RegisterStock(reg, "people", StockName("people"), func() int64 { return tracked }); err != nil {
		t.Fatalf("RegisterStock: %v", err)
	}

	balanced := NewSnapshot(7)
	balanced.Readings[StockName("people")] = StockReading{Registered: true, Opening: 100, Closing: 103}
	got := RunSuite(reg, balanced)
	if got.Outcomes[0].Violation.Detected {
		t.Fatalf("single-term stock reported a Violation on a balanced tick: %+v", got.Outcomes[0].Violation)
	}

	broken := NewSnapshot(7)
	broken.Readings[StockName("people")] = StockReading{Registered: true, Opening: 100, Closing: 102}
	got = RunSuite(reg, broken)
	if !got.Outcomes[0].Violation.Detected {
		t.Fatal("single-term stock did not detect an untracked change — backward-compatible shape must still check, not just register")
	}
}

// TestRegisterStock_DegeneratesToOneTerm proves the documented equivalence:
// RegisterStock(name, stock, snapshot) is RegisterStockWithTerms with the one
// implicit inflow term "tracked_delta". Both must produce byte-identical suite
// results over the same snapshot.
func TestRegisterStock_DegeneratesToOneTerm(t *testing.T) {
	delta := func() int64 { return refuseTracked }

	single := NewRegistry()
	if err := RegisterStock(single, "refuse", StockName("refuse"), delta); err != nil {
		t.Fatal(err)
	}
	multi := NewRegistry()
	if err := RegisterStockWithTerms(multi, "refuse", StockName("refuse"),
		map[string]TermFunc{"tracked_delta": delta}, nil); err != nil {
		t.Fatal(err)
	}

	for _, closing := range []int64{refuseOpening + refuseTracked, refuseOpening + refuseTracked - 50} {
		state := refuseSnapshot(closing)
		a := RunSuite(single, state)
		b := RunSuite(multi, state)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("RegisterStock and RegisterStockWithTerms differ at closing=%d:\nsingle: %+v\nmulti:  %+v", closing, a, b)
		}
	}
}

// TestRegisterStockWithTerms_UnregisteredStockSkipped proves AC-12's skip
// semantics carry over to the multi-term invariant: a stock whose reading is
// absent from the Snapshot is Ran:false, never a false-flagged zero.
func TestRegisterStockWithTerms_UnregisteredStockSkipped(t *testing.T) {
	reg := registerRefuse(t)

	got := RunSuite(reg, NewSnapshot(1)) // no readings populated

	if len(got.Outcomes) != 1 {
		t.Fatalf("len(Outcomes) = %d, want 1", len(got.Outcomes))
	}
	if got.Outcomes[0].Ran {
		t.Fatal("Ran = true for an unregistered multi-term stock, want false")
	}
	if got.Outcomes[0].Violation.Detected {
		t.Fatal("reported a Violation for an unregistered stock — false-flagged an assumed zero")
	}
	if got.AllRan {
		t.Fatal("AllRan = true with the only invariant skipped, want false")
	}
}

// TestRegisterStockWithTerms_DefensiveCopy proves weakness pattern #1's
// control: mutating the caller's ins/outs maps after registration must not
// change what the invariant checks — the registered invariant holds its own
// copy of the terms.
func TestRegisterStockWithTerms_DefensiveCopy(t *testing.T) {
	reg := NewRegistry()
	ins, outs := refuseTermFuncs()
	if err := RegisterStockWithTerms(reg, "refuse", StockName("refuse"), ins, outs); err != nil {
		t.Fatal(err)
	}

	// Late mutation: a caller that still holds the maps changes a term after
	// registration. The registered invariant must keep checking the ORIGINAL
	// values, not this new one.
	ins["generated"] = func() int64 { return 999999 }
	delete(outs, "collected")

	got := RunSuite(reg, refuseSnapshot(refuseOpening+refuseTracked))
	if got.Outcomes[0].Violation.Detected {
		t.Fatal("late mutation of the caller's term maps leaked into the registered invariant — a term added/removed after registration must not silently change what is checked")
	}
}

// TestRegisterStockWithTerms_DeterministicConcurrent proves AC-13 under
// concurrency: RunSuite over a multi-term registry is a pure function of the
// Snapshot — N concurrent runs (and a repeat run) all return byte-identical
// results, and the term funcs being pure means no data race (run with -race).
func TestRegisterStockWithTerms_DeterministicConcurrent(t *testing.T) {
	reg := registerRefuse(t)
	state := refuseSnapshot(refuseOpening + refuseTracked - 50) // deliberately broken

	want := RunSuite(reg, state)

	const workers = 8
	results := make([]SuiteResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = RunSuite(reg, state)
		}()
	}
	wg.Wait()

	for i, r := range results {
		if !reflect.DeepEqual(r, want) {
			t.Fatalf("concurrent run %d diverged from the expected result:\nwant: %+v\ngot:  %+v", i, want, r)
		}
	}
	// And a serial repeat must match too (RunSuite does not mutate reg or the
	// invariants — AC-13).
	if again := RunSuite(reg, state); !reflect.DeepEqual(again, want) {
		t.Fatalf("repeat RunSuite diverged:\nwant: %+v\ngot:  %+v", want, again)
	}
}

// TestRegisterStockWithTerms_OverflowSaturatesNotWraps is SEC-055's regression:
// two math.MaxInt64 inflow terms overflow any int64 running sum. Wrapping
// addition folds 2×MaxInt64 into -2, so a Closing−Opening of -2 reports
// "balanced" on a wildly-false identity; saturating accumulation must instead
// surface a Violation, never a silent pass.
func TestRegisterStockWithTerms_OverflowSaturatesNotWraps(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterStockWithTerms(reg, "overflow", StockName("overflow"),
		map[string]TermFunc{
			"in_a": func() int64 { return math.MaxInt64 },
			"in_b": func() int64 { return math.MaxInt64 },
		}, nil); err != nil {
		t.Fatalf("RegisterStockWithTerms: %v", err)
	}

	// Wrapped sum = MaxInt64 + MaxInt64 = -2; this is the Closing−Opening the
	// old wrapping code would have treated as balanced.
	s := NewSnapshot(1)
	s.Readings[StockName("overflow")] = StockReading{Registered: true, Opening: 0, Closing: -2}

	got := RunSuite(reg, s)
	if len(got.Outcomes) != 1 || !got.Outcomes[0].Ran {
		t.Fatalf("Outcomes = %+v, want exactly one Ran outcome", got.Outcomes)
	}
	if !got.Outcomes[0].Violation.Detected {
		t.Fatal("two MaxInt64 inflows overflowed and were reported balanced — the invariant silently false-negatived (SEC-055)")
	}
	if !got.AnyViolation {
		t.Fatal("AnyViolation = false, want true — an overflowed Σ(ins) must surface as a Violation, not a silent pass")
	}
}

// TestRegisterStockWithTerms_LargeButValidTermNotRejected guards SEC-055's fix
// against the wrong fix: a single large-but-valid term (one MaxInt64 inflow,
// whose sum does NOT overflow) must still balance, never be rejected by a
// magnitude bound on "too-big" terms.
func TestRegisterStockWithTerms_LargeButValidTermNotRejected(t *testing.T) {
	reg := NewRegistry()
	if err := RegisterStockWithTerms(reg, "big", StockName("big"),
		map[string]TermFunc{"in": func() int64 { return math.MaxInt64 }}, nil); err != nil {
		t.Fatalf("RegisterStockWithTerms: %v", err)
	}

	s := NewSnapshot(1)
	s.Readings[StockName("big")] = StockReading{Registered: true, Opening: 0, Closing: math.MaxInt64}

	got := RunSuite(reg, s)
	if len(got.Outcomes) != 1 || !got.Outcomes[0].Ran {
		t.Fatalf("Outcomes = %+v, want exactly one Ran outcome", got.Outcomes)
	}
	if got.Outcomes[0].Violation.Detected {
		t.Fatalf("a single valid MaxInt64 inflow was rejected/flagged as a violation: %+v", got.Outcomes[0].Violation)
	}
}
