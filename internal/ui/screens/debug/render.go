package debug

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// Render draws snap into buf within rect. It is a pure function of
// (buf, rect, snap) — AC-13: the same Snapshot renders identically
// across repeated calls, and (aside from Collect, which already ran)
// nothing here samples the wall clock or any other live source.
//
// Per AC-10, F12 renders nothing at all when snap.DebugOn is false — the
// screen simply is not visible; Render does not clear buf (that would
// itself be a rendering decision this package has no business making
// for whatever else shares the buffer) — it just draws zero cells.
func Render(buf *core.Buffer, rect core.Rect, snap Snapshot, palette widgets.Palette) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 || !snap.DebugOn {
		return
	}

	c := &cursor{buf: buf, rect: rect}
	renderBuildPane(c, snap.Build)
	c.blank()
	renderRuntimePane(c, snap.Runtime)
	c.blank()
	renderRegistryPane(c, snap, palette)
	c.blank()
	renderErrorTailPane(c, snap, palette)
	c.blank()
	renderPhasePane(c, snap.PhaseSeries)
	c.blank()
	renderBoWPane(c, snap)
	if len(snap.Events) > 0 {
		c.blank()
		renderEventsPane(c, snap.Events)
	}
}

// cursor is a simple top-to-bottom line writer confined to rect: each
// line() call draws one row and advances, and once the cursor runs past
// rect's bottom edge every further write is silently dropped (matching
// core.Buffer.Set's own out-of-range-is-a-no-op discipline — a debug
// panel that doesn't fit the terminal degrades to "the bottom is cut
// off," never a panic or corrupted neighbouring content).
type cursor struct {
	buf  *core.Buffer
	rect core.Rect
	y    int
}

func (c *cursor) line(text string, style tcell.Style) {
	y := c.rect.Y + c.y
	c.y++
	if y >= c.rect.Y+c.rect.H {
		return
	}
	drawText(c.buf, c.rect.X, y, text, style, c.rect.W)
}

func (c *cursor) blank() { c.y++ }

// drawText writes s into buf starting at (x, y), truncated to maxWidth
// cells (core.Buffer.Set clips out-of-range writes anyway, but maxWidth
// additionally keeps one pane's text from visually bleeding into a
// neighbouring pane sharing the same buffer at a wider rect than this
// one's own rect.W).
func drawText(buf *core.Buffer, x, y int, s string, style tcell.Style, maxWidth int) {
	i := 0
	for _, r := range s {
		if maxWidth > 0 && i >= maxWidth {
			return
		}
		buf.Set(x+i, y, r, style)
		i++
	}
}

const unavailableGlyph = "unavailable"

func renderBuildPane(c *cursor, b BuildInfo) {
	c.line("=== Build & code ===", tcell.StyleDefault.Bold(true))
	c.line(fmt.Sprintf("version=%s commit=%s branch=%s built=%s go=%s", b.Version, b.Commit, b.Branch, b.BuildTimeUTC, b.GoVersion), tcell.StyleDefault)
	if b.BuildHostAvailable {
		c.line("build host: "+b.BuildHost, tcell.StyleDefault)
	} else {
		c.line("build host: "+unavailableGlyph+" (not yet exposed by foundation.repo)", tcell.StyleDefault)
	}
}

