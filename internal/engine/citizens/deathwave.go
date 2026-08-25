package citizens

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Mortality smoothing / death-wave (FEAT-087, feat.deathwave), self-contained
// within engine.citizens. It consumes the existing Gompertz-Makeham
// MortalityHazard/MortalityDeath (mortality.go) without re-deriving the
// actuarial curve, and wraps them in a bounded monthly realisation path: a
// hazard-selected death is DEFERRED into a [DeathQueue] and released at most
// the monthly budget (data/mortality.json, GR#15) per non-emergency month, so
// a same-birthMonth cohort aging onto the steep Gompertz slope produces a
// smooth ~N-deaths/month tail rather than a single-month population cliff
// (AC-1). Smoothing defers, never destroys: every selected death is eventually
// realised, none dropped, none duplicated (AC-2, §14/§19).
//
// # Weather-driven variation (AC-6/AC-7, adapted to the unregistered edge)
//
// A declared weather emergency suspends the smoothing budget: the queue
// realises at budget×[EmergencyBudgetMultiplier] that month (a major,
// non-smoothed death event). The emergency signal is a deterministic, seeded
// citywide draw [WeatherSeverity] keyed hash(worldSeed, month, "weather") —
// never wall clock, never a shared RNG (GR#21). NOTE (flagged for the SSOT
// pass): the acceptance criteria's intended source is engine.season's weather
// surface, but the engine.citizens → engine.season code.json outbound edge is
// NOT yet registered, so this feature derives the signal locally and leaves
// the engine.season re-pointing as a registered-edge follow-up (ASM-579). It
// does not touch the Gompertz hazard itself (AC-8: suspension of smoothing ≠
// inflation of the hazard).

const (
	// FileMortality is the mortality-smoothing config filename, relative to
	// the resolved data directory (the same module-owned-loader precedent as
	// FileFertility).
	FileMortality = "mortality.json"

	// mortalityDataNotFoundCode / mortalityDataMalformedCode /
	// mortalityDataInvalidCode are the generic foundation.data registry codes
	// (MET-F601/MET-F602/MET-F604) reused for data/mortality.json load
	// failures. GR#7 requires registry-sourced errors with no exceptions; a
	// citizens-specific MET-G010 sub-code is deferred to the registry pass
	// because data/errors.json is out of scope for this change, so the loader
	// reuses foundation.data's own "config not found" / "not well-formed
	// JSON" / "schema validation failed" codes, which already carry
	// {path}/{field}/{rule}/{cause} context.
	mortalityDataNotFoundCode  = "MET-F601"
	mortalityDataMalformedCode = "MET-F602"
	mortalityDataInvalidCode   = "MET-F604"
)

// MortalityNumber is one schema-validated numeric parameter in
// data/mortality.json — mirrors FertilityNumber exactly (value + unit +
// disclosure), so a downstream reader never has to guess units.
type MortalityNumber struct {
	Value      float64 `json:"value"`
	Unit       string  `json:"unit"`
	Disclosure string  `json:"disclosure"`
}

// MortalityMeta is data/mortality.json's documentation block.
type MortalityMeta struct {
	Module        string   `json:"module"`
	FeatureKey    string   `json:"featureKey"`
	SpecRefs      []string `json:"specRefs"`
	BalanceRegime string   `json:"balanceRegime"`
}

// MortalityParams holds the smoothing budget and weather placeholders
// (FEAT-087). Every field is a balance placeholder pending Aaron's
// row-by-row pass — tests assert direction/structure only, never a pinned
// magnitude (the balance-number regime).
type MortalityParams struct {
	MonthlyDeathBudget        MortalityNumber `json:"monthlyDeathBudget"`
	WeatherEmergencyThreshold MortalityNumber `json:"weatherEmergencyThreshold"`
	EmergencyBudgetMultiplier MortalityNumber `json:"emergencyBudgetMultiplier"`
}

// MortalityConfig is the loaded data/mortality.json configuration.
type MortalityConfig struct {
	Version int             `json:"version"`
	Comment string          `json:"$comment"`
	Meta    MortalityMeta   `json:"meta"`
	Params  MortalityParams `json:"params"`
}

