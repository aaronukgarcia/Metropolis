package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
)

// FEAT-1972079852 increment 4 remainder — GR#17 status surface for the
// engine-owns-journal seam.
//
// # What is already built (do not re-build)
//
// The core inc4 ask — "compose constructs a replay.Recorder and calls
// engine.SetCommandJournaler before the first command and before seal" —
// landed in 447e4044 (2026-08-31, rounded ACCEPT): compose.go's Wire()
// resolves Deps.CommandJournaler (defaulting to a freshly constructed
// *replay.NewRecorder() — compose.go, right after
// e.SetGameplayCommandHandler) and calls e.SetCommandJournaler(journaler)
// unconditionally, before the registrationOrder loop and therefore before
// any AdvanceTicks/seal can occur. Composition.Journaler() (compose.go)
// exposes the same instance. feat_1972079852_inc4_journaler_test.go
// already proves: exactly-once + accepted-order journaling
// (TestWire_AcceptedCommandIsJournaled), rejected commands excluded
// (TestWire_RejectedCommandIsNotJournaled), determinism across two boots
// (TestWire_JournalerDeterministicAcrossBoots), and the Deps override seam
// (TestWire_DefaultJournalerCanBeOverridden). This file adds ONLY what was
// still missing: the durability seam's status surface and the
// determinism-across-pool-sizes / replay-equivalence proof, in
// journal_wire_test.go alongside this file — compose.go itself needed NO
// further edit for either.
//
// # The persist-path option (this item's brief asked for one; here is why
// it is not duplicated)
//
// The brief for this increment describes "a replay.Recorder (persist path
// from the composition's persist dir if configured, in-memory otherwise)"
// with Save() called at snapshot boundaries and on shutdown. That EXACT
// shape already exists, built by a different, larger epic
// (FEAT-1972079936, cmd/metroserve/persist.go +
// internal/engine/compose/persistjournal.go), and is STRICTLER than what
// this item's brief asked for: when Deps.PersistStore is non-nil, Wire()
// wraps the resolved journaler (the same *replay.Recorder or override
// this file documents) in newPersistCommandJournaler
// (persistjournal.go), which durably appends EVERY accepted command to
// the configured internal/persist.Store (a DiskStore under metroserve's
// -persist-dir) synchronously, in HandleCommand's own call — not on a
// periodic Save() cadence. A durable-append failure HALTS the engine
// (BUG-472's "HALT + SURFACE" ruling, Aaron 2026-09-01, commands.go
// journalAccepted/latchPersistHalt) rather than being merely logged and
// continuing, which is a STRONGER guarantee than "loud and non-fatal to
// the tick": Aaron's later, ratified ruling supersedes this item's
// original brief on that specific point, and JournalStatus (below)
// reports the halt rather than re-litigating it.
//
// Building a SECOND, independent fixture-file persistence path for the
// Recorder (a periodic Save(dir, name, rec, meta) call keyed off
// MaybeSnapshotEvery's cadence) would duplicate this already-shipped,
// already-tested mechanism and violate GR#3 (Single Source of Truth) —
// two independent "is the journal durable" answers for the same seam,
// each capable of drifting from the other. The seam this item's brief
// actually needs — "never a hardcoded path, document the option" — is
// Deps.PersistStore itself (persist dir supplied by the caller,
// metroserve's -persist-dir flag; nil/unset means in-memory only,
// exactly BL1's ASM-470-ratified default, FEAT-2326609756 still open P2
// for a future auto-save-every-N-ticks scheme layered ON TOP of
// PersistStore's per-command durability rather than replacing it).
//
// # JournalStatus (GR#17)
//
// JournalStatus is the status surface every service with a user-visible
// status field must carry (GR#17): entry count from the concrete
// Recorder when one is wired (a caller-injected CommandJournaler that is
// not a *replay.Recorder — e.g. a test spy — reports EntriesKnown=false
// rather than guessing), and the engine's own persist-halt state
// (core.Engine.PersistHalted(), commands.go) surfaced verbatim — the
// SAME underlying e.persistHalt EngineStatusView already reads (GR#3: no
// second source of truth for "did the durable append fail").
type JournalStatus struct {
	// EntriesKnown is true when the wired journaler is a *replay.Recorder
	// (the common case — either compose's own default or a
	// Deps.CommandJournaler override that happens to also be a
	// *replay.Recorder) and Entries/EntriesErr are therefore meaningful.
	// A caller-injected journaler of some other concrete type (a test
	// spy, or a future non-Recorder implementation) leaves this false and
	// Entries at zero rather than reporting a silently wrong count.
	EntriesKnown bool

	// Entries is the number of records the Recorder has captured so far
	// (Recorder.Len()), valid only when EntriesKnown is true.
	Entries int

	// EntriesErr is set when EntriesKnown is true but Len() itself failed
	// (SEC-037: a struct-copied Recorder rejects Len() rather than
	// reporting 0 — see record.go). A non-nil EntriesErr with
	// EntriesKnown true means "we have a Recorder but it refused to
	// answer", distinct from "no Recorder to ask" (EntriesKnown false).
	EntriesErr error

	// PersistHalted mirrors core.Engine.PersistHalted(): true once a
	// durable-journal append has failed and the engine has latched
	// BUG-472's halt (every command after the first failure is rejected
	// with the SAME code/correlation below, never a fresh one).
	PersistHalted bool

	// PersistHaltCode and PersistHaltCorrelationID are the ORIGINAL
	// failed append's registry code and correlation ID (Aaron's Q100011
	// ruling — the actual code+correlation, not a generic message). Both
	// are the empty string when PersistHalted is false.
	PersistHaltCode          string
	PersistHaltCorrelationID string
}

// JournalStatus reports the engine-owns-journal seam's current health
// (GR#17): how many commands the wired journaler has captured (when it is
// the concrete *replay.Recorder compose constructs or a caller override
// of that same type) and whether the engine's durable-append halt
// (BUG-472) has latched. c.state.e is the same *core.Engine SetCommandJournaler
// was called on inside Wire — JournalStatus never re-derives that state
// via a second path (GR#3).
func (c *Composition) JournalStatus() JournalStatus {
	var st JournalStatus
	if rec, ok := c.state.journaler.(*replay.Recorder); ok {
		st.EntriesKnown = true
		n, err := rec.Len()
		st.Entries = n
		st.EntriesErr = err
	}
	if c.state.e != nil {
		code, corrID, halted := c.state.e.PersistHalted()
		st.PersistHalted = halted
		st.PersistHaltCode = code
		st.PersistHaltCorrelationID = corrID
	}
	return st
}
