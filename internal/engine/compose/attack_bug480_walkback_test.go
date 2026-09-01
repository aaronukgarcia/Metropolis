package compose

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-480 independent destructive round (Opus r1). These tests attack the
// walk-back restore from angles the builder's own two tests do not cover:
// throwaway-vs-real engine equivalence, the corrupt-vs-inconsistent
// taxonomy checked BY ERROR CODE inside the compose package itself (the
// builder's only corruption proof lives in cmd/metroserve), and the dirty
// latch under -race.

// codeOf extracts the registry code from err's *errs.E, or "" when err is
// not a registry error. Attacks below dispatch on the CODE, never on
// message text (a string-match assertion would still pass if the
// skip-vs-fail-closed classification silently changed).
func codeOf(err error) string {
	var e *errs.E
	if !errors.As(err, &e) {
		return ""
	}
	return e.Code
}

// TestAttackBUG480_ThrowawayValidationMatchesRealEngineByteExact attacks the
// headline design risk in tryRestoreCandidate: a candidate is proven on a
// THROWAWAY engine built as core.NewEngine() + Wire(valE, nil) — default
// world seed, default pool size, no Deps — and then applied for real to an
// engine the caller built with entirely different options. If the throwaway
// is not a faithful proxy, a candidate could validate on the probe and then
// diverge (or fail) on the real engine, which by then has already been
// mutated by restoreFromSnapshotBytes and sealed by the first replayed
// AdvanceTicks — an unrecoverable brick, i.e. BUG-480 merely relocated.
//
// The real engine here deliberately uses a seed that is NOT the throwaway's
// default, plus a multi-domain journal (gameplay commands AND ticks) so the
// tail replay exercises the gameplay handler, not just the clock. The proof
// is byte-exact digest equality against an independent from-genesis replay
// at the SAME seed, both immediately after restore AND after a further N
// ticks (a snapshot restore that is right at t=0 but drifts afterwards is
// the classic non-tick-continuous failure this must exclude).
func TestAttackBUG480_ThrowawayValidationMatchesRealEngineByteExact(t *testing.T) {
	ctx := context.Background()
	const cadence = int64(4)
	const attackSeed = uint64(987654321) // deliberately != the throwaway default.
	const extraTicks = int64(7)

	mem := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "throwaway-equiv-480"}

	e1 := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	comp1, err := Wire(e1, &Deps{PersistStore: mem, PersistCity: city})
	if err != nil {
		t.Fatalf("Wire (producer): %v", err)
	}
	driveGameplayOnly(t, e1)
	advanceViaCommand(t, e1, cadence)
	if _, ok, err := comp1.MaybeSnapshotEvery(ctx, mem, city, cadence); err != nil || !ok {
		t.Fatalf("snapshot at tick %d: ok=%v err=%v", cadence, ok, err)
	}
	// A real journal TAIL after the snapshot: a further GAMEPLAY command
	// (so the tail replay exercises the composed gameplay handler on both
	// the throwaway probe and the real engine, not merely the clock) plus
	// more ticks.
	tailCell := protocol.CellRef{X: 1, Y: 1} // inside the tile already bought above.
	tailZone := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("attack-480-zone"),
		Kind:            protocol.KindZone,
		Payload:         protocol.ZonePayload{Cell: tailCell, ZoneType: "dwelling"},
	}
	if res := e1.HandleCommand(tailZone); !res.Accepted {
		t.Fatalf("tail Zone rejected: %+v", res.Error)
	}
	tailBuild := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("attack-480-build"),
		Kind:            protocol.KindBuild,
		Payload:         protocol.BuildPayload{Cell: tailCell, BuildingType: "dwelling"},
	}
	if res := e1.HandleCommand(tailBuild); !res.Accepted {
		t.Fatalf("tail Build rejected: %+v", res.Error)
	}
	advanceViaCommand(t, e1, 3)

	// Independent reference: full genesis replay of the durable journal at
	// the SAME seed, never touching a snapshot at all.
	eRef := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	compRef, err := Wire(eRef, nil)
	if err != nil {
		t.Fatalf("Wire (reference): %v", err)
	}
	cmds, err := RestoreCommands(ctx, mem, city)
	if err != nil {
		t.Fatalf("RestoreCommands: %v", err)
	}
	if err := replayCommands(eRef, cmds); err != nil {
		t.Fatalf("replayCommands (reference): %v", err)
	}

	// Snapshot-aware restore onto a real engine carrying the SAME
	// non-default options as the producer — while tryRestoreCandidate's
	// probe inside used a default-seeded throwaway.
	eR := core.NewEngine(core.WithWorldSeed(attackSeed), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire (restore target): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, mem, city)
	if err != nil {
		t.Fatalf("RestoreLatestSnapshotOrGenesis: %v", err)
	}
	if !usedSnapshot {
		t.Fatal("usedSnapshot = false: the snapshot path was not exercised, so this test proves nothing about throwaway equivalence")
	}
	refClock, err := eRef.Clock()
	if err != nil {
		t.Fatalf("Clock (reference): %v", err)
	}
	if tick != refClock.Tick() {
		t.Fatalf("restored tick = %d, want %d (genesis reference)", tick, refClock.Tick())
	}
	if compR.StateDigest() != compRef.StateDigest() {
		t.Fatalf("digest after snapshot restore = %x, want %x (genesis reference at the same seed) -- the default-seeded throwaway validated a candidate that does NOT reproduce on the real engine", compR.StateDigest(), compRef.StateDigest())
	}

	// Tick-continuity: both engines must advance identically AFTER restore.
	advanceViaCommand(t, eR, extraTicks)
	advanceViaCommand(t, eRef, extraTicks)
	if compR.StateDigest() != compRef.StateDigest() {
		t.Fatalf("digest after restore + %d further ticks = %x, want %x -- the restore is not tick-continuous against the genesis reference", extraTicks, compR.StateDigest(), compRef.StateDigest())
	}
}

