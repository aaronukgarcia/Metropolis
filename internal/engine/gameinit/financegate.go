package gameinit

// financegate.go documents (AC-2, engine.finance edge) how a *GameInit
// satisfies internal/engine/finance's injected mode-gate contract
// WITHOUT this package importing internal/engine/finance -- the interface
// is satisfied structurally, mirroring
// deathservices.SaveParticipant/citizens' save.Participant precedent
// exactly (GR#20's "modules consume each other only via registered
// interfaces" contract cuts both ways: engine.finance's outbound edge to
// feat.gameinit is NOT registered, only feat.gameinit -> engine.finance
// is, so this package must never import finance -- finance instead
// declares its own small ModeGate interface and this package's
// api.go-defined Unlimited(correlationID string) (bool, error) method
// already matches its shape).
//
// The one-line composition-root call this seam needs (see doc.go's
// "Composition seam" section):
//
//	financeAPI.SetModeGate(gi) // gi is the *GameInit constructed at new-game start
//
// financeAPI.SetModeGate mirrors FinanceAPI.SetMilestoneGate's existing
// post-construction wiring precedent exactly: no constructor argument, so
// every existing/future finance test that never calls SetModeGate is
// unaffected (a nil gate is finance's own documented "real mode, unchanged
// behaviour" default -- see finance/mode.go's doc for the mode-gate
// contract itself).
//
// FEAT-143 round finding P2-B: finance.ModeGate.Unlimited now takes a
// correlationID and returns (bool, error) rather than a bare bool, and
// finance.unlimitedLocked (mode.go) fails CLOSED toward Real mode AND
// records finance.ErrModeGateFailed via the registry whenever this
// *GameInit's own SEC-020 copy-guard trips -- see Unlimited's doc comment
// (api.go) for why a bare-bool contract made a copied *GameInit's
// Unlimited-mode session silently, undetectably downgrade to Real.
