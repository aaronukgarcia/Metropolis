package headless

import (
	"encoding/json"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// LoadScenario reads path as a JSON array of wire-format protocol.Command
// documents (AC-4, harness.headless.md) and decodes each via
// protocol.DecodeCommand — the SAME decoder the real transport wire
// format uses (internal/protocol/codec.go), so a scenario script is
// never a bespoke, second command vocabulary; anything a live UI could
// send, a scenario script can too, and vice versa.
//
// A decoded command whose CorrelationID is empty is assigned one via
// protocol.NewCorrelationID() before Command.Validate runs — scenario
// authors are not expected to hand-generate correlation IDs for every
// line of a hand-written fixture (ASM, logged against this file: ties
// this convenience to LoadScenario specifically, not to Command.Validate
// itself, which still requires a non-empty CorrelationID everywhere
// else in this codebase).
//
// Any read or parse failure — a missing/unreadable file, malformed JSON,
// an unknown command Kind, or a failed envelope Validate — is reported
// as a single registry-sourced ErrScenarioReadFailed (MET-H200, AC-8),
// never a panic. LoadScenario returns either every command in the
// script or none — a scenario that fails partway through never produces
// a partial command list for a caller to accidentally run.
func LoadScenario(correlationID, path string) ([]protocol.Command, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.Wrap(ErrScenarioReadFailed, correlationID, err, map[string]any{"path": path})
	}

	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return nil, errs.Wrap(ErrScenarioReadFailed, correlationID, err, map[string]any{
			"path": path, "cause": "scenario file must be a JSON array of command envelopes",
		})
	}

	cmds := make([]protocol.Command, 0, len(elements))
	for i, el := range elements {
		cmd, err := protocol.DecodeCommand(el)
		if err != nil {
			return nil, errs.Wrap(ErrScenarioReadFailed, correlationID, err, map[string]any{"path": path, "index": i})
		}
		if cmd.CorrelationID == "" {
			cmd.CorrelationID = protocol.NewCorrelationID()
		}
		if err := cmd.Validate(); err != nil {
			return nil, errs.Wrap(ErrScenarioReadFailed, correlationID, err, map[string]any{"path": path, "index": i})
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}
