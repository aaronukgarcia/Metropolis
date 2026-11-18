package why

import (
	"math"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// Column grid (text columns, full layout):
//
//	NAME(16) BAR(12) VAL(4) WORD(13) PTS(5)  WHY...
//
// Cut order as the width falls: the why-text goes below whyMinWidth, the bar
// below barMinWidth, the word is shortened below wordMinWidth; the name, the
// word and the signed points are never cut (the name only shrinks when the
// terminal is narrower than the rest of the line).
const (
	nameW         = 16
	barW          = 12
	valW          = 4
	wordFullW     = 13
	wordShortW    = 4
	ptsW          = 5
	spanW         = barW + 1 + valW // bar and value columns together
	whyGap        = 2
	barMinWidth   = 54 // below this the bars go
	wordMinWidth  = 41 // below this the word is shortened
	whyMinWidth   = 76 // below this the why-text goes
	titleMinWidth = 40 // below this the title is just "WHY"

	// shortArrivalsWidth is the width under which the arrivals line uses its
	// compact wording.
	shortArrivalsWidth = 54

	noMonthText = "no month closed yet"
	noHomeless  = "homelessness: not built yet"
	titleFull   = "WHY PEOPLE COME OR GO"
	titleShort  = "WHY"
	pullingTone = 1
	pushingTone = -1
)

// Drop order when there are fewer rows than lines: the line with the lowest
// number goes first. Ten levers and TOTAL go last (the title goes before any
// lever; the honest no-data notice goes last of all).
const (
	dropHomeless    = 1
	dropArrivals    = 2
	dropWellbeing   = 3
	dropMomentum    = 4
	dropEnvironment = 5
	dropSafety      = 6
	dropTitle       = 7
	dropLeverBase   = 8 // lever i (0-based) drops at dropLeverBase + (9 - i)
	dropTotal       = 40
	dropNotice      = 50
)

// row is one panel line, pre-formatted; text(...) lays it out for a width.
type row struct {
	drop       int
	free       string // whole-line text (title, arrivals, homeless, notice)
	freeShort  string // compact wording of free
	shortBelow int    // widths under this use freeShort
	name       string
	bar        string // bar column text ("" = blank)
	span       string // bar+value columns together (TOTAL's score text)
	val        string
	word       string
	wordShort  string
	pts        string
	why        string
	tone       int // pullingTone, pushingTone or 0
	dim        bool
	bold       bool
}

// layout is the column decision for one width.
type layout struct {
	w, nameW, wordW int
	bar, why        bool
}

func layoutFor(w int) layout {
	l := layout{w: w, bar: w >= barMinWidth, why: w >= whyMinWidth, wordW: wordFullW}
	if w < wordMinWidth {
		l.wordW = wordShortW
	}
	fixed := 1 + valW + 1 + l.wordW + 1 + ptsW
	if l.bar {
		fixed = 1 + barW + 1 + valW + 1 + l.wordW + 1 + ptsW
	}
	l.nameW = nameW
	if w-fixed < l.nameW {
		l.nameW = w - fixed
	}
	if l.nameW < 0 {
		l.nameW = 0
	}
	return l
}

// text lays the row out for the layout and clips it to the width.
func (r row) text(l layout) string {
	if r.free != "" {
		t := r.free
		if r.freeShort != "" && l.w < r.shortBelow {
			t = r.freeShort
		}
		return clipRunes(t, l.w)
	}
	var b strings.Builder
	b.WriteString(padRight(clipRunes(r.name, l.nameW), l.nameW))
	switch {
	case l.bar && r.span != "":
		b.WriteString(" " + padRight(clipRunes(r.span, spanW), spanW))
	case l.bar:
		b.WriteString(" " + padRight(r.bar, barW) + " " + padLeft(r.val, valW))
	default:
		b.WriteString(" " + padLeft(r.val, valW))
	}
	word := r.word
	if l.wordW == wordShortW {
		word = r.wordShort
	}
	b.WriteString(" " + padRight(word, l.wordW) + " " + padLeft(r.pts, ptsW))
	if l.why && r.why != "" {
		b.WriteString(strings.Repeat(" ", whyGap) + r.why)
	}
	return clipRunes(strings.TrimRight(b.String(), " "), l.w)
}

// Lines renders the panel as plain text lines for a w x h area. p nil, or a
// patch that is not Known, gives the honest no-data panel. The result never
// has more than h lines nor a line wider than w columns.
func Lines(p *Patch, w, h int) []string {
	if w <= 0 || h <= 0 {
		return nil
	}
	rows := buildRows(p)
	rows = fitRows(rows, h)
	l := layoutFor(w)
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.text(l)
	}
	return out
}

