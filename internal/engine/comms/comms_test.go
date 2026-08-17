package comms

import (
	"errors"
	"math"
	"sync"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/firms"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func newTestComms(t *testing.T) *CommsAPI {
	t.Helper()
	c, err := LoadDefault("comms-test")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	return c
}

func newWiredComms(t *testing.T) (*CommsAPI, *firms.FirmsAPI, *logistics.LogisticsAPI) {
	t.Helper()
	c := newTestComms(t)
	f, err := firms.LoadDefault(1, "comms-test-firms")
	if err != nil {
		t.Fatalf("firms.LoadDefault: %v", err)
	}
	l, err := logistics.LoadDefault("comms-test-logistics")
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if err := c.SetFirms(f); err != nil {
		t.Fatalf("SetFirms: %v", err)
	}
	if err := c.SetLogistics(l); err != nil {
		t.Fatalf("SetLogistics: %v", err)
	}
	return c, f, l
}

func wireGate(t *testing.T, c *CommsAPI) {
	t.Helper()
	if err := c.SetMilestoneGate(MilestoneGateFunc(func(tier int) bool { return true })); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}
}

func advanceTo(t *testing.T, c *CommsAPI, target Era) {
	t.Helper()
	for e := c.Era() + 1; e <= target; e++ {
		if err := c.AdvanceEra(e); err != nil {
			t.Fatalf("AdvanceEra(%v): %v", e, err)
		}
	}
}

func assertErrCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != code {
		t.Fatalf("expected error code %s, got %s", code, e.Code)
	}
}

func closeEnough(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}

// TestEraProgressNoSkip covers AC-2: the six-era ladder is monotonic and
// one-step-at-a-time; a single advance may not skip an intermediate era.
func TestEraProgressNoSkip(t *testing.T) {
	c := newTestComms(t)
	wireGate(t, c)

	// The six eras are an ordered enum (0..5).
	if EraTelephoneExchange != 0 || EraDialUp != 1 || EraBroadbandHub != 2 ||
		EraFibreBackbone != 3 || EraCellular != 4 || EraSubmarineCable != 5 {
		t.Fatalf("era enum must be the ordered six-rung ladder, got %v %v %v %v %v %v",
			EraTelephoneExchange, EraDialUp, EraBroadbandHub, EraFibreBackbone, EraCellular, EraSubmarineCable)
	}

	// Reaching the current era is an idempotent no-op.
	if err := c.AdvanceEra(EraTelephoneExchange); err != nil {
		t.Fatalf("AdvanceEra(current) should be a no-op, got %v", err)
	}

	// Advancing from telephone exchange directly to submarine cable (skipping
	// dial-up, broadband, fibre and cellular) must be rejected.
	if err := c.AdvanceEra(EraSubmarineCable); err == nil {
		t.Fatal("AdvanceEra(submarine-cable) from telephone-exchange must be rejected (era skip)")
	} else {
		assertErrCode(t, err, ErrEraSkip)
	}
	if got := c.Era(); got != EraTelephoneExchange {
		t.Fatalf("era must not change after a rejected skip, got %v", got)
	}

	// One step at a time works.
	if err := c.AdvanceEra(EraDialUp); err != nil {
		t.Fatalf("AdvanceEra(dial-up): %v", err)
	}
	if got := c.Era(); got != EraDialUp {
		t.Fatalf("era = %v, want dial-up", got)
	}
}

// TestEraAdvanceFailsClosedWithoutGate covers AC-4's fail-closed shape: an
// era whose milestone tier is > 0 cannot be reached with no milestone gate
// wired.
func TestEraAdvanceFailsClosedWithoutGate(t *testing.T) {
	c := newTestComms(t)
	if err := c.AdvanceEra(EraDialUp); err == nil {
		t.Fatal("AdvanceEra must fail closed with no milestone gate wired")
	} else {
		assertErrCode(t, err, ErrNotUnlocked)
	}
	if got := c.Era(); got != EraTelephoneExchange {
		t.Fatalf("era must not change on a fail-closed advance, got %v", got)
	}
}

