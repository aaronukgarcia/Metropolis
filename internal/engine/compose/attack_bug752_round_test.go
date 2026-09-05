package compose

import (
	"bytes"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/firms"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// attack_bug752_round_test.go — independent destructive round against
// BUG-752 (opus-round-bug752). Every test here is written by the ATTACKER,
// not the author (GR#23 independence amendment).

// sumFirmCredit is the money-identity probe: the sum of every live firm's
// own CreditOutstanding, which the module's totalCreditOutstanding
// aggregate (SEC-100's cumulative lending bound) claims to mirror.
func sumFirmCredit(fs []firms.Firm) int64 {
	var total int64
	for _, f := range fs {
		total += f.Financial.CreditOutstanding
	}
	return total
}

// TestAttackBUG752_CreditLedgerIdentitySurvivesRoundTrip — ATTACK (1),
// MONEY IDENTITY. totalCreditOutstanding is serialized DIRECTLY on the
// meta record rather than re-derived from the restored firms, so a wire
// that lost (or mis-ordered) one firm's CreditOutstanding would restore an
// aggregate that no longer reconciles with its own parts — and SEC-100's
// cumulative ApproveCredit bound is enforced against that aggregate, so a
// silently-inflated one would deny legitimate borrowing forever while a
// deflated one would let the city borrow past the deposit-backed capacity.
func TestAttackBUG752_CreditLedgerIdentitySurvivesRoundTrip(t *testing.T) {
	e, comp := newTestEngine(t, roundTripSeed)
	capacity := constructionMaterialsCapacity(t, comp)
	// Three firms, so the identity is over a real multi-entry sum and the
	// ascending-FirmID emission order is genuinely exercised.
	registerFirmWithInputRequired(t, comp, capacity*2)
	registerFirmWithInputRequired(t, comp, capacity/2)
	registerFirmWithInputRequired(t, comp, capacity*3)
	if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	// Draw real credit against each firm so CreditOutstanding is non-zero
	// on more than one firm (a single-firm fixture cannot distinguish "the
	// aggregate was restored" from "the aggregate happens to equal the one
	// firm's value").
	all := comp.state.firms.Firms()
	if len(all) < 3 {
		t.Fatalf("fixture is vacuous: expected >=3 registered firms, got %d", len(all))
	}
	for i, f := range all {
		principal := int64(1_000_000 * (i + 1))
		if _, err := comp.state.firms.ApproveCredit(firms.CreditRequest{
			FirmID: f.ID, Principal: principal, Month: 1,
		}); err != nil {
			t.Fatalf("ApproveCredit(firm %d): %v", f.ID, err)
		}
	}

	preSum := sumFirmCredit(comp.state.firms.Firms())
	preTotal := comp.state.firms.TotalCreditOutstanding()
	if preSum == 0 {
		t.Fatal("fixture is vacuous: no credit outstanding before the save")
	}
	if preSum != preTotal {
		t.Fatalf("PRE-SAVE identity already broken (inherited): sum=%d total=%d", preSum, preTotal)
	}

	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clock, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	_, compLoaded := newTestEngine(t, roundTripSeed)
	if err := compLoaded.LoadAt(dir, clock.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}

	postSum := sumFirmCredit(compLoaded.state.firms.Firms())
	postTotal := compLoaded.state.firms.TotalCreditOutstanding()
	if postSum != preSum {
		t.Fatalf("per-firm CreditOutstanding did not survive the boundary: pre=%d post=%d", preSum, postSum)
	}
	if postTotal != preTotal {
		t.Fatalf("totalCreditOutstanding did not survive the boundary: pre=%d post=%d", preTotal, postTotal)
	}
	if postSum != postTotal {
		t.Fatalf("MONEY IDENTITY BROKEN across the save/load boundary: sum(per-firm CreditOutstanding)=%d but totalCreditOutstanding=%d", postSum, postTotal)
	}
}

// TestAttackBUG752_BuildersMerchantSurvivesRoundTrip — ATTACK (2), the
// split ownership: compose's ledger holds the merchant's FirmID while
// engine.firms holds the firm itself. If either half fails to restore, the
// idempotency guard in maybeAutoPlaceBuildersMerchant either dangles (id
// with no firm) or re-fires (firm with no id), registering a SECOND
// merchant. This drives the REAL zone-command trigger, not the field.
func TestAttackBUG752_BuildersMerchantSurvivesRoundTrip(t *testing.T) {
	buyCell := protocol.CellRef{X: 3, Y: 3}
	e, comp := wireInc2TestEngine(t, 501, buyCell)
	zoneCell(t, e, "atk752-farming", protocol.CellRef{X: 4, Y: 4}, "farming")
	zoneCell(t, e, "atk752-manufacturing", protocol.CellRef{X: 5, Y: 5}, "manufacturing")

	merchantID := comp.state.buildersMerchantFirmID
	if merchantID == 0 {
		t.Fatal("fixture is vacuous: merchant never auto-placed")
	}
	preCount := len(comp.state.firms.Firms())

	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clock, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}

	eLoaded, compLoaded := wireInc2TestEngine(t, 501, buyCell)
	if err := compLoaded.LoadAt(dir, clock.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}

	if got := compLoaded.state.buildersMerchantFirmID; got != merchantID {
		t.Fatalf("BuildersMerchantFirmID did not survive Load: want %d got %d", merchantID, got)
	}
	// The id must resolve to a REAL restored firm — a dangling id is the
	// nil-lookup hazard this attack was aimed at.
	firm, err := compLoaded.state.firms.Firm(merchantID)
	if err != nil {
		t.Fatalf("restored BuildersMerchantFirmID %d does not resolve to a registered firm: %v", merchantID, err)
	}
	if firm.Name != buildersMerchantName {
		t.Fatalf("restored merchant firm Name = %q, want %q", firm.Name, buildersMerchantName)
	}
	if got := len(compLoaded.state.firms.Firms()); got != preCount {
		t.Fatalf("firm count changed across the boundary: pre=%d post=%d", preCount, got)
	}

	// The real trigger must stay idempotent on the RESTORED city: a further
	// qualifying zone command must not register a second merchant.
	zoneCell(t, eLoaded, "atk752-farming-2", protocol.CellRef{X: 6, Y: 6}, "farming")
	if got := compLoaded.state.buildersMerchantFirmID; got != merchantID {
		t.Fatalf("a SECOND merchant was auto-placed after the load: id went %d -> %d", merchantID, got)
	}
	if got := len(compLoaded.state.firms.Firms()); got != preCount {
		t.Fatalf("a second merchant firm was registered after the load: firm count %d -> %d", preCount, got)
	}
	if !compLoaded.state.hasBuildersMerchant() {
		t.Fatal("hasBuildersMerchant() false on the restored city — construction sourcing would flip to imported")
	}
}

