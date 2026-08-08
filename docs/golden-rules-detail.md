# docs/golden-rules-detail.md

> Full implementation patterns, code templates, and compliance checklists for all 19 Golden Rules.
> This file is referenced from `CLAUDE.md` and should be read when you need detailed guidance.
> Rules #12-#19 added to this file in v3.1.6 as part of the Golden Rules audit (see `docs/gr-audit-2026-05-06.md`).

---

##  GOLDEN RULE #1: Aggressive Error Trapping

**Every function, API call, database operation, or async action MUST have comprehensive error handling.** This is not optional. This is not "nice to have". This is mandatory.

### The Four Pillars — ALL FOUR REQUIRED ON EVERY ERROR

| Pillar | Requirement | Implementation |
|--------|-------------|----------------|
| **1. Error Log** | Every error MUST be logged to `error_logs` collection | Call `logError()` — no silent failures |
| **2. Error Type** | Every error MUST map to a defined error code | Use `ERROR_CODES` from `error-codes.ts` |
| **3. Correlation ID** | Every error MUST have a unique correlation ID | Use `generateCorrelationId()` or `generateClientCorrelationId()` |
| **4. Selectable Display** | Every error shown to users MUST have copy-pasteable text | User must be able to select and copy the error code + correlation ID |

### Before You Write ANY Code

Ask yourself:
- What can fail here?
- What error code will I use?
- How will the user copy the error details?
- Is the error being logged?

If you cannot answer ALL FOUR questions, **stop and add error handling first**.

### Minimum Error Handling Template

**For API Routes / Server Actions:**
```typescript
import { ERROR_CODES, generateCorrelationId } from '@/lib/error-codes';
import { logError } from '@/lib/firebase-admin';

const correlationId = generateCorrelationId();
try {
  // Your code here
} catch (error) {
  await logError({
    correlationId,
    error,
    errorCode: ERROR_CODES.RELEVANT_CODE.code,
    context: { route: '/api/your-route', userId, action: 'what-you-were-doing' }
  });

  return NextResponse.json({
    success: false,
    error: ERROR_CODES.RELEVANT_CODE.message,
    errorCode: ERROR_CODES.RELEVANT_CODE.code,
    correlationId,  // MUST be included for user to copy
  }, { status: 500 });
}
```

**For React Components / Client-Side:**
```typescript
import { ERROR_CODES, generateClientCorrelationId } from '@/lib/error-codes';

try {
  // Your code here
} catch (error) {
  const correlationId = generateClientCorrelationId();

  // Log to server (fire-and-forget is acceptable)
  fetch('/api/log-error', {
    method: 'POST',
    body: JSON.stringify({ correlationId, error: error.message, errorCode: ERROR_CODES.RELEVANT_CODE.code })
  });

  // Display with SELECTABLE text — user MUST be able to copy this
  toast({
    variant: "destructive",
    title: ,
    description: (
      <span className="select-all cursor-text">
        {error.message} — Ref: {correlationId}
      </span>
    ),
  });
}
```
### What Selectable Display Means

The error message shown to users MUST allow them to:
1. Click/tap on the error text
2. Select (highlight) the error code and correlation ID
3. Copy to clipboard (Ctrl+C / Cmd+C / long-press)
4. Paste into WhatsApp or email to report the issue

**Acceptable implementations:**
- `<span className="select-all cursor-text">` — makes entire span selectable on click
- `<code>` or `<pre>` elements — naturally selectable
- Copy button next to the error — explicit copy action

**NOT acceptable:**
- Error codes only in console logs
- Correlation IDs not shown to user
- Non-selectable toast messages

### Compliance Check

- [ ] Every `try` block has a corresponding `catch` with full error handling
- [ ] Every `catch` generates or uses a correlation ID
- [ ] Every `catch` maps to an `ERROR_CODES` entry
- [ ] Every user-facing error displays selectable text with code + correlation ID
- [ ] Every error is logged via `logError()` or equivalent API call

**If ANY checkbox fails, the code is not ready for commit.**

---

##  GOLDEN RULE #2: Version Discipline

**Every commit MUST bump the version number. Every push to main MUST be verified for build success and version consistency.**

### Version Bump Requirements

| When | Action Required |
|------|-----------------|
| **Every commit** | Bump PATCH version minimum (e.g., 1.17.0 — 1.17.1) |
| **New feature** | Bump MINOR version (e.g., 1.17.1 — 1.18.0) |
| **Breaking change** | Bump MAJOR version (e.g., 1.18.0 — 2.0.0) |

### Files That MUST Be Updated Together

Both files MUST show the same version number — always update them as a pair:

1. `app/package.json` — the `"version"` field
2. `app/src/lib/version.ts` — the `APP_VERSION` constant

**Never update one without the other.**

### Post-Push Verification — MANDATORY

After every push to `main`, you MUST:

1. **Wait for build completion** (~3-5 minutes)
2. **Check build logs for success:**
```powershell
powershell -Command "& 'C:Program Files (x86)GoogleCloud SDKgoogle-cloud-sdkingcloud.cmd' logging read 'resource.type="build"' --project=studio-6033436327-281b1 --limit=20 --freshness=15m --format='value(textPayload)'"
```
3. **Verify version consistency across all pages:**

