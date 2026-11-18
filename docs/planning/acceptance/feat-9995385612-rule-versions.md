# FEAT-9995385612 - Rule versions in saves: acceptance criteria

Written by the business analyst (Sonnet), 2026-11-18. Documents only. Every
claim about the code below was read on main at commit 684b616 (file and line
in brackets). The BUG-1224 fix was read in the read-only worktree
`.claude/worktrees/agent-ad5c8b543590393b6`. Sizes are estimates, not
measurements.

## 0. The problem in five lines

1. Replay stops at the first command the engine now rejects
   (`replayCommands`, `internal/engine/compose/snapshot.go:781`, error
   MET-G811 at `compose/errors.go:122`).
2. Only accepted commands are journaled (`accept` -> `journalAccepted`,
   `internal/engine/core/commands.go:359`, `:464`). A refusal is never in a
   journal.
3. So a rule that newly refuses something breaks every old journal that holds
   the thing it now refuses. First case: BUG-1224, five accepted build orders
   on one cell.
4. A rule that newly accepts or differently applies something does not break
   restore. It silently makes the old city come out different. That is worse.
5. Aaron ruled (BUG-1224, 2026-11-18): saves record which rules they were made
   under; old saves replay under the old rule; new play gets the fix. Every
   one of the ten levers is another rule change, so this is used many times.

## 1. Today, from the code

### 1.1 What is recorded

| Thing | What it holds | Where |
|---|---|---|
| Bundle header (`serialize.Header`) | `formatVersion` "1.0.0", `worldSeed`, `createdAtTick`, `gameMonth`, `appVersion`, sticky `debugTouched`, `shardIndex` | `internal/foundation/serialize/header.go:12` (const), `:25` (struct) |
| `appVersion` | A fixed label, `"metropolis-compose"`. Informational only. Never used for any decision. | `compose/save_wire.go:64`, stamped at `:218` |
| `formatVersion` | The shape of the file. Same major accepted; newer or older major refused (`CheckFormatVersion`). It says nothing about game rules. | `header.go` migration rules and `CheckFormatVersion` |
| Bundle sidecar `Meta` | `saveKind`, `displayName`, milestone tier, and `gameMode` (omitempty). | `internal/engine/save/kind.go:29-60` |
| Journal frame | One `protocol.Command` encoded by `protocol.EncodeCommand` (plain `json.Marshal`): `protocolVersion`, `correlationId`, `issuedAtTick`, `kind`, `payload`. No rule information. `Owner` is `json:"-"` so it is never stored. | `protocol/envelope.go:103`, `protocol/codec.go:67`; durable write in `compose/persistjournal.go:170` |
| Journal file | `root/{sha256(tenant)}/{sha256(city)}/journal.dat`, length + payload + CRC frames. The persist store only sees opaque bytes. | `internal/persist/diskstore.go:44,103`, `persist/journal.go` |
| City sidecars | `seed.json` (world seed), game-mode sidecar and epoch. Read at restore by `checkWorldSeed` / `checkGameMode`. | `compose/snapshot.go:487`, `:604`; `persist/store.go:85-188` |

A snapshot is a zip of a normal save bundle (`buildSnapshotBytes`,
`snapshot.go:229`), so it carries the header and Meta above. Neither holds
anything about rules.

### 1.2 What restore does, step by step

`RestoreLatestSnapshotOrGenesis` (`snapshot.go:323`), called by metroserve
(`cmd/metroserve/persist.go:264`, inside `wireAndRehydratePaging`, `:229`):

1. `checkWorldSeed` - refuse a journal from a different seed (MET-E819).
2. Read the snapshot list and the whole journal (`RestoreCommands`,
   `persistjournal.go:206`) into `[]protocol.Command`.
3. No snapshot: `restoreGenesis` (`snapshot.go:662`) replays every command from
   tick 0 through `e.HandleCommand`.
4. Snapshots exist: walk newest to oldest. For each, `tryRestoreCandidate`
   (`:696`) restores into a throwaway engine, cuts the journal at the snapshot
   tick with `splitJournalAtTick` (`:820`, which can even split one
   AdvanceTicks command in two) and replays the tail on the throwaway. A tail
   inconsistency (MET-G811 or a short tail) is logged and the walk goes to the
   next older snapshot. A corrupt payload fails closed.
5. The first clean candidate is applied for real: `restoreFromSnapshotBytes`
   -> `LoadAt` (`save_wire.go:519`) -> tail replay on the real engine.
6. Every walk-back exhausted: genesis replay.

`replayCommands` skips Subscribe and Unsubscribe (BUG-1225, db7b401) and stops
at the first rejection.

`Composition.Load` (`save_wire.go:256`) always prepends the seed check and
appends the game-mode check. The game-mode check is the exact precedent for
this item: `Meta.gameMode` is omitempty, absent means "old", and
`save/load.go:70-130` refuses before any participant is touched.

### 1.3 Where a version could ride

