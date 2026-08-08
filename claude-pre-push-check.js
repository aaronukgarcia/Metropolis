/**
 * PreToolUse hook — Prix Six pre-push function-deploy bundling check (GR#19).
 *
 * Catches `git push origin main` commands and inspects the commits being
 * pushed. If any commit touches `functions/` files, the message body must
 * contain a `firebase deploy --only functions:` line. If missing, the hook
 * blocks the push and instructs the user to either:
 *   (a) amend the commit message with the bundled deploy command, or
 *   (b) acknowledge by setting CLAUDE_DISABLE_PUSH_CHECK=1.
 *
 * Fail-graceful: ANY parse error or git failure → exit 0 silently.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 */

'use strict';
const { execSync } = require('child_process');

let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => { input += chunk; });
process.stdin.on('end', () => {
  try {
    if (process.env.CLAUDE_DISABLE_PUSH_CHECK === '1') {
      process.exit(0);
    }

    const data = JSON.parse(input);
    const command = data?.tool_input?.command ?? '';

    // Only intercept push to main
    if (!command.includes('git push')) {
      process.exit(0);
    }
    if (!command.includes('main') && !command.includes('origin')) {
      process.exit(0);
    }
    // Skip force-push (assume the user knows what they're doing)
    if (command.includes('--force') || command.includes('-f ')) {
      process.exit(0);
    }

    // Find what's about to be pushed: commits on local main that aren't on origin/main
    let pendingCommits = '';
    try {
      pendingCommits = execSync('git log origin/main..HEAD --pretty=format:%H', {
        encoding: 'utf8',
        timeout: 5000,
      }).trim();
    } catch {
      // Repo state unclear — don't block
      process.exit(0);
    }

    if (!pendingCommits) {
      process.exit(0); // nothing to push
    }

    const commitHashes = pendingCommits.split('\n').filter(Boolean);
    const offendingCommits = [];

    for (const hash of commitHashes) {
      // Did this commit touch functions/?
      let filesChanged = '';
      try {
        filesChanged = execSync(`git show ${hash} --name-only --pretty=format:`, {
          encoding: 'utf8',
          timeout: 5000,
        });
      } catch {
        continue; // skip on error
      }
      const touchesFunctions = filesChanged.split('\n').some(f => f.startsWith('functions/'));
      if (!touchesFunctions) continue;

      // Get full message body
      let messageBody = '';
      try {
        messageBody = execSync(`git show ${hash} --pretty=format:%B --no-patch`, {
          encoding: 'utf8',
          timeout: 5000,
        });
      } catch {
        continue;
      }

      // Does the body contain a firebase deploy --only functions line?
      const deployRegex = /firebase\s+deploy\s+--only\s+functions/i;
      if (!deployRegex.test(messageBody)) {
        offendingCommits.push({ hash: hash.slice(0, 8), msg: messageBody.split('\n')[0] });
      }
    }

    if (offendingCommits.length === 0) {
      process.exit(0);
    }

    // Block with an instructional message
    const list = offendingCommits.map(c => `   ${c.hash} — ${c.msg}`).join('\n');
    const reason = '🛑 GOLDEN RULE #19: Cloud Functions deploy command missing.\n' +
      '\n' +
      'The following commits touch functions/ but their messages don\'t bundle a\n' +
      'firebase deploy --only functions:... command:\n' +
      list + '\n' +
      '\n' +
      'Cloud Functions are NOT auto-deployed by App Hosting. Each commit changing\n' +
      'functions/ MUST end with the bundled deploy command including ALL pending\n' +
      'function changes from prior commits.\n' +
      '\n' +
      'Fix: amend the offending commit messages with the deploy command, OR\n' +
      'acknowledge by setting CLAUDE_DISABLE_PUSH_CHECK=1 (e.g. for pure docs\n' +
      'commits where no actual function code changed).';

    const output = JSON.stringify({
      hookSpecificOutput: {
        hookEventName: 'PreToolUse',
        permissionDecision: 'deny',
        permissionDecisionReason: reason,
      },
    });
    process.stdout.write(output);
    process.exit(0);

  } catch (err) {
    // Any unexpected error — don't block
    process.exit(0);
  }
});
