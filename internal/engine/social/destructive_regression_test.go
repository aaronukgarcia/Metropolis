package social

import (
	"errors"
	"math"
	"strings"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// socialCopy performs the byte-for-byte struct copy (go vet's copylocks
// check would flag a literal `cp := *a`), mirroring engine.crime's
// crimeCopy / engine.services' servicesCopy convention. It is the exact
// value-copy the SEC-020 copy guard exists to catch.
func socialCopy(a *SocialAPI) *SocialAPI {
	c := new(SocialAPI)
	*(*[unsafe.Sizeof(SocialAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(SocialAPI{})]byte)(unsafe.Pointer(a))
	return c
}

// TestRoughSleepingIsCurrentStockNotEverCounter (SEC-177): a homelessness
// case that fails all three paths stays StatusOpen; re-routing the SAME open
// case across passes must never re-increment the rough-sleeping count. With
// the cumulative ever-counter the repro is 3 -> 6; the fix derives the count
// from open homelessness cases, so it stays 3.
func TestRoughSleepingIsCurrentStockNotEverCounter(t *testing.T) {
	cfg := testConfig()
	cfg.HostelCapacity = 0 // every case fails all three paths
	a, err := New(cfg, 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = a.SetPrevention(false)
	_ = a.SetHousingFirst(false)

	_ = a.AdvanceMonth(1, DriverInputs{Deprivation: 1.0}) // 3 homelessness cases
	_ = a.RouteHomelessness(1)
	if got := a.RoughSleeping(); got != 3 {
		t.Fatalf("month 1 rough sleepers = %d, want 3", got)
	}
	// Re-route the SAME three still-open cases next month. The ever-counter
	// bug would report 6; the current-stock derivation reports 3.
	_ = a.RouteHomelessness(2)
	if got := a.RoughSleeping(); got != 3 {
		t.Fatalf("re-routing the same open cases must not re-increment: got %d, want 3", got)
	}
	if got := a.HomelessnessCaseload(); got != 3 {
		t.Fatalf("open homelessness caseload = %d, want 3 (the rough sleepers)", got)
	}
}

// TestHostelOccupancyIsPerMonth (SEC-178): hostel capacity is per-month
// occupancy, not a lifetime cap. 3 cases/month at capacity 2 place 2 each
// month (4 total), leaving a current rough-sleeping stock of 2 — never 2
// total placements and 5 cumulative rough sleepers.
func TestHostelOccupancyIsPerMonth(t *testing.T) {
	a, err := New(testConfig(), 1, "test") // HostelCapacity 2
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = a.SetPrevention(false)
	_ = a.SetHousingFirst(false)

	_ = a.AdvanceMonth(1, DriverInputs{Deprivation: 1.0}) // 3 homeless cases
	_ = a.RouteHomelessness(1)

	_ = a.AdvanceMonth(2, DriverInputs{Deprivation: 1.0}) // 3 more
	_ = a.RouteHomelessness(2)

	// Month 2 must have placed 2 NEW hostel cases: the lifetime-cap bug would
	// leave Resolved=0 in month 2 (capacity never freed).
	m2, err := a.Accounting(CategoryHomelessness, 2)
	if err != nil {
		t.Fatalf("Accounting: %v", err)
	}
	if m2.Resolved != 2 {
		t.Fatalf("month 2 must place 2 hostel cases (per-month capacity), got Resolved=%d", m2.Resolved)
	}
	m1, _ := a.Accounting(CategoryHomelessness, 1)
	if m1.Resolved != 2 {
		t.Fatalf("month 1 must place 2 hostel cases, got Resolved=%d", m1.Resolved)
	}
	// Current rough-sleeping stock: 4 open (1 left over + 3 new) - 2 placed = 2.
	if got := a.RoughSleeping(); got != 2 {
		t.Fatalf("current rough sleepers = %d, want 2 (per-month, not lifetime)", got)
	}
}

// TestFosterPlacementsArePerMonth (SEC-178, fostering half): a foster
// placement that queues at capacity in month 1 succeeds in month 2 because
// the placement count is per-month occupancy, not a lifetime cap.
func TestFosterPlacementsArePerMonth(t *testing.T) {
	a, err := New(testConfig(), 1, "test") // FosterCapacity 2
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = a.AdvanceMonth(1, DriverInputs{CrowdingStress: 2, FinancialStress: 2}) // 4 fostering cases
	ids := a.OpenCaseIDs(CategoryFostering)
	if len(ids) < 3 {
		t.Fatalf("expected at least 3 fostering cases, got %d", len(ids))
	}
	if r, err := a.AttemptFosteringPlacement(ids[0], 1); err != nil || r != PlacementPlaced {
		t.Fatalf("placement 1 (month 1): r=%v err=%v", r, err)
	}
	if r, err := a.AttemptFosteringPlacement(ids[1], 1); err != nil || r != PlacementPlaced {
		t.Fatalf("placement 2 (month 1): r=%v err=%v", r, err)
	}
	// Month 2: the previously-queued case now places — the lifetime cap would
	// keep it queued forever.
	if r, err := a.AttemptFosteringPlacement(ids[2], 2); err != nil || r != PlacementPlaced {
		t.Fatalf("month-2 placement must succeed (per-month capacity): r=%v err=%v", r, err)
	}
}

// TestHugeFuseYearsRejected (SEC-179): a huge finite FuseYears must be
// rejected rather than clamping the months conversion to MaxInt64 and
// wrapping the completion-month sum negative, which would land the step at
// the wrong near-epoch month.
func TestHugeFuseYearsRejected(t *testing.T) {
	a, _ := wiredWithServices(t)
	proj := projections.NewProjectionsAPI()
	if err := a.SetProjections(proj); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}
	if err := a.RegisterProjectionProvider(); err != nil {
		t.Fatalf("RegisterProjectionProvider: %v", err)
	}
	if err := a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 1.0, Month: 0}); err != nil {
		t.Fatalf("baseline funding: %v", err)
	}

	cut := FundingCommand{
		Category:  CategoryFamilySupport,
		Level:     0.5,
		Month:     1,
		FuseYears: 1e18,
		Projection: ProjectedConsequence{
			Description: "family-support caseload projected to rise",
			Series:      []float64{10, 20},
		},
	}
	if err := a.SetFunding(cut); err == nil {
		t.Fatal("expected a huge finite FuseYears to be rejected")
	} else if !errors.Is(err, &errs.E{Code: ErrInvalidFuseYears}) {
		t.Fatalf("error code = %v, want %s", err, ErrInvalidFuseYears)
	}

	// No decision step may have leaked: the curve at a small month is the
	// provider value (0 open cases), not the wrapped step.
	pts, err := proj.Curve(caseloadCurveKey, 1, 1)
	if err != nil {
		t.Fatalf("Curve: %v", err)
	}
	if len(pts) != 1 || pts[0].Value != 0 {
		t.Fatalf("no step may leak at a small month after rejection; got %+v", pts)
	}
}

