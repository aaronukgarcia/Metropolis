// Package keys is ui.keys (MOD-011): the key grammar. It owns the ONE
// place in this codebase a raw key event is turned into an [Action] —
// leader-key mnemonic sequences, the which-key HUD's data (Continuations),
// counts/repeat, undo/redo hooks, 12 map marks, name search, a fuzzy
// command palette, global bindings, and a remappable keymap JSON profile.
//
// Module key: ui.keys (see code.json; GUID 9b4f3509-f2cb-461f-b6ea-f5531b3b55d9)
// Spec ref:   UI-SPEC §3 (leader-key grammar, which-key HUD, counts/
//
//	repeat, marks/search, command palette, globals, mouse-optional);
//	UI-SPEC §6 (line 780: keymaps are remappable, the grammar is the
//	default binding set, stored as data)
//
// Acceptance: docs/planning/acceptance/ui.keys.md (27 ACs)
//
// # The standing rule (AC-20/AC-21, discharges FEAT-033)
//
// No package outside this one may construct a protocol.Command from a raw
// key event. cmd/metropolis and internal/ui/core may call INTO this
// package's registered-action dispatch (Feed/FeedTcellEvent), but the
// translation from "a key was pressed" to "this is the resulting Command"
// belongs here, full stop — duplicating it elsewhere violates GR#20/GR#3.
// This package itself never imports internal/protocol in non-test code
// (grep -rn "internal/protocol" internal/ui/keys/*.go, excluding
// _test.go, returns nothing): a caller's own Action.Run closure is where
// a protocol.Command actually gets constructed and sent on a Transport —
// this package only guarantees THAT closure runs, exactly once, only for
// a completed, registered mnemonic path, and never for anything else.
// feat006_test.go proves the full tcell.EventKey -> Action ->
// protocol.Command -> real Transport path end to end (AC-20).
//
// # Mnemonic-path convention (AC-18)
//
// A mnemonic path is verb -> noun -> variant, e.g. ["b","r","s"] for
// "build road street" (UI-SPEC §3's worked example). New screens
// registering actions should follow that same three-tier shape where it
// applies (a bare single-letter path, e.g. ["z"] for "zone", is a
// legitimate one-tier action; nothing requires exactly three tokens).
// Register(path, action) rejects two shapes structurally, at Register
// time, rather than at runtime under the player's fingers:
//
//   - AC-14: the identical path registered twice (order-dependent
//     last-write-wins is never allowed to happen).
//   - AC-14b (ASM-118): a path that is simultaneously a complete action
//     AND a strict prefix of another registered path (e.g. "b" complete
//     while "b r" is also registered) — the state machine could not tell,
//     on feeding "b", whether to dispatch immediately or wait for a
//     continuation, and UI-SPEC §3 names no wait-vs-fire tiebreak, so the
//     ambiguity is avoided structurally instead of resolved arbitrarily.
//
// # Keymap JSON schema (AC-11, AC-19)
//
// A keymap profile (data/keymap-default.json is the shipped default) is:
//
//	{
//	  "version": 1,
//	  "bindings": { "<physical key token>": "<mnemonic path>", ... }
//	}
//
// Each binding's KEY is exactly one physical key token: a single
// non-whitespace UTF-8 rune ("b", "5") or a "<Name>" special from the
// closed set: Space, Esc, Enter, Tab, Backspace, Left, Right, Up, Down,
// Home, End, PgUp, PgDn, Delete, F1-F12 (the same closed grammar
// internal/harness/uitest's scripted-key DSL documents, independently
// re-declared here rather than imported — see key.go's doc comment for
// why that duplication is deliberate and not weakness-pattern-#2
// material).
//
// Each binding's VALUE is a mnemonic PATH — a whitespace-separated list
// of one or more tokens using that same token grammar, e.g. "b r s" —
// NOT a single token (ruling, Bill, 2026-08-10, resolving ASM-165). A
// one-segment path ("b" bound to "b") is the common, degenerate case,
// and is exactly what the shipped default profile uses throughout. The
// general case lets a player rebind a COMMAND to a different physical
// key ("ctrl+p" bound to "b r s" fires the b-r-s action in one keystroke
// — remapping which key TRIGGERS a command, not merely swapping which
// key stands in for which single mnemonic letter at every tree depth;
// that weaker "alphabet substitution" reading was tried first and
// rejected because it cannot express "move pause off space" and cannot
// be validated meaningfully — see below).
//
// Every binding's target path is checked against the LIVE, already-
// Register()ed action set at ApplyKeymap time (AC-11b, weakness pattern
// #4: a remapped key reaches a dispatch decision, so the allowed domain
// is stated positively and enforced, not merely plausible-looking): the
// path must resolve all the way to a COMPLETE registered action (a
// terminal node in the Register() trie) or a registered global — a path
// that only reaches a valid PREFIX (e.g. "b r" when only "b r s" is
// registered) is rejected, exactly like a path that reaches nothing at
// all. Checking only the target's first token for reachability — a
// weaker property that looks similar — cannot make this distinction and
// was the defect a Destructive-style review (Bill, 2026-08-10) caught
// before this package shipped: it would have silently accepted
// "b r <anything>" whenever "b r ..." was a valid prefix, even for a
// tail that names no real action. A rejected entry is logged
// individually (MET-U303, AC-11b: "that specific binding is rejected...
// while the rest of the profile still loads").
//
// The shipped default profile (data/keymap-default.json) maps every
// physical key token to itself, one segment each (identity substitution)
// — "the grammar above is the default binding set, stored as data"
// (UI-SPEC §6). No Go-side table of default bindings exists anywhere in
// this package (GR#15): even the identity default is loaded from that
// JSON file, not hardcoded. A malformed or schema-invalid profile file
// falls back to the shipped default profile entirely (MET-U302, AC-13).
//
// # Palette argument grammar (AC-9b)
//
// The command palette (palette.go) accepts parameterised free text (e.g.
// ":loan 5M 10y"). Each registered command's argument list states its
// expected domain POSITIVELY, per kind:
//
//	ArgMoney    ^\d+(\.\d+)?[KkMm]?$   parses into a Money (minor units)
//	ArgDuration ^\d+[ydwm]$            parses into a Duration (years/
//	                                   days/weeks/months, unit preserved)
//	ArgInt      ^-?\d+$                parses into an int64
//	ArgString   any non-empty string   passed through verbatim
//
// A value that does not match its argument's kind is REJECTED (MET-U304)
// naming the offending argument and the expected form — never silently
// coerced to a zero value, never truncated to "the parseable prefix".
//
// # Out of scope (see acceptance doc's "Out of scope" section)
//
// Rendering the which-key HUD strip, palette pane, or search-match
// highlighting belongs to ui.core/the owning F-screen; this package
// exposes the data (Continuations, palette matches, search results)
// those panes render. The actual gameplay action set (BuyLand, zone/
// build commands) is registered into this grammar by the owning engine/
// UI modules as they go real — this package defines the mechanism, not
// the city-builder's verb list. Auto-naming of objects that "/" search
// indexes against is out of scope; this package only consumes a
// [NameIndex]. Mouse drag-to-draw gestures are a ui.core/screen concern;
// this package's mouse involvement is limited to AC-12's shared [Action]
// dispatch seam.
//
// # ASM-075 status (re-checked at this item's dispatch, 2026-08-10)
//
// ui.core (MOD-009, done) exposes no dedicated which-key/palette render
// widget as of this package's build — internal/ui/core/views.go's
// ViewModels carries only Patches/Tick/Stale keyed by SubscriptionID (an
// engine-delta-driven model), and internal/ui/widgets has no HUD-strip or
// palette-pane widget either. This CONFIRMS ASM-075's premise: the HUD
// and palette ship as data-only from this package (Continuations,
// Palette.Match, Search results), with rendering deferred to a follow-up
// UI-layer item, exactly as the acceptance doc's Scope section already
// states. ASM-075 itself is not resolved by this package (it names a
// ui.core gap, not a ui.keys one) — it remains open, now with a confirmed
// rather than assumed premise; a follow-up BOW item for the actual
// HUD/palette-pane widget is Bill's call, not logged fresh here.
package keys
