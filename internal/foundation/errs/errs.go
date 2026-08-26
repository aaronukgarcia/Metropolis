package errs

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// clockMu/clock is the package-wide, injectable time source for *E.Time.
// Per M0-ENG §1.1, engine simulation logic must never call the wall clock
// on the tick path; engine bootstrap should call SetClock with the sim
// clock's Now method before the first tick. Defaults to time.Now.
var (
	clockMu sync.RWMutex
	clock   = time.Now
)

// SetClock overrides the package-wide clock used to timestamp every *E
// constructed by New/Wrap. See the package doc's determinism note.
func SetClock(now func() time.Time) {
	clockMu.Lock()
	clock = now
	clockMu.Unlock()
}

func now() time.Time {
	clockMu.RLock()
	defer clockMu.RUnlock()
	return clock()
}

// E is the one error type every Metropolis error returns as. It is never
// constructed directly — use New or Wrap, which enforce GR#7 (registry-
// sourced errors only) at runtime.
type E struct {
	Code          string         // MET-<layer><NNN>, e.g. "MET-F001"
	CorrelationID string         // propagated end-to-end (GR#1)
	Module        string         // owning module key, from the registry entry
	Msg           string         // rendered message template
	Ctx           map[string]any // the context this error was constructed with
	Time          time.Time
	Wrapped       error // non-nil for errors constructed via Wrap
}

// Error implements the error interface. Prefer Display for user-visible
// text — Error additionally appends the wrapped cause, which is useful
// in logs/panics but noisier than the GR#1 one-liner.
func (e *E) Error() string {
	if e.Wrapped != nil {
		return fmt.Sprintf("%s: %v", e.Display(), e.Wrapped)
	}
	return e.Display()
}

// Display renders the GR#1 user-visible one-liner: registry code +
// message + correlation ID, all selectable/copyable as plain text.
func (e *E) Display() string {
	return fmt.Sprintf("[%s] %s (correlation: %s)", e.Code, e.Msg, e.CorrelationID)
}

// Unwrap supports errors.Is/As/Unwrap against the wrapped cause (Wrap
// only — New leaves Wrapped nil).
func (e *E) Unwrap() error {
	return e.Wrapped
}

// Is makes errors.Is(err, target) match any *E with the same Code,
// regardless of correlation ID, message, or instance — so callers can
// test "was this a MET-F003" with a sentinel-style
// errors.Is(err, &errs.E{Code: "MET-F003"}) without constructing a full
// registry-backed error just to compare against.
func (e *E) Is(target error) bool {
	t, ok := target.(*E)
	if !ok {
		return false
	}
	return e.Code == t.Code
}

// New constructs a registry-sourced error. code must be a code present in
// data/errors.json; if it is not or the registry itself failed to load or
// validate, New never panics — it returns a well-formed error instead (MET-F001
// if the registry could not be loaded, MET-F002 if it failed validation,
// or MET-F003 if a valid registry simply lacks the code) (GR#7 enforced at
// runtime, failing loudly rather than silently or fatally).
//
// correlationID should be non-empty (mint one with NewCorrelationID at
// the boundary); an empty value is tolerated but logs a MET-F004 warning
// and the returned error's CorrelationID is replaced with a visible
// placeholder rather than silently propagating an empty string.
func New(code, correlationID string, ctx map[string]any) *E {
	return construct(code, correlationID, nil, ctx)
}

// Wrap is New plus a preserved cause, retrievable via errors.Unwrap /
// errors.As. Use it when translating a lower-level error (e.g. from the
// standard library or a third-party call) into a registry-sourced one.
func Wrap(code, correlationID string, cause error, ctx map[string]any) *E {
	return construct(code, correlationID, cause, ctx)
}

const missingCorrelationPlaceholder = "MISSING-CORRELATION-ID"

