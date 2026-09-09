package traffic

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FEAT-2326609792 inc1 "TABLE FOUNDATION" — the population scale-ladder
// loader + log-linear interpolator (docs/planning/acceptance/
// FEAT-2326609792-inc1.md). This file is schema-agnostic per the Lead's
// 2026-09-09 amendment to AC-11: it does NOT know the field names inc0's
// data/traffic/scale_ladder.json declares (finance/mode-share/etc). Instead
// every rung is flattened into an ordered list of {Key, Value} leaves keyed
// by dotted JSON path (e.g. "tripsByMode.car"), sorted byte-wise ascending
// by Key (GR#21 — no map-range-with-break on any output path). Non-numeric
// leaves (strings, booleans, null, mixed/non-numeric arrays) are carried
// through UNCHANGED from the LOWER rung of the interpolated pair and are
// never arithmetically touched. inc2 will add a typed accessor once the
// field list exists; this surface (LoadScaleLadder / ScaleLadderAt / Rungs)
// does not change between inc1 and inc2 dispatch (AC-11).

// Registry error codes (GR#7) claimed via `node tools/plan/add-error.js
// claim-range engine.traffic --size 8` and minted with `add` (data/errors.json).
const (
	ErrScaleLadderMissingFile          = "MET-G5424"
	ErrScaleLadderUnparsable           = "MET-G5425"
	ErrScaleLadderTooFewRungs          = "MET-G5426"
	ErrScaleLadderUnsortedOrDuplicate  = "MET-G5427"
	ErrScaleLadderRungCountMismatch    = "MET-G5428"
	ErrScaleLadderNonFiniteLeaf        = "MET-G5429"
	ErrScaleLadderPopulationOutOfRange = "MET-G5430"
	ErrScaleLadderFileTooLarge         = "MET-G5431"
	// ErrScaleLadderKeySetMismatch (BUG-829, rework): a rung's flattened key
	// sequence (numeric or non-numeric) differs from rung 0's. Checked at
	// LOAD time, once, over every rung -- interpolateLadder's by-index merge
	// is only safe once every rung is known to share rung 0's exact key
	// sequence (see keySequencesEqual / validateUniformKeySets below).
	ErrScaleLadderKeySetMismatch = "MET-G5432"
)

// scaleLadderMaxFileBytes bounds the ladder file size before Unmarshal
// (FEAT-135 secure-by-default). LoadConfig's traffic.json precedent has no
// such bound; the scale ladder is expected to be far larger (18 rungs of
// dozens of fields each) so this file adds one rather than inheriting an
// absent cap.
const scaleLadderMaxFileBytes = 4 * 1024 * 1024 // 4 MiB

// scaleLadderFileName is the ladder's path under the resolved data
// directory: dir/traffic/scale_ladder.json (dir is the same base "data/"
// directory LoadConfig's dir parameter already resolves to; traffic.json
// sits directly under dir while the ladder — owned by a different BOW item,
// FEAT-2326609792 inc0 — sits under dir/traffic/, matching the real
// data/traffic/scale_ladder.json layout).
const scaleLadderFileName = "traffic" + string(filepath.Separator) + "scale_ladder.json"

// LadderField is one interpolated numeric leaf of a queried rung, keyed by
// its dotted JSON path. Fields is always sorted byte-wise ascending by Key
// (GR#21 determinism — no map iteration order ever reaches an output).
type LadderField struct {
	Key   string
	Value float64
}

// NonNumericField is one leaf that was NOT numeric on the source rung(s)
// (a string, bool, null, or a non-numeric/mixed array). Per the Lead's
// AC-11 amendment its value is carried verbatim from the LOWER rung of the
// interpolated pair (or the exact rung, for an exact-population query) —
// never interpolated. RawValue is the leaf's raw JSON encoding.
type NonNumericField struct {
	Key      string
	RawValue json.RawMessage
}

// LadderPoint is the result of interpolating (or exact-matching) the scale
// ladder at one population. AC-11's registered surface: ScaleLadderAt
// returns this, never a string-keyed per-field lookup function.
type LadderPoint struct {
	Population int64
	Fields     []LadderField     // sorted by Key
	NonNumeric []NonNumericField // sorted by Key, carried from the lower rung
}

// ladderRung is one parsed, flattened rung of the loaded ladder.
type ladderRung struct {
	population int64
	numeric    []LadderField     // sorted by Key
	nonNumeric []NonNumericField // sorted by Key
}

