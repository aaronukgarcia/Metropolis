package headless

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// maxScenarioFileBytes bounds -scenario's os.ReadFile (BUG-305): reading
// an entire file into memory with no size check first means an
// oversized/hostile/accidental -scenario path (a wrong path pointed at a
// multi-GB file, a corrupted symlink loop's target, …) drives an
// unbounded transient allocation before json.Unmarshal ever gets a
// chance to reject the content as malformed. Mirrors the "bound the
// caller-influenced value that sizes memory, before any allocation"
// idiom foundation.solver's MaxRequestPayloadBytes (contract.go) applies
// to a wire-supplied payload, applied here to a disk-supplied one. A
// real scenario script is a short, hand-authored or generated JSON
// command list — a handful of KB even at pathological command counts —
// so 16 MiB is generous headroom no legitimate script can approach while
// still bounding the read far below an operator-error or hostile
// multi-GB file.
const maxScenarioFileBytes = 16 << 20 // 16 MiB

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
	info, statErr := os.Stat(path)
	if statErr != nil {
		return nil, errs.Wrap(ErrScenarioReadFailed, correlationID, statErr, map[string]any{"path": path})
	}
	if info.Size() > maxScenarioFileBytes {
		return nil, errs.New(ErrScenarioReadFailed, correlationID, map[string]any{
			"path": path,
			"cause": fmt.Sprintf(
				"scenario file is %d bytes, exceeds maxScenarioFileBytes (%d) -- refusing to read an unbounded file (BUG-305)",
				info.Size(), maxScenarioFileBytes,
			),
		})
	}

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
