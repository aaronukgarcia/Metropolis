package finance

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// driveManyKeys builds a finance state with MANY keys in every map-backed
// collection so raw map-iteration order (if any emission were unsorted)
// would differ between two saves -- the byte-determinism attack the
// single-key driveFinance cannot express.
func driveManyKeys(t *testing.T, f *FinanceAPI) {
	t.Helper()
	ck(t, f.SetMilestoneGate(allowAllGate{}))
	ck(t, f.BeginMonth(1))
	seedTreasury(t, f, gbp(1000000))
	// Many accounts + credit lines.
	for i := 0; i < 25; i++ {
		id := AccountID("acct." + string(rune('a'+i%26)) + string(rune('0'+i/26)))
		ck(t, f.OpenAccount(id, RoleMoney))
		ck(t, f.SetCreditLine(id, gbp(int64(100+i))))
	}
	// Many loans.
	for i := 0; i < 20; i++ {
		_, err := f.Borrow(LoanRequest{Tier: 0, Principal: gbp(int64(50 + i)), TermMonths: 12})
		ck(t, err)
	}
	// Many firms.
	for i := 0; i < 20; i++ {
		firm, err := NewSimpleFirm("firm"+string(rune('a'+i)), gbp(int64(100+i)), gbp(20), gbp(10), gbp(5))
		ck(t, err)
		_, err = f.RegisterFirm(firm)
		ck(t, err)
	}
	// Many investments.
	for i := 0; i < 20; i++ {
		_, err := f.StartInvestment("inv"+string(rune('a'+i)), gbp(int64(40+i)), gbp(5), 24)
		ck(t, err)
	}
}

func saveIntoScratch(t *testing.T, f *FinanceAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(f)}, cid)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-build"}
	ck(t, mgr.SaveManual(ctx, "det"))
	return root
}

// TestAttack_ManyKeyByteDeterminism forces MANY keys and asserts two saves
// of the same state are byte-identical -- proves sorted emission, not just
// single-key trivial determinism.
func TestAttack_ManyKeyByteDeterminism(t *testing.T) {
	f1 := NewFinanceAPI("run1")
	driveManyKeys(t, f1)
	root1 := saveIntoScratch(t, f1, "run1")

	f2 := NewFinanceAPI("run2")
	driveManyKeys(t, f2)
	root2 := saveIntoScratch(t, f2, "run2")

	dir1 := manualBundleDir(t, root1)
	dir2 := manualBundleDir(t, root2)
	files1 := allFiles(t, dir1)
	files2 := allFiles(t, dir2)
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("file sets differ")
	}
	for _, rel := range files1 {
		b1, _ := os.ReadFile(filepath.Join(dir1, rel))
		b2, _ := os.ReadFile(filepath.Join(dir2, rel))
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte across two saves (raw-map emission?)", rel)
		}
	}
}

// TestAttack_CounterRoundTrip asserts the four id counters restore exactly
// -- the gap driveFinance's compareFinance never checks directly.
func TestAttack_CounterRoundTrip(t *testing.T) {
	orig := NewFinanceAPI("orig")
	driveManyKeys(t, orig)
	root := saveIntoScratch(t, orig, "orig")

	reloaded := NewFinanceAPI("reloaded")
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(reloaded)}, "reloaded")
	_, _, err := mgr.Load(manualBundleDir(t, root))
	ck(t, err)

	if orig.nextTxID != reloaded.nextTxID {
		t.Fatalf("nextTxID %d != %d", orig.nextTxID, reloaded.nextTxID)
	}
	if orig.nextLoanID != reloaded.nextLoanID {
		t.Fatalf("nextLoanID %d != %d", orig.nextLoanID, reloaded.nextLoanID)
	}
	if orig.nextFirmID != reloaded.nextFirmID {
		t.Fatalf("nextFirmID %d != %d", orig.nextFirmID, reloaded.nextFirmID)
	}
	if orig.nextInvestID != reloaded.nextInvestID {
		t.Fatalf("nextInvestID %d != %d", orig.nextInvestID, reloaded.nextInvestID)
	}
}

