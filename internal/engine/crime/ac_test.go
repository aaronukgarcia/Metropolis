package crime

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// AC-1 (GR#20; code.json inbound contract): per-type generation is
// independently queryable and each of deterrence/clearance/prevention is
// drill-through inspectable; changing one driver changes only the terms the
// spec ties it to, not every crime type uniformly.
func TestGenerationDrillThroughTermIsolation(t *testing.T) {
	a := testAPI(t)

	low := defaultDistrict(1)
	low.EraWealth = 0.2
	high := defaultDistrict(2)
	high.EraWealth = 0.9
	// Identical except eraWealth.
	advance(t, a, 0, low, high)

	// eraWealth is tied ONLY to fraud/cyber (§28 "fraud/cyber grows with era
	// & wealth"). Every other type must be unchanged.
	for _, ty := range crimeTypeKeys {
		gLow, err := a.Generation(1, ty)
		if err != nil {
			t.Fatalf("Generation(1, %s): %v", ty, err)
		}
		gHigh, err := a.Generation(2, ty)
		if err != nil {
			t.Fatalf("Generation(2, %s): %v", ty, err)
		}
		if ty == CrimeFraudCyber {
			if gHigh <= gLow {
				t.Fatalf("fraud/cyber must rise with eraWealth: low=%v high=%v", gLow, gHigh)
			}
			continue
		}
		if !almostEqual(gLow, gHigh) {
			t.Fatalf("%s moved with eraWealth (low=%v high=%v) — a driver changed a type it is not tied to", ty, gLow, gHigh)
		}
	}
}

// AC-2 (§28 nine crime types, decomposed): raising port throughput alone
// moves only the smuggling figure; the other eight remain unchanged.
func TestCrimeTypeSmugglingIsolation(t *testing.T) {
	a := testAPI(t)

	base := defaultDistrict(1)
	base.PortThroughput = 0.2
	raised := defaultDistrict(2)
	raised.PortThroughput = 0.9
	advance(t, a, 0, base, raised)

	for _, ty := range crimeTypeKeys {
		gBase, _ := a.Generation(1, ty)
		gRaised, _ := a.Generation(2, ty)
		if ty == CrimeSmuggling {
			if gRaised <= gBase {
				t.Fatalf("smuggling must rise with port throughput: base=%v raised=%v", gBase, gRaised)
			}
			continue
		}
		if !almostEqual(gBase, gRaised) {
			t.Fatalf("%s moved with port throughput (base=%v raised=%v) — smuggling is the only port-driven type", ty, gBase, gRaised)
		}
	}
}

// AC-3 (§28 inequality between ADJACENT districts): the inequality driver is
// a genuine neighbour comparison — identical own-deprivation, different
// neighbour wealth must produce different generation for the inequality-tied
// types, and leave the deprivation-only types unchanged.
func TestInequalityDriverNeighbourComparison(t *testing.T) {
	a := testAPI(t)

	rich := defaultDistrict(1)
	rich.OwnDeprivation = 0.6
	rich.NeighbourWealth = 0.9 // rich neighbours → large gap
	poor := defaultDistrict(2)
	poor.OwnDeprivation = 0.6
	poor.NeighbourWealth = 0.4 // similar neighbours → smaller gap
	advance(t, a, 0, rich, poor)

	// burglary and violent respond to inequality → must differ.
	for _, ty := range []CrimeType{CrimeBurglary, CrimeViolent} {
		gRich, _ := a.Generation(1, ty)
		gPoor, _ := a.Generation(2, ty)
		if almostEqual(gRich, gPoor) {
			t.Fatalf("%s should differ under identical own-deprivation but different neighbour wealth (rich=%v poor=%v)", ty, gRich, gPoor)
		}
	}
	// petty theft responds to deprivation (same in both) but NOT inequality
	// → must be unchanged, proving the driver is a neighbour comparison, not
	// a synonym for own poverty.
	gRich, _ := a.PettyTheft(1)
	gPoor, _ := a.PettyTheft(2)
	if !almostEqual(gRich, gPoor) {
		t.Fatalf("petty theft moved under identical own-deprivation (rich=%v poor=%v) — inequality must not be read as own deprivation", gRich, gPoor)
	}
}

