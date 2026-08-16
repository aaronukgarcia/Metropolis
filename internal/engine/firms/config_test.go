package firms

import (
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// TestLoadConfigValidates the committed data/firms.json round-trips through
// the loader (AC-1's load path, GR#15): four stages in order, a
// superlinear services exponent, and a month-0 base-rate cycle.
func TestLoadConfigValidates(t *testing.T) {
	dir, err := data.ResolveDataDir("firms-config-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	cfg, err := LoadConfig(filepath.Join(dir, "firms.json"), "firms-config-test")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Stages) != 4 {
		t.Fatalf("stages = %d, want 4", len(cfg.Stages))
	}
	for i, st := range stageOrder {
		if cfg.Stages[i].Stage != st {
			t.Fatalf("stage order broken at %d: got %v", i, cfg.Stages[i].Stage)
		}
	}
	if cfg.ServicesDemand.ExponentPerMille <= 1000 {
		t.Fatalf("services exponent %d not superlinear (>1000)", cfg.ServicesDemand.ExponentPerMille)
	}
	if cfg.Credit.BaseRateCycle[0].Month != 0 {
		t.Fatalf("base-rate cycle must start at month 0")
	}
}

// TestFirmsAPISubscriptionSurface (AC-1/US-6): the lifecycle-event stream is
// subscribable — a subscriber observes founded/grown/failed events.
func TestFirmsAPISubscriptionSurface(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetCitizens(seedCitizens(t, 30))

	subID, ch, err := api.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	id, err := api.Found(1)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	ev := <-ch
	if ev.Kind != EventFounded || ev.FirmID != id {
		t.Fatalf("event = %+v, want founded firm %d", ev, id)
	}

	if err := api.Unsubscribe(subID); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if got := len(api.Events()); got == 0 {
		t.Fatal("expected the event log to carry the founded event")
	}
}

// firmsCopy performs SEC-020's attack — a plain FirmsAPI struct copy — via a
// raw byte-for-byte memcpy through unsafe.Pointer, mirroring the
// freight/citizens byte-copy convention (a literal `cp := *api` is legal Go
// with the identical attack shape, but go vet's copylocks check flags it and
// VERIFY runs go vet).
func firmsCopy(api *FirmsAPI) *FirmsAPI {
	cp := new(FirmsAPI)
	*(*[unsafe.Sizeof(FirmsAPI{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(FirmsAPI{})]byte)(unsafe.Pointer(api))
	return cp
}

// TestCopyGuard (SEC-020-class): a method call on a struct-copied FirmsAPI
// is rejected.
func TestCopyGuard(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	copied := firmsCopy(api)
	if _, err := copied.Found(1); !hasCode(err, ErrCopiedValue) {
		t.Fatalf("copied.Found = %v, want ErrCopiedValue", err)
	}
}
