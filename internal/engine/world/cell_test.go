package world

import (
	"testing"
	"unsafe"
)

// TestCellCoreSizeInBudget is AC-4's size check: the Cell struct (the
// API-return snapshot — see grid.go's doc comment on why the REAL
// storage is struct-of-arrays, not repeated Cell values) should be in
// the documented ~30-byte-core range. This measures the RETURN VALUE
// type; memory_test.go measures the actual SoA storage cost per cell,
// which is the number that matters for the 4GB budget (AC-19).
func TestCellCoreSizeInBudget(t *testing.T) {
	size := unsafe.Sizeof(Cell{})
	t.Logf("Cell struct size: %d bytes", size)
	if size > 40 {
		t.Fatalf("Cell struct grew to %d bytes — expected roughly ~30 bytes core (§2.4/AC-4), investigate field additions/padding", size)
	}
}

// TestCellCoreSizeInBudget_ProvenFail: PROOF — a struct padded out with
// extra fields (simulated here via a locally-defined oversized type)
// DOES trip the same 40-byte ceiling, confirming the check is a real
// upper bound, not a tautology that always passes.
func TestCellCoreSizeInBudget_ProvenFail(t *testing.T) {
	type oversizedCell struct {
		Elevation    float32
		Slope        SlopeClass
		Surface      Surface
		Owner        uint32
		Zoning       Zoning
		StructureRef uint32
		LandValue    float32
		Overlay      OverlayScratch
		Padding      [16]byte // simulates uncontrolled growth
	}
	oc := oversizedCell{Padding: [16]byte{1}}
	if oc.Padding[0] != 1 { // read Padding so it's a genuinely used field, not dead weight
		t.Fatal("sanity check failed: Padding field round-trip broke")
	}
	size := unsafe.Sizeof(oc)
	if size <= 40 {
		t.Fatalf("sanity check failed: expected the padded type to exceed 40 bytes, got %d", size)
	}
}
