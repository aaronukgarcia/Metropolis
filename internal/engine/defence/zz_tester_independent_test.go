package defence

// Tester independent reproduction of SEC-215 (round-1 finding). Throwaway —
// removed after the verification run. Does not rely on the junior's
// regression tests; drives the exact attack scenario with its own assertions.

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func errCode(err error) string {
	var e *errs.E
	if !errors.As(err, &e) {
		return "<non-registry>"
	}
	return e.Code
}

func testerReq() MandateResponse {
	return MandateResponse{
		MandateID: "naval-100k", Choice: "naval-base",
		Site:    SiteRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 0, Col: 0}},
		OwnerID: 1, Month: 0,
	}
}

// TestTesterIndependent_SEC215_MissingCitizens wires build+finance but NOT
// citizens, then retries after wiring citizens.
func TestTesterIndependent_SEC215_MissingCitizens(t *testing.T) {
	d := newDefence(t, validConfig(), 4242)
	f := finance.NewFinanceAPI("tester-sec215")
	b := newBuild(t)
	if err := d.SetFinance(f); err != nil {
		t.Fatal(err)
	}
	if err := d.SetBuild(b); err != nil {
		t.Fatal(err)
	}
	// citizens deliberately unwired.

	if _, err := d.RespondToMandate(testerReq()); err == nil {
		t.Fatal("expected error with citizens unwired, got nil")
	} else if got := errCode(err); got != ErrDependencyMissing {
		t.Fatalf("expected ErrDependencyMissing, got %s", got)
	}

	// Independent side-effect assertions on the failed attempt.
	if got := f.TotalMoneyInCirculation(); got != 0 {
		t.Fatalf("FAIL: failed response credited %d money-in-circulation, want 0", int64(got))
	}
	if n := len(b.Queue()); n != 0 {
		t.Fatalf("FAIL: failed response enqueued %d build order(s), want 0", n)
	}
	if _, ok := d.Facility(1); ok {
		t.Fatal("FAIL: failed response recorded a facility")
	}
	if !hasMandate(d.PendingMandates(100_000), "naval-100k") {
		t.Fatal("FAIL: mandate no longer pending after failed response")
	}

	// Wire citizens and retry — must commit exactly once.
	c, err := citizens.NewCitizensAPI(4242, "tester-sec215")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetCitizens(c); err != nil {
		t.Fatal(err)
	}
	r, err := d.RespondToMandate(testerReq())
	if err != nil {
		t.Fatalf("retry: %v", err)
	}

	comp := finance.Money(validConfig().Mandates[0].CompensationMicropounds)
	if got := f.TotalMoneyInCirculation(); got != comp {
		t.Fatalf("FAIL: money-in-circulation after retry = %d, want exactly %d (double-credit)", int64(got), int64(comp))
	}
	if n := len(b.Queue()); n != 1 {
		t.Fatalf("FAIL: build orders after retry = %d, want 1", n)
	}
	if _, ok := d.Facility(r.FacilityID); !ok {
		t.Fatal("FAIL: facility not recorded on retry")
	}
	if hasMandate(d.PendingMandates(100_000), "naval-100k") {
		t.Fatal("FAIL: mandate still pending after successful retry")
	}
}

// TestTesterIndependent_SEC215_MissingFinance wires only build, then asserts
// zero build orders on rejection (the finding's second variant).
func TestTesterIndependent_SEC215_MissingFinance(t *testing.T) {
	d := newDefence(t, validConfig(), 4343)
	b := newBuild(t)
	if err := d.SetBuild(b); err != nil {
		t.Fatal(err)
	}
	// finance AND citizens deliberately unwired.

	if _, err := d.RespondToMandate(testerReq()); err == nil {
		t.Fatal("expected error with finance unwired, got nil")
	} else if got := errCode(err); got != ErrDependencyMissing {
		t.Fatalf("expected ErrDependencyMissing, got %s", got)
	}

	if n := len(b.Queue()); n != 0 {
		t.Fatalf("FAIL: build-only rejection enqueued %d build order(s), want 0", n)
	}
	if _, ok := d.Facility(1); ok {
		t.Fatal("FAIL: build-only rejection recorded a facility")
	}
	if !hasMandate(d.PendingMandates(100_000), "naval-100k") {
		t.Fatal("FAIL: mandate no longer pending")
	}
}
