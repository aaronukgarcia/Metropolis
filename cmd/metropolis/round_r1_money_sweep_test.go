package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// Attack: every live screen, whole frame; any run of five or more digits that
// is not comma-grouped, or any pence figure, is money the new style missed.
func TestRoundR1Money_LiveSweepNoRawFigures(t *testing.T) {
	r := rcNew(t)
	plAdvance(t, r.w, 60)
	bare := regexp.MustCompile(`(^|[^0-9,.])[0-9]{5,}([^0-9,]|$)`)
	pence := regexp.MustCompile(`[0-9]\.[0-9]{2}\b`)
	keys := []struct {
		n string
		k tcell.Key
	}{{"F1", tcell.KeyF1}, {"F2", tcell.KeyF2}, {"F3", tcell.KeyF3}, {"F4", tcell.KeyF4}, {"F5", tcell.KeyF5}, {"F6", tcell.KeyF6}, {"F7", tcell.KeyF7}, {"F8", tcell.KeyF8}, {"F9", tcell.KeyF9}, {"F10", tcell.KeyF10}, {"F11", tcell.KeyF11}, {"F12", tcell.KeyF12}}
	dir := os.Getenv("ROUND_OUT")
	for _, s := range keys {
		f := r.snap(s.n, s.k)
		if dir != "" {
			_ = os.WriteFile(filepath.Join(dir, "frame_"+s.n+".txt"), []byte(f), 0o644)
		}
		for _, ln := range strings.Split(f, "\n") {
			if bare.MatchString(ln) || pence.MatchString(ln) {
				t.Logf("%s suspect: %q", s.n, ln)
			}
		}
	}
}
