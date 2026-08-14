package synth

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

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

	// AcceptedRegression and AcceptedReason are BUG-083's deliberate,
	// visible way for a legitimate slowdown to become the new baseline.
	//
	// # The problem this solves
	//
	// results.go's LoadLatestBaseline (see its doc comment) now freezes
	// the reconstructed baseline at the last record that did NOT
	// regress — replayed from history, not trusted as a stored flag —
	// so an ordinary regressed commit can no longer silently ratchet
	// the comparison point forward. But a gate that can only ever
	// advance on a pass will block FOREVER the moment a change makes
	// the simulation genuinely, intentionally slower (a new phase hook
	// doing real work, a deliberate correctness-over-speed trade-off,
	// …) — there must be a way out, and it must not be a silent env
	// var or a quiet code change (that would just relocate the ratchet
	// risk into "whoever remembers to flip it back").
	//
	// # The mechanism
	//
	// cmd/perfci sets these fields ONLY when the current commit is
	// listed in accepted.go's git-committed AcceptedRegistry — see
	// LoadAcceptedRegistry and cmd/perfci's package doc comment for the
	// full BUG-095 mechanism (this package USED TO set them from a bare
	// -accept-regression/-accept-reason CLI flag pair; that was live-
	// verified as a full bypass of BUG-083's fix and has been removed).
	// Never automatically, never by an ordinary push/PR run. When set
	// AND corroborated by that registry (LoadLatestBaseline re-checks
	// this at the read boundary — see below), AppendResult and
	// LoadLatestBaseline treat this record as
	// deliberately overriding both of BUG-083's reference points at
	// once: the step-to-step "last known good" baseline AND the
	// cumulative-drift anchor (baseline.go's CompareToBaseline) both
	// reset to this record, resetting the drift clock along with the
	// step gate — a human already looked at the regression this run
	// reported and chose to accept it as the new normal.
	//
	// AppendResult rejects AcceptedRegression=true with an empty
	// AcceptedReason (BUG-085's provenance-not-just-a-flag principle
	// applied here too, MET-H311) — an unjustified override is exactly
	// as untrustworthy as an unmeasured record with no provenance.
	//
	// # BUG-095: these two fields are a DECLARATION, not the evidence
	//
	// Live-verified: a hand-crafted PerfRecord with these two fields set
	// to any chosen value, written directly via AppendResult (bypassing
	// cmd/perfci entirely), was accepted verbatim and reset BOTH the
	// baseline and the cumulative-drift anchor to an attacker-chosen
	// figure — a real, unregressed 19ms run was then reported as a 216%
	// regression against the forged anchor. The record's own non-empty-
	// reason check (AppendResult, and previously LoadLatestBaseline too)
	// cannot fix this, because it only asks the record to vouch for
	// itself, and whoever can write the record can write the vouching.
	//
	// LoadLatestBaseline therefore no longer honours AcceptedRegression on
	// its own: it only resets baseline/anchor for a record whose
	// (Preset, CommitHash) is ALSO present in the git-committed
	// AcceptedRegistry (accepted.go) — a file that lives outside the
	// results file entirely (never cache-persisted, never writable by the
	// same second-writer routes BUG-073/085/094/095 all name) and can
	// only gain an entry via a real, reviewed commit. AcceptedReason
	// remains persisted here purely as an informational echo of the
	// registry's reason at the time cmd/perfci wrote this record — it is
	// no longer, on its own, load-bearing evidence of anything. See
	// accepted.go's AcceptedRegistry doc comment for the full mechanism
	// and accepted.AcceptedRegistry.Reason for the corroboration check.
	AcceptedRegression bool   `json:"acceptedRegression,omitempty"`
	AcceptedReason     string `json:"acceptedReason,omitempty"`
}

