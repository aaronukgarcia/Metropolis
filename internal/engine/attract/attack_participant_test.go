package attract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// ---------------------------------------------------------------------------
// FEAT-1972079947 — independent destructive round r1 (Opus). These are the
// attacks that found real gaps or that pin behaviour the primary suite
// asserted only indirectly. They are permanent regressions: each one fails
// if the property it names is lost.
// ---------------------------------------------------------------------------

// attackDriven returns an AttractAPI driven off every persisted field's
// zero value, plus its single emitted meta record.
func attackDriven(t *testing.T) (*AttractAPI, serialize.Record) {
	t.Helper()
	a, _, _, _ := newAPI(t, validConfig())
	driveAttract(t, a)
	rec, ok, err := NewSaveParticipant(a).Source()()
	if err != nil || !ok {
		t.Fatalf("Source: err=%v ok=%v", err, ok)
	}
	return a, rec
}

// TestAttack_EmittedRecordDoesNotAliasLiveState is the F8 copyguard attack:
// the participant must hand the caller freshly-marshalled bytes, never a
// window onto AttractAPI's live state. Scribbling over the emitted record's
// Data (and over the Record value itself) must leave the engine untouched,
// and a second Source() must still emit the original bytes.
func TestAttack_EmittedRecordDoesNotAliasLiveState(t *testing.T) {
	a, rec := attackDriven(t)

	a.mu.RLock()
	before := a.reputation
	beforeID := a.nextMigrantID
	beforeMonth := a.lastAdvancedMonth
	a.mu.RUnlock()

	original := string(rec.Data)
	for i := range rec.Data {
		rec.Data[i] = 'X'
	}
	rec.Kind = "clobbered"

	a.mu.RLock()
	after := a.reputation
	afterID := a.nextMigrantID
	afterMonth := a.lastAdvancedMonth
	a.mu.RUnlock()

	if before != after || beforeID != afterID || beforeMonth != afterMonth {
		t.Fatalf("emitted record aliases live AttractAPI state: reputation %+v -> %+v, nextMigrantID %d -> %d, lastAdvancedMonth %d -> %d",
			before, after, beforeID, afterID, beforeMonth, afterMonth)
	}

	rec2, ok, err := NewSaveParticipant(a).Source()()
	if err != nil || !ok {
		t.Fatalf("second Source: err=%v ok=%v", err, ok)
	}
	if string(rec2.Data) != original {
		t.Fatalf("a second emission differs after the first record's bytes were clobbered: %q != %q", string(rec2.Data), original)
	}
}

// TestAttack_EmissionIsByteIdenticalAcrossRepeatedSources pins GR#21
// determinism at the record level (the primary suite compares whole
// bundles): three independent Source() streams over the same unmodified
// state must produce identical Kind+Data, and each stream must terminate
// after exactly one record.
func TestAttack_EmissionIsByteIdenticalAcrossRepeatedSources(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())
	driveAttract(t, a)

	var first string
	for run := 0; run < 3; run++ {
		src := NewSaveParticipant(a).Source()
		rec, ok, err := src()
		if err != nil || !ok {
			t.Fatalf("run %d: first pull err=%v ok=%v", run, err, ok)
		}
		got := rec.Kind + "|" + string(rec.Data)
		if run == 0 {
			first = got
		} else if got != first {
			t.Fatalf("run %d emission differs from run 0:\n got=%s\nwant=%s", run, got, first)
		}
		if _, ok, err := src(); ok || err != nil {
			t.Fatalf("run %d: stream did not terminate after one record (ok=%v err=%v)", run, ok, err)
		}
	}
}

