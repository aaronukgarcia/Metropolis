package comms

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// SEC-171 — finiteness-of-output class. ParcelVolume multiplies the (unbounded,
// finite, non-negative) baseParcels input by per-era/wealth/share factors that
// are each >= 1, so a finite input such as 1e308 overflows the float64 product
// to +Inf. The fix saturates the computed output so a finite input can never
// leak +Inf/NaN (GR#16).
func TestParcelVolumeNeverNonFinite(t *testing.T) {
	c := newTestComms(t)
	wireGate(t, c)
	if err := c.SetWealth(1.0); err != nil {
		t.Fatalf("SetWealth: %v", err)
	}
	// 1e308 is a finite float64 (below math.MaxFloat64) and non-negative, so
	// SetPostalVolumes accepts it — but the >=1 parcel factors overflow the
	// product. Pre-fix this returned +Inf; post-fix it saturates to a finite
	// value (SEC-171).
	if err := c.SetPostalVolumes(0, 1e308); err != nil {
		t.Fatalf("SetPostalVolumes(0, 1e308): %v", err)
	}
	if got := c.ParcelVolume(); math.IsInf(got, 0) || math.IsNaN(got) {
		t.Fatalf("ParcelVolume() = %v, must be finite for a finite input (SEC-171)", got)
	}
}

// SEC-171 — the class sweep: every computed float OUTPUT that multiplies/sums
// floats must stay finite under an extreme-but-accepted finite input. The four
// multiplicative outputs (LetterVolume, ParcelVolume, HighStreetDrain,
// NetHighStreetDrain) are guarded by satFinite; the share outputs already pass
// through clamp01. If any future multiplicative output drops its guard, the
// extreme input here reopens the SEC-171 class.
func TestComputedOutputsStayFinite(t *testing.T) {
	c, _, _ := newWiredComms(t)
	wireGate(t, c)
	if err := c.SetWealth(1.0); err != nil {
		t.Fatalf("SetWealth: %v", err)
	}
	if err := c.SetCounterplayOffset(1.0); err != nil {
		t.Fatalf("SetCounterplayOffset: %v", err)
	}
	if err := c.SetPostalVolumes(1e308, 1e308); err != nil {
		t.Fatalf("SetPostalVolumes: %v", err)
	}

	checks := map[string]float64{
		"LetterVolume":       c.LetterVolume(),
		"ParcelVolume":       c.ParcelVolume(),
		"ECommerceRawShare":  c.ECommerceRawShare(),
		"ECommerceShare":     c.ECommerceShare(),
		"HighStreetDrain":    c.HighStreetDrain(),
		"NetHighStreetDrain": c.NetHighStreetDrain(),
	}
	for name, v := range checks {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			t.Errorf("%s = %v, must be finite for a finite input (SEC-171 class)", name, v)
		}
	}
}