// ScaleLadder is the loaded, validated, immutable ladder. Interpolation
// (interpolateLadder) is a pure function of a *ScaleLadder and a population
// — no citizen state, no clock, no randomness (AC-8/AC-9).
type ScaleLadder struct {
	rungs []ladderRung // strictly ascending by population, len >= 2
}

// scaleLadderFileMeta mirrors only the top-level fields this loader reads;
// the "provenance" and any other top-level key are ignored (schema-agnostic
// per the amendment — this loader never assumes a fixed field list beyond
// the ladder's own envelope of version/meta/rungs).
type scaleLadderFileMeta struct {
	RungPopulations []int64 `json:"rungPopulations"`
}

type scaleLadderFile struct {
	Meta  scaleLadderFileMeta `json:"meta"`
	Rungs []json.RawMessage   `json:"rungs"`
}

// rungEnvelope reads just the "population" key off a raw rung so the rest
// of the rung can be flattened generically without a fixed field list.
type rungEnvelope struct {
	Population *int64 `json:"population"`
}

// LoadScaleLadder loads and validates data/traffic/scale_ladder.json (via
// filepath.Join(dir, "traffic", "scale_ladder.json")) into t's ladder,
// following the LoadConfig precedent (api.go:139-163): path construction,
// os.ReadFile, encoding/json.Unmarshal, a validation function, invalidInputf
// errors, and the checkNotCopied guard (AC-3). AC-1: every failure mode
// (missing file, unparsable JSON, too-large file, structurally invalid
// ladder) returns a registry-sourced error, never a panic or silent default.
func (t *TrafficAPI) LoadScaleLadder(dir string) error {
	if err := t.checkNotCopied("LoadScaleLadder"); err != nil {
		return err
	}

	path := filepath.Join(dir, scaleLadderFileName)

	f, err := os.Open(path)
	if err != nil {
		return errs.New(ErrScaleLadderMissingFile, t.correlationID, map[string]any{"path": path})
	}
	defer func() { _ = f.Close() }()

	// FEAT-135 secure-by-default: bound the size read BEFORE Unmarshal
	// rather than trusting os.ReadFile to hand back an arbitrarily large
	// buffer. io.LimitReader + a one-byte-over probe distinguishes
	// "exactly at the cap" (fine) from "over the cap" (rejected).
	limited := io.LimitReader(f, scaleLadderMaxFileBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return errs.New(ErrScaleLadderUnparsable, t.correlationID, map[string]any{"path": path, "reason": err.Error()})
	}
	if len(raw) > scaleLadderMaxFileBytes {
		return errs.New(ErrScaleLadderFileTooLarge, t.correlationID, map[string]any{
			"maxBytes": scaleLadderMaxFileBytes,
			"actual":   len(raw),
		})
	}

	var file scaleLadderFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return errs.New(ErrScaleLadderUnparsable, t.correlationID, map[string]any{"path": path, "reason": err.Error()})
	}

	ladder, err := buildScaleLadder(file, t.correlationID)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.ladder = ladder
	return nil
}

