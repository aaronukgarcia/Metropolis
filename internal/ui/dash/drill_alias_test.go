package dash

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// This assignment is a compile-time proof that DrillTarget IS
// protocol.TargetRef, not merely a look-alike: a real distinct-but-
// convertible struct would need an explicit conversion here and this
// file would fail to build. If a future edit ever re-forks DrillTarget
// into its own struct type, this line stops compiling immediately.
var _ protocol.TargetRef = DrillTarget{}

// TestDrillTargetIsTargetRefAlias is the FEAT-231/architect-ruling
// (2026-09-05) closeout check: dash.DrillTarget must be a genuine Go type
// ALIAS (`type DrillTarget = protocol.TargetRef`), not a second,
// structurally-identical struct declaration. reflect.TypeOf sees straight
// through an alias to the underlying named type, so if this ever fails it
// means DrillTarget has been re-declared as its own type -- exactly the
// SSOT fragmentation the one-DrillTarget-type doctrine exists to catch,
// and exactly why the doctrine's live-tree gate
// (viewgate.TestNoSecondDrillTargetType) treats a "TargetRef"-shaped
// struct in internal/protocol as sanctioned but nothing else.
func TestDrillTargetIsTargetRefAlias(t *testing.T) {
	drillType := reflect.TypeOf(DrillTarget{})
	refType := reflect.TypeOf(protocol.TargetRef{})

	if drillType != refType {
		t.Fatalf("dash.DrillTarget (reflect type %v) is NOT identical to protocol.TargetRef (reflect type %v) -- "+
			"DrillTarget must stay declared as `type DrillTarget = protocol.TargetRef` (a type alias), never as its "+
			"own struct, or the one-DrillTarget-type doctrine is silently violated again", drillType, refType)
	}

	// A DrillTarget value's EntityID field must be assignable straight
	// from a protocol.EntityID value with no conversion helper -- proving
	// the field, not just the outer type name, is genuinely shared.
	var eid protocol.EntityID = "line-42"
	d := DrillTarget{ViewName: "f2.ledger", EntityID: eid}
	ref := d // no conversion needed: d is already a protocol.TargetRef
	if ref.EntityID != eid {
		t.Fatalf("DrillTarget->TargetRef identity broken: got EntityID %q, want %q", ref.EntityID, eid)
	}
}
