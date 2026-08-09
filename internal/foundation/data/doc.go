// Package data provides typed, validated loaders for the §24 config data
// file set — the M2 balance surface every coefficient-consuming engine
// module reads instead of hardcoding numbers in Go: consumption.json,
// modes.json, buildings.json, unlock_trees.json, naming_corpus.json,
// seasonal.json, external_world.json, and policies.json. It is the
// GR#15 ("Validators Derive From Data") reference: any validator
// elsewhere in the codebase that needs an "expected" count or value must
// read it from one of these loaded structs, never from a constant
// hardcoded in the validator's own source. This package's own struct
// fields are the only sanctioned source for such expectations — GR#15
// compliance in *other* modules is those modules' own acceptance
// criteria to prove, not this package's job to police.
//
// errors.json (also named in §24) is explicitly OUT of scope here — it
// is already owned and loaded by foundation.errors (MOD-002); this
// package never re-implements that loader, and [LoadAll] does not
// surface it (see the foundation.data.md acceptance doc's Out of scope
// section — a deliberate decision recorded, not a silent omission).
//
// # Loading
//
// Each file has a dedicated typed loader (LoadConsumption, LoadModes,
// LoadBuildings, LoadUnlockTrees, LoadNamingCorpus, LoadSeasonal,
// LoadExternalWorld, LoadPolicies) built on the generic [Load] helper,
// which reads, JSON-decodes, and schema-validates one file via its
// [Validator]. [LoadAll] loads all eight into one aggregate [Config] for
// callers (like engine.core's boot sequence) that want everything up
// front.
//
// # Path resolution
//
// File paths resolve the same way foundation.errors resolves
// data/errors.json (see errs/registry.go): $METROPOLIS_DATA_DIR if set,
// else walking upward from the running executable's directory looking
// for a data/ directory, else walking upward from the current working
// directory — the search that makes `go test` work regardless of the
// per-package working directory Go gives it.
//
// # Errors
//
// Every failure (missing file, malformed JSON, missing version field,
// schema violation) is constructed via errs.New/errs.Wrap against
// placeholder MET-F6xx codes (F600-F699 — NOT yet entered in
// data/errors.json; see the package's registry-wiring note in the
// foundation.data delivery report). No panics on any load path.
//
// # Hot reload
//
// [Store.Reload] re-reads and re-validates a file and, only on success,
// atomically swaps it into the live [Store] via a mutex-guarded pointer
// swap — never a partial or in-place field mutation, so a concurrent
// reader never observes a torn struct (AC-4/AC-13) and a failed reload
// leaves the previously-loaded config fully intact (AC-11). Reload is
// gated by a debug flag the caller controls (feat.debugmode's pattern);
// outside debug mode it returns a registry-sourced "debug mode
// required" error and never touches the live config (AC-5). This
// package does not run a file-watching goroutine — polling or
// filesystem-event watching is the debug console's job (a future
// feat.debugmode/ui.screen.debug concern); Reload is an explicit,
// caller-triggered operation only.
//
// Module key: foundation.data (see code.json)
// Spec ref:   §24; GR#15; M0-ENG §3 (debug as a runtime feature switch)
package data
