// Package stub is H-STUB: StubEngine, a full internal/protocol
// implementation with canned behaviour — the handcrafted Folkestone-64
// fixture world (fixture.go), scripted/recorded delta streams
// (viewport.go), fake-tick speed controls, and chaos knobs (chaos.go).
// The UI is built against H-STUB from week one, every screen and the
// whole key grammar, before one line of real simulation exists
// (M0-ENG §2.1).
//
// StubEngine is a PERMANENT FIXTURE: per M0-ENG §2.1's harness strategy
// ("permanent fixtures... they never get deleted; they become the test
// estate"), this package is not scaffolding retired once a real engine
// module exists. It stays in the tree for the life of the project as the
// stub side of every module's stub/real registry flip (M0-ENG §2,
// sprint-plan-v1.md's GR#20) and as the fixture H-REPLAY (MOD-013, a
// later item) records its golden command/delta streams from.
//
// StubEngine computes nothing — every Command it accepts produces
// canned/scripted output, never a real simulation step (see engine.go's
// StubEngine doc).
//
// # Files
//
//   - fixture.go      — GenerateFolkestone64: the handcrafted, seed-pure
//     64x64 fixture world (terrain bands, named roads/buildings).
//   - viewport.go     — the "f1.viewport" patch schema (v1) and the
//     scripted delta stream Folkestone-64 is served through.
//   - subscriptions.go — per-subscription server-side state (Seq
//     counter, scripted-stream cursor).
//   - chaos.go        — the delayed-delta and burst-delta chaos knobs.
//   - codes.go        — placeholder registry error codes (AC-9/AC-10;
//     see that file's doc for what "registry-sourced" means today).
//   - engine.go       — StubEngine itself: construction, the Run loop,
//     command dispatch, delta emission.
//
// Module key: harness.stub (see code.json; GUID c254728b-4a1c-490f-a1a7-42e08f8605ff)
// Spec ref:   M0-ENG §2.1; BOW MOD-008; acceptance criteria
// docs/planning/acceptance/harness.stub.md
package stub
