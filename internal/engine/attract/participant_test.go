package attract

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// ---------------------------------------------------------------------------
// AC-2 — domain<->wire field-parity drift tests (mirrors
// finance/participant_test.go's TestFinanceWireFieldsMatchDomain /
// TestFinanceAPIFieldsAllClassified, the pilot this participant follows).
// ---------------------------------------------------------------------------

// assertFieldParity checks that domain and wire have the same number of
// fields and that every domain field has a wire counterpart of the same
// reflect.Kind. rename maps a domain field name to its wire field name
// where they differ.
func assertFieldParity(t *testing.T, domain, wire reflect.Type, rename map[string]string) {
	t.Helper()
	if domain.NumField() != wire.NumField() {
		t.Fatalf("%s has %d fields but wire %s has %d -- every serialized %s field must have a wire counterpart or an explicit, commented exclusion (AC-2)",
			domain.Name(), domain.NumField(), wire.Name(), wire.NumField(), domain.Name())
	}
	for i := 0; i < domain.NumField(); i++ {
		df := domain.Field(i)
		want := df.Name
		if r, ok := rename[df.Name]; ok {
			want = r
		}
		wf, ok := wire.FieldByName(want)
		if !ok {
			t.Fatalf("%s field %q has no counterpart %s.%s", domain.Name(), df.Name, wire.Name(), want)
		}
		if wf.Type.Kind() != df.Type.Kind() {
			t.Fatalf("%s.%s has kind %s, want %s to match %s.%s", wire.Name(), wf.Name, wf.Type.Kind(), df.Type.Kind(), domain.Name(), df.Name)
		}
	}
}

func TestAttractWireFieldsMatchDomain(t *testing.T) {
	// reputationState's fields are all unexported (private momentum
	// bookkeeping); their wire counterparts must be exported to be
	// JSON-marshalled, so every field is renamed to its exported form
	// (mirrors finance's lossStreak->LossStreak rename).
	assertFieldParity(t, reflect.TypeOf((*reputationState)(nil)).Elem(), reflect.TypeOf((*reputationStateWire)(nil)).Elem(), map[string]string{
		"hasBaseline": "HasBaseline",
		"baseline":    "Baseline",
		"value":       "Value",
	})
}

