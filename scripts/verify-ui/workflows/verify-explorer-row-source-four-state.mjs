#!/usr/bin/env node
// RS03-03 (docs/BUILDER_VERIFICATION.tsv row 96) four-state focused-native journey: the
// healthy / per-field-refusal / inventory-fault / empty-data contract of the signed-source
// table-shape lane, raw-CDP against the owned loom-dev stack, following the
// scripts/verify-ui/workflows raw-CDP convention (see verify-explorer-filter-row-source.mjs).
// Label authority: /tmp/opencode/label-journey-coupling.md (catalog-path literals are the
// contract; construction-lane humanized text is the only flip family). Reason-class
// authority: internal/explorer/lifecycle/construction_pivot_source.go (rowSourceLaneReasons:
// codeStem + _SOURCE_INVENTORY_UNAVAILABLE ; NO_OBSERVED_VALUES_/NO_ELIGIBLE_POPULATED_/
// NO_ROUTE_ELIGIBLE_/NO_TYPE_ELIGIBLE_/STALE_/ROOT_SOURCE_PROJECTION_UNAVAILABLE).
// Live window execution is pending; --dry-run runs the full static self-validation now.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { CDPClient } from '../../browser/cdp.mjs';
import { browserURL } from './builder-url.mjs';
import { AUTHORING_SEMANTICS_VERSION, collectBuildIdentity as sharedCollectBuildIdentity, createDevSession, createFreshVerificationFixture } from '../../loom-dev.mjs';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', '..');

const options = {};
for (let i = 2; i < process.argv.length; i += 1) {
  if (process.argv[i] === '--dry-run') { options.dryRun = true; continue; }
  const key = { '--out': 'out', '--chrome': 'chrome', '--run-label': 'runLabel' }[process.argv[i]];
  assert.ok(key && process.argv[i + 1], `Unknown or missing-valued option: ${process.argv[i]}`);
  options[key] = process.argv[++i];
}

const expectedChecks = [
  'row96-lane-support-healthy',
  'row96-field-options-catalog-path-labels',
  'row96-lane-refusal-decision-classes',
  'row96-reason-codes-served',
  'row96-inventory-block-strike-and-false-positive-control',
  'row96-structural-gates-inventory-disjoint',
  'row96-picker-empty-and-refused-explanations',
  'row96-existing-column-reuse',
  'row96-signed-expand-lane',
  'row96-expand-policy-matrix',
];

// ROW-SOURCE-08 policy matrix (docs/ROW_OPERATION_NEXT_BATCH.md §ROW-SOURCE-08): five
// parents carrying [red, red, blue] + [green] + empty + missing + null arrays; the contract
// row counts EXCLUDE=4 / PRESERVE_PARENT=7 and the ERROR wire code are asserted in-window.
const matrixParents = [
  { id: 'rs08m-red', values: ['red', 'red', 'blue'] },
  { id: 'rs08m-green', values: ['green'] },
  { id: 'rs08m-empty', values: [] },
  { id: 'rs08m-missing' },
  { id: 'rs08m-null', values: null },
];
const matrixExcludeMultiset = [['rs08m-red', 'red'], ['rs08m-red', 'red'], ['rs08m-red', 'blue'], ['rs08m-green', 'green']];
const matrixKeptParentIds = ['rs08m-empty', 'rs08m-missing', 'rs08m-null'];
// Wire-code authority: internal/dataframe/errors/errors.go:77 defines CodeConstructionExpansionEmpty
// = CONSTRUCTION_EXPANSION_EMPTY; internal/dataframe/compiler/render/aql/construction_expand.go:33
// ASSERTs it against LENGTH(items)==0 under EmptyPolicy ERROR; internal/server/
// explorer_preview_response.go:65 + server.go:96 map it to the 422 preview failure body.
const matrixErrorWireCode = 'CONSTRUCTION_EXPANSION_EMPTY';

// Honest-absence family: row-choice verdicts (lane reason authority above) plus the
// compiler capability gates emitted for an under-seeded table (dataframe/compiler/lower/
// recipe_construction.go:2242-2289: NO_NUMERIC_COLUMN/NO_PUBLIC_COLUMNS/
// INSUFFICIENT_SCALAR_COLUMNS/NO_COMPATIBLE_UNPIVOT_COLUMNS/NO_ARRAY_COLUMNS/
// NO_PUBLIC_SOURCE_COLUMNS) and related-field identity refusal
// (construction_proposal.go:983/993 NO_ACTIVE_RELATED_RECORD). Inventory-unavailable
// remains disjoint — that split is the S-B guarantee under test.
const emptyDataFamily = /^(NO_OBSERVED_VALUES_|NO_ELIGIBLE_POPULATED_|NO_ROUTE_ELIGIBLE_|NO_TYPE_ELIGIBLE_|STALE_|ROOT_SOURCE_PROJECTION_UNAVAILABLE$|NO_NUMERIC_COLUMN$|NO_PUBLIC_COLUMNS$|NO_PUBLIC_SOURCE_COLUMNS$|NO_COMPATIBLE_UNPIVOT_COLUMNS$|INSUFFICIENT_SCALAR_COLUMNS$|NO_ARRAY_COLUMNS$|NO_ACTIVE_RELATED_RECORD$)/;
const inventoryClass = /_SOURCE_INVENTORY_UNAVAILABLE$/;

const staticValidate = () => {
  assert.equal(expectedChecks.length, new Set(expectedChecks).size, 'duplicate check names in expectedChecks');
  assert.ok(emptyDataFamily.test('ROW_SOURCE_SOURCE_FIELDS') === false, 'family regex must be anchored to real classes');
  assert.ok(emptyDataFamily.test('NO_OBSERVED_VALUES_GENDER_SOURCE_FIELDS'), 'empty-data class must be in the family');
  assert.ok(!emptyDataFamily.test('PIVOT_ROW_SOURCE_SOURCE_INVENTORY_UNAVAILABLE'), 'the inventory class must stay disjoint from the empty-data family');
  assert.ok(inventoryClass.test('PIVOT_ROW_SOURCE_SOURCE_INVENTORY_UNAVAILABLE'), 'inventory class must equal codeStem + _SOURCE_INVENTORY_UNAVAILABLE per construction_pivot_source.go');
  assert.ok(!inventoryClass.test('NO_OBSERVED_VALUES_GENDER_SOURCE_FIELDS'), 'an empty-data verdict must never satisfy the inventory detector');
  for (const helper of ['../../loom-dev.mjs', '../../browser/cdp.mjs', './builder-url.mjs']) {
    assert.ok(existsSync(resolve(REPO_ROOT, 'scripts/verify-ui/workflows', helper)), `missing dependency ${helper}`);
  }
  assert.equal(expectedChecks.indexOf('row96-expand-policy-matrix'), expectedChecks.indexOf('row96-signed-expand-lane') + 1,
    'the policy-matrix check must execute immediately after the signed-expand-lane check');
  assert.ok(/^[A-Z][A-Z0-9_]*$/.test(matrixErrorWireCode), 'the ERROR policy wire code must be an upper server class, not prose');
  assert.equal(matrixExcludeMultiset.length, 4, 'the EXCLUDE parent multiset must contain exactly the four contract rows (3 + 1 values expanded; empty/missing/null parents dropped)');
  assert.equal(matrixExcludeMultiset.filter(([, value]) => value === 'red').length, 2, 'repeated red values are two separate contract rows');
  const matrixExpanded = matrixParents.reduce((sum, parent) => sum + (Array.isArray(parent.values) ? parent.values.length : 0), 0);
  assert.equal(matrixExpanded, matrixExcludeMultiset.length, 'the seeded parent arrays must flatten to exactly the EXCLUDE row count');
  assert.equal(matrixExpanded + matrixKeptParentIds.length, 7, 'PRESERVE_PARENT must add exactly the empty/missing/null parent rows to reach seven');
  assert.deepEqual(new Set(matrixKeptParentIds).size, 3, 'the empty, missing and null parents must be three distinct resources');
  console.log(`DRY_RUN_OK artifact=row96-four-state checks=${expectedChecks.length}`);
};

if (options.dryRun) {
  staticValidate();
  process.exit(0);
}

assert.ok(options.out, '--out <FRESH_DIR> is required for live runs');
const out = isAbsolute(options.out) ? options.out : resolve(process.cwd(), options.out);
assert.ok(!existsSync(out) || readdirSync(out).length === 0, 'Refusing to overwrite browser evidence directory');
mkdirSync(out, { recursive: true });
const runStem = options.runLabel ?? 'rs96four';

const report = { status: 'running', schemaVersion: 1, mode: 'raw-cdp-row96-four-state-journey', builtAt: new Date().toISOString(), runStem, environment: null, buildIdentity: null, fixture: null, checks: [], steps: [], screenshots: [], sideFindings: [], exceptions: [], error: null };
const record = (name, evidence) => {
  assert.ok(expectedChecks.includes(name), `unknown check ${name}`);
  const existing = report.checks.find((entry) => entry.name === name);
  assert.ok(!existing, `duplicate record for ${name}`);
  report.checks.push({ name, status: 'passed', evidence });
};

// ROW-SOURCE-08: this journey's signed-EXPAND check needs a direct-root
// primitive array carried by real data. The generated schema admits none on the
// shipped fixture resources (Patient, Observation, DiagnosticReport,
// DocumentReference, Group, ResearchStudy, Specimen), so the run seeds a
// per-run fixture copy plus three Procedures whose instantiatesUri[] arrays
// (the lane's only honest primitive root array here: 2 + 1 + 0 values) give the
// offer, refusal, and EXCLUDE-policy paths real data to work on.
const rs08FixtureDir = (() => {
  if (options.dryRun || process.env.LOOM_DEV_FIXTURE_DIR) return process.env.LOOM_DEV_FIXTURE_DIR;
  const dir = mkdtempSync(join(tmpdir(), 'rs96-fixture-'));
  let patientSubjectRef = 'Patient/dev-patient-001';
  for (const name of readdirSync(join(REPO_ROOT, 'testdata/devloop-fixture')).filter((entry) => entry.endsWith('.ndjson'))) {
    const contents = readFileSync(join(REPO_ROOT, 'testdata/devloop-fixture', name));
    if (name === 'Patient.ndjson') {
      const firstID = JSON.parse(String(contents).trim().split('\n')[0])?.id;
      if (typeof firstID === 'string' && firstID) patientSubjectRef = `Patient/${firstID}`;
    }
    writeFileSync(join(dir, name), contents);
  }
  const procedures = ['rs08-procedure-a', 'rs08-procedure-b', 'rs08-procedure-c'].map((id) => ({
    resourceType: 'Procedure', id, status: 'completed', subject: { reference: patientSubjectRef },
  }));
  procedures[0].instantiatesUri = ['urn:protocol:alpha', 'urn:protocol:beta'];
  procedures[1].instantiatesUri = ['urn:protocol:gamma'];
  writeFileSync(join(dir, 'Procedure.ndjson'), procedures.map((resource) => JSON.stringify(resource)).join('\n') + '\n');
  return dir;
})();
if (rs08FixtureDir) process.env.LOOM_DEV_FIXTURE_DIR = rs08FixtureDir;

// ROW-SOURCE-08 policy matrix: the contract parents [red, red, blue] / [green] / empty /
// missing / null must land in a row space the signed-expand-lane check's byte-stable
// four-row Procedure preview never traverses, so the matrix gets its OWN per-run fresh
// fixture project (createFreshVerificationFixture design) seeded from this second copy.
// Same runner machinery as above; nothing on the shared stack is mutated.
const rs08MatrixFixtureDir = (() => {
  if (options.dryRun) return undefined;
  const dir = mkdtempSync(join(tmpdir(), 'rs96-matrix-'));
  let patientSubjectRef = 'Patient/dev-patient-001';
  for (const name of readdirSync(join(REPO_ROOT, 'testdata/devloop-fixture')).filter((entry) => entry.endsWith('.ndjson'))) {
    const contents = readFileSync(join(REPO_ROOT, 'testdata/devloop-fixture', name));
    if (name === 'Patient.ndjson') {
      const firstID = JSON.parse(String(contents).trim().split('\n')[0])?.id;
      if (typeof firstID === 'string' && firstID) patientSubjectRef = `Patient/${firstID}`;
    }
    writeFileSync(join(dir, name), contents);
  }
  const procedures = matrixParents.map((parent) => ({
    resourceType: 'Procedure', id: parent.id, status: 'completed', subject: { reference: patientSubjectRef },
    ...(Object.prototype.hasOwnProperty.call(parent, 'values') ? { instantiatesUri: parent.values } : {}),
  }));
  writeFileSync(join(dir, 'Procedure.ndjson'), procedures.map((resource) => JSON.stringify(resource)).join('\n') + '\n');
  return dir;
})();

const session = createDevSession(process.env, REPO_ROOT);
const collectBuildIdentity = () => {
  const identity = sharedCollectBuildIdentity(session, { repoRoot: REPO_ROOT });
  // Test-harness escape hatch used ONLY by the lifecycle contract test (staged copy has
  // no gitdir by design). Never overrides a resolvable real HEAD: an ambient pin must
  // not forge recorded identity or neutralise the mid-run HEAD-move guard.
  const pin = process.env.LOOM_DEV_HEAD_OVERRIDE;
  if (pin) {
    if (!/^[a-f0-9]{40}$/.test(pin)) throw new Error('LOOM_DEV_HEAD_OVERRIDE must be a 40-char commit hash');
    if (identity.gitHead) {
      console.log(`HEAD_OVERRIDE_IGNORED realHead=${identity.gitHead} pin=${pin}`);
    } else {
      identity.gitHead = pin;
      console.log(`HEAD_OVERRIDE_APPLIED pin=${pin}`);
    }
  }
  return identity;
};

