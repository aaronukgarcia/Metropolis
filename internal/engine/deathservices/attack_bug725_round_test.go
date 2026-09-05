package deathservices

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// attack_bug725_round_test.go — INDEPENDENT DESTRUCTIVE ROUND
// (opus-round-bug725, attacker != author) against BUG-725's over-length
// handoff-cursor self-correction. Round verdict: ACCEPT with fixes (the P2
// once-per-load full-stream-read cost and the two P3 comment/registry
// gaps this round found are closed in compose.go/api.go/errors.go).

// TestAttackBUG725_DedupeAbsorbsFullReReadAt5000 is attack angle 1: the
// fix's reset-to-0 re-reads the WHOLE handoff stream. Prove the only thing
// stopping a double-count is the per-citizenID body-map dedup, prove that
// dedup is actually EXERCISED (a returned ErrDuplicateDeath), and prove it
// holds at a 5,000-body backlog with conservation intact.
func TestAttackBUG725_DedupeAbsorbsFullReReadAt5000(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")

	const n = 5000
	deaths := make([]citizens.RealisedDeath, n)
	for i := range deaths {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: int64(i%12 + 1)}
	}
	if _, err := d.IntakeFromHandoff(deaths, "corr"); err != nil {
		t.Fatalf("first intake: %v", err)
	}
	snap, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.BodiesReleased != n {
		t.Fatalf("first intake released %d, want %d", snap.BodiesReleased, n)
	}
	if cur, err := d.HandoffCursor("corr"); err != nil || cur != n {
		t.Fatalf("cursor after first intake = %d (err %v), want %d", cur, err, n)
	}

	// The exact shape compose's fix produces: reset to 0, re-read the full
	// stream, resubmit it.
	if err := d.ResetHandoffCursor("corr"); err != nil {
		t.Fatalf("ResetHandoffCursor: %v", err)
	}
	_, err = d.IntakeFromHandoff(deaths, "corr")
	if err == nil {
		t.Fatal("re-intaking 5,000 already-known bodies returned NO ErrDuplicateDeath — the dedup was never exercised, so this proves nothing")
	}
	if !IsDuplicateDeath(err) {
		t.Fatalf("re-intake error is not ErrDuplicateDeath: %v", err)
	}
	snap2, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot 2: %v", err)
	}
	if snap2.BodiesReleased != n {
		t.Fatalf("CONSERVATION BROKEN: the reset-to-0 full re-read double-counted bodies, %d -> %d", n, snap2.BodiesReleased)
	}
	if cur, _ := d.HandoffCursor("corr"); cur != n {
		t.Fatalf("cursor after reset+re-read = %d, want %d", cur, n)
	}
	backlog, err := d.AwaitingBacklog("corr")
	if err != nil {
		t.Fatalf("AwaitingBacklog: %v", err)
	}
	if backlog != n {
		t.Fatalf("backlog after re-read = %d, want %d (a duplicated body would inflate it)", backlog, n)
	}
	if got := len(d.bodies); got != n {
		t.Fatalf("body map holds %d records after the re-read, want %d", got, n)
	}
	t.Logf("5,000-body full re-read absorbed by the O(1)-per-entry body-map dedup: released steady %d, backlog %d, cursor %d", snap2.BodiesReleased, backlog, n)
}

// TestAttackBUG725_CopiedReceiverNeverAdvancesCursor is attack angle 7.
// This fix gave intakeLocked a genuine EARLY RETURN (it now returns
// checkNotCopied's error) while IntakeFromHandoff still advances the cursor
// UNCONDITIONALLY afterwards. If a copied receiver could reach intakeLocked
// that is a NEW silent-drop path: cursor advances past deaths that were
// never applied. Prove IntakeFromHandoff's own guard fires first, so the
// advance is unreachable — and prove the advance would indeed be a drop if
// it were reached (the copy's cursor field is inspected directly).
func TestAttackBUG725_CopiedReceiverNeverAdvancesCursor(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	base := []citizens.RealisedDeath{{CitizenID: 11, DeathMonth: 1}, {CitizenID: 12, DeathMonth: 1}}
	if _, err := d.IntakeFromHandoff(base, "corr"); err != nil {
		t.Fatalf("baseline intake: %v", err)
	}
	wantCursor := int64(len(base))

	cp := deathServicesAPIByteCopy(d)
	before := cp.handoffCursor
	if before != wantCursor {
		t.Fatalf("copy setup: cursor %d want %d", before, wantCursor)
	}
	more := []citizens.RealisedDeath{{CitizenID: 21, DeathMonth: 2}, {CitizenID: 22, DeathMonth: 2}, {CitizenID: 23, DeathMonth: 2}}
	applied, err := cp.IntakeFromHandoff(more, "corr")
	if err == nil {
		t.Fatal("a COPIED DeathServicesAPI accepted IntakeFromHandoff — SEC-020 guard gone")
	}
	if len(applied) != 0 {
		t.Fatalf("copied receiver applied %d bodies", len(applied))
	}
	if cp.handoffCursor != before {
		t.Fatalf("SILENT DROP: a REFUSED copied-receiver IntakeFromHandoff advanced the cursor %d -> %d, "+
			"consuming %d handoff records that were never applied", before, cp.handoffCursor, len(more))
	}
	// The original must be entirely untouched.
	if cur, _ := d.HandoffCursor("corr"); cur != wantCursor {
		t.Fatalf("the copied call mutated the ORIGINAL's cursor: %d want %d", cur, wantCursor)
	}
	snap, _ := d.Snapshot("corr")
	if snap.BodiesReleased != int64(len(base)) {
		t.Fatalf("the copied call mutated the original's bodies: released=%d want %d", snap.BodiesReleased, len(base))
	}
}

// TestAttackBUG725_IntakeLockedEarlyReturnWouldDropDeaths proves the
// HAZARD documented on both IntakeFromHandoff's and intakeLocked's own doc
// comments is real if the guard ordering ever changes: calling
// intakeLocked directly on a copy (the shape a future third call site
// would have) returns zero applied with an error, and the caller's
// unconditional `+= len(deaths)` would then consume the whole page.
// Documented as a latent hazard, not a live defect — the two real call
// sites both guard first.
func TestAttackBUG725_IntakeLockedEarlyReturnWouldDropDeaths(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	cp := deathServicesAPIByteCopy(d)
	deaths := []citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}, {CitizenID: 2, DeathMonth: 1}}
	cp.mu.Lock()
	applied, err := cp.intakeLocked(deaths, "corr")
	cp.mu.Unlock()
	if err == nil {
		t.Fatal("intakeLocked on a copy returned nil error — the new early return is not wired")
	}
	if len(applied) != 0 {
		t.Fatalf("intakeLocked on a copy applied %d bodies", len(applied))
	}
	// This is the load-bearing observation: zero consumed, and
	// IntakeFromHandoff's advance is unconditional. Pin the comment's own
	// escape clause so a future change is forced to re-read it.
	t.Logf("LATENT: intakeLocked now has an early return that consumes 0 of %d deaths; "+
		"IntakeFromHandoff's `handoffCursor += len(deaths)` is unconditional, so a third "+
		"(unguarded) call site would silently drop the whole page", len(deaths))
}