// TestAttractAPIFieldsAllClassified is the highest-teeth AC-2 test: every
// field of AttractAPI itself must be either explicitly EXCLUDED
// (runtime/config/dependency, never persisted) or COVERED by the save (a
// wire field on attractMetaWire). A new field added to AttractAPI that is
// neither serialized nor consciously excluded FAILS the build here -- the
// "built but not serialized" class this participant exists to prevent, and
// the exact class of gap FEAT-1972079947 was filed to close (see
// participant.go's package doc).
func TestAttractAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"correlationID":      "per-instance error correlation, not simulation state",
		"weights":            "construction-time config (Config.Weights), re-supplied by New on every load",
		"world":              "construction-time config (Config.World, a WorldPool seam), re-supplied by New",
		"migrationRate":      "construction-time config (Config.MigrationRate), re-supplied by New",
		"repCfg":             "construction-time config (Config.Reputation), re-supplied by New",
		"seed":               "construction-time config (the world seed New was called with; the counter-based hash draws AC-12 describes are STATELESS functions of this seed plus per-call inputs, never a persisted cursor -- see participant.go's package doc)",
		"citizens":           "wired dependency pointer (SetCitizens), re-supplied by the composition root before Load runs",
		"finance":            "wired dependency pointer (SetFinance), re-supplied by the composition root before Load runs",
		"households":         "wired dependency pointer (SetHouseholds), re-supplied by the composition root before Load runs",
		"mu":                 "runtime lock, not state",
		"termInputs":         "pushed-input snapshot (SetTermInputs); the composition root recomputes and re-pushes all five terms every month BEFORE calling ApplyMigration (compose.go's applyMigration), so a stale post-load value is always overwritten before it can influence a migration decision -- see compose/save_loadat_test.go's TestLoadAt_TickContinuity for the proof that only reputation/lastAdvancedMonth/nextMigrantID (this file's coverage) are load-bearing across a restore",
		"self":               "SEC-020 copy-guard pointer, re-armed by New",
		"wellbeingModifiers": "wired dependency getter (MOD-034's SetWellbeingModifiers), re-supplied by the composition root before Load runs -- a plain func()(float64,float64) closure over the composition root's own state (compose_wellbeing.go) can never be serialized (funcs are not JSON-marshallable) and would be meaningless after a restore anyway, mirroring termInputs'/citizens'/finance's/households' identical wired-dependency exclusion reasoning: compose re-wires this seam every load, BEFORE the first ApplyMigration call, so a stale/missing post-load value is always overwritten before it can influence a migration decision.",
	}
	covered := map[string]bool{
		"reputation":           true,
		"lastAdvancedMonth":    true,
		"hasAdvanced":          true,
		"nextMigrantID":        true,
		"migrantAdmittedMonth": true, // BUG-380 tenure-grace map, attractMetaWire.MigrantAdmittedMonths
	}
	at := reflect.TypeOf((*AttractAPI)(nil)).Elem()
	for i := 0; i < at.NumField(); i++ {
		name := at.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("AttractAPI field %q is neither serialized (add it to attractMetaWire) nor explicitly excluded (add it to the excluded allowlist with a reason) -- AC-2 forbids a silently-unsaved field", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("AttractAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Shared driver + comparison helpers.
// ---------------------------------------------------------------------------

func ckA(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// driveAttract runs a fixed, deterministic three-month sequence exercising
// every serialized field: the pushed terms vary month to month so
// reputation's asymmetric momentum (rise vs fall) actually moves away from
// its zero start (a constant fundamentals input would leave reputation
// pinned at zero forever, which would make the round-trip proof vacuous for
// that field); every month's positive attractiveness gap (A > A_world=50)
// admits migrant households, so nextMigrantID advances measurably each
// call; lastAdvancedMonth/hasAdvanced track the final month processed. No
// RNG anywhere (engine.attract has no foundation/det import).
func driveAttract(t *testing.T, a *AttractAPI) {
	t.Helper()
	terms := []float64{80, 60, 90}
	for i, term := range terms {
		month := int64(i + 1)
		ckA(t, a.SetTermInputs(TermInputs{
			JobAvailability:        term,
			ServiceCoverage:        term,
			Environment:            term,
			LeisureFit:             term,
			Safety:                 term,
			HouseholdIDs:           nil, // empty set -> HousingAffordability reads 100 (households.affordabilityIndex's total==0 branch)
			MonthlyRentMicroPounds: 0,
		}))
		_, err := a.ApplyMigration(MigrationCommand{
			Month:              month,
			ResidentIDs:        nil,
			HousingVacancy:     1000,
			JunctionThroughput: 1000,
		})
		ckA(t, err)
	}
}

// compareAttractInternal asserts a and b hold IDENTICAL persisted state --
// the four fields attractMetaWire covers, read directly (this is a
// white-box package test, so unexported fields are fair game, mirroring
// finance/participant_test.go's direct field pokes in its prove-can-fail
// steps). Also asserts the public Reputation() accessor agrees, since that
// is the load-bearing observable a caller outside this package actually
// sees.
func compareAttractInternal(t *testing.T, a, b *AttractAPI, label string) {
	t.Helper()
	a.mu.RLock()
	aRep, aLast, aHasAdv, aNextID := a.reputation, a.lastAdvancedMonth, a.hasAdvanced, a.nextMigrantID
	aTenure := make(map[uint64]int64, len(a.migrantAdmittedMonth))
	for k, v := range a.migrantAdmittedMonth {
		aTenure[k] = v
	}
	a.mu.RUnlock()
	b.mu.RLock()
	bRep, bLast, bHasAdv, bNextID := b.reputation, b.lastAdvancedMonth, b.hasAdvanced, b.nextMigrantID
	bTenure := make(map[uint64]int64, len(b.migrantAdmittedMonth))
	for k, v := range b.migrantAdmittedMonth {
		bTenure[k] = v
	}
	b.mu.RUnlock()

	if aRep != bRep {
		t.Fatalf("%s: reputation %+v != %+v", label, aRep, bRep)
	}
	if aLast != bLast {
		t.Fatalf("%s: lastAdvancedMonth %d != %d", label, aLast, bLast)
	}
	if aHasAdv != bHasAdv {
		t.Fatalf("%s: hasAdvanced %v != %v", label, aHasAdv, bHasAdv)
	}
	if aNextID != bNextID {
		t.Fatalf("%s: nextMigrantID %d != %d", label, aNextID, bNextID)
	}
	if !reflect.DeepEqual(aTenure, bTenure) {
		t.Fatalf("%s: migrantAdmittedMonth %+v != %+v", label, aTenure, bTenure)
	}
	if a.Reputation() != b.Reputation() {
		t.Fatalf("%s: Reputation() %v != %v", label, a.Reputation(), b.Reputation())
	}
}

// saveInto drives a save of a's participant into a fresh bundle under a
// temp root and returns the bundle root.
func saveInto(t *testing.T, a *AttractAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(a)}, cid)
	ctx := save.Context{WorldSeed: 7, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-build"}
	ckA(t, mgr.SaveManual(ctx, "det"))
	return root
}

// manualBundleDir locates the single manual-save bundle directory under a
// save root, mirroring finance/participant_test.go's own helper.
func manualBundleDir(t *testing.T, root string) string {
	t.Helper()
	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == "header.json" {
			found = filepath.Dir(path)
		}
		return nil
	})
	ckA(t, err)
	if found == "" {
		t.Fatalf("no bundle (header.json) found under %q", root)
	}
	return found
}

// allFiles returns every file under dir, relative to dir, sorted.
func allFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	ckA(t, err)
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// AC-5 — round-trip determinism (the bar).
// ---------------------------------------------------------------------------

func TestAttractParticipant_RoundTrip(t *testing.T) {
	orig, _, _, _ := newAPI(t, validConfig())
	driveAttract(t, orig)

	// Precondition: the drive actually moved every persisted field off its
	// fresh-construction zero value, or this whole test proves nothing.
	orig.mu.RLock()
	if !orig.reputation.hasBaseline || orig.reputation.value == 0 {
		orig.mu.RUnlock()
		t.Fatalf("test setup: driveAttract did not move reputation off its zero start")
	}
	if !orig.hasAdvanced || orig.lastAdvancedMonth != 3 {
		orig.mu.RUnlock()
		t.Fatalf("test setup: driveAttract did not advance to month 3")
	}
	if orig.nextMigrantID <= 1 {
		orig.mu.RUnlock()
		t.Fatalf("test setup: driveAttract did not mint any migrants (nextMigrantID=%d)", orig.nextMigrantID)
	}
	orig.mu.RUnlock()

	root := saveInto(t, orig, "orig")

	// Load into a FRESH AttractAPI (mirrors a composition root reconstructing
	// the module before Load, per NewSaveParticipant's doc comment).
	reloaded, reloadedCA, _, _ := newAPI(t, validConfig())

	// BUG-380 re-round (opus-reround-bug380): seed reloadedCA with a
	// matching cold record for every id orig's tenure map already holds,
	// BEFORE Load runs. sweepDepartedMigrantTenure (migration.go) — called
	// at the top of every ApplyMigration, including continueAttract's call
	// below — checks CitizenAt against whichever CitizensAPI is wired, and
	// prunes any tenure entry that does not resolve. In the REAL system, a
	// composition root's LoadAt restores attract's participant AND
	// citizens' own participant from the SAME save bundle, so the two
	// always agree on who is still alive; this test's default (newAPI
	// gives orig and reloaded independent, disconnected CitizensAPI
	// instances, since it only exercises attract's OWN participant) would
	// otherwise make the sweep see EVERY migrant orig ever admitted as
	// "not found" in reloaded's empty store and prune the whole tenure map
	// on reloaded's very first post-load ApplyMigration call — a
	// test-environment artifact, not a real defect (proved separately at
	// the composition level, with a genuinely SHARED citizens store, by
	// compose's TestAttack380_SaveRestoreMidTenureDifferential and
	// TestReround380_SaveRestoreAfterPruningDifferential). Seeding into a
	// SEPARATE store (rather than sharing origCA directly) also avoids a
	// real id collision: reloaded's own nextMigrantID counter restores to
	// the SAME post-driveAttract value orig's had, so continueAttract's
	// later independent mint on each would collide if they wrote into one
	// shared store.
	orig.mu.RLock()
	seedRecords := make([]citizens.ColdRecord, 0, len(orig.migrantAdmittedMonth))
	for id := range orig.migrantAdmittedMonth {
		seedRecords = append(seedRecords, mkResident(id, 50))
	}
	orig.mu.RUnlock()
	if len(seedRecords) == 0 {
		t.Fatalf("test setup: orig has no tenure entries to mirror into reloadedCA")
	}
	if err := reloadedCA.SeedColdRecords(seedRecords, "corr-attract"); err != nil {
		t.Fatalf("SeedColdRecords(reloadedCA): %v", err)
	}

	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(reloaded)}, "reloaded")
	_, _, err := mgr.Load(manualBundleDir(t, root))
	ckA(t, err)

	compareAttractInternal(t, orig, reloaded, "post-load")

	// Continue identical migration postings on BOTH and assert they stay
	// equal (AC-5e): a divergent restore surfaces the moment new work
	// builds on it -- this is the exact property
	// TestLoadAt_TickContinuity/TestLoadAt_KnownLimitation... in
	// compose/save_loadat_test.go exercise at the composition level.
	continueAttract(t, orig)
	continueAttract(t, reloaded)
	compareAttractInternal(t, orig, reloaded, "post-continue")

	// Prove-can-fail: mutate one reloaded field directly -> divergence,
	// proving compareAttractInternal has teeth rather than vacuously
	// passing. Compared directly here (not via compareAttractInternal,
	// which calls t.Fatalf on the very divergence this step deliberately
	// introduces).
	reloaded2, _, _, _ := newAPI(t, validConfig())
	mgr2 := save.NewManager(root, []save.Participant{NewSaveParticipant(reloaded2)}, "reloaded2")
	_, _, err = mgr2.Load(manualBundleDir(t, root))
	ckA(t, err)
	reloaded2.mu.Lock()
	reloaded2.reputation.value += 1
	reloaded2.mu.Unlock()
	if reloaded2.Reputation() == orig.Reputation() {
		t.Fatalf("prove-can-fail: mutating a reloaded participant's reputation.value did not diverge from the original -- the comparison has no teeth")
	}
}

