package invariant

import "time"

// livenessTimeout bounds how long a "prove this did not hang" test
// waits before declaring a hang. This is a LIVENESS safety net, not a
// performance assertion — BUG-031's lesson (never assert on wall-clock
// as a pass/fail ceiling for CORRECT, possibly-slow-under-load
// behaviour) does not apply here: a correctly-rejected call returns in
// microseconds, so a generous multi-second bound only ever fires for an
// actual hang, never for a slow-but-correct CI runner.
const livenessTimeout = 5 * time.Second

func timeoutChan() <-chan time.Time {
	return time.After(livenessTimeout)
}
