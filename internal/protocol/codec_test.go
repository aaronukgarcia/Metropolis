package protocol

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// fixtureCommands returns one well-formed Command per registered Kind,
// so tests that range over it exercise every payload type in
// commands.go. Kept as a function (not a package var) so each test gets
// its own Command values and mutating one in a test can't leak into
// another.
func fixtureCommands(t *testing.T) map[Kind]Command {
	t.Helper()
	corr := CorrelationID("test-correlation-1")
	return map[Kind]Command{
		KindAdvanceTicks: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    100,
			Kind:            KindAdvanceTicks,
			Payload:         AdvanceTicksPayload{N: 30},
		},
		KindSetSpeed: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    100,
			Kind:            KindSetSpeed,
			Payload:         SetSpeedPayload{Speed: 3},
		},
		KindPause: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    100,
			Kind:            KindPause,
			Payload:         PausePayload{},
		},
		KindResume: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    101,
			Kind:            KindResume,
			Payload:         ResumePayload{},
		},
		KindSubscribe: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    102,
			Kind:            KindSubscribe,
			Payload: SubscribePayload{
				ViewName: "f2.ledger",
				Params:   map[string]string{"district": "central"},
			},
		},
		KindUnsubscribe: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    103,
			Kind:            KindUnsubscribe,
			Payload:         UnsubscribePayload{SubscriptionID: "sub-1"},
		},
		KindInspectEntity: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    104,
			Kind:            KindInspectEntity,
			Payload:         InspectEntityPayload{EntityRef: "citizen:482913"},
		},
		KindDebug: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    105,
			Kind:            KindDebug,
			Payload:         DebugPayload{Op: "force-unlock-tier", Args: map[string]string{"tier": "7"}},
		},
		KindBuy: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    106,
			Kind:            KindBuy,
			Payload:         BuyPayload{Cell: CellRef{X: 3, Y: 7}},
		},
		KindZone: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    107,
			Kind:            KindZone,
			Payload:         ZonePayload{Cell: CellRef{X: 3, Y: 7}, ZoneType: "Dwelling"},
		},
		KindBuild: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    108,
			Kind:            KindBuild,
			Payload:         BuildPayload{Cell: CellRef{X: 3, Y: 7}, BuildingType: "house.small"},
		},
		KindDemolish: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    109,
			Kind:            KindDemolish,
			Payload:         DemolishPayload{Cell: CellRef{X: 3, Y: 7}},
		},
		KindSetFunding: {
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    110,
			Kind:            KindSetFunding,
			Payload:         SetFundingPayload{ServiceID: "clinic-1", Level: 0.5},
		},
	}
}

func TestEveryKnownKindIsFixtured(t *testing.T) {
	fixtures := fixtureCommands(t)
	for _, kind := range KnownKinds() {
		if _, ok := fixtures[kind]; !ok {
			t.Errorf("Kind %q is registered in commandRegistry but has no fixture in fixtureCommands — commands.go and codec_test.go have drifted", kind)
		}
	}
	if len(fixtures) != len(KnownKinds()) {
		t.Errorf("fixtureCommands has %d entries, KnownKinds() has %d — counts must match", len(fixtures), len(KnownKinds()))
	}
}

func TestCommandRoundTrip(t *testing.T) {
	for kind, cmd := range fixtureCommands(t) {
		t.Run(string(kind), func(t *testing.T) {
			if err := cmd.Validate(); err != nil {
				t.Fatalf("fixture command failed Validate(): %v", err)
			}

			data, err := EncodeCommand(cmd)
			if err != nil {
				t.Fatalf("EncodeCommand: %v", err)
			}

			got, err := DecodeCommand(data)
			if err != nil {
				t.Fatalf("DecodeCommand: %v", err)
			}

			// Payload types may contain maps (e.g. DebugPayload.Args),
			// which are not comparable with ==, so compare deeply.
			if !reflect.DeepEqual(got, cmd) {
				t.Fatalf("round trip mismatch:\n  in:  %#v\n  out: %#v", cmd, got)
			}

			// Re-encoding the decoded value must produce byte-identical
			// JSON (the determinism note in codec.go) since field order
			// is fixed by struct declaration order.
			data2, err := EncodeCommand(got)
			if err != nil {
				t.Fatalf("EncodeCommand (second pass): %v", err)
			}
			if string(data) != string(data2) {
				t.Fatalf("re-encoding was not byte-stable:\n  first:  %s\n  second: %s", data, data2)
			}
		})
	}
}

func TestDecodeCommand_UnknownKind(t *testing.T) {
	raw := `{"protocolVersion":"1.0","correlationId":"c1","issuedAtTick":1,"kind":"FutureCommand","payload":{}}`
	_, err := DecodeCommand([]byte(raw))
	if err == nil {
		t.Fatal("expected an error decoding an unknown kind, got nil")
	}
	var unknownErr *UnknownKindError
	if !errors.As(err, &unknownErr) {
		t.Fatalf("expected *UnknownKindError, got %T: %v", err, err)
	}
	if unknownErr.Kind != "FutureCommand" {
		t.Fatalf("UnknownKindError.Kind = %q, want %q", unknownErr.Kind, "FutureCommand")
	}
	if !errors.Is(err, ErrUnknownCommandKind) {
		t.Fatal("errors.Is(err, ErrUnknownCommandKind) = false, want true")
	}
}