- Bundle `Meta` (omitempty field, like `gameMode`). Cheapest for snapshots.
- The journal frame. The frame is opaque bytes to the store, so it can change
  shape without touching `internal/persist`.
- A city sidecar next to `seed.json`.
- A new protocol command kind. Rejected below: it is wire surface.

### 1.4 Terminal game versus metroserve

- `cmd/metropolis` has **no save, no load and no journal**. It calls
  `compose.Wire(engine, &compose.Deps{GameMode: gameMode})` with no persist
  store (`cmd/metropolis/boot.go:555`). Every terminal game starts a new city
  under the current rules. The only production callers of `Composition.Save`,
  `Load` and `LoadAt` are the snapshot functions in `compose/snapshot.go`.
- So "the terminal game's Load/LoadAt path" means the `Composition` Load and
  LoadAt methods (used by tests, by snapshots, and by any future terminal
  load screen). The criteria below test those methods directly and the
  metroserve wake path separately. See assumption A1.
- metroserve is the only place with a long-lived journal. A city on the home
  server will cross several rule versions in one journal.

## 2. Design decisions

### 2.1 (a) Unit of versioning: ONE integer for the whole engine

`rules.Current` is a single monotonically increasing integer. Version 0 is
"before this feature". Each rule change adds exactly one new number.

Why: a save then has one answer to "which rules", saves are totally ordered,
"newer than this build" is a plain comparison, and there are never untested
mixes of flags. Rejected: per-rule flags (2^n combinations, no ordering, a
save could hold a set no build ever tested); a semver string like
`formatVersion` (rule changes are not additive or breaking in the semver
sense, and there is nothing useful about minor and patch); using `appVersion`
or git describe (changes on every commit, would break fixtures that did not
change rules).

### 2.2 (b) Where recorded: a stamp on every durable journal frame, plus `Meta.ruleVersion` on every snapshot

- **Snapshot / save bundle**: `Meta.RuleVersion int` with
  `json:"ruleVersion,omitempty"`, written from `Context.RuleVersion` by
  `Composition.Save`. Absent means 0. A version-0 save is byte-identical to
  today's.
- **Durable journal**: a frame recorded while the engine is at version 0 is
  exactly today's bytes (the plain command JSON). A frame recorded at version
  N >= 1 is an envelope `{"rv":N,"cmd":<today's command JSON>}`. The envelope
  is written and read only in `compose/persistjournal.go` (write at `:170`,
  read in `RestoreCommands` at `:206`, which changes to return the version
  with each command). Nothing in `internal/protocol`, the wire, TypeScript or
  `internal/persist` changes.
- **Replay** sets the active version from each frame before applying that
  frame. A frame with no envelope is version 0. A snapshot restore starts the
  tail at `Meta.ruleVersion`.

Why per frame: a journal that spans versions needs no extra bookkeeping. There
is no marker to place, no segment to find, and no interaction with
`splitJournalAtTick` (which cuts and even splits commands by tick count).
Every frame says what it was accepted under, so any cut of the journal still
knows. The cost is about 15 bytes on each version-1-or-later frame; the
developer reports the measured median frame size before and after.

A useful side effect: a build that predates this feature meets an envelope
frame, finds no `kind`, and fails with an unknown-kind decode error. A rollback
to an older build on a city that has run under newer rules therefore fails
closed instead of misreplaying. A plain `rv` field added to the command JSON
would be silently ignored by that old build, which is why the envelope is
preferred.

Rejected alternatives, one line each:
- Marker entries in the journal (a new frame or a new protocol command kind):
  needs a new frame type or wire surface, can land on the wrong side of a
  snapshot cut, and a marker has to be written at exactly the right moment.
- Per-segment files or a segment table: changes the store layout and the torn
  write story for no gain.
- A sidecar of "from frame index N the version is V": two files that can
  disagree after a crash, and the index does not survive the tail split.
- Version only in the snapshot header: cannot describe a journal that crosses
  a boundary after the snapshot.