// TestBackDatedCloseRejected (SEC-180): closing a case in a month before it
// opened would record Resolved=1 at a month where Opened=0 and drive the
// AC-11 conservation identity negative. All three closure kinds plus the
// escalation path reject it with a typed registry error.
func TestBackDatedCloseRejected(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resolveID, _ := a.InjectCrisis(CrisisEvent{ID: "c1", Month: 5})
	if err := a.ResolveCase(resolveID, 2, "reunited"); err == nil {
		t.Fatal("expected a back-dated ResolveCase (month 2 < opened 5) to be rejected")
	} else if !errors.Is(err, &errs.E{Code: ErrBackDatedMonth}) {
		t.Fatalf("ResolveCase error = %v, want %s", err, ErrBackDatedMonth)
	}

	lostID, _ := a.InjectCrisis(CrisisEvent{ID: "c2", Month: 5})
	if err := a.LoseToFollowUp(lostID, 2); err == nil {
		t.Fatal("expected a back-dated LoseToFollowUp to be rejected")
	} else if !errors.Is(err, &errs.E{Code: ErrBackDatedMonth}) {
		t.Fatalf("LoseToFollowUp error = %v, want %s", err, ErrBackDatedMonth)
	}

	escID, _ := a.InjectCrisis(CrisisEvent{ID: "c3", Month: 5})
	if _, err := a.EscalateCase(escID, 2, CategoryFostering); err == nil {
		t.Fatal("expected a back-dated EscalateCase to be rejected")
	} else if !errors.Is(err, &errs.E{Code: ErrBackDatedMonth}) {
		t.Fatalf("EscalateCase error = %v, want %s", err, ErrBackDatedMonth)
	}

	// The conserved identity is untouched: month 2 records no resolution.
	s, err := a.Accounting(CategoryFamilySupport, 2)
	if err != nil {
		t.Fatalf("Accounting: %v", err)
	}
	if s.Resolved != 0 || s.Escalated != 0 || s.LostToFollowUp != 0 {
		t.Fatalf("back-dated closures must not record any month-2 event, got %+v", s)
	}
}

