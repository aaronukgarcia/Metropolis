package chrome

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// mustFiguresPatch builds a well-formed "chrome.topbar" figures patch.
func mustFiguresPatch(t *testing.T, date string, cycle, speed int, money, pop int64, rating string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schemaVersion": wireSchemaVersion,
		"figures": map[string]any{
			"date": date, "clockCycle": cycle, "speed": speed,
			"money": money, "population": pop, "rating": rating,
		},
	})
	if err != nil {
		t.Fatalf("marshal figures patch: %v", err)
	}
	return raw
}

// rowString renders the runes of buffer row y into a trimmed string.
func rowString(buf *core.Buffer, y, w int) string {
	var sb strings.Builder
	for x := 0; x < w; x++ {
		sb.WriteRune(buf.Get(x, y).Rune)
	}
	return strings.TrimRight(sb.String(), " ")
}

// recordingNavigator records every DrillTarget it navigates to, so tests
// can assert exactly which targets a crisis or jump fired.
type recordingNavigator struct {
	calls []dash.DrillTarget
}

func (r *recordingNavigator) Navigate(t dash.DrillTarget) error {
	r.calls = append(r.calls, t)
	return nil
}

// recordingEffects returns an Effects whose Pause increments *pauses and
// whose Navigator records into *nav, so tests can assert exactly what a
// crisis or jump fired.
func recordingEffects() (Effects, *int, *recordingNavigator) {
	pauses := new(int)
	nav := &recordingNavigator{}
	return Effects{
		Pause:     func() { *pauses++ },
		Navigator: nav,
	}, pauses, nav
}

// TestFiguresRenderAndUpdate is AC-1: the top bar renders date, clock-cycle,
// money, population, and rating from injected values, and each field
// updates when a new delta arrives. A build that rendered a static top bar
// (or dropped a field) would fail the "want" substring check or the update
// half.
//
// FEAT-216: SPEED is deliberately absent from the rendered line — the
// bottom bar (cmd/metropolis/statusbar.go) owns machine state. The field is
// still carried and still decoded, which TestFEAT216_SpeedIsCarriedButNotRendered
// below asserts separately; what changed is only what this line prints.
func TestFiguresRenderAndUpdate(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})

	c.ApplyFiguresPatch(mustFiguresPatch(t, "Aug 2026", 14, 2, 123456, 50000, "AA"))
	buf := core.NewBuffer(80, 6)
	c.Render(buf, core.Rect{X: 0, Y: 0, W: 80, H: 6})

	top := rowString(buf, 0, 80)
	for _, want := range []string{
		"Aug 2026", "cycle 14/30", "money 123456", "pop 50000", "rating AA",
	} {
		if !strings.Contains(top, want) {
			t.Errorf("top bar %q does not contain %q", top, want)
		}
	}

	// A second delta updates every field (AC-1's "updates when a new delta
	// arrives"). A fresh buffer, not the one above: Render draws only as many
	// cells as its string is long and never blanks the row, so reusing the
	// buffer leaves a tail of the LONGER previous line behind and the
	// assertions would be reading two renders spliced together. (The live
	// binary does not have this problem — chromeTopBarDraw blanks row 0 in
	// the bar's own style before calling Render.)
	buf = core.NewBuffer(80, 6)
	c.ApplyFiguresPatch(mustFiguresPatch(t, "Sep 2026", 5, 1, 999, 60000, "BB"))
	c.Render(buf, core.Rect{X: 0, Y: 0, W: 80, H: 6})
	top = rowString(buf, 0, 80)
	for _, want := range []string{
		"Sep 2026", "cycle 5/30", "money 999", "pop 60000", "rating BB",
	} {
		if !strings.Contains(top, want) {
			t.Errorf("updated top bar %q does not contain %q", top, want)
		}
	}
}

// TestFEAT216_SpeedIsCarriedButNotRendered pins both halves of the lead's
// ruling at once, because each half is wrong without the other:
//
//   - the top bar must NOT print speed (the bottom bar owns machine state,
//     and two bars printing one fact in two formats is what the ruling
//     removes), and
//   - Figures must still CARRY the real value, so the view keeps telling the
//     truth to any caller. Zeroing the field to satisfy the layout would
//     make the data lie.
func TestFEAT216_SpeedIsCarriedButNotRendered(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})
	c.ApplyFiguresPatch(mustFiguresPatch(t, "Aug 2026", 14, 4, 123456, 50000, "AA"))

	if got := c.Figures().Speed; got != 4 {
		t.Errorf("Figures().Speed = %d, want 4 — the field must still carry the real multiplier", got)
	}

	buf := core.NewBuffer(80, 6)
	c.Render(buf, core.Rect{X: 0, Y: 0, W: 80, H: 6})
	if top := rowString(buf, 0, 80); strings.Contains(strings.ToLower(top), "speed") {
		t.Errorf("top bar still prints speed: %q", top)
	}
}