// fitRows drops the lowest-priority rows until at most h remain, keeping order.
func fitRows(rows []row, h int) []row {
	for len(rows) > h {
		lo := 0
		for i := range rows {
			if rows[i].drop < rows[lo].drop {
				lo = i
			}
		}
		rows = append(rows[:lo:lo], rows[lo+1:]...)
	}
	return rows
}

// buildRows builds every panel line for the patch in display order.
func buildRows(p *Patch) []row {
	if p == nil || !p.Known {
		return noDataRows()
	}
	pts := allocateTenths(p)
	var rows []row
	rows = append(rows, row{drop: dropTitle, bold: true, free: titleFull + "   month " + strconv.FormatInt(p.Month, 10), freeShort: titleShort, shortBelow: titleMinWidth})
	for i := 0; i < LeverCount; i++ {
		rows = append(rows, leverRow(&p.Levers[i], LeverNames[i], dropLeverBase+(LeverCount-1-i), pts.lever[i], ""))
	}
	for i := 0; i < ModifierCount; i++ {
		drop := dropSafety
		if i == 1 {
			drop = dropEnvironment
		}
		var m *Lever
		var t int64
		if len(p.Modifiers) == ModifierCount {
			m, t = &p.Modifiers[i], pts.mod[i]
		}
		rows = append(rows, leverRow(m, ModifierNames[i], drop, t, "extra modifier"))
	}
	rows = append(rows, momentumRow(p, pts.rep))
	rows = append(rows, wellbeingRow(p, pts.well))
	rows = append(rows, totalRow(p, pts.net))
	rows = append(rows, arrivalsRow(p))
	rows = append(rows, homelessRow(p))
	return rows
}

// noDataRows is the honest panel before any month has closed.
func noDataRows() []row {
	rows := []row{
		{drop: dropTitle, bold: true, free: titleFull, freeShort: titleShort, shortBelow: titleMinWidth},
		{drop: dropNotice, free: noMonthText},
	}
	for i := 0; i < LeverCount; i++ {
		rows = append(rows, row{drop: dropLeverBase + (LeverCount - 1 - i), name: LeverNames[i],
			bar: "[" + strings.Repeat(".", 10) + "]", val: "--", word: "no data yet", wordShort: "n/a", dim: true})
	}
	for i := 0; i < ModifierCount; i++ {
		drop := dropSafety
		if i == 1 {
			drop = dropEnvironment
		}
		rows = append(rows, row{drop: drop, name: ModifierNames[i],
			bar: "[" + strings.Repeat(".", 10) + "]", val: "--", word: "no data yet", wordShort: "n/a", dim: true,
			why: "extra modifier"})
	}
	rows = append(rows, row{drop: dropHomeless, free: noHomeless, dim: true})
	return rows
}

// leverRow formats one lever or modifier line. l nil means "not sent".
func leverRow(l *Lever, name string, drop int, tenths int64, note string) row {
	r := row{drop: drop, name: name}
	if l == nil || !l.Built {
		r.bar = "[" + strings.Repeat(".", 10) + "]"
		r.val, r.word, r.wordShort = "--", "not built yet", "none"
		r.pts = fmtTenths(0, false)
		r.dim = true
		return r
	}
	r.bar = barFor(l.Value)
	r.val = strconv.FormatInt(int64(math.Round(l.Value)), 10)
	r.word, r.wordShort, r.tone = wordFor(tenths)
	r.pts = fmtTenths(tenths, true)
	r.why = partsText(l.Parts)
	if r.why == "" {
		r.why = note
	}
	return r
}

func momentumRow(p *Patch, tenths int64) row {
	trend := p.ReputationTrend
	if trend == "" {
		switch {
		case p.ReputationPoints > 0.05:
			trend = TrendRising
		case p.ReputationPoints < -0.05:
			trend = TrendFalling
		default:
			trend = TrendSteady
		}
	}
	short, tone := "flat", 0
	switch trend {
	case TrendRising:
		short, tone = "up", pullingTone
	case TrendFalling:
		short, tone = "down", pushingTone
	}
	return row{drop: dropMomentum, name: "Momentum / pride",
		val: signedInt(int64(math.Round(p.Reputation))), word: trend, wordShort: short,
		pts: fmtTenths(tenths, true), why: "pride, big inertia", tone: tone}
}

func wellbeingRow(p *Patch, tenths int64) row {
	word, short, tone := wordFor(tenths)
	return row{drop: dropWellbeing, name: "Wellbeing", word: word, wordShort: short, tone: tone,
		pts: fmtTenths(tenths, true), why: "city mood x" + fmtFixed(p.WellbeingFactor, 2)}
}