// TestCapabilityGateIndependence covers AC-3: the four gates are four
// independent era→value mappings, not a single blended scalar. Advancing one
// era, at least two gates change by DIFFERENT amounts.
func TestCapabilityGateIndependence(t *testing.T) {
	c := newTestComms(t)
	wireGate(t, c)

	officeBefore, err := c.OfficeTierCeiling(EraTelephoneExchange)
	if err != nil {
		t.Fatalf("OfficeTierCeiling(exchange): %v", err)
	}
	researchBefore, err := c.ResearchRateModifier(EraTelephoneExchange)
	if err != nil {
		t.Fatalf("ResearchRateModifier(exchange): %v", err)
	}
	dcBefore, err := c.DataCentreEligible(EraTelephoneExchange)
	if err != nil {
		t.Fatalf("DataCentreEligible(exchange): %v", err)
	}
	rwBefore, err := c.RemoteWorkBaseCoefficient(EraTelephoneExchange)
	if err != nil {
		t.Fatalf("RemoteWorkBaseCoefficient(exchange): %v", err)
	}

	if err := c.AdvanceEra(EraDialUp); err != nil {
		t.Fatalf("AdvanceEra(dial-up): %v", err)
	}

	officeAfter, _ := c.OfficeTierCeiling(EraDialUp)
	researchAfter, _ := c.ResearchRateModifier(EraDialUp)
	dcAfter, _ := c.DataCentreEligible(EraDialUp)
	rwAfter, _ := c.RemoteWorkBaseCoefficient(EraDialUp)

	// The office ceiling does not move across exchange→dial-up (delta 0), while
	// the research modifier rises continuously — two gates, two different
	// amounts. A lazy single-scalar implementation would move all four in
	// lockstep and fail this assertion.
	if officeAfter != officeBefore {
		t.Errorf("office-tier ceiling moved %d→%d across exchange→dial-up (this gate is flat here)", officeBefore, officeAfter)
	}
	if researchAfter <= researchBefore {
		t.Errorf("research-rate modifier must rise across exchange→dial-up, got %v→%v", researchBefore, researchAfter)
	}
	if dcAfter != dcBefore {
		t.Errorf("data-centre eligibility must stay flat across exchange→dial-up (it steps at fibre), got %v→%v", dcBefore, dcAfter)
	}
	if rwAfter <= rwBefore {
		t.Errorf("remote-work base must rise across exchange→dial-up, got %v→%v", rwBefore, rwAfter)
	}

	// The data-centre gate is a STEP function: it flips true at fibre while the
	// research modifier rises continuously — the two never move in lockstep.
	fibreDC, _ := c.DataCentreEligible(EraFibreBackbone)
	fibreResearch, _ := c.ResearchRateModifier(EraFibreBackbone)
	if !fibreDC {
		t.Errorf("data-centre eligibility must be true at fibre, got %v", fibreDC)
	}
	if !closeEnough(fibreResearch, 1.6) {
		t.Errorf("research-rate modifier at fibre = %v, want 1.6 (data/comms.json)", fibreResearch)
	}
}

