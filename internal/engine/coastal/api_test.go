package coastal

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/news"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// TestNoPlayerTriggerArrival (AC-2): arrival events appear over simulated
// months from the Advance path alone — there is no other way to produce one.
func TestNoPlayerTriggerArrival(t *testing.T) {
	cfg := testConfig()
	cfg.BaseArrivalRate = 1.0 // one arrival per month, guaranteed (whole part 1)
	api := mustAPI(t, cfg, newFakeShore(oneCell))

	for m := int64(0); m < 6; m++ {
		if _, err := api.Advance(m); err != nil {
			t.Fatalf("Advance(%d): %v", m, err)
		}
	}
	if got := api.ArrivalCount(); got != 6 {
		t.Fatalf("expected 6 arrivals from 6 advances, got %d", got)
	}
	for _, ev := range api.Arrivals() {
		if ev.Month < 0 || ev.Size < 1 {
			t.Fatalf("arrival event out of domain: %+v", ev)
		}
	}
}

// TestArrivalScheduledOnlyViaAdvance (AC-2): the exported CoastalAPI surface
// carries no Create/Trigger/AddArrival-shaped command — a lazy-but-plausible
// exported AddArrivalEvent(n) "for testing" would be caught here.
func TestArrivalScheduledOnlyViaAdvance(t *testing.T) {
	typ := reflect.TypeOf((*CoastalAPI)(nil))
	for i := 0; i < typ.NumMethod(); i++ {
		name := strings.ToLower(typ.Method(i).Name)
		for _, forbidden := range []string{"create", "trigger", "addarrival", "spawnarrival"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("CoastalAPI exports a direct-arrival-creation entry point %q (AC-2 violation)", typ.Method(i).Name)
			}
		}
	}
}

// TestSeasonScaledFrequency (AC-3): holding era fixed, varying the season
// multiplier changes the arrival rate over a fixed number of months.
func TestSeasonScaledFrequency(t *testing.T) {
	count := func(seasonMult float64) int {
		cfg := testConfig()
		cfg.BaseArrivalRate = 1.0
		for i := range cfg.SeasonMultipliers {
			cfg.SeasonMultipliers[i] = seasonMult
		}
		api := mustAPI(t, cfg, newFakeShore(oneCell))
		for m := int64(0); m < 4; m++ {
			if _, err := api.Advance(m); err != nil {
				t.Fatalf("Advance: %v", err)
			}
		}
		return api.ArrivalCount()
	}
	low := count(0.0)
	high := count(10.0)
	if low == high {
		t.Fatalf("season multiplier 0.0 and 10.0 produced identical arrival counts (%d) — frequency did not respond to season", low)
	}
}

// TestEraScaledFrequency (AC-3): raising the era/milestone tier changes the
// arrival rate.
func TestEraScaledFrequency(t *testing.T) {
	countFor := func(mult float64) int {
		cfg := testConfig()
		cfg.BaseArrivalRate = 1.0
		for i := range cfg.EraMultipliers {
			cfg.EraMultipliers[i] = mult
		}
		api := mustAPI(t, cfg, newFakeShore(oneCell))
		for m := int64(0); m < 4; m++ {
			if _, err := api.Advance(m); err != nil {
				t.Fatalf("Advance: %v", err)
			}
		}
		return api.ArrivalCount()
	}
	low := countFor(0.0)
	high := countFor(5.0)
	if low == high {
		t.Fatalf("era multiplier 0.0 and 5.0 produced identical arrival counts (%d) — frequency did not respond to era", low)
	}
}

