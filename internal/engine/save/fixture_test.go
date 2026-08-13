package save

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// widget and gadget are two independent domain-state fixtures, standing
// in for two different real engine modules' saved state (AC-3/AC-4/AC-5
// require "non-trivial state in at least two different registered
// participants"). Neither is registered on DefaultParticipants — see
// participant.go's doc comment for why that stays empty until a real
// domain module lands.
type widget struct {
	ID    int
	Name  string
	Score float64
}

// widgetWire is widget's save/wire projection — every exported widget
// field must have a named counterpart here, or an explicit exclusion
// (AC-2/AC-18's opt-out policy, ASM #2). Nothing on widget is currently
// excluded.
type widgetWire struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

// TestWidgetWireFieldsMatchWidget is the AC-2-shaped field-parity drift
// test this package's doc.go describes as the obligation every
// registered Participant's owning package must carry, demonstrated here
// against the fixture type used by this package's own tests (widget is
// not itself a registered production Participant — DefaultParticipants
// stays empty until a real domain module registers one, per
// participant.go's doc comment — so this test is a template/proof the
// pattern works, not something AC-2's registered-participant-count check
// requires today).
func TestWidgetWireFieldsMatchWidget(t *testing.T) {
	domainType := reflect.TypeOf(widget{})
	wireType := reflect.TypeOf(widgetWire{})

	if domainType.NumField() != wireType.NumField() {
		t.Fatalf("widget has %d exported fields but widgetWire has %d -- every exported widget field must have a named counterpart on widgetWire or an explicit, commented exclusion (AC-2/AC-18)", domainType.NumField(), wireType.NumField())
	}
	for i := 0; i < domainType.NumField(); i++ {
		df := domainType.Field(i)
		wf, ok := wireType.FieldByName(df.Name)
		if !ok {
			t.Fatalf("widget field %q has no corresponding widgetWire.%s", df.Name, df.Name)
		}
		if wf.Type.Kind() != df.Type.Kind() {
			t.Fatalf("widgetWire.%s has kind %s, want %s to match widget.%s", wf.Name, wf.Type.Kind(), df.Type.Kind(), df.Name)
		}
	}
}

func widgetToRecord(w widget) serialize.Record {
	data, err := json.Marshal(widgetWire(w))
	if err != nil {
		panic(err)
	}
	return serialize.Record{Kind: "widget", Data: data}
}

// widgetParticipant is a fixture Participant wrapping an in-memory slice
// of widgets. Source snapshots the slice at call time (deterministic,
// ordered); Handler reconstructs into a fresh slice so a test can
// compare pre-save and post-load state field-by-field.
type widgetParticipant struct {
	mu    sync.Mutex
	items []widget

	// blockOnFirstSource, if non-nil, is closed by Source's caller
	// synchronization in a concurrency test to hold a save "in flight"
	// deterministically (AC-11's concurrency test needs a controllable
	// overlap window rather than relying on real timing).
	blockOnFirstSource chan struct{}
	releaseSource      chan struct{}
}

func newWidgetParticipant(items ...widget) *widgetParticipant {
	return &widgetParticipant{items: items}
}

func (p *widgetParticipant) Kind() string { return "widget" }

func (p *widgetParticipant) Source() serialize.RecordSource {
	p.mu.Lock()
	items := append([]widget(nil), p.items...)
	p.mu.Unlock()

	idx := 0
	firstCall := true
	return func() (serialize.Record, bool, error) {
		if firstCall && p.blockOnFirstSource != nil {
			firstCall = false
			close(p.blockOnFirstSource)
			<-p.releaseSource
		}
		if idx >= len(items) {
			return serialize.Record{}, false, nil
		}
		rec := widgetToRecord(items[idx])
		idx++
		return rec, true, nil
	}
}

func (p *widgetParticipant) Handler() serialize.RecordHandler {
	var loaded []widget
	return func(rec serialize.Record) error {
		var w widgetWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return err
		}
		loaded = append(loaded, widget(w))
		p.mu.Lock()
		p.items = loaded
		p.mu.Unlock()
		return nil
	}
}

func (p *widgetParticipant) State() []widget {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]widget(nil), p.items...)
}

// gadget is the second, independent fixture domain type (AC-3/4/5's
// "at least two different registered participants" requirement).
type gadget struct {
	SerialNo string
	Weight   int
}

type gadgetWire struct {
	SerialNo string `json:"serialNo"`
	Weight   int    `json:"weight"`
}

type gadgetParticipant struct {
	mu    sync.Mutex
	items []gadget
}

func newGadgetParticipant(items ...gadget) *gadgetParticipant {
	return &gadgetParticipant{items: items}
}

func (p *gadgetParticipant) Kind() string { return "gadget" }

func (p *gadgetParticipant) Source() serialize.RecordSource {
	p.mu.Lock()
	items := append([]gadget(nil), p.items...)
	p.mu.Unlock()

	idx := 0
	return func() (serialize.Record, bool, error) {
		if idx >= len(items) {
			return serialize.Record{}, false, nil
		}
		g := items[idx]
		idx++
		data, err := json.Marshal(gadgetWire(g))
		if err != nil {
			return serialize.Record{}, false, err
		}
		return serialize.Record{Kind: "gadget", Data: data}, true, nil
	}
}

func (p *gadgetParticipant) Handler() serialize.RecordHandler {
	var loaded []gadget
	return func(rec serialize.Record) error {
		var g gadgetWire
		if err := json.Unmarshal(rec.Data, &g); err != nil {
			return err
		}
		loaded = append(loaded, gadget(g))
		p.mu.Lock()
		p.items = loaded
		p.mu.Unlock()
		return nil
	}
}

func (p *gadgetParticipant) State() []gadget {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]gadget(nil), p.items...)
}

// erroringParticipant's Source fails after N successfully-produced
// records, for AC-13's forced-mid-write-failure test.
type erroringParticipant struct {
	kind      string
	failAfter int
}

func (p *erroringParticipant) Kind() string { return p.kind }

func (p *erroringParticipant) Source() serialize.RecordSource {
	n := 0
	return func() (serialize.Record, bool, error) {
		if n >= p.failAfter {
			return serialize.Record{}, false, fmt.Errorf("erroringParticipant: forced failure after %d records", p.failAfter)
		}
		n++
		data, _ := json.Marshal(map[string]int{"n": n})
		return serialize.Record{Kind: p.kind, Data: data}, true, nil
	}
}

func (p *erroringParticipant) Handler() serialize.RecordHandler {
	return func(serialize.Record) error { return nil }
}

func fixtureContext(tick, month int64) Context {
	return Context{
		WorldSeed:     42,
		CreatedAtTick: tick,
		GameMonth:     month,
		AppVersion:    "test-build",
		DebugTouched:  false,
	}
}
