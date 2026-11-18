package why

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// want80 is the exact panel for the golden sample city at 80 columns.
const want80 = `WHY PEOPLE COME OR GO   month 13
Housing          [#######---]   71 pulling        +3.1  rent share 29
Jobs             [..........]   -- not built yet   0.0
Services         [######----]   62 pulling        +2.2  coverage 62
Health care      [..........]   -- not built yet   0.0
Policy           [..........]   -- not built yet   0.0
Shops            [..........]   -- not built yet   0.0
Parks            [######----]   55 pulling        +1.5  entertainment venues 1
Transport links  [..........]   -- not built yet   0.0
Congestion       [..........]   -- not built yet   0.0
Tourism          [..........]   -- not built yet   0.0
Safety           [#####-----]   52 pulling        +1.2  safe 52
Environment      [#####-----]   46 pulling        +0.6  clean 46
Momentum / pride                +2 rising         +0.3  pride, big inertia
Wellbeing                          pushing        -1.0  city mood x0.98
TOTAL            47.9 vs 40.0      net gain       +7.9  people, before the cap
Wanted 8, room for 62 (limit: free homes), moved in 8
homelessness: not built yet`

// want60 is the same city at 60 columns: no why-text, bars kept.
const want60 = `WHY PEOPLE COME OR GO   month 13
Housing          [#######---]   71 pulling        +3.1
Jobs             [..........]   -- not built yet   0.0
Services         [######----]   62 pulling        +2.2
Health care      [..........]   -- not built yet   0.0
Policy           [..........]   -- not built yet   0.0
Shops            [..........]   -- not built yet   0.0
Parks            [######----]   55 pulling        +1.5
Transport links  [..........]   -- not built yet   0.0
Congestion       [..........]   -- not built yet   0.0
Tourism          [..........]   -- not built yet   0.0
Safety           [#####-----]   52 pulling        +1.2
Environment      [#####-----]   46 pulling        +0.6
Momentum / pride                +2 rising         +0.3
Wellbeing                          pushing        -1.0
TOTAL            47.9 vs 40.0      net gain       +7.9
Wanted 8, room for 62 (limit: free homes), moved in 8
homelessness: not built yet`

func sampleScreen(t *testing.T) *Screen {
	t.Helper()
	s := boundScreen()
	apply(s, fixture(t, "sample.json"))
	return s
}

func TestRender_Sample80x24_Exact(t *testing.T) {
	got := joined(nonEmpty(frame(sampleScreen(t), 80, 24)))
	if got != want80 {
		t.Fatalf("80x24 panel mismatch\n--- got\n%s\n--- want\n%s", got, want80)
	}
	if n := len(nonEmpty(frame(sampleScreen(t), 80, 20))); n != 18 {
		t.Fatalf("panel uses %d rows, want 18 (of the 20 content rows)", n)
	}
}

func TestRender_Sample60x24_Exact(t *testing.T) {
	got := joined(nonEmpty(frame(sampleScreen(t), 60, 24)))
	if got != want60 {
		t.Fatalf("60x24 panel mismatch\n--- got\n%s\n--- want\n%s", got, want60)
	}
	for _, line := range strings.Split(got, "\n") {
		if utf8.RuneCountInString(line) > 60 {
			t.Fatalf("line wider than 60: %q", line)
		}
	}
}