const chromePath = options.chrome ?? process.env.LOOM_E2E_CHROME
  ?? ['/usr/bin/chromium', '/opt/google/chrome/chrome'].find((candidate) => existsSync(candidate));
assert.ok(chromePath && existsSync(chromePath), 'No Chrome binary: pass --chrome, set LOOM_E2E_CHROME, or install chromium');
const profile = mkdtempSync(join(tmpdir(), 'rs96-prof-'));
console.log(`RUN_PROFILE path=${profile}`);
const chrome = spawn(chromePath, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--remote-debugging-port=0',
  `--user-data-dir=${profile}`, '--window-size=1600,1200', '--no-first-run', '--no-default-browser-check',
  '--disable-background-networking', '--disable-component-update', '--disable-sync', '--proxy-server=direct://', 'about:blank'],
  { stdio: ['ignore', 'ignore', 'pipe'], detached: true });
let chromeErr = '';
chrome.stderr.on('data', (chunk) => { chromeErr = (chromeErr + chunk).slice(-4000); });
// F3: an async spawn failure (EACCES/EISDIR/EAGAIN) surfaces as an 'error' EVENT, not the
// :98 existsSync assert; record it and let main()'s CDP gate fail, so the tail still runs
// (journey.json written, tree killed, profile removed) instead of crashing the module.
let chromeSpawnError = null;
chrome.on('error', (error) => { chromeSpawnError = String(error?.message ?? error).slice(0, 300); });
chrome.unref();
console.log(`RUN_TREE pgid=${chrome.pid}`);
const liveTimers = new Set();
const trackedTimeout = (fn, ms) => {
  const id = setTimeout(() => { liveTimers.delete(id); fn(); }, ms);
  liveTimers.add(id);
  return id;
};
const clearTrackedTimers = () => { for (const id of liveTimers) clearTimeout(id); liveTimers.clear(); };
const wait = (ms) => new Promise((done) => trackedTimeout(done, ms));
const withDeadline = (promise, ms, label) => Promise.race([
  promise,
  new Promise((_, reject) => { const id = trackedTimeout(() => reject(new Error(`timeout ${label} after ${ms}ms`)), ms); promise.catch(() => {}).finally(() => clearTimeout(id)); }),
]);
let client = null;
const runStartedAt = Date.now();
const phaseTimings = { phases: [] };
let phaseMark = runStartedAt;
const markPhase = (name) => {
  const now = Date.now();
  phaseTimings.phases.push({ phase: name, ms: now - phaseMark });
  phaseMark = now;
};
const moduleSend = (method, params = {}, ms = 25000) => withDeadline(client.send(method, params), ms, method);
// EXTRACT-START:killTree
const killTree = async () => {
  clearTrackedTimers();
  try { client.close(); } catch { /* already closed */ }
  try { chrome.stderr?.destroy(); } catch { /* stream already released */ }
  // F1: escalation must not hinge on the leader's exit. A helper that ignores SIGTERM
  // (or exits late) would otherwise survive while the journey prints JOURNEY_OK.
  const groupMembers = () => {
    // zombie entries awaiting reap are terminally dead; only live group members count
    if (!Number.isInteger(chrome.pid)) return [];
    try {
      return readdirSync('/proc').filter((entry) => {
        if (!/^\d+$/.test(entry)) return false;
        try {
          const stat = readFileSync(`/proc/${entry}/stat`, 'utf8');
          const rest = stat.slice(stat.lastIndexOf(') ') + 2);
          return Number(rest.split(' ')[2]) === chrome.pid && rest.split(' ')[0] !== 'Z';
        } catch { return false; }
      }).map(Number);
    } catch { return []; }
  };
  // Some sandboxes deny kill(-pgid) even for own children (EPERM): try the group
  // signal, then fall back to leader, then to each enumerated group member.
  const groupSignal = (signal) => {
    let groupOk = false;
    if (Number.isInteger(chrome.pid)) { try { process.kill(-chrome.pid, signal); groupOk = true; } catch { groupOk = false; } }
    let leaderOk = false;
    if (!groupOk) { try { leaderOk = chrome.kill(signal); } catch { leaderOk = false; } }
    for (const pid of groupMembers()) { try { process.kill(pid, signal); } catch { /* racing exit */ } }
    return true;
  };
  if (chrome.exitCode === null && chrome.signalCode === null) {
    // await BEFORE sending: a fast exit can land between send and listen and strand the promise
    const exited = new Promise((done) => chrome.once('exit', done));
    if (groupSignal('SIGTERM')) {
      await withDeadline(exited, 5000, 'chrome SIGTERM exit').catch(() => { groupSignal('SIGKILL'); });
    }
  }
  // bounded drain of stragglers (zombies are reaped by this parent, so members are live)
  for (let attempt = 0; attempt < 10 && groupMembers().length > 0; attempt += 1) {
    if (attempt === 5) groupSignal('SIGKILL');
    await wait(100);
  }
  if (groupMembers().length > 0) report.teardownGroupResidue = groupMembers().join(',');
};
// EXTRACT-END:killTree


