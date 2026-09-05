package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/gameinit"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// compose_gameinit.go is BUG-737's fix: FEAT-143 (mkey feat.gameinit) was
// built (internal/engine/gameinit/, full API + tests) but never wired at
// the composition root — Wire never called gameinit.New, financeAPI never
// got SetModeGate, no save ever declared a GameMode, and the f2.finance
// UnlimitedMoney banner never had anything to publish. This file is the
// one-line composition seam gameinit/doc.go's own "Composition seam"
// section documents, landed exactly as specified there:
//
//	gi, err := gameinit.New(mode, cfg, correlationID)      // once, at new-game start
//	financeAPI.SetModeGate(gi)                              // engine.finance edge
//	wire, err := gi.GameModeWire(correlationID)             // save.Context.GameMode / WithExpectedGameMode
//
// GR#25 note (the original BUG-737 dispatch STOPPED here): this file only
// exists because the feat.compositionroot -> feat.gameinit edge is now
// registered in code.json (SSOT batch 3, trunk 7bd8237) — confirmed live
// via `node tools/plan/edge-lint.js` reporting 0 NEW findings with this
// file's "github.com/.../internal/engine/gameinit" import present. Before
// that landed, importing gameinit here would have been an unregistered
// dependency (an instant fail-closed EDGE-LINT-001 finding), so the
// original round correctly stopped rather than building around it.
//
// cmd/metropolis (feat.skeleton) and cmd/metroserve do NOT have a
// registered edge to feat.gameinit (checked the same way: neither key
// appears in feat.gameinit's code.json inbound.consumers, and
// cmd/metroserve is not even a registered module) — so neither may import
// this package directly. They instead pass Deps.GameMode as a plain wire
// STRING (gameinit.Mode's own String() values, "real"/"unlimited"/""),
// which this file is the ONLY place that ever turns into a real
// *gameinit.GameInit. See Deps.GameMode's own doc comment (compose.go) for
// why the seam is a bare string rather than a typed value.

// resolveGameMode parses raw (Deps.GameMode) into a gameinit.Mode,
// defaulting to gameinit.ModeReal when raw is empty. Empty is every
// pre-BUG-737 Wire caller/test (Deps zero value) and MUST reproduce prior
// behaviour byte-for-byte: real mode, the full financial-failure loop,
// treasury sourced from data/gameinit.json's startingCapitalMicropounds
// (deliberately kept equal to the pre-wiring initialTreasury literal, see
// data/gameinit.json's disclosure). A non-empty, unrecognised value is
// NEVER silently coerced to Real (AC-1's false-pass-risk note) — it
// propagates gameinit.ErrUnknownGameMode up through wireGameInit/Wire.
func resolveGameMode(raw, correlationID string) (gameinit.Mode, error) {
	if raw == "" {
		return gameinit.ModeReal, nil
	}
	return gameinit.ParseMode(raw, correlationID)
}

// wireGameInit constructs FEAT-143's *gameinit.GameInit at composition
// time (once, at new-game start — AC-1/AC-3) and installs it as
// financeAPI's mode gate (financeAPI.SetModeGate, the engine.finance edge
// gameinit/financegate.go documents) so every placement/OPEX/payroll check
// and the insolvency/debt-rating triggers consult it from the very first
// tick (AC-2). The data-sourced config backing the returned instance is
// loaded via gameinit.LoadDefault, which resolves data/'s directory
// through the existing foundation/data seam (AC-6/GR#15) — Wire never
// hand-rolls a second data-dir resolution.
//
// Returns the constructed *gameinit.GameInit so Wire's caller can also
// read GameModeWire()/StartingCapitalMicropounds() for the save-context
// (save_wire.go) and opening-treasury seams. Every failure — an
// unrecognised mode string, a data-load/validation failure, or
// SetModeGate rejecting a struct-copied FinanceAPI — is a registry-sourced
// *errs.E naming "gameinit" (GR#1), never a silent nil/zero fallback.
func wireGameInit(financeAPI *finance.FinanceAPI, rawMode, correlationID string) (*gameinit.GameInit, error) {
	mode, err := resolveGameMode(rawMode, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "gameinit", "step": "resolveGameMode", "rawMode": rawMode})
	}
	gi, err := gameinit.LoadDefault(mode, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "gameinit", "step": "LoadDefault"})
	}
	if err := financeAPI.SetModeGate(gi); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{"module": "gameinit", "step": "SetModeGate"})
	}
	return gi, nil
}

// GameMode returns the composed session's locked FEAT-143 initialization
// mode as its wire string ("real"/"unlimited") — the same value threaded
// into save.Context.GameMode and save.WithExpectedGameMode
// (save_wire.go), and the GR#17 read accessor for anything (tests,
// inspection tooling, a future dev-console readout) that wants to observe
// the live mode without re-deriving it. Mirrors this package's other
// read-only Composition accessors (Population/Treasury/...): a *GameInit
// is never nil after a successful Wire (wireGameInit either returns one or
// Wire itself fails), so the only realistic failure is the SEC-020
// copy-guard tripping on an impossible struct-copied instance — treated
// the same way finance/mode.go's unlimitedLocked treats a failing gate:
// fails CLOSED to the "real" string rather than propagating an error a
// simple accessor's callers are not set up to handle.
func (c *Composition) GameMode() string {
	if c.state.gameInit == nil {
		return gameinit.ModeReal.String()
	}
	wire, err := c.state.gameInit.GameModeWire(c.state.cid)
	if err != nil {
		return gameinit.ModeReal.String()
	}
	return wire
}
