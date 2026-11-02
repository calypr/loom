#!/usr/bin/env node
// ROW-SOURCE-02 §18 DERIVE row-source journey — committed repeatable verification.
// Standalone raw-CDP browser journey against the owned loom-dev stack, following the
// scripts/verify-ui/workflows raw-CDP convention (see core-protocol-browser.mjs and the
// sibling verify-explorer-filter-row-source.mjs): a directly-runnable node script, not a
// Playwright registry entry.
//
// Legs (sibling of the filter journey; slice-1 contract from internal/explorer/lifecycle/derive_source.go):
//   1 DISCOVERY   More actions -> Calculate entry enabled; editor optgroup "Dataset fields (not in output)"
//                 exposes the signed populated root scalar valueQuantity.value (decimal)
//   2 CONFIGURE   guided ADD, left = signed option, right = literal 3 -> proposal READY
//                 preview keeps all 6 rows (PROPAGATE_NULL row-preservation), derived == value + 3
//                 for the 3 populated rows and blank for the 3 rows without valueQuantity.value
//   3 CANCEL      byte-stable pre-proposal construction
//   4 APPLY       persisted DERIVE step + server-owned signed source projection (unselected field,
//                 ownerStepId == derive step id) + exact rows/cells
//   4 RELOAD      replacement banner + capability echo deriveSourceInput columnId == persisted projection
//   4 EDIT        literal 3 -> 2 => value + 2 preview, persisted; back to 3 => value + 3, persisted
//   4 REMOVE      derive steps 1->0, derived column gone, no derive-owned projection left behind
//   4 RE-ADD      honest re-materialization: new derive-owned columnId, capability echo equal
//   API negatives 9/9 + 2 pinned contract behaviours (valid control decorated, forged sig 409,
//                 cross-snapshot stale 409, tampered path 409, empty/absent choiceId 400,
//                 three selections 400 at-most-2, duplicate 400, column mismatch 422,
//                 unpopulated valueInteger never offered/never accepted, non-DERIVE step 200-undecorated)
//
// Exit-code contract (kept from the sibling): every named check must run AND pass. A skipped
// (missing) check or a thrown assertion is a failure; an empty identity where rows are required
// is a failure. Any failure exits 1.
//
// Reproduce (owned stack env identifies the target, per loom-dev conventions):
//   LOOM_DEV_SOURCE_ROOT=<repo> LOOM_DEV_COMPOSE_PROJECT=loom-dev-stravinsky \
//   LOOM_DEV_API_PORT=8182 LOOM_DEV_UI_PORT=3182 \
//   node scripts/verify-ui/workflows/verify-explorer-derive-row-source.mjs --out <FRESH_DIR>
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { CDPClient } from '../../browser/cdp.mjs';
import { browserURL } from './builder-url.mjs';
import { AUTHORING_SEMANTICS_VERSION, commandEnvironment, createDevSession, createFreshVerificationFixture, generatedJ01ConceptNDJSON } from '../../loom-dev.mjs';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', '..');
const STAMP_SCRIPT = '/workspace/loom-dev-build-stamp.sh';

const options = {};
for (let i = 2; i < process.argv.length; i += 1) {
  const key = { '--out': 'out', '--chrome': 'chrome', '--run-label': 'runLabel' }[process.argv[i]];
  assert.ok(key && process.argv[i + 1], `Unknown or missing-valued option: ${process.argv[i]}`);
  options[key] = process.argv[++i];
}
assert.ok(options.out, '--out <FRESH_DIR> is required');
const out = isAbsolute(options.out) ? options.out : resolve(process.cwd(), options.out);
assert.ok(!existsSync(out) || readdirSync(out).length === 0, 'Refusing to overwrite browser evidence directory');
mkdirSync(out, { recursive: true });

const runStem = options.runLabel ?? `drsrc${randomUUID().replace(/-/g, '').slice(0, 8)}`;
assert.match(runStem, /^[a-z0-9][a-z0-9-]{0,16}$/, '--run-label must be a short lowercase slug leaving room for two fixture suffixes');
const mainLabel = `${runStem}-a`;
const negLabel = `${runStem}-b`;
const staleLabel = `${runStem}-c`;
const DERIVED_LABEL = 'Derived + 3';
const DERIVED_NAME = 'derived_plus_three';

const session = createDevSession(process.env, REPO_ROOT);
const uiOrigin = session.uiUrl;
const apiOrigin = session.apiUrl;

