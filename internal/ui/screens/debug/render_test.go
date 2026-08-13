package debug_test

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	debug "github.com/aaronukgarcia/Metropolis/internal/ui/screens/debug"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderToText(t *testing.T, snap debug.Snapshot) string {
	t.Helper()
	buf := core.NewBuffer(160, 60)
	debug.Render(buf, core.Rect{X: 0, Y: 0, W: 160, H: 60}, snap, widgets.DefaultPalette)
	w, h := buf.Size()
	var sb strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := buf.Get(x, y).Rune
			if r == 0 {
				r = ' '
			}
			sb.WriteRune(r)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func debugOnSnapshot(s *debug.Screen) debug.Snapshot {
	snap := s.Collect()
	snap.DebugOn = true
	return snap
}

// --- AC-1: build info renders verbatim, never hardcoded ---

func TestRender_BuildInfo_RendersInjectedValuesVerbatim(t *testing.T) {
	origVersion, origCommit, origBranch, origBuildTime := buildinfo.Version, buildinfo.Commit, buildinfo.Branch, buildinfo.BuildTime
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.Branch, buildinfo.BuildTime = origVersion, origCommit, origBranch, origBuildTime
	})
	buildinfo.Version = "v9.9.9-test"
	buildinfo.Commit = "deadbeefcafe"
	buildinfo.Branch = "test-branch-xyz"
	buildinfo.BuildTime = "2099-01-01T00:00:00Z"

	s := debug.NewScreen(nil, "corr-1", debug.WithDebugFlag(func() bool { return true }))
	text := renderToText(t, debugOnSnapshot(s))

	for _, want := range []string{"v9.9.9-test", "deadbeefcafe", "test-branch-xyz", "2099-01-01T00:00:00Z"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered build pane missing %q:\n%s", want, text)
		}
	}
}

// --- SEC-011: raw control/escape bytes in errs.Entry.Msg must never
// reach the Buffer as a raw rune via the real F12 error-tail render
// path (the BOW item's documented example: an untrusted/wrapped error
// message carrying a raw ESC-led sequence, e.g. "\x1b[2J", reconstructs
// on the real terminal once flushed if this pane doesn't filter it). ---

func TestRender_ErrorTail_StripsRawEscapeFromEntryMsg(t *testing.T) {
	s := debug.NewScreen(nil, "corr-1",
		debug.WithDebugFlag(func() bool { return true }),
		debug.WithErrorTailSource(func() []errs.Entry {
			return []errs.Entry{{Ts: "t0", Level: "error", Code: "MET-U100", Msg: "boom\x1b[2Jmore"}}
		}),
	)
	buf := core.NewBuffer(160, 60)
	debug.Render(buf, core.Rect{X: 0, Y: 0, W: 160, H: 60}, debugOnSnapshot(s), widgets.DefaultPalette)

	w, h := buf.Size()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if r := buf.Get(x, y).Rune; r == 0x1B {
				t.Fatalf("raw ESC rune (0x1B) found in rendered Buffer at (%d,%d) — SEC-011 escape injection not filtered", x, y)
			}
		}
	}

	text := renderToText(t, debugOnSnapshot(s))
	if !strings.Contains(text, "boom") || !strings.Contains(text, "more") {
		t.Errorf("rendered error-tail pane lost the surrounding legitimate text, want \"boom\" and \"more\" both present:\n%s", text)
	}
	if strings.Contains(text, "\x1b[2J") {
		t.Errorf("rendered error-tail pane still contains the raw escape sequence verbatim:\n%q", text)
	}
}

// --- AC-2: memory fields render against their named budget ---

func TestRender_RuntimeMemory_ShownAgainstNamedBudget(t *testing.T) {
	s := debug.NewScreen(nil, "corr-1",
		debug.WithDebugFlag(func() bool { return true }),
		debug.WithRuntimeSource(func() debug.RuntimeMetrics {
			return debug.RuntimeMetrics{HeapInUseBytes: 12_000_000, SysBytes: 20_000_000, ArenaOccupancyBytes: 1_000_000}
		}),
	)
	text := renderToText(t, debugOnSnapshot(s))

	if !strings.Contains(text, "UI process domain") {
		t.Errorf("rendered runtime pane never names a memory budget region:\n%s", text)
	}
	if !strings.Contains(text, "12.00 MB") {
		t.Errorf("rendered runtime pane missing the heap-in-use figure:\n%s", text)
	}
}

// --- AC-3/AC-4: registry rows and toggle-control gating ---

