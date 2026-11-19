package finance

// BUG-1222: the P&L pane is one quadrant of the F2 grid, so it is short (11
// rows at 80x24, 10 once a second chrome row lands). When it is short the lines
// that prove Baseline One leg 3 must survive: the period label, the Net
// treasury change and the City economy Household consumption.

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func plPtr(v int64) *int64 { return &v }

func plPounds(p int64) int64 { return p * 1000 } // micropounds

// plRealMonth is the row set of the real month-3 frame (5 income, 7 outflow, one
// uncategorised residue), Net = income - outflows + residue.
func plRealMonth() PLView {
	item := func(label string, cur, prev int64) PLItem {
		return PLItem{Label: label, ValueMicropounds: plPounds(cur), PrevValueMicropounds: plPtr(plPounds(prev))}
	}
	pl := PLView{
		Period: "Month 3 vs Month 2",
		Revenues: []PLItem{
			item("Income tax", 42000, 40000),
			item("Sales tax", 352, 300),
			item("Corporation tax", 440, 400),
			item("Council tax", 1504, 1500),
			item("Opening capital (one-off)", 0, 1500000),
		},
		Expenses: []PLItem{
			item("Public sector wages", 16800, 16000),
			item("Benefits", 3000, 3000),
			item("Social support", 3000, 3000),
			item("Coastal processing", 250000, 250000),
			item("Coastal integration", 300000, 300000),
			item("Maintenance", 1200, 1100),
			item("Interest", 90, 80),
		},
		CityEconomy: []PLItem{
			item("Household consumption", 2090, 1760),
			item("Wages paid (all employers)", 61000, 59000),
		},
	}
	other := item("Other treasury movements", -75, 20)
	pl.Other = &other
	var net int64
	for _, r := range pl.Revenues {
		net += r.ValueMicropounds
	}
	for _, e := range pl.Expenses {
		net -= e.ValueMicropounds
	}
	net += pl.Other.ValueMicropounds
	n := PLItem{Label: "Net treasury change", ValueMicropounds: net, PrevValueMicropounds: plPtr(plPounds(-1))}
	pl.Net = &n
	return pl
}

func plRender(pl PLView, w, h int) []string {
	buf := core.NewBuffer(w, h)
	RenderPL(buf, core.Rect{X: 0, Y: 0, W: w, H: h}, pl, true, tcell.StyleDefault)
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		r := make([]rune, w)
		for x := 0; x < w; x++ {
			c := buf.Get(x, y).Rune
			if c == 0 {
				c = ' '
			}
			r[x] = c
		}
		rows[y] = strings.TrimRight(string(r), " ")
	}
	return rows
}

// plAmountRe matches one whole-pound figure with thousands commas: "-£529,869"
// (FEAT-9995385625: no pence on screen).
var plAmountRe = regexp.MustCompile(`(-?)£([0-9][0-9,]*)`)

// plFirstAmount reads the first figure on a line back as whole pounds.
func plFirstAmount(t *testing.T, line string) int64 {
	t.Helper()
	m := plAmountRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("no amount on line %q", line)
	}
	v, err := strconv.ParseInt(strings.ReplaceAll(m[2], ",", ""), 10, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", m[2], err)
	}
	if m[1] == "-" {
		v = -v
	}
	return v
}

// A residue carries an explicit sign, except a figure that rounds to zero, which
// is a bare "£0" (FEAT-9995385625 r2).
var plSignedRe = regexp.MustCompile(`([+-]?)£([0-9][0-9,]*)`)

// plParsed is a rendered P&L read back into numbers (pence).
type plParsed struct {
	incomeSum, outflowSum, residue int64 // incomeSum/outflowSum include the folds
	net                            int64
	haveNet, haveCons, haveWages   bool
	haveResidue                    bool
	incomeFold, outflowFold        string // the fold lines, verbatim
	bodyLines                      int    // income, outflow, fold and residue lines
	residueBeforeNet               bool
}

