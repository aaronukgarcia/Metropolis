package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	buildscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/build"
	financescreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/finance"
)

func rrPtr(v int64) *int64 { return &v }

func rrF2Frame(scale int64) string {
	w, h := 120, 14
	buf := core.NewBuffer(w, h)
	k := int64(1000) * scale
	pl := financescreen.PLView{
		Period:      "Month 2 vs Month 1",
		Revenues:    []financescreen.PLItem{{Label: "Income tax", ValueMicropounds: 42_000 * k, PrevValueMicropounds: rrPtr(0)}, {Label: "Sales tax", ValueMicropounds: 374 * k, PrevValueMicropounds: rrPtr(0)}},
		Expenses:    []financescreen.PLItem{{Label: "Public sector wages", ValueMicropounds: 16_800 * k, PrevValueMicropounds: rrPtr(0)}, {Label: "Coastal processing", ValueMicropounds: 250_000 * k, PrevValueMicropounds: rrPtr(250_000 * k)}},
		Other:       &financescreen.PLItem{Label: "Other treasury movements", ValueMicropounds: -1_000 * k, PrevValueMicropounds: rrPtr(1_000 * k)},
		Net:         &financescreen.PLItem{Label: "Net treasury change", ValueMicropounds: -530_161 * k, PrevValueMicropounds: rrPtr(948_500 * k)},
		CityEconomy: []financescreen.PLItem{{Label: "Household consumption", ValueMicropounds: 1_870 * k, PrevValueMicropounds: rrPtr(0)}},
	}
	bs := financescreen.BalanceSheetView{
		Assets:      []financescreen.BalanceItem{{Label: "Treasury", ValueMicropounds: 123_485_323 * k}, {Label: "Reserves", ValueMicropounds: 0}},
		Liabilities: []financescreen.BalanceItem{{Label: "Outstanding Debt", ValueMicropounds: 5 * k}},
		NetWorth:    123_485_318 * k,
	}
	st := tcell.StyleDefault
	financescreen.RenderPL(buf, core.Rect{X: 0, Y: 0, W: 60, H: 13}, pl, true, st)
	financescreen.RenderBalanceSheet(buf, core.Rect{X: 60, Y: 0, W: 60, H: 13}, bs, true, st)
	var sb strings.Builder
	for y := 0; y < h; y++ {
		sb.WriteString(strings.TrimRight(bufferRowString(buf, w, y), " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestRoundR1Money_F2LayoutBigFigures(t *testing.T) {
	dir := os.Getenv("ROUND_OUT")
	for name, scale := range map[string]int64{"nine": 1, "twelve": 1000} {
		f := rrF2Frame(scale)
		if dir != "" {
			_ = os.WriteFile(filepath.Join(dir, "f2_"+name+".txt"), []byte(f), 0o644)
		}
		t.Logf("%s:\n%s", name, f)
		for _, ln := range strings.Split(f, "\n") {
			if utf8.RuneCountInString(ln) > 120 {
				t.Errorf("%s: line wider than 120: %q", name, ln)
			}
		}
	}
}

func TestRoundR1Money_F3HalfWidth(t *testing.T) {
	buf := core.NewBuffer(60, 8)
	es := []buildscreen.CatalogueEntry{
		{ID: "a", Name: "Heavy Industry", Section: "I", CostRaw: "£60000.00", CapacityRaw: "0 homes, 80 jobs", Unlock: buildscreen.UnlockUnlocked},
		{ID: "b", Name: "Dwelling", Section: "R", CostRaw: "£123485323000.00", CapacityRaw: "8 homes, 0 jobs", Unlock: buildscreen.UnlockUnlocked},
	}
	buildscreen.RenderCatalogue(buf, core.Rect{X: 0, Y: 0, W: 60, H: 8}, es, true, tcell.StyleDefault)
	for y := 0; y < 4; y++ {
		t.Logf("|%s|", bufferRowString(buf, 60, y))
	}
}
