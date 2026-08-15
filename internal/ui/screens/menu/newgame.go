package menu

import (
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// NewGame submits the new-game setup form (MEN-5, ASM-255): it takes a
// seed and a debug flag and issues a new-game protocol.Command via send,
// then records the request so the form's state is inspectable. The field
// set is deliberately limited to the spec's own parenthetical (seed +
// debug flag) — not expanded — per ASM-255.
//
// The seed is carried as the player typed it (a string); the engine owns
// parsing/validation, matching the DebugPayload's key/value-args shape.
// The debug flag is serialised as "true"/"false" in the command's Args.
func (s *Screen) NewGame(seed string, debug bool, send SendCommandFunc) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "NewGame"}); err != nil {
		return err
	}
	req := NewGameRequest{Seed: seed, Debug: debug}
	cmd := opCommand(s.correlationID, opNewGame, map[string]string{
		"seed":  seed,
		"debug": strconv.FormatBool(debug),
	})
	if err := send(cmd); err != nil {
		return err
	}
	s.mu.Lock()
	s.newGameReq = req
	s.haveNewGame = true
	s.mu.Unlock()
	return nil
}

// LastNewGameRequest returns the most recently submitted new-game request.
// have is false until NewGame has succeeded once.
func (s *Screen) LastNewGameRequest() (req NewGameRequest, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LastNewGameRequest"}); err != nil {
		return NewGameRequest{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LastNewGameRequest"}); err != nil {
		return NewGameRequest{}, false
	}
	return s.newGameReq, s.haveNewGame
}
