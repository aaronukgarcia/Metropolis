package registry

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeModule is a minimal Module implementation for tests.
type fakeModule struct {
	name    string
	version string
	health  Health
}

func (f fakeModule) Name() string    { return f.name }
func (f fakeModule) Version() string { return f.version }
func (f fakeModule) Health() Health  { return f.health }

func newStub(key string) fakeModule {
	return fakeModule{name: key, version: "0.0.1-stub", health: HealthOK}
}

func newReal(key string) fakeModule {
	return fakeModule{name: key, version: "1.0.0", health: HealthOK}
}

// AC-1: Register accepts key, semver (via option), initial status,
// CanToggle and feature-flag source, and the entry reflects them.
func TestRegister_BasicFields(t *testing.T) {
	r := NewRegistry()
	err := r.Register("engine.traffic", nil, newStub("engine.traffic"),
		WithVersion("2.3.1"),
		WithStatus(StatusStub),
		WithCanToggle(true),
		WithFlagSource("env:TRAFFIC_MODE"),
	)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	entry, ok := r.Get("engine.traffic")
	if !ok {
		t.Fatalf("Get: expected entry to exist")
	}
	if entry.Key != "engine.traffic" {
		t.Errorf("Key = %q, want engine.traffic", entry.Key)
	}
	if entry.Semver != "2.3.1" {
		t.Errorf("Semver = %q, want 2.3.1", entry.Semver)
	}
	if entry.Status != StatusStub {
		t.Errorf("Status = %q, want stub", entry.Status)
	}
	if !entry.CanToggle {
		t.Errorf("CanToggle = false, want true")
	}
	if entry.FlagSource != "env:TRAFFIC_MODE" {
		t.Errorf("FlagSource = %q, want env:TRAFFIC_MODE", entry.FlagSource)
	}
}

// AC-2 / GR#20: registering with a nil stub is a loud, registry-sourced
// error, never a silent success.
func TestRegister_NilStubRejected(t *testing.T) {
	r := NewRegistry()
	err := r.Register("engine.crime", newReal("engine.crime"), nil)
	if err == nil {
		t.Fatal("Register with nil stub: expected error, got nil")
	}
	if !strings.Contains(err.Error(), codeNilStub) {
		t.Errorf("error %v does not mention placeholder code %s", err, codeNilStub)
	}
	if _, ok := r.Get("engine.crime"); ok {
		t.Error("module with nil stub must not be registered")
	}
}

// AC-3: any mix of real and stub statuses may be booted together; both
// groups are queryable and neither blocks the other.
func TestRegister_MixedRealAndStubBoot(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("engine.traffic", newReal("engine.traffic"), newStub("engine.traffic"), WithStatus(StatusReal)); err != nil {
		t.Fatalf("Register real: %v", err)
	}
	if err := r.Register("engine.crime", nil, newStub("engine.crime"), WithStatus(StatusStub)); err != nil {
		t.Fatalf("Register stub: %v", err)
	}

	traffic, ok := r.Get("engine.traffic")
	if !ok || traffic.Status != StatusReal {
		t.Fatalf("engine.traffic: got %+v, ok=%v", traffic, ok)
	}
	crime, ok := r.Get("engine.crime")
	if !ok || crime.Status != StatusStub {
		t.Fatalf("engine.crime: got %+v, ok=%v", crime, ok)
	}

	all := r.List()
	if len(all) != 2 {
		t.Fatalf("List() len = %d, want 2", len(all))
	}
}

// AC-4: ModuleEntry exposes exactly the six F12-row fields (plus the key
// used for lookup), matching M0-ENG §3.
func TestModuleEntry_HasAllSixFields(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("engine.economy", nil, newStub("engine.economy"),
		WithVersion("0.1.0"), WithFlagSource("cfg:economy"), WithCanToggle(true))
	entry, _ := r.Get("engine.economy")

	// Compile-time-ish assertions via field access; go doc verification is
	// done manually (see task report), this test guards the runtime shape.
	_ = entry.Semver
	_ = entry.Status
	_ = entry.Health
	_ = entry.LastTickCostMicros
	_ = entry.FlagSource
	_ = entry.CanToggle
}

// AC-5: RecordTickCost reflects the most recent value, not an
// accumulating sum, and the rolling window (TickCostHistory) retains the
// full call sequence.
func TestRecordTickCost_LatestValueAndHistory(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("engine.traffic", nil, newStub("engine.traffic"))

	values := []uint64{100, 250, 90, 400}
	for _, v := range values {
		if err := r.RecordTickCost("engine.traffic", v); err != nil {
			t.Fatalf("RecordTickCost(%d): %v", v, err)
		}
	}

	entry, _ := r.Get("engine.traffic")
	if entry.LastTickCostMicros != 400 {
		t.Errorf("LastTickCostMicros = %d, want 400 (most recent, not sum)", entry.LastTickCostMicros)
	}

	hist, ok := r.TickCostHistory("engine.traffic")
	if !ok {
		t.Fatal("TickCostHistory: expected ok")
	}
	if len(hist) != len(values) {
		t.Fatalf("history len = %d, want %d", len(hist), len(values))
	}
	for i, v := range values {
		if hist[i] != v {
			t.Errorf("history[%d] = %d, want %d", i, hist[i], v)
		}
	}
}

