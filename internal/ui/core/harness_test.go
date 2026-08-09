package core

import (
	"testing"
	"time"
)

// waitForCondition polls cond every millisecond for up to a second and
// fails the test if it never becomes true. Used across this package's
// tests to synchronize against goroutines (InputLoop/RenderLoop/
// ViewsLoop) without sleeping a fixed, flaky duration.
func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met within timeout")
	}
}