// buildScaleLadder validates a parsed scaleLadderFile and flattens each raw
// rung into a ladderRung. Split out from LoadScaleLadder so tests can drive
// it directly from hand-built fixtures without touching disk (mirrors
// validateConfig's split, api.go).
func buildScaleLadder(file scaleLadderFile, correlationID string) (*ScaleLadder, error) {
	// AC-2/GR#15: the rung count and population list are DERIVED from the
	// data file's own meta.rungPopulations declaration, never a literal.
	declared := file.Meta.RungPopulations
	if len(file.Rungs) < 2 || len(declared) < 2 {
		return nil, errs.New(ErrScaleLadderTooFewRungs, correlationID, map[string]any{"count": len(file.Rungs)})
	}
	if len(declared) != len(file.Rungs) {
		return nil, errs.New(ErrScaleLadderRungCountMismatch, correlationID, map[string]any{
			"declared": len(declared),
			"actual":   len(file.Rungs),
		})
	}

	rungs := make([]ladderRung, 0, len(file.Rungs))
	for i, raw := range file.Rungs {
		var env rungEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, errs.New(ErrScaleLadderUnparsable, correlationID, map[string]any{
				"path": fmt.Sprintf("rungs[%d]", i), "reason": err.Error(),
			})
		}
		if env.Population == nil {
			return nil, errs.New(ErrScaleLadderUnparsable, correlationID, map[string]any{
				"path": fmt.Sprintf("rungs[%d]", i), "reason": "missing \"population\" key",
			})
		}
		if *env.Population != declared[i] {
			return nil, errs.New(ErrScaleLadderRungCountMismatch, correlationID, map[string]any{
				"declared": declared[i],
				"actual":   *env.Population,
			})
		}

		var body map[string]json.RawMessage
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, errs.New(ErrScaleLadderUnparsable, correlationID, map[string]any{
				"path": fmt.Sprintf("rungs[%d]", i), "reason": err.Error(),
			})
		}
		delete(body, "population")

		numeric, nonNumeric, err := flattenRung(body, correlationID, *env.Population)
		if err != nil {
			return nil, err
		}

		rungs = append(rungs, ladderRung{
			population: *env.Population,
			numeric:    numeric,
			nonNumeric: nonNumeric,
		})
	}

	// AC-1: strictly ascending, no duplicates.
	for i := 1; i < len(rungs); i++ {
		if rungs[i].population <= rungs[i-1].population {
			return nil, errs.New(ErrScaleLadderUnsortedOrDuplicate, correlationID, map[string]any{
				"index": i, "prev": rungs[i-1].population, "next": rungs[i].population,
			})
		}
	}

	// BUG-829 (P1 PANIC): interpolateLadder merges bracketing rungs BY
	// INDEX, which is only safe if every rung shares the identical
	// flattened key sequence. Validate that here, once, at load time, so a
	// ragged/mismatched file is REJECTED with a registry error rather than
	// panicking (or silently mispairing) at query time.
	if err := validateUniformKeySets(rungs, correlationID); err != nil {
		return nil, err
	}

	return &ScaleLadder{rungs: rungs}, nil
}

// validateUniformKeySets checks that every rung's flattened key sequence
// (both the numeric Fields and the NonNumeric leaves, independently) is
// byte-identical to rung 0's -- same length, same keys, same order (order
// is already guaranteed identical by flattenRung's own sort, so this is
// effectively a key-SET equality check expressed as a sequence compare).
// A missing/extra/renamed leaf on any rung after rung 0 is rejected here,
// closing BUG-829 (index-out-of-range panic / silent mismatched merge).
func validateUniformKeySets(rungs []ladderRung, correlationID string) error {
	if len(rungs) == 0 {
		return nil
	}
	wantNumeric := keysOf(rungs[0].numeric)
	wantNonNumeric := nonNumericKeysOf(rungs[0].nonNumeric)
	for i := 1; i < len(rungs); i++ {
		gotNumeric := keysOf(rungs[i].numeric)
		if !equalStrings(wantNumeric, gotNumeric) {
			return errs.New(ErrScaleLadderKeySetMismatch, correlationID, map[string]any{
				"index":         i,
				"population":    rungs[i].population,
				"leafKind":      "numeric",
				"rung0Count":    len(wantNumeric),
				"thisRungCount": len(gotNumeric),
			})
		}
		gotNonNumeric := nonNumericKeysOf(rungs[i].nonNumeric)
		if !equalStrings(wantNonNumeric, gotNonNumeric) {
			return errs.New(ErrScaleLadderKeySetMismatch, correlationID, map[string]any{
				"index":         i,
				"population":    rungs[i].population,
				"leafKind":      "nonNumeric",
				"rung0Count":    len(wantNonNumeric),
				"thisRungCount": len(gotNonNumeric),
			})
		}
	}
	return nil
}

func keysOf(fields []LadderField) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Key
	}
	return out
}

func nonNumericKeysOf(fields []NonNumericField) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Key
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// flattenRung walks body's leaves and produces two SORTED (by dotted-path
// Key, byte-wise) slices: numeric leaves and non-numeric leaves. Sorting
// happens once here, at load time — the interpolator (below) then merges
// two already-sorted slices without ever ranging a map on an output path
// (GR#21).
func flattenRung(body map[string]json.RawMessage, correlationID string, population int64) ([]LadderField, []NonNumericField, error) {
	var numeric []LadderField
	var nonNumeric []NonNumericField

	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		var generic any
		if err := json.Unmarshal(body[k], &generic); err != nil {
			return nil, nil, errs.New(ErrScaleLadderUnparsable, correlationID, map[string]any{
				"path": k, "reason": err.Error(),
			})
		}
		n, nn, err := flattenLeaf(k, generic, correlationID, population)
		if err != nil {
			return nil, nil, err
		}
		numeric = append(numeric, n...)
		nonNumeric = append(nonNumeric, nn...)
	}

	sort.Slice(numeric, func(i, j int) bool { return numeric[i].Key < numeric[j].Key })
	sort.Slice(nonNumeric, func(i, j int) bool { return nonNumeric[i].Key < nonNumeric[j].Key })
	return numeric, nonNumeric, nil
}