// SEC-172 — idempotency/dedup class. RegisterLastMileDepot must not accept the
// same district twice: a duplicate would mint a second real firm and call
// logistics.Provision again (resetting the shelf to full — a free restock). The
// fix dedups on the district before any firm/shelf side effect, matching
// RegisterFulfilmentCentre's idempotency.
func TestRegisterLastMileDepotIdempotent(t *testing.T) {
	c, f, l := newWiredComms(t)
	if _, err := c.RegisterFulfilmentCentre(); err != nil {
		t.Fatalf("RegisterFulfilmentCentre: %v", err)
	}
	first, err := c.RegisterLastMileDepot("north")
	if err != nil {
		t.Fatalf("RegisterLastMileDepot(north): %v", err)
	}

	// Drain the district shelf to empty (capacity 10000, data/comms.json).
	if res, err := c.DeliverParcels("north", 10000); err != nil {
		t.Fatalf("DeliverParcels(drain): %v", err)
	} else if res.Shortfall != 0 {
		t.Fatalf("precondition: draining a full shelf must have shortfall 0, got %d", res.Shortfall)
	}
	before, err := l.Stock("north", parcelCommodity)
	if err != nil {
		t.Fatalf("Stock(north): %v", err)
	}
	if before.Level != 0 {
		t.Fatalf("precondition: shelf must be empty after draining, got level %d", before.Level)
	}

	// Re-registering the SAME district must be idempotent.
	second, err := c.RegisterLastMileDepot("north")
	if err != nil {
		t.Fatalf("second RegisterLastMileDepot(north): %v", err)
	}
	if second.FirmID != first.FirmID {
		t.Errorf("re-registering a district must return the existing depot (FirmID %d), got %d", first.FirmID, second.FirmID)
	}
	if got := c.LastMileDepotCount(); got != 1 {
		t.Errorf("LastMileDepotCount() = %d, want 1 (no second depot)", got)
	}
	// Fulfilment centre + exactly one depot — the duplicate must not mint a firm.
	if got := f.FoundedCount(); got != 2 {
		t.Errorf("FoundedCount() = %d, want 2 (fulfilment + one depot — no duplicate firm)", got)
	}

	// The shelf must NOT have been reset to full by the duplicate registration.
	after, err := l.Stock("north", parcelCommodity)
	if err != nil {
		t.Fatalf("Stock(north) after re-register: %v", err)
	}
	if after.Level != 0 {
		t.Errorf("re-registering a district must not restock the shelf: level = %d, want 0 (SEC-172)", after.Level)
	}
	// And a fresh delivery reports a full shortfall — proving no free restock.
	if res, err := c.DeliverParcels("north", 10000); err != nil {
		t.Fatalf("DeliverParcels(after re-register): %v", err)
	} else if res.Shortfall != 10000 {
		t.Errorf("delivery after re-register must report a full shortfall (no free restock), got %d", res.Shortfall)
	}
}

