package synth

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %s, got nil", code)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("want *errs.E, got %T (%v)", err, err)
	}
	if e.Code != code {
		t.Fatalf("want code %s, got %s (%v)", code, e.Code, err)
	}
}

func validParams() Params {
	return Params{CitizenCount: 100, Seed: 7, Sprawl: 0.5, NetworkShape: NetworkGrid}
}

// TestValidateParams_CitizenCountOutOfRangeBoundary is AC-7b's per-
// parameter boundary coverage for CitizenCount: a value one below the
// minimum, one above the maximum, and both boundary values themselves
// accepted.
func TestValidateParams_CitizenCountOutOfRangeBoundary(t *testing.T) {
	p := validParams()

	p.CitizenCount = MinSyntheticCitizens - 1
	wantCode(t, ValidateParams("t", p), codeCitizenCountOutOfRange)

	p.CitizenCount = MaxSyntheticCitizens + 1
	wantCode(t, ValidateParams("t", p), codeCitizenCountOutOfRange)

	p.CitizenCount = MinSyntheticCitizens
	if err := ValidateParams("t", p); err != nil {
		t.Fatalf("min boundary should be legal, got %v", err)
	}

	// The maximum boundary is deliberately NOT exercised by actually
	// generating MaxSyntheticCitizens (30M) records here — this is a
	// domain-boundary test on ValidateParams alone (no generation work
	// performed), so asserting acceptance at the literal ceiling costs
	// nothing more than one int64 comparison.
	p.CitizenCount = MaxSyntheticCitizens
	if err := ValidateParams("t", p); err != nil {
		t.Fatalf("max boundary should be legal, got %v", err)
	}
}

// TestValidateParams_CitizenCountCeilingRejectsTooLarge is AC-1b's own
// named check: a request exceeding MaxSyntheticCitizens is rejected
// BEFORE any generation work starts, never clamped down to a legal
// value. Generate itself is exercised (not just ValidateParams) so this
// also proves the ceiling is enforced on the real entry point, not only
// on a helper a caller could bypass.
func TestValidateParams_CitizenCountCeilingRejectsTooLarge(t *testing.T) {
	p := validParams()
	p.CitizenCount = MaxSyntheticCitizens + 1

	var buf discardWriter
	_, err := Generate("t", p, &buf)
	wantCode(t, err, codeCitizenCountOutOfRange)

	if buf.wrote {
		t.Fatal("Generate wrote to its output writer before rejecting an over-ceiling request — the ceiling must be enforced before any generation work begins (AC-1b(b))")
	}
}

// discardWriter proves Generate never touches its io.Writer before
// validation passes (AC-1b(b): "enforced before any allocation begins").
type discardWriter struct{ wrote bool }

func (d *discardWriter) Write(p []byte) (int, error) {
	d.wrote = true
	return len(p), nil
}

// TestValidateParams_SprawlBoundary is AC-7b's per-parameter boundary
// coverage for Sprawl.
func TestValidateParams_SprawlBoundary(t *testing.T) {
	p := validParams()

	p.Sprawl = MinSprawl - 0.0001
	wantCode(t, ValidateParams("t", p), codeSprawlOutOfRange)

	p.Sprawl = MaxSprawl + 0.0001
	wantCode(t, ValidateParams("t", p), codeSprawlOutOfRange)

	p.Sprawl = MinSprawl
	if err := ValidateParams("t", p); err != nil {
		t.Fatalf("min sprawl boundary should be legal, got %v", err)
	}

	p.Sprawl = MaxSprawl
	if err := ValidateParams("t", p); err != nil {
		t.Fatalf("max sprawl boundary should be legal, got %v", err)
	}
}

// TestValidateParams_NetworkShapeDomain is AC-7b's per-parameter
// boundary/domain coverage for NetworkShape: every legal enum value is
// accepted, and a value outside the closed set is rejected — never
// treated as an arbitrary free string.
func TestValidateParams_NetworkShapeDomain(t *testing.T) {
	for _, shape := range []NetworkShape{NetworkGrid, NetworkRadial, NetworkOrganic} {
		p := validParams()
		p.NetworkShape = shape
		if err := ValidateParams("t", p); err != nil {
			t.Fatalf("shape %q should be legal, got %v", shape, err)
		}
	}

	p := validParams()
	p.NetworkShape = NetworkShape("hexagonal")
	wantCode(t, ValidateParams("t", p), codeInvalidNetworkShape)
}

// TestValidateParams_InvalidCombinationRejectsFirstViolation is AC-7's
// general invalid-input coverage: a nonsensical combination (negative
// population AND an out-of-domain sprawl) is rejected with a registry
// code, never accepted or silently repaired.
func TestValidateParams_InvalidCombinationRejectsFirstViolation(t *testing.T) {
	p := Params{CitizenCount: -5, Seed: 0, Sprawl: 99, NetworkShape: "bogus"}
	err := ValidateParams("t", p)
	wantCode(t, err, codeCitizenCountOutOfRange)
}