// TestAttackBUG752_DanglingMerchantID_OldSaveShape — ATTACK (2), the
// asymmetric-migration half: what happens if the ledger carries a merchant
// id whose firm is NOT in the registry (the shape an old bundle would
// produce if BuildersMerchantFirmID had been written before the firms
// shard existed). Proves the failure MODE is a clean, non-panicking
// no-op-with-dangling-id rather than a nil deref, and pins that the
// idempotency guard suppresses re-placement (so the dangling id is a
// permanent silent hole, not a crash) — the disclosure this round records.
func TestAttackBUG752_DanglingMerchantID_OldSaveShape(t *testing.T) {
	buyCell := protocol.CellRef{X: 3, Y: 3}
	e, comp := wireInc2TestEngine(t, 502, buyCell)
	zoneCell(t, e, "atk752d-farming", protocol.CellRef{X: 4, Y: 4}, "farming")
	zoneCell(t, e, "atk752d-manufacturing", protocol.CellRef{X: 5, Y: 5}, "manufacturing")
	merchantID := comp.state.buildersMerchantFirmID
	if merchantID == 0 {
		t.Fatal("fixture is vacuous")
	}

	// Forge the hazardous shape directly: remove the firm, keep the id.
	if err := comp.state.firms.RemoveFirm(merchantID); err != nil {
		t.Fatalf("RemoveFirm: %v", err)
	}
	if _, err := comp.state.firms.Firm(merchantID); err == nil {
		t.Fatal("fixture setup failed: merchant firm still resolves")
	}

	// The next qualifying zone command must not panic.
	zoneCell(t, e, "atk752d-farming-2", protocol.CellRef{X: 6, Y: 6}, "farming")
	if comp.state.buildersMerchantFirmID != merchantID {
		t.Fatalf("guard re-placed the merchant: %d -> %d", merchantID, comp.state.buildersMerchantFirmID)
	}
	// hasBuildersMerchant still reports true off the dangling id — the
	// documented consequence (construction sources "locally" from a firm
	// that no longer exists). Non-crashing, but silently wrong.
	if !comp.state.hasBuildersMerchant() {
		t.Fatal("hasBuildersMerchant() false — behaviour changed from the recorded finding")
	}
	// And the city keeps ticking without a panic.
	if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks with a dangling merchant id: %v", err)
	}
}

