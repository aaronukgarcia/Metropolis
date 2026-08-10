//go:build ignore

// Command gen regenerates this repo's checked-in sample fixture
// (fixtures/folkestone64-sample.ndjson.gz + .header.json) from a short,
// deterministic H-STUB session (AC-6/AC-15). It is NOT part of the
// normal build (excluded via the "ignore" build tag) — run it explicitly
// with:
//
//	go run internal/harness/replay/gen/main.go
//
// from the repo root whenever the sample fixture needs regenerating
// (e.g. after a protocol or serializer format change). The session
// itself is fully deterministic (StubEngine with chaos disabled, no
// wall-clock dependency anywhere on this path — GR#21), so re-running
// this script without any code change reproduces byte-identical output.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/stub"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// fixtureName is the checked-in sample's base name — must be a single
// clean path component (serialize.ValidateShardName, via
// replay.Save/Load).
const fixtureName = "folkestone64-sample"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	tr := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer,
		protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer,
		protocol.DefaultDeltaBuffer,
	)
	eng, err := stub.NewStubEngine(tr)
	if err != nil {
		return fmt.Errorf("NewStubEngine: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = eng.Run(ctx)
	}()

	rec := replay.NewRecorder()

	// This script is its OWN drain loop rather than using
	// Recorder.TapTransport: TapTransport's forwarding goroutines and a
	// caller both trying to read tr.Results() would split the stream
	// between two independent consumers (a Go channel has no fan-out —
	// see tap.go's doc comment), and this script needs to correlate each
	// Result back to the command that caused it. So this one goroutine
	// is the sole reader of Results/Events/Deltas: it records every
	// message via rec.Observe*, AND forwards each Result onward on
	// resultCh for send() below to correlate against.
	resultCh := make(chan protocol.CommandResult, protocol.DefaultResultBuffer)
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		results, events, deltas := tr.Results(), tr.Events(), tr.Deltas()
		for results != nil || events != nil || deltas != nil {
			select {
			case r, ok := <-results:
				if !ok {
					results = nil
					continue
				}
				_ = rec.ObserveResult(r)
				resultCh <- r
			case e, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				_ = rec.ObserveEvent(e)
			case d, ok := <-deltas:
				if !ok {
					deltas = nil
					continue
				}
				_ = rec.ObserveDelta(d)
			}
		}
	}()

	send := func(kind protocol.Kind, payload protocol.CommandPayload) error {
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.NewCorrelationID(),
			Kind:            kind,
			Payload:         payload,
		}
		if err := tr.SendCommand(cmd); err != nil {
			return fmt.Errorf("SendCommand(%s): %w", kind, err)
		}
		if err := rec.ObserveCommand(cmd); err != nil {
			return fmt.Errorf("ObserveCommand(%s): %w", kind, err)
		}
		select {
		case res := <-resultCh:
			if res.CorrelationID != cmd.CorrelationID {
				return fmt.Errorf("command %s: got result for correlation %q, want %q (unexpected interleaving)", kind, res.CorrelationID, cmd.CorrelationID)
			}
			if !res.Accepted {
				return fmt.Errorf("command %s rejected: %+v", kind, res.Error)
			}
		case <-time.After(2 * time.Second):
			return fmt.Errorf("timed out waiting for a CommandResult to %s", kind)
		}
		return nil
	}

	if err := send(protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"}); err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		if err := send(protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 1}); err != nil {
			return err
		}
	}
	if err := send(protocol.KindPause, protocol.PausePayload{}); err != nil {
		return err
	}
	if err := send(protocol.KindResume, protocol.ResumePayload{}); err != nil {
		return err
	}

	cancel()
	<-runDone
	if err := tr.Close(); err != nil {
		return fmt.Errorf("Close: %w", err)
	}
	// Close() closes Results/Events/Deltas — a closed Go channel still
	// delivers everything already buffered before reporting ok=false, so
	// waiting for drainDone here is a genuine synchronisation point (not
	// a fixed delay): the drain loop above is guaranteed to have
	// recorded every already-queued message by the time it returns.
	<-drainDone

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return fmt.Errorf("runtime.Caller(0) failed — cannot locate repo root")
	}
	// this file: internal/harness/replay/gen/main.go -> repo root is five
	// levels up (main.go -> gen -> replay -> harness -> internal -> root).
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))))
	fixturesDir := filepath.Join(repoRoot, "fixtures")

	meta := replay.FixtureMeta{WorldSeed: int64(stub.FixtureSeed), AppVersion: "gen/main.go (H-STUB, chaos disabled)"}
	if err := replay.Save(fixturesDir, fixtureName, rec, meta); err != nil {
		return fmt.Errorf("Save: %w", err)
	}

	fmt.Printf("wrote %s (%d records) to %s\n", fixtureName, rec.Len(), fixturesDir)
	return nil
}
