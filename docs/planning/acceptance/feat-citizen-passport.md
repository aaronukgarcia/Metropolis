# Requirement pack: the citizen dummy-passport (FEAT-2326609842)

Standard: `docs/planning/acceptance/TEMPLATE-requirement-pack.md`. Author: lead BA pass, 2026-10-31, from Aaron's words: "plan a dummy-passport (photo cartoon esk ID) with a guid number, listing each persons name address, job, etc." Companion to the property card (`feat-property-card.md`, FEAT-2326609841) and shares its auto-approved generated-art ruling.

## 1. Context and goal

- **BOW:** FEAT-2326609842 (P2). mkey for commits: `[FEAT-2326609842]`.
- **Northstar stage:** dogfood depth — Option B's whole premise is persistent INDIVIDUAL citizens ("no culls ever, up to 100M"); the passport is the first surface where the player meets one.
- **The dual-engine fact that shapes everything (verified 2026-10-31):** individual citizens exist ONLY on the Go engine. `ColdRecord` (internal/engine/citizens/coldshard.go:354) already carries: `ID` (uint64), `BirthMonth` (age), `Sex`, `Household`, `Partner`, `ChildCount`, `Home` (CellRef — a real address), `District`, `Workplace`, `School`, `Personality`, `Attainment`, `Stage`, `Schooling`, `HealthBand`, `Access`, `Wealth`, `EmploymentState`, `Sector`, and five satisfaction axes. The TS webconsole sim is aggregate (rates over headcounts) and has NO individuals — a passport cannot be faked there without inventing a second population model (GR#3 violation; refused).
- **Two consequences:**
  1. The passport's data path is a **protocol citizen query** against the composed Go engine (a request/response command or a parameterised view), surfaced in the TUI and/or the live-engine webconsole (`LiveEngineBadge`/wsserver path). The local TS sim shows no passports — honestly absent, not mocked.
  2. Citizens have **no name**. The passport shows a name generated deterministically from the ID (seeded first/surname tables — presentational derivation, no engine field, no save change). The same seed drives the cartoon avatar.
- **What the player sees:** a passport-booklet-styled card: cartoon portrait, formatted GUID (e.g. `MET-0000-0042-1337` from the uint64), generated name, date of birth (from BirthMonth + the game calendar), address ("<coordLabel of Home cell>, District N"), occupation (Sector + EmploymentState; Workplace/School refs resolved to building names where known), household line (partner yes/no, N children), health band, wealth band, and a satisfaction strip (five axes as small bars). Closable like the property card.
- **Entry points (ruled below, Q1):** clicking a residential building's card → "Residents…" list → passport; the TUI census screen (which would finally give census its purpose and bears on the parked F6 owner ruling); a debug/census "random citizen" draw.

## 2. Acceptance criteria

Surface key: **Query** = the new protocol request `KindQueryCitizen` (or a parameterised view — dev proposes, lead decides at dispatch) answered by the composed engine from the REAL CitizensAPI; **Card** = the passport component (TUI panel and/or webconsole live-engine component per Q1).

- **AC-1 (the query reaches the real engine).** Given a composed engine and a live citizen ID, when the query is issued through the protocol seam (never a direct API call from the surface), then the response carries exactly the ColdRecord-derived fields above; an unknown/dead ID returns a registry-coded refusal, not zeros. *Mutation:* answer from a literal struct — the engine-reads-pinned test (services/population precedent) fails.
- **AC-2 (deterministic identity).** The generated name, formatted GUID and cartoon portrait are pure functions of the citizen ID (+ fixed tables): the same ID renders byte-identically twice and across save/load; two different IDs differ visibly. No clock, no RNG draw outside `det` seeding (GR#21). *Mutation:* seed the avatar from wall-clock — the repeat-render test fails.
- **AC-3 (address is the Home cell).** The address line formats the citizen's `Home` CellRef via the house coordLabel convention plus District; a citizen with no home (homeless per the social model) shows an honest "no fixed address", never a fabricated one. *Mutation:* hardcode a cell — the two-citizen test fails.
- **AC-4 (occupation honesty).** Employed shows Sector + resolved Workplace where the ref is non-zero; unemployed/student/retired show their EmploymentState and School where set; the card never shows a job for `Workplace == 0`. *Mutation:* default the sector label — the unemployed-citizen test fails.
- **AC-5 (age and dates).** Date of birth derives from `BirthMonth` and the game calendar (Jan Y1 epoch); age in years matches the engine's own ageing maths on the same record. *Mutation:* off-by-one the month-to-date conversion — the known-fixture test fails.
- **AC-6 (closable, live).** The card closes on Escape/close/click-off; while open on a running engine, a refreshed query (tick or manual refresh per surface idiom) reflects changes (a citizen who gains a job shows it) — stale data is marked stale, per the house subscription idiom. *Mutation:* freeze at open — the employment-change test fails.
- **AC-7 (privacy of the aggregate).** The query is bounded: one citizen per request, listing endpoints page at a stated cap (the residents-of-a-building list caps and pages; no query returns the whole population). Oversized/garbage IDs are refused at the codec with registry codes. *Mutation:* remove the cap — the bound test fails.
- **AC-8 (satisfaction strip).** The five satisfaction axes render as bars scaled from the record's int32 ranges, with the scaling constant shared with any existing wellbeing display convention — one source. *Mutation:* scale one axis differently — the equality test fails.

## 3. Non-goals

- No name/identity field added to the engine or saves (names stay presentational derivations).
- No TS-sim fake individuals; the local webconsole sim shows no passports.
- No citizen editing, no life-event triggering from the card (read-only).
- No photo realism — the cartoon avatar is the auto-approved generated art (FEAT-2326609841 ruling).
- Full census screens/browse-all-citizens UX beyond the ruled entry points (census screen scope is its own item once F6 is ruled).

## 4. Developer direction

- **Files (engine/protocol):** `internal/protocol/commands.go` (+payload), `internal/engine/core/commands.go`, `internal/engine/stub/engine.go`, `internal/engine/compose/` (a `citizen_query.go` answering from CitizensAPI — read-only, no hook), registry codes via `tools/plan/add-error.js` (claim-range feat.compositionroot; pre-announce the range in the brief — the G5490 collision lesson).
- **Files (surface, per Q1 ruling):** TUI: the census screen package already exists (parked); or webconsole: `src/components/CitizenPassport.tsx` + the live-engine query path (wsserver). Name generator + avatar generator in a leaf module (TDZ rule), seeded from ID via the house det conventions.
- **Constraints:** the wiring-port pre-push floor applies in full (astgate — note any new mutex-bearing type floods the gate and cross-products existing *Wiring fields; errs render gate — every new code's raise params match its template; golangci; `./internal/converge/...`; scoped packages). Worktree tests need METROPOLIS_DATA_DIR/ERRORS_PATH. GR#21 end to end; BUG-602 in render paths; copy guards per the coastal pattern if any new compose type carries a mutex (prefer a plain function — a read-only query needs no state and no mutex: AVOID creating a *Wiring type at all).
- **Round rules:** attacker ≠ author, own throwaway worktree, covering verdict after commit.

## 5. Tester watch-fors

1. **Built-but-not-wired:** issue the query against a REAL composed engine (headless or metroserve) and show a real citizen's passport fields changing month to month; a test over a hand-built record proves nothing.
2. Determinism: the same ID twice, and across a save/load, byte-identical card model.
3. The no-home, no-job, student, retired and dead-ID cases each render honestly (no fabricated fields).
4. Bounds: the residents list cap, garbage IDs through the real codec.
5. No wall-clock; ages from the game calendar only.
6. Dual-engine: state explicitly that the local TS sim path shows no passports and why.

## 6. Round guidance (GR#23)

Attack first: (1) the query as a scrape vector (pagination/cap bypass, whole-population enumeration cost at 1M+ citizens — the query must hit the indexed rowOf path, never a scan; tie to the FEAT-2326609830 oracle); (2) identity leakage between IDs (avatar/name collisions — birthday-bound the generator and state the collision odds honestly); (3) stale-as-live on the card; (4) dead/paged-out citizens (the cold-store paging path — a paged citizen must still answer); (5) codec hostility on the ID field; (6) determinism under -count=3.

## 7. Done means

From a running composed engine, the player opens a citizen's passport (via a ruled entry point), sees the cartoon portrait, formatted GUID, generated name, true age/address/occupation/household/health/wealth/satisfaction from the real record, watches it change as months pass, and every AC is mutation-proven, with FEAT-2326609842 carrying an independent ACCEPT and refs.

---

## Open questions for Aaron (interims stated; dispatchable for the engine+protocol half immediately)

1. **Surface first:** (a) TUI census screen (revives the parked package, and recommends settling the F6 owner ruling as "census takes F6"); (b) webconsole live-engine mode (the prettier card, but only works connected to the Go engine — the local sim honestly shows none); (c) both, TUI first. **Recommend (c)**: the protocol query is shared; TUI proves it; the webconsole card follows.
2. **Entry point:** building card → "Residents…" list (needs a residents-of-building index — cheap via Home CellRef), a census browse, or a "random citizen" button? Interim: residents list from the property card + a random-citizen draw on the census screen.
3. **Name generator flavour:** plain British name tables (Kent setting) or broader? Interim: deterministic British-style tables, ~2k first × 2k last names.