func TestRender_RegistryRows_MatchEntriesAndGateToggleControl(t *testing.T) {
	reg := registry.NewRegistry()
	_ = reg.Register("crime", nil, fakeModule{name: "crime", version: "0.1.0", health: registry.HealthOK}, registry.WithCanToggle(true), registry.WithStatus(registry.StatusStub))
	_ = reg.Register("finance", nil, fakeModule{name: "finance", version: "0.2.0", health: registry.HealthDegraded}, registry.WithCanToggle(false), registry.WithStatus(registry.StatusStub))

	s := debug.NewScreen(reg, "corr-1", debug.WithDebugFlag(func() bool { return true }))
	text := renderToText(t, debugOnSnapshot(s))

	if !strings.Contains(text, "crime") || !strings.Contains(text, "finance") {
		t.Fatalf("rendered registry pane missing a module row:\n%s", text)
	}

	lines := strings.Split(text, "\n")
	var crimeLine, financeLine string
	for _, l := range lines {
		if strings.Contains(l, "crime") && strings.Contains(l, "status=") {
			crimeLine = l
		}
		if strings.Contains(l, "finance") && strings.Contains(l, "status=") {
			financeLine = l
		}
	}
	if !strings.Contains(crimeLine, "toggle") {
		t.Errorf("CanToggle=true row has no toggle control: %q", crimeLine)
	}
	if strings.Contains(financeLine, "toggle") {
		t.Errorf("CanToggle=false row unexpectedly offers a toggle control: %q", financeLine)
	}
}

// --- AC-8: phase sparkline reuses widgets.Sparkline, fixed order ---

func TestRender_PhaseSparkline_FixedOrderAndPresent(t *testing.T) {
	reg := registry.NewRegistry()
	stubMod := fakeModule{name: "production", version: "0.1.0", health: registry.HealthOK}
	_ = reg.Register("production", nil, stubMod)
	for i := 0; i < 60; i++ {
		_ = reg.RecordTickCost("production", uint64(100+i))
	}

	s := debug.NewScreen(reg, "corr-1", debug.WithDebugFlag(func() bool { return true }))
	snap := debugOnSnapshot(s)

	wantOrder := []string{"production", "logistics-settlement", "consumption-shortfall", "population", "land-value-decay", "finance"}
	if len(snap.PhaseSeries) != len(wantOrder) {
		t.Fatalf("len(PhaseSeries) = %d, want %d", len(snap.PhaseSeries), len(wantOrder))
	}
	for i, want := range wantOrder {
		if snap.PhaseSeries[i].Phase != want {
			t.Errorf("PhaseSeries[%d].Phase = %q, want %q", i, snap.PhaseSeries[i].Phase, want)
		}
	}
	if !snap.PhaseSeries[0].Available || len(snap.PhaseSeries[0].Micros) != 60 {
		t.Fatalf("production phase series = %+v, want Available with 60 samples", snap.PhaseSeries[0])
	}

	text := renderToText(t, snap)
	if !strings.Contains(text, "production") {
		t.Errorf("rendered phase pane missing production row:\n%s", text)
	}
}

// --- AC-9: BoW tab ---

type fakeBoW struct{ summary debug.BoWSummary }

func (f fakeBoW) Summary() (debug.BoWSummary, error) { return f.summary, nil }

func TestRender_BoWTab_ShowsCountsAndInProgress(t *testing.T) {
	src := fakeBoW{summary: debug.BoWSummary{
		OpenByPriority: map[string]int{"P0": 1, "P1": 3, "P2": 0, "P3": 5},
		InProgress:     []debug.BoWItem{{Code: "FEAT-007", Title: "F12 info panel", Priority: "P1"}},
	}}
	s := debug.NewScreen(nil, "corr-1", debug.WithDebugFlag(func() bool { return true }), debug.WithBoWSource(src))
	text := renderToText(t, debugOnSnapshot(s))

	for _, want := range []string{"P0=1", "P1=3", "P2=0", "P3=5", "FEAT-007", "F12 info panel"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered BoW pane missing %q:\n%s", want, text)
		}
	}
}

// --- AC-10: visibility follows the debug flag ---

func TestRender_HiddenWhenDebugOff(t *testing.T) {
	s := debug.NewScreen(nil, "corr-1", debug.WithDebugFlag(func() bool { return false }))
	text := renderToText(t, s.Collect())
	if strings.TrimSpace(text) != "" {
		t.Fatalf("F12 rendered content while debug is off:\n%s", text)
	}
}

func TestRender_VisibleWhenDebugOn(t *testing.T) {
	s := debug.NewScreen(nil, "corr-1", debug.WithDebugFlag(func() bool { return true }))
	text := renderToText(t, s.Collect())
	if strings.TrimSpace(text) == "" {
		t.Fatal("F12 rendered nothing while debug is on")
	}
}

