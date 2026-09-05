package protocol

// FEAT-042 independent destructive round (opus-round-feat042).
// These tests are ADVERSARIAL: hostile EntityID inputs, wire
// forward/backward compatibility across the frozen v1 boundary, and
// determinism hammering. Kept as permanent regressions per GR#23.

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- ValidateEntityID hostile inputs (attack surface #2) --------------
//
// EntityID values become lookup-key material (ui/dash/nav.go composes
// map keys as ViewName+"\x00"+EntityID). A malformed id that the
// validator accepts could mis-resolve a downstream drill or collide two
// distinct targets. ValidateEntityID must reject cleanly with MET-P003
// and NEVER panic, for every shape below.
func TestAttack_ValidateEntityID_HostileInputs(t *testing.T) {
	reject := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"single-space", " "},
		{"leading-space", " line-42"},
		{"trailing-space", "line-42 "},
		{"inner-space", "line 42"},
		{"tab", "line\t42"},
		{"newline", "line\n42"},
		{"carriage-return", "line\r42"},
		{"nul-byte", "line\x0042"}, // the exact nav.go key separator
		{"nul-only", "\x00"},       // would blank a composite key
		{"bell-control", "line\x0742"},
		{"del-control", "line\x7f42"},
		{"leading-dot", ".hidden"}, // path-dotfile shape
		{"leading-colon", ":id"},   // typed:id with empty type
		{"leading-hyphen", "-flag"},
		{"leading-underscore", "_x"},
		{"slash", "a/b"},                  // path segment separator
		{"backslash", "a\\b"},             // windows path separator
		{"dotdot-slash", "../etc/passwd"}, // path traversal
		{"dotdot-back", "..\\secret"},
		{"pipe", "a|b"},
		{"semicolon", "a;rm -rf"},
		{"quote", "a\"b"},
		{"single-quote", "a'b"},
		{"brace", "a{b}"},
		{"percent", "a%20b"}, // url-encoded space
		{"at", "a@b"},
		{"hash", "a#b"},
		{"plus", "a+b"},
		{"equals", "a=b"},
		{"star", "a*b"},
		{"unicode-accent", "café"},
		{"unicode-cjk", "东京"},
		{"emoji", "🚦"},
		{"nbsp", "line\u00a042"},         // non-breaking space
		{"zero-width", "line\u200b42"},   // zero-width space (homograph risk)
		{"rtl-override", "line\u202e42"}, // bidi override (spoofing)
	}
	for _, tc := range reject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			// Must not panic (the harness fails the test if it does).
			if err := ValidateEntityID(EntityID(tc.id)); err == nil {
				t.Fatalf("ValidateEntityID(%q) = nil, want a rejection", tc.id)
			}
		})
	}

	accept := []string{
		"line-42",
		"junction:14",
		"citizen:482913",
		"a.b.c",
		"junction-14-approach-2",
		"A",
		"9",
		"Z9_.:-",                  // every allowed non-leading char, alnum lead
		strings.Repeat("a", 4096), // long but well-formed: must still pass
	}
	for _, id := range accept {
		t.Run("accept/"+id[:min(len(id), 16)], func(t *testing.T) {
			if err := ValidateEntityID(EntityID(id)); err != nil {
				t.Fatalf("ValidateEntityID(%q) = %v, want nil", id, err)
			}
		})
	}
}

// TestAttack_ValidateEntityID_HugeInputNoHangNoPanic feeds a 1 MiB id.
// The validator's pattern is a linear, backtracking-free character
// class, so this must return (a rejection, since the string contains no
// disallowed char it is actually ACCEPTED) promptly without a ReDoS
// blow-up or panic.
func TestAttack_ValidateEntityID_HugeInputNoHangNoPanic(t *testing.T) {
	huge := EntityID(strings.Repeat("a", 1<<20))
	// A megabyte of 'a' is technically well-formed; the point of the test
	// is that evaluation is bounded and panic-free, not the verdict.
	_ = ValidateEntityID(huge)

	hugeBad := EntityID(strings.Repeat("a", 1<<20) + " ") // one trailing space
	if err := ValidateEntityID(hugeBad); err == nil {
		t.Fatal("a 1 MiB id with a trailing space was accepted; want rejection")
	}
}

// TestAttack_EntityID_NoValidIDContainsKeySeparator proves the invariant
// ui/dash/nav.go's composite key relies on: a VALID EntityID can never
// contain the NUL byte used to join ViewName and EntityID, so two
// distinct (view, entity) pairs can never alias to the same map key.
func TestAttack_EntityID_NoValidIDContainsKeySeparator(t *testing.T) {
	// If NUL ever validated, "a\x00b" as an EntityID under view "v" would
	// collide with view "v\x00a", entity "b". Prove NUL is unreachable.
	if err := ValidateEntityID(EntityID("a\x00b")); err == nil {
		t.Fatal("EntityID containing the nav.go key separator (NUL) was accepted -- composite-key collision is possible")
	}
}

