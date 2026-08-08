# Metropolis Dev-Team Process v1.1

**2026-08-08 · directed by Aaron after Sprint 0 wave 1 · supersedes the wave-1 "lead reviews everything directly" flow**

## Roles

| Role | Model | Mandate |
|------|-------|---------|
| **Lead (Bill, Fable 5)** | — | Architecture, briefs, dispatch, FINAL review of test-clean work only, commits, BOW state. Does not burn tokens on basic errors — those never reach him. |
| **Jnr developers** (per item) | Sonnet | Build to the brief. Fix their own test failures — every bounce goes back to the SAME junior with its context intact. |
| **Tester** (one, persistent) | Sonnet | Verification ONLY: runs the build/vet/test/-race/gofmt suite plus the item brief's specific checks, confirms deliverables match the brief. Output is PASS or FAIL with exact evidence. **Never edits a file, never suggests fixes** — a FAIL hands straight back to the junior. |
| **Documentation** (one, persistent) | Sonnet | Owns documentation consistency: house style, spec refs, code.json keys in package headers, `docs/design/` index + freeze-review packet. **May edit .md files only** — doc problems inside .go files go back to the junior as a FAIL item. |

## Flow per BOW item

```
Bill brief → Jnr builds → Tester verdict
                              │ FAIL → same Jnr fixes → Tester again (loop)
                              │ PASS ↓
                     Documentation pass (.md only)
                              ↓
                     Bill final review → commit [mkey] → BOW ref + done
```

- The Tester's FAIL report is forwarded verbatim to the junior; the junior's fix returns to the Tester, not to Bill.
- Bill sees an item only when it is test-clean and doc-clean; his review is architectural (contracts, determinism, GR compliance), not mechanical.
- Nothing is committed that has not passed the Tester; nothing is `done` in the BOW without Bill's final review and a commit ref.
- The Tester's suite is derived from the item's brief (GR#15: expected results come from the brief/spec, not invented) — baseline for Go items: `go build ./...`, `go vet ./...`, `go test <pkg> -race -count=1`, `gofmt -l .` empty, deliverables-vs-brief checklist, forbidden-touch check (`git status` shows only allowed paths).

## Rationale (wave-1 lessons)

- Wave 1 worked but the lead personally caught a junior's basic robustness bug (BOM handling in the plan guard) — mechanical catches belong to a cheaper, dedicated verifier.
- Juniors' self-reported "verification output" is not trusted evidence; an independent runner is (the Tester re-runs everything from scratch).
- A single documentation owner prevents N juniors drifting into N house styles across `docs/design/`.
