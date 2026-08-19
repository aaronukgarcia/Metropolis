package census

// Independent-destructive attack additions (GR#23) beyond the builder's own
// 36 tests: wire-level edge cases not covered by the shipped suite. Kept as
// sustained-value regression coverage per the house evidence protocol.

import (
	"math"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestAttack_DeltaBeforeSubscribe proves a Delta arriving before
// BindSubscription (i.e. before this screen's own subscription setup
// completed) is dropped like any other unbound subscription -- never
// applied, never panics.
func TestAttack_DeltaBeforeSubscribe(t *testing.T) {
	s := New("corr-attack-early")
	// Note: no BindSubscription call here.
	s.ApplyDelta(protocolDelta(t, "sub-never-bound", fullPatch()))
	if s.HaveData() {
		t.Error("ApplyDelta before BindSubscription set haveData=true -- delta was applied despite no bound subscription")
	}
	if _, ok := s.AgeBandSeries(); ok {
		t.Error("AgeBandSeries has data after an unbound-subscription delta")
	}
}

// TestAttack_EmptySubscriptionID proves an empty-string SubscriptionID is
// treated like any other unbound ID (dropped), never a special-cased
// wildcard match.
func TestAttack_EmptySubscriptionID(t *testing.T) {
	s := New("corr-attack-empty-sub")
	s.BindSubscription("sub-real")
	s.ApplyDelta(protocolDelta(t, "", fullPatch()))
	if s.HaveData() {
		t.Error("empty SubscriptionID delta was applied -- should be dropped as unbound")
	}
}

// TestAttack_DuplicateKPIKeysInPatch drives a single patch carrying two
// wireKPITile entries for the same key with different values, and asserts
// the behaviour is at least non-crashing and documents which value "wins"
// (last-in-slice for KPITiles(), since it is not map-deduplicated; last-in
// for KPISource() since kpiSources IS a map keyed by Key -- an asymmetry
// worth the lead's attention even though neither path panics or corrupts
// unrelated keys).
func TestAttack_DuplicateKPIKeysInPatch(t *testing.T) {
	s := New("corr-attack-dupe")
	s.BindSubscription("sub-dupe")
	kpis := []wireKPITile{
		{Key: KPIKeyGDP, Value: 111},
		{Key: KPIKeyGDP, Value: 222},
	}
	kpiSources := []wireKPISource{
		{Key: KPIKeyGDP, LineValue: 111},
		{Key: KPIKeyGDP, LineValue: 222},
	}
	s.ApplyDelta(protocolDelta(t, "sub-dupe", wirePatch{SchemaVersion: 1, KPIs: &kpis, KPISources: &kpiSources}))

	tiles, ok := s.KPITiles()
	if !ok {
		t.Fatal("KPITiles ok=false")
	}
	if len(tiles) != 2 {
		t.Fatalf("KPITiles() dropped/merged duplicate keys: got %d tiles, want 2 (both fixture rows preserved verbatim, not silently deduplicated -- a real dedup policy would need to be an explicit decision, not implicit slice pass-through)", len(tiles))
	}

	src, ok := s.KPISource(KPIKeyGDP)
	if !ok {
		t.Fatal("KPISource(gdp) ok=false")
	}
	if src.LineValue != 222 {
		t.Errorf("KPISource(gdp).LineValue = %d, want 222 (last-writer-wins on the map path) -- confirms KPITiles (slice, both survive) and KPISource (map, last wins) resolve duplicate keys inconsistently; not a crash, but a real behavioural asymmetry for a duplicate the engine should never send but the wire schema does not forbid", src.LineValue)
	}
}

// TestAttack_NaNInfKPIValue proves a NaN/Inf KPI value neither panics the
// renderer nor silently coerces to a misleading finite number.
//
// Note on delivery path: encoding/json refuses to marshal NaN/Inf
// (json: unsupported value), which this test discovered when it first
// tried to drive NaN through protocolDelta/mustJSON like every other
// attack here -- so a NaN/Inf KPITile can never actually arrive over
// int.protocol's JSON-encoded wire (the transport itself is the real
// defense here). That still leaves the in-process Render* call site
// reachable by any future non-wire caller (e.g. a local recomputation
// path), so this test drives RenderKPITiles directly to prove the
// render layer itself is not the thing relying on "the wire never sends
// this" as its only defense.
func TestAttack_NaNInfKPIValue(t *testing.T) {
	tiles := []KPITile{
		{Key: KPIKeyGDP, Value: math.NaN()},
		{Key: KPIKeyHappiness, Value: math.Inf(1)},
		{Key: KPIKeyLandValue, Value: math.Inf(-1)},
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("RenderKPITiles panicked on NaN/Inf KPI values: %v", r)
			}
		}()
		buf := core.NewBuffer(80, 10)
		rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
		RenderKPITiles(buf, rect, tiles, true, widgets.DefaultPalette.Style(widgets.TokenMoney))
		rows := renderedText(buf, rect)
		// Document what actually renders for a NaN/Inf figure -- Go's
		// fmt renders "NaN"/"+Inf"/"-Inf" for %.2f, which is at least not
		// a silently-wrong finite number, but it is also not the
		// "unavailable" state AC-12 mandates for an engine-rejected
		// figure -- a NaN/Inf KPI value is a WIRE-LEVEL malformed value,
		// not an Unavailable=true rejection, and nothing in ApplyDelta
		// validates numeric finiteness before storing it. This is a
		// finding, not a crash.
		if !rowContains(rows, "NaN") {
			t.Errorf("expected the GDP tile to render Go's NaN string somewhere, rows=%v", rows)
		}
	}()
}

