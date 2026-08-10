package serialize

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// recordSourceFromSlice builds a RecordSource that yields recs in order,
// then reports exhaustion. Used throughout as the simplest possible
// streaming producer.
func recordSourceFromSlice(recs []Record) RecordSource {
	i := 0
	return func() (Record, bool, error) {
		if i >= len(recs) {
			return Record{}, false, nil
		}
		r := recs[i]
		i++
		return r, true, nil
	}
}

func sampleRecords(n int) []Record {
	recs := make([]Record, n)
	for i := range recs {
		data, _ := json.Marshal(map[string]any{"id": i, "name": "citizen"})
		recs[i] = Record{Kind: "citizen", Data: data}
	}
	return recs
}

func TestNDJSONRoundTrip(t *testing.T) {
	recs := sampleRecords(50)
	var buf bytes.Buffer

	ser := NDJSONSerializer{}
	meta, err := ser.WriteShard(&buf, ShardMeta{Name: "citizens.0", Kind: "citizen"}, recordSourceFromSlice(recs))
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if meta.RecordCount != int64(len(recs)) {
		t.Fatalf("RecordCount = %d, want %d", meta.RecordCount, len(recs))
	}
	if meta.ByteSize != int64(buf.Len()) {
		t.Fatalf("ByteSize = %d, want %d (actual buffer length)", meta.ByteSize, buf.Len())
	}
	if meta.SHA256 == "" {
		t.Fatal("SHA256 not set")
	}
	if meta.Encoding != "ndjson+gzip" {
		t.Fatalf("Encoding = %q, want ndjson+gzip", meta.Encoding)
	}

	var got []Record
	err = ser.ReadShard(bytes.NewReader(buf.Bytes()), 0, func(r Record) error {
		got = append(got, r)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadShard: %v", err)
	}
	if len(got) != len(recs) {
		t.Fatalf("read back %d records, want %d", len(got), len(recs))
	}
	for i := range recs {
		if got[i].Kind != recs[i].Kind {
			t.Errorf("record %d: Kind = %q, want %q", i, got[i].Kind, recs[i].Kind)
		}
		if !bytes.Equal(got[i].Data, recs[i].Data) {
			t.Errorf("record %d: Data = %s, want %s", i, got[i].Data, recs[i].Data)
		}
	}
}

func TestNDJSONRoundTripEmptyShard(t *testing.T) {
	var buf bytes.Buffer
	ser := NDJSONSerializer{}
	meta, err := ser.WriteShard(&buf, ShardMeta{Name: "empty"}, recordSourceFromSlice(nil))
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if meta.RecordCount != 0 {
		t.Fatalf("RecordCount = %d, want 0", meta.RecordCount)
	}

	var count int
	err = ser.ReadShard(bytes.NewReader(buf.Bytes()), 0, func(Record) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("ReadShard: %v", err)
	}
	if count != 0 {
		t.Fatalf("handled %d records from empty shard, want 0", count)
	}
}

func TestNDJSONByteDeterminism(t *testing.T) {
	recs := sampleRecords(200)
	ser := NDJSONSerializer{}

	var buf1, buf2 bytes.Buffer
	if _, err := ser.WriteShard(&buf1, ShardMeta{Name: "s"}, recordSourceFromSlice(recs)); err != nil {
		t.Fatalf("WriteShard #1: %v", err)
	}
	if _, err := ser.WriteShard(&buf2, ShardMeta{Name: "s"}, recordSourceFromSlice(recs)); err != nil {
		t.Fatalf("WriteShard #2: %v", err)
	}

	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Fatal("two writes of the same records produced different bytes; NDJSONSerializer must be byte-deterministic")
	}
}

