package gameinit

// savewire.go documents (AC-4/AC-5, feat.saveux edge) exactly how a
// *GameInit's locked mode reaches internal/engine/save's Meta/Load
// surface. This package deliberately does NOT import
// internal/engine/save -- no feat.saveux -> feat.gameinit reverse edge is
// registered (only the forward feat.gameinit -> feat.saveux edge exists in
// code.json), so save.Meta's new GameMode field and save.WithExpectedGameMode
// LoadOption are added directly to the save package (see that package's
// kind.go/options.go/load.go/errors.go), never here.
//
// GameModeWire returns the exact string a composition-root caller threads
// into BOTH sides of feat.saveux's contract:
//
//   - Write path: set save.Context.GameMode = gi.GameModeWire() before
//     every SaveManual/Autosave/Milestone call, so the mode is declared on
//     the initial save and every subsequent save (AC-4).
//   - Read path: pass save.WithExpectedGameMode(gi.GameModeWire()) to every
//     Manager.Load/LoadAt/LoadLatest call, so a bundle whose declared mode
//     differs (or is absent) fails closed with save's own
//     ErrGameModeMismatch rather than silently re-moding the session
//     (AC-5).
//
// Both are one-line composition-root calls; see doc.go's "Composition
// seam" section for the exact call shape and this module's home
// (internal/engine/gameinit/) for where the *GameInit instance itself is
// constructed once, at new-game start.
//
// Returns [ErrGameInitCopied] on a struct-copied *GameInit rather than
// silently returning "" (FEAT-143 round finding P2-B/P2-A): an empty
// wire string used to be indistinguishable from "no mode declared" both
// here and in save.WithExpectedGameMode, which is exactly the escape
// hatch save's own attack test (TestAttackFEAT143_EmptyExpectedModeIsAnEscapeHatch)
// found. A caller MUST now check the error and never thread "" into
// save.Context.GameMode or save.WithExpectedGameMode on failure.
func (g *GameInit) GameModeWire(correlationID string) (string, error) {
	if err := g.checkNotCopied(correlationID, "GameModeWire"); err != nil {
		return "", err
	}
	return g.mode.String(), nil
}
