package core

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// NewScreen constructs and initializes a real tcell.Screen for the
// running terminal. A tcell initialization failure (AC-8: "e.g. no
// compatible terminal") is returned as a clear, registry-sourced
// *errs.E (MET-U001) rather than a panic or a silently blank screen —
// the caller (cmd/metropolis, later) is expected to display *errs.E's
// Display() text and exit, not attempt to render into a screen that
// failed to come up.
//
// correlationID should be minted by the caller (errs.NewCorrelationID())
// so a startup failure is traceable the same way any other Metropolis
// error is (GR#1).
//
// This is construction, not rendering: it runs once, before T-RENDER's
// goroutine exists, so it is not a violation of AC-3's single-goroutine
// screen-access rule. To keep AC-3's grep check unambiguous (it looks
// for the receiver name "screen." across this package's non-test
// files), the returned value is deliberately not bound to a local named
// "screen" here.
func NewScreen(correlationID string) (tcell.Screen, error) {
	return newScreenWith(correlationID, tcell.NewScreen)
}

// newScreenWith is NewScreen's implementation, parameterized on the
// tcell.Screen constructor so screen_test.go can inject a failing
// constructor or a fake Screen whose Init fails, without needing a real
// (or absent) terminal in CI.
func newScreenWith(correlationID string, ctor func() (tcell.Screen, error)) (tcell.Screen, error) {
	scr, err := ctor()
	if err != nil {
		return nil, errs.Wrap("MET-U001", correlationID, err, map[string]any{"cause": err.Error()})
	}
	if err := scr.Init(); err != nil {
		return nil, errs.Wrap("MET-U001", correlationID, err, map[string]any{"cause": err.Error()})
	}
	return scr, nil
}
