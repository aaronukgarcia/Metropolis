package why

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func rr1Mut(t *testing.T, f func(p *Patch)) json.RawMessage {
	p := samplePatch(t)
	f(&p)
	return mustMarshal(t, p)
}

func rr1Dump(t *testing.T, name string, rows []string) {
	t.Logf("== %s\n%s", name, joined(nonEmpty(rows)))
}

func TestRoundR1_Sizes(t *testing.T) {
	s := boundScreen()
	apply(s, fixture(t, "sample.json"))
	for _, d := range [][2]int{{0, 0}, {1, 1}, {79, 23}, {40, 5}, {60, 10}, {80, 24}, {200, 100}} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("panic at %v: %v", d, r)
				}
			}()
			rows := frame(s, max(d[0], 1), max(d[1], 1))
			for _, r := range rows {
				if len([]rune(r)) > d[0] {
					t.Errorf("row wider than %d", d[0])
				}
			}
		}()
	}
	// offset rect in larger buffer: nothing outside rect
	buf := core.NewBuffer(100, 40)
	s.Render(buf, core.Rect{X: 10, Y: 5, W: 60, H: 12})
	for y := 0; y < 40; y++ {
		for x := 0; x < 100; x++ {
			if (x < 10 || x >= 70 || y < 5 || y >= 17) && buf.Get(x, y).Rune != 0 && buf.Get(x, y).Rune != ' ' {
				t.Fatalf("draw outside rect at %d,%d", x, y)
			}
		}
	}
	RenderPatch(buf, core.Rect{W: 80, H: 24}, nil)
}

func TestRoundR1_Bad(t *testing.T) {
	cases := map[string]func(p *Patch){
		"eleven levers": func(p *Patch) { p.Levers = append(p.Levers, p.Levers[0]) },
		"nine levers":   func(p *Patch) { p.Levers = p.Levers[:9] },
		"nil levers":    func(p *Patch) { p.Levers = nil },
		"neg value":     func(p *Patch) { p.Levers[0].Value = -1 },
		"big value":     func(p *Patch) { p.Levers[0].Value = 1e9 },
		"big points":    func(p *Patch) { p.Levers[0].Points = 1e9 },
		"neg month":     func(p *Patch) { p.Month = -1 },
		"neg room":      func(p *Patch) { p.Room = -5 },
		"badlimit":      func(p *Patch) { p.LimitedBy = "x" },
		"wrong id":      func(p *Patch) { p.Levers[0].ID = "jobs" },
		"schema2":       func(p *Patch) { p.SchemaVersion = 2 },
	}
	for name, f := range cases {
		s := boundScreen()
		apply(s, fixture(t, "sample.json"))
		before := joined(frame(s, 80, 24))
		apply(s, rr1Mut(t, f))
		if after := joined(frame(s, 80, 24)); after != before {
			t.Errorf("%s: patch accepted/changed frame", name)
		}
	}
	// raw junk
	for _, raw := range []string{"", "null", "[]", "{", `{"schemaVersion":1,"known":true}`, `{"schemaVersion":1,"known":true,"levers":[]}`, "NaN"} {
		s := boundScreen()
		apply(s, json.RawMessage(raw))
		_ = frame(s, 80, 24)
	}
}

func TestRegression_FEAT9995385616_HugeValuesRefused(t *testing.T) {
	big := 1e300
	for name, f := range map[string]func(p *Patch){
		"net 1e300":  func(p *Patch) { p.Net = big },
		"net -1e300": func(p *Patch) { p.Net = -big },
		"rep pts":    func(p *Patch) { p.ReputationPoints = big },
		"well pts":   func(p *Patch) { p.WellbeingPoints = -big },
		"score":      func(p *Patch) { p.Score = big },
		"wellfactor": func(p *Patch) { p.WellbeingFactor = -big },
		"wanted":     func(p *Patch) { p.Wanted = math.MinInt64; p.Room = math.MaxInt64 },
		"moved":      func(p *Patch) { v := int64(math.MinInt64); w := int64(math.MaxInt64); p.MovedIn = &v; p.Left = &w },
		"part":       func(p *Patch) { p.Levers[0].Parts = []Part{{K: strings.Repeat("é", 500), V: big}, {K: "", V: -big}} },
		"aworld":     func(p *Patch) { v := -big; p.AWorld = &v },
		"homeless":   func(p *Patch) { p.Homeless = &Homeless{Built: true, Count: math.MinInt64} },
		"unbuilt":    func(p *Patch) { p.Levers[0].Built = false; p.Levers[0].Value = 5000; p.Levers[0].Points = 9999 },
	} {
		s := boundScreen()
		apply(s, rr1Mut(t, f))
		if name == "unbuilt" {
			// values of a lever that is not built are never shown: accepted, harmless.
			if !s.HaveData() {
				t.Errorf("unbuilt: an unbuilt lever's values should not refuse the patch")
			}
		} else {
			if s.HaveData() {
				t.Errorf("%s: absurd patch was accepted, want it refused whole", name)
			}
			continue
		}
		for _, d := range [][2]int{{80, 24}, {60, 24}} {
			rows := frame(s, d[0], d[1])
			j := joined(rows)
			if strings.Contains(j, "--9") || strings.Contains(j, "9223372036854775808") || strings.Contains(j, "NaN") || strings.Contains(j, "Inf") {
				t.Errorf("%s %v: suspicious number\n%s", name, d, j)
			}
			if d[0] == 80 {
				rr1Dump(t, name, rows)
			}
		}
	}
}

func TestRoundR1_StaleOrder(t *testing.T) {
	s := boundScreen()
	apply(s, rr1Mut(t, func(p *Patch) { p.Month = 9 }))
	apply(s, rr1Mut(t, func(p *Patch) { p.Month = 3 }))
	snap, _ := s.Snapshot()
	t.Logf("month after 9 then 3 = %d", snap.Month)
}
