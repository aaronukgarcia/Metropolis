package power

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCatalogue(t *testing.T) PylonCatalogue {
	t.Helper()
	cat, err := LoadPylonCatalogue(writeTemp(t, validCatalogueJSON), "test-cid")
	if err != nil {
		t.Fatalf("LoadPylonCatalogue: %v", err)
	}
	return cat
}

func testBounds() Bounds { return Bounds{Width: 200, Height: 200} }

func TestPlaceLine_AcceptsAndCarriesCapacity(t *testing.T) {
	p := New(testCatalogue(t), testBounds())
	line, err := p.PlaceLine("standardLattice", 3, 4, 30, 40, "test-cid")
	if err != nil {
		t.Fatalf("PlaceLine: %v", err)
	}
	if line.ID != 1 {
		t.Errorf("ID = %d, want 1 (monotonic from first placement)", line.ID)
	}
	if line.Class != ClassStandardLattice {
		t.Errorf("Class = %v, want standardLattice", line.Class)
	}
	if line.CapacityMW != 40 {
		t.Errorf("CapacityMW = %v, want catalogue's 40", line.CapacityMW)
	}
}

func TestPlaceLine_Rejections(t *testing.T) {
	p := New(testCatalogue(t), testBounds())
	cases := []struct {
		name  string
		class string
		from  [2]int
		to    [2]int
		code  string
	}{
		{"unknown class", "hvdcFrance", [2]int{0, 0}, [2]int{1, 1}, ErrUnknownClass},
		{"empty class", "", [2]int{0, 0}, [2]int{1, 1}, ErrUnknownClass},
		{"endpoint out of bounds", "localPole", [2]int{0, 0}, [2]int{200, 5}, ErrPlacementInvalid},
		{"negative endpoint", "localPole", [2]int{-1, 0}, [2]int{5, 5}, ErrPlacementInvalid},
		{"degenerate segment", "localPole", [2]int{7, 7}, [2]int{7, 7}, ErrPlacementInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.PlaceLine(tc.class, tc.from[0], tc.from[1], tc.to[0], tc.to[1], "test-cid")
			if err == nil {
				t.Fatalf("expected registry error %s, got nil", tc.code)
			}
			var e *errs.E
			if !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("error = %v, want registry code %s", err, tc.code)
			}
		})
	}
	// A rejected placement must leave no residue.
	lines, err := p.Lines("test-cid")
	if err != nil {
		t.Fatalf("Lines: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("rejected placements leaked state: %d lines stored", len(lines))
	}
}

// TestLines_DeterministicOrder (GR#21): placements observed through Lines
// come back in placement-ID order regardless of insertion interleaving,
// and the returned slice is a copy — mutating it cannot touch the store.
func TestLines_DeterministicOrder(t *testing.T) {
	p := New(testCatalogue(t), testBounds())
	for i := 0; i < 5; i++ {
		if _, err := p.PlaceLine("localPole", i, 0, i+1, 0, "test-cid"); err != nil {
			t.Fatalf("PlaceLine %d: %v", i, err)
		}
	}
	first, err := p.Lines("test-cid")
	if err != nil {
		t.Fatalf("Lines: %v", err)
	}
	if len(first) != 5 {
		t.Fatalf("len(Lines) = %d, want 5", len(first))
	}
	for i := 1; i < len(first); i++ {
		if first[i].ID <= first[i-1].ID {
			t.Fatalf("IDs not ascending at %d: %d then %d", i, first[i-1].ID, first[i].ID)
		}
	}
	second, _ := p.Lines("test-cid")
	for i := range second {
		if first[i] != second[i] {
			t.Fatalf("two consecutive Lines() calls disagree at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
	first[0].CapacityMW = -999
	third, _ := p.Lines("test-cid")
	if third[0].CapacityMW <= 0 {
		t.Fatal("mutating the returned slice changed the store — Lines is not a copy")
	}
}

// powerAPIByteCopy performs SEC-020's attack — a plain PowerAPI struct
// copy — via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// ui.screen.map sec020_test.go's mapScreenByteCopy: a literal `cp := *p`
// is legal Go but is exactly what `go vet`'s copylocks check flags; the
// byte-level copy produces identical runtime semantics without the
// flaggable copy expression.
func powerAPIByteCopy(p *PowerAPI) *PowerAPI {
	c := new(PowerAPI)
	*(*[unsafe.Sizeof(PowerAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(PowerAPI{})]byte)(unsafe.Pointer(p))
	return c
}

func TestPowerAPI_CopiedReceiverRejected(t *testing.T) {
	p := New(testCatalogue(t), testBounds())
	cp := powerAPIByteCopy(p)
	if _, err := cp.PlaceLine("localPole", 0, 0, 1, 1, "test-cid"); err == nil {
		t.Fatal("PlaceLine on struct copy accepted, want ErrPowerAPICopied")
	} else {
		var e *errs.E
		if !errors.As(err, &e) || e.Code != ErrPowerAPICopied {
			t.Fatalf("error = %v, want registry code %s", err, ErrPowerAPICopied)
		}
	}
	if _, err := cp.Lines("test-cid"); err == nil {
		t.Fatal("Lines on struct copy accepted, want ErrPowerAPICopied")
	}
}

func TestNew_ZeroBounds_FailClosed(t *testing.T) {
	p := New(testCatalogue(t), Bounds{})
	if _, err := p.PlaceLine("localPole", 0, 0, 0, 1, "test-cid"); err == nil {
		t.Fatal("placement into zero bounds accepted, want ErrPlacementInvalid")
	}
}
