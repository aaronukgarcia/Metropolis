// Package checkpoint is feat.checkpoint (FEAT-064): whole-state
// checkpoints with revert-and-fork lineage, sitting one layer above
// feat.saveux's save orchestration (internal/engine/save) and
// int.serializer's bundle format (internal/foundation/serialize). A
// checkpoint is a complete, independently-loadable state bundle — the
// same header+shards shape feat.saveux's SaveManual produces, via the
// same Participant registry — never a delta against a parent checkpoint
// (AC-1). This package adds the lineage vocabulary feat.saveux
// deliberately does not carry: explicit parentage, fork-on-revert
// branch identities, a bounded/prunable fork tree, and the metadata
// surface a future save-UI screen renders as a lineage tree.
//
// Module key: feat.checkpoint (see code.json; GUID
// 9b46faf8-d4b8-4829-9633-860eef3c5b0e)
// Spec ref: FEAT-064 BOW ruling (Aaron, 2026-08-12) — the only spec
// authority; no master-spec section names checkpoints. See
// docs/planning/acceptance/feat.checkpoint.md for the acceptance
// criteria this package is built against.
//
// # Whole-state, not delta (AC-1)
//
// [Manager.CreateCheckpoint] writes each checkpoint through
// feat.saveux's own [save.Manager.SaveManual] path: every registered
// [save.Participant] streams its full current state into an
// int.serializer bundle (header.json + shards/). A checkpoint therefore
// never depends on any other bundle being present on disk — deleting
// every other file under the root still leaves the checkpoint fully
// loadable by [Manager.Load]. This package never reimplements the save
// mechanics: it consumes [save.Participant], [save.Context],
// [save.Manager], [save.Meta], and [save.ReadMeta] verbatim (GR#3,
// GR#20).
//
// # Fork-on-revert (AC-3)
//
// Reverting to checkpoint X does not destroy the timeline it is leaving.
// [Manager.Revert] loads X back into the live participants (reusing
// feat.saveux's Load, which reconstructs state via each Participant's
// Handler) and then creates a NEW checkpoint — a fresh branch head whose
// ParentID is X — capturing the just-reverted state. The branch that was
// active before the revert is left fully intact and independently
// loadable; nothing is deleted, truncated, or rebased by the act of
// reverting itself. Pruning is a separate, bounded concern (see below).
//
// # Bounded retention (AC-6/AC-7/AC-8, Aaron's ruling)
//
// The fork tree is bounded: after every checkpoint creation or revert,
// the N most-recently-abandoned branches remain loadable and older ones
// auto-prune. N is [MaxRetainedForks], a single named constant — NOT a
// number re-typed at each call site. Its current value is a BALANCE
// PLACEHOLDER pending Aaron's balance-regime approval (placeholder +
// directional tests + delegated proposal + Aaron's row-by-row approval +
// balance pass, per the standing rule). Shape-only tests parameterise
// across several candidate values so the rule holds at any N, not just
// the checked-in number. Pruning removes only branch heads and any
// checkpoint exclusive to a pruned branch's own path — never a checkpoint
// that a still-retained branch's lineage chain passes through (AC-8,
// structural sharing). A prune failure is non-fatal to the create/revert
// that triggered it: the checkpoint is already promoted by then, so the
// failure is surfaced via [Manager.LastPruneError] rather than returned
// alongside the created checkpoint (SEC-190).
//
// # Reuse of feat.saveux and harness.replay (GR#3/GR#20)
//
// feat.saveux supplies the whole-state save/load mechanics and the
// Participant/SaveKind model; int.serializer supplies the bundle format;
// harness.replay (MOD-013) supplies the Recorder/EnginePlayer/CompareResult
// surface that fork-integrity verification (AC-12) is specified to reuse.
// AC-11/AC-12 — the per-branch command log and its replay-based integrity
// verification — are NOT built here: they are blocked on MOD-013's
// Recorder being in-memory-only (buffers with no incremental flush), the
// wrong shape for an always-on fork log (ASM-442/ASM-470). This package
// never reimplements Recorder or its fixture format; when MOD-013
// resolves the durability gap, AC-11/AC-12 will consume
// harness.replay.Recorder and [replay.EnginePlayer]/[replay.CompareResult]
// exactly as that package exposes them today.
//
// # Determinism (GR#21, AC-13/AC-14)
//
// Checkpoint bundles are byte-deterministic: this package's own lineage
// sidecar (checkpoint-meta.json) carries only a ParentID, never a
// timestamp or random value, so two checkpoints taken from the same
// deterministic state produce byte-identical bundles across every file
// (AC-13). This package never reads the wall clock and uses no wall-clock
// timer or sleeper in non-test code (AC-14) — ordering is driven by
// simulation ticks (save.Context.CreatedAtTick) and explicit parentage,
// never wall time. Numeric safety follows foundation/num (GR#16): the fork
// counter is incremented with saturating arithmetic so it can never wrap.
//
// # FEAT-065 interface note (AC-16)
//
// [Manager.CurrentID] returns the identifier of the currently-active
// checkpoint/branch without mutating checkpoint state. It exists as a
// stable, read-only accessor so a future FEAT-065 dev-mode debug console
// can attach that identifier to a captured feedback record and point back
// at exact reproducible state. This is an interface note only: FEAT-065
// owns everything about its own capture format, storage, and UI.
package checkpoint
