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

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
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
	// FEAT-1972079936 Phase 1 inc4: durable persistence + rehydrate-on-restart.
	// persist-dir default "" keeps persistence OFF (behaviour byte-for-byte
	// unchanged); see setUpPersistence (persist.go) for the wiring/rehydrate.
	persistDir := fs.String("persist-dir", "", "directory for durable city persistence (empty = OFF; a persisted city rehydrates from its journal on restart)")
	city := fs.String("city", "default", "city identity to persist/rehydrate under (FEAT-1972079936 Phase 1; tenant is the placeholder \"local\")")
	printVersion := fs.Bool("version", false, "print build identity and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *printVersion {
		_, _ = fmt.Fprintln(stdout, buildinfo.String())
		return 0
	}

	// FEAT-1972079936 Phase 2 inc2 (AC-5): with persistence enabled, serve a
	// multi-city CityHost so each WS connection is routed to its own city's
	// engine (connection->city routing). The no-persist default path below is
	// the legacy single-city host, byte-for-byte unchanged (AC-6). Gating on
	// persist-dir keeps a default `metroserve` invocation identical to today.
	if *persistDir != "" {
		return runHosted(*addr, *persistDir, *city, *tickInterval, stdout, stderr)
	}

	correlationID := string(protocol.NewCorrelationID())

	e := core.NewEngine(core.WithWorldSeed(*seed))
	if _, _, err := setUpPersistence(e, *persistDir, *city, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "metroserve: %v\n", err)
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

// runHosted is metroserve's FEAT-1972079936 Phase 2 inc2 multi-city host
// path (AC-5). It builds a CityHost over the shared durable Store, pre-creates
// the default city so a client that names none is served immediately, and
// serves the WS endpoint behind a per-connection transport RESOLVER
// (wsserver.WithTransportResolver) that maps each handshake's (tenant, city)
// to that city's own engine transport via host.GetOrCreate.
//
// The host owns each city's pump/command-loop/tick-driver (buildCity in
// cityhost.go starts them), so this path has NO top-level single-city engine,
// transport, pump, loop, or tickLoop of its own — that machinery moves under
// the host's per-city lifecycle, exactly as AC-5's minimal-diff option calls
// for. On shutdown, host.Close() cancels every city and joins its goroutines.
//
// The resolver closure is where the import-direction constraint is honoured:
// wsserver cannot import persist (or this main package), so it hands the
// resolver two plain strings; metroserve builds the persist.CityKey here,
// inside its own package, and calls its own host — no new dependency edge is
// forced on internal/protocol.
func runHosted(addr, persistDir, cityID string, tickInterval time.Duration, stdout, stderr *os.File) int {
	host, err := NewCityHost(persistDir, tickInterval)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "metroserve: %v\n", err)
		return 1
	}
	defer func() { _ = host.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Pre-create the default city (tenant/city matching wsserver's defaults)
	// so the very first default-client connection is served without a build
	// stall, and a corrupt persisted default surfaces at boot rather than on
	// the first connection.
	if _, err := host.GetOrCreate(ctx, persist.CityKey{TenantID: persistTenantID, CityID: cityID}); err != nil {
		_, _ = fmt.Fprintf(stderr, "metroserve: pre-create city %q: %v\n", cityID, err)
		return 1
	}

	resolver := func(tenant, city string) (protocol.Transport, error) {
		// The city outlives the connection that triggered its build (it runs
		// under the host's root context), so build under Background here, not
		// the request context — the passed ctx only bounds the rehydrate work,
		// which is fine to run to completion for a durable city.
		rc, err := host.GetOrCreate(context.Background(), persist.CityKey{TenantID: tenant, CityID: city})
		if err != nil {
			return nil, err
		}
		return rc.Transport(), nil
	}

	// transport is nil: with a resolver installed the Server never touches its
	// single wrapped transport field (every connection binds to a resolved
	// per-city transport instead).
	wsHandler := wsserver.New(nil, buildinfo.Version, wsserver.DefaultHandshakeTimeout, wsserver.WithTransportResolver(resolver))
	mux := http.NewServeMux()
	mux.Handle("/ws", wsHandler)
	httpSrv := &http.Server{Addr: addr, Handler: mux}

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.ListenAndServe() }()

	_, _ = fmt.Fprintf(stdout, "metroserve %s listening on ws://%s/ws (multi-city host; default city %q; engine version handshake required)\n", buildinfo.Version, addr, cityID)

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