// TestRescueCapacityShortfall (AC-4): a batch of simultaneous arrivals
// exceeding the coastguard/lifeboat capacity records a shortfall on at least
// one event's rescue outcome; a fully-resourced batch records none.
func TestRescueCapacityShortfall(t *testing.T) {
	cfg := testConfig()
	cfg.BaseArrivalRate = 5.0 // 5 arrivals guaranteed (whole part 5)
	cfg.MaxBoatSize = 10      // each boat carries at least 1, so total >= 5

	svc := services.New("corr-test")
	registerRescueServices(t, svc, cfg.Rescue.CoastguardServiceID, cfg.Rescue.LifeboatServiceID, 1, 1) // total capacity 2 < 5

	api := mustAPI(t, cfg, newFakeShore(oneCell))
	if err := api.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if _, err := api.Advance(0); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	shortfalls := 0
	for _, ev := range api.Arrivals() {
		if ev.Rescue.CapacityShortfall {
			shortfalls++
			if ev.Rescue.ShortfallPeople <= 0 {
				t.Fatalf("shortfall recorded with non-positive magnitude: %+v", ev.Rescue)
			}
		}
	}
	if shortfalls == 0 {
		t.Fatalf("batch of 5+ arrivals against rescue capacity 2 recorded no shortfall")
	}

	// Fully-resourced: capacity far above the batch -> no shortfall.
	svc2 := services.New("corr-test")
	registerRescueServices(t, svc2, cfg.Rescue.CoastguardServiceID, cfg.Rescue.LifeboatServiceID, 1000, 1000)
	api2 := mustAPI(t, cfg, newFakeShore(oneCell))
	if err := api2.SetServices(svc2); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if _, err := api2.Advance(0); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	for _, ev := range api2.Arrivals() {
		if ev.Rescue.CapacityShortfall {
			t.Fatalf("fully-resourced rescue recorded a shortfall: %+v", ev.Rescue)
		}
	}
}

// TestCaseworkerOverflowHotelsAndFriction (AC-5): forcing caseload above the
// caseworker-throughput ceiling records a non-zero hotel-requisition cost and
// a satisfaction-friction signal, and ONLY in the overflow case.
func TestCaseworkerOverflowHotelsAndFriction(t *testing.T) {
	overflowCfg := testConfig()
	overflowCfg.BaseArrivalRate = 5.0
	overflowCfg.MaxBoatSize = 10
	overflowCfg.Reception.CaseworkerThroughputPerMonth = 2 // ceiling far below caseload

	api := mustAPI(t, overflowCfg, newFakeShore(oneCell))
	if _, err := api.Advance(0); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if api.HotelCost() <= 0 {
		t.Fatalf("overflow caseworker caseload recorded no hotel-requisition cost (got %d)", api.HotelCost())
	}
	if api.SatisfactionFriction() <= 0 {
		t.Fatalf("overflow caseworker caseload recorded no satisfaction friction (got %v)", api.SatisfactionFriction())
	}

	// No-overflow control: throughput far above caseload -> zero cost+friction.
	okCfg := testConfig()
	okCfg.BaseArrivalRate = 1.0
	okCfg.MaxBoatSize = 1
	okCfg.Reception.CaseworkerThroughputPerMonth = 100
	ok := mustAPI(t, okCfg, newFakeShore(oneCell))
	if _, err := ok.Advance(0); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if ok.HotelCost() != 0 || ok.SatisfactionFriction() != 0 {
		t.Fatalf("no-overflow case recorded cost/friction: hotel=%d friction=%v", ok.HotelCost(), ok.SatisfactionFriction())
	}
}

// TestHotelRequisitionCost (AC-5/AC-10 placeholder): the hotel-requisition
// cost is the overflow count times the data cost-per-case, non-zero when
// there is overflow.
func TestHotelRequisitionCost(t *testing.T) {
	cfg := testConfig()
	cfg.BaseArrivalRate = 3.0
	cfg.MaxBoatSize = 10
	cfg.Reception.CaseworkerThroughputPerMonth = 1
	cfg.Reception.HotelCostPerCase = 777
	api := mustAPI(t, cfg, newFakeShore(oneCell))
	if _, err := api.Advance(0); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if api.HotelCost() <= 0 {
		t.Fatalf("expected a non-zero hotel cost with overflow, got %d", api.HotelCost())
	}
}

// TestGrantedBecomesCitizen (AC-6): a granted case, at the end of its
// pipeline, creates a full citizen record through engine.citizens — the
// population count actually increases, it does not merely flip a local flag.
func TestGrantedBecomesCitizen(t *testing.T) {
	cit, err := citizens.NewCitizensAPI(7, "corr-test")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	cfg := testConfig()
	cfg.BaseArrivalRate = 1.0 // 1 arrival, 1 person
	cfg.MaxBoatSize = 1
	cfg.Pipeline.GrantRate = 1.0 // always granted
	cfg.Pipeline.MinMonths = 1
	cfg.Pipeline.MaxMonths = 1 // duration exactly 1 month

	api := mustAPI(t, cfg, newFakeShore(oneCell))
	if err := api.SetCitizens(cit); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}

	before := cit.TotalPopulation("corr-test")
	if _, err := api.Advance(0); err != nil {
		t.Fatalf("Advance(0): %v", err)
	}
	if _, err := api.Advance(1); err != nil {
		t.Fatalf("Advance(1): %v", err)
	}
	after := cit.TotalPopulation("corr-test")
	if after != before+1 {
		t.Fatalf("granted case did not increase citizen population: before=%d after=%d", before, after)
	}

	// The first case's terminal stage is Granted and carries a citizen ID.
	stage, err := api.CaseStage(CaseID(1))
	if err != nil {
		t.Fatalf("CaseStage: %v", err)
	}
	if stage != CaseGranted {
		t.Fatalf("expected granted stage, got %v", stage)
	}
	k, err := api.Case(CaseID(1))
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if k.CitizenID == 0 {
		t.Fatalf("granted case carries no citizen ID (a bare flag flip)")
	}
}

