package protocol

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func queryEnvelope(kind string, payload string) []byte {
	return []byte(`{"protocolVersion":"` + ProtocolVersion + `","correlationId":"c-1","issuedAtTick":1,"kind":"` + kind + `","payload":` + payload + `}`)
}

// wantBoundsErr asserts err is the registry-coded query-bounds refusal.
func wantBoundsErr(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want %s refusal, got nil", what, ErrQueryOutOfBounds)
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrQueryOutOfBounds {
		t.Fatalf("%s: want registry code %s, got %v", what, ErrQueryOutOfBounds, err)
	}
}

// AC-7: garbage citizen ids are refused AT THE CODEC with a registry code.
func TestDecodeQueryCitizen_GarbageIDsRefused(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"zero", `{"id":0}`},
		{"absent", `{}`},
		{"max uint64", `{"id":18446744073709551615}`},
		{"just above bound", `{"id":` + strconv.FormatUint(MaxQueryCitizenID+1, 10) + `}`},
	} {
		_, err := DecodeCommand(queryEnvelope("QueryCitizen", tc.payload))
		wantBoundsErr(t, err, tc.name)
	}
	// Boundary values pass.
	for _, ok := range []string{`{"id":1}`, `{"id":` + strconv.FormatUint(MaxQueryCitizenID, 10) + `}`} {
		if _, err := DecodeCommand(queryEnvelope("QueryCitizen", ok)); err != nil {
			t.Errorf("payload %s should decode: %v", ok, err)
		}
	}
	// Type garbage fails as a decode error (never a zero-value command).
	for _, bad := range []string{`{"id":-1}`, `{"id":"abc"}`, `{"id":1.5}`} {
		if _, err := DecodeCommand(queryEnvelope("QueryCitizen", bad)); err == nil {
			t.Errorf("payload %s should be refused", bad)
		}
	}
}

// AC-7: the page cap is a hard bound -- over-cap is refused, not clamped.
func TestDecodeQueryOccupants_BoundsRefused(t *testing.T) {
	cases := map[string]string{
		"limit over cap":    `{"by":"home","cell":{"x":1,"y":1},"limit":` + strconv.Itoa(OccupantsPageCap+1) + `}`,
		"limit huge":        `{"by":"home","cell":{"x":1,"y":1},"limit":1000000}`,
		"negative limit":    `{"by":"home","cell":{"x":1,"y":1},"limit":-1}`,
		"afterID above max": `{"by":"home","cell":{"x":1,"y":1},"afterID":` + strconv.FormatUint(MaxQueryCitizenID+1, 10) + `}`,
		"unknown by":        `{"by":"everyone","limit":5}`,
		"empty by":          `{"limit":5}`,
		"workplace ref 0":   `{"by":"workplace","ref":0,"limit":5}`,
		"negative home x":   `{"by":"home","cell":{"x":-1,"y":0},"limit":5}`,
		"negative home y":   `{"by":"home","cell":{"x":0,"y":-3},"limit":5}`,
	}
	for name, payload := range cases {
		_, err := DecodeCommand(queryEnvelope("QueryOccupants", payload))
		wantBoundsErr(t, err, name)
	}
	// afterID is uint64: a negative value is refused by the JSON decode itself
	// (before validateBounds), still strictly refused, never clamped to 0.
	if _, err := DecodeCommand(queryEnvelope("QueryOccupants", `{"by":"home","cell":{"x":1,"y":1},"afterID":-1}`)); err == nil {
		t.Error("negative afterID accepted")
	}
	for _, ok := range []string{
		`{"by":"home","cell":{"x":1,"y":1},"limit":` + strconv.Itoa(OccupantsPageCap) + `}`,
		`{"by":"workplace","ref":9,"limit":0}`,
		`{"by":"home","cell":{"x":0,"y":0},"afterID":` + strconv.FormatUint(MaxQueryCitizenID, 10) + `}`,
		`{"by":"home","cell":{"x":0,"y":0},"afterID":0}`,
	} {
		if _, err := DecodeCommand(queryEnvelope("QueryOccupants", ok)); err != nil {
			t.Errorf("payload %s should decode: %v", ok, err)
		}
	}
}