// TestAttackBUG480_CorruptNewestNeverWalkedPast is the compose-package
// corruption proof the builder's change lacks entirely (its only
// corrupt-snapshot test lives in cmd/metroserve, so a mutation that lets
// corruption be walked past leaves this whole package green — verified: the
// r1 round mutated tryRestoreCandidate to skip corrupt payloads and
// ./internal/engine/compose stayed ok). Four distinct corruption shapes are
// each asserted to fail the WHOLE restore closed, BY ERROR CODE
// (ErrSnapshotUnpackFailed) rather than message text, and to leave the
// older, perfectly good snapshot UNUSED — the walk-back must never launder
// corruption into a silent, quiet rollback to stale state.
func TestAttackBUG480_CorruptNewestNeverWalkedPast(t *testing.T) {
	names := []string{"truncated", "bad-magic", "empty", "plain-junk"}
	payloads := map[string][]byte{
		"bad-magic":  []byte("PK\x03\x04 this is not a real zip central directory at all"),
		"empty":      {},
		"plain-junk": []byte(strings.Repeat("A", 4096)),
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const cadence = int64(4)
			mem := persist.NewMemStore()
			city := persist.CityKey{TenantID: "t", CityID: "corrupt-480-" + name}

			e1, comp1 := buildPersistedComposition(t, mem, city)
			advanceViaCommand(t, e1, cadence)
			good, ok, err := comp1.MaybeSnapshotEvery(ctx, mem, city, cadence)
			if err != nil || !ok {
				t.Fatalf("good snapshot: ok=%v err=%v", ok, err)
			}
			advanceViaCommand(t, e1, cadence)

			payload := payloads[name]
			if name == "truncated" {
				full, err := comp1.buildSnapshotBytes()
				if err != nil {
					t.Fatalf("buildSnapshotBytes: %v", err)
				}
				payload = full[:len(full)/2] // a genuinely torn zip.
			}
			bad, err := mem.PutSnapshot(ctx, city, payload)
			if err != nil {
				t.Fatalf("PutSnapshot (corrupt): %v", err)
			}
			if bad == good {
				t.Fatal("corrupt snapshot reused the good snapshot id")
			}

			eR := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
			compR, err := Wire(eR, nil)
			if err != nil {
				t.Fatalf("Wire: %v", err)
			}
			usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, mem, city)
			if err == nil {
				t.Fatalf("restore SUCCEEDED over a corrupt newest snapshot (usedSnapshot=%v tick=%d) -- corruption was walked past, which BUG-480 explicitly forbids", usedSnapshot, tick)
			}
			if got := codeOf(err); got != ErrSnapshotUnpackFailed {
				t.Fatalf("restore error code = %q, want ErrSnapshotUnpackFailed (%q) -- the corrupt-vs-tail-inconsistent distinction must be by registry code, not message text", got, ErrSnapshotUnpackFailed)
			}
			if usedSnapshot {
				t.Fatal("usedSnapshot = true on a failed restore")
			}
			// And it must NOT have been logged as a benign skip.
			for _, entry := range errs.Recent() {
				if entry.Code != ErrSnapshotSkipped {
					continue
				}
				if c, _ := entry.Ctx["city"].(string); strings.Contains(c, city.CityID) {
					t.Fatalf("corrupt snapshot was logged as ErrSnapshotSkipped (%s) -- corruption must fail closed, never be reported as a walked-past candidate", ErrSnapshotSkipped)
				}
			}
		})
	}
}