func construct(code, correlationID string, cause error, ctx map[string]any) *E {
	t := now()
	if ctx == nil {
		ctx = map[string]any{}
	}

	// BUG-357 root fix: Wrap must supply the cause it looks like it supplies.
	// When wrapping a non-nil cause and the caller did not provide an explicit
	// "cause" ctx key, inject the cause text so templates carrying {cause}
	// render it instead of the literal placeholder — the 376-site measurement
	// (BUG-357) was dominated by exactly this asymmetry. An explicit ctx
	// "cause" always wins; a nil cause leaves {cause} literal, which is a
	// genuine ctx gap the mechanical gate is built to catch, not a value to
	// invent here.
	if cause != nil {
		if _, ok := ctx["cause"]; !ok {
			// Copy-on-inject (BUG-357 LOW finding, pr6 round): never mutate the
			// caller's ctx map. A map shared across calls would otherwise carry
			// the first Wrap's cause into every later error built from the same
			// map — a New with a nil cause would then render the stale cause
			// text, violating the "nil cause leaves {cause} literal" contract.
			injected := make(map[string]any, len(ctx)+1)
			for k, v := range ctx {
				injected[k] = v
			}
			injected["cause"] = cause.Error()
			ctx = injected
		}
	}

	if correlationID == "" {
		logEntry(Entry{
			Ts:            t.UTC().Format(time.RFC3339Nano),
			Level:         "warn",
			Code:          "MET-F004",
			CorrelationID: missingCorrelationPlaceholder,
			Module:        "foundation.errors",
			Msg:           fmt.Sprintf("correlation ID missing or empty when constructing error %s", code),
		})
		correlationID = missingCorrelationPlaceholder
	}

	entries, regErr := loadRegistry()

	// BUG-279: a registry that failed to load/validate is a DIFFERENT fault
	// from a valid registry missing one code. The former is fatal and reported
	// as MET-F001 (could not load) or MET-F002 (failed validation); only the
	// latter — a valid registry with no such code — is the MET-F003
	// "unregistered code" fallback. Collapsing all three to MET-F003 (the old
	// behaviour) left F001/F002 unreachable and downgraded fatal to error.
	if regErr != nil {
		return constructRegistryFailure(code, correlationID, cause, regErr, t)
	}

	entry, found := entries[code]
	if !found {
		return constructUnregistered(code, correlationID, cause, entries, t)
	}

	msg := renderTemplate(entry.Message, code, correlationID, ctx)
	e := &E{
		Code:          code,
		CorrelationID: correlationID,
		Module:        entry.Module,
		Msg:           msg,
		Ctx:           ctx,
		Time:          t,
		Wrapped:       cause,
	}
	logEntry(e.toEntry(entry.Severity))
	return e
}

// constructUnregistered builds the MET-F003 "unregistered code" fallback used
// when a VALID registry simply has no entry for the requested code (found ==
// false). BUG-279: this is now the ONLY caller path for MET-F003 — a registry
// that failed to load or validate is routed to constructRegistryFailure
// (MET-F001/F002) BEFORE this function is reached, so entries here is always
// the successfully-loaded map and regErr is always nil. MET-F003's own
// template (from data/errors.json) is used for consistency; a hardcoded
// template remains as a defence-in-depth fallback for the pathological case of
// a valid registry that somehow lacks its own MET-F003 entry, so this path can
// never panic.
func constructUnregistered(code, correlationID string, cause error, entries map[string]registryEntry, t time.Time) *E {
	constructor := "New"
	if cause != nil {
		constructor = "Wrap"
	}

	fallbackCtx := map[string]any{"code": code, "constructor": constructor}
	module := "foundation.errors"

	var msg string
	if fbEntry, ok := entries["MET-F003"]; ok {
		msg = renderTemplate(fbEntry.Message, "MET-F003", correlationID, fallbackCtx)
		module = fbEntry.Module
	} else {
		msg = renderTemplate(
			"unregistered error code {code} requested via {constructor} (correlation {correlationId})",
			"MET-F003", correlationID, fallbackCtx,
		)
	}

	e := &E{
		Code:          "MET-F003",
		CorrelationID: correlationID,
		Module:        module,
		Msg:           msg,
		Ctx:           fallbackCtx,
		Time:          t,
		Wrapped:       cause,
	}
	logEntry(e.toEntry("error"))
	return e
}