// AC-4 (§28 concave deterrence): marginal crime-reduction-per-patrol-unit
// strictly decreases as patrol coverage rises (three-point check).
func TestConcaveDeterrenceMarginalReturn(t *testing.T) {
	hs := 5.0
	c1, c2, c3 := 1.0, 5.0, 20.0
	d1 := DeterrenceFor(c1, hs)
	d2 := DeterrenceFor(c2, hs)
	d3 := DeterrenceFor(c3, hs)

	m1 := d2 - d1 // marginal return from c1→c2
	m2 := d3 - d2 // marginal return from c2→c3
	if m1 <= 0 || m2 <= 0 {
		t.Fatalf("deterrence must rise with coverage: %v %v %v", d1, d2, d3)
	}
	if secondDiff := m2 - m1; secondDiff >= 0 {
		t.Fatalf("deterrence must be concave (diminishing marginal returns): second difference %v", secondDiff)
	}
}

// AC-5 (§28 clearance vs prevention — two mechanisms, independently movable):
// raising detective capacity (holding prevention fixed) moves clearance, not
// generation; raising prevention infrastructure (holding detective capacity
// fixed) moves prevention, not clearance.
func TestClearanceIsolation(t *testing.T) {
	low := testAPI(t)
	high := testAPI(t)

	base := defaultDistrict(1)
	for m := int64(0); m < 2; m++ {
		lowD := base
		lowD.DetectiveCapacity = 2
		highD := base
		highD.DetectiveCapacity = 20
		advance(t, low, m, lowD)
		advance(t, high, m, highD)
	}

	cLow, _ := low.Clearance(1)
	cHigh, _ := high.Clearance(1)
	if cHigh <= cLow {
		t.Fatalf("clearance must rise with detective capacity: low=%v high=%v", cLow, cHigh)
	}
	// The driver-attributable generation term is unchanged (detectives do not
	// touch generation), and prevention is unchanged.
	for _, ty := range crimeTypeKeys {
		gLow, _ := low.Generation(1, ty)
		gHigh, _ := high.Generation(1, ty)
		if !almostEqual(gLow, gHigh) {
			t.Fatalf("%s generation moved with detective capacity (low=%v high=%v) — clearance must not touch generation", ty, gLow, gHigh)
		}
	}
	pLow, _ := low.Prevention(1)
	pHigh, _ := high.Prevention(1)
	if !almostEqual(pLow, pHigh) {
		t.Fatalf("prevention moved with detective capacity: low=%v high=%v", pLow, pHigh)
	}
	// And the persistence/recurrence metric drops (clearance suppresses
	// persistence).
	for _, ty := range crimeTypeKeys {
		rLow, _ := low.Recurrence(1, ty)
		rHigh, _ := high.Recurrence(1, ty)
		if rHigh >= rLow {
			t.Fatalf("%s recurrence did not drop with clearance: low=%v high=%v", ty, rLow, rHigh)
		}
	}
}

func TestPreventionIsolation(t *testing.T) {
	low := testAPI(t)
	high := testAPI(t)

	base := defaultDistrict(1)
	for m := int64(0); m < 2; m++ {
		lowD := base
		lowD.PreventionInfrastructure = 0.1
		highD := base
		highD.PreventionInfrastructure = 0.8
		advance(t, low, m, lowD)
		advance(t, high, m, highD)
	}

	pLow, _ := low.Prevention(1)
	pHigh, _ := high.Prevention(1)
	if pHigh <= pLow {
		t.Fatalf("prevention must rise with infrastructure: low=%v high=%v", pLow, pHigh)
	}
	// The driver-attributable generation term drops (prevention cuts new
	// generation), and clearance is unchanged.
	for _, ty := range crimeTypeKeys {
		gLow, _ := low.Generation(1, ty)
		gHigh, _ := high.Generation(1, ty)
		if gHigh >= gLow {
			t.Fatalf("%s generation did not drop with prevention: low=%v high=%v", ty, gLow, gHigh)
		}
	}
	cLow, _ := low.Clearance(1)
	cHigh, _ := high.Clearance(1)
	if !almostEqual(cLow, cHigh) {
		t.Fatalf("clearance moved with prevention: low=%v high=%v", cLow, cHigh)
	}
}

