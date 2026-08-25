package mapscreen

import "testing"

// TestPowerSpanCells_DeterministicBresenham pins the span walker's
// contract: endpoints inclusive, cell count = max(|dx|,|dy|)+1 for these
// axis-aligned and diagonal cases, first/last cells exactly the
// requested endpoints (GR#21: same inputs, same cells, every time).
func TestPowerSpanCells_DeterministicBresenham(t *testing.T) {
	cases := []struct {
		name       string
		from, to   [2]int
		wantFirst  [2]int
		wantLast   [2]int
		wantMiddle [2]int // a known interior cell
		len        int
	}{
		{"horizontal", [2]int{2, 2}, [2]int{5, 2}, [2]int{2, 2}, [2]int{5, 2}, [2]int{3, 2}, 4},
		{"vertical reverse", [2]int{4, 9}, [2]int{4, 6}, [2]int{4, 9}, [2]int{4, 6}, [2]int{4, 8}, 4},
		{"diagonal", [2]int{0, 0}, [2]int{3, 3}, [2]int{0, 0}, [2]int{3, 3}, [2]int{1, 1}, 4},
		{"single step", [2]int{7, 7}, [2]int{8, 7}, [2]int{7, 7}, [2]int{8, 7}, [2]int{7, 7}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cells := powerSpanCells(tc.from[0], tc.from[1], tc.to[0], tc.to[1])
			if len(cells) != tc.len {
				t.Fatalf("len = %d, want %d (%v)", len(cells), tc.len, cells)
			}
			if cells[0] != tc.wantFirst || cells[len(cells)-1] != tc.wantLast {
				t.Fatalf("endpoints = %v..%v, want %v..%v", cells[0], cells[len(cells)-1], tc.wantFirst, tc.wantLast)
			}
			if cells[1] != tc.wantMiddle && len(cells) > 2 {
				if cells[1] != tc.wantMiddle && cells[2] != tc.wantMiddle {
					t.Fatalf("middle cell %v not covered (cells %v)", tc.wantMiddle, cells)
				}
			}
		})
	}
}
