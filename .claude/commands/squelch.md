---
description: Squelch startup memory lookups and run checkin/renew operations directly to conserve token budget
allowed-tools: Bash, Read
---

# /squelch — bypass startup memory-lookup protocol for checkin/renew operations

Use this command as a mandatory optimization gate to protect the user's API credit and token budget during routine session maintenance.

## The Squelch Protocol

When performing a check-in (`checkin`), renewal (`renew` / `ping`), or checking session status (`status`):

1. **BYPASS MEMORY PROTOCOLS:**
   - **DO NOT** read the 21KB `CLAUDE.md` file.
   - **DO NOT** query Vestige memory, recall old files, or search database indexes.
   - **DO NOT** schedule any Cron tasks or arm loop alarms.

2. **EXECUTE DIRECTLY:**
   Run the sync command immediately on the command line:
   ```bash
   node claude-sync.js checkin --any
   ```
   or
   ```bash
   node claude-sync.js renew
   ```
   or
   ```bash
   node claude-sync.js status
   ```

3. **STOP IMMEDIATELY:**
   Print the console output verbatim, report success, and terminate your loop. Do not provide preambles, summaries, or conversational filler.