// AC-6 (§28 gang FORMATION — the persistence requirement): 23 sustained
// months then relax → no gang; a full 24+ months → a gang with a stable id.
func TestGangFormation23ThenRelax(t *testing.T) {
	a := testAPI(t)
	for m := int64(0); m < 23; m++ {
		advance(t, a, m, formationDistrict(1))
	}
	// Relax exactly one condition in month 24.
	relaxed := formationDistrict(1)
	relaxed.Blight = 0.0
	advance(t, a, 23, relaxed)

	if got := len(a.GangIDs()); got != 0 {
		t.Fatalf("expected no gang after 23 sustained months then a relax, got %d gang(s)", got)
	}
}

func TestGangFormationSustained(t *testing.T) {
	a, _ := formGang(t, 1)
	ids := a.GangIDs()
	if len(ids) != 1 {
		t.Fatalf("expected exactly 1 gang, got %d", len(ids))
	}
	g, ok := a.Gang(ids[0])
	if !ok {
		t.Fatalf("gang %d not queryable after formation", ids[0])
	}
	if g.Name == "" {
		t.Fatal("gang must be named")
	}
	if g.District != 1 {
		t.Fatalf("gang formed in wrong district: %d", g.District)
	}
}

// AC-7 (§28 gang effects — four distinct, independently isolable).
func TestGangTerritory(t *testing.T) {
	a, gid := formGang(t, 1)
	g, _ := a.Gang(gid)
	if len(g.Territory) == 0 {
		t.Fatal("gang must claim a queryable, non-empty territory")
	}
	// A second gang in another district claims a distinct territory.
	d2 := formationDistrict(2)
	for m := int64(24); m < 48; m++ {
		advance(t, a, m, d2)
	}
	ids := a.GangIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 gangs, got %d", len(ids))
	}
	var g2 Gang
	for _, id := range ids {
		if id != gid {
			g2, _ = a.Gang(id)
		}
	}
	if sameSet(g.Territory, g2.Territory) {
		t.Fatal("two gangs must not claim identical territory")
	}
}

func TestGangTax(t *testing.T) {
	a, gid := formGang(t, 1)
	// One more month so the tax/closure effects have been applied.
	advance(t, a, 24, formationDistrict(1))
	g, _ := a.Gang(gid)
	if g.TaxLevyMicroPounds <= 0 {
		t.Fatal("gang must levy a queryable positive business tax")
	}
	if g.BusinessClosures <= 0 {
		t.Fatal("gang tax must raise a queryable positive closure figure")
	}
}

func TestGangRecruit(t *testing.T) {
	a, gid := formGang(t, 1)
	before, _ := a.EligiblePool(1)
	advance(t, a, 24, formationDistrict(1))
	after, _ := a.EligiblePool(1)
	if after >= before {
		t.Fatalf("gang recruitment must reduce the eligible pool: before=%d after=%d", before, after)
	}
	g, _ := a.Gang(gid)
	if g.Recruited <= 0 {
		t.Fatal("gang must record a positive recruitment count")
	}
}