| Page | URL | What to Check |
|------|-----|---------------|
| About | https://prix6.win/about | Version number displayed |
| Login | https://prix6.win/login | Version number in footer/corner |

4. **Confirm all pages show IDENTICAL version numbers**

### Version Verification Checklist

- [ ] Build logs show `DONE` status
- [ ] About page shows correct version
- [ ] Login page shows correct version
- [ ] Both pages show IDENTICAL version
- [ ] Version matches what's in `package.json` and `version.ts`

**A push is NOT complete until all boxes are checked.**

---
##  GOLDEN RULE #3: Single Source of Truth

**Data MUST have exactly one authoritative source. If technical constraints require duplication, the Consistency Checker MUST validate synchronisation.**

### The Principle

Every piece of data in the system must have ONE canonical location. All other usages must either:
- **Reference** the source (preferred), OR
- **Duplicate with mandatory sync validation** (when technically unavoidable)

### Prohibited Patterns

| Don't Do This | Do This Instead |
|------------------|-------------------|
| Store user email in Firestore AND Firebase Auth independently | Firebase Auth is the source; Firestore references or caches with sync check |
| Store driver names in multiple collections | Single `drivers` collection; other collections reference by ID |
| Hardcode values that exist in the database | Read from database or use constants file as single source |
| Store calculated values that can be derived | Calculate on read, or cache with clear invalidation rules |

### Firebase Auth vs Firestore — CRITICAL

| Data Field | Authoritative Source | Duplication Rules |
|------------|---------------------|-------------------|
| Email | Firebase Auth | If cached in Firestore, CC MUST verify match |
| Display Name | Firebase Auth | If cached in Firestore, CC MUST verify match |
| UID | Firebase Auth | Firestore documents keyed by UID — this is a reference, not duplication |
| User preferences | Firestore | NOT in Firebase Auth |
| Team memberships | Firestore | NOT in Firebase Auth |

### Consistency Checker Requirements

The CC (`/app/src/components/admin/ConsistencyChecker.tsx`) MUST include validations for:

| Check | What It Validates |
|-------|-------------------|
| `auth-firestore-email-sync` | User email in Firebase Auth matches email in Firestore users collection |
| `auth-firestore-name-sync` | Display name in Firebase Auth matches name in Firestore (if stored) |
| `driver-reference-integrity` | All driver IDs referenced in teams/predictions exist in drivers collection |
| `track-reference-integrity` | All track IDs referenced in races exist in tracks collection |

### Compliance Checklist

- [ ] New data fields have exactly one source of truth
- [ ] Any duplication is documented with justification
- [ ] Any duplication has a corresponding CC validation
- [ ] References use IDs, not copied values
- [ ] Firebase Auth data is not independently stored in Firestore without sync checks

---

##  GOLDEN RULE #4: Identity Prefix

**EVERY response you give MUST start with your assigned name prefix. No exceptions.**

| If you are | Your prefix | Example |
|------------|-------------|---------|
| First instance (Bill) | `bill> ` | `bill> I've updated the file...` |
| Second instance (Bob) | `bob> ` | `bob> The build completed...` |
| Third instance (Ben) | `ben> ` | `ben> I've reviewed the backup system...` |

### Correct Examples

```
bob> I've reviewed the error handling and it follows Golden Rule #1.

bob> Version bumped to 1.30.1 in both package.json and version.ts.

bob> Build 1.30.1 deployed at 20:21 successfully. About page and Login page both show 1.30.1.
```

### WRONG — Never Do This

```
I've reviewed the error handling...     — WRONG: No prefix

Sure, I can help with that...           — WRONG: No prefix

The build completed successfully.       — WRONG: No prefix
```

### Mid-Session Self-Check

**Every 5 responses, mentally verify: "Am I still using my prefix?"**

If you catch yourself without it:
1. Immediately correct by adding the prefix
2. Apologise: `bob> Apologies — I dropped my prefix. Correcting now.`

---
##  GOLDEN RULE #5: Verbose Confirmations

**When completing key actions, you MUST provide explicit, timestamped confirmations. No vague "done" or "completed" messages.**

### Required Confirmation Formats

| Action | Required Confirmation Format |
|--------|------------------------------|
| Version bump | `bob> Version bumped to 1.30.1 in both package.json and version.ts.` |
| Commit | `bob> Committed: "feat: add deadline warnings" (1.30.1)` |
| Push to main | `bob> Pushed 1.30.1 to main. Monitoring build...` |
| Build success | `bob> Build 1.30.1 deployed at 20:21 successfully.` |
| Version verified | `bob> Version check: About page = 1.30.1, Login page = 1.30.1. Match confirmed.` |
| Build failure | `bob> Build 1.30.1 FAILED at 20:25. Error: [specific error]. Investigating...` |
| File claimed | `bob> Claimed /app/src/components/Scoring.tsx — now in my NO-TOUCH ZONE.` |
| File released | `bob> Released /app/src/components/Scoring.tsx — available for others.` |

### Full Deployment Confirmation Sequence