func renderRuntimePane(c *cursor, r RuntimeMetrics) {
	c.line("=== Runtime ===", tcell.StyleDefault.Bold(true))
	c.line(fmt.Sprintf("uptime=%.0fs sim=%s speed=%.1fx tick=%d", r.UptimeSeconds, orNA(r.SimDate), r.Speed, r.TickNumber), tcell.StyleDefault)

	c.line(fmt.Sprintf("heap in-use: %s / %s budget (%s)", formatBytes(r.HeapInUseBytes), formatBytes(uiProcessDomainBudget.BudgetBytes), uiProcessDomainBudget.Name), tcell.StyleDefault)
	c.line(fmt.Sprintf("sys: %s / %s budget (%s)", formatBytes(r.SysBytes), formatBytes(uiProcessDomainBudget.BudgetBytes), uiProcessDomainBudget.Name), tcell.StyleDefault)
	c.line(fmt.Sprintf("arena occupancy: %s / %s budget (%s)", formatBytes(r.ArenaOccupancyBytes), formatBytes(uiProcessDomainBudget.BudgetBytes), uiProcessDomainBudget.Name), tcell.StyleDefault)
	c.line(fmt.Sprintf("GC pause p99: %d us", r.GCPauseP99Micros), tcell.StyleDefault)

	c.line(fmt.Sprintf("goroutines=%d input-echo p99=%dus", r.GoroutineCount, r.InputEchoLatencyP99Micros), tcell.StyleDefault)
	c.line(fmt.Sprintf("queue depths: input=%d delta=%d persist=%d", r.InputQueueDepth, r.DeltaQueueDepth, r.PersistQueueDepth), tcell.StyleDefault)
}

func orNA(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}

// repeatSuffix renders errs.Entry.Repeat as the " x<N>" badge BUG-025
// requires on both F12 error-tail render paths, so a coalesced entry
// (SEC-030/SEC-031/SEC-033's ring-buffer coalescing) no longer renders
// byte-identically to one seen exactly once. Repeat counts ADDITIONAL
// occurrences beyond the first (see errs.Entry.Repeat's doc comment,
// whose own worked example is literally "MET-U101 x4127" for a Repeat
// of 4127) — this suffix reproduces that exact worked example verbatim,
// so "x4127" reads as "4127 more of these landed after the one shown,"
// not a total. Repeat == 0 (the overwhelming common case: seen once, no
// coalescing) renders no suffix at all — never a bare "x0" noise floor
// on every ordinary line.
func repeatSuffix(repeat int) string {
	if repeat <= 0 {
		return ""
	}
	return fmt.Sprintf(" x%d", repeat)
}

func renderRegistryPane(c *cursor, snap Snapshot, palette widgets.Palette) {
	c.line("=== Module registry ===", tcell.StyleDefault.Bold(true))
	if !snap.RegistryAvailable {
		c.line(unavailableGlyph+": "+snap.RegistryReason, palette.Style(widgets.TokenWarning))
		return
	}
	for _, m := range snap.Registry {
		toggle := ""
		if m.CanToggle {
			toggle = " [toggle: REAL/OFF/STUB, re-enter key to confirm]"
		}
		style := tcell.StyleDefault
		switch m.Health {
		case registry.HealthError:
			style = palette.Style(widgets.TokenDanger)
		case registry.HealthDegraded:
			style = palette.Style(widgets.TokenWarning)
		}
		c.line(fmt.Sprintf("%-24s v%-8s status=%-5s health=%-9s last=%dus flag=%s%s",
			m.Key, m.Semver, m.Status, m.Health, m.LastTickCostMicros, m.FlagSource, toggle), style)
	}
}

func renderErrorTailPane(c *cursor, snap Snapshot, palette widgets.Palette) {
	c.line("=== Error tail (last 50) ===", tcell.StyleDefault.Bold(true))
	if !snap.ErrorTailAvailable {
		c.line(unavailableGlyph+": "+snap.ErrorTailReason, palette.Style(widgets.TokenWarning))
		return
	}
	if len(snap.ErrorTail) == 0 {
		c.line("(no entries)", tcell.StyleDefault)
		return
	}
	for i, e := range snap.ErrorTail {
		style := tcell.StyleDefault
		switch e.Level {
		case "error", "fatal":
			style = palette.Style(widgets.TokenDanger)
		case "warn":
			style = palette.Style(widgets.TokenWarning)
		}
		c.line(fmt.Sprintf("[%2d] %s %-5s %-10s %s: %s%s", i, e.Ts, e.Level, e.Code, e.Module, e.Msg, repeatSuffix(e.Repeat)), style)
	}
}

