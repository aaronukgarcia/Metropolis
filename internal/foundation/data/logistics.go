package data

import (
	"encoding/json"
	"sort"
)

// This file defines data/logistics.json's typed schema (engine.logistics,
// MOD-025), routed through the SAME generic [Load] every other §24 file
// uses — the identical split engine.season (LoadSeasonal) and
// engine.market (LoadMarketFile, MOD-020 ruling 1) already draw, not a
// third self-contained loader.
//
// LogisticsFile.Validate covers every check that applies uniformly to a
// commodity record regardless of WHICH commodity it is: a non-empty unit,
// a non-negative throughput, a shortfall factor strictly in (0, 1], a
// non-negative shelf life, a non-negative holding cost, and a
// defaultBufferPolicy that names a policy present in the file's own
// bufferPolicies map. It deliberately does NOT check that all nine §6
// commodities are present — that requires knowing which commodity keys
// this consumer needs, which is engine.logistics domain knowledge, exactly
// the same reasoning that keeps Seasonal's requiredCurves list and
// Market's waste-specific XOR check OUT of this package and in the owning
// engine module's own Load instead. engine.logistics.Load performs that
// completeness check itself, the same way engine.season.Load and
// engine.market.Load perform their own key-specific checks after calling
// the shared loader.

// FileLogistics is data/logistics.json's filename, relative to the
// resolved data directory (see ResolveDataDir). Added here per the same
// MOD-020-ruling-1 precedent that gave market.json its FileMarket
// constant rather than growing load.go's §24 constant block, which is
// written specifically about the eight files LoadAll aggregates.
const FileLogistics = "logistics.json"

// BufferPolicyRecord is one data/logistics.json "bufferPolicies" entry:
// a player-tunable safety-buffer tier (US-10/AC-3). SafetyBuffer is the
// fraction of forecast demand added to a replenishment order — "lean"
// smaller (lower holding cost, higher stockout risk), "fat" larger
// (higher holding cost, lower stockout risk). Exact values are unpinned
// balance data (ASM-234), so this field is a plain number read from the
// file, never a Go literal in engine.logistics.
type BufferPolicyRecord struct {
	SafetyBuffer float64 `json:"safetyBuffer"`
	Comment      string  `json:"comment,omitempty"`
}

// LogisticsCommodityRecord is one data/logistics.json "commodities"
// entry: the per-commodity coarse delivery model. Throughput is the
// nominal per-tick delivery capacity into one district; ShortfallFactor
// is the deterministic delivery-reliability fraction in (0,1] that
// converts nominal throughput into effective throughput (the coarse
// abstraction standing in for congestion/loss the full JIT model would
// compute); ShelfLifeTicks is the shelf-life clock (0 = non-perishable);
// HoldingCostMicropoundsPerUnitPerTick is the per-unit per-tick holding
// cost in micro-pounds (M0-ENG §1.2); DefaultBufferPolicy names which of
// the file's bufferPolicies a newly provisioned stock starts on.
type LogisticsCommodityRecord struct {
	Unit                                 string  `json:"unit"`
	Throughput                           int64   `json:"throughput"`
	ShortfallFactor                      float64 `json:"shortfallFactor"`
	ShelfLifeTicks                       int64   `json:"shelfLifeTicks"`
	HoldingCostMicropoundsPerUnitPerTick int64   `json:"holdingCostMicropoundsPerUnitPerTick"`
	DefaultBufferPolicy                  string  `json:"defaultBufferPolicy"`
	Comment                              string  `json:"comment,omitempty"`
}

// LogisticsFile is data/logistics.json's top-level schema (§8/§II.5,
// MOD-025). Commodities is keyed by the raw commodity string (the same
// keys engine.market's CommodityType and data/market.json use) — this
// package does not import engine.market's CommodityType (the reverse of
// the intended dependency direction, foundation -> engine), so the key
// stays a plain string here; engine.logistics.Load re-types each key
// into engine.market's CommodityType after a successful Load.
type LogisticsFile struct {
	Version        int                                 `json:"version"`
	Meta           json.RawMessage                     `json:"meta,omitempty"`
	BufferPolicies map[string]BufferPolicyRecord       `json:"bufferPolicies"`
	Commodities    map[string]LogisticsCommodityRecord `json:"commodities"`
}

// Validate implements Validator. See this file's package-level doc
// comment for exactly which checks live here versus in
// engine.logistics.Load, and why.
func (l *LogisticsFile) Validate() error {
	if err := requireVersion(l.Version); err != nil {
		return err
	}

	// Deterministic (sorted) iteration over both maps — Go map iteration
	// order is randomized per-run, so a logistics.json with MULTIPLE
	// simultaneously-violating entries would otherwise blame a different
	// entry on different runs against the byte-identical file (GR#21,
	// BUG-098's lesson, mirrored from MarketFile.Validate).
	policyNames := make([]string, 0, len(l.BufferPolicies))
	for name := range l.BufferPolicies {
		policyNames = append(policyNames, name)
	}
	sort.Strings(policyNames)
	for _, name := range policyNames {
		rec := l.BufferPolicies[name]
		if rec.SafetyBuffer < 0 {
			return fieldErr("bufferPolicies["+name+"].safetyBuffer", "must be >= 0")
		}
	}

	commodityNames := make([]string, 0, len(l.Commodities))
	for name := range l.Commodities {
		commodityNames = append(commodityNames, name)
	}
	sort.Strings(commodityNames)
	for _, name := range commodityNames {
		rec := l.Commodities[name]
		if err := requireNonEmptyString("commodities["+name+"].unit", rec.Unit); err != nil {
			return err
		}
		if rec.Throughput < 0 {
			return fieldErr("commodities["+name+"].throughput", "must be >= 0")
		}
		if rec.ShortfallFactor <= 0 || rec.ShortfallFactor > 1 {
			return fieldErr("commodities["+name+"].shortfallFactor", "must be in (0, 1]")
		}
		if rec.ShelfLifeTicks < 0 {
			return fieldErr("commodities["+name+"].shelfLifeTicks", "must be >= 0 (0 = non-perishable)")
		}
		if rec.HoldingCostMicropoundsPerUnitPerTick < 0 {
			return fieldErr("commodities["+name+"].holdingCostMicropoundsPerUnitPerTick", "must be >= 0")
		}
		if err := requireNonEmptyString("commodities["+name+"].defaultBufferPolicy", rec.DefaultBufferPolicy); err != nil {
			return err
		}
		if _, ok := l.BufferPolicies[rec.DefaultBufferPolicy]; !ok {
			return fieldErr("commodities["+name+"].defaultBufferPolicy",
				"must name a policy present in bufferPolicies (got \""+rec.DefaultBufferPolicy+"\")")
		}
	}
	return nil
}