func plParse(t *testing.T, rows []string) plParsed {
	t.Helper()
	var p plParsed
	section := ""
	for _, ln := range rows {
		trim := strings.TrimSpace(ln)
		switch {
		case trim == "":
		case strings.HasPrefix(trim, "Treasury income:"):
			section = "in"
		case strings.HasPrefix(trim, "Treasury outflows:"):
			section = "out"
		case strings.HasPrefix(trim, "Net t"):
			p.net = plFirstAmount(t, ln)
			p.haveNet = true
			section = "net"
		case strings.HasPrefix(trim, "City economy"):
			section = "city"
		case strings.HasPrefix(trim, "Hous"):
			p.haveCons = true
		case strings.HasPrefix(trim, "Wages paid"):
			p.haveWages = true
		case strings.HasPrefix(trim, "Other t"):
			m := plSignedRe.FindStringSubmatch(ln)
			if m == nil || (m[1] == "" && m[2] != "0") {
				t.Fatalf("residue row has no explicit sign: %q", ln)
			}
			v, _ := strconv.ParseInt(strings.ReplaceAll(m[2], ",", ""), 10, 64)
			if m[1] == "-" {
				v = -v
			}
			p.residue = v
			p.haveResidue = true
			p.bodyLines++
			p.residueBeforeNet = true
		case section == "in" || section == "out":
			if !plAmountRe.MatchString(ln) {
				t.Fatalf("body line without an amount: %q", ln)
			}
			v := plFirstAmount(t, ln)
			p.bodyLines++
			if strings.HasPrefix(trim, "...") {
				if section == "in" {
					p.incomeFold = trim
				} else {
					p.outflowFold = trim
				}
			}
			if section == "in" {
				p.incomeSum += v
			} else {
				p.outflowSum += v
			}
		}
	}
	return p
}

// reconciles reports sum(income + fold) - sum(outflows + fold) + residue == Net.
func (p plParsed) reconciles() bool {
	return p.incomeSum-p.outflowSum+p.residue == p.net
}

func plSumVisible(t *testing.T, rows []string) (sum, net int64, foldSeen bool) {
	t.Helper()
	p := plParse(t, rows)
	return p.incomeSum - p.outflowSum + p.residue, p.net, p.incomeFold != "" || p.outflowFold != ""
}

func TestBUG1222_PLKeepsNetAndConsumptionAtShortHeights(t *testing.T) {
	pl := plRealMonth()
	// Pane heights the 2x2 grid hands the P&L at 22 and 20 content rows.
	for _, h := range []int{11, 10} {
		rows := plRender(pl, 40, h)
		frame := strings.Join(rows, "\n")
		if !strings.Contains(rows[0], "Month 3 vs Month 2") {
			t.Fatalf("h=%d: period label missing from heading %q", h, rows[0])
		}
		if !strings.Contains(frame, "Net treasury change") {
			t.Fatalf("h=%d: Net line missing:\n%s", h, frame)
		}
		if !strings.Contains(frame, "Household consumption") {
			t.Fatalf("h=%d: Household consumption line missing:\n%s", h, frame)
		}
		if !strings.Contains(frame, "City economy (not treasury money):") {
			t.Fatalf("h=%d: City economy heading missing:\n%s", h, frame)
		}
		// The figure is not clipped: Net's amount is on its line.
		for _, ln := range rows {
			if strings.Contains(ln, "Net treasury change") && !plAmountRe.MatchString(ln) {
				t.Fatalf("h=%d: Net amount clipped: %q", h, ln)
			}
			if strings.Contains(ln, "Household consumption") && !plAmountRe.MatchString(ln) {
				t.Fatalf("h=%d: consumption amount clipped: %q", h, ln)
			}
		}
		if strings.Contains(frame, "Benefits") && !strings.Contains(frame, "Coastal integration") {
			t.Fatalf("h=%d: a small row survived while the largest was dropped:\n%s", h, frame)
		}
		sum, net, fold := plSumVisible(t, rows)
		if !fold {
			t.Fatalf("h=%d: twelve rows cannot all fit, expected a folded line:\n%s", h, frame)
		}
		if sum != net {
			t.Fatalf("h=%d: visible rows plus folded line = %d pence, Net line = %d:\n%s", h, sum, net, frame)
		}
	}
}

