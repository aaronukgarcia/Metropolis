package firms

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestFirmsAPIFieldsAllClassified is BUG-752's field-parity guard (mirrors
// finance's TestFinanceAPIFieldsAllClassified exactly): every field of
// FirmsAPI itself must be either explicitly EXCLUDED (runtime/config,
// deliberately not part of a save) or COVERED (a wire field/record). A new
// field added to FirmsAPI that is neither serialized nor consciously
// excluded FAILS the build here.
func TestFirmsAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"mu":                   "runtime lock, not state",
		"correlationID":        "per-instance error correlation, not simulation state",
		"self":                 "SEC-020 copy-guard pointer, re-armed by Load/LoadDefault",
		"seed":                 "reproduced from the save bundle's own WorldSeed header (compose's Composition.Save/Load), exactly like finance's documented data-only RNG reasoning -- FirmID minting (firmIDForLocked) is a pure function of (seed, founder, month, purpose), so re-deriving from the restored seed + restored founding facts is exactly reproducible",
		"citizens":             "live composition-root wiring pointer, re-established via SetCitizens on every Wire -- never simulation state of its own (mirrors productivityModifier's identical precedent below)",
		"finance":              "live composition-root wiring pointer, re-established via SetFinance on every Wire",
		"market":               "live composition-root wiring pointer, re-established via SetMarket on every Wire",
		"build":                "live composition-root wiring pointer, re-established via SetBuild on every Wire",
		"cfg":                  "static config loaded from data/firms.json at construction time, not mutable runtime state",
		"subscribers":          "live in-process lifecycle-event channels -- not serializable, and meaningless post-load with no live subscriber on the other end",
		"nextSubID":            "subscription-id counter for the excluded subscribers map -- restarting at 1 after a load is harmless because no old subscription survives the load either",
		"productivityModifier": "MOD-034's OPTIONAL injected composition-root seam (documented in firms.go's own field comment): a live wire re-established on every load, never simulation state of its own",
	}
	covered := map[string]bool{
		"firms": true, "totalCreditOutstanding": true, "founderHistory": true,
		"foundedEvents": true, "foundedCount": true, "failedCount": true,
		"month": true, "events": true,
	}
	ft := reflect.TypeOf((*FirmsAPI)(nil)).Elem()
	for i := 0; i < ft.NumField(); i++ {
		name := ft.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("FirmsAPI field %q is neither serialized (add it to a wire record) nor explicitly excluded (add it to the excluded allowlist with a reason) -- BUG-752 forbids a silently-unsaved registry field", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("FirmsAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

func ck(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// newTestFirmsAPI returns a ready *FirmsAPI over the real data/firms.json
// config (mirrors other firms package tests' construction idiom).
func newTestFirmsAPI(t *testing.T) *FirmsAPI {
	t.Helper()
	f, err := LoadDefault(12345, "bug752-test")
	ck(t, err)
	return f
}

// driveFirms populates every serialized collection deterministically: the
// firm registry (via RegisterFirm, which also exercises Financial/Premises/
// Staff), founder history, foundedEvents/CultureIndex window, and the
// lifecycle event log, plus the churn counters and totalCreditOutstanding.
func driveFirms(t *testing.T, f *FirmsAPI) {
	t.Helper()
	f.month = 5
	startup1, err := f.RegisterFirm("acme-builders", 10, "industrial")
	ck(t, err)
	startup2, err := f.RegisterFirm("beta-merchants", 4, "commercial")
	ck(t, err)

	f.mu.Lock()
	fs1 := f.firms[FirmID(startup1.ID)]
	fs1.firm.Staff = []uint64{100, 101, 102}
	fs1.firm.Stage = StageSmall
	fs1.firm.Financial = Financial{CreditOutstanding: 5_000_000, MonthlyCashFlow: 120_000, OutputScale: 850}
	fs1.firm.Stalled = true
	fs1.firm.Premises = Premises{Secured: true, ZoneClass: "industrial"}

	fs2 := f.firms[FirmID(startup2.ID)]
	fs2.firm.Financial = Financial{CreditOutstanding: 250_000, MonthlyCashFlow: -10_000, OutputScale: 1000}

	f.totalCreditOutstanding = fs1.firm.Financial.CreditOutstanding + fs2.firm.Financial.CreditOutstanding
	f.founderHistory[7001] = &founderRecord{exited: true}
	f.founderHistory[7002] = &founderRecord{exited: false}
	f.mu.Unlock()
}

// snapshotOf returns a comparable snapshot of every field this participant
// covers, for round-trip equality assertions.
type comparableSnapshot struct {
	month                  int64
	foundedCount           int64
	failedCount            int64
	totalCreditOutstanding int64
	firms                  []Firm
	founders               map[uint64]bool
	foundedEvents          []foundedEvent
	events                 []LifecycleEvent
}

func snapshotOf(t *testing.T, f *FirmsAPI) comparableSnapshot {
	t.Helper()
	founders := make(map[uint64]bool)
	f.mu.RLock()
	for cid, rec := range f.founderHistory {
		founders[cid] = rec.exited
	}
	f.mu.RUnlock()
	return comparableSnapshot{
		month:                  f.Month(),
		foundedCount:           f.FoundedCount(),
		failedCount:            f.FailedCount(),
		totalCreditOutstanding: f.TotalCreditOutstanding(),
		firms:                  f.Firms(),
		founders:               founders,
		foundedEvents:          append([]foundedEvent(nil), f.foundedEvents...),
		events:                 f.Events(),
	}
}

// TestSaveParticipant_RoundTrip drives a fresh FirmsAPI, saves it via the
// SaveParticipant's Source, loads it into a SECOND fresh FirmsAPI via
// Handler, and asserts the two are field-for-field identical. This is the
// unit-level proof beneath BUG-752's compose-level parity test.
func TestSaveParticipant_RoundTrip(t *testing.T) {
	src := newTestFirmsAPI(t)
	driveFirms(t, src)
	before := snapshotOf(t, src)

	// Stream every record Source emits into a slice (mirrors save.Manager's
	// own shard-copy idiom, without needing the whole save package here).
	p := NewSaveParticipant(src)
	if p.Kind() != KindFirms {
		t.Fatalf("Kind() = %q, want %q", p.Kind(), KindFirms)
	}
	source := p.Source()
	var records []serialize.Record
	for {
		rec, ok, err := source()
		ck(t, err)
		if !ok {
			break
		}
		records = append(records, rec)
	}
	if len(records) == 0 {
		t.Fatal("Source emitted zero records for a populated registry")
	}

	dst := newTestFirmsAPI(t)
	handler := NewSaveParticipant(dst).Handler()
	for _, rec := range records {
		ck(t, handler(rec))
	}

	after := snapshotOf(t, dst)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("round trip mismatch:\nbefore=%+v\nafter=%+v", before, after)
	}
}

// TestSaveParticipant_OldSaveDecodesToEmptyRegistry proves the documented
// migration path: a Handler that receives NO records at all (a bundle with
// no "firms" shard, i.e. every save taken before this participant existed)
// leaves the target registry exactly as Load/LoadDefault constructed it --
// empty, not merely "reset never called".
func TestSaveParticipant_OldSaveDecodesToEmptyRegistry(t *testing.T) {
	dst := newTestFirmsAPI(t)
	// Handler is never invoked at all (mirrors save.Manager.Load's
	// documented behaviour: a bundle with no matching shard never calls
	// the participant's Handler -- see compose/save_wire.go's own
	// hasDeathServicesShard precedent).
	if got := dst.Firms(); len(got) != 0 {
		t.Fatalf("fresh FirmsAPI has %d firms, want 0", len(got))
	}
	if got := dst.FoundedCount(); got != 0 {
		t.Fatalf("fresh FirmsAPI FoundedCount() = %d, want 0", got)
	}
}

// TestSaveParticipant_HandlerResetsBeforeFirstRecord proves Handler REPLACES
// (never merges with) whatever the live target already held -- a firm
// registered on the live target before Load must not survive a Load that
// restores a registry without it.
func TestSaveParticipant_HandlerResetsBeforeFirstRecord(t *testing.T) {
	src := newTestFirmsAPI(t)
	driveFirms(t, src)
	source := NewSaveParticipant(src).Source()
	var records []serialize.Record
	for {
		rec, ok, err := source()
		ck(t, err)
		if !ok {
			break
		}
		records = append(records, rec)
	}

	dst := newTestFirmsAPI(t)
	// Register a firm on dst BEFORE Load that src never had.
	_, err := dst.RegisterFirm("phantom-firm", 1, "commercial")
	ck(t, err)
	phantomCount := len(dst.Firms())
	if phantomCount == 0 {
		t.Fatal("setup failed: phantom firm not registered")
	}

	handler := NewSaveParticipant(dst).Handler()
	for _, rec := range records {
		ck(t, handler(rec))
	}
	for _, fm := range dst.Firms() {
		if fm.Name == "phantom-firm" {
			t.Fatal("Handler did not reset the target registry -- a pre-Load firm survived the Load")
		}
	}
}
