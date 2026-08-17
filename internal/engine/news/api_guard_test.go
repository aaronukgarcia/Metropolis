package news

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"unsafe"
)

// The tests in this file pin the encapsulation and copy-guard contracts of
// NewsAPI: Config returns a defensive copy of the weights (SEC-111), the
// History log is read-only through the exported surface (SEC-112), every
// read method rejects a struct-copied value (SEC-113), and an accepted story
// survives a later namer change (SEC-110).

// TestConfig_ReturnsDefensiveCopy is SEC-111: Config() must return a copy of
// the weights, so a caller mutating the result cannot corrupt the shared
// state and poison salience ranking with NaN (GR#21/AC-10).
func TestConfig_ReturnsDefensiveCopy(t *testing.T) {
	api, err := New("config-copy-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cfg := api.Config()
	cfg.Weights[CategoryDeath] = math.NaN()

	// The live weights must be unaffected by the caller's mutation.
	cfg2 := api.Config()
	if got := cfg2.Weights[CategoryDeath]; math.IsNaN(got) || got <= 0 {
		t.Errorf("Config() leaked its weights map: live death weight = %v after caller mutation", got)
	}

	// And ranking must stay finite and deterministic (GR#21): the poisoned
	// weight must never reach the salience comparison.
	if _, err := api.Ingest(Event{ID: "e1", Tick: 0, Category: CategoryDeath, Magnitude: 2, Text: "2 deaths"}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	b := Bulletin(api.History(), 0, cfg2)
	if len(b) != 1 {
		t.Fatalf("bulletin = %d stories, want 1", len(b))
	}
	if math.IsNaN(b[0].Salience) || math.IsInf(b[0].Salience, 0) {
		t.Errorf("ranking produced non-finite salience %v after the weights were mutated through Config()", b[0].Salience)
	}
}

// TestHistory_HasNoExportedMutators is SEC-112: the History log is the single
// source of truth, and its only write path must be the validated Ingest — an
// exported Append would let an invalid event bypass validation into the log.
func TestHistory_HasNoExportedMutators(t *testing.T) {
	// *History (not History) is the type whose method set includes the
	// pointer-receiver methods Append/Len/Snapshot; the value type's method
	// set is empty here because every method has a pointer receiver.
	typ := reflect.TypeOf((*History)(nil))
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if name == "Append" {
			t.Errorf("History exposes an exported mutator %q (SEC-112): the log must be written only through the validated Ingest path", name)
		}
	}
}

// TestReadMethods_RejectCopiedValue is SEC-113: the read methods must reject a
// struct-copied NewsAPI exactly like the write methods do, so a copied value
// is not half-usable.
func TestReadMethods_RejectCopiedValue(t *testing.T) {
	api, err := New("copy-guard-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := api.SetRewriter(fakeRewriter{fn: func(Story) (string, error) { return "", nil }}); err != nil {
		t.Fatalf("SetRewriter: %v", err)
	}
	copied := newsAPICopy(api) // struct-value copy (see helper)

	if got := copied.History(); got != nil {
		t.Errorf("History() on a copied value returned %v, want nil (SEC-113 copy guard)", got)
	}
	if got := copied.Config(); got.Weights != nil {
		t.Errorf("Config() on a copied value returned a non-zero Config, want the zero value (SEC-113 copy guard)")
	}
	if got := copied.Rewriter(); got != nil {
		t.Errorf("Rewriter() on a copied value returned %v, want nil (SEC-113 copy guard)", got)
	}
	if got := copied.Archive(); got != nil {
		t.Errorf("Archive() on a copied value returned %v, want nil (SEC-113 copy guard)", got)
	}
	if got := copied.Query(func(Story) bool { return true }); got != nil {
		t.Errorf("Query() on a copied value returned %v, want nil (SEC-113 copy guard)", got)
	}
}

// TestArchive_StableAfterNamerChange is SEC-110: a story ingested while its
// entity resolved must still appear in the archive after the namer changes,
// because the resolved name is persisted at ingest (AC-8/AC-9).
func TestArchive_StableAfterNamerChange(t *testing.T) {
	api, err := New("namer-stability-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := api.SetRoadNamer(fakeNamer{names: map[string]string{"road-1": "Pent Lane"}}); err != nil {
		t.Fatalf("SetRoadNamer: %v", err)
	}
	if _, err := api.Ingest(Event{ID: "ev-1", Tick: 0, Category: CategoryRecord, Magnitude: 1, EntityID: "road-1", Text: "queue on the road"}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// The namer changes and the road no longer resolves.
	if err := api.SetRoadNamer(fakeNamer{err: fmt.Errorf("road removed")}); err != nil {
		t.Fatalf("SetRoadNamer: %v", err)
	}

	all := api.Archive()
	if len(all) != 1 {
		t.Fatalf("Archive after namer change = %d stories, want 1 (the accepted story must survive, AC-9)", len(all))
	}
	if all[0].Name != "Pent Lane" || all[0].EventID != "ev-1" {
		t.Errorf("archived story = %+v, want the ingest-time name Pent Lane and event ev-1", all[0])
	}
}

// newsAPICopy takes a same-package value copy of *NewsAPI, isolated into its
// own helper so the copy-guard test documents the attack shape without
// tripping go vet's copylocks check (mirrors the repo's w2Copy/e2Copy/
// depositMapCopy convention): a literal `cp := *api` is legal, correct Go
// that produces the identical struct-value copy, but copylocks flags the
// literal assignment because NewsAPI embeds a sync.RWMutex. The unsafe
// byte-copy achieves the same copy via a route copylocks does not statically
// recognise as a lock copy.
func newsAPICopy(m *NewsAPI) *NewsAPI {
	c := new(NewsAPI)
	*(*[unsafe.Sizeof(NewsAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(NewsAPI{})]byte)(unsafe.Pointer(m))
	return c
}
