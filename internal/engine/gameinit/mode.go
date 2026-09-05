package gameinit

// Mode is one of feat.gameinit's exactly-two initialization modes (AC-1).
// A new game is initialized in exactly one of these -- never both, never
// neither, and the zero value ("") is deliberately NOT a valid Mode, so a
// zero-value default can never silently pass as a real mode (AC-1's
// false-pass-risk note).
type Mode string

const (
	// ModeReal: finite starting capital (AC-6), the full financial-failure
	// loop active (AC-2) -- the survival game.
	ModeReal Mode = "real"

	// ModeUnlimited: financial checks bypassed, an un-depletable reserve
	// (AC-2) -- the sandbox/experimentation mode.
	ModeUnlimited Mode = "unlimited"
)

// Valid reports whether m is one of the two known modes (AC-1). The zero
// value and any other string are invalid.
func (m Mode) Valid() bool {
	return m == ModeReal || m == ModeUnlimited
}

// String returns the mode's wire/display form -- the exact string this
// package's savewire.go threads into save.Context.GameMode and
// save.WithExpectedGameMode (feat.saveux), and financegate.go/the UI
// publisher read via Unlimited() rather than comparing this string
// directly.
func (m Mode) String() string {
	return string(m)
}

// ParseMode parses s into a Mode, rejecting anything other than the two
// known wire values with [ErrUnknownGameMode] (AC-1/AC-5's fail-closed
// contract: an absent or unrecognised mode string is never silently
// coerced to a default).
func ParseMode(s string, correlationID string) (Mode, error) {
	m := Mode(s)
	if !m.Valid() {
		return "", newUnknownGameModeErr(s, correlationID)
	}
	return m, nil
}
