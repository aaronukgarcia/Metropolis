package checkpoint

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file holds the regression tests for the Destructive findings
// SEC-188 / SEC-189 / SEC-190 on feat.checkpoint. Each test reproduces the
// reported attack path and asserts the fixed behaviour.
//
// The failure CLASSES closed:
//   - SEC-188: a collision check keyed on one artifact type (the
//     checkpoint-meta.json sidecar) when the namespace it guards (manual/)
//     actually holds several (checkpoints AND feat.saveux manual saves).
//   - SEC-189: a derived identifier (the fork name) with no length bound
//     matching its source's domain (save's 255-byte manual-name limit).
//   - SEC-190: a success value returned alongside a non-nil error,
//     contradicting the documented atomicity contract.
//
// See the delivery note for the full enumeration of each class's sibling
// sites (bundleExists in CreateCheckpoint AND nextFreeForkName; forkName
// at every call site; both prune-failure returns in CreateCheckpoint and
// Revert).

// TestSEC188_ManualSaveNotOverwrittenByCreate proves CreateCheckpoint never
// save-overs a same-named manual save (a bundle with no checkpoint-meta.json
// sidecar) that already occupies the shared manual/ namespace.
func TestSEC188_ManualSaveNotOverwrittenByCreate(t *testing.T) {
	root := t.TempDir()
	widgets := newMemParticipant("widget", entry{ID: 1, Name: "manual-save", Score: 7})
	sm := save.NewManager(root, []save.Participant{widgets}, "corr-sec188")
	if err := sm.SaveManual(fixtureContext(5, 1), "A"); err != nil {
		t.Fatalf("SaveManual A: %v", err)
	}

	m := NewManager(root, []save.Participant{widgets}, "corr-sec188")
	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); !errors.Is(err, &errs.E{Code: ErrNameOccupied}) {
		t.Fatalf("CreateCheckpoint over a manual save = %v, want ErrNameOccupied", err)
	}

	// The manual save is untouched: it still reconstructs the original
	// manual-save state, and no checkpoint sidecar was written into it.
	if _, _, err := m.Load("A"); err != nil {
		t.Fatalf("Load manual save A: %v", err)
	}
	if got, want := widgets.state(), []entry{{ID: 1, Name: "manual-save", Score: 7}}; !entriesEqual(got, want) {
		t.Fatalf("manual save state = %+v, want %+v (it was overwritten)", got, want)
	}
	if isCheckpoint(root, "A") {
		t.Fatalf("manual save A was converted into a checkpoint")
	}
}

// TestSEC188_ForkNameSkipsManualSaveCollision proves Revert's derived fork
// name skips past a same-named manual save, not just a same-named
// checkpoint (SEC-175 fixed the checkpoint case; this closes the sibling).
func TestSEC188_ForkNameSkipsManualSaveCollision(t *testing.T) {
	root := t.TempDir()
	widgets := newMemParticipant("widget", entry{ID: 1, Name: "state-A"})
	m := NewManager(root, []save.Participant{widgets}, "corr-sec188")
	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatalf("CreateCheckpoint A: %v", err)
	}

	// A manual save occupies the first derived fork name A.fork1.
	sm := save.NewManager(root, []save.Participant{widgets}, "corr-sec188")
	widgets.setState(entry{ID: 99, Name: "manual-fork1"})
	if err := sm.SaveManual(fixtureContext(20, 1), "A.fork1"); err != nil {
		t.Fatalf("SaveManual A.fork1: %v", err)
	}

	// Restore live state to A before reverting.
	widgets.setState(entry{ID: 1, Name: "state-A"})
	d, err := m.Revert(fixtureContext(30, 1), "A")
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if d.ID != "A.fork2" {
		t.Fatalf("revert produced %q, want A.fork2 (must skip the manual save A.fork1)", d.ID)
	}

	// The manual save A.fork1 is untouched.
	if _, _, err := m.Load("A.fork1"); err != nil {
		t.Fatalf("Load manual save A.fork1: %v", err)
	}
	if got, want := widgets.state(), []entry{{ID: 99, Name: "manual-fork1"}}; !entriesEqual(got, want) {
		t.Fatalf("manual save A.fork1 state = %+v, want manual-fork1 (it was overwritten)", got)
	}
}

// TestSEC189_LongNameRejectedAtCreate proves a 250-char checkpoint name (the
// finding's repro) is rejected at create rather than created-but-unrevertible.
func TestSEC189_LongNameRejectedAtCreate(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-sec189")

	for _, name := range []string{
		strings.Repeat("x", 250),                    // the finding's exact repro
		strings.Repeat("x", maxCheckpointNameLen+1), // one past the boundary
	} {
		if _, err := m.CreateCheckpoint(fixtureContext(1, 1), name, ""); !errors.Is(err, &errs.E{Code: ErrCheckpointNameTooLong}) {
			t.Fatalf("CreateCheckpoint(%d-char name) error = %v, want ErrCheckpointNameTooLong", len(name), err)
		}
	}
}

// TestSEC189_MaxLenNameAcceptedAndRevertible proves the boundary itself is
// accepted AND revertible — the create-at and revert-at domains agree.
func TestSEC189_MaxLenNameAcceptedAndRevertible(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-sec189")

	longName := strings.Repeat("x", maxCheckpointNameLen)
	if _, err := m.CreateCheckpoint(fixtureContext(1, 1), longName, ""); err != nil {
		t.Fatalf("CreateCheckpoint(maxLen) = %v, want success", err)
	}
	if _, err := m.Revert(fixtureContext(2, 2), ID(longName)); err != nil {
		t.Fatalf("Revert(maxLen checkpoint) = %v, want success (name must be revertible at maxCheckpointNameLen)", err)
	}
}