// TickCostHistory caps at 60 samples (the F12 sparkline window).
func TestRecordTickCost_RingBufferCapsAt60(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("engine.traffic", nil, newStub("engine.traffic"))

	for i := 0; i < 90; i++ {
		if err := r.RecordTickCost("engine.traffic", uint64(i)); err != nil {
			t.Fatalf("RecordTickCost(%d): %v", i, err)
		}
	}

	hist, ok := r.TickCostHistory("engine.traffic")
	if !ok {
		t.Fatal("expected ok")
	}
	if len(hist) != 60 {
		t.Fatalf("history len = %d, want 60", len(hist))
	}
	// Oldest retained sample should be 30 (90 samples written, last 60 kept).
	if hist[0] != 30 {
		t.Errorf("hist[0] = %d, want 30", hist[0])
	}
	if hist[59] != 89 {
		t.Errorf("hist[59] = %d, want 89", hist[59])
	}
}

// AC-6: status and health are orthogonal — a module can be status:real
// and health:degraded simultaneously.
func TestUpdateHealth_IndependentOfStatus(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("engine.traffic", newReal("engine.traffic"), newStub("engine.traffic"), WithStatus(StatusReal))

	if err := r.UpdateHealth("engine.traffic", HealthDegraded); err != nil {
		t.Fatalf("UpdateHealth: %v", err)
	}

	entry, _ := r.Get("engine.traffic")
	if entry.Status != StatusReal {
		t.Errorf("Status = %q, want real (unaffected by UpdateHealth)", entry.Status)
	}
	if entry.Health != HealthDegraded {
		t.Errorf("Health = %q, want degraded", entry.Health)
	}
}

// AC-7 / AC-13: CanToggle is declared at registration, not inferred, and
// gates SetStatus with a registry-sourced error when false.
func TestSetStatus_CanToggleGate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("engine.locked", nil, newStub("engine.locked"), WithCanToggle(false))
	_ = r.Register("engine.free", nil, newStub("engine.free"), WithCanToggle(true))

	if err := r.SetStatus("engine.locked", StatusOff, "engine.locked"); err == nil {
		t.Fatal("SetStatus on CanToggle:false module: expected error, got nil")
	} else if !strings.Contains(err.Error(), codeCannotToggle) {
		t.Errorf("error %v does not mention placeholder code %s", err, codeCannotToggle)
	}
	entry, _ := r.Get("engine.locked")
	if entry.Status != StatusStub {
		t.Errorf("Status changed despite CanToggle:false: got %q", entry.Status)
	}

	if err := r.SetStatus("engine.free", StatusOff, "engine.free"); err != nil {
		t.Fatalf("SetStatus on CanToggle:true module: %v", err)
	}
	entry, _ = r.Get("engine.free")
	if entry.Status != StatusOff {
		t.Errorf("Status = %q, want off", entry.Status)
	}
}