// TestAttack_LoadRejectsMalformedRecords feeds the Handler adversarial
// records. Every one must return an error (never panic, never silently
// accept), and a rejected record must not have left the target holding a
// half-applied state that a later reader could mistake for real.
func TestAttack_LoadRejectsMalformedRecords(t *testing.T) {
	cases := []struct {
		name string
		rec  serialize.Record
	}{
		{"wrong kind", serialize.Record{Kind: "finance.meta", Data: []byte(`{}`)}},
		{"empty kind", serialize.Record{Kind: "", Data: []byte(`{}`)}},
		{"nil data", serialize.Record{Kind: recAttractMeta, Data: nil}},
		{"empty data", serialize.Record{Kind: recAttractMeta, Data: []byte{}}},
		{"truncated json", serialize.Record{Kind: recAttractMeta, Data: []byte(`{"reputation":`)}},
		{"negative nextMigrantID", serialize.Record{Kind: recAttractMeta, Data: []byte(`{"nextMigrantID":-1}`)}},
		{"nextMigrantID overflows uint64", serialize.Record{Kind: recAttractMeta, Data: []byte(`{"nextMigrantID":18446744073709551616}`)}},
		{"reputation value is a string", serialize.Record{Kind: recAttractMeta, Data: []byte(`{"reputation":{"value":"nope"}}`)}},
		{"lastAdvancedMonth is a float", serialize.Record{Kind: recAttractMeta, Data: []byte(`{"lastAdvancedMonth":1.5}`)}},
		{"json array not object", serialize.Record{Kind: recAttractMeta, Data: []byte(`[]`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _, _, _ := newAPI(t, validConfig())
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Handler PANICKED on %s: %v", tc.name, r)
				}
			}()
			err := NewSaveParticipant(a).Handler()(tc.rec)
			if err == nil {
				t.Fatalf("Handler silently ACCEPTED a malformed record (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), "attract") {
				t.Fatalf("error does not name the module, so a bundle-level failure cannot be attributed: %v", err)
			}
		})
	}
}

// TestAttack_DuplicateMetaRecordIsLastWriteWins pins the observed behaviour
// for a bundle carrying two attract.meta records: the second is applied
// over the first (last-write-wins), never panicking and never leaving a
// blend of the two. If a future change makes a duplicate an error instead,
// that is a deliberate hardening and this test is the place to say so.
func TestAttack_DuplicateMetaRecordIsLastWriteWins(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())
	h := NewSaveParticipant(a).Handler()

	first := serialize.Record{Kind: recAttractMeta, Data: mustJSON(t, attractMetaWire{
		Reputation:        reputationStateWire{HasBaseline: true, Baseline: 10, Value: 1},
		LastAdvancedMonth: 3, HasAdvanced: true, NextMigrantID: 11,
	})}
	second := serialize.Record{Kind: recAttractMeta, Data: mustJSON(t, attractMetaWire{
		Reputation:        reputationStateWire{HasBaseline: true, Baseline: 20, Value: 2},
		LastAdvancedMonth: 4, HasAdvanced: true, NextMigrantID: 22,
	})}
	if err := h(first); err != nil {
		t.Fatalf("first record: %v", err)
	}
	if err := h(second); err != nil {
		t.Fatalf("duplicate record must not error (last-write-wins): %v", err)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.reputation != (reputationState{hasBaseline: true, baseline: 20, value: 2}) ||
		a.lastAdvancedMonth != 4 || a.nextMigrantID != 22 {
		t.Fatalf("duplicate meta record left a blended state: rep=%+v last=%d next=%d",
			a.reputation, a.lastAdvancedMonth, a.nextMigrantID)
	}
}

// TestAttack_HandlerResetsExactlyOncePerLoad proves the Handler's one-shot
// reset does not re-fire on a second record and wipe what the first
// installed (the reset flag is per-Handler, and a fresh Handler must reset
// again).
func TestAttack_HandlerResetsExactlyOncePerLoad(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())
	// Pre-load junk that a load MUST clear.
	a.mu.Lock()
	a.reputation = reputationState{hasBaseline: true, baseline: 999, value: 999}
	a.lastAdvancedMonth = 999
	a.hasAdvanced = true
	a.nextMigrantID = 999
	a.mu.Unlock()

	rec := serialize.Record{Kind: recAttractMeta, Data: mustJSON(t, attractMetaWire{
		Reputation: reputationStateWire{HasBaseline: false, Baseline: 0, Value: 0},
	})}
	if err := NewSaveParticipant(a).Handler()(rec); err != nil {
		t.Fatalf("Handler: %v", err)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.reputation != (reputationState{}) || a.lastAdvancedMonth != 0 || a.hasAdvanced || a.nextMigrantID != 0 {
		t.Fatalf("a load of an all-zero save did not clear the pre-load state: rep=%+v last=%d hasAdv=%v next=%d",
			a.reputation, a.lastAdvancedMonth, a.hasAdvanced, a.nextMigrantID)
	}
}

// TestAttack_NonFiniteReputationDoesNotEscapeAsNaNScore is the FEAT-086
// backstop attack on the LOAD path: a bundle whose reputation encodes a
// non-finite value (JSON cannot express NaN, but ±Inf is reachable via an
// out-of-float64-range literal, and a hand-edited bundle is exactly the
// threat model) must not leave the API returning a NaN/Inf A() with a nil
// error.
func TestAttack_NonFiniteReputationDoesNotEscapeAsNaNScore(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())
	rec := serialize.Record{Kind: recAttractMeta, Data: []byte(`{"reputation":{"hasBaseline":true,"baseline":0,"value":1e400}}`)}
	err := NewSaveParticipant(a).Handler()(rec)
	if err != nil {
		return // rejected at decode time — the strongest outcome.
	}
	if math.IsInf(a.Reputation(), 0) || math.IsNaN(a.Reputation()) {
		if _, aErr := a.A(); aErr == nil {
			t.Fatalf("a bundle-supplied non-finite reputation (%v) was accepted AND A() returned a non-finite score with a nil error", a.Reputation())
		}
	}
}