// constructRegistryFailure builds the fatal error returned when the registry
// itself could not be used, so the requested code is irrelevant — NO code can
// be resolved (BUG-279). It distinguishes the two fatal modes data/errors.json
// defines: MET-F001 when the registry could not be loaded at all (path
// unresolved, file unreadable, bytes unparseable) and MET-F002 when it loaded
// but failed schema validation (duplicate/misformatted code, missing field,
// malformed token). Both are severity "fatal"; the pre-fix code collapsed every
// registry failure to MET-F003 ("unregistered code", severity "error"),
// leaving F001/F002 unreachable and downgrading a whole-registry outage.
//
// Like constructUnregistered's hardcoded branch, this path can never depend on
// the (broken) registry to render its own message — regErr != nil means the
// entries map is unusable — so F001/F002's templates are hardcoded here, kept
// in sync with data/errors.json. The {path}/{cause} tokens are filled from the
// classified registryError.
func constructRegistryFailure(code, correlationID string, cause error, regErr error, t time.Time) *E {
	kind := registryLoadFailed
	path := ""
	var re *registryError
	if errors.As(regErr, &re) {
		kind = re.kind
		path = re.path
	}

	failCode := "MET-F001"
	tmpl := "error registry failed to load from {path}: {cause}"
	if kind == registryValidationFailed {
		failCode = "MET-F002"
		tmpl = "error registry at {path} failed validation: {cause}"
	}
	if path == "" {
		// Resolution itself failed, so no concrete path is known; name the
		// configured default so the message is still actionable.
		path = relRegistryPath
	}

	fallbackCtx := map[string]any{
		"path":  path,
		"cause": regErr.Error(),
		"code":  code, // the originally-requested code, preserved for the audit ctx
	}
	msg := renderTemplate(tmpl, failCode, correlationID, fallbackCtx)

	e := &E{
		Code:          failCode,
		CorrelationID: correlationID,
		Module:        "foundation.errors",
		Msg:           msg,
		Ctx:           fallbackCtx,
		Time:          t,
		Wrapped:       cause,
	}
	// Log at the registry entry's declared fatal severity — a whole-registry
	// outage must not be recorded as a mere "error" (the pre-fix downgrade).
	logEntry(e.toEntry("fatal"))
	return e
}

func (e *E) toEntry(level string) Entry {
	return Entry{
		Ts:            e.Time.UTC().Format(time.RFC3339Nano),
		Level:         level,
		Code:          e.Code,
		CorrelationID: e.CorrelationID,
		Module:        e.Module,
		Msg:           e.Msg,
		Ctx:           e.Ctx,
	}
}

// renderTemplate substitutes {key} placeholders in tmpl. "code" and
// "correlationId" resolve to the code/correlationID parameters unless
// ctx explicitly overrides them (constructUnregistered relies on this to
// report the originally-requested code under the {code} placeholder of
// the MET-F003 template, distinct from the MET-F003 code itself). Any
// other {key} not present in ctx is left as a visible literal
// "{key}" — a missing template value degrades to visible text, never a
// silent drop or a panic.
func renderTemplate(tmpl, code, correlationID string, ctx map[string]any) string {
	resolve := func(key string) string {
		if v, ok := ctx[key]; ok {
			return fmt.Sprint(v)
		}
		switch key {
		case "code":
			return code
		case "correlationId":
			return correlationID
		}
		return "{" + key + "}"
	}

	var sb strings.Builder
	i := 0
	for i < len(tmpl) {
		if tmpl[i] == '{' {
			if j := strings.IndexByte(tmpl[i:], '}'); j >= 0 {
				key := tmpl[i+1 : i+j]
				sb.WriteString(resolve(key))
				i += j + 1
				continue
			}
		}
		sb.WriteByte(tmpl[i])
		i++
	}
	return sb.String()
}
