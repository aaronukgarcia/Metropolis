package balance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strings"
)

// CellStatus is the closed terminal classification of one sweep cell's
// outcome (AC-2). StatusNotYetRun is never written by a completed sweep — it
// is synthesized by readResults for a cell absent from an interrupted sweep's
// partial output (AC-9), so "not attempted" is always distinguishable from
// "attempted and failed" and from "completed".
type CellStatus string

const (
	StatusCompleted CellStatus = "completed"
	StatusTimedOut  CellStatus = "timed-out"
	StatusCrashed   CellStatus = "crashed"
	StatusRejected  CellStatus = "rejected"
	StatusNotYetRun CellStatus = "not-yet-run"
)

// CauseCategory is the closed, documented set of non-completed failure causes
// (AC-3). A registry error's code travels alongside in CellResult.ErrorCode.
type CauseCategory string

const (
	CauseHeadlessExitNonzero CauseCategory = "headless-exit-nonzero"
	CauseTimeout             CauseCategory = "timeout"
	CauseInvalidParameter    CauseCategory = "invalid-parameter"
	CauseSynthGeneration     CauseCategory = "synth-generation-failed"
)

// Cell identifies one (config, seed) cell by its raw parameter values and
// seed — every record carries its own identity, never just a count (AC-2's
// false-pass risk).
type Cell struct {
	Config map[string]string `json:"config"`
	Seed   uint64            `json:"seed"`
}

// CellResult is one terminal record for one attempt of one cell. A retried
// cell has one record per attempt (AC-4: additive, never substitutive).
type CellResult struct {
	Cell      Cell          `json:"cell"`
	Status    CellStatus    `json:"status"`
	Attempt   int           `json:"attempt"`
	Cause     CauseCategory `json:"cause,omitempty"`
	ErrorCode string        `json:"errorCode,omitempty"`

	// Completed-cell metrics (AC-5):
	SimulatedMonths     int64    `json:"simulatedMonths,omitempty"`
	SecondsPerMonthAt1x float64  `json:"secondsPerMonthAt1x,omitempty"`
	RealHours           *float64 `json:"realHours,omitempty"`
}

// sweepMeta is the first NDJSON line of a results stream: provenance + shape
// (AC-12). Records follow, one per line, in ascending (sweep-point, seed,
// attempt) order — never completion order (ICD §7).
type sweepMeta struct {
	Kind         string `json:"kind"`
	ScenarioHash string `json:"scenarioHash"`
	CommitHash   string `json:"commitHash"`
	Version      string `json:"version"`
	WorkerCount  int    `json:"workerCount"`
	TotalCells   int    `json:"totalCells"`
}

// SweepResult is the in-memory sweep outcome (the meta line + its records).
type SweepResult struct {
	ScenarioHash string
	CommitHash   string
	Version      string
	WorkerCount  int
	TotalCells   int
	Records      []CellResult
}

// cellKey returns the attempt-independent identity of a record: its canonical
// config rendering plus its seed. Used to reconcile a partial results file
// against the full expected cell set (AC-9).
func (r CellResult) cellKey() string {
	return canonicalConfig(r.Cell.Config) + "|seed=" + uint64String(r.Cell.Seed)
}

// sortKey extends cellKey with the attempt index, so retries of the same cell
// order deterministically after the original attempt.
func (r CellResult) sortKey() string {
	return r.cellKey() + "|attempt=" + intString(r.Attempt)
}

func canonicalConfig(config map[string]string) string {
	names := make([]string, 0, len(config))
	for n := range config {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte('=')
		b.WriteString(config[n])
		b.WriteByte(';')
	}
	return b.String()
}

func uint64String(v uint64) string {
	return strings.TrimSpace(jsonNumber(v))
}

func intString(v int) string {
	return strings.TrimSpace(jsonNumber(int64(v)))
}

func jsonNumber(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// sortRecords orders records in ascending (sweep-point, seed, attempt) order,
// in place. This is the ICD §7 determinism guarantee: results merge in this
// fixed order regardless of worker count or fan-out completion order.
func sortRecords(records []CellResult) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].sortKey() < records[j].sortKey()
	})
}

// writeResults streams the sweep as NDJSON to w: the sweepMeta line first,
// then every record. Deterministic: the records slice is already sorted, and
// no wall-clock or map-iteration-order value reaches the output.
func writeResults(w io.Writer, res SweepResult) error {
	enc := json.NewEncoder(w)
	if err := enc.Encode(sweepMeta{
		Kind:         "sweep-meta",
		ScenarioHash: res.ScenarioHash,
		CommitHash:   res.CommitHash,
		Version:      res.Version,
		WorkerCount:  res.WorkerCount,
		TotalCells:   res.TotalCells,
	}); err != nil {
		return err
	}
	for _, r := range res.Records {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

// readResults reads a (possibly truncated) results stream and reconciles it
// against the scenario's full expected cell set (AC-9): every expected
// (config, seed) cell that has no record in the partial file is reported as
// StatusNotYetRun, distinct from completed/crashed/timed-out/rejected. A
// truncated stream (io.ErrUnexpectedEOF at the last partial line) is treated
// as an interrupted sweep, not a fatal parse error.
func readResults(r io.Reader, scn *Scenario) (SweepResult, error) {
	var meta *sweepMeta
	byCell := map[string][]CellResult{}

	dec := json.NewDecoder(r)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break // clean end, or a torn final line — both are "partial"
			}
			return SweepResult{}, wrapErr(codeResultsWriteFailed, err, nil)
		}

		var m sweepMeta
		if json.Unmarshal(raw, &m) == nil && m.Kind == "sweep-meta" {
			meta = &m
			continue
		}
		var cr CellResult
		if json.Unmarshal(raw, &cr) == nil && cr.Cell.Config != nil {
			byCell[cr.cellKey()] = append(byCell[cr.cellKey()], cr)
		}
	}

	// Reconcile against the expected cell set in deterministic order.
	expectedCells, err := scn.cells()
	if err != nil {
		return SweepResult{}, err
	}
	records := make([]CellResult, 0, len(expectedCells))
	for _, c := range expectedCells {
		key := CellResult{Cell: Cell{Config: c.Config.Config, Seed: c.Seed}}.cellKey()
		if got, ok := byCell[key]; ok {
			records = append(records, got...)
		} else {
			records = append(records, CellResult{
				Cell:   Cell{Config: c.Config.Config, Seed: c.Seed},
				Status: StatusNotYetRun,
			})
		}
	}
	sortRecords(records)

	res := SweepResult{Records: records}
	if meta != nil {
		res.ScenarioHash = meta.ScenarioHash
		res.CommitHash = meta.CommitHash
		res.Version = meta.Version
		res.WorkerCount = meta.WorkerCount
		res.TotalCells = meta.TotalCells
	}
	return res, nil
}

// sha256Sum is the SHA-256 hex digest used for scenario provenance (AC-12).
func sha256Sum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
