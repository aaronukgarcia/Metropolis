package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- FEAT-042 entity addressing (AC-20/AC-21/AC-23) ---

func TestValidateEntityID(t *testing.T) {
	valid := []EntityID{
		"ledger:42",
		"diagram:arrow-7",
		"citizen:482913",
		"junction:14.approaches",
		"a:1",
	}
	for _, id := range valid {
		if err := ValidateEntityID(id); err != nil {
			t.Errorf("ValidateEntityID(%q) = %v, want nil", id, err)
		}
	}

	invalid := []EntityID{
		"",
		"NoColon",
		"Uppercase:1",
		"1:leading-digit-type",
		"type:",
		":noid",
		"type:has space",
		"type:slash/1",
		"type:id,comma",
	}
	for _, id := range invalid {
		if err := ValidateEntityID(id); err == nil {
			t.Errorf("ValidateEntityID(%q) = nil, want an error", id)
		}
	}

	// GR#7 / AC-32: the invalid case must surface the registered MET-P003
	// code through the errs registry, not a bare fmt.Errorf.
	err := ValidateEntityID(EntityID("bad id"))
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("ValidateEntityID invalid returned %T, want *errs.E", err)
	}
	if e.Code != ErrInvalidEntityID {
		t.Fatalf("ValidateEntityID invalid error code = %q, want %q (MET-P003)", e.Code, ErrInvalidEntityID)
	}
}

func TestTargetRefJSONRoundTrip(t *testing.T) {
	in := TargetRef{ViewName: "f2.ledger", EntityID: EntityID("ledger:42")}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal(TargetRef): %v", err)
	}
	// Exactly two JSON-tagged exported fields (AC-21 binding).
	got := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(target): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("TargetRef marshalled to %d keys, want exactly 2: %s", len(got), data)
	}
	if _, ok := got["viewName"]; !ok {
		t.Fatalf("TargetRef JSON missing viewName key: %s", data)
	}
	if _, ok := got["entityId"]; !ok {
		t.Fatalf("TargetRef JSON missing entityId key: %s", data)
	}

	var out TargetRef
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal(TargetRef): %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("TargetRef round trip mismatch:\n  in:  %#v\n  out: %#v", in, out)
	}
}

// --- FEAT-042 crisis field already on main via FEAT-013 (AC-24/AC-25) ---
// TestEventCrisisIndependentOfSeverity and TestEventCrisisAbsentDefaultsFalse
// (events_crisis_test.go) are the recorded AC-24/AC-25 coverage; they were
// landed with the field itself on 2026-08-15 (FEAT-013). This amendment adds
// no new crisis surface — those tests are re-verified RED→GREEN below.

// --- FEAT-042 compatibility (AC-26/AC-28/AC-29/AC-30) ---

func TestGoldenBytesUnchanged(t *testing.T) {
	// AC-26: marshalling a Command/Delta/Event whose FEAT-042 new fields are
	// all at their zero value produces byte-identical JSON to the
	// pre-amendment schema. The golden fixtures were captured from the
	// pre-amendment code before this amendment's struct edits.
	dir := filepath.Join("testdata", "golden")

	cmd := Command{
		ProtocolVersion: ProtocolVersion,
		CorrelationID:   "test-correlation-1",
		IssuedAtTick:    100,
		Kind:            KindInspectEntity,
		Payload:         InspectEntityPayload{EntityRef: "citizen:482913"},
	}
	cmdData, err := EncodeCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, filepath.Join(dir, "command.json"), cmdData)

	delta := Delta{
		SubscriptionID: "sub-1",
		Tick:           100,
		Seq:            1,
		Patch:          []byte(`{"district":"central","count":42}`),
	}
	deltaData, err := EncodeDelta(delta)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, filepath.Join(dir, "delta.json"), deltaData)

	ev := Event{
		Kind:     "milestone.reached",
		Tick:     1,
		Severity: SeverityInfo,
	}
	evData, err := EncodeEvent(ev)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, filepath.Join(dir, "event.json"), evData)
}

func checkGolden(t *testing.T, path string, data []byte) {
	t.Helper()
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", path, err)
	}
	if !bytes.Equal(golden, data) {
		t.Fatalf("byte mismatch vs golden fixture %s:\n  golden: %s\n  now:    %s", path, golden, data)
	}
}

func TestProtocolVersionStill10(t *testing.T) {
	// AC-28: ProtocolVersion remains the literal "1.0" after the amendment —
	// Validate does exact string equality, so a bump would reject every
	// already-recorded fixture.
	if ProtocolVersion != "1.0" {
		t.Fatalf("ProtocolVersion = %q, want \"1.0\"", ProtocolVersion)
	}
	cmd := Command{
		ProtocolVersion: "1.0",
		CorrelationID:   "c1",
		IssuedAtTick:    1,
		Kind:            KindPause,
		Payload:         PausePayload{},
	}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("Command{ProtocolVersion: \"1.0\"}.Validate() = %v, want nil", err)
	}
}

func TestPreAmendmentReplayLoads(t *testing.T) {
	// AC-29: a pre-amendment-shaped fixture (no crisis/entityId/targetRef
	// keys) loads cleanly with the new fields at their documented zero
	// values — not an error, not a silently-wrong non-zero value.
	eventData := []byte(`{"kind":"milestone.reached","tick":1,"severity":"info"}`)
	ev, err := DecodeEvent(eventData)
	if err != nil {
		t.Fatalf("DecodeEvent(pre-amendment event) = %v, want nil", err)
	}
	if ev.Crisis {
		t.Fatalf("pre-amendment event decoded with Crisis=true, want false")
	}

	deltaData := []byte(`{"subscriptionId":"sub-1","tick":1,"seq":1,"patch":{"a":1}}`)
	delta, err := DecodeDelta(deltaData)
	if err != nil {
		t.Fatalf("DecodeDelta(pre-amendment delta) = %v, want nil", err)
	}
	if delta.Seq != 1 {
		t.Fatalf("pre-amendment delta Seq = %d, want 1", delta.Seq)
	}

	// The golden fixtures themselves are pre-amendment-shaped replay
	// fixtures: they decode under the post-amendment code.
	goldenEvent, err := os.ReadFile(filepath.Join("testdata", "golden", "event.json"))
	if err != nil {
		t.Fatal(err)
	}
	ev2, err := DecodeEvent(goldenEvent)
	if err != nil {
		t.Fatalf("DecodeEvent(golden event fixture) = %v, want nil", err)
	}
	if ev2.Crisis {
		t.Fatal("golden event fixture decoded with Crisis=true, want false")
	}
}

func TestNoEngineOrUIImport(t *testing.T) {
	// AC-30 (GR#20): the seam package imports nothing from internal/engine
	// or internal/ui. Equivalent to `go list -deps ./internal/protocol/...`
	// filtered for the two banned subtrees.
	cmd := exec.Command("go", "list", "-deps", "./...")
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "internal/engine") || strings.Contains(line, "internal/ui") {
			t.Fatalf("protocol package (or a dep) imports the banned subtree %q", line)
		}
	}
}