// TestAttack_LoadIntoNonEmptyFullyReplaces: a Load into a FinanceAPI that
// already holds DIFFERENT state must fully overwrite it (Handler resets),
// never merge. The target here is pre-driven with its own separate ledger.
func TestAttack_LoadIntoNonEmptyFullyReplaces(t *testing.T) {
	orig := NewFinanceAPI("orig")
	driveManyKeys(t, orig)
	root := saveIntoScratch(t, orig, "orig")

	// Pre-populate the target with a DIFFERENT, larger ledger.
	target := NewFinanceAPI("target")
	ck(t, target.SetMilestoneGate(allowAllGate{}))
	ck(t, target.BeginMonth(99))
	seedTreasury(t, target, gbp(777777))
	ck(t, target.OpenAccount(AccountID("ghost.acct"), RoleMoney))
	for i := 0; i < 40; i++ {
		_, err := target.Borrow(LoanRequest{Tier: 0, Principal: gbp(int64(500 + i)), TermMonths: 6})
		ck(t, err)
	}

	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(target)}, "target")
	_, _, err := mgr.Load(manualBundleDir(t, root))
	ck(t, err)

	// The ghost account must be GONE (full replace, not merge).
	if _, ok := target.AccountBalance(AccountID("ghost.acct")); ok {
		t.Fatalf("ghost.acct survived load -- Handler merged instead of replacing")
	}
	// Loan count must equal the SAVED count, not saved+target.
	if len(target.loans) != len(orig.loans) {
		t.Fatalf("loan count %d != saved %d -- merge, not replace", len(target.loans), len(orig.loans))
	}
	compareFinance(t, orig, target, "load-into-nonempty")
	// role map must equal accounts map exactly (rebuild lossless, no ghost).
	if len(target.role) != len(target.accounts) {
		t.Fatalf("role map size %d != accounts %d after load", len(target.role), len(target.accounts))
	}
	for id, a := range target.accounts {
		if target.role[id] != a.Role {
			t.Fatalf("role[%s]=%d != account.Role %d", id, target.role[id], a.Role)
		}
	}
}

// TestAttack_RoleMapRebuild: every restored account's role is rebuilt
// correctly (no zero/wrong role), including non-RoleMoney accounts.
func TestAttack_RoleMapRebuild(t *testing.T) {
	orig := NewFinanceAPI("orig")
	driveManyKeys(t, orig)
	root := saveIntoScratch(t, orig, "orig")

	reloaded := NewFinanceAPI("reloaded")
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(reloaded)}, "reloaded")
	_, _, err := mgr.Load(manualBundleDir(t, root))
	ck(t, err)

	if len(orig.role) != len(reloaded.role) {
		t.Fatalf("role map size %d != %d", len(orig.role), len(reloaded.role))
	}
	for id, r := range orig.role {
		if reloaded.role[id] != r {
			t.Fatalf("role[%s] %d != %d", id, reloaded.role[id], r)
		}
	}
	// The liability + external well-known accounts must retain their roles.
	if reloaded.role[AcctDebt] != RoleLiability {
		t.Fatalf("AcctDebt role %d != RoleLiability", reloaded.role[AcctDebt])
	}
	if reloaded.role[AcctExternal] != RoleExternal {
		t.Fatalf("AcctExternal role %d != RoleExternal", reloaded.role[AcctExternal])
	}
}

// TestAttack_CopyguardFiresOnParticipant: a struct-copied FinanceAPI's
// participant must fail closed on Kind/Source/Handler.
func TestAttack_CopyguardFiresOnParticipant(t *testing.T) {
	orig := NewFinanceAPI("orig")
	driveManyKeys(t, orig)
	// Reproduce the exact guard-visible state of a struct-copied FinanceAPI
	// (self still points at the ORIGINAL, not at this value) without a
	// vet-copylocks-tripping `*orig` copy of the embedded RWMutex.
	var copied FinanceAPI
	copied.self.Store(orig)
	sp := NewSaveParticipant(&copied)

	if sp.Kind() != "" {
		t.Fatalf("copied participant Kind() = %q, want empty (guard should fire)", sp.Kind())
	}
	src := sp.Source()
	if _, _, err := src(); err == nil {
		t.Fatalf("copied participant Source() first pull returned nil error -- guard did not fire")
	}
	h := sp.Handler()
	if err := h(serialize.Record{}); err == nil {
		t.Fatalf("copied participant Handler() returned nil error -- guard did not fire")
	}
	// And the ORIGINAL still works.
	if NewSaveParticipant(orig).Kind() != KindFinance {
		t.Fatalf("original participant Kind() broken")
	}
}
