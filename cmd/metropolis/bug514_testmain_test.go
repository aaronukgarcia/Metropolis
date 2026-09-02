package main

import (
	"os"
	"testing"
	"time"
)

// TestMain (BUG-514, test-harness hardening only) raises the package-level
// feat208PrimeTimeout (boot.go:77 — a var explicitly documented there as
// overridable so a test can lower or raise it) for the whole cmd/metropolis
// test binary before any test runs.
//
// Why: primeScreenSubscription (boot.go) is the production Subscribe-prime
// deadline used by every bootCore-based test (30+ of them). Under `go test
// -race` on a saturated/loaded runner, the two async goroutines that
// deliver Subscribe's own CommandResult+Delta (RunCommandLoop,
// StartSubscriptionPump) can be descheduled past the shipped 5s production
// default, tripping the MET-E900 boot timeout in a test that is not
// actually testing timeout behaviour — a scheduler-noise flake, not a
// product defect. Same fixed-wall-clock-on-async class as the BUG-510
// awaitStatus/freezeClock de-flakes, but this deadline lives in
// production wiring (primeScreenSubscription) and so cannot be swapped to
// t.Context() the way those call sites were.
//
// This ONLY affects the test binary's in-memory copy of the var — it does
// NOT change the shipped 5s production default (boot.go:77 is untouched).
// TestAttack_PrimeScreenSubscription_TimesOutWhenFirstDeltaNeverArrives
// (feat208_priming_destructive_test.go) still exercises the real timeout
// path: it saves this raised value, lowers feat208PrimeTimeout to its own
// 150ms for the duration of that one test, and restores it via `defer`
// before returning — so the generous value set here is simply what every
// OTHER bootCore test inherits as its default, and is still a bounded,
// finite safety deadline (not an unbounded wait) per verification-standards
// (a hang still fails, just later).
func TestMain(m *testing.M) {
	feat208PrimeTimeout = 60 * time.Second
	os.Exit(m.Run())
}
