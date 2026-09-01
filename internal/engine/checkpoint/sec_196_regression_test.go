package checkpoint

import (
	"errors"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file holds the regression test for the Destructive finding SEC-196
// on feat.checkpoint. The failure CLASS closed is "a derived identifier
// with no length bound matching its source's domain" — SEC-189 bounded the
// CreateCheckpoint instance (maxCheckpointNameLen reserves the fork-suffix
// budget), and SEC-196 is the SAME class at the sibling derivation
// nextFreeForkName/forkName in Revert: a fork checkpoint is itself a valid
// future revert target, so a fork-of-fork chain grows the derived name by
// len(".fork")+digits(seq) per level with no bound, and the deepest fork of
// a long-named checkpoint becomes unrevertible (its own fork name exceeds
// save's 255-byte manual-name limit). The fix bounds the derived fork name
// against maxSaveNameLen at derivation and rejects the revert with
// ErrForkNameTooLong BEFORE any state mutation — so the failure is loud and
// pre-mutation, not a confusing MET-E817 after a load-and-recover cycle.

// TestSEC196_ForkOfForkChainRejectedBeforeOverLimit reproduces the
// finding's exact chain: a max-length (maxCheckpointNameLen = 231-byte)
// checkpoint is reverted to itself, then to each resulting fork, growing the
// derived name 231 -> 237 -> 243 -> 249 -> 255 (+6 per level). The NEXT
// revert (to the 255-byte fork) would derive a 261-byte name and must be
// rejected with ErrForkNameTooLong BEFORE loading the target into live
// state — not fail with feat.saveux's generic MET-E817 after mutating and
// recovering (the pre-fix behaviour SEC-196 reports).
func TestSEC196_ForkOfForkChainRejectedBeforeOverLimit(t *testing.T) {
	root := t.TempDir()
	widgets := newMemParticipant("widget", entry{ID: 1, Name: "state-A", Score: 1})
	m := NewManager(root, []save.Participant{widgets}, "corr-sec196", 42)

	longName := strings.Repeat("x", maxCheckpointNameLen)
	if _, err := m.CreateCheckpoint(fixtureContext(10, 1), longName, ""); err != nil {
		t.Fatalf("CreateCheckpoint(%d-byte name) = %v, want success", len(longName), err)
	}

	// Drive the fork-of-fork chain through the public API, targeting each
	// revert's returned fork ID (never hardcoding ".forkN"), until the
	// deepest fork reaches maxSaveNameLen bytes.
	target := ID(longName)
	var deepest ID
	for len(deepest) < maxSaveNameLen {
		fork, err := m.Revert(fixtureContext(20, 1), target)
		if err != nil {
			t.Fatalf("Revert(%d-byte target) = %v, want success while the derived fork name stays within maxSaveNameLen", len(target), err)
		}
		if len(fork.ID) <= len(target) {
			t.Fatalf("fork name %d bytes did not grow past target %d bytes", len(fork.ID), len(target))
		}
		if len(fork.ID) > maxSaveNameLen {
			t.Fatalf("Revert produced an over-length fork %d bytes (maxSaveNameLen %d)", len(fork.ID), maxSaveNameLen)
		}
		deepest = fork.ID
		target = fork.ID
	}
	if len(deepest) != maxSaveNameLen {
		t.Fatalf("deepest fork = %d bytes, want exactly maxSaveNameLen %d", len(deepest), maxSaveNameLen)
	}

	// The next revert (target = the maxSaveNameLen-byte fork) derives an
	// over-length name and must be rejected with the dedicated registry
	// error. Pre-fix, this returned feat.saveux's MET-E817.
	loadsBefore := widgets.loadsCount()
	if _, err := m.Revert(fixtureContext(30, 1), deepest); !errors.Is(err, &errs.E{Code: ErrForkNameTooLong}) {
		t.Fatalf("Revert(%d-byte fork) error = %v, want ErrForkNameTooLong", len(deepest), err)
	}

	// GR#1 fail-before-mutation: the rejected revert never reached
	// feat.saveux's Load, so the participant's handler was not invoked
	// again. Pre-fix, Revert loaded the target, failed at SaveManual, then
	// reloaded the prior head — two more loads.
	if got := widgets.loadsCount(); got != loadsBefore {
		t.Fatalf("handler loads after rejected revert = %d, want unchanged %d (the rejection happened after a load)", got, loadsBefore)
	}

	// The active head is unchanged: still the maxSaveNameLen-byte fork.
	if id, err := m.CurrentID(); err != nil || id != deepest {
		t.Fatalf("CurrentID after rejected revert = %q (err %v), want the unchanged deepest fork %q", id, err, deepest)
	}

	// No over-length fork was created: exactly 5 nodes (root + 4 forks),
	// none longer than maxSaveNameLen.
	tree, err := Lineage(root)
	if err != nil {
		t.Fatalf("Lineage: %v", err)
	}
	if got := len(tree.Nodes); got != 5 {
		t.Fatalf("Lineage nodes = %d, want 5 (an over-length fork was created)", got)
	}
	for _, n := range tree.Nodes {
		if len(n.ID) > maxSaveNameLen {
			t.Fatalf("Lineage contains an over-length node %d bytes", len(n.ID))
		}
	}
}
