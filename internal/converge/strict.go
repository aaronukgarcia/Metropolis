package converge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-1140 (Run side): shared fail-closed checks every Domain adapter applies
// before and while it runs a Journal, so a trajectory that carries no
// information (empty journal, advance or sample before any setup, a typo'd
// argument key silently ignored, a required argument silently defaulted to
// zero, sample ticks out of order) is a registry error and never a quietly
// meaningless series that Compare could then pass.

// checkJournalShape rejects, before any op runs, a journal with no entries
// (MET-H513) and sample ticks that
// are negative or not strictly increasing (MET-H518). Sample ticks are the
// only ticks Compare aligns on; other entries' ticks are not constrained.
// The no-sample check (MET-H514) is requireSamples, run after the ops so an
// op's own error is never masked by it.
func checkJournalShape(domain string, j Journal) error {
	if len(j.Entries) == 0 {
		return errs.New(codeEmptyJournal, errs.NewCorrelationID(), map[string]any{"domain": domain})
	}
	var prev int64 = -1
	for _, e := range j.Entries {
		if e.Op != "sample" {
			continue
		}
		if e.Tick < 0 || e.Tick <= prev {
			return errs.New(codeSampleTickOrder, errs.NewCorrelationID(), map[string]any{
				"domain": domain, "tick": e.Tick, "previous": prev,
			})
		}
		prev = e.Tick
	}
	return nil
}

// requireSamples is the MET-H514 check: a Run that produced no sample (the
// journal carried no sample op) has an empty trajectory, which is an error.
func requireSamples(domain string, traj Trajectory) error {
	if len(traj) == 0 {
		return errs.New(codeNoSample, errs.NewCorrelationID(), map[string]any{"domain": domain})
	}
	return nil
}

// setupMissing is the MET-H515 error: op ran before the setup it needs.
func setupMissing(domain string, entry JournalEntry, reason string) error {
	return errs.New(codeSetupMissing, errs.NewCorrelationID(), map[string]any{
		"domain": domain, "op": entry.Op, "tick": entry.Tick, "reason": reason,
	})
}

// decodeArgs strictly decodes entry.Args into dst: every key must be in
// allowed (MET-H516 names the first unknown key in sorted order, for
// determinism), every key in required must be present (MET-H517, never
// defaulted to zero), and a malformed object is codeJournalOpFailed
// (MET-H503). Absent, null or empty Args are an empty object. dst may be nil
// when the op takes no arguments.
func decodeArgs(domain string, entry JournalEntry, spec argKeys, dst any) error {
	allowed, required := spec.allowed, spec.required
	raw := bytes.TrimSpace(entry.Args)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		raw = []byte("{}")
	}
	malformed := func(err error) error {
		return errs.New(codeJournalOpFailed, errs.NewCorrelationID(), map[string]any{
			"tick": entry.Tick, "op": entry.Op, "domain": domain,
			"reason": fmt.Sprintf("malformed args: %v", err),
		})
	}
	keys, err := scanObject(raw)
	if err != nil {
		return malformed(err)
	}
	ok := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		ok[k] = true
	}
	var unknown []string
	for k := range keys {
		if !ok[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return errs.New(codeUnknownArg, errs.NewCorrelationID(), map[string]any{
			"domain": domain, "op": entry.Op, "tick": entry.Tick, "key": unknown[0],
		})
	}
	for _, k := range required {
		// A JSON null for a required key is as good as absent: it would
		// decode to the zero value and silently default the argument.
		if v, present := keys[k]; !present || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return errs.New(codeMissingArg, errs.NewCorrelationID(), map[string]any{
				"domain": domain, "op": entry.Op, "tick": entry.Tick, "key": k,
			})
		}
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return malformed(err)
	}
	return nil
}

// maxAdvanceDays bounds one advance op's days argument. Reason: every real
// journal advances at most 30 days in a single op (the population action
// file), and the engine steps one day at a time, so an unbounded value
// (1e11 was probed) ran for minutes with no result. 3650 (ten years of
// days) is over 100 times the largest real value and still finishes in
// seconds. The other numeric arguments (finance month, amounts; services
// n) feed O(1) arithmetic, not a loop, so they need no bound here; seed
// count and ageYears already carry their own bounds.
const maxAdvanceDays = 3650

// checkAdvanceDays is the MET-H519 check, applied after the days >= 1 check.
func checkAdvanceDays(domain string, entry JournalEntry, days int64) error {
	if days > maxAdvanceDays {
		return errs.New(codeAdvanceTooLarge, errs.NewCorrelationID(), map[string]any{
			"domain": domain, "op": entry.Op, "tick": entry.Tick, "days": days, "bound": maxAdvanceDays,
		})
	}
	return nil
}

// scanObject parses raw as one JSON object into its raw values by walking
// tokens, so a duplicate key (which a plain map decode silently collapses,
// last wins) is an error naming the key. Anything but a single object, or
// trailing data after it, is an error.
func scanObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("args must be a JSON object")
	}
	keys := map[string]json.RawMessage{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("non-string object key")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if _, dup := keys[key]; dup {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		keys[key] = v
	}
	if _, err := dec.Token(); err != nil { // closing brace
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after the args object")
	}
	return keys, nil
}

// journalEntryFields are the top-level fields a journal entry may carry:
// the three the harness reads plus "note", which the services action file
// uses on its sample rows as authoring documentation (ignored).
var journalEntryFields = []string{"tick", "op", "args", "note"}

// UnmarshalJSON decodes a journal entry strictly: a top-level field outside
// journalEntryFields (a misspelled "tick" would otherwise silently become 0)
// is refused (MET-H516 naming the field).
func (e *JournalEntry) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	var unknown []string
	for k := range m {
		known := false
		for _, f := range journalEntryFields {
			if k == f {
				known = true
			}
		}
		if !known {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return errs.New(codeUnknownArg, errs.NewCorrelationID(), map[string]any{
			"domain": "journal", "op": "entry", "tick": int64(0), "key": unknown[0],
		})
	}
	var plain struct {
		Tick int64           `json:"tick"`
		Op   string          `json:"op"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(b, &plain); err != nil {
		return err
	}
	*e = JournalEntry{Tick: plain.Tick, Op: plain.Op, Args: plain.Args}
	return nil
}