// TestRemoteWorkShareSectorAware covers AC-4: remote-work share is a function
// of era AND sector — two sectors with different documented affinities
// produce different shares at the SAME era.
func TestRemoteWorkShareSectorAware(t *testing.T) {
	c := newTestComms(t)

	tertiary, err := c.RemoteWorkShare(EraFibreBackbone, SectorTertiary)
	if err != nil {
		t.Fatalf("RemoteWorkShare(fibre, tertiary): %v", err)
	}
	primary, err := c.RemoteWorkShare(EraFibreBackbone, SectorPrimary)
	if err != nil {
		t.Fatalf("RemoteWorkShare(fibre, primary): %v", err)
	}
	if !(tertiary > primary) {
		t.Errorf("tertiary remote-work share (%v) must exceed primary (%v) at the same era", tertiary, primary)
	}
	// The exact values come from data/comms.json (0.25 base × 0.7 / × 0.1).
	if !closeEnough(tertiary, 0.25*0.7) || !closeEnough(primary, 0.25*0.1) {
		t.Errorf("remote-work shares (%v,%v) must equal base×affinity (0.175, 0.025)", tertiary, primary)
	}

	// A higher era raises the base for the same sector.
	cableTertiary, _ := c.RemoteWorkShare(EraSubmarineCable, SectorTertiary)
	if cableTertiary <= tertiary {
		t.Errorf("submarine-cable tertiary share (%v) must exceed fibre's (%v)", cableTertiary, tertiary)
	}

	// Out-of-domain inputs are rejected.
	if _, err := c.RemoteWorkShare(Era(99), SectorTertiary); err == nil {
		t.Fatal("RemoteWorkShare with an invalid era must error")
	} else {
		assertErrCode(t, err, ErrInvalidEra)
	}
	if _, err := c.RemoteWorkShare(EraFibreBackbone, Sector(99)); err == nil {
		t.Fatal("RemoteWorkShare with an invalid sector must error")
	} else {
		assertErrCode(t, err, ErrUnknownSector)
	}
}

// TestLetterDeclineAndParcelGrowth covers AC-5: letter volume and parcel
// volume are two independent series — letters decline as era advances, and
// parcels grow with e-commerce share while letters stay flat.
func TestLetterDeclineAndParcelGrowth(t *testing.T) {
	c, _, _ := newWiredComms(t)
	wireGate(t, c)
	if err := c.SetWealth(1.0); err != nil {
		t.Fatalf("SetWealth: %v", err)
	}

	letterAtExchange := c.LetterVolume()

	advanceTo(t, c, EraBroadbandHub)

	letterAtBroadband := c.LetterVolume()
	if letterAtBroadband >= letterAtExchange {
		t.Errorf("letter volume must decline as era advances, got %v → %v", letterAtExchange, letterAtBroadband)
	}

	// Era and wealth are now held FIXED. Raising e-commerce share (by lifting
	// the infrastructure cap) must raise parcel volume but leave letter volume
	// untouched.
	letterBefore := c.LetterVolume()
	parcelBefore := c.ParcelVolume()

	if _, err := c.RegisterFulfilmentCentre(); err != nil {
		t.Fatalf("RegisterFulfilmentCentre: %v", err)
	}
	if _, err := c.RegisterLastMileDepot("north"); err != nil {
		t.Fatalf("RegisterLastMileDepot: %v", err)
	}

	letterAfter := c.LetterVolume()
	parcelAfter := c.ParcelVolume()

	if !closeEnough(letterAfter, letterBefore) {
		t.Errorf("letter volume must not rise with e-commerce share, got %v → %v", letterBefore, letterAfter)
	}
	if parcelAfter <= parcelBefore {
		t.Errorf("parcel volume must rise with e-commerce share (era/wealth fixed), got %v → %v", parcelBefore, parcelAfter)
	}
}

// TestECommerceShareFulfilmentRequired covers AC-6: the e-commerce share
// cannot exceed the no-infrastructure floor while no fulfilment centre and
// last-mile depot are operational, and can exceed it once both exist.
func TestECommerceShareFulfilmentRequired(t *testing.T) {
	c, _, _ := newWiredComms(t)
	wireGate(t, c)
	if err := c.SetWealth(1.0); err != nil {
		t.Fatalf("SetWealth: %v", err)
	}
	advanceTo(t, c, EraBroadbandHub)

	floor := c.cfg.eCommerce.NoInfrastructureFloor
	raw := c.ECommerceRawShare()
	if raw <= floor {
		t.Fatalf("precondition: raw share %v must exceed the floor %v (data/comms.json)", raw, floor)
	}

	// No infrastructure: the effective share is capped at the floor.
	if got := c.ECommerceShare(); !closeEnough(got, floor) {
		t.Errorf("ECommerceShare without infrastructure = %v, want floor %v", got, floor)
	}

	// Fulfilment centre alone is not enough — the share stays capped until a
	// last-mile depot is also operational (§35 requires both).
	if _, err := c.RegisterFulfilmentCentre(); err != nil {
		t.Fatalf("RegisterFulfilmentCentre: %v", err)
	}
	if got := c.ECommerceShare(); !closeEnough(got, floor) {
		t.Errorf("ECommerceShare with a fulfilment centre but no depot = %v, want floor %v", got, floor)
	}

	// With both, the share can exceed the floor.
	if _, err := c.RegisterLastMileDepot("north"); err != nil {
		t.Fatalf("RegisterLastMileDepot: %v", err)
	}
	if got := c.ECommerceShare(); got <= floor || !closeEnough(got, raw) {
		t.Errorf("ECommerceShare with full infrastructure = %v, want > floor and == raw %v", got, raw)
	}
}

