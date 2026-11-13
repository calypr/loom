import assert from 'node:assert/strict';
import test from 'node:test';
import { spawn } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', '..', '..');
const SUBJECT = join(REPO_ROOT, 'scripts/verify-ui/workflows/verify-explorer-row-source-four-state.mjs');
const MAIN_MARKER = 'const main = async () => {';
const INJECTED = `${MAIN_MARKER} if (process.env.RS96_FORCE_FAIL === '1') throw new Error('injected failure');`;
const DEEP_MARKER = "  markPhase('cdp-connect');";
const DEEP_INJECTED = `${DEEP_MARKER} if (process.env.RS96_FORCE_FAIL_DEEP === '1') throw new Error('injected failure');`;
const SPAWN_TIMEOUT_MS = 120000;
const POST_EXIT_GRACE_MS = 1000;
const STACK_ENV = {
  LOOM_DEV_SOURCE_ROOT: REPO_ROOT,
  LOOM_DEV_COMPOSE_PROJECT: 'loom-dev-stravinsky',
  LOOM_DEV_API_PORT: '8182',
  LOOM_DEV_UI_PORT: '3182',
};

const listProfiles = (directory) => readdirSync(directory).filter((name) => name.startsWith('rs96-prof-'));

const groupMembers = (pgid) => {
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
  const nested = join(root, 'scripts', 'verify-ui', 'workflows');
  mkdirSync(nested, { recursive: true });
  symlinkSync(join(REPO_ROOT, 'scripts/loom-dev.mjs'), join(root, 'scripts', 'loom-dev.mjs'));
  symlinkSync(join(REPO_ROOT, 'scripts/browser'), join(root, 'scripts', 'browser'));
  symlinkSync(join(REPO_ROOT, 'testdata'), join(root, 'testdata'));
  symlinkSync(join(REPO_ROOT, 'scripts/verify-ui/workflows/builder-url.mjs'), join(nested, 'builder-url.mjs'));
  const copyPath = join(nested, 'subject-copy.mjs');
  cpSync(SUBJECT, copyPath);
  const deep = process.env.RS96_FORCE_FAIL_DEEP === '1';
  const source = readFileSync(copyPath, 'utf8');
  const marker = deep ? DEEP_MARKER : MAIN_MARKER;
  const injected = deep ? DEEP_INJECTED : INJECTED;
  const markerCount = source.split(marker).length - 1;
  assert.equal(markerCount, 1, 'forced-failure injection must land on a unique marker');
  let stagedSource = source;
  if (deep) {
    const cdpMarker = "  const portFile = join(profile, 'DevToolsActivePort');";
    assert.equal(source.split(MAIN_MARKER).length - 1, 1, 'main boundary must be unique');
    assert.equal(source.split(cdpMarker).length - 1, 1, 'CDP boundary must be unique');
    const start = source.indexOf(MAIN_MARKER) + MAIN_MARKER.length;
    const end = source.indexOf(cdpMarker, start);
    assert.ok(end > start, 'server preconditions must precede the real CDP connection');
    stagedSource = source.slice(0, start) + "\n  report.lifecycleOnly = true;\n" + source.slice(end);
  }
  writeFileSync(copyPath, stagedSource.replace(marker, injected));
  const out = join(root, 'out');
  const childTmp = join(root, 'tmp');
  mkdirSync(childTmp);
  const uniqueSlug = `${slug}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`.slice(0, 17);
  const child = spawn(process.execPath, [copyPath, '--out', out, '--run-label', uniqueSlug], {
    env: { ...process.env, ...STACK_ENV, RS96_FORCE_FAIL: '1', RS96_FORCE_FAIL_DEEP: process.env.RS96_FORCE_FAIL_DEEP === '1' ? '1' : '0', TMPDIR: childTmp, TMP: childTmp, TEMP: childTmp },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout = (stdout + chunk).slice(-16000); });
  child.stderr.on('data', (chunk) => { stderr = (stderr + chunk).slice(-16000); });
  let timedOut = false;
  const exit = await new Promise((done) => {
    const killer = setTimeout(() => { timedOut = true; try { child.kill('SIGKILL'); } catch { } }, SPAWN_TIMEOUT_MS);
    child.on('exit', (code, signal) => { clearTimeout(killer); done({ code, signal }); });
  });
  await new Promise((done) => setTimeout(done, POST_EXIT_GRACE_MS));
  const profileMarker = stdout.match(/RUN_PROFILE path=([^\r\n]+)/);
  const profilePaths = profileMarker ? [profileMarker[1]] : listProfiles(childTmp).map((name) => join(childTmp, name));
  const treeMarker = stdout.match(/RUN_TREE pgid=(\d+)/);
  const treePgid = treeMarker ? Number(treeMarker[1]) : null;
  const survivors = treePgid === null ? [] : groupMembers(treePgid).map((pid) => ({ pid, profilePath: profilePaths.join(',') || 'none' }));
  let dirGoneAtExit = false;
  for (let attempt = 0; attempt < 60 && !dirGoneAtExit; attempt += 1) {
    dirGoneAtExit = profilePaths.every((profilePath) => !existsSync(profilePath));
    if (!dirGoneAtExit && attempt < 59) await new Promise((done) => setTimeout(done, 50));
  }
  const journeyPath = join(out, 'journey.json');
  let report = null;
  for (let attempt = 0; attempt < 20 && report === null; attempt += 1) {
    try { report = JSON.parse(readFileSync(journeyPath, 'utf8')); }
    catch { if (attempt < 19) await new Promise((done) => setTimeout(done, 50)); }
  }
  for (const pid of groupMembers(treePgid)) { try { process.kill(pid, 'SIGKILL'); } catch { } }
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
    const reachedCdp = (run.report?.phaseTimings?.phases ?? []).some((phase) => phase.phase === 'cdp-connect');
    assert.equal(run.report?.lifecycleOnly, true, 'deep case must use isolated lifecycle preconditions');
    assert.equal(run.report?.status, 'failed', 'deep injected failure must be recorded');
    assert.match(String(run.report?.error ?? ''), /injected failure/, 'deep case must reach its deliberate post-CDP failure');
    assert.equal(run.report?.buildIdentity, null, 'lifecycle test must not claim a serving build identity');
    assert.equal(run.report?.fixture, null, 'lifecycle test must not create a serving fixture');
    assert.ok(reachedCdp, `deep failed run must reach the cdp-connect phase (identity/fixture early throw made it vacuous); phases=${JSON.stringify(run.report?.phaseTimings?.phases ?? null)}; ${summarize(run)}`);
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