func TestGangCrimeUplift(t *testing.T) {
	a, _ := formGang(t, 1)
	// Advance the gang district AND a benign comparison district with the
	// same drivers except the gang is absent in district 2.
	d1 := formationDistrict(1)
	d2 := formationDistrict(2)
	// Ensure district 2 does not form a gang: break one condition.
	d2.Blight = 0.0
	advance(t, a, 24, d1, d2)

	for _, ty := range crimeTypeKeys {
		g1, _ := a.Generation(1, ty)
		g2, _ := a.Generation(2, ty)
		if g1 <= g2 {
			t.Fatalf("%s in the gang's district must exceed the gang-free district (gang=%v free=%v) — a gang raises EVERY local crime type", ty, g1, g2)
		}
	}
}

// AC-8 (§28 gang REMOVAL — the full-stack requirement): clearance pressure
// alone does not remove a gang; all four components together do.
func TestGangRemovalClearanceOnlySurvives(t *testing.T) {
	a, gid := formGang(t, 1)
	// Maximum clearance pressure only; the other three components stay at
	// pre-gang baseline (zero).
	d := formationDistrict(1)
	d.DetectiveCapacity = 100 // clearance saturates at its ceiling
	d.PrisonAbsorption = 0
	d.RegenerationInvestment = 0
	d.PreventionInfrastructure = 0
	for m := int64(24); m < 60; m++ {
		advance(t, a, m, d)
	}
	if _, ok := a.Gang(gid); !ok {
		t.Fatal("clearance pressure alone must NOT remove the gang (full stack required)")
	}
}

func TestGangFullStackRemoves(t *testing.T) {
	a, gid := formGang(t, 1)
	d := formationDistrict(1)
	d.DetectiveCapacity = 100
	d.PrisonAbsorption = 0.8
	d.RegenerationInvestment = 0.8
	d.PreventionInfrastructure = 0.8
	removed := false
	for m := int64(24); m < 40; m++ {
		advance(t, a, m, d)
		if _, ok := a.Gang(gid); !ok {
			removed = true
			break
		}
	}
	if !removed {
		t.Fatal("the full removal stack must remove the gang within a bounded number of months")
	}
}

// AC-9 (§28 decapitation-without-regeneration respawns — the asymmetry):
// decapitation without regeneration respawns a fresh entity; with concurrent
// regeneration investment it does not.
func TestDecapitationRespawn(t *testing.T) {
	a, oldID := formGang(t, 1)
	if err := a.Decapitate(oldID); err != nil {
		t.Fatalf("Decapitate: %v", err)
	}
	if len(a.GangIDs()) != 0 {
		t.Fatal("decapitation must delete the gang entity")
	}
	// Continue with the generative conditions unchanged (no regeneration) —
	// a NEW gang must re-form within the observation window.
	respawned := false
	var newID GangID
	for m := int64(24); m < 64; m++ {
		advance(t, a, m, formationDistrict(1))
		ids := a.GangIDs()
		if len(ids) == 1 {
			respawned = true
			newID = ids[0]
			break
		}
	}
	if !respawned {
		t.Fatal("decapitation without regeneration must respawn a fresh gang within the observation window")
	}
	if newID == oldID {
		t.Fatal("respawned gang must be a fresh entity (new id), not the same gang un-deleted")
	}
}

func TestRegenerationPreventsRespawn(t *testing.T) {
	a, oldID := formGang(t, 1)
	if err := a.Decapitate(oldID); err != nil {
		t.Fatalf("Decapitate: %v", err)
	}
	d := formationDistrict(1)
	d.RegenerationInvestment = 0.8 // concurrent regeneration investment
	for m := int64(24); m < 64; m++ {
		advance(t, a, m, d)
	}
	if got := len(a.GangIDs()); got != 0 {
		t.Fatalf("decapitation with regeneration must not respawn, got %d gang(s)", got)
	}
}