// TestFulfilmentFirmRegistered covers AC-7: the fulfilment centre is
// registered as a REAL firm through engine.firms (thousands of jobs), not a
// comms-owned pseudo-employer.
func TestFulfilmentFirmRegistered(t *testing.T) {
	c, f, _ := newWiredComms(t)

	fc, err := c.RegisterFulfilmentCentre()
	if err != nil {
		t.Fatalf("RegisterFulfilmentCentre: %v", err)
	}
	// "thousands of jobs" — data/comms.json's fulfilment.staff = 2500.
	if fc.Staff < 1000 {
		t.Errorf("fulfilment-centre staff %d must be in the documented thousands range", fc.Staff)
	}

	// The firm is queryable through engine.firms' FirmsAPI.
	firm, err := f.Firm(firms.FirmID(fc.FirmID))
	if err != nil {
		t.Fatalf("firms.Firm(%d): %v", fc.FirmID, err)
	}
	// The jobs figure landed in the real firm record (as its input requirement,
	// the placeholder headroom engine.firms carries until the labour-pool hires).
	if firm.InputRequired != fc.Staff {
		t.Errorf("registered firm's input requirement %d must equal the fulfilment-centre staff %d", firm.InputRequired, fc.Staff)
	}
}

// TestDeliveryMovementLastMile covers AC-8: delivery trips are real
// engine.logistics movements — a saturated last-mile route returns the same
// shortfall (deferred, next-day) semantics logistics defines.
func TestDeliveryMovementLastMile(t *testing.T) {
	c, _, _ := newWiredComms(t)
	if _, err := c.RegisterFulfilmentCentre(); err != nil {
		t.Fatalf("RegisterFulfilmentCentre: %v", err)
	}
	if _, err := c.RegisterLastMileDepot("north"); err != nil {
		t.Fatalf("RegisterLastMileDepot: %v", err)
	}

	// A small delivery fits within the depot shelf: no shortfall.
	small, err := c.DeliverParcels("north", 2000)
	if err != nil {
		t.Fatalf("DeliverParcels(small): %v", err)
	}
	if small.Shortfall != 0 {
		t.Errorf("small delivery shortfall = %d, want 0", small.Shortfall)
	}

	// A saturated request exceeds the shelf: the excess is deferred (shortfall).
	sat, err := c.DeliverParcels("north", 15000)
	if err != nil {
		t.Fatalf("DeliverParcels(saturated): %v", err)
	}
	if sat.Shortfall <= 0 {
		t.Errorf("saturated delivery shortfall = %d, want > 0 (deferred next-day load)", sat.Shortfall)
	}

	// A fresh API with no fulfilment centre cannot resolve a delivery.
	bare := newTestComms(t)
	l, err := logistics.LoadDefault("comms-test-logistics2")
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if err := bare.SetLogistics(l); err != nil {
		t.Fatalf("SetLogistics: %v", err)
	}
	if _, err := bare.DeliverParcels("north", 10); err == nil {
		t.Fatal("DeliverParcels without a fulfilment centre must error")
	} else {
		assertErrCode(t, err, ErrFulfilmentNotRegistered)
	}

	// And a negative parcel count is rejected.
	if _, err := c.DeliverParcels("north", -1); err == nil {
		t.Fatal("DeliverParcels with a negative count must error")
	} else {
		assertErrCode(t, err, ErrOutOfRange)
	}
}

