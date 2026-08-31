package protocol

import (
	"errors"
	"testing"
)

// version_test.go — FEAT-1972079936 Phase 0 increment 1, AC-1: semver
// parse/compare, and the Command.Validate mutation proving minor is
// additive-tolerant while major is not, on the SAME test harness (the
// doc's explicit false-pass guard: two separately-written tests that
// could each trivially pass prove nothing about decoupling).

func TestParseWireVersion_Valid(t *testing.T) {
	cases := []struct {
		in   string
		want WireVersion
	}{
		{"1.0", WireVersion{Major: 1, Minor: 0}},
		{"2.3", WireVersion{Major: 2, Minor: 3}},
		{"0.0", WireVersion{Major: 0, Minor: 0}},
		{"10.42", WireVersion{Major: 10, Minor: 42}},
	}
	for _, c := range cases {
		got, err := ParseWireVersion(c.in)
		if err != nil {
			t.Fatalf("ParseWireVersion(%q) unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseWireVersion(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseWireVersion_Malformed(t *testing.T) {
	cases := []string{
		"", "1", "1.", ".1", "1.0.0", "a.b", "1.a", "a.1", "-1.0", "1.-1",
		"v1.0", "1.0-dirty", "1.0-153-gABCD",
	}
	for _, in := range cases {
		if _, err := ParseWireVersion(in); !errors.Is(err, ErrMalformedWireVersion) {
			t.Errorf("ParseWireVersion(%q): expected ErrMalformedWireVersion, got %v", in, err)
		}
	}
}

func TestWireVersion_StringRoundTrip(t *testing.T) {
	v := WireVersion{Major: 3, Minor: 7}
	got, err := ParseWireVersion(v.String())
	if err != nil {
		t.Fatalf("ParseWireVersion(%q) unexpected error: %v", v.String(), err)
	}
	if got != v {
		t.Fatalf("round trip: got %+v, want %+v", got, v)
	}
}

func TestWireVersion_Compare(t *testing.T) {
	cases := []struct {
		a, b WireVersion
		want int
	}{
		{WireVersion{1, 0}, WireVersion{1, 0}, 0},
		{WireVersion{1, 0}, WireVersion{1, 1}, -1},
		{WireVersion{1, 1}, WireVersion{1, 0}, 1},
		{WireVersion{1, 9}, WireVersion{2, 0}, -1},
		{WireVersion{2, 0}, WireVersion{1, 9}, 1},
	}
	for _, c := range cases {
		if got := c.a.Compare(c.b); got != c.want {
			t.Errorf("%+v.Compare(%+v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestWireVersion_Equal(t *testing.T) {
	if !(WireVersion{1, 2}).Equal(WireVersion{1, 2}) {
		t.Fatal("expected equal versions to compare Equal")
	}
	if (WireVersion{1, 2}).Equal(WireVersion{1, 3}) {
		t.Fatal("expected differing minors to NOT compare Equal")
	}
}

func TestWireVersion_IsZero(t *testing.T) {
	if !(WireVersion{}).IsZero() {
		t.Fatal("zero value must report IsZero")
	}
	if (WireVersion{Major: 1}).IsZero() {
		t.Fatal("non-zero major must not report IsZero")
	}
}

// TestCommandValidate_MinorTolerant_MajorNot is AC-1's exact mutation:
// bump CurrentWireVersion's MINOR only, then validate an envelope stamped
// with the OLD (lower) minor — must still pass (additive, no refusal).
// Then bump the MAJOR far enough to fall BELOW the supported window and
// re-validate the SAME old-stamped envelope — must now fail (below the
// window floor, refused). Both assertions run against the same cmd value
// on the same test, per the doc's false-pass guard.
//
// FEAT-1972079936 Phase 0 inc2 note: a major bump of exactly
// CurrentVersionWindowDepth or fewer is now IN-WINDOW and accepted (see
// TestCommandValidate_InWindowOlderMajor_Accepted below) — this is the
// deliberate change from inc1's "major must match exactly." This test is
// updated (not deleted, GR#12) to bump the major PAST the window instead,
// which is the one comparison that still discriminates "refused" from
// "accepted" under inc2's new rule.
func TestCommandValidate_MinorTolerant_MajorNot(t *testing.T) {
	originalVersion := CurrentWireVersion
	originalDepth := CurrentVersionWindowDepth
	defer func() {
		CurrentWireVersion = originalVersion
		CurrentVersionWindowDepth = originalDepth
	}()
	CurrentVersionWindowDepth = 2

	oldVersion := WireVersion{Major: originalVersion.Major, Minor: originalVersion.Minor}
	cmd := Command{
		ProtocolVersion: oldVersion.String(),
		CorrelationID:   "test-corr-1",
		IssuedAtTick:    0,
		Kind:            "AdvanceTicks",
		Payload:         testPayload{},
	}

	// Bump only the minor (simulating a newer, additive server release).
	CurrentWireVersion = WireVersion{Major: oldVersion.Major, Minor: oldVersion.Minor + 1}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("expected an older-minor Command to still validate against a newer-minor server, got: %v", err)
	}

	// Now bump the major PAST the window (depth 2 + 1 = 3 majors ahead) —
	// the SAME old-stamped command must now be refused as below-floor.
	CurrentWireVersion = WireVersion{Major: oldVersion.Major + CurrentVersionWindowDepth + 1, Minor: 0}
	if err := cmd.Validate(); !errors.Is(err, ErrUnsupportedProtocolVersion) {
		t.Fatalf("expected a below-window-floor major Command to be refused, got: %v", err)
	}
}

// TestCommandValidate_InWindowOlderMajor_Accepted is inc2's AC-3/AC-4
// "in-window-connects" proof at the envelope level: a Command stamped
// with a major 1 or 2 majors behind current (window depth 2, i.e. a
// 3-major window) validates successfully — this is the actual behaviour
// change from inc1, which refused ANY major mismatch.
func TestCommandValidate_InWindowOlderMajor_Accepted(t *testing.T) {
	originalVersion := CurrentWireVersion
	originalDepth := CurrentVersionWindowDepth
	defer func() {
		CurrentWireVersion = originalVersion
		CurrentVersionWindowDepth = originalDepth
	}()
	CurrentVersionWindowDepth = 2
	CurrentWireVersion = WireVersion{Major: 5, Minor: 0}

	for _, majorBack := range []int{0, 1, 2} {
		v := WireVersion{Major: 5 - majorBack, Minor: 0}
		cmd := Command{
			ProtocolVersion: v.String(),
			CorrelationID:   "test-corr-inwindow",
			IssuedAtTick:    0,
			Kind:            "AdvanceTicks",
			Payload:         testPayload{},
		}
		if err := cmd.Validate(); err != nil {
			t.Fatalf("major %d back (window depth 2): expected acceptance, got: %v", majorBack, err)
		}
	}
}

// TestCommandValidate_BelowWindowFloor_Refused is the other side of the
// same boundary: a major exactly ONE step past the window floor (window
// depth N -> floor major-N is accepted, major-N-1 is not) is refused —
// proving the boundary is pinned at N, not merely "some refusal happens
// somewhere" (the doc's AC-4 false-pass guard).
func TestCommandValidate_BelowWindowFloor_Refused(t *testing.T) {
	originalVersion := CurrentWireVersion
	originalDepth := CurrentVersionWindowDepth
	defer func() {
		CurrentWireVersion = originalVersion
		CurrentVersionWindowDepth = originalDepth
	}()
	CurrentVersionWindowDepth = 2
	CurrentWireVersion = WireVersion{Major: 5, Minor: 0}

	floor := WireVersion{Major: 3, Minor: 0}      // 5 - 2 = floor, must be accepted
	belowFloor := WireVersion{Major: 2, Minor: 0} // one step further, must be refused

	floorCmd := Command{ProtocolVersion: floor.String(), CorrelationID: "c1", Kind: "AdvanceTicks", Payload: testPayload{}}
	if err := floorCmd.Validate(); err != nil {
		t.Fatalf("expected the window FLOOR version itself to be accepted (boundary-inclusive), got: %v", err)
	}

	belowCmd := Command{ProtocolVersion: belowFloor.String(), CorrelationID: "c2", Kind: "AdvanceTicks", Payload: testPayload{}}
	if err := belowCmd.Validate(); !errors.Is(err, ErrUnsupportedProtocolVersion) {
		t.Fatalf("expected one-step-below-floor to be refused, got: %v", err)
	}
}

// TestCommandValidate_NewerMajorThanServer_StillRefused proves inc2 did
// not accidentally widen acceptance in the OTHER direction too: a major
// NEWER than current is refused exactly like before, regardless of window
// depth (a window only reaches backward, never forward).
func TestCommandValidate_NewerMajorThanServer_StillRefused(t *testing.T) {
	original := CurrentWireVersion
	defer func() { CurrentWireVersion = original }()
	CurrentWireVersion = WireVersion{Major: 1, Minor: 0}

	cmd := Command{ProtocolVersion: "2.0", CorrelationID: "c3", Kind: "AdvanceTicks", Payload: testPayload{}}
	if err := cmd.Validate(); !errors.Is(err, ErrUnsupportedProtocolVersion) {
		t.Fatalf("expected a newer major than current to be refused, got: %v", err)
	}
}

// TestCommandValidate_NewerMinorThanServer_Refused proves the OTHER
// direction is not silently tolerated too: a client claiming a minor
// newer than what this build understands must be refused, not
// permissively accepted (only OLDER minors are additive-safe).
func TestCommandValidate_NewerMinorThanServer_Refused(t *testing.T) {
	original := CurrentWireVersion
	defer func() { CurrentWireVersion = original }()
	CurrentWireVersion = WireVersion{Major: 1, Minor: 0}

	cmd := Command{
		ProtocolVersion: "1.1",
		CorrelationID:   "test-corr-2",
		IssuedAtTick:    0,
		Kind:            "AdvanceTicks",
		Payload:         testPayload{},
	}
	if err := cmd.Validate(); !errors.Is(err, ErrUnsupportedProtocolVersion) {
		t.Fatalf("expected a newer-minor-than-server Command to be refused, got: %v", err)
	}
}

func TestCommandValidate_MalformedProtocolVersion_Refused(t *testing.T) {
	cmd := Command{
		ProtocolVersion: "not-a-version",
		CorrelationID:   "test-corr-3",
		IssuedAtTick:    0,
		Kind:            "AdvanceTicks",
		Payload:         testPayload{},
	}
	if err := cmd.Validate(); !errors.Is(err, ErrUnsupportedProtocolVersion) {
		t.Fatalf("expected malformed ProtocolVersion to be refused, got: %v", err)
	}
}

func TestWindowFloorMajor(t *testing.T) {
	cases := []struct {
		current WireVersion
		n       int
		want    int
	}{
		{WireVersion{5, 0}, 2, 3},
		{WireVersion{1, 0}, 2, 0}, // clamped, never negative
		{WireVersion{0, 0}, 2, 0},
		{WireVersion{5, 0}, 0, 5}, // depth 0 -> only current itself
	}
	for _, c := range cases {
		if got := WindowFloorMajor(c.current, c.n); got != c.want {
			t.Errorf("WindowFloorMajor(%+v, %d) = %d, want %d", c.current, c.n, got, c.want)
		}
	}
}

func TestInVersionWindow(t *testing.T) {
	current := WireVersion{Major: 5, Minor: 0}
	n := 2
	inWindow := []WireVersion{{5, 9}, {4, 0}, {3, 7}}
	for _, v := range inWindow {
		if !InVersionWindow(v, current, n) {
			t.Errorf("InVersionWindow(%+v, current=%+v, n=%d) = false, want true", v, current, n)
		}
	}
	outOfWindow := []WireVersion{{2, 0}, {6, 0}}
	for _, v := range outOfWindow {
		if InVersionWindow(v, current, n) {
			t.Errorf("InVersionWindow(%+v, current=%+v, n=%d) = true, want false", v, current, n)
		}
	}
}

func TestIntersectCapabilities(t *testing.T) {
	got := IntersectCapabilities([]string{"A", "B", "C"}, []string{"B", "C", "D"})
	want := []string{"B", "C"}
	if len(got) != len(want) {
		t.Fatalf("IntersectCapabilities = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("IntersectCapabilities = %v, want %v", got, want)
		}
	}
}

func TestIntersectCapabilities_EmptyClient_YieldsEmpty(t *testing.T) {
	got := IntersectCapabilities([]string{"A", "B", "C"}, nil)
	if len(got) != 0 {
		t.Fatalf("expected empty intersection for an empty client set, got %v", got)
	}
}

func TestIntersectCapabilities_Dedupes(t *testing.T) {
	got := IntersectCapabilities([]string{"A"}, []string{"A", "A", "A"})
	if len(got) != 1 || got[0] != "A" {
		t.Fatalf("expected a deduplicated single-element intersection, got %v", got)
	}
}

// testPayload is a minimal CommandPayload for this file's Validate tests
// — it only needs to satisfy the interface, never decoded from the wire.
type testPayload struct{}

func (testPayload) commandKind() Kind { return "AdvanceTicks" }
