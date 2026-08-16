---
description: Generate or inspect a secure-by-default Go module skeleton pre-wired with copyguards, registry error structures, and safe coercion
allowed-tools: Read, Edit, Bash
---

# /safe-module — bootstrap a new Go engine package with secure-by-default primitives

Use this skill whenever a new Go package or module is being created under `internal/` (complying with FEAT-135). It ensures the module starts with perfect compliance with security, concurrency, and error standards.

## Structure Requirements

The bootstrapped package MUST contain:

1. **Copy Guard Protection:**
   Incorporate a strict copy guard block to prevent internal state duplication (SEC-020/066):
   ```go
   import "sync"
   
   type MyModule struct {
       mu   sync.Mutex
       noCopy noCopy // From foundation/registry
       // fields...
   }
   
   type noCopy struct{}
   func (*noCopy) Lock()   {}
   func (*noCopy) Unlock() {}
   ```

2. **Safe Coercion & Coercion Helpers:**
   Mandate the use of `foundation/num` helpers for all inputs. Never use naked float conversions or unchecked boundary divisions.
   - For int64 bounds: use `num.SafeInt64()`
   - For NaN checks: use `num.SafeFloat64()`

3. **Registry-Sourced Errors:**
   Pre-wire an `errors.go` containing your package-level constant declarations linked to the centralized registry ranges (`MET-<layer><NNN>`).

4. **Immutable View Proscriptions:**
   All exported slice-returning accessors must return a deep defensive copy rather than an internal live pointer slice to prevent encapsulation leakage.