// TestHighStreetDrainCounterplay covers AC-9: the raw drain is a function of
// e-commerce share; the counterplay offset lowers the NET drain but never to
// zero while e-commerce share is nonzero.
func TestHighStreetDrainCounterplay(t *testing.T) {
	c, _, _ := newWiredComms(t)
	wireGate(t, c)
	if err := c.SetWealth(1.0); err != nil {
		t.Fatalf("SetWealth: %v", err)
	}
	advanceTo(t, c, EraBroadbandHub)
	if _, err := c.RegisterFulfilmentCentre(); err != nil {
		t.Fatalf("RegisterFulfilmentCentre: %v", err)
	}
	if _, err := c.RegisterLastMileDepot("north"); err != nil {
		t.Fatalf("RegisterLastMileDepot: %v", err)
	}

	raw := c.HighStreetDrain()
	if raw <= 0 {
		t.Fatalf("raw drain must be positive with nonzero e-commerce share, got %v", raw)
	}

	// With no counterplay, net == raw.
	if net := c.NetHighStreetDrain(); !closeEnough(net, raw) {
		t.Errorf("net drain with zero counterplay = %v, want raw %v", net, raw)
	}

	// Maximum counterplay dampens but never zeroes.
	if err := c.SetCounterplayOffset(1.0); err != nil {
		t.Fatalf("SetCounterplayOffset: %v", err)
	}
	net := c.NetHighStreetDrain()
	if net >= raw {
		t.Errorf("net drain %v must be lower than raw %v after counterplay", net, raw)
	}
	if net <= 0 {
		t.Errorf("net drain must never reach zero while e-commerce share is nonzero, got %v", net)
	}
}

// TestInvalidEraRejectsWithoutStateMutation covers AC-10's first half: an
// out-of-enum era query/advance returns a registry-sourced error and creates
// no zero-value era state.
func TestInvalidEraRejectsWithoutStateMutation(t *testing.T) {
	c := newTestComms(t)
	before := c.Era()

	if _, err := c.OfficeTierCeiling(Era(99)); err == nil {
		t.Fatal("OfficeTierCeiling(Era(99)) must error")
	} else {
		assertErrCode(t, err, ErrInvalidEra)
	}
	if _, err := c.DataCentreEligible(Era(99)); err == nil {
		t.Fatal("DataCentreEligible(Era(99)) must error")
	} else {
		assertErrCode(t, err, ErrInvalidEra)
	}
	if err := c.AdvanceEra(Era(99)); err == nil {
		t.Fatal("AdvanceEra(Era(99)) must error")
	} else {
		assertErrCode(t, err, ErrInvalidEra)
	}

	if got := c.Era(); got != before {
		t.Fatalf("era state changed after invalid-era rejections: %v → %v", before, got)
	}
}

// TestNoFirmRefRejectsWithoutStateMutation covers AC-10's second half: a
// fulfilment-centre registration with no firm reference is rejected and no
// fulfilment-centre record is created.
func TestNoFirmRefRejectsWithoutStateMutation(t *testing.T) {
	c := newTestComms(t)

	if _, err := c.RegisterFulfilmentCentre(); err == nil {
		t.Fatal("RegisterFulfilmentCentre without engine.firms wired must error")
	} else {
		assertErrCode(t, err, ErrNoFirmRef)
	}

	if _, err := c.FulfilmentCentre(); err == nil {
		t.Fatal("FulfilmentCentre after a rejected registration must report not-registered")
	} else {
		assertErrCode(t, err, ErrFulfilmentNotRegistered)
	}
}

