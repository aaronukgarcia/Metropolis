package debug

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// FidelityDial is the minimal HOT-radius control surface (§5.2's
// adaptive fidelity dial) that whichever module ends up owning
// adaptive fidelity implements. This package only gates access to it
// (AC-8) — it does not own the radius value, the promotion logic, or
// the cost formula (no such module exists yet; out of scope, see
// doc.go). A caller with a real fidelity-dial implementation wires it
// in via WithFidelityDial.
type FidelityDial interface {
	// Range reports the dial's valid HOT-radius bounds (min, max).
	Range() (min, max int)
	// Current reports the presently configured HOT radius.
	Current() int
	// Cost reports the current radius's observable cost (§14: "watch
	// cost").
	Cost() float64
	// SetRadius adjusts the HOT radius, returning an error if r is
	// outside Range() or otherwise rejected by the owning
	// implementation.
	SetRadius(r int) error
}

// FidelityDial returns the injected FidelityDial implementation, gated
// on debug being on (AC-8). Rejected with a registry-sourced error when
// debug is off (AC-9/AC-11) or when no FidelityDial has been configured
// via WithFidelityDial.
func (s *State) FidelityDial(correlationID string) (FidelityDial, error) {
	if err := s.requireOn(correlationID, "fidelity-dial"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	d := s.fidelityDial
	s.mu.Unlock()
	if d == nil {
		return nil, errs.New(ErrFidelityDialNotConfigured, correlationID, nil)
	}
	return d, nil
}
