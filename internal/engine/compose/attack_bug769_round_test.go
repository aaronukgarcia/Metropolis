package compose

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/spiral"
)

// attack_bug769_round_test.go — independent destructive round
// (opus-round-bug769). Probes the BUG-769 insolvency publish leg for
// contradictions between the two signals it now carries on the SAME wire
// patch (Insolvent, read live from FinanceAPI on every publish, and
// InsolvencyVerdict, a month-boundary atomic mirror of spiral's read of
// the same FinanceAPI), and for the staleness of that mirror.

const bug769AttackSeed = uint64(76911001)

// decodeInsolvencyPatchFields runs the REAL publish path
// (buildFinanceBalanceSheetPatch) and returns the three insolvency fields
// as decoded from the wire JSON — never from the Go structs directly.
func decodeInsolvencyPatchFields(t *testing.T, st *simState) (months *int, insolvent *bool, verdict *string) {
	t.Helper()
	raw, err := st.buildFinanceBalanceSheetPatch()
	if err != nil {
		t.Fatalf("buildFinanceBalanceSheetPatch: %v", err)
	}
	var decoded struct {
		InsolvencyMonths  *int    `json:"insolvencyMonths"`
		Insolvent         *bool   `json:"insolvent"`
		InsolvencyVerdict *string `json:"insolvencyVerdict"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal finance patch: %v", err)
	}
	return decoded.InsolvencyMonths, decoded.Insolvent, decoded.InsolvencyVerdict
}

// TestAttackBUG769_RecoveryAfterGameOver probes the round brief's claim (4):
// "3 starved then funded -> verdict flips back to none the next month and
// the news feed shows one clear". finance's gameOver LATCHES (insolvency.go
// only ever sets it true), while insolvencyMonths resets on any met month —
// so the post-game-over recovered state is Months=0 + Insolvent=true, and
// the verdict CANNOT flip back to none. This test records the real
// behaviour so the shipped criteria are not believed.
func TestAttackBUG769_RecoveryAfterGameOver(t *testing.T) {
	e, comp, _ := wireBUG759(t, bug769AttackSeed)
	st := comp.state
	f := st.finance
	starveBUG759(t, f)

	advanceInChunks(t, e, 3*core.DailyTicksPerMonth)
	if !f.IsInsolvent() {
		t.Fatalf("fixture: expected insolvent after 3 starved months, months=%d", f.InsolvencyMonths())
	}
	months, insolvent, verdict := decodeInsolvencyPatchFields(t, st)
	if months == nil || insolvent == nil || verdict == nil {
		t.Fatalf("insolvency fields absent from the patch: months=%v insolvent=%v verdict=%v", months, insolvent, verdict)
	}
	t.Logf("after 3 starved months: months=%d insolvent=%v verdict=%q", *months, *insolvent, *verdict)
	if *verdict != "insolvency" {
		t.Fatalf("verdict after game over = %q, want insolvency", *verdict)
	}

	// Now fund the city and run several more months.
	fundBUG759(t, f)
	advanceInChunks(t, e, 3*core.DailyTicksPerMonth)

	months2, insolvent2, verdict2 := decodeInsolvencyPatchFields(t, st)
	t.Logf("after 3 FUNDED months: months=%d insolvent=%v verdict=%q", *months2, *insolvent2, *verdict2)

	// Record the real shape. The interesting (and defect-shaped) case is
	// Months==0 while Insolvent stays true: the TUI's RenderInsolvency
	// draws NOTHING for that combination, and the webconsole news feed
	// never emits its "resolved" entry.
	if *insolvent2 && *months2 == 0 {
		t.Logf("recovered-but-latched state reached: Months=0 while Insolvent=true and verdict=%q", *verdict2)
	}
	if !*insolvent2 {
		t.Logf("NOTE: insolvent cleared after funding — gameOver is NOT latched as read")
	}
}

// TestAttackBUG769_StaleVerdictAcrossResetForLoad probes attack (2): the
// atomic verdict mirror is written only at a month boundary, while
// insolvent/insolvencyMonths are read LIVE from finance on every publish.
// finance's own save participant reset (resetForLoad, exercised by every
// Composition.Load) zeroes insolvencyMonths/gameOver — nothing resets
// insolvencyVerdictPub. This test forces that exact divergence.
func TestAttackBUG769_StaleVerdictAfterFinanceStateCleared(t *testing.T) {
	e, comp, _ := wireBUG759(t, bug769AttackSeed+1)
	st := comp.state
	f := st.finance

	// Save the pristine, solvent city FIRST.
	root := t.TempDir()
	if err := comp.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}

	starveBUG759(t, f)
	advanceInChunks(t, e, 3*core.DailyTicksPerMonth)
	if !f.IsInsolvent() {
		t.Fatal("fixture: expected insolvent")
	}
	if _, _, v := decodeInsolvencyPatchFields(t, st); v == nil || *v != "insolvency" {
		t.Fatalf("fixture: verdict not insolvency, got %v", v)
	}

	// Real production rewind: Load the SOLVENT bundle taken before the
	// starve back into this SAME live composition (snapshot.go's
	// restoreFromSnapshotBytes / LoadAt walk-back funnels through exactly
	// this path). finance's participant resetForLoad zeroes
	// insolvencyMonths/gameOver; nothing resets insolvencyVerdictPub.
	if err := comp.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}

	months, insolvent, verdict := decodeInsolvencyPatchFields(t, st)
	t.Logf("after finance reset-for-load: months=%v insolvent=%v verdict=%q", *months, *insolvent, *verdict)
	if *insolvent {
		t.Fatalf("fixture: finance reset did not clear gameOver")
	}
	if *verdict == "insolvency" {
		t.Errorf("FINDING (stale mirror): the wire publishes insolvent=false, insolvencyMonths=%d AND insolvencyVerdict=%q simultaneously — the two signals on the SAME patch contradict each other after a load-shaped finance reset, and nothing resets insolvencyVerdictPub", *months, *verdict)
	}
	_ = e
	_ = spiral.DeathNone
}
