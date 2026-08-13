package metricsdash

import "testing"

// weaknessFixtureBoundary is a hand-built cmdWeakness-shaped stdout
// fixture with one class at exactly the recurrence threshold (3) and
// one at threshold-1 (2) — AC-2's boundary case, which is the one that
// actually distinguishes a correct threshold from an off-by-one.
const weaknessFixtureBoundary = `Security weakness patterns — 5 finding(s), 5 still open

  input-validation                 3 total    3 open  ###
  logging-gap                      2 total    2 open  ##

RECURRING (>=3) — these are training signals, not just defects:
  input-validation x3 — the devs keep writing this. Fixing each instance treats the symptom.
`

func TestParseWeaknessText_BoundaryRecurrence(t *testing.T) {
	rep, err := ParseWeaknessText(weaknessFixtureBoundary)
	if err != nil {
		t.Fatalf("ParseWeaknessText: %v", err)
	}
	if len(rep.Classes) != 2 {
		t.Fatalf("expected 2 classes, got %d: %+v", len(rep.Classes), rep.Classes)
	}

	byName := map[string]WeaknessClass{}
	for _, c := range rep.Classes {
		byName[c.Name] = c
	}

	at3, ok := byName["input-validation"]
	if !ok {
		t.Fatalf("missing input-validation class: %+v", rep.Classes)
	}
	if at3.Total != 3 || !at3.Recurring {
		t.Errorf("class at exactly 3: got Total=%d Recurring=%v, want Total=3 Recurring=true", at3.Total, at3.Recurring)
	}

	at2, ok := byName["logging-gap"]
	if !ok {
		t.Fatalf("missing logging-gap class: %+v", rep.Classes)
	}
	if at2.Total != 2 || at2.Recurring {
		t.Errorf("class at 2 (below threshold): got Total=%d Recurring=%v, want Total=2 Recurring=false", at2.Total, at2.Recurring)
	}

	// False-pass guard (AC-2's own warning): a test that only checked a
	// class with 6 findings flagged recurring would also pass a build
	// using threshold 1 or 4. The boundary assertions above are what
	// actually distinguish threshold==3 from those — this second check
	// just also proves totals/open counts round-trip.
	if rep.TotalFindings != 5 || rep.OpenFindings != 5 {
		t.Errorf("TotalFindings/OpenFindings = %d/%d, want 5/5", rep.TotalFindings, rep.OpenFindings)
	}
}

func TestParseWeaknessText_NoFindings(t *testing.T) {
	rep, err := ParseWeaknessText("No security findings recorded yet.\n")
	if err != nil {
		t.Fatalf("ParseWeaknessText: %v", err)
	}
	if len(rep.Classes) != 0 || rep.TotalFindings != 0 {
		t.Errorf("expected empty report for the known-empty sentinel, got %+v", rep)
	}
}

func TestParseWeaknessText_UnrecognisedOutputIsAnError(t *testing.T) {
	// Neither the empty sentinel nor a parseable histogram row —
	// proves a genuinely broken/changed source surfaces as a visible
	// failure rather than silently rendering an empty dashboard
	// (AC-1's "never a hardcoded/fabricated value" spirit applied to
	// the failure path too).
	if _, err := ParseWeaknessText("some unexpected node crash output\n"); err == nil {
		t.Fatal("expected an error for unrecognised weakness output, got nil")
	}
}

func TestParseWeaknessText_RowsMatchCmdWeaknessFormat(t *testing.T) {
	// Regression guard: proves the parser is reading cmdWeakness's REAL
	// column layout (padEnd + "total"/"open" literals), not a
	// convenient shape invented for the test fixture alone.
	text := "Security weakness patterns — 1 finding(s), 0 still open\n\n" +
		"  a-very-long-finding-class-name   1 total    0 open  #\n"
	rep, err := ParseWeaknessText(text)
	if err != nil {
		t.Fatalf("ParseWeaknessText: %v", err)
	}
	if len(rep.Classes) != 1 || rep.Classes[0].Name != "a-very-long-finding-class-name" {
		t.Fatalf("unexpected classes: %+v", rep.Classes)
	}
}
