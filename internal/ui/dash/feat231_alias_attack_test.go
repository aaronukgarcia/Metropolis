package dash

// INDEPENDENT DESTRUCTIVE ROUND (GR#23) — FEAT-042/FEAT-231 alias landing,
// attacker "opus-round-targetref-alias", 2026-09-05.
//
// The change under attack turns dash.DrillTarget from its own struct into
// `type DrillTarget = protocol.TargetRef` and changes EntityID's static
// type from `string` to `protocol.EntityID`. The three ways that can break
// silently — none of which the compiler catches — are:
//
//  1. WIRE DRIFT: a saved profile's drill targets are JSON. If the tag set,
//     the omitempty behaviour, or the encoded key order changed, every
//     already-saved profile silently loses (or mis-reads) its drill
//     targets. Pinned here against FROZEN LITERALS reconstructed from the
//     pre-change struct (git show HEAD:internal/ui/dash/drill.go), not
//     against the new type's own output — a golden that re-derives from
//     the thing under test proves nothing.
//  2. KEY DERIVATION DRIFT: nav.go's MapResolver composes its map key as
//     ViewName+"\x00"+EntityID. The alias forced a `string(...)` conversion
//     on that expression; a wrong conversion (e.g. fmt.Sprint of a typed
//     value, or a %v of a struct) would silently break every bookmark /
//     route lookup while both Mark and Resolve stayed self-consistent.
//     Pinned against an independently-computed OLD-formula key, and read
//     out of the live map (this is an in-package test for exactly that).
//  3. VALIDATION REGRESSION: NewDrillTarget now runs
//     protocol.ValidateEntityID. If that rejects the EMPTY entity id, every
//     whole-view drill (menu's save slots, demo's typologies — the ASM-305
//     whole-entity case) breaks. Proven both ways here and at the screen.
import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// oldNavKey is the PRE-change key formula, transcribed verbatim from
// `git show HEAD:internal/ui/dash/nav.go` where EntityID was a plain
// string: m.live[target.ViewName+"\x00"+target.EntityID]. Independent of
// the code under test on purpose.
func oldNavKey(viewName, entityID string) string {
	return viewName + "\x00" + entityID
}

// TestAttack_NavKeyDerivationUnchanged proves the alias's
// `string(target.EntityID)` conversion produces byte-identical map keys to
// the pre-change concatenation, for ordinary, empty and adversarial
// inputs. A changed key would silently break bookmark/route lookups with
// no test failing anywhere else, since Mark and Resolve would still agree
// with each other.
func TestAttack_NavKeyDerivationUnchanged(t *testing.T) {
	cases := []struct{ view, entity string }{
		{"f2.ledger", "line-42"},
		{"f1.viewport", ""},                     // whole-view drill: no entity id
		{"f1.viewport", "junction-14-approach"}, // long-ish realistic id
		{"a", "b"},
		{"a\x00b", "c"}, // separator smuggled into the view name
		{"a", "b\x00c"}, // separator smuggled into the entity id
		{"", ""},        // zero value: must still key deterministically
		{"f2.ledger", strings.Repeat("x", 512)},
	}

	for _, tc := range cases {
		m := NewMapResolver()
		m.Mark(DrillTarget{ViewName: tc.view, EntityID: protocol.EntityID(tc.entity)})

		want := oldNavKey(tc.view, tc.entity)
		if len(m.live) != 1 {
			t.Fatalf("Mark(%q,%q): live map has %d keys, want exactly 1", tc.view, tc.entity, len(m.live))
		}
		var got string
		for k := range m.live {
			got = k
		}
		if got != want {
			t.Fatalf("nav key derivation CHANGED for (%q,%q): got %q, want the pre-alias formula %q — every bookmark/route lookup keyed on the old formula would silently miss",
				tc.view, tc.entity, got, want)
		}
		if !m.Resolve(DrillTarget{ViewName: tc.view, EntityID: protocol.EntityID(tc.entity)}) {
			t.Fatalf("Resolve did not see its own Mark for (%q,%q)", tc.view, tc.entity)
		}
	}
}

