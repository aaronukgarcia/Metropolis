package market

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
// FEAT-1972079941 inc4 — engine.market save.Participant tests.
//
// Mirrors the inc1/inc2/inc3 (finance/unlocks/build) participant suites,
// adapted to the pivotal finding for THIS module: engine.market is a
// STATELESS, immutable query surface (AC-14 / AC-18 / ASM-489). It has no
// mutable runtime state to persist — no clearing price, stock level, or id
// counter. See participant.go's design note.
//
// Consequences for the five mandatory test shapes:
//   1. Field-parity drift  — PRESENT and the primary deliverable: every
//      MarketAPI field is classified as excluded config/correlation; the
//      moment a mutable field is added without serialization the build reddens.
//   2. Round-trip + continue-on-both + prove-can-fail — round-trip PRESENT
//      (a save/load must not corrupt the config the queries read); the
//      per-scalar prove-can-fail / distinct-non-zero-value drops are N/A
//      because there are NO mutable scalars to drop (documented below), and
//      faking state to manufacture one would be dishonest.
//   3. Byte-determinism — PRESENT (two saves are byte-identical).
//   4. Load-into-non-empty full-replace — PRESENT (a load into a
//      config-populated MarketAPI leaves the config intact and queryable).
//   5. Copyguard-fires + unknown-record-kind — unknown-record-kind PRESENT;
//      copyguard is N/A: MarketAPI holds no sync.Locker, so there is no
//      copied-mutex hazard to guard (unlike finance/unlocks/build).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// (1) Field-parity drift tests — the "built but not serialized" guard, and
// the whole reason this stateless module still registers a Participant.
// ---------------------------------------------------------------------------

// TestMarketAPIFieldsAllClassified fails the build if any MarketAPI field is
// neither serialized (covered) nor explicitly excluded (immutable config /
// per-instance correlation). engine.market is stateless today, so EVERY
// field is excluded and covered is empty — but the instant a mutable field
// (e.g. a dynamic clearing price or a stock level) is added to MarketAPI
// without being serialized, it lands in neither map and this reddens. That
// is exactly the "built but not serialized" class this inc exists to prevent.
func TestMarketAPIFieldsAllClassified(t *testing.T) {
	excluded := map[string]string{
		"commodities":   "immutable config, loaded once from data/market.json and never mutated after construction (AC-14); reloaded from the current data file on load, never restored from a save",
		"pricingMode":   "immutable config (static v1, AC-4/AC-18); a dynamic world price lives in the separate feat.commoditymarket surface, not here",
		"correlationID": "per-instance error correlation, not simulation state",
	}
	// Covered: serialized via a wire record. EMPTY today — engine.market has
	// no mutable runtime state (this is the finding, asserted below too).
	covered := map[string]bool{}

	mt := reflect.TypeOf((*MarketAPI)(nil)).Elem()
	for i := 0; i < mt.NumField(); i++ {
		name := mt.Field(i).Name
		_, isExcluded := excluded[name]
		if !isExcluded && !covered[name] {
			t.Fatalf("MarketAPI field %q is neither serialized (add it to a wire record) nor explicitly excluded (add it to the excluded allowlist with a reason) -- the 'built but not serialized' class this inc forbids. If engine.market has gained mutable runtime state (a clearing price, a stock level, an id counter), it must now be serialized like finance/unlocks/build.", name)
		}
		if isExcluded && covered[name] {
			t.Fatalf("MarketAPI field %q is listed as BOTH excluded and covered -- pick one", name)
		}
	}
}

