package widgets

// PulseDurationMS is the shared threshold-pulse duration — UI-SPEC §2:
// "a 300 ms highlight pulse on any value that just crossed a
// threshold."
const PulseDurationMS = 300

// PulseState is the shared, reusable threshold-pulse animation
// primitive (AC-10: "not per-widget bespoke code"). It is a plain data
// value with no wall-clock dependency of its own — GR#21/AC-12: the
// *timing* is the caller's render-loop's job (core's T-RENDER 10Hz
// tick advances ElapsedMS by its own frame delta); this package never
// samples the wall clock. A widget that wants a pulse effect (e.g. BigNum
// recolouring bright for 300ms after a threshold cross) reads
// state.Active and lightens/brightens its style accordingly — that
// styling choice is the widget's, not pulse.go's; this file only
// tracks whether "now" is inside the pulse window.
type PulseState struct {
	// Active is true while the pulse is still within its window.
	Active bool
	// ElapsedMS is milliseconds since TriggerPulse, advanced by TickPulse.
	ElapsedMS int
}

// TriggerPulse returns a freshly started pulse: Active, ElapsedMS 0.
// Call this the instant a caller-tracked value crosses its configured
// threshold (that crossing detection is the caller's concern — e.g.
// comparing Thresholds.State across two consecutive frames — pulse.go
// only owns the resulting highlight window).
func TriggerPulse() PulseState {
	return PulseState{Active: true, ElapsedMS: 0}
}

// TickPulse advances state by deltaMS (the caller's own frame delta,
// never internally sampled from the wall clock) and returns the
// updated state: still Active while ElapsedMS < PulseDurationMS, and
// permanently inactive once it reaches or exceeds that duration. A
// negative deltaMS is treated as 0 (a caller bug elsewhere should not
// be able to rewind a pulse backwards past reactivation). Ticking an
// already-inactive state is a no-op (ElapsedMS does not keep climbing
// forever; ticking a finished pulse for an hour of frames costs the
// same one branch as ticking it once).
func TickPulse(state PulseState, deltaMS int) PulseState {
	if !state.Active {
		return state
	}
	if deltaMS < 0 {
		deltaMS = 0
	}
	state.ElapsedMS += deltaMS
	if state.ElapsedMS >= PulseDurationMS {
		state.ElapsedMS = PulseDurationMS
		state.Active = false
	}
	return state
}