const seed = async (result) => {
  const project = result.target.fixtureProject;
  const explorer = result.seed.bootstrapExplorerId;
  const base = `${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
  const call = async (path, body) => {
    const response = await fetch(`${base}/${path}`, {
      method: body ? 'POST' : 'GET',
      headers: body ? { 'Content-Type': 'application/json' } : {},
      body: body ? JSON.stringify(body) : undefined,
    });
    return { status: response.status, value: await response.json().catch(() => ({})) };
  };
  let builder = (await call('builder')).value;
  const root = builder.catalog.nodes.find((node) => node.rowRootEligible && node.resourceType === 'Observation').nodeId;
  const idCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === root
    && String(candidate.fieldPath || '').replace(/^root\./, '') === 'id');
  const valueCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === root
    && String(candidate.fieldPath || '').replace(/^root\./, '') === 'valueInteger');
  assert.ok(valueCandidate, 'seed requires the valueInteger numeric candidate for the stage capability');
  const commands = async (commandId, list, state) => call('commands', {
    commandId,
    semanticsVersion: AUTHORING_SEMANTICS_VERSION,
    snapshotToken: state.catalog.snapshotToken,
    expectedDraftVersion: state.draftVersion,
    ...(state.draftDigest ? { expectedDraftDigest: state.draftDigest } : {}),
    commands: list,
  });
  let response = await commands(`${runStem}-table`, [{ type: 'CREATE_TABLE', title: 'RSO derive row source', rootNodeId: root }], builder);
  assert.equal(response.status, 200, `seed CREATE_TABLE failed (${response.status}): ${JSON.stringify(response.value).slice(0, 400)}`);
  builder = (await call('builder')).value;
  const outputId = builder.workspace.documents[0].output.id;
  response = await commands(`${runStem}-id`, [{
    type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId,
    projectionMode: idCandidate.defaultProjectionMode, initialPresentation: 'TABLE', title: 'ID',
  }], builder);
  assert.equal(response.status, 200, `seed ADD_COLUMN failed (${response.status}): ${JSON.stringify(response.value).slice(0, 400)}`);
  // The stage DERIVE capability requires >=1 public scalar numeric column on the append stage
  // (internal/dataframe/compiler/lower/recipe_construction.go:2242), so seed a projected numeric
  // column; the signed dataset-field lane still binds its own server-owned source column.
  // valueInteger (not valueQuantity.value) is deliberate: projecting the SAME field the signed
  // slot claims makes the compiler emit 500 CONSTRUCTION_RECEIPT_CONTRACT_INVALID with no
  // actionable diagnostic (live-pinned defect, evidence /tmp/opencode/derive-probe/probe6 runs).
  builder = (await call('builder')).value;
  response = await commands(`${runStem}-value`, [{
    type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: valueCandidate.candidateId,
    projectionMode: valueCandidate.defaultProjectionMode, initialPresentation: 'TABLE', title: 'Number',
  }], builder);
  assert.equal(response.status, 200, `seed ADD_COLUMN value failed (${response.status}): ${JSON.stringify(response.value).slice(0, 400)}`);
  return { project, explorer, base, call };
};

const report = {
  status: 'running',
  schemaVersion: 1,
  mode: 'raw-cdp-derive-row-source-journey',
  builtAt: new Date().toISOString(),
  environment: { composeProject: session.composeProject, apiUrl: apiOrigin, uiUrl: uiOrigin, runStem, mainLabel, negLabel, staleLabel },
  buildIdentity: null,
  oracle: null,
  fixture: null,
  checks: [],
  steps: [],
  screenshots: [],
  exceptions: [],
  negatives: [],
  error: null,
};
const record = (name, evidence) => {
  const existing = report.checks.find((entry) => entry.name === name);
  if (existing) existing.status = 'passed', existing.evidence = { ...existing.evidence, ...evidence };
  else report.checks.push({ name, status: 'passed', evidence });
};
const save = (name, value) => writeFileSync(join(out, name), typeof value === 'string' ? value : JSON.stringify(value, null, 2));

// Independent oracle over the FULL live dataset for the base Observation node: the pinned
// testdata/devloop-fixture ndjson plus the repo-generated J01 concept observations. The base
// Observation catalog node spans all routes (1006 rows) and the filter lane only signs
// cardinality-one scalars, so grids are validated row-by-row against this full oracle, with one
// deterministic scoped grid on valueQuantity.unit='cm' (exactly the two devloop cm Observations).
const computeOracle = () => {
  const observations = readFileSync(join(session.fixtureDir, 'Observation.ndjson'), 'utf8')
    .split(/\r?\n/).filter((line) => line.trim()).map((line) => JSON.parse(line));
  const generated = (generatedJ01ConceptNDJSON(session.fixtureDir) || '')
    .split(/\r?\n/).filter((line) => line.trim()).map((line) => JSON.parse(line));
  const rows = [...observations, ...generated]
    .map((observation) => ({
      id: observation.id,
      status: observation.status,
      unit: observation?.valueQuantity?.unit ?? null,
      value: observation?.valueQuantity?.value ?? null,
    }));
  const byId = new Map(rows.map((row) => [row.id, row]));
  assert.ok(rows.length >= 1006, `Fixture oracle expects at least six devloop + 1000 generated J01 observations, got ${rows.length}`);
  assert.ok(rows.every((row) => typeof row.id === 'string' && row.id.length > 0), 'every observation must have an id');
  assert.ok(rows.every((row) => row.value === null || Number.isFinite(row.value)), 'every oracle value must be numeric or null');
  assert.ok(rows.every((row) => row.status === 'final'), 'every fixture observation is status final (the prelude filter is row-preserving)');
  const cmRows = rows.filter((row) => row.unit === 'cm').sort((a, b) => (a.id < b.id ? -1 : 1));
  assert.equal(cmRows.length, 2, 'Fixture oracle expects exactly two cm Observations');
  assert.ok(cmRows.every((row) => Number.isFinite(row.value)), 'cm scoping rows must all carry valueQuantity.value');
  const nullRows = rows.filter((row) => row.value === null);
  assert.ok(nullRows.length >= 3, 'Fixture oracle expects unpopulated-value rows for the PROPAGATE_NULL evidence');
  const populatedRows = rows.filter((row) => row.value !== null);
  assert.ok(populatedRows.length >= 3, 'Fixture oracle expects populated-value rows for arithmetic evidence');
  return { byId, rows, cmRows, derived: (row, offset) => (row.value === null ? null : Number((row.value + offset).toFixed(6))) };
};

// Docker build-stamp identity, using loom-dev.mjs's own command environment so the scoped bridge
// accepts the exec (service loom-api, /workspace stamp script, --check), same as the sibling.
const collectBuildIdentity = () => {
  const env = commandEnvironment({
    composeProject: session.composeProject,
    sourceRoot: session.sourceRoot,
    fixtureProject: session.fixtureProject,
    fixtureGeneration: session.fixtureGeneration,
    host: session.host,
    apiPort: session.apiPort,
    uiPort: session.uiPort,
    populationMappingCursorSecret: session.populationMappingCursorSecret,
  });
  const stamp = spawnSync('docker', ['compose', '--project-name', session.composeProject, '--file', join(session.sourceRoot, 'compose.dev.yaml'),
    'exec', '-T', 'loom-api', STAMP_SCRIPT, '--check'], { encoding: 'utf8', env, timeout: 30000 });
  const recorded = spawnSync('docker', ['compose', '--project-name', session.composeProject, '--file', join(session.sourceRoot, 'compose.dev.yaml'),
    'exec', '-T', 'loom-api', 'cat', '/workspace/tmp/arango-fhir-server.build.json'], { encoding: 'utf8', env, timeout: 30000 });
  const head = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: REPO_ROOT, encoding: 'utf8' });
  const [expectedSource, liveSource, runningBinary] = (stamp.stdout || '').trim().split(/\s+/);
  let recordedStamp = null;
  try { recordedStamp = JSON.parse(recorded.stdout); } catch { recordedStamp = { raw: (recorded.stdout || '').trim() }; }
  return {
    expectedSource: expectedSource ?? null,
    liveSource: liveSource ?? null,
    runningBinary: runningBinary ?? null,
    checkExit: stamp.status,
    recordedBinary: recordedStamp?.binary ?? null,
    recordedSource: recordedStamp?.source ?? null,
    gitHead: (head.stdout || '').trim() || null,
  };
};

// ---- Raw CDP harness (flags and DevToolsActivePort path match loom-dev/core-protocol) ----
const chromePath = options.chrome ?? process.env.LOOM_E2E_CHROME
  ?? ['/usr/bin/chromium', '/opt/google/chrome/chrome'].find((candidate) => existsSync(candidate));
assert.ok(chromePath && existsSync(chromePath), 'No Chrome binary: pass --chrome, set LOOM_E2E_CHROME, or install /opt/google/chrome/chrome');

const profile = mkdtempSync(join(tmpdir(), 'drsrc-prof-'));
const chrome = spawn(chromePath, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--remote-debugging-port=0',
  `--user-data-dir=${profile}`, '--window-size=1600,1200', '--no-first-run', '--no-default-browser-check',
  '--disable-background-networking', '--disable-component-update', '--disable-sync', '--proxy-server=direct://', 'about:blank'],
  { stdio: ['ignore', 'ignore', 'pipe'] });
let chromeErr = '';
chrome.stderr.on('data', (chunk) => { chromeErr = (chromeErr + chunk).slice(-4000); });

const wait = (ms) => new Promise((done) => setTimeout(done, ms));
let mainCtx = null;
let client = null;
let unpopulatedPaths = [];
let postPreludeBaseline = null;
const exceptions = [];
report.exceptions = exceptions;

try {
  const portFile = join(profile, 'DevToolsActivePort');
  for (let i = 0; i < 150 && !existsSync(portFile); i += 1) await wait(100);
  assert.ok(existsSync(portFile), `chrome devtools port not advertised; stderr=${chromeErr.slice(-400)}`);
  const [port] = readFileSync(portFile, 'utf8').trim().split('\n');
  const target = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' })).json();
  client = await CDPClient.connect(target.webSocketDebuggerUrl);
  const send = (method, params = {}, ms = 25000) => Promise.race([
    client.send(method, params),
    new Promise((_, reject) => setTimeout(() => reject(new Error(`timeout ${method}`)), ms)),
  ]);
  client.on('Runtime.exceptionThrown', (event) => exceptions.push(event.exceptionDetails?.exception?.description
    || event.exceptionDetails?.text || 'exception'));

  const until = async (probe, message, ms = 30000) => {
    const deadline = Date.now() + ms;
    for (;;) {
      const value = await probe();
      if (value) return value;
      if (Date.now() > deadline) {
        try { save(`FAIL-${String(message).replace(/\W+/g, '_')}.html`, await evaluate('document.documentElement.outerHTML')); } catch { /* best effort */ }
        assert.fail(`condition failed: ${message}`);
      }
      await wait(80);
    }
  };
  const evaluate = async (expression, ms = 25000) => {
    const result = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }, ms);
    assert.ok(!result.exceptionDetails, JSON.stringify(result.exceptionDetails));
    return result.result?.value;
  };
  const bounding = (selector) => evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(!e)return null;const b=e.getBoundingClientRect();return{x:b.x+b.width/2,y:b.y+b.height/2,w:b.width,h:b.height};})()`);
  const mounted = (selector) => evaluate(`Boolean(document.querySelector(${JSON.stringify(selector)}))`);
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
  const setSelect = (selector, value) => evaluate(`(()=>{const el=document.querySelector(${JSON.stringify(selector)});if(!el)return 'missing';const setter=Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype,'value').set;setter.call(el,${JSON.stringify(value)});el.dispatchEvent(new Event('change',{bubbles:true}));return el.value;})()`);
  const setInput = (selector, value) => evaluate(`(()=>{const el=document.querySelector(${JSON.stringify(selector)});if(!el)return 'missing';const setter=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;setter.call(el,${JSON.stringify(value)});el.dispatchEvent(new Event('input',{bubbles:true}));return el.value;})()`);
  const screenshot = async (name) => {
    const shot = await send('Page.captureScreenshot', { format: 'png' });
    const file = `${name}.png`;
    writeFileSync(join(out, file), Buffer.from(shot.data, 'base64'));
    report.screenshots.push(file);
  };
  const readDomPreview = () => evaluate(`(()=>{const t=document.querySelector('[data-testid="construction-proposal-preview"]')||document.querySelector('[data-testid="construction-preview"]');if(!t)return null;const th=[...t.querySelectorAll('thead th, [role=columnheader]')].map(e=>e.textContent.trim());const trs=[...t.querySelectorAll('tbody tr,[role=row]')];const rows=trs.map(tr=>[...tr.querySelectorAll('td,[role=cell]')].map(td=>td.textContent.trim()));return{headers:th,rows};})()`);
  const navigate = async () => {
    await send('Page.navigate', { url: browserURL({ uiUrl: uiOrigin }, mainCtx.project, mainCtx.explorer, 'builder') });
    await until(() => mounted('[data-testid="construction-workspace"]'), 'workspace mounted', 60000);
    await until(() => mounted('[data-testid="construction-action-keep-rows"]'), 'keep-rows action visible', 60000);
    await wait(300);
  };
  const openCalculateEditor = async () => {
    if (!(await mounted('[data-testid="construction-action-calculate"]'))) {
      await realClick('[data-testid="construction-more-actions"]');
    }
    const entry = await until(() => evaluate(`(()=>{const e=document.querySelector('[data-testid="construction-action-calculate"]');return e?!e.disabled&&{aria:e.getAttribute('aria-label')}:null;})()`),
      'Calculate entry present and enabled in More actions', 20000);
    await realClick('[data-testid="construction-action-calculate"]');
    await until(() => mounted('[data-testid="construction-calculate-editor"]'), 'calculate editor mounted', 20000);
    return entry;
  };
  const closeCalculateEditor = async () => {
    if (await mounted('[data-testid="construction-cancel-proposal"]')) { await realClick('[data-testid="construction-cancel-proposal"]'); await wait(200); }
    if (await mounted('[data-testid="construction-close-operation-editor"]')) await realClick('[data-testid="construction-close-operation-editor"]');
    await until(() => evaluate(`!document.querySelector('[data-testid="construction-calculate-editor"]')`), 'calculate editor closed', 15000);
  };
  // Guided form: ADD, left = signed dataset-field option, right = numeric literal.
  const configureCalculateForm = async (signedOptionValue, literalText) => {
    await setSelect('[data-testid="construction-calculate-editor"] select[aria-label="Operation"]', 'ADD');
    const guided = await mounted('[data-testid="construction-calculate-editor"] select[aria-label="Operation"]');
    assert.ok(guided, 'guided expression view (Operation select) must be visible by default');
    await setSelect('[data-testid="construction-calculate-editor"] select[aria-label="First value kind"]', 'column');
    await until(() => mounted('[data-testid="construction-calculate-left-column"]'), 'left column select', 15000);
    const committed = await setSelect('[data-testid="construction-calculate-editor"] select[aria-label="First value column"]', signedOptionValue);
    // Binding a signed option re-keys the slot to its server-shaped columnId and removes the
    // signed choice from the options list (FormulaOperand filters boundChoiceIds).
    assert.ok(/^derive-input_/.test(committed),
      `picking signed left option must materialize a derive-input_ column slot, got ${JSON.stringify(committed)}`);
    const stillOffered = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-calculate-editor"] select[aria-label="First value column"]');if(!s)return 'missing';const og=s.querySelector('optgroup[label="Dataset fields (not in output)"]');if(!og)return 'removed';return [...og.querySelectorAll('option')].some(o=>o.value===${JSON.stringify(signedOptionValue)})?'still-listed':'removed';})()`);
    assert.equal(stillOffered, 'removed', `signed option must disappear from options once bound (got ${stillOffered})`);
    await setSelect('[data-testid="construction-calculate-editor"] select[aria-label="Second value kind"]', 'literal');
    await until(() => mounted('[data-testid="construction-calculate-editor"] input[aria-label="Second value number"]'), 'right number input', 15000);
    const setBack = await setInput('[data-testid="construction-calculate-editor"] input[aria-label="Second value number"]', literalText);
    assert.equal(setBack, literalText, 'right number input set failed');
    await setInput('[data-testid="construction-calculate-editor"] input[aria-label="Output column name"]', DERIVED_NAME);
    await setInput('[data-testid="construction-calculate-editor"] input[aria-label="Output column label"]', DERIVED_LABEL);
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'proposal ready', 30000);
  };
  const readSignedLeftOption = async () => evaluate(`(()=>{
    const s=document.querySelector('[data-testid="construction-calculate-editor"] select[aria-label="First value column"]');
    if(!s)return null;
    const og=s.querySelector('optgroup[label="Dataset fields (not in output)"]');
    if(!og)return null;
    const opt=[...og.querySelectorAll('option')].find(o=>o.getAttribute('title')==='valueQuantity.value' && o.getAttribute('data-testid')==='construction-calculate-left-source-option');
    return opt?{value:opt.value,label:opt.textContent.trim(),title:opt.getAttribute('title')}:null;
  })()`);
  const builderDoc = async () => (await mainCtx.call('builder')).value?.workspace?.documents?.[0]?.construction || {};
  const previewRowsByLabel = (preview) => (preview?.rows || [])
    .map((row) => ({ id: (row.find((cell) => /^dev-/.test(cell)) ?? '').trim(), derived: ((preview?.headers || []).findIndex((header) => header.includes('Derived')) >= 0 ? row[(preview.headers).findIndex((header) => header.includes('Derived'))] : '') ?? '' }))
    .filter((entry) => entry.id !== '');
  const evidence = { blankCells: 0, numericCells: 0 };
  const checkPreviewAgainstOracle = (preview, offset, label, exactIds = null) => {
    const seen = previewRowsByLabel(preview);
    assertNonEmpty(seen, `${label} preview rows`);
    const ids = seen.map((entry) => entry.id);
    assert.equal(new Set(ids).size, ids.length, `${label}: duplicate visible row ids ${JSON.stringify(ids)}`);
    for (const entry of seen) {
      const row = oracle.byId.get(entry.id);
      assert.ok(row, `${label}: visible row ${entry.id} is not part of the live observation oracle`);
      const want = oracle.derived(row, offset);
      if (want === null) {
        assert.ok(entry.derived === '' || /^(-|n\/?a|null|—)$/i.test(entry.derived), `${label}: row ${entry.id} (no valueQuantity.value) must keep a preserved blank derived cell, got ${JSON.stringify(entry.derived)}`);
        evidence.blankCells += 1;
      } else {
        const got = Number.parseFloat(entry.derived);
        assert.ok(Number.isFinite(got) && Math.abs(got - want) < 1e-6, `${label}: row ${entry.id} derived ${JSON.stringify(entry.derived)} != expected ${want}`);
        evidence.numericCells += 1;
      }
    }
    if (exactIds) {
      assert.deepEqual([...ids].sort(), [...exactIds].sort(), `${label}: scoped grid must show exactly the scoped rows: ${JSON.stringify(ids)}`);
    }
    return seen;
  };
  const deriveStep = async () => {
    const doc = await builderDoc();
    return (doc.steps || []).find((step) => step.operation?.kind === 'DERIVE');
  };
  const deriveSourceEchoCaps = async (step) => {
    const stageKeys = [(step.inputs || []).find((input) => input.kind === 'STEP_OUTPUT')?.stepId, 'source_projection']
      .filter((stageId, index, all) => stageId && all.indexOf(stageId) === index);
    let echo = null;
    for (let i = 0; i < 12 && !echo; i += 1) {
      const bb = (await mainCtx.call('builder')).value;
      for (const stageId of stageKeys) {
        const cc = await mainCtx.call('construction-capabilities', {
          snapshotToken: bb.catalog.snapshotToken, expectedDraftVersion: bb.draftVersion, expectedDraftDigest: bb.draftDigest,
          outputId: bb.workspace.documents[0].output.id, stageId,
        });
        const candidate = (cc.value?.deriveSourceInput?.choices || []).find((choice) => choice.fieldPath === 'valueQuantity.value');
        if (candidate?.columnId) { echo = candidate; break; }
      }
      if (!echo) await wait(200);
    }
    return echo;
  };
  const historyIdsOf = () => evaluate(`(()=>[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(e=>e.getAttribute('data-testid').replace('construction-history-step-','')))()`);
  const historyButtonEnabled = (historyId) => evaluate(`(()=>{const e=document.querySelector('[data-testid="construction-history-step-${historyId}"]');return !!e && !e.disabled;})()`);
  const historyButtonPressed = (historyId) => evaluate(`(()=>{const e=document.querySelector('[data-testid="construction-history-step-${historyId}"]');return !!e && e.getAttribute('aria-pressed')==='true';})()`);
  const clickHistoryStep = async (historyId) => {
    const selector = `[data-testid="construction-history-step-${historyId}"]`;
    await until(() => historyButtonEnabled(historyId), `history button enabled ${historyId}`, 20000);
    await evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(e)e.scrollIntoView({block:'center',inline:'center'});return true;})()`);
    await wait(120);
    const hit = await evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(!e)return 'missing';const r=e.getBoundingClientRect();if(r.width<1||r.height<1)return 'zero';const c=document.elementFromPoint(r.left+r.width/2,r.top+r.height/2);return !c?'offscreen':(e===c||e.contains(c)?'ok':('covered:'+c.tagName+'.'+(c.className||'').toString().slice(0,40)));})()`);
    if (hit === 'ok') await realClick(selector); else await evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});e.click();return true;})()`);
    await wait(150);
    if (!(await historyButtonPressed(historyId))) {
      await evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});e.click();return true;})()`);
      await wait(150);
    }
  };
  const openEditorFromHistory = async (editorReady) => {
    await until(() => mounted('[data-testid="construction-more-actions"]'), 'builder settled after reload', 25000);
    for (let round = 0; round < 3; round += 1) {
      const historyIds = await historyIdsOf();
      assert.ok(historyIds.length >= 2, `expected filter+derive history steps, got ${JSON.stringify(historyIds)}`);
      for (const historyId of historyIds) {
        await clickHistoryStep(historyId);
        try {
          await until(editorReady, `editor ready via history ${historyId}`, 10000);
          return historyId;
        } catch {
          if (await mounted('[data-testid="construction-close-operation-editor"]')) {
            await realClick('[data-testid="construction-close-operation-editor"]');
            await wait(400);
          }
        }
      }
      await navigate(); await wait(200);
    }
    assert.fail('no history step opened the requested editor');
  };

  // Build identity BEFORE the run (pin the deployed build).
  const stampBefore = collectBuildIdentity();
  report.buildIdentity = { before: stampBefore, after: null };
  save('build-identity.before.json', stampBefore);
  assert.equal(stampBefore.checkExit, 0, `serving build identity must be current before the journey runs: ${JSON.stringify(stampBefore)}`);

  // Independent oracle.
  const oracle = computeOracle();
  report.oracle = oracle;

  // Self-seed three independent fixtures: A carries the raw-CDP UI journey, B is a pristine proposal
  // host for the API negatives (its candidate references a single new step; a persisted DERIVE step in
  // the same project would be a second step and mask candidate validation), and C supplies a valid
  // signed valueQuantity.value choice whose embedded snapshot differs from B (the provably-stale 409).
  const mainFixture = await createFreshVerificationFixture(session, mainLabel, { populateBootstrap: false });
  const negFixture = await createFreshVerificationFixture(session, negLabel, { populateBootstrap: false });
  const staleFixture = await createFreshVerificationFixture(session, staleLabel, { populateBootstrap: false });
  mainCtx = await seed(mainFixture);
  const negCtx = await seed(negFixture);
  const staleCtx = await seed(staleFixture);
  const seedBaseline = (await mainCtx.call('builder')).value;
  save('baseline-builder.json', seedBaseline);
  report.fixture = {
    journeyProject: mainCtx.project, explorer: mainCtx.explorer,
    negativesProject: negCtx.project, staleProject: staleCtx.project,
    baselineDraftVersion: seedBaseline.draftVersion, baselineDraftDigest: seedBaseline.draftDigest,
  };

  await Promise.all([send('Page.enable'), send('Runtime.enable')]);

  // STEP 0 behavior: capabilities expose the deriveSourceInput valueQuantity.value choice.
  await navigate();
  {
    const builder = (await mainCtx.call('builder')).value;
    const caps = await mainCtx.call('construction-capabilities', {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, outputId: builder.workspace.documents[0].output.id, stageId: 'source_projection',
    });
    const dsi = caps.value.deriveSourceInput;
    const numericChoice = dsi?.choices?.find((choice) => choice.fieldPath === 'valueQuantity.value');
    assert.ok(caps.status === 200 && dsi && dsi.supported && numericChoice,
      `step 0 deriveSourceInput missing valueQuantity.value on capabilities (status ${caps.status})`);
    assert.ok(['integer', 'decimal'].includes(String(numericChoice.logicalType).toLowerCase()),
      `step 0 signed option must be numeric kind, got ${numericChoice.logicalType}`);
    assert.equal(numericChoice.isPopulated, true, 'step 0 signed option must report isPopulated');
    // Population flags come from catalog-global candidate stats, so which fields carry
    // isPopulated=false varies; capture them for the editor filter assertion below.
    unpopulatedPaths = dsi.choices.filter((choice) => choice.isPopulated !== true).map((choice) => choice.fieldPath);
    report.steps.push({ step: 'behave', status: caps.status, supported: dsi.supported, choicesPaths: dsi.choices.map((c) => c.fieldPath),
      valueQuantityColumnIdBefore: numericChoice.columnId ?? null });
    record('step-0-behavior-capabilities', { status: caps.status, supported: dsi.supported, choicesPaths: dsi.choices.map((c) => c.fieldPath) });
  }

  // STEP 1 PRELUDE: a saved row-preserving FILTER step (status EQUALS final). The Calculate
  // action is gated on construction.steps.length >= 1 (BuilderWorkspace calculateAvailable), so
  // the calculate lane is only reachable after one saved operation; this filter keeps every row.
  {
    await realClick('[data-testid="construction-action-keep-rows"]');
    await until(() => mounted('[data-testid="construction-filter-editor"]'), 'filter-editor mounted', 20000);
    const statusOption = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]');if(!s)return null;const og=s.querySelector('optgroup[label="Dataset fields (not in output)"]');if(!og)return null;const o=[...og.querySelectorAll('option')].find(o=>o.getAttribute('title')==='status');return o?o.value:null;})()`);
    assert.ok(statusOption, 'status signed option must be offered in the filter lane for the row-preserving prelude');
    await setSelect('[data-testid="construction-filter-editor"] select[aria-label="Column"]', statusOption);
    await until(() => mounted('[data-testid="construction-filter-editor"] select[aria-label="Condition"]'), 'condition select', 15000);
    await setSelect('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EQUALS');
    await until(() => mounted('[data-testid="construction-filter-editor"] input[aria-label="Value"]'), 'value input', 15000);
    await setInput('[data-testid="construction-filter-editor"] input[aria-label="Value"]', 'final');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'prelude proposal ready', 30000);
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      const doc = await builderDoc();
      if ((doc.steps || []).some((s) => s.operation?.kind === 'FILTER')) break;
      await wait(300);
    }
    await wait(400);
    const doc = await builderDoc();
    assert.ok((doc.steps || []).some((s) => s.operation?.kind === 'FILTER'), 'PRELUDE: FILTER step was not persisted');
    const preludeRows = await evaluate(`(()=>{const t=document.querySelector('[data-testid="preview-table-content"]');if(!t)return null;return [...t.querySelectorAll('[role=row]')].map(r=>[...r.querySelectorAll('[role=cell]')].map(c=>c.textContent.trim()));})()`);
    const preludeIds = (preludeRows || []).flat().filter((cell) => /^dev-/.test(cell));
    assertNonEmpty(preludeIds, 'PRELUDE post-apply preview');
    assert.ok(preludeIds.every((id) => oracle.byId.has(id)), `PRELUDE: unknown row ids in table: ${JSON.stringify(preludeIds.filter((id) => !oracle.byId.has(id)).slice(0, 5))}`);
    if (await mounted('[data-testid="construction-close-operation-editor"]')) await realClick('[data-testid="construction-close-operation-editor"]');
    postPreludeBaseline = (await mainCtx.call('builder')).value;
    record('step-1-prelude-filter-step', { preludeIdentityCount: preludeIds.length });
  }

  // STEP 1 DISCOVERY: Calculate entry + signed dataset-field option in the editor.
  {
    const entry = await openCalculateEditor();
    assert.ok(/calc/i.test(entry?.aria || ''), `Calculate action aria-label missing name: ${JSON.stringify(entry)}`);
    const replacementBanner = await mounted('[data-testid="construction-calculate-replacement"]');
    const newBanner = await mounted('[data-testid="construction-calculate-new-output"]');
    assert.ok(!replacementBanner && newBanner, 'fresh Calculate must advertise a new column (no replacement banner)');
    await setSelect('[data-testid="construction-calculate-editor"] select[aria-label="First value kind"]', 'column');
    await until(() => mounted('[data-testid="construction-calculate-left-column"]'), 'left column select', 15000);
    const discovery = await evaluate(`(()=>{
      const s=document.querySelector('[data-testid="construction-calculate-editor"] select[aria-label="First value column"]');
      if(!s)return {present:false};
      const main=[...s.querySelectorAll(':scope > option')].map(o=>({value:o.value,label:o.textContent.trim()}));
      const og=s.querySelector('optgroup[label="Dataset fields (not in output)"]');
      const opts=og?[...og.querySelectorAll('option')].map(o=>({value:o.value,label:o.textContent.trim(),title:o.getAttribute('title'),testid:o.getAttribute('data-testid')})):null;
      const vq=opts?opts.find(o=>o.title==='valueQuantity.value'):null;
      return {present:true,mainOptions:main,optgroupLabel:og?.getAttribute('label')??null,optgroupOptions:opts,valueQuantityOption:vq??null};
    })()`);
    save('step1-discovery-select.json', discovery);
    await screenshot('step1-discovery');
    assert.ok(discovery.present && discovery.optgroupLabel === 'Dataset fields (not in output)', `missing optgroup label ${discovery.optgroupLabel}`);
    assert.ok(discovery.valueQuantityOption && discovery.valueQuantityOption.title === 'valueQuantity.value',
      `valueQuantity.value signed option missing: ${JSON.stringify(discovery.valueQuantityOption)}`);
    assert.equal(discovery.valueQuantityOption.testid, 'construction-calculate-left-source-option', 'signed option data-testid mismatch');
    assert.ok(/value/i.test(discovery.valueQuantityOption.label) && /decimal/i.test(discovery.valueQuantityOption.label),
      `signed option label must name the decimal value field: ${discovery.valueQuantityOption.label}`);
    assert.ok(!(discovery.optgroupOptions || []).some((option) => unpopulatedPaths.includes(option.title)),
      `the editor must NOT offer isPopulated=false dataset fields (unpopulated paths ${JSON.stringify(unpopulatedPaths)}`);
    record('step-1-discovery', { optgroupLabel: discovery.optgroupLabel, optionTooltip: discovery.valueQuantityOption.title, optionLabel: discovery.valueQuantityOption.label });
    await closeCalculateEditor();
  }

  // STEP 2 CONFIGURE + oracle-exact preview (row-preserving), then preview evidence.
  {
    await openCalculateEditor();
    await setSelect('[data-testid="construction-calculate-editor"] select[aria-label="First value kind"]', 'column');
    await until(() => mounted('[data-testid="construction-calculate-left-column"]'), 'left column select', 15000);
    const option = await until(() => readSignedLeftOption(), 'signed valueQuantity.value option present', 20000);
    await configureCalculateForm(option.value, '3');
    const domPreview = await readDomPreview();
    await screenshot('step2-preview');
    report.steps.push({ step: 'configure', domPreview });
    const seen = checkPreviewAgainstOracle(domPreview, 3, 'STEP 2');
    record('step-2-configure-oracle-preview', { previewRows: seen });
    await closeCalculateEditor();
  }

  // STEP 3 CANCEL byte-stability against the post-prelude baseline (form was cancelled; only the
  // row-preserving prelude legitimately mutated the draft before this point).
  {
    const baseline = postPreludeBaseline;
    const after = await mainCtx.call('builder');
    const same = JSON.stringify(after.value.workspace) === JSON.stringify(baseline.workspace)
      && after.value.draftVersion === baseline.draftVersion && after.value.draftDigest === baseline.draftDigest;
    save('step3-cancel-after.json', after.value);
    report.steps.push({ step: 'cancel', sameDoc: same, before: { dv: baseline.draftVersion, dd: baseline.draftDigest }, after: { dv: after.value.draftVersion, dd: after.value.draftDigest } });
    assert.ok(same, `step 3 CANCEL changed doc: ${after.value.draftDigest}`);
    record('step-3-cancel-byte-stable', { sameDoc: same, draftDigest: after.value.draftDigest });
  }

  // STEP 4 APPLY -> persisted DERIVE step + signed unselected source projection + exact rows.
  {
    await navigate();
    await openCalculateEditor();
    await setSelect('[data-testid="construction-calculate-editor"] select[aria-label="First value kind"]', 'column');
    await until(() => mounted('[data-testid="construction-calculate-left-column"]'), 'left column select', 15000);
    const option = await until(() => readSignedLeftOption(), 'signed valueQuantity.value option present', 20000);
    await configureCalculateForm(option.value, '3');
    await until(() => mounted('[data-testid="construction-apply-proposal"]'), 'apply visible', 20000);
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      if (await deriveStep()) break;
      await wait(300);
    }
    await wait(400);
    const doc = await builderDoc();
    save('step4-doc-after-apply.json', { steps: doc.steps, sourceProjections: doc.sourceProjections });
    const step = (doc.steps || []).find((s) => s.operation?.kind === 'DERIVE');
    assert.ok(step, 'APPLY: no persisted DERIVE step');
    const derive = step.operation.derive;
    assert.ok(derive && derive.operation === 'ADD' && derive.missingInputPolicy === 'PROPAGATE_NULL',
      `APPLY: persisted derive contract wrong: ${JSON.stringify(derive)}`);
    assert.equal(derive.left?.kind, 'COLUMN', 'APPLY: left operand must be the signed column');
    assert.equal(derive.right?.kind, 'LITERAL', 'APPLY: right operand must be the literal');
    const projection = (doc.sourceProjections || []).find((p) => p.fieldPath === 'valueQuantity.value');
    assert.ok(projection && projection.columnId && projection.columnId.length > 1,
      `APPLY: signed source projection not persisted: ${JSON.stringify(doc.sourceProjections)}`);
    assert.equal(projection.ownerStepId, step.id, `APPLY: projection owner ${projection.ownerStepId} != derive step ${step.id}`);
    const selectedColumns = (doc.steps || []).flatMap((s) => s.outputs || []).map((o) => o.id);
    assert.ok(selectedColumns.includes(projection.columnId), 'APPLY: the compiled step must carry the signed slot output');
    assert.equal(derive.left.columnId, projection.columnId, `APPLY: left operand ${derive.left.columnId} != projection ${projection.columnId}`);
    const main = await evaluate(`(()=>{const t=document.querySelector('[data-testid="preview-table-content"]');if(!t)return null;const h=[...t.querySelectorAll('[role=columnheader]')].map(c=>c.textContent.trim());const rs=[...t.querySelectorAll('[role=row]')].map(r=>[...r.querySelectorAll('[role=cell]')].map(c=>c.textContent.trim()));return{h,rs};})()`);
    await screenshot('step4-after-apply');
    const preview = { headers: main?.h || [], rows: main?.rs || [] };
    // The signed slot column header is present though the field was never selected; its data
    // materialization is asserted exactly in the scoped cm grid (small deterministic page).
    assert.ok((main?.h || []).some((header) => /valueQuantity\.value/i.test(header)),
      `APPLY: signed slot column header absent: ${JSON.stringify(main?.h)}`);
    const seen = checkPreviewAgainstOracle(preview, 3, 'STEP 4 APPLY');
    report.steps.push({ step: 'apply', steps: (doc.steps || []).map((s) => s.operation?.kind),
      sourceProjections: (doc.sourceProjections || []).map((p) => ({ col: p.columnId, owner: p.ownerStepId, path: p.fieldPath, fhir: p.fhirType })), previewDOM: main });
    await screenshot('step4-preview-post-apply');
    if (await mounted('[data-testid="construction-close-operation-editor"]')) await realClick('[data-testid="construction-close-operation-editor"]');
    record('step-4-apply-exact-rows', { previewRows: seen, projectionColumnId: projection.columnId, projectionOwner: projection.ownerStepId });
    report.deriveStepId = step.id;
  }

  // STEP 4 RELOAD -> replacement banner + capability echo columnId equals the persisted projection.
  {
    await navigate();
    const doc = await builderDoc();
    const step = (doc.steps || []).find((s) => s.operation?.kind === 'DERIVE');
    const projection = (doc.sourceProjections || []).find((p) => p.fieldPath === 'valueQuantity.value');
    const historyIds = await historyIdsOf();
    assert.ok(historyIds.includes(step.id) || historyIds.length >= 1, `RELOAD: history step ${step.id} not listed: ${JSON.stringify(historyIds)}`);
    await openEditorFromHistory(() => mounted('[data-testid="construction-calculate-editor"]'));
    await until(() => mounted('[data-testid="construction-calculate-replacement"]'), 'replacement banner', 15000);
    let reloadCaps = null;
    let reloadEchoReady = false;
    const reloadStageKeys = [(step.inputs || []).find((input) => input.kind === 'STEP_OUTPUT')?.stepId, 'source_projection']
      .filter((stageId, index, all) => stageId && all.indexOf(stageId) === index);
    for (let i = 0; i < 12 && !reloadEchoReady; i += 1) {
      const builder = (await mainCtx.call('builder')).value;
      for (const stageId of reloadStageKeys) {
        const caps = await mainCtx.call('construction-capabilities', {
          snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
          expectedDraftDigest: builder.draftDigest, outputId: builder.workspace.documents[0].output.id, stageId,
        });
        const dsi = caps.status === 200 ? caps.value?.deriveSourceInput : null;
        if (dsi && !reloadCaps) reloadCaps = dsi;
        if ((dsi?.choices || []).some((choice) => choice.fieldPath === 'valueQuantity.value' && choice.columnId)) {
          reloadCaps = dsi;
          reloadEchoReady = true;
          break;
        }
      }
      if (!reloadEchoReady) await wait(200);
    }
    const echo = reloadCaps?.choices?.find((choice) => choice.fieldPath === 'valueQuantity.value');
    assert.ok(echo && echo.columnId && echo.columnId.length > 1, `RELOAD capability echo: valueQuantity.value columnId not materialized; persisted col=${projection?.columnId}`);
    assert.equal(echo.columnId, projection.columnId, `RELOAD: capability echo columnId ${echo.columnId} != persisted source projection ${projection.columnId}`);
    report.steps.push({ step: 'reload', persistedProjectionCol: projection.columnId, echoColumnId: echo.columnId });
    await screenshot('step4-reload-editor');
    await closeCalculateEditor();
    record('step-4-reload-materialized-echo', { echoColumnId: echo.columnId, persistedProjectionCol: projection.columnId });
  }

  // STEP 4 EDIT: DERIVE literal 3 -> 2 (every visible derived cell becomes value + 2) -> back to
  // 3, each persisted. Row-identity and PROPAGATE_NULL blanks are re-validated on every page.
  {
    await navigate(); await wait(300);
    await openEditorFromHistory(() => mounted('[data-testid="construction-calculate-editor"] input[aria-label="Second value number"]'));
    await setInput('[data-testid="construction-calculate-editor"] input[aria-label="Second value number"]', '2');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'proposal ready literal two', 30000);
    await wait(300);
    const twoDom = await readDomPreview();
    const seenTwo = checkPreviewAgainstOracle(twoDom, 2, 'STEP 4 EDIT(literal-2)');
    await screenshot('step4-edit-literal-two');
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      const current = await deriveStep();
      const lit = current?.operation?.derive?.right?.literal;
      if (Number(lit?.integer ?? lit?.decimal) === 2) break;
      await wait(300);
    }
    const persistedTwo = (await deriveStep())?.operation?.derive?.right;
    assert.equal(Number(persistedTwo?.literal?.integer ?? persistedTwo?.literal?.decimal), 2, `EDIT: literal 2 not persisted: ${JSON.stringify(persistedTwo)}`);
    record('step-4-edit-literal-two', { previewRows: seenTwo, persistedLiteral: 2 });

    await navigate(); await wait(200);
    await openEditorFromHistory(() => mounted('[data-testid="construction-calculate-editor"] input[aria-label="Second value number"]'));
    await setInput('[data-testid="construction-calculate-editor"] input[aria-label="Second value number"]', '3');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'proposal ready back-to-three', 30000);
    await wait(300);
    const threeDom = await readDomPreview();
    const seenThree = checkPreviewAgainstOracle(threeDom, 3, 'STEP 4 EDIT(back-3)');
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      const current = await deriveStep();
      const lit = current?.operation?.derive?.right?.literal;
      if (Number(lit?.integer ?? lit?.decimal) === 3) break;
      await wait(300);
    }
    const persistedThree = (await deriveStep())?.operation?.derive?.right;
    assert.equal(Number(persistedThree?.literal?.integer ?? persistedThree?.literal?.decimal), 3, `EDIT: literal 3 not restored: ${JSON.stringify(persistedThree)}`);
    record('step-4-edit-back-three', { previewRows: seenThree, persistedLiteral: 3 });
  }

  // STEP 4 REMOVE -> derived column gone, no derive-owned projection left behind.
  {
    if (await mounted('[data-testid="construction-close-operation-editor"]')) { await realClick('[data-testid="construction-close-operation-editor"]'); await wait(200); }
    await navigate(); await wait(300);
    const docBefore = await builderDoc();
    const step = (docBefore.steps || []).find((s) => s.operation?.kind === 'DERIVE');
    assert.ok(step, 'REMOVE: no DERIVE step to remove');
    const historyIds = await evaluate(`(()=>[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(e=>e.getAttribute('data-testid').replace('construction-history-step-','')))()`);
    const target = historyIds.includes(step.id) ? step.id : historyIds[0];
    await clickHistoryStep(target);
    if (!(await mounted(`[data-testid="construction-remove-step-${target}"]`))) {
      await wait(1500);
      if (!(await mounted(`[data-testid="construction-remove-step-${target}"]`))) await clickHistoryStep(target);
    }
    await until(() => mounted(`[data-testid="construction-remove-step-${target}"]`), 'remove btn present after select', 15000);
    await realClick(`[data-testid="construction-remove-step-${target}"]`);
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'removal proposal ready', 30000);
    await screenshot('step4-remove-proposal');
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      const current = await deriveStep();
      if (!current) break;
      await wait(300);
    }
    const afterDoc = await builderDoc();
    await wait(400);
    const mainAfter = await evaluate(`(()=>{const t=document.querySelector('[data-testid="preview-table-content"]');if(!t)return null;const h=[...t.querySelectorAll('[role=columnheader]')].map(c=>c.textContent.trim());const rs=[...t.querySelectorAll('[role=row]')].map(r=>[...r.querySelectorAll('[role=cell]')].map(c=>c.textContent.trim()));return{h,rs};})()`);
    const afterIds = (mainAfter?.rs || []).flat().filter((cell) => /^dev-/.test(cell)).sort();
    await screenshot('step4-remove');
    report.steps.push({ step: 'remove', deriveStepsAfter: (afterDoc.steps || []).filter((s) => s.operation?.kind === 'DERIVE').length,
      afterRemoveIdentity: afterIds, sourceProjectionsAfter: (afterDoc.sourceProjections || []).map((p) => ({ p: p.fieldPath, o: p.ownerStepId })) });
    assert.equal((afterDoc.steps || []).filter((s) => s.operation?.kind === 'DERIVE').length, 0, 'REMOVE: DERIVE step still present');
    assert.ok(!(mainAfter?.h || []).some((header) => header.includes('Derived')), `REMOVE: derived column header still visible: ${JSON.stringify(mainAfter?.h)}`);
    assertNonEmpty(afterIds, 'REMOVE post-preview rows');
    assert.ok(afterIds.every((id) => oracle.byId.has(id)), `REMOVE: unknown row ids after removal: ${JSON.stringify(afterIds.filter((id) => !oracle.byId.has(id)).slice(0, 5))}`);
    const leftovers = (afterDoc.sourceProjections || []).filter((p) => p.ownerStepId && p.fieldPath === 'valueQuantity.value');
    assert.equal(leftovers.length, 0, `REMOVE: leftover derive-owned valueQuantity.value source projection: ${JSON.stringify(leftovers)}`);
    record('step-4-remove-unowned', { afterRemoveIdentity: afterIds, unowned: leftovers.length === 0 });
  }

  // STEP 4 RE-ADD -> honest re-materialization (new derive-owned columnId, capability echo equal).
  {
    await navigate(); await wait(200);
    await openCalculateEditor();
    await setSelect('[data-testid="construction-calculate-editor"] select[aria-label="First value kind"]', 'column');
    await until(() => mounted('[data-testid="construction-calculate-left-column"]'), 'left column select', 15000);
    const option = await until(() => readSignedLeftOption(), 'signed valueQuantity.value option present', 20000);
    await configureCalculateForm(option.value, '3');
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      if (await deriveStep()) break;
      await wait(300);
    }
    await wait(400);
    const dom = await readDomPreview().catch(() => null) || await evaluate(`(()=>{const t=document.querySelector('[data-testid="preview-table-content"]');if(!t)return null;return{headers:[...t.querySelectorAll('[role=columnheader]')].map(c=>c.textContent.trim()),rows:[...t.querySelectorAll('[role=row]')].map(r=>[...r.querySelectorAll('[role=cell]')].map(c=>c.textContent.trim()))};})()`);
    await screenshot('step4-re-add');
    const seen = checkPreviewAgainstOracle(dom, 3, 'RE-ADD');
    const construction = (await mainCtx.call('builder')).value.workspace.documents[0].construction;
    const step = (construction.steps || []).find((s) => s.operation?.kind === 'DERIVE');
    const projection = (construction.sourceProjections || []).find((p) => p.fieldPath === 'valueQuantity.value');
    assert.ok(step, 'RE-ADD: no DERIVE step persisted after re-add');
    assert.ok(projection && projection.columnId && projection.columnId.length > 1, `RE-ADD: signed projection not re-materialized: ${JSON.stringify(projection)}`);
    assert.equal(projection.ownerStepId, step.id, `RE-ADD: projection owner ${projection.ownerStepId} != derive step id ${step.id}`);
    const echo = await deriveSourceEchoCaps(step);
    assert.ok(echo && echo.columnId && echo.columnId.length > 1, `RE-ADD: capability echo did not materialize a columnId (got ${echo?.columnId}; persisted ${projection.columnId})`);
    assert.equal(echo.columnId, projection.columnId, `RE-ADD: capability echo valueQuantity.value columnId ${echo.columnId} != re-materialized ${projection.columnId}`);
    record('step-4-re-add-honest-rematerialization', { deriveStepId: step.id, projCol: projection.columnId, projOwner: projection.ownerStepId, capEcho: echo.columnId, previewRows: seen });
  }

  // STEP 5 SCOPED GRID: a second saved FILTER (valueQuantity.unit EQUALS cm) deterministically
  // reduces to the two cm Observations, so the derived column must show exactly oracle value + 3
  // for both rows with matching identity — the arithmetic leg at exact-scope precision.
  {
    if (await mounted('[data-testid="construction-close-operation-editor"]')) { await realClick('[data-testid="construction-close-operation-editor"]'); await wait(200); }
    await navigate(); await wait(200);
    await realClick('[data-testid="construction-action-keep-rows"]');
    await until(() => mounted('[data-testid="construction-filter-editor"]'), 'filter editor for scoped grid', 20000);
    const unitOption = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]');if(!s)return null;const og=s.querySelector('optgroup[label="Dataset fields (not in output)"]');if(!og)return null;const o=[...og.querySelectorAll('option')].find(o=>o.getAttribute('title')==='valueQuantity.unit');return o?o.value:null;})()`);
    assert.ok(unitOption, 'valueQuantity.unit signed option must be offered for the scoped grid leg');
    await setSelect('[data-testid="construction-filter-editor"] select[aria-label="Column"]', unitOption);
    await until(() => mounted('[data-testid="construction-filter-editor"] select[aria-label="Condition"]'), 'condition select', 15000);
    await setSelect('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EQUALS');
    await until(() => mounted('[data-testid="construction-filter-editor"] input[aria-label="Value"]'), 'value input', 15000);
    await setInput('[data-testid="construction-filter-editor"] input[aria-label="Value"]', 'cm');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'scoped proposal ready', 30000);
    await wait(300);
    const cmDom = await readDomPreview();
    const cmSeen = checkPreviewAgainstOracle(cmDom, 3, 'STEP 5 SCOPED-cm', oracle.cmRows.map((row) => row.id));
    await screenshot('step5-scoped-cm');
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      const doc = await builderDoc();
      const filters = (doc.steps || []).filter((s) => s.operation?.kind === 'FILTER');
      if (filters.some((s) => s.operation?.filter?.values?.[0]?.string === 'cm')) break;
      await wait(300);
    }
    await wait(400);
    const table = await evaluate(`(()=>{const t=document.querySelector('[data-testid="preview-table-content"]');if(!t)return null;const h=[...t.querySelectorAll('[role=columnheader]')].map(c=>c.textContent.trim());const rs=[...t.querySelectorAll('[role=row]')].map(r=>[...r.querySelectorAll('[role=cell]')].map(c=>c.textContent.trim()));return{h,rs};})()`);
    const cmTableSeen = checkPreviewAgainstOracle({ headers: table?.h || [], rows: table?.rs || [] }, 3, 'STEP 5 SCOPED-cm-table', oracle.cmRows.map((row) => row.id));
    // Exact-scope materialization of the unselected signed field: its slot column carries the
    // raw valueQuantity.value data (never user-selected; only the server-owned projection knows it).
    const slotHeaderIndex = (table?.h || []).findIndex((header) => /valueQuantity\.value/i.test(header));
    assert.ok(slotHeaderIndex >= 0, `SCOPED-cm: signed slot column absent from scoped table: ${JSON.stringify(table?.h)}`);
    (table?.rs || []).filter((row) => row.some((cell) => /^dev-/.test(cell))).forEach((row, rowIndex) => {
      const oracleRow = oracle.byId.get((row.find((cell) => /^dev-/.test(cell)) || '').trim());
      const cell = (row[slotHeaderIndex] ?? '').trim();
      assert.ok(oracleRow && Math.abs(Number.parseFloat(cell) - oracleRow.value) < 1e-6,
        `SCOPED-cm row ${rowIndex}: signed slot cell ${JSON.stringify(cell)} != raw valueQuantity.value ${oracleRow?.value}`);
    });
    await screenshot('step5-scoped-cm-table');
    if (await mounted('[data-testid="construction-close-operation-editor"]')) await realClick('[data-testid="construction-close-operation-editor"]');
    record('step-5-scoped-cm-grid', { previewRows: cmSeen, tableRows: cmTableSeen });
    assert.ok(evidence.blankCells > 0 && evidence.numericCells > 0,
      `aggregate row evidence: blanks=${evidence.blankCells} numerics=${evidence.numericCells} (both must be > 0 across the run)`);
    record('row-evidence-blanks-and-numerics', { blankCells: evidence.blankCells, numericCells: evidence.numericCells });
  }

  // ---- API negatives against the deployed binary, on a pristine proposal host ----
  await runApiNegatives(negCtx, staleCtx);

  // Post-run build identity: source and running binary must be unchanged across the run.
  const stampAfter = collectBuildIdentity();
  report.buildIdentity.after = stampAfter;
  save('build-identity.after.json', stampAfter);
  assert.equal(stampAfter.gitHead, stampBefore.gitHead, `git HEAD moved during run (${stampBefore.gitHead} -> ${stampAfter.gitHead})`);
  assert.equal(stampAfter.runningBinary, stampBefore.runningBinary, `serving binary changed during run (${stampBefore.runningBinary} -> ${stampAfter.runningBinary})`);
  assert.equal(stampAfter.liveSource, stampBefore.liveSource, `serving source digest changed during run (${stampBefore.liveSource} -> ${stampAfter.liveSource})`);

  assert.deepEqual(exceptions, [], `uncaught page exceptions: ${JSON.stringify(exceptions)}`);

  // Exit-code contract: every named check must have run and passed.
  const expectedChecks = [
    'step-0-behavior-capabilities', 'step-1-prelude-filter-step', 'step-1-discovery', 'step-2-configure-oracle-preview', 'step-3-cancel-byte-stable',
    'step-4-apply-exact-rows', 'step-4-reload-materialized-echo', 'step-4-edit-literal-two', 'step-4-edit-back-three',
    'step-4-remove-unowned', 'step-4-re-add-honest-rematerialization', 'step-5-scoped-cm-grid', 'row-evidence-blanks-and-numerics',
    'negative-D0-control-valid-choice', 'negative-D1-forged-signature', 'negative-D2-stale-choiceId', 'negative-D3-tampered-payload-path',
    'negative-D4-empty-choiceId', 'negative-D5-missing-choiceId', 'negative-D6-three-selections', 'negative-D7-duplicate',
    'negative-D8-column-mismatch', 'negative-D9-nonnumeric-choice-refusal',
    'contract-D10-non-derive-step-undecorated',
  ];
  const present = new Set(report.checks.filter((entry) => entry.status === 'passed').map((entry) => entry.name));
  const skipped = expectedChecks.filter((name) => !present.has(name));
  assert.equal(skipped.length, 0, `skipped checks (each assertion must run): ${JSON.stringify(skipped)}`);

  // Evidence floor: every leg must have produced its screenshot; silently dropped captures fail.
  const EXPECTED_SCREENSHOT_FLOOR = 11;
  assert.ok(report.screenshots.length >= EXPECTED_SCREENSHOT_FLOOR,
    `screenshot tally below the per-leg floor: wrote ${report.screenshots.length}, expected at least ${EXPECTED_SCREENSHOT_FLOOR}`);
  assert.equal(new Set(report.screenshots).size, report.screenshots.length, `duplicate screenshot names written: ${JSON.stringify(report.screenshots)}`);

  report.status = 'passed';
  save('journey.json', report);
  console.log(`JOURNEY_OK build=${stampBefore.gitHead} runningBinary=${stampBefore.runningBinary} checks=${report.checks.length} screenshots=${report.screenshots.length}`);
} catch (error) {
  report.status = 'failed';
  report.error = error instanceof Error ? error.message : String(error);
  try { save('journey.json', report); } catch { /* best effort */ }
  console.error(`JOURNEY_FAILED ${report.error}`);
  process.exitCode = 1;
} finally {
  try { if (client) await client.send('Browser.close'); } catch { /* best effort */ }
  if (chrome && chrome.exitCode === null && chrome.signalCode === null) {
    chrome.kill('SIGKILL');
  }
}

