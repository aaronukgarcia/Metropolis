// Package num is the single source of truth for GR#16 numeric safety:
// saturating int64 arithmetic and the float64→int64 / float64-finiteness
// choke points every engine module routes its quantity arithmetic through,
// plus — since FEAT-135 / feat.securehelpers — the *reject* form of those
// same choke points: coercion helpers that return a registry-sourced error
// on NaN/±Inf/overflow/out-of-range instead of silently clamping.
//
// # GR#16 — the numeric-safety standard
//
// Before this package, every engine module re-derived safe arithmetic
// ad-hoc (satAddMoney / safeMul / mulDiv in engine.finance,
// saturatingInt64FromFloat in engine.logistics, and near-identical
// satAdd/satSub/safeMul/clampInt64FromFloat copies in engine.build,
// engine.households, and engine.attract), and every Destructive round
// found a different overflow site as a result. The standard that emerged:
//
//   - every int64 quantity routes through SatAdd / SatSub / SafeMul —
//     never a raw + / - / * that can wrap negative, invent, or destroy
//     units;
//   - every int64↔float64 conversion routes through ClampInt64FromFloat —
//     never a bare int64(float64(...)) that wraps 2^63 (==
//     float64(math.MaxInt64)) into a negative value on amd64;
//   - every float64 arithmetic result is IsFinite-guarded (or checked with
//     GuardFinite) — never left able to leak +Inf/NaN from a finite input;
//   - every public mutator validates like its constructor, and every
//     public query validates like its mutator (defence-in-depth on the
//     numeric inputs, exactly as engine.consumption re-validates sources
//     at Solve time).
//
// # The reject form vs the saturating form (FEAT-135 coexistence contract)
//
// The saturating helpers (SatAdd / SatSub / SafeMul / ClampInt64FromFloat)
// are the *conserved-arithmetic* path: internal accumulation where
// saturation is the documented invariant (engine.finance, engine.build).
// They clamp — NaN→0, +Inf→MaxInt64 — because a conserved ledger must never
// invent or destroy units, and saturation is the invariant there.
//
// The rejecting helpers (SafeInt64 / BoundedFloat / SafeInt64FromAny /
// SafeFloat64FromAny, plus the string-length boundary SanitizeEventID and
// the bounded-history primitive BoundedLedger — SEC-203's BoundedString +
// BoundedLedger pair) are the *boundary-validation* path: module entry
// points, command handlers, and data loaders, where a bad input must be
// rejected with a registry-sourced error (GR#7), never silently clamped.
// This is SEC-093's exact shape — an ordered range check (level < 0 ||
// level > 1) is false for NaN, so a non-finite value slipped past it; the
// rejecting form checks non-finiteness FIRST and returns a distinct
// non-finite code (MET-F800), so NaN/±Inf can never ride an ordered check
// through a boundary. SEC-080 (the XP counter that wrapped int64 negative)
// and SEC-066 (the live-pointer leak) generalise the same way: reject,
// never wrap or leak.
//
// The rule in one line: never wrap; never leak +Inf/NaN from a finite
// input; reject — never silently clamp — at the boundary; never return a
// live pointer.
//
// Golden Rules honoured: #7 (every coercion failure returns a
// registry-sourced MET- code, registered in data/errors.json under the
// F800-F899 sub-range reserved for foundation.num), #15 (bounds are
// caller-supplied parameters, never hardcoded), #16 (type-safe coercion at
// stored-value boundaries), #20 (the package is a registered foundation
// module with an inbound contract), #21 (every helper is a pure,
// deterministic function of its inputs).
//
// Module key: foundation.num (see code.json; GUID 74ff5b3b-bfc6-4376-b461-267f4467a39f)
package num