// The 60-column cut order: why-text first, then bars, then the long word.
// The name, the word and the signed points survive every cut.
func TestRender_CutOrderAsWidthFalls(t *testing.T) {
	s := sampleScreen(t)
	housing := func(w int) string { return frame(s, w, 24)[1] }

	cases := []struct {
		w                         int
		why, bar, longWord, title bool
	}{
		{80, true, true, true, true},
		{76, true, true, true, true},
		{75, false, true, true, true},
		{54, false, true, true, true},
		{53, false, false, true, true},
		{41, false, false, true, true},
		{40, false, false, false, true},
		{39, false, false, false, false},
	}
	for _, c := range cases {
		line := housing(c.w)
		if got := strings.Contains(line, "rent share"); got != c.why {
			t.Errorf("w=%d why-text present=%v want %v: %q", c.w, got, c.why, line)
		}
		if got := strings.Contains(line, "[#######---]"); got != c.bar {
			t.Errorf("w=%d bar present=%v want %v: %q", c.w, got, c.bar, line)
		}
		if got := strings.Contains(line, "pulling"); got != c.longWord {
			t.Errorf("w=%d long word present=%v want %v: %q", c.w, got, c.longWord, line)
		}
		for _, must := range []string{"Housing", "+3.1"} {
			if !strings.Contains(line, must) {
				t.Errorf("w=%d line lost %q: %q", c.w, must, line)
			}
		}
		if !strings.Contains(line, "pull") {
			t.Errorf("w=%d line lost its word: %q", c.w, line)
		}
		if got := strings.HasPrefix(frame(s, c.w, 24)[0], "WHY PEOPLE"); got != c.title {
			t.Errorf("w=%d long title present=%v want %v", c.w, got, c.title)
		}
	}
}

func TestRender_TinyAndDegenerateAreasNeverPanic(t *testing.T) {
	cases := [][2]int{{40, 10}, {10, 3}, {1, 1}, {0, 0}, {3, 1}, {200, 60}, {80, 1}, {1, 24}}
	patches := map[string]*Patch{}
	sp := samplePatch(t)
	patches["sample"] = &sp
	patches["nil"] = nil
	for _, c := range cases {
		for name, p := range patches {
			t.Run(fmt.Sprintf("%s_%dx%d", name, c[0], c[1]), func(t *testing.T) {
				lines := Lines(p, c[0], c[1])
				if len(lines) > c[1] && c[1] > 0 {
					t.Fatalf("%d lines for height %d", len(lines), c[1])
				}
				for _, l := range lines {
					if utf8.RuneCountInString(l) > c[0] {
						t.Fatalf("line wider than %d: %q", c[0], l)
					}
				}
				// Through the buffer too, including a rect offset past the edge.
				s := boundScreen()
				if p != nil {
					apply(s, fixture(t, "sample.json"))
				}
				_ = frame(s, c[0], c[1])
			})
		}
	}
}

func TestRender_40x10_KeepsTenLeversOrTotalFirst(t *testing.T) {
	rows := nonEmpty(frame(sampleScreen(t), 40, 10))
	if len(rows) != 10 {
		t.Fatalf("got %d rows, want 10", len(rows))
	}
	if !strings.HasPrefix(rows[len(rows)-1], "TOTAL") {
		t.Fatalf("TOTAL must survive a short terminal, last row = %q", rows[len(rows)-1])
	}
	if !strings.HasPrefix(rows[0], "Housing") {
		t.Fatalf("first row = %q", rows[0])
	}
}

func TestRender_10x3_NoNegativeRect(t *testing.T) {
	rows := nonEmpty(frame(sampleScreen(t), 10, 3))
	if len(rows) > 3 {
		t.Fatalf("rows = %d", len(rows))
	}
}

func TestRender_AllUnbuiltCity(t *testing.T) {
	p := samplePatch(t)
	for i := range p.Levers {
		p.Levers[i] = Lever{ID: LeverIDs[i], Built: false, Value: 99, Points: 9.9}
	}
	p.Modifiers = nil
	p.ReputationPoints, p.WellbeingPoints, p.Net, p.Score = 0, 0, 0, 40
	p.Wanted, p.Room, p.LimitedBy = 0, 0, ""
	s := boundScreen()
	apply(s, mustMarshal(t, p))
	rows := frame(s, 60, 24)
	got := joined(rows)
	if n := strings.Count(got, "not built yet"); n != 13 { // 10 levers + 2 modifiers + homelessness
		t.Fatalf("not built yet appears %d times, want 13\n%s", n, got)
	}
	if strings.Contains(got, "99") || strings.Contains(got, "9.9") {
		t.Fatalf("an unbuilt lever's value leaked onto the screen\n%s", got)
	}
	for _, r := range rows[1:13] {
		if !strings.HasSuffix(r, "0.0") {
			t.Fatalf("unbuilt line must end in points 0.0: %q", r)
		}
	}
	total := rowStartingWith(t, rows, "TOTAL")
	if !strings.Contains(total, "balanced") || !strings.HasSuffix(total, "0.0") {
		t.Fatalf("TOTAL = %q", total)
	}
}