// TestWorldProfileSkills (AC-6): the granted citizen's skills (education
// attainment) are drawn from the configured world profile, so two different
// profiles produce different skill values — not a hardcoded uniform default.
func TestWorldProfileSkills(t *testing.T) {
	grant := func(mean int32) int32 {
		cit, err := citizens.NewCitizensAPI(7, "corr-test")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		cfg := testConfig()
		cfg.BaseArrivalRate = 1.0
		cfg.MaxBoatSize = 1
		cfg.Pipeline.GrantRate = 1.0
		cfg.Pipeline.MinMonths = 1
		cfg.Pipeline.MaxMonths = 1
		cfg.WorldProfile.AttainmentMean = mean
		cfg.WorldProfile.AttainmentSpread = 0

		api := mustAPI(t, cfg, newFakeShore(oneCell))
		if err := api.SetCitizens(cit); err != nil {
			t.Fatalf("SetCitizens: %v", err)
		}
		if _, err := api.Advance(0); err != nil {
			t.Fatalf("Advance(0): %v", err)
		}
		if _, err := api.Advance(1); err != nil {
			t.Fatalf("Advance(1): %v", err)
		}
		k, err := api.Case(CaseID(1))
		if err != nil {
			t.Fatalf("Case: %v", err)
		}
		rec, ok := cit.CitizenAt(k.CitizenID, "corr-test")
		if !ok {
			t.Fatalf("granted citizen %d not found in citizens", k.CitizenID)
		}
		return rec.Education.Attainment
	}
	low := grant(10)
	high := grant(80)
	if low == high {
		t.Fatalf("world-profile skills not configurable: both profiles produced attainment %d", low)
	}
}

// TestNotGrantedDeparture (AC-7): a not-granted case records a non-zero
// managed-departure cost and its terminal state is queryable, not deleted.
func TestNotGrantedDeparture(t *testing.T) {
	cit, err := citizens.NewCitizensAPI(7, "corr-test")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	cfg := testConfig()
	cfg.BaseArrivalRate = 1.0
	cfg.MaxBoatSize = 1
	cfg.Pipeline.GrantRate = 0.0 // always not-granted
	cfg.Pipeline.MinMonths = 1
	cfg.Pipeline.MaxMonths = 1
	cfg.Pipeline.DepartureCostPerCase = 500

	api := mustAPI(t, cfg, newFakeShore(oneCell))
	if err := api.SetCitizens(cit); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	if _, err := api.Advance(0); err != nil {
		t.Fatalf("Advance(0): %v", err)
	}
	if _, err := api.Advance(1); err != nil {
		t.Fatalf("Advance(1): %v", err)
	}
	if api.DepartureCost() <= 0 {
		t.Fatalf("not-granted case recorded no departure cost (got %d)", api.DepartureCost())
	}
	// Terminal state queryable (AC-7), not deleted.
	stage, err := api.CaseStage(CaseID(1))
	if err != nil {
		t.Fatalf("CaseStage (terminal) : %v", err)
	}
	if stage != CaseNotGranted {
		t.Fatalf("expected not-granted terminal stage, got %v", stage)
	}
	k, err := api.Case(CaseID(1))
	if err != nil {
		t.Fatalf("Case (terminal): %v", err)
	}
	if k.DepartureCost != 500 {
		t.Fatalf("expected departure cost 500 on the terminal case, got %d", k.DepartureCost)
	}
}