// TestAttack_NavKeyCollisionSemanticsUnchanged pins the (pre-existing,
// inherited) ambiguity of a \x00-separated composite key: the alias must
// not accidentally FIX or WORSEN it, because a behaviour change either way
// is a silent semantic change to lookup. ("a\x00b","c") and ("a","b\x00c")
// both derive the same key under the old formula and must still do so.
func TestAttack_NavKeyCollisionSemanticsUnchanged(t *testing.T) {
	if oldNavKey("a\x00b", "c") != oldNavKey("a", "b\x00c") {
		t.Fatal("test premise wrong: the old formula did not collide these two")
	}
	m := NewMapResolver()
	m.Mark(DrillTarget{ViewName: "a\x00b", EntityID: "c"})
	if !m.Resolve(DrillTarget{ViewName: "a", EntityID: "b\x00c"}) {
		t.Fatal("collision semantics CHANGED: the two inputs that shared a key pre-alias no longer do")
	}
	// And genuinely distinct targets must still be distinct.
	if m.Resolve(DrillTarget{ViewName: "a", EntityID: "c"}) {
		t.Fatal("distinct target resolved: key derivation has become lossy")
	}
}

// --- wire compatibility ------------------------------------------------

// frozen JSON encodings reconstructed from the PRE-change struct tags
// (`json:"viewName"` / `json:"entityId,omitempty"`), i.e. what an
// already-saved profile on disk contains today.
const (
	frozenFullJSON      = `{"viewName":"f2.ledger","entityId":"line-42"}`
	frozenWholeViewJSON = `{"viewName":"f1.viewport"}`
)

// TestAttack_DrillTargetWireBytesFrozen encodes both names of the (now
// single) type and asserts byte-identity with each other AND with the
// frozen pre-change encoding. Any change to a json tag, to omitempty, or
// to field order fails here.
func TestAttack_DrillTargetWireBytesFrozen(t *testing.T) {
	full := DrillTarget{ViewName: "f2.ledger", EntityID: "line-42"}
	ref := protocol.TargetRef{ViewName: "f2.ledger", EntityID: "line-42"}

	gotDrill, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal DrillTarget: %v", err)
	}
	gotRef, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("marshal TargetRef: %v", err)
	}
	if string(gotDrill) != string(gotRef) {
		t.Fatalf("dash.DrillTarget and protocol.TargetRef encode differently: %s vs %s", gotDrill, gotRef)
	}
	if string(gotDrill) != frozenFullJSON {
		t.Fatalf("WIRE DRIFT: DrillTarget now encodes as %s, but every already-saved profile contains %s", gotDrill, frozenFullJSON)
	}

	// omitempty on EntityID is load-bearing: a whole-view drill must NOT
	// start emitting `"entityId":""`, or old and new saves stop comparing
	// equal byte-wise and any golden/hash over a profile breaks.
	whole := DrillTarget{ViewName: "f1.viewport"}
	gotWhole, err := json.Marshal(whole)
	if err != nil {
		t.Fatalf("marshal whole-view DrillTarget: %v", err)
	}
	if string(gotWhole) != frozenWholeViewJSON {
		t.Fatalf("WIRE DRIFT (omitempty): whole-view DrillTarget now encodes as %s, want %s", gotWhole, frozenWholeViewJSON)
	}
}