// TestAttackBUG480_TickAheadOfJournalIsSkipNotCorrupt pins the OTHER half of
// the taxonomy: a structurally VALID snapshot whose recorded tick the
// journal can never reach (the swallowed-append class) must be classified as
// a tail-inconsistency and skipped, NOT as corruption. Together with the
// test above this proves the two classes are separated by code and that
// neither has collapsed into the other.
func TestAttackBUG480_TickAheadOfJournalIsSkipNotCorrupt(t *testing.T) {
	ctx := context.Background()
	const cadence = int64(4)
	mem := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "ahead-480"}

	// A composition whose journal goes to a DIFFERENT store, so the
	// snapshot recorded tick is arbitrarily far ahead of this city journal
	// -- the extreme form of "snapshot tick ahead of journal".
	other := persist.NewMemStore()
	eSrc, compSrc := buildPersistedComposition(t, other, persist.CityKey{TenantID: "t", CityID: "other"})
	advanceViaCommand(t, eSrc, 100)
	ahead, err := compSrc.buildSnapshotBytes()
	if err != nil {
		t.Fatalf("buildSnapshotBytes: %v", err)
	}

	e1, _ := buildPersistedComposition(t, mem, city)
	advanceViaCommand(t, e1, cadence)
	if _, err := mem.PutSnapshot(ctx, city, ahead); err != nil {
		t.Fatalf("PutSnapshot (ahead): %v", err)
	}

	eR := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, mem, city)
	if err != nil {
		t.Fatalf("restore failed closed on a merely tail-INCONSISTENT snapshot (%v) -- this class must be skipped, not fatal", err)
	}
	if usedSnapshot {
		t.Fatal("usedSnapshot = true: the ahead-of-journal snapshot was USED")
	}
	if tick != cadence {
		t.Fatalf("restored tick = %d, want %d (genesis replay of the real journal)", tick, cadence)
	}
	found := false
	for _, entry := range errs.Recent() {
		if entry.Code != ErrSnapshotSkipped {
			continue
		}
		if c, _ := entry.Ctx["city"].(string); strings.Contains(c, city.CityID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s entry for the skipped ahead-of-journal candidate -- the skip was silent", ErrSnapshotSkipped)
	}
}

// TestAttackBUG480_DirtyLatchUnderRace hammers the two atomics BUG-480 added
// to persistCommandJournaler from many goroutines at once (run under -race):
// MarkDirtyLoggedOnce must have EXACTLY one winner no matter how many
// callers race it, and Dirty() must be true for every observer once any
// append has failed. A latch that leaked more than one winner would
// reintroduce the per-boundary log flood the design explicitly forbids.
func TestAttackBUG480_DirtyLatchUnderRace(t *testing.T) {
	const goroutines = 64
	mem := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "latch-race-480"}
	j := newPersistCommandJournaler(noopInnerJournaler{}, alwaysFailAppendStore{mem}, city)

	var wg sync.WaitGroup
	winners := make([]bool, goroutines)
	dirtyObserved := make([]bool, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			cmd := protocol.Command{
				ProtocolVersion: protocol.ProtocolVersion,
				CorrelationID:   protocol.NewCorrelationID(),
				Kind:            protocol.KindAdvanceTicks,
				Payload:         protocol.AdvanceTicksPayload{N: 1},
			}
			if err := j.ObserveCommand(cmd); err == nil {
				t.Errorf("ObserveCommand: want the synthetic append failure, got nil")
			}
			dirtyObserved[i] = j.Dirty()
			winners[i] = j.MarkDirtyLoggedOnce()
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, w := range winners {
		if w {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("MarkDirtyLoggedOnce had %d winners across %d racing goroutines, want exactly 1", wins, goroutines)
	}
	for i, d := range dirtyObserved {
		if !d {
			t.Fatalf("goroutine %d observed Dirty()==false after its own append failed -- the latch is not visible to the observer that set it", i)
		}
	}
	if !j.Dirty() {
		t.Fatal("Dirty() = false after every append failed")
	}
}

// noopInnerJournaler is an inner core.CommandJournaler that accepts
// everything, so ObserveCommand always reaches the durable AppendJournal
// step.
type noopInnerJournaler struct{}

func (noopInnerJournaler) ObserveCommand(protocol.Command) error { return nil }

// alwaysFailAppendStore fails EVERY AppendJournal (unlike the builder
// failingAppendStore, which fails a single Nth call via a non-atomic
// counter and is explicitly single-goroutine-only, so it cannot be used
// under -race).
type alwaysFailAppendStore struct{ persist.Store }

func (alwaysFailAppendStore) AppendJournal(context.Context, persist.CityKey, []byte) error {
	return errors.New("attack: synthetic durable-append failure")
}