// SEC-173 — no-lock-across-a-seam class. DeliverParcels must not hold c.mu
// across logistics.Draw, because Draw fires subscribed ShortfallHandlers
// synchronously and a handler that queries comms would deadlock on a re-entrant
// RLock. The non-reentrant RWMutex makes this a deterministic deadlock pre-fix;
// the timeout below is a fail-safe bound on the hang, not a probabilistic check.
func TestDeliverParcelsShortfallHandlerNoDeadlock(t *testing.T) {
	c, _, l := newWiredComms(t)
	if _, err := c.RegisterFulfilmentCentre(); err != nil {
		t.Fatalf("RegisterFulfilmentCentre: %v", err)
	}
	if _, err := c.RegisterLastMileDepot("north"); err != nil {
		t.Fatalf("RegisterLastMileDepot: %v", err)
	}

	// A shortfall handler that re-enters comms from inside logistics.Draw.
	if err := l.SubscribeShortfalls(func(logistics.ShortfallEvent) {
		_ = c.ECommerceShare()
	}); err != nil {
		t.Fatalf("SubscribeShortfalls: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// 100000 > shelf capacity 10000, so Draw runs short and fires the handler.
		_, _ = c.DeliverParcels("north", 100000)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("DeliverParcels deadlocked: a ShortfallHandler that calls a comms method blocked on a re-entrant RLock (SEC-173)")
	}
}

// SEC-173 class — the second user-callback seam. AdvanceEra must not hold c.mu
// across the MilestoneGate callback: a gate that queries comms (e.g. c.Era())
// would deadlock pre-fix. The snapshot-release-call pattern releases the lock
// before the callback, then re-validates the one-step ladder before committing.
func TestAdvanceEraMilestoneGateNoDeadlock(t *testing.T) {
	c := newTestComms(t)
	if err := c.SetMilestoneGate(MilestoneGateFunc(func(tier int) bool {
		_ = c.Era() // re-entrant comms query from inside the gate callback
		return true
	})); err != nil {
		t.Fatalf("SetMilestoneGate: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.AdvanceEra(EraDialUp)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("AdvanceEra deadlocked: a MilestoneGate callback that calls a comms method blocked on a re-entrant RLock (SEC-173)")
	}
	if got := c.Era(); got != EraDialUp {
		t.Errorf("Era() = %v, want dial-up after the gate callback re-entered comms", got)
	}
}

// SEC-191 — check-then-set idempotency TOCTOU class. The register methods
// release c.mu across their external seam (SEC-173) and re-check idempotency on
// re-acquire, but that double-check is not atomic: N concurrent same-key callers
// all pass the FIRST check before any winner commits, so N firms get minted
// (N-1 orphaned EventFounded records) instead of one. The reservation fix makes
// a concurrent caller observe the in-progress registration and wait, so exactly
// one external firm is minted under any schedule. Each test below drives N
// concurrent same-key registrations to completion and asserts the external
// entity count is exactly 1 (deterministic post-fix).

func TestRegisterFulfilmentCentreConcurrentSingleFirm(t *testing.T) {
	c, f, _ := newWiredComms(t)

	const n = 64
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	firmIDs := make([]uint64, n)
	errs := make([]error, n)
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			ready.Done()
			<-start
			fc, err := c.RegisterFulfilmentCentre()
			firmIDs[i] = fc.FirmID
			errs[i] = err
			done.Done()
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()

	if got := f.FoundedCount(); got != 1 {
		t.Fatalf("FoundedCount() = %d, want exactly 1 (SEC-191: %d concurrent registrations minted %d firms)", got, n, got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("RegisterFulfilmentCentre caller %d errored: %v", i, errs[i])
		}
		if firmIDs[i] != firmIDs[0] {
			t.Errorf("caller %d got FirmID %d, want the single winner's %d", i, firmIDs[i], firmIDs[0])
		}
	}
}

func TestRegisterLastMileDepotConcurrentSingleFirm(t *testing.T) {
	c, f, _ := newWiredComms(t)

	const n = 64
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	firmIDs := make([]uint64, n)
	errs := make([]error, n)
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			ready.Done()
			<-start
			depot, err := c.RegisterLastMileDepot("north")
			firmIDs[i] = depot.FirmID
			errs[i] = err
			done.Done()
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()

	if got := f.FoundedCount(); got != 1 {
		t.Fatalf("FoundedCount() = %d, want exactly 1 (SEC-191: %d concurrent depot registrations minted %d firms)", got, n, got)
	}
	if got := c.LastMileDepotCount(); got != 1 {
		t.Fatalf("LastMileDepotCount() = %d, want 1", got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("RegisterLastMileDepot caller %d errored: %v", i, errs[i])
		}
		if firmIDs[i] != firmIDs[0] {
			t.Errorf("caller %d got FirmID %d, want the single winner's %d", i, firmIDs[i], firmIDs[0])
		}
	}
}

func TestRegisterPostalServicesConcurrentSingleRegistration(t *testing.T) {
	c := newTestComms(t)
	s := services.New("comms-test-services-sec191")
	if err := c.SetServices(s); err != nil {
		t.Fatalf("SetServices: %v", err)
	}

	const n = 64
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			ready.Done()
			<-start
			errs[i] = c.RegisterPostalServices()
			done.Done()
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("RegisterPostalServices caller %d errored (pre-fix the services seam surfaced ErrDuplicateService): %v", i, errs[i])
		}
	}
	// Exactly one kind and two service instances, each registered exactly once —
	// the services seam rejects duplicate IDs, so all-nil above proves no second
	// registration raced through.
	kind := services.ServiceKind(c.cfg.postal.Kind)
	if _, ok := s.KindDef(kind); !ok {
		t.Fatalf("postal kind %q not registered", kind)
	}
	sortingID := services.ServiceID(c.cfg.postal.SortingOffice.ID)
	hubID := services.ServiceID(c.cfg.postal.ParcelHub.ID)
	for _, id := range []services.ServiceID{sortingID, hubID} {
		if _, err := s.Capacity(id); err != nil {
			t.Fatalf("postal service %q not registered: %v", id, err)
		}
	}
}