```
bob> Pushed 1.30.1 to main at 20:15. Monitoring build...

[after checking build logs]

bob> Build 1.30.1 completed at 20:21 (6 min build time). Verifying deployment...

[after checking pages]

bob> Deployment verified:
     - Build status: SUCCESS
     - About page: 1.30.1
     - Login page: 1.30.1
     - All versions match. Deployment complete.
```

### WRONG — Never Do This

```
bob> Done.                              — WRONG: What's done? What version?

bob> Build finished.                    — WRONG: Success or failure? What version?

bob> I've updated the version.          — WRONG: To what number? In which files?

bob> Pushed the changes.                — WRONG: What version? To which branch?
```

---

##  GOLDEN RULE #6: GUID Documentation Discipline

**Every code change MUST respect and maintain the GUID commenting system. Read existing comments before modifying code. Update GUID versions and remarks when logic changes. Update `code.json` to reflect all GUID additions and changes.**

### Before Modifying ANY Code

1. **Read the GUID comments** on the code block you're about to change
2. **Understand the three fields:**
   - `[Intent]` — why this code exists
   - `[Inbound Trigger]` — what causes it to execute
   - `[Downstream Impact]` — what breaks if this code changes
3. **Consider the downstream impact** — if the comment says "X depends on this", check X before changing

### When Changing Existing Code

| What Changed | Required Action |
|-------------|----------------|
| Logic changed (behaviour differs) | Increment GUID version (e.g., v03 — v04), update all three remark fields, update `code.json` |
| Refactored but same behaviour | Increment GUID version, update remarks to reflect new structure |
| Deleted code | Remove GUID from `code.json`, remove from other GUIDs' dependency lists |
| Moved code to different file | Update GUID remarks with new location, update `code.json` |

### When Adding New Code

Every new logical block MUST have:

```
// GUID: [MODULE_NAME]-[SEQ]-v03
// [Intent] Why this code exists.
// [Inbound Trigger] What causes this code to execute.
// [Downstream Impact] What depends on this code or what breaks if it changes.
```

- Use the module naming convention from the file
- Start at v03 (Fully Audited) for new code where you know the business logic
- Add a corresponding entry to `code.json`

### Updating code.json

The manifest at `code.json` MUST stay in sync with code comments:

- **New GUID** — Add entry with guid, version, logic_category, description, dependencies
- **Changed GUID** — Increment version number, update description if behaviour changed
- **Removed GUID** — Delete entry, remove from all dependency arrays
- **logic_category** must be one of: `VALIDATION`, `TRANSFORMATION`, `ORCHESTRATION`, `RECOVERY`

### Compliance Checklist

- [ ] All modified code blocks have updated GUID versions and remarks
- [ ] All new code blocks have GUID comments with all three fields
- [ ] `code.json` reflects all GUID additions, changes, and deletions
- [ ] Dependency arrays in `code.json` are accurate (no stale references)
- [ ] No GUID comments describe behaviour that no longer matches the code

---
##  GOLDEN RULE #7: Registry-Sourced Errors

**Every error MUST be created from the error registry. No exceptions.**

### The Four Diagnostic Questions

Every error log MUST answer automatically:

| # | Question | Answered By |
|---|----------|-------------|
| 1 | **Where did it fail?** | `file` + `functionName` + `correlationId` |
| 2 | **What was it trying to do?** | `message` + `context` |
| 3 | **Known failure modes?** | `recovery` + `failureModes` |
| 4 | **Who triggered it?** | `calledBy` + `calls` + `context` |

### Required Pattern

```typescript
import { ERRORS } from '@/lib/error-registry';
import { createTracedError, logTracedError } from '@/lib/traced-error';

const traced = createTracedError(ERRORS.SMOKE_TEST_FAILED, {
  correlationId,
  context: { importPath, backupDate }
});
await logTracedError(traced, db);
throw traced;
```

### Forbidden Patterns

```typescript
// NEVER hardcode error codes
throw new Error('PX-7004: Smoke test failed');

// NEVER manually construct metadata
logError('PX-7004', 'Smoke test failed', context);

// NEVER log without registry
console.error('[BACKUP_FUNCTIONS-026]', error);
```

### Adding New Errors

1. Add to `errorProfile.emits` in `code.json`
2. Run `npx tsx scripts/generate-error-registry.ts` from the `app/` directory
3. Import `ERRORS.NEW_ERROR_KEY` from `@/lib/error-registry`
4. **Never skip the generation step**

### Lookup Protocol

| User Says | Lookup Type | Where to Look |
|-----------|-------------|---------------|
| "PX-7004" | Error code | `code-index.json` — `byErrorCode["PX-7004"]` |
| "smoke test error" | Topic | `code-index.json` — `byTopic["smoke"]` |
| "BACKUP_FUNCTIONS-026" | GUID | `code.json` — find GUID entry |
| "error in scoring.ts" | File | `code-index.json` — `byFile["app/src/lib/scoring.ts"]` |

1. Check `code-index.json` **FIRST** (instant lookup)
2. Answer the four diagnostic questions
3. Report: GUID — File — Function — Recovery
4. **Never grep blindly** — use the registry

### Compliance Checklist

