package why

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// refused applies raw to a screen holding the good sample and asserts the
// patch is refused: the frame is byte-identical to the one before.
func assertRefused(t *testing.T, name string, raw json.RawMessage) {
	t.Helper()
	s := sampleScreen(t)
	before := joined(frame(s, 80, 24))
	apply(s, raw)
	after := joined(frame(s, 80, 24))
	if before != after {
		t.Fatalf("%s: a refused patch changed the frame\n--- before\n%s\n--- after\n%s", name, before, after)
	}
	if _, err := decodePatch(raw); err == nil {
		t.Fatalf("%s: decodePatch accepted it", name)
	}
}

func TestWire_RefusedPatchesKeepTheLastGoodFrame(t *testing.T) {
	good := samplePatch(t)

	mut := func(f func(p *Patch)) json.RawMessage {
		p := clonePatch(good)
		f(&p)
		return mustMarshal(t, p)
	}
	cases := map[string]json.RawMessage{
		"not json":          json.RawMessage(`{"schemaVersion":1,"known":true,"levers":[`),
		"empty":             json.RawMessage(``),
		"json null":         json.RawMessage(`null`),
		"wrong type":        json.RawMessage(`{"schemaVersion":1,"known":"yes"}`),
		"schema 2":          mut(func(p *Patch) { p.SchemaVersion = 2 }),
		"schema 0":          mut(func(p *Patch) { p.SchemaVersion = 0 }),
		"nine levers":       mut(func(p *Patch) { p.Levers = p.Levers[:9] }),
		"eleven levers":     mut(func(p *Patch) { p.Levers = append(p.Levers, Lever{}) }),
		"one modifier":      mut(func(p *Patch) { p.Modifiers = p.Modifiers[:1] }),
		"value over 100":    mut(func(p *Patch) { p.Levers[0].Value = 150 }),
		"value under 0":     mut(func(p *Patch) { p.Levers[0].Value = -1 }),
		"points huge":       mut(func(p *Patch) { p.Levers[0].Points = 1000 }),
		"levers swapped":    mut(func(p *Patch) { p.Levers[0], p.Levers[1] = p.Levers[1], p.Levers[0] }),
		"modifiers swapped": mut(func(p *Patch) { p.Modifiers[0], p.Modifiers[1] = p.Modifiers[1], p.Modifiers[0] }),
		"bad limitedBy":     mut(func(p *Patch) { p.LimitedBy = "weather" }),
		"bad trend":         mut(func(p *Patch) { p.ReputationTrend = "soaring" }),
		"no neutral":        mut(func(p *Patch) { p.AWorld, p.Neutral = nil, nil }),
		"negative room":     mut(func(p *Patch) { p.Room = -1 }),
		"negative month":    mut(func(p *Patch) { p.Month = -3 }),
		"reputation huge":   mut(func(p *Patch) { p.Reputation = 500 }),
		"too many parts": mut(func(p *Patch) {
			p.Levers[0].Parts = make([]Part, maxParts+1)
		}),
		"oversized": append(append(json.RawMessage(`{"schemaVersion":1,"known":false,"pad":"`),
			bytes.Repeat([]byte("x"), maxPatchWireBytes)...), []byte(`"}`)...),
	}
	for name, raw := range cases {
		assertRefused(t, name, raw)
	}
}

func TestWire_UnknownSubscriptionDropped(t *testing.T) {
	s := boundScreen()
	s.ApplyDelta(protocol.Delta{SubscriptionID: "other", Patch: fixture(t, "sample.json")})
	if s.HaveData() {
		t.Fatal("a delta for an unbound subscription was applied")
	}
	s.UnbindSubscription("sub-why")
	apply(s, fixture(t, "sample.json"))
	if s.HaveData() {
		t.Fatal("a delta after unbind was applied")
	}
}