// --- Wire forward/backward compatibility (attack surface #1, #7) ------
//
// The load-bearing frozen-v1 claim. TargetRef rides no existing envelope
// (verified: it is a standalone type, embedded in no Command/Delta/Event),
// so the only new wire field on a frozen message is Event.Crisis. Prove
// BOTH directions across the amendment boundary.

// A v2 producer emitting Crisis==true, decoded by a v2 consumer.
func TestAttack_WireCompat_V2CrisisRoundTrips(t *testing.T) {
	ev := Event{
		Kind:       "flood.major",
		Tick:       200,
		Severity:   SeverityCritical,
		Crisis:     true,
		EntityRefs: []string{"district:harbour"},
	}
	b, err := EncodeEvent(ev)
	if err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}
	if !strings.Contains(string(b), `"crisis":true`) {
		t.Fatalf("Crisis==true did not reach the wire: %s", b)
	}
	got, err := DecodeEvent(b)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	if got.Crisis != true {
		t.Fatalf("Crisis round-trip lost the flag: %+v", got)
	}
}

// A v1 producer message (no crisis key at all, plus a hypothetical
// UNKNOWN future key) decoded by v2 code: crisis must decode false, the
// decode must not error, and an unknown key must be tolerated (forward
// compat -- a v1 consumer seeing a v2 message must not choke).
func TestAttack_WireCompat_UnknownKeyTolerated(t *testing.T) {
	// v2 message carrying a key a v1 consumer has never heard of.
	forward := []byte(`{"kind":"gang.formed","tick":9,"severity":"warning","crisis":true,"someFutureField":{"x":1}}`)
	ev, err := DecodeEvent(forward)
	if err != nil {
		t.Fatalf("DecodeEvent rejected a message with an unknown field: %v -- a frozen additive contract must tolerate forward keys", err)
	}
	if ev.Kind != "gang.formed" || ev.Crisis != true {
		t.Fatalf("known fields mis-decoded alongside an unknown key: %+v", ev)
	}

	// v1 message: no crisis key. Must decode to Crisis==false.
	backward := []byte(`{"kind":"gang.formed","tick":9,"severity":"warning"}`)
	ev2, err := DecodeEvent(backward)
	if err != nil {
		t.Fatalf("DecodeEvent(v1 message) = %v, want clean decode", err)
	}
	if ev2.Crisis != false {
		t.Fatalf("absent crisis key decoded to %v, want false", ev2.Crisis)
	}
}

// TestAttack_WireCompat_CrisisFalseNeverOnWire proves omitempty holds so
// a v2 producer emitting a non-crisis event is byte-identical to what v1
// produced -- a v1 consumer must not even see the key.
func TestAttack_WireCompat_CrisisFalseNeverOnWire(t *testing.T) {
	ev := Event{Kind: "milestone.reached", Tick: 1, Severity: SeverityInfo}
	b, err := EncodeEvent(ev)
	if err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}
	if strings.Contains(string(b), "crisis") {
		t.Fatalf("non-crisis event leaked the crisis key (breaks v1 byte-identity): %s", b)
	}
}

// --- Determinism hammer (attack surface #4, GR#21) --------------------

func TestAttack_Determinism_RepeatedMarshalByteIdentical(t *testing.T) {
	ev := Event{
		Kind:       "junction.gridlocked",
		Tick:       42,
		Severity:   SeverityWarning,
		Crisis:     true,
		EntityRefs: []string{"junction:14", "junction:15"},
		// Fields is a map -- the classic non-determinism trap. encoding/json
		// sorts map keys, so repeated marshals must still be byte-identical.
		Fields: map[string]string{"zeta": "1", "alpha": "2", "mu": "3"},
	}
	first, err := EncodeEvent(ev)
	if err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}
	for i := 0; i < 500; i++ {
		b, err := EncodeEvent(ev)
		if err != nil {
			t.Fatalf("EncodeEvent iter %d: %v", i, err)
		}
		if string(b) != string(first) {
			t.Fatalf("Event marshal non-deterministic at iter %d:\n first: %s\n got:   %s", i, first, b)
		}
	}

	ref := TargetRef{ViewName: "f2.ledger", EntityID: EntityID("line-42")}
	rb, _ := json.Marshal(ref)
	for i := 0; i < 500; i++ {
		b, _ := json.Marshal(ref)
		if string(b) != string(rb) {
			t.Fatalf("TargetRef marshal non-deterministic at iter %d", i)
		}
	}
}
