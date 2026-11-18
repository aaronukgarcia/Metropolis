package main

// FEAT-9995385616 lane A3 (round r1 question 3): a real boot must not log an
// error because the F5 screen asked the engine for a view nothing publishes
// yet. The prime is held behind whyViewServed until lane A2 registers f5.why.

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/screens/why"
)

// unknownWhyViewCount counts MET-E004 entries naming the f5.why view in the
// in-memory error ring (a coalesced entry counts its repeats).
func unknownWhyViewCount() int {
	n := 0
	for _, e := range errs.Recent() {
		if e.Code != "MET-E004" {
			continue
		}
		hit := strings.Contains(e.Msg, why.ViewSubscriptionName)
		for _, v := range e.Ctx {
			if s, ok := v.(string); ok && strings.Contains(s, why.ViewSubscriptionName) {
				hit = true
			}
		}
		if hit {
			n += 1 + e.Repeat
		}
	}
	return n
}

func TestRegression_FEAT9995385616_BootLogsNoUnknownWhyView(t *testing.T) {
	before := unknownWhyViewCount()
	r := rcNew(t)
	f := r.snap("F5 after boot", tcell.KeyF5)
	if !strings.Contains(f, "no month closed yet") {
		t.Fatalf("F5 lost its honest no-data state:\n%s", f)
	}
	if after := unknownWhyViewCount(); after != before {
		t.Fatalf("boot logged %d MET-E004 for view %q; the Subscribe must wait for the publisher lane (whyViewServed)", after-before, why.ViewSubscriptionName)
	}
}