// TestSEC189_ForkNameNeverExceedsLimit pins the length invariant directly:
// forkName of the longest accepted target, at any fork sequence up to the
// widest int64, never exceeds the reserved budget — so the derived
// identifier's domain can never outgrow its source's domain.
func TestSEC189_ForkNameNeverExceedsLimit(t *testing.T) {
	target := ID(strings.Repeat("x", maxCheckpointNameLen))
	budget := maxCheckpointNameLen + len(forkNamePrefix) + maxForkSeqDigits
	for _, seq := range []int64{0, 1, 9, 10, 999_999_999, math.MaxInt64} {
		if got := len(forkName(target, seq)); got > budget {
			t.Fatalf("forkName(maxLen target, seq=%d) length = %d, exceeds budget %d", seq, got, budget)
		}
	}
}

// TestSEC190_PruneFailureNonFatal_CreateCheckpoint proves a prune failure no
// longer returns a promoted checkpoint alongside a non-nil error: the
// checkpoint is returned with a nil error, and the failure is surfaced via
// LastPruneError.
func TestSEC190_PruneFailureNonFatal_CreateCheckpoint(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-sec190")
	if err := m.SetMaxRetainedForks(100); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatal(err)
	}
	for _, b := range []struct {
		name string
		tick int64
	}{{"B", 20}, {"C", 30}, {"D", 40}, {"E", 50}, {"F", 60}} {
		if _, err := m.CreateCheckpoint(fixtureContext(b.tick, 1), b.name, "A"); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SetMaxRetainedForks(1); err != nil {
		t.Fatal(err)
	}

	orig := pruneRename
	pruneRename = func(oldpath, newpath string) error { return errors.New("injected prune failure") }
	defer func() { pruneRename = orig }()

	cp, err := m.CreateCheckpoint(fixtureContext(70, 1), "G", "A")
	if err != nil {
		t.Fatalf("CreateCheckpoint error = %v, want nil (prune failure is non-fatal)", err)
	}
	if cp.ID != "G" || !cp.Active {
		t.Fatalf("CreateCheckpoint returned %+v, want a promoted active G", cp)
	}
	if !errors.Is(m.LastPruneError(), &errs.E{Code: ErrPruneFailed}) {
		t.Fatalf("LastPruneError = %v, want ErrPruneFailed", m.LastPruneError())
	}
	if id, _ := m.CurrentID(); id != "G" {
		t.Fatalf("CurrentID = %q, want G (checkpoint was actually promoted)", id)
	}
}

// TestSEC190_PruneFailureNonFatal_Revert proves the identical contract holds
// for Revert: a prune failure does not make the fork look un-created.
func TestSEC190_PruneFailureNonFatal_Revert(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root, []save.Participant{newMemParticipant("widget")}, "corr-sec190")
	if err := m.SetMaxRetainedForks(100); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), "A", ""); err != nil {
		t.Fatal(err)
	}
	for _, b := range []struct {
		name string
		tick int64
	}{{"B", 20}, {"C", 30}, {"D", 40}, {"E", 50}, {"F", 60}} {
		if _, err := m.CreateCheckpoint(fixtureContext(b.tick, 1), b.name, "A"); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.SetMaxRetainedForks(1); err != nil {
		t.Fatal(err)
	}

	orig := pruneRename
	pruneRename = func(oldpath, newpath string) error { return errors.New("injected prune failure") }
	defer func() { pruneRename = orig }()

	d, err := m.Revert(fixtureContext(70, 1), "A")
	if err != nil {
		t.Fatalf("Revert error = %v, want nil (prune failure is non-fatal)", err)
	}
	if d.ID == "" || !d.Active {
		t.Fatalf("Revert returned %+v, want a promoted active fork", d)
	}
	if !errors.Is(m.LastPruneError(), &errs.E{Code: ErrPruneFailed}) {
		t.Fatalf("LastPruneError = %v, want ErrPruneFailed", m.LastPruneError())
	}
	if id, _ := m.CurrentID(); id != d.ID {
		t.Fatalf("CurrentID = %q, want the fork %q", id, d.ID)
	}
}

// TestMaxCheckpointNameLenTracksSaveManualLimit is the weakness-pattern-#2
// drift test for the duplicated save-name limit: it probes feat.saveux's
// actual manual-save name limit (which this package cannot import, since
// save.maxSaveNameLen is unexported) and asserts maxCheckpointNameLen
// reserves exactly the fork-suffix budget on top of it — so a change to
// save's limit fails here rather than silently producing unrevertible
// checkpoints.
func TestMaxCheckpointNameLenTracksSaveManualLimit(t *testing.T) {
	root := t.TempDir()
	sm := save.NewManager(root, []save.Participant{newMemParticipant("w")}, "corr-drift")

	// Binary-search save's real manual-name limit: the largest n SaveManual
	// accepts.
	limit := 0
	lo, hi := 0, 1024
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if sm.SaveManual(fixtureContext(1, 1), strings.Repeat("a", mid)) == nil {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	limit = lo

	if maxSaveNameLen != limit {
		t.Fatalf("maxSaveNameLen = %d, want %d (save's real manual-name limit) — the bounds have drifted; update maxSaveNameLen in meta.go", maxSaveNameLen, limit)
	}
	if want := limit - len(forkNamePrefix) - maxForkSeqDigits; maxCheckpointNameLen != want {
		t.Fatalf("maxCheckpointNameLen = %d, want %d (save's manual-name limit %d minus the fork-suffix budget %d+%d) — the bounds have drifted; update maxCheckpointNameLen in meta.go", maxCheckpointNameLen, want, limit, len(forkNamePrefix), maxForkSeqDigits)
	}
}