// TestRenderColourPerTier is AC-2: N alerts of mixed tiers render with the
// correct colour per tier. It checks the foreground colour of each rendered
// alert row against the tier's palette token, so a build that painted every
// alert one colour (or ignored tier) fails.
func TestRenderColourPerTier(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})

	crit := mustAlert(t, "crit", TierCritical, false, "f3", protocol.Tick(1))
	warn := mustAlert(t, "warn", TierWarning, false, "f2", protocol.Tick(2))
	info := mustAlert(t, "info", TierInfo, false, "f1", protocol.Tick(3))
	for _, a := range []Alert{crit, warn, info} {
		if err := c.AddAlert(a); err != nil {
			t.Fatal(err)
		}
	}

	buf := core.NewBuffer(80, 6)
	c.Render(buf, core.Rect{X: 0, Y: 0, W: 80, H: 6})

	// Row 0 is the top bar; rows 1..3 are the alerts in stack order.
	wantRows := []struct {
		id    string
		token widgets.Token
	}{{"crit", widgets.TokenDanger}, {"warn", widgets.TokenWarning}, {"info", widgets.TokenSelection}}
	for i, w := range wantRows {
		row := 1 + i
		gotFg, _, _ := buf.Get(0, row).Style.Decompose()
		wantFg := widgets.DefaultPalette.Color(w.token)
		if gotFg != wantFg {
			t.Errorf("alert %q row %d foreground = %v, want %v (tier %v)", w.id, row, gotFg, wantFg, w.token)
		}
	}
}

// TestJumpToSelectedAlert is AC-3: selecting a known alert navigates to
// exactly its registered target through the same dash.Navigator seam (via
// Effects.Navigator) every other jump uses — no second, parallel navigation
// path. A build with a bespoke navigation call per alert type would fail
// the "exactly this target" assertion.
func TestJumpToSelectedAlert(t *testing.T) {
	effects, _, navigated := recordingEffects()
	c := NewChrome("test", widgets.DefaultPalette, effects)

	a := mustAlert(t, "water", TierWarning, false, "f7.water", protocol.Tick(1))
	if err := c.AddAlert(a); err != nil {
		t.Fatal(err)
	}

	if !c.JumpTo(a) {
		t.Fatal("JumpTo returned false for a valid alert")
	}
	if !reflect.DeepEqual(navigated.calls, []dash.DrillTarget{drill("f7.water")}) {
		t.Fatalf("navigated = %v, want [f7.water]", navigated.calls)
	}
}

// TestBangJumpsToTopAlert is AC-4: firing the registered `!` global invokes
// the jump mechanism against whichever alert is currently ranked first — NOT
// the first-inserted alert. The high-tier alert is inserted LAST here, so a
// build that bound `!` to a fixed/first-inserted alert would navigate to the
// low-tier alert and fail.
func TestBangJumpsToTopAlert(t *testing.T) {
	effects, _, navigated := recordingEffects()
	c := NewChrome("test", widgets.DefaultPalette, effects)

	low := mustAlert(t, "low", TierInfo, false, "f1", protocol.Tick(1))
	high := mustAlert(t, "high", TierCritical, false, "f3", protocol.Tick(2))
	if err := c.AddAlert(low); err != nil {
		t.Fatal(err)
	}
	if err := c.AddAlert(high); err != nil {
		t.Fatal(err)
	}

	g := keys.NewKeyGrammar(nil, 0, 0, "test")
	if err := c.RegisterBang(g); err != nil {
		t.Fatalf("RegisterBang: %v", err)
	}

	res := g.Feed(keys.KeyRune('!'))
	if res.Status != keys.GlobalDispatched {
		t.Fatalf("Feed('!') status = %v, want GlobalDispatched", res.Status)
	}

	if !reflect.DeepEqual(navigated.calls, []dash.DrillTarget{drill("f3")}) {
		t.Fatalf("`!` navigated = %v, want [f3] (the ranked-first high alert, not the first-inserted low)", navigated.calls)
	}
}

// TestNonCrisisCriticalDoesNotPause is AC-6's first half: a P0/TierCritical
// alert that is NOT tagged crisis (e.g. "Loan payment due") must NOT
// auto-pause or redirect. A build that derived crisis from tier would pause
// here and fail.
func TestNonCrisisCriticalDoesNotPause(t *testing.T) {
	effects, pauses, navigated := recordingEffects()
	c := NewChrome("test", widgets.DefaultPalette, effects)

	loan := mustAlert(t, "loan", TierCritical, false, "f2.finance", protocol.Tick(1))
	if err := c.AddAlert(loan); err != nil {
		t.Fatal(err)
	}

	if *pauses != 0 {
		t.Fatalf("non-crisis P0 alert caused %d pause(s), want 0", *pauses)
	}
	if len(navigated.calls) != 0 {
		t.Fatalf("non-crisis P0 alert navigated %v, want none", navigated.calls)
	}
}

