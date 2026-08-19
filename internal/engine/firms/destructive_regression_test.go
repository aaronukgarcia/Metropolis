package firms

import (
	"math"
	"reflect"
	"sync"
	"testing"
)

// This file holds the regression tests for the six Destructive findings
// (SEC-100..SEC-105). Each test asserts the FIXED behaviour and would FAIL
// against the pre-fix code — see the per-test comment.

// TestSEC100CumulativeCreditBounded (P1, bounds-overflow): cumulative
// outstanding credit must never exceed the deposit-backed capacity, so a
// firm cannot borrow the full capacity repeatedly.
func TestSEC100CumulativeCreditBounded(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	fin := mustFinance(t)
	_ = api.SetFinance(fin)
	_ = api.SetCitizens(seedCitizens(t, 5))

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}

	seedDeposits(t, fin, 1_000_000_000) // £1000 deposit pool
	if cap := api.LendingCapacity(); cap != 900_000_000 {
		t.Fatalf("LendingCapacity = %d, want 900_000_000", cap)
	}

	approved := 0
	for i := 0; i < 5; i++ {
		d, err := api.ApproveCredit(CreditRequest{FirmID: id, Principal: 900_000_000, Month: 0})
		if err == nil && d.Approved {
			approved++
			continue
		}
		if !hasCode(err, ErrCreditDenied) {
			t.Fatalf("approval %d returned unexpected error %v", i, err)
		}
	}
	if approved != 1 {
		t.Fatalf("approved %d of 5 identical £900 requests; want exactly 1 (cumulative bound)", approved)
	}
	if got := api.TotalCreditOutstanding(); got != 900_000_000 {
		t.Fatalf("total outstanding = %d, want 900_000_000", got)
	}
}

// TestSEC101DuplicateHireRejected (P1, input-validation): the roster is a
// SET of real CitizenIDs — a duplicate hire is rejected, never deduped into
// a smaller headcount that could fake the staff floor.
func TestSEC101DuplicateHireRejected(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetCitizens(seedCitizens(t, 30))

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	if err := api.HireStaff(id, []uint64{2, 2, 2, 2, 2}); !hasCode(err, ErrDuplicateStaff) {
		t.Fatalf("HireStaff(duplicates) = %v, want ErrDuplicateStaff", err)
	}
	firm, err := api.Firm(id)
	if err != nil {
		t.Fatalf("Firm: %v", err)
	}
	if len(firm.Staff) != 1 || firm.Staff[0] != 1 {
		t.Fatalf("roster = %v, want [1] (duplicate hire must not append)", firm.Staff)
	}
	// A duplicate against the founder (already on roster) is also rejected.
	if err := api.HireStaff(id, []uint64{1}); !hasCode(err, ErrDuplicateStaff) {
		t.Fatalf("HireStaff(founder again) = %v, want ErrDuplicateStaff", err)
	}
}

// founderToFirm maps each founder citizen ID to its firm ID.
func founderToFirm(t *testing.T, api *FirmsAPI) map[uint64]FirmID {
	t.Helper()
	m := make(map[uint64]FirmID)
	for _, f := range api.Firms() {
		m[f.FounderCitizenID] = f.ID
	}
	return m
}

// TestSEC102ConcurrentFoundingDeterministicFirmIDs (P1, concurrency-safety):
// sharded concurrent founding must map every founder to the SAME firm ID as
// sequential founding, independent of goroutine scheduling.
func TestSEC102ConcurrentFoundingDeterministicFirmIDs(t *testing.T) {
	cfg := controlledConfig()
	cfg.Founding.BasePerMille = 1000 // every citizen founds, deterministically
	cfg.Founding.AmbitionPerMille = 0
	cfg.Founding.ExitFounderBoostPerMille = 0

	ids := make([]uint64, 200)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}

	seq := newAPIWithConfig(t, cfg, 7)
	_ = seq.SetCitizens(seedCitizens(t, 200))
	if _, err := seq.EvaluateFounding(ids, 1); err != nil {
		t.Fatalf("sequential EvaluateFounding: %v", err)
	}
	want := founderToFirm(t, seq)
	if len(want) != 200 {
		t.Fatalf("expected 200 founders, got %d", len(want))
	}

	for run := 0; run < 5; run++ {
		conc := newAPIWithConfig(t, cfg, 7)
		_ = conc.SetCitizens(seedCitizens(t, 200))
		const shards = 4
		var wg sync.WaitGroup
		for s := 0; s < shards; s++ {
			shard := s
			wg.Add(1)
			go func() {
				defer wg.Done()
				chunk := ids[shard*50 : (shard+1)*50]
				if _, err := conc.EvaluateFounding(chunk, 1); err != nil {
					t.Errorf("shard %d: %v", shard, err)
				}
			}()
		}
		wg.Wait()

		if got := founderToFirm(t, conc); !reflect.DeepEqual(want, got) {
			t.Fatalf("run %d: concurrent founder→firmID mapping diverged from sequential", run)
		}
	}
}

