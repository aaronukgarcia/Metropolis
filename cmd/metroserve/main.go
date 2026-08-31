// Command metroserve is the FEAT-1972079852 increment-1 network host: it
// wires a real *core.Engine (compose.Wire, the same composition root
// cmd/metropolis and internal/harness/headless already use) over a real
// *protocol.InProcTransport, then exposes that transport to out-of-process
// clients (the webconsole) over a WebSocket JSON-RPC endpoint
// (internal/protocol/wsserver), guarded by the version handshake DD1/DD2
// ruled on FEAT-1972079852 (Aaron, 2026-08-31): a client whose version does
// not match this binary's own is refused, never served.
//
// This is deliberately a SEPARATE binary from cmd/metropolis (the TUI),
// not a mode flag on it: the TUI's own process topology (M0-ENG §1.1) owns
// one InProcTransport for its own tcell render/input loops; metroserve
// owns a second, independent Engine+Transport pair for network clients.
// Running both against the SAME city at once is out of scope for
// increment 1 (tracked as a follow-up under FEAT-1972079852's inc2+ —
// see its BOW comments) — metroserve is its own standalone city today,
// exactly like internal/harness/headless already is.
//
// # What this binary proves, and what it doesn't (read before demoing it)
//
//   - PROVEN: a real engine.core simulation, reachable over a real
//     network socket, from a real out-of-process client, refusing a
//     version-mismatched connection rather than serving it.
//   - NOT proven: production deployment shape, auth, TLS, multi-client
//     fan-out (v1 scope is one live WS connection at a time per
//     wsserver's own doc comment), or the webconsole's full adapter
//     (FEAT-1972079852's inc2+: journal-as-wire-trace, all views beyond
//     f2.finance, staleness UI).
//
// Module key: int.protocol (see code.json) — this binary is a transport
// host for int.protocol's existing envelope/subscription machinery, not a
// new module in its own right.
// Spec ref:   docs/planning/acceptance/feat-1972079852-engine-protocol-adapter.md
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/protocol/wsserver"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("metroserve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "localhost:9999", "address to listen on (DD1 placeholder address, per the acceptance doc's Implementation Notes)")
	seed := fs.Uint64("seed", 1, "world seed")
	tickInterval := fs.Duration("tick-interval", 250*time.Millisecond, "wall-clock interval between single-tick AdvanceTicks commands (v1 fixed-step driver; see the doc comment on tickLoop for why this is simpler than cmd/metropolis's own BUG-322 tick driver)")
	printVersion := fs.Bool("version", false, "print build identity and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *printVersion {
		_, _ = fmt.Fprintln(stdout, buildinfo.String())
		return 0
	}

	correlationID := string(protocol.NewCorrelationID())

	e := core.NewEngine(core.WithWorldSeed(*seed))
	if _, err := compose.Wire(e, nil); err != nil {
		_, _ = fmt.Fprintf(stderr, "metroserve: compose.Wire failed: %v\n", err)
		return 1
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pumpDone, err := e.StartSubscriptionPump(ctx, transport)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "metroserve: StartSubscriptionPump failed: %v\n", err)
		_ = transport.Close()
		return 1
	}

	loopDone := make(chan error, 1)
	go func() { loopDone <- e.RunCommandLoop(ctx, transport) }()

	tickDone := tickLoop(ctx, transport, *tickInterval, correlationID)

	wsHandler := wsserver.New(transport, buildinfo.Version, wsserver.DefaultHandshakeTimeout)
	mux := http.NewServeMux()
	mux.Handle("/ws", wsHandler)
	httpSrv := &http.Server{Addr: *addr, Handler: mux}

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.ListenAndServe() }()

	_, _ = fmt.Fprintf(stdout, "metroserve %s listening on ws://%s/ws (engine version handshake required)\n", buildinfo.Version, *addr)

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			_, _ = fmt.Fprintf(stderr, "metroserve: http server error: %v\n", err)
		}
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)

	<-tickDone
	<-loopDone
	<-pumpDone
	_ = transport.Close()
	return 0
}

// tickLoop is metroserve's v1 wall-clock tick driver: every tickInterval
// it sends exactly one AdvanceTicksPayload{N:1} command. This is
// DELIBERATELY simpler than cmd/metropolis's own tickdriver.go (BUG-322's
// fixed-step-with-bounded-catch-up pacer): that driver's extra machinery
// (credit accumulation, catch-up batching, Speed8x-aware pacing) exists to
// track a player-visible clock UI metroserve does not have in increment 1.
// A dev/network host that simply advances one tick per wall-clock interval
// is sufficient to prove the WS transport end-to-end; adopting the full
// pacer (or extracting it into a shared, importable package so both
// binaries use one implementation, GR#3) is left as an inc2+ follow-up.
//
// Errors sending the tick (a full command queue, a closed transport) are
// registry-logged (GR#1) and the loop keeps trying on the next interval
// rather than crashing the whole server over a single missed tick.
func tickLoop(ctx context.Context, transport *protocol.InProcTransport, interval time.Duration, correlationID string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cmd := protocol.Command{
					ProtocolVersion: protocol.ProtocolVersion,
					CorrelationID:   protocol.CorrelationID(correlationID),
					Kind:            protocol.KindAdvanceTicks,
					Payload:         protocol.AdvanceTicksPayload{N: 1},
				}
				if err := transport.SendCommand(cmd); err != nil {
					// Not wrapped as a registry *errs.E: the underlying
					// error (ErrCommandQueueFull/ErrTransportClosed) is
					// already one of protocol's own documented sentinels,
					// and there is no MET-Pxxx code that means "a
					// best-effort background tick command was dropped" --
					// inventing one to satisfy GR#7's letter here would
					// misrepresent the failure. Logged plainly instead;
					// the loop retries on its own next interval.
					fmt.Fprintf(os.Stderr, "metroserve: tickLoop: SendCommand failed: %v (correlation %s)\n", err, correlationID)
				}
			}
		}
	}()
	return done
}
