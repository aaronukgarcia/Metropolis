package synth

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestGenerate_ProducesHeaderAndShard is AC-1/AC-2's happy path: Generate
// returns a serialize.Header whose ShardIndex names exactly the one
// "synth" shard it wrote, using int.serializer's own types (no second
// save/bundle shape invented).
func TestGenerate_ProducesHeaderAndShard(t *testing.T) {
	p := Params{CitizenCount: 50, Seed: 123, Sprawl: 0.3, NetworkShape: NetworkGrid}
	var buf bytes.Buffer

	header, err := Generate("t", p, &buf)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if header.WorldSeed != int64(p.Seed) {
		t.Fatalf("header.WorldSeed = %d, want %d", header.WorldSeed, p.Seed)
	}
	if len(header.ShardIndex) != 1 || header.ShardIndex[0].Name != "synth" {
		t.Fatalf("header.ShardIndex = %+v, want exactly one shard named \"synth\"", header.ShardIndex)
	}
	if buf.Len() == 0 {
		t.Fatal("Generate wrote an empty buffer")
	}
}

// TestGenerate_RecordCountScalesWithCitizenCount is AC-1b(a)'s
// "citizenCount ... determine[s] the size of ... the amount of
// generation work performed" made concrete: reading the shard back
// produces exactly CitizenCount+1 records (the synth-meta record, plus
// one synth-citizen record per citizen).
func TestGenerate_RecordCountScalesWithCitizenCount(t *testing.T) {
	for _, n := range []int64{1, 10, 250} {
		p := Params{CitizenCount: n, Seed: 1, Sprawl: 0.5, NetworkShape: NetworkOrganic}
		var buf bytes.Buffer
		if _, err := Generate("t", p, &buf); err != nil {
			t.Fatalf("citizenCount=%d: Generate: %v", n, err)
		}

		var count int64
		var sawMeta bool
		err := (serialize.NDJSONSerializer{}).ReadShard(&buf, 0, func(rec serialize.Record) error {
			switch rec.Kind {
			case "synth-meta":
				sawMeta = true
				var m synthMeta
				if err := json.Unmarshal(rec.Data, &m); err != nil {
					t.Fatalf("decoding synth-meta: %v", err)
				}
				if m.CitizenCount != n {
					t.Fatalf("synth-meta.CitizenCount = %d, want %d", m.CitizenCount, n)
				}
			case "synth-citizen":
				count++
			default:
				t.Fatalf("unexpected record kind %q", rec.Kind)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("citizenCount=%d: ReadShard: %v", n, err)
		}
		if !sawMeta {
			t.Fatalf("citizenCount=%d: no synth-meta record found", n)
		}
		if count != n {
			t.Fatalf("citizenCount=%d: got %d synth-citizen records, want %d", n, count, n)
		}
	}
}

// TestGenerate_AllThreeNetworkShapesProduceOutput exercises every member
// of the closed NetworkShape enum through the real Generate entry point,
// not just ValidateParams.
func TestGenerate_AllThreeNetworkShapesProduceOutput(t *testing.T) {
	for _, shape := range []NetworkShape{NetworkGrid, NetworkRadial, NetworkOrganic} {
		p := Params{CitizenCount: 20, Seed: 9, Sprawl: 0.7, NetworkShape: shape}
		var buf bytes.Buffer
		if _, err := Generate("t", p, &buf); err != nil {
			t.Fatalf("shape %q: Generate: %v", shape, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("shape %q: empty output", shape)
		}
	}
}

// TestPresets_WithinDocumentedBounds proves Preset1M/Preset10M (AC-3)
// stay inside MinSyntheticCitizens..MaxSyntheticCitizens without paying
// the cost of actually generating a 1M/10M-citizen shard in the unit
// test suite — that scale is exercised by cmd/perfci, run manually /in a
// dedicated perf CI job, not on every `go test ./... -race` invocation.
func TestPresets_WithinDocumentedBounds(t *testing.T) {
	for _, p := range []Params{Preset1M(0), Preset10M(0)} {
		if err := ValidateParams("t", p); err != nil {
			t.Fatalf("preset %+v failed ValidateParams: %v", p, err)
		}
	}
	if Preset1M(0).CitizenCount != OneMillionCitizens {
		t.Fatalf("Preset1M citizenCount = %d, want %d", Preset1M(0).CitizenCount, OneMillionCitizens)
	}
	if Preset10M(0).CitizenCount != TenMillionCitizens {
		t.Fatalf("Preset10M citizenCount = %d, want %d", Preset10M(0).CitizenCount, TenMillionCitizens)
	}
}