// continueAttract runs one more deterministic month, driving a new posting
// so a post-load state that silently diverged would show up as unequal
// totals.
func continueAttract(t *testing.T, a *AttractAPI) {
	t.Helper()
	ckA(t, a.SetTermInputs(TermInputs{
		JobAvailability:        70,
		ServiceCoverage:        70,
		Environment:            70,
		LeisureFit:             70,
		Safety:                 70,
		HouseholdIDs:           nil,
		MonthlyRentMicroPounds: 0,
	}))
	_, err := a.ApplyMigration(MigrationCommand{
		Month:              4,
		ResidentIDs:        nil,
		HousingVacancy:     1000,
		JunctionThroughput: 1000,
	})
	ckA(t, err)
}

// ---------------------------------------------------------------------------
// AC-3 — byte determinism.
// ---------------------------------------------------------------------------

func TestAttractParticipant_ByteDeterminism(t *testing.T) {
	a1, _, _, _ := newAPI(t, validConfig())
	driveAttract(t, a1)
	root1 := saveInto(t, a1, "run1")

	a2, _, _, _ := newAPI(t, validConfig())
	driveAttract(t, a2)
	root2 := saveInto(t, a2, "run2")

	dir1 := manualBundleDir(t, root1)
	dir2 := manualBundleDir(t, root2)

	files1 := allFiles(t, dir1)
	files2 := allFiles(t, dir2)
	if len(files1) == 0 {
		t.Fatalf("test setup: bundle %q has no files", dir1)
	}
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("bundle file sets differ: run1=%v run2=%v", files1, files2)
	}
	for _, rel := range files1 {
		b1, err := os.ReadFile(filepath.Join(dir1, rel))
		ckA(t, err)
		b2, err := os.ReadFile(filepath.Join(dir2, rel))
		ckA(t, err)
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic attract state (correlation ID differs by design and is NOT persisted)", rel)
		}
	}
}

