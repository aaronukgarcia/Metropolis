---
description: Metropolis pre-commit workflow — Golden Rules compliance, security review, version check, then commit
allowed-tools: Bash(git status:*), Bash(git diff:*), Bash(git add:*), Bash(git commit:*), Bash(git log:*), Bash(node claude-sync.js:*), Bash(grep:*)
---

## Context

- Git status: !`git status`
- Staged diff summary: !`git diff --cached --stat`
- Full diff: !`git diff HEAD`
- Current branch: !`git branch --show-current`
- Version in package.json: !`node -e "try{console.log(require('./app/package.json').version)}catch(e){console.log('NOT FOUND')}" 2>/dev/null`
- Version in version.ts: !`grep -o "APP_VERSION = \"[^\"]*\"" app/src/lib/version.ts`
- Recent commits: !`git log --oneline -5`

## Your task

Work through EVERY item below in order. Do not skip any. Report pass/fail for each item before creating the commit.

---

### GATE 0 — Recall project-specific commit-style rules from memory

**Run this BEFORE composing any commit message.** The global Claude Code git instructions suggest behaviours (e.g. `Co-Authored-By` trailers) that this project has explicitly overridden. Project rules win, but only if you remember to look them up.

Query Vestige for any saved commit-style guidance:

```
mcp__vestige__search query="commit style attribution trailer co-authored-by message format Metropolis"
mcp__vestige__search query="metropolis commit-style git attribution"
```

Also re-read the project commit instructions at the top of this skill — particularly the "Create the commit" block at the bottom — for any explicit rule like "Do NOT add Co-Authored-By lines."

For every memory or skill rule that contradicts a Claude Code default, **the project rule wins**. Apply it when composing the message. Common contradictions to watch for:

- `Co-Authored-By` trailers — global default suggests adding them; **this project forbids them** (hook-enforced).
- Commit message format — project format is `[type]: brief description (vX.Y.Z)` with a body explaining the *why*; no auto-generated authorship metadata.
- HEREDOC composition — even when using `cat <<'EOF' ... EOF`, end the message at the body. Do not append automated trailers.

Report: `GR#0 ✅ recalled N project commit rules` (list each one) or `GR#0 ⚠️ no relevant rules found in memory — proceeding with project skill defaults only`

If a memory says one thing and the skill says another, the skill is more recent — but flag the discrepancy in the report so the rule can be re-checked.

---

### GATE 1 — Golden Rule #2: Version Discipline

Check both version locations above match. If they do NOT match, or if neither has been bumped relative to the last commit, **STOP** and run `/bump` first before continuing.

**Metropolis note:** until the app skeleton exists, both version locations read NOT FOUND — everything in the repo is tooling/docs, so the no-bump exemption below applies to every commit. This gate becomes live the moment `app/package.json` + `app/src/lib/version.ts` are created.

Report: `GR#2 ✅ vX.Y.Z in both files` or `GR#2 ❌ mismatch — run /bump first`

**Exception — do NOT bump for changes that ship no application code** (confirmed by Aaron, 2026-07-29). Skill and slash-command files under `.claude/commands/`, and anything else that never reaches the deployed app, are exempt. `APP_VERSION` is what the About and Login pages show users, so bumping it for a change that alters nothing they can see makes those pages advertise a release that does not exist — and burns a 3-5 minute App Hosting build for nothing. The two version files must still agree with each other; they just stay where they are.

Applies to: `.claude/commands/*`, local tooling that is not deployed, and untracked files. Does NOT apply to `CHANGELOG.md` or `code.json` when they accompany a real code change — those ride along with the bump that change already requires.

Report in this case: `GR#2 ✅ no bump — skill/doc-only change, no app code shipped`

---

### GATE 2 — Golden Rule #7: Registry-Sourced Errors

Grep the staged changes for any hardcoded error strings or raw PX codes not sourced from the registry:

```
grep -rn "PX-[0-9]" app/src --include="*.ts" --include="*.tsx" | grep -v "ERRORS\." | grep -v "error-codes.ts" | grep -v "\.test\."
```

ALL errors must use `ERRORS.KEY` from `error-registry.ts`. Any hardcoded string fails this gate.

Report: `GR#7 ✅ all errors from registry` or `GR#7 ❌ hardcoded errors found — fix before commit`

