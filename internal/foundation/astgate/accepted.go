package astgate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// findingKeyFieldCount is the number of pipe-delimited components
// violationKey (gate.go) always assembles a key from: File,
// ReceiverExprPrinted, Kind.String(), FuncName, ValueName,
// MatchedExprPrinted -- see violationKey's own fmt.Sprintf. SEC-051's
// shape check below is built directly off that tuple's actual shape, not
// a hand-rolled guess at what a "plausible-looking" key might contain.
const findingKeyFieldCount = 6

// findingKeyValidKinds is the exhaustive set of strings FuncKind.String()
// (gate.go) can ever produce -- the 3rd pipe-delimited field of a
// well-formed key must be exactly one of these. Kept in sync with
// FuncKind.String() by hand (there are only three cases and they have
// not changed since BUG-024's original design); a fourth kind added to
// FuncKind without a matching entry here would make every NEW finding of
// that kind fail validateFindingKeyShape at accept-time, which is a loud,
// immediate build failure -- not a silent gap -- so drift is caught fast.
var findingKeyValidKinds = map[string]bool{
	"receiver method":      true, // KindReceiverMethod
	"field access chain":   true, // KindFieldAccess
	"function (parameter)": true, // KindParameterFunc
}

// validateFindingKeyShape implements SEC-051's fix: a STRUCTURAL check
// that key is SHAPED the way violationKey (gate.go) could plausibly have
// produced it, run at LoadAcceptedFindings time -- before any live scan
// exists to compare against. This is deliberately a different, EARLIER
// check than enforceNoOrphanedEntries (gate_test.go): that one asks "does
// a live finding currently match this key" (which a fabricated-but-
// currently-true-by-coincidence entry, or a speculative entry added right
// before the matching code lands, can slip past); this one asks "could
// the scanner's own key-generation logic have EVER produced a string
// shaped like this, regardless of whether anything live matches it right
// now" -- catching a hand-typed/fabricated entry that merely LOOKS
// plausible on sight.
//
// It intentionally does NOT attempt to validate the free-text fields
// (ReceiverExprPrinted/MatchedExprPrinted can legitimately be almost any
// syntactically-valid Go type expression printExpr can emit, and FuncName
// can be a synthetic "<func literal at line N>" label for an anonymous
// closure -- see funcLitName) -- validating those against the full Go
// grammar would just reimplement go/parser for no real gain. What IS
// checked is the part of the shape that is fixed regardless of source
// content: the field COUNT, the file path's form, and the kind field's
// closed enumeration -- exactly the properties a hand-typed fabrication
// is most likely to get wrong (SEC-051's own reproduction, "internal/
// orphanfabfix|SpeculativeType|function (parameter)|FutureFunc|x", has
// only 5 fields where a real key always has 6 -- it is missing the
// MatchedExprPrinted component entirely).
func validateFindingKeyShape(key string) error {
	fields := strings.Split(key, "|")
	if len(fields) != findingKeyFieldCount {
		return fmt.Errorf("expected %d pipe-delimited fields (file|receiverExpr|kind|funcName|valueName|matchedExpr) as violationKey produces, got %d",
			findingKeyFieldCount, len(fields))
	}
	file, kind, funcName, valueName, matchedExpr := fields[0], fields[2], fields[3], fields[4], fields[5]

	if file == "" {
		return fmt.Errorf("file component is empty")
	}
	if strings.Contains(file, "\\") {
		return fmt.Errorf("file component %q uses backslashes -- violationKey's File is always a forward-slash, repo-root-relative path (pf.Rel)", file)
	}
	if filepath.IsAbs(file) || strings.HasPrefix(file, "/") {
		return fmt.Errorf("file component %q is an absolute path -- violationKey's File is always repo-root-relative", file)
	}
	if strings.Contains(file, "..") {
		return fmt.Errorf("file component %q contains a path-traversal segment -- not a shape astgate's own repo-relative walk can produce", file)
	}
	if !strings.HasSuffix(file, ".go") {
		return fmt.Errorf("file component %q does not end in .go", file)
	}

	if !findingKeyValidKinds[kind] {
		return fmt.Errorf("kind component %q is not one of FuncKind.String()'s known values (%q, %q, %q)",
			kind, "receiver method", "field access chain", "function (parameter)")
	}

	if funcName == "" {
		return fmt.Errorf("funcName component is empty -- violationKey's FuncName is always a real declaration or closure name")
	}
	if valueName == "" {
		return fmt.Errorf("valueName component is empty -- violationKey's ValueName always falls back to \"_\" rather than going empty")
	}
	if matchedExpr == "" {
		return fmt.Errorf("matchedExpr component is empty -- violationKey's MatchedExprPrinted is always the matched type expression's printed text")
	}
	return nil
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
//
// SEC-051 (fabrication resistance): every entry's Finding key is also run
// through validateFindingKeyShape, which fails the load with MET-F708 if
// the key is not SHAPED the way violationKey (gate.go) could ever have
// produced it -- wrong field count, a non-.go/absolute/backslashed file
// component, or a kind component outside FuncKind.String()'s three known
// values. This runs at LOAD time, independently of whatever the current
// live scan does or does not find, which is what makes it catch a
// fabricated entry EARLIER than enforceNoOrphanedEntries' cross-reference
// (gate_test.go) does -- that check only fires once a live scan result
// exists to compare against; this one fires the moment the file is read.
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
		if shapeErr := validateFindingKeyShape(finding); shapeErr != nil {
			return nil, errs.New("MET-F708", errs.NewCorrelationID(), map[string]any{
				"path": path, "index": i, "finding": finding, "reason": shapeErr.Error(),
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