func TestNDJSONWriteShardPropagatesSourceError(t *testing.T) {
	sentinel := errTest("boom")
	src := func() (Record, bool, error) { return Record{}, false, sentinel }

	var buf bytes.Buffer
	_, err := (NDJSONSerializer{}).WriteShard(&buf, ShardMeta{Name: "s"}, src)
	if err == nil {
		t.Fatal("expected error from failing RecordSource, got nil")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestBundleRoundTripAndValidate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "save1")
	if err := CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	recs := sampleRecords(10)
	meta := ShardMeta{Name: "citizens.0", Kind: "citizen", Encoding: "ndjson+gzip"}
	f, err := CreateShardWriter(dir, meta)
	if err != nil {
		t.Fatalf("CreateShardWriter: %v", err)
	}
	meta, err = (NDJSONSerializer{}).WriteShard(f, meta, recordSourceFromSlice(recs))
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing shard file: %v", err)
	}

	h := NewHeader(42, 100, 3, "test-build")
	h.ShardIndex = append(h.ShardIndex, meta)
	if err := WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	got, err := ValidateBundle(dir)
	if err != nil {
		t.Fatalf("ValidateBundle: %v", err)
	}
	if got.WorldSeed != 42 || got.GameMonth != 3 || got.CreatedAtTick != 100 {
		t.Fatalf("header round-trip mismatch: %+v", got)
	}

	r, err := OpenShardReader(dir, meta)
	if err != nil {
		t.Fatalf("OpenShardReader: %v", err)
	}
	defer func() { _ = r.Close() }()
	var readBack int
	if err := (NDJSONSerializer{}).ReadShard(r, 0, func(Record) error { readBack++; return nil }); err != nil {
		t.Fatalf("ReadShard: %v", err)
	}
	if readBack != len(recs) {
		t.Fatalf("read back %d records, want %d", readBack, len(recs))
	}
}

func TestValidateBundleCatchesCorruption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "save2")
	if err := CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	meta := ShardMeta{Name: "citizens.0", Encoding: "ndjson+gzip"}
	f, err := CreateShardWriter(dir, meta)
	if err != nil {
		t.Fatalf("CreateShardWriter: %v", err)
	}
	meta, err = (NDJSONSerializer{}).WriteShard(f, meta, recordSourceFromSlice(sampleRecords(5)))
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	_ = f.Close()

	h := NewHeader(1, 1, 1, "test-build")
	h.ShardIndex = append(h.ShardIndex, meta)
	if err := WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	// Flip a byte in the shard file to simulate corruption.
	shardPath, err := ShardPath(dir, meta)
	if err != nil {
		t.Fatalf("ShardPath: %v", err)
	}
	raw, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatalf("reading shard: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("shard file unexpectedly empty")
	}
	raw[0] ^= 0xFF
	if err := os.WriteFile(shardPath, raw, 0o644); err != nil {
		t.Fatalf("writing corrupted shard: %v", err)
	}

	if _, err := ValidateBundle(dir); err == nil {
		t.Fatal("expected ValidateBundle to detect corruption, got nil error")
	}
}

func TestCreateBundleDirRefusesExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "save3")
	if err := CreateBundleDir(dir); err != nil {
		t.Fatalf("first CreateBundleDir: %v", err)
	}
	if err := CreateBundleDir(dir); err == nil {
		t.Fatal("expected error creating an already-existing bundle directory")
	}
}

func TestCheckFormatVersionMatrix(t *testing.T) {
	cases := []struct {
		name    string
		saved   string
		wantErr bool
	}{
		{"same version", CurrentFormatVersion, false},
		{"same major, higher minor", "1.9.0", false},
		{"same major, higher patch", "1.0.99", false},
		{"same major, lower minor", "1.0.0", false},
		{"older major", "0.9.0", true},
		{"newer major", "2.0.0", true},
		{"malformed", "not-a-version", true},
		{"two components", "1.0", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckFormatVersion(c.saved)
			if c.wantErr && err == nil {
				t.Fatalf("CheckFormatVersion(%q): expected error, got nil", c.saved)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("CheckFormatVersion(%q): unexpected error: %v", c.saved, err)
			}
		})
	}
}

func TestParseSemVer(t *testing.T) {
	got, err := ParseSemVer("1.2.3")
	if err != nil {
		t.Fatalf("ParseSemVer: %v", err)
	}
	want := Semver{Major: 1, Minor: 2, Patch: 3}
	if got != want {
		t.Fatalf("ParseSemVer(1.2.3) = %+v, want %+v", got, want)
	}

	if _, err := ParseSemVer("1.2"); err == nil {
		t.Fatal("expected error for two-component version")
	}
	if _, err := ParseSemVer("1.2.x"); err == nil {
		t.Fatal("expected error for non-numeric component")
	}
	if _, err := ParseSemVer("-1.2.3"); err == nil {
		t.Fatal("expected error for negative component")
	}
}

