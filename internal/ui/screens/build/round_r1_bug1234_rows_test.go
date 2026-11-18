package build

import (
	"math"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// Attack: no draw may write outside its rect (guard margin of 7 cells on all
// sides), never panic, across sizes and awkward data.
func TestRoundR1_BUG1234_RowsStayInRect(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	names := []string{"", "x", "dwelling", "Büro-£ŝtore…long-name-with-multibyte-runes-ééé", strings.Repeat("w", 80)}
	statuses := []BuildOrderStatus{StatusComplete, StatusInProgress, StatusMaterialsPending, StatusLabourPending, "bogus-status-that-is-very-long-indeed"}
	var orders []BuildOrder
	for i, n := range names {
		for j, st := range statuses {
			orders = append(orders, BuildOrder{ID: uint64(i*10 + j), Zone: n, MaterialsDrawn: math.MinInt64, MaterialsBillTotal: math.MaxInt64, LabourRemaining: math.MinInt64, LeadTimeRemaining: -5, Status: st})
		}
	}
	var cat []CatalogueEntry
	for _, n := range names {
		for _, c := range []string{"", "£15000.00", "£-100", "£-5.00", "£1.50", "£99999999999999999999.00", "free"} {
			cat = append(cat, CatalogueEntry{Name: n, Section: "R", CostRaw: c, CapacityRaw: "8 homes, 0 jobs"})
		}
	}
	zs := []ZoneInfo{{Name: "", Zone: "z"}, {Name: names[3], Materials: math.MaxInt64, Labour: math.MinInt64}}
	for _, W := range []int{0, 1, 2, 5, 20, 39, 40, 41, 60, 200} {
		for _, H := range []int{0, 1, 2, 3, 10} {
			for _, which := range []string{"q", "qa", "q0", "c", "z"} {
				const m = 7
				buf := core.NewBuffer(W+2*m, H+2*m)
				rect := core.Rect{X: m, Y: m, W: W, H: H}
				os := orders
				if which == "q0" {
					os = nil
				}
				if which == "qa" {
					os = orders[:0:0]
					for _, o := range orders {
						if o.Status != StatusComplete {
							os = append(os, o)
						}
					}
				}
				switch which {
				case "c":
					RenderCatalogue(buf, rect, cat, true, style)
				case "z":
					RenderZones(buf, rect, zs, true, style)
				default:
					RenderQueue(buf, rect, os, true, style)
				}
				for y := 0; y < H+2*m; y++ {
					for x := 0; x < W+2*m; x++ {
						inside := x >= m && x < m+W && y >= m && y < m+H
						if !inside && buf.Get(x, y).Rune != 0 && buf.Get(x, y).Rune != ' ' {
							t.Fatalf("%s W=%d H=%d wrote outside rect at %d,%d", which, W, H, x, y)
						}
					}
				}
			}
		}
	}
}

// Attack: at realistic widths (>=40) every queue row for an ordinary name keeps
// its whole status badge.
func TestRoundR1_BUG1234_BadgeNeverCutAt40Plus(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	for _, W := range []int{60, 61, 120, 200} {
		for _, st := range []BuildOrderStatus{StatusInProgress, StatusMaterialsPending, StatusLabourPending} {
			os := []BuildOrder{{ID: 1234, Zone: "light-industrial-park", MaterialsDrawn: 1234, MaterialsBillTotal: 6543, LabourRemaining: 99, LeadTimeRemaining: 365, Status: st}}
			buf := core.NewBuffer(W, 4)
			RenderQueue(buf, core.Rect{W: W, H: 4}, os, true, style)
			rows := renderedText(buf, core.Rect{W: W, H: 4})
			if !strings.Contains(rows[1], "["+string(st)+"]") {
				t.Errorf("W=%d status %s badge cut: %q", W, st, rows[1])
			}
		}
	}
}
