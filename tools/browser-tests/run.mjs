#!/usr/bin/env node
// Browser test runner: build, boot a throwaway TimerPi, run tests/*.mjs.
import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..', '..');
const work = mkdtempSync(join(tmpdir(), 'tp-browser-'));
const bin = join(work, 'timerpi');

const freePort = () => new Promise((res) => {
  const s = createServer().listen(0, () => { const { port } = s.address(); s.close(() => res(port)); });
});

execFileSync('go', ['build', '-o', bin, '.'], { cwd: repo, stdio: 'inherit' });
const port = await freePort();
const base = `http://127.0.0.1:${port}`;
const server = spawn(bin, [], {
  cwd: work, // no templates/public here: proves the embedded copy is served
  env: { ...process.env, TIMERPI_DATA_DIR: join(work, 'data'), TIMERPI_HTTP_PORT: String(port), TIMERPI_DISPLAY: 'off' },
  stdio: ['ignore', 'ignore', 'pipe'],
});
let serverErr = '';
server.stderr.on('data', (d) => { serverErr += d; });
for (let i = 0; i < 50; i++) {
  try { if ((await fetch(base + '/health')).ok) break; } catch { /* booting */ }
  await new Promise((r) => setTimeout(r, 100));
}

const browser = await chromium.launch(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {});
const filter = process.argv[2] || '';
let failures = 0;
let seq = 0;
for (const file of readdirSync(join(here, 'tests')).filter((f) => f.endsWith('.mjs') && f.includes(filter)).sort()) {
  const mod = await import(join(here, 'tests', file));
  const t = {
    browser, base,
    check(label, cond) {
      if (cond) console.log(`  ok: ${label}`);
      else { failures++; console.log(`  FAIL: ${label}`); }
    },
    // A fresh event with one room; ctx is signed in as its SuperOperator.
    async newEvent(rooms = ['Room A']) {
      const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
      const H = { Origin: base };
      const ev = await (await ctx.request.post(base + '/api/events', { data: { name: `Test ${++seq}`, password: 'test-pw1', rooms }, headers: H })).json();
      const lobby = await (await ctx.request.get(base + '/api/events/' + ev.code)).json();
      return { ctx, H, event: ev.code, rooms: lobby.event.rooms.map((r) => r.code), room: lobby.event.rooms[0].code };
    },
  };
  console.log(`${file}: ${mod.name}`);
  try {
    await mod.run(t);
  } catch (e) {
    failures++;
    console.log(`  FAIL: threw ${e.stack || e}`);
  }
}
await browser.close();
server.kill();
rmSync(work, { recursive: true, force: true });
if (failures) {
  console.log(`\n${failures} failure(s)`);
  if (serverErr) console.log('server log tail:\n' + serverErr.split('\n').slice(-15).join('\n'));
}
process.exit(failures ? 1 : 0);