// RenderTailDetail draws entry's complete fields (ts, level, code,
// correlationId, module, msg, ctx) into buf within rect — AC-7's "Enter
// on a tail entry opens the full log," as opposed to the compact
// single-line summary renderErrorTailPane draws per row.
func RenderTailDetail(buf *core.Buffer, rect core.Rect, entry errs.Entry) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	c := &cursor{buf: buf, rect: rect}
	c.line("=== Log entry detail ===", tcell.StyleDefault.Bold(true))
	c.line("ts: "+entry.Ts, tcell.StyleDefault)
	c.line("level: "+entry.Level, tcell.StyleDefault)
	c.line("code: "+entry.Code+repeatSuffix(entry.Repeat), tcell.StyleDefault)
	c.line("correlationId: "+entry.CorrelationID, tcell.StyleDefault)
	c.line("module: "+entry.Module, tcell.StyleDefault)
	c.line("msg: "+entry.Msg, tcell.StyleDefault)
	if len(entry.Ctx) == 0 {
		c.line("ctx: (empty)", tcell.StyleDefault)
		return
	}
	c.line("ctx:", tcell.StyleDefault)
	for _, k := range sortedCtxKeys(entry.Ctx) {
		c.line(fmt.Sprintf("  %s = %v", k, entry.Ctx[k]), tcell.StyleDefault)
	}
}

func renderPhasePane(c *cursor, series []PhaseSeries) {
	c.line("=== Phase timing (last 60 ticks, us) ===", tcell.StyleDefault.Bold(true))
	for _, ps := range series {
		if !ps.Available {
			c.line(fmt.Sprintf("%-24s %s: %s", ps.Phase, unavailableGlyph, ps.Reason), tcell.StyleDefault)
			continue
		}
		label := fmt.Sprintf("%-24s ", ps.Phase)
		y := c.rect.Y + c.y
		c.y++
		if y >= c.rect.Y+c.rect.H {
			continue
		}
		drawText(c.buf, c.rect.X, y, label, tcell.StyleDefault, c.rect.W)
		sparkRect := core.Rect{X: c.rect.X + len(label), Y: y, W: c.rect.W - len(label), H: 1}
		widgets.Sparkline(c.buf, sparkRect, ps.microsAsFloat64(), tcell.StyleDefault)
	}
}

func renderBoWPane(c *cursor, snap Snapshot) {
	c.line("=== BoW (read-only) ===", tcell.StyleDefault.Bold(true))
	if !snap.BoWAvailable {
		c.line(unavailableGlyph+": "+snap.BoWReason, tcell.StyleDefault)
		return
	}
	var parts []string
	for _, p := range sortedPriorities(snap.BoW.OpenByPriority) {
		parts = append(parts, fmt.Sprintf("%s=%d", p, snap.BoW.OpenByPriority[p]))
	}
	c.line("open: "+strings.Join(parts, " "), tcell.StyleDefault)
	if len(snap.BoW.InProgress) == 0 {
		c.line("in_progress: (none)", tcell.StyleDefault)
		return
	}
	c.line("in_progress:", tcell.StyleDefault)
	for _, item := range snap.BoW.InProgress {
		c.line(fmt.Sprintf("  %s [%s] %s", item.Code, item.Priority, item.Title), tcell.StyleDefault)
	}
}

// sortedCtxKeys returns entry.Ctx's keys in sorted order — never Go map
// iteration order (GR#21-adjacent determinism discipline, even though
// RenderTailDetail is an on-demand detail view rather than a hot render
// path).
func sortedCtxKeys(ctx map[string]any) []string {
	keys := make([]string, 0, len(ctx))
	for k := range ctx {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func renderEventsPane(c *cursor, events []string) {
	c.line("=== Events ===", tcell.StyleDefault.Bold(true))
	for _, e := range events {
		c.line(e, tcell.StyleDefault)
	}
}
