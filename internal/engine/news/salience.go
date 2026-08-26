package news

import (
	_ "embed"
	"encoding/json"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file owns the salience-scoring editor (§29.2: "editor = salience
// scoring: deaths, firsts, records, crises, milestones") and its GR#15
// data file.
//
// §29 names the five categories but gives no relative weighting between
// them, so the weight table is data (salience.json) rather than a Go
// literal. The weights are placeholders pending Aaron's balance pass —
// salience.json carries a disclosure field stating exactly that, and a
// balance change is a data edit, never a code change.

//go:embed salience.json
var embeddedSalienceJSON []byte

// salienceFile is salience.json's schema: the per-category weight table
// plus a non-empty disclosure naming the values as pending tuning.
type salienceFile struct {
	Version    int                `json:"version"`
	Weights    map[string]float64 `json:"weights"`
	Disclosure string             `json:"disclosure"`
}

// salienceDataInvalid builds the MET-G2301 data-invalid error with the full
// ctx its template renders: field/rule/cause. Every salience call site must
// supply all three keys, or a missing one reaches the user as a literal token
// instead of a value (BUG-357: MET-G2301 previously had validators supplying
// only {field,rule} while the template also names {cause}).
func salienceDataInvalid(correlationID, field, rule, cause string) error {
	return errs.New(ErrSalienceDataInvalid, correlationID, map[string]any{
		"field": field,
		"rule":  rule,
		"cause": cause,
	})
}

// loadSalienceWeights unmarshals and validates the embedded salience.json,
// returning the weight map keyed by [Category]. It is deterministic and
// does not cache: it is expected to be called once at [New] (and directly
// by tests that exercise the failure paths), so there is no sync.Once
// state to reset. Every failure is a registry-sourced *errs.E (GR#7) —
// never a silent default substitution (GR#15).
func loadSalienceWeights(correlationID string) (map[Category]float64, error) {
	var sf salienceFile
	if err := json.Unmarshal(embeddedSalienceJSON, &sf); err != nil {
		// Route Unmarshal errors through the helper to supply full {field,rule,cause} ctx.
		return nil, errs.Wrap(ErrSalienceDataInvalid, correlationID, err, map[string]any{
			"field": "",
			"rule":  "JSON format or schema",
			"cause": err.Error(),
		})
	}
	if sf.Disclosure == "" {
		return nil, salienceDataInvalid(correlationID, "disclosure", "non-empty pending-tuning disclosure required (GR#15)", "")
	}

	weights := make(map[Category]float64, len(sf.Weights))
	for name, w := range sf.Weights {
		c := Category(name)
		if !ValidCategory(c) {
			return nil, salienceDataInvalid(correlationID, "weights."+name, "category must be one of death|first|record|crisis|milestone (§29)", "")
		}
		if w <= 0 || !num.IsFinite(w) {
			return nil, salienceDataInvalid(correlationID, "weights."+name, "weight must be a positive finite number (never NaN/±Inf — GR#21)", "")
		}
		weights[c] = w
	}

	// Completeness: every §29 category must carry a weight. Iterated over
	// the ordered allCategories, never the map, so a file missing several
	// weights reports the same (first) missing category every run (GR#21).
	for _, c := range allCategories {
		if _, ok := weights[c]; !ok {
			return nil, salienceDataInvalid(correlationID, "weights."+string(c), "weight required for every §29 category", "")
		}
	}
	return weights, nil
}

// salience returns the editor's score for an event: category weight ×
// magnitude. Magnitude is widened to float64 only here, for ranking — the
// stored magnitude and the annual aggregates stay int64 (GR#16).
func salience(cat Category, magnitude int64, weights map[Category]float64) float64 {
	return weights[cat] * float64(magnitude)
}

// scored is one event's resolved story plus its editor score, the
// intermediate the bulletin/annual/epilogue rank from.
type scored struct {
	story    Story
	salience float64
}

// rankEvents ranks persisted records by salience descending, breaking ties
// by EventID ascending (AC-10's documented, deterministic tie-break rule).
// It ranges over the input slice (never a map) and uses sort.SliceStable,
// so the order is identical across repeated runs and worker counts. Each
// story is rebuilt from the record's ingest-time name — never re-resolved
// against a live namer, so ranking cannot fail or drop a story after the
// namer changes (SEC-110).
func rankEvents(records []record, cfg Config) []scored {
	out := make([]scored, 0, len(records))
	for _, r := range records {
		out = append(out, scored{
			story:    buildStory(r.ev, r.name),
			salience: salience(r.ev.Category, r.ev.Magnitude, cfg.Weights),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].salience != out[j].salience {
			return out[i].salience > out[j].salience
		}
		return out[i].story.EventID < out[j].story.EventID
	})
	return out
}

// buildStory assembles the emitted [Story] for one event with an
// already-resolved name (the name may be empty when the event has no named
// entity, or the empty name recorded at ingest). It performs no resolution
// and cannot fail — the name was resolved once, at ingest, and is stored
// with the record (SEC-110).
func buildStory(ev Event, name string) Story {
	return Story{
		EventID:  ev.ID,
		EntityID: ev.EntityID,
		Tick:     ev.Tick,
		Month:    monthOf(ev.Tick),
		Name:     name,
		Text:     ev.Text,
	}
}

// resolveName resolves one event's named entity through cfg.Namer. An event
// with no entity reference yields an empty name (valid — not every story has
// a named entity). An event whose entity reference cannot be resolved yields
// [ErrUnresolvedEntity] (AC-8), never a fabricated placeholder name.
func resolveName(ev Event, cfg Config, correlationID string) (string, error) {
	if ev.EntityID == "" {
		return "", nil
	}
	if cfg.Namer == nil {
		return "", errs.New(ErrUnresolvedEntity, correlationID, map[string]any{
			"entityId": ev.EntityID,
			"eventId":  ev.ID,
			"cause":    "no RoadNamer wired (engine.roads seam unset)",
		})
	}
	name, err := cfg.Namer.RoadName(ev.EntityID)
	if err != nil {
		return "", errs.Wrap(ErrUnresolvedEntity, correlationID, err, map[string]any{
			"entityId": ev.EntityID,
			"eventId":  ev.ID,
			"cause":    err.Error(),
		})
	}
	if name == "" {
		return "", errs.New(ErrUnresolvedEntity, correlationID, map[string]any{
			"entityId": ev.EntityID,
			"eventId":  ev.ID,
			"cause":    "RoadNamer returned an empty name",
		})
	}
	return name, nil
}

// materializeStory resolves one event's named entity and builds its emitted
// [Story] — the ingest path, where a name is resolved exactly once before it
// is persisted with the record.
func materializeStory(ev Event, cfg Config, correlationID string) (Story, error) {
	name, err := resolveName(ev, cfg, correlationID)
	if err != nil {
		return Story{}, err
	}
	return buildStory(ev, name), nil
}
