#!/usr/bin/env node
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { CDPClient } from '../../browser/cdp.mjs';
import { browserURL } from './builder-url.mjs';
import { AUTHORING_SEMANTICS_VERSION, collectBuildIdentity as sharedCollectBuildIdentity, createDevSession, createFreshVerificationFixture } from '../../loom-dev.mjs';
import { PIVOT_KEYWORD_NAMES } from '../helpers/pivot-keyword-fixture-oracle.mjs';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', '..');

const options = {};
for (let i = 2; i < process.argv.length; i += 1) {
  if (process.argv[i] === '--dry-run') { options.dryRun = true; continue; }
  const key = { '--out': 'out', '--chrome': 'chrome', '--run-label': 'runLabel' }[process.argv[i]];
  assert.ok(key && process.argv[i + 1], `Unknown or missing-valued option: ${process.argv[i]}`);
  options[key] = process.argv[++i];
}

const expectedChecks = [
  'pk-seed-table',
  'pk-editor-mounts',
  'pk-fields-selected',
  'pk-categories-discovered',
  'pk-keyword-names-applied',
  'pk-cancel-byte-stability',
  'pk-comparison-proposal-available',
  'pk-apply-success',
  'pk-grid-values',
  'pk-document-persistence',
  'pk-reload-persistence',
  'pk-duplicate-negative-blocked',
  'pk-rename-recovers',
  'pk-remove-restores',
];

const legit4xx = /^(TABLE_SHAPE_|DUPLICATE_OUTPUT_NAME|INVALID_AUTHORING_COMMAND|DRAFT_CONFLICT|TABLE_PIVOT_|INVALID_|UNRESOLVED_|NAME_)/;

const staticValidate = () => {
  assert.equal(expectedChecks.length, new Set(expectedChecks).size, 'duplicate check names in expectedChecks');
  assert.ok(legit4xx.test('DUPLICATE_OUTPUT_NAME'), 'duplicate-name family must be admissible');
  assert.ok(!legit4xx.test('INTERNAL_SERVER_ERROR'), '5xx internals must never be admissible');
  for (const helper of ['../../loom-dev.mjs', '../../browser/cdp.mjs', './builder-url.mjs', '../helpers/pivot-keyword-fixture-oracle.mjs']) {
    assert.ok(existsSync(resolve(REPO_ROOT, 'scripts/verify-ui/workflows', helper)), `missing dependency ${helper}`);
  }
  const workflow = readFileSync(join(import.meta.dirname ?? dirname(fileURLToPath(import.meta.url)), 'verify-explorer-pivot-keyword-names.mjs'), 'utf8');
  for (const name of expectedChecks) assert.ok(workflow.includes(`'${name}'`), `check ${name} never recorded in source`);
  assert.match(workflow, /construction-apply-proposal/, 'apply must drive the real apply-proposal control');
  assert.match(workflow, /Pivot output name/, 'keyword names must be typed through the real name inputs');
  console.log(`DRY_RUN_OK artifact=pivot-keyword-names checks=${expectedChecks.length}`);
};

if (options.dryRun) {
  staticValidate();
  process.exit(0);
}

assert.ok(options.out, '--out <FRESH_DIR> is required for live runs');
const out = isAbsolute(options.out) ? options.out : resolve(process.cwd(), options.out);
assert.ok(!existsSync(out) || readdirSync(out).length === 0, 'Refusing to overwrite browser evidence directory');
mkdirSync(out, { recursive: true });
const runStem = options.runLabel ?? 'pkw';

const scriptBytes = readFileSync(import.meta.url ? fileURLToPath(import.meta.url) : 'verify-explorer-pivot-keyword-names.mjs');
const oracleBytes = readFileSync(join(REPO_ROOT, 'scripts/verify-ui/helpers/pivot-keyword-fixture-oracle.mjs'));
const report = { status: 'running', scriptIdentity: { workflowSha256: createHash('sha256').update(scriptBytes).digest('hex'), oracleSha256: createHash('sha256').update(oracleBytes).digest('hex') }, schemaVersion: 1, mode: 'raw-cdp-pivot-keyword-names-journey', builtAt: new Date().toISOString(), runStem, environment: null, buildIdentity: null, fixture: null, checks: [], steps: [], screenshots: [], sideFindings: [], exceptions: [], error: null };
const record = (name, evidence) => {
  assert.ok(expectedChecks.includes(name), `unknown check ${name}`);
  const existing = report.checks.find((entry) => entry.name === name);
  assert.ok(!existing, `duplicate record for ${name}`);
  report.checks.push({ name, status: 'passed', evidence });
};

const session = createDevSession(process.env, REPO_ROOT);
const collectBuildIdentity = () => {
  const identity = sharedCollectBuildIdentity(session, { repoRoot: REPO_ROOT });
  const pin = process.env.LOOM_DEV_HEAD_OVERRIDE;
  if (pin) {
    if (!/^[a-f0-9]{40}$/.test(pin)) throw new Error('LOOM_DEV_HEAD_OVERRIDE must be a 40-char commit hash');
    if (!identity.gitHead) identity.gitHead = pin;
  }
  return identity;
};

const chromePath = options.chrome ?? process.env.LOOM_E2E_CHROME
  ?? ['/usr/bin/chromium', '/opt/google/chrome/chrome'].find((candidate) => existsSync(candidate));
assert.ok(chromePath && existsSync(chromePath), 'No Chrome binary: pass --chrome, set LOOM_E2E_CHROME, or install chromium');
const profile = mkdtempSync(join(tmpdir(), 'pkw-prof-'));
console.log(`RUN_PROFILE path=${profile}`);
const chrome = spawn(chromePath, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--remote-debugging-port=0',
  `--user-data-dir=${profile}`, '--window-size=1600,1200', '--no-first-run', '--no-default-browser-check',
  '--disable-background-networking', '--disable-component-update', '--disable-sync', '--proxy-server=direct://', 'about:blank'],
  { stdio: ['ignore', 'ignore', 'pipe'], detached: true });