// TestBackDatedFosterPlacementRejected (SEC-180): the fostering placement
// path also closes a case, so a placement attempt in a month before the case
// opened is rejected rather than recording a phantom placement.
func TestBackDatedFosterPlacementRejected(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = a.AdvanceMonth(3, DriverInputs{CrowdingStress: 1, FinancialStress: 1})
	ids := a.OpenCaseIDs(CategoryFostering)
	if len(ids) == 0 {
		t.Fatal("expected fostering cases")
	}
	if r, err := a.AttemptFosteringPlacement(ids[0], 1); err == nil || r != PlacementQueued {
		t.Fatalf("back-dated placement: r=%v err=%v, want queued + ErrBackDatedMonth", r, err)
	} else if !errors.Is(err, &errs.E{Code: ErrBackDatedMonth}) {
		t.Fatalf("error = %v, want %s", err, ErrBackDatedMonth)
	}
}

// TestHugeDeprivationRejected (SEC-181, P1): the DriverInputs domain is
// prose-only in the old code, so Deprivation=1e5 would yield ~900k proposals
// and 1e15 would exhaust memory. The boundary now rejects any out-of-domain
// driver value before any allocation.
func TestHugeDeprivationRejected(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := a.GenerateCaseload(0, DriverInputs{Deprivation: 1e5}); err == nil {
		t.Fatal("expected Deprivation=1e5 to be rejected")
	} else if !errors.Is(err, &errs.E{Code: ErrInvalidDriverInput}) {
		t.Fatalf("error = %v, want %s", err, ErrInvalidDriverInput)
	}
	// 1e15 would OOM the old code; it must be rejected, never allocated.
	if err := a.AdvanceMonth(0, DriverInputs{Deprivation: 1e15}); err == nil {
		t.Fatal("expected Deprivation=1e15 to be rejected")
	} else if !errors.Is(err, &errs.E{Code: ErrInvalidDriverInput}) {
		t.Fatalf("error = %v, want %s", err, ErrInvalidDriverInput)
	}
	// Every other documented domain is enforced too (the class sweep).
	for _, in := range []DriverInputs{
		{Deprivation: math.NaN()},
		{Deprivation: -0.1},
		{NightlifeDensity: 2},
		{NightlifeDensity: math.NaN()},
		{CrowdingStress: -1},
		{CrowdingStress: math.Inf(1)},
		{FinancialStress: -1},
		{UnemploymentMonths: -1},
	} {
		if _, err := a.GenerateCaseload(0, in); err == nil {
			t.Fatalf("expected driver input %+v to be rejected", in)
		} else if !errors.Is(err, &errs.E{Code: ErrInvalidDriverInput}) {
			t.Fatalf("input %+v: error = %v, want %s", in, err, ErrInvalidDriverInput)
		}
	}
}

// TestMagnitudeDriverCountBounded (SEC-195): the two magnitude drivers
// (CrowdingStress, FinancialStress) are documented "finite and >= 0", so
// SEC-181 does not bound them — but the per-month proposal COUNT must still be
// bounded at the allocation site, or a huge finite magnitude mints unbounded
// proposals (CrowdingStress=1e5 -> 400,000 via the shipped rates; 1e15 ->
// ~4e15 -> OOM; FinancialStress=1e5 -> 1,000,000 ledger cases). The fix bounds
// the COUNT, not the magnitude: a driver whose month would propose more than
// maxCaseloadProposalsPerMonth cases is rejected with ErrCaseloadExceedsLimit
// before any allocation — never a balance-number clamp on the magnitude itself.
func TestMagnitudeDriverCountBounded(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Run("crowding_1e5_rejected", func(t *testing.T) {
		out, err := a.GenerateCaseload(0, DriverInputs{CrowdingStress: 1e5})
		if err == nil {
			t.Fatalf("CrowdingStress=1e5 minted %d proposals with no error; want rejection", len(out))
		}
		if !errors.Is(err, &errs.E{Code: ErrCaseloadExceedsLimit}) {
			t.Fatalf("error = %v, want %s", err, ErrCaseloadExceedsLimit)
		}
	})

	t.Run("crowding_1e15_rejected_never_OOM", func(t *testing.T) {
		// 1e15 would OOM the pre-fix code (~4e15 proposals). Rejected before any
		// allocation, so this subtest is only safe to run post-fix.
		if _, err := a.GenerateCaseload(0, DriverInputs{CrowdingStress: 1e15}); err == nil {
			t.Fatal("CrowdingStress=1e15 must be rejected, never OOM")
		} else if !errors.Is(err, &errs.E{Code: ErrCaseloadExceedsLimit}) {
			t.Fatalf("error = %v, want %s", err, ErrCaseloadExceedsLimit)
		}
	})

	t.Run("financial_1e5_advance_rejected", func(t *testing.T) {
		if err := a.AdvanceMonth(0, DriverInputs{FinancialStress: 1e5}); err == nil {
			t.Fatal("FinancialStress=1e5 must be rejected, never open 1,000,000 ledger cases")
		} else if !errors.Is(err, &errs.E{Code: ErrCaseloadExceedsLimit}) {
			t.Fatalf("error = %v, want %s", err, ErrCaseloadExceedsLimit)
		}
		// The rejected AdvanceMonth must not have opened any case (no partial
		// ledger mutation before the bound rejects).
		if ids := a.OpenCaseIDs(CategoryFostering); len(ids) != 0 {
			t.Fatalf("rejected AdvanceMonth must not open cases, got %d fostering cases", len(ids))
		}
	})

	t.Run("modest_magnitude_unaffected", func(t *testing.T) {
		if _, err := a.GenerateCaseload(0, DriverInputs{CrowdingStress: 2, FinancialStress: 3}); err != nil {
			t.Fatalf("in-domain magnitudes must not hit the count bound: %v", err)
		}
	})
}