// TestAttackBUG752_FirmIDMintingAfterLoad_NoSilentOverwrite — ATTACK (3).
// FirmIDs are a PURE function of (seed, founder, month, purpose), so a new
// registration after a load, in the same month, with the same purpose
// string, redraws the SAME first candidate id as a firm already in the
// restored registry. firmIDForLocked must walk to the next attempt rather
// than overwrite the restored firm (which would silently delete a firm and
// its CreditOutstanding while leaving totalCreditOutstanding intact).
func TestAttackBUG752_FirmIDMintingAfterLoad_NoSilentOverwrite(t *testing.T) {
	e, comp := newTestEngine(t, roundTripSeed)
	// RegisterFirm mints from (seed, founder=0, month, "stagefirm:"+name),
	// so registering the SAME name twice in the SAME month is the exact
	// collision shape.
	const dupName = "atk752-collider"
	if _, err := comp.state.firms.RegisterFirm(dupName, 1, "industrial"); err != nil {
		t.Fatalf("RegisterFirm: %v", err)
	}
	preIDs := comp.state.firms.Firms()
	if len(preIDs) != 1 {
		t.Fatalf("expected 1 firm, got %d", len(preIDs))
	}
	firstID := preIDs[0].ID
	// Give it a distinguishing balance, so an overwrite is detectable.
	if _, err := comp.state.firms.ApproveCredit(firms.CreditRequest{FirmID: firstID, Principal: 7_777_777, Month: 0}); err != nil {
		t.Fatalf("ApproveCredit: %v", err)
	}
	if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clock, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	_, compLoaded := newTestEngine(t, roundTripSeed)
	if err := compLoaded.LoadAt(dir, clock.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}

	// The colliding registration, on the restored registry, same month.
	if _, err := compLoaded.state.firms.RegisterFirm(dupName, 1, "industrial"); err != nil {
		t.Fatalf("RegisterFirm (post-load collision): %v", err)
	}
	after := compLoaded.state.firms.Firms()
	if len(after) != 2 {
		t.Fatalf("COLLISION: expected 2 firms after the duplicate registration, got %d (the restored firm was silently overwritten)", len(after))
	}
	restored, err := compLoaded.state.firms.Firm(firstID)
	if err != nil {
		t.Fatalf("the restored firm %d is gone after the colliding registration: %v", firstID, err)
	}
	if restored.Financial.CreditOutstanding != 7_777_777 {
		t.Fatalf("the restored firm's balance was clobbered: got %d want 7777777", restored.Financial.CreditOutstanding)
	}
	// And the money identity must still hold after the collision walk.
	if s, tot := sumFirmCredit(after), compLoaded.state.firms.TotalCreditOutstanding(); s != tot {
		t.Fatalf("money identity broken after a post-load id collision: sum=%d total=%d", s, tot)
	}
}

// TestAttackBUG752_TwoLoadsOfTheSameBundleAreIdentical — ATTACK (5),
// determinism. Two independent loads of the SAME bundle must produce
// byte-identical StateDigests and identical firms-side observables; a
// map-range emission or a nondeterministic decode order would show here.
func TestAttackBUG752_TwoLoadsOfTheSameBundleAreIdentical(t *testing.T) {
	e, comp := newTestEngine(t, roundTripSeed)
	capacity := constructionMaterialsCapacity(t, comp)
	for i := 0; i < 5; i++ {
		registerFirmWithInputRequired(t, comp, capacity*int64(i+1))
	}
	for m := 0; m < 3; m++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
			t.Fatalf("AdvanceTicks: %v", err)
		}
	}
	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clock, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}

	type obs struct {
		digest [32]byte
		scale  int64
		count  int
		credit int64
		total  int64
		events int
	}
	snap := func() obs {
		_, c := newTestEngine(t, roundTripSeed)
		if err := c.LoadAt(dir, clock.Tick()); err != nil {
			t.Fatalf("LoadAt: %v", err)
		}
		scale, err := c.state.firms.AggregateOutputScale()
		if err != nil {
			t.Fatalf("AggregateOutputScale: %v", err)
		}
		fs := c.state.firms.Firms()
		return obs{
			digest: c.StateDigest(),
			scale:  scale,
			count:  len(fs),
			credit: sumFirmCredit(fs),
			total:  c.state.firms.TotalCreditOutstanding(),
			events: len(c.state.firms.Events()),
		}
	}
	a, b := snap(), snap()
	if a != b {
		t.Fatalf("two loads of the same bundle differ:\nA=%+v\nB=%+v", a, b)
	}
	if a.count != 5 {
		t.Fatalf("fixture is vacuous: expected 5 restored firms, got %d", a.count)
	}
	// StateDigest of the loaded arm must equal the never-saved source's.
	if got := comp.StateDigest(); got != a.digest {
		t.Fatalf("loaded StateDigest != source StateDigest (%x vs %x)", a.digest, got)
	}
}

