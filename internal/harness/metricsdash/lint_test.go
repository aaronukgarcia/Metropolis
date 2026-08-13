package metricsdash

import "testing"

// lintFixtureTwoFindings is a hand-built cmdLint-shaped stdout fixture
// with 2 findings across two classes (AC-4).
const lintFixtureTwoFindings = `BOW lint — prose-vs-graph drift (report-only, always exits 0)

Class 1 — prose names a gate, no bow_dependencies row (1):
  FEAT-070 — prose says "gated against" about MOD-090, but no bow_dependencies row links them

Class 2 — cited code does not resolve (1):
  BUG-201 — cites "MOD-999" which does not exist in bow_items

2 finding(s) total. Report-only — this command never exits nonzero for findings.
`

func TestParseLintText_TwoFindings(t *testing.T) {
	rep, err := ParseLintText(lintFixtureTwoFindings)
	if err != nil {
		t.Fatalf("ParseLintText: %v", err)
	}
	if rep.Total != 2 || len(rep.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", rep.Total, rep.Findings)
	}

	f1 := rep.Findings[0]
	if f1.Class != 1 || f1.OwnerCode != "FEAT-070" || f1.TargetCode != "MOD-090" {
		t.Errorf("finding 1 = %+v, want Class=1 Owner=FEAT-070 Target=MOD-090", f1)
	}

	f2 := rep.Findings[1]
	if f2.Class != 2 || f2.OwnerCode != "BUG-201" || f2.TargetCode != "MOD-999" {
		t.Errorf("finding 2 = %+v, want Class=2 Owner=BUG-201 Target=MOD-999", f2)
	}
}

func TestParseLintText_NoDrift(t *testing.T) {
	text := "BOW lint — prose-vs-graph drift (report-only, always exits 0)\n\n" +
		"No drift found: every prose-cited gating relationship is wired, every cited code resolves, no done item cites a still-open gate.\n"
	rep, err := ParseLintText(text)
	if err != nil {
		t.Fatalf("ParseLintText: %v", err)
	}
	if rep.Total != 0 || len(rep.Findings) != 0 {
		t.Errorf("expected empty report for the known-clean sentinel, got %+v", rep)
	}
}

func TestParseLintText_UnrecognisedOutputIsAnError(t *testing.T) {
	if _, err := ParseLintText("node crashed with a stack trace\n"); err == nil {
		t.Fatal("expected an error for unrecognised lint output, got nil")
	}
}

func TestParseLintText_Class3FindingParsed(t *testing.T) {
	text := "BOW lint — prose-vs-graph drift (report-only, always exits 0)\n\n" +
		"Class 3 — done item cites a gate that is still open (1):\n" +
		"  FEAT-050 — is done but still cites still-open BUG-333 as a gate\n\n" +
		"1 finding(s) total. Report-only — this command never exits nonzero for findings.\n"
	rep, err := ParseLintText(text)
	if err != nil {
		t.Fatalf("ParseLintText: %v", err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Class != 3 {
		t.Fatalf("expected 1 class-3 finding, got %+v", rep.Findings)
	}
	if rep.Findings[0].OwnerCode != "FEAT-050" || rep.Findings[0].TargetCode != "BUG-333" {
		t.Errorf("finding = %+v, want Owner=FEAT-050 Target=BUG-333", rep.Findings[0])
	}
}
