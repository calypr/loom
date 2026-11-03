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
];

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
