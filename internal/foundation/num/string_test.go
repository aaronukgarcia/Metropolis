package num

import (
	"strings"
	"testing"
)

// TestSanitizeEventID_RejectsEmptyAndOverLength (SEC-203): the BoundedString
// boundary hard-fails — with a registry-sourced code, never a bare
// fmt.Errorf — an empty id and an id over MaxEventIDLength, and accepts an id
// AT the ceiling (the bound is "over", never "at or over").
func TestSanitizeEventID_RejectsEmptyAndOverLength(t *testing.T) {
	v, err := SanitizeEventID("")
	if v != "" {
		t.Errorf(`SanitizeEventID("") = %q, want ""`, v)
	}
	wantCode(t, err, codeStringEmpty)

	tooLong := strings.Repeat("x", MaxEventIDLength+1)
	v, err = SanitizeEventID(tooLong)
	if v != "" {
		t.Errorf("SanitizeEventID(too long) = %q, want empty zero value", v)
	}
	wantCode(t, err, codeStringTooLong)

	atCeiling := strings.Repeat("x", MaxEventIDLength)
	if v, err := SanitizeEventID(atCeiling); err != nil || v != atCeiling {
		t.Errorf("SanitizeEventID(at ceiling) = (%q, %v), want (%q, nil)", v, err, atCeiling)
	}

	if v, err := SanitizeEventID("crisis-1"); err != nil || v != "crisis-1" {
		t.Errorf(`SanitizeEventID("crisis-1") = (%q, %v), want ("crisis-1", nil)`, v, err)
	}
}

// TestSanitizeEventID_Deterministic (GR#21): identical inputs yield identical
// values and identical error codes on every call. The correlation ID is an
// audit field and is intentionally not compared.
func TestSanitizeEventID_Deterministic(t *testing.T) {
	v1, e1 := SanitizeEventID("abc")
	v2, e2 := SanitizeEventID("abc")
	if v1 != v2 {
		t.Errorf("SanitizeEventID non-deterministic: %q vs %q", v1, v2)
	}
	assertSameCode(t, e1, e2)

	_, e1 = SanitizeEventID(strings.Repeat("y", MaxEventIDLength+1))
	_, e2 = SanitizeEventID(strings.Repeat("y", MaxEventIDLength+1))
	assertSameCode(t, e1, e2)

	_, e1 = SanitizeEventID("")
	_, e2 = SanitizeEventID("")
	assertSameCode(t, e1, e2)
}
