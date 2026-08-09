package core

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestEngine_ZeroValue_FailsClosed_NoMuTouch proves the zero-value case
// Destructive-2's reachability argument depends on: `var e Engine`
// (never passed through NewEngine, so self was never stored) must be
// rejected the same way a copy is -- and, because the identity check
// (SEC-016) now runs before mu, must be rejected WITHOUT ever touching
// mu or the nil hooks map (a bare `Engine{}`'s hooks field is nil, so
// reaching the `e.hooks[kind] = append(...)` line would nil-map-write
// panic; the pre-lock identity check means that line is never reached
// for this case either). Unlike TestSEC016_CopyDuringLock_RejectedNotHung
// (sec016_poc_test.go, excluded from -race builds because its attack IS
// a data race by construction), this test performs no concurrent copy
// and no unsynchronized memory access, so it runs under `go test
// ./... -race -count=1` like everything else in this package.
func TestEngine_ZeroValue_FailsClosed_NoMuTouch(t *testing.T) {
	var e Engine

	err := e.RegisterPhaseHook(PhaseDailyTick, noopHook{})
	if !errors.Is(err, &errs.E{Code: ErrEngineCopied}) {
		t.Fatalf("zero-value Engine RegisterPhaseHook: err = %v, want ErrEngineCopied", err)
	}

	err = e.AdvanceTicks("corr-zero-value", 1)
	if !errors.Is(err, &errs.E{Code: ErrEngineCopied}) {
		t.Fatalf("zero-value Engine AdvanceTicks: err = %v, want ErrEngineCopied", err)
	}
}