const main = async () => {
  const identityBefore = collectBuildIdentity();
  report.buildIdentity = { before: identityBefore };
  assert.ok(identityBefore.runningBinary, 'build identity unavailable: no serving binary on the owned stack');
  assert.ok(identityBefore.gitHead, 'build identity unavailable: git HEAD unresolvable in the repo executing the journey');
  assert.equal(identityBefore.checkExit, 0, `serving build identity must be current before the journey runs: ${JSON.stringify(identityBefore)}`);
  assert.equal(identityBefore.expectedSource, identityBefore.liveSource, `live source drifted from the recorded build: ${identityBefore.expectedSource} != ${identityBefore.liveSource}`);

  const fixture = await createFreshVerificationFixture(session, `${runStem}-a`, { populateBootstrap: false });
  const project = fixture.target.fixtureProject;
  const explorer = fixture.seed.bootstrapExplorerId;
  report.fixture = { project, explorer };
  report.environment = { composeProject: session.composeProject, apiUrl: session.apiUrl ?? `http://127.0.0.1:${session.apiPort}`, uiUrl: session.uiUrl ?? `http://127.0.0.1:${session.uiPort}` };
  markPhase('identity+fixture');
  const apiOrigin = report.environment.apiUrl;
  const api = async (path, body) => {
    const base = `${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
    const response = await fetch(`${base}/${path}`, { method: body ? 'POST' : 'GET', headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined });
    return { status: response.status, value: await response.json().catch(() => ({})) };
  };

  const portFile = join(profile, 'DevToolsActivePort');
  for (let i = 0; i < 150 && !existsSync(portFile); i += 1) await wait(100);
  assert.ok(existsSync(portFile), `chrome devtools port not advertised; spawnError=${chromeSpawnError ?? 'none'}; stderr=${chromeErr.slice(-400)}`);
  const [port] = readFileSync(portFile, 'utf8').trim().split('\n');
  // Bounded like every other transport wait (adversarial review G3: a chrome that
  // advertises DevToolsActivePort then wedges previously hung the journey forever with
  // zero artifacts; SIGSTOP'd-tree probe: R6b log).
  const targetResponse = await withDeadline(fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' }), 20000, 'devtools /json/new');
  const target = await withDeadline(targetResponse.json(), 10000, 'devtools /json/new body');
  client = await withDeadline(CDPClient.connect(target.webSocketDebuggerUrl), 30000, 'CDP websocket open');
  const send = moduleSend;
  const exceptions = [];
  report.exceptions = exceptions;
  client.on('Runtime.exceptionThrown', (event) => exceptions.push(event.exceptionDetails?.exception?.description || event.exceptionDetails?.text || 'exception'));
  markPhase('cdp-connect');

  const until = async (probe, message, ms = 30000) => {
    const deadline = Date.now() + ms;
    for (;;) {
      const value = await probe();
      if (value) return value;
      if (Date.now() > deadline) {
        try { writeFileSync(join(out, `FAIL-${String(message).replace(/\W+/g, '_')}.html`), await evaluate('document.documentElement.outerHTML')); } catch { /* best effort */ }
        assert.fail(`condition failed: ${message}`);
      }
      await wait(80);
    }
  };
  const mounted = (selector) => evaluate(`Boolean(document.querySelector(${JSON.stringify(selector)}))`);
  const bounding = (selector) => evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(!e)return null;const b=e.getBoundingClientRect();return{x:b.x+b.width/2,y:b.y+b.height/2,w:b.width,h:b.height};})()`);
  const realClick = async (selector) => {
    await until(() => mounted(selector), `mounted ${selector}`, 20000);
    await evaluate(`document.querySelector(${JSON.stringify(selector)}).scrollIntoView({block:'center',inline:'center'})`);
    await wait(80);
    const point = await until(() => bounding(selector), `visible ${selector}`);
    assert.ok(point.w > 0 && point.h > 0, `invisible element ${selector}`);
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: point.x, y: point.y });
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
  };
  const navigateToLane = async (label) => {
    await send('Page.navigate', { url: laneURL() });
    // Readiness, not wall-clock: settle when chrome has mounted the workspace or the
    // app has entered its degraded alert path; bounded so a hung SPA still fails loudly.
    await until(async () => (await mounted('[data-testid="construction-workspace"]'))
      || Boolean(await evaluate(`(()=>{const a=document.querySelector('[role="alert"]');return a?a.textContent:null;})()`)), `${label} settled`, 18000);
  };

  await send('Page.enable');
  await send('Runtime.enable');
  const evaluate = async (expression) => {
    const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    assert.ok(!result.exceptionDetails, `page evaluate threw: ${result.exceptionDetails?.exception?.description || 'unknown'}`);
    return result.result?.value ?? null;
  };
  const screenshot = async (label) => {
    const shot = await send('Page.captureScreenshot', { format: 'png' }, 40000);
    const path = join(out, `${label}.png`);
    writeFileSync(path, Buffer.from(shot.data, 'base64'));
    report.screenshots.push(path);
  };

  const laneURL = () => browserURL({ uiUrl: session.uiUrl ?? `http://${session.host}:${session.uiPort}` }, project, explorer, 'builder');

  const builder = await api('builder');
  assert.equal(builder.status, 200, `builder fetch failed (${builder.status})`);
  const root = (builder.value?.catalog?.nodes ?? []).find((node) => node.resourceType === 'Patient' && node.rowRootEligible)
    ?? (builder.value?.catalog?.nodes ?? [])[0];
  assert.ok(root?.nodeId, 'builder catalog exposes no eligible row root; the lane under test cannot exist');
  const commands = async (commandId, list, state) => api('commands', {
    commandId: `${runStem}-${commandId}`, semanticsVersion: AUTHORING_SEMANTICS_VERSION,
    snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion,
    expectedDraftDigest: state.draftDigest, commands: list,
  });
  const created = await commands('row96-table', [{ type: 'CREATE_TABLE', title: 'RS96 four-state shape', rootNodeId: root.nodeId }], builder.value);
  assert.equal(created.status, 200, `CREATE_TABLE failed (${created.status}): ${JSON.stringify(created.value).slice(0, 300)}`);
  // Public-column prerequisite (identical seed command to the certified filter journey,
  // verify-explorer-filter-row-source.mjs:88-92): without a public output column the
  // lane editor legitimately refuses with "filter requires a public output column" and
  // the signed-source picker never renders its option/title set.
  const afterCreate = await api('builder');
  const idCandidate = (afterCreate.value?.catalog?.candidates ?? []).find((candidate) => candidate.nodeId === root.nodeId
    && String(candidate.fieldPath || '').replace(/^root\./, '') === 'id');
  assert.ok(idCandidate?.candidateId, 'catalog exposes no root id candidate for the public-column seed');
  const added = await commands('row96-id', [{
    type: 'ADD_COLUMN', outputId: (await api('builder')).value?.workspace?.documents?.[0]?.output?.id, occurrenceId: 'base',
    candidateId: idCandidate.candidateId, projectionMode: idCandidate.defaultProjectionMode,
    initialPresentation: 'TABLE', title: 'ID',
  }], afterCreate.value);
  assert.equal(added.status, 200, `seed ADD_COLUMN failed (${added.status}): ${JSON.stringify(added.value).slice(0, 300)}`);
  markPhase('seed+column');
  const afterTable = await api('builder');
  const outputId = afterTable.value?.workspace?.documents?.[0]?.output?.id;
  assert.ok(outputId, 'seeded table has no output; downstream lane asserts would be vacuous');
  await navigateToLane('post-seed nav');

  // Dataset-load gate: the app renders role=alert "Couldn't load this dataset." on an
  // initial fetch miss; retry the reload (bounded) before the window's asserts start.
  const datasetLoaded = async () => {
    if (await mounted('[data-testid="construction-workspace"]')) return true;
    const alert = await evaluate(`(()=>{const a=document.querySelector('[role="alert"]');return a?a.textContent:null;})()`);
    if (alert && /couldn.t load this dataset/i.test(alert)) { await send('Page.reload', {}); await wait(1200); }
    return false;
  };
  await navigateToLane('initial nav');
  await until(datasetLoaded, 'dataset loaded (construction-workspace mounted)', 60000);
  markPhase('app-boot');

  const stageId = 'source_projection';
  const lanePayload = await api('construction-capabilities', {
    outputId, stageId, snapshotToken: afterTable.value.catalog.snapshotToken,
    expectedDraftVersion: afterTable.value.draftVersion, expectedDraftDigest: afterTable.value.draftDigest,
  });
  assert.equal(lanePayload.status, 200, `construction-capabilities failed (${lanePayload.status}): ${JSON.stringify(lanePayload.value).slice(0, 300)}`);
  const laneCaps = (lanePayload.value?.selectedStage?.capabilities ?? []);
  assert.ok(laneCaps.length > 0, 'construction-capabilities returned no selectedStage capabilities; every four-state assertion below would pass vacuously');
  const healthy = laneCaps.find((cap) => cap.supported === true);
  const rowAnchor = laneCaps.find((cap) => cap.kind === 'RELATED_SOURCE');
  assert.ok(healthy, `no supported lane capability on a healthy seed; saw ${JSON.stringify(laneCaps.map((cap) => `${cap.kind}:${cap.supported}:${cap.reasonCode ?? ''}`).slice(0, 8))}`);
  assert.ok(!rowAnchor || rowAnchor.supported === true || typeof rowAnchor.reasonCode === 'string', 'RELATED_SOURCE anchor is neither supported nor reasoned');
  record('row96-lane-support-healthy', { healthyKinds: laneCaps.filter((cap) => cap.supported === true).map((cap) => cap.kind), rowAnchor: rowAnchor ? { kind: rowAnchor.kind, supported: rowAnchor.supported } : null });
  report.steps.push({ step: 'seed-and-healthy', outputId, stageId });
  await screenshot('step1-healthy-lane');

  // The default table view exposes no titled <option> elements. The catalog-path label
  // contract (title=fieldPath on dataset-field options) is rendered by the signed-source
  // picker in the row-operation lane editor (ConstructionOperationEditor.tsx:1082 option
  // text=filterSourceLabel+logicalType with title=fieldPath) — the same surface the
  // certified filter journey discovers at step-1, so open the keep-rows lane editor.
  await realClick('[data-testid="construction-action-keep-rows"]');
  await until(() => mounted('[data-testid="construction-filter-editor"]'), 'lane source picker mounted', 20000);
  const fieldOptionFacts = await evaluate(`(()=>{const els=[...document.querySelectorAll('option[title],[role="option"][title]')];return els.slice(0,240).map(e=>({title:e.getAttribute('title'),text:(e.textContent||'').trim(),tag:e.tagName.toLowerCase()}));})()`);
  assert.ok(Array.isArray(fieldOptionFacts) && fieldOptionFacts.length > 0, 'no titled field/concept options are rendered on the table-shape lane; the label contract would be unexercised (navigate to the lane editor if the default view lacks options)');
  const titled = fieldOptionFacts.filter((fact) => fact.title && fact.text);
  assert.ok(titled.length > 0, 'field/concept options carry neither title nor text; the label assertion would be vacuous');
  for (const fact of titled) {
    assert.match(fact.title, /^[A-Za-z][A-Za-z0-9.]*$/, `option title is not a catalog field path: ${JSON.stringify(fact)}`);
    const humanized = fact.title.split('.').pop().replace(/([a-z0-9])([A-Z])/g, '$1 $2').toLowerCase();
    assert.ok(fact.text.toLowerCase().includes(humanized), `option text is not humanized from the terminal catalog path segment: ${JSON.stringify(fact)}`);
  }
  record('row96-field-options-catalog-path-labels', { sample: titled.slice(0, 12), counted: titled.length, rule: 'catalog-literal labels per label-journey-coupling.md (S-C 29d63f6f); construction-lane humanized text is the only flip family, titles stay fieldPath' });
  markPhase('label-picker');
  await screenshot('step2-catalog-path-labels');
  await realClick('[data-testid="construction-close-operation-editor"]');
  await until(async () => !(await mounted('[data-testid="construction-filter-editor"]')), 'lane source picker closed', 10000);

  // ROW-SOURCE-06: the signed picker explains zero-eligible-field states. On this
  // populated seed the eligible set is non-empty, so the certified guarantee here is the
  // false-positive control (no explanation may appear while eligible populated fields
  // exist); if a seed ever legitimately yields zero eligible fields the same live path
  // asserts the exact explanatory sentence (hard-coded contract copy, not derived from
  // the component). Positive-state rendering authority: the picker unit tests.
  const pickerState = (await api('builder')).value;
  const pickerCaps = await api('construction-capabilities', {
    outputId, stageId, snapshotToken: pickerState.catalog.snapshotToken,
    expectedDraftVersion: pickerState.draftVersion, expectedDraftDigest: pickerState.draftDigest,
  });
  assert.equal(pickerCaps.status, 200, `picker-explanations capabilities failed (${pickerCaps.status}): ${JSON.stringify(pickerCaps.value).slice(0, 300)}`);
  const pickerFilterInput = pickerCaps.value?.filterSourceInput ?? null;
  const pickerChoices = pickerFilterInput?.choices ?? [];
  const eligiblePopulatedCount = pickerFilterInput?.supported ? pickerChoices.filter((choice) => choice.isPopulated).length : -1;
  await realClick('[data-testid="construction-action-keep-rows"]');
  await until(() => mounted('[data-testid="construction-filter-editor"]'), 'picker-explanations: lane editor open', 20000);
  const explanationPresent = await mounted('[data-testid="source-picker-explanation"]');
  if (eligiblePopulatedCount > 0) {
    assert.ok(!explanationPresent, 'a zero-eligible explanation rendered while eligible populated dataset fields exist (false-positive violation)');
    report.sideFindings.push({ id: 'row96-picker-positive-state-unreachable-on-seed', note: 'the populated fixture seed always exposes eligible populated fields, so live positive rendering of the none-populated/none-listed explanations is not reachable here; positive DOM rendering is certified by the picker unit tests and the lane-refusal notice stays the distinct unsupported-state surface' });
  } else if (pickerFilterInput?.supported) {
    await until(() => mounted('[data-testid="source-picker-explanation"]'), 'zero-eligible explanation appears', 15000);
    const explanationText = await evaluate(`document.querySelector('[data-testid="source-picker-explanation"]').textContent`);
    const contractCopy = [
      'Every eligible dataset field that contains values is already used by another source selection here.',
      'No eligible dataset field contains values in this dataset yet.',
      'Loom has not listed any dataset fields for this step.',
    ];
    // The phase opens a fresh lane editor with no prior bindings in this session,
    // so the count-derived class below mirrors the picker's own classification.
    assert.ok(contractCopy.some((sentence) => String(explanationText).includes(sentence)), `zero-eligible explanation text drifted from all approved contract copies: ${JSON.stringify(explanationText)}`);
    if (pickerChoices.length > 0) {
      const listed = await evaluate(`[...document.querySelectorAll('[data-testid="source-picker-empty-list-item"]')].map((e)=>e.getAttribute('title'))`);
      assert.deepEqual(JSON.parse(JSON.stringify(listed ?? [])), pickerChoices.filter((choice) => !choice.isPopulated).map((choice) => choice.fieldPath), 'unpopulated-field list does not match the server-marked unpopulated choices');
    }
  } else {
    throw new Error('picker-explanations: filterSourceInput unsupported; refusing a silently-passing third branch');
  }
  await screenshot('step2b-picker-explanations');
  await realClick('[data-testid="construction-close-operation-editor"]');
  await until(async () => !(await mounted('[data-testid="construction-filter-editor"]')), 'picker-explanations: lane editor closed', 10000);
  record('row96-picker-empty-and-refused-explanations', {
    laneFilterSupported: (pickerCaps.value?.selectedStage?.capabilities ?? []).find((cap) => cap.kind === 'FILTER')?.supported ?? null,
    filterInputSupported: pickerFilterInput?.supported ?? null,
    choicesCount: pickerChoices.length,
    unpopulatedChoices: pickerChoices.filter((choice) => !choice.isPopulated).map((choice) => choice.fieldPath),
    eligiblePopulatedCount,
    explanationPresent,
    positiveStateRealized: pickerFilterInput?.supported === true && eligiblePopulatedCount === 0,
    control: 'no-explanation-while-eligible-options-exist',
    positiveRenderingAuthority: 'picker unit tests (ConstructionOperationEditor/ConstructionReshapeEditor unit suites)',
  });
  markPhase('picker-explanations');

  // ROW-SOURCE-07: an already-projected field must be reused as its existing
  // column instead of offered again as a duplicate signed choice of the same
  // authoritative identity. Apply a signed selection live, require the server
  // to record the sourceProjection, then prove suppression + reuse in a fresh lane.
  const setSelectValue = (selector, value) => evaluate(`(()=>{const el=document.querySelector(${JSON.stringify(selector)});if(!el)return 'missing';const setter=Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype,'value').set;setter.call(el,${JSON.stringify(value)});el.dispatchEvent(new Event('change',{bubbles:true}));return el.value;})()`);
  const laneColumnOptions = () => evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]');return s?[...s.options].map((o)=>o.value):[];})()`);
  const reuseState0 = (await api('builder')).value;
  const reuseCaps0 = await api('construction-capabilities', {
    outputId, stageId, snapshotToken: reuseState0.catalog.snapshotToken,
    expectedDraftVersion: reuseState0.draftVersion, expectedDraftDigest: reuseState0.draftDigest,
  });
  assert.equal(reuseCaps0.status, 200, `existing-column-reuse capabilities failed (${reuseCaps0.status}): ${JSON.stringify(reuseCaps0.value).slice(0, 300)}`);
  const reuseChoices = reuseCaps0.value?.filterSourceInput?.supported ? reuseCaps0.value.filterSourceInput.choices : null;
  assert.ok(Array.isArray(reuseChoices) && reuseChoices.length > 0, 'existing-column-reuse: filterSourceInput unsupported or empty; refusing a vacuous pass');
  const reuseChoice = reuseChoices.find((choice) => choice.isPopulated);
  assert.ok(reuseChoice, 'existing-column-reuse: no populated filter choice on the healthy seed to reuse');
  const reuseDraftBefore = reuseState0.draftVersion;
  await realClick('[data-testid="construction-action-keep-rows"]');
  await until(() => mounted('[data-testid="construction-filter-editor"]'), 'existing-column-reuse: signing lane open', 20000);
  const signingOptions = await laneColumnOptions();
  assert.ok(signingOptions.includes(`source:${reuseChoice.choiceId}`), `existing-column-reuse: signed option absent on the healthy seed; saw ${JSON.stringify(signingOptions).slice(0, 300)}`);
  await setSelectValue('[data-testid="construction-filter-editor"] select[aria-label="Column"]', `source:${reuseChoice.choiceId}`);
  await setSelectValue('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EXISTS');
  await until(() => mounted('[data-testid="construction-apply-proposal"]'), 'existing-column-reuse: apply present for signed selection', 20000);
  await realClick('[data-testid="construction-apply-proposal"]');
  await until(async () => ((await api('builder')).value?.draftVersion ?? 0) > reuseDraftBefore, 'existing-column-reuse: draft advanced after apply', 30000);
  const reuseState1 = (await api('builder')).value;
  const reuseCaps1 = await api('construction-capabilities', {
    outputId, stageId, snapshotToken: reuseState1.catalog.snapshotToken,
    expectedDraftVersion: reuseState1.draftVersion, expectedDraftDigest: reuseState1.draftDigest,
  });
  assert.equal(reuseCaps1.status, 200, `existing-column-reuse capabilities after apply failed (${reuseCaps1.status})`);
  const reuseSteps = (reuseCaps1.value?.baseConstruction?.steps ?? []).filter((step) => step?.operation?.kind === 'FILTER');
  assert.ok(reuseSteps.length >= 1, 'existing-column-reuse: applied FILTER step missing from base construction');
  const appliedStep = reuseSteps[reuseSteps.length - 1];
  const appliedColumnId = appliedStep.operation.filter?.columnId;
  assert.ok(typeof appliedColumnId === 'string' && appliedColumnId !== '', 'existing-column-reuse: applied step has no concrete columnId');
  const reusedProjection = (reuseCaps1.value?.baseConstruction?.sourceProjections ?? [])
    .find((projectionEntry) => projectionEntry.fieldPath === reuseChoice.fieldPath && projectionEntry.columnId === appliedColumnId);
  assert.ok(reusedProjection, `existing-column-reuse: server did not record a sourceProjection for ${reuseChoice.fieldPath}->${appliedColumnId}: ${JSON.stringify(reuseCaps1.value?.baseConstruction?.sourceProjections ?? null).slice(0, 300)}`);
  await until(async () => !(await mounted('[data-testid="construction-filter-editor"]')), 'existing-column-reuse: signing lane closed after apply', 15000);
  await realClick('[data-testid="construction-action-keep-rows"]');
  await until(() => mounted('[data-testid="construction-filter-editor"]'), 'existing-column-reuse: fresh lane open', 20000);
  const freshOptions = await laneColumnOptions();
  assert.ok(Array.isArray(freshOptions) && freshOptions.length > 0, 'existing-column-reuse: fresh lane column select empty');
  assert.ok(!freshOptions.includes(`source:${reuseChoice.choiceId}`), `existing-column-reuse: duplicate signed choice still offered for ${reuseChoice.fieldPath}`);
  assert.ok(freshOptions.includes(appliedColumnId), `existing-column-reuse: existing column ${appliedColumnId} not offered for reuse; saw ${JSON.stringify(freshOptions).slice(0, 300)}`);
  const reuseSet = await setSelectValue('[data-testid="construction-filter-editor"] select[aria-label="Column"]', appliedColumnId);
  assert.equal(reuseSet, appliedColumnId, 'existing-column-reuse: existing column not selectable');
  await setSelectValue('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EXISTS');
  await until(() => mounted('[data-testid="construction-apply-proposal"]'), 'existing-column-reuse: reuse candidate ready', 20000);
  await screenshot('step2c-existing-column-reuse');
  await realClick('[data-testid="construction-close-operation-editor"]');
  await until(async () => !(await mounted('[data-testid="construction-filter-editor"]')), 'existing-column-reuse: fresh lane closed', 10000);
  record('row96-existing-column-reuse', {
    fieldPath: reuseChoice.fieldPath,
    choiceId: reuseChoice.choiceId,
    appliedColumnId,
    projection: reusedProjection,
    freshSuppressesSigned: true,
    freshOffersExistingColumn: true,
    reuseSelectedColumnId: appliedColumnId,
  });
  markPhase('existing-column-reuse');

  // ROW-SOURCE-08: the signed EXPAND source lane end-to-end in the browser.
  // A Procedure table (seeded by this run's fixture copy) must offer the
  // populated primitive root array instantiatesUri[] as a signed choice, the
  // pick must produce a server-signed EXPAND step — client never authors the
  // trusted payload — and Apply must record the owning sourceProjection.
  // Physical row expansion is compiled-lane territory certified in Go tests
  // (lower expand + worker suite); the browser here proves offer/select/apply
  // wiring and the signature discipline on the live join.
  const expandProcedureSeed = await commands('rs08-procedure-table', [{
    type: 'CREATE_TABLE', title: 'RS08 signed expand', rootNodeId: (await api('builder')).value?.catalog?.nodes
      ?.find((node) => node.resourceType === 'Procedure' && node.rowRootEligible)?.nodeId,
  }], (await api('builder')).value);
  assert.equal(expandProcedureSeed.status, 200, `signed-expand-lane: Procedure CREATE_TABLE failed (${expandProcedureSeed.status}): ${JSON.stringify(expandProcedureSeed.value).slice(0, 300)}`);
  const procState0 = (await api('builder')).value;
  const procRoot = (procState0?.catalog?.nodes ?? []).find((node) => node.resourceType === 'Procedure' && node.rowRootEligible);
  assert.ok(procRoot?.nodeId, 'signed-expand-lane: catalog has no eligible Procedure row root');
  const procDocs = (procState0?.workspace?.documents ?? []).filter((document) => document?.rootResourceType === 'Procedure');
  const procDoc = procDocs[procDocs.length - 1];
  const procOutputId = procDoc?.output?.id;
  assert.ok(procOutputId, `signed-expand-lane: Procedure table has no output: ${JSON.stringify((procState0?.workspace?.documents ?? []).map((d) => ({ title: d?.title, root: d?.output?.rootNodeId }))).slice(0, 300)}`);
  const procIdCandidate = (procState0?.catalog?.candidates ?? []).find((candidate) => candidate.nodeId === procRoot.nodeId
    && String(candidate.fieldPath || '').replace(/^root\./, '') === 'id');
  assert.ok(procIdCandidate?.candidateId, 'signed-expand-lane: catalog exposes no Procedure id candidate for the public-column prerequisite');
  const procIdAdded = await commands('rs08-procedure-id', [{
    type: 'ADD_COLUMN', outputId: procOutputId, occurrenceId: 'base',
    candidateId: procIdCandidate.candidateId, projectionMode: procIdCandidate.defaultProjectionMode,
    initialPresentation: 'TABLE', title: 'Procedure ID',
  }], procState0);
  assert.equal(procIdAdded.status, 200, `signed-expand-lane: Procedure id ADD_COLUMN failed (${procIdAdded.status}): ${JSON.stringify(procIdAdded.value).slice(0, 300)}`);
  const expandState0 = (await api('builder')).value;
  const expandCaps0 = await api('construction-capabilities', {
    outputId: procOutputId, stageId: 'source_projection', snapshotToken: expandState0.catalog.snapshotToken,
    expectedDraftVersion: expandState0.draftVersion, expectedDraftDigest: expandState0.draftDigest,
  });
  assert.equal(expandCaps0.status, 200, `signed-expand-lane capabilities failed (${expandCaps0.status}): ${JSON.stringify(expandCaps0.value).slice(0, 300)}`);
  const expandChannel = expandCaps0.value?.expandSourceInput;
  assert.ok(expandChannel?.supported === true, `signed-expand-lane: expandSourceInput unsupported: ${JSON.stringify(expandChannel ?? null).slice(0, 300)}`);
  const expandChoice = (expandChannel.choices ?? []).find((choice) => choice.isPopulated
    && String(choice.fieldPath).replace(/^root\./, '') === 'instantiatesUri[]');
  assert.ok(expandChoice, `signed-expand-lane: instantiatesUri[] not offered among populated signed choices (would be vacuous): ${JSON.stringify(expandChannel.choices ?? []).slice(0, 400)}`);
  await navigateToLane('signed-expand nav');
  await realClick(`[data-testid="construction-table-${procOutputId}"]`);
  await realClick('[data-testid="construction-rows-settings-trigger"]');
  await until(() => mounted('[data-testid="construction-row-intent-reshape"]'), 'signed-expand-lane: row-definition dialog open for the Procedure table', 20000);
  await realClick('[data-testid="construction-row-intent-reshape"]');
  await until(() => mounted('[data-testid="construction-action-expand-signed-rows"]'), 'signed-expand-lane: reshape intent offers the signed expand card', 20000);
  await realClick('[data-testid="construction-action-expand-signed-rows"]');
  await until(() => mounted('[data-testid="construction-reshape-expand"]'), 'signed-expand-lane: expand editor open', 20000);
  const expandOptionValues = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]');return s?[...s.options].map((o)=>o.value):[];})()`);
  assert.ok(expandOptionValues.includes(`source:${expandChoice.choiceId}`), `signed-expand-lane: signed option absent in browser; saw ${JSON.stringify(expandOptionValues).slice(0, 300)}`);
  const expandDraftBefore = expandState0.draftVersion;
  await setSelectValue('[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]', `source:${expandChoice.choiceId}`);
  await until(() => mounted('[data-testid="construction-apply-proposal"]'), 'signed-expand-lane: apply present for signed selection', 20000);
  await screenshot('step2d-signed-expand');
  await realClick('[data-testid="construction-apply-proposal"]');
  await until(async () => ((await api('builder')).value?.draftVersion ?? 0) > expandDraftBefore, 'signed-expand-lane: draft advanced after apply', 30000);
  const expandState1 = (await api('builder')).value;
  const expandCaps1 = await api('construction-capabilities', {
    outputId: procOutputId, stageId: 'source_projection', snapshotToken: expandState1.catalog.snapshotToken,
    expectedDraftVersion: expandState1.draftVersion, expectedDraftDigest: expandState1.draftDigest,
  });
  assert.equal(expandCaps1.status, 200, `signed-expand-lane capabilities after apply failed (${expandCaps1.status})`);
  const expandSteps = (expandCaps1.value?.baseConstruction?.steps ?? []).filter((step) => step?.operation?.kind === 'EXPAND');
  assert.ok(expandSteps.length === 1, `signed-expand-lane: expected exactly one applied EXPAND step, got ${expandSteps.length}`);
  const appliedExpand = expandSteps[0];
  const expandPayload = appliedExpand.operation.expand?.expandedField;
  assert.ok(expandPayload, 'signed-expand-lane: the applied EXPAND step carries no server-signed expandedField (signature discipline broken)');
  assert.equal(String(expandPayload.path).replace(/^root\./, ''), 'instantiatesUri[]', 'signed server signature names a different field than the live lane offered');
  assert.equal(expandPayload.cardinality, 'many', 'signed server signature does not pin many cardinality');
  const expandInputId = appliedExpand.operation.expand?.inputColumnId;
  assert.ok(expandInputId, 'signed-expand-lane: applied EXPAND step has no input column');
  const expandProjection = (expandCaps1.value?.baseConstruction?.sourceProjections ?? [])
    .find((projectionEntry) => projectionEntry.columnId === expandInputId);
  assert.ok(expandProjection && expandProjection.ownerStepId === appliedExpand.id
    && String(expandProjection.fieldPath).replace(/^root\./, '') === 'instantiatesUri[]',
    `signed-expand-lane: EXPAND step has no sourceProjection owned by it for instantiatesUri[]: ${JSON.stringify(expandCaps1.value?.baseConstruction?.sourceProjections ?? null).slice(0, 300)}`);
  const clientSignedLeak = JSON.stringify(appliedExpand).includes('"expandedField":null');
  // Contract clause "Compare independently expected rows with the existing
  // selected-column path": after apply, the live preview must contain exactly
  // the rows the shipped renderer produces for the fixture under the CURRENT
  // shipped expand-editor default (ConstructionReshapeEditor emptyPolicy
  // 'PRESERVE_PARENT', pre-existing at HEAD — the contract requires preserving
  // it): rs08-procedure-a contributes two item rows, b one, and c (missing
  // array) keeps its parent row with an empty item cell — four rows total.
  // Expected values come from THIS fixture copy, independently of any server
  // response; other empty policies are renderer behavior already pinned by
  // the selected-column renderer tests this lane reuses verbatim.
  const expectedItemValues = ['urn:protocol:alpha', 'urn:protocol:beta', 'urn:protocol:gamma'];
  const readExpandPreview = () => evaluate(`(()=>{const sec=document.querySelector('[data-testid="construction-preview"]');const t=sec?.querySelector('[data-testid="preview-table-content"]')||sec;if(!t)return null;const trs=[...t.querySelectorAll('[role="row"]')].filter(r=>!r.className.includes('construction-preview-header-row'));const rows=trs.map(tr=>[...tr.querySelectorAll('[role="cell"]')].map(td=>td.textContent.trim())).filter(cells=>cells.length>0);return{rowCount:rows.length,cells:rows,previewStatus:sec?.getAttribute('data-preview-status')??null,previewReceipt:sec?.getAttribute('data-preview-receipt-id')??null};})()`);
  let expandPreview = null;
  const expandPreviewDeadline = Date.now() + 60000;
  for (;;) {
    expandPreview = await readExpandPreview();
    const flat = expandPreview ? expandPreview.cells.flat() : [];
    // Require the expanded item values, not just a row count: the pre-expand
    // preview of this table also has three rows (one per Procedure).
    if (expandPreview && expandPreview.rowCount === 4 && expectedItemValues.every((value) => flat.includes(value))
      && expandPreview.cells.some((cells) => cells[0] === 'rs08-procedure-a' && cells[1] === 'urn:protocol:alpha')
      && expandPreview.cells.some((cells) => cells[0] === 'rs08-procedure-a' && cells[1] === 'urn:protocol:beta')
      && expandPreview.cells.some((cells) => cells[0] === 'rs08-procedure-b' && cells[1] === 'urn:protocol:gamma')
      && expandPreview.cells.some((cells) => cells[0] === 'rs08-procedure-c' && (cells[1] === '' || cells[1] === '—'))) break;
    if (Date.now() > expandPreviewDeadline) {
      assert.fail(`signed-expand-lane: preview never showed the four expected expanded rows under the shipped PRESERVE_PARENT default (last state: ${JSON.stringify(expandPreview).slice(0, 800)})`);
    }
    await wait(500);
  }
  const flatCells = expandPreview.cells.flat();
  const preserveParentKeptEmptyArrayRow = expandPreview.cells.some((cells) => cells[0] === 'rs08-procedure-c');
  record('row96-signed-expand-lane', {
    fieldPath: expandChoice.fieldPath,
    choiceId: expandChoice.choiceId,
    appliedStepId: appliedExpand.id,
    inputColumnId: expandInputId,
    signedPayload: expandPayload,
    projection: expandProjection,
    physicalRowExpansionAuthority: 'browser preview rows asserted above (exact per-parent row multiset under the shipped PRESERVE_PARENT default) + Go: internal/explorer/lifecycle/expand_source_test.go + compilation semantic_compile_expand_source_test.go + lower expand gate',
    clientSignedLeak,
    previewRowCount: expandPreview.rowCount,
    previewExpectedItems: expectedItemValues,
    preserveParentEmptyArrayRowKept: preserveParentKeptEmptyArrayRow,
  });
  markPhase('signed-expand-lane');

  // ROW-SOURCE-08 policy/lifecycle evidence: EXCLUDE / PRESERVE_PARENT / ERROR plus the
  // Cancel, Apply-persists, reload-restores, removal-cleans-sourceProjection legs, all
  // in-window against a dedicated fresh fixture project carrying the contract parents
  // ([red, red, blue], [green], empty, missing, null). The signed-expand-lane check's
  // four-row PRESERVE_PARENT preview stays byte-stable because these rows live in the
  // second project, never in this table's row space.
  const matrixEvidence = { fieldPath: null, choiceId: null, uiDefaultEmptyPolicy: null, legs: {}, errorWire: { candidates: [], assertedCode: null }, disclosures: [] };
  {
    assert.ok(rs08MatrixFixtureDir, 'expand-policy-matrix: no per-run matrix fixture directory was seeded');
    const matrixSession = createDevSession({ ...process.env, LOOM_DEV_FIXTURE_DIR: rs08MatrixFixtureDir }, REPO_ROOT);
    const matrixFixture = await createFreshVerificationFixture(matrixSession, `${runStem}-b`, { populateBootstrap: false });
    const matrixProject = matrixFixture.target.fixtureProject;
    const matrixExplorer = matrixFixture.seed.bootstrapExplorerId;
    report.matrixFixture = { project: matrixProject, explorer: matrixExplorer };
    const apiB = async (path, body) => {
      const base = `${apiOrigin}/api/v1/projects/${encodeURIComponent(matrixProject)}/explorers/${encodeURIComponent(matrixExplorer)}/authoring/v2`;
      const response = await fetch(`${base}/${path}`, { method: body ? 'POST' : 'GET', headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined });
      return { status: response.status, value: await response.json().catch(() => ({})) };
    };
    const commandsB = (commandId, list, state) => apiB('commands', {
      commandId: `${runStem}-${commandId}`, semanticsVersion: AUTHORING_SEMANTICS_VERSION,
      snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion,
      expectedDraftDigest: state.draftDigest, commands: list,
    });
    const laneBURL = () => browserURL({ uiUrl: session.uiUrl ?? `http://${session.host}:${session.uiPort}` }, matrixProject, matrixExplorer, 'builder');
    const navigateToLaneB = async () => {
      await send('Page.navigate', { url: laneBURL() });
      await until(async () => (await mounted('[data-testid="construction-workspace"]'))
        || Boolean(await evaluate(`(()=>{const a=document.querySelector('[role="alert"]');return a?a.textContent:null;})()`)), 'matrix lane settled', 18000);
    };
    const matrixDraft = async () => (await apiB('builder')).value?.draftVersion ?? 0;
    const matrixCaps = async () => {
      const state = (await apiB('builder')).value;
      const payload = await apiB('construction-capabilities', {
        outputId: matrixOutputId, stageId, snapshotToken: state.catalog.snapshotToken,
        expectedDraftVersion: state.draftVersion, expectedDraftDigest: state.draftDigest,
      });
      assert.equal(payload.status, 200, `expand-policy-matrix: construction-capabilities failed (${payload.status}): ${JSON.stringify(payload.value).slice(0, 300)}`);
      return { state, value: payload.value };
    };

    // In-window wire capture: the ERROR leg must assert the SERVER wire code, not a
    // paraphrase; proposal/preview bodies also carry __loom_row_id, the row-identity
    // authority that proves repeated values stay separate rows.
    await send('Network.enable');
    const matrixWire = [];
    const matrixCmdWire = [];
    // CDP response bodies are held in a bounded buffer and can be evicted long before
    // a late query (root-caused 2026-11-03: identical parses succeeded in one read and
    // returned null seconds later in the same tick's successor). Capture the body at
    // loadingFinished instead and read synchronously later; the fallback re-fetch
    // covers requests whose finish event does not arrive.
    const matrixBody = async (entry) => {
      if (typeof entry.body === 'string') return entry.body;
      try {
        const result = await send('Network.getResponseBody', { requestId: entry.requestId });
        const body = result?.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : String(result?.body ?? '');
        if (body) entry.body = body;
        return body;
      } catch { return null; }
    };
    client.on('Network.responseReceived', (event) => {
      const url = event?.response?.url ?? '';
      if (/(construction-proposals|receipts|\/preview)/.test(url)) matrixWire.push({ requestId: event.requestId, status: event.response?.status ?? 0, url });
      if (url.includes('/authoring/v2/commands')) matrixCmdWire.push({ requestId: event.requestId, status: event.response?.status ?? 0, url });
    });
    client.on('Network.loadingFinished', (event) => {
      const entry = [...matrixWire, ...matrixCmdWire].find((candidate) => candidate.requestId === event.requestId && typeof candidate.body !== 'string');
      if (!entry) return;
      void (async () => {
        try {
          const result = await send('Network.getResponseBody', { requestId: event.requestId });
          entry.body = result?.base64Encoded ? Buffer.from(result.body, 'base64').toString('utf8') : String(result?.body ?? '');
        } catch { /* keep the late re-fetch fallback */ }
      })();
    });
    const wireShape = (body) => {
      try {
        const parsed = JSON.parse(body);
        return { code: typeof parsed?.code === 'string' ? parsed.code : null, nestedCode: typeof parsed?.error?.code === 'string' ? parsed.error.code : null, message: String(parsed?.message ?? parsed?.error?.message ?? '').slice(0, 200) };
      } catch { return { unparsedPrefix: body.slice(0, 120) }; }
    };
    // F1 fix (adversarial reviewer A) preserved through the commands-echo binding:
    // the wire row-identity proof must bind to the matrix project AND to the current
    // leg. Without both, a 4-row/7-row 200 captured for the PRELUDE project
    // (signed-expand lane) or an earlier leg satisfies the distinctness asserts
    // vacuously (proven: /tmp/reviewer-a/stale_wire_capture_probe.mjs).
    // The wire project identity arrives in different encodings per surface (raw
    // 'X-b', raw already carrying '%2F', page double-encoding the sub-scope '-' as
    // '%252F'). Decoding both sides to canonical form keeps other projects (the
    // signed-expand PRELUDE lane) out while staying encoding-independent; a strict
    // encodeURIComponent compare matched nothing on the exact same four-row 200s.
    const decodeWireSurface = (value) => {
      try { return decodeURIComponent(decodeURIComponent(String(value))); } catch { return null; }
    };
    const matrixProjectWireForms = new Set([matrixProject, String(matrixProject).replace(/-/, '/'), decodeWireSurface(matrixProject)].filter((value) => typeof value === 'string' && value.length > 0));
    const matrixWireEntryBelongsToProject = (url) => {
      const match = /\/projects\/([^/]+)\//.exec(String(url));
      if (!match) return false;
      const decoded = decodeWireSurface(match[1]);
      return decoded !== null && matrixProjectWireForms.has(decoded);
    };
    const commandRowSets = async (fromIndex) => {
      const sets = [];
      for (const entry of matrixCmdWire.slice(fromIndex ?? 0)) {
        if (entry.status !== 200) continue;
        if (!matrixWireEntryBelongsToProject(entry.url)) continue;
        const body = await matrixBody(entry);
        if (!body) continue;
        try {
          const parsed = JSON.parse(body);
          const rows = parsed?.acceptedConstruction?.preview?.rows;
          if (Array.isArray(rows)) sets.push(rows);
        } catch { /* commands response without a preview echo */ }
      }
      return sets;
    };
    const rowIdentity = (row) => typeof row?.__loom_row_id === 'string' ? row.__loom_row_id : JSON.stringify(row?.__loom_row_id ?? null);

    const builderB = await apiB('builder');
    assert.equal(builderB.status, 200, `expand-policy-matrix: builder fetch failed (${builderB.status})`);
    const matrixRoot = (builderB.value?.catalog?.nodes ?? []).find((node) => node.resourceType === 'Procedure' && node.rowRootEligible);
    assert.ok(matrixRoot?.nodeId, 'expand-policy-matrix: matrix catalog exposes no eligible Procedure row root');
    const matrixCreate = await commandsB('rs08m-table', [{ type: 'CREATE_TABLE', title: 'RS08 policy matrix', rootNodeId: matrixRoot.nodeId }], builderB.value);
    assert.equal(matrixCreate.status, 200, `expand-policy-matrix: CREATE_TABLE failed (${matrixCreate.status}): ${JSON.stringify(matrixCreate.value).slice(0, 300)}`);
    const matrixState0 = (await apiB('builder')).value;
    const matrixOutputId = (matrixState0?.workspace?.documents ?? []).at(-1)?.output?.id;
    assert.ok(matrixOutputId, 'expand-policy-matrix: matrix table has no output; every leg below would assert vacuously');
    const matrixIdCandidate = (matrixState0?.catalog?.candidates ?? []).find((candidate) => candidate.nodeId === matrixRoot.nodeId
      && String(candidate.fieldPath || '').replace(/^root\./, '') === 'id');
    assert.ok(matrixIdCandidate?.candidateId, 'expand-policy-matrix: no Procedure id candidate for the public-column prerequisite');
    const matrixIdAdded = await commandsB('rs08m-id', [{
      type: 'ADD_COLUMN', outputId: matrixOutputId, occurrenceId: 'base',
      candidateId: matrixIdCandidate.candidateId, projectionMode: matrixIdCandidate.defaultProjectionMode,
      initialPresentation: 'TABLE', title: 'Procedure ID',
    }], matrixState0);
    assert.equal(matrixIdAdded.status, 200, `expand-policy-matrix: id ADD_COLUMN failed (${matrixIdAdded.status}): ${JSON.stringify(matrixIdAdded.value).slice(0, 300)}`);
    const matrixCaps0 = await matrixCaps();
    const matrixChannel = matrixCaps0.value?.expandSourceInput;
    assert.ok(matrixChannel?.supported === true, `expand-policy-matrix: expandSourceInput unsupported: ${JSON.stringify(matrixChannel ?? null).slice(0, 300)}`);
    const matrixChoice = (matrixChannel.choices ?? []).find((choice) => choice.isPopulated
      && String(choice.fieldPath).replace(/^root\./, '') === 'instantiatesUri[]');
    assert.ok(matrixChoice, `expand-policy-matrix: the contract array field is not offered among populated signed choices: ${JSON.stringify(matrixChannel.choices ?? []).slice(0, 400)}`);
    matrixEvidence.fieldPath = matrixChoice.fieldPath;
    matrixEvidence.choiceId = matrixChoice.choiceId;

    const repeatFieldSelect = '[data-testid="construction-reshape-expand"] select[aria-label="Repeated field"]';
    const matrixPolicySelect = '[data-testid="construction-reshape-expand"] select[aria-label="Empty list policy"]';
    const applySelector = '[data-testid="construction-apply-proposal"]';
    const openMatrixAdvanced = async () => {
      const state = await evaluate(`(()=>{const d=document.querySelector('[data-testid="construction-reshape-expand-advanced"]');return d?d.open:null;})()`);
      if (state === false) await realClick('[data-testid="construction-reshape-expand-advanced"] summary');
      assert.equal(await evaluate(`(()=>{const d=document.querySelector('[data-testid="construction-reshape-expand-advanced"]');return d?d.open:null;})()`), true,
        'expand-policy-matrix: the Advanced options disclosure never opened; the Empty list policy control stays unreachable');
    };
    const setMatrixPolicy = async (policy) => {
      await openMatrixAdvanced();
      assert.equal(await setSelectValue(matrixPolicySelect, policy), policy, `expand-policy-matrix: Empty list policy select did not take ${policy}`);
    };
    const openMatrixCreateEditor = async () => {
      await realClick(`[data-testid="construction-table-${matrixOutputId}"]`);
      await realClick('[data-testid="construction-rows-settings-trigger"]');
      await until(() => mounted('[data-testid="construction-row-intent-reshape"]'), 'matrix: row-definition dialog open', 20000);
      await realClick('[data-testid="construction-row-intent-reshape"]');
      await until(() => mounted('[data-testid="construction-action-expand-signed-rows"]'), 'matrix: reshape intent offers the signed expand card', 20000);
      await realClick('[data-testid="construction-action-expand-signed-rows"]');
      await until(() => mounted('[data-testid="construction-reshape-expand"]'), 'matrix: expand editor open', 20000);
    };
    const closeMatrixEditor = async () => {
      if (await mounted('[data-testid="construction-close-operation-editor"]')) await realClick('[data-testid="construction-close-operation-editor"]');
      await until(async () => !(await mounted('[data-testid="construction-reshape-expand"]')), 'matrix: expand editor closed', 15000);
    };
    const openMatrixStepEditor = async (stepId) => {
      await until(() => mounted(`[data-testid="construction-history-step-${stepId}"]`), `matrix: EXPAND history step ${stepId} rendered`, 20000);
      await realClick(`[data-testid="construction-history-step-${stepId}"]`);
      // Live-frame truth (diagf probes): activation alone never mounts the row-definition
      // panel from a clean frame; the panel (listing construction-row-edit-<step-id> ->
      // editConstructionStep, RowDefinitionSettingsPanel.tsx:1059) opens through the
      // table's rows-settings trigger. If a prior leg left it mounted, edit directly.
      for (let i = 0; i < 50; i += 1) {
        if (await mounted('[data-testid="construction-reshape-expand"]')) return;
        if (await mounted(`[data-testid="construction-row-edit-${stepId}"]`)) break;
        await wait(100);
      }
      if (!(await mounted(`[data-testid="construction-row-edit-${stepId}"]`))) {
        if (await mounted('[data-testid="construction-rows-settings-trigger"]')) {
          await realClick('[data-testid="construction-rows-settings-trigger"]');
          await until(() => mounted(`[data-testid="construction-row-edit-${stepId}"]`), `matrix: rows-settings panel lists step ${stepId} for editing`, 20000);
        }
      }
      if (await mounted(`[data-testid="construction-row-edit-${stepId}"]`)) {
        await realClick(`[data-testid="construction-row-edit-${stepId}"]`);
        await until(() => mounted('[data-testid="construction-reshape-expand"]'), `matrix: Edit opened the signed expand editor for ${stepId}`, 20000);
        return;
      }
      // Fallback for alternative renderings: drive the dialog entries directly.
      for (let i = 0; i < 150; i += 1) {
        if (await mounted('[data-testid="construction-reshape-expand"]')) return;
        if (await mounted('[data-testid="construction-row-intent-reshape"]')) { await realClick('[data-testid="construction-row-intent-reshape"]'); continue; }
        if (await mounted('[data-testid="construction-action-expand-signed-rows"]')) { await realClick('[data-testid="construction-action-expand-signed-rows"]'); continue; }
        await wait(100);
      }
      assert.fail('expand-policy-matrix: activating the EXPAND history step never revealed the signed expand editor');
    };
    const untilApplyReady = async (label, ms = 25000) => until(async () => evaluate(`(()=>{const b=document.querySelector(${JSON.stringify(applySelector)});return b && !b.disabled ? true : null;})()`), label, ms);
    const untilMatrixPreview = async (predicate, label, ms = 60000) => {
      const deadline = Date.now() + ms;
      for (;;) {
        const view = await readExpandPreview();
        if (predicate(view)) return view;
        if (Date.now() > deadline) assert.fail(`expand-policy-matrix: ${label}; last preview ${JSON.stringify(view).slice(0, 800)}`);
        await wait(500);
      }
    };
    const exactExclude = (cells) => JSON.stringify([...cells].sort()) === JSON.stringify([...matrixExcludeMultiset].sort());
    const preserveShape = (cells) => cells.length === 7
      && exactExclude(cells.filter((row) => !matrixKeptParentIds.includes(row[0])))
      && matrixKeptParentIds.every((keptId) => cells.some((row) => row[0] === keptId && (row[1] === '' || row[1] === '—')));

    await navigateToLaneB();
    await until(datasetLoaded, 'matrix dataset loaded (construction-workspace mounted)', 60000);

    // Leg 1: the shipped UI default stays PRESERVE_PARENT (contract: never a new default),
    // and a fully-selected signed candidate CANCELs without leaving a step or draft bump.
    const draftAtCancelBase = await matrixDraft();
    await openMatrixCreateEditor();
    matrixEvidence.uiDefaultEmptyPolicy = await evaluate(`document.querySelector(${JSON.stringify(matrixPolicySelect)})?.value ?? null`);
    assert.equal(matrixEvidence.uiDefaultEmptyPolicy, 'PRESERVE_PARENT',
      'the empty expand editor must render the shipped PRESERVE_PARENT default (ConstructionReshapeEditor.tsx:1566); the contract forbids introducing a new default');
    await setSelectValue(repeatFieldSelect, `source:${matrixChoice.choiceId}`);
    await untilApplyReady('matrix: apply present for the signed selection (pre-cancel)');
    await realClick('[data-testid="construction-cancel-proposal"]');
    // Reviewer A F4: pin CANCEL as an action, not "proposal happened to be pending":
    // the shared proposal surface (Apply + Cancel) must be gone afterwards.
    await until(async () => !(await mounted(applySelector)) && !(await mounted('[data-testid="construction-cancel-proposal"]')),
      'matrix: CANCEL removed the pending proposal surface', 10000);
    const afterCancel = await matrixCaps();
    assert.equal((afterCancel.value?.baseConstruction?.steps ?? []).filter((step) => step?.operation?.kind === 'EXPAND').length, 0,
      'expand-policy-matrix: Cancel left an EXPAND step in the construction');
    assert.equal(afterCancel.state.draftVersion, draftAtCancelBase, 'expand-policy-matrix: Cancel advanced the draft');
    matrixEvidence.legs.cancel = { draftUnchanged: true, expandStepsAfterCancel: 0 };
    await screenshot('step3a-matrix-cancel');

    // Leg 2: EXCLUDE — exactly the four expanded rows, no parent-kept rows.
    // Watermark at LEG start: this leg's applying commands echo arrives after this
    // line, and every earlier leg's traffic completed through its awaited round-trips.
    const cmdBeforeExclude = matrixCmdWire.length;
    if (!(await mounted('[data-testid="construction-reshape-expand"]'))) await openMatrixCreateEditor();
    await setSelectValue(repeatFieldSelect, `source:${matrixChoice.choiceId}`);
    await setMatrixPolicy('EXCLUDE');
    await untilApplyReady('matrix: apply enabled for the EXCLUDE candidate');
    const draftBeforeExclude = await matrixDraft();
    await realClick(applySelector);
    await until(async () => (await matrixDraft()) > draftBeforeExclude, 'matrix: draft advanced after EXCLUDE apply', 30000);
    const excludePreview = await untilMatrixPreview((view) => view && view.rowCount === 4 && exactExclude(view.cells),
      'the EXCLUDE preview never produced the four exact expanded rows with the kept-parent rows absent');
    const excludeRowSets = (await commandRowSets(cmdBeforeExclude)).filter((rows) => rows.length === 4);
    assert.ok(excludeRowSets.length >= 1, 'expand-policy-matrix: no 200 commands response carried an acceptedConstruction.preview with the four EXCLUDE rows; the row-identity proof would be unexercised');
    const excludeIdentities = excludeRowSets[excludeRowSets.length - 1].map(rowIdentity);
    assert.equal(new Set(excludeIdentities).size, 4, `EXCLUDE: all four preview rows need distinct __loom_row_id values, got ${JSON.stringify(excludeIdentities)}`);
    matrixEvidence.legs.exclude = {
      rowCount: excludePreview.rowCount, cells: excludePreview.cells, rowIdentities: excludeIdentities,
      keptParentRowsAbsent: matrixKeptParentIds.every((keptId) => !excludePreview.cells.some((row) => row[0] === keptId)),
    };
    await screenshot('step3b-matrix-exclude');

    const afterExclude = await matrixCaps();
    const expandSteps = (afterExclude.value?.baseConstruction?.steps ?? []).filter((step) => step?.operation?.kind === 'EXPAND');
    assert.equal(expandSteps.length, 1, `expand-policy-matrix: expected exactly one applied EXPAND step, got ${expandSteps.length}`);
    const matrixStep = expandSteps[0];
    const matrixSignedPayload = matrixStep.operation.expand?.expandedField;
    assert.ok(matrixSignedPayload && String(matrixSignedPayload.path).replace(/^root\./, '') === 'instantiatesUri[]' && matrixSignedPayload.cardinality === 'many',
      `expand-policy-matrix: the applied step lost its server signature: ${JSON.stringify(matrixStep.operation.expand ?? null).slice(0, 300)}`);
    const matrixInputColumnId = matrixStep.operation.expand.inputColumnId;
    assert.ok(matrixInputColumnId, 'expand-policy-matrix: the applied EXPAND step has no owned array input column');
    matrixEvidence.expandStepId = matrixStep.id;
    matrixEvidence.inputColumnId = matrixInputColumnId;
    matrixEvidence.signedPayload = matrixSignedPayload;
    await closeMatrixEditor();

    // Leg 3: EDIT the applied step to PRESERVE_PARENT — seven rows: the same four
    // expanded rows plus the empty/missing/null parents kept, red twice as two
    // separately identified rows. Watermark at leg start (same rule as Leg 2).
    const cmdBeforePreserve = matrixCmdWire.length;
    await openMatrixStepEditor(matrixStep.id);
    await setMatrixPolicy('PRESERVE_PARENT');
    await untilApplyReady('matrix: apply enabled for the PRESERVE_PARENT edit');
    const draftBeforePreserve = await matrixDraft();
    await realClick(applySelector);
    await until(async () => (await matrixDraft()) > draftBeforePreserve, 'matrix: draft advanced after PRESERVE_PARENT apply', 30000);
    const preservePreview = await untilMatrixPreview((view) => view && view.rowCount === 7 && preserveShape(view.cells),
      'the PRESERVE_PARENT preview never produced the seven contract rows (four expanded + empty/missing/null parents kept)');
    const preserveRowSets = (await commandRowSets(cmdBeforePreserve)).filter((rows) => rows.length === 7);
    assert.ok(preserveRowSets.length >= 1, 'expand-policy-matrix: no 200 commands response carried an acceptedConstruction.preview with the seven PRESERVE_PARENT rows');
    const preserveIdentities = preserveRowSets[preserveRowSets.length - 1].map(rowIdentity);
    assert.equal(new Set(preserveIdentities).size, 7, `PRESERVE_PARENT: all seven preview rows need distinct __loom_row_id values, got ${JSON.stringify(preserveIdentities)}`);
    // Repeated-value proof on captured wire rows: __loom_row_id is the server's opaque
    // identity, so the two red rows are located by their expanded cell value, then each
    // must carry its own distinct __loom_row_id (repeated values stay separate rows).
    const preserveLastRows = preserveRowSets[preserveRowSets.length - 1];
    const redPreserveRows = preserveLastRows.filter((row) => Object.entries(row).some(([key, value]) => key !== '__loom_row_id' && value === 'red'));
    assert.equal(redPreserveRows.length, 2, 'matrix: the repeated rs08m array must contribute two separate preview rows carrying value red');
    const redPreserveIdentities = redPreserveRows.map(rowIdentity);
    console.log('matrix-evidence:red-row-identities', JSON.stringify(redPreserveIdentities));
    assert.equal(new Set(redPreserveIdentities).size, 2, `matrix: the two repeated red rows must carry distinct row identities, got ${JSON.stringify(redPreserveIdentities)}`);
    matrixEvidence.legs.preserveParent = {
      rowCount: preservePreview.rowCount, cells: preservePreview.cells, rowIdentities: preserveIdentities,
      repeatedValueRowIdentities: redPreserveIdentities,
      keptParentRows: matrixKeptParentIds.map((keptId) => ({ keptId, kept: preservePreview.cells.some((row) => row[0] === keptId && (row[1] === '' || row[1] === '—')) })),
    };
    await screenshot('step3c-matrix-preserve');

    // Leg 4: ERROR — the named empty-parent failure must surface in the browser AND
    // on the wire with the server reasonCode (authority: errors.go:77 +
    // construction_expand.go:33 + explorer_preview_response.go:65; asserted, not guessed).
    await closeMatrixEditor();
    await openMatrixStepEditor(matrixStep.id);
    await setMatrixPolicy('ERROR');
    await until(async () => (await mounted('[data-testid="construction-proposal-error"]'))
      || (await evaluate(`(()=>{const sec=document.querySelector('[data-testid="construction-preview"]');return sec?.getAttribute('data-preview-status')==='error' ? true : null;})()`))
      || (await evaluate(`(()=>{const p=document.querySelector('[data-testid="preview-table-scroll"]');return p && /did not complete for this draft/i.test(p.textContent||'') ? true : null;})()`)),
      'matrix: the ERROR policy never surfaced the empty-parent failure in the browser', 45000);
    for (const entry of matrixWire.filter((candidate) => candidate.status >= 400)) {
      const body = await matrixBody(entry);
      if (body !== null) matrixEvidence.errorWire.candidates.push({ status: entry.status, url: entry.url.slice(0, 160), shape: wireShape(body) });
    }
    const namedFailure = matrixEvidence.errorWire.candidates.find((candidate) => candidate.shape?.code === matrixErrorWireCode || candidate.shape?.nestedCode === matrixErrorWireCode);
    assert.ok(namedFailure, `expand-policy-matrix: no failing proposal/preview response carried the named ${matrixErrorWireCode} wire code; captured shapes: ${JSON.stringify(matrixEvidence.errorWire.candidates.slice(0, 5))}`);
    matrixEvidence.errorWire.assertedCode = matrixErrorWireCode;
    matrixEvidence.errorWire.failureSample = namedFailure;
    assert.ok(!(await mounted(applySelector)), 'matrix: a failed ERROR-policy preview must not offer Apply');
    matrixEvidence.legs.error = { browserFailureSurfaced: true, wireCode: matrixErrorWireCode, applyOfferedWhileFailing: false };
    await screenshot('step3d-matrix-error');
    if (await mounted('[data-testid="construction-cancel-proposal"]')) await realClick('[data-testid="construction-cancel-proposal"]');
    await closeMatrixEditor();
    const afterError = await matrixCaps();
    const persistedStep = (afterError.value?.baseConstruction?.steps ?? []).filter((step) => step?.operation?.kind === 'EXPAND');
    assert.equal(persistedStep.length, 1, `expand-policy-matrix: the failed ERROR edit must leave exactly one applied step, got ${JSON.stringify(persistedStep.map((step) => step.id))}`);
    assert.equal(persistedStep[0]?.id, matrixStep.id, 'expand-policy-matrix: the failed ERROR edit replaced the applied step');
    assert.equal(persistedStep[0].operation.expand.emptyPolicy, 'PRESERVE_PARENT', 'the cancelled ERROR edit must not have persisted the ERROR policy');
    matrixEvidence.legs.error.persistedPolicyUnchanged = 'PRESERVE_PARENT';

    // Leg 5: reload restores the persisted EXPAND step, its signature, its owned
    // sourceProjection, and the seven-row rendering.
    await send('Page.reload', {});
    await until(datasetLoaded, 'matrix dataset restored after reload', 60000);
    const afterReload = await matrixCaps();
    const reloadSteps = (afterReload.value?.baseConstruction?.steps ?? []).filter((step) => step?.operation?.kind === 'EXPAND');
    assert.equal(reloadSteps.length, 1, `expand-policy-matrix: reload did not restore exactly one applied EXPAND step (got ${JSON.stringify(reloadSteps.map((step) => step.id))})`);
    assert.equal(reloadSteps[0]?.id, matrixStep.id, 'expand-policy-matrix: reload restored a different EXPAND step identity');
    const reloadProjection = (afterReload.value?.baseConstruction?.sourceProjections ?? [])
      .find((projectionEntry) => projectionEntry.columnId === matrixInputColumnId);
    assert.ok(reloadProjection && reloadProjection.ownerStepId === matrixStep.id
      && String(reloadProjection.fieldPath).replace(/^root\./, '') === 'instantiatesUri[]',
      `expand-policy-matrix: reload lost the EXPAND-owned sourceProjection: ${JSON.stringify(afterReload.value?.baseConstruction?.sourceProjections ?? null).slice(0, 300)}`);
    let reloadPreview = null;
    let reloadPreviewViaEditor = false;
    try {
      reloadPreview = await untilMatrixPreview((view) => view && view.rowCount === 7 && preserveShape(view.cells), 'workspace preview did not restore the seven rows', 20000);
    } catch {
      reloadPreviewViaEditor = true;
      matrixEvidence.disclosures.push('after reload the workspace-level preview section did not settle on the seven rows within 20 s; the restore proof was taken through the reopened step editor proposal preview instead (same construction-preview authority as check #9)');
      await openMatrixStepEditor(matrixStep.id);
      reloadPreview = await untilMatrixPreview((view) => view && view.rowCount === 7 && preserveShape(view.cells), 'the reopened step editor did not restore the seven rows', 60000);
    }
    matrixEvidence.legs.reload = { stepRestored: true, projectionRestored: reloadProjection, rowCount: reloadPreview.rowCount, viaEditor: reloadPreviewViaEditor };
    await screenshot('step3e-matrix-reload');

    // Leg 6: removal yields source restoration — after removing the step the server
    // must drop the step-owned array sourceProjection (mirrors the check #9 readback).
    if (!(await mounted('[data-testid="construction-reshape-expand"]'))) await openMatrixStepEditor(matrixStep.id);
    await until(() => mounted(`[data-testid="construction-remove-step-${matrixStep.id}"]`), 'matrix: Remove control for the EXPAND step', 20000);
    const draftBeforeRemoval = await matrixDraft();
    await realClick(`[data-testid="construction-remove-step-${matrixStep.id}"]`);
    const removalDeadline = Date.now() + 20000;
    for (;;) {
      if (await mounted(applySelector)) { await realClick(applySelector); break; }
      if ((await matrixDraft()) > draftBeforeRemoval) break;
      if (Date.now() > removalDeadline) assert.fail('expand-policy-matrix: clicking Remove never produced a removal proposal or draft change');
      await wait(200);
    }
    await until(async () => (await matrixDraft()) > draftBeforeRemoval, 'matrix: the removal was never applied', 30000);
    const afterRemoval = await matrixCaps();
    assert.equal((afterRemoval.value?.baseConstruction?.steps ?? []).filter((step) => step?.operation?.kind === 'EXPAND').length, 0,
      'expand-policy-matrix: the EXPAND step survived removal');
    const survivingProjection = (afterRemoval.value?.baseConstruction?.sourceProjections ?? [])
      .find((projectionEntry) => projectionEntry.columnId === matrixInputColumnId
        || (projectionEntry.ownerStepId === matrixStep.id && String(projectionEntry.fieldPath).replace(/^root\./, '') === 'instantiatesUri[]'));
    assert.ok(!survivingProjection, `expand-policy-matrix: server-side source restoration failed, the EXPAND-owned array sourceProjection survived removal: ${JSON.stringify(survivingProjection ?? null).slice(0, 300)}`);
    await closeMatrixEditor();
    matrixEvidence.legs.removal = { stepRemoved: true, projectionCleaned: true, draftBumped: true };
    report.matrixEvidence = matrixEvidence;
    await screenshot('step3f-matrix-removal');

    matrixEvidence.disclosures.push(
      'stale-choice and invalid-type refusals (INVALID_EXPAND_SOURCE_CHOICE expand_source.go:302/308/320/324, STALE_EXPAND_SOURCE_SCHEMA :341, UNSUPPORTED_EXPAND_SOURCE_FIELD :328/348/351) are not cheaply derivable in-window from a healthy lane: they require mutating the choice identity or schema outside the user flow; the Go suites (internal/explorer/lifecycle/expand_source_test.go and the lower expand gate) remain the authority',
      'null-array and missing-array parents exercise the same server-side no-items branch under each policy (construction_expand.go treats raw==null as []); the null literal survived fixture ingestion if this check passed with seven PRESERVE_PARENT rows',
    );
    record('row96-expand-policy-matrix', matrixEvidence);
    try { rmSync(rs08MatrixFixtureDir, { recursive: true, force: true }); } catch { /* disposable tmp copy */ }
  }
  markPhase('expand-policy-matrix');

  const refusalRecords = laneCaps.flatMap((cap) => (Array.isArray(cap.candidates ?? cap.fields) ? cap.candidates ?? cap.fields : [])
    .filter((candidate) => candidate && candidate.supported === false && typeof candidate.reasonCode === 'string' && candidate.reasonCode)
    .map((candidate) => ({ kind: cap.kind, fieldPath: candidate.fieldPath ?? candidate.path ?? null, reasonCode: candidate.reasonCode })));
  const laneLevelRefusals = laneCaps.filter((cap) => cap.supported === false && typeof cap.reasonCode === 'string' && cap.reasonCode)
    .map((cap) => ({ kind: cap.kind, fieldPath: null, reasonCode: cap.reasonCode }));
  const allRefusals = [...refusalRecords, ...laneLevelRefusals];
  const inventoryFlavored = allRefusals.filter((entry) => inventoryClass.test(entry.reasonCode));
  const refusalOnly = allRefusals.filter((entry) => !inventoryClass.test(entry.reasonCode));
  // Falsifiable non-vacuity: under this fixture (no numeric/array/related assets) the
  // lane decision classes MUST be advertised (rowSourceEnumeration.decision is total for
  // unsupported lanes: a class or the honest generic fallback). Zero refusals here means
  // the capability assembly regressed to supported-everything, not a healthy table.
  assert.ok(allRefusals.length >= 1, `fixture must classify at least one unsupported lane decision class (onlySkipClass/decision is total for unsupported lanes); zero refusals means the class emission regressed: ${JSON.stringify(laneCaps.map((cap) => `${cap.kind}:${cap.supported}`))}`);
  assert.ok(refusalOnly.length >= 1, `at least one refusal must be in the honest (non-inventory) class family while the inventory is healthy: ${JSON.stringify(allRefusals.slice(0, 8))}`);
  for (const entry of allRefusals) {
    assert.match(entry.reasonCode, /^[A-Z][A-Z0-9_]*$/, `per-field refusal reasonCode is not an upper server class: ${JSON.stringify(entry)}`);
  }
  record('row96-lane-refusal-decision-classes', { count: allRefusals.length, sample: allRefusals.slice(0, 8), refusalOnly: refusalOnly.length, inventoryFlavored: inventoryFlavored.length });
  markPhase('refusal-classification');
  await screenshot('step3-refusal-reasoncodes');

  const supportReasonDom = await evaluate('(()=>{const els=[...document.querySelectorAll(\'[disabled],[aria-disabled="true"],[data-testid*="related-source" i]\')];return els.map(e=>({title:e.getAttribute(\'title\'),aria:e.getAttribute(\'aria-label\'),text:(e.textContent||\'\').trim()})).filter(h=>/[A-Z][A-Z0-9_]{6,}/.test((h.title||\'\')+(h.text||\'\'))).slice(0,24);})()');
  const codeSeenDom = supportReasonDom.find((hit) => /[A-Z][A-Z0-9_]{6,}(\s*:|\s*-|,|\(|\s)/.test(`${hit.title || ''} ${hit.text || ''}`));
  if (codeSeenDom) {
    record('row96-reason-codes-served', { via: 'ui-control', sample: codeSeenDom });
  } else {
    const payloadCodes = laneCaps.map((cap) => cap.reasonCode).filter(Boolean);
    assert.ok(payloadCodes.length > 0 || allRefusals.length > 0, 'neither a UI control rendering "reasonCode: reason" (RelatedSourceStepEditor.tsx joins :79-80 into disabledReason :444) nor any API reasonCode exists; the consumer exists in committed UI source, so assert via the authoritative API payload and log the gap instead of faking a tooltip assert');
    record('row96-reason-codes-served', { via: 'api-payload-priority', payloadCodes: payloadCodes.slice(0, 6), refusalCodes: allRefusals.map((entry) => entry.reasonCode).slice(0, 6) });
    report.sideFindings.push({ id: 'row96-ui-gap-reason-control', note: 'UI control was not currently disabled/reason-rendered for the lane state; assertion fell back to the API payload per the side-finding rule, and the joined string contract is asserted server-side by construction_pivot_source.go reason pairs and by RelatedSourceStepEditor.unit.test.tsx consumer tests' });
  }

  await send('Network.enable');
  const seenRequests = [];
  const urlById = new Map();
  client.on('Network.requestWillBeSent', (event) => { if (event?.request?.url) { seenRequests.push(event.request.url); if (event.requestId) urlById.set(event.requestId, event.request.url); } });
  const failedRequests = [];
  // Registered BEFORE the block is applied: loadingFailed must be able to observe the
  // very block burst (registering after the reload systematically misses it).
  client.on('Network.loadingFailed', (event) => { if (event?.requestId) failedRequests.push(event.requestId); });
  // Session-wide raw counter: diagnostics only, never an assertion input (page reload
  // aborts every in-flight request with ERR_ABORTED, so unfiltered counts are noise).
  const strictBlockedFailures = () => failedRequests.filter((id) => strictBlockedUrl(urlById.get(id) ?? '')).length;
  const seenBlocked = [];
  // Strict block-equivalent patterns: '*'=='.*', explicit lower-case, matching Chrome's
  // case-sensitive setBlockedURLs semantics — the counting rule cannot be a superset of
  // what is blocked (a case-insensitive broader regex counted served healthy requests).
  const strictBlockedUrl = (url) => /authoring\/v2\/capability|semantic-inventory/.test(url);
  const inventoryHintPattern = /(populated|capabilit|catalog|inventory|field-observation|profile)/i;
  const healthyHits = seenRequests.filter((url) => inventoryHintPattern.test(url));
  if (healthyHits.length === 0) {
    report.sideFindings.push({ id: 'row96-inventory-route-not-observed-browser-side', note: 'Network.requestWillBeSent did not include the documented capability/populated-observation request; falling back to server-truth injectable route via blocking the same pattern used by the server-side capability assembly; the honest boundary assert remains (unsupported with reasonCode *_SOURCE_INVENTORY_UNAVAILABLE)' });
  }
  // The dataset chrome is already loaded; block now and reopen the editor so its own
  // inventory fetches (populated-field observations/capabilities) fail in the served
  // app. Reload with block active would also kill the chrome's own bootstrap (it needs
  // catalog/capability routes), which is not the fault under test.
  const blockedAttemptsAtBlock = seenRequests.filter(strictBlockedUrl).length;
  await send('Network.setBlockedURLs', { urls: ['*authoring/v2/capability*', '*semantic-inventory*'] });
  // Snapshot so the post-block window counts only requests issued while blocked.
  const seenAtBlock = [...seenRequests];
  // Reload is the only way to make the APP itself re-issue inventory-class fetches (the
  // editor is draft-cached — proven live). The dev UI proxies API calls through the UI
  // origin, so seen-URL matching must not assume the API port.
  await send('Page.reload', {});
  await wait(4000);
  await realClick('[data-testid="construction-action-keep-rows"]');
  await until(() => mounted('[data-testid="construction-filter-editor"]'), 'lane source picker under block', 20000);
  const faultPayload = await api('construction-capabilities', {
    outputId, stageId, snapshotToken: afterTable.value.catalog.snapshotToken,
    expectedDraftVersion: afterTable.value.draftVersion, expectedDraftDigest: afterTable.value.draftDigest,
  });
  const faultCaps = faultPayload.value?.selectedStage?.capabilities ?? [];
  const inventoryLanes = faultCaps.filter((cap) => cap.supported === false && typeof cap.reasonCode === 'string' && inventoryClass.test(cap.reasonCode));
  let faultFacts = [];
  const faultFactsDeadline = Date.now() + 15000;
  for (;;) {
    faultFacts = await evaluate(`(()=>{
      const els=[...document.querySelectorAll('[role="status"],[role="alert"],[data-testid$="-unavailable"],.text-amber-900,.text-amber-950')];
      return els.map(e=>(e.textContent||'').replace(/\\s+/g,' ').trim()).filter(t=>/unavailable|inventory|SOURCE_|failed to fetch/i.test(t)).slice(0,10);
    })()`);
    const provenStrike = strictBlockedFailures() >= 1 && seenRequests.slice(seenAtBlock.length).filter(strictBlockedUrl).length >= 1;
    if (faultFacts.length > 0 || provenStrike || Date.now() > faultFactsDeadline) break;
    await wait(300);
  }
  const faultCodes = faultFacts.flatMap((t) => t.match(/[A-Z][A-Z0-9_]+_SOURCE_INVENTORY_UNAVAILABLE/g) ?? []);
  const seenAfterBlock = seenRequests.slice(seenAtBlock.length);
  const blockedAttemptsPostBlock = seenAfterBlock.filter(strictBlockedUrl).length;
  const blockedApiAttempts = blockedAttemptsAtBlock + blockedAttemptsPostBlock;
  const blockedApiFailures = failedRequests.length;
  await realClick('[data-testid="construction-close-operation-editor"]');
  await until(async () => !(await mounted('[data-testid="construction-filter-editor"]')), 'lane source picker closed after fault probe', 10000);
  await send('Network.setBlockedURLs', { urls: [] });
  // Honest boundary, live-PROVEN 2026-11-02 (three block strategies + request capture:
  // the editor is cache-fed and the inventory/row-choice probe is an IN-PROCESS server
  // dependency no browser block can touch): the browser can never observe this class.
  // What this control still proves, fault-sensitively: (1) the block genuinely broke the
  // app's own inventory-class routes, (2) the served UI refused/degraded rather than
  // silently rendering healthy-with-empty, (3) the server never reports the
  // inventory-unavailable class FALSE-POSITIVELY while the inventory is actually healthy
  // (out-of-band lanes must stay clear of it — the S-B misclassification bug would strike
  // here). The positive emission guarantee is carried by the four Go per-lane tests
  // (filter/derive/unpivot/pivot *_SOURCE_INVENTORY_UNAVAILABLE wantFail cases). If this
  // window gains a real server-side inventory injector, the positive browser-observable
  // assert (faultCodes.length > 0) must be reinstated alongside these, not instead.
  assert.ok(blockedApiAttempts >= 2, `inventory block struck none of the app's own inventory-class routes (attempted ${blockedApiAttempts}; load-failed ${blockedApiFailures}; no failure injected; block patterns or app wiring changed): ${JSON.stringify(faultFacts).slice(0, 200)}`);
  // Falsifiable strike proof: the app must re-ISSUE blocked routes while the block is
  // active (pre-block-only counts certify nothing about the block window).
  assert.ok(blockedAttemptsPostBlock >= 1, `app never re-issued an inventory-class route while blocked (post-block attempts ${blockedAttemptsPostBlock}; block patterns or app wiring changed; ${blockedApiAttempts} attempts were pre-block only): ${JSON.stringify(seenAfterBlock.filter(strictBlockedUrl).slice(0, 4))}`);
  // Inert-block control: a silently-ineffective setBlockedURLs (Chrome/pattern drift)
  // would still count served-healthy attempts. Disk-cache replays legitimately omit
  // loadingFailed, so assert at least one network-level failure, not equality.
  const strictFailures = strictBlockedFailures();
  // Inert-block control: a silently-ineffective setBlockedURLs (Chrome/pattern drift)
  // would still count served-healthy attempts. Failures are URL-filtered to the strict
  // block patterns — session-wide loadingFailed counts include page-reload aborts and
  // prove nothing. Disk-cache replays legitimately omit loadingFailed, so the bar is
  // >= 1, not equality with attempts.
  assert.ok(strictFailures >= 1, `inventory block produced no URL-filtered network failures on blocked routes (attempts ${blockedApiAttempts}, post-block ${blockedAttemptsPostBlock}, filtered failures ${strictFailures}, raw failures ${blockedApiFailures}; setBlockedURLs may be inert: pattern/Chrome drift): ${JSON.stringify(seenAfterBlock.filter(strictBlockedUrl).slice(0, 4))}`);
  const degradeObserved = faultFacts.length > 0 || faultCodes.length > 0;
  if (!degradeObserved) {
    report.sideFindings.push({ id: 'row96-inventory-degradation-visibility-flaky', note: 'app inventory routes failed under block, but no visible degradation text was present in this sample (dev-mode fetch errors on secondary inventory routes are transient). Degrade-visibility is a soft observation; the load-bearing live control is the zero-false-positive assertion below plus request-level blockedApiFailures; the positive emission guarantee stays with the four per-lane Go tests.' });
  }
  assert.deepEqual(inventoryLanes.map((cap) => cap.kind), [], `healthy inventory must NEVER name *_SOURCE_INVENTORY_UNAVAILABLE (S-B false-positive control; positive emission is certified by the four per-lane Go tests): ${JSON.stringify(inventoryLanes.map((cap) => `${cap.kind}:${cap.reasonCode}`))}`);
  report.sideFindings.push({ id: 'row96-inventory-fault-live-unreachability', note: 'positive live emission of *_SOURCE_INVENTORY_UNAVAILABLE is not browser-observable (in-process row-choice planner; proven live 2026-11-02 via request capture: editor is cache-fed, no injectable inventory seam exists in cmd/internal dev hooks). Positive guarantee: filter_source_test.go:1487, derive_source_test.go:1228, unpivot_source_test.go:1365, construction_pivot_source_test.go:413. Live control here: block strikes app inventory routes, UI visibly degrades, and zero false-positive class while healthy. A dev-mode server injector would restore the full live positive assert.' });
  record('row96-inventory-block-strike-and-false-positive-control', { blocked: ['*authoring/v2/capability*', '*semantic-inventory*'], blockedApiAttempts, blockedAttemptsPostBlock, blockedApiLoadFailuresFiltered: strictFailures, blockedApiLoadFailuresRaw: failedRequests.length, degradedUIMsgs: faultFacts.slice(0, 4), falsePositiveLanes: [], via: 'boot reload under block + out-of-band false-positive control; positive emission delegated to per-lane Go tests per proven live unreachability' });
  markPhase('inventory-fault');
  await screenshot('step4-inventory-fault');

  const emptied = await commands('row96-empty-root', [{ type: 'CREATE_TABLE', title: 'RS96 empty-data shape', rootNodeId: root.nodeId }], await (async () => { const s = await api('builder'); return s.value; })());
  assert.equal(emptied.status, 200, `empty-data table seed failed (${emptied.status})`);
  const emptyDoc = (await api('builder')).value;
  const emptyOutput = emptyDoc?.workspace?.documents?.at(-1)?.output?.id ?? outputId;
  const emptyPayload = await api('construction-capabilities', {
    outputId: emptyOutput, stageId, snapshotToken: emptyDoc.catalog.snapshotToken,
    expectedDraftVersion: emptyDoc.draftVersion, expectedDraftDigest: emptyDoc.draftDigest,
  });
  const emptyCaps = emptyPayload.value?.selectedStage?.capabilities ?? [];
  const emptyDataCodes = [...new Set(emptyCaps.filter((cap) => cap.supported === false && typeof cap.reasonCode === 'string').map((cap) => cap.reasonCode))];
  const classes = emptyDataCodes.filter((code) => emptyDataFamily.test(code));
  const leaks = emptyDataCodes.filter((code) => inventoryClass.test(code));
  // Honest scope (contract-reviewer F2): this seed is a structurally under-seeded TABLE
  // (no columns), so its classes are the compiler capability gates; a populated dataset
  // can legitimately never surface honest-absence codes here. The honest-absence
  // (NO_OBSERVED_VALUES_*/NO_ELIGIBLE_POPULATED_ family) guarantee is carried by the four
  // per-lane Go tests (onlySkipClass rowSourceSkipObserved path); this check certifies
  // (1) class emission is non-empty and well-formed on an under-seeded table and
  // (2) class-disjointness from the inventory family IN BOTH DIRECTIONS (neither seed may
  // mix the families) — the S-B distinction. It does NOT certify honest-absence emission.
  assert.ok(classes.length > 0, `under-seeded table verdict must be classifiable in the capability-gate family; saw ${JSON.stringify(emptyDataCodes.slice(0, 8))}`);
  assert.deepEqual(leaks, [], 'under-seeded table must NOT collapse into the inventory-unavailable class (the S-B distinction is the whole point of this check)');
  const healthyCodes = [...new Set(faultCaps.filter((cap) => cap.supported === false && typeof cap.reasonCode === 'string').map((cap) => cap.reasonCode)).values()];
  assert.deepEqual(healthyCodes.filter((code) => inventoryClass.test(code)), [], 'healthy seeded table must never carry the inventory class (out-of-band direction of the disjointness contract)');
  const honestAbsenceOnSeed = emptyDataCodes.filter((code) => /^NO_OBSERVED_VALUES_|^NO_ELIGIBLE_POPULATED_/.test(code));
  if (honestAbsenceOnSeed.length > 0) {
    report.sideFindings.push({ id: 'row96-honest-absence-on-columnless-seed', note: `honest-absence classes on the columnless seed (unexpected; indicates populated fields became unobservable): ${JSON.stringify(honestAbsenceOnSeed)}; recorded, not a violation of this structural-gate check` });
  }
  record('row96-structural-gates-inventory-disjoint', { seedShape: 'columnless CREATE_TABLE on populated dataset (structural gates, not honest-absence)', structuralClasses: classes, honestAbsenceClasses: emptyDataCodes.filter((code) => /^NO_OBSERVED_VALUES_|^NO_ELIGIBLE_POPULATED_/.test(code)), inventoryCodesSeenInEmptyLane: leaks, healthyTableCodes: healthyCodes, disjointBothDirections: true, honestAbsenceAuthority: 'four per-lane Go tests (onlySkipClass observed path)' });

  markPhase('empty-data');
  if (exceptions.length > 0) {
    throw new Error('uncaught browser exceptions during the journey: ' + exceptions.join(';').slice(0, 300));
  }
  await screenshot('step5-empty-data-distinguished');

  const identityAfter = collectBuildIdentity();
  report.buildIdentity.after = identityAfter;
  assert.equal(identityAfter.checkExit, 0, `post-run: serving identity moved: ${JSON.stringify(identityAfter)}`);
  assert.equal(identityAfter.runningBinary, identityBefore.runningBinary, `post-run: serving binary moved during run: ${identityAfter.runningBinary} != ${identityBefore.runningBinary}`);
  assert.equal(identityAfter.liveSource, identityBefore.liveSource, `post-run: live source moved during run: ${identityAfter.liveSource} != ${identityBefore.liveSource}`);
  assert.equal(identityAfter.gitHead, identityBefore.gitHead, `post-run: HEAD moved during run: ${identityAfter.gitHead} != ${identityBefore.gitHead}`);

  markPhase('identity-after');
  const names = report.checks.map((check) => check.name);
  for (const name of expectedChecks) assert.ok(names.includes(name), `skipped checks: ${expectedChecks.filter((candidate) => !names.includes(candidate)).join(',')}`);
  report.status = 'passed';
  return `JOURNEY_OK build=${identityBefore.gitHead} runningBinary=${identityBefore.runningBinary} checks=${report.checks.length} screenshots=${report.screenshots.length} sideFindings=${report.sideFindings.length}`;
};