func totalRow(p *Patch, tenths int64) row {
	world := 0.0
	if p.AWorld != nil {
		world = *p.AWorld
	} else if p.Neutral != nil {
		world = *p.Neutral
	}
	score := fmtTenths(roundTenths(p.Score), false) + " vs " + fmtTenths(roundTenths(world), false)
	word, short, tone := "balanced", "even", 0
	switch {
	case tenths > 0:
		word, short, tone = "net gain", "gain", pullingTone
	case tenths < 0:
		word, short, tone = "net loss", "loss", pushingTone
	}
	return row{drop: dropTotal, bold: true, name: "TOTAL", span: score, word: word, wordShort: short, tone: tone,
		pts: fmtTenths(tenths, true), why: "people, before the cap"}
}

func arrivalsRow(p *Patch) row {
	in, out := arrivals(p)
	lim, limShort := "", ""
	switch p.LimitedBy {
	case LimitHomes:
		lim, limShort = " (limit: free homes)", " (homes)"
	case LimitTransport:
		lim, limShort = " (limit: transport links)", " (transport)"
	}
	if p.Wanted < 0 {
		full := "Wanted to leave " + strconv.FormatInt(-p.Wanted, 10)
		short := "leave " + strconv.FormatInt(-p.Wanted, 10)
		if out != nil {
			full += ", left " + strconv.FormatInt(*out, 10)
			short += ", left " + strconv.FormatInt(*out, 10)
		}
		return row{drop: dropArrivals, free: full, freeShort: short, shortBelow: shortArrivalsWidth}
	}
	full := "Wanted " + strconv.FormatInt(p.Wanted, 10) + ", room for " + strconv.FormatInt(p.Room, 10) + lim
	short := "want " + strconv.FormatInt(p.Wanted, 10) + ", room " + strconv.FormatInt(p.Room, 10) + limShort
	if in != nil {
		full += ", moved in " + strconv.FormatInt(*in, 10)
		short += ", in " + strconv.FormatInt(*in, 10)
	}
	return row{drop: dropArrivals, free: full, freeShort: short, shortBelow: shortArrivalsWidth}
}

// arrivals picks the monthly arrivals and departures from whichever
// spellings the patch carries.
func arrivals(p *Patch) (in, out *int64) {
	in, out = p.MovedIn, p.Left
	if in == nil {
		in = p.Inflow
	}
	if out == nil {
		out = p.Outflow
	}
	return in, out
}

func homelessRow(p *Patch) row {
	if p.Homeless != nil && p.Homeless.Built {
		return row{drop: dropHomeless, free: "homelessness: " + strconv.FormatInt(p.Homeless.Count, 10) + " people"}
	}
	return row{drop: dropHomeless, free: noHomeless, dim: true}
}

// tenthsSet is the on-screen points of every line, in tenths of a point.
type tenthsSet struct {
	lever [LeverCount]int64
	mod   [ModifierCount]int64
	rep   int64
	well  int64
	net   int64
}

// allocateTenths turns the patch's points into whole tenths. Each line is
// rounded to the nearest tenth; when the patch's own numbers add up to its
// net (within 0.05) the lines are then nudged, one tenth at a time on the
// lines that were rounded furthest the wrong way, until the printed lines sum
// exactly to the printed net. When the patch's numbers do NOT add up the raw
// rounding is kept so the mismatch stays visible.
func allocateTenths(p *Patch) tenthsSet {
	var vals []float64
	var ptr []*int64
	var out tenthsSet
	add := func(v float64, dst *int64) {
		vals = append(vals, v)
		ptr = append(ptr, dst)
	}
	for i := range p.Levers {
		v := 0.0
		if p.Levers[i].Built {
			v = p.Levers[i].Points
		}
		add(v, &out.lever[i])
	}
	if len(p.Modifiers) == ModifierCount {
		for i := range p.Modifiers {
			v := 0.0
			if p.Modifiers[i].Built {
				v = p.Modifiers[i].Points
			}
			add(v, &out.mod[i])
		}
	}
	add(p.ReputationPoints, &out.rep)
	add(p.WellbeingPoints, &out.well)

	sum := 0.0
	var rounded int64
	for i, v := range vals {
		*ptr[i] = roundTenths(v)
		rounded += *ptr[i]
		sum += v
	}
	out.net = roundTenths(p.Net)
	if math.Abs(sum-p.Net) > 0.05+1e-9 {
		return out
	}
	// Nudge until the lines sum to the net. Bounded: at most len(vals)*2 steps.
	for step := 0; rounded != out.net && step < 2*len(vals)+2; step++ {
		dir := int64(1)
		if rounded > out.net {
			dir = -1
		}
		best, bestScore := -1, math.Inf(-1)
		for i, v := range vals {
			// How far the current rounding sits on the wrong side of v.
			score := float64(dir) * (v*10 - float64(*ptr[i]))
			if score > bestScore {
				best, bestScore = i, score
			}
		}
		*ptr[best] += dir
		rounded += dir
	}
	return out
}