func TestDebugTouchedIsSticky(t *testing.T) {
	h := NewHeader(1, 1, 1, "test-build")
	if h.DebugTouched() {
		t.Fatal("new header must start with DebugTouched = false")
	}

	h.TouchDebug()
	if !h.DebugTouched() {
		t.Fatal("TouchDebug must set DebugTouched = true")
	}

	// Sticky: merging false must not clear it.
	h.MergeDebugTouched(false)
	if !h.DebugTouched() {
		t.Fatal("DebugTouched must remain true after merging false (sticky invariant)")
	}

	h.MergeDebugTouched(true)
	if !h.DebugTouched() {
		t.Fatal("DebugTouched must remain true after merging true")
	}

	// A fresh header carrying forward a previously debug-touched flag via
	// MergeDebugTouched (e.g. metctl export / save-over) must also end up
	// touched.
	h2 := NewHeader(1, 1, 1, "test-build")
	h2.MergeDebugTouched(true)
	if !h2.DebugTouched() {
		t.Fatal("MergeDebugTouched(true) on a clean header must set DebugTouched")
	}
}

func TestHeaderJSONRoundTripPreservesDebugTouched(t *testing.T) {
	h := NewHeader(7, 8, 9, "test-build")
	h.TouchDebug()

	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Header
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !got.DebugTouched() {
		t.Fatal("DebugTouched did not survive a JSON round-trip")
	}
}

// TestDebugTouchedFieldIsUnexported is SEC-024's structural proof that the
// bypass is closed: the exact PoC the finding described — a caller from
// another package holding the same *Header and clearing the flag with a
// bare `h.DebugTouched = false` — cannot even be EXPRESSED once the field
// is unexported, because Go's compiler refuses to name an unexported
// field from outside its package. There is no way to write a Go source
// file that fails to compile as a runnable test (a "does not compile"
// assertion isn't expressible in-language), so this test instead asserts
// the structural property that guarantees it: reflect.StructField.PkgPath
// is empty for an EXPORTED field and non-empty for an unexported one, so
// a non-empty PkgPath here is direct evidence no external package can
// reference this field by name, in source, at all.
func TestDebugTouchedFieldIsUnexported(t *testing.T) {
	typ := reflect.TypeOf(Header{})
	f, ok := typ.FieldByName("debugTouched")
	if !ok {
		t.Fatal(`expected Header to have a field named "debugTouched"`)
	}
	if f.PkgPath == "" {
		t.Fatal("debugTouched has an empty PkgPath, meaning it is EXPORTED — SEC-024 requires it unexported so TouchDebug/MergeDebugTouched are the only mutation path")
	}
	// Belt-and-braces: the old exported spelling must not exist at all,
	// so nothing (test, production code, or a future accidental re-add)
	// can silently resurrect the bypass under the original name.
	if _, exported := typ.FieldByName("DebugTouched"); exported {
		t.Fatal(`Header must not also have an exported "DebugTouched" field — that field name is reserved for the DebugTouched() accessor method`)
	}
}

// TestDebugTouchedFalseRoundTrips is the false-value half of the
// round-trip guarantee (SEC-024 fix requirement): a header that was never
// debug-touched must marshal/unmarshal back to false, not just true. This
// guards specifically against a marshalling bug that always writes/reads
// the flag as true (which would "fix" the bypass by replacing it with
// silent, permanent false-positive flagging of every clean save — worse
// than the original defect).
func TestDebugTouchedFalseRoundTrips(t *testing.T) {
	h := NewHeader(2, 3, 4, "test-build")
	if h.DebugTouched() {
		t.Fatal("precondition: fresh header must start with DebugTouched() = false")
	}

	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Header
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.DebugTouched() {
		t.Fatal("a never-touched header must round-trip to DebugTouched() = false, not true")
	}
}

// TestHeaderJSONBackwardCompat_DebugTouchedTrue proves an existing
// on-disk header.json written by a prior build — before this field was
// unexported and MarshalJSON/UnmarshalJSON were added — still decodes
// correctly. The wire shape (the "debugTouched" JSON key) is unchanged by
// this fix; only the in-memory Go field changed. Hand-building the JSON
// here (rather than round-tripping through this package's own Marshal)
// is deliberate: it exercises the UnmarshalJSON path against bytes that
// look exactly like what is already sitting on disk in real saves,
// independent of whatever this package's own Marshal happens to produce.
func TestHeaderJSONBackwardCompat_DebugTouchedTrue(t *testing.T) {
	raw := []byte(`{
		"formatVersion": "1.0.0",
		"worldSeed": 42,
		"createdAtTick": 100,
		"gameMonth": 5,
		"appVersion": "legacy-build",
		"debugTouched": true,
		"shardIndex": []
	}`)
	var h Header
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("Unmarshal legacy header.json: %v", err)
	}
	if !h.DebugTouched() {
		t.Fatal(`legacy header.json with "debugTouched": true must load as DebugTouched() = true`)
	}
}

