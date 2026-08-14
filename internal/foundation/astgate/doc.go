// Package astgate is the mechanical, AST-derived copy-guard gate BUG-024
// asked for: it replaces the hand-swept SEC-020 enumeration (nine manual
// passes by four different agents, all sharing the same blind spot — see
// below) with a standing, CI-run check.
//
// Module key: tool.astgate (see code.json; GUID 2e729a3d-8bd4-499d-9051-61af3ac23b13)
// Spec ref:   BUG-024; GR#15 (validators derive from data/the AST itself,
//
//	never a hardcoded list); GR#20/#21 posture (mechanical,
//	CI-enforced gates over remembered procedure); BUG-008's
//	internal/foundation/errs/source_scan_test.go, the sibling
//	AST-based gate this package mirrors in shape and in the
//	practice of declaring its own blind spots honestly rather
//	than silently having them.
//
// # What this gate checks
//
// It walks the AST of cmd/ and internal/ (excluding _test.go files, same
// rationale as scanSourceCodes: fixture/test types are not production
// copy hazards) and, per Go package (one package = one directory, matched
// syntactically, no go/types — see "Known blind spots" below):
//
//  1. Finds every CANDIDATE TYPE: a struct with at least one sync.Mutex/
//     sync.RWMutex field held BY VALUE (not by pointer — a pointer field
//     aliases the same mutex across copies, a different, non-SEC-020-
//     shaped hazard, out of scope per BUG-024's own definition of done)
//     AND at least one field that is a slice, map, channel, or pointer
//     (the aliasable-reference-field set). A field typed via a same-
//     package `type X = sync.Mutex`/`type X = sync.RWMutex` alias is
//     resolved and treated as a mutex value field the same as the
//     unaliased spelling (collectMutexAliases) — a locally aliased
//     mutex is the identical value-copy hazard, just spelled
//     differently; cross-package alias resolution is not attempted
//     (see "Known blind spots").
//
//  2. Finds every REACHABLE FUNCTION for each candidate type T — split
//     into two sets that MUST both be populated, because conflating them
//     (or forgetting the second) is the exact incident this item exists
//     to close:
//
//     - receiver methods: func (x T) M(...) / func (x *T) M(...)
//     - functions (with or WITHOUT a receiver) taking T or *T as a
//     PARAMETER, by position or name, including T wrapped in a slice
//     ([]T/[]*T) or variadic (...T/...*T) — each slice element or
//     variadic argument is its own fresh value-copy of T at the call
//     site, the literal hazard this gate exists to catch — e.g.
//     errs.SetSink(l *Logger). This is the shape that escaped nine
//     hand-sweeps (SEC-031/BUG-024's dispatch comment): every prior
//     sweep only grepped `func (x *T)` and never looked at ordinary
//     function signatures at all. Receiver-method status and
//     parameter-taking status are independent, not mutually exclusive:
//     a method can ALSO take a second, independent candidate-typed
//     parameter (func (g *Guarded) Merge(other *Guarded)), and a
//     method whose OWN receiver type is not a candidate must still be
//     checked for candidate-typed parameters (func (r *Attacher)
//     Attach(g *Guarded)) — both are enumerated via the parameter path
//     regardless of fd.Recv. Function literals (closures) are scanned
//     too, at any nesting depth — assigned to a var, returned from
//     another closure, a struct-literal field value, inside a slice
//     literal, or passed inline as an argument — via scanFuncLits
//     (BUG-138), which walks every *ast.FuncLit in the file and runs
//     the identical candidate-parameter check (appendParamFuncs) used
//     for an ordinary *ast.FuncDecl. A closure has no receiver, so
//     only its parameter list is relevant here, not the receiver-
//     method path above. A THIRD path (SEC-049): every method declared
//     on a type W that WRAPS a candidate type C via a struct field —
//     embedded, named-by-value, or a pointer field, e.g. WorldAPI's
//     `w *World` — is reachable for C through that field-then-method
//     access chain (a.w.M(...)), even though W is not itself a
//     candidate type and never takes C by parameter. This is what made
//     WorldAPI's own exported methods, which lock a.w.mu directly,
//     invisible before this fix (findFieldReachableFuncs). It is one
//     field-access hop deep only — see "Known blind spots" below for
//     the residual, un-transitively-resolved wrapper-of-a-wrapper case.
//
//  3. For each reachable function, determines whether it GUARDS: its body
//     contains a call recognisable as an identity/copy check — a call to
//     a method named checkNotCopied reached on the receiver or the
//     matched parameter (the concrete pattern already in this codebase;
//     see checkNotCopied's doc comments in internal/foundation/errs/log.go
//     and internal/protocol/transport.go). An unguarded reachable function
//     for a candidate type is a hard violation.
//
//  4. For each GUARDED function, additionally looks at its rejection
//     path (recognised only in the `if !x.checkNotCopied() { ... }` /
//     `if cond && !x.checkNotCopied() { ... }` shape actually used
//     throughout this codebase — see "Known blind spots") for a call
//     that reaches a package-level variable whose name or constructing
//     call looks ring/buffer-shaped (a heuristic proxy for "bounded
//     shared resource", see FindFloodRisks's doc comment) AND that is
//     also referenced from somewhere else in the package outside any
//     rejection path. This is advisory only (AC-7) — it never fails the
//     gate on its own, and false positives are expected and accepted.
//
// # Known blind spots (declared, per BUG-008's precedent, rather than
// silently unhandled — a gate with unadvertised false negatives is worse
// than no gate)
//
//   - NO TYPE-CHECKING. Types are matched by their bare source-level
//     name within one directory (one Go package). A parameter typed as
//     a QUALIFIED type from another package (e.g. `func F(l *errs.Logger)`
//     written outside package errs) is invisible to this gate — it is
//     matched only where the type is used unqualified, i.e. from within
//     its own defining package. Cross-package parameter functions taking
//     a guarded type by its qualified name are a real, undetected gap.
//   - MUTEX-ALIAS RESOLUTION IS SAME-PACKAGE ONLY. `type X = sync.Mutex`
//     declared in the SAME package as the struct using it is resolved
//     (collectMutexAliases); an alias declared in one package and used
//     in another (`import "pkgy"; type f struct{ mu pkgy.Mu; ... }`
//     where pkgy.Mu = sync.Mutex) is invisible for the same reason
//     qualified cross-package types are — this gate never resolves
//     identifiers across package boundaries. A defined (non-alias) type
//     `type X sync.Mutex` is correctly NOT treated as a mutex value: Go
//     does not carry over sync.Mutex's method set to a defined type, so
//     a field of that type would not actually behave as a lockable
//     mutex, and is out of scope on those grounds, not merely undetected.
//   - GUARD-CALL RECOGNITION is syntactic and name-based: only a call
//     literally spelled `<value>.checkNotCopied(...)` is recognised. A
//     guard reached indirectly through a one-level-removed helper
//     function, a guard implemented via a naming convention other than
//     checkNotCopied, or a guard on an interface-typed parameter whose
//     concrete type is only known at runtime, are all invisible and will
//     be reported unguarded (a false positive on the SAFE side) or,
//     worse, could theoretically be miscounted guarded if a coincidental
//     `checkNotCopied` call existed on an unrelated value of the same
//     name — not observed in this codebase today, logged as a permanent
//     shape risk rather than something this gate resolves.
//   - GUARD-CALL PRESENCE, not guard-call EFFECT. AC-4 only checks that
//     the call exists somewhere in the function body reachable from the
//     matched value — it does not verify the function actually branches
//     on the result (e.g. a `_ = x.checkNotCopied()` that is never
//     acted on would be misreported as guarded). Real code in this
//     tree always branches on it; a deliberately decorative call would
//     defeat this heuristic.
//   - REJECTION-PATH ANALYSIS (AC-6/AC-7) is single-level and syntactic:
//     only statements written directly inside the recognised
//     `if !x.checkNotCopied() { ... }`-shaped block are inspected. A
//     rejection path that delegates to a helper function one level
//     removed (as internal/foundation/errs/log.go's real
//     rejectCopiedLog does) is NOT walked into — this is a real, known
//     gap in the flooding-risk flag specifically (not in AC-4's hard
//     violation check, which has no such limitation), logged as
//     ASM-119 below.
//   - The "fixed-capacity shared resource" test for AC-6 is a name-based
//     heuristic (the touched package-level variable's declared type name,
//     or the name of the call that constructs it, contains "ring" or
//     "buffer", case-insensitive) — not a structural proof of bounded
//     capacity. It will both miss non-ring-named bounded resources and
//     flag unrelated ring/buffer-named state that poses no real risk;
//     AC-7 explicitly accepts this trade-off for an advisory-only check.
//   - MUTEX-ALIAS RESOLUTION IS SINGLE-HOP ONLY. collectMutexAliases
//     resolves a same-package `type X = sync.Mutex`/`type X = sync.RWMutex`
//     alias by matching ts.Type as exactly that selector expression; it
//     does not follow a chain of local aliases. `type A = sync.Mutex;
//     type B = A` leaves B unresolved — a struct field typed B is
//     invisible to findCandidateTypes, the same class of gap as the
//     cross-package alias case above, just one hop shorter.
//   - MAP-TYPED CANDIDATE VALUES ARE INVISIBLE. baseTypeName's recursive
//     unwrapping has cases for pointer (*T), slice/array ([]T/[N]T), and
//     variadic (...T) shapes, but none for *ast.MapType — so a function
//     parameter or struct field typed map[K]T or map[K]*T, where T is a
//     candidate mutex-hazard type, is never enumerated as reachable.
//     Ranging over map[K]T copies each T value, mutex included, the same
//     SEC-020 shape this gate exists to catch; it is simply not walked
//     into today.
//   - THE BASELINE-RATCHET ALLOWLIST CANNOT DISTINGUISH A SAME-COMMIT
//     SELF-APPROVAL FROM A LEGITIMATE PRE-EXISTING FINDING. The ratchet
//     has no diff-against-base-branch plumbing — it only ever sees the
//     tree it is run against — so a fabricated accepted-findings.json
//     entry added in the SAME commit/diff as its matching unguarded
//     (violating) code passes the ratchet exactly as cleanly as a
//     genuinely pre-existing accepted finding would; nothing mechanical
//     distinguishes the two by matching key alone. This is a structural
//     property of any allowlist-based ratchet lacking diff-against-base
//     tooling, not something specific to this implementation (confirmed
//     by BUG-024/ASM-204's Destructive round). Judged an acceptable
//     residual, not a defect: coverage comes from branch protection,
//     mandatory PR review, and GR#23's own per-commit Destructive-verdict
//     requirement, not from the ratchet mechanism itself.
//   - FIELD-ACCESS CHAINS (SEC-049) ARE ONE HOP DEEP AND SAME-PACKAGE
//     ONLY. findFieldReachableFuncs resolves W.field.Method(...) where
//     field's type names a candidate type directly. It does NOT resolve
//     transitively through a chain of wrappers: if X holds a *WorldAPI
//     field and WorldAPI holds a *World field, X's own methods are not
//     walked into a 3-segment X-to-World chain — only WorldAPI's own
//     methods are enumerated for World. Nor does it resolve a field
//     whose type is a QUALIFIED, other-package name (the same no-type-
//     checking, same-directory-only scope every other check in this
//     gate already has). A field reached via a slice/map/chan of a
//     candidate type (e.g. `ws []*World`) is DELIBERATELY, EXPLICITLY
//     rejected by collectFieldWraps' isContainerFieldType helper (SEC-049
//     round 2, Destructive reattack: baseTypeName's *ast.ArrayType case —
//     added for the unrelated slice-parameter fix — would otherwise
//     silently unwrap such a field down to its element type and match it
//     exactly like a direct pointer field, even though a container can
//     never satisfy isGuarded's literal method-call shape, making any
//     such finding permanently, uncorrectably unguardable). This is the
//     same different-hazard-shape reasoning as the map-typed-candidate-
//     values note above (a container of independently-copyable values,
//     not one single field-then-method access chain), now enforced in
//     code rather than merely assumed. An unnamed receiver on the
//     wrapping type is also out of scope by construction — its fields
//     are syntactically unreachable in the method body, so there is no
//     access chain to be missing a guard on.
//
// See gate_test.go for the fixture-driven proof that every one of the
// above actually fires (and, for the ones that must never miss, the
// negative-control proof that a deliberately unguarded fixture is
// actually caught).
package astgate
