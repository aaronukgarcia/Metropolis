package crime_test

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/engine/prison"
)

// TestJusticeChainConservation is AC-12's full conservation identity (§28
// justice-chain — pipeline of identifiable people): every month, per
// district, the three crime-side identities hold exactly (arrest → charge →
// trial → sentence, each a genuine partition of the prior stage's own log),
// AND crime's sentenced-to-prison figure is cross-checked against a REAL
// engine.prison instance's independently-computed intake ledger.
//
// This is an external (package crime_test) test — deliberately, not a
// white-box file in package crime — because it needs to import
// engine.prison, and engine.prison itself imports engine.crime (for
// DistrictID and to satisfy crime.PrisonIntake); a white-box crime test
// importing prison would be a genuine import cycle. Other packages split
// cross-engine composition tests the same way (attract/s6_endtoend_test.go
// is package attract_test for the identical reason).
//
// Before ASM-1053, this test's prison cross-check used a hand-rolled fake
// ledger fed DIRECTLY from crime's own toPrison count
// (ledger.record(m, toPrison)), then asserted crime's count matched the
// fake — a tautology that could never fail, because the "independent"
// ledger's value WAS the value under test. engine.prison (MOD-056) did not
// exist yet at the time. It exists now, so this test wires the REAL
// *prison.PrisonAPI as crime's PrisonIntake seam (crime.PrisonIntake needs
// only IntakeCount(district, month) int64, which *prison.PrisonAPI
// implements directly) and drives it through prison's own public Admit API
// — a bug in EITHER module's own accounting (crime's identities, or
// prison's real intake counting) now fails this test.
func TestJusticeChainConservation(t *testing.T) {
	a, err := crime.New(42, "crime-ac12")
	if err != nil {
		t.Fatalf("crime.New: %v", err)
	}
	p, err := prison.LoadDefault("prison-ac12")
	if err != nil {
		t.Fatalf("prison.LoadDefault: %v", err)
	}
	// Every synthesised offender ID this test admits is treated as a real
	// citizen (AC-10's existence gate is not this test's concern — Admit's
	// own validation/ledgering behaviour is).
	if err := p.SetCitizenExists(func(uint64) bool { return true }); err != nil {
		t.Fatalf("SetCitizenExists: %v", err)
	}
	if err := a.SetPrisonIntake(p); err != nil {
		t.Fatalf("SetPrisonIntake: %v", err)
	}

	// Drive arrests: high active crime + clearance, same fixture the
	// original AC-12 test used.
	d := crime.DistrictInput{
		District:                 1,
		OwnDeprivation:           0.5,
		NeighbourWealth:          0.5,
		YouthUnemployment:        0.1,
		Blight:                   0.1,
		YouthLeisureDesert:       0.2,
		PolicePresence:           0.5,
		EraWealth:                0.3,
		PortThroughput:           0.2,
		CustomsFunding:           0.2,
		PatrolCoverage:           5,
		DetectiveCapacity:        20, // clearance ceiling
		PreventionInfrastructure: 0.3,
		EligiblePool:             100000,
		CourthouseThroughput:     5,
	}

	var nextCitizenID uint64 = 1
	sawPrisonAdmission := false

	for m := int64(0); m < 6; m++ {
		if err := a.AdvanceMonth(m, []crime.DistrictInput{d}, crime.SecurityInput{}); err != nil {
			t.Fatalf("AdvanceMonth(%d): %v", m, err)
		}
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

		// Prison cross-check: independently admit exactly toPrison FRESH
		// citizens into the REAL prison instance for this (district, month)
		// via prison's own public Admit — this is standing in for the
		// composition root's per-offender sentencing bridge, which does not
		// exist yet (crime exposes no per-offender ID accessor, only
		// aggregate counts — see the honest gap noted in this task's report).
		// Each Admission is independently validated and ledgered by
		// PrisonAPI.Admit's real logic (citizen-existence check, re-admission
		// rejection, intake-count increment keyed by (district, month)): a
		// bug in prison's OWN intake counting fails VerifyPrisonIntake below
		// exactly as it would in production, because IntakeCount reads
		// prison's real internal ledger, not a value this test set directly.
		for i := int64(0); i < toPrison; i++ {
			id := nextCitizenID
			nextCitizenID++
			if err := p.Admit(prison.Admission{
				CitizenID:      id,
				District:       1,
				Month:          m,
				Offence:        prison.OffenceMinor,
				SentenceMonths: 12,
			}); err != nil {
				t.Fatalf("month %d Admit(citizen=%d): %v", m, id, err)
			}
			sawPrisonAdmission = true
		}

		ok, err := a.VerifyPrisonIntake(1, m)
		if err != nil {
			t.Fatalf("VerifyPrisonIntake(%d): %v", m, err)
		}
		if !ok {
			t.Fatalf("month %d prison cross-check failed: crime toPrison=%d, prison IntakeCount=%d", m, toPrison, p.IntakeCount(1, m))
		}
	}

	// A quiet run that never sentences anyone to prison would make the
	// cross-check above vacuously true every month (0 == 0) and mask a
	// dropped Admit/IntakeCount wiring entirely — assert the fixture
	// actually drove at least one real admission through prison's API, so
	// this test's cross-check is known to be live.
	if !sawPrisonAdmission {
		t.Fatal("fixture drove zero prison admissions over 6 months — the cross-check never actually exercised prison.Admit/IntakeCount")
	}
}
