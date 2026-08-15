package menu

import "github.com/aaronukgarcia/Metropolis/internal/protocol"

// Op names this screen issues its actions under (MEN-1/MEN-5). The v1
// protocol command vocabulary (internal/protocol/commands.go) is
// skeleton-era — AdvanceTicks/SetSpeed/Pause/Resume/Subscribe/Unsubscribe/
// InspectEntity/Debug — and carries no dedicated NewGame/LoadGame/
// SaveGame/DeleteSave Kind yet. This screen therefore issues its
// player-facing save/menu actions as protocol.DebugPayload commands with
// these fixed Op strings (the one op-defined, key/value-args command the
// protocol exposes today), rather than inventing new Kinds outside the
// protocol package or reaching into the engine. When engine.core's real
// new-game/load/save/delete command Kinds land, these Op strings are
// replaced by the typed payloads (logged as ASM-524). See commands.go's
// extension rule in internal/protocol.
const (
	opNewGame    = "menu.new-game"
	opLoadSave   = "menu.load-save"
	opSaveGame   = "menu.save-game"
	opDeleteSave = "menu.delete-save"
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
