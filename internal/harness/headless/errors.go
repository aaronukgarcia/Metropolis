package headless

// Registry error codes for harness.headless (MOD-015). Range: H200-H299,
// claimed in data/errors.json's "ranges.reserved" table (checked against
// `grep -rn "MET-H2" internal/ cmd/` before claiming, per BUG-008's
// lesson). Every code below IS registered there with real
// severity/module/message/remedy fields (GR#7) — see that file's
// "H200-H299" reserved-range entry and its "codes" section.
const (
	// ErrScenarioReadFailed: the -scenario file could not be read, was not
	// a well-formed JSON array of protocol.Command envelopes, or one of
	// its elements failed to decode/validate (AC-8). Checked before the
	// engine boots or any tick advances, so a bad scenario file never
	// produces a panic or a partial run.
	ErrScenarioReadFailed = "MET-H200"

	// ErrOutputWriteFailed: creating the -out bundle directory, opening a
	// shard writer, or writing the header failed (AC-9). A headless run
	// never silently reports success with no file written.
	ErrOutputWriteFailed = "MET-H201"

	// ErrCommandRejected: a command issued by the headless driver — a
	// scenario-script command, or an internal AdvanceTicks batch — was
	// rejected by the engine. Aborts the run rather than silently
	// skipping the rejected command.
	ErrCommandRejected = "MET-H202"

	// ErrEngineRunFailed: engine.core.RunCommandLoop exited abnormally
	// (engine.headless.md AC-4/AC-7) — the transport closed before this
	// package told it to shut down. A headless run has no operator
	// watching a screen, so this must never be mistaken for a clean,
	// complete run; any -out snapshot this run may have already started
	// writing is not to be trusted.
	ErrEngineRunFailed = "MET-H203"

	// ErrInputReadFailed: FEAT-035's -in/-resume flag (Config.InDir)
	// pointed at a bundle directory whose header.json could not be read
	// (serialize.ReadHeader failed) — the reload mechanism AC-M1's
	// end-to-end test relies on to carry a prior run's DebugTouched flag
	// forward via Engine.Snapshot's MergeDebugTouched. Checked before the
	// engine boots, so a bad -in path never produces a partial run.
	ErrInputReadFailed = "MET-H204"

	// ErrCommandSendFailed: SendCommand itself failed at the transport
	// layer (a failed cmd.Validate(), protocol.ErrTransportClosed, or
	// protocol.ErrCommandQueueFull from InProcTransport.SendCommand), so
	// the command never reached the engine — there is no engine.core
	// rejection code to report (distinct from ErrCommandRejected, which
	// surfaces a real one via result.Error.Code). BUG-220 gave this branch
	// its own dedicated code instead of stuffing a transport error string
	// into ErrCommandRejected's {engineErrorCode} placeholder.
	ErrCommandSendFailed = "MET-H205"
)
