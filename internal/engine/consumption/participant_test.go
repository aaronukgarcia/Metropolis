package consumption

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// ---------------------------------------------------------------------------
// FEAT-1972079941 inc7 — engine.consumption save.Participant tests.
//
// Mirrors the inc4 engine.market participant suite, adapted to the pivotal
// finding for THIS module: engine.consumption's UtilityAPI is a STATELESS,
// immutable query surface (api.go's AC-17 note). It has no mutable runtime
// state to persist — no accumulated counter, cached solve, or id. The real
// durable state in this package (Network.lastSolve — derived; an
// over-abstracted AquiferYield.current — durable) lives on caller-owned
// Network/AquiferYield VALUES held by the composition root, not on
// UtilityAPI. See participant.go's design note.
//
// Consequences for the five mandatory test shapes:
//  1. Field-parity drift  — PRESENT and the primary deliverable: every
//     UtilityAPI field is classified as excluded config/injected-dep/
//     correlation; the moment a mutable field is added without serialization
//     the build reddens.
//  2. Round-trip + continue-on-both + prove-can-fail — round-trip PRESENT
//     (a save/load must not corrupt the config the queries read); the
//     per-scalar prove-can-fail / distinct-non-zero-value drops are N/A
//     because there are NO mutable scalars to drop (documented below), and
//     faking state to manufacture one would be dishonest. The sharpest tooth
//     available is the record-count/shape of the emitted stream.
//  3. Byte-determinism — PRESENT (two saves are byte-identical).
//  4. Load-into-non-empty full-replace — PRESENT (a load into a
//     config-populated UtilityAPI leaves the config intact and queryable).
//  5. Copyguard-fires + unknown-record-kind — unknown-record-kind PRESENT;
//     copyguard is N/A: UtilityAPI holds no sync.Locker, so there is no
//     copied-mutex hazard to guard. The fail-closed behaviour proven instead
//     is the corrupt-payload decode path.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// (1) Field-parity drift tests — the "built but not serialized" guard, and
// the whole reason this stateless module still registers a Participant.
// ---------------------------------------------------------------------------