func TestDecodeCommand_MalformedJSON(t *testing.T) {
	_, err := DecodeCommand([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected an error decoding malformed JSON, got nil")
	}
	var unknownErr *UnknownKindError
	if errors.As(err, &unknownErr) {
		t.Fatal("malformed JSON must not surface as UnknownKindError")
	}
}

func TestCommandValidate_MissingCorrelationID(t *testing.T) {
	cmd := Command{
		ProtocolVersion: ProtocolVersion,
		CorrelationID:   "",
		IssuedAtTick:    1,
		Kind:            KindPause,
		Payload:         PausePayload{},
	}
	err := cmd.Validate()
	if !errors.Is(err, ErrMissingCorrelationID) {
		t.Fatalf("Validate() = %v, want ErrMissingCorrelationID", err)
	}
}

func TestCommandValidate_WrongProtocolVersion(t *testing.T) {
	cmd := Command{
		ProtocolVersion: "0.9",
		CorrelationID:   "c1",
		IssuedAtTick:    1,
		Kind:            KindPause,
		Payload:         PausePayload{},
	}
	if err := cmd.Validate(); !errors.Is(err, ErrUnsupportedProtocolVersion) {
		t.Fatalf("Validate() = %v, want ErrUnsupportedProtocolVersion", err)
	}
}

func TestCommandValidate_NilPayload(t *testing.T) {
	cmd := Command{
		ProtocolVersion: ProtocolVersion,
		CorrelationID:   "c1",
		IssuedAtTick:    1,
		Kind:            KindPause,
		Payload:         nil,
	}
	if err := cmd.Validate(); !errors.Is(err, ErrNilPayload) {
		t.Fatalf("Validate() = %v, want ErrNilPayload", err)
	}
}

func TestCommandValidate_KindPayloadMismatch(t *testing.T) {
	cmd := Command{
		ProtocolVersion: ProtocolVersion,
		CorrelationID:   "c1",
		IssuedAtTick:    1,
		Kind:            KindPause,
		Payload:         ResumePayload{}, // wrong payload for KindPause
	}
	if err := cmd.Validate(); !errors.Is(err, ErrKindPayloadMismatch) {
		t.Fatalf("Validate() = %v, want ErrKindPayloadMismatch", err)
	}
}

func TestCommandResultRoundTrip(t *testing.T) {
	cases := []CommandResult{
		{CorrelationID: "c1", Tick: 42, Accepted: true},
		{CorrelationID: "c2", Tick: 43, Accepted: false, Error: &ErrorRef{Code: "MET-E042", Display: "junction capacity exceeded"}},
	}
	for _, want := range cases {
		if err := want.Validate(); err != nil {
			t.Fatalf("fixture CommandResult failed Validate(): %v", err)
		}
		data, err := EncodeCommandResult(want)
		if err != nil {
			t.Fatalf("EncodeCommandResult: %v", err)
		}
		got, err := DecodeCommandResult(data)
		if err != nil {
			t.Fatalf("DecodeCommandResult: %v", err)
		}
		if got.CorrelationID != want.CorrelationID || got.Tick != want.Tick || got.Accepted != want.Accepted {
			t.Fatalf("round trip mismatch: in=%#v out=%#v", want, got)
		}
		if (got.Error == nil) != (want.Error == nil) {
			t.Fatalf("Error presence mismatch: in=%v out=%v", want.Error, got.Error)
		}
		if want.Error != nil && *got.Error != *want.Error {
			t.Fatalf("ErrorRef mismatch: in=%#v out=%#v", *want.Error, *got.Error)
		}
	}
}

func TestCommandResultValidate(t *testing.T) {
	tests := []struct {
		name string
		r    CommandResult
		want error
	}{
		{"missing correlation", CommandResult{Accepted: true}, ErrMissingCorrelationID},
		{"accepted with error", CommandResult{CorrelationID: "c1", Accepted: true, Error: &ErrorRef{Code: "MET-E001", Display: "x"}}, ErrAcceptedResultHasError},
		{"rejected without error", CommandResult{CorrelationID: "c1", Accepted: false}, ErrRejectedResultMissingError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.r.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestEventRoundTrip(t *testing.T) {
	want := Event{
		Kind:          "junction.gridlocked",
		Tick:          77,
		Severity:      SeverityWarning,
		EntityRefs:    []string{"junction:14"},
		Fields:        map[string]string{"approach": "north"},
		CorrelationID: "c1",
	}
	data, err := EncodeEvent(want)
	if err != nil {
		t.Fatalf("EncodeEvent: %v", err)
	}
	got, err := DecodeEvent(data)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	if got.Kind != want.Kind || got.Tick != want.Tick || got.Severity != want.Severity || got.CorrelationID != want.CorrelationID {
		t.Fatalf("round trip mismatch: in=%#v out=%#v", want, got)
	}
}

func TestDeltaRoundTrip(t *testing.T) {
	want := Delta{
		SubscriptionID: "sub-1",
		Tick:           88,
		Seq:            5,
		Patch:          json.RawMessage(`{"cash":1234}`),
	}
	data, err := EncodeDelta(want)
	if err != nil {
		t.Fatalf("EncodeDelta: %v", err)
	}
	got, err := DecodeDelta(data)
	if err != nil {
		t.Fatalf("DecodeDelta: %v", err)
	}
	if got.SubscriptionID != want.SubscriptionID || got.Tick != want.Tick || got.Seq != want.Seq {
		t.Fatalf("round trip mismatch: in=%#v out=%#v", want, got)
	}
	if string(got.Patch) != string(want.Patch) {
		t.Fatalf("Patch mismatch: in=%s out=%s", want.Patch, got.Patch)
	}
}

func TestNewCorrelationID_NonEmptyAndUnique(t *testing.T) {
	a := NewCorrelationID()
	b := NewCorrelationID()
	if a == "" || b == "" {
		t.Fatal("NewCorrelationID must never return empty")
	}
	if a == b {
		t.Fatal("two calls to NewCorrelationID produced the same value")
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("NewCorrelationID's output failed Validate(): %v", err)
	}
}
