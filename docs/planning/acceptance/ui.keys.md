BOW code: MOD-011

# Acceptance criteria — ui.keys (MOD-011)

**BOW code:** MOD-011
**Spec refs:** UI-SPEC §3 (`docs/METROPOLIS-MASTER-v2.1.md` lines 742-756: leader-key grammar, which-key HUD, counts/repeat, marks/search, command palette, globals, mouse-optional); UI-SPEC §6 (line 780: "a layout/profile JSON schema (keymaps are remappable; the grammar above is the default binding set, stored as data)"); UI-SPEC §5 (performance budget — keystroke echo <10ms, pane focus <5ms, applicable to key handling latency); code.json `ui.keys` entry (inbound `KeyGrammar`, "Go package API + keymap JSON schema", pattern "leader-key state machine; every action registered with mnemonic path").
**Date:** 2026-08-08
**Status:** draft-ahead
**Package under test:** `internal/ui/keys/` (path from `node claude-bow.js show MOD-011`)
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/ui/keys/...`.

## User stories

- **US-1.** As the player, I need a verb→noun→variant leader-key language (`b r s` = build road street) so that two to four keystrokes reach every command and thirty learned keys unlock three hundred (UI-SPEC §3).
- **US-2.** As a novice player, I need a which-key HUD that appears within one UI tick after any prefix and shows my available continuations, so that I can discover the grammar by reading rather than memorising documentation (UI-SPEC §3).
- **US-3.** As an expert player, I need the which-key HUD to dim after N uses of a learned sequence, vim-style counts/repeat (`5 b r s`, `.`), 12 map marks, and `/` name search, so that the UI withdraws as I learn and I can operate at the speed of thought (UI-SPEC §3).
- **US-4.** As the player, I need a fuzzy command palette (`:`) that lists every action with its key sequence printed beside it and accepts parameterised commands (`:loan 5M 10y`), so that the palette doubles as the tutorial for the key grammar (UI-SPEC §3, the "VS Code trick").
- **US-5.** As a player who wants a different physical layout, I need keymaps stored as a remappable JSON profile, so that the shipped grammar is the default binding set, not a hardcoded one (UI-SPEC §6).
- **US-6.** As every other UI screen/module built later in the sprint plan (e.g. `feat.saveux`, which depends on this item per code.json), I need a stable `KeyGrammar` package API — every action registered with a mnemonic path — so that new screens plug into one grammar rather than each inventing key handling.

## Scope

The leader-key state machine, which-key HUD data model, counts/repeat/undo-redo hooks, marks, name search, fuzzy command palette, global bindings, and the remappable keymap JSON schema. Rendering the HUD/palette panes themselves belongs to `ui.core`/the F-screens; this item owns the grammar engine and the data it drives those panes with.

## Acceptance criteria

### Functional

- **AC-1 (GR#20).** A `KeyGrammar` (or equivalent) interface/type exists implementing the inbound contract from code.json: a leader-key state machine where every action is registered with a mnemonic path (e.g. `["b","r","s"]`). Check: `go doc ./internal/ui/keys KeyGrammar` shows a `Register(path []string, action Action)` (or equivalent) method and a `Feed(key)`/`HandleKey` method advancing the state machine.
- **AC-2.** Feeding the key sequence for a registered mnemonic path (e.g. `b`, `r`, `s`) invokes the registered action exactly once, and an incomplete prefix (`b`, `r`) invokes nothing yet. Check: a passing test asserts both (`grep -rn "func Test.*[Ll]eader\|func Test.*[Mm]nemonic" internal/ui/keys/*_test.go`).
- **AC-3.** After any prefix keystroke, the grammar exposes the set of valid continuations (for the which-key HUD to render) via a queryable method, e.g. `Continuations() []Continuation`, populated within the same call — no async delay that would miss UI-SPEC §3's "within one UI tick" requirement structurally. Check: `go doc ./internal/ui/keys Continuations` (or equivalent) exists; a passing test asserts continuations are available synchronously after a prefix key (`grep -rn "func Test.*[Cc]ontinuation\|func Test.*[Ww]hichKey" internal/ui/keys/*_test.go`).
- **AC-4.** A per-sequence usage counter drives HUD dimming: after a configurable N uses of the same completed sequence, a query (e.g. `ShouldDimHUD(path)`) returns true. Check: a passing test drives a sequence N+1 times and asserts the dim threshold flips (`grep -rn "func Test.*[Dd]im" internal/ui/keys/*_test.go`).
- **AC-5.** Numeric count prefixes are supported: feeding `5`, `b`, `r`, `s` invokes the `b r s` action with a `Count == 5` (or equivalent) parameter, and no digit prefix defaults to `Count == 1`. Check: a passing test covers both counted and uncounted invocation (`grep -rn "func Test.*[Cc]ount" internal/ui/keys/*_test.go`).
- **AC-6.** `.` repeats the last build/zone action invoked (with its original count and target-relative parameters), and `u`/`U` invoke registered undo/redo hooks where the consuming screen/engine has registered them as available — the grammar calls through to a registered `Undo`/`Redo` capability, it does not implement undo semantics itself (that is the engine's "where the engine permits" per UI-SPEC §3). Check: `go doc ./internal/ui/keys` shows a `RepeatLast`/`.`-bound handler and `RegisterUndo`/`RegisterRedo` (or equivalent) hooks; passing tests cover repeat and undo/redo dispatch (`grep -rn "func Test.*[Rr]epeat\|func Test.*[Uu]ndo" internal/ui/keys/*_test.go`).
- **AC-7.** Twelve map marks are supported: `m a`..`m l` (or equivalent 12-slot addressing) records a location, `' a`..`' l` retrieves it; a 13th distinct mark identifier is rejected as invalid rather than silently overwriting mark `a`. Check: a passing test asserts exactly 12 valid mark slots and rejection of a 13th (`grep -rn "func Test.*[Mm]ark" internal/ui/keys/*_test.go`).
- **AC-8.** `/` opens a name-search mode over a caller-supplied searchable name index (the grammar doesn't own auto-naming data, it consumes it), and `n`/`N` step forward/backward through matches. Check: `go doc ./internal/ui/keys` shows a `Search(query string, index NameIndex)` (or equivalent) and `NextMatch`/`PrevMatch`; passing test coverage (`grep -rn "func Test.*[Ss]earch" internal/ui/keys/*_test.go`).
- **AC-9.** A fuzzy command palette exists that lists every registered action with its full mnemonic path rendered beside it, ranks matches by fuzzy score against free-text input, and accepts parameterised command syntax (e.g. `:loan 5M 10y` parses into an action name plus positional/typed arguments). Check: `go doc ./internal/ui/keys Palette` (or equivalent) exists; a passing test asserts a parameterised command parses into the expected action+args tuple (`grep -rn "func Test.*[Pp]alette" internal/ui/keys/*_test.go`).
- **AC-10.** Global bindings (`Space` pause, `1/2/3` speed, `o`/`O` overlay cycle, `?` context help, `!` top alert, `F9` ticker, `` ` `` console) are registered distinctly from the leader-sequence tree and always resolve regardless of current prefix state (a global fires even mid-sequence, or the grammar defines and documents the interruption rule if it doesn't). Check: `grep -n "Global\|globals" internal/ui/keys/*.go` finds a distinct registration path; a passing test asserts a global fires during an in-progress leader sequence, or `doc.go` documents that globals are blocked mid-sequence and why (`grep -rn "func Test.*[Gg]lobal" internal/ui/keys/*_test.go`).
- **AC-11.** Keymaps are loadable from a JSON profile file conforming to a documented schema, remapping physical keys to mnemonic paths without recompiling; the shipped default profile reproduces exactly the UI-SPEC §3 grammar (`b`→build, `z`→zone, etc.). Check: a `data/keymap-default.json` (or equivalent path) exists and validates against the schema; a passing test loads it and asserts at least the `b`, `z`, `p`, `s`, `d`, `i`, `g`, `t` top-level verbs are present (`grep -rn "func Test.*[Kk]eymap\|func Test.*[Dd]efault" internal/ui/keys/*_test.go`).
- **AC-12.** Every mouse-triggerable action registered through the grammar also has a key path (UI-SPEC §3: "every mouse act has a key path") — this is enforced structurally by having mouse handlers invoke the same registered `Action` as their key-path equivalent, not a separate code path. Check: `grep -n "Action\b" internal/ui/keys/*.go` shows mouse-event handlers (if implemented in this package) dispatching through the same `Action` type as key handlers; if mouse handling lives in `ui.core` instead, `doc.go` states that this package's registered-action model is the shared seam both key and mouse paths call into.

### Error handling

- **AC-13 (GR#7).** Loading a malformed or schema-invalid keymap JSON profile produces a registry-sourced error (new `MET-U`-range code added to `data/errors.json`) identifying the offending key/path, and falls back to the shipped default profile rather than leaving the grammar unusable. Check: `grep -n "MET-" internal/ui/keys/*.go` finds a registry code reference; a passing test covers malformed-JSON load with fallback (`grep -rn "func Test.*[Mm]alformed\|func Test.*[Ii]nvalid.*[Kk]eymap" internal/ui/keys/*_test.go`).
- **AC-14.** Registering two actions at the same mnemonic path (a conflicting binding) is rejected at `Register` time with a typed error, never a silent last-write-wins overwrite that would make key grammar order-dependent. Check: passing test coverage (`grep -rn "func Test.*[Cc]onflict\|func Test.*[Dd]uplicate" internal/ui/keys/*_test.go`).

### Determinism & safety

- **AC-15 (GR#21).** Feeding an identical key sequence against an identical registered-action set and keymap produces an identical resulting action+args dispatch every time — no randomness, no map-iteration-order dependence in continuation ordering (which-key HUD listings must render in a stable order). Check: `grep -rn "for .* := range" internal/ui/keys/*.go` — any range over a `map` feeding `Continuations()`'s output order is sorted before return (`grep -n "sort\." internal/ui/keys/*.go` corroborates); a passing determinism test exists (`grep -rn "func Test.*[Dd]eterminis\|func Test.*[Oo]rder" internal/ui/keys/*_test.go`).
- **AC-16 (SG-7 scoped; GR#21).** `grep -rn "time.Now\|time.Since" internal/ui/keys/*.go` (excluding `_test.go`) returns no matches outside a clearly-labelled, injectable-clock site used only for the HUD's own dim-after-N-uses bookkeeping (if time-based rather than count-based) or the 300ms highlight-pulse timing — any such site must be overridable for tests, per the pattern established in `foundation.errors`.
- **AC-17.** `go test ./internal/ui/keys/... -race -count=1` passes with no data race — the grammar's state machine must be safe if fed from the same T-INPUT goroutine UI-SPEC §1 describes while a concurrent palette search reads the action registry. Check: `grep -n "go func()" internal/ui/keys/*_test.go` finds at least one concurrency test.

### Documentation

- **AC-18.** `internal/ui/keys/doc.go` states the module key `ui.keys`, cites UI-SPEC §3 and §6, and documents the mnemonic-path convention (verb→noun→variant) new screens must follow when registering actions. Check: `grep -n "ui.keys" internal/ui/keys/doc.go` and `grep -n "UI-SPEC" internal/ui/keys/doc.go` both match.
- **AC-19.** The keymap JSON schema is documented (field names, types, how a remapped profile overrides the default) in `doc.go` or a sibling `.md`, since it is also `feat.saveux`'s and any future settings-screen's data contract. Check: file exists and is referenced by the loader code (AC-11).

## Out of scope

- Rendering the which-key HUD strip, palette pane, or search-match highlighting — that is `ui.core`/the relevant F-screen's job; this item exposes the data (`Continuations()`, `Palette` matches, search results) those panes render.
- The actual gameplay action set (`BuyLand`, zone/build commands) — those are registered into this grammar by the owning engine/UI modules as they go real (M0-ENG §2), not defined here.
- Auto-naming of objects (`§20`) that `/` search indexes against — this item only consumes a `NameIndex` interface, it does not generate names.
- Mouse drag-to-draw gestures for roads/zones — UI-SPEC §3 lists this as a `ui.core`/screen-level interaction; this item's mouse concern is limited to ensuring the shared `Action` dispatch seam (AC-12).

## Escalations

- **Assumption flagged (per BA instructions §3).** This item depends on `MOD-009` (ui.core, Sprint 1). If `ui.core`'s widget/pane API doesn't yet expose a place to render `Continuations()`/palette output by Sprint 2 dispatch, the which-key HUD and palette panes may need to ship as data-only in this item with rendering deferred to a follow-up UI-layer BOW item — the owning BA should confirm ui.core's actual surface at dispatch and adjust AC-3/AC-9's "rendered" language if needed.
- **For Bill.** UI-SPEC §3 doesn't specify the exact HUD dim threshold N or the palette's fuzzy-match algorithm/library choice; AC-4/AC-9 are written to be satisfiable by any reasonable choice the junior documents, per GR#15 (validators derive from data, not hardcoded assumptions) — flagging in case Bill wants a specific N or library mandated rather than left to the implementer.
