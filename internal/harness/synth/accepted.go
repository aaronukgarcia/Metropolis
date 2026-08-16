package synth

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// AcceptedRegressionEntry is one human-reviewed, GIT-COMMITTED acceptance
// record (BUG-095) — the evidence that a real human looked at a specific
// commit's regression and decided it is intentional. Unlike a PerfRecord's
// AcceptedRegression/AcceptedReason fields (results.go), which live in
// perf-results*.ndjson — a file that is never committed to git, only
// cache-persisted across CI runs (see .github/workflows/ci.yml's
// actions/cache steps) — an AcceptedRegressionEntry lives in a file that
// IS part of this repository's source tree, so adding one requires the
// same thing any other code change requires: a commit, a PR, code review,
// and a permanent line in `git log` naming who added it and why.
type AcceptedRegressionEntry struct {
	Preset     string `json:"preset"`
	CommitHash string `json:"commitHash"`
	Reason     string `json:"reason"`
}

// acceptedKey identifies one (preset, commit) pair — the same pair a
// PerfRecord's Preset/CommitHash fields carry, so a registry entry can be
// matched against a specific persisted record unambiguously.
type acceptedKey struct {
	Preset     string
	CommitHash string
}

// AcceptedRegistry is the loaded, indexed form of a checked-in
// accepted-regressions file (BUG-095) — see LoadAcceptedRegistry. The zero
// value (including a nil AcceptedRegistry) behaves as an empty registry:
// Reason on a nil map is a safe, defined Go operation that simply never
// matches, which is the correct "nothing has ever been accepted" starting
// state and lets callers that never touch acceptance (most existing tests)
// pass a bare nil without special-casing it.
//
// # Why this is the control, not one more validation on the record
//
// BUG-095's finding was that AcceptedRegression/AcceptedReason are just
// two struct fields on a PerfRecord — checked only for non-empty-reason
// and non-negative values — with nothing binding them to cmd/perfci's real
// -accept-regression flow, so ANY writer that can put a line into the
// results file (a hand edit, a corrupted-cache restore, a re-uploaded
// artifact — the exact second-writer threat model BUG-073/085/094 already
// name) can forge "a human accepted this" and move both the baseline and
// the drift anchor to any value. Adding a third field, or checking the
// existing two more strictly, does not fix that: it is the same mistake
// one layer deeper, because the record can still fully vouch for itself.
//
// This registry is the alternative: the fact "a human accepted commit C's
// regression for preset P, for reason R" is no longer asserted BY the
// results file at all. It is asserted by this file, which:
//
//   - is part of the git tree, not the actions/cache-persisted results
//     file — a second writer who can corrupt/restore/hand-edit
//     perf-results.ndjson has no route to this file at all, because it
//     never travels through that channel;
//   - requires a real commit (and, on this repo's branch-protected main,
//     a reviewed PR) to add or change an entry — git history and code
//     review ARE the evidence trail, the same way GR#23's Destructive
//     verdict or a signed commit are evidence that live outside the
//     artifact they vouch for;
//   - is read fresh from the checked-out working tree by cmd/perfci on
//     every run (LoadAcceptedRegistry), so results.go's LoadLatestBaseline
//     can cross-check a record's SELF-DECLARED AcceptedRegression=true
//     against this INDEPENDENT source before ever honouring it — see
//     LoadLatestBaseline's doc comment for exactly how that
//     cross-check works.
//
// # BUG-245: the ledger's provenance is the COMMIT, not the workstation
//
// The gate itself (cmd/perfci) does NOT call this working-tree reader for
// its acceptance decision any more. It calls provenance.go's
// LoadAcceptedRegistryFromWorkingDir, which reads the ledger's COMMITTED
// content at HEAD via git (git show HEAD:<relPath>) and, for every entry,
// verifies the named commitHash is a real commit object in the repository —
// so a local, uncommitted edit to the working-tree file cannot self-vouch a
// regression, and a committed entry cannot name a fabricated hash. This
// function remains the pure parser (also used by metricsdash, which is a
// read-only dashboard, not a gate) and the base primitive the provenance
// loader reuses via parseAcceptedRegistry.
type AcceptedRegistry map[acceptedKey]string

