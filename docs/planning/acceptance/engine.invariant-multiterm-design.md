BOW code: BUG-067

# Design proposal — multi-term & cross-module stock registration (engine.invariant, MOD-019)

**BOW code:** BUG-067 (attack on BUG-058's claim 2; also references MOD-019, engine.invariant)
**Status:** PROPOSAL ONLY — not implemented. No production code, code.json, or master-plan-v2.1.json changed by this document.
**Date:** 2026-08-13
**Scope:** API design for expressing multi-term conservation identities and cross-module stock coupling. Requires a ruling from Bill/Aaron before any junior builds against it (flagged P1 architecture decision per BUG-067).

## 1. What actually exists today (not what the master-plan inbound block claims)

`docs/planning/master-plan-v2.1.json`'s `engine.invariant` entry documents the inbound contract as:

> `RegisterStock(name string, snapshot func() int64)` — "each registered stock's per-tick delta must equal tracked ins minus outs"

**This function does not exist in `internal/engine/invariant/`.** The real, landed API (`registry.go`, `snapshot.go`, `conservation.go`) is:

- `Registry.Register(inv Invariant)` — registers a hand-written `Invariant` (`Name() string`, `Check(Snapshot) Result`), keyed by name, reject-on-duplicate, no unregister.
- Each of the four v1 stocks (`people.go`, `money.go`, `goods.go`, `vehicle.go`) is a hand-written type wrapping the shared `stockCheck{name, stock StockName}` (`conservation.go`), whose `Check` does exactly one thing: `reading, ok := state.Reading(stock); actualDelta := reading.Closing - reading.Opening; balanced iff actualDelta == reading.TrackedDelta`.
- `StockReading` (`snapshot.go`) already carries `Opening int64`, `Closing int64`, `TrackedDelta int64`, `Suspects []string`. `TrackedDelta`'s doc comment says it is "the **sum** of every tracked flow the owning module recorded" — e.g. for money, "income - expenditure" — but the struct has nowhere to put the individual terms; the owning module must pre-sum them into one `int64` before handing over a `Snapshot` via `SnapshotProvider` (`hook.go`).

So the gap BUG-067 names is real but narrower than "the checking arithmetic can't handle multiple terms" — `Closing - Opening == TrackedDelta` is term-count-agnostic arithmetic already. What's actually missing is: (a) no registration-time API that takes named term functions and derives `TrackedDelta` itself (today every stock needs a hand-written `Invariant` type, which is fine for 4 stocks but not for 6+ more modules), (b) no way to preserve *which* term is wrong in a `Violation` for diagnosis, and (c) no primitive at all for a two-module coupling, where the "ins"/"outs" live in different owning modules' snapshots entirely.

## 2. Proposed API shape

### 2.a `RegisterStock` — a real convenience constructor (single- and multi-term, backward compatible)

```go
// TermFunc reports one named flow/level component's current value.
// Called once per tick, from whatever SnapshotProvider assembles the
// Snapshot RunSuite checks — never memoized across ticks by this
// package (mirrors the existing "no wall clock, pure function of
// Snapshot" determinism rule, AC-15/AC-13).
type TermFunc func() int64

// RegisterStock builds an Invariant for a single conserved stock from
// named opening/closing levels and named inflow/outflow terms, and
// registers it against reg. This replaces hand-writing a
// stockCheck-wrapping type per stock (people.go/money.go/goods.go/
// vehicle.go's existing pattern) for every NEW stock a module adds
// (US-5) -- the four v1 types are NOT migrated by this proposal (see
// §4, backward compatibility).
//
// ins/outs use map[string]TermFunc (never a bare slice) so each term
// carries a stable name that survives into a Violation's per-term
// breakdown (§3) -- "compost_out" beats ins[2] when a module's owner
// is staring at a violation at 2am.
//
// Single-term backward compatibility: a caller with today's one
// pre-summed TrackedDelta value can register with a single ins entry
// and an empty outs map (or vice versa) -- e.g.
// ins: map[string]TermFunc{"tracked_delta": currentTrackedDeltaFunc}.
// This is a strict superset of the old shape, not a breaking change to
// the *concept*; see §4 for why the 4 existing hand-written types are
// left alone rather than migrated.
func RegisterStock(
    reg *Registry,
    name string,
    stock StockName,
    opening, closing TermFunc,
    ins, outs map[string]TermFunc,
) error
```

`RegisterStock` internally builds a `Snapshot`-compatible reading per tick (opening/closing/sum(ins)-sum(outs) as `TrackedDelta`, `Terms` populated — see §3) and registers an `Invariant` wrapping it, via the existing `Registry.Register`. It is sugar over the existing primitives, not a replacement for the `Invariant` interface (a module with more complex per-tick derivation than "sum some functions" can still hand-write an `Invariant` directly, as today).

**Open question for the term functions' relationship to `SnapshotProvider`:** today a single `SnapshotProvider` builds the whole `Snapshot` once per tick (`hook.go`); `RegisterStock`'s term functions are called from *inside* that same per-tick assembly, not on a separate cadence — this needs to be nailed down: does `RegisterStock` register the `TermFunc`s so `RunSuite`/`Hook` calls them directly each tick (bypassing the caller-supplied `SnapshotProvider` for this stock), or does it only supply a *builder* that a `SnapshotProvider` implementation must call while constructing the `Snapshot` it already owns? The former is simpler for module authors; the latter keeps `SnapshotProvider` as the single per-tick assembly point (current architecture) and avoids two competing paths writing into `Snapshot.Readings`. **Recommend the latter** (register a builder, `SnapshotProvider` calls it) to avoid a second write path into `Snapshot`, but this is exactly the kind of call a BA/Bill should confirm, not something a junior should improvise.

### 2.b `StockReading.Terms` — named-term breakdown for diagnosis

```go
type StockReading struct {
    Registered   bool
    Opening      int64
    Closing      int64
    TrackedDelta int64      // unchanged: sum(Terms values), kept for
                             // every existing consumer (stockCheck,
                             // any code reading TrackedDelta directly)
    Suspects     []string

    // Terms optionally names the individual flow components that sum
    // to TrackedDelta -- e.g. {"generated": 500, "collected": -420,
    // "composted": -60, "landfilled": -20} for refuse mass. Nil is
    // valid (today's single-scalar callers, and the 4 v1 stocks,
    // never populate it) -- stockCheck's balance arithmetic NEVER
    // reads Terms, only TrackedDelta, so a stock with no Terms
    // behaves identically to today. Terms exists purely so a
    // Violation can report per-term detail (§3) instead of one
    // opaque unexplained number.
    Terms map[string]int64
}
```

This is additive: every existing reader of `StockReading` (there is exactly one, `stockCheck.Check`) is untouched; `Terms` is new, optional, and never consulted by the existing balance check.

### 2.c Violation gains an optional per-term breakdown

`Violation` (`violation.go`) gains an optional field, `Terms map[string]int64` (copy of the offending `StockReading.Terms`, subject to AC-11b's existing "never interpolate into Message" sanitisation rule — same treatment as `EntityIDs`). `newViolation` takes an extra `terms map[string]int64` argument. Zero-value/nil is unchanged behaviour for the 4 v1 stocks.

## 3. Cross-module coupling — the hard part

Refuse's `compost_out` and farming's `compost_in` are two *different modules'* stocks; neither module's own `Closing - Opening == TrackedDelta` check can see the other side. A within-module `RegisterStock` cannot express "my outflow term must equal someone else's inflow term for the same tick" because that's not a property of one stock's balance at all — it's a property of the transfer *between* two stocks' balances, each of which is independently already conserving.

**Proposed: a distinct registration primitive, not a special case of `RegisterStock`.**

```go
// TransferPair names two modules' term functions that must be equal
// for the same tick -- the amount one module reports leaving must
// equal the amount the other reports arriving. This does NOT replace
// either module's own RegisterStock call: refuse still separately
// proves its own mass balances (generated - collected - composted -
// landfilled), and farming still separately proves its own mass
// balances; TransferPair additionally proves the two modules agree
// with each other about the one term they share.
type TransferPair struct {
    Name       string   // e.g. "refuse.compost -> farming.compost"
    SourceTerm TermFunc // refuse's compost_out
    DestTerm   TermFunc // farming's compost_in
}

// RegisterTransfer registers a coupled-stock check against reg,
// verifying SourceTerm() == DestTerm() every tick this check runs.
// Lives in the SAME Registry/RunSuite as ordinary stock invariants
// (no separate suite, no separate wiring point) -- it implements
// Invariant like everything else, so RunSuite's existing
// ran/skipped/violation reporting (AC-1b) and the existing Hook/Wire
// wiring (AC-7) apply unchanged. Detected via a NEW Invariant
// implementation (transferCheck), not stockCheck -- the balance
// identity is "A == B", not "Closing - Opening == TrackedDelta".
func RegisterTransfer(reg *Registry, pair TransferPair) error
```

A `transferCheck`'s `Check` compares `SourceTerm()` and `DestTerm()` directly (no `Snapshot.Readings` lookup at all — it does not need `Opening`/`Closing`, only the two live term values for the tick) and reports a `Violation` (reusing the existing type; `Expected`/`Actual` become `SourceTerm()`/`DestTerm()`, `InvariantName` is `pair.Name`) when they differ.

**Why a distinct primitive and not "just register the same StockName from two modules":** forcing this into `RegisterStock`/`Registry.Register`'s existing single-name-key dedup (`ErrDuplicateInvariant`) would mean the *second* module's registration attempt for a shared stock name collides and fails outright — the existing dedup exists precisely to catch accidental double-registration of the *same* stock, so reusing that key space for a deliberately-shared coupling point would either require special-casing the duplicate check (fragile) or silently changing its meaning (worse). A separate primitive keeps `Registry.Register`'s "one name, one owner" invariant intact and gives the coupling concept its own, honestly-named type instead of overloading an existing one.

**Ordering/tick-alignment risk (open question):** `SourceTerm()` and `DestTerm()` must be read from the SAME tick's state. If refuse's `SnapshotProvider` runs earlier in the phase pipeline and farming's runs later, a naive "call both functions when `transferCheck.Check` runs" could read refuse's *this-tick* value against farming's *last-tick* value if farming hasn't updated its term function's closure yet. This needs a ruling: either (a) both `TermFunc`s must be guaranteed pure reads of already-committed per-tick state (i.e., `RegisterTransfer` runs its check no earlier than both modules' own phase hooks have run for the tick — an ordering constraint on `Wire`/phase registration), or (b) `TransferPair` should snapshot both terms into the `Snapshot` structure itself (each module writes its shared term into `Snapshot.Readings` under an agreed key, and `transferCheck` reads from `Snapshot` like `stockCheck` does, never calling live closures) — option (b) is more consistent with the existing "Snapshot is the single per-tick input" architecture (doc.go, snapshot.go) and is the recommended default, but is a call for Bill given it changes what a "shared term" writes into.

## 4. Backward compatibility / migration for the 1 existing caller pattern

There is no literal existing `RegisterStock` caller to migrate (§1 — it doesn't exist yet), but there are 4 existing hand-written `Invariant` types (`people.go`, `money.go`, `goods.go`, `vehicle.go`) built directly against `stockCheck`/`Registry.Register`, all `done`-adjacent and cited by numbered ACs in `engine.invariant.md` (AC-2 through AC-6). **Recommendation: do not migrate them.** `RegisterStock` is additive sugar for NEW stocks (refuse, dispatch, education, social, and any future module); the 4 v1 types keep working exactly as today, since `RegisterStock` is a constructor that produces the same `Invariant`-satisfying shape they already hand-write, not a replacement for the interface. This avoids re-touching already-reviewed, AC-numbered code for a refactor with no functional benefit, consistent with the same caution `engine.invariant.md`'s own "For Bill" escalation shows about renumbering ACs on live code.

## 5. Where the check runs / failure & reporting shape (GR#7)

No new run-time hook or suite: both `RegisterStock`'s per-stock `Invariant` and `RegisterTransfer`'s `transferCheck` are ordinary `Invariant` implementations registered into the same `*Registry`, run by the same `RunSuite` (`registry.go`), wired via the same `Wire`/`WireDaily` (`wire.go`) against the same `core.PhaseKind` the caller already chose. `SuiteResult.Outcomes` gains entries named `pair.Name` for transfer checks alongside the existing per-stock names — no change to `SuiteResult`'s shape (AC-1b's ran/skipped/violation structure already generalises).

Error/reporting: reuse `MET-E300` (`ErrConservationViolation`) for both ordinary and transfer violations — a transfer mismatch IS a conservation violation, just with a two-module cause rather than a within-module one; `Violation.InvariantName` (already free text, `pair.Name` in the transfer case) is how a consumer tells them apart, so no new registry code is needed for the violation-reporting path itself. Two NEW registry codes are needed for construction-time misuse, mirroring `ErrNilInvariant`/`ErrDuplicateInvariant`'s existing pattern:

- `ErrNilTermFunc` (next free code after `MET-E304`, i.e. `MET-E305`) — `RegisterStock`/`RegisterTransfer` called with a nil `TermFunc` in `ins`/`outs`/`SourceTerm`/`DestTerm`.
- `ErrEmptyTermSet` (`MET-E306`) — `RegisterStock` called with both `ins` and `outs` empty (a stock with no tracked flows at all can never balance meaningfully; this is very likely a caller bug, not a legitimate zero-flow stock, and should be rejected loudly rather than silently registering a check that always "balances" against `TrackedDelta == 0` for the wrong reason).

Both added to `data/errors.json`'s MOD-019 E300-E399 range per GR#7, same as the existing four.

## 6. Summary of proposed additions (all new, nothing removed or renamed)

| Addition | Kind | Backward compat |
|---|---|---|
| `TermFunc` | type | new |
| `RegisterStock(reg, name, stock, opening, closing, ins, outs)` | func | new; 4 v1 stocks NOT migrated (§4) |
| `StockReading.Terms map[string]int64` | field | additive, nil-safe, unread by existing `stockCheck` |
| `Violation.Terms map[string]int64` | field | additive, nil-safe |
| `TransferPair` | type | new |
| `RegisterTransfer(reg, pair)` | func | new |
| `MET-E305` `ErrNilTermFunc`, `MET-E306` `ErrEmptyTermSet` | registry errors | new, additive |

## 7. Open questions requiring a Bill/Aaron ruling (not to be unilaterally decided by a junior)

1. **Does `RegisterStock` own per-tick term evaluation, or does it only supply a builder that the existing `SnapshotProvider` must call?** (§2.a) — affects whether there are one or two write paths into `Snapshot.Readings`.
2. **Cross-module tick alignment for `RegisterTransfer`** (§3): live-closure comparison with an ordering guarantee, vs. routing shared terms through `Snapshot` itself so `transferCheck` reads committed per-tick state like every other invariant. Recommendation given (option b, route through `Snapshot`) but this changes what "a shared term" means architecturally and should be ruled on, not assumed.
3. **Naming convention for `TransferPair.Name` and the shared term keys** (e.g. `"refuse.compost"` vs. a more structured key) — needs to be consistent enough that a future third/fourth cross-module pair (are there others beyond refuse<->farming among the 6+ modules BUG-067 names — dispatch, education, social?) doesn't each invent its own ad hoc naming.
4. **Should `RegisterStock`'s `ins`/`outs` maps be validated for a total-zero-flow stock at registration time (`ErrEmptyTermSet`, §5), or is a legitimate zero-flow-tracked stock (e.g. a stock that only ever changes via one already-covered term) a real case this would wrongly reject?** Needs a concrete example from one of the 6 target modules before finalizing as a hard rejection vs. a warning.
5. **Do dispatch (fleet), education (cohort), and social (cases) — the other three modules BUG-067 names as multi-term but single-module — actually need anything beyond `RegisterStock` (§2.a), or does the refuse<->farming pair remain the ONLY case needing `RegisterTransfer`?** This document assumes yes based on BUG-067's own description (dispatched-returned, enrolled-graduated-dropped, opened-closed are all within-module multi-term, not cross-module), but each owning module's BA should confirm before `RegisterTransfer` is scoped as narrowly as "just refuse<->farming."
