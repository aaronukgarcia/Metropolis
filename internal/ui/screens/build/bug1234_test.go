package build

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// BUG-1234 part 2: completed orders collapse into one line, active first.
func TestBUG1234_Queue_CompletedCollapseAndActiveFirst(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	var orders []BuildOrder
	for i := uint64(1); i <= 23; i++ {
		orders = append(orders, BuildOrder{ID: i, Zone: "dwelling", MaterialsDrawn: 100, MaterialsBillTotal: 100, Status: StatusComplete})
	}
	orders = append(orders,
		BuildOrder{ID: 24, Zone: "shop", MaterialsDrawn: 10, MaterialsBillTotal: 80, LeadTimeRemaining: 12, Status: StatusInProgress},
		BuildOrder{ID: 25, Zone: "office", MaterialsBillTotal: 150, LeadTimeRemaining: 60, Status: StatusMaterialsPending},
	)
	buf := renderBuf(60, 10, func(b *core.Buffer, r core.Rect) { RenderQueue(b, r, orders, true, style) })
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 60, H: 10})
	if !strings.Contains(rows[1], "#24 shop") || !strings.Contains(rows[2], "#25 office") {
		t.Fatalf("active orders not listed first: %q", rows)
	}
	if !strings.Contains(rows[3], "23 completed") {
		t.Fatalf("row 3 = %q, want the one completed line", rows[3])
	}
	for _, r := range rows {
		if strings.Contains(r, "#1 dwelling") || strings.Contains(r, "#23 dwelling") {
			t.Fatalf("a completed order got its own row: %q", rows)
		}
	}
}

func TestBUG1234_Queue_MoreLineWhenActiveOverflow(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	var orders []BuildOrder
	for i := uint64(1); i <= 10; i++ {
		orders = append(orders, BuildOrder{ID: i, Zone: "shop", LeadTimeRemaining: 5, Status: StatusInProgress})
	}
	orders = append(orders, BuildOrder{ID: 11, Zone: "shop", Status: StatusComplete}, BuildOrder{ID: 12, Zone: "shop", Status: StatusComplete})
	// H=6: header + 5 rows = 3 active + "+7 more" + "2 completed".
	buf := renderBuf(60, 6, func(b *core.Buffer, r core.Rect) { RenderQueue(b, r, orders, true, style) })
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: 60, H: 6})
	if !strings.Contains(rows[3], "#3 shop") || !strings.Contains(rows[4], "+7 more") || !strings.Contains(rows[5], "2 completed") {
		t.Fatalf("overflow layout wrong: %q", rows)
	}
}

// BUG-1234 part 3: rows fit the 40-column half width, badges never cut.
func TestBUG1234_RowsFitFortyColumns(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	const w = 40
	statuses := []BuildOrderStatus{StatusMaterialsPending, StatusLabourPending, StatusInProgress}
	var orders []BuildOrder
	for i, st := range statuses {
		orders = append(orders, BuildOrder{ID: uint64(100 + i), Zone: "manufacturing", MaterialsDrawn: 250, MaterialsBillTotal: 250, LabourRemaining: 80, LeadTimeRemaining: 90, Status: st})
	}
	buf := renderBuf(w, 8, func(b *core.Buffer, r core.Rect) { RenderQueue(b, r, orders, true, style) })
	rows := renderedText(buf, core.Rect{X: 0, Y: 0, W: w, H: 8})
	for i, st := range statuses {
		row := rows[1+i]
		if !strings.HasSuffix(row, "["+string(st)+"]") {
			t.Errorf("queue row cut or badge lost at %d columns: %q", w, row)
		}
	}

	var entries []CatalogueEntry
	for _, n := range [][3]string{
		{"Dwelling", "£15000.00", "8 homes, 0 jobs"},
		{"Heavy Industry", "£60000.00", "0 homes, 80 jobs"},
		{"Manufacturing", "£37500.00", "0 homes, 50 jobs"},
		{"Entertainment", "£30000.00", "0 homes, 15 jobs"},
	} {
		entries = append(entries, CatalogueEntry{ID: n[0], Name: n[0], Section: "zone", CostRaw: n[1], CapacityRaw: n[2], Unlock: UnlockUnlocked})
	}
	buf = renderBuf(w, 6, func(b *core.Buffer, r core.Rect) { RenderCatalogue(b, r, entries, true, style) })
	rows = renderedText(buf, core.Rect{X: 0, Y: 0, W: w, H: 6})
	for i := range entries {
		row := rows[1+i]
		if !strings.HasSuffix(row, "[unlocked]") || !strings.Contains(row, entries[i].Name) {
			t.Errorf("catalogue row cut at %d columns: %q", w, row)
		}
	}
	// One tier for the whole column: "Heavy Industry £60,000 80 jobs
	// [unlocked]" is 41 wide, so at 40 every row drops the commas together.
	if rows[1] != "Dwelling £15000 8 homes [unlocked]" || rows[2] != "Heavy Industry £60000 80 jobs [unlocked]" {
		t.Errorf("catalogue compact format changed: %q", rows[1:3])
	}
	buf = renderBuf(44, 6, func(b *core.Buffer, r core.Rect) { RenderCatalogue(b, r, entries, true, style) })
	if got := renderedText(buf, core.Rect{X: 0, Y: 0, W: 44, H: 6})[1]; got != "Dwelling £15,000 8 homes [unlocked]" {
		t.Errorf("at 44 columns the comma tier should hold: %q", got)
	}

	zones := []ZoneInfo{
		{Zone: "heavy_industry", Name: "Heavy Industry", Materials: 400, Labour: 120, BaseLeadTimeDays: 150},
		{Zone: "dwelling", Name: "Dwelling", Materials: 100, Labour: 40, BaseLeadTimeDays: 45},
	}
	buf = renderBuf(w, 4, func(b *core.Buffer, r core.Rect) { RenderZones(b, r, zones, true, style) })
	rows = renderedText(buf, core.Rect{X: 0, Y: 0, W: w, H: 4})
	for i, z := range zones {
		want := fmt.Sprintf("lead%dd", z.BaseLeadTimeDays)
		if !strings.HasSuffix(rows[1+i], want) && !strings.HasSuffix(rows[1+i], fmt.Sprintf("lead %dd", z.BaseLeadTimeDays)) {
			t.Errorf("zone row cut at %d columns: %q", w, rows[1+i])
		}
	}
}

// A very long name is shortened with an ellipsis, never the badge.
func TestBUG1234_LongNameShortenedBadgeKept(t *testing.T) {
	style := widgets.DefaultPalette.Style(widgets.TokenMoney)
	orders := []BuildOrder{{ID: 123, Zone: "an_extremely_long_zone_name_indeed", MaterialsDrawn: 250, MaterialsBillTotal: 250, LeadTimeRemaining: 90, Status: StatusMaterialsPending}}
	buf := renderBuf(40, 3, func(b *core.Buffer, r core.Rect) { RenderQueue(b, r, orders, true, style) })
	row := renderedText(buf, core.Rect{X: 0, Y: 0, W: 40, H: 3})[1]
	if !strings.HasSuffix(row, "[materials-pending]") || !strings.Contains(row, "…") {
		t.Errorf("long name row = %q", row)
	}
}