// AC-10 (§28 command ladder gates strategy sliders): the citywide mix is
// settable only once a Constabulary HQ is built, and shifting each weight
// moves its own mechanism.
func TestConstabularyGateStrategyMix(t *testing.T) {
	a := testAPI(t)

	// No HQ → rejection.
	err := a.SetStrategyMix(StrategyMix{Patrol: 0.8, Detective: 0.1, Community: 0.1})
	if !errors.Is(err, &errs.E{Code: ErrNoConstabularyHQ}) {
		t.Fatalf("expected ErrNoConstabularyHQ before HQ is built, got %v", err)
	}

	if err := a.BuildConstabularyHQ(); err != nil {
		t.Fatalf("BuildConstabularyHQ: %v", err)
	}
	if err := a.SetStrategyMix(StrategyMix{Patrol: 0.8, Detective: 0.1, Community: 0.1}); err != nil {
		t.Fatalf("SetStrategyMix after HQ: %v", err)
	}

	d := defaultDistrict(1)

	// Patrol-heavy mix.
	advance(t, a, 0, d)
	patrolDet, _ := a.Deterrence(1)
	patrolClear, _ := a.Clearance(1)
	patrolPrev, _ := a.Prevention(1)

	// Detective-heavy mix.
	if err := a.SetStrategyMix(StrategyMix{Patrol: 0.1, Detective: 0.8, Community: 0.1}); err != nil {
		t.Fatalf("SetStrategyMix detective-heavy: %v", err)
	}
	advance(t, a, 1, d)
	detClear, _ := a.Clearance(1)
	detDeter, _ := a.Deterrence(1)

	// Community-heavy mix.
	if err := a.SetStrategyMix(StrategyMix{Patrol: 0.1, Detective: 0.1, Community: 0.8}); err != nil {
		t.Fatalf("SetStrategyMix community-heavy: %v", err)
	}
	advance(t, a, 2, d)
	comPrev, _ := a.Prevention(1)

	// Each weight moves its own term: patrol→deterrence, detective→clearance,
	// community→prevention.
	if detDeter >= patrolDet {
		t.Fatalf("shifting patrol weight up must raise deterrence: patrol=%v detective=%v", patrolDet, detDeter)
	}
	if detClear <= patrolClear {
		t.Fatalf("shifting detective weight up must raise clearance: patrol=%v detective=%v", patrolClear, detClear)
	}
	if comPrev <= patrolPrev {
		t.Fatalf("shifting community weight up must raise prevention: patrol=%v community=%v", patrolPrev, comPrev)
	}
}

// AC-11 (§28 MI5-analogue threat dial — never random-spam): no event fires
// from a quiet baseline regardless of elapsed ticks, and any fired event is
// preceded by the documented lead window; raising funding lowers probability.
func TestThreatPrecursorNoRandomSpam(t *testing.T) {
	// (a) quiet baseline: no exposure, no activity → no event ever, no matter
	// how many months elapse.
	quiet := testAPI(t)
	for m := int64(0); m < 200; m++ {
		advanceSec(t, quiet, m, SecurityInput{Exposure: 0, Funding: 0, Liaison: 0})
	}
	if quiet.LastThreatEventMonth() != 0 {
		t.Fatalf("quiet baseline must never fire an event, fired at month %d", quiet.LastThreatEventMonth())
	}
	if quiet.ThreatLevel() != 0 {
		t.Fatalf("quiet baseline threat level must stay 0, got %v", quiet.ThreatLevel())
	}

	// (b) a threat-level rise precedes any fired event by at least the lead
	// window within the same run.
	exposed := testAPI(t)
	var firedMonth int64 = -1
	for m := int64(0); m < 400; m++ {
		advanceSec(t, exposed, m, SecurityInput{Exposure: 1.0, Funding: 0, Liaison: 0})
		if exposed.LastThreatEventMonth() != 0 {
			firedMonth = exposed.LastThreatEventMonth()
			break
		}
	}
	if firedMonth < 0 {
		t.Fatal("expected a threat event to fire under sustained high exposure")
	}
	if firedMonth < 6 {
		t.Fatalf("event fired at month %d before the 6-month lead window", firedMonth)
	}
}

