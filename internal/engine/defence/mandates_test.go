package defence

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// TestMandate100k_Threshold anchors the naval mandate to the spec-literal
// 100,000 figure (AC-4): just below fires nothing, at the threshold the named
// naval mandate fires.
func TestMandate100k_Threshold(t *testing.T) {
	d := newDefence(t, validConfig(), 1)
	if hasMandate(d.PendingMandates(99_999), "naval-100k") {
		t.Fatal("naval mandate fired below 100,000")
	}
	if !hasMandate(d.PendingMandates(100_000), "naval-100k") {
		t.Fatal("naval mandate did not fire at 100,000")
	}
}

// TestMandate500k_Threshold anchors the army mandate to the spec-literal
// 500,000 figure (AC-4).
func TestMandate500k_Threshold(t *testing.T) {
	d := newDefence(t, validConfig(), 1)
	if hasMandate(d.PendingMandates(499_999), "army-500k") {
		t.Fatal("army mandate fired below 500,000")
	}
	if !hasMandate(d.PendingMandates(500_000), "army-500k") {
		t.Fatal("army mandate did not fire at 500,000")
	}
}

// TestMandate1M_Threshold anchors the air-defence mandate to the spec-literal
// 1,000,000 figure (AC-4).
func TestMandate1M_Threshold(t *testing.T) {
	d := newDefence(t, validConfig(), 1)
	if hasMandate(d.PendingMandates(999_999), "airdefence-1m") {
		t.Fatal("air-defence mandate fired below 1,000,000")
	}
	if !hasMandate(d.PendingMandates(1_000_000), "airdefence-1m") {
		t.Fatal("air-defence mandate did not fire at 1,000,000")
	}
}

// TestChoiceWithinCompliance_DrivesBuildAndCompensation submits two different
// valid choices in two separate scenario runs and asserts the built facility's
// type differs between runs while both receive the documented compensation
// (AC-5).
func TestChoiceWithinCompliance_DrivesBuildAndCompensation(t *testing.T) {
	compensation := finance.Money(validConfig().Mandates[0].CompensationMicropounds)

	// Run 1: full naval base at site A.
	d1, _, _ := newWiredDefence(t, 7)
	r1, err := d1.RespondToMandate(MandateResponse{
		MandateID: "naval-100k", Choice: "naval-base",
		Site:    SiteRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}},
		OwnerID: 1, Month: 0,
	})
	if err != nil {
		t.Fatalf("run1 RespondToMandate: %v", err)
	}
	if r1.Compensation != compensation {
		t.Fatalf("run1 compensation = %d, want %d", int64(r1.Compensation), int64(compensation))
	}
	f1, ok := d1.Facility(r1.FacilityID)
	if !ok {
		t.Fatalf("run1 facility %d not recorded", uint64(r1.FacilityID))
	}

	// Run 2: patrol berth at site B (distinct seed so personnel IDs cannot
	// collide with run 1's settlement).
	d2, _, _ := newWiredDefence(t, 8)
	r2, err := d2.RespondToMandate(MandateResponse{
		MandateID: "naval-100k", Choice: "naval-patrol-berth",
		Site:    SiteRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 1}},
		OwnerID: 1, Month: 0,
	})
	if err != nil {
		t.Fatalf("run2 RespondToMandate: %v", err)
	}
	if r2.Compensation != compensation {
		t.Fatalf("run2 compensation = %d, want %d", int64(r2.Compensation), int64(compensation))
	}
	f2, ok := d2.Facility(r2.FacilityID)
	if !ok {
		t.Fatalf("run2 facility %d not recorded", uint64(r2.FacilityID))
	}

	if f1.Type == f2.Type {
		t.Fatalf("two distinct choices produced the same facility type %q — the choice did not drive the build", f1.Type)
	}
	if f1.Site == f2.Site {
		t.Fatalf("two scenario runs recorded the same site %v — siting did not differ", f1.Site)
	}
}