func TestWire_AliasSpellingsAccepted(t *testing.T) {
	raw := json.RawMessage(`{"schemaVersion":1,"known":true,"month":2,"neutral":40,"score":41,"net":1,
	 "levers":[
	  {"name":"Housing","built":true,"value":50,"points":1},
	  {"name":"Jobs","built":false},
	  {"name":"Services","built":false},
	  {"name":"Health care","built":false},
	  {"name":"Policy","built":false},
	  {"name":"Shops","built":false},
	  {"name":"Parks","built":false},
	  {"name":"Transport links","built":false},
	  {"name":"Congestion","built":false},
	  {"name":"Tourism","built":false}],
	 "wellbeingFactor":1,"wanted":1,"room":9,"limitedBy":"transport","inflow":1,"outflow":0}`)
	p, err := decodePatch(raw)
	if err != nil {
		t.Fatalf("alias patch refused: %v", err)
	}
	s := boundScreen()
	apply(s, raw)
	got := joined(frame(s, 80, 24))
	for _, want := range []string{"Wanted 1, room for 9 (limit: transport links), moved in 1", "Safety", "Environment"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q\n%s", want, got)
		}
	}
	if in, _ := arrivals(&p); in == nil || *in != 1 {
		t.Fatal("inflow alias not read")
	}
	// No modifiers sent: both lines read not built yet.
	if n := strings.Count(got, "not built yet"); n != 9+2+1 {
		t.Fatalf("not built yet = %d\n%s", n, got)
	}
}

func TestWire_LongLabelsAreClipped(t *testing.T) {
	p := samplePatch(t)
	p.Levers[0].Parts = []Part{{K: strings.Repeat("é", 500), V: 1}}
	s := boundScreen()
	apply(s, mustMarshal(t, p))
	for _, l := range frame(s, 80, 24) {
		if len([]rune(l)) > 80 {
			t.Fatalf("line wider than 80: %q", l)
		}
	}
	snap, ok := s.Snapshot()
	if !ok || len(snap.Levers[0].Parts[0].K) > maxText {
		t.Fatalf("label not clipped to %d bytes", maxText)
	}
}

func TestSnapshot_IsADeepCopy(t *testing.T) {
	s := sampleScreen(t)
	a, _ := s.Snapshot()
	a.Levers[0].Value = 1
	a.Levers[0].Parts[0].V = 999
	*a.AWorld = 5
	b, _ := s.Snapshot()
	if b.Levers[0].Value != 71 || b.Levers[0].Parts[0].V != 29 || *b.AWorld != 40 {
		t.Fatal("Snapshot shares memory with the screen")
	}
}

func TestSubscribe_SendsTheViewName(t *testing.T) {
	s := New("c")
	var got protocol.Command
	if err := s.Subscribe(func(c protocol.Command) error { got = c; return nil }); err != nil {
		t.Fatal(err)
	}
	pl, ok := got.Payload.(protocol.SubscribePayload)
	if got.Kind != protocol.KindSubscribe || !ok || pl.ViewName != "f5.why" {
		t.Fatalf("command = %+v", got)
	}
}

// --- copy guard ---------------------------------------------------------

func screenCopy(s *Screen) *Screen {
	c := new(Screen)
	*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(c)) =
		*(*[unsafe.Sizeof(Screen{})]byte)(unsafe.Pointer(s))
	return c
}

func TestScreen_CopyDetectedAndRejected(t *testing.T) {
	s := sampleScreen(t)
	c := screenCopy(s)

	err := c.Subscribe(func(protocol.Command) error { return nil })
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrScreenCopied {
		t.Fatalf("Subscribe on a copy: %v", err)
	}
	if c.HaveData() {
		t.Error("HaveData on a copy returned true")
	}
	if _, ok := c.Snapshot(); ok {
		t.Error("Snapshot on a copy returned ok")
	}
	c.BindSubscription("x")
	c.UnbindSubscription("x")
	c.ApplyDelta(protocol.Delta{SubscriptionID: "sub-why", Patch: fixture(t, "nodata.json")})
	if !s.HaveData() {
		t.Fatal("original lost its data")
	}
	if _, ok := s.Snapshot(); !ok {
		t.Fatal("a delta on the copy reached the original")
	}
	rows := frame(c, 80, 24)
	if len(nonEmpty(rows)) != 0 {
		t.Fatalf("a struct-copied receiver drew something:\n%s", joined(rows))
	}
}

// --- package hygiene ------------------------------------------------------

func TestNoWallClockNoEngineImportNoMapRange(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"time.Now(", "internal/engine", "math/rand", "range map"} {
			if bytes.Contains(b, []byte(bad)) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
	}
}