// TestCrisisAutoPausesRegardlessOfTier is AC-6's second half: a
// crisis-tagged alert auto-pauses regardless of its display tier — even an
// TierInfo crisis pauses. A build that gated auto-pause on high tier would
// fail here.
func TestCrisisAutoPausesRegardlessOfTier(t *testing.T) {
	effects, pauses, navigated := recordingEffects()
	c := NewChrome("test", widgets.DefaultPalette, effects)

	crisis := mustAlert(t, "stockout", TierInfo, true, "f7.water", protocol.Tick(1))
	if err := c.AddAlert(crisis); err != nil {
		t.Fatal(err)
	}

	if *pauses != 1 {
		t.Fatalf("crisis-tagged alert caused %d pause(s), want 1", *pauses)
	}
	if !reflect.DeepEqual(navigated.calls, []dash.DrillTarget{drill("f7.water")}) {
		t.Fatalf("crisis navigated %v, want [f7.water]", navigated.calls)
	}
}

// TestCrisisFiresExactlyOnce is AC-8's first half: the SAME crisis ID fed
// across >=3 consecutive deltas triggers pause+redirect on the first
// occurrence only. A level-triggered (per-delta) implementation would pause
// 3 times and fail.
func TestCrisisFiresExactlyOnce(t *testing.T) {
	effects, pauses, navigated := recordingEffects()
	c := NewChrome("test", widgets.DefaultPalette, effects)

	crisis := mustAlert(t, "c1", TierCritical, true, "f7.water", protocol.Tick(1))
	for i := 0; i < 3; i++ {
		if err := c.AddAlert(crisis); err != nil {
			t.Fatal(err)
		}
	}

	if *pauses != 1 {
		t.Fatalf("same-ID crisis across 3 deltas caused %d pause(s), want exactly 1", *pauses)
	}
	if len(navigated.calls) != 1 {
		t.Fatalf("same-ID crisis navigated %d times, want exactly 1", len(navigated.calls))
	}
}

// TestSecondCrisisFires is AC-8's second half: a DIFFERENT crisis ID, even
// while the first crisis's alert is still on the stack, fires a fresh
// pause+redirect — the dedupe is keyed on crisis identity, not on "a crisis
// is currently active at all". A build that suppressed all crises once one
// was active would fail.
func TestSecondCrisisFires(t *testing.T) {
	effects, pauses, navigated := recordingEffects()
	c := NewChrome("test", widgets.DefaultPalette, effects)

	c1 := mustAlert(t, "c1", TierCritical, true, "f7.water", protocol.Tick(1))
	c2 := mustAlert(t, "c2", TierCritical, true, "f8.school", protocol.Tick(2))
	if err := c.AddAlert(c1); err != nil {
		t.Fatal(err)
	}
	if err := c.AddAlert(c2); err != nil {
		t.Fatal(err)
	}

	if *pauses != 2 {
		t.Fatalf("two distinct crises caused %d pause(s), want 2", *pauses)
	}
	if !reflect.DeepEqual(navigated.calls, []dash.DrillTarget{drill("f7.water"), drill("f8.school")}) {
		t.Fatalf("crises navigated %v, want [f7.water f8.school]", navigated.calls)
	}
}

// TestResumeDoesNotRefireOngoingCrisis is AC-9: after a crisis fires and the
// player manually resumes, a still-ongoing same-ID crisis in the next delta
// does NOT auto-pause again — the seenCrisis dedupe survives the resume. A
// naive "pause flag" implementation that relatches on a stale
// crisis-still-present check would pause twice and fail.
func TestResumeDoesNotRefireOngoingCrisis(t *testing.T) {
	effects, pauses, _ := recordingEffects()
	c := NewChrome("test", widgets.DefaultPalette, effects)

	c1 := mustAlert(t, "c1", TierCritical, true, "f7.water", protocol.Tick(1))
	if err := c.AddAlert(c1); err != nil {
		t.Fatal(err)
	}
	if *pauses != 1 {
		t.Fatalf("initial crisis caused %d pause(s), want 1", *pauses)
	}

	// "Manual resume" — nothing in Chrome tracks pause state, so resume is
	// simply the player's own action; the next delta reports the SAME crisis
	// ID again while the condition persists.
	if err := c.AddAlert(c1); err != nil {
		t.Fatal(err)
	}
	if *pauses != 1 {
		t.Fatalf("still-ongoing same-ID crisis after resume caused a refire: %d pause(s), want 1", *pauses)
	}

	// A genuinely NEW crisis still pauses — AC-9's second half (and AC-8's),
	// confirming the resume path didn't break the new-crisis path.
	c2 := mustAlert(t, "c2", TierCritical, true, "f8.school", protocol.Tick(2))
	if err := c.AddAlert(c2); err != nil {
		t.Fatal(err)
	}
	if *pauses != 2 {
		t.Fatalf("new crisis after resume caused %d pause(s), want 2", *pauses)
	}
}

