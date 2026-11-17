# Overnight run checkpoint (2026-11-17 night)

The lead (Bev) rewrites this file at every landing and every dispatch burst.
If the session stalls (token limit, crash, compaction), a resumed lead reads
this file, the BOW comments on each item, `git log -10`, `gh run list
--branch main --limit 3` and `git worktree list`, and carries on. It never
redoes committed work and never trusts recall over those four sources.

## Aaron's order (22:15)

"do all with fan out and low cost agents which you must orchestrate for
quality, I want lots of progress made against all the outstanding bow items".
Checkpoints so a token stall waits and resumes; auto restart.

## Rules for the night

- Sonnet builds and small reviews, Haiku mechanical docs. Opus reviews only
  for money and data-loss items (meter, R2 store, reconnect).
- Land only on a recorded ACCEPT and a green CI run. CI red = stop landing,
  fix first.
- No spend: no R2, Cloudflare, Azure, AWS calls. pix: SAFE deploys; this
  session only asks SAFE. Load nothing big on pix until SAFE confirms the
  WSL memory raise.
- No deletes outside the project temp folder (the 22:19 deletion token is
  spent; new worktrees wait for Aaron).
- Every Go brief lists: astgate live-tree, the FULL touched packages,
  go vet ./..., scratch inside E:\git\Metropolis\temp only, commands run
  from the worktree root (never cd into a subfolder of the main checkout:
  the hooks resolve from the repo root).
- At most two heavy Go -race suites at once; about four code lanes.
- Plain `git add` then plain `git commit -m` in separate calls; no trailers.

## Landed and CI-green before the night started

BUG-1204 2431b40, BUG-1206 6381d4f, BUG-1197 2fa4531, BUG-1205 781e605.
pix: metroserve container up (FEAT-9995385600), SAFE owns deploys, port 8120.

## In flight (update as they report)

| Item | Stage | Where |
|---|---|---|
| BUG-1209 socket closed on unload | round r1 running | estate .claude/worktrees/agent-a98f4cc555eb3b196 |
| FEAT-9995385596 meter | r2 rework running (after r1 REJECT) | .claude/worktrees/agent-afac334e274decd2f |
| FEAT-9995385598 workers + staggered walks | build running (part 1 first) | .claude/worktrees/agent-a59928c4840009f1f |
| FEAT-9995385595 hibernation ACs | reviewed, READY FOR DEV, uncommitted doc | docs/planning/acceptance/feat-9995385595-hibernation.md |
| FEAT-9995385603 inc2b ICD | writing | docs/planning/acceptance/ |
| FEAT-9995385601 launcher ACs | writing | docs/planning/acceptance/ |

## Queue, in order (start as lanes free up)

1. Land BUG-1209 on ACCEPT; then meter r2 round (Opus); land the meter.
2. Sibling-doc fixes the hibernation review listed (waking doc AC-H1, AC-P2
   enums, AC-H5 dependency; meter doc AC-14/30/38) - one docs lane.
3. Commit the docs: workers ACs rewrite, hibernation ACs, inc2b ICD,
   launcher ACs, deploy/compose.yml + .dockerignore (round first for the
   deploy files, mkey FEAT-9995385600).
4. FEAT-9995385597 reconnect/waking/draining build (after BUG-1209 and the
   meter land; same server files).
5. inc2a FEAT-9995385594 R2 store against an in-process fake S3 (after the
   meter lands: it extended persist.Store with HostStore). Pre-assign error
   codes in the brief. Scope addition from the BUG-1205 profile is on the item.
6. Hibernation build FEAT-9995385595 (after BUG-1209 and reconnect).
7. Small bugs, grouped by file area: webconsole BUG-1194 + BUG-1190;
   webconsole BUG-1192 + BUG-1200 + BUG-1191; Go test infra BUG-1136 +
   BUG-1193; test hygiene BUG-1195 + BUG-1207; BUG-1199 (azure workflow);
   after the meter: BUG-1208, BUG-1202, BUG-1203. Then BUG-1142, BUG-1144
   and the next P1/P2 bugs from `node claude-bow.js list --by-seq`.
8. pix measurements (round-trip p95, journal append latency, wake time) once
   SAFE confirms the redeploy on port 8120.

## Parked for Aaron (morning)

- 103 dirty worktrees were kept (uncommitted content; not forced). 32 clean
  ones were removed under token 20261117-221954-de3c67.
- Hibernation ASM-H6 (store retries run inside the stop, city not playable
  for up to ~70 s) and ASM-H7 (pause counts through the no-command clock).
- GGR95 delete reports went to SAFE's fallback log (bus refused the text).
