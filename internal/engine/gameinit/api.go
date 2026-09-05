package gameinit

import (
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// GameInit is feat.gameinit's inbound surface (code.json guid
// 939179db-288c-42d4-8dc0-4ba794acfda5): the locked, immutable-for-the-
// session initialization mode (AC-1/AC-3) plus the loaded, data-sourced
// config (AC-6).
//
// The zero value is not usable; construct via [New], [Load], or
// [LoadDefault]. A *GameInit is safe for concurrent use: mode and cfg are
// set exactly once at construction and never mutated afterwards (there is
// no mutex because there is nothing to guard -- every field is
// write-once), and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020 family, mirroring
// deathservices.DeathServicesAPI / finance.FinanceAPI).
type GameInit struct {
	mode          Mode
	cfg           Config
	correlationID string

	// self is the SEC-020 copyguard (atomic.Pointer, mirroring
	// deathservices.DeathServicesAPI.self / finance.FinanceAPI.self):
	// stored exactly once, at the end of construction, before the value
	// is returned to any caller.
	self atomic.Pointer[GameInit]
}

func newUnknownGameModeErr(mode string, correlationID string) error {
	return errs.New(ErrUnknownGameMode, correlationID, map[string]any{"mode": mode})
}

// New constructs a *GameInit locked at mode (AC-1/AC-3). Rejects an
// invalid mode with [ErrUnknownGameMode] -- there is no zero-value/default
// fallback (AC-1's false-pass-risk note: the engine's own mode is a real
// enum value read back from this call, never a silently-defaulted zero
// value).
func New(mode Mode, cfg Config, correlationID string) (*GameInit, error) {
	if !mode.Valid() {
		return nil, newUnknownGameModeErr(mode.String(), correlationID)
	}
	g := &GameInit{mode: mode, cfg: cfg, correlationID: correlationID}
	g.self.Store(g)
	return g, nil
}

// Load reads and validates data/gameinit.json (via [LoadConfig]) and
// returns a *GameInit locked at mode.
func Load(dir string, mode Mode, correlationID string) (*GameInit, error) {
	cfg, err := LoadConfig(dir, correlationID)
	if err != nil {
		return nil, err
	}
	return New(mode, cfg, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it, locked at mode.
func LoadDefault(mode Mode, correlationID string) (*GameInit, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, mode, correlationID)
}

// checkNotCopied rejects a method call on a struct copy of the *GameInit
// New/Load/LoadDefault returned.
func (g *GameInit) checkNotCopied(correlationID, method string) error {
	if g.self.Load() != g {
		return errs.New(ErrGameInitCopied, correlationID, map[string]any{"method": method})
	}
	return nil
}

// Mode returns the locked initialization mode (AC-1/AC-3). Never changes
// across the session's lifetime.
//
// Returns [ErrGameInitCopied] on a struct-copied *GameInit rather than
// silently reporting the zero Mode (FEAT-143 round finding P2-B,
// TestAttackFEAT143_CopiedGameInitSilentlyReportsRealMode): a swallowed
// copy-guard error here used to make a copy of an Unlimited session
// report itself as an unlocked, empty-string Mode with no error on any
// channel. correlationID identifies THIS call (GR#1) -- pass the
// caller's own correlation id, not necessarily the one the *GameInit was
// constructed with.
func (g *GameInit) Mode(correlationID string) (Mode, error) {
	if err := g.checkNotCopied(correlationID, "Mode"); err != nil {
		return "", err
	}
	return g.mode, nil
}

// Unlimited reports whether the session is running in Unlimited Money
// mode (AC-2). This is the exact predicate [GameInit] hands
// engine.finance as its injected mode gate (financegate.go) and
// ui.screen.finance's publisher reads for the infinite indicator (AC-7).
//
// Returns [ErrGameInitCopied] on a struct-copied *GameInit rather than
// silently reporting false (FEAT-143 round finding P2-B): finance.ModeGate
// (mode.go) is the one caller this matters most for -- see mode.go's
// unlimitedLocked for how a returned error there fails CLOSED toward Real
// mode rather than silently treating a wiring bug as "Unlimited".
func (g *GameInit) Unlimited(correlationID string) (bool, error) {
	if err := g.checkNotCopied(correlationID, "Unlimited"); err != nil {
		return false, err
	}
	return g.mode == ModeUnlimited, nil
}

// Config returns the loaded configuration (read-only value copy).
func (g *GameInit) Config() (Config, error) {
	if err := g.checkNotCopied(g.correlationID, "Config"); err != nil {
		return Config{}, err
	}
	return g.cfg, nil
}

// StartingCapitalMicropounds returns the real-mode starting treasury
// balance (AC-6). Callers that want "what should the treasury open at"
// still need to consult Mode()/Unlimited() themselves -- this accessor
// only ever returns the data-sourced real-mode figure, regardless of the
// locked mode, since an un-depletable Unlimited reserve is not a finite
// number at all (AC-2's "genuine bypass, not a huge balance" ruling).
//
// Returns [ErrGameInitCopied] on a struct-copied *GameInit rather than
// silently reporting 0 (FEAT-143 round finding P2-B).
func (g *GameInit) StartingCapitalMicropounds(correlationID string) (int64, error) {
	if err := g.checkNotCopied(correlationID, "StartingCapitalMicropounds"); err != nil {
		return 0, err
	}
	return g.cfg.StartingCapitalMicropounds(), nil
}

// SetGameMode is the ONE explicit mode-changing command surface this
// package exposes (AC-3, GR#12) -- the entry point a dev-console command
// or a settings-screen write would route to. It ALWAYS returns
// [ErrModeLocked] and NEVER mutates g.mode: the mode is captured once at
// New/Load time and is immutable for the whole session, no matter which
// surface asks. See immutability_test.go for the mechanical proof that no
// other code path in this package ever assigns to the mode field either.
func (g *GameInit) SetGameMode(requested Mode) error {
	if err := g.checkNotCopied(g.correlationID, "SetGameMode"); err != nil {
		return err
	}
	return errs.New(ErrModeLocked, g.correlationID, map[string]any{
		"currentMode":   g.mode.String(),
		"requestedMode": requested.String(),
	})
}