// TestAlreadyPausedNewCrisisStillRedirects is AC-10: a NEW crisis arriving
// while the world is already paused (from a prior crisis or a manual pause)
// still fires the redirect — the Pause is idempotent, but the Navigate is
// NOT skipped just because "we're already paused". A build that guarded the
// whole handler behind "if already paused, return early" would drop the
// second crisis's redirect and fail.
func TestAlreadyPausedNewCrisisStillRedirects(t *testing.T) {
	effects, pauses, navigated := recordingEffects()
	c := NewChrome("test", widgets.DefaultPalette, effects)

	c1 := mustAlert(t, "c1", TierCritical, true, "f7.water", protocol.Tick(1))
	if err := c.AddAlert(c1); err != nil {
		t.Fatal(err)
	}
	// The world is now paused (from c1). A distinct crisis arrives.
	c2 := mustAlert(t, "c2", TierCritical, true, "f8.school", protocol.Tick(2))
	if err := c.AddAlert(c2); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(navigated.calls, []dash.DrillTarget{drill("f7.water"), drill("f8.school")}) {
		t.Fatalf("second crisis's redirect was skipped: navigated %v, want [f7.water f8.school]", navigated.calls)
	}
	// Pause fired again (idempotent at the engine), never a toggle-to-unpause.
	if *pauses != 2 {
		t.Fatalf("second crisis caused %d pause(s), want 2 (idempotent re-pause)", *pauses)
	}
}

// TestResolvedAlertRemoved is AC-12: a resolved alert's ID disappears from
// the stack on the delta that reports resolution, rather than staying as a
// dead entry. A build that never removed resolved alerts would fail.
func TestResolvedAlertRemoved(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})

	a := mustAlert(t, "water", TierWarning, false, "f7.water", protocol.Tick(1))
	if err := c.AddAlert(a); err != nil {
		t.Fatal(err)
	}
	if got := ids(c.Alerts()); !reflect.DeepEqual(got, []string{"water"}) {
		t.Fatalf("before resolve, stack = %v, want [water]", got)
	}

	c.ResolveAlert("water")

	if got := c.Alerts(); len(got) != 0 {
		t.Fatalf("after resolve, stack = %v, want empty", ids(got))
	}
}

// TestSubscribeSendsFiguresView is AC-1's "sourced from int.protocol view
// subscriptions" half: Subscribe sends exactly one KindSubscribe command for
// the chrome.topbar view. It also pins the command shape a composition root
// relies on.
func TestSubscribeSendsFiguresView(t *testing.T) {
	c := NewChrome("cid-1", widgets.DefaultPalette, Effects{})

	var got protocol.Command
	err := c.Subscribe(func(cmd protocol.Command) error {
		got = cmd
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Kind != protocol.KindSubscribe {
		t.Fatalf("Subscribe sent kind %q, want %q", got.Kind, protocol.KindSubscribe)
	}
	if got.CorrelationID != "cid-1" {
		t.Fatalf("Subscribe correlation = %q, want %q", got.CorrelationID, "cid-1")
	}
	p, ok := got.Payload.(protocol.SubscribePayload)
	if !ok {
		t.Fatalf("Subscribe payload type = %T, want SubscribePayload", got.Payload)
	}
	if p.ViewName != ViewChrome {
		t.Fatalf("Subscribe view = %q, want %q", p.ViewName, ViewChrome)
	}
}

// TestPauseCommandIsSharedPause is AC-7's "equivalent to Space, not a bespoke
// pause implementation" half: PauseCommand returns the shared KindPause
// command (the one Space's binding sends), not a chrome-private control.
func TestPauseCommandIsSharedPause(t *testing.T) {
	cmd := PauseCommand("cid-2")
	if cmd.Kind != protocol.KindPause {
		t.Fatalf("PauseCommand kind = %q, want %q", cmd.Kind, protocol.KindPause)
	}
	if _, ok := cmd.Payload.(protocol.PausePayload); !ok {
		t.Fatalf("PauseCommand payload type = %T, want PausePayload", cmd.Payload)
	}
	if cmd.CorrelationID != "cid-2" {
		t.Fatalf("PauseCommand correlation = %q, want cid-2", cmd.CorrelationID)
	}
}