// roundTenths rounds v to whole tenths, halves away from zero.
func roundTenths(v float64) int64 {
	r := math.Round(v * 10)
	switch {
	case math.IsNaN(r):
		return 0
	case r >= maxTenths:
		return maxTenths
	case r <= -maxTenths:
		return -maxTenths
	}
	return int64(r)
}

// maxTenths is the saturation point of roundTenths: well inside int64 so the
// sign of fmtTenths can never overflow (-MinInt64 is MinInt64).
const maxTenths = 1 << 62

// fmtTenths prints tenths as a fixed one-decimal number. signed prints "+"
// for positive values; zero never carries a sign.
func fmtTenths(t int64, signed bool) string {
	sign := ""
	switch {
	case t < 0:
		if t < -maxTenths {
			t = -maxTenths
		}
		sign, t = "-", -t
	case t > 0 && signed:
		sign = "+"
	}
	if t > maxTenths {
		t = maxTenths
	}
	return sign + strconv.FormatInt(t/10, 10) + "." + strconv.FormatInt(t%10, 10)
}

// fmtFixed prints v with the given number of decimals, locale-free.
func fmtFixed(v float64, decimals int) string {
	if !finite(v) {
		return "?"
	}
	return strconv.FormatFloat(v, 'f', decimals, 64)
}

func signedInt(v int64) string {
	if v > 0 {
		return "+" + strconv.FormatInt(v, 10)
	}
	return strconv.FormatInt(v, 10)
}

// wordFor returns the lever word (and its short form and tone) for the
// printed points, so the word always agrees with the number beside it.
func wordFor(tenths int64) (word, short string, tone int) {
	switch {
	case tenths > 0:
		return "pulling", "pull", pullingTone
	case tenths < 0:
		return "pushing", "push", pushingTone
	}
	return "neutral", "neut", 0
}

// barFor draws a 10-cell bar for a 0..100 value.
func barFor(v float64) string {
	n := int(math.Floor(v/10 + 0.5))
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	return "[" + strings.Repeat("#", n) + strings.Repeat("-", 10-n) + "]"
}

// partsText prints up to three parts as "k v, k v".
func partsText(parts []Part) string {
	var b strings.Builder
	for i, pt := range parts {
		if i == 3 {
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(pt.K + " " + trimZero(fmtFixed(pt.V, 1)))
	}
	return b.String()
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }

func padRight(s string, n int) string {
	if r := []rune(s); len(r) < n {
		return s + strings.Repeat(" ", n-len(r))
	}
	return s
}

func padLeft(s string, n int) string {
	if r := []rune(s); len(r) < n {
		return strings.Repeat(" ", n-len(r)) + s
	}
	return s
}

func clipRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// Render draws the panel at the top-left of rect, one row per line, clipped
// to rect. On a struct-copied receiver it draws nothing.
func (s *Screen) Render(buf *core.Buffer, rect core.Rect) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Render"}); err != nil {
		return
	}
	s.mu.Lock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Render"}); err != nil {
		s.mu.Unlock()
		return
	}
	var p *Patch
	if s.patch != nil {
		c := clonePatch(*s.patch)
		p = &c
	}
	s.mu.Unlock()
	RenderPatch(buf, rect, p)
}

// RenderPatch draws the panel for p (nil = no data) into rect.
func RenderPatch(buf *core.Buffer, rect core.Rect, p *Patch) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	rows := fitRows(buildRows(p), rect.H)
	l := layoutFor(rect.W)
	for i, r := range rows {
		style := tcell.StyleDefault
		switch {
		case r.dim:
			style = style.Dim(true)
		case r.tone == pullingTone:
			style = style.Foreground(tcell.ColorGreen)
		case r.tone == pushingTone:
			style = style.Foreground(tcell.ColorRed)
		}
		if r.bold {
			style = style.Bold(true)
		}
		x := 0
		for _, ch := range r.text(l) {
			if x >= rect.W {
				break
			}
			buf.Set(rect.X+x, rect.Y+i, ch, style)
			x++
		}
	}
}