// TestPolicyTradeOff (AC-11): raising processing funding reduces the backlog
// while increasing a cost metric — the opposite-direction pairing, in the
// same comparison.
func TestPolicyTradeOff(t *testing.T) {
	run := func(funding float64) (backlog int64, opex int64) {
		cfg := testConfig()
		cfg.BaseArrivalRate = 10.0
		cfg.MaxBoatSize = 1 // 10 arrivals, 10 cases per month
		cfg.Reception.CaseworkerThroughputPerMonth = 5
		cfg.Policy.ProcessingFundingThroughputGainPerUnit = 1.0
		cfg.Policy.ProcessingFundingOpexPerUnitPerMonth = 1000

		api := mustAPI(t, cfg, newFakeShore(oneCell))
		if err := api.SetProcessingFunding(funding); err != nil {
			t.Fatalf("SetProcessingFunding: %v", err)
		}
		for m := int64(0); m < 6; m++ {
			if _, err := api.Advance(m); err != nil {
				t.Fatalf("Advance: %v", err)
			}
		}
		return api.Backlog(), api.ProcessingOpex()
	}
	lowBacklog, lowOpex := run(0.0)
	highBacklog, highOpex := run(1.0)
	if highBacklog >= lowBacklog {
		t.Fatalf("raising processing funding did not reduce backlog: low=%d high=%d", lowBacklog, highBacklog)
	}
	if highOpex <= lowOpex {
		t.Fatalf("raising processing funding did not increase cost: low=%d high=%d", lowOpex, highOpex)
	}
}

// TestPolicyTradeOffHousingApproach (AC-11): raising the housing-approach
// slider toward concentrated centres lowers the hotel cost while raising the
// satisfaction friction — two metrics moving in opposite directions.
func TestPolicyTradeOffHousingApproach(t *testing.T) {
	run := func(approach float64) (hotel int64, friction float64) {
		cfg := testConfig()
		cfg.BaseArrivalRate = 3.0
		cfg.MaxBoatSize = 10
		cfg.Reception.CaseworkerThroughputPerMonth = 1 // overflow guaranteed
		cfg.Reception.HotelCostPerCase = 1000
		cfg.Policy.HousingApproachCostPerUnitPerMonth = -400 // centres cheaper
		cfg.Policy.HousingApproachFrictionIncreasePerUnit = 0.5

		api := mustAPI(t, cfg, newFakeShore(oneCell))
		if err := api.SetHousingApproach(approach); err != nil {
			t.Fatalf("SetHousingApproach: %v", err)
		}
		if _, err := api.Advance(0); err != nil {
			t.Fatalf("Advance: %v", err)
		}
		return api.HotelCost(), api.SatisfactionFriction()
	}
	dispersalHotel, dispersalFriction := run(0.0)
	centresHotel, centresFriction := run(1.0)
	if centresHotel >= dispersalHotel {
		t.Fatalf("concentrated centres did not lower hotel cost: dispersal=%d centres=%d", dispersalHotel, centresHotel)
	}
	if centresFriction <= dispersalFriction {
		t.Fatalf("concentrated centres did not raise friction: dispersal=%v centres=%v", dispersalFriction, centresFriction)
	}
}

// TestPolicyTradeOffIntegrationInvestment (AC-11): raising integration
// investment raises integration speed (shorter pipeline durations) while
// raising integration opex — opposite directions.
func TestPolicyTradeOffIntegrationInvestment(t *testing.T) {
	resolveMonth := func(investment float64) int64 {
		cit, err := citizens.NewCitizensAPI(7, "corr-test")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		cfg := testConfig()
		cfg.BaseArrivalRate = 1.0
		cfg.MaxBoatSize = 1
		cfg.Pipeline.GrantRate = 1.0
		cfg.Pipeline.MinMonths = 3
		cfg.Pipeline.MaxMonths = 3
		cfg.Pipeline.MaxReductionMonths = 2
		cfg.Policy.IntegrationInvestmentGainPerUnit = 1.0

		api := mustAPI(t, cfg, newFakeShore(oneCell))
		if err := api.SetCitizens(cit); err != nil {
			t.Fatalf("SetCitizens: %v", err)
		}
		if err := api.SetIntegrationInvestment(investment); err != nil {
			t.Fatalf("SetIntegrationInvestment: %v", err)
		}
		if _, err := api.Advance(0); err != nil {
			t.Fatalf("Advance: %v", err)
		}
		k, err := api.Case(CaseID(1))
		if err != nil {
			t.Fatalf("Case: %v", err)
		}
		return k.ResolveMonth
	}
	slow := resolveMonth(0.0)
	fast := resolveMonth(1.0)
	if fast >= slow {
		t.Fatalf("integration investment did not shorten the pipeline: investment0 resolveMonth=%d investment1 resolveMonth=%d", slow, fast)
	}
}