// AC-8: a guarded toggle changes status and fires the hook exactly once.
func TestSetStatus_FiresHookExactlyOnce(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("engine.crime", nil, newStub("engine.crime"), WithCanToggle(true), WithStatus(StatusStub))

	var mu sync.Mutex
	var events []ToggleEvent
	r.SetToggleHook(func(e ToggleEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	if err := r.SetStatus("engine.crime", StatusOff, "engine.crime"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("hook fired %d times, want 1", len(events))
	}
	if events[0] != (ToggleEvent{Key: "engine.crime", From: StatusStub, To: StatusOff}) {
		t.Errorf("event = %+v, want {engine.crime stub off}", events[0])
	}
}

// A failed toggle (bad confirm token, CanToggle false, unregistered key)
// must never fire the hook.
func TestSetStatus_HookNotFiredOnFailure(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("engine.crime", nil, newStub("engine.crime"), WithCanToggle(true))

	fired := false
	r.SetToggleHook(func(ToggleEvent) { fired = true })

	if err := r.SetStatus("engine.crime", StatusOff, "wrong-token"); err == nil {
		t.Fatal("expected error for mismatched confirm token")
	}
	if fired {
		t.Error("hook fired on a failed toggle")
	}
}

// AC-9 / AC-12: List/Get are the two query shapes both consumers need;
// Get on an unregistered key returns the ok-idiom zero value, no panic.
func TestGet_UnregisteredKeyReturnsZeroValueFalse(t *testing.T) {
	r := NewRegistry()
	entry, ok := r.Get("does.not.exist")
	if ok {
		t.Error("Get on unregistered key: ok = true, want false")
	}
	if entry != (ModuleEntry{}) {
		t.Errorf("Get on unregistered key: entry = %+v, want zero value", entry)
	}
}

// AC-10: List() returns identical, stably sorted-by-key ordering on every
// call, regardless of registration order.
func TestList_StableSortedOrder(t *testing.T) {
	r := NewRegistry()
	keys := []string{"ui.screen.debug", "engine.crime", "foundation.det", "engine.traffic"}
	for _, k := range keys {
		if err := r.Register(k, nil, newStub(k)); err != nil {
			t.Fatalf("Register(%s): %v", k, err)
		}
	}

	want := append([]string(nil), keys...)
	sort.Strings(want)

	for i := 0; i < 5; i++ {
		got := r.List()
		if len(got) != len(want) {
			t.Fatalf("List() len = %d, want %d", len(got), len(want))
		}
		for j, entry := range got {
			if entry.Key != want[j] {
				t.Fatalf("call %d: List()[%d].Key = %q, want %q", i, j, entry.Key, want[j])
			}
		}
	}
}

// AC-11: duplicate registration is a registry-sourced error, not a
// silent overwrite or a panic.
func TestRegister_DuplicateKeyRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("engine.traffic", nil, newStub("engine.traffic"), WithVersion("1.0.0")); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register("engine.traffic", nil, newStub("engine.traffic"), WithVersion("2.0.0"))
	if err == nil {
		t.Fatal("duplicate Register: expected error, got nil")
	}
	if !strings.Contains(err.Error(), codeDuplicateKey) {
		t.Errorf("error %v does not mention placeholder code %s", err, codeDuplicateKey)
	}

	entry, _ := r.Get("engine.traffic")
	if entry.Semver != "1.0.0" {
		t.Errorf("Semver = %q, want 1.0.0 (original registration must survive)", entry.Semver)
	}
}

// AC-12: mutation calls (UpdateHealth/RecordTickCost/SetStatus) on an
// unregistered key are active errors, distinct from Get's ok-idiom.
func TestMutations_UnregisteredKeyIsError(t *testing.T) {
	r := NewRegistry()

	if err := r.UpdateHealth("nope", HealthOK); err == nil {
		t.Error("UpdateHealth on unregistered key: expected error")
	}
	if err := r.RecordTickCost("nope", 10); err == nil {
		t.Error("RecordTickCost on unregistered key: expected error")
	}
	if err := r.SetStatus("nope", StatusOff, "nope"); err == nil {
		t.Error("SetStatus on unregistered key: expected error")
	}
	if _, ok := r.TickCostHistory("nope"); ok {
		t.Error("TickCostHistory on unregistered key: expected ok=false")
	}
}

// AC-16: boot order matches registration order, deterministically, given
// the same sequence of Register calls.
func TestBootOrder_MatchesRegistrationOrder(t *testing.T) {
	order := []string{"foundation.det", "engine.traffic", "engine.crime", "ui.screen.debug"}

	r1 := NewRegistry()
	for _, k := range order {
		_ = r1.Register(k, nil, newStub(k))
	}
	r2 := NewRegistry()
	for _, k := range order {
		_ = r2.Register(k, nil, newStub(k))
	}

	got1 := keysOf(r1.BootOrder())
	got2 := keysOf(r2.BootOrder())

	if fmt.Sprint(got1) != fmt.Sprint(order) {
		t.Fatalf("BootOrder = %v, want %v", got1, order)
	}
	if fmt.Sprint(got1) != fmt.Sprint(got2) {
		t.Fatalf("BootOrder not deterministic across identical registration sequences: %v vs %v", got1, got2)
	}
}

func keysOf(entries []ModuleEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Key
	}
	return out
}

// SetStatus to StatusReal without a real implementation registered must
// fail loudly rather than silently activating a module that does not
// exist.
func TestSetStatus_RealWithoutImplementationRejected(t *testing.T) {
	r := NewRegistry()
	_ = r.Register("engine.crime", nil, newStub("engine.crime"), WithCanToggle(true))

	if err := r.SetStatus("engine.crime", StatusReal, "engine.crime"); err == nil {
		t.Fatal("expected error toggling to real with no real implementation")
	}
}

// AC-14 / GR#21: concurrent writers (per-module health/tick-cost updates)
// racing a concurrent reader (List, the F12 render path) must be race-free.
// Run with -race.
func TestConcurrentAccess_NoDataRace(t *testing.T) {
	r := NewRegistry()
	keys := []string{"engine.a", "engine.b", "engine.c", "engine.d"}
	for _, k := range keys {
		if err := r.Register(k, nil, newStub(k), WithCanToggle(true)); err != nil {
			t.Fatalf("Register(%s): %v", k, err)
		}
	}

	var writers, readers sync.WaitGroup
	stop := make(chan struct{})

	// Writers: each hammers a distinct module's health/tick-cost fields,
	// a fixed number of iterations so this test terminates on its own.
	for i, k := range keys {
		writers.Add(1)
		go func(key string, seed int) {
			defer writers.Done()
			for n := 0; n < 500; n++ {
				_ = r.RecordTickCost(key, uint64(n+seed))
				_ = r.UpdateHealth(key, HealthOK)
			}
		}(k, i*1000)
	}

	// Readers: continuously list and get while writers are active, until
	// told to stop.
	for i := 0; i < 3; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = r.List()
					_, _ = r.Get("engine.a")
					_, _ = r.TickCostHistory("engine.b")
				}
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()
}
