package protocol

import (
	"errors"
	"reflect"
	"testing"
)

// TestGameplayCommand_RoundTrip proves each of the four gameplay command
// kinds (Buy, Zone, Build, Demolish) survives the full envelope path:
// Validate -> EncodeCommand -> DecodeCommand -> deep-equal -> byte-stable
// re-encode. It is the gameplay-specific companion to codec_test.go's
// TestCommandRoundTrip (which ranges over the shared fixture map). It can
// fail two ways a broad fixture range would also catch but which matter
// most here: a missing derefCommandPayload case in codec.go (DecodeCommand
// would then leave the pointer payload, and DeepEqual against the value
// payload fails), or a payload whose commandKind() returns the wrong
// constant (Validate rejects before we even encode).
func TestGameplayCommand_RoundTrip(t *testing.T) {
	corr := CorrelationID("gameplay-round-trip")
	commands := []Command{
		{
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    200,
			Kind:            KindBuy,
			Payload:         BuyPayload{Cell: CellRef{X: 3, Y: 7}},
		},
		{
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    201,
			Kind:            KindZone,
			Payload:         ZonePayload{Cell: CellRef{X: 4, Y: 9}, ZoneType: "Dwelling"},
		},
		{
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    202,
			Kind:            KindBuild,
			Payload:         BuildPayload{Cell: CellRef{X: 5, Y: 11}, BuildingType: "house.small"},
		},
		{
			ProtocolVersion: ProtocolVersion,
			CorrelationID:   corr,
			IssuedAtTick:    203,
			Kind:            KindDemolish,
			Payload:         DemolishPayload{Cell: CellRef{X: 6, Y: 13}},
		},
	}

	for _, want := range commands {
		t.Run(string(want.Kind), func(t *testing.T) {
			if err := want.Validate(); err != nil {
				t.Fatalf("fixture command failed Validate(): %v", err)
			}

			data, err := EncodeCommand(want)
			if err != nil {
				t.Fatalf("EncodeCommand: %v", err)
			}

			got, err := DecodeCommand(data)
			if err != nil {
				t.Fatalf("DecodeCommand: %v", err)
			}

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip mismatch:\n  in:  %#v\n  out: %#v", want, got)
			}

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

// TestGameplayCommand_KindPayloadMismatch proves Command.Validate() still
// enforces the kind<->payload match for the NEW kinds: an envelope whose
// Kind field disagrees with the payload's registered kind must be rejected
// with ErrKindPayloadMismatch. It deliberately pairs each gameplay Kind
// with a DIFFERENT gameplay payload, so a regression where one payload's
// commandKind() returns the wrong constant (e.g. a copy-paste returning
// KindBuy from ZonePayload) fails here — the mismatched pair would then
// Validate cleanly and this test would see nil instead of the expected
// error.
func TestGameplayCommand_KindPayloadMismatch(t *testing.T) {
	tests := []struct {
		name    string
		kind    Kind
		payload CommandPayload
	}{
		{
			name:    "Buy envelope carrying a Zone payload",
			kind:    KindBuy,
			payload: ZonePayload{Cell: CellRef{X: 1, Y: 2}, ZoneType: "Shop"},
		},
		{
			name:    "Zone envelope carrying a Build payload",
			kind:    KindZone,
			payload: BuildPayload{Cell: CellRef{X: 1, Y: 2}, BuildingType: "house.small"},
		},
		{
			name:    "Build envelope carrying a Demolish payload",
			kind:    KindBuild,
			payload: DemolishPayload{Cell: CellRef{X: 1, Y: 2}},
		},
		{
			name:    "Demolish envelope carrying a Buy payload",
			kind:    KindDemolish,
			payload: BuyPayload{Cell: CellRef{X: 1, Y: 2}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := Command{
				ProtocolVersion: ProtocolVersion,
				CorrelationID:   "gameplay-mismatch",
				IssuedAtTick:    1,
				Kind:            tt.kind,
				Payload:         tt.payload,
			}
			if err := cmd.Validate(); !errors.Is(err, ErrKindPayloadMismatch) {
				t.Fatalf("Validate() = %v, want ErrKindPayloadMismatch", err)
			}
		})
	}
}