// ---------------------------------------------------------------------------
// FEAT-169-class regression: nextMigrantID must round-trip EXACTLY, so
// citizen ids minted after a restore never collide with ids minted before
// the save.
// ---------------------------------------------------------------------------

// TestAttractParticipant_NextMigrantIDContinuesWithoutCollision proves the
// deterministic migrant-id counter survives a save/load and that every id
// minted AFTER the restore is disjoint from every id minted BEFORE the
// save that produced it -- the exact silent-collision class FEAT-169 found
// (attract's own migrantIDHighBit colliding with citizens' fertility-child
// range) and that a naive "reset the counter to 1 on load" bug would
// reintroduce here.
func TestAttractParticipant_NextMigrantIDContinuesWithoutCollision(t *testing.T) {
	orig, _, _, _ := newAPI(t, validConfig())

	preSaveIDs := map[uint64]bool{}
	for i := 0; i < 3; i++ {
		id := orig.mintMigrantID()
		if preSaveIDs[id] {
			t.Fatalf("test setup: pre-save mintMigrantID produced a duplicate id %d", id)
		}
		preSaveIDs[id] = true
	}
	orig.mu.RLock()
	savedCounter := orig.nextMigrantID
	orig.mu.RUnlock()

	root := saveInto(t, orig, "orig")

	reloaded, _, _, _ := newAPI(t, validConfig())
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(reloaded)}, "reloaded")
	_, _, err := mgr.Load(manualBundleDir(t, root))
	ckA(t, err)

	reloaded.mu.RLock()
	restoredCounter := reloaded.nextMigrantID
	reloaded.mu.RUnlock()
	if restoredCounter != savedCounter {
		t.Fatalf("nextMigrantID did not round-trip exactly: saved=%d restored=%d", savedCounter, restoredCounter)
	}

	// Mint post-restore ids and assert every one is disjoint from every
	// pre-save id -- the collision proof.
	for i := 0; i < 5; i++ {
		id := reloaded.mintMigrantID()
		if preSaveIDs[id] {
			t.Fatalf("prove-can-fail: post-restore mintMigrantID minted id %d, which COLLIDES with a pre-save migrant id -- the counter did not continue from the saved value", id)
		}
	}

	// Prove-can-fail (positive control): a counter that WAS naively reset to
	// 1 on load would immediately re-mint the very first pre-save id --
	// verify that specific failure mode is what this test would have caught.
	fresh, _, _, _ := newAPI(t, validConfig())
	firstEverID := fresh.mintMigrantID()
	if !preSaveIDs[firstEverID] {
		t.Fatalf("test setup: a freshly-constructed AttractAPI's first minted id (%d) is not in the pre-save set (%v) -- the collision scenario this test guards against is not actually reachable by a reset-to-1 bug", firstEverID, preSaveIDs)
	}
}