func TestRender_TransportLimitsArrivals(t *testing.T) {
	p := samplePatch(t)
	p.LimitedBy, p.Room = LimitTransport, 4
	s := boundScreen()
	apply(s, mustMarshal(t, p))
	got := joined(frame(s, 80, 24))
	if !strings.Contains(got, "Wanted 8, room for 4 (limit: transport links), moved in 8") {
		t.Fatalf("transport limit not shown\n%s", got)
	}
	if strings.Contains(got, "free homes") {
		t.Fatalf("homes named as the limit while transport limits\n%s", got)
	}
	short := joined(frame(s, 50, 24))
	if !strings.Contains(short, "(transport)") {
		t.Fatalf("compact arrivals line lost the limit\n%s", short)
	}
}

func TestRender_NegativePointsAndLeavers(t *testing.T) {
	p := samplePatch(t)
	p.Levers[0].Points, p.Levers[0].Value = -2.5, 15
	p.Levers[2].Points, p.Levers[2].Value = -0.04, 39.6
	p.Levers[6].Points, p.Levers[6].Value = -1.0, 30
	p.Modifiers[0].Points, p.Modifiers[0].Value = -3.0, 10
	p.Modifiers[1].Points, p.Modifiers[1].Value = 0.6, 46
	p.ReputationPoints, p.Reputation, p.ReputationTrend = -0.4, -3.2, ""
	p.WellbeingPoints = -1.0
	p.Net, p.Score = -6.34, 33.66
	p.Wanted, p.Room, p.LimitedBy = -6, 62, LimitHomes
	left := int64(5)
	zero := int64(0)
	p.MovedIn, p.Left = &zero, &left
	s := boundScreen()
	apply(s, mustMarshal(t, p))
	rows := frame(s, 80, 24)
	got := joined(rows)
	for _, want := range []string{"pushing", "-2.5", "Wanted to leave 6, left 5", "falling", "net loss"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "-0.0") {
		t.Fatalf("negative zero printed\n%s", got)
	}
	if strings.Contains(got, "room for") {
		t.Fatalf("room clause shown for a city people are leaving\n%s", got)
	}
	// The -0.04 lever rounds to 0.0 and must read neutral, not pushing.
	if l := rowStartingWith(t, rows, "Services"); !strings.Contains(l, "neutral") {
		t.Fatalf("services line = %q", l)
	}
}

func TestRender_NoDataBeforeAnyPatch(t *testing.T) {
	s := boundScreen()
	rows := nonEmpty(frame(s, 80, 24))
	got := joined(rows)
	if !strings.HasPrefix(rows[0], "WHY PEOPLE COME OR GO") {
		t.Fatalf("title = %q", rows[0])
	}
	if !strings.Contains(got, "no month closed yet") {
		t.Fatalf("no honest no-data line\n%s", got)
	}
	for i := 0; i < LeverCount; i++ {
		if strings.Count(got, LeverNames[i]) != 1 {
			t.Fatalf("lever %q should appear exactly once\n%s", LeverNames[i], got)
		}
	}
	for _, n := range ModifierNames {
		if !strings.Contains(got, n) {
			t.Fatalf("modifier %q missing\n%s", n, got)
		}
	}
	if strings.Contains(got, "pulling") || strings.Contains(got, "TOTAL") || strings.Contains(got, "+") {
		t.Fatalf("figures invented in the no-data state\n%s", got)
	}
	if s.HaveData() {
		t.Fatal("HaveData true with nothing received")
	}
}

func TestRender_KnownFalseAfterGoodPatchResetsToNoData(t *testing.T) {
	s := sampleScreen(t)
	apply(s, fixture(t, "nodata.json"))
	got := joined(frame(s, 80, 24))
	if !strings.Contains(got, "no month closed yet") || strings.Contains(got, "pulling") {
		t.Fatalf("known=false must clear the figures\n%s", got)
	}
	if !s.HaveData() {
		t.Fatal("a known=false patch is still data received")
	}
	if _, ok := s.Snapshot(); ok {
		t.Fatal("Snapshot ok for a known=false patch")
	}
}