// TestRefuseMandate_BlocksBuildAndGatesGrants asserts refusal is legal, does
// NOT force-build, and prices the refusal: reputation penalty nonzero and
// subsequent grant bids rejected with the refusal-specific code (AC-6).
func TestRefuseMandate_BlocksBuildAndGatesGrants(t *testing.T) {
	d, _, _ := newWiredDefence(t, 1)

	res, err := d.RespondToMandate(MandateResponse{MandateID: "naval-100k", Refuse: true, Month: 0})
	if err != nil {
		t.Fatalf("RespondToMandate(refuse): %v", err)
	}
	if !res.Refused {
		t.Fatal("refusal result not marked Refused")
	}

	// (a) No facility was force-built.
	if _, ok := d.Facility(1); ok {
		t.Fatal("a refusal force-built a facility")
	}
	if got := d.ReputationPenalty(); got != validConfig().Reputation.RefusalPenaltyPoints {
		t.Fatalf("ReputationPenalty() = %d, want %d", got, validConfig().Reputation.RefusalPenaltyPoints)
	}

	// (b) A subsequent grant bid is rejected specifically because a refusal is
	// in effect — distinct from an undeclared-pot / bad-bid rejection.
	_, err = d.BidForGrant(GrantBid{Pot: "transport", MatchFunding: 1_000_000, Month: 1})
	isErr(t, err, ErrGrantRefused)
}

// TestRefusalCost_ReputationAccumulates asserts the reputation penalty is a
// queryable, accumulating cost (AC-6): two refusals double the penalty.
func TestRefusalCost_ReputationAccumulates(t *testing.T) {
	d := newDefence(t, validConfig(), 1)
	penalty := int64(validConfig().Reputation.RefusalPenaltyPoints)
	if _, err := d.RespondToMandate(MandateResponse{MandateID: "naval-100k", Refuse: true, Month: 0}); err != nil {
		t.Fatalf("refuse 1: %v", err)
	}
	if _, err := d.RespondToMandate(MandateResponse{MandateID: "army-500k", Refuse: true, Month: 0}); err != nil {
		t.Fatalf("refuse 2: %v", err)
	}
	if got := d.ReputationPenalty(); got != 2*penalty {
		t.Fatalf("ReputationPenalty() = %d, want %d", got, 2*penalty)
	}
}

// TestDoubleMandateResponse_Rejected asserts a second response to an
// already-responded mandate is rejected with a typed error, never silently
// overwriting the first (AC-12).
func TestDoubleMandateResponse_Rejected(t *testing.T) {
	d, _, _ := newWiredDefence(t, 1)
	first := MandateResponse{
		MandateID: "naval-100k", Choice: "naval-base",
		Site:    SiteRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}},
		OwnerID: 1, Month: 0,
	}
	if _, err := d.RespondToMandate(first); err != nil {
		t.Fatalf("first response: %v", err)
	}
	if _, err := d.RespondToMandate(MandateResponse{MandateID: "naval-100k", Refuse: true, Month: 0}); err == nil {
		t.Fatal("second response to a responded mandate returned nil error")
	} else {
		isErr(t, err, ErrMandateAlreadyResponded)
	}
}

// TestIneligibleSite_Rejected asserts a facility-siting command against an
// out-of-bounds cell returns the registry-sourced ErrIneligibleSite (AC-11),
// and that no facility is recorded.
func TestIneligibleSite_Rejected(t *testing.T) {
	d, _, _ := newWiredDefence(t, 1)
	_, err := d.RespondToMandate(MandateResponse{
		MandateID: "naval-100k", Choice: "naval-base",
		Site:    SiteRef{Tile: world.TileCoord{X: 999, Y: 999}, Local: world.CellLocal{Row: 0, Col: 0}},
		OwnerID: 1, Month: 0,
	})
	isErr(t, err, ErrIneligibleSite)
	if _, ok := d.Facility(1); ok {
		t.Fatal("an ineligible site recorded a facility")
	}
}

// TestInvalidChoice_Rejected asserts a choice outside the mandate's compliant
// set is rejected with ErrInvalidChoice (AC-5's choice-within-compliance).
func TestInvalidChoice_Rejected(t *testing.T) {
	d, _, _ := newWiredDefence(t, 1)
	_, err := d.RespondToMandate(MandateResponse{
		MandateID: "naval-100k", Choice: "not-a-choice",
		Site:    SiteRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}},
		OwnerID: 1, Month: 0,
	})
	isErr(t, err, ErrInvalidChoice)
}

