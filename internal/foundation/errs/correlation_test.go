package errs

import (
	"context"
	"testing"
	"time"
)

func TestNewCorrelationID_ValidFormat(t *testing.T) {
	id := NewCorrelationID()
	if !IsValidCorrelationID(id) {
		t.Errorf("NewCorrelationID() = %q, not a valid UUIDv4", id)
	}
}

func TestNewCorrelationID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := NewCorrelationID()
		if seen[id] {
			t.Fatalf("duplicate correlation ID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestIsValidCorrelationID_RejectsGarbage(t *testing.T) {
	cases := []string{"", "not-a-uuid", "12345678-1234-1234-1234-123456789012", "MISSING-CORRELATION-ID"}
	for _, c := range cases {
		if IsValidCorrelationID(c) {
			t.Errorf("IsValidCorrelationID(%q) = true, want false", c)
		}
	}
}

func TestContextCorrelationID_RoundTrip(t *testing.T) {
	id := NewCorrelationID()
	ctx := ContextWithCorrelationID(context.Background(), id)

	got, ok := CorrelationIDFromContext(ctx)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != id {
		t.Errorf("got %q, want %q", got, id)
	}
}

func TestContextCorrelationID_AbsentByDefault(t *testing.T) {
	_, ok := CorrelationIDFromContext(context.Background())
	if ok {
		t.Error("expected ok=false for a context with no correlation ID set")
	}
}

// TestDegradedCorrelationID_UsesInjectedClock proves the crypto/rand
// -failure fallback path derives its entropy from the package's
// injectable clock (errs.go's now()/SetClock) rather than reading the
// wall clock directly (GR#21/M0-ENG §1.1: no direct wall-clock reads,
// even on catastrophic paths). Two distinct fixed clocks must produce
// two distinct, still-well-formed IDs; the same fixed clock must
// reproduce the same ID, which would not hold if time.Now() were still
// being read underneath.
func TestDegradedCorrelationID_UsesInjectedClock(t *testing.T) {
	t.Cleanup(func() { SetClock(time.Now) })

	fixedA := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	SetClock(func() time.Time { return fixedA })
	idA1 := degradedCorrelationID()
	idA2 := degradedCorrelationID()

	if idA1 != idA2 {
		t.Fatalf("expected degradedCorrelationID to be deterministic under a fixed injected clock, got %q then %q", idA1, idA2)
	}
	if !IsValidCorrelationID(idA1) {
		t.Errorf("degradedCorrelationID() = %q, not a valid UUIDv4", idA1)
	}

	fixedB := time.Date(2030, 6, 7, 8, 9, 10, 11, time.UTC)
	SetClock(func() time.Time { return fixedB })
	idB := degradedCorrelationID()

	if idB == idA1 {
		t.Errorf("expected a different injected clock to change the degraded ID, both were %q", idA1)
	}
	if !IsValidCorrelationID(idB) {
		t.Errorf("degradedCorrelationID() = %q, not a valid UUIDv4", idB)
	}
}
