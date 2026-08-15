package protocol

import (
	"strings"
	"testing"
)

// TestEventCrisisIndependentOfSeverity is FEAT-042 AC-24's two-sided check,
// the carrier half of ui.alerts' AC-6 (FEAT-013): Crisis is settable to true
// on a non-critical Event and settable to false on a critical Event, with
// neither assignment rejected or silently overridden. A tier/severity-derived
// shortcut (e.g. a Crisis() method computing Severity == SeverityCritical)
// cannot satisfy both sides — it would force the "critical but not crisis"
// case (a P0 "loan payment due") to read true, which is exactly the false
// auto-pause AC-6 exists to prevent.
func TestEventCrisisIndependentOfSeverity(t *testing.T) {
	crisisButInfo := Event{Kind: "water.stockout", Severity: SeverityInfo, Crisis: true}
	if !crisisButInfo.Crisis {
		t.Fatalf("Event{Severity: SeverityInfo, Crisis: true} read back Crisis=%v, want true", crisisButInfo.Crisis)
	}

	criticalButNotCrisis := Event{Kind: "finance.loan.due", Severity: SeverityCritical, Crisis: false}
	if criticalButNotCrisis.Crisis {
		t.Fatalf("Event{Severity: SeverityCritical, Crisis: false} read back Crisis=%v, want false", criticalButNotCrisis.Crisis)
	}
}

// TestEventCrisisAbsentDefaultsFalse is FEAT-042 AC-25: decoding an Event
// whose JSON carries no "crisis" key at all yields Crisis==false with no
// decode error. This is the correct reading for both a genuinely non-crisis
// event and a record produced before the field existed (pre-amendment-shaped
// data), and it is what makes the additive field safe to add under the frozen
// ProtocolVersion.
func TestEventCrisisAbsentDefaultsFalse(t *testing.T) {
	data := []byte(`{"kind":"milestone.reached","tick":1,"severity":"info"}`)
	got, err := DecodeEvent(data)
	if err != nil {
		t.Fatalf("DecodeEvent(no crisis key) = %v, want nil", err)
	}
	if got.Crisis {
		t.Fatalf("DecodeEvent(no crisis key).Crisis = true, want false")
	}
}

// TestEventCrisisOmitemptyAndRoundTrip checks the wire shape: a false Crisis
// is omitted from the encoded JSON (so an unchanged event's bytes stay
// byte-identical to pre-amendment output — FEAT-042 AC-26), while a true
// Crisis is present and round-trips back to true.
func TestEventCrisisOmitemptyAndRoundTrip(t *testing.T) {
	plain, err := EncodeEvent(Event{Kind: "x", Tick: 1, Severity: SeverityInfo})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "crisis") {
		t.Fatalf("Crisis=false encoded with a crisis key present: %s (omitempty must drop it)", plain)
	}

	tagged := Event{Kind: "water.stockout", Tick: 2, Severity: SeverityWarning, Crisis: true}
	got, err := DecodeEvent(mustEncodeEvent(t, tagged))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Crisis {
		t.Fatalf("Crisis=true round-tripped to false")
	}
}

func mustEncodeEvent(t *testing.T, e Event) []byte {
	t.Helper()
	data, err := EncodeEvent(e)
	if err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}
	return data
}
