package protocol

// FEAT-2326609842 round r1 (opus-round-r1-passport): codec hostility beyond
// the builders' set -- every truncation of a valid envelope, duplicate and
// case-folded keys, huge strings, int-boundary offsets, unknown fields.

import (
	"strconv"
	"strings"
	"testing"
)

func TestRoundR1_TruncationNeverPanicsNorYieldsBadQuery(t *testing.T) {
	for _, env := range [][]byte{
		queryEnvelope("QueryCitizen", `{"id":123456789}`),
		queryEnvelope("QueryOccupants", `{"by":"workplace","ref":77,"afterID":40,"limit":50}`),
	} {
		for n := 0; n < len(env); n++ {
			cmd, err := DecodeCommand(env[:n])
			if err == nil {
				t.Errorf("truncated envelope (len %d) decoded: %+v", n, cmd)
			}
		}
	}
}

func TestRoundR1_DuplicateAndCaseFoldedKeys(t *testing.T) {
	// Last-wins: a zero after a good id must be refused at the bounds check.
	if _, err := DecodeCommand(queryEnvelope("QueryCitizen", `{"id":5,"id":0}`)); err == nil {
		t.Error("duplicate key ending in id 0 accepted")
	}
	// Case-folded duplicate: encoding/json matches "ID" to id; last wins.
	if _, err := DecodeCommand(queryEnvelope("QueryCitizen", `{"id":5,"ID":0}`)); err == nil {
		t.Error("case-folded duplicate ending in 0 accepted")
	}
	// A limit smuggled after a legal one must still hit the cap.
	if _, err := DecodeCommand(queryEnvelope("QueryOccupants", `{"by":"home","cell":{"x":1,"y":1},"limit":10,"Limit":100000}`)); err == nil {
		t.Error("case-folded oversize limit accepted")
	}
	// Case variants of by are not silently accepted as a different listing.
	if _, err := DecodeCommand(queryEnvelope("QueryOccupants", `{"by":"HOME","cell":{"x":1,"y":1}}`)); err == nil {
		t.Error("by=HOME accepted")
	}
}

func TestRoundR1_HugeAndBoundaryValues(t *testing.T) {
	huge := strings.Repeat("h", 1<<20)
	if _, err := DecodeCommand(queryEnvelope("QueryOccupants", `{"by":"`+huge+`","cell":{"x":1,"y":1}}`)); err == nil {
		t.Error("1 MiB by accepted")
	}
	// FLIPPED for the r3 keyset protocol (offset removed): afterID is uint64 and
	// bounded 0..MaxQueryCitizenID. The old offset-hostility values that are now
	// INSIDE the legal id range (int64 max, 1<<63) are no longer hostile and move
	// to the accepted list below; every value outside the range, negative,
	// overflowing or non-integer stays strictly refused.
	for _, after := range []string{strconv.FormatUint(MaxQueryCitizenID+1, 10), "18446744073709551615", "-9223372036854775808", "-1", "18446744073709551616", "1e3", "1.5", "\"7\"", "null9"} {
		if _, err := DecodeCommand(queryEnvelope("QueryOccupants", `{"by":"home","cell":{"x":1,"y":1},"afterID":`+after+`}`)); err == nil {
			t.Errorf("afterID %s accepted", after)
		}
	}
	for _, after := range []string{"0", "1", "9223372036854775807", "9223372036854775808", strconv.FormatUint(MaxQueryCitizenID, 10)} {
		if _, err := DecodeCommand(queryEnvelope("QueryOccupants", `{"by":"home","cell":{"x":1,"y":1},"afterID":`+after+`,"limit":50}`)); err != nil {
			t.Errorf("in-range afterID %s refused: %v", after, err)
		}
	}
	for _, ref := range []string{"4294967296", "-1"} {
		if _, err := DecodeCommand(queryEnvelope("QueryOccupants", `{"by":"workplace","ref":`+ref+`}`)); err == nil {
			t.Errorf("ref %s accepted", ref)
		}
	}
	for _, x := range []string{"9223372036854775807", "-9223372036854775808"} {
		cmd, err := DecodeCommand(queryEnvelope("QueryOccupants", `{"by":"home","cell":{"x":`+x+`,"y":0}}`))
		t.Logf("cell.x=%s -> err=%v (a huge positive x passing the codec is refused by compose homeRefOfCell) cmd=%v", x, err, cmd.Kind)
	}
}

func TestRoundR1_UnknownFieldsInQueryCitizen(t *testing.T) {
	cmd, err := DecodeCommand(queryEnvelope("QueryCitizen", `{"id":5,"limit":100000,"by":"home"}`))
	t.Logf("unknown fields: err=%v kind=%v (house codec does not DisallowUnknownFields; nothing to smuggle since QueryCitizen has no page)", err, cmd.Kind)
}
