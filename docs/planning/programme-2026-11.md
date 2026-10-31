# Programme 2026-11: fix the mess, in order

Author: lead (Bev), 2026-10-31, at Aaron's direction: "look at the whole BOW, so much is broken, take a holistic view and plan a programme to fix this mess." Source: the full open board at 2026-10-31 23:50 (706 open: 1 P0, 77 P1, 363 P2, 259 P3, 5 P4, 1 P5) read end to end, plus tonight's playtest evidence (debug.json digest, screenshots, the consolidator blind review).

## The diagnosis

The board is big but the breakage is NOT uniform. It clusters into eight workstreams, and three facts organise everything:

1. **The game now works well enough that Aaron plays it — and playing it finds real defects faster than any audit.** Tonight alone: the lineage city-loss, the Fix All nuke-fleet double-order, the overlay pile-up, the consolidator invariant violation live on screen. The programme must serve the dogfood loop first (northstar: watchable → dogfood → convergence → depth → balance).
2. **~40% of open items are round-generated test-debt** (unpinned mutants, vacuous fixtures, stale comments — the BUG-8xx/9xx traffic swarm and inc2 follow-ups). Real, but none of it hurts the player today. It must stop occupying headline space: batch it, don't interleave it.
3. **~20 items are decisions only Aaron can make** (balance numbers, cadence calls, wire-or-park). They are cheap for him in ONE sitting with a prepared workbook and expensive forever as scattered blockers.

## The workstreams, in execution order

### WS-A — A session you can trust (player-data integrity) [NEW EPIC]
The player must never silently lose a city, duplicate a tile, or mint money.
- BUG-1156 lineage-pointer loss (P1, hit live tonight; fix shape on the item: never overwrite the pointer while saves exist elsewhere; offer adoption).
- BUG-1149 + BUG-1150 consolidator Undo money-mint and tile-overlap (P1 pair, one lane — same code).
- BUG-1135 TUI save/load dropped ops; BUG-461 journal re-persist; BUG-672/673/678/765 IndexedDB mirror has no restore path; BUG-576 corrupt-save mislabel; BUG-485/495 seed-check gaps; BUG-1146 hostile social saves; BUG-774 game-over policy (needs a small Aaron ruling).
- Exit test: kill the browser/process at any point, reload, and the same city returns byte-stable; no interleaving of player actions with Undo can create money or stack tiles.

### WS-B — A city you can read (the UI overhaul + the Fix engine) [EPIC EXISTS: FEAT-2326609843]
Tonight's playtest feedback, already packed as UX-1..5, PLUS the Fix/advisor correctness cluster that the pack's words sit on top of:
- BUG-1164 pipeline-aware Fix sizing (counts under-construction capacity; kills the 28-nukes-twice class) — FIRST, it burns money today.
- BUG-1165 "cheapest picked" honesty; BUG-647 advisor compares incomparable units; BUG-425 surplus renders in alarm red (filed in AUGUST — same defect Aaron re-found tonight); BUG-1077 Fix queues units for gated 0-demand rows; FEAT-2326609735 optimal-provider refinement; BUG-596/620/727 wrong placement notices; BUG-465 zoom jump; BUG-545 staleness banner; BUG-688 stale alerts; BUG-1157/1162/1160 (lanes already running/filed).
- FOLD: FEAT-1972079873 (the 2026-08-26 webconsole UX epic) is a duplicate of FEAT-2326609843 — merge its scope list in and close it.
- Exit test: the pack's "Done means" + a fresh-eyes playtest by Aaron where every number on screen has a direction and a unit he doesn't have to ask about.

### WS-C — Consolidator stabilisation [NEW EPIC]
The single buggiest feature on the board (6 confirmed defects from the blind review + the live MET-V871 + a ~15-item layout/inc3-4 backlog).
- Now: BUG-1155 invariant violation (live tonight), BUG-1151 wedge (lane done, round pending), BUG-1152 capacity-dip, BUG-1154 dead field.
- Then the layout cluster (BUG-763 capex not booked, 794, 808, 812, 813, 817, 818, 754, 790) as one themed wave in file-locality order.
- Aaron calls folded into WS-D: BUG-1153 cadence, 796/797/773/793/805 economics.
- Exit test: a 1M-city 2000-tick soak with consolidator+layout ON: zero invariant violations, Undo safe, books balanced against the ledger.

