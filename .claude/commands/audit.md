---
description: Run the full Golden Rules + Skills + Hooks audit (Phases A-D from gr-audit-2026-05-06.md). Outputs a dated report to docs/. Quarterly cadence recommended.
allowed-tools: Bash(git log:*), Bash(git diff:*), Bash(grep:*), Bash(node:*), Bash(ls:*), Read, Write
---

## Context

- Today's date: !`date +%Y-%m-%d`
- Total commits last 6 months: !`git log --pretty=format:"%h" --since="6 months ago" 2>/dev/null | wc -l`
- Recent fix commits: !`git log --pretty=format:"%h %ad %s" --date=short --since="6 months ago" --grep="^fix:" 2>/dev/null | head -30`
- Skills available: !`ls .claude/commands/`
- Hooks file: !`cat .claude/settings.json 2>/dev/null | head -60`
- code.json size: !`node -e "const d=require('./code.json'); console.log('GUIDs:', d.total_guids, 'updated:', d.last_updated)"`

## Your task

Run a comprehensive audit of the Prix Six rule/skill/hook architecture. Find drift between rules-as-written and rules-as-applied. Identify gap categories. Produce a ranked recommendations document.

The reference run is `docs/gr-audit-2026-05-06.md` — read it before starting so you understand the format and depth expected.

This skill outputs to `docs/gr-audit-YYYY-MM-DD.md`. Do NOT overwrite a same-day file; if one exists, suffix with `-2`, `-3`, etc.

---

### Phase A — Inventory

Build a coverage matrix mapping every {GR | Skill | Hook | Memory pattern} so we can see what's covered and what isn't.

**Sub-tasks:**

1. Read `CLAUDE.md` for the canonical GR list (one-line summaries)
2. Read `docs/golden-rules-detail.md` for full implementation patterns and checklists
3. List every skill in `.claude/commands/` with its `description:` frontmatter
4. Read `.claude/settings.json` for hooks
5. Search Vestige with broad queries to find pattern memories tagged `prixsix`:
   ```
   mcp__vestige__search query="prix six golden rule pattern"
   mcp__vestige__search query="prix six bug fix root cause"
   mcp__vestige__search query="prix six lesson learned violation"
   ```

**Output:** A table per layer (GRs, Skills, Hooks, Patterns) listing each entry with a one-line summary, plus drift flags ("in memory but not in CLAUDE.md", "in CLAUDE.md but no enforcing skill", etc).

---

### Phase B — Cross-reference: bugs → preventable?

For each `fix:` commit in the last 6 months, ask three questions:

1. Was a GR or Skill in place that COULD have prevented or detected this bug?
2. If yes, why didn't it fire? (Wasn't queried, wasn't automated, didn't apply)
3. If no, what GR/Skill/Hook would have caught it?

Categorise each bug:
- `prevented` (rule existed and worked)
- `detected late` (rule existed but caught only reactively)
- `not preventable` (genuine novelty, no rule applies)
- `gap-found` (no rule, rule should be added)

Sample 30-50 fix commits across the period. Pull commit messages with:

```bash
git log --pretty=format:"%h %ai %s%n%b%n---" --since="6 months ago" --grep="^fix:" 2>/dev/null | head -300
```

For high-volume bug categories (e.g. Pit Wall replay had 25+ commits in March 2026), group them.

**Output:** A table per bug category with sample commits, root cause class, existing GR/Skill (if any), and gap classification.

---

### Phase C — Memory hygiene

Identify Vestige memories that should be merged, demoted, or rewritten. This phase is a subset of `/memory-hygiene`; if that skill has been run recently, link to its output instead of duplicating.

Run broad queries:

```
mcp__vestige__search query="prix six current version as of"
mcp__vestige__search query="prix six version state"
```

For each result with timestamp-anchored "current" facts older than 90 days, flag for demotion.

```
mcp__vestige__search query="prix six golden rule reference"
mcp__vestige__search query="prix six error handling pattern"
```