// TestOutOfRangeInputsRejected covers AC-11: share/wealth/counterplay inputs
// above 100% or below 0%, and negative letter/parcel volumes, are rejected
// with a typed error — never silently clamped.
func TestOutOfRangeInputsRejected(t *testing.T) {
	c := newTestComms(t)

	if err := c.SetWealth(1.5); err == nil {
		t.Fatal("SetWealth(1.5) (above 100%) must error")
	} else {
		assertErrCode(t, err, ErrOutOfRange)
	}
	if err := c.SetWealth(-0.1); err == nil {
		t.Fatal("SetWealth(-0.1) (below 0%) must error")
	} else {
		assertErrCode(t, err, ErrOutOfRange)
	}
	if err := c.SetCounterplayOffset(2.0); err == nil {
		t.Fatal("SetCounterplayOffset(2.0) must error")
	} else {
		assertErrCode(t, err, ErrOutOfRange)
	}
	if err := c.SetPostalVolumes(-1, 0); err == nil {
		t.Fatal("SetPostalVolumes(-1, 0) must error")
	} else {
		assertErrCode(t, err, ErrOutOfRange)
	}
	if err := c.SetPostalVolumes(0, -1); err == nil {
		t.Fatal("SetPostalVolumes(0, -1) must error")
	} else {
		assertErrCode(t, err, ErrOutOfRange)
	}
	if err := c.SetWealth(math.NaN()); err == nil {
		t.Fatal("SetWealth(NaN) must error")
	} else {
		assertErrCode(t, err, ErrNonFinite)
	}

	// Rejections never silently clamp: a subsequent valid input still works and
	// the API remains usable.
	if err := c.SetWealth(0.5); err != nil {
		t.Fatalf("SetWealth(0.5) after rejections: %v", err)
	}
}

// TestDeterminism covers AC-12: era-gate resolution, e-commerce share and
// remote-work share are deterministic functions of prior state + commands —
// two APIs built from identical data and driven with identical commands are
// byte-identical across all query surfaces.
func TestDeterminism(t *testing.T) {
	run := func() (*CommsAPI, map[string]float64) {
		c, _, _ := newWiredComms(t)
		wireGate(t, c)
		_ = c.SetWealth(0.7)
		_ = c.SetCounterplayOffset(0.3)
		advanceTo(t, c, EraFibreBackbone)
		_, _ = c.RegisterFulfilmentCentre()
		_, _ = c.RegisterLastMileDepot("north")

		off, _ := c.OfficeTierCeiling(EraFibreBackbone)
		research, _ := c.ResearchRateModifier(EraFibreBackbone)
		rwBase, _ := c.RemoteWorkBaseCoefficient(EraFibreBackbone)
		rwTert, _ := c.RemoteWorkShare(EraFibreBackbone, SectorTertiary)

		return c, map[string]float64{
			"officeCeiling": float64(off),
			"research":      research,
			"rwBase":        rwBase,
			"rwTertiary":    rwTert,
			"rawShare":      c.ECommerceRawShare(),
			"share":         c.ECommerceShare(),
			"letters":       c.LetterVolume(),
			"parcels":       c.ParcelVolume(),
			"drain":         c.HighStreetDrain(),
			"netDrain":      c.NetHighStreetDrain(),
		}
	}

	a, aVals := run()
	b, bVals := run()
	if a.Era() != b.Era() {
		t.Fatalf("era diverged: %v vs %v", a.Era(), b.Era())
	}
	for k, av := range aVals {
		if bv, ok := bVals[k]; !ok || bv != av {
			t.Errorf("query %s diverged across identical runs: %v vs %v", k, av, bv)
		}
	}
}

