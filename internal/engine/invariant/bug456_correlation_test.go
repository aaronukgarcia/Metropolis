package invariant

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestBUG456_CopiedRegistry_ValidCorrelationID is the copy-safety
// regression for BUG-456's lazy correlation-ID mint in
// Registry.checkNotCopied. Invariants() now passes "" (hot per-tick
// path), but a real struct copy must still be rejected with an
// ErrRegistryCopied error carrying a NON-EMPTY, VALID correlation ID.
//
// Invariants() itself swallows the error (returns a nil slice), so this
// asserts against checkNotCopied directly — the exact function BUG-456
// changed — proving the lazy `if correlationID == "" { mint }` branch
// fires and yields a valid UUIDv4 rather than the empty string (which
// errs.New would downgrade to the MISSING-CORRELATION-ID placeholder).
func TestBUG456_CopiedRegistry_ValidCorrelationID(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(fakeInvariant{name: "a"}); err != nil {
		t.Fatal(err)
	}

	var copyOfReg Registry
	*(*[unsafe.Sizeof(Registry{})]byte)(unsafe.Pointer(&copyOfReg)) = *(*[unsafe.Sizeof(Registry{})]byte)(unsafe.Pointer(reg))

	// Call the changed function with "" exactly as the hot callers do.
	err := copyOfReg.checkNotCopied("", map[string]any{"method": "Invariants"})
	if err == nil {
		t.Fatal("copied Registry.checkNotCopied(\"\") returned nil, want ErrRegistryCopied")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("error is not *errs.E: %T (%v)", err, err)
	}
	if e.Code != ErrRegistryCopied {
		t.Fatalf("error code = %q, want %q", e.Code, ErrRegistryCopied)
	}
	if e.CorrelationID == "" {
		t.Fatal("copied Registry error has EMPTY correlation ID — lazy mint branch did not fire (BUG-456 regression)")
	}
	if e.CorrelationID == "MISSING-CORRELATION-ID" {
		t.Fatal("copied Registry error carries MISSING-CORRELATION-ID placeholder — lazy mint branch did not fire (BUG-456 regression)")
	}
	if !errs.IsValidCorrelationID(e.CorrelationID) {
		t.Fatalf("copied Registry error correlation ID %q is not a valid UUIDv4 (BUG-456 regression)", e.CorrelationID)
	}

	// A cold caller that still passes an eager ID must have it honoured
	// verbatim (the lazy branch must NOT overwrite a supplied ID).
	supplied := errs.NewCorrelationID()
	err2 := copyOfReg.checkNotCopied(supplied, map[string]any{"method": "Register"})
	var e2 *errs.E
	if !errors.As(err2, &e2) {
		t.Fatalf("eager-path error is not *errs.E: %T", err2)
	}
	if e2.CorrelationID != supplied {
		t.Fatalf("eager-supplied correlation ID was overwritten: got %q want %q", e2.CorrelationID, supplied)
	}
}

// TestBUG456_MultiViolation_SharesOneCorrelationID pins the "one ID per
// tick's batch" contract in handleViolations' doc comment. The lazy
// `if correlationID == "" { mint }` must mint ONCE on the first detected
// violation and REUSE it for every later violation in the same call —
// not mint per violation. It also asserts the minted ID is valid and
// non-placeholder, and that a batch of only-clean outcomes mints nothing.
func TestBUG456_MultiViolation_SharesOneCorrelationID(t *testing.T) {
	var logged []*errs.E
	h := &Hook{
		devMode: false,
		logSink: func(er *errs.E) { logged = append(logged, er) },
	}

	result := SuiteResult{
		Tick:         42,
		AnyViolation: true,
		AllRan:       true,
		Outcomes: []InvariantOutcome{
			{Name: "people", Ran: true, Violation: Violation{Detected: true, InvariantName: "people", Tick: 42, Expected: 0, Actual: 1}},
			{Name: "money", Ran: true, Violation: Violation{Detected: false}},
			{Name: "jobs", Ran: true, Violation: Violation{Detected: true, InvariantName: "jobs", Tick: 42, Expected: 5, Actual: 9}},
			{Name: "waste", Ran: true, Violation: Violation{Detected: true, InvariantName: "waste", Tick: 42, Expected: 2, Actual: 0}},
		},
	}

	h.handleViolations(result)

	if len(logged) != 3 {
		t.Fatalf("logged %d violations, want 3 (one per Detected outcome)", len(logged))
	}
	first := logged[0].CorrelationID
	if first == "" || first == "MISSING-CORRELATION-ID" {
		t.Fatalf("first violation correlation ID invalid: %q", first)
	}
	if !errs.IsValidCorrelationID(first) {
		t.Fatalf("correlation ID %q is not a valid UUIDv4", first)
	}
	for i, e := range logged {
		if e.CorrelationID != first {
			t.Fatalf("violation %d correlation ID = %q, want shared %q — 'one ID per batch' contract broken (minted per-violation)", i, e.CorrelationID, first)
		}
	}
}

// TestBUG456_NoViolations_MintsNothing is the defensive path: a batch
// with no Detected violation must mint no ID and log nothing (the lazy
// `if ""` never fires).
func TestBUG456_NoViolations_MintsNothing(t *testing.T) {
	var logged []*errs.E
	h := &Hook{
		devMode: false,
		logSink: func(er *errs.E) { logged = append(logged, er) },
	}
	result := SuiteResult{
		Tick:   7,
		AllRan: true,
		Outcomes: []InvariantOutcome{
			{Name: "people", Ran: true, Violation: Violation{Detected: false}},
			{Name: "money", Ran: true, Violation: Violation{Detected: false}},
		},
	}
	h.handleViolations(result)
	if len(logged) != 0 {
		t.Fatalf("clean batch logged %d errors, want 0", len(logged))
	}
}