// TestAttack_ZeroLengthEntityListLegitimateZero proves a KPI source with a
// genuinely empty (but present, not Unavailable) entity list renders as a
// line-value pane, not mistaken for an unavailable state -- the
// AC-6/AC-12 boundary: "no entities" (population KPI legitimately at zero)
// must render differently from "engine rejected this query".
func TestAttack_ZeroLengthEntityListLegitimateZero(t *testing.T) {
	s := New("corr-attack-zero-entities")
	s.BindSubscription("sub-zero-entities")
	kpiSources := []wireKPISource{
		{Key: KPIKeyHomeless, EntityIDs: []uint64{}, LineValue: 0}, // legitimately zero homeless citizens
	}
	s.ApplyDelta(protocolDelta(t, "sub-zero-entities", wirePatch{SchemaVersion: 1, KPISources: &kpiSources}))

	src, ok := s.KPISource(KPIKeyHomeless)
	if !ok {
		t.Fatal("KPISource ok=false")
	}
	if src.Unavailable {
		t.Fatal("a legitimately-zero (but present, not rejected) source was marked Unavailable -- AC-6/AC-12 boundary violated")
	}
	buf, rect := renderKPISourceInto(src, true)
	rows := renderedText(buf, rect)
	if rowContains(rows, "unavailable") {
		t.Error("a legitimate zero-entity KPI source rendered as \"unavailable\" -- indistinguishable from an engine rejection (AC-12 regression)")
	}
	if !rowContains(rows, "line value: 0") {
		t.Errorf("expected \"line value: 0\" for a legitimate zero-entity source, rows=%v", rows)
	}
}

// TestAttack_UnicodeControlCharsInBioStrings drives a citizen bio whose
// string fields (IndustryTie, Employment.State/Sector) carry raw C0/C1
// control characters (ESC, BEL, CR) and a unicode format character
// (U+202E RIGHT-TO-LEFT OVERRIDE, escaped per staticcheck ST1018 rather
// than embedded literally), and proves the render path never panics and
// never lets a raw control character reach the buffer --
// core.Buffer.Set's sanitizeRune (SEC-011) is the actual enforcement
// point (this package invents no bespoke sanitizer of its own, correctly
// reusing the shared boundary), so this test is really an integration
// check that census's render path funnels every character through that
// boundary rather than, e.g., writing bytes directly.
func TestAttack_UnicodeControlCharsInBioStrings(t *testing.T) {
	evil := "fintech\x1b[31mRED\x07\r" + string(rune(0x202e)) + "reversed"
	bio := CitizenBio{
		GUID: "citizen:evil",
		Education: CitizenEducationBio{
			IndustryTie: evil,
		},
		Employment: CitizenEmploymentBio{
			State:  evil,
			Sector: evil,
		},
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("RenderCitizenBio panicked on control-character input: %v", r)
			}
		}()
		buf := core.NewBuffer(120, 10)
		rect := core.Rect{X: 0, Y: 0, W: 120, H: 10}
		style := widgets.DefaultPalette.Style(widgets.TokenMoney)
		RenderCitizenBio(buf, rect, bio, true, style)

		for y := rect.Y; y < rect.Y+rect.H; y++ {
			for x := rect.X; x < rect.X+rect.W; x++ {
				r := buf.Get(x, y).Rune
				if r == 0x1b || r == 0x07 || r == '\r' || r == 0x202e {
					t.Fatalf("raw control/format character %U reached the buffer at (%d,%d) -- sanitizeRune boundary bypassed", r, x, y)
				}
			}
		}
	}()

	// GUID itself (used directly in a drawText title line) gets the same
	// treatment.
	evilGUID := "citizen:\x1b[2Jclear"
	bio2 := CitizenBio{GUID: evilGUID}
	buf2 := core.NewBuffer(80, 4)
	rect2 := core.Rect{X: 0, Y: 0, W: 80, H: 4}
	RenderCitizenBio(buf2, rect2, bio2, true, widgets.DefaultPalette.Style(widgets.TokenMoney))
	rows := renderedText(buf2, rect2)
	if strings.ContainsRune(strings.Join(rows, ""), 0x1b) {
		t.Fatal("raw ESC reached rendered text via GUID field")
	}
}
