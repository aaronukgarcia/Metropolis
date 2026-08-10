package synth

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// PerfRecord is one PerfResult persisted to disk, keyed by commit hash
// and scale preset (AC-5) — the schema a CI graphing step consumes. The
// on-disk form is one JSON object per line (NDJSON — the same encoding
// this codebase already uses for save/fixture shards, foundation/
// serialize/ndjson.go, chosen here again for the same "append cheaply,
// read line by line" reason, GR#3) so a long-lived results file can be
// appended to, one line per CI run, without reading/rewriting the whole
// file.
type PerfRecord struct {
	CommitHash string     `json:"commitHash"`
	Preset     string     `json:"preset"`
	Result     PerfResult `json:"result"`
}

// AppendResult appends rec as one JSON line to path, creating the file
// (and any missing parent directory) if it does not already exist
// (AC-5). Never truncates or rewrites existing lines — this is the only
// way this package's results file is ever mutated.
func AppendResult(path string, rec PerfRecord) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("synth: opening results file %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("synth: encoding perf record: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("synth: writing results file %q: %w", path, err)
	}
	return nil
}

// LoadLatestBaseline reads path (an AppendResult-written NDJSON file)
// and returns the most recently appended PerfRecord's Result for the
// given preset — the "stored baseline for the current branch's parent
// commit" AC-6 asks for, under the simplifying assumption that the last
// line written for a preset IS that preset's most recent measurement
// (true as long as every CI run appends via AppendResult in commit
// order, which is this package's only writer).
//
// A missing file is NOT an error (AC-8: "a missing perf baseline ...
// does not fail the build") — it returns (nil, nil), the caller's signal
// to record a new baseline rather than compare against one. A file that
// exists but contains no record for preset returns the same (nil, nil)
// for the identical reason: a fresh scale preset has no prior baseline
// either. Only a file that exists AND fails to parse as the expected
// schema is an error (codeBaselineCorrupt) — distinct from "no baseline
// yet".
func LoadLatestBaseline(path, preset string) (*PerfResult, error) {
	correlationID := errs.NewCorrelationID()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("synth: opening results file %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var latest *PerfResult
	scanner := bufio.NewScanner(f)
	// A results file grows one line per CI run over a project's
	// lifetime, never one enormous unbounded record — the stdlib
	// default token-size cap is appropriate here (unlike
	// foundation/serialize's shard reader, which handles hostile/
	// arbitrarily large payloads and deliberately does NOT use
	// bufio.Scanner for that reason).
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		var rec PerfRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			return nil, errs.Wrap(codeBaselineCorrupt, correlationID, err, map[string]any{
				"path": path, "line": lineNo,
			})
		}
		if rec.Preset == preset {
			result := rec.Result
			latest = &result
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errs.Wrap(codeBaselineCorrupt, correlationID, err, map[string]any{"path": path})
	}
	return latest, nil
}
