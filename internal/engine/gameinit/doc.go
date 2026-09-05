// Package gameinit implements FEAT-143 (mkey feat.gameinit, code.json guid
// 939179db-288c-42d4-8dc0-4ba794acfda5): Game Initialization Modes -- the
// player's choice, locked at new-game start, between Real (finite starting
// capital, full financial-failure loop) and Unlimited Money (sandbox: the
// finance failure loop bypassed, an un-depletable reserve).
//
// # Spec refs
//
// FEAT-143's own Desc is the full feature spec. Master plan anchors:
// docs/METROPOLIS-MASTER-v2.1.md §7 finance/insolvency (line 212,
// "Insolvency (can't meet obligations for 3 consecutive months with no
// available credit) = game over"); §13 line 12 ("The player starts with
// money"); §39. Acceptance criteria: docs/planning/acceptance/feat.gameinit.md
// (AC-1..AC-7).
//
// # Registered edges (code.json, GR#20/GR#25)
//
// feat.gameinit has exactly three outbound edges: engine.finance,
// feat.saveux, ui.screen.finance. This package consumes each ONLY through
// its registered contract:
//
//   - engine.finance: this package implements finance.ModeGate (a tiny
//     Unlimited() bool interface finance.FinanceAPI already declares as its
//     injected mode policy, mirroring FinanceAPI.MilestoneGate's
//     post-construction SetMilestoneGate precedent) and hands a *GameInit to
//     the composition root to wire via FinanceAPI.SetModeGate. This package
//     never reaches into finance's ledger/accounts directly.
//   - feat.saveux: this package never imports internal/engine/save (no such
//     reverse dependency is registered). Instead it exposes GameInit.Mode()
//     as a plain string the composition root threads through
//     save.Context.GameMode (write path) and save.WithExpectedGameMode
//     (read/enforcement path) -- both fields/options live in the save
//     package itself (feat.saveux), which is the one that actually owns
//     Meta and Load. See this package's savewire.go for the exact call
//     shape and doc.go's "Composition seam" section below.
//   - ui.screen.finance: this package exposes GameInit.Mode()/Unlimited()
//     (each error-returning as of the P2-B fix, see api.go); the
//     composition root's f2.finance publisher (owned elsewhere) reads
//     them to populate wirePatch.UnlimitedMoney. This package never
//     imports internal/ui/screens/finance.
//
// # Lock-at-startup, immutable for the session (AC-3, GR#12)
//
// A *GameInit is constructed exactly once, at new-game start, via [New] or
// [Load]/[LoadDefault] -- the Mode argument is captured into an unexported
// field and there is no exported setter that ever mutates it (see
// immutability_test.go's grep-based proof: no `\bmode\s*=` assignment --
// bare or through a selector -- anywhere in this package's production code
// outside the constructor's struct literal). [GameInit.SetGameMode] exists
// as the one explicit
// mode-changing command surface a dev-console or settings screen might
// route to -- it ALWAYS returns [ErrModeLocked] and never touches the
// locked field, so every mode-changing surface funnels through one
// rejecting entry point rather than each caller inventing its own "sorry,
// no" logic.
//
// # Composition seam
//
// The lead owns internal/engine/compose/compose.go and this package does
// NOT edit it. The one-line calls a future compose.go wiring pass needs
// are (see savewire.go and financegate.go for the exact signatures):
//
//	gi, err := gameinit.New(mode, cfg, correlationID)      // once, at new-game start
//	financeAPI.SetModeGate(gi)                              // engine.finance edge
//	wire, err := gi.GameModeWire(correlationID)             // MUST check err (P2-B/P2-A):
//	if err != nil { /* handle -- never thread "" through */ }
//	ctx.GameMode = wire                                     // feat.saveux write path (save.Context)
//	save.WithExpectedGameMode(wire)                         // feat.saveux read path (passed to Manager.Load)
//
// (GameModeWire, Mode, Unlimited, and StartingCapitalMicropounds all
// return an error alongside their value as of the FEAT-143 round's P2-B
// fix -- a struct-copied *GameInit rejects every call rather than
// silently reporting a zero value, and every one of these composition
// calls must check the error rather than assume success.)
//
// # Data-sourced starting capital (AC-6, GR#15)
//
// The real-mode starting treasury balance is loaded from
// data/gameinit.json (config.go), never a bare Go literal. The file's
// startingCapitalMicropounds carries a placeholder:true disclosure per the
// standing balance-number regime -- see feat.gameinit.md's Escalations.
// AC-6 checks only that the value is finite and strictly positive, never
// this magnitude.
package gameinit
