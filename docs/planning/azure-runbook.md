# Azure metroserve runbook — FEAT-2326609775 inc1

**Status:** inc1 deliverable 6. Nothing in this file has been executed against
Azure — it is the reviewed procedure the lead/Aaron run, not something a build
lane executes (this session was explicitly instructed not to create or mutate
any Azure resource).

**Read first:** `docs/planning/azure-cloud-engine-design.md` (the design), and
`node claude-bow.js show FEAT-2326609775` for Aaron's rulings (£20/month hard
cap, shadow-first, turbo 6.25 re-ratified).

---

## 1. The £20/month hard cap — Aaron's actual ruling (2026-09-04)

The design doc's own §3.3 says it plainly: **Azure Budgets alert; they do not
stop.** The only real hard stop on pay-as-you-go is an automation runbook that
disables the app. Aaron's ruling (BOW comment, 2026-09-04) is **£20/month**,
tighter than the design doc's own §8 estimate table (~£2 at inc1, ~£33-71 at
inc4) — inc4-scale spend is explicitly OUT of budget under this cap until
Aaron revisits it. This section is the actual disable mechanism; §2 is the
alert.

### 1.1 One-command disable (run this FIRST, before anything else, so it's proven working)

```bash
az containerapp update \
  --name metropolis-metroserve \
  --resource-group rg-metropolis-dev \
  --min-replicas 0 --max-replicas 0
```

Setting `max-replicas 0` stops the Container App from running ANY replica —
this is the actual spend stop (scale-to-zero already means idle cost is near
nothing, but a runaway is a replica that never goes idle, e.g. a crash-loop
that keeps restarting, or a client that never disconnects and pins the
container warm). Re-enable with:

```bash
az containerapp update \
  --name metropolis-metroserve \
  --resource-group rg-metropolis-dev \
  --min-replicas 0 --max-replicas 1
```

**Verify the disable actually took effect** (GR#1 — do not trust the command's
exit code alone):

```bash
az containerapp show \
  --name metropolis-metroserve \
  --resource-group rg-metropolis-dev \
  --query "{minReplicas:properties.template.scale.minReplicas, maxReplicas:properties.template.scale.maxReplicas, activeRevisionsMode:properties.configuration.activeRevisionsMode}" \
  -o table
curl -s -o /dev/null -w "%{http_code}\n" https://<fqdn>/health
# expect a non-200 within a minute or so once existing replicas drain
```

### 1.2 Automation runbook — an Azure Monitor action group + Logic App / Function that calls 1.1 automatically

This is the piece the design doc flags as "~20 lines, and the difference
between a £2 month and a £500 surprise." Concretely (Azure Automation Account
+ PowerShell runbook is the lowest-ceremony option, since this repo already
has zero Azure Functions infrastructure to stand up):

```powershell
# runbooks/Disable-MetroserveOnBudgetAlert.ps1 (create in an Azure Automation
# Account, triggered by the Action Group in §2 below — NOT checked into this
# repo, since it is Azure-side infrastructure-as-config, not application code)
Connect-AzAccount -Identity
Update-AzContainerApp `
  -ResourceGroupName "rg-metropolis-dev" `
  -Name "metropolis-metroserve" `
  -TemplateScaleMinReplica 0 `
  -TemplateScaleMaxReplica 0
Write-Output "metroserve disabled by budget alert at $(Get-Date -Format o)"
```

The Automation Account needs a **system-assigned managed identity** with the
`Container Apps Contributor` role scoped to `rg-metropolis-dev` ONLY (never
subscription-wide — the same least-privilege posture as the GitHub OIDC
credential in `.github/workflows/azure-deploy.yml`).

### 1.3 Verification (run once, immediately after standing up 1.2)

**Do not trust an untested kill switch.** Before relying on this runbook:

