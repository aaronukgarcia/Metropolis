package trade

import (
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// Op names this screen issues its player actions under (TRD-1 create/
// cancel, TRD-3 set-buffer). The v1 protocol command vocabulary
// (internal/protocol/commands.go) is sealed — only the protocol package
// can add a Kind, and it has no CreateContract/CancelContract/SetBuffer
// yet — so this screen issues its actions as protocol.DebugPayload
// commands with these fixed Op strings, mirroring ui.screen.menu's
// ASM-524 precedent. When the real command Kinds land (the
// Buy/Zone/Build/Demolish precedent, commit 613b7d0), these Op strings
// are replaced by typed payloads — logged as ASM-1193, flagged to Bill
// because these are in-world gameplay actions riding the Debug seam that
// commands.go's extension-rule note reserves for the F12 escape hatch.
const (
	opCreateContract = "trade.create-contract"
	opCancelContract = "trade.cancel-contract"
	opSetBuffer      = "trade.set-buffer"
)

// opCommand builds a protocol.Command carrying this screen's action as a
// DebugPayload (Op + key/value Args). correlationID is minted once at
// construction (screen.go's New) and reused so every action this screen
// issues is traceable end-to-end (GR#1).
func opCommand(correlationID string, op string, args map[string]string) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(correlationID),
		Kind:            protocol.KindDebug,
		Payload:         protocol.DebugPayload{Op: op, Args: args},
	}
}

// CancellationPenalty returns the penalty cancelling contractID would
// incur (TRD-7): the contract's CancellationPenaltyMicropounds, which the
// view reports as 0 while the contract is still inside its penalty-free
// window. It is the "surface the penalty amount BEFORE commit" half of
// TRD-7 — the caller shows this figure in a confirmation step, then calls
// CancelContract to actually issue the command. found is false when the
// view has no such contract (or the contracts sub-surface is unavailable).
func (s *Screen) CancellationPenalty(contractID string) (penaltyMicropounds int64, found bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CancellationPenalty"}); err != nil {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CancellationPenalty"}); err != nil {
		return 0, false
	}
	for _, c := range s.contracts {
		if c.ID == contractID {
			return c.CancellationPenaltyMicropounds, true
		}
	}
	return 0, false
}

// CreateContract issues the create-contract action (TRD-1) for a new
// import contract on commodity with the given term and £/unit price.
// Price is in micropounds (M0-ENG §1.2). It returns a registry-sourced
// error (MET-V104, ErrUnknownCommodity) if commodity is empty — the engine
// remains the authority on whether the contract is accepted, but an empty
// commodity is rejected here rather than sent as a malformed command.
func (s *Screen) CreateContract(send SendCommandFunc, commodity string, termMonths int, pricePerUnitMicropounds int64) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CreateContract"}); err != nil {
		return err
	}
	if commodity == "" {
		return errs.New(ErrUnknownCommodity, s.correlationID, map[string]any{"commodity": ""})
	}
	args := map[string]string{
		"commodity":               commodity,
		"termMonths":              strconv.Itoa(termMonths),
		"pricePerUnitMicropounds": strconv.FormatInt(pricePerUnitMicropounds, 10),
	}
	return send(opCommand(s.correlationID, opCreateContract, args))
}

// CancelContract issues the cancel-contract action (TRD-1/TRD-7) for the
// given contract, carrying the cancellation penalty it would incur in the
// command's Args so the penalty is never a silently-applied charge with no
// explanation. It is the "commit" half of TRD-7 — the caller is expected
// to have surfaced the penalty via CancellationPenalty first. A cancel for
// a contract ID absent from the current view is refused loudly with
// MET-V103, never silently dropped (TRD-7's "never a silent rejection").
func (s *Screen) CancelContract(send SendCommandFunc, contractID string) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CancelContract"}); err != nil {
		return err
	}
	penalty, found := s.CancellationPenalty(contractID)
	if !found {
		return errs.New(ErrUnknownContract, s.correlationID, map[string]any{"contractId": contractID})
	}
	args := map[string]string{
		"contractId":         contractID,
		"penaltyMicropounds": strconv.FormatInt(penalty, 10),
	}
	return send(opCommand(s.correlationID, opCancelContract, args))
}

// SetBuffer issues the set-buffer action (TRD-3) for commodity, setting
// its warehouse safety-buffer target in t/day (the only spec-fixed unit
// for flow figures — ASM-251). A commodity absent from the current
// warehouse view is refused loudly with MET-V104, never a silently-created
// row.
func (s *Screen) SetBuffer(send SendCommandFunc, commodity string, bufferTonnesPerDay int64) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetBuffer"}); err != nil {
		return err
	}
	if !s.hasCommodity(commodity) {
		return errs.New(ErrUnknownCommodity, s.correlationID, map[string]any{"commodity": commodity})
	}
	args := map[string]string{
		"commodity":          commodity,
		"bufferTonnesPerDay": strconv.FormatInt(bufferTonnesPerDay, 10),
	}
	return send(opCommand(s.correlationID, opSetBuffer, args))
}

// hasCommodity reports whether commodity is present in the current
// warehouse view. Reads s.warehouse under mu.
func (s *Screen) hasCommodity(commodity string) bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "hasCommodity"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "hasCommodity"}); err != nil {
		return false
	}
	for _, w := range s.warehouse {
		if w.Commodity == commodity {
			return true
		}
	}
	return false
}
