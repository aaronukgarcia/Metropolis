package protocol

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- AC-20: EntityID validation ---------------------------------------

func TestValidateEntityID_ValidCase(t *testing.T) {
	if err := ValidateEntityID(EntityID("line-42")); err != nil {
		t.Fatalf("ValidateEntityID(%q) = %v, want nil", "line-42", err)
	}
}

func TestValidateEntityID_InvalidCase(t *testing.T) {
	err := ValidateEntityID(EntityID(""))
	if err == nil {
		t.Fatal("ValidateEntityID(\"\") = nil, want an error for the empty EntityID")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("ValidateEntityID error %v is not an *errs.E (registry-sourced, GR#7)", err)
	}
	if e.Code != ErrInvalidEntityID {
		t.Fatalf("ValidateEntityID error code = %q, want %q", e.Code, ErrInvalidEntityID)
	}
}

func TestValidateEntityID_RejectsWhitespace(t *testing.T) {
	if err := ValidateEntityID(EntityID("bad entity id")); err == nil {
		t.Fatal("ValidateEntityID accepted a value containing whitespace")
	}
}

// --- AC-21: TargetRef round-trips through JSON, field-for-field -------

func TestTargetRefJSONRoundTrip(t *testing.T) {
	want := TargetRef{ViewName: "f2.ledger", EntityID: EntityID("line-42")}

	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal(TargetRef): %v", err)
	}

	var got TargetRef
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal(TargetRef): %v", err)
	}

	if got != want {
		t.Fatalf("TargetRef round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestTargetRefZeroEntityIDOmittedFromWire(t *testing.T) {
	// A whole-view TargetRef (empty EntityID) omits the key entirely,
	// matching DrillTarget's own "empty means whole view" contract
	// (FEAT-042 AC-21/ui.dash's drill.go).
	ref := TargetRef{ViewName: "f1.viewport"}
	b, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("Marshal(TargetRef): %v", err)
	}
	want := `{"viewName":"f1.viewport"}`
	if string(b) != want {
		t.Fatalf("Marshal(TargetRef with zero EntityID) = %s, want %s", b, want)
	}
}

// --- AC-26: byte-determinism for the unchanged case --------------------
//
// Golden JSON fixtures under testdata/feat042/ were captured from the
// live (post-amendment) code with every FEAT-042 field at its zero
// value, which is byte-identical to what the pre-amendment code would
// have produced for the same values (Command and Delta gained no new
// fields at all; Event's new Crisis field is omitempty and false is its
// zero value, so it is absent from the wire either way). A future change
// that accidentally drops an omitempty tag, reorders a field, or adds a
// non-zero default would break this test.

func readGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "feat042", name))
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", name, err)
	}
	return string(b)
}

func TestGolden_CommandByteIdentical(t *testing.T) {
	cmd := Command{
		ProtocolVersion: ProtocolVersion,
		CorrelationID:   "fixed-correlation-id",
		IssuedAtTick:    42,
		Kind:            KindAdvanceTicks,
		Payload:         AdvanceTicksPayload{N: 30},
	}
	b, err := EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("EncodeCommand: %v", err)
	}
	want := readGolden(t, "command.golden.json")
	if string(b)+"\n" != want {
		t.Fatalf("Command marshal diverged from golden fixture:\n got:  %s\n want: %s", b, want)
	}
}

func TestGolden_DeltaByteIdentical(t *testing.T) {
	delta := Delta{
		SubscriptionID: "sub-1",
		Tick:           42,
		Seq:            1,
		Patch:          json.RawMessage(`{"x":1}`),
	}
	b, err := EncodeDelta(delta)
	if err != nil {
		t.Fatalf("EncodeDelta: %v", err)
	}
	want := readGolden(t, "delta.golden.json")
	if string(b)+"\n" != want {
		t.Fatalf("Delta marshal diverged from golden fixture:\n got:  %s\n want: %s", b, want)
	}
}

func TestGolden_EventByteIdentical(t *testing.T) {
	// Crisis deliberately left at its zero value (false) -- this is the
	// case AC-26 exists to prove stays byte-identical to pre-amendment
	// output: the omitempty tag must keep "crisis" off the wire here.
	ev := Event{
		Kind:     "milestone.reached",
		Tick:     42,
		Severity: SeverityInfo,
	}
	b, err := EncodeEvent(ev)
	if err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}
	want := readGolden(t, "event.golden.json")
	if string(b)+"\n" != want {
		t.Fatalf("Event marshal diverged from golden fixture:\n got:  %s\n want: %s", b, want)
	}
	if bytesContain(b, "crisis") {
		t.Fatalf("zero-value Crisis leaked onto the wire despite omitempty: %s", b)
	}
}

func bytesContain(b []byte, sub string) bool {
	return len(sub) > 0 && (func() bool {
		s := string(b)
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// --- AC-28: ProtocolVersion does not move ------------------------------

func TestProtocolVersionUnchangedByAmendment(t *testing.T) {
	if ProtocolVersion != "1.0" {
		t.Fatalf("ProtocolVersion = %q, want %q -- FEAT-042 is additive and must not bump this (AC-28); a version-string bump would reject every already-recorded fixture's Commands on the next replay, since Command.Validate does an EXACT string-equality check, not a compatible-range check", ProtocolVersion, "1.0")
	}

	cmd := Command{
		ProtocolVersion: "1.0",
		CorrelationID:   "corr-1",
		IssuedAtTick:    1,
		Kind:            KindPause,
		Payload:         PausePayload{},
	}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("Command{ProtocolVersion: \"1.0\"}.Validate() = %v, want nil against the current running constant", err)
	}
}

// --- AC-29: a v1-recorded fixture replays cleanly under v2 -------------

func TestV1RecordedEventReplaysCleanUnderPostAmendmentCode(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "feat042", "v1_event.json"))
	if err != nil {
		t.Fatalf("read v1_event.json fixture: %v", err)
	}

	ev, err := DecodeEvent(raw)
	if err != nil {
		t.Fatalf("DecodeEvent(pre-amendment-shaped JSON) = %v, want a clean decode (AC-29)", err)
	}

	// The fixture predates Crisis entirely -- its absence must decode to
	// false, not an error and not some other non-zero default (AC-25),
	// and every pre-existing field must still be exactly as recorded.
	if ev.Crisis != false {
		t.Fatalf("Crisis = %v after decoding a pre-amendment fixture with no crisis key, want false", ev.Crisis)
	}
	if ev.Kind != "junction.gridlocked" || ev.Tick != 128 || ev.Severity != SeverityWarning {
		t.Fatalf("decoded Event %+v does not match the v1 fixture's recorded fields", ev)
	}
	if len(ev.EntityRefs) != 1 || ev.EntityRefs[0] != "junction:14" {
		t.Fatalf("decoded Event.EntityRefs = %v, want [\"junction:14\"]", ev.EntityRefs)
	}
}