function assertNonEmpty(values, label) {
  assert.ok(Array.isArray(values) && values.length > 0, `${label}: zero rows matched, expected at least one oracle row`);
}

async function runApiNegatives(host, staleCtx) {
  const swapSignature = (token) => {
    const segments = token.split('.');
    const signature = segments[segments.length - 1];
    const last = signature[signature.length - 1];
    segments[segments.length - 1] = signature.slice(0, -1) + (last === 'a' ? 'b' : 'a');
    return segments.join('.');
  };
  const tamperPayload = (token, mutate) => {
    const [prefix, payload, signature] = token.split('.');
    const decoded = JSON.parse(Buffer.from(payload, 'base64url').toString());
    mutate(decoded);
    return `${prefix}.${Buffer.from(JSON.stringify(decoded)).toString('base64url')}.${signature}`;
  };
  const embeddedSnapshot = (token) => {
    try { return JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString()).snapshotToken ?? null; } catch { return null; }
  };
  const capabilities = async (ctx) => {
    const builder = (await ctx.call('builder')).value;
    const stepCount = (builder.workspace?.documents?.[0]?.construction?.steps || []).length;
    assert.equal(stepCount, 0, 'the negatives host must be a pristine proposal host so candidateConstruction references only its new step');
    const caps = await ctx.call('construction-capabilities', {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, outputId: builder.workspace.documents[0].output.id, stageId: 'source_projection',
    });
    assert.equal(caps.status, 200, 'capabilities must be 200 for the negatives control');
    return { builder, choices: caps.value.deriveSourceInput?.choices || [] };
  };
  const hostCaps = await capabilities(host);
  const numericChoice = hostCaps.choices.find((choice) => choice.fieldPath === 'valueQuantity.value');
  assert.ok(numericChoice, 'valueQuantity.value choice must exist on the negatives host');

  // Provably-stale choiceId: a valid signed token whose embedded snapshot comes from another
  // fixture, so it is stale relative to the proposal host (the cross-snapshot 409 negative).
  const staleCaps = await capabilities(staleCtx);
  const staleNumeric = staleCaps.choices.find((choice) => choice.fieldPath === 'valueQuantity.value');
  assert.ok(staleNumeric, 'valueQuantity.value choice must exist on the cross-snapshot fixture');
  const staleCandidate = staleNumeric.choiceId;
  assert.notEqual(embeddedSnapshot(staleCandidate), hostCaps.builder.catalog.snapshotToken,
    'cross-fixture choiceId must embed a snapshot distinct from the proposal host (provably stale)');

  const uuid = () => `${Date.now().toString(16)}-${Math.random().toString(16).slice(2, 8)}`;
  const proposalRequest = (selections, mutateStep) => {
    const stepId = `derive_neg_${uuid()}`;
    const colId = `derive-input_${uuid()}`;
    const outCol = `derived_${uuid()}`;
    const step = {
      id: stepId, inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'DERIVE',
        derive: {
          constructionId: stepId, outputColumnId: outCol, operation: 'ADD',
          left: { kind: 'COLUMN', columnId: colId },
          right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 3 } },
          missingInputPolicy: 'PROPAGATE_NULL',
        },
      },
      outputs: [{ id: colId, name: 'vq_value', label: 'Value', type: 'decimal' }, { id: outCol, name: 'derived_total', label: 'Derived total', type: 'integer' }],
    };
    mutateStep?.(step, colId);
    return {
      snapshotToken: hostCaps.builder.catalog.snapshotToken, expectedDraftVersion: hostCaps.builder.draftVersion,
      expectedDraftDigest: hostCaps.builder.draftDigest, outputId: hostCaps.builder.workspace.documents[0].output.id,
      changedStepId: stepId, candidateConstruction: { version: 1, steps: [step] }, limit: 25,
      deriveSources: selections.map((choiceId) => ({ choiceId, columnId: colId })),
    };
  };
  const expectCase = async (name, body, expected, options = {}) => {
    const response = await host.call('construction-proposals', body);
    const accepted = response.status === 200;
    const pass = expected === '200' ? accepted : (!accepted && String(response.status) === String(expected));
    report.negatives.push({ name, expected, status: response.status, accepted, bodyExcerpt: JSON.stringify(response.value).slice(0, 300) });
    assert.ok(pass, `${name}: expected ${expected}, got ${response.status} ${JSON.stringify(response.value).slice(0, 160)}`);
    if (options.diagnosticCode) {
      const encoded = JSON.stringify(response.value);
      assert.ok(encoded.includes(options.diagnosticCode), `${name}: response missing diagnostic code ${options.diagnosticCode}`);
      record(`negative-${name}`, { expected, status: response.status, diagnosticCode: options.diagnosticCode });
    } else if (options.record !== false) {
      record(`negative-${name}`, { expected, status: response.status });
    }
    return response;
  };

  const control = await expectCase('D0-control-valid-choice', proposalRequest([numericChoice.choiceId]), '200', { record: false });
  assert.ok(JSON.stringify(control.value).includes('"valueQuantity.value"'), 'D0: valid choice must decorate the candidate with the signed fieldPath');
  record('negative-D0-control-valid-choice', { expected: '200', status: 200, decorated: true, columnId: numericChoice.columnId ?? null });

  await expectCase('D1-forged-signature', proposalRequest([swapSignature(numericChoice.choiceId)]), '409', { diagnosticCode: 'INVALID_DERIVE_SOURCE_CHOICE' });
  await expectCase('D2-stale-choiceId', proposalRequest([staleCandidate]), '409');
  await expectCase('D3-tampered-payload-path', proposalRequest([tamperPayload(numericChoice.choiceId, (obj) => { obj.path = 'manually_tampered_path'; })]), '409');
  await expectCase('D4-empty-choiceId', proposalRequest(['']), '400', { diagnosticCode: 'MALFORMED_REQUEST' });
  {
    const body = proposalRequest([numericChoice.choiceId]);
    delete body.deriveSources[0].choiceId;
    await expectCase('D5-missing-choiceId', body, '400', { diagnosticCode: 'MALFORMED_REQUEST' });
  }
  await expectCase('D6-three-selections', proposalRequest([numericChoice.choiceId, numericChoice.choiceId, numericChoice.choiceId]), '400', { diagnosticCode: 'MALFORMED_REQUEST' });
  await expectCase('D7-duplicate', proposalRequest([numericChoice.choiceId, numericChoice.choiceId]), '400', { diagnosticCode: 'MALFORMED_REQUEST' });
  {
    const body = proposalRequest([numericChoice.choiceId]);
    body.deriveSources[0].columnId = 'derive-input_someone_elses_col';
    await expectCase('D8-column-mismatch', body, '422', { diagnosticCode: 'DERIVE_SOURCE_COLUMN_MISMATCH' });
  }
  {
    // Non-numeric refusal, LIVE-observable (pinned 422 UNSUPPORTED_DERIVE_SOURCE_FIELD): row-choice
    // tokens are signed in one domain, so a genuinely-signed filter-lane token for a non-numeric
    // field (status/unit/id/effectiveDateTime) is a valid signature on the DERIVE channel. The
    // numeric admission guard must refuse it.
    const builder = (await host.call('builder')).value;
    const caps = await host.call('construction-capabilities', {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, outputId: builder.workspace.documents[0].output.id, stageId: 'source_projection',
    });
    const nonNumericPaths = new Set((caps.value.deriveSourceInput?.choices || []).map((choice) => choice.fieldPath));
    const nonNumericChoice = (caps.value.filterSourceInput?.choices || [])
      .find((choice) => !nonNumericPaths.has(choice.fieldPath) && !['integer', 'decimal'].includes(choice.logicalType));
    assert.ok(nonNumericChoice, `signed non-numeric filter token source for the DERIVE refusal must exist on the host; signed: ${JSON.stringify((caps.value.filterSourceInput?.choices || []).map((choice) => choice.fieldPath))}`);
    await expectCase('D9-nonnumeric-choice-refusal', proposalRequest([nonNumericChoice.choiceId]), '422', { diagnosticCode: 'UNSUPPORTED_DERIVE_SOURCE_FIELD' });
  }
  {
    // Contract behaviour (live-pinned): deriveSources attached to a NON-DERIVE changed step are
    // ignored (200) instead of refused; the guard rail that must hold is that the signed field is
    // never decorated into the ignored candidate. Any decoration here would be a source leak.
    const response = await host.call('construction-proposals', proposalRequest([numericChoice.choiceId], (step) => {
      step.operation = { kind: 'FILTER', filter: { columnId: 'derive-input_x', operator: 'EQUALS', values: [{ kind: 'STRING', string: 'x' }] } };
    }));
    report.negatives.push({ name: 'D10-non-derive-step', expected: '200-undecorated', status: response.status });
    assert.equal(response.status, 200, `D10: live contract is ignore-not-refuse for non-DERIVE steps, got ${response.status}`);
    assert.ok(!JSON.stringify(response.value).includes('"valueQuantity.value"'), 'D10: ignored deriveSources must never decorate the signed fieldPath');
    record('contract-D10-non-derive-step-undecorated', { status: response.status, undecorated: true });
  }
}
