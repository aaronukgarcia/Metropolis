// Package save is feat.saveux (FEAT-011): the save/load orchestration
// layer that sits above int.serializer's StateSerializer/bundle contract
// (internal/foundation/serialize). It owns the participant registry each
// domain module registers its saved state through, the three save-trigger
// paths, atomic/interrupted-write safety, and corrupted/older-save
// handling on load — it does not decode or interpret any citizen/
// building/market record itself (that belongs to the owning engine
// module) and does not build the F10 save/load UI screen (that consumes
// this package's List/Load/SaveManual surface from a future
// internal/ui/screens/... item).
//
// Module key: feat.saveux (see code.json; GUID
// d50904e1-5eda-42b6-bdc7-e075d4648e20)
// Spec ref: §3 (Time — "Save points: manual anytime + autosave every game
// year (rolling 10) + milestone saves", line 131; the fixed monthly-tick
// phase list, lines 127-129); §14 (Debug Mode — "Fixed RNG seed per
// save", debug-touched save-header flagging, M0-ENG §3); A3
// (Serialization amendment — StateSerializer, NDJSON canonical +
// binary at scale, lossless metctl export); §4 (Population Scale & the
// Milestone Ladder, lines 137-157 — the 13-tier Wilderness->Centopolis
// table this package's Tiers slice mirrors).
//
// # The three save-kinds and their triggers
//
//   - Manual ([Manager.SaveManual]): fires whenever the player asks, at
//     any tick-phase boundary, under any game speed. Named by the
//     caller; never pruned.
//   - Autosave ([Manager.Autosave]): fires exactly once per completed
//     game year, off the simulation's OWN year-boundary event/tick
//     count — never a real-world timer (AC-4/AC-15: this package never
//     imports "time" for triggering). Retains at most 10 bundles,
//     pruning the oldest ONLY after the new one is written and
//     confirmed serialize.ValidateBundle-clean (AC-4/AC-13's ordering
//     guarantee — see "Atomic promotion" below).
//   - Milestone ([Manager.Milestone]): fires when the simulated
//     population crosses one of the Tiers ladder's 13 thresholds
//     (§4). Distinct from the autosave rotation and never pruned by it.
//     This package does not itself detect a tier crossing — that is
//     §4's owning module (engine.unlocks)'s job; see "Milestone-trigger
//     linkage" below for why.
//
// # Atomic promotion — the staging-then-rename design (AC-9, ASM #3)
//
// Every save (regardless of kind) is written into a directory under
// root/.staging/<random>/ — a location List/Load never scan — and is
// moved into its final, discoverable name via os.Rename ONLY after a
// full serialize.ValidateBundle pass succeeds on the staged bundle.
// os.Rename within the same save root is atomic on every OS this
// project targets, so a reader can only ever observe the pre-promotion
// (old, or nothing) state or the fully-promoted (new) state — never a
// half-written directory sitting at a name List/Load would return
// (AC-9, AC-16). If the write fails at any point before promotion, the
// staging directory is removed and nothing under root's discoverable
// tree (manual/, autosave/, milestone/) is touched — this is what makes
// AC-13's "a failed 11th autosave leaves the retained 10 untouched"
// property fall out of the ordering rather than needing separate
// bookkeeping. The staging directory name itself is a random suffix
// (os.MkdirTemp), never derived from or affecting the bundle's own
// content, so its non-determinism has no bearing on AC-14's
// byte-determinism guarantee, which is checked against the FINAL,
// promoted bundle only.
//
// # Milestone-trigger linkage (AC-5, ASM #1 — logged per this item's
// acceptance file's Escalations section)
//
// §3 names "milestone saves" as a save point but does not itself state
// what triggers one; §4's 13-tier population ladder is the only
// spec-defined notion of "milestone" in the master doc, and
// engine.unlocks's own acceptance file independently arrives at the
// same reading (its AC-4 fires grants "in the same tick" a population
// threshold is crossed). This package adopts that linkage — a milestone
// save fires on a §4 tier crossing — as an inference, not literal spec
// text, logged as assumption ASM (feat.saveux, priority P1) rather than
// assumed silently. Because code.json's feat.saveux outbound call list
// names only int.serializer (BUG-058: no registered edge to
// engine.unlocks or engine.core exists yet), this package does NOT call
// out to either — [Manager.Milestone] takes the crossed [Tier] as a
// parameter, and it is the future engine.unlocks/engine.core wiring's
// job to detect the crossing and call in, once those call edges exist.
//
// # The exclusion-allowlist policy (AC-2, AC-18, ASM #2)
//
// Every registered [Participant]'s domain struct type must carry a
// reflection-based field-parity drift test in ITS OWNING package,
// modelled on internal/foundation/serialize's
// TestHeaderWireFieldsMatchHeader: every exported field of the domain
// struct must have a named counterpart in its save/wire projection, OR
// appear on an explicit, commented exclusion allowlist stating why it
// is not persisted (e.g. a derived/cached value recomputed on load).
// This is opt-OUT, not opt-in (ASM #2): a new field is assumed
// persisted unless explicitly excluded with a reason, so a forgotten
// field fails the drift test loudly the moment it is added, rather than
// silently never being saved until someone remembers. [DefaultParticipants]
// is empty as of this build — no domain engine module has registered a
// Participant yet (they land in later sprints) — so there is currently
// nothing for this policy to apply to; the moment one registers, its
// package must carry the matching drift test or the registry/test-count
// check this file's acceptance criteria describe fails.
//
// # Save-kind and provenance metadata (AC-6, ASM #4)
//
// Every bundle carries, alongside int.serializer's own Header, a
// [Meta] sidecar file (save-meta.json) recording [SaveKind] and (for
// milestone saves) the crossed tier. This lives in feat.saveux's own
// file rather than widening serialize.Header's wire shape: Header is a
// fixed contract at int.serializer's module boundary (GR#20) — widening
// it would make that package depend on feat.saveux's own vocabulary
// (Manual/Autosave/Milestone) for a concept it doesn't otherwise need.
package save
