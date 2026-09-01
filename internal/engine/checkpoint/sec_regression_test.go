package checkpoint

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// This file holds the regression tests for the Destructive findings
// SEC-174 / SEC-175 / SEC-176 on feat.checkpoint. Each test reproduces the
// reported attack path and asserts the fixed behaviour:
//
//   - SEC-174: CreateCheckpoint must reject a name that already names a
//     checkpoint BEFORE any save, so the prior bundle is never overwritten
//     and a lineage cycle can never be formed.
//   - SEC-175: Revert's derived fork name must never collide with (and
//     silently save-over) an existing checkpoint named in the fork
//     namespace.
//   - SEC-176: a Revert whose fork-save/head-write fails after the Load
//     must restore the prior active head, so CurrentID and live state stay
//     consistent (no half-applied revert).
//
// The failure CLASS closed is "an identity/name accepted (or derived)
// without existence-checking, so it collides or cycles" (SEC-174/175) and
// "a mutation applied before a later fallible step, leaving partial state
// on failure" (SEC-176) — see the delivery note for the full enumeration.

// TestSEC174_RecreateRejectedNoOverwrite proves re-creating an existing
// checkpoint name is rejected and the prior bundle is left intact: with
// A (root) and B (parent A), re-creating A with parent B would re-parent A
// under B (a lineage cycle) and save-over A's bundle. Both must not happen.
func TestSEC174_RecreateRejectedNoOverwrite(t *testing.T) {
	root := t.TempDir()
	widgets := newMemParticipant("widget", entry{ID: 1, Name: "state-A", Score: 1})
	m := NewManager(root, []save.Participant{widgets}, "corr-sec174", 42)

	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}
	widgets.setState(entry{ID: 2, Name: "state-B", Score: 2})
	if _, err := m.CreateCheckpoint(fixtureContext(20, 1), "B", "A"); err != nil {
		t.Fatalf("CreateCheckpoint B: %v", err)
	}

	// Mutate the live world to a THIRD state, then attempt to re-create A
	// with parent B (where B.parent == A). A save-over would write state-C
	// into A's bundle AND re-parent A under B (a lineage cycle); both must
	// be refused.
	widgets.setState(entry{ID: 3, Name: "state-C", Score: 3})
	if _, err := m.CreateCheckpoint(fixtureContext(30, 1), "A", "B"); !errors.Is(err, &errs.E{Code: ErrNameOccupied}) {
		t.Fatalf("re-creating A error = %v, want ErrNameOccupied", err)
	}

	// The prior A bundle is NOT overwritten: A still reconstructs state-A
	// (not the state-C a save-over would have written).
	if _, _, err := m.Load("A"); err != nil {
		t.Fatalf("Load A after rejected re-create: %v", err)
	}
	if got, want := widgets.state(), []entry{{ID: 1, Name: "state-A", Score: 1}}; !entriesEqual(got, want) {
		t.Fatalf("A state after rejected re-create = %+v, want state-A (bundle was overwritten)", got)
	}

	// Lineage stays acyclic: exactly two nodes and exactly one root (A).
	// A cycle would surface zero roots (every node's parent "present" but
	// unreachable), so this assertion is the cycle detector.
	tree, err := Lineage(root)
	if err != nil {
		t.Fatalf("Lineage: %v", err)
	}
	if len(tree.Nodes) != 2 {
		t.Fatalf("Lineage nodes = %d, want 2", len(tree.Nodes))
	}
	if len(tree.Roots) != 1 || tree.Roots[0].Checkpoint.ID != "A" {
		t.Fatalf("roots = %d (want exactly A) — a cycle would surface zero roots", len(tree.Roots))
	}
}