// TestMarketIsStateless documents-and-enforces the pivotal finding: MarketAPI
// carries no serialized (covered) scalar, and the meta wire is correspondingly
// empty. If a mutable scalar is ever added to the save, the covered set in
// TestMarketAPIFieldsAllClassified grows and this count must be updated in
// lockstep -- so a scalar added to marketMetaWire without a matching serialized
// MarketAPI field (or vice-versa) is caught here.
func TestMarketMetaWireMatchesSerializedScalars(t *testing.T) {
	const serializedScalarCount = 0 // engine.market is stateless (v1)
	mw := reflect.TypeOf((*marketMetaWire)(nil)).Elem()
	if mw.NumField() != serializedScalarCount {
		t.Fatalf("marketMetaWire has %d fields but %d serialized scalars are expected -- meta wire drifted from the (empty) scalar set; if market gained mutable state, update serializedScalarCount AND the covered map in TestMarketAPIFieldsAllClassified", mw.NumField(), serializedScalarCount)
	}
}

// ---------------------------------------------------------------------------
// Shared save/load drivers.
// ---------------------------------------------------------------------------

// saveInto drives a save of m's participant into a fresh bundle under a temp
// root and returns the bundle root directory.
func saveInto(t *testing.T, m *MarketAPI, cid string) string {
	t.Helper()
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(m)}, cid)
	ctx := save.Context{WorldSeed: 42, CreatedAtTick: 100, GameMonth: 3, AppVersion: "test-market"}
	if err := mgr.SaveManual(ctx, "det"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	return root
}

