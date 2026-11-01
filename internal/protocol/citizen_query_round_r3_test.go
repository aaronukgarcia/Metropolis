package protocol

// FEAT-2326609842 round r3 (opus-round-r3-passport): the keyset cursor must
// survive the codec exactly at the top of the id space, where fertility-child
// ids live (1<<63 + n). A float64 anywhere on the Go path would round these
// (2^63 has a 2048-wide float64 ulp) and turn the cursor into a skip or a
// repeat.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoundR3_KeysetCursorExactAtTopOfIDSpace(t *testing.T) {
	for _, after := range []uint64{1<<53 + 1, 1<<63 + 1, 1<<63 + 12345, MaxQueryCitizenID} {
		in := Command{ProtocolVersion: ProtocolVersion, CorrelationID: "r3", Kind: KindQueryOccupants,
			Payload: QueryOccupantsPayload{By: OccupantsByHome, Cell: CellRef{X: 1, Y: 2}, AfterID: after, Limit: 3}}
		raw, err := EncodeCommand(in)
		if err != nil {
			t.Fatalf("encode %d: %v", after, err)
		}
		got, err := DecodeCommand(raw)
		if err != nil {
			t.Fatalf("decode %d: %v", after, err)
		}
		if got.Payload.(QueryOccupantsPayload).AfterID != after {
			t.Errorf("afterID %d round-tripped to %d", after, got.Payload.(QueryOccupantsPayload).AfterID)
		}
		// The answer's cursor fields ride CommandResult.Result as JSON.
		page := OccupantPage{By: OccupantsByHome, AfterID: after, NextAfterID: after + 1, HasMore: true,
			Entries: []OccupantEntry{{ID: after + 1}}}
		body, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		res, err := json.Marshal(CommandResult{CorrelationID: "r3", Accepted: true, Result: body})
		if err != nil {
			t.Fatal(err)
		}
		var back CommandResult
		if err := json.Unmarshal(res, &back); err != nil {
			t.Fatal(err)
		}
		var p OccupantPage
		if err := json.Unmarshal(back.Result, &p); err != nil {
			t.Fatal(err)
		}
		if p.AfterID != after || p.NextAfterID != after+1 || p.Entries[0].ID != after+1 {
			t.Errorf("page cursor drifted: %+v (want after %d)", p, after)
		}
		if !strings.Contains(string(res), `"nextAfterID":`) {
			t.Errorf("wire missing nextAfterID: %s", res)
		}
	}
}
