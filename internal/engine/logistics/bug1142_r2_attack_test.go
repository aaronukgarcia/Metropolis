package logistics

// BUG-1142 destructive round r2 (opus-round-r2-bug1142): hostile logistics
// save shards must be refused fail-closed, and the refusal must surface as
// the save layer's registry code (MET-E813) through a real Manager.Load.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

func r2rec(t *testing.T, kind string, v any) serialize.Record {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return serialize.Record{Kind: kind, Data: b}
}

func TestBUG1142R2_Attack_HostileRecordsRefused(t *testing.T) {
	good := logisticsStockWire{District: "city", Commodity: string(market.ConstructionMaterials), Level: 10, Capacity: 100, BufferPolicy: "lean"}
	mut := func(f func(*logisticsStockWire)) logisticsStockWire { w := good; f(&w); return w }
	cases := map[string][]serialize.Record{
		"levelOverCapacity": {r2rec(t, recLogisticsMeta, logisticsMetaWire{1}), r2rec(t, recLogisticsStock, mut(func(w *logisticsStockWire) { w.Level = 101 }))},
		"negativeLevel":     {r2rec(t, recLogisticsMeta, logisticsMetaWire{1}), r2rec(t, recLogisticsStock, mut(func(w *logisticsStockWire) { w.Level = -1 }))},
		"negativeCapacity":  {r2rec(t, recLogisticsMeta, logisticsMetaWire{1}), r2rec(t, recLogisticsStock, mut(func(w *logisticsStockWire) { w.Capacity = -1; w.Level = -1 }))},
		"unknownCommodity":  {r2rec(t, recLogisticsMeta, logisticsMetaWire{1}), r2rec(t, recLogisticsStock, mut(func(w *logisticsStockWire) { w.Commodity = "unobtainium" }))},
		"unknownPolicy":     {r2rec(t, recLogisticsMeta, logisticsMetaWire{1}), r2rec(t, recLogisticsStock, mut(func(w *logisticsStockWire) { w.BufferPolicy = "hoard" }))},
		"emptyDistrict":     {r2rec(t, recLogisticsMeta, logisticsMetaWire{1}), r2rec(t, recLogisticsStock, mut(func(w *logisticsStockWire) { w.District = "" }))},
		"duplicateShelf":    {r2rec(t, recLogisticsMeta, logisticsMetaWire{2}), r2rec(t, recLogisticsStock, good), r2rec(t, recLogisticsStock, good)},
		"unknownKind":       {r2rec(t, recLogisticsMeta, logisticsMetaWire{0}), {Kind: "logistics.bogus", Data: []byte("{}")}},
		"truncatedJSON":     {r2rec(t, recLogisticsMeta, logisticsMetaWire{1}), {Kind: recLogisticsStock, Data: []byte(`{"district":"city","commod`)}},
		"badMetaJSON":       {{Kind: recLogisticsMeta, Data: []byte(`{"stocks":"x"}`)}},
	}
	for name, recs := range cases {
		name, recs := name, recs
		t.Run(name, func(t *testing.T) {
			a, err := LoadDefault(errs.NewCorrelationID())
			if err != nil {
				t.Fatal(err)
			}
			h := NewSaveParticipant(a).Handler()
			var last error
			for _, r := range recs {
				if last = h(r); last != nil {
					break
				}
			}
			if last == nil {
				t.Fatalf("hostile shard %s accepted", name)
			}
		})
	}
}

// TestBUG1142R2_Attack_MetaCountNotCrossChecked: the meta record's stock
// count is never checked against the records that follow. Logged probe.
func TestBUG1142R2_Attack_MetaCountNotCrossChecked(t *testing.T) {
	a, _ := LoadDefault(errs.NewCorrelationID())
	if err := a.ProvisionStartingStocks(); err != nil {
		t.Fatal(err)
	}
	h := NewSaveParticipant(a).Handler()
	if err := h(r2rec(t, recLogisticsMeta, logisticsMetaWire{Stocks: 5})); err != nil {
		t.Logf("meta-only shard refused: %v", err)
		return
	}
	s, _ := a.DigestStocks()
	t.Logf("meta says 5 shelves, zero stock records followed: load accepted, %d shelves now (count not cross-checked)", len(s))
}

// r2HostileSource wraps the real participant so the save it writes carries a
// level > capacity shelf (the bundle's own size/hash integrity is then
// consistent, so the refusal must come from the logistics Handler).
type r2HostileSource struct{ *SaveParticipant }

func (p r2HostileSource) Source() serialize.RecordSource {
	src := p.SaveParticipant.Source()
	return func() (serialize.Record, bool, error) {
		rec, ok, err := src()
		if err != nil || !ok || rec.Kind != recLogisticsStock {
			return rec, ok, err
		}
		var w logisticsStockWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return rec, ok, err
		}
		w.Level = w.Capacity + 1
		rec.Data, err = json.Marshal(w)
		return rec, ok, err
	}
}

// TestBUG1142R2_Attack_ManagerSurfacesRegistryCode: a self-consistent bundle
// whose logistics shard carries level > capacity is refused by a real
// Manager.Load with the save layer's MET-E813 registry code.
func TestBUG1142R2_Attack_ManagerSurfacesRegistryCode(t *testing.T) {
	cid := errs.NewCorrelationID()
	a, _ := LoadDefault(cid)
	if err := a.ProvisionStartingStocks(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mgr := save.NewManager(root, []save.Participant{r2HostileSource{NewSaveParticipant(a)}}, cid)
	if err := mgr.SaveManual(save.Context{WorldSeed: 1, CreatedAtTick: 1, AppVersion: "t"}, "det"); err != nil {
		t.Fatal(err)
	}
	b, _ := LoadDefault(cid)
	_, _, lerr := save.NewManager(root, []save.Participant{NewSaveParticipant(b)}, cid).Load(bundleDir(t, root))
	if lerr == nil {
		t.Fatal("hostile shard (level > capacity) loaded")
	}
	t.Logf("refusal: %v", lerr)
	if !strings.Contains(lerr.Error(), "MET-E813") || !strings.Contains(lerr.Error(), "invalid level") {
		t.Errorf("refusal is not the handler's MET-E813: %v", lerr)
	}
}