// TestHugeConfigRateCountBounded (SEC-195, config-rate door): a huge finite
// caseload rate shares the unbounded-count shape — it is validated only
// finite/non-negative, so a huge finite rate with an in-domain driver also
// OOMs. The count bound at the allocation site closes this door without
// inventing a balance bound on the rate itself.
func TestHugeConfigRateCountBounded(t *testing.T) {
	cfg := testConfig()
	cfg.Caseload.FamilyPerDeprivation = 1e15
	a, err := New(cfg, 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Deprivation=1.0 is in-domain; the huge rate makes the proposal count
	// pathological, so the allocation is rejected rather than OOM'd.
	if out, err := a.GenerateCaseload(0, DriverInputs{Deprivation: 1.0}); err == nil {
		t.Fatalf("expected a huge finite rate to be rejected, got %d proposals", len(out))
	} else if !errors.Is(err, &errs.E{Code: ErrCaseloadExceedsLimit}) {
		t.Fatalf("error = %v, want %s", err, ErrCaseloadExceedsLimit)
	}
}

// TestHugeCrisisRateCountBounded (SEC-195, crisis door): InjectCrisis opens
// caseloadCount(CrisisFamilyCases) cases, so a huge finite CrisisFamilyCases
// rate would loop ~forever and poison the ledger. The same count bound rejects
// it at the allocation site.
func TestHugeCrisisRateCountBounded(t *testing.T) {
	cfg := testConfig()
	cfg.Caseload.CrisisFamilyCases = 1e15
	a, err := New(cfg, 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a.InjectCrisis(CrisisEvent{ID: "c1", Month: 1}); err == nil {
		t.Fatal("expected a huge finite CrisisFamilyCases rate to be rejected")
	} else if !errors.Is(err, &errs.E{Code: ErrCaseloadExceedsLimit}) {
		t.Fatalf("error = %v, want %s", err, ErrCaseloadExceedsLimit)
	}
}

// TestCrisisIDLengthBounded (SEC-203): InjectCrisis validated CrisisEvent.ID
// only as non-empty, then concatenated "crisis:"+ev.ID inside the open-case
// loop — a huge id was byte-copied up to count× into the conserved, append-
// only case ledger and retained for the life of the API. The fix bounds
// len(ev.ID) at the boundary via num.SanitizeEventID (MaxEventIDLength) and
// hoists the Source prefix out of the loop so every opened case shares ONE
// backing array.
func TestCrisisIDLengthBounded(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// An id over the ceiling is rejected with a registry-sourced error BEFORE
	// any case is opened — the retained-amplification vector is closed.
	tooLong := strings.Repeat("x", num.MaxEventIDLength+1)
	if _, err := a.InjectCrisis(CrisisEvent{ID: tooLong, Month: 1}); err == nil {
		t.Fatal("expected a >64-byte crisis id to be rejected")
	} else if !errors.Is(err, &errs.E{Code: "MET-F805"}) {
		t.Fatalf("over-length id error = %v, want MET-F805", err)
	}

	// An empty id is rejected too (SanitizeEventID's empty-guard — the old
	// ErrInvalidCrisis path, now routed through the shared boundary).
	if _, err := a.InjectCrisis(CrisisEvent{ID: "", Month: 1}); err == nil {
		t.Fatal("expected an empty crisis id to be rejected")
	} else if !errors.Is(err, &errs.E{Code: "MET-F804"}) {
		t.Fatalf("empty id error = %v, want MET-F804", err)
	}

	// The bound is "over", never "at or over": an id AT the ceiling is accepted,
	// and each opened case stays individually traceable (AC-5).
	atCeiling := strings.Repeat("y", num.MaxEventIDLength)
	first, err := a.InjectCrisis(CrisisEvent{ID: atCeiling, Month: 1})
	if err != nil {
		t.Fatalf("a %d-byte crisis id must be accepted: %v", num.MaxEventIDLength, err)
	}
	c, err := a.Case(first)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if c.CrisisID != atCeiling {
		t.Fatalf("case must stay traceable to the crisis id: CrisisID length %d, want %d", len(c.CrisisID), len(atCeiling))
	}
	if c.Source != "crisis:"+atCeiling {
		t.Fatalf("case Source = %q, want the hoisted \"crisis:\"+id prefix", c.Source)
	}
}

// TestCopiedValueReadAccessorsRejected (SEC-182): the seven read-only
// accessors previously skipped checkNotCopied, so a struct-copied value
// returned a stale scalar (0 after the original reached a non-zero value).
// Every accessor now runs the guard and returns its zero value.
func TestCopiedValueReadAccessorsRejected(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Give the original a real, non-zero stock so a stale-scalar return would
	// be visible.
	_ = a.AdvanceMonth(1, DriverInputs{Deprivation: 1.0})
	_ = a.RouteHomelessness(1)
	if a.RoughSleeping() == 0 || a.HostelPlaced() == 0 || a.HomelessnessCaseload() == 0 {
		t.Fatal("original must carry non-zero stocks for the stale-scalar test to be meaningful")
	}

	cp := socialCopy(a)
	if got := cp.RoughSleeping(); got != 0 {
		t.Fatalf("copied RoughSleeping() = %d, want 0 (guard must fire)", got)
	}
	if got := cp.RoughSleepingLocation(); got != "" {
		t.Fatalf("copied RoughSleepingLocation() = %q, want empty", got)
	}
	if got := cp.Prevented(); got != 0 {
		t.Fatalf("copied Prevented() = %d, want 0", got)
	}
	if got := cp.HostelPlaced(); got != 0 {
		t.Fatalf("copied HostelPlaced() = %d, want 0", got)
	}
	if got := cp.HousingFirstPlaced(); got != 0 {
		t.Fatalf("copied HousingFirstPlaced() = %d, want 0", got)
	}
	if got := cp.CarersReleased(); got != 0 {
		t.Fatalf("copied CarersReleased() = %d, want 0", got)
	}
	if got := cp.FamilySupportCaseload(); got != 0 {
		t.Fatalf("copied FamilySupportCaseload() = %d, want 0", got)
	}
	if got := cp.HomelessnessCaseload(); got != 0 {
		t.Fatalf("copied HomelessnessCaseload() = %d, want 0", got)
	}
	if got := cp.DisabilityCarersCaseload(); got != 0 {
		t.Fatalf("copied DisabilityCarersCaseload() = %d, want 0", got)
	}
	if got := cp.FosteringCaseload(); got != 0 {
		t.Fatalf("copied FosteringCaseload() = %d, want 0", got)
	}
	if got := cp.AddictionCaseload(); got != 0 {
		t.Fatalf("copied AddictionCaseload() = %d, want 0", got)
	}

	// The original remains intact and live.
	if a.RoughSleeping() == 0 {
		t.Fatal("original corrupted by the copy-guard test")
	}
}

// TestSlowFuseThresholdDrift (SEC-183, half 1): social's slowFuseThresholdYears
// duplicates engine.projections' unexported threshold across the module
// boundary (GR#20 forbids the import). A drift test pins both: the local
// constant to A5's "5 game-years", and projections' gate behaviour at the 5-
// year boundary. If EITHER side changes, one of the assertions fails.
func TestSlowFuseThresholdDrift(t *testing.T) {
	if slowFuseThresholdYears != 5.0 {
		t.Fatalf("social slowFuseThresholdYears drifted to %v: A5 specifies 5 game-years; change it in lockstep with engine.projections' slowFuseThresholdYears", slowFuseThresholdYears)
	}

	p := projections.NewProjectionsAPI()
	above := projections.Decision{ID: "drift-above", FuseYears: 5.0000001}
	if err := p.EnqueueDecision(above); err == nil {
		t.Fatal("engine.projections accepted a >5-year decision with no payload — its threshold has drifted ABOVE 5; keep the two constants in lockstep")
	} else if !errors.Is(err, &errs.E{Code: projections.ErrSlowFuseMissingPayload}) {
		t.Fatalf("engine.projections rejected a >5-year decision with %v, want %s", err, projections.ErrSlowFuseMissingPayload)
	}
	at := projections.Decision{ID: "drift-at", FuseYears: 5}
	if err := p.EnqueueDecision(at); err != nil {
		t.Fatalf("engine.projections rejected a 5-year decision with no payload (%v) — its threshold has drifted BELOW 5; keep the two constants in lockstep", err)
	}
}

// TestRejectedCutLeavesFundingUnchanged (SEC-183, half 2): a funding cut that
// is rejected — whether by this module's own local Slow-Fuse check or (after
// the reorder) by the projections gate — must leave funding untouched, never
// a partial state where the cut landed but the projection did not.
func TestRejectedCutLeavesFundingUnchanged(t *testing.T) {
	a, _ := wiredWithServices(t)
	if err := a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 1.0, Month: 0}); err != nil {
		t.Fatalf("baseline funding: %v", err)
	}
	before, err := a.FundingLevel(CategoryFamilySupport)
	if err != nil {
		t.Fatalf("FundingLevel: %v", err)
	}

	// Local rejection: a slow-fuse cut with no projection series.
	err = a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 0.5, Month: 1, FuseYears: 10})
	if err == nil || !errors.Is(err, &errs.E{Code: ErrSlowFusePayloadMissing}) {
		t.Fatalf("expected ErrSlowFusePayloadMissing, got %v", err)
	}
	after, err := a.FundingLevel(CategoryFamilySupport)
	if err != nil {
		t.Fatalf("FundingLevel: %v", err)
	}
	if after != before {
		t.Fatalf("a rejected cut must leave funding unchanged: before=%v after=%v", before, after)
	}

	// Degenerate FuseYears rejection also leaves funding unchanged.
	err = a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 0.5, Month: 1, FuseYears: 1e18, Projection: ProjectedConsequence{Series: []float64{10, 20}}})
	if err == nil || !errors.Is(err, &errs.E{Code: ErrInvalidFuseYears}) {
		t.Fatalf("expected ErrInvalidFuseYears, got %v", err)
	}
	after, err = a.FundingLevel(CategoryFamilySupport)
	if err != nil {
		t.Fatalf("FundingLevel: %v", err)
	}
	if after != before {
		t.Fatalf("a rejected cut must leave funding unchanged: before=%v after=%v", before, after)
	}
}