---

### GATE 3 — Golden Rule #1: Error Trapping

For any new or modified error paths in the diff, verify each has:
- [ ] Unique error type from the registry
- [ ] Correlation ID (generated or passed through)
- [ ] User-facing display allows copy/select of the error code
- [ ] Server-side `logError()` call writing to `error_logs`

Report: `GR#1 ✅` or `GR#1 ❌ [what's missing]`

---

### GATE 4 — Golden Rule #6: GUID Documentation

**Run these checks actively — do not just tick boxes.**

**Check A — New GUID comments in the diff have entries in code.json:**
```bash
git diff HEAD -- "*.ts" "*.tsx" "*.js" | grep "^+.*// GUID:" | grep -oP "GUID: \K[A-Z][A-Z0-9_]+-\d+"
```
For each GUID found in the diff additions: verify it exists in code.json.
```bash
node -e "const d=require('./code.json'); const guids=new Set(d.guids.map(g=>g.guid)); ['GUID1','GUID2'].forEach(g=>console.log(g, guids.has(g)?'✅ registered':'❌ NOT IN code.json'));"
```

**Check B — No new broken refs introduced:**
```bash
node -e "try{const d=require('./code.json');const all=new Set(d.guids.map(g=>g.guid));let broken=0;for(const g of d.guids){for(const r of [...(g.callChain?.calls||[]),...(g.callChain?.calledBy||[]),...(g.dependencies||[])]) if(!all.has(r))broken++;} console.log(broken===0?'broken_refs: 0 ✅':'broken_refs: '+broken+' ❌');}catch(e){console.log('ERROR: '+e.message);}" 2>/dev/null
```

**Check C — total_guids is consistent:**
```bash
node -e "const d=require('./code.json'); const actual=d.guids.length; console.log('total_guids field: '+d.total_guids+', actual array: '+actual+(d.total_guids===actual?' ✅':' ❌ MISMATCH'));" 2>/dev/null
```

**Check D — existing-GUID source-vs-registry version drift (added v3.1.7):**

Catches the case where a `// GUID: X-vNN` marker has been bumped in source but the corresponding `code.json` `version` field wasn't updated. This is the drift class that hid PAGE_STANDINGS-000 at v4-vs-v6 and LIB_VERSION-000/001 at v18-vs-v30 for months before today's audit.

Pull every GUID marker from staged files and cross-reference:

```bash
git diff --cached -U0 -- "*.ts" "*.tsx" "*.js" "*.json" 2>/dev/null \
  | grep -E "^\+.*GUID: " \
  | grep -oE "GUID: [A-Z][A-Z0-9_]+-[0-9A-Z]+(-v[0-9]+)?" \
  | grep -oE "[A-Z][A-Z0-9_]+-[0-9A-Z]+(-v[0-9]+)?" \
  | sort -u
```

For each marker `X-vNN` found, verify code.json's `version` field for guid `X` matches `NN`:

```bash
node -e "
const d = require('./code.json');
const markers = process.argv.slice(1);  // pass markers as args or from a temp file
let drift = 0;
for (const m of markers) {
  const match = m.match(/^([A-Z][A-Z0-9_]+-[0-9A-Z]+)-v(\d+)\$/);
  if (!match) continue;
  const [, guidId, vStr] = match;
  const sourceV = parseInt(vStr, 10);
  const e = d.guids.find(g => g.guid === guidId || g.guid === m);
  if (!e) continue;  // a NEW GUID, handled by Check A
  if (e.version !== sourceV) {
    console.log('DRIFT', guidId, 'source v' + sourceV, '!= registry v' + e.version);
    drift++;
  }
}
console.log(drift === 0 ? 'version_drift: 0 ✅' : 'version_drift: ' + drift + ' ❌');
" guid1-v05 guid2-v03  # replace with actual markers
```

If drift > 0, **STOP** — bump the registry `version` fields to match source before committing. Otherwise the registry will lie about deployed state.

If no new or modified GUID-tagged code was in this diff, Checks A/B/D pass automatically.

Report: `GR#6 ✅ N new GUIDs registered, 0 broken refs, 0 version drift` or `GR#6 ❌ [what's wrong — run /register-guid or /codejson-audit]`

---