// TestSEC175_ForkNameCollisionAvoided proves Revert skips a fork name that
// already names a player checkpoint rather than silently save-overwriting
// it: a checkpoint named A.fork1 (the first name Revert("A") would derive)
// survives the revert, and the revert produces the next free name A.fork2.
func TestSEC175_ForkNameCollisionAvoided(t *testing.T) {
	root := t.TempDir()
	widgets := newMemParticipant("widget")
	m := NewManager(root, []save.Participant{widgets}, "corr-sec175", 42)

	widgets.setState(entry{ID: 1, Name: "state-A"})
	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}
	// A player checkpoint that collides with the first derived fork name.
	widgets.setState(entry{ID: 99, Name: "state-X"})
	if _, err := m.CreateCheckpoint(fixtureContext(20, 1), "A.fork1", "A"); err != nil {
		t.Fatalf("CreateCheckpoint A.fork1: %v", err)
	}

	// Reverting to A derives A.fork1 (fork sequence 1), which now collides;
	// it must skip to A.fork2 and never clobber the player's checkpoint.
	d, err := m.Revert(fixtureContext(30, 1), "A")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if d.ID != "A.fork2" {
		t.Fatalf("revert produced %q, want A.fork2 (must skip the colliding A.fork1)", d.ID)
	}

	// The player's A.fork1 is untouched: it still holds state-X.
	if _, _, err := m.Load("A.fork1"); err != nil {
		t.Fatalf("Load A.fork1: %v", err)
	}
	if got, want := widgets.state(), []entry{{ID: 99, Name: "state-X"}}; !entriesEqual(got, want) {
		t.Fatalf("A.fork1 state after revert = %+v, want state-X (player checkpoint was clobbered)", got)
	}

	// The new fork holds the reverted state-A and is the active head.
	if id, _ := m.CurrentID(); id != "A.fork2" {
		t.Fatalf("CurrentID = %q, want A.fork2", id)
	}
	if _, _, err := m.Load("A.fork2"); err != nil {
		t.Fatalf("Load A.fork2: %v", err)
	}
	if got, want := widgets.state(), []entry{{ID: 1, Name: "state-A"}}; !entriesEqual(got, want) {
		t.Fatalf("A.fork2 state = %+v, want state-A", got)
	}
}

// TestSEC176_FailedRevertRestoresPriorHead proves a Revert whose fork-save
// fails after the Load does not leave a half-applied state: the active head
// pointer is unchanged (CurrentID still reports B) and live state is
// restored to B rather than left reverted to A.
func TestSEC176_FailedRevertRestoresPriorHead(t *testing.T) {
	root := t.TempDir()
	state := newFailSourceParticipant("widget")
	m := NewManager(root, []save.Participant{state}, "corr-sec176", 42)

	state.setState(entry{ID: 1, Name: "state-A"})
	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}
	state.setState(entry{ID: 2, Name: "state-B"})
	if _, err := m.CreateCheckpoint(fixtureContext(20, 1), "B", "A"); err != nil {
		t.Fatalf("CreateCheckpoint B: %v", err)
	}

	// Fail the fork-save Revert performs after its Load: the Load itself
	// succeeds (live state becomes A) but saveBundle fails, exercising the
	// SEC-176 recovery path.
	state.setFailSave(true)
	if _, err := m.Revert(fixtureContext(30, 1), "A"); err == nil {
		t.Fatalf("Revert succeeded despite injected fork-save failure")
	}

	// The head pointer was never advanced: CurrentID still reports B.
	if id, err := m.CurrentID(); err != nil || id != "B" {
		t.Fatalf("CurrentID after failed revert = %q (err %v), want B", id, err)
	}
	// Live state was restored to B, not left reverted to A.
	if got, want := state.state(), []entry{{ID: 2, Name: "state-B"}}; !entriesEqual(got, want) {
		t.Fatalf("live state after failed revert = %+v, want state-B (half-applied revert left it reverted)", got)
	}
}

// failSourceParticipant is a fixture save.Participant whose Source can be
// switched to fail on demand, so a test can force SaveManual (and thus
// checkpoint.saveBundle) to fail at a precise point — the injection point
// SEC-176's half-applied-revert reproduction needs. Its Handler/state
// mirror memParticipant (fixture_test.go) so Load reconstructs state
// normally; only Source (the save path) is fail-switchable.
type failSourceParticipant struct {
	mu       sync.Mutex
	kind     string
	items    []entry
	failSave bool
}

func newFailSourceParticipant(kind string, items ...entry) *failSourceParticipant {
	return &failSourceParticipant{kind: kind, items: items}
}

func (p *failSourceParticipant) Kind() string { return p.kind }

func (p *failSourceParticipant) setFailSave(b bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failSave = b
}

func (p *failSourceParticipant) setState(items ...entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.items = append([]entry(nil), items...)
}

func (p *failSourceParticipant) state() []entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]entry(nil), p.items...)
}

func (p *failSourceParticipant) Source() serialize.RecordSource {
	p.mu.Lock()
	if p.failSave {
		p.mu.Unlock()
		return func() (serialize.Record, bool, error) {
			return serialize.Record{}, false, errors.New("injected source failure (SEC-176 fixture)")
		}
	}
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

func (p *failSourceParticipant) Handler() serialize.RecordHandler {
	var loaded []entry
	return func(rec serialize.Record) error {
		var w entryWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return err
		}
		loaded = append(loaded, entry(w))
		p.mu.Lock()
		p.items = loaded
		p.mu.Unlock()
		return nil
	}
}