// validate rejects a schema-invalid MortalityConfig: a missing unit or
// disclosure, a non-finite value, a negative budget, a threshold outside
// [0,1], or a multiplier below 1. No silent default substitution — the
// malformed parameter is named and the load fails (mirrors
// FertilityConfig.validate).
func (cfg *MortalityConfig) validate(correlationID string) error {
	bad := func(field, rule string) error {
		return errs.New(mortalityDataInvalidCode, correlationID, map[string]any{
			"path": FileMortality, "field": field, "rule": rule,
		})
	}

	if cfg.Version <= 0 {
		return bad("version", "must be positive")
	}
	if cfg.Meta.FeatureKey == "" || cfg.Meta.BalanceRegime == "" {
		return bad("meta", "featureKey and balanceRegime are required")
	}

	numbers := []struct {
		field string
		n     MortalityNumber
	}{
		{"params.monthlyDeathBudget", cfg.Params.MonthlyDeathBudget},
		{"params.weatherEmergencyThreshold", cfg.Params.WeatherEmergencyThreshold},
		{"params.emergencyBudgetMultiplier", cfg.Params.EmergencyBudgetMultiplier},
	}
	for _, e := range numbers {
		if !num.IsFinite(e.n.Value) {
			return bad(e.field+".value", "must be finite")
		}
		if e.n.Unit == "" {
			return bad(e.field+".unit", "is required")
		}
		if e.n.Disclosure == "" {
			return bad(e.field+".disclosure", "is required")
		}
	}

	if cfg.Params.MonthlyDeathBudget.Value < 0 {
		return bad("params.monthlyDeathBudget", "must be non-negative")
	}
	thr := cfg.Params.WeatherEmergencyThreshold.Value
	if thr < 0 || thr > 1 {
		return bad("params.weatherEmergencyThreshold", "must be in [0,1]")
	}
	if cfg.Params.EmergencyBudgetMultiplier.Value < 1 {
		return bad("params.emergencyBudgetMultiplier", "must be >= 1")
	}
	return nil
}