// TestRespondToMandate_MissingDependency_NoPartialState is the SEC-215
// regression: the round-1 attack wired build+finance but NOT citizens and
// observed RespondToMandate return ErrDependencyMissing only after it had
// already enqueued a build order, credited the compensation, and recorded the
// facility — so a retry after wiring citizens doubled the money and the build.
// The fix pre-flights the whole dependency surface, so the failed attempt must
// leave zero side effects and the retry must commit exactly once.
func TestRespondToMandate_MissingDependency_NoPartialState(t *testing.T) {
	d := newDefence(t, validConfig(), 1)
	f := finance.NewFinanceAPI("corr-defence")
	b := newBuild(t)
	if err := d.SetFinance(f); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	if err := d.SetBuild(b); err != nil {
		t.Fatalf("SetBuild: %v", err)
	}

	req := MandateResponse{
		MandateID: "naval-100k", Choice: "naval-base",
		Site:    SiteRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}},
		OwnerID: 1, Month: 0,
	}

	// Attempt with citizens unwired: must fail with ErrDependencyMissing.
	_, err := d.RespondToMandate(req)
	isErr(t, err, ErrDependencyMissing)

	// ...and must have committed NOTHING.
	if n := len(b.Queue()); n != 0 {
		t.Fatalf("failed response enqueued %d build order(s), want 0", n)
	}
	if m := f.TotalMoneyInCirculation(); m != 0 {
		t.Fatalf("failed response created %d money-in-circulation, want 0", int64(m))
	}
	if _, ok := d.Facility(1); ok {
		t.Fatal("failed response recorded a facility")
	}
	if !hasMandate(d.PendingMandates(100_000), "naval-100k") {
		t.Fatal("mandate no longer pending after a failed response")
	}

	// Wire citizens and retry: exactly one commit, exactly one compensation.
	c, err := citizens.NewCitizensAPI(1, "corr-defence")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := d.SetCitizens(c); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	r, err := d.RespondToMandate(req)
	if err != nil {
		t.Fatalf("retry RespondToMandate: %v", err)
	}
	comp := finance.Money(validConfig().Mandates[0].CompensationMicropounds)
	if m := f.TotalMoneyInCirculation(); m != comp {
		t.Fatalf("money-in-circulation after retry = %d, want exactly %d", int64(m), int64(comp))
	}
	if n := len(b.Queue()); n != 1 {
		t.Fatalf("retry enqueued %d build order(s), want 1", n)
	}
	if _, ok := d.Facility(r.FacilityID); !ok {
		t.Fatalf("retry did not record facility %d", uint64(r.FacilityID))
	}
	if hasMandate(d.PendingMandates(100_000), "naval-100k") {
		t.Fatal("mandate still pending after a successful retry")
	}
}

// TestRespondToMandate_MissingBuildAndFinance_NoPartialState covers the
// finding's second variant: only build wired. The pre-flight must fail on the
// first missing dependency (finance) without enqueuing any build order.
func TestRespondToMandate_MissingBuildAndFinance_NoPartialState(t *testing.T) {
	d := newDefence(t, validConfig(), 1)
	b := newBuild(t)
	if err := d.SetBuild(b); err != nil {
		t.Fatalf("SetBuild: %v", err)
	}

	req := MandateResponse{
		MandateID: "naval-100k", Choice: "naval-base",
		Site:    SiteRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}},
		OwnerID: 1, Month: 0,
	}
	_, err := d.RespondToMandate(req)
	isErr(t, err, ErrDependencyMissing)
	if n := len(b.Queue()); n != 0 {
		t.Fatalf("failed response enqueued %d build order(s), want 0", n)
	}
	if _, ok := d.Facility(1); ok {
		t.Fatal("failed response recorded a facility")
	}
	if !hasMandate(d.PendingMandates(100_000), "naval-100k") {
		t.Fatal("mandate no longer pending after a failed response")
	}
}