func TestNoRandomSpamFundingDampensProbability(t *testing.T) {
	// Hold exposure fixed, raise funding → trigger probability strictly falls.
	pLow := TriggerProbabilityFor(0.8, 0.0, 0.0, testAPI(t).cfg.Threat)
	pHigh := TriggerProbabilityFor(0.8, 0.9, 0.0, testAPI(t).cfg.Threat)
	if pHigh >= pLow {
		t.Fatalf("raising Security Service funding must strictly reduce trigger probability: low=%v high=%v", pLow, pHigh)
	}
}

// AC-12 (§28 justice-chain conservation identity — pipeline of identifiable
// people): every month, per district, the three identities hold exactly, with
// every term independently sourced and the prison cross-check against an
// independent intake ledger.
func TestJusticeChainConservation(t *testing.T) {
	a := testAPI(t)
	// Drive arrests: high active crime + clearance.
	d := defaultDistrict(1)
	d.DetectiveCapacity = 20 // clearance ceiling
	d.CourthouseThroughput = 5
	// An independent prison intake ledger, keyed by (district, month) —
	// simulating engine.prison's own counting, fed ONLY from the emitted
	// sentence records (not from crime's aggregate).
	ledger := &fakePrisonIntake{counts: map[DistrictID]map[int64]int64{}}
	if err := a.SetPrisonIntake(ledger); err != nil {
		t.Fatalf("SetPrisonIntake: %v", err)
	}

	for m := int64(0); m < 6; m++ {
		advance(t, a, m, d)
		arrested, _ := a.OffendersArrested(1)
		charged, _ := a.OffendersCharged(1)
		releasedNC, _ := a.OffendersReleasedNoCharge(1)
		convicted, _ := a.OffendersConvicted(1)
		acquitted, _ := a.OffendersAcquitted(1)
		awaiting, _ := a.OffendersAwaitingTrial(1)
		toPrison, _ := a.OffendersSentencedToPrison(1)
		nonCustodial, _ := a.OffendersSentencedNonCustodial(1)

		// Identity 1: arrested == charged + releasedNoCharge.
		if arrested != charged+releasedNC {
			t.Fatalf("month %d identity 1 violated: arrested=%d charged=%d releasedNoCharge=%d", m, arrested, charged, releasedNC)
		}
		// Identity 2: charged == convicted + acquitted + awaitingTrial.
		if charged != convicted+acquitted+awaiting {
			t.Fatalf("month %d identity 2 violated: charged=%d convicted=%d acquitted=%d awaiting=%d", m, charged, convicted, acquitted, awaiting)
		}
		// Identity 3: convicted == sentencedToPrison + sentencedNonCustodial.
		if convicted != toPrison+nonCustodial {
			t.Fatalf("month %d identity 3 violated: convicted=%d prison=%d nonCustodial=%d", m, convicted, toPrison, nonCustodial)
		}

		// Prison cross-check: feed the prison ledger from crime's own sentence
		// records, then verify crime's aggregate matches the independent count.
		ledger.record(m, toPrison)
		ok, err := a.VerifyPrisonIntake(1, m)
		if err != nil {
			t.Fatalf("VerifyPrisonIntake(%d): %v", m, err)
		}
		if !ok {
			t.Fatalf("month %d prison cross-check failed: crime=%d intake=%d", m, toPrison, ledger.count(1, m))
		}
	}
}

// fakePrisonIntake is the test double for engine.prison's independent intake
// ledger (AC-12's cross-check seam).
type fakePrisonIntake struct {
	counts map[DistrictID]map[int64]int64
}

func (f *fakePrisonIntake) IntakeCount(d DistrictID, month int64) int64 {
	return f.count(d, month)
}

func (f *fakePrisonIntake) count(d DistrictID, month int64) int64 {
	if f.counts[d] == nil {
		return 0
	}
	return f.counts[d][month]
}

func (f *fakePrisonIntake) record(month, n int64) {
	if f.counts[1] == nil {
		f.counts[1] = map[int64]int64{}
	}
	f.counts[1][month] = n
}

