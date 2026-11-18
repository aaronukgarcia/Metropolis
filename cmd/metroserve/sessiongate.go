// sessiongate.go is BUG-1196 (a2.md D1, Aaron-approved 2026-11-17): until
// session fan-out exists, a city serves exactly ONE live WebSocket
// connection at a time. wsserver's pump documents one live pump per
// Transport (a second connection would race the first's read of
// Results/Events/Deltas), but both metroserve paths used to admit a second
// connection to the same city. The gate below refuses it at the handshake
// with MET-P043 (the resolver returns a registry *errs.E; wsserver's
// resolverRefusal passes the code through instead of wrapping it in
// MET-P030), the refused socket is closed by wsserver, and the first
// connection is untouched. When the first connection closes the city is
// claimable again.
//
// The claim is taken in the transport resolver (before the handshake reply
// is written) so two simultaneous handshakes cannot both pass; wsserver's
// onOpen then confirms it and onClose releases it. A claim that is never
// confirmed (the client hung up between the resolver and the reply, a path
// on which wsserver fires no lifecycle hook) lapses after claimPendingTTL
// so a city can never be locked out forever by a half-open handshake.
package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/protocol/wsserver"
)

// ErrCitySessionBusy (MET-P043) is the handshake refusal for a second
// concurrent connection to a city that already has a live one.
const ErrCitySessionBusy = "MET-P043"

// claimPendingTTL bounds how long a claim whose resolver has RETURNED but
// whose connection has not yet been confirmed holds a city. It starts only
// after the resolver returns: while the resolver runs (a cold rebuild or an
// eviction teardown can take arbitrarily long) the claim never expires.
const claimPendingTTL = 10 * time.Second

var errSessionGateCopied = errors.New("metroserve: sessionGate used after being copied (SEC-020: must be used via the *sessionGate from newSessionGate)")

// sessionSlot is one city's gate state. Every claim carries a generation
// token; the resolver-phase calls (resolved, abandon) act only on a matching
// token, so a stale claim's late call can never clear a newer claim. The
// wsserver lifecycle hooks carry no token (they receive only tenant and
// city), so confirmed connections are counted instead: closed() releases the
// city only when the LAST confirmed connection has gone (F2).
type sessionSlot struct {
	token        uint64    // generation of the current claim
	resolving    bool      // the claim's resolver is still running: no expiry
	pendingUntil time.Time // resolver returned, unconfirmed: holds the city until then
	opens        int       // confirmed live connections
}

func (s *sessionSlot) idle() bool {
	return !s.resolving && s.pendingUntil.IsZero() && s.opens == 0
}

// sessionGate tracks, per city key, whether a live connection holds it.
// Wall-clock time here only bounds an unconfirmed claim; it never reaches an
// engine (GR#21).
type sessionGate struct {
	self atomic.Pointer[sessionGate] // SEC-020 copy guard

	mu      sync.Mutex
	slots   map[string]*sessionSlot
	nextTok uint64
	now     func() time.Time
}

func newSessionGate() *sessionGate {
	g := &sessionGate{slots: make(map[string]*sessionSlot), now: time.Now}
	g.self.Store(g)
	return g
}

func (g *sessionGate) checkNotCopied() error {
	if g.self.Load() != g {
		return errSessionGateCopied
	}
	return nil
}

func sessionGateKey(tenant, city string) string { return tenant + "/" + city }

