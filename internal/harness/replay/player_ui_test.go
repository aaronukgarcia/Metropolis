package replay

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestUIPlayerServesCannedStream is AC-3(a): a UIPlayer built from a
// fixture serves exactly its recorded results/events/deltas, then closes.
func TestUIPlayerServesCannedStream(t *testing.T) {
	dir := t.TempDir()
	r := sampleRecorder(t)
	if err := Save(dir, "ui", r, FixtureMeta{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fx, err := Load(dir, "ui")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, err := NewUIPlayer(fx)
	if err != nil {
		t.Fatalf("NewUIPlayer: %v", err)
	}

	var results int
	for range p.Results() {
		results++
	}
	var events int
	for range p.Events() {
		events++
	}
	var deltas int
	for range p.Deltas() {
		deltas++
	}
	if results != 1 || events != 1 || deltas != 1 {
		t.Fatalf("got results=%d events=%d deltas=%d, want 1 each", results, events, deltas)
	}
}

// TestUIPlayerImplementsTransportShape confirms UIPlayer's method set is
// assignable to the read-side of protocol.Transport (documentation-only
// compile check, no runtime assertions needed beyond compiling).
func TestUIPlayerImplementsTransportShape(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "shape", sampleRecorder(t), FixtureMeta{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fx, err := Load(dir, "shape")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, err := NewUIPlayer(fx)
	if err != nil {
		t.Fatalf("NewUIPlayer: %v", err)
	}
	var _ interface {
		Results() <-chan protocol.CommandResult
		Events() <-chan protocol.Event
		Deltas() <-chan protocol.Delta
	} = p
}
