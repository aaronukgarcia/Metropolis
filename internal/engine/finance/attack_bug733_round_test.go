package finance

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// attack_bug733_round_test.go — INDEPENDENT destructive round against
// BUG-733's finance surface (attacker opus-round-bug733, NOT the author):
// int64 saturation on accrual, old-save (field-absent) decode, and an
// accrue -> save -> load -> repay exactness arc.

// TestAttackBUG733_AccrualSaturatesNeverWraps: an adversarial/absurd
// accrual must saturate at the int64 ceiling, never wrap to a NEGATIVE
// debt (which would read as the outside world owing the treasury money).
func TestAttackBUG733_AccrualSaturatesNeverWraps(t *testing.T) {
	f := NewFinanceAPI("attack-bug733-overflow")
	f.RecordCremationShortfall(1, Money(math.MaxInt64))
	if got := f.CremationShortfallOwed(); got != Money(math.MaxInt64) {
		t.Fatalf("first MaxInt64 accrual: owed=%d want %d", got, int64(math.MaxInt64))
	}
	f.RecordCremationShortfall(2, Money(math.MaxInt64))
	got := f.CremationShortfallOwed()
	if got < 0 {
		t.Fatalf("BUG-733: accrual WRAPPED to a negative debt (%d) — satAddMoney did not saturate", got)
	}
	if got != Money(math.MaxInt64) {
		t.Fatalf("BUG-733: accrual did not saturate at the int64 ceiling — owed=%d want %d", got, int64(math.MaxInt64))
	}
	// And a saturated debt must still be repayable down to exactly zero.
	f.RepayCremationShortfall(Money(math.MaxInt64))
	if got := f.CremationShortfallOwed(); got != 0 {
		t.Fatalf("BUG-733: a saturated debt did not repay to zero, owed=%d", got)
	}
}

// TestAttackBUG733_OldSaveWithoutTheFieldDecodesToZero: a save written
// BEFORE this change carries no cremationShortfall key at all. Decoding
// it must yield a zero debt (never a garbage/negative balance), and must
// not disturb the fields that DID exist.
func TestAttackBUG733_OldSaveWithoutTheFieldDecodesToZero(t *testing.T) {
	// A hand-built pre-BUG-733 meta payload: every key that existed
	// before, and deliberately NEITHER of the two new ones.
	old := map[string]any{
		"nextTxID": 7, "nextLoanID": 2, "nextFirmID": 3, "nextInvestID": 4,
		"moneyStock": 1234, "openingStock": 1000, "trackedDelta": 234,
		"month": 9, "totalCreditLine": 500, "totalDebt": 42,
		"missedPayments": 1, "insolvencyMonths": 2, "gameOver": false,
		"backlog": 77,
	}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Sanity: the fixture really does omit the new keys, so this test
	// cannot silently stop testing what it claims to (GR#15-style
	// non-vacuity guard).
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("probe unmarshal: %v", err)
	}
	if _, ok := probe["cremationShortfall"]; ok {
		t.Fatalf("fixture error: the 'old save' payload contains cremationShortfall — this test is vacuous")
	}

	var m financeMetaWire
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode old meta: %v", err)
	}
	if m.CremationShortfall != 0 || m.LastCremationShortfallMonth != 0 {
		t.Fatalf("BUG-733: an old save without the field decoded to (owed=%d, month=%d), want (0, 0)", m.CremationShortfall, m.LastCremationShortfallMonth)
	}
	if m.Backlog != 77 || m.Month != 9 || m.TotalDebt != 42 {
		t.Fatalf("BUG-733: decoding an old save disturbed pre-existing fields: %+v", m)
	}

	// And the same payload through the real load path, on an API that
	// already carries a NON-ZERO debt: resetForLoad must clear it, so a
	// loaded old city never inherits the previous session's debt.
	f := NewFinanceAPI("attack-bug733-oldsave")
	f.RecordCremationShortfall(3, gbp(500))
	if err := f.resetForLoad(); err != nil {
		t.Fatalf("resetForLoad: %v", err)
	}
	if got := f.CremationShortfallOwed(); got != 0 {
		t.Fatalf("BUG-733: resetForLoad left a stale debt of %d — a loaded city would inherit the previous city's cremation debt", got)
	}
	if err := f.applyLoadRecord(serialize.Record{Kind: recMeta, Data: raw}); err != nil {
		t.Fatalf("applyLoadRecord(old meta): %v", err)
	}
	if got := f.CremationShortfallOwed(); got != 0 {
		t.Fatalf("BUG-733: loading a pre-BUG-733 save produced owed=%d, want 0", got)
	}
}