// AppendResult appends rec as one JSON line to path, creating the file
// (and any missing parent directory) if it does not already exist
// (AC-5). Never truncates or rewrites existing lines — this is the only
// way this package's results file is ever mutated.
//
// BUG-055: rejects rec if rec.Result.Measured is false, BEFORE any
// write happens. See PerfResult.Measured's doc comment (perf.go) for
// the full provenance rationale — this is the enforcement half of that
// flag; without it, a hand-built PerfResult{} literal was persisted
// exactly as if RunPerf had produced it, with nothing distinguishing a
// real measurement from a fabricated one once it reached the results
// file a CI gate trusts.
func AppendResult(path string, rec PerfRecord) error {
	if !rec.Result.Measured {
		return errs.New(codeUnmeasuredResult, errs.NewCorrelationID(), map[string]any{
			"path": path, "preset": rec.Preset, "commitHash": rec.CommitHash,
		})
	}
	// BUG-085: Measured alone is a self-reported bool with no
	// structural backing — also reject values a genuine RunPerf
	// measurement can never structurally produce (see
	// PerfResult.ImplausibleReason's doc comment, perf.go).
	if reason := rec.Result.ImplausibleReason(); reason != "" {
		return errs.New(codeImplausibleResult, errs.NewCorrelationID(), map[string]any{
			"path": path, "preset": rec.Preset, "commitHash": rec.CommitHash, "reason": reason,
		})
	}
	// BUG-083: an accepted-regression override with no recorded
	// justification is exactly as untrustworthy as an unmeasured
	// record — see PerfRecord.AcceptedRegression's doc comment.
	if rec.AcceptedRegression && strings.TrimSpace(rec.AcceptedReason) == "" {
		return errs.New(codeUnjustifiedAcceptedRegression, errs.NewCorrelationID(), map[string]any{
			"path": path, "preset": rec.Preset, "commitHash": rec.CommitHash,
		})
	}

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

// CorruptLine reports one NDJSON line LoadLatestBaseline could not parse
// as a PerfRecord (BUG-054). A torn/partial line is an EXPECTED failure
// mode of an append-only results file — CI jobs get cancelled mid-write
// — so LoadLatestBaseline's recovery contract is: skip the bad line and
// keep looking for a good, later record, rather than aborting the whole
// read (that old behaviour permanently hid every good baseline sitting
// after the first bad line, the exact BUG-031 "infra fragility breaks a
// good build" shape relocated into this file format). A skipped line is
// still REPORTED here, never silently dropped (Golden Rule #1/#17) — the
// caller decides how loudly to surface it (cmd/perfci logs a non-fatal
// warning naming every skipped line when at least one good record was
// still recovered; see LoadLatestBaseline's doc comment for when it
// instead returns a hard codeBaselineCorrupt error).
type CorruptLine struct {
	LineNo int
	Err    error
}

// maxResultsLineBytes is ASM-355's finite ceiling on a single NDJSON
// results line (measured on the raw line, INCLUDING its trailing '\n').
// The BUG-074 fix removed bufio.Scanner's 64KiB token cap entirely by
// switching to bufio.Reader.ReadString('\n'), which reads a line of ANY
// length into memory — trading one wrong-shaped failure (the scan aborts
// permanently with ErrTooLong) for another (a single multi-GB line OOMs
// the reader outright). A real PerfRecord line is a few hundred bytes (a
// handful of phases, counters, and a commit hash), so 1 MiB is
// deliberately generous headroom no genuine record can approach, while
// still bounding per-line memory far below the failure ASM-355 names. A
// line longer than this is recorded as a CorruptLine and skipped — the
// same recovery contract as a torn line — rather than read unbounded or
// allowed to abort the whole scan.
const maxResultsLineBytes = 1 << 20 // 1 MiB

// readResultsLine reads one '\n'-terminated line from r without trusting
// the file's line lengths: it accumulates the line incrementally and, the
// moment it exceeds maxResultsLineBytes, stops retaining any further
// bytes and switches to draining (discarding) the remainder of the line,
// so memory stays bounded no matter how large a single line is (ASM-355).
// The returned line is the raw content INCLUDING any trailing '\n'
// (matching bufio.Reader.ReadString's shape so the caller's empty-line
// semantics are preserved); oversized is true when the line exceeded the
// ceiling, in which case line holds only a truncated prefix. err is
// io.EOF at a clean end of stream, or the underlying read error.
func readResultsLine(r *bufio.Reader) (line []byte, oversized bool, err error) {
	for {
		frag, rerr := r.ReadSlice('\n')
		if len(frag) > 0 && !oversized {
			line = append(line, frag...)
			if len(line) > maxResultsLineBytes {
				oversized = true
			}
		}
		// once oversized, further fragments are discarded, not retained.
		if rerr != bufio.ErrBufferFull {
			if rerr == io.EOF {
				err = io.EOF
			} else if rerr != nil {
				err = rerr
			}
			return line, oversized, err
		}
		// rerr == bufio.ErrBufferFull: the internal buffer filled without
		// finding '\n', so this line continues — loop to read (and, once
		// oversized, discard) the next fragment.
	}
}

// LoadLatestBaseline reads path (an AppendResult-written NDJSON file)
// and reconstructs TWO reference points for preset — baseline (the
// step-to-step "last known good") and anchor (BUG-083's fixed
// cumulative-drift reference) — that together are the "stored baseline
// for the current branch's parent commit" AC-6 asks for, hardened
// against the exact ratchet Destructive-7 live-verified: 30 successive
// commits, each individually under RegressionThreshold against the
// immediately prior stored figure, compounding 100ms to 1.327s
// (13.27x) with zero CI signal, because the pre-fix version of this
// function simply returned whatever was LAST APPENDED — which, since
// AppendResult ran unconditionally on every evaluated run, was
// identical to "last measured" regardless of whether that measurement
// passed.
//
// # BUG-083: reconstructed by REPLAY, not trusted as a stored flag
//
// baseline is not read off a stored "this record passed" bit (which
// would itself be exactly as spoofable as the Measured flag was before
// BUG-055/BUG-073 — see PerfRecord.AcceptedRegression's doc comment for
// the ONE flag this function does trust, and why). Instead, every
// usable record for preset is replayed forward in commit order through
// baseline.go's own CompareToBaseline: baseline only advances to a
// record when that record, compared against the CURRENT reconstructed
// baseline and anchor, is NOT regressed by either check. (ASM-353:
// cmd/perfci no longer appends a regressed run at all — see
// cmd/perfci's finishGate — so a record that WOULD have regressed should
// not even appear in a normally-written file; this replay-freeze remains
// as the read-boundary defence for the second-writer routes
// BUG-073/085/095 name and for legacy files written before ASM-353. Any
// such record found here is simply skipped when reconstructing what the
// next run should compare against, so baseline freezes at the last
// record that actually passed rather than sliding forward on a
// regression that slipped through.)
//
// anchor is seeded from the FIRST usable record found for preset, and
// otherwise never moves except when a record explicitly sets
// AcceptedRegression=true AND the record's (preset, CommitHash) is
// corroborated by accepted — the git-committed AcceptedRegistry loaded
// via LoadAcceptedRegistry (accepted.go) — the one deliberate, visible
// way a legitimate, intended slowdown becomes the new baseline; see
// PerfRecord.AcceptedRegression's doc comment and accepted.go's BUG-095
// rationale for why the record's own fields are no longer sufficient on
// their own. Both baseline and anchor reset together on such a record,
// so accepting a regression also resets the cumulative-drift clock, not
// just the step-to-step one. A record whose AcceptedRegression is true
// but is NOT corroborated by accepted is not trusted as an override —
// it is reported (CorruptLine) and then replayed exactly as an ordinary,
// non-accepted measurement instead (see the loop body below), which is
// what stops a forged "accepted" record from moving either reference
// point at all.
//
// A missing file is NOT an error (AC-8: "a missing perf baseline ...
// does not fail the build") — it returns (nil, nil, nil, nil), the
// caller's signal to record a new baseline rather than compare against
// one. A file that exists but contains no record for preset returns the
// same (nil, nil, nil, nil) for the identical reason: a fresh scale
// preset has no prior baseline either.
//
// Malformed, oversized (ASM-355), unmeasured (BUG-073), or implausible
// (BUG-085) lines are collected as CorruptLine entries and skipped, NOT
// treated as fatal,
// as long as a good record for preset is still found somewhere in the
// file (BUG-054's recovery contract) — the returned error is nil in
// that case, but the corrupt-line list is never empty, so a caller that
// cares can still log/report it (GR#17: recoverable does not mean
// silent). Only when NO usable record for preset can be recovered AT
// ALL does this function return a hard error (codeBaselineCorrupt),
// because at that point "corrupt" and "no baseline yet" are genuinely
// indistinguishable from the caller's perspective otherwise.
//
// # BUG-086: the hard-error path is per-preset, not whole-file
//
// A syntactically malformed line can't be attributed to any preset —
// json.Unmarshal fails before rec.Preset is even readable — but the
// hard-error decision above is scoped to preset. Escalating to
// codeBaselineCorrupt purely because SOME corrupt line exists ANYWHERE
// in the file, even when every other line proves it belongs to a
// different preset entirely, would turn one preset's torn write into a
// false-positive failure for an unrelated preset's legitimate first run
// (AC-8). See otherPresetRecordSeen in the implementation below for the
// more precise, per-preset-aware signal this function now uses instead.
func LoadLatestBaseline(path, preset string, accepted AcceptedRegistry) (baseline, anchor *PerfResult, corrupt []CorruptLine, err error) {
	correlationID := errs.NewCorrelationID()

	f, openErr := os.Open(path)
	if openErr != nil {
		if os.IsNotExist(openErr) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("synth: opening results file %q: %w", path, openErr)
	}
	defer func() { _ = f.Close() }()

	// BUG-074: this used to be bufio.Scanner, on the stated assumption
	// that "a results file grows one line per CI run, never one
	// enormous unbounded record" made the stdlib default 64KiB
	// per-token cap safe. That assumption was never enforced anywhere —
	// PerfResult.PhaseTimings is an unbounded slice — and Destructive-5
	// live-verified the consequence: bufio.Scanner's token-size cap
	// sits UNDERNEATH the per-line recovery loop below, so one
	// oversized line makes Scan() return false PERMANENTLY with
	// bufio.ErrTooLong, never becomes a CorruptLine entry, and silently
	// terminates the scan before it ever reaches a good, later record —
	// reproducing BUG-054's exact "hide a later good baseline" failure
	// for a cause BUG-054's own fix comment dismissed rather than
	// enforced. bufio.Reader.ReadString('\n') has no fixed token cap —
	// any single line, however large, is read in full — which removes
	// the hidden ceiling entirely rather than merely raising it to a
	// still-crossable number. ASM-355 then closed the downside of that
	// removal: an unbounded per-line read trades one failure mode for
	// another (a single multi-GB line OOMs the reader outright).
	// readResultsLine below re-adds a GENEROUS finite ceiling
	// (maxResultsLineBytes) with its own CorruptLine path, so an
	// oversized line is skipped like a torn one instead of either
	// aborting the scan or exhausting memory.
	reader := bufio.NewReader(f)
	lineNo := 0
	// BUG-086: the corrupt-line list above is whole-FILE-grained (a
	// syntactically malformed line can't even be attributed to a preset —
	// json.Unmarshal fails before rec.Preset is readable), but the hard-
	// error decision below is per-PRESET. Without this signal, one
	// corrupt line anywhere in a shared results file would escalate a
	// totally unrelated preset's legitimate "fresh preset, no prior
	// baseline" case (AC-8) into a hard codeBaselineCorrupt failure.
	// otherPresetRecordSeen tracks whether at least one line elsewhere in
	// the file parsed as a well-formed PerfRecord for a DIFFERENT preset
	// — proof the file's NDJSON format is generally intact, so an
	// unparseable line is much more plausibly that OTHER preset's own
	// torn/corrupt write than this preset's. It deliberately does NOT
	// fire on a well-formed-but-rejected record (Measured=false,
	// implausible, uncorroborated accepted-regression) for the REQUESTED
	// preset — those are still-attributable, known-bad evidence about
	// THIS preset specifically, not an unrelated one, so they must not
	// soften the hard-error path below.
	otherPresetRecordSeen := false
	// BUG-187: otherPresetRecordSeen (above) proves the file format is
	// generally sound, but it must ONLY soften the hard-error decision
	// below for corrupt entries that are genuinely UNATTRIBUTABLE — a
	// json.Unmarshal failure before rec.Preset was even readable, or a
	// well-formed record for a DIFFERENT preset. It must NEVER soften the
	// decision for a corrupt entry that WAS successfully attributed to
	// the REQUESTED preset itself and then rejected by the provenance
	// checks below (Measured=false / implausible / unjustified or
	// uncorroborated AcceptedRegression — BUG-073/085/095). Live-verified
	// (Destructive, BUG-187): a file with a valid record for an unrelated
	// preset plus a tampered {"preset":requested,"measured":false} line
	// for the REQUESTED preset was wrongly treated as "fresh preset, no
	// baseline yet" (nil, nil, nil) purely because the unrelated preset's
	// valid record set otherPresetRecordSeen — laundering a known-bad,
	// attributable rejection of THIS preset's own history into a silent
	// pass, the exact BUG-071-family direction and a real regression of
	// BUG-073/085/095's provenance guarantees. requestedPresetCorruptSeen
	// tracks that distinct, stronger signal: at least one corrupt entry
	// is KNOWN — not merely presumed by elimination — to belong to the
	// requested preset, which must always still hard-error regardless of
	// what other presets' records prove about the file's general format.
	requestedPresetCorruptSeen := false
	for {
		line, oversized, readErr := readResultsLine(reader)
		if readErr != nil && readErr != io.EOF {
			return nil, nil, corrupt, fmt.Errorf("synth: reading results file %q: %w", path, readErr)
		}
		// len(line) == 0 only when the read hit EOF without reading any
		// bytes (i.e. the file ended cleanly on the previous line's
		// newline) — that phantom final read is not a line and must not
		// be counted or parsed. Anything else read, even a final line
		// with no trailing newline, is real content.
		if oversized {
			lineNo++
			// ASM-355: a line over maxResultsLineBytes is recorded as a
			// CorruptLine (unattributable — its preset was never read) and
			// skipped, exactly like a torn line, so a good later record is
			// still recovered rather than the whole scan aborting or the
			// reader exhausting memory on an unbounded line.
			corrupt = append(corrupt, CorruptLine{LineNo: lineNo, Err: fmt.Errorf("record at line %d exceeds maxResultsLineBytes (%d bytes) -- refusing to read an unbounded line (ASM-355)", lineNo, maxResultsLineBytes)})
			if readErr == io.EOF {
				break
			}
			continue
		}
		if len(line) != 0 {
			lineNo++
			trimmed := strings.TrimRight(string(line), "\r\n")
			var rec PerfRecord
			if unmarshalErr := json.Unmarshal([]byte(trimmed), &rec); unmarshalErr != nil {
				// BUG-054: do NOT abort the whole read on the first
				// bad line — record it and keep scanning, so a good,
				// later record (the common "torn final line from a
				// cancelled job, followed by nothing" case, or the
				// rarer "one bad line buried in history" case) is
				// still found.
				corrupt = append(corrupt, CorruptLine{LineNo: lineNo, Err: unmarshalErr})
			} else if rec.Preset != preset {
				// BUG-086: well-formed JSON for a preset OTHER than the
				// one requested — proof this file's format is generally
				// intact, which is the signal the hard-error check below
				// uses to avoid escalating an unrelated preset's corrupt
				// line into this preset's failure. Deliberately not
				// scrutinised any further (Measured/plausibility/
				// accepted-registry checks only apply to the requested
				// preset's own records) — those checks decide what THIS
				// function trusts as a baseline candidate, not whether
				// the file format itself is sound.
				otherPresetRecordSeen = true
			} else {
				corruptBefore := len(corrupt)
				switch {
				// BUG-073: AppendResult enforces Measured==true BEFORE
				// a record is ever written (MET-H308) — but that
				// enforcement lived only at the write boundary. Any
				// syntactically-valid PerfRecord JSON line reaching
				// this file by any OTHER route (a hand edit, a
				// corrupted-cache restore resurrecting a foreign/old
				// file, a manual merge-conflict resolution, a
				// re-uploaded and edited artifact) was accepted
				// verbatim as the latest measurement with zero error
				// and zero CorruptLine flag. Re-check provenance HERE,
				// at the boundary that actually matters — the moment
				// this data becomes a decision input — not only at
				// the write boundary that a second, non-AppendResult
				// writer can simply bypass.
				case !rec.Result.Measured:
					corrupt = append(corrupt, CorruptLine{
						LineNo: lineNo,
						Err:    fmt.Errorf("record at line %d for preset %q has Measured=false — refusing to trust an unmeasured/hand-injected record as a baseline (BUG-073)", lineNo, preset),
					})
				// BUG-085: Measured is a self-reported bool with no
				// structural backing — also re-check plausibility HERE,
				// at the read boundary, for the identical "a second
				// writer can bypass AppendResult" reason BUG-073's fix
				// re-checks Measured here rather than trusting the
				// write-boundary check alone.
				case rec.Result.ImplausibleReason() != "":
					corrupt = append(corrupt, CorruptLine{
						LineNo: lineNo,
						Err:    fmt.Errorf("record at line %d for preset %q is not a plausible genuine measurement: %s (BUG-085)", lineNo, preset, rec.Result.ImplausibleReason()),
					})
				// BUG-083: an override with no recorded justification
				// is exactly as untrustworthy as an unmeasured record —
				// see PerfRecord.AcceptedRegression's doc comment.
				case rec.AcceptedRegression && strings.TrimSpace(rec.AcceptedReason) == "":
					corrupt = append(corrupt, CorruptLine{
						LineNo: lineNo,
						Err:    fmt.Errorf("record at line %d for preset %q sets AcceptedRegression=true with no AcceptedReason — refusing to trust an unjustified baseline override (BUG-083)", lineNo, preset),
					})
				// BUG-095: AcceptedRegression=true with a non-empty reason
				// (the only two checks that existed before this fix) is
				// still not sufficient on its own — that is exactly the
				// self-vouching flag a second, non-cmd/perfci writer can
				// forge with zero friction (live-verified: a hand-injected
				// accepted record with a plausible-sounding reason reset
				// both reference points and turned a genuine unregressed
				// 19ms run into a reported 216% regression). It is
				// honoured only when this record's (preset, CommitHash) is
				// ALSO present in accepted, the git-committed registry
				// loaded from OUTSIDE this results file (see accepted.go
				// for why a second writer of THIS file has no route to
				// that one).
				//
				// A record failing this check is skipped ENTIRELY, the
				// same as every other case in this switch — it must NOT
				// fall through to the ordinary replay logic below, because
				// a forged record whose value happens to look like an
				// "improvement" (a smaller PerMonthTick, exactly the shape
				// Destructive-9's own reproduction used) would otherwise
				// sail through the ordinary non-regressed path and become
				// the ordinary baseline anyway — a forged record must not
				// gain influence through EITHER door.
				case rec.AcceptedRegression && !acceptedIsCorroborated(accepted, preset, rec.CommitHash):
					corrupt = append(corrupt, CorruptLine{
						LineNo: lineNo,
						Err: fmt.Errorf(
							"record at line %d for preset %q sets AcceptedRegression=true (reason %q) but commit %q has no matching entry in the accepted-regressions registry — refusing to trust a self-declared acceptance with no corroborating evidence outside the results file, and refusing to treat it as an ordinary measurement either (BUG-095)",
							lineNo, preset, rec.AcceptedReason, rec.CommitHash,
						),
					})
				default:
					result := rec.Result
					switch {
					case rec.AcceptedRegression:
						// Reached only when acceptedIsCorroborated returned
						// true above (the outer switch's cases are
						// evaluated in order, and any AcceptedRegression
						// record that fails corroboration was already
						// claimed by the case above and skipped entirely)
						// — a deliberate, visible, GIT-CORROBORATED human
						// override: reset BOTH reference points to this
						// record.
						baseline = &result
						anchor = &result
					case anchor == nil:
						// First usable record ever seen for preset:
						// establishes both reference points (AC-8's
						// "no prior baseline" case, extended to also
						// seed the cumulative anchor).
						baseline = &result
						anchor = &result
					default:
						cmp := CompareToBaseline(baseline, anchor, result)
						if !cmp.CouldNotEvaluate() && !cmp.Regressed {
							baseline = &result
						}
						// else: BUG-083's freeze. This record stays in
						// the file (AC-5 history/graphing) but does not
						// become the reconstructed baseline — anchor is
						// untouched either way, it only ever moves on a
						// registry-corroborated AcceptedRegression record
						// above.
					}
				}
				if len(corrupt) > corruptBefore {
					// BUG-187: the switch above added at least one
					// CorruptLine, and this branch is only reached for a
					// record whose rec.Preset == preset — so this corrupt
					// entry is a KNOWN, attributable rejection of the
					// REQUESTED preset's own history (Measured=false /
					// implausible / unjustified or uncorroborated
					// AcceptedRegression), never a line that could plausibly
					// belong to some other preset instead. It must always
					// still hard-error below, regardless of what
					// otherPresetRecordSeen proves about the rest of the
					// file's format.
					requestedPresetCorruptSeen = true
				}
			}
		}
		if readErr == io.EOF {
			break
		}
	}

	if baseline == nil && len(corrupt) > 0 && (requestedPresetCorruptSeen || !otherPresetRecordSeen) {
		// Nothing usable recovered for preset, and the file is not
		// merely absent/unwritten — this is genuine corruption, and
		// GR#17 forbids treating it as indistinguishable from "no
		// baseline yet". Report it as a hard failure rather than
		// silently returning (nil, nil, nil, nil), which a caller would
		// read as "first run, record a new baseline" and thereby mask a
		// corrupted history under a fresh one.
		//
		// BUG-086: this escalation is skipped when otherPresetRecordSeen
		// is true AND requestedPresetCorruptSeen is false — a well-formed
		// record for a DIFFERENT preset proves the file's NDJSON format
		// is generally sound, so a corrupt line that cannot be
		// attributed to any preset is far more plausibly that OTHER
		// preset's own torn write than genuine corruption affecting the
		// requested (fresh, never-yet-recorded) preset. Falls through to
		// the ordinary AC-8 "no prior baseline" return below in that
		// case. When NO other preset's record is present either, the
		// corrupt line's preset can't be ruled out either way, and the
		// original hard-error behaviour is preserved — exactly the
		// ambiguous case this function cannot safely treat as benign.
		//
		// BUG-187: otherPresetRecordSeen must NEVER excuse a corrupt
		// entry that requestedPresetCorruptSeen proves was successfully
		// attributed to the REQUESTED preset itself and rejected by the
		// provenance checks above (Measured=false / implausible /
		// unjustified or uncorroborated AcceptedRegression). That is
		// known-bad evidence about THIS preset's own history, not an
		// unrelated preset's torn write — a tampered or rejected record
		// for the requested preset must always still hard-error here,
		// regardless of what other presets elsewhere in the file prove
		// about the format. requestedPresetCorruptSeen therefore takes
		// priority over otherPresetRecordSeen in the condition above.
		// The registry template for codeBaselineCorrupt (MET-H306,
		// data/errors.json) renders "{path} ... {line}: {cause}" — the
		// ctx keys MUST match those placeholders or the one-line GR#1
		// display prints the literal braces instead of the facts
		// (live-verified by this chain's Destructive). The first corrupt
		// line is the one named: with zero usable records recovered, it
		// is where any human investigation starts; corruptLines carries
		// the full count alongside it.
		return nil, nil, corrupt, errs.Wrap(codeBaselineCorrupt, correlationID, corrupt[0].Err, map[string]any{
			"path": path, "line": corrupt[0].LineNo, "cause": corrupt[0].Err.Error(), "corruptLines": len(corrupt),
		})
	}

	return baseline, anchor, corrupt, nil
}
