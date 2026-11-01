package logistics

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// BUG-1142 round r1 (F1) -- engine.logistics implements the save.Participant
// contract (structurally; this package never imports engine/save).
//
// Before this participant a Load ran against a fresh Wire whose default
// composition had already provisioned the starting stock, and nothing
// restored the drawn-down shelf: every load refilled ConstructionMaterials
// (300 t minted per save/load cycle in the round's measurement), and the
// composition StateDigest was blind to it.
//
// STATE (serialized, one record per shelf, sorted by district then commodity
// -- GR#21, never a map range):
//   - level        -- the only field Draw/Restock mutate.
//   - capacity     -- set by Provision, which is called by more than the
//     startingStocks data (comms parcel shelves, refuse disposal sites
//     provision on this same API), so it is per-shelf runtime state the
//     data file cannot rebuild.
//   - bufferPolicy -- player-tunable via SetBufferPolicy.
//   - district, commodity -- the shelf identity (the set of shelves is itself
//     state: refuse/comms create them at runtime).
//
// CONFIG (excluded, reloaded from data/logistics.json on load, never restored
// from a save -- the hard-reset-replay premise, FEAT-1972079897): cfg, market,
// correlationID, self (copy guard), mu, and the stockState fields
// holdingCost/shelfLife (re-derived from the commodity config on load). subs
// are runtime wiring registered by Wire, not state.
//
// A load REPLACES the whole shelf set (the first record -- always a meta
// record -- clears it), so the Wire-provisioned shelf never survives a load
// that carried a logistics shard. A bundle with NO logistics shard (written
// before this participant existed) leaves the Wire-provisioned stock in
// place: the correct default, because no earlier build ever had a materials
// supply (the shelf did not exist before BUG-1142).

const (
	// KindLogistics is this participant's stable shard label.
	KindLogistics = "logistics"

	recLogisticsMeta  = "logistics.meta"
	recLogisticsStock = "logistics.stock"
)

// logisticsMetaWire carries the shelf count, so a load of an EMPTY shelf set
// still has a deterministic reset trigger.
type logisticsMetaWire struct {
	Stocks int `json:"stocks"`
}

// logisticsStockWire is one shelf on the wire.
type logisticsStockWire struct {
	District     string `json:"district"`
	Commodity    string `json:"commodity"`
	Level        int64  `json:"level"`
	Capacity     int64  `json:"capacity"`
	BufferPolicy string `json:"bufferPolicy"`
}

// logisticsSnapshot is a point-in-time copy of the shelves.
type logisticsSnapshot struct {
	stocks []logisticsStockWire
}

func (s *logisticsSnapshot) total() int { return 1 + len(s.stocks) }

func (s *logisticsSnapshot) recordAt(i int) (serialize.Record, error) {
	kind := recLogisticsMeta
	var value any = logisticsMetaWire{Stocks: len(s.stocks)}
	if i > 0 {
		kind = recLogisticsStock
		value = s.stocks[i-1]
	}
	b, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("logistics: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: b}, nil
}

// snapshotForSave copies every shelf under the read lock, sorted.
func (l *LogisticsAPI) snapshotForSave() (logisticsSnapshot, error) {
	if err := l.checkNotCopied("snapshotForSave"); err != nil {
		return logisticsSnapshot{}, err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]logisticsStockWire, 0, len(l.stocks))
	for k, st := range l.stocks {
		out = append(out, logisticsStockWire{
			District:     k.district,
			Commodity:    string(k.commodity),
			Level:        st.level,
			Capacity:     st.capacity,
			BufferPolicy: string(st.bufferPolicy),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].District != out[j].District {
			return out[i].District < out[j].District
		}
		return out[i].Commodity < out[j].Commodity
	})
	return logisticsSnapshot{stocks: out}, nil
}

