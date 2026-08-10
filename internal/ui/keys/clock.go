package keys

import "time"

// Clock is the injectable time source this package's leader-sequence
// inactivity timeout (AC-2d, ASM-117) is built on. T-INPUT's contract is
// non-blocking (UI-SPEC §1/§5) — this package never sleeps or starts its
// own timer goroutine to implement the timeout; instead it records when a
// sequence went pending and leaves it to the caller (typically driven off
// the same tick a render loop already runs on) to call
// [KeyGrammar.CheckIdleTimeout] with the current time. A test drives this
// deterministically with a fake Clock, advancing it past the threshold
// and calling CheckIdleTimeout directly — no wall-clock sleep, ever, on
// this path (BUG-031's lesson: never assert on wall-clock timing).
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a plain func() time.Time to a Clock, mirroring the
// stdlib http.HandlerFunc idiom — the common case for both the real
// clock below and a test's fake one.
type ClockFunc func() time.Time

// Now implements Clock.
func (f ClockFunc) Now() time.Time { return f() }

// systemClock is the ONE site in this package that calls time.Now
// (grep -rn "time\.Now|time\.Since" internal/ui/keys/*.go, excluding
// _test.go — AC-16). Used only as NewKeyGrammar's default when the
// caller passes a nil Clock; every timeout computation elsewhere in this
// package goes through the injected Clock interface, never this
// function, directly.
var systemClock Clock = ClockFunc(time.Now)