// TestUtilityAPIFieldsAllClassified fails the build if any UtilityAPI field is
// neither serialized (covered) nor explicitly excluded (immutable config /
// injected dependency / per-instance correlation). engine.consumption is
// stateless today, so EVERY field is excluded and covered is empty — but the
// instant a mutable field (e.g. an accumulated billing counter, or the
// compose root's Networks moved to live inside UtilityAPI) is added without
// being serialized, it lands in neither map and this reddens. That is exactly
// the "built but not serialized" class this inc exists to prevent.
func TestUtilityAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"consumption":   "immutable config, the coefficient model loaded once from data/consumption.json and never mutated after construction (AC-17); reloaded from the current data file on load, never restored from a save",
		"season":        "injected dependency (*season.SeasonAPI), re-wired by the composition root on load; not simulation state this module owns",
		"market":        "injected dependency (*market.MarketAPI), re-wired by the composition root on load; not simulation state this module owns",
		"correlationID": "per-instance error correlation, not simulation state",
	}
	// Covered: serialized via a wire record. EMPTY today — engine.consumption
	// has no mutable runtime state (this is the finding, asserted below too).
	covered := map[string]bool{}

	ut := reflect.TypeOf((*UtilityAPI)(nil)).Elem()
	for i := 0; i < ut.NumField(); i++ {
		name := ut.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("UtilityAPI field %q is neither serialized (add it to a wire record) nor explicitly excluded (add it to the excluded allowlist with a reason) -- the 'built but not serialized' class this inc forbids. If engine.consumption has gained mutable runtime state (a billing counter, a cached solve, an id, or an owned Network), it must now be serialized like finance/unlocks/build/refuse/traffic.", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("UtilityAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

// TestConsumptionMetaWireMatchesSerializedScalars documents-and-enforces the
// pivotal finding: UtilityAPI carries no serialized (covered) scalar, and the
// meta wire is correspondingly empty. If a mutable scalar is ever added to the
// save, the covered set in TestUtilityAPIFieldsAllClassified grows and this
// count must be updated in lockstep -- so a scalar added to
// consumptionMetaWire without a matching serialized UtilityAPI field (or
// vice-versa) is caught here.
func TestConsumptionMetaWireMatchesSerializedScalars(t *testing.T) {
	const serializedScalarCount = 0 // engine.consumption is stateless (v1)
	mw := reflect.TypeOf((*consumptionMetaWire)(nil)).Elem()
	if mw.NumField() != serializedScalarCount {
		t.Fatalf("consumptionMetaWire has %d fields but %d serialized scalars are expected -- meta wire drifted from the (empty) scalar set; if consumption gained mutable state, update serializedScalarCount AND the covered map in TestUtilityAPIFieldsAllClassified", mw.NumField(), serializedScalarCount)
	}
}

// ---------------------------------------------------------------------------
// Shared save/load drivers.
// ---------------------------------------------------------------------------

// saveInto drives a save of a's participant into a fresh bundle under a temp
// root and returns the bundle root directory.
func saveInto(t *testing.T, a *UtilityAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(a)}, cid)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-consumption"}
	if err := mgr.SaveManual(ctx, "det"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	return root
}

// loadInto loads the single manual bundle under root into a.
func loadInto(t *testing.T, root string, a *UtilityAPI, cid string) {
	t.Helper()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(a)}, cid)
	if _, _, err := mgr.Load(manualBundleDir(t, root)); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// demandProbe is a fixed set of pure query inputs used to prove the
// config-driven demand model still answers identically after a save/load.
// MonthIndex 2 (March) keeps every seasonal multiplier at 1.0 so the probe is
// stable, and the two GasNetworkPresent variants exercise the all-electric
// reroute branch as well.
var demandProbeOpts = []DemandOptions{
	{MonthIndex: 2, GasNetworkPresent: true},
	{MonthIndex: 2, GasNetworkPresent: false},
}

// assertConfigIntact asserts every spec-transcribed, config-driven query
// still answers exactly as the reference API does -- the property a
// consumption save/load must never break (the config is authoritative and
// must survive untouched). It walks the residential baseline, the wastewater
// fraction, and EVERY §17.2 class coefficient row (sorted, GR#21), plus a
// full ClassDemand/ResidentialDemand computation under both gas-network
// variants.
func assertConfigIntact(t *testing.T, got, want *UtilityAPI, label string) {
	t.Helper()

	if got.ResidentialBaseline() != want.ResidentialBaseline() {
		t.Fatalf("%s: ResidentialBaseline diverged: got %+v want %+v", label, got.ResidentialBaseline(), want.ResidentialBaseline())
	}
	if got.WastewaterFraction() != want.WastewaterFraction() {
		t.Fatalf("%s: WastewaterFraction diverged: got %v want %v", label, got.WastewaterFraction(), want.WastewaterFraction())
	}

	// Every class coefficient row, in sorted key order (GR#21).
	refs := make([]string, 0, len(want.consumption.Classes))
	for ref := range want.consumption.Classes {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	if len(refs) == 0 {
		t.Fatalf("%s: test setup: reference API has no §17.2 classes to compare", label)
	}
	if len(got.consumption.Classes) != len(want.consumption.Classes) {
		t.Fatalf("%s: class map size %d != %d", label, len(got.consumption.Classes), len(want.consumption.Classes))
	}
	for _, ref := range refs {
		wCoef, wErr := want.ClassCoefficients(ref)
		gCoef, gErr := got.ClassCoefficients(ref)
		if (wErr == nil) != (gErr == nil) || wCoef != gCoef {
			t.Fatalf("%s: ClassCoefficients(%q) diverged: got (%+v,%v) want (%+v,%v)", label, ref, gCoef, gErr, wCoef, wErr)
		}
		for _, opts := range demandProbeOpts {
			wd, wErr := want.ClassDemand(ref, 7, opts)
			gd, gErr := got.ClassDemand(ref, 7, opts)
			if (wErr == nil) != (gErr == nil) || wd != gd {
				t.Fatalf("%s: ClassDemand(%q,7,%+v) diverged: got (%+v,%v) want (%+v,%v)", label, ref, opts, gd, gErr, wd, wErr)
			}
		}
	}

	// Residential per-person baseline under both gas-network variants.
	for _, opts := range demandProbeOpts {
		wd, wErr := want.ResidentialDemand(1000, opts)
		gd, gErr := got.ResidentialDemand(1000, opts)
		if (wErr == nil) != (gErr == nil) || wd != gd {
			t.Fatalf("%s: ResidentialDemand(1000,%+v) diverged: got (%+v,%v) want (%+v,%v)", label, opts, gd, gErr, wd, wErr)
		}
	}
}

// ---------------------------------------------------------------------------
// (2) Round-trip: a save + load must leave the config-driven queries intact.
// ---------------------------------------------------------------------------

func TestConsumptionParticipant_RoundTrip(t *testing.T) {
	orig := realAPI(t)
	root := saveInto(t, orig, "orig")

	// Load into a FRESH UtilityAPI (same data/consumption.json).
	// engine.consumption is stateless, so the load streams the (empty) meta
	// record and must leave the reloaded API's config fully intact and
	// queryable.
	reloaded := realAPI(t)
	loadInto(t, root, reloaded, "reloaded")

	assertConfigIntact(t, reloaded, orig, "post-load")

	// Continue-on-both: the same queries on both must stay identical -- a
	// divergent restore would surface here.
	assertConfigIntact(t, orig, reloaded, "post-load-symmetric")

	// The load must have consumed exactly one (meta) record: prove the source
	// emits precisely one record, then exhausts. This is the stateless
	// module's analogue of the stateful "prove-can-fail per scalar" drops --
	// there are no mutable scalars to drop, so the sharpest available tooth is
	// the record-count/shape of the stream itself.
	src := NewSaveParticipant(orig).Source()
	rec, ok, err := src()
	if err != nil || !ok {
		t.Fatalf("Source first pull: rec=%v ok=%v err=%v, want one record", rec, ok, err)
	}
	if rec.Kind != recConsumptionMeta {
		t.Fatalf("first record kind = %q, want %q", rec.Kind, recConsumptionMeta)
	}
	if _, ok, err := src(); ok || err != nil {
		t.Fatalf("Source second pull: ok=%v err=%v, want exhaustion (ok=false,err=nil)", ok, err)
	}
}

// ---------------------------------------------------------------------------
// (3) Byte determinism.
// ---------------------------------------------------------------------------

func TestConsumptionParticipant_ByteDeterminism(t *testing.T) {
	a1 := realAPI(t)
	root1 := saveInto(t, a1, "run1")

	a2 := realAPI(t)
	root2 := saveInto(t, a2, "run2")

	// The correlation ID differs between the two saves by design and is NOT
	// persisted, so the bundles must be byte-identical.
	assertBundlesByteIdentical(t, root1, root2)
}

// ---------------------------------------------------------------------------
// (4) Load-into-non-empty (full replace, not merge / not corrupt).
// ---------------------------------------------------------------------------

// TestAttack_LoadIntoNonEmptyPreservesConfig: a Load into a UtilityAPI that is
// already fully populated with config must leave every config-driven query
// answering correctly (Handler's reset ran, the meta record applied, and the
// immutable config was neither dropped nor merged into garbage).
func TestAttack_LoadIntoNonEmptyPreservesConfig(t *testing.T) {
	orig := realAPI(t)
	root := saveInto(t, orig, "orig")

	// Target already holds the full config (a fresh real load).
	target := realAPI(t)
	if len(target.consumption.Classes) == 0 {
		t.Fatalf("test setup: target class map not populated")
	}
	beforeClasses := len(target.consumption.Classes)

	loadInto(t, root, target, "target")

	// Config must be intact and identical to the reference after the load.
	assertConfigIntact(t, target, orig, "load-into-nonempty")
	if len(target.consumption.Classes) != beforeClasses {
		t.Fatalf("load corrupted the class map size: %d != %d", len(target.consumption.Classes), beforeClasses)
	}
}

// ---------------------------------------------------------------------------
// (5) Unknown-record-kind rejection (+ copyguard note).
// ---------------------------------------------------------------------------

// TestAttack_UnknownRecordKindRejected: an unrecognised record kind fails
// loud and closed, never a silent partial load.
func TestAttack_UnknownRecordKindRejected(t *testing.T) {
	a := realAPI(t)
	h := NewSaveParticipant(a).Handler()
	if err := h(serialize.Record{Kind: "consumption.bogus", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("Handler accepted an unknown record kind -- want a loud error")
	}
}

// TestAttack_CorruptMetaRecordRejected: a malformed meta payload must be a
// hard decode error, never a silent success. (The stateless module's analogue
// of the stateful copyguard-fires test: since UtilityAPI holds no mutex there
// is no copied-mutex hazard to guard, so the fail-closed behaviour proven here
// is the decode path instead.)
func TestAttack_CorruptMetaRecordRejected(t *testing.T) {
	a := realAPI(t)
	h := NewSaveParticipant(a).Handler()
	if err := h(serialize.Record{Kind: recConsumptionMeta, Data: []byte(`{not json`)}); err == nil {
		t.Fatalf("Handler accepted a corrupt meta payload -- want a loud decode error")
	}
}

// TestParticipantContractBasics: the participant reports the stable kind and a
// well-formed single-record source, and a well-formed meta record loads
// cleanly.
func TestParticipantContractBasics(t *testing.T) {
	a := realAPI(t)
	p := NewSaveParticipant(a)
	if p.Kind() != KindConsumption {
		t.Fatalf("Kind() = %q, want %q", p.Kind(), KindConsumption)
	}
	// A well-formed empty meta record loads without error.
	h := p.Handler()
	if err := h(serialize.Record{Kind: recConsumptionMeta, Data: []byte(`{}`)}); err != nil {
		t.Fatalf("Handler rejected a well-formed empty meta record: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Bundle byte-comparison helpers (mirroring the finance/market pilots).
// ---------------------------------------------------------------------------

func assertBundlesByteIdentical(t *testing.T, root1, root2 string) {
	t.Helper()
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
		if err != nil {
			t.Fatalf("read %q: %v", rel, err)
		}
		b2, err := os.ReadFile(filepath.Join(dir2, rel))
		if err != nil {
			t.Fatalf("read %q: %v", rel, err)
		}
		if string(b1) != string(b2) {
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic consumption state (correlation ID differs by design and is NOT persisted)", rel)
		}
	}
}

// manualBundleDir locates the single manual-save bundle directory under a
// save root by finding the header.json leaf.
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
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
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
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(out)
	return out
}