// LoadMortalityConfig reads and validates data/mortality.json from dir,
// returning the parsed MortalityConfig. Every failure is a registry-sourced
// *errs.E (GR#7).
func LoadMortalityConfig(dir, correlationID string) (MortalityConfig, error) {
	var cfg MortalityConfig
	path := filepath.Join(dir, FileMortality)
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, errs.Wrap(mortalityDataNotFoundCode, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, errs.Wrap(mortalityDataMalformedCode, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	if err := cfg.validate(correlationID); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadDefaultMortalityConfig resolves data/'s directory via foundation/data
// and loads data/mortality.json — the convenience entry point NewCitizensAPI
// uses.
func LoadDefaultMortalityConfig(correlationID string) (MortalityConfig, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return MortalityConfig{}, err
	}
	return LoadMortalityConfig(dir, correlationID)
}

// weatherEntityID is the fixed entity id the citywide weather stream is keyed
// on. It is a reserved id (0) distinct from any citizen id in this package's
// stream space, and the "weather" purpose tag keeps it independent of the
// "mortality"/"fertility"/"education"/"employment" streams, so a weather draw
// can never perturb a citizen's own stream (GR#21/M0-ENG §1.2).
const weatherEntityID = uint64(0)

// WeatherSeverity returns the deterministic, seeded monthly weather severity
// in [0,1): a draw from the counter-based hash stream
// hash(worldSeed, weatherEntityID, month, "weather"). It is a pure function of
// (seed, month) — no wall clock, no shared RNG object, byte-identical across
// worker counts and fidelity mixes (AC-14/AC-15).
func WeatherSeverity(seed uint64, month int64) float64 {
	stream := det.NewStream(seed, weatherEntityID, month, "weather")
	return stream.Float64()
}

// IsWeatherEmergency reports whether month is a declared weather emergency:
// its deterministic severity is at or above data/mortality.json's
// weatherEmergencyThreshold. Suspending smoothing is the only effect here —
// the underlying Gompertz-Makeham hazard is NOT modified (AC-8).
func IsWeatherEmergency(seed uint64, month int64, cfg MortalityConfig) bool {
	return WeatherSeverity(seed, month) >= cfg.Params.WeatherEmergencyThreshold.Value
}

// MonthlyDeathBudget returns the maximum number of selected deaths realised
// in month: the data-sourced budget, or budget×EmergencyBudgetMultiplier
// during a weather emergency. It is always bounded (non-negative and finite)
// — even an emergency month is capped, never "kill everyone" (AC-1's "no
// single month kills everyone" holds in every month).
func MonthlyDeathBudget(seed uint64, month int64, cfg MortalityConfig) int {
	budget := int(cfg.Params.MonthlyDeathBudget.Value)
	if budget < 0 {
		budget = 0
	}
	if IsWeatherEmergency(seed, month, cfg) {
		mult := cfg.Params.EmergencyBudgetMultiplier.Value
		if mult < 1 {
			mult = 1
		}
		return int(float64(budget) * mult)
	}
	return budget
}

// QueuedDeath is one hazard-selected, not-yet-realised death. SelectionMonth
// is the calendar month the hazard draw selected it (AC-3: the single,
// terminal selection event).
type QueuedDeath struct {
	CitizenID      uint64
	SelectionMonth int64
}

// RealisedDeath is one death released from the queue into the FEAT-088 death
// services handoff surface, carrying the three fields a funeral-throughput
// consumer needs — (CitizenID, DeathMonth, Emergency) — in AC-9's order.
type RealisedDeath struct {
	CitizenID  uint64
	DeathMonth int64
	Emergency  bool
}

// DeathQueue is the city-wide smoothing queue (AC-1/AC-2/AC-4). It holds
// hazard-selected deaths and releases them at a bounded monthly rate in a
// deterministic FIFO total order: by (selection month, then citizen id). A
// queued citizen is the single, terminal selection event — Enqueue is
// idempotent, and Realise sorts pending entries so the realisation sequence
// is a pure function of the queued set, never of the enqueue order a given
// worker count happened to produce (AC-4/AC-15).
type DeathQueue struct {
	// entries is the pending (selected, not-yet-realised) death list. It is
	// re-sorted by (SelectionMonth, CitizenID) at the start of Realise, so
	// its order never depends on shard-completion order.
	entries []QueuedDeath
	// index is a set of queued citizen ids so Queued() is O(1) and Enqueue is
	// idempotent.
	index map[uint64]struct{}
}

// NewDeathQueue returns an empty queue.
func NewDeathQueue() *DeathQueue {
	return &DeathQueue{index: make(map[uint64]struct{})}
}

// Len returns the number of pending (selected, not-yet-realised) deaths.
func (q *DeathQueue) Len() int { return len(q.entries) }

// Queued reports whether citizenID already has a pending death entry (its
// single terminal selection).
func (q *DeathQueue) Queued(citizenID uint64) bool {
	_, ok := q.index[citizenID]
	return ok
}

// Enqueue records a hazard-selected death. Idempotent: a citizen already in
// the queue is not re-selected (AC-3 — the queue entry is the single,
// terminal selection event).
func (q *DeathQueue) Enqueue(citizenID uint64, selectionMonth int64) {
	if _, ok := q.index[citizenID]; ok {
		return
	}
	q.index[citizenID] = struct{}{}
	q.entries = append(q.entries, QueuedDeath{CitizenID: citizenID, SelectionMonth: selectionMonth})
}

// Remove cancels a pending death for citizenID. It is the dequeue a departing
// ALIVE citizen needs: emigration reaches the death-queue via the
// LifeEventDeath command path (applyEmigration), and a hazard-selected
// citizen who leaves the city before their selected death is realised must be
// dropped here — otherwise realiseDeathsLocked later drains the emigrant and
// records a RealisedDeath for a citizen who did NOT die (AC-13 phantom
// death). A no-op for a citizen with no pending entry; the surviving entries'
// relative order is irrelevant because Realise re-sorts by (selection month,
// citizen id) at release time.
func (q *DeathQueue) Remove(citizenID uint64) {
	if _, ok := q.index[citizenID]; !ok {
		return
	}
	delete(q.index, citizenID)
	for i, e := range q.entries {
		if e.CitizenID == citizenID {
			q.entries = append(q.entries[:i], q.entries[i+1:]...)
			return
		}
	}
}

// Realise releases up to budget queued deaths in deterministic FIFO order
// (selection month, then citizen id), removing them from the queue and
// returning them as RealisedDeath records tagged with deathMonth and
// emergency. The remainder stays queued — smoothing defers, never destroys
// (AC-2).
func (q *DeathQueue) Realise(budget int, month int64, emergency bool) []RealisedDeath {
	if budget <= 0 || len(q.entries) == 0 {
		return nil
	}
	sort.Slice(q.entries, func(i, j int) bool {
		if q.entries[i].SelectionMonth != q.entries[j].SelectionMonth {
			return q.entries[i].SelectionMonth < q.entries[j].SelectionMonth
		}
		return q.entries[i].CitizenID < q.entries[j].CitizenID
	})

	n := budget
	if n > len(q.entries) {
		n = len(q.entries)
	}
	realised := make([]RealisedDeath, 0, n)
	for _, e := range q.entries[:n] {
		realised = append(realised, RealisedDeath{
			CitizenID: e.CitizenID, DeathMonth: month, Emergency: emergency,
		})
	}

	q.entries = q.entries[n:]
	q.index = make(map[uint64]struct{}, len(q.entries))
	for _, e := range q.entries {
		q.index[e.CitizenID] = struct{}{}
	}
	return realised
}
