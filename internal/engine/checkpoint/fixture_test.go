package checkpoint

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// entry is the fixture domain record this package's tests stand in for a
// real engine module's saved state (AC-1 requires non-trivial state in at
// least two registered participants). It is not registered on
// save.DefaultParticipants — that stays empty until a real domain module
// lands, mirroring feat.saveux's own fixture policy.
type entry struct {
	ID    int
	Name  string
	Score float64
}

// entryWire is entry's save/wire projection — every exported entry field
// has a named counterpart here (AC-2/AC-18's opt-out parity discipline,
// demonstrated against the fixture type).
type entryWire struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

// memParticipant is a fixture save.Participant wrapping an in-memory slice
// of entries. Source snapshots the slice at call time (deterministic,
// ordered); Handler reconstructs into the live slice so a test can compare
// pre-save and post-load state field-by-field.
type memParticipant struct {
	mu    sync.Mutex
	kind  string
	items []entry
	// loads counts how many records this participant's Handler has
	// reconstructed since construction — the observable trace of a
	// feat.saveux Load. The SEC-196 test uses it to prove a rejected
	// Revert fails BEFORE any load (GR#1: fail before mutation).
	loads int
}

func newMemParticipant(kind string, items ...entry) *memParticipant {
	return &memParticipant{kind: kind, items: items}
}

func (p *memParticipant) Kind() string { return p.kind }

func (p *memParticipant) Source() serialize.RecordSource {
	p.mu.Lock()
	items := append([]entry(nil), p.items...)
	p.mu.Unlock()

	idx := 0
	return func() (serialize.Record, bool, error) {
		if idx >= len(items) {
			return serialize.Record{}, false, nil
		}
		data, err := json.Marshal(entryWire(items[idx]))
		if err != nil {
			return serialize.Record{}, false, err
		}
		idx++
		return serialize.Record{Kind: p.kind, Data: data}, true, nil
	}
}

func (p *memParticipant) Handler() serialize.RecordHandler {
	var loaded []entry
	return func(rec serialize.Record) error {
		var w entryWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return err
		}
		loaded = append(loaded, entry(w))
		p.mu.Lock()
		p.items = loaded
		p.loads++
		p.mu.Unlock()
		return nil
	}
}

// loadsCount returns the number of records this participant's Handler has
// reconstructed (the observable trace of feat.saveux Load calls).
func (p *memParticipant) loadsCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loads
}

func (p *memParticipant) setState(items ...entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.items = append([]entry(nil), items...)
}

func (p *memParticipant) state() []entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]entry(nil), p.items...)
}

func fixtureContext(tick, month int64) save.Context {
	return save.Context{
		WorldSeed:     42,
		CreatedAtTick: tick,
		GameMonth:     month,
		AppVersion:    "test-build",
		DebugTouched:  false,
	}
}

func entriesEqual(a, b []entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestEntryWireFieldsMatchEntry is the AC-2/AC-18-shaped field-parity drift
// test every registered Participant's owning package must carry,
// demonstrated here against the fixture type (a template/proof, since
// memParticipant is not itself a production registration).
func TestEntryWireFieldsMatchEntry(t *testing.T) {
	domain := entry{ID: 1, Name: "x", Score: 1.5}
	data, err := json.Marshal(entryWire(domain))
	if err != nil {
		t.Fatalf("marshal entryWire: %v", err)
	}
	var back entryWire
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal entryWire: %v", err)
	}
	if back.ID != domain.ID || back.Name != domain.Name || back.Score != domain.Score {
		t.Fatalf("entryWire round-trip mismatch: got %+v, want %+v", back, domain)
	}
}