// TestHeaderJSONBackwardCompat_DebugTouchedFalse is the false-value
// counterpart to the above: a legacy clean save must not be misread as
// debug-touched.
func TestHeaderJSONBackwardCompat_DebugTouchedFalse(t *testing.T) {
	raw := []byte(`{
		"formatVersion": "1.0.0",
		"worldSeed": 42,
		"createdAtTick": 100,
		"gameMonth": 5,
		"appVersion": "legacy-build",
		"debugTouched": false,
		"shardIndex": []
	}`)
	var h Header
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("Unmarshal legacy header.json: %v", err)
	}
	if h.DebugTouched() {
		t.Fatal(`legacy header.json with "debugTouched": false must load as DebugTouched() = false`)
	}
}

// TestMergeDebugTouchedIsStickyOnly directly targets the sticky-merge
// requirement called out in the fix brief: MergeDebugTouched(false) on an
// already-true header must leave it true (never regress true -> false),
// while MergeDebugTouched(true) on a false header sets it. Distinct from
// TestDebugTouchedIsSticky above in that it isolates MergeDebugTouched
// alone (not interleaved with TouchDebug) and explicitly exercises both
// directions.
func TestMergeDebugTouchedIsStickyOnly(t *testing.T) {
	touched := NewHeader(1, 1, 1, "test-build")
	touched.TouchDebug()
	touched.MergeDebugTouched(false)
	if !touched.DebugTouched() {
		t.Fatal("MergeDebugTouched(false) on an already-true header must leave it true (sticky)")
	}

	clean := NewHeader(1, 1, 1, "test-build")
	clean.MergeDebugTouched(false)
	if clean.DebugTouched() {
		t.Fatal("MergeDebugTouched(false) on a clean header must leave it false")
	}
	clean.MergeDebugTouched(true)
	if !clean.DebugTouched() {
		t.Fatal("MergeDebugTouched(true) on a clean header must set it true")
	}
}

// TestTouchDebugIsIdempotent: calling TouchDebug twice must be a no-op the
// second time (still just true), per the fix brief's stickiness
// requirement.
func TestTouchDebugIsIdempotent(t *testing.T) {
	h := NewHeader(1, 1, 1, "test-build")
	h.TouchDebug()
	h.TouchDebug()
	if !h.DebugTouched() {
		t.Fatal("TouchDebug called twice must leave DebugTouched() = true")
	}
}

// TestUnmarshalJSONMergesDebugTouched is SEC-029: json.Unmarshal into an
// ALREADY debug-touched *Header must not clear the flag just because the
// decoded JSON says "false". Every round-trip/backward-compat test above
// only ever decodes into a FRESH `var h Header` (h.debugTouched starts
// false) -- which is exactly why none of them caught this: OR-merge and
// plain assignment agree whenever the target starts false. This test
// exists specifically to cover the other case: decoding into a target
// that is already true.
func TestUnmarshalJSONMergesDebugTouched(t *testing.T) {
	h := NewHeader(1, 1, 1, "test-build")
	h.TouchDebug()
	if !h.DebugTouched() {
		t.Fatal("precondition: header must already be debug-touched before the attack unmarshal")
	}

	clean := NewHeader(1, 1, 1, "test-build") // debugTouched: false
	raw, err := json.Marshal(clean)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// The idiomatic decode-into-existing-value call: json.Unmarshal(data,
	// &h) where h is already populated (and already touched), exactly the
	// shape a save-over-reusing-an-existing-header caller would use.
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !h.DebugTouched() {
		t.Fatal("json.Unmarshal of a clean header's JSON into an already-touched *Header must not clear DebugTouched (SEC-029: UnmarshalJSON must OR-merge, not assign -- the sticky invariant must survive decode-into-existing, not just decode-into-fresh)")
	}
}

