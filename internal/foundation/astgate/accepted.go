package astgate

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// AcceptedFinding is one human-reviewed, GIT-COMMITTED ratchet-allowlist
// entry (BUG-024's ratchet requirement, ASM-204's lead ruling) -- the
// evidence that a real human looked at a specific astgate finding and
// decided it is a pre-existing, accepted condition rather than something
// that should block the build.
//
// This mirrors internal/harness/synth/accepted.go's AcceptedRegressionEntry
// / AcceptedRegistry pattern (BUG-095), which is this project's established
// baseline-ratchet convention (GR#3 -- one way to do this, not two):
//
//   - the fact "finding F is accepted, for reason R" is asserted by a file
//     that lives in the git tree, not by anything the gate itself measures
//     at run time -- a self-declared "this is fine" inside the scanned code
//     has no route to silence the gate;
//   - adding or removing an entry requires a real commit (and, on this
//     repo's branch-protected main, a reviewed PR) -- git history and code
//     review ARE the evidence trail;
//   - it is read fresh from the checked-out working tree on every test run
//     (LoadAcceptedFindings), so TestRun_LiveTree_ReportsFindings can check
//     every live violation against it before deciding pass/fail.
//
// Unlike synth's AcceptedRegressionEntry (keyed on preset+commitHash,
// because a perf regression is a property of a specific commit), an
// astgate finding is a property of a specific source location and shape.
//
// BUG-119 (fixed here): the key was originally the finding's exact
// violationMessage TEXT, which embeds "file:line" -- a cosmetic edit
// ANYWHERE above the flagged line (a comment, a blank line, a reordered
// import) shifts every subsequent line number in that file with no
// change to the finding's actual shape, which flipped every already-
// accepted finding downstream in the file into a false "NEW violation"
// build failure. Demonstrated live: one comment line added above an
// import block flipped 12 accepted commands.go findings to failing.
//
// The Finding field now holds violationKey's output (gate.go): the same
// identity tuple violationMessage's text encodes -- file, candidate type,
// function name, which enumeration path (receiver vs. parameter) caught
// it, and the matched value's identifier name -- with the line number
// deliberately excluded. This is still a stable, deterministic identifier
// astgate derives for free from the AST (GR#15); it is simply no longer
// sensitive to WHERE ELSE in the file an edit landed.
type AcceptedFinding struct {
	Finding string `json:"finding"` // violationKey(rf) -- see BUG-119 above, NOT the human-readable message text
	Reason  string `json:"reason"`
}

// AcceptedFindings is the loaded, indexed form of a checked-in
// accepted-findings.json file -- see LoadAcceptedFindings. The zero value
// (including a nil AcceptedFindings) behaves as an empty registry: Reason
// on a nil map is a safe, defined Go operation that simply never matches,
// which is the correct "nothing has ever been accepted" starting state.
type AcceptedFindings map[string]string

// Reason reports whether key (violationKey's output -- BUG-119, NOT the
// human-readable violationMessage text) is recorded in this registry, and
// if so, the reason a human gave for accepting it.
func (a AcceptedFindings) Reason(key string) (string, bool) {
	reason, ok := a[key]
	return reason, ok
}

// LoadAcceptedFindings reads path (a JSON array of AcceptedFinding,
// intended to be checked into git alongside this package -- see
// AcceptedFindings' doc comment for why that location matters) and returns
// it indexed by finding text.
//
// A MISSING file is not an error and returns an empty registry -- the same
// "absence means nothing has been accepted yet" posture
// synth.LoadAcceptedRegistry takes, so a repo state that has never needed
// to accept a finding is not required to have created this file first.
//
// A file that EXISTS but is malformed, or contains an entry missing either
// field, or contains a duplicate finding, is a hard error -- fail closed,
// not open: this file is the sole evidence the ratchet trusts, so a
// corrupt or ambiguous copy of it must never be silently treated as
// "nothing accepted" (which would just quietly turn every pre-existing
// finding into a fresh, unreviewed failure) nor as "whichever entry was
// read last wins" (which would silently drop a recorded reason).
func LoadAcceptedFindings(path string) (AcceptedFindings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AcceptedFindings{}, nil
		}
		return nil, errs.Wrap("MET-F704", errs.NewCorrelationID(), err, map[string]any{"path": path, "cause": err.Error()})
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		// An explicitly-created-but-empty file is treated the same as a
		// missing one, mirroring synth.LoadAcceptedRegistry's precedent.
		return AcceptedFindings{}, nil
	}

	var entries []AcceptedFinding
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		return nil, errs.Wrap("MET-F705", errs.NewCorrelationID(), err, map[string]any{"path": path, "cause": err.Error()})
	}

	out := make(AcceptedFindings, len(entries))
	for i, e := range entries {
		finding := strings.TrimSpace(e.Finding)
		reason := strings.TrimSpace(e.Reason)
		if finding == "" || reason == "" {
			return nil, errs.New("MET-F706", errs.NewCorrelationID(), map[string]any{
				"path": path, "index": i, "finding": e.Finding, "reason": e.Reason,
			})
		}
		if _, dup := out[finding]; dup {
			return nil, errs.New("MET-F707", errs.NewCorrelationID(), map[string]any{
				"path": path, "finding": finding,
			})
		}
		out[finding] = reason
	}
	return out, nil
}
