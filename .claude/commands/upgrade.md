---
description: npm dependency audit and upgrade — categorise outdated packages, upgrade in stages, fix breaking changes, verify build
allowed-tools: Bash(npm:*), Bash(npx:*), Bash(node:*), Bash(git:*)
---

## Context

- Outdated packages: !`cd app && npm outdated 2>&1`
- Current version: !`grep -o "\"version\": \"[^\"]*\"" app/package.json | head -1`
- Node version: !`node --version`

## Your task

Audit and upgrade all npm packages for the Prix Six app at `app/`. Work through stages in order, running a build check after each. Do not advance to the next stage if the current one has unresolved errors.

**Working directory for all commands:** `app/`

---

### STAGE 0 — Categorise

From the `npm outdated` output above, sort every outdated package into one of three buckets:

| Bucket | Criteria | Action |
|--------|----------|--------|
| **Safe** | Current = Wanted, just behind Latest by patch/minor | Upgrade now |
| **Major** | MAJOR version jump (X.y.z → Y.y.z) | Upgrade with care, fix breaking changes |
| **Hold** | Known ecosystem conflict OR massive migration effort (e.g. Tailwind v4, Next.js major) | Flag, do not upgrade unless user confirms |

Known holds to be aware of:
- `zod` — must stay v3 while `genkit` peer dep requires `^3.x`
- `tailwindcss` — v4 is a config system rewrite; requires CSS migration
- `next` — major bumps may change middleware/proxy API, App Router behaviour

Present the table before starting upgrades. Ask if the user wants to proceed with all buckets or just safe/major.

---

### STAGE 1 — Safe patch/minor upgrades

Install all Safe packages in one command:
```bash
npm install pkg1@latest pkg2@latest ...
```

Then run:
```bash
npx tsc --noEmit 2>&1 | grep -v "parse-firestore-export"
```

Fix any TypeScript errors before proceeding. Report: `STAGE 1 ✅` or `STAGE 1 ❌ [errors]`

---

### STAGE 2 — Remove obsolete packages

Check for packages that are obsoleted by their parent (e.g. `@types/date-fns` is bundled in `date-fns` v3+):
```bash
npm outdated 2>&1 | grep -i "^@types/"
```

Uninstall any confirmed obsolete `@types/` packages.

---

### STAGE 3 — Major upgrades (one at a time)

For each major upgrade:
1. Install the package
2. Run `npx tsc --noEmit` — fix all new TypeScript errors
3. Run `npm run build` — fix any build errors
4. Move to next package only when clean

Common breaking changes to watch for:
- **date-fns v4**: locale import paths changed
- **@hookform/resolvers v5**: resolver function signatures changed
- **recharts v3**: `LegendProps`/`TooltipProps` payload fields moved to context
- **firebase v12**: modular API changes in auth, firestore, messaging
- **dotenv v17**: may drop older Node versions

---

### STAGE 4 — Hold packages (only if user confirmed)

**Tailwind CSS v4 migration:**
1. Install: `npm install tailwindcss@latest @tailwindcss/postcss@latest`
2. Delete `tailwind.config.ts` — config moves to CSS
3. Update `postcss.config.mjs`: `tailwindcss: {}` → `"@tailwindcss/postcss": {}`
4. Migrate `globals.css`: replace `@tailwind base/components/utilities` with `@import "tailwindcss"` and add `@theme inline {}` block for custom tokens
5. **CRITICAL**: After migration, scan for broken CSS variable syntax:
   ```bash
   grep -rn "\[--[a-zA-Z-]*\]" src --include="*.tsx" --include="*.ts"
   grep -rn "theme(spacing\." src --include="*.tsx" --include="*.ts"
   ```
   Any `[--var]` must become `(--var)`. Any `theme(spacing.X)` must become the resolved value (e.g. `1rem`).

**Next.js major migration:**
1. Install: `npm install next@latest`
2. Check `next.config.ts` for deprecated options
3. In Next.js 16+, middleware file is `proxy.ts` (not `middleware.ts`); export function is `proxy()`
4. `cookies()`, `headers()` are async — must `await`
5. Run `npm run build` and fix all errors

---

### STAGE 5 — Final verification

```bash
npx tsc --noEmit 2>&1 | grep -v "parse-firestore-export"
npm run build 2>&1 | tail -20
```

Both must be clean before proceeding to commit.

---

### STAGE 6 — Commit

Run `/bump` to increment the version, then `/commit` with a chore commit summarising all upgrades.

Format:
```
chore: upgrade dependencies — [list major ones] (vX.Y.Z)
```

Report:
```
bill> ✅ Upgrade complete
     Safe:   N packages
     Major:  N packages (N with code fixes)
     Held:   [list with reason]
     Build:  ✅ clean
     TS:     ✅ 0 errors
```