// TestAttackBUG752_LifecycleAndCultureWindowSurvive — ATTACK (4).
// foundedEvents feeds CultureIndex and the lifecycle event log feeds
// Events(); both are append-only slices restored by APPEND, so a Handler
// that failed to reset (or that ran twice) would double them. Proves the
// culture window and event log come back exactly once, at the exact values.
func TestAttackBUG752_LifecycleAndCultureWindowSurvive(t *testing.T) {
	e, comp := newTestEngine(t, roundTripSeed)
	capacity := constructionMaterialsCapacity(t, comp)
	for i := 0; i < 4; i++ {
		registerFirmWithInputRequired(t, comp, capacity*int64(i+1))
	}
	if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	preCulture := comp.state.firms.CultureIndex()
	preEvents := len(comp.state.firms.Events())
	if preEvents == 0 {
		t.Fatal("fixture is vacuous: no lifecycle events recorded")
	}

	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clock, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	_, compLoaded := newTestEngine(t, roundTripSeed)
	if err := compLoaded.LoadAt(dir, clock.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if got := compLoaded.state.firms.CultureIndex(); got != preCulture {
		t.Fatalf("CultureIndex did not survive the boundary: pre=%d post=%d", preCulture, got)
	}
	if got := len(compLoaded.state.firms.Events()); got != preEvents {
		t.Fatalf("lifecycle event log did not survive exactly once: pre=%d post=%d", preEvents, got)
	}
	// A SECOND load into the SAME composition must not double the
	// append-only logs (Handler's reset-on-first-record contract).
	if err := compLoaded.LoadAt(dir, clock.Tick()); err != nil {
		t.Fatalf("second LoadAt: %v", err)
	}
	if got := len(compLoaded.state.firms.Events()); got != preEvents {
		t.Fatalf("a second Load DOUBLED the lifecycle event log: %d -> %d", preEvents, got)
	}
	if got := compLoaded.state.firms.CultureIndex(); got != preCulture {
		t.Fatalf("a second Load changed CultureIndex: %d -> %d", preCulture, got)
	}
}

// TestAttackBUG752_FirmsShardEmissionIsOrderStable — ATTACK (5)/GR#21.
// snapshotForSave flattens two MAPS (firms, founderHistory) to slices; a
// missing sort would emit them in Go's randomised map-range order. No test
// in the estate caught that: the byte-identical round-trip test's fixture
// registers no firms, so its firms shard is empty and trivially stable.
// This pins the real thing — repeated emissions from ONE composition with
// several firms must be byte-identical.
//
// PROVEN TO FAIL: deleting both sort.Slice calls in
// firms/participant.go's snapshotForSave reds this test (and nothing else
// in the estate did).
func TestAttackBUG752_FirmsShardEmissionIsOrderStable(t *testing.T) {
	e, comp := newTestEngine(t, roundTripSeed)
	capacity := constructionMaterialsCapacity(t, comp)
	for i := 0; i < 12; i++ {
		registerFirmWithInputRequired(t, comp, capacity*int64(i+1))
	}
	if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	first := participantStreams(t, comp)["firms"]
	if len(first) == 0 {
		t.Fatal("fixture is vacuous: the firms shard emitted nothing")
	}
	for i := 0; i < 30; i++ {
		again := participantStreams(t, comp)["firms"]
		if !bytes.Equal(first, again) {
			t.Fatalf("firms shard emission is NOT order-stable on repeat %d (GR#21 map-range nondeterminism)", i)
		}
	}
}
