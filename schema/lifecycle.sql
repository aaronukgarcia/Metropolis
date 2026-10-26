-- lifecycle.sql - GGR65 dev lifecycle tables (Aaron, 2026-09-24). Forge asset schema.safe.lifecycle.
-- Sits beside the Metropolis BOW tables (bow_items, bow_dependencies, bow_comments, bow_git_refs); defects and
-- assumptions are bow_comments tagged with role and checkpoint. These two tables add what BOW lacks:
-- the frozen AC hash, and an append-only log of every state transition with its evidence.
CREATE TABLE IF NOT EXISTS ac_freeze (
  bow_code    VARCHAR(40)  NOT NULL PRIMARY KEY,
  sha256      CHAR(64)     NOT NULL,           -- hash of the AC text at the accepted transition
  frozen_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  frozen_by   VARCHAR(40)  NOT NULL,           -- identity of the AC Reviewer that accepted it
  spec        VARCHAR(300) NULL                -- <file>#<requirement id> the AC block was hashed from (L.R4, LD10)
);
CREATE TABLE IF NOT EXISTS lifecycle_event (
  id           BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  bow_code     VARCHAR(40)  NOT NULL,
  from_state   VARCHAR(40)  NOT NULL,
  to_state     VARCHAR(40)  NOT NULL,
  checkpoint   VARCHAR(20)  NULL,              -- CP-spec, CP-review, CP-dev, CP-T1..CP-T4, CP-verdict, CP-milestone, or SKIP (Aaron's recorded waiver)
  by_identity  VARCHAR(40)  NOT NULL,
  role         VARCHAR(20)  NOT NULL,          -- architect, reviewer, developer, t1, t2, t3, t4, verdict, aaron
  evidence_ref TEXT         NULL,              -- test id, output path, commit sha, or Aaron's answer
  at           TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  -- L.R4 / LD9 (2026-10-23): written only by tools/lifecycle-state.js; `lifecycle-state.js migrate` adds these to an existing table
  result       ENUM('PASS','FAIL') NULL,
  revision     CHAR(40)     NULL,              -- THE REVISION: tip of lc/<CODE>/developer a gate result belongs to
  legacy       TINYINT      NOT NULL DEFAULT 0, -- 1 = row written before LD9 (the old state name verified displays as done)
  project      VARCHAR(120) NULL,
  session_id   VARCHAR(80)  NULL,
  identity     VARCHAR(60)  NULL,
  epic         VARCHAR(40)  NULL,
  KEY (bow_code, at)
);
CREATE OR REPLACE VIEW v_lifecycle_state AS
  SELECT e.bow_code, e.to_state AS state, e.at, e.by_identity
  FROM lifecycle_event e
  JOIN (SELECT bow_code, MAX(id) mid FROM lifecycle_event GROUP BY bow_code) m ON m.mid = e.id;
CREATE OR REPLACE VIEW v_assumption_bounces AS
  SELECT e.bow_code, COUNT(*) AS bounces FROM lifecycle_event e
  WHERE e.to_state = 'rejected(assumptions)'
    AND e.id > COALESCE((SELECT MAX(a.id) FROM lifecycle_event a WHERE a.bow_code = e.bow_code AND a.checkpoint = 'AC-AMEND'), 0)
  GROUP BY e.bow_code HAVING COUNT(*) >= 2;

-- SAFE-122 (2026-10-18): one row per dispatched sub-agent task, so token savings from tiering (SAFE-120) and brief caps
-- (SAFE-121) can be proved against a baseline. Tokens are as the harness reported them, never estimated; a field the
-- source does not give stays NULL. source=transcript rows are harvested from ~/.claude/projects/*/*.jsonl (background
-- agent launch + completion notice, joined on the agent id); source=manual rows come from `usage-ledger.js add`.
CREATE TABLE IF NOT EXISTS dispatch_usage (
  id            BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  at            DATETIME     NOT NULL,              -- completion time (UTC)
  project       VARCHAR(120) NOT NULL,              -- transcript folder, e.g. E--projects-63-SAFE
  bow_code      VARCHAR(40)  NULL,
  role          VARCHAR(20)  NULL,                  -- lifecycle role when known (developer, t1..t4, verdict, reviewer)
  tier          CHAR(2)      NULL,                  -- T0..T3 (SAFE-120)
  model         VARCHAR(60)  NULL,
  description   VARCHAR(200) NULL,
  total_tokens  INT          NULL,
  input_tokens  INT          NULL,
  cached_tokens INT          NULL,
  output_tokens INT          NULL,
  tool_uses     INT          NULL,
  duration_ms   INT          NULL,
  budget_minutes INT         NULL,                  -- SAFE-208: BUDGET line of the brief (dispatch.log)
  budget_tokens INT          NULL,
  deviation     VARCHAR(30)  NULL,                  -- MODEL-REASON | second-bounce when the model left its role table
  retries       INT          NULL,
  acceptance    ENUM('accepted','rejected','pending') NULL,
  source        ENUM('transcript','manual','dispatch-log') NOT NULL,
  source_ref    VARCHAR(200) NOT NULL,              -- transcript file + agent id, or a manual reference
  evidence_ref  TEXT         NULL,
  UNIQUE KEY (source_ref),
  KEY (at), KEY (project), KEY (bow_code)
);
