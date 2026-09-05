package dash_test

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
)

// TestDrillTargetFieldsMatchTargetRef is FEAT-042 AC-22's drift-prevention
// check: dash.DrillTarget is its own independent struct rather than
// embedding/wrapping protocol.TargetRef directly (drill.go's own doc
// comment explains why -- DrillTarget predates TargetRef and needed its
// own validation-carrying constructor), so this reflection-based
// field-parity test, modeled directly on
// TestHeaderWireFieldsMatchHeader (internal/foundation/serialize), proves
// the two stay in lockstep field-for-field. Without this test, the two
// structs could silently drift the next time either side adds a field --
// exactly the shape int.serializer's Header/headerWire drift test
// (ASM-096) exists to prevent, generalised here to a second boundary.
func TestDrillTargetFieldsMatchTargetRef(t *testing.T) {
	drillType := reflect.TypeOf(dash.DrillTarget{})
	refType := reflect.TypeOf(protocol.TargetRef{})

	if drillType.NumField() != refType.NumField() {
		t.Fatalf("dash.DrillTarget has %d fields but protocol.TargetRef has %d -- these two structs must be kept in EXACT 1:1 correspondence by hand: whoever adds or removes a field on one must make the matching change on the other, or the odd one out silently diverges (FEAT-042 AC-22)", drillType.NumField(), refType.NumField())
	}

	for i := 0; i < refType.NumField(); i++ {
		rf := refType.Field(i)

		wantTag := rf.Tag.Get("json")
		if wantTag == "" || wantTag == "-" {
			t.Fatalf("protocol.TargetRef field %q has no usable `json:\"...\"` tag, so this drift test cannot verify dash.DrillTarget mirrors it correctly", rf.Name)
		}
		// Strip a trailing ",omitempty" for the tag-name comparison --
		// TargetRef.EntityID and DrillTarget.EntityID both use it, but
		// the two sides are free to differ on the option itself as long
		// as the wire KEY NAME agrees.
		wantWireName := wantTag
		for i, c := range wantTag {
			if c == ',' {
				wantWireName = wantTag[:i]
				break
			}
		}

		df, ok := drillType.FieldByName(rf.Name)
		if !ok {
			t.Fatalf("protocol.TargetRef field %q has no corresponding dash.DrillTarget.%s -- dash.DrillTarget has drifted out of sync with protocol.TargetRef (FEAT-042 AC-22); add the missing field", rf.Name, rf.Name)
		}

		gotTag := df.Tag.Get("json")
		gotWireName := gotTag
		for i, c := range gotTag {
			if c == ',' {
				gotWireName = gotTag[:i]
				break
			}
		}
		if gotWireName != wantWireName {
			t.Fatalf("dash.DrillTarget.%s has json wire name %q, want %q to match protocol.TargetRef.%s", df.Name, gotWireName, wantWireName, rf.Name)
		}

		// TargetRef.EntityID is protocol.EntityID (a defined string
		// type); DrillTarget.EntityID is a plain string -- both are
		// string-KINDED, which is the compatibility bar this test holds
		// the two structs to (drill.go documents DrillTarget as expected
		// to losslessly convert to/from TargetRef, not to share the
		// exact same Go type for every field).
		if df.Type.Kind() != reflect.String || rf.Type.Kind() != reflect.String {
			t.Fatalf("dash.DrillTarget.%s (%s) and protocol.TargetRef.%s (%s) must both be string-kinded to stay losslessly convertible", df.Name, df.Type, rf.Name, rf.Type)
		}
	}
}

// TestDrillTargetLosslesslyConvertsToTargetRef proves the round-trip
// AC-22 requires in practice, not just at the type-shape level: any
// dash.DrillTarget converts to an equivalent protocol.TargetRef and back
// without losing information.
func TestDrillTargetLosslesslyConvertsToTargetRef(t *testing.T) {
	d, err := dash.NewDrillTarget("f2.ledger", "line-42")
	if err != nil {
		t.Fatalf("NewDrillTarget: %v", err)
	}

	ref := protocol.TargetRef{ViewName: d.ViewName, EntityID: protocol.EntityID(d.EntityID)}

	back, err := dash.NewDrillTarget(ref.ViewName, string(ref.EntityID))
	if err != nil {
		t.Fatalf("NewDrillTarget (round-trip): %v", err)
	}
	if back != d {
		t.Fatalf("DrillTarget -> TargetRef -> DrillTarget round-trip mismatch: got %+v, want %+v", back, d)
	}
}