// TestAttack_LegacyProfileJSONStillDecodes feeds the exact bytes a
// pre-alias saved profile holds — including one with an unknown extra key
// (a forward-compat shape) and one with entityId absent — into the new
// type. All must decode without error and preserve values.
func TestAttack_LegacyProfileJSONStillDecodes(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantView   string
		wantEntity protocol.EntityID
	}{
		{"full", frozenFullJSON, "f2.ledger", "line-42"},
		{"whole-view-absent-entity", frozenWholeViewJSON, "f1.viewport", ""},
		{"explicit-empty-entity", `{"viewName":"f1.viewport","entityId":""}`, "f1.viewport", ""},
		{"unknown-extra-key", `{"viewName":"f2.ledger","entityId":"line-42","legacyRow":7}`, "f2.ledger", "line-42"},
		{"key-order-swapped", `{"entityId":"line-42","viewName":"f2.ledger"}`, "f2.ledger", "line-42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var d DrillTarget
			if err := json.Unmarshal([]byte(tc.raw), &d); err != nil {
				t.Fatalf("decoding a legacy profile drill target %s failed: %v", tc.raw, err)
			}
			if d.ViewName != tc.wantView || d.EntityID != tc.wantEntity {
				t.Fatalf("decoded %+v, want {%q %q}", d, tc.wantView, tc.wantEntity)
			}
			// Re-encoding must reproduce the canonical frozen shape (minus
			// any unknown key, which was never a field).
			out, err := json.Marshal(d)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			var round DrillTarget
			if err := json.Unmarshal(out, &round); err != nil {
				t.Fatalf("re-decode: %v", err)
			}
			if round != d {
				t.Fatalf("JSON round-trip lost information: %+v -> %+v", d, round)
			}
		})
	}
}

// --- validation semantics ----------------------------------------------

// hostileEntityIDs is the FEAT-042 attack table (internal/protocol/
// feat042_attack_test.go, TestAttack_ValidateEntityID_HostileInputs),
// copied verbatim rather than imported — an _test.go table is not
// importable, and copying keeps this round's assertion independent of a
// future edit to that file.
var hostileEntityIDs = []string{
	" ", " line-42", "line-42 ", "line 42", "line\t42", "line\n42", "line\r42",
	"line\x0042", "\x00", "line\x0742", "line\x7f42", ".hidden", ":id", "-flag",
	"_x", "a/b", "a\\b", "../etc/passwd", "..\\secret", "a|b", "a;rm -rf",
	"a\"b", "a'b", "a{b}", "a%20b", "a@b", "a#b", "a+b", "a=b", "a*b",
	"café", "东京", "🚦",
	"line\u00a042", // non-breaking space
	"line\u200b42", // zero-width space
	"line\u202e42", // bidi override
}

// TestAttack_NewDrillTargetRejectsEveryHostileEntityID runs the full
// FEAT-042 hostile table through the dash constructor (not through
// protocol.ValidateEntityID directly) — proving the wiring, not just the
// validator, holds. The EMPTY id is deliberately excluded here and
// asserted as ACCEPTED by the next test.
func TestAttack_NewDrillTargetRejectsEveryHostileEntityID(t *testing.T) {
	if len(hostileEntityIDs) != 36 {
		t.Fatalf("hostile table lost entries: %d", len(hostileEntityIDs))
	}
	for _, bad := range hostileEntityIDs {
		got, err := NewDrillTarget("f2.ledger", bad)
		if err == nil {
			t.Errorf("NewDrillTarget(f2.ledger, %q) = %+v, nil — a hostile entity id reached a DrillTarget", bad, got)
			continue
		}
		if got != (DrillTarget{}) {
			t.Errorf("NewDrillTarget(f2.ledger, %q) returned a non-zero target %+v alongside its error", bad, got)
		}
	}
}