// TestAttackBUG733_AccrueSaveLoadRepayIsExact drives the exact arc the
// round brief calls for: accrue a debt, round-trip it through the real
// save/load participant path, then repay it on the RESTORED instance and
// prove the balance lands on exactly zero — i.e. the restored debt is the
// same number, not merely a non-zero one.
func TestAttackBUG733_AccrueSaveLoadRepayIsExact(t *testing.T) {
	src := NewFinanceAPI("attack-bug733-roundtrip-src")
	src.RecordCremationShortfall(4, gbp(150))
	src.RecordCremationShortfall(5, gbp(300))
	src.RecordCremationShortfall(7, gbp(75))
	want := gbp(525)
	if got := src.CremationShortfallOwed(); got != want {
		t.Fatalf("fixture: src owed=%d want %d", got, want)
	}

	root := saveInto(t, src, "attack-bug733-roundtrip-src")
	dst := NewFinanceAPI("attack-bug733-roundtrip-dst")
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(dst)}, "attack-bug733-roundtrip-dst")
	if _, _, err := mgr.Load(manualBundleDir(t, root)); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := dst.CremationShortfallOwed(); got != want {
		t.Fatalf("BUG-733: restored debt = %d, want %d (exact)", got, want)
	}
	month, owed := dst.CremationShortfall()
	if month != 7 || owed != want {
		t.Fatalf("BUG-733: restored CremationShortfall() = (month=%d, owed=%d), want (7, %d)", month, owed, want)
	}

	// Repay one leg's worth on the restored instance, then the rest.
	dst.RepayCremationShortfall(gbp(300))
	if got := dst.CremationShortfallOwed(); got != gbp(225) {
		t.Fatalf("BUG-733: partial repay on the RESTORED instance = %d, want %d", got, gbp(225))
	}
	dst.RepayCremationShortfall(gbp(225))
	if got := dst.CremationShortfallOwed(); got != 0 {
		t.Fatalf("BUG-733: repaying the full restored balance left %d owed, want 0", got)
	}
}

// TestAttackBUG733_UnlimitedModeNeverAccruesCremationDebt: FEAT-143 AC-2
// says the insolvency/debt-rating triggers are INERT in Unlimited Money
// mode. The new cremation debt is driven entirely by SettleOpex's
// rejection, so the property that must hold is that SettleOpex CANNOT be
// rejected in Unlimited mode however broke the treasury is — otherwise a
// sandbox session would silently accumulate a cremation debt that Real
// mode's own rules say cannot exist there.
func TestAttackBUG733_UnlimitedModeNeverAccruesCremationDebt(t *testing.T) {
	for _, unlimited := range []bool{false, true} {
		f := NewFinanceAPI("attack-bug733-mode")
		if err := f.SetModeGate(attackModeGate{u: unlimited}); err != nil {
			t.Fatalf("SetModeGate(%v): %v", unlimited, err)
		}
		// AcctTreasury opens at zero with no credit line, so any positive
		// opex is unaffordable in Real mode.
		_, err := f.SettleOpex(gbp(500))
		if unlimited && err != nil {
			t.Fatalf("BUG-733 x FEAT-143 AC-2: SettleOpex was REJECTED in Unlimited Money mode (%v) — compose would accrue a cremation debt in a sandbox session, where the ruling says the money triggers are inert", err)
		}
		if !unlimited && err == nil {
			t.Fatalf("VACUITY GUARD: SettleOpex against an empty Real-mode treasury succeeded — this test cannot distinguish the two modes")
		}
	}
}