let chromeErr = '';
chrome.stderr.on('data', (chunk) => { chromeErr = (chromeErr + chunk).slice(-4000); });
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
const killTree = async () => {
  clearTrackedTimers();
  try { client.close(); } catch { }
  try { chrome.stderr?.destroy(); } catch { }
  const groupMembers = () => {
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
  const groupSignal = (signal) => {
    let groupOk = false;
    if (Number.isInteger(chrome.pid)) { try { process.kill(-chrome.pid, signal); groupOk = true; } catch { groupOk = false; } }
    if (!groupOk) { try { chrome.kill(signal); } catch { } }
    for (const pid of groupMembers()) { try { process.kill(pid, signal); } catch { } }
    return true;
  };
  if (chrome.exitCode === null && chrome.signalCode === null) {
    const exited = new Promise((done) => chrome.once('exit', done));
    if (groupSignal('SIGTERM')) {
      await withDeadline(exited, 5000, 'chrome SIGTERM exit').catch(() => { groupSignal('SIGKILL'); });
    }
  }
  for (let attempt = 0; attempt < 10 && groupMembers().length > 0; attempt += 1) {
    if (attempt === 5) groupSignal('SIGKILL');
    await wait(100);
  }
  if (groupMembers().length > 0) report.teardownGroupResidue = groupMembers().join(',');
};

const main = async () => {
  const identityBefore = collectBuildIdentity();
  report.buildIdentity = { before: identityBefore };
  assert.ok(identityBefore.runningBinary, 'build identity unavailable: no serving binary on the owned stack');
  assert.ok(identityBefore.gitHead, 'build identity unavailable: git HEAD unresolvable');
  assert.equal(identityBefore.checkExit, 0, `serving build identity must be current: ${JSON.stringify(identityBefore)}`);
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
  const assertNoInternal = (response, label) => {
    assert.ok(response.status < 500, `${label}: server 5xx class is NEVER legitimate: ${response.status} ${JSON.stringify(response.value).slice(0, 300)}`);
  };

  const portFile = join(profile, 'DevToolsActivePort');
  for (let i = 0; i < 150 && !existsSync(portFile); i += 1) await wait(100);
  assert.ok(existsSync(portFile), `chrome devtools port not advertised; spawnError=${chromeSpawnError ?? 'none'}; stderr=${chromeErr.slice(-400)}`);
  const [port] = readFileSync(portFile, 'utf8').trim().split('\n');
  const targetResponse = await withDeadline(fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' }), 20000, 'devtools /json/new');
  const target = await withDeadline(targetResponse.json(), 10000, 'devtools /json/new body');
  client = await withDeadline(CDPClient.connect(target.webSocketDebuggerUrl), 30000, 'CDP websocket open');
  const send = moduleSend;
  const exceptions = [];
  report.exceptions = exceptions;
  client.on('Runtime.exceptionThrown', (event) => exceptions.push(event.exceptionDetails?.exception?.description || event.exceptionDetails?.text || 'exception'));
  const browser5xx = [];
  const browser4xx = [];
  const cmdResponses = [];
  client.on('Network.responseReceived', (event) => {
    const url = String(event.response?.url ?? '');
    if (!url.includes('/authoring/v2/')) return;
    const status = Number(event.response?.status ?? 0);
    if (status >= 500) browser5xx.push({ url: url.split('/v2/')[1], status, requestId: event.requestId });
    else if (status >= 400) browser4xx.push({ url: url.split('/v2/')[1], status, requestId: event.requestId });
    if (url.includes('/authoring/v2/commands')) cmdResponses.push({ status, requestId: event.requestId, timestamp: event.timestamp });
  });
  await send('Network.enable').catch(() => {});
  markPhase('cdp-connect');

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
  const until = async (probe, message, ms = 30000) => {
    const deadline = Date.now() + ms;
    for (;;) {
      const value = await probe();
      if (value) return value;
      if (Date.now() > deadline) {
        try { writeFileSync(join(out, `FAIL-${String(message).replace(/\W+/g, '_')}.html`), await evaluate('document.documentElement.outerHTML')); } catch { }
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
  const setInputValue = (selector, value) => evaluate(`(()=>{const el=document.querySelector(${JSON.stringify(selector)});if(!el)return 'missing';const setter=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;setter.call(el,${JSON.stringify(value)});el.dispatchEvent(new Event('input',{bubbles:true}));return el.value;})()`);
  const navigateToLane = async (label) => {
    await send('Page.navigate', { url: laneURL() });
    await until(async () => (await mounted('[data-testid="construction-workspace"]'))
      || Boolean(await evaluate(`(()=>{const a=document.querySelector('[role="alert"]');return a?a.textContent:null;})()`)), `${label} settled`, 18000);
  };
  const readGrid = () => evaluate(`(()=>{const sec=document.querySelector('[data-testid="construction-preview"]');const t=sec?.querySelector('[data-testid="preview-table-content"]')||sec;if(!t)return null;const headerRow=t.querySelector('[role="row"]');const headers=headerRow?[...headerRow.querySelectorAll('[role="columnheader"],[role="cell"]')].map(c=>c.textContent.trim()):[];const trs=[...t.querySelectorAll('[role="row"]')].filter(r=>!r.className.includes('construction-preview-header-row'));const rows=trs.map(tr=>[...tr.querySelectorAll('[role="cell"]')].map(td=>td.textContent.trim())).filter(cells=>cells.length>0);return{headers,rowCount:rows.length,cells:rows,previewStatus:sec?.getAttribute('data-preview-status')??null,previewReceipt:sec?.getAttribute('data-preview-receipt-id')??null};})()`);

  const builder0 = await api('builder');
  assert.equal(builder0.status, 200, `builder fetch failed (${builder0.status})`);
  const nodes = (builder0.value.catalog?.nodes ?? []).filter((n) => n.rowRootEligible);
  const obsRoot = nodes.find((n) => n.resourceType === 'Observation') ?? nodes[0];
  assert.ok(obsRoot?.nodeId, 'no eligible row root for the seeded table');
  const commands = async (commandId, list, state) => api('commands', {
    commandId: `${runStem}-${commandId}`, semanticsVersion: AUTHORING_SEMANTICS_VERSION,
    snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion,
    expectedDraftDigest: state.draftDigest, commands: list,
  });
  const created = await commands('pkw-table', [{ type: 'CREATE_TABLE', title: 'PKW keyword pivot', rootNodeId: obsRoot.nodeId }], builder0.value);
  assertNoInternal(created, 'CREATE_TABLE');
  assert.equal(created.status, 200, `CREATE_TABLE failed (${created.status}): ${JSON.stringify(created.value).slice(0, 300)}`);
  let state = (await api('builder')).value;
  const outputId = (state.workspace.documents ?? [])[0]?.output?.id;
  assert.ok(outputId, 'seeded table has no output id');
  const byPath = (path) => (state.catalog?.candidates ?? []).find((c) => c.nodeId === obsRoot.nodeId && String(c.fieldPath || '').replace(/^root\./, '') === path);
  const adds = [];
  for (const path of ['status', 'subject.reference', 'id', 'valueQuantity.value']) {
    const cand = byPath(path);
    assert.ok(cand?.candidateId, `no catalog candidate for ${path}`);
    adds.push({ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: cand.candidateId, projectionMode: cand.defaultProjectionMode, initialPresentation: 'TABLE', title: path.replace('.', '_') });
  }
  const added = await commands('pkw-cols', adds, state);
  assertNoInternal(added, 'ADD_COLUMN seed');
  assert.equal(added.status, 200, `ADD_COLUMN failed (${added.status}): ${JSON.stringify(added.value).slice(0, 400)}`);
  state = (await api('builder')).value;
  record('pk-seed-table', { outputId, rootNode: obsRoot.resourceType, columns: adds.map((a) => a.title) });
  markPhase('seed');

  const datasetLoaded = async () => {
    if (await mounted('[data-testid="construction-workspace"]')) return true;
    const alert = await evaluate(`(()=>{const a=document.querySelector('[role="alert"]');return a?a.textContent:null;})()`);
    if (alert && /couldn.t load this dataset/i.test(alert)) { await send('Page.reload', {}); await wait(1200); }
    return false;
  };
  await navigateToLane('initial nav');
  await until(datasetLoaded, 'dataset loaded (construction-workspace mounted)', 60000);
  await realClick(`[data-testid="construction-table-${outputId}"]`);
  await wait(500);
  const seededGrid = await until(async () => {
    const g = await readGrid();
    return g && g.headers.length >= 4 && g.rowCount > 0 ? g : null;
  }, 'seeded base grid rendered for the remove baseline', 30000);
  await realClick('[data-testid="construction-rows-settings-trigger"]');
  await until(() => mounted('[data-testid="construction-row-intent-reshape"]'), 'reshape intent present', 20000);
  await realClick('[data-testid="construction-row-intent-reshape"]');
  await realClick('[data-testid="construction-action-table-pivot-rows"]');
  await until(() => mounted('[data-testid="construction-reshape-pivot"]'), 'pivot editor mounted', 20000);
  record('pk-editor-mounts', { via: 'construction-action-table-pivot-rows', editor: 'construction-reshape-pivot' });
  await screenshot('step1-editor-mounted');
  markPhase('editor');

  const openPivotEditor = async () => {
    if (await mounted('[data-testid="construction-reshape-pivot"]')) return;
    await realClick('[data-testid="construction-rows-settings-trigger"]');
    await until(() => mounted('[data-testid="construction-row-intent-reshape"]'), 'reshape intent re-present', 20000);
    await realClick('[data-testid="construction-row-intent-reshape"]');
    if (!(await mounted('[data-testid="construction-action-table-pivot-rows"]'))) {
      const editButtons = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-row-operation-history"]');if(!s)return[];return [...s.querySelectorAll('[data-testid^="construction-row-edit-"]')].map(b=>b.getAttribute('data-testid'));})()`);
      assert.ok(editButtons.length > 0, 'applied pivot offers neither the variant trigger nor a history Edit control; reopen would be vacuous');
      await realClick(`[data-testid="${editButtons[0]}"]`);
      await until(() => mounted('[data-testid="construction-reshape-pivot"]'), 'pivot editor remounted via history Edit', 20000);
      return;
    }
    await realClick('[data-testid="construction-action-table-pivot-rows"]');
    await until(() => mounted('[data-testid="construction-reshape-pivot"]'), 'pivot editor remounted', 20000);
  };
  const configureFieldsAndCategories = async () => {
    await until(async () => (await evaluate(`document.querySelectorAll('input[aria-label^="Pivot group "]').length`)) >= 3, 'pivot group checkboxes rendered', 20000);
    const groupToggled = await evaluate(`(()=>{const els=[...document.querySelectorAll('input[aria-label^="Pivot group "]')];const t=els.find(e=>/status/i.test(e.getAttribute('aria-label'))&&!e.disabled);if(!t)return 'none';if(!t.checked)t.click();return t.getAttribute('aria-label');})()`);
    assert.ok(groupToggled !== 'none', 'no enabled status group-key checkbox; discovery would never auto-fire');
    await wait(200);
    const pickOption = (aria, wantRe, rejectRe) => evaluate(`(()=>{const s=document.querySelector('select[aria-label=${JSON.stringify(aria)}]');if(!s)return 'missing';const o=[...s.options].find(o=>new RegExp(${JSON.stringify(wantRe)},'i').test(o.textContent.trim())&&!(o.disabled||new RegExp(${JSON.stringify(rejectRe)},'i').test(o.textContent.trim())&&/^(effective|id)/i.test(o.textContent.trim())));if(!o)return 'no-option:'+[...s.options].map(x=>x.textContent.trim()).join('|');const setter=Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype,'value').set;setter.call(s,o.value);s.dispatchEvent(new Event('change',{bubbles:true}));return String(o.textContent.trim());})()`);
    const catLabel = await until(async () => { const r = await pickOption('Pivot category field', 'reference', 'zznever'); return typeof r === 'string' && !r.startsWith('no-option') && r !== 'missing' ? r : null; }, 'category field option selected (subject_reference)', 20000);
    await wait(250);
    const valLabel = await until(async () => { const r = await pickOption('Pivot values field', 'valueQuantity', 'id'); return typeof r === 'string' && !r.startsWith('no-option') && r !== 'missing' ? r : null; }, 'values field option selected (valueQuantity_value)', 20000);
    await until(() => mounted('[data-testid="construction-reshape-pivot-categories"]'), 'category discovery completed (details mounted)', 30000);
    await realClick('[data-testid="construction-reshape-pivot-categories"] summary');
    await until(async () => (await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-reshape-pivot-categories"]');return s?[...s.querySelectorAll('input[aria-label^="Include category"],input[aria-label^="Keep saved category"]')].length:0;})()`)) >= 2, 'Include-category rows rendered', 20000);
    await setInputValue('input[aria-label="Search category values"]', '');
    const selectedCount = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-reshape-pivot-categories"]');const boxes=[...(s?.querySelectorAll('input[type="checkbox"][aria-label^="Include category"],input[type="checkbox"][aria-label^="Keep saved category"]')??[])];const unchecked=boxes.filter(b=>!b.checked);for(const b of unchecked)b.click();return boxes.length;})()`);
    await wait(250);
    const checkedCount = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-reshape-pivot-categories"]');return [...(s?.querySelectorAll('input[type="checkbox"][aria-label^="Include category"],input[type="checkbox"][aria-label^="Keep saved category"]:checked')??[])].length;})()`);
    assert.ok(selectedCount >= 2, `discovery yielded ${selectedCount} category rows; the duplicate-ID class needs a second category`);
    assert.ok(checkedCount >= 2, `only ${checkedCount} categories checked after select-all`);
    if (!(await evaluate(`document.querySelector('select[aria-label="Pivot duplicate policy"]')?.value === 'SUM'`))) {
      await realClick('[data-testid="construction-reshape-pivot-advanced"] summary');
      const setPolicy = await evaluate(`(()=>{const s=document.querySelector('select[aria-label="Pivot duplicate policy"]');if(!s)return 'missing';const setter=Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype,'value').set;setter.call(s,'SUM');s.dispatchEvent(new Event('change',{bubbles:true}));return s.value;})()`);
      assert.equal(setPolicy, 'SUM', 'duplicate policy did not take SUM');
      await wait(250);
    }
    return { categoryLabel: String(catLabel), valueLabel: String(valLabel), groupKey: String(groupToggled), discovered: selectedCount, checked: checkedCount };
  };

  const config = await configureFieldsAndCategories();
  record('pk-fields-selected', { category: config.categoryLabel, values: config.valueLabel, groupKey: config.groupKey });
  assert.ok(config.checked <= 6, `more category rows than the owned dataset can supply (${config.checked}); the keyword-name assignment below would be misaligned`);
  record('pk-categories-discovered', { selectedCategories: config.checked, discovered: config.discovered });

  const keywordNames = [PIVOT_KEYWORD_NAMES[0], PIVOT_KEYWORD_NAMES[1]];
  const setNameInputs = async (names) => {
    const inputs = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-reshape-pivot"]');return s?[...s.querySelectorAll('input[aria-label^="Pivot output name "]')].filter(e=>!e.disabled).length:0;})()`);
    assert.ok(inputs >= names.length, `expected >=${names.length} enabled category name inputs, saw ${inputs}: the name assignment below would be vacuous`);
    const applied = [];
    for (let i = 0; i < names.length; i += 1) {
      const got = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-reshape-pivot"]');const pick=(pre,i)=>[...s.querySelectorAll('input[aria-label^="'+pre+' "]')].filter(e=>!e.disabled)[i];const out=[];for(const pre of ['Pivot output name','Pivot output label']){const el=pick(pre,${i});if(!el)return {fail:pre+' missing'};const setter=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;setter.call(el,${JSON.stringify(names[i])});el.dispatchEvent(new Event('input',{bubbles:true}));out.push({aria:el.getAttribute('aria-label'),value:el.value});}return out;})()`);
      assert.ok(Array.isArray(got) && got.length === 2 && got.every((e) => e && e.value === names[i]), `keyword name/label inputs ${i} did not hold the typed value: ${JSON.stringify(got)}`);
      applied.push(got);
    }
    await wait(250);
    const held = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-reshape-pivot"]');const out=[];for(const pre of ['Pivot output name','Pivot output label']){const els=[...s.querySelectorAll('input[aria-label^="'+pre+' "]')].filter(e=>!e.disabled).slice(0,${names.length});out.push(els.map(e=>e.value));}return out;})()`);
    assert.ok(Array.isArray(held) && held.length === 2
      && held[0] && held[0].length === names.length
      && held[0].every((v, i) => v === names[i])
      && held[1] && held[1].every((v, i) => v === names[i]),
    `typed values did not survive React re-render: expected ${JSON.stringify(names)} got ${JSON.stringify(held)}`);
    return applied;
  };

  const beforeTyping = await api('builder');
  const baseline = { draftVersion: beforeTyping.value.draftVersion, draftDigest: beforeTyping.value.draftDigest, tableShape: (beforeTyping.value.workspace.documents ?? [])[0]?.output?.tableShape ?? null };

  const appliedNames = await setNameInputs(keywordNames);
  record('pk-keyword-names-applied', { names: keywordNames, inputs: appliedNames });
  markPhase('names');

  await realClick('[data-testid="construction-row-intent-records"]');
  await until(async () => !(await mounted('[data-testid="construction-reshape-pivot"]')), 'pivot editor closed', 10000);
  const afterCancel = await api('builder');
  assertNoInternal(afterCancel, 'builder-after-cancel');
  assert.equal(afterCancel.value.draftVersion, baseline.draftVersion, 'Cancel must not bump draftVersion');
  assert.equal(afterCancel.value.draftDigest, baseline.draftDigest, 'Cancel must not change the draft digest (byte stability)');
  assert.deepEqual((afterCancel.value.workspace.documents ?? [])[0]?.output?.tableShape ?? null, baseline.tableShape, 'Cancel must not persist a tableShape');
  record('pk-cancel-byte-stability', { draftVersion: baseline.draftVersion, draftDigest: baseline.draftDigest.slice(0, 24) });

  const state3 = (await api('builder')).value;
  const caps = await api('table-shape-capabilities', {
    snapshotToken: state3.catalog.snapshotToken, expectedDraftVersion: state3.draftVersion, expectedDraftDigest: state3.draftDigest, outputId,
  });
  assertNoInternal(caps, 'table-shape-capabilities');
  assert.equal(caps.status, 200, `capabilities failed (${caps.status})`);
  const choose = (list, re) => (list ?? []).find((c) => re.test(JSON.stringify(c)));
  const choiceIdOf = (c) => (typeof c === 'string' ? c : (c?.choiceId ?? c?.id));
  const categoryChoice = choose(caps.value.categoryColumns, /reference/i) ?? caps.value.categoryColumns?.[0];
  const valueChoice = choose(caps.value.valueColumns, /valueQuantity/i) ?? caps.value.valueColumns?.[0];
  const groupChoices = [choose(caps.value.groupColumns, /status/) ?? (caps.value.groupColumns ?? [])[0]];
  const dupChoice = (caps.value.duplicatePolicies ?? []).find((d) => /sum/i.test(d.label ?? '')) ?? (caps.value.duplicatePolicies ?? [])[0];
  assert.ok(categoryChoice && valueChoice && groupChoices[0] && dupChoice, `capabilities lack category/value/group/dup choices: ${JSON.stringify(Object.keys(caps.value))}`);
  const st = () => ({ snapshotToken: state3.catalog.snapshotToken, expectedDraftVersion: state3.draftVersion, expectedDraftDigest: state3.draftDigest });
  const discovery = await api('table-shape-category-discoveries', {
    ...st(), catalogId: caps.value.catalogId, outputId,
    categoryColumnChoiceId: choiceIdOf(categoryChoice), valueColumnChoiceId: choiceIdOf(valueChoice),
  });
  assertNoInternal(discovery, 'category-discoveries');
  assert.equal(discovery.status, 200, `discovery failed (${discovery.status}): ${JSON.stringify(discovery.value).slice(0, 300)}`);
  const discoveredCategories = (discovery.value.categories ?? []).slice(0, 8);
  assert.ok(discoveredCategories.length >= 2, `comparison probe needs >=2 categories, saw ${(discovery.value.categories ?? []).length}`);
  const probeName = (i) => (i < keywordNames.length ? keywordNames[i] : `${keywordNames[i % keywordNames.length]}${i}`);
  const resolution = await api('table-shape-resolutions', {
    ...st(), outputId, catalogId: caps.value.catalogId, kind: 'PIVOT',
    pivot: {
      categoryDiscoveryId: discovery.value.discoveryIdentity,
      groupColumnChoiceIds: groupChoices.map(choiceIdOf),
      categoryColumnChoiceId: choiceIdOf(categoryChoice),
      valueColumnChoiceId: choiceIdOf(valueChoice),
      categories: discoveredCategories.map((c, i) => ({
        choiceId: typeof c === 'string' ? c : (c?.id ?? c?.choiceId),
        outputColumn: probeName(i), outputLabel: String(c?.label ?? c?.key ?? c ?? ''),
      })),
      duplicatePolicyChoiceId: choiceIdOf(dupChoice),
      missingPolicyChoiceId: choiceIdOf((caps.value.missingCellPolicies ?? [])[0]),
      unlistedPolicyChoiceId: choiceIdOf((caps.value.unlistedCategoryPolicies ?? [])[0]),
    },
  });
  assertNoInternal(resolution, 'pivot resolution (duplicate-ID class must not 500)');
  assert.equal(resolution.status, 200, `pivot resolution failed (${resolution.status}): ${JSON.stringify(resolution.value).slice(0, 300)}`);
  const proposal = await api('table-shape-proposals', {
    ...st(), outputId, catalogId: caps.value.catalogId, mode: 'ADD',
    reshapeResolutionId: resolution.value.resolutionId ?? resolution.value.id, derivedResolutionIds: [],
  });
  assertNoInternal(proposal, 'pivot proposal');
  assert.equal(proposal.status, 200, `pivot proposal failed (${proposal.status}): ${JSON.stringify(proposal.value).slice(0, 300)}`);
  const comparisonAvailable = proposal.value?.comparison?.status === 'AVAILABLE' || proposal.value?.proposal?.comparison?.status === 'AVAILABLE';
  const changedColumns = proposal.value?.comparison?.changedColumns ?? proposal.value?.proposal?.comparison?.changedColumns ?? [];
  assert.ok(comparisonAvailable, `comparison probe not AVAILABLE: ${JSON.stringify(proposal.value).slice(0, 400)}`);
  assert.ok(keywordNames.every((n) => changedColumns.includes(n)), `keyword columns missing from server comparison surface: ${JSON.stringify(changedColumns).slice(0, 300)}`);
  record('pk-comparison-proposal-available', { changedColumns: changedColumns.slice(0, 8), categories: discoveredCategories.map((c) => c.key ?? c.choiceId), status: 'AVAILABLE' });
  markPhase('server-probe');

  await openPivotEditor();
  await configureFieldsAndCategories();
  await setNameInputs(keywordNames);
  await screenshot('step2-before-apply');
  await until(async () => mounted('[data-testid="construction-apply-proposal"]') && await evaluate(`(()=>{const b=document.querySelector('[data-testid="construction-apply-proposal"]');return b && !b.disabled;})()`), 'apply-proposal for keyword-named pivot renders enabled', 20000).catch(async (error) => {
    const panelDiag = await evaluate(`(()=>{const p=document.querySelector('[data-testid="construction-proposal-panel"]');return p?{status:p.getAttribute('data-proposal-status'),stats:[...p.querySelectorAll('[role="status"],[role="alert"]')].map(e=>e.textContent.trim().slice(0,160))}:null;})()`);
    assert.fail(`apply-proposal control absent or disabled for a valid keyword-named pivot; panel=${JSON.stringify(panelDiag)} (${String(error?.message).slice(0, 120)})`);
  });
  const applyReqs = [];
  client.on('Network.requestWillBeSent', (event) => { if (String(event.request?.url ?? '').includes('/authoring/v2/'))   applyReqs.push({ method: event.request.method, tail: String(event.request.url).split('/v2/')[1], bodies: String(event.request.postData ?? '').slice(0, 240) }); });
  const draftBeforeApply = (await api('builder')).value?.draftVersion;
  await realClick('[data-testid="construction-apply-proposal"]');
  await wait(600);
  const postClickPanel = await evaluate(`(()=>{const p=document.querySelector('[data-testid="construction-proposal-panel"]');return p?{status:p.getAttribute('data-proposal-status'),hasApply:!!p.querySelector('[data-testid="construction-apply-proposal"]')}:null;})()`);
  report.steps.push({ step: 'apply-click', draftBeforeApply, applyReqs, postClickPanel });
  const afterApply = await api('builder');
  assertNoInternal(afterApply, 'builder-after-apply');
  const doc0 = (afterApply.value.workspace.documents ?? [])[0] ?? {};
  const collectPivotColumnNames = (document) => {
    const found = [];
    const walk = (node) => {
      if (Array.isArray(node)) { for (const child of node) walk(child); return; }
      if (node && typeof node === 'object') {
        if (String(node.id ?? '').startsWith('pivot-column') && typeof node.name === 'string') found.push(node.name);
        for (const value of Object.values(node)) walk(value);
      }
    };
    walk(document);
    return found;
  };
  const pivotColumnNames = (document) => {
    const names = collectPivotColumnNames(document);
    if (names.length > 0) return names;
    return (document?.columns ?? []).filter((c) => String(c?.id ?? '').startsWith('pivot-column')).map((c) => c?.name);
  };
  const draftAfterApply = afterApply.value?.draftVersion;
  const constructionHasPivot = JSON.stringify(doc0?.construction ?? null).includes('"PIVOT"');
  const appliedPivotNames = pivotColumnNames(doc0);
  assert.ok(draftAfterApply !== draftBeforeApply, `apply reached no server write: draftVersion ${draftBeforeApply} unchanged; panel=${JSON.stringify(postClickPanel)} reqs=${JSON.stringify(applyReqs).slice(0, 400)}`);
  assert.ok(constructionHasPivot || appliedPivotNames.length > 0, `apply bumped the draft but persisted no pivot representation: doc=${JSON.stringify(doc0?.construction ?? null).slice(0, 500)}`);
  record('pk-apply-success', { persistedVia: 'construction-step', persistedNames: appliedPivotNames.slice(0, 4), draftVersion: draftAfterApply });

  await realClick(`[data-testid="construction-table-${outputId}"]`);
  await wait(700);
  for (let attempt = 0; attempt < 2; attempt += 1) {
    const g = await readGrid();
    if (g?.headers?.includes(keywordNames[0])) break;
    if (attempt === 0) { await send('Page.reload', {}); await until(() => mounted('[data-testid="construction-workspace"]'), 'workspace after grid-refresh reload', 30000); await realClick(`[data-testid="construction-table-${outputId}"]`); await wait(900); }
  }
  const grid = await until(async () => {
    const g = await readGrid();
    if (!g) return null;
    if (!g.headers.some((h) => h === keywordNames[0])) return null;
    return g;
  }, 'preview grid shows the keyword header', 40000);
  const keywordCells = await evaluate(`(()=>{const sec=document.querySelector('[data-testid="construction-preview"]');const t=sec?.querySelector('[data-testid="preview-table-content"]')||sec;const headerRow=t.querySelector('[role="row"]');const headers=[...headerRow.querySelectorAll('[role="columnheader"],[role="cell"]')].map(c=>c.textContent.trim());const idx=${JSON.stringify(keywordNames)}.map(n=>headers.indexOf(n));const trs=[...t.querySelectorAll('[role="row"]')].filter(r=>!r.className.includes('construction-preview-header-row'));const out=[];for(const tr of trs){const cells=[...tr.querySelectorAll('[role="cell"]')].map(td=>td.textContent.trim());out.push(idx.map(i=>i>=0?cells[i]:null));}return out;})()`);
  const numeric = keywordCells.flat().filter((v) => v !== null && v !== '').map(Number).filter((n) => Number.isFinite(n));
  assert.ok(numeric.length >= 1, `no numeric cells under the keyword columns; grid=${JSON.stringify(grid.cells).slice(0, 300)}`);
  assert.ok(numeric.some((n) => n !== 0), 'all keyword cells zero: the live dataset comparison would be vacuous');
  assert.ok(grid.previewReceipt, 'keyword grid renders without a backing receipt (the comparison evidence would be unattributed)');
  assert.equal(grid.previewStatus, 'ready', `keyword grid preview not ready: ${grid.previewStatus}`);
  record('pk-grid-values', { headers: grid.headers.slice(0, 10), numericCount: numeric.length, sample: numeric.slice(0, 4), previewStatus: grid.previewStatus, receipt: grid.previewReceipt });
  await screenshot('step3-keyword-grid');
  markPhase('grid');

  const outputs = pivotColumnNames((afterApply.value.workspace.documents ?? [])[0] ?? {});
  assert.deepEqual(outputs.slice(0, 2).sort(), [...keywordNames].sort(), `persisted pivot column names drifted from the typed keywords: ${JSON.stringify(outputs)}`);
  record('pk-document-persistence', { outputs, persistedColumnCount: outputs.length });

  await navigateToLane('post-apply reload');
  await until(async () => (await mounted('[data-testid="construction-workspace"]')) && (await readGrid()), 'workspace + grid after reload', 60000);
  await realClick(`[data-testid="construction-table-${outputId}"]`);
  const reloadedGrid = await until(async () => {
    const g = await readGrid();
    if (!g?.headers.includes(keywordNames[0]) || !g?.headers.includes(keywordNames[1])) return null;
    return g;
  }, 'reloaded grid still shows both keyword headers', 40000);
  const reloadedNumeric = await evaluate(`(()=>{const sec=document.querySelector('[data-testid="construction-preview"]');const t=sec?.querySelector('[data-testid="preview-table-content"]')||sec;const headerRow=t.querySelector('[role="row"]');const headers=[...headerRow.querySelectorAll('[role="columnheader"],[role="cell"]')].map(c=>c.textContent.trim());const idx=${JSON.stringify(keywordNames)}.map(n=>headers.indexOf(n));const trs=[...t.querySelectorAll('[role="row"]')].filter(r=>!r.className.includes('construction-preview-header-row'));const out=[];for(const tr of trs){const cells=[...tr.querySelectorAll('[role="cell"]')].map(td=>td.textContent.trim());out.push(idx.map(i=>i>=0?cells[i]:null));}return out;})()`);
  assert.deepEqual(reloadedNumeric, keywordCells, 'reload changed keyword cell values');
  assert.ok(reloadedGrid.previewReceipt, 'reloaded preview lost its backing receipt');
  record('pk-reload-persistence', { rowsEqual: JSON.stringify(reloadedNumeric) === JSON.stringify(keywordCells), receipt: reloadedGrid.previewReceipt });
  await screenshot('step4-reload');
  markPhase('reload');

  await openPivotEditor();
  await configureFieldsAndCategories();
  await setNameInputs([keywordNames[0], keywordNames[0]]);
  await wait(400);
  const applyStateDup = await evaluate(`(()=>{const b=document.querySelector('[data-testid="construction-apply-proposal"]');return b?{present:true,disabled:b.disabled}:{present:false};})()`);
  const statusText = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-reshape-pivot"]');return s?[...s.querySelectorAll('[role="status"],[role="alert"],p')].map(e=>e.textContent.trim()).filter(Boolean).slice(0,8):[];})()`);
  let dupOutcome;
  if (!applyStateDup.present || applyStateDup.disabled) {
    dupOutcome = { lane: 'ui-gate', applyState: applyStateDup, statusText };
  } else {
    const cmdWindowStart = cmdResponses.length;
    await realClick('[data-testid="construction-apply-proposal"]');
    await wait(700);
    const newCmds = cmdResponses.slice(cmdWindowStart);
    let dupCode = null;
    for (const entry of newCmds) {
      assert.ok(entry.status < 500, `duplicate-name apply 5xx (the pivot-500 class this journey guards): ${JSON.stringify(entry)}`);
      if (entry.status >= 400 && dupCode === null) {
        try {
          const body = await send('Network.getResponseBody', { requestId: entry.requestId });
          const parsed = JSON.parse(Buffer.from(body.body ?? '', body.base64Encoded ? 'base64' : 'utf8').toString('utf8'));
          dupCode = parsed?.error?.code ?? parsed?.code ?? parsed?.diagnostics?.[0]?.code ?? null;
        } catch { dupCode = null; }
      }
    }
    const afterDupAttempt = await api('builder');
    assertNoInternal(afterDupAttempt, 'duplicate-name apply attempt');
    const stillKeyword = pivotColumnNames((afterDupAttempt.value.workspace.documents ?? [])[0] ?? {});
    const dupCount = stillKeyword.filter((n) => n === keywordNames[0]).length;
    assert.ok(dupCount < 2, 'duplicate names were silently accepted into the document');
    dupOutcome = newCmds.length === 0
      ? { lane: 'ui-blocked-no-request', applyState: applyStateDup, observedResponses: 0 }
      : { lane: 'server-rejection', applyState: applyStateDup, statuses: newCmds.map((c) => c.status), code: dupCode };
    if (dupOutcome.lane === 'server-rejection') {
      const typed4xx = newCmds.every((c) => c.status >= 400 && c.status < 500) && legit4xx.test(String(dupCode));
      const noop2xx = newCmds.every((c) => c.status === 200) && dupCount === 0 && (afterDupAttempt.value?.draftVersion ?? null) === (await api('builder')).value?.draftVersion;
      assert.ok(typed4xx || noop2xx, `duplicate-name server lane in an inadmissible shape: ${JSON.stringify(dupOutcome)}`);
    }
  }
  const postDup = await api('builder');
  assertNoInternal(postDup, 'builder-after-duplicate');
  const docOutputs = pivotColumnNames((postDup.value.workspace.documents ?? [])[0] ?? {});
  const docDupNames = docOutputs.filter((n, i) => docOutputs.indexOf(n) !== i);
  assert.ok(docDupNames.length === 0, `duplicate names reached the persisted document: ${JSON.stringify(docOutputs)}`);
  const dupRecordable = ['ui-gate', 'ui-blocked-no-request', 'server-rejection'].includes(dupOutcome.lane)
    && (dupOutcome.lane !== 'server-rejection' || dupOutcome.statuses.every((s) => s >= 400 && s < 500 ? legit4xx.test(String(dupOutcome.code)) : s === 200));
  assert.ok(dupRecordable, `duplicate-name rejection in an inadmissible class: ${JSON.stringify(dupOutcome)}`);
  record('pk-duplicate-negative-blocked', dupOutcome);
  await screenshot('step5-duplicate-gate');

  await setNameInputs([keywordNames[0], keywordNames[1]]);
  const sampleProposal = () => evaluate(`(()=>{const b=document.querySelector('[data-testid="construction-apply-proposal"]');const p=document.querySelector('[data-testid="construction-proposal-panel"]');return {apply:b?{present:true,disabled:b.disabled}:{present:false},panel:p?{status:p.getAttribute('data-proposal-status'),stats:[...p.querySelectorAll('[role="status"],[role="alert"]')].map(e=>e.textContent.trim().slice(0,120))}:null};})()`);
  const recoverState = await until(async () => {
    const s = await sampleProposal();
    return s.apply.present && !s.apply.disabled ? s : null;
  }, 'valid keyword rename re-enables apply', 25000).catch(async (error) => {
    const diag = await sampleProposal();
    assert.fail(`valid rename did not re-enable apply (positive control for the duplicate gate): ${JSON.stringify(diag)} vs dup ${JSON.stringify(applyStateDup)} (${String(error?.message).slice(0, 100)})`);
  });
  record('pk-rename-recovers', { from: dupOutcome.lane, to: recoverState });
  if (await mounted('[data-testid="construction-row-intent-records"]')) {
    await realClick('[data-testid="construction-row-intent-records"]');
  } else {
    await realClick('[data-testid="construction-cancel-proposal"]');
  }
  await until(async () => !(await mounted('[data-testid="construction-reshape-pivot"]')), 'editor closed after recovery', 10000);
  const afterRecoverClose = await api('builder');
  const postCloseNames = pivotColumnNames((afterRecoverClose.value.workspace.documents ?? [])[0] ?? {});
  assert.ok(!(postCloseNames.filter((n) => n === keywordNames[0]).length >= 2), `the discarded duplicate-name edit leaked into the document: ${JSON.stringify(postCloseNames)}`);

  await send('Page.reload', {});
  await until(() => mounted('[data-testid="construction-workspace"]'), 'workspace after pre-remove reload', 30000);
  await realClick(`[data-testid="construction-table-${outputId}"]`);
  await wait(500);
  await realClick('[data-testid="construction-rows-settings-trigger"]');
  const historyPresent = await until(async () => mounted('[data-testid="construction-row-operation-history"]'), 'applied-changes history visible', 20000).catch(() => false);
  assert.ok(historyPresent, 'applied-changes history missing; the remove affordance under test does not exist');
  const removeSelectors = await until(async () => {
    const found = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-row-operation-history"]');if(!s)return[];return [...s.querySelectorAll('[data-testid^="construction-row-remove-"]')].filter(b=>!b.disabled).map(b=>b.getAttribute('data-testid'));})()`);
    return found.length > 0 ? found : null;
  }, 'enabled construction-row-remove-* control in the history', 20000);
  const draftBeforeRemove = (await api('builder')).value?.draftVersion;
  for (const selector of removeSelectors) {
    await realClick(`[data-testid="${selector}"]`);
    await wait(350);
    const removalApplyAppeared = await until(async () => evaluate(`(()=>{const b=document.querySelector('[data-testid="construction-apply-proposal"]');return b&&!b.disabled?b.textContent.trim():null;})()`), 'removal confirm control appears', 1500).catch(() => null);
    if (removalApplyAppeared) {
      await realClick('[data-testid="construction-apply-proposal"]');
      report.steps.push({ step: 'removal-confirmed', via: selector, label: removalApplyAppeared });
    }
  }
  const draftMoved = await until(async () => ((await api('builder')).value?.draftVersion ?? null) !== draftBeforeRemove, 'server recorded the removal (draft moved)', 20000);
  void draftMoved;
  const baseGrid = await until(async () => {
    const g = await readGrid();
    if (!g) return null;
    if (g.headers.includes(keywordNames[0]) || g.headers.includes(keywordNames[1])) return null;
    return g;
  }, 'grid back to base shape after remove', 40000);
  const removedDoc = await api('builder');
  assertNoInternal(removedDoc, 'builder-after-remove');
  const stillPivotNames = pivotColumnNames((removedDoc.value.workspace.documents ?? [])[0] ?? {});
  const stillPivotStep = JSON.stringify((removedDoc.value.workspace.documents ?? [])[0]?.construction ?? null).includes('"PIVOT"');
  assert.ok(stillPivotNames.length === 0 && !stillPivotStep, `pivot survived remove: names=${JSON.stringify(stillPivotNames)} stepPresent=${stillPivotStep}`);
  assert.ok(Array.isArray(baseGrid.headers) && baseGrid.headers.length >= 4, `post-remove grid has no base shape: ${JSON.stringify(baseGrid.headers)}`);
  assert.ok(baseGrid.rowCount > 0, 'post-remove grid lost all rows');
  assert.deepEqual(baseGrid.headers, seededGrid.headers, 'remove did not restore the exact seeded base grid headers');
  record('pk-remove-restores', { removed: removeSelectors, headers: baseGrid.headers.slice(0, 8), rowCount: baseGrid.rowCount, seededHeaders: seededGrid.headers.slice(0, 8) });
  await screenshot('step6-remove-restored');
  markPhase('remove');

  const identityAfter = collectBuildIdentity();
  report.buildIdentity.after = identityAfter;
  assert.equal(identityAfter.runningBinary, identityBefore.runningBinary, `serving binary moved during run: ${identityAfter.runningBinary} != ${identityBefore.runningBinary}`);
  assert.equal(identityAfter.liveSource, identityBefore.liveSource, 'live source moved during run');
  assert.equal(identityAfter.gitHead, identityBefore.gitHead, 'HEAD moved during run');
  report.buildIdentity.stampVerified = true;
  markPhase('identity-after');

  if (exceptions.length > 0) {
    throw new Error('uncaught browser exceptions during the journey: ' + exceptions.join(';').slice(0, 300));
  }
  if (browser5xx.length > 0) {
    throw new Error('browser-side 5xx on authoring endpoints: ' + JSON.stringify(browser5xx).slice(0, 400));
  }
  report.browserRejectedRequests = browser4xx.slice(0, 12);

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
  } catch (screenshotError) { report.failureScreenshotError = String(screenshotError?.message || screenshotError).slice(0, 300); }
  report.exceptions.push(report.error);
  summaryLine = `JOURNEY_FAILED ${String(error?.message ?? error).slice(0, 400)}`;
}
await killTree();
try {
  rmSync(profile, { recursive: true, force: true });
} catch (profileError) {
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
}
console.log(summaryLine);
console.log(`PHASE_TIMINGS total=${report.phaseTimings.totalMs}ms ` + phaseTimings.phases.map((p) => `${p.phase}=${p.ms}ms`).join(' '));
process.exitCode = exitCode;
