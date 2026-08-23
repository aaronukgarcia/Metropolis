// Package transport is int.transport (MOD-086 / INT-005): the WebSocket
// bridge that lets the React front end (web/) drive the composed
// baseline-one engine over a real network seam.
//
// # Wire shape (JSON-RPC-style, one WebSocket per session)
//
// Client -> server: each text frame is a raw protocol.Command envelope
// JSON (protocolVersion/correlationId/issuedAtTick/kind/payload) — the
// exact bytes protocol.EncodeCommand produces. The command's own
// CorrelationID doubles as the JSON-RPC request id.
//
// Server -> client: one JSON object per frame, discriminated by "type":
//
//	{"type":"result","result":{...protocol.CommandResult...}}
//	{"type":"delta","delta":{...protocol.Delta...}}   (subscription pushes)
//	{"type":"event","event":{...protocol.Event...}}   (engine events)
//	{"type":"error","error":{"code","display","correlationId"}}
//
// A CommandResult frame always matches a previously sent Command by
// correlationId; the first Delta of a fresh subscription carries the
// Subscribe command's CorrelationID (engine/core/subscribe.go's
// pendingCorrID contract), which is how this package binds a
// SubscriptionID to the session that must receive it.
//
// # What this package does NOT do
//
// It adds no engine behaviour and touches no engine state outside the
// existing protocol.Transport seams: commands go through
// InProcTransport.SendCommand into core.Engine.RunCommandLoop exactly as
// the TUI's do, deltas flow out of core.Engine.StartSubscriptionPump,
// and determinism (GR#21/the determinism gate) is untouched — read-only
// queries (Subscribe/InspectEntity) and gameplay intents ride the same
// single command path everything else already uses.
package transport