func TestRender_HomelessSlot(t *testing.T) {
	p := samplePatch(t)
	p.Homeless = &Homeless{Built: true, Count: 12}
	s := boundScreen()
	apply(s, mustMarshal(t, p))
	if got := joined(frame(s, 80, 24)); !strings.Contains(got, "homelessness: 12 people") {
		t.Fatalf("built homelessness slot not shown\n%s", got)
	}
}

func TestRender_Deterministic(t *testing.T) {
	a := joined(frame(sampleScreen(t), 80, 24))
	b := joined(frame(sampleScreen(t), 80, 24))
	if a != b {
		t.Fatal("same input rendered differently")
	}
}

func TestFormatting_FixedAndLocaleFree(t *testing.T) {
	cases := []struct {
		t      int64
		signed bool
		want   string
	}{{0, true, "0.0"}, {1, true, "+0.1"}, {-1, true, "-0.1"}, {79, false, "7.9"}, {-1234, true, "-123.4"}, {5, false, "0.5"}}
	for _, c := range cases {
		if got := fmtTenths(c.t, c.signed); got != c.want {
			t.Errorf("fmtTenths(%d,%v)=%q want %q", c.t, c.signed, got, c.want)
		}
	}
	if roundTenths(-0.04) != 0 || roundTenths(0.05) != 1 || roundTenths(-0.05) != -1 {
		t.Error("roundTenths halves must go away from zero and small negatives to zero")
	}
	if barFor(0) != "[----------]" || barFor(100) != "[##########]" || barFor(54.9) != "[#####-----]" || barFor(55) != "[######----]" {
		t.Error("bar rounding wrong")
	}
	if barFor(-5) != "[----------]" || barFor(500) != "[##########]" {
		t.Error("bar must clamp")
	}
}

// --- the arithmetic: parse the rendered text back -------------------------

func rowStartingWith(t *testing.T, rows []string, prefix string) string {
	t.Helper()
	for _, r := range rows {
		if strings.HasPrefix(r, prefix) {
			return r
		}
	}
	t.Fatalf("no row starts with %q in\n%s", prefix, joined(rows))
	return ""
}

