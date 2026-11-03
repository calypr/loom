import assert from 'node:assert/strict';
import test from 'node:test';
import { spawn, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', '..', '..');
const SUBJECT = join(REPO_ROOT, 'scripts/verify-ui/workflows/verify-explorer-row-source-four-state.mjs');
const MAIN_MARKER = 'const main = async () => {';
const INJECTED = `${MAIN_MARKER} if (process.env.RS96_FORCE_FAIL === '1') throw new Error('injected failure');`;
const DEEP_MARKER = "  markPhase('cdp-connect');"; // throws right after connect+listeners completed and the phase was recorded
const DEEP_INJECTED = `${DEEP_MARKER} if (process.env.RS96_FORCE_FAIL_DEEP === '1') throw new Error('injected failure');`;
const SPAWN_TIMEOUT_MS = 120000;
const POST_EXIT_GRACE_MS = 1000;
const STACK_ENV = {
  // this repo IS the stack's source root; the compose pin identifies the owned live stack
  LOOM_DEV_SOURCE_ROOT: REPO_ROOT,
  LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-stravinsky',
  LOOM_DEV_API_PORT: '8182',
  LOOM_DEV_UI_PORT: '3182',
};

const listProfiles = (before) => readdirSync(tmpdir()).filter((name) => name.startsWith('rs96-prof-') && !before.has(name));

// Process-group survivor scan: zombies (empty cmdline, invisible to pgrep -f / cmdline
// filters) are still group members, and no unrelated path-mentioning process is touched.
const groupMembers = (pgid) => {
  // zombie entries awaiting reap are terminally dead; only live group members count
  if (!Number.isInteger(pgid)) return [];
  try {
    return readdirSync('/proc').filter((entry) => {
      if (!/^\d+$/.test(entry)) return false;
      try {
        const rest = (() => { const stat = readFileSync(`/proc/${entry}/stat`, 'utf8'); return stat.slice(stat.lastIndexOf(') ') + 2); })();
        return Number(rest.split(' ')[2]) === pgid && rest.split(' ')[0] !== 'Z';
      } catch { return false; }
    }).map(Number);
  } catch { return []; }
};

const stageFailedRun = async (slug) => {
  const root = mkdtempSync(join(tmpdir(), 'rs96-lifecycle-'));
  // Two-level staging: the copy's imports resolve inside this disposable tree via the
  // root symlinks below; its dependency census reads the real tree (read-only).
  // Geometry: the copy at root/v/x has `..`=v and `../..`=root, so its ../../ imports hit
  // the loom-dev.mjs and browser symlinks at root; builder-url is a sibling of the copy.
  const nested = join(root, 'v', 'x');
  mkdirSync(nested, { recursive: true });
  symlinkSync(join(REPO_ROOT, 'scripts/loom-dev.mjs'), join(root, 'loom-dev.mjs'));
  symlinkSync(join(REPO_ROOT, 'scripts/browser'), join(root, 'browser'));
  symlinkSync(join(REPO_ROOT, 'scripts/verify-ui/workflows/builder-url.mjs'), join(nested, 'builder-url.mjs'));
  const copyPath = join(nested, 'subject-copy.mjs');
  cpSync(SUBJECT, copyPath);
  const deep = process.env.RS96_FORCE_FAIL_DEEP === '1';
  const source = readFileSync(copyPath, 'utf8');
  const marker = deep ? DEEP_MARKER : MAIN_MARKER;
  const injected = deep ? DEEP_INJECTED : INJECTED;
  const markerCount = source.split(marker).length - 1;
  assert.equal(markerCount, 1, 'forced-failure injection must land on a unique marker');
  writeFileSync(copyPath, source.replace(marker, injected));
  // The subject asserts a resolvable gitHead; the staged tree deliberately has no
  // gitdir and nothing may be written into the shared tree, so pin the head via the
  // subject's validated test-only override, sourced from the real repo HEAD.
  const head = spawnSync('git', ['-C', REPO_ROOT, 'rev-parse', 'HEAD'], { encoding: 'utf8' });
  assert.equal(head.status, 0, `real repo HEAD unresolvable: ${head.stderr}`);
  const headOverride = head.stdout.trim();
  const out = join(root, 'out');
  const before = new Set(readdirSync(tmpdir()).filter((name) => name.startsWith('rs96-prof-')));
  const uniqueSlug = `${slug}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`.slice(0, 17);
  const child = spawn(process.execPath, [copyPath, '--out', out, '--run-label', uniqueSlug], {
    env: { ...process.env, ...STACK_ENV, RS96_FORCE_FAIL: '1', RS96_FORCE_FAIL_DEEP: process.env.RS96_FORCE_FAIL_DEEP === '1' ? '1' : '0', LOOM_DEV_HEAD_OVERRIDE: headOverride },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout = (stdout + chunk).slice(-16000); });
  child.stderr.on('data', (chunk) => { stderr = (stderr + chunk).slice(-16000); });
  let timedOut = false;
  const exit = await new Promise((done) => {
    const killer = setTimeout(() => { timedOut = true; try { child.kill('SIGKILL'); } catch { /* already gone */ } }, SPAWN_TIMEOUT_MS);
    child.on('exit', (code, signal) => { clearTimeout(killer); done({ code, signal }); });
  });
  await new Promise((done) => setTimeout(done, POST_EXIT_GRACE_MS));
  const profileMarker = stdout.match(/RUN_PROFILE path=(\/tmp\/rs96-prof-[^\s]+)/);
  const profilePaths = profileMarker ? [profileMarker[1]] : listProfiles(before).map((name) => join(tmpdir(), name));
  const treeMarker = stdout.match(/RUN_TREE pgid=(\d+)/);
  const treePgid = treeMarker ? Number(treeMarker[1]) : null;
  const survivors = treePgid === null ? [] : groupMembers(treePgid).map((pid) => ({ pid, profilePath: profilePaths.join(',') || 'none' }));
  // Measured BEFORE this harness's own cleanup: deletion is the child's responsibility.
  // The bounded poll absorbs the volume's stale-metadata reads; process-group members
  // (above) are the authoritative survivors — rmSync succeeds even while a live holder
  // keeps the tree open, so dirGone alone is never the lifecycle proof.
  let dirGoneAtExit = false;
  for (let attempt = 0; attempt < 60 && !dirGoneAtExit; attempt += 1) {
    dirGoneAtExit = profilePaths.every((profilePath) => !existsSync(profilePath));
    if (!dirGoneAtExit && attempt < 59) await new Promise((done) => setTimeout(done, 50));
  }
  const journeyPath = join(out, 'journey.json');
  // Buffered at exit+grace BEFORE any cleanup: this docker volume intermittently
  // ENOENTs a file that milliseconds-earlier reads see intact, so retry then keep
  // the parsed value; later assertions never reopen the path.
  let report = null;
  for (let attempt = 0; attempt < 20 && report === null; attempt += 1) {
    try { report = JSON.parse(readFileSync(journeyPath, 'utf8')); }
    catch { if (attempt < 19) await new Promise((done) => setTimeout(done, 50)); }
  }
  // kill(-pgid) can be denied (EPERM) in sandboxed kernels: signal members individually
  for (const pid of groupMembers(treePgid)) { try { process.kill(pid, 'SIGKILL'); } catch { /* racing exit */ } }
  for (const profilePath of profilePaths) rmSync(profilePath, { recursive: true, force: true });
  if (process.env.RS96_KEEP_STAGED !== '1') rmSync(root, { recursive: true, force: true });
  return { ...exit, timedOut, stdout, stderr, out, journeyPath, report, dirGone: dirGoneAtExit, stagedRoot: root, profilePaths, survivors };
};

const summarize = (run) => `exit=${run.code} signal=${run.signal} timedOut=${run.timedOut} profiles=${run.profilePaths.length} survivors=${run.survivors.length}\nstdout=${run.stdout.slice(-2000)}\nstderr=${run.stderr.slice(-2000)}`;

test('fails loudly and records failure', async () => {
  const run = await stageFailedRun('rs96-lc-a');
  assert.ok(!run.timedOut, `child exceeded ${SPAWN_TIMEOUT_MS}ms cap; ${summarize(run)}`);
  assert.notEqual(run.code, 0, `forced mid-main failure must exit nonzero; ${summarize(run)}`);
  assert.equal(run.profilePaths.length, 1, `expected exactly one fresh rs96-prof profile; ${summarize(run)}`);
  assert.ok(existsSync(run.journeyPath) || run.report !== null, `failure journey report missing at ${run.journeyPath}; ${summarize(run)}`);
  assert.equal(run.report?.status, 'failed', `failure report must record status=failed; ${summarize(run)}`);
  assert.match(String(run.report?.error ?? ''), /injected failure/, `report.error must name the injected failure; ${summarize(run)}`);
  assert.match(run.stdout, /JOURNEY_FAILED/, `stdout must announce JOURNEY_FAILED; ${summarize(run)}`);
  assert.match(run.stdout, /RUN_TREE pgid=\d+/, `child must announce its chrome process-group for survivor accounting; ${summarize(run)}`);
  console.log(`[row96-lifecycle] observation: ${run.survivors.length} process(es) still lived in the chrome group ${POST_EXIT_GRACE_MS}ms after child exit: ${run.survivors.map((entry) => entry.pid).join(',') || 'none'}`);
});

const contractSkip = process.env.RS96_LIFECYCLE_SKIP === '1' ? 'explicitly opted out: these tests spawn real short-lived browsers against the owned stack' : false;

const assertNoSurvivors = async (slug) => {
  process.env.RS96_FORCE_FAIL_DEEP = '1';
  try {
    const run = await stageFailedRun(slug);
    assert.ok(!run.timedOut, `child exceeded ${SPAWN_TIMEOUT_MS}ms cap; ${summarize(run)}`);
    assert.notEqual(run.code, 0, `forced deep failure must exit nonzero; ${summarize(run)}`);
    assert.equal(run.profilePaths.length, 1, `deep failure must still create exactly one profile; ${summarize(run)}`);
    assert.match(run.stdout, /RUN_TREE pgid=\d+/, `deep case must have launched the tracked chrome tree; ${summarize(run)}`);
    assert.deepEqual(run.survivors, [], `no process may remain in the chrome process group after child exit plus grace; ${run.survivors.map((entry) => `${entry.pid}@${entry.profilePath}`).join(' , ')}; ${summarize(run)}`);
    // D-4 (reviewer): the deep case must actually transit the CDP phase or it can
    // green-off on an early identity/fixture throw without ever launching a browser tree.
    const reachedCdp = (run.report?.phaseTimings?.phases ?? []).some((phase) => phase.phase === 'cdp-connect');
    assert.ok(reachedCdp, `deep failed run must reach the cdp-connect phase (identity/fixture early throw made it vacuous); phases=${JSON.stringify(run.report?.phaseTimings?.phases ?? null)}; ${summarize(run)}`);
    // profile teardown guarantee: the harness removes its own user-data-dir even on failure
    assert.ok(run.dirGone, `run profile dir must be deleted by harness teardown, persisted: ${run.profilePaths.join(' , ')}`);
  } finally {
    delete process.env.RS96_FORCE_FAIL_DEEP;
  }
};

test('browser tree does not outlive a failed run', { skip: contractSkip }, async () => {
  const run = await stageFailedRun('rs96-lc-b');
  assert.ok(!run.timedOut, `child exceeded ${SPAWN_TIMEOUT_MS}ms cap; ${summarize(run)}`);
  assert.notEqual(run.code, 0, `forced mid-main failure must exit nonzero; ${summarize(run)}`);
  assert.equal(run.profilePaths.length, 1, `shallow failure must still create exactly one profile; ${summarize(run)}`);
  assert.match(run.stdout, /RUN_TREE pgid=\d+/, `shallow case must have launched the tracked chrome tree; ${summarize(run)}`);
  assert.deepEqual(run.survivors, [], `no process may remain in the chrome process group after child exit plus grace; ${run.survivors.map((entry) => `${entry.pid}@${entry.profilePath}`).join(' , ')}; ${summarize(run)}`);
});

test('browser tree does not outlive a deep failed run (post-launch throw)', { skip: contractSkip }, async () => {
  await assertNoSurvivors('rs96-lc-c');
});