1. Manually trigger the Automation runbook (`Start-AzAutomationRunbook`, or
   the Portal's "Start" button) — NOT by actually blowing the budget.
2. Confirm `max-replicas` drops to 0 (the `az containerapp show` query above).
3. Confirm `/health` stops responding within a reasonable window.
4. Re-enable (§1.1's second command) and confirm `/health` comes back.
5. Record the date this was verified in this file's changelog (§5) — an
   unverified kill switch is worse than none, because it creates false
   confidence.

---

## 2. Azure Budget + alerts (the warning, not the stop)

```bash
az consumption budget create \
  --budget-name metropolis-metroserve-monthly \
  --amount 20 \
  --category cost \
  --time-grain monthly \
  --start-date $(date -u +%Y-%m-01) \
  --end-date 2030-01-01 \
  --resource-group rg-metropolis-dev \
  --notifications '{
    "Actual_50": {"enabled": true, "operator": "GreaterThan", "threshold": 50, "contactEmails": ["<aaron-email>"]},
    "Actual_80": {"enabled": true, "operator": "GreaterThan", "threshold": 80, "contactEmails": ["<aaron-email>"]},
    "Actual_100": {"enabled": true, "operator": "GreaterThan", "threshold": 100, "contactEmails": ["<aaron-email>"], "contactGroups": ["<action-group-resource-id-from-1.2>"]}
  }'
```

The 100% threshold's `contactGroups` entry is what fires the Action Group
that runs §1.2's runbook — the alert and the stop are wired together at this
one point. **Verify the wiring, not just each half in isolation.**

---

## 3. Pre-deploy checklist (run through this before the FIRST real dispatch of `.github/workflows/azure-deploy.yml`)

- [ ] `rg-metropolis-dev` resource group exists (or the workflow's first run
      creates it — confirm the region is `uksouth`).
- [ ] `AZURE_CLIENT_ID` / `AZURE_TENANT_ID` / `AZURE_SUBSCRIPTION_ID` GitHub
      Actions **variables** are set (Settings -> Secrets and variables ->
      Actions -> Variables), pointing at an App Registration with a
      federated credential scoped to this repo + the `azure-metroserve`
      GitHub Environment (matching the workflow's `environment:` key) — NOT
      a client secret (Q100142: OIDC, no long-lived secret in a public repo).
    - App Registration RBAC: `Contributor` (or narrower) on `rg-metropolis-dev`
      ONLY.
- [ ] §1's kill switch is stood up AND verified (§1.3) BEFORE the first
      deploy — GR#27's spirit ("no wipe without a proven capture path")
      applies here as "no spend without a proven stop path."
- [ ] §2's Budget + alert is created.
- [ ] The Container Apps secret `metroserve-shared-secret` is set to a REAL
      random value before the first `azure-deploy.yml` create-path run — the
      workflow's `--secrets` line ships a placeholder
      (`REPLACE-BEFORE-FIRST-RUN`) that MUST be overwritten:
      ```bash
      az containerapp secret set \
        --name metropolis-metroserve --resource-group rg-metropolis-dev \
        --secrets metroserve-shared-secret=$(openssl rand -hex 32)
      ```
      Record the value in a password manager, not in this repo (it is a
      runtime secret, not source).
- [ ] Actually run `.github/workflows/azure-deploy.yml` via
      `gh workflow run azure-deploy.yml -f city=dogfood` (workflow_dispatch
      only — it is NOT wired to push/PR, see that file's own header comment
      for why).
- [ ] Run the smoke test against the deployed FQDN (the workflow's job
      summary prints the exact command):
      ```bash
      node tools/azure/smoke.mjs \
        --health-url https://<fqdn>/health \
        --ws-url wss://<fqdn>/ws \
        --secret <the-real-secret-from-the-step-above>
      ```
      Confirm PASS against round-trip p95 < 100ms.
- [ ] Isolate journal-append latency (design doc §6.5 step 5): run the smoke
      test a SECOND time against a throwaway LOCAL `metroserve -persist-dir ""`
      instance (persistence off) and diff the two `round-trip (full)` p95
      numbers — the difference is journal-append's actual contribution,
      since a single external client cannot measure server-internal fsync
      timing directly (see `tools/azure/smoke.mjs`'s own header comment for
      why this is a proxy, not a direct measurement, in inc1).
- [ ] Kill-recovery (design doc §6.5 step 6):
      ```bash
      az containerapp revision restart --name metropolis-metroserve --resource-group rg-metropolis-dev
      ```
      then re-run the smoke test and confirm the reported city tick did not
      regress (§4 below covers the observed cold-start budget).

---

## 4. Known characteristics to expect (not surprises)

- **The data/ catalogue ships INSIDE the image, at `/data-src`, never at
  `/data`.** `data/errors.json` (the whole GR#7 error registry) and every
  other `data/*.json` catalogue file (`consumption.json`, `buildings.json`,
  etc.) are resolved by cmd/metroserve at process start via a filesystem
  search that a container's isolated filesystem cannot satisfy on its own
  (found by the r1 destructive round, 2026-09-04 — see the Dockerfile's own
  regression-class comment above the `COPY --from=builder /src/data
  /data-src` line). The Dockerfile bakes `METROPOLIS_DATA_DIR=/data-src` and
  `METROPOLIS_ERRORS_PATH=/data-src/errors.json` into the image so this
  never needs operator action — but if you ever hand-roll a
  `docker run`/`az containerapp create` command that overrides the image's
  `ENV` (some deploy tooling does), do NOT let it silently drop these two.
  Symptom if it happens: EITHER the container crash-loops with `MET-G801`/
  `MET-F600` in its logs (no `data/` tree reachable at all) OR it boots but
  every single error it raises — including this increment's own
  `MET-P040`/`MET-P041`/`MET-P042` — collapses to the generic `MET-F001`
  "error registry failed to load" fallback instead of its real, registered
  message (a live GR#1/GR#7 hole that looks fine in a shallow smoke test
  because *an* error still comes back, just the wrong one).
- **Cold start:** `min-replicas 0` means the FIRST request after idle pays a
  cold start (image pull if not cached on the node, plus the journal-replay
  rehydrate cost — O(commands) per persist.go's own doc comment). Expect
  several seconds, not milliseconds, on that first hit; this is why the
  smoke test's `/health` p50/p95 is measured over 20 requests, not one.
- **`max-replicas` is fixed at 1** (design doc §1.4 — no file lock/ownership
  lease on the store yet, and `wsserver` serves one live WS connection per
  city). Do NOT raise it before FEAT-2326609775 inc4's ownership-lease work
  lands, or two replicas WILL corrupt the same city's journal.
- **The shared secret has two delivery paths** — an HTTP header
  (`X-Metroserve-Secret`) for non-browser clients, and a `?secret=` query
  parameter for the webconsole's browser client (browsers cannot set custom
  headers on the WebSocket handshake, RFC 6455 §4.1) — see
  `cmd/metroserve/portknock.go`'s `SharedSecretQueryParam` doc comment.
  **A query-string secret is logged by anything that logs full URLs**
  (some CDNs/proxies, browser history). Container Apps' own access logs are
  the concrete exposure to check before treating this as fully private —
  proportionate for Aaron-only access (Q100025/Q100144) but not once a
  second person has the link (Q100144's own trigger for real auth).
- **The build-string handshake gate (`MET-P010`) is UNCHANGED by this
  increment.** A deploy of ANY new commit locks out every open browser tab
  running the old build — Q100145 is still open. Warn Aaron before the
  first real deploy if he has a tab open against a local dev server built
  from a different commit than what's being deployed (they are different
  processes/ports so this is only a concern if he later points a local tab
  at the cloud URL).

---

## 5. Changelog

- 2026-09-04 (Bev's build lane, inc1): runbook authored. Kill-switch NOT yet
  stood up or verified (§1.3) — first real Azure action for the lead/Aaron.