// claim reserves the city for one connection and returns its token, or
// reports it busy (ok false).
func (g *sessionGate) claim(tenant, city string) (tok uint64, ok bool) {
	if g.checkNotCopied() != nil {
		return 0, false // fail closed: a copied gate cannot guard anything
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := sessionGateKey(tenant, city)
	s := g.slots[k]
	if s == nil {
		s = &sessionSlot{}
		g.slots[k] = s
	}
	if s.opens > 0 || s.resolving || g.now().Before(s.pendingUntil) {
		return 0, false
	}
	g.nextTok++
	s.token = g.nextTok
	s.resolving = true
	s.pendingUntil = time.Time{}
	return s.token, true
}

// resolved ends the resolver phase of claim tok and starts its unconfirmed
// TTL. A non-matching token is ignored.
func (g *sessionGate) resolved(tenant, city string, tok uint64) {
	if g.checkNotCopied() != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.slots[sessionGateKey(tenant, city)]; s != nil && s.token == tok && s.resolving {
		s.resolving = false
		s.pendingUntil = g.now().Add(claimPendingTTL)
	}
}

// abandon drops claim tok (the resolver failed). A non-matching token is
// ignored, so a stale claim cannot clear a newer one.
func (g *sessionGate) abandon(tenant, city string, tok uint64) {
	if g.checkNotCopied() != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := sessionGateKey(tenant, city)
	if s := g.slots[k]; s != nil && s.token == tok {
		s.resolving = false
		s.pendingUntil = time.Time{}
		if s.idle() {
			delete(g.slots, k)
		}
	}
}

// opened confirms a connection once wsserver has bound it. It consumes a
// pending (resolver-returned) claim; it never ends a different claim's
// resolver phase.
func (g *sessionGate) opened(tenant, city string) {
	if g.checkNotCopied() != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := sessionGateKey(tenant, city)
	s := g.slots[k]
	if s == nil {
		s = &sessionSlot{}
		g.slots[k] = s
	}
	s.opens++
	if !s.resolving {
		s.pendingUntil = time.Time{}
	}
}

// closed ends one confirmed connection; the city is released only when no
// confirmed connection and no claim remains (F2: closing one connection must
// not free the city under another that is still live).
func (g *sessionGate) closed(tenant, city string) {
	if g.checkNotCopied() != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := sessionGateKey(tenant, city)
	s := g.slots[k]
	if s == nil {
		return
	}
	if s.opens > 0 {
		s.opens--
	}
	if s.idle() {
		delete(g.slots, k)
	}
}

// busyError builds the MET-P043 refusal the resolver hands to wsserver.
func busyError(tenant, city string) error {
	return errs.New(ErrCitySessionBusy, "", map[string]any{"tenantId": tenant, "cityId": city})
}

// gatedResolver wraps resolve so a second connection to a busy city is
// refused with MET-P043 before any city is built or routed. keyOf maps the
// handshake's (tenant, city) to the gate key (identity for hosted mode, a
// constant for the single-engine legacy path).
func (g *sessionGate) gatedResolver(resolve wsserver.TransportResolver, keyOf func(tenant, city string) (string, string)) wsserver.TransportResolver {
	return func(tenant, city string) (protocol.Transport, error) {
		kt, kc := keyOf(tenant, city)
		tok, ok := g.claim(kt, kc)
		if !ok {
			return nil, busyError(tenant, city)
		}
		tr, err := resolve(tenant, city)
		if err != nil {
			g.abandon(kt, kc, tok)
			return nil, err
		}
		g.resolved(kt, kc, tok)
		return tr, nil
	}
}

// gatedLifecycle chains the gate's confirm/release around the optional
// inner open/close hooks (hosted mode's host.Acquire/Release).
func (g *sessionGate) gatedLifecycle(keyOf func(tenant, city string) (string, string), innerOpen, innerClose func(tenant, city string)) wsserver.Option {
	return wsserver.WithConnectionLifecycle(
		func(tenant, city string) {
			kt, kc := keyOf(tenant, city)
			g.opened(kt, kc)
			if innerOpen != nil {
				innerOpen(tenant, city)
			}
		},
		func(tenant, city string) {
			if innerClose != nil {
				innerClose(tenant, city)
			}
			kt, kc := keyOf(tenant, city)
			g.closed(kt, kc)
		},
	)
}

// hostedWSOptions is the hosted-mode wsserver wiring: the host-backed
// transport resolver and the host's connection refcount (FEAT-1972079942
// AC-2: onOpen pins the city via Acquire, onClose releases it so the idle
// evictor can unload it), both behind the BUG-1196 one-live-connection-per-
// city gate. The (tenant, city) the hooks receive are the SAME default-
// applied strings the resolver keyed on, so refcount, gate and routing agree.
func hostedWSOptions(host *CityHost) []wsserver.Option {
	hostResolve := func(tenant, city string) (protocol.Transport, error) {
		// The city outlives the connection that triggered its build (it runs
		// under the host's root context), so build under Background here, not
		// the request context -- the passed ctx only bounds the rehydrate
		// work, which is fine to run to completion for a durable city.
		// BUG-1225: the transport handed back is this connection's own view,
		// stamping its subscription epoch on every command.
		return host.BindConnection(context.Background(), tenant, city)
	}
	gate := newSessionGate()
	return []wsserver.Option{
		wsserver.WithTransportResolver(gate.gatedResolver(hostResolve, identityKey)),
		gate.gatedLifecycle(identityKey,
			func(tenant, city string) { host.Acquire(tenant, city) },
			func(tenant, city string) { host.Release(tenant, city) },
		),
	}
}

func identityKey(tenant, city string) (string, string) { return tenant, city }

// singleEngineKey collapses every handshake onto one gate slot: the legacy
// path serves one transport whatever city the client names.
func singleEngineKey(string, string) (string, string) { return "single", "single" }

// legacyWSOptions is the single-engine wsserver wiring (run()): every
// handshake, whatever city it names, resolves to the one transport and shares
// ONE gate slot, so a second connection is refused while the first is live.
//
// BUG-1225: eng (optional) is the single engine behind transport; each
// connection gets its own subscription epoch, stamped on its commands and
// ended by wsserver on that connection's own exit path.
func legacyWSOptions(transport protocol.Transport, eng ...*core.Engine) []wsserver.Option {
	gate := newSessionGate()
	resolve := func(string, string) (protocol.Transport, error) { return transport, nil }
	if len(eng) > 0 && eng[0] != nil {
		e := eng[0]
		resolve = func(string, string) (protocol.Transport, error) { return newOwnedTransport(transport, e), nil }
	}
	return []wsserver.Option{
		wsserver.WithTransportResolver(gate.gatedResolver(resolve, singleEngineKey)),
		gate.gatedLifecycle(singleEngineKey, nil, nil),
	}
}