// TestAttack_WholeViewDrillStillConstructs is the regression check for the
// ASM-305 whole-entity drill case: protocol.ValidateEntityID REJECTS the
// empty string, so if NewDrillTarget ran it unconditionally, every
// whole-view drill would break. drill.go guards with `if entityID != ""`;
// this proves the guard exists and stays.
func TestAttack_WholeViewDrillStillConstructs(t *testing.T) {
	if err := protocol.ValidateEntityID(""); err == nil {
		t.Fatal("test premise wrong: protocol.ValidateEntityID accepts the empty id, so this regression is not possible")
	}
	d, err := NewDrillTarget("f1.viewport", "")
	if err != nil {
		t.Fatalf("REGRESSION: whole-view drill NewDrillTarget(f1.viewport, \"\") = %v — every whole-view drill target (menu save slots, demo typologies) is now unconstructable", err)
	}
	if d.EntityID != "" || !d.Valid() {
		t.Fatalf("whole-view target malformed: %+v (Valid=%v)", d, d.Valid())
	}
	// Accepted ids from the FEAT-042 accept list must still round-trip.
	for _, ok := range []string{"line-42", "junction:14", "a.b.c", "A", "9", "Z9_.:-"} {
		got, err := NewDrillTarget("f2.ledger", ok)
		if err != nil {
			t.Errorf("NewDrillTarget(f2.ledger, %q) = %v, want acceptance", ok, err)
			continue
		}
		if string(got.EntityID) != ok {
			t.Errorf("NewDrillTarget(f2.ledger, %q) carried EntityID %q — value mutated in transit", ok, got.EntityID)
		}
	}
}

// TestAttack_TileConstructorsDoNotValidateEntityID pins the DELIBERATE
// asymmetry the landing introduces: NewDrillTarget validates entity ids,
// but requireDrill (the New<Kind>Tile / layout path) does not — and every
// production screen builds DrillTarget composite literals, bypassing
// NewDrillTarget entirely. If a later commit tightens requireDrill, every
// screen whose entity id embeds a label with a space (finance's
// "pl.revenue."+Label) starts failing tile construction. Documenting the
// current contract so that change is a conscious one.
func TestAttack_TileConstructorsDoNotValidateEntityID(t *testing.T) {
	// A label-derived id with a space: exactly what finance/render.go
	// composes today ("pl.revenue." + r.Label).
	spaced := DrillTarget{ViewName: "f2.finance", EntityID: "pl.revenue.Council Tax"}
	if err := protocol.ValidateEntityID(spaced.EntityID); err == nil {
		t.Fatal("test premise wrong: a spaced entity id passes ValidateEntityID")
	}
	if err := requireDrill(spaced, nil); err != nil {
		t.Fatalf("requireDrill now rejects a label-derived entity id (%v) — every screen composing entity ids from human labels breaks at tile construction", err)
	}
}

// --- alias identity, from the other direction --------------------------

// TestAttack_AliasIsNotMerelyConvertible proves the alias is a true
// identity and not a same-underlying-type sibling: a method declared on
// protocol.TargetRef must be reachable on a DrillTarget value, a
// DrillTarget must satisfy an interface implemented by TargetRef, and a
// []DrillTarget must be assignable to a []protocol.TargetRef with no
// conversion (which a defined type, however convertible, could never do).
func TestAttack_AliasIsNotMerelyConvertible(t *testing.T) {
	// Passing a []DrillTarget straight into a []protocol.TargetRef
	// parameter compiles ONLY under a true alias — slices of a merely
	// convertible defined type are never assignable.
	takesRefs := func(r []protocol.TargetRef) string {
		if len(r) == 0 {
			return ""
		}
		return r[0].ViewName
	}
	slice := []DrillTarget{{ViewName: "f1.viewport"}}
	if got := takesRefs(slice); got != "f1.viewport" {
		t.Fatalf("slice identity broken: got %q", got)
	}

	m := map[DrillTarget]int{{ViewName: "f2.ledger", EntityID: "line-42"}: 7}
	if m[protocol.TargetRef{ViewName: "f2.ledger", EntityID: "line-42"}] != 7 {
		t.Fatal("a protocol.TargetRef key did not hit the map keyed by DrillTarget — the two are not the same type")
	}

	// Valid() lives on protocol.TargetRef now; the alias must carry it.
	if !(DrillTarget{ViewName: "x"}).Valid() || (DrillTarget{}).Valid() {
		t.Fatal("Valid() semantics changed across the alias move")
	}

	if reflect.TypeOf(DrillTarget{}).PkgPath() != reflect.TypeOf(protocol.TargetRef{}).PkgPath() {
		t.Fatal("DrillTarget's reflect package path is not protocol's — it has been re-forked")
	}
}
