/**
 * PostToolUse hook — fires after every Bash command.
 * When the command was a git commit, outputs a reflection prompt that
 * Claude must respond to before continuing work.
 *
 * Born from 2026-03-24: 20+ commits across 2 days with repeated violations
 * of Golden Rules, bypassed safety hooks, speculative fixes, and no learning
 * loop. This hook forces a pause-and-reflect after every commit.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." }, ... }
 * Returns text on stdout that becomes part of the conversation context.
 */

let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => { input += chunk; });
process.stdin.on('end', () => {
  try {
    const data = JSON.parse(input);
    const command = data?.tool_input?.command ?? '';

    // Only trigger on git commit commands
    if (!command.includes('commit') || !command.includes('git')) {
      process.exit(0);
    }

    // Check if the commit actually succeeded (tool_output contains commit hash)
    const output = data?.tool_output?.stdout ?? data?.tool_output ?? '';
    if (typeof output === 'string' && !output.includes('[main ') && !output.includes('[develop ')) {
      // Commit didn't succeed — don't reflect on a failed commit
      process.exit(0);
    }

    const reflection = `
<user-prompt-submit-hook>
🪞 POST-COMMIT REFLECTION — Answer these before continuing:

1. **Golden Rules check:**
   - GR#2: Did you bump the version? Are package.json and version.ts in sync?
   - GR#6: Are there new GUIDs that need registering in code.json?
   - GR#7: Any new error handling that should use the error registry?
   - GR#11: Any security implications in what you just committed?

2. **Memory & learning:**
   - Did you learn anything this commit that should be saved to memory or Vestige?
   - Did you make a mistake earlier that this commit corrects? If so, is the lesson captured?
   - Is there a pattern here worth remembering for next time?

3. **User relationship:**
   - What is the user's current tone? (satisfied / neutral / frustrated / angry)
   - If frustrated or angry — what caused it and what should you do differently?
   - Are you putting work on the user that you could do yourself?

4. **Quality check:**
   - Did you confirm the root cause before pushing, or is this speculative?
   - Did you test with Puppeteer or local simulation before pushing?
   - Could you have combined this with the previous commit to save a build?

5. **Artefacts:**
   - Does code.json need updating?
   - Does CHANGELOG.md need a new entry?
   - Does MEMORY.md or any memory file need updating?
   - Should the /diagnose skill be updated with new learnings?

Answer briefly (2-3 words per item is fine). Skip items that clearly don't apply.
If ALL items are clean, just say "✅ Reflection clean — no actions needed."
</user-prompt-submit-hook>`;

    process.stdout.write(reflection);
    process.exit(0);

  } catch {
    process.exit(0);
  }
});