When does the stamp change inside one journal? The live version is fixed for a
process (it is the build's `rules.Current`). It differs from the previous
frame's version only when the process was restarted on a newer build. So the
switch happens at one place: the end of a restore (2.7). The first live frame
accepted after that carries the new version. No marker is written, and a wake
followed by an immediate shutdown leaves the journal byte-identical.

### 2.3 (c) How code asks "which rule applies here"

One new leaf package, `internal/engine/rules`. It imports nothing from the
engine. It holds:

- `Current` - the live version (the last registry entry).
- `OldestSupported` - the oldest version this build can still replay
  (initially 0).
- `Has(active, introduced int) bool` - true when `active >= introduced`. Every
  rule check in the engine is `rules.Has(active, rules.RefuseOccupiedCell)`
  where `RefuseOccupiedCell` is the named constant for the registry entry. No
  integer literal at call sites.
- The registry (2.4).

Where `active` comes from: `Composition` owns the one value (`state.ruleVersion`).
Each module that has a versioned rule gets a getter injected at `Wire`, the
same shape as the existing wellbeing and mortality getters. A module built
without a getter (a unit test of the bare module) treats `active` as
`rules.Current`. Live play is always `Current`. Replay sets the value from the
journal frame or the snapshot. The value changes only between commands, never
inside one, so no module sees it change mid-command.

The value is data, not clock or randomness, so determinism (GR#21) is
untouched. It is not hashed (2.6).

### 2.4 (d) How a developer adds a rule change, and what the harness forces

Steps for rule change N:

1. Add one entry to the registry in `internal/engine/rules/registry.go`:
   `{Number: N, Date: "YYYY-MM-DD", Item: "BUG-nnnn", Old: "<one line, plain
   English: how the engine behaved before>", New: "<one line: how it behaves
   now>", Fixture: "testdata/rules/vN"}` and one named constant for N.
2. Branch the code at the single place where behaviour changes:
   `if rules.Has(active, rules.TheConstant) { new } else { old }`. Keep the old
   branch.
3. Generate the old-journal fixture (see C4): the generator runs the real
   command path with the engine pinned to N-1, includes at least one command
   whose outcome differs under N, and writes a persist directory (journal and a
   mid-run snapshot) to `testdata/rules/vN`. Commit it.
4. Run the registry tests. They fail until 1 to 3 are all done (C1 to C5).

The registry entry fields are the only prose; the operator message (2.8) is
built from them, so the developer writes the explanation once.

What the harness forces: a registry entry with no fixture fails; a fixture
that does not exercise the rule fails; a rule constant that is never used
fails; an integer literal at a call site fails; a gap, duplicate or empty field
in the registry fails. See C1 to C5.

### 2.5 (e) Journals and snapshots recorded before this feature

No recorded version means version 0. Version 0 means today's behaviour,
including duplicate build orders accepted and charged. This is the only
interpretation that is correct for the bytes already on disk (including the
home-server city), and it requires no migration of any file.

### 2.6 (f) State digest and the determinism gate

The rule version is **not** hashed into `StateDigest`
(`compose/state_digest.go:83`). Reasons: the digest describes simulation state,
the version describes how future commands will be judged; hashing it would move
every existing golden hash; and misapplied rules already show up as a different
digest because the state differs. The version is checked directly instead
(`Composition.RuleVersion()` getter asserted in the tests).

Existing replay fixtures (`internal/harness/replay/fixture.go`) get an optional
`ruleVersion` in their header, omitted for every existing fixture, read as 0.
The replay player builds its engine pinned to the fixture's version. A fixture
replay therefore never silently picks up `Current`. Fixtures stay
single-version (they are recorded in one session); only the durable journal
needs to span versions.

### 2.7 (g) Loading under old rules: replay old, continue new, where the switch happens

The rule, stated exactly:

1. Replay of recorded history (the snapshot's own state plus every journaled
   command) always runs under the version recorded with it.
2. The moment the restore function finishes (after the last journaled command
   is applied, before the first new command is accepted) the live version
   becomes `rules.Current`. That is the switch. It is one assignment inside the
   restore function; nothing is written at that moment.
3. It is journaled by the next accepted command: its frame carries the new
   version (2.2). A snapshot taken after the switch records the new version.
4. Nothing is retroactive. A duplicate order already in the old city's queue
   stays and is processed. The new rule governs only commands submitted after
   the switch.
5. `Composition.Load` and `LoadAt` called directly (no journal) apply the same
   rule: the state is loaded as saved, the loaded version is available from
   `LoadedRuleVersion()`, and the live version after the call is `Current`.
   The restore function reads `LoadedRuleVersion()` to pin the tail replay and
   then releases to `Current`.

### 2.8 (g) What the operator sees

When a restore ends having replayed anything below `Current`, one WARN
registry event is raised (constructed so it is logged, not returned as an
error, the same pattern as `MET-E1000` in `save/load.go`) and metroserve prints
one stdout line next to the existing "rehydrated city ..." line
(`cmd/metroserve/persist.go:274`). Both are sourced from the registry: the
message lists, for each version crossed, its number, date, item and the Old and
New one-liners. No rule text is typed in code. When the last replayed version
already equals `Current` (a second wake of the same city) there is no warning.

### 2.9 (h) Retirement (so ten levers do not mean ten permanent old code paths)

The mechanism is built now and not used: `rules.OldestSupported`. A snapshot
or frame older than it is refused with a registry error naming both versions.
The policy for when to raise it is Aaron's call (Q1). A snapshot taken under a
newer version needs no old code at all to restore, which is what makes
retirement possible: after the home-server city has taken a snapshot at the
new version, the old branch is only needed for genesis replay of the old part
of the journal.

## 3. Acceptance criteria

Test kinds used: **unit** (one package), **registry** (a test that reads the
registry and the source tree), **restore** (a test over compose with a real
`persist` store, snapshot and journal, the pattern of
`bug1225_replay_skip_test.go`), **host** (cmd/metroserve wake path with a real
`DiskStore`), **frame** (terminal frame at 80x24), **gate** (the existing
determinism and golden gates, unchanged).

### Registry and call sites

**C1 (registry, unit).** The registry has one entry, number 1: date
2026-11-18, item BUG-1224, non-empty Old ("a second build order on a cell that
already holds a queued or standing structure is accepted and charged") and New
("refused with MET-G510 and not charged"), fixture path. `rules.Current == 1`.
The test fails on: a number gap or duplicate, numbers not ascending,
`Current` not equal to the last entry, an empty field, a date that is not ISO,
an item that does not match `(BUG|FEAT|MOD|INT)-\d+`, a fixture path that is
empty.
*Can-fail proof:* delete a field, add entry 3 without 2, set `Current` to 2;
each makes the test red.

**C2 (registry). A rule change cannot land without its old-journal fixture.**
For every entry N >= 1 the directory named by `Fixture` must exist and load as
a persist directory with at least one journal frame and one snapshot, and the
fixture's recorded version must be N-1. A registry entry whose fixture is
missing, empty or recorded at the wrong version fails the test.
*Can-fail proof:* rename the fixture directory; stub the fixture to zero
frames; re-record it at version 1. Each is red.

**C3 (restore). The fixture must discriminate.** For every entry N >= 1 the
fixture is replayed twice by the same code: pinned to N-1 it restores; pinned
to N the result must differ (a rejected command, or a different state digest).
A fixture that restores identically under both versions proves nothing and
fails.
*Can-fail proof:* a fixture with no duplicate order restores identically under
both; the test is red.

**C4 (registry).** Call-site hygiene, by a source scan (AST, the style of the
existing astgate): (a) every call to `rules.Has` has a registered named
constant as its second argument, never a literal; (b) every registered
constant is referenced by at least one non-test file; (c) no non-test file
outside `internal/engine/rules` reads `rules.Current` except the composition
and the save/restore code.
*Can-fail proof:* add a call with the literal `1`; add a registry entry whose
constant nothing uses; call `rules.Current` from the build module. Each is red.

**C5 (unit). Live play is always current.** A freshly wired composition reports
`RuleVersion() == rules.Current`. A bare build module with no getter treats
the version as `Current`. A Build accepted live is journaled as an envelope
frame `{"rv":1,"cmd":...}` (decoded by the restore reader to version 1).
*Can-fail proof:* change the default to 0; red.

### Recording

**C6 (unit). Snapshot Meta.** `Composition.Save` writes `Meta.ruleVersion`
equal to the live version. A save by an engine pinned to 0 produces a Meta file
byte-identical to one written by the code before this change (field absent). A
save at 1 contains `"ruleVersion":1`. `Load` returns the Meta version.
*Can-fail proof:* omit the field on write; the version-1 save assertion is red.
Remove omitempty; the byte-identity assertion is red.

**C7 (unit). Journal frame shape.** A command accepted at version 0 is stored
as exactly the bytes of `protocol.EncodeCommand` (the in-memory recorder and
the durable store still agree byte for byte at version 0). A command at
version N >= 1 is stored as the envelope. `RestoreCommands` (its new form,
which returns the version with each command and cannot be called in a way that
drops it) round-trips both. A malformed envelope (non-integer `rv`, negative
`rv`, `rv` with no `cmd`, `rv` of 0) fails with a registry error and is never
read as version 0.
*Can-fail proof:* each malformed case is a table row and must produce the
named error; writing the version-1 frame as a plain command fails the
round-trip row.

**C8 (unit). Rollback protection.** `protocol.DecodeCommand` applied to an
envelope frame returns the unknown-kind error (this is today's decoder: the
test pins it, so a future change that makes the decoder tolerate unknown
envelope keys is noticed).
*Can-fail proof:* none needed beyond the assertion; it fails if the decoder
starts accepting the frame.

### Restore: the headline cases

**C9 (restore). Five duplicate orders, recorded under version 0, restore
exactly.** The fixture is produced by the real command path with the engine
pinned to version 0: buy tile 0, zone a cell, five Build orders on that one
cell (all accepted), advance 12 months, with a snapshot taken mid-run. Restore
it (a) from snapshot plus tail and (b) from genesis. Expected for both:
`StateDigest`, treasury and population equal the uninterrupted original run's
(a control engine pinned to 0 that ran the same commands); the build queue
holds 5 orders; the treasury is lower than a single-order run's by exactly four
order costs (60,000 at today's 15,000 - the developer confirms the figure
against the code and states it in the test); `RuleVersion()` after restore ==
`rules.Current`; `LoadedRuleVersion()` == 0.
*Can-fail proof:* make restore replay under `Current` instead of the recorded
version: the replay is rejected with MET-G811 (red). Make `replayCommands`
treat MET-G510 as tolerated: the queue holds 1 not 5 and the treasury is
higher than the original (red) - this is exactly the builder's wrong claim
recorded on BUG-1224.

**C10 (restore). After that restore, new play gets the fix.** On the
restored city: a sixth Build on the same cell is refused with MET-G510 and the
journal length does not grow (a refusal is not journaled) and the treasury is
unchanged; a Build on a different free cell is accepted and its frame is
`rv=1`; a second restart of the city (genesis, and snapshot plus tail) gives
the digest of the uninterrupted continuation.
*Can-fail proof:* leave the live version at the loaded 0 after restore: the
sixth Build is accepted (red).

**C11 (restore). A journal that crosses the boundary restores exactly.**
Session A (pinned 0): buy, zone, three Builds on one cell, 40 ticks. Restart
into session B (the real restore, switch to 1): a duplicate Build is refused,
two Builds on new cells are accepted, 400 ticks. Restart again. The journal
now holds version-0 frames followed by version-1 frames. Restore it three ways
and compare each to a control engine that ran pinned to 0 for session A, was
switched in process at the boundary, and ran session B: genesis; snapshot
taken before the boundary; snapshot taken after the boundary (plus a snapshot
taken exactly at the boundary tick). Expected for all four: equal digest,
treasury, population, queue and `RuleVersion()` == 1.
*Can-fail proof:* make the replay use one version for the whole journal (the
first frame's, or the last): red in genesis and in the straddling-snapshot
cases. Move the version read from "before the frame" to "after the frame":
red.

**C12 (restore). Version order is checked.** A journal whose frames go 1 then
0 is refused with a new registry error ("rule version went backwards"). This
is not a tail inconsistency: it fails the restore closed and does NOT walk back
to an older snapshot (walking back would silently drop real progress).
*Can-fail proof:* hand-build the journal; today's code with a naive
implementation walks back and returns a successful older state; the test asserts
an error and an untouched real engine, and is red until it fails closed.

**C13 (restore). A future version is refused, never treated as current.** (a) a
snapshot whose Meta says `rules.Current + 1`; (b) a journal frame with
`rv = Current + 1`, on genesis and on the tail of a snapshot restore. Each is
refused with one new registry error that names the saved version and this
build's version and says to run a newer build. It is refused before any
participant state is applied (the pristine-digest proof used for MET-E819 in
`attack_bug479_*`), through `Composition.Load`, `LoadAt` and
`RestoreLatestSnapshotOrGenesis`. The newest snapshot being future does not
cause a walk-back to an older snapshot.
*Can-fail proof:* clamp the version to `Current` (the tempting wrong fix):
the restore succeeds and the test is red.

**C14 (restore). Too old is refused when support is retired.** With the test
seam `rules.OldestSupported = 1`, a version-0 snapshot and a version-0 journal
are each refused with a registry error naming both versions. With
`OldestSupported = 0` (the shipped value) they restore.
*Can-fail proof:* ignore `OldestSupported`; the refusal case is red.

**C15 (restore, host). Artifacts from before this feature.** One snapshot and
one journal captured from the real pre-feature build (the developer builds the
parent commit in a throwaway worktree and runs the generator once; the files
are committed) restore under the new code as version 0, with the digest the
old build reported at the same tick. This is the proof that "no version
recorded = 0" is true of real bytes and not only of bytes the new code wrote.
*Can-fail proof:* make an absent `ruleVersion` mean `Current`; red.

**C16 (restore). Snapshot after the switch needs no old rule.** On the C9
city, after restore and 30 more ticks, take a snapshot. Its Meta says 1. With
`OldestSupported = 1` that snapshot plus its tail restores; the old-branch code
is not entered (counted by a test hook on the old branch).
*Can-fail proof:* record 0 in snapshots taken after the switch; the
`OldestSupported = 1` restore is refused (red).

### Restore: other entry points

**C17 (unit, restore). Direct Load and LoadAt.** `Composition.Load` and `LoadAt`
over a version-0 bundle with no journal: loads, `LoadedRuleVersion() == 0`,
live version == `Current`, and a further duplicate Build is refused. Over a
future-version bundle: refused as in C13 with the composition pristine. Over a
version-1 bundle written by `Save`: loads, `LoadedRuleVersion() == 1`.
*Can-fail proof:* skip the check in `Load` and call it only in `LoadAt`; the
`Load` case is red.

**C18 (host). metroserve wake path.** `wireAndRehydrate` over a `DiskStore`
directory holding the C9 city: wakes, the city is at the right tick, and exactly
one stdout line and one WARN registry event name version 0 to 1 with the
registry's Old and New text. Wake again after only tick traffic: no warning
(the last replayed frame is already version 1). A wake over a future-version
journal: the process error contains the new registry code, the process exits
non-zero (the existing "corrupt journal is fatal" behaviour), and journal.dat,
every snapshot file and seed.json are byte-identical before and after (hash
compare).
*Can-fail proof:* print the message but build it from a string literal: the
registry-text assertion (change the registry text in the test and see the
output change) is red. Append a marker at wake: the byte-identical assertion
is red.

**C19 (frame). The terminal game boots at the current rules.** The
`cmd/metropolis` boot test asserts the composition's `RuleVersion()` ==
`rules.Current`. The existing 80x24 frame tests pass unchanged apart from the
two input changes already made in the BUG-1224 worktree (section 6).
*Can-fail proof:* pin the boot to 0; the BUG-1224 frame test (a second `b` on
one cell shows the refusal) is red.

### Existing behaviour that must not move

**C20 (restore). BUG-1225 is unaffected.** `bug1225_replay_skip_test.go` passes
unchanged. A new case: the same interleaving of Subscribe, Unsubscribe and
dead-owner Subscribe, with a version-1 envelope on those frames, restores to the
control digest with no subscription re-created. A skipped frame still takes
part in the ordering check of C12.
*Can-fail proof:* the two BUG-1225 mutations (delete the whole skip, delete the
Unsubscribe half) stay red; a third mutation, "skip the frame before reading
its version", makes C12's ordering case red.

**C21 (gate). The digest does not depend on the version.** Two compositions in
the same state, one at version 0 and one at 1, have equal `StateDigest`. The
whole existing determinism gate, golden files and sample fixtures pass with no
change to any golden file in this item's diff (checked with `git diff --stat`
over `testdata`, `*.golden`, and the fixture directories: only new files).
*Can-fail proof:* hash the version; every golden moves (red).

**C22 (restore). Fixtures replay at their own version.** A harness fixture with
no `ruleVersion` replays as 0 and gives the same hashes as before this item. A
fixture with `ruleVersion: 1` replays as 1. One with a future value is refused
by the fixture loader with a registry error.
*Can-fail proof:* make the replay player use `Current`; an old fixture that
contains a duplicate build moves its hash (red) - include one such fixture in
the test set.

**C23 (unit). Failure leaves nothing behind.** Every refused restore (C12, C13,
C14, malformed frame) leaves the engine, the composition and every file in the
persist directory unchanged, and writes nothing to the journal.
*Can-fail proof:* stamp or append before the check; the byte-identical
assertion is red.

### BUG-1224 as rule version 1

**C24 (unit, build module). The rule.** At version 0, a second order on an
occupied cell is accepted, mints an order id (`nextOrder` advances) and is
charged when built. At version 1 it is refused with MET-G510 before any id is
minted (`nextOrder` unchanged), a demolished cell is free again, and a
completed order whose structure was demolished does not hold the cell. The
existing tests of the BUG-1224 worktree pass at version 1; the same cases are
repeated at version 0 with the opposite expectation.
*Can-fail proof:* remove the version branch so the refusal is unconditional:
the version-0 cases are red. Remove the refusal: the version-1 cases are red.

**C25 (unit). Registry text is true.** The C1 entry's Old and New text is
asserted against behaviour by C24 (a comment in the registry names the test).
Changing the behaviour without changing the text is not detectable by a
machine; the dispatch checklist item for the Opus review states it.

### Error codes and messages

**C26 (registry). New codes are registry-sourced.** New MET codes (claimed
with `tools/plan/add-error.js claim-range`, owner `engine.compose` or the save
module, not `engine.build`; see the owner-versus-scanner note in memory)
exist for: saved under newer rules; saved under rules this build no longer
replays; rule version out of order; malformed rule stamp; the WARN
"restored under older rules, now running version N". MET-G510 is added as
built. `errors-lint`, the Go error scanner and the astgate pass; every
message is in `data/errors.json` with a remedy.
*Can-fail proof:* use a code not in the registry; the existing scanner is red.

**C27 (race, determinism). Concurrency and order.** The new tests run under
`-race` (the reduced set, within the 30-minute watchdog). The registry code
uses no map range (GR#21). The version is read and written only while the
engine's command path is serialised.
*Can-fail proof:* the race detector; and a test that applies the same journal
under two goroutine interleavings of metroserve's tick loop and a wake and
gets one digest.

## 4. Can-fail proof, in one place

Every criterion above lists its own proof. The rule for the developer: before
a test is called finished, run it against the mutation named in its proof and
paste the red output in the BOW comment. Two proofs are mandatory for the
review round (Opus): C9's "tolerate G510" mutation (it is the wrong fix that
looks right) and C11's "one version for the whole journal" mutation.

## 5. BUG-1224 as version 1, density stacking as version 2

### 5.1 What changes in the BUG-1224 worktree

Read-only worktree `.claude/worktrees/agent-ad5c8b543590393b6`. Today its
`SubmitBuildCommand` (`build.go`, the lines added after `:687`) refuses
unconditionally using `cellInUse`. Changes needed:

1. The refusal becomes `if rules.Has(b.activeRules(), rules.RefuseOccupiedCell) && cellInUse(...)`.
   `activeRules()` calls the injected getter, or `rules.Current` when none.
2. `BuildAPI` gets `SetRuleVersionSource(func() int)`; `compose.Wire` injects
   `func() int { return st.ruleVersion }` (Wire is owned by the record/restore
   lane, see section 8).
3. `ErrCellInUse` (MET-G510) and its `errors.json` entry stay as written.
4. The two tests that had their input changed
   (`cmd/metropolis/attack_bug493_endtoend_test.go`,
   `wire_tui_screens_test.go`) keep the change: they test current play.
5. `bug1224_cell_in_use_test.go` (build and compose) and the frame test keep
   their assertions and run at version 1. Add the version-0 mirror cases
   (C24) and the C9 to C11 estate.
6. The lead's rename `cellInUseLocked -> cellInUse` is already in; no more
   code in that worktree changes.

The worktree itself is not landed as-is (Aaron: keep for reference). The
developer builds on current main and ports the logic.

### 5.2 Density stacking (FEAT-9995385606) as version 2

Registry entry 2: Old "a second build order on an occupied cell is refused
with MET-G510 (version 1)"; New "a further order adds a level (8 more people),
caps 4 / 8 / 12 by zone with unzoned cells at 12, each level 50 percent dearer".
The code is `switch`: below 1 accept-and-charge (old), exactly 1 refuse, 2 and
above stack. Version 2 does not make old version-1 journals fail (they hold no
second orders, because refusals are never journaled) but it must still be a new
number: a version-2 save holds stacked orders that a version-1 build would
refuse, and the future-version check (C13) is what stops that. A version-0
journal still replays duplicates as accepted and charged, not as stacks; that is
the point of the old branch.

## 6. Fallout

Things that move, with the reason:

1. `internal/engine/save` - `Meta` and `Context` gain a field; a load option
   for the future-version check beside `WithExpectedGameMode`. Save-package
   tests that compare Meta bytes keep passing for version 0 (omitempty).
2. `compose/persistjournal.go` and its tests (`persistjournal_test.go`,
   `persistjournal_restore_test.go`, `feat_1972079852_inc4_journaler_test.go`):
   the existing assertion "durable bytes are byte-identical to the in-memory
   journal" stays true at version 0 and must compare decoded commands at
   version 1.
3. Every caller of `RestoreCommands` (compose snapshot code, the attack tests
   that call it, metroserve): the signature changes on purpose so no caller
   can drop the version. Mechanical.
4. `compose/snapshot.go`: restore, throwaway candidate and real restore share
   one replay function that applies versions; `tryRestoreCandidate` must use
   it too, or the throwaway and the real engine disagree.
5. `internal/harness/replay`: fixture header gets an optional field; the
   player pins the engine. Existing fixtures untouched.
6. New default for every composed engine is version 1: any existing test that
   builds twice on one cell now sees MET-G510. Found so far: the two tests
   changed in the BUG-1224 worktree. The developer runs the bounded set and
   `--webconsole-tsx-all` and lists any more (memory: bounded set misses
   tsx-all).
7. `data/errors.json`: MET-G510 plus the new codes (C26). Master plan and
   `code.json`: the new `engine.rules` leaf module and the edges
   engine.build -> rules, engine.compose -> rules, engine.save -> rules
   (use `/register-guid`; `edge-lint`).
8. TypeScript, protocol, wire fixtures: **no change** (the journal is not on
   the wire). State this in the BOW comment so nobody adds parity work.
9. The 30-minute watchdog and the two-suite limit apply to the new restore
   tests; the C11 case advances 400 ticks, not a year.

### The home-server journal (the dogfood city): read-only checks before deploy

SAFE deploys; this item only asks for the evidence. None of these steps writes
to pix.

1. Copy the city's data out of the running container to a scratch folder
   under `E:\git\Metropolis\temp\` (read-only: `docker --context pix compose
   -p metropolis -f deploy/compose.yml cp <service>:/data/. <scratch>`, run
   from the repo root, service name as in `deploy/compose.yml`).
2. Run a small read-only scan tool (new, `tools/journalscan`, built in the
   fallout lane) over the copy. It reads `journal.dat` with the persist
   reader and reports: frame count, last tick, number of Build commands per
   cell with no Demolish between them (cells with 2 or more accepted orders),
   any frame that does not decode, snapshot count and their ticks.
   Expected: a number. If it is zero the old rule never fired on pix and the
   deploy is risk-free either way; if it is above zero, the copy is the live
   proof for C15.
3. Restore the copy with the new build in a throwaway process (the C18 test
   harness pointed at the copy) and compare tick, population, treasury and
   `StateDigest` against the values the running server reports. They must be
   equal, and the stdout line must say version 0 to 1.
4. Only then does the lead ask SAFE to redeploy. After deploy, the first
   `docker --context pix compose ... logs` must show the WARN line once, and
   a second restart must not show it.

## 7. Out of scope, assumptions, questions

### Out of scope

- A save/load menu in the terminal game (there is none; see A1).
- Any change to the wire protocol, TypeScript types or `/health`.
- Migrating or rewriting any existing journal or snapshot (nothing is
  migrated; version 0 is read as is).
- Density stacking itself (FEAT-9995385606) and every lever; this item only
  provides the mechanism and lands BUG-1224 as the first use.
- An in-game banner for "loaded under old rules".
- Fixtures that cross a version boundary inside one harness fixture
  (fixtures are single-version).
- Downgrade of the binary on a city that already holds version-1 frames:
  refused by the envelope (C8) and documented; not otherwise handled.

### Assumptions (nothing in the brief or the code says otherwise)

- A1. "The terminal game's Load/LoadAt path" is `Composition.Load` and
  `LoadAt`. The terminal game binary has no persistence today
  (`boot.go:555`), so it always starts new at the current rules.
- A2. A rejected command is never journaled, so no old journal contains a
  command a newer rule refuses unless it was accepted under the old rule.
  (Read from `accept`/`journalAccepted`; if some path journals before
  deciding, the criteria still hold but C10's journal-length assertion
  changes.)
- A3. The engine's version only changes at a restart, never during a running
  process. If hot rule switching is ever wanted, per-frame stamping already
  supports it; the switch point just moves.
- A4. The persist store does not inspect journal bytes
  (`internal/persist` treats frames as opaque). Read from `store.go`.
- A5. Only one real city exists (the home-server dogfood city). Retirement
  (Q1) can therefore be done city by city.
- A6. The charge for a build order happens when it is built, not when it is
  queued, so five queued duplicates are five charges. This is the BUG-1224
  evidence (75,000 for one dwelling). C9 states the exact figure after the
  developer reads the code.

### Questions only Aaron can answer

**Q1. How long must an old rule be kept alive?**
Each lever that changes a tick-time formula (the ten equal migration weights
is one) means the old formula stays in the code for as long as an old journal
needs replaying from genesis. Options:
- (A) Keep every old rule branch for ever. Simplest; the code grows with every
  lever.
- (B) Build the retirement switch now (`OldestSupported`) and raise it for a
  version once the home-server city has taken a snapshot under the newer
  version; then delete the old branch and its fixture is marked retired.
  **Recommended.** Cost: one deliberate step per retirement; old pre-snapshot
  history can no longer be replayed from genesis.
- (C) Version only command-acceptance rules like BUG-1224 and let tick-time
  formulas change without a version, so an old city is restored only from a
  snapshot. Cheapest, but a tail that crosses a formula change then replays
  differently from how it was lived.

**Q2. How loudly should a city say it was loaded under older rules?**
Options:
- (A) Server log line and a registry event only (what the criteria require).
  **Recommended** for Baseline One: the only city that will ever cross is
  yours, and you read the log.
- (B) As A, plus a line in the terminal game's debug error tail.
- (C) As A, plus a one-time banner on the top bar for the first minute.
B and C are each a small separate item on top of this one.

## 8. Size and lanes

Guess: large. About 4 lane-days of building and about 3 working days elapsed,
counting 3 to 5 review rounds on the restore parts (the delivery path's own
yardstick).

Lanes that do not share files:

| Lane | Files | Size | Review |
|---|---|---|---|
| 1. Rules package and registry tests (C1, C2 skeleton, C4, C5 default, C26 codes) | new `internal/engine/rules/*`, `data/errors.json` entries, master plan / `code.json` registration | S (half a day) | Sonnet |
| 2. Record and restore (C6, C7, C8, C9, C11 to C17, C20, C23, C27) | `internal/engine/save/{kind,context,load,options}.go`, `compose/{save_wire,persistjournal,snapshot,compose,errors}.go`, new `compose/ruleversion.go` and tests | L (2 days; the risky part) | **Opus**: journal format, the shared replay function, fail-closed paths, walk-back interaction, the switch |
| 3. Replay harness (C21, C22) | `internal/harness/replay/{fixture,player_engine}.go` and tests | S (half a day) | Sonnet; the lead runs the unchanged-goldens check |
| 4. BUG-1224 as version 1 plus the headline fixture (C3, C9, C10, C24, C25, C19) | `internal/engine/build/{build,errors}.go` and tests, `testdata/rules/v1`, the two cmd/metropolis tests | M (1 day) | **Opus**: it is the money and the headline restore case |
| 5. Wake path, scan tool, fallout (C18, section 6 checks) | `cmd/metroserve/persist.go` and test, new `tools/journalscan` | S to M (half to one day) | Sonnet; Opus only for the C18 "byte-identical on refusal" assertion |

Order: lane 1 first (the API the others import is fixed up front:
`rules.Has`, `Current`, `OldestSupported`, `Lookup(n)`, the named constants).
Lanes 2 and 3 in parallel after it. Lanes 4 and 5 after lane 2 lands (4 needs
the version getter and the stamped journal; 5 needs the restore function).
`compose.go` Wire is edited only by lane 2; lane 4 touches only the build
package and its injection point named in 5.1.

Opus is required for lanes 2 and 4. They are restore-path and money work, the
delivery path's own rule for Opus reviews.

Standing briefing for every lane: mention BUDGET, the 30-minute test cap, no
full-suite runs, claim error codes before use, and that a lane stops and
reports if it finds a journal path that records before it decides.