// TestCaseloadOverflowSaturatesNotZeroes (SEC-199): caseloadCount's old
// finite-guard `if !numFinite(v) || v <= 0 { return 0 }` collapsed +Inf — a
// rate*driver product that overflowed float64 — to 0, so the SEC-195 ceiling
// never saw it and a pathological combination silently produced ZERO
// proposals with nil error. That is non-monotonic with the finite case:
// CrowdingStress=1.0 * 1e308 = 1e308 (finite) saturates to MaxInt64 and is
// rejected, while CrowdingStress=2.0 * 1e308 = 2e308 = +Inf was silently
// accepted as zero. The fix saturates +Inf to math.MaxInt64 so the ceiling
// rejects it exactly as it rejects the finite saturation; NaN and non-positive
// values still collapse to 0 (the honest empty/poison cases).
func TestCaseloadOverflowSaturatesNotZeroes(t *testing.T) {
	// Direct unit check: +Inf must saturate to MaxInt64, never collapse to 0.
	if got := caseloadCount(math.Inf(1)); got != math.MaxInt64 {
		t.Fatalf("caseloadCount(+Inf) = %d, want math.MaxInt64 (saturate, not 0)", got)
	}
	// NaN and non-positive still collapse to 0.
	if got := caseloadCount(math.NaN()); got != 0 {
		t.Fatalf("caseloadCount(NaN) = %d, want 0", got)
	}
	if got := caseloadCount(0); got != 0 {
		t.Fatalf("caseloadCount(0) = %d, want 0", got)
	}
	if got := caseloadCount(-1); got != 0 {
		t.Fatalf("caseloadCount(-1) = %d, want 0", got)
	}
	// A finite 1e308 saturates to MaxInt64 (the finite-saturating regime).
	if got := caseloadCount(1e308); got != math.MaxInt64 {
		t.Fatalf("caseloadCount(1e308) = %d, want math.MaxInt64", got)
	}

	// Non-monotonicity through the public API: a config rate of 1e308 is
	// finite/non-negative so Config.validate accepts it; CrowdingStress=1.0
	// produces 1e308 (finite -> MaxInt64 -> rejected), while CrowdingStress=2.0
	// produces 2e308 = +Inf. Pre-fix, +Inf collapsed to 0 proposals with nil
	// error; post-fix it saturates to MaxInt64 and the ceiling rejects.
	cfg := testConfig()
	cfg.Caseload.FamilyPerCrowdingStress = 1e308
	a, err := New(cfg, 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := a.GenerateCaseload(0, DriverInputs{CrowdingStress: 1.0}); err == nil {
		t.Fatal("CrowdingStress=1.0 with rate 1e308 must be rejected by the ceiling")
	} else if !errors.Is(err, &errs.E{Code: ErrCaseloadExceedsLimit}) {
		t.Fatalf("CrowdingStress=1.0: error = %v, want %s", err, ErrCaseloadExceedsLimit)
	}

	if out, err := a.GenerateCaseload(0, DriverInputs{CrowdingStress: 2.0}); err == nil {
		t.Fatalf("CrowdingStress=2.0 (2e308 = +Inf) must be rejected, not silently zero: got %d proposals with nil error", len(out))
	} else if !errors.Is(err, &errs.E{Code: ErrCaseloadExceedsLimit}) {
		t.Fatalf("CrowdingStress=2.0: error = %v, want %s", err, ErrCaseloadExceedsLimit)
	}
}

// TestProjectedDeltaOverflowRejected (SEC-200): projectedDelta computes
// Series[last]-Series[0]. seriesFinite validates each series value is finite,
// but finite-minus-finite can overflow — [-MaxFloat64, +MaxFloat64] yields
// +Inf, which flows into engine.projections' queued step and poisons the curve
// at the completion month. The derived delta must be finite-guarded and
// rejected with ErrInvalidSeries, mirroring seriesFinite (weakness pattern #3:
// the guard closed the NaN/Inf series-value instance but left the derived-
// value overflow standing).
func TestProjectedDeltaOverflowRejected(t *testing.T) {
	a, _ := wiredWithServices(t)
	proj := projections.NewProjectionsAPI()
	if err := a.SetProjections(proj); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}
	if err := a.RegisterProjectionProvider(); err != nil {
		t.Fatalf("RegisterProjectionProvider: %v", err)
	}
	if err := a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 1.0, Month: 0}); err != nil {
		t.Fatalf("baseline funding: %v", err)
	}

	// Both overflow directions of finite-minus-finite must be rejected: a
	// finite difference that overflows to +Inf (last-first) or -Inf
	// (first-last) is a poisoned step, never a silently-stored curve value.
	for _, tc := range []struct {
		name   string
		series []float64
	}{
		{"positive overflow", []float64{-math.MaxFloat64, math.MaxFloat64}},
		{"negative overflow", []float64{math.MaxFloat64, -math.MaxFloat64}},
	} {
		cut := FundingCommand{
			Category:  CategoryFamilySupport,
			Level:     0.5,
			Month:     1,
			FuseYears: 10,
			Projection: ProjectedConsequence{
				Description: "overflowing last-minus-first delta",
				Series:      tc.series,
			},
		}
		if err := a.SetFunding(cut); err == nil {
			t.Fatalf("%s: expected a series whose delta overflows to be rejected", tc.name)
		} else if !errors.Is(err, &errs.E{Code: ErrInvalidSeries}) {
			t.Fatalf("%s: error = %v, want %s", tc.name, err, ErrInvalidSeries)
		}
	}

	// No ±Inf step may leak into the curve at the completion month: the value
	// must be the 0-open-case provider value, not a poisoned step.
	pts, err := proj.Curve(caseloadCurveKey, 121, 121)
	if err != nil {
		t.Fatalf("Curve: %v", err)
	}
	if len(pts) != 1 || pts[0].Value != 0 {
		t.Fatalf("no ±Inf step may leak after rejection; completion-month value = %+v, want 0", pts)
	}
}

