package converge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// attack_phase3_inc2_correlation_test.go — FEAT-1972079936 Phase-3 inc2,
// F7 fix (independent round r1's "cosmetic, BUG-357 class" finding): a
// rejected bridge command used to double-wrap both the inner registry
// code and the correlation ID, e.g.
//
//	[MET-H505] finance A/B bridge could not apply action 0 op Zone: MET-G501: [MET-G501] ... (correlation: X) (correlation: X)
//
// Root cause: finance_ab_actions.go's issue() closure embedded the
// rejected command's ALREADY fully-rendered res.Error.Display (itself
// "[code] msg (correlation: id)", core/commands.go's toErrorRef) behind a
// redundant "res.Error.Code + \": \"" prefix, then wrapped THAT inside a
// fresh errs.New(codeActionOpFailed, cid, ...) call using the SAME
// correlation ID for the whole run — doubling both the code and the
// correlation suffix. This test drives a real rejection (an unknown
// build.ZoneType, MET-G501) through RunFinanceActionsComposed and proves
// the resulting error's message carries exactly one correlation-ID
// occurrence and never repeats the inner code back-to-back.
func TestAttack_Phase3Inc2_RejectedCommandMessage_NoDoubleWrap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bogus-zone-actions.json")
	// A bogus zoneType is rejected by build.SubmitZoneCommand
	// (ErrUnknownZoneType, MET-G501) before any ownership/funds check —
	// the simplest real rejection this bridge's "zone" op can trigger.
	const data = `{"actions":[{"op":"zone","cell":{"x":1,"y":1},"zoneType":"not_a_real_zone_type","tsSpec":"res_hut"}]}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, _, err := RunFinanceActionsComposed(path)
	if err == nil {
		t.Fatal("expected the bogus zoneType to be rejected by the composed engine, got nil error")
	}
	msg := err.Error()

	if n := strings.Count(msg, "(correlation:"); n != 1 {
		t.Fatalf("expected exactly ONE correlation-ID occurrence in the rejection message, found %d: %q", n, msg)
	}
	if strings.Contains(msg, "MET-G501: [MET-G501]") {
		t.Fatalf("inner code MET-G501 is double-wrapped (code prefix duplicating the bracketed code inside the embedded Display): %q", msg)
	}
	if !strings.Contains(msg, "MET-G501") {
		t.Fatalf("expected the inner rejection's code MET-G501 to still be visible in the message: %q", msg)
	}
	if !strings.HasPrefix(msg, "[MET-H505]") {
		t.Fatalf("expected the outer bridge error's own code MET-H505 to lead the message: %q", msg)
	}
	t.Logf("rejection message (single correlation, no double-wrap): %s", msg)
}