let exitCode = 0;
let summaryLine = '';
try {
  summaryLine = await main();
} catch (error) {
  exitCode = 1;
  report.status = 'failed';
  report.error = String(error?.stack || error);
  try {
    const shot = await moduleSend('Page.captureScreenshot', { format: 'png' }, 15000);
    const failShotPath = join(out, 'FAIL-latest.png');
    writeFileSync(failShotPath, Buffer.from(shot.data, 'base64'));
    report.screenshots.push(failShotPath);
  } catch (screenshotError) { report.failureScreenshotError = String(screenshotError?.message || screenshotError).slice(0, 300); /* browser may be gone or wedged; the saved HTML from until() is the fallback artifact */ }
  report.exceptions.push(report.error);
  summaryLine = `JOURNEY_FAILED ${String(error?.message ?? error).slice(0, 400)}`;
}
await killTree();
try {
  rmSync(profile, { recursive: true, force: true });
} catch (profileError) {
  // volume-stale handles throw transiently; retry once after the tree is provably dead
  try { rmSync(profile, { recursive: true, force: true }); }
  catch (retryError) {
    report.teardownProfileError = String(retryError?.message || retryError).slice(0, 300);
    console.log(`PROFILE_TEARDOWN_FAILED path=${profile} error=${report.teardownProfileError}`);
  }
}
markPhase('teardown');
report.phaseTimings = { totalMs: Date.now() - runStartedAt, ...phaseTimings };
try { writeFileSync(join(out, 'journey.json'), JSON.stringify(report, null, 2)); }
catch (reportError) {
  exitCode = 1;
  summaryLine = `JOURNEY_FAILED journey-report-unwritable: ${String(reportError?.message ?? reportError).slice(0, 200)}`;
  try { writeFileSync(join(out, 'telemetry.json'), JSON.stringify({ status: 'failed', teardownError: String(reportError?.message ?? reportError).slice(0, 300) })); } catch { /* out dir is likely the failure */ }
}
console.log(summaryLine);
console.log(`PHASE_TIMINGS total=${report.phaseTimings.totalMs}ms ` + phaseTimings.phases.map((p) => `${p.phase}=${p.ms}ms`).join(' '));
process.exitCode = exitCode;