// parseTenths parses "+3.1", "-1.0", "0.0" into tenths.
func parseTenths(t *testing.T, s string) int64 {
	t.Helper()
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	whole, frac, ok := strings.Cut(s, ".")
	if !ok || len(frac) != 1 {
		t.Fatalf("not a one-decimal number: %q", s)
	}
	w, err1 := strconv.ParseInt(whole, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil {
		t.Fatalf("bad number %q", s)
	}
	v := w*10 + f
	if neg {
		v = -v
	}
	return v
}

// lastField is the signed-points column at 60 columns (no why-text there).
func lastField(row string) string {
	f := strings.Fields(row)
	return f[len(f)-1]
}

// onScreenSum parses a rendered 60-column frame back: the ten levers, the two
// modifiers, momentum and wellbeing against TOTAL.
func onScreenSum(t *testing.T, rows []string) (sum, total int64) {
	t.Helper()
	prefixes := []string{}
	prefixes = append(prefixes, LeverNames[:]...)
	prefixes = append(prefixes, ModifierNames[:]...)
	prefixes = append(prefixes, "Momentum", "Wellbeing")
	for _, p := range prefixes {
		sum += parseTenths(t, lastField(rowStartingWith(t, rows, p)))
	}
	total = parseTenths(t, lastField(rowStartingWith(t, rows, "TOTAL")))
	return sum, total
}

func TestArithmetic_ParseBack_Golden(t *testing.T) {
	sum, total := onScreenSum(t, frame(sampleScreen(t), 60, 24))
	if sum != total || total != 79 {
		t.Fatalf("on-screen points sum to %d tenths, TOTAL shows %d (want 79)", sum, total)
	}
}

// Raw points that do NOT round to the net (3.14 + 2.24 + ... each rounds down)
// must still add up on screen when the patch itself adds up.
func TestArithmetic_ParseBack_RoundingNudge(t *testing.T) {
	p := samplePatch(t)
	pts := []float64{3.14, 2.24, 1.54, 1.24, 0.64}
	p.Levers[0].Points, p.Levers[2].Points, p.Levers[6].Points = pts[0], pts[1], pts[2]
	p.Modifiers[0].Points, p.Modifiers[1].Points = pts[3], pts[4]
	p.ReputationPoints, p.WellbeingPoints = 0.34, -1.04
	// 3.14+2.24+1.54+1.24+0.64+0.34-1.04 = 8.14; rounded lines sum to 8.1 + ... check below
	p.Net = 8.14
	p.Score = 48.14
	s := boundScreen()
	apply(s, mustMarshal(t, p))
	rows := frame(s, 60, 24)
	sum, total := onScreenSum(t, rows)
	if sum != total {
		t.Fatalf("on-screen points sum to %d tenths, TOTAL shows %d\n%s", sum, total, joined(rows))
	}
	if total != 81 {
		t.Fatalf("TOTAL = %d tenths, want 81", total)
	}
}

// A patch whose own numbers do not add up must show the mismatch, not hide it.
func TestArithmetic_InconsistentPatchIsNotFudged(t *testing.T) {
	p := samplePatch(t)
	p.Net = 12.0 // lines still sum to 7.9
	s := boundScreen()
	apply(s, mustMarshal(t, p))
	sum, total := onScreenSum(t, frame(s, 60, 24))
	if sum == total || total != 120 || sum != 79 {
		t.Fatalf("sum=%d total=%d: the screen must print raw figures when the patch is inconsistent", sum, total)
	}
}

// 300 deterministic consistent patches: the printed lines always add up.
func TestArithmetic_ParseBack_Sweep(t *testing.T) {
	seed := uint64(0x9E3779B97F4A7C15)
	next := func() float64 { // xorshift, deterministic
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return float64(seed%20001)/1000 - 10 // -10.000 .. +10.000
	}
	for n := 0; n < 300; n++ {
		p := samplePatch(t)
		total := 0.0
		for i := range p.Levers {
			p.Levers[i].Built = n%7 != i%7
			p.Levers[i].Value = 50
			p.Levers[i].Points = 0
			if p.Levers[i].Built {
				p.Levers[i].Points = next() / 3
				total += p.Levers[i].Points
			}
		}
		for i := range p.Modifiers {
			p.Modifiers[i].Points = next() / 3
			total += p.Modifiers[i].Points
		}
		p.ReputationPoints, p.WellbeingPoints = next()/3, next()/3
		total += p.ReputationPoints + p.WellbeingPoints
		p.Net, p.Score = total, 40+total
		s := boundScreen()
		apply(s, mustMarshal(t, p))
		rows := frame(s, 60, 24)
		sum, tot := onScreenSum(t, rows)
		if sum != tot {
			t.Fatalf("case %d: on-screen points sum to %d tenths, TOTAL shows %d\n%s", n, sum, tot, joined(rows))
		}
		if d := float64(tot)/10 - total; d > 0.05+1e-9 || d < -0.05-1e-9 {
			t.Fatalf("case %d: TOTAL %v differs from the true net %v by more than half a tenth", n, float64(tot)/10, total)
		}
	}
}

func TestGolden_RoundTripsThroughTheContractType(t *testing.T) {
	p := samplePatch(t)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var again Patch
	if err := json.Unmarshal(b, &again); err != nil {
		t.Fatal(err)
	}
	if len(again.Levers) != LeverCount || len(again.Modifiers) != ModifierCount {
		t.Fatalf("lost lines in the round trip: %d levers, %d modifiers", len(again.Levers), len(again.Modifiers))
	}
	for i, l := range again.Levers {
		if l.ID != LeverIDs[i] {
			t.Fatalf("golden lever %d id %q, want %q", i, l.ID, LeverIDs[i])
		}
	}
}
