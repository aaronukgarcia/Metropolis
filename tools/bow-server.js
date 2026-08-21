#!/usr/bin/env node
// tools/bow-server.js — local web UI for the Metropolis Book of Work.
// Live-queries the metro MariaDB on every request and pops the browser.
// Usage:  node tools/bow-server.js        (port 8765, overridable via BOW_PORT)
'use strict';

const http = require('http');
const fs = require('fs');
const path = require('path');
const { exec } = require('child_process');
const mysql = require('mysql2/promise');

const PORT = +(process.env.BOW_PORT || 8765);
const TEMPLATE = path.join(__dirname, '..', 'bow-ui-template.html');

async function query() {
  const c = await mysql.createConnection({
    host: process.env.METRO_DB_HOST || 'localhost',
    port: +(process.env.METRO_DB_PORT || 3306),
    user: process.env.METRO_DB_USER || 'root',
    password: process.env.METRO_DB_PASSWORD || '',
    database: process.env.METRO_DB_NAME || 'metro'
  });

  const [items] = await c.query(
    `SELECT guid, code, mkey, seq, sprint, item_type, title, description, priority,
            milestone, status, code_path, codejson_ref, finding_class,
            created_at, updated_at, closed_at, closed_note
       FROM bow_items ORDER BY seq`
  );
  const [deps] = await c.query(
    `SELECT i.code AS item, d.code AS dep FROM bow_dependencies bd
       JOIN bow_items i ON i.guid=bd.item_guid JOIN bow_items d ON d.guid=bd.depends_on_guid`
  );
  const [verdicts] = await c.query(
    `SELECT i.code, v.verdict, v.attacker, v.note, v.created_at
       FROM bow_destructive_verdicts v JOIN bow_items i ON i.guid=v.item_guid ORDER BY v.created_at DESC`
  );
  const [comments] = await c.query(
    `SELECT i.code, c.author, c.body, c.created_at
       FROM bow_comments c JOIN bow_items i ON i.guid=c.item_guid ORDER BY c.created_at DESC`
  );
  const [refs] = await c.query(
    `SELECT i.code, r.commit_hash, r.note, r.created_at
       FROM bow_git_refs r JOIN bow_items i ON i.guid=r.item_guid ORDER BY r.created_at DESC`
  );
  await c.end();

  const depMap = {}, depOnMap = {};
  for (const d of deps) { (depMap[d.item] = depMap[d.item] || []).push(d.dep); (depOnMap[d.dep] = depOnMap[d.dep] || []).push(d.item); }

  const latestV = {};
  for (const v of verdicts) if (!(v.code in latestV)) latestV[v.code] = { verdict: v.verdict, attacker: v.attacker, at: v.created_at };

  const rows = items.map(i => ({
    code: i.code, mkey: i.mkey || '', seq: i.seq, sprint: i.sprint, type: i.item_type,
    title: i.title, description: i.description || '', priority: i.priority,
    milestone: i.milestone || '', status: i.status, path: i.code_path || '',
    cls: i.finding_class || '', created: i.created_at, updated: i.updated_at,
    closed: i.closed_at, closedNote: i.closed_note || '',
    deps: depMap[i.code] || [], dependents: depOnMap[i.code] || [],
    verdict: latestV[i.code] || null,
    comments: (comments.filter(x => x.code === i.code)).map(x => ({ author: x.author, body: x.body, at: x.created_at })),
    refs: (refs.filter(x => x.code === i.code)).map(x => ({ hash: x.commit_hash, note: x.note, at: x.created_at }))
  }));

  // Activity feed (audit log): verdicts + comments + refs + closed + created, merged, desc.
  const feed = [];
  for (const v of verdicts) feed.push({ t: 'verdict', code: v.code, text: `${v.verdict.toUpperCase()} by ${v.attacker || '?'}`, at: v.created_at });
  for (const cm of comments) feed.push({ t: 'comment', code: cm.code, text: `comment by ${cm.author || '?'}`, at: cm.created_at });
  for (const r of refs) feed.push({ t: 'commit', code: r.code, text: `commit ${(r.commit_hash || '').slice(0, 7)}`, at: r.created_at });
  for (const i of items) { if (i.closed_at) feed.push({ t: 'closed', code: i.code, text: `closed (${i.status})`, at: i.closed_at }); }
  feed.sort((a, b) => (a.at < b.at ? 1 : a.at > b.at ? -1 : 0));

  const lastUpdated = feed.length ? feed[0].at : (items.length ? items[0].updated_at : null);

  return { lastUpdated, items: rows, activity: feed.slice(0, 40) };
}

const server = http.createServer(async (req, res) => {
  if (req.url === '/favicon.ico') { res.writeHead(204); return res.end(); }
  try {
    const data = await query();
    let html = fs.readFileSync(TEMPLATE, 'utf8');
    // Escape '<' so a BOW field containing a literal "</script>" cannot
    // close the embedding <script type="application/json"> tag early (the
    // HTML tokenizer scans for that byte sequence regardless of script
    // type). Function-form replacement avoids String.replace's special
    // $-pattern handling ($&, $`, $', $$, $1..$9) being applied to BOW
    // field content that happens to contain one of those two-char sequences.
    const json = JSON.stringify(data).replace(/</g, '\\u003c');
    html = html.replace('__BOW_DATA__', () => json);
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    res.end(html);
  } catch (e) {
    res.writeHead(500, { 'Content-Type': 'text/plain; charset=utf-8' });
    res.end('BOW server error: ' + e.message);
  }
});

server.listen(PORT, '127.0.0.1', () => {
  const url = `http://localhost:${PORT}`;
  console.log(`Metropolis Book of Work → ${url}  (Ctrl+C to stop)`);
  exec(`start "" "${url}"`, (err) => { if (err) console.log(`Open ${url} manually.`); });
});