- [ ] Error is created via `createTracedError(ERRORS.KEY)` — not hardcoded
- [ ] Error is logged via `logTracedError()` — not manual console.error
- [ ] Error code comes from `error-registry.ts` — not inline string
- [ ] Error includes correlation ID and context
- [ ] `error-registry.ts` was regenerated if `code.json` errorProfile changed

---
##  GOLDEN RULE #8: Prompt Identity Enforcement

**The "who" check. When the user says "who", you MUST verify your prompt prefix is correct. If it is not — or was not — log a violation to Vestige memory. No exceptions.**

### The Rule

Every agent that has checked in via `claude-sync.js checkin` MUST prefix ALL responses with their assigned name (e.g. `bill>`, `bob>`, `ben>`). Rule #8 adds **enforcement and accountability**.

### The "who" Check

When the user says **"who"**:

1. **Check:** Am I currently using the correct prompt prefix?
2. **Check:** Have I been using it consistently since checkin — or did I have to be corrected?
3. **If either check fails:** Log a violation to Vestige memory immediately

### Violation Logging — MANDATORY

Every violation MUST be logged to Vestige memory with:

| Field | Example |
|-------|---------|
| **Agent name** | Bill |
| **Date/time** | 2026-01-31 17:30 UTC |
| **Violation number** | #1 (incrementing counter per agent) |
| **What happened** | Didn't use `bill>` prefix after checkin |
| **Reason why** | Treated checkin output as informational instead of a directive |

Use `mcp__vestige__smart_ingest` with tags `["prompt-violation", "<agent-name>", "golden-rule-8", "counter", "prixsix"]`.

### Scorekeeping

Maintain a running tally across sessions:
- `Bill = N violations`
- `Bob = N violations`
- `Ben = N violations`

When logging a new violation, recall the previous count first via `mcp__vestige__recall` with query `"prompt identity violation"`, increment, and store the updated tally.

### Honesty During "who" Check

**Do NOT claim "no violation" if you had to be corrected during the session.** The "who" check must be honest — it covers the entire session, not just the current message. Falsely reporting "no violation" is itself a violation.

---

##  GOLDEN RULE #9: Shell Preference

**When executing commands, prioritize shell selection in this order: PowerShell — CMD — bash. Only use bash when PowerShell and CMD cannot accomplish the task.**

### Shell Priority

| Shell | Use For | Examples |
|-------|---------|----------|
| **PowerShell** | Most operations, Azure CLI, gcloud CLI, git, npm | `Get-Content`, `az containerapp logs`, `gcloud logging read` |
| **CMD** | Legacy batch scripts, simple commands when PowerShell escaping is problematic | `dir`, `copy`, basic git commands |
| **bash** | Unix-specific tools, complex piping that requires Unix semantics | `grep` with complex regex, `find` (though prefer Glob tool) |

### Tool Preference Over bash

Before reaching for bash, check if a specialized tool can accomplish the task:
- **File search** — Use `Glob` tool (not `find` or `ls`)
- **Content search** — Use `Grep` tool (not `grep` or `rg`)
- **Read files** — Use `Read` tool (not `cat`/`head`/`tail`)
- **Edit files** — Use `Edit` tool (not `sed`/`awk`)
- **Write files** — Use `Write` tool (not `echo >`/`cat <<EOF`)

---
##  GOLDEN RULE #10: Dependency Update Discipline

**Any dependency that is discovered or encountered during a bug fix or feature build MUST be checked for available updates.**

### What Counts as Encountered

| Scenario | Action Required |
|----------|-----------------|
| Import statement in code you're modifying | Check that package for updates |
| Error message mentioning a package | Check that package for updates |
| Using a CLI tool (gcloud, az, npm, etc.) | Check tool version vs latest |
| Debugging an issue caused by a library | Check for bug fixes in newer versions |
| Adding a new dependency | Already checking latest — this is standard |

### How to Check for Updates

```powershell
cd E:GoogleDrivePapers-PrixSix.Currentapp
npm outdated
```

**For a specific package:**
```powershell
npm show <package-name> version
npm show <package-name>@latest version
```

### When NOT to Update

Acceptable reasons to defer:
- **Breaking changes** require significant refactoring (document in issue/comment)
- **Major version jump** needs dedicated testing effort (create follow-up task)
- **Security audit required** for new version (enterprise/compliance constraint)
- **Dependency conflict** with other packages (needs resolution plan)

**Never defer for:**
- "It works, why change it?" (technical debt accumulation)
- "Too busy" (compounds future maintenance burden)
- Security patches (MUST update unless breaking changes require emergency workaround)

### Reporting Updates in Commit Messages

```
feat: improve error handling in auth flow

- Updated firebase-admin 11.5.0 — 12.0.0
- Updated express 4.18.2 — 4.19.2 (security patch CVE-2024-xxxx)
- Deferred @google-cloud/firestore 7.1.0 — 8.0.0 (breaking: query syntax changes)
```

### Compliance Checklist

- [ ] Identified all dependencies touched by this work
- [ ] Checked each dependency for available updates
- [ ] Updated dependencies OR documented reason to defer
- [ ] Tested that updates don't break existing functionality
- [ ] Included dependency changes in commit message if updated

---
##  GOLDEN RULE #11: Pre-Commit Security Review