// flattenLeaf recursively flattens one decoded JSON value at dotted path
// key. Objects recurse element-wise (nested objects interpolate
// element-wise per the amendment); arrays of ALL-numeric elements flatten
// as "key.0", "key.1", ...; every other shape (string/bool/null, or a
// mixed/non-numeric array) is a single non-numeric leaf carried verbatim.
func flattenLeaf(key string, v any, correlationID string, population int64) ([]LadderField, []NonNumericField, error) {
	switch val := v.(type) {
	case float64:
		if !num.IsFinite(val) || val < 0 {
			return nil, nil, errs.New(ErrScaleLadderNonFiniteLeaf, correlationID, map[string]any{
				"population": population, "key": key, "value": val,
			})
		}
		return []LadderField{{Key: key, Value: val}}, nil, nil
	case map[string]any:
		subKeys := make([]string, 0, len(val))
		for k := range val {
			subKeys = append(subKeys, k)
		}
		sort.Strings(subKeys)
		var numeric []LadderField
		var nonNumeric []NonNumericField
		for _, k := range subKeys {
			n, nn, err := flattenLeaf(key+"."+k, val[k], correlationID, population)
			if err != nil {
				return nil, nil, err
			}
			numeric = append(numeric, n...)
			nonNumeric = append(nonNumeric, nn...)
		}
		return numeric, nonNumeric, nil
	case []any:
		allNumeric := len(val) > 0
		for _, e := range val {
			if _, ok := e.(float64); !ok {
				allNumeric = false
				break
			}
		}
		if allNumeric {
			var numeric []LadderField
			for i, e := range val {
				f := e.(float64)
				if !num.IsFinite(f) || f < 0 {
					return nil, nil, errs.New(ErrScaleLadderNonFiniteLeaf, correlationID, map[string]any{
						"population": population, "key": fmt.Sprintf("%s.%d", key, i), "value": f,
					})
				}
				numeric = append(numeric, LadderField{Key: fmt.Sprintf("%s.%d", key, i), Value: f})
			}
			return numeric, nil, nil
		}
		raw, err := json.Marshal(val)
		if err != nil {
			return nil, nil, errs.New(ErrScaleLadderUnparsable, correlationID, map[string]any{"path": key, "reason": err.Error()})
		}
		return nil, []NonNumericField{{Key: key, RawValue: raw}}, nil
	default:
		raw, err := json.Marshal(val)
		if err != nil {
			return nil, nil, errs.New(ErrScaleLadderUnparsable, correlationID, map[string]any{"path": key, "reason": err.Error()})
		}
		return nil, []NonNumericField{{Key: key, RawValue: raw}}, nil
	}
}

// Rungs reports the loaded ladder's rung populations in ascending order
// (AC-2's data-derivation hook: tests read the rung count/populations from
// here, never from a literal). Returns nil if no ladder is loaded.
func (t *TrafficAPI) Rungs() []int64 {
	if err := t.checkNotCopied("Rungs"); err != nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.ladder == nil {
		return nil
	}
	out := make([]int64, len(t.ladder.rungs))
	for i, r := range t.ladder.rungs {
		out[i] = r.population
	}
	return out
}

// ScaleLadderAt interpolates the loaded ladder at population (AC-5/AC-11).
// It takes ONLY the population — no citizen slice, no world state — so it
// cannot scale with citizen count by construction (AC-9). AC-10: nothing in
// this package calls it yet; inc2 is its first consumer.
func (t *TrafficAPI) ScaleLadderAt(population int64) (LadderPoint, error) {
	if err := t.checkNotCopied("ScaleLadderAt"); err != nil {
		return LadderPoint{}, err
	}
	t.mu.RLock()
	ladder := t.ladder
	correlationID := t.correlationID
	t.mu.RUnlock()
	if ladder == nil {
		return LadderPoint{}, errs.New(ErrScaleLadderTooFewRungs, correlationID, map[string]any{"count": 0})
	}
	return interpolateLadder(ladder, population, correlationID)
}