// loadInto loads the single manual bundle under root into m.
func loadInto(t *testing.T, root string, m *MarketAPI, cid string) {
	t.Helper()
	mgr := save.NewManager(root, []save.Participant{NewSaveParticipant(m)}, cid)
	if _, _, err := mgr.Load(manualBundleDir(t, root)); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// assertConfigIntact asserts every commodity's spec-transcribed query still
// answers exactly as the reference API does -- the property a market save/load
// must never break (the config is authoritative and must survive untouched).
func assertConfigIntact(t *testing.T, got, want *MarketAPI, label string) {
	t.Helper()
	if len(got.commodities) != len(want.commodities) {
		t.Fatalf("%s: registry size %d != %d", label, len(got.commodities), len(want.commodities))
	}
	for _, c := range allCommodities {
		wMode, wErr := want.SupplyMode(c)
		gMode, gErr := got.SupplyMode(c)
		if (wErr == nil) != (gErr == nil) || wMode != gMode {
			t.Fatalf("%s: SupplyMode(%q) diverged: got (%v,%v) want (%v,%v)", label, c, gMode, gErr, wMode, wErr)
		}
		if c == Waste {
			wp, wErr := want.ExportPrice(c)
			gp, gErr := got.ExportPrice(c)
			if (wErr == nil) != (gErr == nil) || wp != gp {
				t.Fatalf("%s: ExportPrice(%q) diverged: got (%v,%v) want (%v,%v)", label, c, gp, gErr, wp, wErr)
			}
			continue
		}
		wp, wErr := want.Price(c)
		gp, gErr := got.Price(c)
		if (wErr == nil) != (gErr == nil) || wp != gp {
			t.Fatalf("%s: Price(%q) diverged: got (%v,%v) want (%v,%v)", label, c, gp, gErr, wp, wErr)
		}
		wa, wErr := want.Availability(c, 7)
		ga, gErr := got.Availability(c, 7)
		if (wErr == nil) != (gErr == nil) || wa != ga {
			t.Fatalf("%s: Availability(%q,7) diverged: got (%v,%v) want (%v,%v)", label, c, ga, gErr, wa, wErr)
		}
	}
}

// ---------------------------------------------------------------------------
// (2) Round-trip: a save + load must leave the config-driven queries intact.
// ---------------------------------------------------------------------------

func TestMarketParticipant_RoundTrip(t *testing.T) {
	orig := realAPI(t)
	root := saveInto(t, orig, "orig")

	// Load into a FRESH MarketAPI (same data/market.json). engine.market is
	// stateless, so the load streams the (empty) meta record and must leave
	// the reloaded API's config fully intact and queryable.
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
	if rec.Kind != recMarketMeta {
		t.Fatalf("first record kind = %q, want %q", rec.Kind, recMarketMeta)
	}
	if _, ok, err := src(); ok || err != nil {
		t.Fatalf("Source second pull: ok=%v err=%v, want exhaustion (ok=false,err=nil)", ok, err)
	}
}

// ---------------------------------------------------------------------------
// (3) Byte determinism.
// ---------------------------------------------------------------------------

func TestMarketParticipant_ByteDeterminism(t *testing.T) {
	m1 := realAPI(t)
	root1 := saveInto(t, m1, "run1")

	m2 := realAPI(t)
	root2 := saveInto(t, m2, "run2")

	// The correlation ID differs between the two saves by design and is NOT
	// persisted, so the bundles must be byte-identical.
	assertBundlesByteIdentical(t, root1, root2)
}

// ---------------------------------------------------------------------------
// (4) Load-into-non-empty (full replace, not merge / not corrupt).
// ---------------------------------------------------------------------------

// TestAttack_LoadIntoNonEmptyPreservesConfig: a Load into a MarketAPI that is
// already fully populated with config must leave every config-driven query
// answering correctly (Handler's reset ran, the meta record applied, and the
// immutable config was neither dropped nor merged into garbage).
func TestAttack_LoadIntoNonEmptyPreservesConfig(t *testing.T) {
	orig := realAPI(t)
	root := saveInto(t, orig, "orig")

	// Target already holds the full config (a fresh real load).
	target := realAPI(t)
	if len(target.commodities) != len(allCommodities) {
		t.Fatalf("test setup: target registry not fully populated (%d)", len(target.commodities))
	}

	loadInto(t, root, target, "target")

	// Config must be intact and identical to the reference after the load.
	assertConfigIntact(t, target, orig, "load-into-nonempty")
	if len(target.commodities) != len(allCommodities) {
		t.Fatalf("load corrupted the registry size: %d != %d", len(target.commodities), len(allCommodities))
	}
}

// ---------------------------------------------------------------------------
// (5) Unknown-record-kind rejection (+ copyguard note).
// ---------------------------------------------------------------------------

// TestAttack_UnknownRecordKindRejected: an unrecognised record kind fails
// loud and closed, never a silent partial load.
func TestAttack_UnknownRecordKindRejected(t *testing.T) {
	m := realAPI(t)
	h := NewSaveParticipant(m).Handler()
	if err := h(serialize.Record{Kind: "market.bogus", Data: []byte(`{}`)}); err == nil {
		t.Fatalf("Handler accepted an unknown record kind -- want a loud error")
	}
}

// TestAttack_CorruptMetaRecordRejected: a malformed meta payload must be a
// hard decode error, never a silent success. (The stateless module's analogue
// of the stateful copyguard-fires test: since MarketAPI holds no mutex there
// is no copied-mutex hazard to guard, so the fail-closed behaviour proven here
// is the decode path instead.)
func TestAttack_CorruptMetaRecordRejected(t *testing.T) {
	m := realAPI(t)
	h := NewSaveParticipant(m).Handler()
	if err := h(serialize.Record{Kind: recMarketMeta, Data: []byte(`{not json`)}); err == nil {
		t.Fatalf("Handler accepted a corrupt meta payload -- want a loud decode error")
	}
}

// TestParticipantContractBasics: the participant reports the stable kind and a
// well-formed single-record source, and a well-formed meta record loads
// cleanly.
func TestParticipantContractBasics(t *testing.T) {
	m := realAPI(t)
	p := NewSaveParticipant(m)
	if p.Kind() != KindMarket {
		t.Fatalf("Kind() = %q, want %q", p.Kind(), KindMarket)
	}
	// A well-formed empty meta record loads without error.
	h := p.Handler()
	if err := h(serialize.Record{Kind: recMarketMeta, Data: []byte(`{}`)}); err != nil {
		t.Fatalf("Handler rejected a well-formed empty meta record: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Bundle byte-comparison helpers (mirroring the finance/unlocks/build pilots).
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
			t.Fatalf("file %q differs byte-for-byte between two saves of the same deterministic market state (correlation ID differs by design and is NOT persisted)", rel)
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