**Before EVERY commit, you MUST perform threat modeling on all code changes. Ask how the change could be exploited, circumvented, or compromised. This is mandatory — no exceptions.**

### The Five Mandatory Questions

Before every commit, you MUST ask and answer:

| # | Question | What to Look For |
|---|----------|------------------|
| 1 | **How could this be exploited?** | Input validation bypass, injection attacks, authentication bypass, information disclosure |
| 2 | **How can this be circumvented?** | Client-side checks only, missing server validation, race conditions, timing attacks |
| 3 | **Is it secure?** | Credentials exposure, weak randomness, missing authorization, unencrypted sensitive data |
| 4 | **How would I get around this security?** | Privilege escalation paths, token/session hijacking, direct API calls bypassing UI logic |
| 5 | **What's the worst case if this fails?** | Data breach, account takeover, system compromise, financial loss, reputation damage |

### Attack Vectors to Always Consider

| Threat Category | What to Check |
|----------------|---------------|
| **Authentication Bypass** | Can auth be skipped? Are credentials properly validated server-side? |
| **Authorization Bypass** | Can users access data/functions they shouldn't? Are permissions checked server-side? |
| **Privilege Escalation** | Can a regular user gain admin rights? Are role checks in Firestore rules? |
| **Client-Side Manipulation** | Are critical checks only on client? Can API be called directly? |
| **Injection Attacks** | SQL/NoSQL injection, XSS, HTML injection, command injection in user inputs |
| **Credential Exposure** | Are secrets in code? Logged in plaintext? Stored unencrypted? |
| **Weak Randomness** | Using Math.random() for security? Modulo bias in token generation? |
| **Rate Limiting** | Can endpoint be spammed? DoS/DoW attack vectors? |
| **Data Leakage** | PII in logs? Sensitive data in error messages? Debug info in production? |
| **Session Security** | Token expiry? Session fixation? CSRF protection? |

### Documentation Requirements

After completing security review, you MUST:

1. **Log to Vestige Memory** using this format:

```
Pre-Commit Security Review - [Timestamp]

Files Changed: [list]
Commit: [description]

Security Analysis:
1. Exploitation vectors considered: [list]
2. Circumvention attempts analyzed: [list]
3. Security controls validated: [list]
4. Attack scenarios tested: [list]

Threats Identified:
- [Threat 1]: Mitigated by [control]
- [Threat 2]: Mitigated by [control]
- [Accepted Risk]: [justification]

Result: Security review complete. Code ready for commit.
```

2. **Update code.json** with security-relevant remarks in GUID comments

### Compliance Checklist

- [ ] All 5 security questions asked and answered
- [ ] Attack vectors specific to this change identified
- [ ] Client-side security checks verified to have server-side enforcement
- [ ] No credentials, secrets, or PII exposed in code or logs
- [ ] Authorization checks present for privileged operations
- [ ] Input validation on all user-controlled data
- [ ] Security review logged to Vestige memory with timestamp
- [ ] code.json updated with security-relevant remarks

**If ANY checkbox fails, the code is not ready for commit.**

### Rationale

The plaintext PIN storage vulnerability (missed in initial review, caught by Gemini) demonstrates why this rule is mandatory. Reactive security fails — proactive threat modeling prevents. Thinking "how would I steal credentials?" before commit would have caught it.

---

## GOLDEN RULE #12: Dependency & Completeness Check

**Before marking work complete, verify every dependent system is also complete. Never create a dependency without an implementation. Never ship a feature without its supporting infrastructure.**

### What "complete" requires

| If you create… | …then you also need |
|---|---|
| A new API endpoint | Error logging via `logTracedError`, registered error code, GUID comment, code.json entry |
| A new Firestore collection | Security rules, CC validation, schema documented, backup verified to include it |
| A new feature | A path back out (rollback / feature flag / disable mechanism) |
| A new backup mechanism | A verified restore test |
| A new dependency in source | Implementation actually present (no orphaned imports) |
| A new scheduled function | Status field readable by admin dashboard + `/health-check` CHECK 11 covers it |

### Origin

Codified 2026-02-12 after multiple incidents where work was claimed "complete" but key dependents were missing — most notably the version bump that didn't update `commit-history.json`, leaving `/about/dev` 27 versions stale.

### Compliance checklist

- [ ] Every new import points to code that exists in this commit
- [ ] Every new collection has security rules in `firestore.rules` (committed AND deployed via `/rules-deploy`)
- [ ] Every new feature has a corresponding off-switch (feature flag, env var, or `.disable()` path)
- [ ] Every backup write has a corresponding restore-test run within 7 days
- [ ] Every new error path has a registry entry, correlation ID, and admin viewer integration
- [ ] Documentation (CHANGELOG.md, /about/dev commit-history, code.json) updated alongside the code

**If ANY checkbox fails, the work is not complete. Do not claim done.**

---

## GOLDEN RULE #13: Complete All Identified Issues

**When the user identifies multiple failures and asks for them to be fixed, ALL must be resolved immediately — no "TODO" markers, no "lower priority" punts, no "we'll get to it later".**

### The pattern this prevents