// TestHeaderWireFieldsMatchHeader is a drift test (Weakness pattern #2 --
// a value manually duplicated across a boundary needs a test proving the
// two copies still agree, per this project's precedent with the F12
// phase-name mirror and the stub's tick-limit mirror). headerWire
// (SEC-024/ASM-096) is a hand-maintained JSON mirror of Header, required
// because Header.debugTouched is unexported and encoding/json cannot see
// it directly (see headerWire's doc comment in header.go). A field added
// to Header later and forgotten in headerWire would silently vanish from
// every save's on-disk JSON, with nothing failing -- this test is what
// catches that.
//
// debugTouched/DebugTouched is a deliberate, single named exception: the
// field is unexported on Header (SEC-024's whole point) but must be
// exported on headerWire for encoding/json to touch it at all, so this
// test maps that one field by an explicit override rather than requiring
// identical Go field names everywhere.
func TestHeaderWireFieldsMatchHeader(t *testing.T) {
	headerType := reflect.TypeOf(Header{})
	wireType := reflect.TypeOf(headerWire{})

	if headerType.NumField() != wireType.NumField() {
		t.Fatalf("Header has %d fields but headerWire (its hand-maintained JSON mirror, see SEC-024/ASM-096 in header.go) has %d -- these two structs must be kept in EXACT 1:1 correspondence by hand: whoever added or removed a field on one must make the matching change on the other, or the odd one out silently vanishes from (or corrupts) every save's on-disk JSON", headerType.NumField(), wireType.NumField())
	}

	for i := 0; i < headerType.NumField(); i++ {
		hf := headerType.Field(i)

		// wantWireName/wantGoName: the JSON wire name and the Go field
		// name this Header field is expected to correspond to on
		// headerWire. debugTouched is the one deliberate exception
		// (unexported on Header, must be exported on headerWire);
		// every other field is expected to keep the SAME Go name and
		// carry a json tag that headerWire's matching field must echo
		// exactly.
		wantGoName := hf.Name
		var wantWireName string
		if hf.Name == "debugTouched" {
			wantGoName = "DebugTouched"
			wantWireName = "debugTouched"
		} else {
			tag := hf.Tag.Get("json")
			if tag == "" || tag == "-" {
				t.Fatalf("Header field %q has no usable `json:\"...\"` tag, so this drift test cannot verify headerWire mirrors it correctly -- give it a tag, or if it is deliberately renamed across the unexported boundary like debugTouched, add it to this test's exception list", hf.Name)
			}
			wantWireName = tag
		}

		wf, ok := wireType.FieldByName(wantGoName)
		if !ok {
			t.Fatalf("Header field %q (expected wire name %q) has no corresponding headerWire.%s -- headerWire has drifted out of sync with Header; add the missing field to headerWire AND to both MarshalJSON and UnmarshalJSON's field-by-field copies in header.go", hf.Name, wantWireName, wantGoName)
		}
		if gotTag := wf.Tag.Get("json"); gotTag != wantWireName {
			t.Fatalf("headerWire.%s has json tag %q, want %q to match Header.%s -- these two structs' wire names must stay in lockstep or the on-disk JSON key silently diverges from what Header actually holds", wf.Name, gotTag, wantWireName, hf.Name)
		}

		wantType := hf.Type
		if hf.Name == "debugTouched" {
			// The field is bool on both sides; compare Kind rather than
			// exact Type since Header's is an unnamed bool and this is
			// the one field where that's expected, not a mismatch.
			if wf.Type.Kind() != reflect.Bool {
				t.Fatalf("headerWire.DebugTouched must be bool to mirror Header.debugTouched, got %s", wf.Type)
			}
			continue
		}
		if wf.Type != wantType {
			t.Fatalf("Header.%s is type %s but headerWire.%s is type %s -- these must match exactly for the JSON round-trip to be lossless", hf.Name, hf.Type, wf.Name, wf.Type)
		}
	}
}

func TestBinarySerializerNotImplemented(t *testing.T) {
	bs := BinarySerializer{}
	if _, err := bs.WriteShard(&bytes.Buffer{}, ShardMeta{Name: "x"}, recordSourceFromSlice(nil)); err == nil {
		t.Fatal("expected BinarySerializer.WriteShard to return an error")
	}
	if err := bs.ReadShard(bytes.NewReader(nil), 0, func(Record) error { return nil }); err == nil {
		t.Fatal("expected BinarySerializer.ReadShard to return an error")
	}
}