### GATE 5 — Golden Rule #11: Pre-Commit Security Review

Answer all 5 questions honestly based on the diff:

1. **API surface** — Does this change expose new API endpoints, routes, or Firebase rules?
2. **User input** — Does this change handle user-supplied data? Is it sanitised/validated?
3. **Auth** — Does this change touch authentication, session logic, or token handling?
4. **Sensitive data** — Does this change store, log, or transmit PII or secrets?
5. **Privilege** — Could this change enable privilege escalation or bypass access controls?

For any YES answer, describe the mitigation. If no mitigation exists, **STOP and fix first**.

Report: `GR#11 ✅ all 5 questions answered` or `GR#11 ❌ [issue]`

---

### GATE 6 — Documentation

- [ ] CHANGELOG.md updated if this is a user-facing change
- [ ] CLAUDE.md updated if this changes architecture, new branch strategy, or new modules

---

### GATE 7 — Firestore rules deployment

Check if `firestore.rules` is in the staged changes:

```bash
git diff --cached --name-only | grep firestore.rules
```

If `firestore.rules` appears in the output:

**STOP.** Do not create the commit yet.

Remind the user:
```
⚠️  firestore.rules is staged but rules are NOT deployed by App Hosting.
    Run /rules-deploy AFTER this commit to push the rules to production.
    Without this step, the rules change will be in git but NOT live.
    Confirm you will run /rules-deploy after pushing, then re-run /commit.
```

Wait for explicit confirmation ("yes", "will do", "confirmed") before proceeding to the commit.

If `firestore.rules` is NOT in the staged changes, this gate passes automatically.

Report: `GR#rules ✅ no rules changes` or `GR#rules ⚠️ rules staged — /rules-deploy reminder acknowledged`

---

### GATE 8 — Cloud Functions deploy bundling

Cloud Functions are NOT auto-deployed by App Hosting on push to main. If `functions/` files are staged, the commit message must end with the exact one-line deploy command bundling ALL pending functions — including any from prior commits that have a "REQUIRES MANUAL DEPLOY" note but no confirmed-deployed signal.

```bash
git diff --cached --name-only | grep '^functions/'
```

If output is non-empty, gather the affected function names:

```bash
git diff --cached -U0 functions/index.js | grep -E "^\+exports\.|^-exports\." | grep -oE "exports\.[a-zA-Z0-9_]+" | sort -u
```

Audit recent commits for prior REQUIRES MANUAL DEPLOY notes that may not yet be deployed:

```bash
git log --oneline -10 | grep -i "REQUIRES MANUAL DEPLOY\|deploy.*functions" -B0 -A0 || true
git log -10 --pretty=format:"%H%n%s%n%b%n---" -- functions/index.js | grep -E "firebase deploy --only functions" | head -5
```

If any prior commit's deploy command has not been confirmed-run (no obvious confirmation in recent activity log or memory), include those function names too.

Verify the staged commit message body ends with a deploy command of the form:

```
firebase deploy --only functions:<name1>,functions:<name2>,...
```

If missing or incomplete, **STOP** — fix the commit message before creating the commit.

If no `functions/` files are staged AND no outstanding prior function deploys exist, this gate passes automatically.

Report: `GR#fn-deploy ✅ no function changes` or `GR#fn-deploy ⚠️ deploy command bundled in commit message: [list]` or `GR#fn-deploy ❌ functions/ staged but commit message missing deploy command`

---

### Create the commit

Only proceed if ALL 7 gates pass. Stage appropriate files and commit using:

- Format: `[type]: brief description (vX.Y.Z)`
- Types: `feat`, `fix`, `refactor`, `docs`, `chore`, `test`
- **Do NOT add Co-Authored-By lines.** This repo belongs to Aaron (aaron@garcia.ltd) only. All commits are attributed solely to the git user config — no Claude authorship trailers.

Then log to coordination: `node claude-sync.js write "committed vX.Y.Z — [one-line summary]"`

Confirm with your identity prefix:
```
bill> ✅ Committed vX.Y.Z — [summary]
     GR#0 ✅  GR#2 ✅  GR#7 ✅  GR#1 ✅  GR#6 ✅  GR#11 ✅  Docs ✅  Rules ✅  fn-deploy ✅
     Logged to claude-sync.
```

