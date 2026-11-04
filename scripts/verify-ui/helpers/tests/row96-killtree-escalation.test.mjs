import assert from 'node:assert/strict';
import test from 'node:test';
import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

// F1/F2 anti-regression (reviewer ses_e611c1be9ffe): the group SIGKILL must not hinge on
// the leader's exit, and an inherited stderr pipe may not pin the journey's event loop.
// The killTree body is EXTRACTED from the subject under test, so this suite breaks the
// moment someone weakens escalation back to the leader-exit timeout path.
const REPO_ROOT = resolve(import.meta.dirname, '..', '..', '..', '..');
const SUBJECT = join(REPO_ROOT, 'scripts/verify-ui/workflows/verify-explorer-row-source-four-state.mjs');

const subjectSource = readFileSync(SUBJECT, 'utf8');
const killTreeMatch = subjectSource.match(/\/\/ EXTRACT-START:killTree\n(const killTree = async \(\) => \{[\s\S]*?\n\};)\n\/\/ EXTRACT-END:killTree/);
assert.ok(killTreeMatch, 'killTree must remain extractable (EXTRACT-START/END sentinels) for the F1 regression probe');

const STUBBORN_SRC = [
  "process.on('SIGTERM', () => {});",
  "process.on('SIGHUP', () => {});",
  'const beat = setInterval(() => {}, 1000);',
  'setTimeout(() => { clearInterval(beat); process.exit(0); }, 30000);',
  '',
].join('\n');

const LEADER_SRC = [
  "import { spawn } from 'node:child_process';",
  "import { writeFileSync } from 'node:fs';",
  "const helper = spawn(process.execPath, [process\.argv[2], process\.argv[3]], { stdio: ['ignore', 'ignore', 'inherit'] });",
  "writeFileSync(process\.argv[4], 'HELPER pid=' + helper.pid);",
  "process.on('SIGTERM', () => process.exit(0));",
  'setTimeout(() => process.exit(0), 30000);',
  '',
].join('\n');

// argv: leaderPath stubbornPath infoPath stderrMode
const CASE_HEAD = [
  "import { spawn } from 'node:child_process';",
  "import { readdirSync, readFileSync } from 'node:fs';",
  'const liveTimers = new Set();',
  'const trackedTimeout = (fn, ms) => { const id = setTimeout(() => { liveTimers.delete(id); fn(); }, ms); liveTimers.add(id); return id; };',
  'const clearTrackedTimers = () => { for (const id of liveTimers) clearTimeout(id); liveTimers.clear(); };',
  'const wait = (ms) => new Promise((done) => trackedTimeout(done, ms));',
  'const withDeadline = (promise, ms, label) => Promise.race([',
  '  promise,',
  "  new Promise((_, reject) => { const id = trackedTimeout(() => reject(new Error('deadline ' + label)), ms); promise.catch(() => {}).finally(() => clearTimeout(id)); }),",
  ']);',
  'const client = null;',
  'const report = {};',
  "const stderrMode = process\.argv[5] === 'inherit-stderr' ? 'inherit' : 'pipe';",
  "const chrome = spawn(process.execPath, [process\.argv[2], process\.argv[2], process\.argv[3], process\.argv[4]], { stdio: ['ignore', 'ignore', stderrMode], detached: true });",
  "chrome.stderr?.on('data', () => {});",
  'chrome.unref();',
].join('\n');
const CASE_TAIL = [
  'await new Promise((done) => setTimeout(done, 600));',
  'await killTree();',
  "console.log('KILLTREE_RETURNED residue=' + (report.teardownGroupResidue ?? 'none'));",
  '// NO process.exit(): the case must end because its handles are genuinely released.',
  '',
].join('\n');

const harnessDir = mkdtempSync(join(tmpdir(), 'rs96-killtree-'));
const writeFixture = (name, text) => { const p = join(harnessDir, name); writeFileSync(p, text); return p; };
const stubbornPath = writeFixture('stubborn.mjs', STUBBORN_SRC);
const leaderPath = writeFixture('leader.mjs', LEADER_SRC);

const spawnCase = (mode) => new Promise((done) => {
  const casePath = writeFixture(`case-${mode}.mjs`, `${CASE_HEAD}\n${killTreeMatch[1]}\n${CASE_TAIL}`);
  const infoPath = join(harnessDir, `case-${mode}.info`);
  const child = spawn(process.execPath, [casePath, leaderPath, stubbornPath, infoPath, mode], { stdio: ['ignore', 'pipe', 'pipe'] });
  let stdout = '';
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stderr.on('data', (chunk) => { stdout += chunk; });
  const killer = setTimeout(() => { child.kill('SIGKILL'); }, 20000);
  child.on('exit', (code, signal) => {
    clearTimeout(killer);
    let helperPid = null;
    try { helperPid = Number(readFileSync(infoPath, 'utf8').match(/pid=(\d+)/)[1]); } catch { /* case died before leader wrote */ }
    done({ code, signal, stdout, helperPid });
  });
});

const alive = (pid) => {
  try { return !readFileSync(`/proc/${pid}/stat`, 'utf8').includes(') Z'); } catch { return false; }
};

for (const mode of ['devnull', 'inherit-stderr']) {
  test(`killTree exterminates a SIGTERM-ignoring helper and releases the loop (${mode})`, async () => {
    const run = await spawnCase(mode);
    assert.ok(run.helperPid, `case must have launched the stuck helper; ${run.stdout}`);
    assert.equal(run.signal, null, `probe case must exit on its own, not by harness SIGKILL (loop holdback); ${run.stdout}`);
    assert.equal(run.code, 0, `probe case exit; ${run.stdout}`);
    assert.match(run.stdout, /KILLTREE_RETURNED residue=none/, `killTree must report an empty group; ${run.stdout}`);
    for (let attempt = 0; attempt < 30 && alive(run.helperPid); attempt += 1) {
      await new Promise((done) => setTimeout(done, 100));
    }
    assert.ok(!alive(run.helperPid), `SIGTERM-ignoring helper ${run.helperPid} survived killTree escalation`);
  });
}

test('subject killTree escalates to the group independent of leader exit', () => {
  const body = killTreeMatch[1];
  assert.match(body, /SIGKILL/, 'group SIGKILL must exist');
  const drainSegment = body.slice(body.indexOf('for (let attempt'));
  assert.ok(drainSegment.includes('SIGKILL'), 'SIGKILL escalation must live in the post-leader straggler drain, not only in the leader-exit timeout catch');
});

process.on('beforeExit', () => { if (process.env.RS96_KT_KEEP !== '1') rmSync(harnessDir, { recursive: true, force: true }); });