// TestConcurrentAccessIsRaceFree covers AC-14: the API is safe for concurrent
// use (verified under -race), with a concurrent hammer of read and write
// calls across the exported surface.
func TestConcurrentAccessIsRaceFree(t *testing.T) {
	c, _, _ := newWiredComms(t)
	wireGate(t, c)
	if err := c.SetWealth(0.5); err != nil {
		t.Fatalf("SetWealth: %v", err)
	}
	if err := c.SetCounterplayOffset(0.2); err != nil {
		t.Fatalf("SetCounterplayOffset: %v", err)
	}
	advanceTo(t, c, EraBroadbandHub)
	if _, err := c.RegisterFulfilmentCentre(); err != nil {
		t.Fatalf("RegisterFulfilmentCentre: %v", err)
	}
	if _, err := c.RegisterLastMileDepot("north"); err != nil {
		t.Fatalf("RegisterLastMileDepot: %v", err)
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				_ = c.Era()
				_, _ = c.OfficeTierCeiling(EraFibreBackbone)
				_, _ = c.RemoteWorkShare(EraFibreBackbone, SectorTertiary)
				_ = c.ECommerceShare()
				_ = c.LetterVolume()
				_ = c.ParcelVolume()
				_ = c.HighStreetDrain()
				_ = c.NetHighStreetDrain()
				_ = c.SetWealth(0.5)
				_ = c.SetCounterplayOffset(0.3)
			}
		}()
	}
	wg.Wait()

	if got := c.Era(); got != EraBroadbandHub {
		t.Fatalf("era drifted after concurrent access: %v", got)
	}
	if got := c.LastMileDepotCount(); got != 1 {
		t.Fatalf("depot count drifted after concurrent access: %d", got)
	}
}

// TestPostalServicesFundingReliability covers US-5: the post-and-parcel
// infrastructure draws from engine.services' generic funding→quality pool, so
// a funding cut degrades delivery reliability like any other service.
func TestPostalServicesFundingReliability(t *testing.T) {
	c := newTestComms(t)
	s := services.New("comms-test-services")
	if err := c.SetServices(s); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if err := c.RegisterPostalServices(); err != nil {
		t.Fatalf("RegisterPostalServices: %v", err)
	}

	sortingID := services.ServiceID(c.cfg.postal.SortingOffice.ID)
	hubID := services.ServiceID(c.cfg.postal.ParcelHub.ID)

	if err := s.SetFunding(sortingID, 1.0); err != nil {
		t.Fatalf("SetFunding(sorting): %v", err)
	}
	if err := s.SetFunding(hubID, 1.0); err != nil {
		t.Fatalf("SetFunding(hub): %v", err)
	}
	full, err := c.PostalDeliveryReliability()
	if err != nil {
		t.Fatalf("PostalDeliveryReliability(full funding): %v", err)
	}
	if !closeEnough(full, 1.0) {
		t.Errorf("full-funding reliability = %v, want 1.0", full)
	}

	// A funding cut to the parcel hub degrades delivery reliability.
	if err := s.SetFunding(hubID, 0.0); err != nil {
		t.Fatalf("SetFunding(hub, 0): %v", err)
	}
	cut, err := c.PostalDeliveryReliability()
	if err != nil {
		t.Fatalf("PostalDeliveryReliability(cut): %v", err)
	}
	if cut >= full {
		t.Errorf("postal reliability must degrade with a funding cut, got %v → %v", full, cut)
	}
}

// TestCopiedValueRejected covers the SEC-020 copy guard: a method call on a
// struct-copied *CommsAPI is rejected.
func TestCopiedValueRejected(t *testing.T) {
	c := newTestComms(t)
	cp := commsCopy(c)
	if err := cp.AdvanceEra(EraDialUp); err == nil {
		t.Fatal("AdvanceEra on a struct copy must error")
	} else {
		assertErrCode(t, err, ErrCopiedValue)
	}
}

// commsCopy takes a same-package value copy of *CommsAPI via a byte-copy that
// go vet's copylocks check does not statically recognise as a lock copy. A
// plain `cp := *c` produces the identical attack shape but trips copylocks
// (CommsAPI contains sync.RWMutex) and would fail this package's own baseline.
// Mirrors engine.services' servicesCopy / engine.world's w2Copy convention.
func commsCopy(c *CommsAPI) *CommsAPI {
	out := new(CommsAPI)
	*(*[unsafe.Sizeof(CommsAPI{})]byte)(unsafe.Pointer(out)) = *(*[unsafe.Sizeof(CommsAPI{})]byte)(unsafe.Pointer(c))
	return out
}