func TestBUG1222_PLRoomyPaneShowsEverythingWithoutFolding(t *testing.T) {
	pl := plRealMonth()
	rows := plRender(pl, 80, 30)
	frame := strings.Join(rows, "\n")
	for _, want := range []string{"Opening capital (one-off)", "Interest", "Wages paid (all employers)", "Net treasury change", "Household consumption"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("roomy pane lost %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "more income rows") || strings.Contains(frame, "more outflow rows") {
		t.Fatalf("roomy pane folded rows:\n%s", frame)
	}
	// Wide pane keeps the previous-month column.
	if !strings.Contains(frame, "£1,500,000") {
		t.Fatalf("wide pane dropped the previous-month column:\n%s", frame)
	}
	sum, net, _ := plSumVisible(t, rows)
	if sum != net {
		t.Fatalf("roomy rows sum %d != net %d", sum, net)
	}
}

func TestBUG1222_PLNarrowPaneDropsPreviousMonthColumn(t *testing.T) {
	rows := plRender(plRealMonth(), 40, 30)
	frame := strings.Join(rows, "\n")
	if strings.Contains(frame, "£1,500,000") {
		t.Fatalf("40-column pane still shows a previous-month column:\n%s", frame)
	}
	for _, ln := range rows {
		if strings.Contains(ln, "Income tax") && !plAmountRe.MatchString(ln) {
			t.Fatalf("amount clipped at 40 columns: %q", ln)
		}
	}
}

func TestBUG1222_PLTinyPanesNeverPanicAndKeepNet(t *testing.T) {
	pl := plRealMonth()
	for _, h := range []int{14, 8, 6, 5, 4, 3, 2, 1} {
		rows := plRender(pl, 40, h)
		frame := strings.Join(rows, "\n")
		if h >= 2 && !strings.Contains(frame, "Net treasury change") {
			t.Fatalf("h=%d: Net missing:\n%s", h, frame)
		}
		if h >= 3 && !strings.Contains(frame, "Household consumption") {
			t.Fatalf("h=%d: Household consumption missing:\n%s", h, frame)
		}
		if h >= 8 {
			sum, net, _ := plSumVisible(t, rows)
			if sum != net {
				t.Fatalf("h=%d: visible rows sum %d != Net %d:\n%s", h, sum, net, frame)
			}
		}
	}
	// Narrow and tiny at once.
	for _, w := range []int{1, 8, 20, 30} {
		for _, h := range []int{1, 3, 8} {
			_ = plRender(pl, w, h)
		}
	}
}

// plStatement builds a consistent statement: Net = income - outflows + residue.
func plStatement(income, outflows []int64, residue *int64) PLView {
	pl := PLView{Period: "Month 3 vs Month 2"}
	var net int64
	for i, v := range income {
		pl.Revenues = append(pl.Revenues, PLItem{Label: "Inc " + strconv.Itoa(i), ValueMicropounds: plPounds(v)})
		net += plPounds(v)
	}
	for i, v := range outflows {
		pl.Expenses = append(pl.Expenses, PLItem{Label: "Out " + strconv.Itoa(i), ValueMicropounds: plPounds(v)})
		net -= plPounds(v)
	}
	if residue != nil {
		o := PLItem{Label: "Other treasury movements", ValueMicropounds: *residue}
		pl.Other = &o
		net += *residue
	}
	pl.Net = &PLItem{Label: "Net treasury change", ValueMicropounds: net}
	pl.CityEconomy = []PLItem{
		{Label: "Household consumption", ValueMicropounds: plPounds(7)},
		{Label: "Wages paid (all employers)", ValueMicropounds: plPounds(9)},
	}
	return pl
}

func TestBUG1222_FoldWordingSignAndPlacement(t *testing.T) {
	neg := plPounds(-80)
	// 5 income rows (100..500) and 5 outflow rows (10..50), residue -0.075.
	pl := plStatement([]int64{100, 200, 300, 400, 500}, []int64{10, 20, 30, 40, 50}, &neg)
	// h=9: 8 lines = Net, consumption, then the reconciling set of 5 (two
	// headings, two folds, residue) and one spare line for the City heading.
	rows := plRender(pl, 40, 9)
	frame := strings.Join(rows, "\n")
	var incFold, outFold, inHdr, outHdr, resid, net = -1, -1, -1, -1, -1, -1
	for i, ln := range rows {
		switch {
		case strings.Contains(ln, "... 5 more income rows"):
			incFold = i
		case strings.Contains(ln, "... 5 more outflow rows"):
			outFold = i
		case strings.HasPrefix(ln, "Treasury income:"):
			inHdr = i
		case strings.HasPrefix(ln, "Treasury outflows:"):
			outHdr = i
		case strings.Contains(ln, "Other treasury movements"):
			resid = i
		case strings.Contains(ln, "Net treasury change"):
			net = i
		}
	}
	if incFold < 0 || outFold < 0 {
		t.Fatalf("both folds expected:\n%s", frame)
	}
	if inHdr >= incFold || incFold >= outHdr || outHdr >= outFold || outFold >= resid || resid+1 != net {
		t.Fatalf("fold under the wrong heading or residue not directly above Net (in %d/%d out %d/%d resid %d net %d):\n%s", inHdr, incFold, outHdr, outFold, resid, net, frame)
	}
	if !strings.Contains(rows[incFold], "£1,500") || strings.Contains(rows[incFold], "-") {
		t.Fatalf("income fold must be a positive 1,500 total: %q", rows[incFold])
	}
	if !strings.Contains(rows[outFold], "£150") || strings.Contains(rows[outFold], "-") {
		t.Fatalf("outflow fold must be a positive 150 total: %q", rows[outFold])
	}
	if !strings.Contains(rows[resid], "-£80") {
		t.Fatalf("negative residue needs an explicit minus: %q", rows[resid])
	}
	pos := plPounds(40)
	pl = plStatement([]int64{100, 200, 300}, []int64{10, 20, 30}, &pos)
	rows = plRender(pl, 40, 8)
	frame = strings.Join(rows, "\n")
	for _, ln := range rows {
		if strings.Contains(ln, "Other treasury movements") && !strings.Contains(ln, "+£40") {
			t.Fatalf("positive residue needs an explicit plus: %q\n%s", ln, frame)
		}
	}
	p := plParse(t, rows)
	if !p.reconciles() {
		t.Fatalf("does not reconcile:\n%s", frame)
	}
}

func TestBUG1222_FoldOfOneRowShowsTheRow(t *testing.T) {
	// 1 income + 1 outflow row: the single hidden row per side shows as itself, not as a "1 more" fold.
	zero := int64(0)
	pl := plStatement([]int64{100}, []int64{10}, &zero)
	rows := plRender(pl, 40, 8)
	frame := strings.Join(rows, "\n")
	if strings.Contains(frame, "...") {
		t.Fatalf("a fold of one row must show the row instead:\n%s", frame)
	}
	if !strings.Contains(frame, "Inc 0") || !strings.Contains(frame, "Out 0") {
		t.Fatalf("rows missing:\n%s", frame)
	}
}

func TestBUG1222_NarrowPaneShortensLabelNotAmount(t *testing.T) {
	pl := plStatement([]int64{100, 200, 300, 400, 500}, []int64{10, 20, 30, 40, 50}, nil)
	rows := plRender(pl, 30, 8)
	for _, ln := range rows {
		if strings.HasPrefix(strings.TrimSpace(ln), "...") && !strings.Contains(ln, "£") {
			t.Fatalf("fold amount lost at a narrow width: %q", ln)
		}
	}
	p := plParse(t, rows)
	if p.bodyLines > 0 && !p.reconciles() {
		t.Fatalf("narrow pane does not reconcile:\n%s", strings.Join(rows, "\n"))
	}
}

func TestBUG1222_PropertyRenderedStatementReconciles(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	cases := map[string]PLView{
		"real month":     plRealMonth(),
		"all income":     plStatement([]int64{50, 40, 30, 20, 10, 5}, nil, nil),
		"all outflow":    plStatement(nil, []int64{50, 40, 30, 20, 10, 5}, nil),
		"negative resid": plStatement([]int64{50, 40, 30, 20}, []int64{25, 15, 5, 3, 2}, i64(plPounds(-12340))),
		"positive resid": plStatement([]int64{50, 40, 30, 20}, []int64{25, 15, 5, 3, 2}, i64(plPounds(770))),
		"one huge row":   plStatement([]int64{1, 1, 1, 9000000}, []int64{2, 2, 2, 2}, nil),
		"equal amounts":  plStatement([]int64{7, 7, 7, 7, 7}, []int64{7, 7, 7, 7, 7}, i64(0)),
		"zero rows":      plStatement(nil, nil, nil),
		"zero rows+res":  plStatement(nil, nil, i64(plPounds(-500))),
		"one each":       plStatement([]int64{5}, []int64{3}, i64(plPounds(1))),
	}
	for name, pl := range cases {
		rowCount := len(pl.Revenues) + len(pl.Expenses)
		for w := 24; w <= 60; w += 3 {
			for h := 2; h <= 14; h++ {
				rows := plRender(pl, w, h)
				frame := strings.Join(rows, "\n")
				p := plParse(t, rows)
				if !p.haveNet {
					t.Fatalf("%s %dx%d: Net missing:\n%s", name, w, h, frame)
				}
				if h >= 3 && !p.haveCons {
					t.Fatalf("%s %dx%d: consumption missing:\n%s", name, w, h, frame)
				}
				if p.bodyLines > 0 && !p.reconciles() {
					t.Fatalf("%s %dx%d: income %d - outflows %d + residue %d != Net %d:\n%s", name, w, h, p.incomeSum, p.outflowSum, p.residue, p.net, frame)
				}
				if strings.Contains(p.incomeFold+p.outflowFold, " 1 more") {
					t.Fatalf("%s %dx%d: a fold of one row", name, w, h)
				}
				if p.incomeFold != "" && !strings.Contains(p.incomeFold, "more") && w >= 30 {
					t.Fatalf("%s %dx%d: income fold lost its meaning: %q", name, w, h, p.incomeFold)
				}
				if p.haveWages && (rowCount > 0 || pl.Other != nil) && p.bodyLines == 0 {
					t.Fatalf("%s %dx%d: wages shown while the reconciling set is not:\n%s", name, w, h, frame)
				}
				// The wide-enough panes carry the full wording.
				if plLayoutFor(w-1, pl.Net != nil && pl.Net.PrevValueMicropounds != nil, plAmountWidth(pl)).labelW >= 25 {
					if p.incomeFold != "" && !strings.Contains(p.incomeFold, "more income rows") {
						t.Fatalf("%s %dx%d: income fold wording: %q", name, w, h, p.incomeFold)
					}
					if p.outflowFold != "" && !strings.Contains(p.outflowFold, "more outflow rows") {
						t.Fatalf("%s %dx%d: outflow fold wording: %q", name, w, h, p.outflowFold)
					}
				}
				if p.incomeFold != "" && strings.Contains(p.incomeFold, "outflow") || p.outflowFold != "" && strings.Contains(p.outflowFold, "income") {
					t.Fatalf("%s %dx%d: fold under the wrong heading", name, w, h)
				}
			}
		}
	}
}