User: *"Fix this dam mess — A, B, C, D, E."*
Agent (wrong): *"Fixed A, B, D, E. Marked C as TODO (lower priority)."*
User reaction: *"SLOPPY."* Trust eroded; user has to re-list issues.

### The rule

When the user lists N failures and asks them fixed:

1. **Fix all N.** No exceptions.
2. **Do not classify any as "lower priority"** — the user's frustration level signals that all are blocking.
3. **Do not claim completion** until every item on the original list is verified resolved.
4. **Verify each fix before reporting done** — don't claim X is fixed without checking.
5. **If a fix genuinely cannot be done in this session** (true blocker, not effort), say so explicitly and ask whether to defer or push through.

### Origin

Codified 2026-02-12 after a session where 4-of-5 user-identified failures were fixed and the 5th was marked "TODO (lower priority)". User had to re-prompt; the deferred item was actually as important as the others.

### Application across "all yes" responses

When the user replies "all yes" or "go for it" to a multi-item plan, treat every plan item as binding. Do not silently drop or defer items mid-execution. If new information makes one item infeasible, surface it before continuing.

### Compliance checklist

- [ ] Every issue from the user's original list has a verifiable fix in this commit
- [ ] No "TODO" / "later" / "lower priority" markers added to staged code or comments
- [ ] Each fix has been verified (test run, manual check, log inspection — depending on type)
- [ ] If any item couldn't be resolved, it's surfaced explicitly to the user, not buried

---

## GOLDEN RULE #14: Memory Recall at Task Start

**Query Vestige memory at the start of every non-trivial task — not only at session start. Project-specific rules in memory override Claude Code defaults, but only if they get queried in time.**

### The pattern this prevents

A rule was saved to Vestige (e.g. "no Co-Authored-By trailers in this repo"). Session-start recall pulled identity-related memories but not commit-style rules. When composing a commit, the agent fell back to the global Claude Code default and added the trailer — violating a rule that was sitting in memory with retention 0.95+. Fix: query memory at the moment the task starts, not just at session start.

### When to query memory (non-exhaustive)

| Task | Suggested query |
|---|---|
| Composing a commit message | `commit style attribution trailer message format Prix Six` |
| Replying to a bug report | `<feature area> bug fix lesson Prix Six` |
| Starting work on an unfamiliar module | `<module name> rule pattern Prix Six` |
| Executing a destructive Firestore op | `destructive operation safety pattern Prix Six` |
| Adding a Cloud Function | `Cloud Functions deploy pattern Prix Six` |
| Running CI / build commands | `build environment local Prix Six` |

### How to query

```
mcp__vestige__search query="<topic words> Prix Six"
mcp__vestige__search query="prixsix <feature> rule"
```

Two queries with slightly different wording is safer than one — Vestige uses hybrid keyword+semantic search, and a single bad keyword can miss a relevant memory.

### Application

The `/commit` skill GATE 0 (added 2026-05-06) implements this rule for commits specifically. Other skills should follow the same pattern at their entry point.

### Compliance checklist

- [ ] Before starting a non-trivial task, queried Vestige with task-relevant terms
- [ ] Surfaced any project-specific rules that override global defaults
- [ ] Applied those rules when composing the response or code change
- [ ] If memory and source-of-truth file (CLAUDE.md, skill file) disagree, source-of-truth wins; flag the discrepancy

---

## GOLDEN RULE #15: Validators Derive From Data

**Validators must compute expected values from data, not hardcode them. Hardcoded constants in validators ossify and contradict reality.**

### The pattern this prevents

`lib/consistency.ts` had `if (RaceSchedule.length !== 24)` — but the 2026 calendar dropped to 22 (Bahrain & Saudi cancelled). The hardcoded 24 became a perpetual false-positive warning. Worse, the next agent (me) almost added the cancelled races back to align the data with the validator before reading the data file's leading comment.

### The rule

| Anti-pattern | Right approach |
|---|---|
| `if (X.length !== 24)` | `const expected = X.expected ?? X.length; if (X.length !== expected)` |
| Hardcoded "expected count of N" in validator | Derive from a constant exported alongside the data |
| Validator says "missing X" without checking the data file's comments | Validator reads the data file's metadata or comment-derived expected state |

### Default fix direction

When validator and data disagree:
1. **Read the data file's leading comment first.** Often it explains why the count/shape is what it is.
2. **Default fix: align the validator with the data.** Validators ossify into hardcoded expectations that lag reality.
3. **Only modify data if the validator's expectation is genuinely correct** and the data is genuinely wrong.
4. **Add a comment** to the validator explaining where the expected value comes from, so the next reader doesn't repeat the cycle.

### Compliance checklist

- [ ] No hardcoded numeric expectations in validators (counts, sizes, lengths)
- [ ] If a constant must be hardcoded, it's exported from the data module, not inlined in the validator
- [ ] Each validator has a comment explaining how its expected value is determined
- [ ] When validator + data disagree, the data file's comments were read before "fixing"

---

## GOLDEN RULE #16: Type-Safe Storage Boundaries

**Never trust TypeScript types about Firestore data. Coerce values via safe helpers before any operation that throws on bad input.**

### Why TypeScript types lie

