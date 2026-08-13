package metricsdash

import "testing"

// gateStatusFixtureMixed is a hand-built cmdGateStatus-shaped stdout
// fixture with a mixed verdict set (some pass, one fail, one skipped)
// across all five named checks (AC-3) — proving the per-check detail
// survives, not just the rolled-up overall verdict.
const gateStatusFixtureMixed = `Sprint 3 — latest gate run 11111111-1111-4111-8111-111111111111:

  check 1 (data-files): PASS  [bill, 2026-08-12T10:00:00Z]
  check 2 (call-edges): PASS  [bill, 2026-08-12T10:00:00Z]
  check 3 (tripwires): FAIL  [bill, 2026-08-12T10:00:00Z]
    tripwire mismatch on feat.metricsdash
  check 4 (boundary-rulings): SKIPPED  [bill, 2026-08-12T10:00:00Z]
  check 5 (ready-queue): PASS  [bill, 2026-08-12T10:00:00Z]

Overall (derived, checks 1/2/3/5; check 4 is advisory): FAIL
`

func TestParseGateStatusText_MixedVerdictsAllFiveChecks(t *testing.T) {
	rep, err := ParseGateStatusText("3", gateStatusFixtureMixed)
	if err != nil {
		t.Fatalf("ParseGateStatusText: %v", err)
	}
	if len(rep.Checks) != 5 {
		t.Fatalf("expected all 5 named checks, got %d: %+v", len(rep.Checks), rep.Checks)
	}
	if rep.Overall != "FAIL" {
		t.Errorf("Overall = %q, want FAIL", rep.Overall)
	}

	want := map[int]struct {
		Name    string
		Verdict string
	}{
		1: {"data-files", "PASS"},
		2: {"call-edges", "PASS"},
		3: {"tripwires", "FAIL"},
		4: {"boundary-rulings", "SKIPPED"},
		5: {"ready-queue", "PASS"},
	}
	for _, c := range rep.Checks {
		w, ok := want[c.Number]
		if !ok {
			t.Fatalf("unexpected check number %d", c.Number)
		}
		if c.Name != w.Name || c.Verdict != w.Verdict {
			t.Errorf("check %d = %+v, want Name=%q Verdict=%q", c.Number, c, w.Name, w.Verdict)
		}
	}

	// False-pass guard: a test only checking the overall FAIL line would
	// also pass a build that dropped every per-check detail (AC-3's own
	// "what a lazy implementation looks like" warning) — the per-check
	// assertions above are what actually catch that.
	if rep.Checks[2].Verdict != "FAIL" {
		t.Fatal("the individual failing check (tripwires) must be visible, not just the overall verdict")
	}
}

func TestParseGateStatusText_ManualOverrideFlagged(t *testing.T) {
	text := `Sprint 3 — latest gate run 22222222-2222-4222-8222-222222222222:

  check 1 (data-files): PASS  [MANUAL-OVERRIDE — NOT mechanically verified]  [aaron, 2026-08-12T10:00:00Z]

Overall (derived, checks 1/2/3/5; check 4 is advisory): PASS
`
	rep, err := ParseGateStatusText("3", text)
	if err != nil {
		t.Fatalf("ParseGateStatusText: %v", err)
	}
	if len(rep.Checks) != 1 || !rep.Checks[0].ManualOverride {
		t.Fatalf("expected the manual-override tag to be detected, got %+v", rep.Checks)
	}
}

func TestParseGateStatusText_NoVerdictsRecorded(t *testing.T) {
	text := "Sprint 9: NO GATE VERDICTS RECORDED. Per AC-28, treat this identically to overall FAIL — dispatch must not proceed.\n"
	rep, err := ParseGateStatusText("9", text)
	if err != nil {
		t.Fatalf("ParseGateStatusText: %v", err)
	}
	if !rep.NoVerdictsRecorded {
		t.Errorf("expected NoVerdictsRecorded=true, got %+v", rep)
	}
}

func TestParseGateStatusText_UnrecognisedOutputIsAnError(t *testing.T) {
	if _, err := ParseGateStatusText("3", "node crashed\n"); err == nil {
		t.Fatal("expected an error for unrecognised gate-status output, got nil")
	}
}