// Reason reports whether commitHash's regression for preset was accepted
// via a git-committed entry in this registry, and if so, the reason a
// human recorded for it. A nil or empty AcceptedRegistry always returns
// ("", false) — the safe default when no registry file was ever loaded.
func (r AcceptedRegistry) Reason(preset, commitHash string) (string, bool) {
	reason, ok := r[acceptedKey{Preset: preset, CommitHash: commitHash}]
	return reason, ok
}

// acceptedIsCorroborated is a tiny, named boolean wrapper around
// AcceptedRegistry.Reason for results.go's LoadLatestBaseline — used only
// to keep that function's replay switch reading as a plain boolean guard
// rather than a two-value assignment inside a case expression.
func acceptedIsCorroborated(accepted AcceptedRegistry, preset, commitHash string) bool {
	_, ok := accepted.Reason(preset, commitHash)
	return ok
}

// LoadAcceptedRegistry reads path (a JSON array of AcceptedRegressionEntry,
// intended to be checked into git alongside this package — see
// AcceptedRegistry's doc comment for why that location matters) and
// returns it indexed by (preset, commitHash).
//
// A MISSING file is not an error and returns an empty registry: most
// repository states have never had a regression accepted, and requiring
// the file to exist before it is ever needed would make ordinary,
// unregressed CI runs depend on a file nobody had a reason to create yet
// (the same "absence is not failure" posture AC-8 already takes for the
// results file itself, results.go's LoadLatestBaseline — but for a
// different reason: THAT absence means "no history yet"; THIS absence
// means "nobody has ever needed to accept a regression yet", which is
// equally unremarkable).
//
// A file that EXISTS but is malformed, or contains an entry missing any
// of preset/commitHash/reason, or contains two entries for the same
// (preset, commitHash) pair, is a hard error — fail closed, not open: this
// file is the sole source of truth BUG-095's acceptance evidence now
// relies on, so a corrupt or ambiguous copy of it must never be silently
// treated as "nothing accepted" (that would just relocate BUG-097's
// "missing looks like lost" ambiguity into a security-relevant file) nor
// silently treated as "whichever entry we happened to read last wins".
func LoadAcceptedRegistry(path string) (AcceptedRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AcceptedRegistry{}, nil
		}
		return nil, fmt.Errorf("synth: opening accepted-regressions registry %q: %w", path, err)
	}
	return parseAcceptedRegistry(data, path)
}

// parseAcceptedRegistry is the shared parser for both LoadAcceptedRegistry
// (the pure working-tree reader, above) and LoadAcceptedRegistryFromGit
// (provenance.go — the BUG-245 committed-at-HEAD reader). source is the
// human-readable origin used in error messages (a file path for the former,
// "HEAD:<relPath>" for the latter), so a corrupt registry always reports
// WHERE the bad content came from rather than only that it is bad.
func parseAcceptedRegistry(data []byte, source string) (AcceptedRegistry, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		// An explicitly-created-but-empty file is treated the same as a
		// missing one (a human may `git init` this file with `[]` before
		// ever needing an entry) rather than as a JSON parse error.
		return AcceptedRegistry{}, nil
	}

	var entries []AcceptedRegressionEntry
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		return nil, fmt.Errorf("synth: parsing accepted-regressions registry %q: %w", source, err)
	}

	registry := make(AcceptedRegistry, len(entries))
	for i, e := range entries {
		preset := strings.TrimSpace(e.Preset)
		commit := strings.TrimSpace(e.CommitHash)
		reason := strings.TrimSpace(e.Reason)
		if preset == "" || commit == "" || reason == "" {
			return nil, fmt.Errorf(
				"synth: accepted-regressions registry %q entry %d is incomplete (preset=%q commitHash=%q reason=%q) -- every field is required, this file is the sole evidence BUG-095's acceptance check trusts",
				source, i, e.Preset, e.CommitHash, e.Reason,
			)
		}
		key := acceptedKey{Preset: preset, CommitHash: commit}
		if _, dup := registry[key]; dup {
			return nil, fmt.Errorf(
				"synth: accepted-regressions registry %q has more than one entry for preset %q commit %q -- ambiguous, refusing to guess which reason is authoritative",
				source, preset, commit,
			)
		}
		registry[key] = reason
	}
	return registry, nil
}