// TestNewsReporting (AC-12): an arrival event pushes a factual, non-subjective
// record through the wired engine.news edge carrying the case count.
func TestNewsReporting(t *testing.T) {
	nws, err := news.New("corr-test")
	if err != nil {
		t.Fatalf("news.New: %v", err)
	}
	cfg := testConfig()
	cfg.BaseArrivalRate = 1.0
	cfg.MaxBoatSize = 1

	api := mustAPI(t, cfg, newFakeShore(oneCell))
	if err := api.SetNews(nws); err != nil {
		t.Fatalf("SetNews: %v", err)
	}
	if _, err := api.Advance(0); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	found := false
	for _, st := range nws.Archive() {
		if strings.Contains(st.Text, "small boat") {
			found = true
			// The factual case-count figure is carried in the prose, with no
			// sentiment/spin tag (news.Story has no such field — AC-12).
			if !strings.Contains(st.Text, "1 people") {
				t.Fatalf("arrival news omitted the case-count figure: %q", st.Text)
			}
		}
	}
	if !found {
		t.Fatalf("no factual arrival record reached engine.news")
	}
}

// TestUnknownCase (AC-13): querying an unregistered case ID returns a
// registry-sourced error, never a fabricated zero-value record.
func TestUnknownCase(t *testing.T) {
	api := mustAPI(t, testConfig(), newFakeShore(oneCell))
	if _, err := api.CaseStage(CaseID(999999)); err == nil {
		t.Fatalf("expected ErrUnknownCase for unregistered case, got nil")
	} else {
		assertRegistryCode(t, err, ErrUnknownCase)
	}
	if _, err := api.Case(CaseID(999999)); err == nil {
		t.Fatalf("expected ErrUnknownCase for unregistered case (Case), got nil")
	} else {
		assertRegistryCode(t, err, ErrUnknownCase)
	}
}

// TestInvalidPolicyRange (AC-13): setting a policy coefficient outside its
// documented range returns a registry-sourced error and does NOT silently
// clamp the value.
func TestInvalidPolicyRange(t *testing.T) {
	api := mustAPI(t, testConfig(), newFakeShore(oneCell))
	if err := api.SetProcessingFunding(1.5); err == nil {
		t.Fatalf("expected ErrInvalidPolicyRange for funding 1.5, got nil")
	} else {
		assertRegistryCode(t, err, ErrInvalidPolicyRange)
	}
	// The coefficient must not have been silently clamped to a legal value.
	if got := api.ProcessingFunding(); got != testConfig().Policy.ProcessingFundingDefault {
		t.Fatalf("policy coefficient was mutated by a rejected set: got %v", got)
	}
	if err := api.SetHousingApproach(-0.1); err == nil {
		t.Fatalf("expected ErrInvalidPolicyRange for approach -0.1, got nil")
	} else {
		assertRegistryCode(t, err, ErrInvalidPolicyRange)
	}
	// Non-finite is a distinct error, before the range check.
	if err := api.SetIntegrationInvestment(math.NaN()); err == nil {
		t.Fatalf("expected ErrNonFinite for NaN investment, got nil")
	} else {
		assertRegistryCode(t, err, ErrNonFinite)
	}
}

// TestInvalidShoreCell (AC-14): an arrival scheduled against a cell the shore
// source does not classify as shore returns a registry-sourced error and no
// event is silently placed on an arbitrary cell.
func TestInvalidShoreCell(t *testing.T) {
	cfg := testConfig()
	cfg.BaseArrivalRate = 1.0
	cfg.MaxBoatSize = 1

	// The source lists a cell but its classifier rejects it (a stale/malformed
	// shore source — the world-model disagreement AC-14 guards against).
	bad := fakeShore{cells: []CellCoord{{X: 9, Y: 9}}, set: map[CellCoord]bool{}}
	api := mustAPI(t, cfg, bad)

	if _, err := api.Advance(0); err == nil {
		t.Fatalf("expected ErrInvalidShoreCell for a non-shore cell, got nil")
	} else {
		assertRegistryCode(t, err, ErrInvalidShoreCell)
	}
	if api.ArrivalCount() != 0 {
		t.Fatalf("an arrival was silently placed on a non-shore cell: count=%d", api.ArrivalCount())
	}
}