// interpolateLadder is the pure interpolation core (AC-8/AC-9): given an
// already-loaded ladder and a population, it returns the log-linear
// interpolation (or exact match) with no clock, randomness, or citizen
// state involved. Rounding of integer-valued fields is deliberately NOT
// done here — see RoundCount's doc comment — this function always returns
// float64 values; callers round exactly once, at the point of use.
//
// Interpolation rule (AC-5): for population p strictly between rung i and
// i+1, w = (ln p - ln p_i) / (ln p_{i+1} - ln p_i); each numeric field
// f = f_i + w*(f_{i+1} - f_i) (linear in the field, logarithmic in
// population — math.Log, matching the TypeScript Math.log side exactly per
// AC-6/AC-7). An exact rung population returns that rung's values verbatim,
// no arithmetic. p below the first or above the last rung is a registry
// error (ErrScaleLadderPopulationOutOfRange) — never clamped, never
// extrapolated (Aaron's rule, AC-5).
func interpolateLadder(ladder *ScaleLadder, population int64, correlationID string) (LadderPoint, error) {
	rungs := ladder.rungs
	first, last := rungs[0].population, rungs[len(rungs)-1].population
	if population < first || population > last {
		return LadderPoint{}, errs.New(ErrScaleLadderPopulationOutOfRange, correlationID, map[string]any{
			"population": population, "min": first, "max": last,
		})
	}

	// Exact match (including the endpoints) returns the rung verbatim --
	// COPIED, not aliased (BUG-828): ScaleLadder is documented "immutable",
	// so a caller mutating the returned point must never poison the loaded
	// ladder for every later call.
	for _, r := range rungs {
		if r.population == population {
			return LadderPoint{Population: population, Fields: copyFields(r.numeric), NonNumeric: copyNonNumeric(r.nonNumeric)}, nil
		}
	}

	// Locate the bracketing pair via a linear scan over the (small, <=18)
	// rung list -- no map, no early-break-over-a-map (GR#21 §18).
	lo := 0
	for i := 0; i < len(rungs)-1; i++ {
		if rungs[i].population < population && population < rungs[i+1].population {
			lo = i
			break
		}
	}
	rLo, rHi := rungs[lo], rungs[lo+1]

	w := (math.Log(float64(population)) - math.Log(float64(rLo.population))) /
		(math.Log(float64(rHi.population)) - math.Log(float64(rLo.population)))

	// rLo.numeric and rHi.numeric are both sorted by Key (flattenRung), and
	// every rung in one ladder shares the same key set (schema-agnostic but
	// internally consistent, per inc0's contract) -- merge them pairwise by
	// index rather than a map lookup.
	fields := make([]LadderField, len(rLo.numeric))
	for i := range rLo.numeric {
		lo, hi := rLo.numeric[i].Value, rHi.numeric[i].Value
		fields[i] = LadderField{Key: rLo.numeric[i].Key, Value: lo + w*(hi-lo)}
	}

	// Non-numeric leaves are carried from the LOWER rung verbatim (amended
	// AC-11) -- never interpolated. COPIED (BUG-828), same reasoning as the
	// exact-match path above: NonNumeric here is otherwise rLo's own slice.
	return LadderPoint{Population: population, Fields: fields, NonNumeric: copyNonNumeric(rLo.nonNumeric)}, nil
}

// copyFields returns a fresh slice with the same LadderField values --
// LadderField has no pointer/slice fields, so a shallow copy is a full
// copy. Used on every ScaleLadderAt return path (BUG-828) so a caller
// mutating the result can never reach the loaded ladder's own slices.
func copyFields(src []LadderField) []LadderField {
	out := make([]LadderField, len(src))
	copy(out, src)
	return out
}

// copyNonNumeric returns a fresh slice AND a fresh copy of each leaf's
// RawValue ([]byte) -- a shallow slice copy alone would still alias the
// underlying json.RawMessage bytes, which is exactly what the attack's
// TestRoundAliasingExactRung mutates (BUG-828).
func copyNonNumeric(src []NonNumericField) []NonNumericField {
	out := make([]NonNumericField, len(src))
	for i, f := range src {
		raw := make(json.RawMessage, len(f.RawValue))
		copy(raw, f.RawValue)
		out[i] = NonNumericField{Key: f.Key, RawValue: raw}
	}
	return out
}

// RoundCount is the ONE shared integer-rounding rule (AC-7): floor(x + 0.5)
// for a non-negative x. Both the Go and TypeScript implementations must
// agree byte-for-byte -- the TypeScript sibling is
// webconsole/src/sim/scaleLadder.ts's roundCount, documented identically.
// RoundCount is applied exactly once, at the point of use, to any
// interpolated field the caller treats as an integer count (people,
// vehicles, spaces) -- interpolateLadder itself never rounds.
func RoundCount(x float64) int64 {
	return int64(math.Floor(x + 0.5))
}