Firestore data flows through code that:
- Reads docs written by older versions of the schema
- Reads docs written by manual scripts with different shapes
- Reads docs where some fields are missing entirely
- Has Firestore Timestamp objects that look like JS objects, not strings, despite TS declaring `string`

Critical fact: **`new Date(invalidInput)` does NOT throw — it returns an Invalid Date** whose `.getTime()` is `NaN`. Downstream calls like `format(invalidDate, 'yyyy-MM-dd')` or `.toISOString()` then throw `RangeError: Invalid time value` and crash the entire render path.

### Required pattern

```typescript
function safeDate(input: any): Date | null {
  if (input == null) return null;
  if (typeof input === 'object') {
    const secs = input.seconds ?? input._seconds;
    if (typeof secs === 'number') {
      const d = new Date(secs * 1000);
      return isNaN(d.getTime()) ? null : d;
    }
    if (typeof input.toDate === 'function') {
      try {
        const d = input.toDate();
        return d instanceof Date && !isNaN(d.getTime()) ? d : null;
      } catch { return null; }
    }
    return null;
  }
  if (typeof input === 'string' || typeof input === 'number') {
    const d = new Date(input);
    return isNaN(d.getTime()) ? null : d;
  }
  return null;
}
```

The reference implementation lives in `app/src/app/(app)/admin/_components/ErrorLogViewer.tsx` (added v3.1.1 to fix `err_mou1bkbm_29343000`).

### Equivalent helpers needed for other types

| Type to coerce | Helper | Risk if not used |
|---|---|---|
| Date / Timestamp | `safeDate(input)` | `format()` throws Invalid time value |
| String | `String(input ?? '')` | `.toUpperCase()` throws on numbers (PX-9001 v2.5.4) |
| JSON parse | `safeJsonParse(input)` | `JSON.parse(undefined)` throws |
| Array | `Array.isArray(input) ? input : []` | `.forEach` / `.map` throws on non-arrays |

### When to apply

Any time data flows from Firestore (`useDoc`, `useCollection`, `db.collection()...get()`) into:
- Date constructors
- String methods (`.toUpperCase()`, `.toLowerCase()`, `.includes()`)
- `JSON.parse()`
- `.length` / `.forEach` / `.map` accesses

The TS type declaration is a hint, not a guarantee. **Coerce, then operate.**

### Compliance checklist

- [ ] No `new Date(firestoreField)` calls without `safeDate()` wrapping
- [ ] No `.toUpperCase()` / `.toLowerCase()` on Firestore data without `String(input ?? '')` coercion
- [ ] No `.forEach`/`.map`/`.length` on Firestore array fields without `Array.isArray` guard
- [ ] TypeScript type declarations on Firestore-sourced types reflect ALL real shapes (e.g. `string | { seconds: number }` not just `string`)

---

## GOLDEN RULE #17: Silent Failure Detection

**Every Cloud Function with a user-visible status field MUST have automated freshness monitoring. Silent OOM, network errors, or undeployed functions can hide for weeks otherwise.**

### The pattern this prevents

`runRecoveryTest` OOMed every Sunday for 6+ weeks (since 2026-03-22). The function appeared `ACTIVE` in `gcloud functions list`. Each scheduled run executed, ran auth-verify, started the storage check, then died at "Memory limit of 512 MiB exceeded" — **before** writing `lastSmokeTestTimestamp`. The dashboard showed "Last smoke test: 2026-03-22" for 6 weeks. Nobody noticed because nobody had a freshness monitor.

### The rule

For any scheduled Cloud Function whose status is shown in the admin dashboard:

