// claude-statusline.js — Status line script
// Reads identity file + stdin JSON, outputs "name> [Model] dir"
const fs = require('fs');
const path = require('path');

let input = '';
process.stdin.setEncoding('utf-8');
process.stdin.on('data', chunk => { input += chunk; });
process.stdin.on('end', () => {
  let name = '???';
  let model = 'Claude';
  let dir = '';

  try {
    const data = JSON.parse(input);
    model = data.model?.display_name || 'Claude';
    dir = path.basename(data.workspace?.current_dir || '');

    // Read identity: per-window file first (multi-window safe — the shared
    // .identity is last-checkin-wins and can show ANOTHER window's name),
    // then the legacy shared file as fallback.
    const projectDir = data.workspace?.project_dir || __dirname;
    const dotClaude = path.join(projectDir, '.claude');
    const candidates = [];
    if (data.session_id) candidates.push(path.join(dotClaude, `.identity-${data.session_id}`));
    candidates.push(path.join(dotClaude, '.identity'));
    for (const p of candidates) {
      try { name = fs.readFileSync(p, 'utf-8').trim(); break; } catch {}
    }
  } catch {}

  process.stdout.write(`${name}> [${model}] ${dir}`);
});