// TestAttractParticipant_OldSaveBackfillsMigrantTenure is BUG-380's
// backward-compatibility proof: a save record taken BEFORE the tenure-grace
// field existed (no "migrantAdmittedMonths" key at all -- hand-built here,
// mirroring exactly what an old attractMetaWire JSON blob looks like) must
// decode WITHOUT error, and applyLoadRecord's backfill (participant.go)
// must install every REAL migrant id implied by the restored nextMigrantID
// counter at the record's own LastAdvancedMonth -- never left ungated
// (which would make an old save's migrants instantly emigration-eligible
// with no grace at all) and never a decode failure.
func TestAttractParticipant_OldSaveBackfillsMigrantTenure(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())

	// A hand-built OLD-SHAPE record: NextMigrantID=4 (i.e. two real migrant
	// pairs minted, ids base+2..base+4) but no migrantAdmittedMonths key at
	// all -- exactly what json.Marshal of a pre-BUG-380 attractMetaWire
	// produced.
	oldRec := serialize.Record{Kind: recAttractMeta, Data: []byte(`{"reputation":{"hasBaseline":true,"baseline":5,"value":6},"lastAdvancedMonth":9,"hasAdvanced":true,"nextMigrantID":4}`)}
	if err := NewSaveParticipant(a).Handler()(oldRec); err != nil {
		t.Fatalf("Handler rejected an old-shape (pre-BUG-380) record: %v", err)
	}

	a.mu.RLock()
	tenure := make(map[uint64]int64, len(a.migrantAdmittedMonth))
	for k, v := range a.migrantAdmittedMonth {
		tenure[k] = v
	}
	a.mu.RUnlock()

	wantIDs := []uint64{migrantIDHighBit | 2, migrantIDHighBit | 3, migrantIDHighBit | 4}
	if len(tenure) != len(wantIDs) {
		t.Fatalf("backfilled migrantAdmittedMonth has %d entries, want %d (%v): got %v", len(tenure), len(wantIDs), wantIDs, tenure)
	}
	for _, id := range wantIDs {
		month, ok := tenure[id]
		if !ok {
			t.Fatalf("backfill missing real migrant id %d (nextMigrantID=4 implies ids base+2..base+4 exist): %v", id, tenure)
		}
		if month != 9 {
			t.Fatalf("id %d backfilled at month %d, want the record's own lastAdvancedMonth (9)", id, month)
		}
	}

	// The backfilled tenure must actually GATE emigration: at the backfill
	// month itself (9), the migrant is still within a fresh 12-month grace
	// counted from 9 -- migrantBelowTenureGrace(id, 9) must be true (tenure
	// 0 < 12), never instantly eligible just because it was backfilled
	// rather than recorded at real admission time.
	if !a.migrantBelowTenureGrace(wantIDs[0], 9) {
		t.Fatalf("a backfilled migrant is NOT gated at its own backfill month (tenure 0) -- an old save's migrants would be instantly emigration-eligible with no grace at all")
	}
	if a.migrantBelowTenureGrace(wantIDs[0], 9+migrantTenureGraceMonths) {
		t.Fatalf("a backfilled migrant is STILL gated once tenure clears migrantTenureGraceMonths (%d) from its backfilled month -- the backfill must behave exactly like a real admission from that point on", migrantTenureGraceMonths)
	}
}