// --- AC-11: unavailable panes render clear text, never blank/panic ---

func TestRender_UnavailablePanes_NoPanicClearText(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Render panicked on unavailable panes: %v", r)
		}
	}()
	s := debug.NewScreen(nil, "corr-1", debug.WithDebugFlag(func() bool { return true }), debug.WithErrorTailSource(nil))
	text := renderToText(t, debugOnSnapshot(s))

	if strings.Count(text, "unavailable") < 2 {
		t.Errorf("expected at least 2 'unavailable' panes (registry, error tail), got:\n%s", text)
	}
}

// --- BUG-025: coalesced entries (Repeat > 0) must render visibly
// differently from a single occurrence (Repeat == 0), on BOTH F12
// render paths — the compact tail row and the detail view. ---

func TestRender_ErrorTail_RepeatCountVisibleWhenNonZero(t *testing.T) {
	single := debug.NewScreen(nil, "corr-1",
		debug.WithDebugFlag(func() bool { return true }),
		debug.WithErrorTailSource(func() []errs.Entry {
			return []errs.Entry{{Ts: "t0", Level: "warn", Code: "MET-U100", Module: "mod", Msg: "hello", Repeat: 0}}
		}),
	)
	coalesced := debug.NewScreen(nil, "corr-1",
		debug.WithDebugFlag(func() bool { return true }),
		debug.WithErrorTailSource(func() []errs.Entry {
			return []errs.Entry{{Ts: "t0", Level: "warn", Code: "MET-U100", Module: "mod", Msg: "hello", Repeat: 4127}}
		}),
	)

	singleText := renderToText(t, debugOnSnapshot(single))
	coalescedText := renderToText(t, debugOnSnapshot(coalesced))

	if singleText == coalescedText {
		t.Fatalf("compact tail row renders a coalesced entry (Repeat=4127) byte-identically to a single occurrence (Repeat=0):\n%s", singleText)
	}
	if !strings.Contains(coalescedText, "x4127") {
		t.Errorf("compact tail row missing repeat-count suffix for Repeat=4127:\n%s", coalescedText)
	}
	if strings.Contains(singleText, "x0") {
		t.Errorf("compact tail row shows bare repeat noise (x0) for an ordinary Repeat=0 entry:\n%s", singleText)
	}
}

func TestRenderTailDetail_RepeatCountVisibleWhenNonZero(t *testing.T) {
	single := errs.Entry{Ts: "t0", Level: "warn", Code: "MET-U100", CorrelationID: "corr-1", Module: "mod", Msg: "hello", Repeat: 0}
	coalesced := single
	coalesced.Repeat = 4127

	renderDetail := func(e errs.Entry) string {
		buf := core.NewBuffer(160, 60)
		debug.RenderTailDetail(buf, core.Rect{X: 0, Y: 0, W: 160, H: 60}, e)
		w, h := buf.Size()
		var sb strings.Builder
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r := buf.Get(x, y).Rune
				if r == 0 {
					r = ' '
				}
				sb.WriteRune(r)
			}
			sb.WriteByte('\n')
		}
		return sb.String()
	}

	singleText := renderDetail(single)
	coalescedText := renderDetail(coalesced)

	if singleText == coalescedText {
		t.Fatalf("detail view renders a coalesced entry (Repeat=4127) byte-identically to a single occurrence (Repeat=0):\n%s", singleText)
	}
	if !strings.Contains(coalescedText, "x4127") {
		t.Errorf("detail view missing repeat-count suffix for Repeat=4127:\n%s", coalescedText)
	}
	if strings.Contains(singleText, "x0") {
		t.Errorf("detail view shows bare repeat noise (x0) for an ordinary Repeat=0 entry:\n%s", singleText)
	}
}

// --- AC-13: pure function of inputs; repeated calls identical ---

func TestRender_PureFunction_RepeatedCallsIdentical(t *testing.T) {
	reg := registry.NewRegistry()
	_ = reg.Register("crime", nil, fakeModule{name: "crime", version: "0.1.0", health: registry.HealthOK}, registry.WithCanToggle(true))
	s := debug.NewScreen(reg, "corr-1",
		debug.WithDebugFlag(func() bool { return true }),
		debug.WithErrorTailSource(func() []errs.Entry {
			return []errs.Entry{{Ts: "t0", Level: "warn", Code: "MET-U100", Msg: "hello"}}
		}),
	)
	snap := debugOnSnapshot(s)

	first := renderToText(t, snap)
	for i := 0; i < 5; i++ {
		got := renderToText(t, snap)
		if got != first {
			t.Fatalf("Render(snap) is not repeatable at iteration %d", i)
		}
	}
}