// The in-process path (hand-built Command, no DecodeCommand) enforces the same bounds.
func TestCommandValidate_EnforcesQueryBounds(t *testing.T) {
	mk := func(k Kind, p CommandPayload) Command {
		return Command{ProtocolVersion: ProtocolVersion, CorrelationID: "c-1", Kind: k, Payload: p}
	}
	wantBoundsErr(t, mk(KindQueryCitizen, QueryCitizenPayload{ID: 0}).Validate(), "validate zero id")
	wantBoundsErr(t, mk(KindQueryOccupants, QueryOccupantsPayload{By: OccupantsByHome, Limit: OccupantsPageCap + 1}).Validate(), "validate over cap")
	if err := mk(KindQueryCitizen, QueryCitizenPayload{ID: 5}).Validate(); err != nil {
		t.Errorf("valid query refused: %v", err)
	}
}

// The raised bounds error renders from the REGISTRY (message template filled,
// not the unregistered/registry-failure fallback).
func TestQueryBoundsErr_RendersRegistryMessage(t *testing.T) {
	_, err := DecodeCommand(queryEnvelope("QueryCitizen", `{"id":0}`))
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("want *errs.E, got %v", err)
	}
	msg := e.Error()
	for _, want := range []string{ErrQueryOutOfBounds, "QueryCitizen", "refused"} {
		if !strings.Contains(msg, want) {
			t.Errorf("rendered error %q missing %q", msg, want)
		}
	}
	if strings.Contains(strings.ToLower(msg), "unregistered") {
		t.Errorf("code resolved as unregistered: %q", msg)
	}
}

// Garbage through the REAL codec: never a zero-value command, never a panic.
func TestDecodeQuery_GarbagePayloads(t *testing.T) {
	for _, kind := range []string{"QueryCitizen", "QueryOccupants"} {
		for _, payload := range []string{`null`, `[]`, `"x"`, `123`, `{"by":5}`, `{"id":{}}`, `{"limit":"9"}`, `{`} {
			cmd, err := DecodeCommand(queryEnvelope(kind, payload))
			if err == nil {
				t.Errorf("%s payload %s decoded to %+v; want refusal", kind, payload, cmd)
			}
		}
	}
	if _, err := DecodeCommand([]byte(`not json`)); err == nil {
		t.Error("non-JSON envelope accepted")
	}
}

// Both kinds round-trip through encode/decode with their fields intact.
func TestQueryKinds_RoundTrip(t *testing.T) {
	for _, tc := range []Command{
		{ProtocolVersion: ProtocolVersion, CorrelationID: "c-1", Kind: KindQueryCitizen, Payload: QueryCitizenPayload{ID: 77}},
		{ProtocolVersion: ProtocolVersion, CorrelationID: "c-1", Kind: KindQueryOccupants, Payload: QueryOccupantsPayload{By: OccupantsByWorkplace, Ref: 3, AfterID: 10, Limit: 20}},
	} {
		raw, err := EncodeCommand(tc)
		if err != nil {
			t.Fatalf("encode %s: %v", tc.Kind, err)
		}
		got, err := DecodeCommand(raw)
		if err != nil {
			t.Fatalf("decode %s: %v", tc.Kind, err)
		}
		if got.Payload != tc.Payload {
			t.Errorf("%s payload changed: %+v vs %+v", tc.Kind, got.Payload, tc.Payload)
		}
	}
}

// A CommandResult with no Result encodes without the key (pre-existing results
// stay byte-identical); one with a Result carries it.
func TestCommandResult_ResultOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(CommandResult{CorrelationID: "c", Accepted: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "result") {
		t.Errorf("empty Result leaked into wire: %s", raw)
	}
	raw, _ = json.Marshal(CommandResult{CorrelationID: "c", Accepted: true, Result: json.RawMessage(`{"a":1}`)})
	if !strings.Contains(string(raw), `"result":{"a":1}`) {
		t.Errorf("Result missing: %s", raw)
	}
}
