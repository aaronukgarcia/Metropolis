package firms

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// newTestAPI constructs a FirmsAPI against the REAL data/firms.json (via
// ResolveDataDir), with no siblings wired yet — tests wire what they need.
func newTestAPI(t *testing.T, seed uint64) *FirmsAPI {
	t.Helper()
	dir, err := data.ResolveDataDir("firms-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	api, err := Load(dir, seed, "firms-test-correlation")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return api
}

// newAPIWithConfig constructs a FirmsAPI with a fully-controlled config
// (for the isolation/permutation/determinism tests, whose assertions rest
// on known weights, not the production balance numbers). Same-package, so
// the config and self guard are set directly.
func newAPIWithConfig(t *testing.T, cfg config, seed uint64) *FirmsAPI {
	t.Helper()
	api := &FirmsAPI{
		correlationID:  "firms-test-correlation",
		seed:           seed,
		cfg:            cfg,
		firms:          make(map[FirmID]*firmState),
		founderHistory: make(map[uint64]*founderRecord),
		subscribers:    make(map[uint64]chan LifecycleEvent),
		nextSubID:      1,
	}
	api.self.Store(api)
	return api
}

// controlledConfig is a deterministic, hand-set config for isolation and
// determinism tests: ambition is the ONLY non-zero founding driver and is
// scaled ×1000 so ambition=100 → pPerMille=1000 (always founds) and
// ambition=0 → pPerMille=0 (never founds). This makes the AC-3 permutation
// test and the AC-17 determinism test exact rather than probable.
func controlledConfig() config {
	return config{
		Stages: []stageConfig{
			{Stage: StageStartup, MinStaff: 1, PremiseClass: "dwelling"},
			{Stage: StageSmall, MinStaff: 6, PremiseClass: "shop"},
			{Stage: StageMedium, MinStaff: 26, PremiseClass: "office"},
			{Stage: StageEnterprise, MinStaff: 251, PremiseClass: "heavy_industry"},
		},
		Founding: foundingConfig{
			BasePerMille:             0,
			AmbitionPerMille:         1000,
			EducationPerMille:        0,
			WealthPerMille:           0,
			PremisesPerMille:         0,
			DemandPerMille:           0,
			ExitFounderBoostPerMille: 200,
		},
		ServicesDemand: servicesDemandConfig{ExponentPerMille: 1300, Multiplier: 1000},
		Credit: creditConfig{
			DepositToLendingRatioPerMille: 900,
			CultureWindowMonths:           12,
			StageSpreadBp: map[Stage]int64{
				StageStartup: 300, StageSmall: 200, StageMedium: 100, StageEnterprise: 0,
			},
			BaseRateCycle: []ratePoint{{Month: 0, BaseRateBp: 500}, {Month: 96, BaseRateBp: 900}},
		},
	}
}

// mustCitizens builds a CitizensAPI seeded with the given cold records.
func mustCitizens(t *testing.T, recs []citizens.ColdRecord) *citizens.CitizensAPI {
	t.Helper()
	c, err := citizens.NewCitizensAPI(1, "firms-test-citizens")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := c.SeedColdRecords(recs, "firms-test-citizens"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	return c
}

// citizenRecord builds a valid cold record with the given id, ambition
// (0-100), sector, and wealth. Attainment/satisfaction are fixed so the
// only founding input that varies is ambition.
func citizenRecord(id uint64, ambition int32, sector citizens.Sector, wealth int64) citizens.ColdRecord {
	var p [citizens.NumPersonalityAxes]int8
	p[citizens.AxisAmbition] = int8(ambition)
	return citizens.ColdRecord{
		ID:              id,
		BirthMonth:      240,
		Sex:             citizens.SexFemale,
		Home:            1,
		Personality:     p,
		Attainment:      50,
		Stage:           citizens.StageAdultEd,
		HealthBand:      citizens.HealthGood,
		Wealth:          wealth,
		EmploymentState: citizens.EmploymentEmployed,
		Sector:          sector,
		SatHousing:      50,
		SatServices:     50,
		SatEnvironment:  50,
		SatLeisureFit:   50,
		SatCommute:      50,
	}
}

// mustFinance builds an empty FinanceAPI.
func mustFinance(t *testing.T) *finance.FinanceAPI {
	t.Helper()
	return finance.NewFinanceAPI("firms-test-finance")
}

// mustCitizensAmbitious seeds n citizens with a deterministic, non-zero
// ambition spread (i*37 % 101) so founding actually occurs under a
// probabilistic config — the determinism/concurrency tests need real
// founders, not an all-zero-ambition population (SEC-102's degenerate-test
// fix).
func mustCitizensAmbitious(t *testing.T, n int) *citizens.CitizensAPI {
	t.Helper()
	recs := make([]citizens.ColdRecord, 0, n)
	for i := 1; i <= n; i++ {
		amb := int32((i * 37) % 101) // 0..100, deterministic spread
		recs = append(recs, citizenRecord(uint64(i), amb, citizens.SectorTertiary, 0))
	}
	return mustCitizens(t, recs)
}

// seedDeposits credits the household-wealth account (an external inflow),
// raising the bank's deposit pool by amount micro-pounds.
func seedDeposits(t *testing.T, fin *finance.FinanceAPI, amount int64) {
	t.Helper()
	if _, err := fin.Post(finance.Transaction{
		Description: "seed household deposits",
		Entries: []finance.Entry{
			{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: finance.Money(amount)},
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(amount)},
		},
	}); err != nil {
		t.Fatalf("seedDeposits: %v", err)
	}
}

// drainDeposits removes amount from the household-wealth account (an
// external outflow), lowering the bank's deposit pool.
func drainDeposits(t *testing.T, fin *finance.FinanceAPI, amount int64) {
	t.Helper()
	if _, err := fin.Post(finance.Transaction{
		Description: "drain household deposits",
		Entries: []finance.Entry{
			{Account: finance.AcctHouseholds, Side: finance.SideDebit, Amount: finance.Money(amount)},
			{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: finance.Money(amount)},
		},
	}); err != nil {
		t.Fatalf("drainDeposits: %v", err)
	}
}

// hasCode reports whether err is a *errs.E with the given registry code
// (errors.Is matches *errs.E by Code — see errs.E.Is).
func hasCode(err error, code string) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, &errs.E{Code: code})
}