1. The function must write a timestamp to a known Firestore field on every successful run
2. `/health-check` CHECK 11 must include that field with an expected max age (schedule + grace)
3. The admin dashboard must display the timestamp prominently AND show a "stale" indicator if it's beyond expected
4. If the function fails (OOM or otherwise), the failure path should ALSO write a heartbeat (e.g. via structured log) so monitoring can distinguish "ran and failed" from "didn't run"
5. **AMENDMENT (Aaron, 2026-07-26 — BUG-SMOKE-001):** every health/monitoring FAILURE must ALSO
   write a registry-coded entry to `error_logs` (the admin's single pane of glass). A status doc
   or dashboard badge alone does not count — the smoke test was broken for 4 months because its
   failures only touched `backup_status`. Functions-side helper: `writeErrorLog` in
   `functions/index.js` (BACKUP_FUNCTIONS-032), shape-compatible with the app's `logError`.
   Additionally, order matters: write the result status BEFORE any non-essential cleanup, so a
   cleanup crash can never erase the record; and never page-delete Firestore with
   `limit(N).get()` on fat collections — use refs-only `recursiveDelete`.

### Required pattern

```javascript
exports.someScheduledFn = onSchedule({...}, async () => {
  try {
    // ... do work ...
    await writeStatus(db, {
      lastSomeFnTimestamp: Timestamp.now(),
      lastSomeFnStatus: 'SUCCESS',
      // ... other diagnostic fields ...
    });
    console.log(JSON.stringify({ severity: 'INFO', message: 'SOMEFN_HEARTBEAT', timestamp: ... }));
  } catch (err) {
    // Heartbeat on failure too — distinguishes "ran and failed" from "didn't run"
    console.log(JSON.stringify({ severity: 'ERROR', message: 'SOMEFN_HEARTBEAT', error: err.message }));
    throw err; // re-throw so Cloud Functions marks invocation as failed
  }
});
```

### Compliance checklist

- [ ] Every new scheduled function writes a `lastXTimestamp` field to a known status doc on success
- [ ] Failure paths also emit a heartbeat (structured log AND/OR a `lastXStatus: 'FAILED'` doc field)
- [ ] `/health-check` CHECK 11 includes the new field with appropriate `maxAgeH`
- [ ] Admin dashboard displays the timestamp + stale indicator
- [ ] Memory provisioning includes ≥30% headroom for "all of X" datasets that grow over time

---

## GOLDEN RULE #18: Migration Dead-Code Audit

**When eliminating a collection, field, or feature, audit for orphaned readers/validators in the SAME commit, not a follow-up. The audit is part of the migration, not a chore that comes later.**

### The pattern this prevents

SSOT-001 eliminated the `scores` Firestore collection in favour of real-time computation from `race_results × predictions`. The migration changed how scores were produced — but **left an orphaned validator** in `lib/consistency.ts` that still tried to read the eliminated collection. Result: 163 false-positive CC warnings every run, every day, for ~6 weeks until v3.1.3 caught it.

### The rule

When a commit eliminates a collection, field, or feature:

1. **Grep for all readers** in the same session:
   ```bash
   grep -rn "collection('<eliminated>')" app/src
   grep -rn "<eliminatedField>" app/src
   ```
2. **Check skills and validators** that may reference the old shape:
   ```bash
   grep -rn "<eliminated>" app/src/lib/consistency.ts
   grep -rn "<eliminated>" .claude/commands/
   ```
3. **Remove or rewrite each finding** in the same commit — not a follow-up
4. **Document the elimination** in CHANGELOG.md and as a `@REMOVED` comment in any non-trivial removal so future readers know the history

### Specific risk areas

- Validators in `lib/consistency.ts` (163-warning case)
- Type interfaces that still declare the eliminated field (results-utils.tsx `Score` interface)
- API routes that still write to the eliminated collection (signup/route.ts late-joiner-handicap → `scores`)
- Skill files that still mention the eliminated thing (`/cc`, `/check-race-data`)
- Backup/restore scripts that still expect the old shape

### Compliance checklist

- [ ] All readers of the eliminated thing identified via grep
- [ ] All readers either updated or explicitly removed in this commit
- [ ] No type interfaces still declare the eliminated fields as required
- [ ] Skills and validators that mention the eliminated thing are updated
- [ ] Migration commit message lists the readers found and what was done with each
- [ ] Follow-up audit memory written if any reader was deferred (rare; should usually be in scope)

---

## GOLDEN RULE #19: Cloud Functions Deploy Bundling

**Cloud Functions are NOT auto-deployed by App Hosting on push to main. Every commit changing `functions/` MUST end with the bundled `firebase deploy --only functions:...` command including ALL pending function changes from prior commits.**

### Why this exists

App Hosting deploys via push to main. Cloud Functions deploy only via manual `firebase deploy --only functions:<name>`. The two are independent. If you forget the manual deploy, your code is in git but your functions are still running the old version.

The Prix Six 2026-05-06 session accumulated FOUR pending function deploys across two commits before noticing — `applyBackupRetention` (v3.1.2) plus three changes in v3.1.5. Easy to lose track.

### The rule

Every commit message body that touches `functions/` MUST end with:

```
REQUIRES MANUAL DEPLOY: firebase deploy --only functions:<name1>,functions:<name2>,...
```

The deploy command MUST include:
- All functions changed in **this** commit
- All functions changed in any **prior commit** that doesn't have a confirmed deploy

### Audit prior commits

Before composing the commit message, run:

```bash
git log --oneline -10 -- functions/index.js
git log -10 --pretty=format:"%H%n%s%n%b%n---" -- functions/index.js | grep -E "firebase deploy --only functions" | head -5
```

For each prior commit's listed deploy command, ask: **was that deploy actually run?** If you can't confirm yes (e.g. via Cloud Function logs showing recent invocations of the new code), include those function names in the new bundled command.

### Two distinct deploys per release

When reporting "vX.Y.Z is live" after a release that includes function changes, ALWAYS distinguish:

> ✅ App Hosting (push triggered) — vX.Y.Z live at /api/version
> ⚠ Cloud Functions — pending manual deploy: `firebase deploy --only functions:<list>`

Don't claim full deploy until both have happened.

### Compliance checklist

- [ ] If `functions/` files in staged changes, commit message body ends with `firebase deploy --only functions:...`
- [ ] Bundled command includes any prior pending function deploys (audited via `git log` of `functions/`)
- [ ] Release status reporting distinguishes App Hosting from Cloud Functions deploys
- [ ] If user has not run the deploy command after the commit landed, flag it explicitly in the next session's status report

---

*See also: `CLAUDE.md` for the one-line summaries of all 19 rules.*
*Audit history: `docs/gr-audit-2026-05-06.md` documents the bug-cross-reference that surfaced rules #14-#19.*
