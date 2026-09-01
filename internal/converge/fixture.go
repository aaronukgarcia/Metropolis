package converge

import (
	"encoding/json"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fixtureFile is the on-disk shape a captured TS-side trajectory
// fixture is expected to have: a domain tag (must match the Domain
// this fixture is being compared against — a mismatch is the caller's
// responsibility to check, since only the caller knows which Domain it
// loaded the fixture for) and the trajectory's samples, encoded with
// exactly Sample's own json tags — no second, parallel encoding.
//
//	{
//	  "domain": "finance",
//	  "samples": [
//	    {"tick": 1, "values": {"treasury": 1000000, "netWorth": 950000}},
//	    {"tick": 2, "values": {"treasury": 1050000, "netWorth": 990000}}
//	  ]
//	}
type fixtureFile struct {
	Domain  string     `json:"domain"`
	Samples Trajectory `json:"samples"`
}

// LoadFixture reads a captured trajectory fixture from path and returns
// its domain tag and Trajectory. A missing/unreadable file returns
// codeFixtureLoadFailed (MET-H500); malformed JSON or a fixture missing
// its "samples" array returns codeFixtureDecodeFailed (MET-H501) — never
// a panic, never a silently-empty Trajectory standing in for either
// failure.
func LoadFixture(path string) (domain string, traj Trajectory, err error) {
	b, readErr := os.ReadFile(path)
	if readErr != nil {
		return "", nil, errs.New(codeFixtureLoadFailed, errs.NewCorrelationID(), map[string]any{
			"path": path, "reason": readErr.Error(),
		})
	}
	return decodeFixture(path, b)
}

func decodeFixture(path string, b []byte) (string, Trajectory, error) {
	var f fixtureFile
	if err := json.Unmarshal(b, &f); err != nil {
		return "", nil, errs.New(codeFixtureDecodeFailed, errs.NewCorrelationID(), map[string]any{
			"path": path, "reason": err.Error(),
		})
	}
	if f.Samples == nil {
		return "", nil, errs.New(codeFixtureDecodeFailed, errs.NewCorrelationID(), map[string]any{
			"path": path, "reason": "fixture has no \"samples\" array",
		})
	}
	return f.Domain, f.Samples, nil
}

// SaveFixture writes traj to path in the same shape LoadFixture reads,
// tagged with domain. Used by the webconsole-side capture tooling (and
// by this package's own tests) to produce a fixture from a live run —
// never hand-authored, so a fixture always round-trips through the same
// encoding LoadFixture decodes.
func SaveFixture(path, domain string, traj Trajectory) error {
	f := fixtureFile{Domain: domain, Samples: traj}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return errs.New(codeFixtureDecodeFailed, errs.NewCorrelationID(), map[string]any{
			"path": path, "reason": err.Error(),
		})
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return errs.New(codeFixtureLoadFailed, errs.NewCorrelationID(), map[string]any{
			"path": path, "reason": err.Error(),
		})
	}
	return nil
}