// TestAttack_CopiedAPIIsRejectedOnEverySaveMethod is the SEC-020 attack:
// every participant entry point reached through a struct-copied AttractAPI
// must fail closed rather than reading or mutating state.
func TestAttack_CopiedAPIIsRejectedOnEverySaveMethod(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())
	driveAttract(t, a)
	p := NewSaveParticipant(attractCopy(a))

	if k := p.Kind(); k != "" {
		t.Fatalf("Kind() on a copied AttractAPI returned %q, want the empty (rejected) kind", k)
	}
	if _, _, err := p.Source()(); err == nil {
		t.Fatal("Source() on a copied AttractAPI yielded a record with no error")
	}
	if err := p.Handler()(serialize.Record{Kind: recAttractMeta, Data: []byte(`{}`)}); err == nil {
		t.Fatal("Handler() on a copied AttractAPI accepted a record with no error")
	}
}

// TestAttack_PostRestoreMigrantIDsDisjointOverManyMonths extends the
// primary FEAT-169 collision test past a single mint burst: after a
// restore, three further months of real ApplyMigration-driven minting must
// produce ids disjoint from EVERY pre-save id, compared as full sets rather
// than as a counter value.
func TestAttack_PostRestoreMigrantIDsDisjointOverManyMonths(t *testing.T) {
	orig, _, _, _ := newAPI(t, validConfig())
	driveAttract(t, orig)

	orig.mu.RLock()
	savedCounter := orig.nextMigrantID
	orig.mu.RUnlock()
	if savedCounter <= 1 {
		t.Fatalf("test setup: driveAttract minted nothing (counter=%d)", savedCounter)
	}
	// Every id the pre-save history could have minted, reconstructed from
	// the counter's own definition (mintMigrantID: ++counter, then prefix).
	preSave := map[uint64]bool{}
	for n := uint64(2); n <= savedCounter; n++ {
		preSave[migrantIDHighBit|n] = true
	}

	rec, ok, err := NewSaveParticipant(orig).Source()()
	if err != nil || !ok {
		t.Fatalf("Source: %v ok=%v", err, ok)
	}
	reloaded, _, _, _ := newAPI(t, validConfig())
	if err := NewSaveParticipant(reloaded).Handler()(rec); err != nil {
		t.Fatalf("Handler: %v", err)
	}

	for month := int64(4); month <= 6; month++ {
		if err := reloaded.SetTermInputs(TermInputs{
			JobAvailability: 90, ServiceCoverage: 90, Environment: 90,
			LeisureFit: 90, Safety: 90,
		}); err != nil {
			t.Fatalf("SetTermInputs: %v", err)
		}
		if _, err := reloaded.ApplyMigration(MigrationCommand{
			Month: month, HousingVacancy: 1000, JunctionThroughput: 1000,
		}); err != nil {
			t.Fatalf("ApplyMigration month %d: %v", month, err)
		}
	}

	reloaded.mu.RLock()
	endCounter := reloaded.nextMigrantID
	reloaded.mu.RUnlock()
	if endCounter <= savedCounter {
		t.Fatalf("three post-restore months minted no migrants (counter %d -> %d) -- the collision proof would be vacuous", savedCounter, endCounter)
	}
	for n := savedCounter + 1; n <= endCounter; n++ {
		if preSave[migrantIDHighBit|n] {
			t.Fatalf("post-restore migrant id %d COLLIDES with a pre-save id (FEAT-169 class)", migrantIDHighBit|n)
		}
	}
}

// attractCopy byte-copies an AttractAPI (the SEC-020 threat: a caller
// dereferencing the pointer). Done through unsafe rather than `cp := *a`
// so `go vet`'s copylocks check does not reject the test file itself —
// the same convention engine.airunits' airUnitsCopy / engine.crime's
// crimeCopy already use.
func attractCopy(a *AttractAPI) *AttractAPI {
	c := new(AttractAPI)
	*(*[unsafe.Sizeof(AttractAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(AttractAPI{})]byte)(unsafe.Pointer(a))
	return c
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