### WS-D — The balance workbook (ONE Aaron sitting) [NEW EPIC]
Every number-judgement on the board, in one prepared sheet, one pass, instead of 20 scattered blockers: the existing 99-item ruling sheet + FEAT-9995385582 (the real 4.5x structural deficit tonight's debug exposed) + BUG-539 XP grind, BUG-1090 level-reward %, BUG-785/789/792 airport-tax monoculture, BUG-751 productivity one-way, BUG-753 capex ceiling, BUG-706 grid tariffs, BUG-741 free dispensation, FEAT-2326609786 power retune, BUG-876, BUG-793, plus the social/fiscal/coastal flags from the wiring wave.
- I prepare the workbook (each row: current number, measured effect, proposed value, blast radius); Aaron rules row-by-row; then ONE tuning tranche lands under the balance-number regime with directional tests.
- Exit: the deficit story of a stock level-15 city is a design, not an accident.

### WS-E — CI and guards you can believe [NEW EPIC]
- BUG-799 reclaim TOCTOU (its flake red tonight's push — winners=0 mode); BUG-859 compose at the 20-minute cliff (pair with FEAT-2326609826 compose.go split); the flake family BUG-1117/908/528/801/492/537/1105; BUG-1098 index race.
- Guard chain: BUG-1148 (r3 in flight), BUG-1163, BUG-1166, BUG-729 exemption-order hole, BUG-549/553/351 author/secret-guard residuals, BUG-387 recency self-block (costs us a covering verdict per commit — mechanise it).
- Hygiene: BUG-811/1120/1115 worktree+test-litter prune (needs Aaron's deletion-gate nod once), BUG-836-840 junk-titled items (re-title or close), BUG-1103 history purge (heavy op — schedule deliberately, Aaron call).
- Exit: ten consecutive green pushes with zero reruns and zero guard false-positive retries.

### WS-F — Wire it or park it [NEW EPIC]
The dominant defect class made measurable: BUG-1125 (27 of 66 engine packages unwired), BUG-1126 (9 of 14 UI screens unwired), FEAT-211 F-key registry, FEAT-192 dormant-module ICDs, BUG-366/556/522 (education produces NOTHING today), FEAT-2326609746 zone tax unwired.
- I produce a one-page wire/park/delete triage table per package/screen; Aaron ticks a column; lanes wire the "wire" set (census first — it owns F6 and the passport ships into it).
- Exit: zero packages in the repo that nothing reaches; CLAUDE.md's screen list matches reality.

### WS-G — Scale northstar (continues, already an epic: FEAT-2326609824 + FEAT-2326609764)
Partition inc3a is landing tonight; then the inc2 follow-up set (BUG-1064-1094, triaged: correctness ones first — 1064/1065/1068/1091/1137 are stale-aggregate bugs, the rest pins), then the FLIP (gated on the Aaron sheet), then per-domain convergence (BUG-523 TS-vs-Go money model is the big known divergence), BUG-666 chain remainder (FEAT-2326609838/839), citizens Home/Workplace (FEAT-2326609845 — also unblocks passport lists and real employment).

### WS-H — Test-debt bulk (background filler, never headline)
The ~80 P3 pin/vacuous/stale-comment items (traffic inc0-10 swarm, inc9b family, units-lint findings). Batched by FILE LOCALITY per the FEAT-085 machinery, dispatched only when WS-A..F have no ready lane. Never interleaved with feature lanes in the same files.

## Sequencing and capacity

- Lanes run 3-5 concurrent (the proven tonight shape): WS-A and WS-B first claim, WS-C/WS-E fill, WS-H is filler-only.
- Rounds stay mandatory for every code-bearing landing (tonight: 4 of 7 first-round verdicts were REJECTs with real findings — the gate pays).
- WS-D and WS-F each need ONE Aaron sitting with a prepared artefact; nothing else blocks on him.
- The 5 in-flight landings (card, inc3a, 1142, 1144-done, passport) finish under the current protocol before new WS waves dispatch.

## What only Aaron can decide (the short list)
1. WS-D workbook sitting: when. (I prepare it regardless; ~1 evening of his time.)
2. WS-F wire/park/delete table: tick the column per module/screen.
3. BUG-774 game-over policy; BUG-1153 consolidator cadence; UI pack Q1-Q4 (interims hold).
4. The deletion-gate nod for worktree/test-litter pruning, and whether/when to run the BUG-1103 history purge.
5. Priority override if he disagrees with WS order — the board default is A, B, C/E parallel, D sitting, F sitting, G continuous, H filler.
