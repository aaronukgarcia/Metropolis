package citizens

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// Round r2 attacker (FEAT-2326609845).

func r2Records(t *testing.T, api *CitizensAPI) []serialize.Record {
	t.Helper()
	src := NewSaveParticipant(api).Source()
	var out []serialize.Record
	for {
		rec, ok, err := src()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return out
		}
		out = append(out, serialize.Record{Kind: rec.Kind, Data: append([]byte(nil), rec.Data...)})
	}
}

func r2Feed(api *CitizensAPI, recs []serialize.Record) error {
	h := NewSaveParticipant(api).Handler()
	for _, r := range recs {
		if err := h(r); err != nil {
			return err
		}
	}
	return nil
}

func r2State(t *testing.T) *CitizensAPI {
	api := housingAPI(t, 300)
	caps := []HomeCapacity{}
	for c := 900; c > 100; c -= 37 { // descending input order, many cells
		caps = append(caps, HomeCapacity{Cell: CellRef(c), Capacity: int32(c%5 + 1)})
	}
	must(t, api.SetHomeCapacities(caps, "c"))
	if _, err := api.AssignHomes("c"); err != nil {
		t.Fatal(err)
	}
	return api
}

func TestRoundR2_FEAT845_SaveBytesDeterministic(t *testing.T) {
	var prev []byte
	for i := 0; i < 8; i++ {
		recs := r2Records(t, r2State(t))
		var b bytes.Buffer
		n := 0
		for _, r := range recs {
			b.WriteString(r.Kind)
			b.Write(r.Data)
			if r.Kind == recCitizensHousingCap {
				n++
			}
		}
		if n == 0 {
			t.Fatal("no housingcap records emitted")
		}
		if prev != nil && !bytes.Equal(prev, b.Bytes()) {
			t.Fatalf("run %d: save records differ from run 0", i)
		}
		prev = b.Bytes()
	}
	// save -> load -> save is byte-identical too (staged restore re-saves).
	a := r2State(t)
	recs := r2Records(t, a)
	out, _ := NewCitizensAPI(7, "r2")
	must(t, r2Feed(out, recs))
	again := r2Records(t, out)
	if fmt.Sprint(recs) != fmt.Sprint(again) {
		t.Fatal("save->load->save not identical")
	}
	must(t, out.RestoreHousing("c"))
	again2 := r2Records(t, out)
	if fmt.Sprint(recs) != fmt.Sprint(again2) {
		t.Fatal("save->load->restore->save not identical")
	}
}

func TestRoundR2_FEAT845_MalformedFailsClosed(t *testing.T) {
	recs := r2Records(t, r2State(t))
	mut := func(name string, f func([]serialize.Record) []serialize.Record) {
		cp := make([]serialize.Record, len(recs))
		copy(cp, recs)
		cp = f(cp)
		out, _ := NewCitizensAPI(7, "r2")
		err := r2Feed(out, cp)
		t.Logf("%s -> err=%v", name, err)
		if err == nil {
			t.Errorf("%s: load accepted a malformed housingcap record", name)
		}
	}
	idx := -1
	meta := -1
	for i, r := range recs {
		if r.Kind == recCitizensHousingCap && idx < 0 {
			idx = i
		}
		if strings.HasSuffix(r.Kind, ".meta") || r.Kind == "citizens.meta" {
			meta = i
		}
	}
	set := func(data string) func([]serialize.Record) []serialize.Record {
		return func(c []serialize.Record) []serialize.Record {
			c[idx] = serialize.Record{Kind: recCitizensHousingCap, Data: []byte(data)}
			return c
		}
	}
	mut("garbage", set(`{"cell":`))
	mut("cell0", set(`{"cell":0,"capacity":3}`))
	mut("capneg", set(`{"cell":5,"capacity":-1}`))
	mut("capzero", set(`{"cell":5,"capacity":0}`))
	mut("wrongtype", set(`{"cell":"x","capacity":1}`))
	mut("overflow", set(`{"cell":5,"capacity":99999999999}`))
	mut("cellneg", set(`{"cell":-5,"capacity":1}`))
	if meta >= 0 {
		mut("flag-off-with-caps", func(c []serialize.Record) []serialize.Record {
			var m map[string]any
			_ = json.Unmarshal(c[meta].Data, &m)
			delete(m, "housingOn")
			d, _ := json.Marshal(m)
			c[meta] = serialize.Record{Kind: c[meta].Kind, Data: d}
			return c
		})
	} else {
		t.Logf("meta record kind not found; kinds: %v", recs[0].Kind)
	}
	// Advisory probes (not asserted): duplicate cell, unknown field.
	for _, probe := range []struct{ n, d string }{{"dup", ""}, {"unknownField", `{"cell":5,"capacity":1,"zzz":1}`}} {
		cp := make([]serialize.Record, len(recs))
		copy(cp, recs)
		if probe.n == "dup" {
			cp = append(cp, cp[idx])
		} else {
			cp[idx] = serialize.Record{Kind: recCitizensHousingCap, Data: []byte(probe.d)}
		}
		out, _ := NewCitizensAPI(7, "r2")
		t.Logf("ADVISORY %s -> err=%v", probe.n, r2Feed(out, cp))
	}
}

// F6 via the REAL single-count path (removeColdLocked only, as the death and
// departure commands use), with new homeless arriving and no AssignHomes ever.
func TestRoundR2_FEAT845_PendingBoundedUnderChurn(t *testing.T) {
	api := housingAPI(t, 1000)
	must(t, api.SetHomeCapacities(nil, "c"))
	next := uint64(1001)
	oldest := uint64(1)
	maxRatio := 0.0
	maxLen := 0
	for step := 0; step < 20000; step++ {
		// one arrival, one death (live homeless stays 1000)
		must(t, api.SeedColdRecords([]ColdRecord{{ID: next}}, "c"))
		next++
		api.mu.Lock()
		api.removeColdLocked(oldest)
		oldest++
		l := len(api.housing.pending)
		api.mu.Unlock()
		if l > maxLen {
			maxLen = l
		}
		if r := float64(l) / 1000; r > maxRatio {
			maxRatio = r
		}
	}
	// shrink phase: everybody dies, nobody arrives
	for ; oldest < next; oldest++ {
		api.mu.Lock()
		api.removeColdLocked(oldest)
		api.mu.Unlock()
	}
	api.mu.RLock()
	final := len(api.housing.pending)
	api.mu.RUnlock()
	t.Logf("steady churn: max pending=%d for 1000 live homeless (ratio %.2f); after all die pending=%d", maxLen, maxRatio, final)
	if maxLen > 2*1000+130 {
		t.Errorf("pending grew to %d for 1000 live homeless", maxLen)
	}
	if final > 130 {
		t.Errorf("pending holds %d dead ids with zero live homeless", final)
	}
}