// AC-13 (§28 courthouse backlog releases offenders): saturate throughput →
// backlog grows → a distinct released-on-backlog outcome appears → the
// effective clearance driver softens.
func TestBacklogRelease(t *testing.T) {
	a := testAPI(t)
	d := defaultDistrict(1)
	d.DetectiveCapacity = 100
	d.CourthouseThroughput = 0 // saturated: nothing is ever decided

	for m := int64(0); m < 4; m++ {
		advance(t, a, m, d)
	}

	backlog, _ := a.Backlog(1)
	if backlog <= 0 {
		t.Fatalf("saturated courthouse must accumulate a backlog, got %d", backlog)
	}
	released, _ := a.OffendersReleasedOnBacklog(1)
	if released <= 0 {
		t.Fatalf("backlog past threshold must release offenders, released=%d", released)
	}
	// The release must measurably soften the effective clearance driver.
	eff, _ := a.EffectiveClearance(1)
	if eff <= 0 {
		t.Fatalf("effective clearance after release must be positive, got %v", eff)
	}

	// Contrast: a non-saturated courthouse never releases.
	b := testAPI(t)
	okD := defaultDistrict(1)
	okD.CourthouseThroughput = 10000
	for m := int64(0); m < 4; m++ {
		advance(t, b, m, okD)
	}
	okReleased, _ := b.OffendersReleasedOnBacklog(1)
	if okReleased != 0 {
		t.Fatalf("non-saturated courthouse must not release, released=%d", okReleased)
	}
}

// AC-14 (GR#7): querying an unregistered district returns a registry-sourced
// error (no silently-created zero entry); a strategy command against an
// un-built HQ returns a registry-sourced error (no silently-dropped command).
func TestUnregisteredDistrict(t *testing.T) {
	a := testAPI(t)
	_, err := a.Generation(999, CrimePettyTheft)
	if !errors.Is(err, &errs.E{Code: ErrUnregisteredDistrict}) {
		t.Fatalf("expected ErrUnregisteredDistrict, got %v", err)
	}
	// No side effect: the district must NOT have been created.
	if _, err2 := a.PettyTheft(999); !errors.Is(err2, &errs.E{Code: ErrUnregisteredDistrict}) {
		t.Fatalf("second query must also reject the still-unregistered district, got %v", err2)
	}
}

func TestNoHQ(t *testing.T) {
	a := testAPI(t)
	err := a.SetStrategyMix(StrategyMix{Patrol: 0.5, Detective: 0.3, Community: 0.2})
	if !errors.Is(err, &errs.E{Code: ErrNoConstabularyHQ}) {
		t.Fatalf("expected ErrNoConstabularyHQ, got %v", err)
	}
}

// AC-15: a decapitation command against a nonexistent gang, and a mix whose
// weights do not sum to the documented total, are both rejected with a typed
// error — never silently ignored or renormalised.
func TestInvalidDecap(t *testing.T) {
	a := testAPI(t)
	if err := a.Decapitate(GangID(12345)); !errors.Is(err, &errs.E{Code: ErrInvalidDecapitation}) {
		t.Fatalf("expected ErrInvalidDecapitation, got %v", err)
	}
}

func TestInvalidMix(t *testing.T) {
	a := testAPI(t)
	if err := a.BuildConstabularyHQ(); err != nil {
		t.Fatalf("BuildConstabularyHQ: %v", err)
	}
	// Does not sum to the documented total (1.0).
	if err := a.SetStrategyMix(StrategyMix{Patrol: 0.9, Detective: 0.9, Community: 0.9}); !errors.Is(err, &errs.E{Code: ErrInvalidMix}) {
		t.Fatalf("expected ErrInvalidMix, got %v", err)
	}
}

// sameSet reports whether two uint64 slices hold the same elements.
func sameSet(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[uint64]int{}
	for _, v := range a {
		m[v]++
	}
	for _, v := range b {
		if m[v] == 0 {
			return false
		}
		m[v]--
	}
	return true
}