For results with similarity > 0.85, flag as duplicate-merge candidates.

Also check for memories pointing to the legacy project path (`E:\GoogleDrive\Papers\03-PrixSix\`):

```
mcp__vestige__search query="E:\\GoogleDrive\\Papers\\03-PrixSix"
```

**Output:** List of memory IDs with recommended action (demote / merge / rewrite).

---

### Phase D — code.json hygiene

Check the registry for systemic issues:

```bash
# 1. Broken refs
node -e "try{const d=require('./code.json');const all=new Set(d.guids.map(g=>g.guid));let broken=0;for(const g of d.guids){for(const r of [...(g.callChain?.calls||[]),...(g.callChain?.calledBy||[]),...(g.dependencies||[])]) if(!all.has(r))broken++;} console.log('broken_refs:', broken);}catch(e){console.log('ERROR:', e.message);}"

# 2. total_guids vs actual array length
node -e "const d=require('./code.json'); console.log('field:', d.total_guids, 'actual:', d.guids.length, d.total_guids===d.guids.length ? 'MATCH' : 'MISMATCH');"

# 3. GUID source markers vs registry version field
grep -rn "// GUID: " app/src functions --include="*.ts" --include="*.tsx" --include="*.js" 2>/dev/null \
  | grep -oE "GUID: [A-Z][A-Z0-9_]+-[0-9A-Z]+-v[0-9]+" \
  | grep -oE "[A-Z][A-Z0-9_]+-[0-9A-Z]+-v[0-9]+" \
  | sort -u > /tmp/source-guids.txt

# Then for each marker, check code.json's version field vs the source v number
node -e "
const fs = require('fs');
const d = require('./code.json');
const markers = fs.readFileSync('/tmp/source-guids.txt', 'utf8').split('\\n').filter(Boolean);
let drift = 0;
for (const m of markers) {
  const match = m.match(/^([A-Z][A-Z0-9_]+-[0-9A-Z]+)-v(\\d+)\$/);
  if (!match) continue;
  const [, id, v] = match;
  const e = d.guids.find(g => g.guid === id);
  if (e && e.version !== parseInt(v, 10)) {
    console.log('DRIFT', id, 'src v' + v, '!=', 'reg v' + e.version);
    drift++;
  }
}
console.log('total drift:', drift);
"
```

**Output:** Drift report — broken refs count, total_guids match, version drift entries.

---

### Phase E — Recommendations

Synthesise A+B+C+D into a ranked recommendations list. Use the priority scheme from `docs/gr-audit-2026-05-06.md`:

- **P0** — Drift fixes (canonicalise rules that are in memory but not docs)
- **P1** — New GRs targeting recurring gap categories
- **P2** — New skills for systematic checks
- **P3** — Skill updates (add new gates/checks to existing skills)
- **P4** — Hook additions (auto-running checks)
- **P5** — Memory hygiene one-shots

For each recommendation:
- What it does
- What gap it closes
- Effort estimate
- Risk level

**Output:** A final summary table with ~10-15 recommendations ranked by impact ÷ effort.

---

### Final deliverable

Write the full report to `docs/gr-audit-<today>.md`:

```bash
NEW_AUDIT="docs/gr-audit-$(date +%Y-%m-%d).md"
# If file exists, suffix with -2, -3, etc
test -f "$NEW_AUDIT" && NEW_AUDIT="docs/gr-audit-$(date +%Y-%m-%d)-2.md"
```

Format the doc with:
- Title + status header
- Phase A inventory tables
- Phase B cross-reference table with gap classification
- Phase C memory hygiene list
- Phase D code.json drift report
- Phase E recommendations table
- Implementation plan (which versions ship which P-priority items)

Then add a `/bow` entry pointing to the new audit doc with priority based on the highest-priority gap found.

---

### Cadence recommendation

Run `/audit` quarterly OR after any 50+-commit period of focused work. The two together (calendar + activity) catch both gradual drift and post-feature-burst gaps.

Reference the previous audit at the top of the new one so the diffs are visible.