// TestSEC103PremisesZoneMustMatchStage (P2, insecure-call-surface): growth is
// gated on the firm's secured zone matching the target stage's required zone
// class, not merely "any premises secured".
func TestSEC103PremisesZoneMustMatchStage(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetCitizens(seedCitizens(t, 30))
	_ = api.SetBuild(mustBuild(t))

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	// Grant a dwelling (Startup's class), but Small requires "shop".
	if err := api.GrantPremises(id, "dwelling"); err != nil {
		t.Fatalf("GrantPremises: %v", err)
	}
	if err := api.Grow(id, []uint64{2, 3, 4, 5, 6}); !hasCode(err, ErrNoPremises) {
		t.Fatalf("Grow(wrong zone) = %v, want ErrNoPremises", err)
	}
	if st, _ := api.Stage(id); st != StageStartup {
		t.Fatalf("advanced on a mismatched zone: stage = %v", st)
	}

	// Grant the RIGHT zone, then grow (the earlier blocked attempt did not
	// commit its hires, so the same hires are still fresh).
	if err := api.GrantPremises(id, "shop"); err != nil {
		t.Fatalf("GrantPremises: %v", err)
	}
	if err := api.Grow(id, []uint64{2, 3, 4, 5, 6}); err != nil {
		t.Fatalf("Grow(right zone) = %v", err)
	}
	if st, _ := api.Stage(id); st != StageSmall {
		t.Fatalf("stage = %v, want Small", st)
	}
}

// TestSEC104ServicesDemandDoesNotOverflow (P2, integer-conversion): a huge
// served-firm count saturates to MaxInt64, never wraps to a negative figure.
func TestSEC104ServicesDemandDoesNotOverflow(t *testing.T) {
	api := newTestAPI(t, 1)
	d := api.ServicesDemand(1 << 44) // 2^44
	if d < 0 {
		t.Fatalf("ServicesDemand(2^44) = %d, want non-negative (saturated)", d)
	}
	if d != math.MaxInt64 {
		t.Fatalf("ServicesDemand(2^44) = %d, want math.MaxInt64 (saturated)", d)
	}
}

// validRawFirmsData is a minimal schema-valid rawFirmsData for buildConfig
// validation tests.
func validRawFirmsData() rawFirmsData {
	return rawFirmsData{
		Version: 1,
		Stages: []rawStage{
			{Stage: "startup", MinStaff: 1, PremiseClass: "dwelling"},
			{Stage: "small", MinStaff: 6, PremiseClass: "shop"},
			{Stage: "medium", MinStaff: 26, PremiseClass: "office"},
			{Stage: "enterprise", MinStaff: 251, PremiseClass: "heavy_industry"},
		},
		Founding: rawFounding{
			BasePerMille:             1,
			AmbitionPerMille:         200,
			EducationPerMille:        150,
			SectorExperiencePerMille: 100,
			WealthPerMille:           150,
			PremisesPerMille:         150,
			DemandPerMille:           100,
			ExitFounderBoostPerMille: 200,
		},
		ServicesDemand: rawServices{ExponentPerMille: 1300, Multiplier: 1000},
		Credit: rawCredit{
			DepositToLendingRatioPerMille: 900,
			CultureWindowMonths:           12,
			StageSpreadBp:                 map[string]int64{"startup": 300, "small": 200, "medium": 100, "enterprise": 0},
			BaseRateCycle:                 []rawRatePoint{{Month: 0, BaseRateBp: 500}},
		},
		LabourMarket: rawLabourMarket{EnterpriseCeiling: 500},
	}
}

// TestSEC105ConfigRejectsNonSuperlinearExponent (P3, input-validation): an
// exponent barely above 1 that truncates to linear-or-worse demand is
// rejected at validation, not silently accepted.
func TestSEC105ConfigRejectsNonSuperlinearExponent(t *testing.T) {
	raw := validRawFirmsData()
	raw.ServicesDemand.ExponentPerMille = 1001 // e = 1.001
	raw.ServicesDemand.Multiplier = 1          // Demand(1)=1, Demand(2)=2
	if _, err := buildConfig(raw, "firms.json", "firms-test"); !hasCode(err, ErrFirmsDataInvalid) {
		t.Fatalf("exponent=1001/multiplier=1 = %v, want ErrFirmsDataInvalid", err)
	}

	// The shipped form is still accepted.
	if _, err := buildConfig(validRawFirmsData(), "firms.json", "firms-test"); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}