// TestProjectionSeriesLengthBounded (SEC-202): a funding cut's projected-
// consequence Series had no length bound, so a 1,000,000-point series was
// accepted with nil error and drove three O(n) passes (seriesFinite,
// toProjectedConsequence's make([]Point, 0, len(Series)), and projections'
// consequence.empty()) plus a ~32MB transient allocation — weakness pattern
// #6 (bound the INPUT, not just the output). The fix rejects len(Series) over
// maxProjectionSeriesPoints at the write boundary with a registry-sourced
// error, mirroring maxCaseloadProposalsPerMonth (SEC-195).
func TestProjectionSeriesLengthBounded(t *testing.T) {
	// Off-by-one on the ceiling itself: a series AT the ceiling is accepted,
	// one OVER is rejected — the bound is "over", never "at or over".
	if err := checkSeriesLength(make([]float64, maxProjectionSeriesPoints), "test"); err != nil {
		t.Fatalf("a series of exactly maxProjectionSeriesPoints points must be accepted, got %v", err)
	}
	if err := checkSeriesLength(make([]float64, maxProjectionSeriesPoints+1), "test"); err == nil {
		t.Fatal("a series of maxProjectionSeriesPoints+1 points must be rejected at the helper boundary")
	} else if !errors.Is(err, &errs.E{Code: ErrProjectionSeriesTooLong}) {
		t.Fatalf("error = %v, want %s", err, ErrProjectionSeriesTooLong)
	}

	// Through the public API: a >ceiling series must be rejected with a
	// registry error BEFORE any O(n) scan or allocation, leaving funding
	// unchanged and no decision step leaked into the curve.
	a, _ := wiredWithServices(t)
	proj := projections.NewProjectionsAPI()
	if err := a.SetProjections(proj); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}
	if err := a.RegisterProjectionProvider(); err != nil {
		t.Fatalf("RegisterProjectionProvider: %v", err)
	}
	if err := a.SetFunding(FundingCommand{Category: CategoryFamilySupport, Level: 1.0, Month: 0}); err != nil {
		t.Fatalf("baseline funding: %v", err)
	}

	series := make([]float64, maxProjectionSeriesPoints+1)
	for i := range series {
		series[i] = float64(i)
	}
	cut := FundingCommand{
		Category:  CategoryFamilySupport,
		Level:     0.5,
		Month:     1,
		FuseYears: 10,
		Projection: ProjectedConsequence{
			Description: "family-support caseload projected to rise",
			Series:      series,
		},
	}
	if err := a.SetFunding(cut); err == nil {
		t.Fatalf("expected a series of %d points to be rejected, got nil error", len(series))
	} else if !errors.Is(err, &errs.E{Code: ErrProjectionSeriesTooLong}) {
		t.Fatalf("error = %v, want %s", err, ErrProjectionSeriesTooLong)
	}

	// The rejected cut must leave funding unchanged and leak no decision step
	// into the curve (the length check runs before EnqueueDecision).
	if got, err := a.FundingLevel(CategoryFamilySupport); err != nil || got != 1.0 {
		t.Fatalf("rejected cut must leave funding unchanged: got %v err=%v", got, err)
	}
	pts, err := proj.Curve(caseloadCurveKey, 121, 121)
	if err != nil {
		t.Fatalf("Curve: %v", err)
	}
	if len(pts) != 1 || pts[0].Value != 0 {
		t.Fatalf("no step may leak after rejection; completion-month value = %+v, want 0", pts)
	}
}

