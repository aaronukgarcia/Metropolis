package diagrams

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// codeMissingNode is the registry code for a topology edge that references
// a node ID absent from the node set (AC-7). Range U900-U949 is reserved
// for ui.diagrams in data/errors.json (lead arbitration, 2026-08-14: the
// U900-U999 block was split — ui.diagrams owns U900-U949, ui.alerts owns
// U950-U999).
const codeMissingNode = "MET-U900"

// codeCoordOutOfRange is the registry code for a network node whose raw cell
// coordinate is outside the renderable range (SEC-067): a magnitude beyond
// maxCoord, or a node span that exceeds the render buffer. Same U900-U949
// reservation as codeMissingNode.
const codeCoordOutOfRange = "MET-U901"

// errMissingNode builds the MET-U900 error for a dangling edge reference,
// naming both the offending edge and the missing node ID (AC-7). Each
// render call that detects a malformed topology is a boundary, so it mints
// a fresh correlation ID rather than propagating one it was never given.
func errMissingNode(edge, node SourceID) error {
	return errs.New(codeMissingNode, errs.NewCorrelationID(), map[string]any{
		"edge": string(edge),
		"node": string(node),
	})
}

// errCoordOutOfRange builds the MET-U901 error for an out-of-range network
// grid coordinate (SEC-067), naming the offending node and its raw (x, y)
// coordinate. Each render call that detects an out-of-range coordinate is a
// boundary, so it mints a fresh correlation ID.
func errCoordOutOfRange(node SourceID, x, y int) error {
	return errs.New(codeCoordOutOfRange, errs.NewCorrelationID(), map[string]any{
		"node": string(node),
		"x":    x,
		"y":    y,
	})
}
