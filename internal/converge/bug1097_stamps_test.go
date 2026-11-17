package converge

import (
	"errors"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-1097 AC-8: converge Report build stamps. Two DISTINCT strings so a
// swapped placement is caught; empty stamp is a validation error.
func TestBug1097_AC8_StampsCarriedAndRendered(t *testing.T) {
	contract := Contract{"treasury": {Tier: TierExact}, "netWorth": {Tier: TierExact}}
	rep, err := CompareStamped("finance", "go-build-REF-1", "ts-build-CAND-2", sampleTrajectory(), sampleTrajectory(), contract)
	if err != nil {
		t.Fatalf("CompareStamped: %v", err)
	}
	if rep.ReferenceBuild != "go-build-REF-1" || rep.CandidateBuild != "ts-build-CAND-2" {
		t.Fatalf("stamps misplaced: ref=%q cand=%q", rep.ReferenceBuild, rep.CandidateBuild)
	}
	txt := rep.Text()
	if !strings.Contains(txt, "referenceBuild=go-build-REF-1\n") || !strings.Contains(txt, "candidateBuild=ts-build-CAND-2\n") {
		t.Fatalf("rendered text must print both stamps in place, got:\n%s", txt)
	}
	if !rep.Pass {
		t.Fatalf("identical trajectories must still pass: %v", rep.Diffs)
	}
}

func TestBug1097_AC8_EmptyStampIsValidationError(t *testing.T) {
	contract := Contract{"treasury": {Tier: TierExact}, "netWorth": {Tier: TierExact}}
	for _, c := range []struct{ ref, cand string }{{"", "c"}, {"r", ""}, {"", ""}} {
		rep, err := CompareStamped("finance", c.ref, c.cand, sampleTrajectory(), sampleTrajectory(), contract)
		if err == nil || rep != nil {
			t.Fatalf("stamps (%q,%q): want validation error and nil report, got rep=%v err=%v", c.ref, c.cand, rep, err)
		}
		if !errors.Is(err, &errs.E{Code: codeBuildStampMissing}) {
			t.Fatalf("stamps (%q,%q): want %s, got %v", c.ref, c.cand, codeBuildStampMissing, err)
		}
	}
}