// TestMaxInt64HostelCapacityDoesNotWrapNegative (SEC-201): HostelCapacity is
// validated only >= 0, so MaxInt64 passes Config.validate. categoryCapacity
// registers float64(MaxInt64) = 2^63 into services, and the bare int64(c) read
// back at RouteHomelessness wrapped 2^63 to MinInt64 on amd64 — the capacity
// gate read negative, so every hostel placement was silently skipped. The fix
// reads capacity back through num.ClampInt64FromFloat, so a MaxInt64 config
// means "effectively unlimited" and all cases place.
func TestMaxInt64HostelCapacityDoesNotWrapNegative(t *testing.T) {
	cfg := testConfig()
	cfg.HostelCapacity = math.MaxInt64
	a, err := New(cfg, 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := services.New("test")
	if err := a.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if err := a.RegisterServices(); err != nil {
		t.Fatalf("RegisterServices: %v", err)
	}
	_ = a.SetPrevention(false)
	_ = a.SetHousingFirst(false)

	_ = a.AdvanceMonth(1, DriverInputs{Deprivation: 1.0}) // 3 homelessness cases
	if err := a.RouteHomelessness(1); err != nil {
		t.Fatalf("RouteHomelessness: %v", err)
	}

	if got := a.HostelPlaced(); got != 3 {
		t.Fatalf("HostelCapacity=MaxInt64 must place all 3 cases (effectively unlimited), got %d", got)
	}
	if got := a.RoughSleeping(); got != 0 {
		t.Fatalf("no case may fall through to rough sleeping with unlimited capacity, got %d", got)
	}
}

// TestMaxInt64FosterCapacityDoesNotWrapNegative (SEC-201, fostering half): the
// same bare int64(c) read-back at AttemptFosteringPlacement wrapped a MaxInt64
// FosterCapacity to MinInt64, so `fosterPlacements >= capacity` was true
// immediately and the first placement was silently queued. The fix saturates
// the read-back, so unlimited capacity places.
func TestMaxInt64FosterCapacityDoesNotWrapNegative(t *testing.T) {
	cfg := testConfig()
	cfg.FosterCapacity = math.MaxInt64
	a, err := New(cfg, 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := services.New("test")
	if err := a.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if err := a.RegisterServices(); err != nil {
		t.Fatalf("RegisterServices: %v", err)
	}

	_ = a.AdvanceMonth(1, DriverInputs{CrowdingStress: 1, FinancialStress: 1}) // 2 fostering cases
	ids := a.OpenCaseIDs(CategoryFostering)
	if len(ids) == 0 {
		t.Fatal("expected fostering cases")
	}
	if r, err := a.AttemptFosteringPlacement(ids[0], 1); err != nil || r != PlacementPlaced {
		t.Fatalf("first placement must succeed with unlimited capacity: r=%v err=%v", r, err)
	}
}