// DigestStocks returns every shelf (sorted by district then commodity) as a
// read-only copy, for the composition StateDigest (the round found logistics
// unhashed).
func (l *LogisticsAPI) DigestStocks() ([]Stock, error) {
	if err := l.checkNotCopied("DigestStocks"); err != nil {
		return nil, err
	}
	snap, err := l.snapshotForSave()
	if err != nil {
		return nil, err
	}
	out := make([]Stock, 0, len(snap.stocks))
	for _, w := range snap.stocks {
		out = append(out, Stock{District: w.District, Commodity: market.CommodityType(w.Commodity), Level: w.Level, Capacity: w.Capacity, BufferPolicy: BufferPolicy(w.BufferPolicy)})
	}
	return out, nil
}

// resetForLoad clears every shelf before a Load streams records in.
func (l *LogisticsAPI) resetForLoad() error {
	if err := l.checkNotCopied("resetForLoad"); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stocks = make(map[stockKey]*stockState)
	return nil
}

// applyLoadRecord decodes and installs one record, fail-closed: an unknown
// commodity or buffer policy, a negative figure, level > capacity or a
// duplicate shelf is a hard error, never a clamp.
func (l *LogisticsAPI) applyLoadRecord(rec serialize.Record) error {
	if err := l.checkNotCopied("applyLoadRecord"); err != nil {
		return err
	}
	switch rec.Kind {
	case recLogisticsMeta:
		var m logisticsMetaWire
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return fmt.Errorf("logistics: decoding %s record: %w", rec.Kind, err)
		}
		return nil
	case recLogisticsStock:
		var w logisticsStockWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("logistics: decoding %s record: %w", rec.Kind, err)
		}
		c := market.CommodityType(w.Commodity)
		cfg, err := l.commodityConfigFor(c)
		if err != nil {
			return fmt.Errorf("logistics: save names unknown commodity %q: %w", w.Commodity, err)
		}
		if _, ok := l.cfg.BufferPolicies[w.BufferPolicy]; !ok {
			return fmt.Errorf("logistics: save names unknown buffer policy %q for %s/%s", w.BufferPolicy, w.District, w.Commodity)
		}
		if w.District == "" || w.Capacity < 0 || w.Level < 0 || w.Level > w.Capacity {
			return fmt.Errorf("logistics: save shelf %s/%s has invalid level %d / capacity %d", w.District, w.Commodity, w.Level, w.Capacity)
		}
		key := stockKey{district: w.District, commodity: c}
		l.mu.Lock()
		defer l.mu.Unlock()
		if _, dup := l.stocks[key]; dup {
			return fmt.Errorf("logistics: save carries duplicate shelf %s/%s", w.District, w.Commodity)
		}
		l.stocks[key] = &stockState{
			level:        w.Level,
			capacity:     w.Capacity,
			holdingCost:  cfg.holdingCost,
			shelfLife:    cfg.shelfLifeTicks,
			bufferPolicy: BufferPolicy(w.BufferPolicy),
		}
		return nil
	default:
		return fmt.Errorf("logistics: unknown logistics save record kind %q", rec.Kind)
	}
}

// SaveParticipant adapts a *LogisticsAPI to the save.Participant contract.
type SaveParticipant struct {
	l *LogisticsAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing l.
func NewSaveParticipant(l *LogisticsAPI) *SaveParticipant {
	// SEC-020 pre-lock guard: a copied LogisticsAPI is still wrapped, but
	// every method below re-checks checkNotCopied and fails closed.
	_ = l.checkNotCopied("NewSaveParticipant")
	return &SaveParticipant{l: l}
}

// Kind returns the logistics shard label.
func (p *SaveParticipant) Kind() string {
	if err := p.l.checkNotCopied("Kind"); err != nil {
		return ""
	}
	return KindLogistics
}

// Source returns a fresh pull-iterator over the shelves.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.l.checkNotCopied("Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.l.snapshotForSave()
	idx := 0
	return func() (serialize.Record, bool, error) {
		if snapErr != nil {
			err := snapErr
			snapErr = nil
			return serialize.Record{}, false, err
		}
		if idx >= snap.total() {
			return serialize.Record{}, false, nil
		}
		rec, err := snap.recordAt(idx)
		if err != nil {
			return serialize.Record{}, false, err
		}
		idx++
		return rec, true, nil
	}
}

// Handler returns a fresh sink; the first record clears the shelf set.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.l.checkNotCopied("Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.l.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.l.applyLoadRecord(rec)
	}
}
