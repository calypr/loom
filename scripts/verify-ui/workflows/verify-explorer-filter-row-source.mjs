#!/usr/bin/env node
// ROW-SOURCE-01 §18 Filter row-source journey — committed repeatable verification.
// Standalone raw-CDP browser journey against the owned loom-dev stack, following the
// scripts/verify-ui/workflows raw-CDP convention (see core-protocol-browser.mjs): a
// directly-runnable node script, not a Playwright registry entry.
//
// Legs (assertions carried over verbatim from the authoritative journey-run v3 report):
//   1 DISCOVERY   optgroup "Dataset fields (not in output)" exposes the unselected gender field
//   2 CONFIGURE   native picker -> EQUALS female -> proposal READY -> DOM preview == oracle
//   3 CANCEL      byte-stable pre-proposal construction
//   4 APPLY       persisted FILTER step + materialized gender source projection + exact rows
//   4 RELOAD      capability echo gender columnId == persisted source projection (materialized echo)
//   4 EDIT        value -> male => 0 rows; value -> female => oracle rows; persisted
//   4 REMOVE      filter steps 1->0, rows return to unfiltered, gender projection unowned
//   4 RE-ADD      honest re-materialization: new filter-owned columnId, capability echo equal
//   API negatives 7/7 (valid control, forged sig 409, cross-snapshot stale 409, tampered path 409,
//                     empty/absent choiceId 400, unpopulated field never offered/never accepted)
//
// Exit-code contract (kept from the raw harness): every named check must run AND pass. A skipped
// (missing) check or a thrown assertion is a failure; an empty identity where rows are required
// is a failure. Any failure exits 1.
//
// Reproduce (owned stack env identifies the target, per loom-dev conventions):
//   LOOM_DEV_SOURCE_ROOT=<repo> LOOM_DEV_COMPOSE_PROJECT=loom-dev-stravinsky \
//   LOOM_DEV_API_PORT=8182 LOOM_DEV_UI_PORT=3182 \
//   node scripts/verify-ui/workflows/verify-explorer-filter-row-source.mjs --out <FRESH_DIR>
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { CDPClient } from '../../browser/cdp.mjs';
import { browserURL } from './builder-url.mjs';
import { AUTHORING_SEMANTICS_VERSION, commandEnvironment, createDevSession, createFreshVerificationFixture } from '../../loom-dev.mjs';

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

const runStem = options.runLabel ?? `rsrc${randomUUID().replace(/-/g, '').slice(0, 8)}`;
assert.match(runStem, /^[a-z0-9][a-z0-9-]{0,17}$/, '--run-label must be a short lowercase slug leaving room for two fixture suffixes');
const mainLabel = `${runStem}-a`;
const negLabel = `${runStem}-b`;
const staleLabel = `${runStem}-c`;

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
  const root = builder.catalog.nodes.find((node) => node.rowRootEligible && node.resourceType === 'Patient').nodeId;
  const idCandidate = builder.catalog.candidates.find((candidate) => candidate.nodeId === root
    && String(candidate.fieldPath || '').replace(/^root\./, '') === 'id');
  const commands = async (commandId, list, state) => call('commands', {
    commandId,
    semanticsVersion: AUTHORING_SEMANTICS_VERSION,
    snapshotToken: state.catalog.snapshotToken,
    expectedDraftVersion: state.draftVersion,
    ...(state.draftDigest ? { expectedDraftDigest: state.draftDigest } : {}),
    commands: list,
  });
  let response = await commands(`${runStem}-table`, [{ type: 'CREATE_TABLE', title: 'RSO filter row source', rootNodeId: root }], builder);
  assert.equal(response.status, 200, `seed CREATE_TABLE failed (${response.status}): ${JSON.stringify(response.value).slice(0, 400)}`);
  builder = (await call('builder')).value;
  const outputId = builder.workspace.documents[0].output.id;
  response = await commands(`${runStem}-id`, [{
    type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: idCandidate.candidateId,
    projectionMode: idCandidate.defaultProjectionMode, initialPresentation: 'TABLE', title: 'ID',
  }], builder);
  assert.equal(response.status, 200, `seed ADD_COLUMN failed (${response.status}): ${JSON.stringify(response.value).slice(0, 400)}`);
  return { project, explorer, base, call };
};

const report = {
  status: 'running',
  schemaVersion: 1,
  mode: 'raw-cdp-filter-row-source-journey',
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

// Independent oracle computed straight from the pinned source fixture.
const computeOracle = () => {
  const patients = readFileSync(join(session.fixtureDir, 'Patient.ndjson'), 'utf8')
    .split(/\r?\n/).filter((line) => line.trim()).map((line) => JSON.parse(line));
  const equalFemale = patients.filter((patient) => patient.gender === 'female').map((patient) => patient.id).sort();
  const missingGender = patients.filter((patient) => patient.gender === undefined).map((patient) => patient.id).sort();
  assert.equal(equalFemale.length, 1, 'Fixture oracle expects exactly one female Patient');
  assert.equal(missingGender.length, 1, 'Fixture oracle expects exactly one Patient with no gender');
  return { equalFemale, missingGender, all: patients.map((patient) => patient.id).sort() };
};

// Docker build-stamp identity, using loom-dev.mjs's own command environment so the scoped bridge
// accepts the exec (service loom-api, /workspace stamp script, --check), same as waitForFreshBuild.
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

const profile = mkdtempSync(join(tmpdir(), 'rsrc-prof-'));
const chrome = spawn(chromePath, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--remote-debugging-port=0',
  `--user-data-dir=${profile}`, '--window-size=1600,1200', '--no-first-run', '--no-default-browser-check',
  '--disable-background-networking', '--disable-component-update', '--disable-sync', '--proxy-server=direct://', 'about:blank'],
  { stdio: ['ignore', 'ignore', 'pipe'] });
let chromeErr = '';
chrome.stderr.on('data', (chunk) => { chromeErr = (chromeErr + chunk).slice(-4000); });

const wait = (ms) => new Promise((done) => setTimeout(done, ms));
let mainCtx = null;
let client = null;
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

  // Build identity BEFORE the run (pin the deployed build). Barrier must PASS, never certify on a
  // stale or unreadable identity (mirrors the derive script's gate).
  const stampBefore = collectBuildIdentity();
  assert.equal(stampBefore.checkExit, 0, `serving build identity must be current before the journey runs: ${JSON.stringify(stampBefore)}`);
  assert.ok(stampBefore.runningBinary && stampBefore.liveSource && stampBefore.expectedSource && stampBefore.gitHead,
    `build identity must be fully recorded (runningBinary/liveSource/expectedSource/gitHead non-null): ${JSON.stringify(stampBefore)}`);
  requireRecordedIdentity(stampBefore, 'pre-run:');
  report.buildIdentity = { before: stampBefore, after: null };
  save('build-identity.before.json', stampBefore);

  // Independent oracle.
  const oracle = computeOracle();
  report.oracle = oracle;

  // Self-seed three independent fixtures: A carries the raw-CDP UI journey, B is a pristine
  // zero-step proposal host for the API negatives (candidateConstruction references a single new
  // step, which a project with a persisted step would reject), and C supplies a valid signed
  // gender choice whose embedded snapshot differs from B (the provably-stale choiceId, matching
  // the v3 cross-snapshot negative).
  const mainFixture = await createFreshVerificationFixture(session, mainLabel, { populateBootstrap: false });
  const negFixture = await createFreshVerificationFixture(session, negLabel, { populateBootstrap: false });
  const staleFixture = await createFreshVerificationFixture(session, staleLabel, { populateBootstrap: false });
  mainCtx = await seed(mainFixture);
  const negCtx = await seed(negFixture);
  const staleCtx = await seed(staleFixture);
  const baselineResp = await mainCtx.call('builder');
  const baseline = baselineResp.value;
  save('baseline-builder.json', baseline);
  report.fixture = {
    journeyProject: mainCtx.project, explorer: mainCtx.explorer,
    negativesProject: negCtx.project, staleProject: staleCtx.project,
    baselineDraftVersion: baseline.draftVersion, baselineDraftDigest: baseline.draftDigest,
  };

  await Promise.all([send('Page.enable'), send('Runtime.enable')]);

  const openKeepRowsEditorAndPick = async (fieldPath, value) => {
    await realClick('[data-testid="construction-action-keep-rows"]');
    await until(() => mounted('[data-testid="construction-filter-editor"]'), 'filter-editor mounted', 20000);
    const info = await evaluate(`(()=>{
      const s=document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]');
      if(!s)return null;
      const og=s.querySelector('optgroup[label="Dataset fields (not in output)"]');
      if(!og)return null;
      const opt=[...og.querySelectorAll('option')].find(o=>o.getAttribute('title')===${JSON.stringify(fieldPath)});
      return opt?{value:opt.value,label:opt.textContent.trim(),title:opt.getAttribute('title')}:null;
    })()`);
    assert.ok(info && info.value, `${fieldPath} option not present in dataset-field optgroup`);
    const picked = { fieldPath, wantedValue: value, sourceToken: info.value };
    const committed = await setSelect('[data-testid="construction-filter-editor"] select[aria-label="Column"]', info.value);
    assert.ok(committed && committed !== 'missing' && committed.length > 0, `picking ${fieldPath} produced no committed column value: ${JSON.stringify(committed)}`);
    picked.committedColumn = committed;
    await until(() => mounted('[data-testid="construction-filter-editor"] select[aria-label="Condition"]'), 'condition select', 15000);
    const conditionOptions = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]');if(!s)return null;return [...s.querySelectorAll('option')].map(o=>({value:o.value,label:o.textContent.trim()}));})()`);
    picked.conditionOptions = conditionOptions;
    const equality = (conditionOptions || []).find((option) => /^equals?$/i.test(option.value) || /^equals?$/i.test(option.label))
      || (conditionOptions || []).find((option) => /(equal|equals)/i.test(option.value) || /(equal|equals)/i.test(option.label));
    assert.ok(equality, `no equality condition option present; conditionOptions=${JSON.stringify(conditionOptions)}`);
    await setSelect('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', equality.value);
    picked.conditionChosen = equality;
    await until(() => mounted('[data-testid="construction-filter-editor"] input[aria-label="Value"]'), 'value input', 15000);
    const setBack = await setInput('[data-testid="construction-filter-editor"] input[aria-label="Value"]', value);
    assert.equal(setBack, value, 'value input set failed');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'proposal ready', 30000);
    // Post-selection read-back (the silent-vacuous half of this helper): every read above happens in
    // the same turn as our own write, so an app-side reset or an option that never bound could satisfy
    // the whole helper. Re-read what the picker actually holds once it announces READY; zero matches
    // (no selected option) is a failure, not a pass.
    const bound = await evaluate(`(()=>{
      const s=document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]');
      const c=document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Condition"]');
      const v=document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]');
      if(!s||!c||!v)return null;
      const opt=s.selectedIndex>=0&&s.options?s.options[s.selectedIndex]:null;
      const copt=c.selectedIndex>=0&&c.options?c.options[c.selectedIndex]:null;
      return opt?{colTitle:opt.getAttribute('title'),colValue:s.value,condValue:copt?copt.value:null,condLabel:copt?copt.textContent.trim():null,inputValue:v.value}:null;
    })()`);
    assert.ok(bound && bound.colTitle, `${fieldPath} picker read-back: no column option is selected after the editor announced READY (zero matches = the pick never bound): ${JSON.stringify(bound)}`);
    assert.equal(bound.colTitle, fieldPath, `picker: the selected column option is not ${fieldPath} after the app settled: ${JSON.stringify(bound)}`);
    assert.equal(bound.colValue, info.value, `picker: the column select drifted from the picked token ${info.value}: ${JSON.stringify(bound)}`);
    assert.ok(bound.condValue && /(equal|equals)/i.test(`${bound.condValue} ${bound.condLabel || ''}`), `picker: no equality condition is actually selected after the app settled: ${JSON.stringify(bound)}`);
    assert.equal(bound.inputValue, value, `picker: the value input no longer holds ${value} after the app settled: ${JSON.stringify(bound)}`);
    picked.boundReadBack = bound;
    picked.proposalReady = true;
    report.steps.push({ step: 'picker', ...picked });
    await wait(400);
  };
  // Server-truth doc read. It deliberately does NOT default the construction object to {}: an absent
  // construction turned every `(doc.steps || []).filter(...)` census below into an empty scan that
  // its own `length === 0` assert then certified as success (the mut-H2 silent-vacuous channel).
  const builderDoc = async () => {
    const builder = (await mainCtx.call('builder')).value;
    const doc = builder?.workspace?.documents?.[0]?.construction;
    assert.ok(doc && typeof doc === 'object' && !Array.isArray(doc),
      `server-truth read returned no construction object for document 0; a census over it would pass vacuously: keys=${JSON.stringify(Object.keys(builder?.workspace?.documents?.[0] ?? {}))} doc=${JSON.stringify(builder?.workspace?.documents?.[0] ?? null).slice(0, 300)}`);
    return doc;
  };

  // STEP 0 behavior: capabilities expose the filterSourceInput gender choice.
  await navigate();
  {
    const builder = (await mainCtx.call('builder')).value;
    const caps = await mainCtx.call('construction-capabilities', {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, outputId: builder.workspace.documents[0].output.id, stageId: 'source_projection',
    });
    const fsi = caps.value.filterSourceInput;
    assert.ok(caps.status === 200 && fsi && fsi.supported && fsi.choices.find((choice) => choice.fieldPath === 'gender'),
      `step 0 filterSourceInput missing gender on capabilities (status ${caps.status})`);
    report.steps.push({ step: 'behave', status: caps.status, supported: fsi.supported, choicesPaths: fsi.choices.map((c) => c.fieldPath),
      genderColumnIdBefore: fsi.choices.find((c) => c.fieldPath === 'gender')?.columnId ?? null });
    record('step-0-behavior-capabilities', { status: caps.status, supported: fsi.supported, choicesPaths: fsi.choices.map((c) => c.fieldPath) });
  }

  // STEP 1 DISCOVERY
  {
    await realClick('[data-testid="construction-action-keep-rows"]');
    await until(() => mounted('[data-testid="construction-filter-editor"]'), 'filter editor', 20000);
    const discovery = await evaluate(`(()=>{
      const s=document.querySelector('[data-testid="construction-filter-editor"] select[aria-label="Column"]');
      if(!s)return {present:false};
      const main=[...s.querySelectorAll(':scope > option')].map(o=>({value:o.value,label:o.textContent.trim(),title:o.getAttribute('title')}));
      const og=s.querySelector('optgroup[label="Dataset fields (not in output)"]');
      const opts=og?[...og.querySelectorAll('option')].map(o=>({value:o.value,label:o.textContent.trim(),title:o.getAttribute('title')})):null;
      const gender=opts?opts.find(o=>o.title==='gender'):null;
      return {present:true,mainOptions:main,optgroupLabel:og?.getAttribute('label')??null,optgroupOptions:opts,genderOption:gender??null};
    })()`);
    save('step1-discovery-select.json', discovery);
    await screenshot('step1-discovery');
    assert.ok(discovery.present && discovery.optgroupLabel === 'Dataset fields (not in output)', `missing optgroup label ${discovery.optgroupLabel}`);
    assert.ok(discovery.genderOption && discovery.genderOption.title === 'gender', `gender option tooltip missing title: ${JSON.stringify(discovery.genderOption)}`);
    assert.ok(/patient/i.test(discovery.genderOption.label) && /gender/i.test(discovery.genderOption.label), `gender option label does not name Patient gender field: ${discovery.genderOption.label}`);
    record('step-1-discovery', { optgroupLabel: discovery.optgroupLabel, genderTooltip: discovery.genderOption.title, genderLabel: discovery.genderOption.label });
    await realClick('[data-testid="construction-close-operation-editor"]');
    await until(() => evaluate(`!document.querySelector('[data-testid="construction-filter-editor"]')`), 'filter editor closed', 10000);
  }

  // STEP 2 CONFIGURE + oracle-exact preview, then CANCEL
  {
    await openKeepRowsEditorAndPick('gender', 'female');
    const domPreview = await readDomPreview();
    await screenshot('step2-preview');
    report.steps.push({ step: 'configure', domPreview });
    const domIdentity = (domPreview?.rows || []).map((row) => row.find((cell) => /^dev-p/.test(cell))).filter(Boolean).sort();
    assert.deepEqual(domIdentity, oracle.equalFemale, `step 2 UI preview identity mismatch: ${JSON.stringify(domIdentity)}`);
    await screenshot('step2-preview-final');
    if (await mounted('[data-testid="construction-cancel-proposal"]')) {
      await realClick('[data-testid="construction-cancel-proposal"]');
      await wait(200);
    }
    if (await mounted('[data-testid="construction-close-operation-editor"]')) {
      await realClick('[data-testid="construction-close-operation-editor"]');
    }
    await until(() => evaluate(`!document.querySelector('[data-testid="construction-filter-editor"]')`), 'editor closed after cancel', 10000);
    record('step-2-configure-oracle-preview', { previewIdentity: domIdentity, oracle: oracle.equalFemale });
  }

  // STEP 3 CANCEL byte-stability
  {
    const after = await mainCtx.call('builder');
    const same = JSON.stringify(after.value.workspace) === JSON.stringify(baseline.workspace)
      && after.value.draftVersion === baseline.draftVersion && after.value.draftDigest === baseline.draftDigest;
    save('step3-cancel-after.json', after.value);
    report.steps.push({ step: 'cancel', sameDoc: same, before: { dv: baseline.draftVersion, dd: baseline.draftDigest }, after: { dv: after.value.draftVersion, dd: after.value.draftDigest } });
    assert.ok(same, `step 3 CANCEL changed doc: ${after.value.draftDigest}`);
    record('step-3-cancel-byte-stable', { sameDoc: same, draftDigest: after.value.draftDigest });
  }

  // STEP 4 APPLY -> persisted doc + exact rows
  {
    await navigate();
    await openKeepRowsEditorAndPick('gender', 'female');
    await until(() => mounted('[data-testid="construction-apply-proposal"]'), 'apply visible', 20000);
    await realClick('[data-testid="construction-apply-proposal"]');
    let applyObserved = false;
    for (let i = 0; i < 25; i += 1) {
      const builder = (await mainCtx.call('builder')).value;
      const steps = builder.workspace.documents[0].construction?.steps || [];
      if (steps.some((step) => step.operation?.kind === 'FILTER')) { applyObserved = true; break; }
      await wait(300);
    }
    assert.ok(applyObserved, `step4 APPLY: the persisted doc never showed a FILTER step within ${25 * 300}ms; falling through an unobserved persistence is a failure`);
    await wait(400);
    const doc = await builderDoc();
    save('step4-doc-after-apply.json', { steps: doc.steps, sourceProjections: doc.sourceProjections });
    const main = await evaluate(`(()=>{const t=document.querySelector('[data-testid="preview-table-content"]');if(!t)return null;const h=[...t.querySelectorAll('[role=columnheader]')].map(c=>c.textContent.trim());const rs=[...t.querySelectorAll('[role=row]')].map(r=>[...r.querySelectorAll('[role=cell]')].map(c=>c.textContent.trim()));return{h,rs};})()`);
    await screenshot('step4-after-apply');
    const identity = (main?.rs || []).flat().filter((cell) => /^dev-p/.test(cell)).sort();
    report.steps.push({ step: 'apply', steps: (doc.steps || []).map((s) => s.operation?.kind),
      sourceProjections: (doc.sourceProjections || []).map((p) => ({ col: p.columnId, owner: p.ownerStepId, path: p.fieldPath, fhir: p.fhirType })), previewDOM: main });
    assertNonEmpty(identity, 'step4 post-apply preview');
    assert.deepEqual(identity, oracle.equalFemale, `step4 post-apply preview identity: ${JSON.stringify(identity)}`);
    const appliedGenderProjection = (doc.sourceProjections || []).find((projection) => projection.fieldPath === 'gender');
    assert.ok(appliedGenderProjection && appliedGenderProjection.columnId && appliedGenderProjection.columnId.length > 1 && appliedGenderProjection.ownerStepId,
      `step4 APPLY: zero matches for a filter-owned gender source projection over ${(doc.sourceProjections || []).length} projection(s); an empty census must fail, not certify the materialization claim: ${JSON.stringify(doc.sourceProjections || [])}`);
    await screenshot('step4-preview-post-apply');
    if (await mounted('[data-testid="construction-close-operation-editor"]')) await realClick('[data-testid="construction-close-operation-editor"]');
    record('step-4-apply-exact-rows', { identity, sourceProjections: (doc.sourceProjections || []).map((p) => ({ col: p.columnId, path: p.fieldPath })) });
  }

  // STEP 4 RELOAD -> materialized server-echo columnId equals the persisted source projection
  {
    await navigate();
    await realClick('[data-testid="construction-action-keep-rows"]');
    await until(() => mounted('[data-testid="construction-filter-editor"]'), 'filter editor post-reload', 20000);
    const saved = await evaluate(`(()=>{const c=document.querySelector('[data-testid="construction-filter-condition-tree"]');return c?{text:c.innerText,rows:[...c.querySelectorAll('[data-testid^="construction-filter-edit-"]')].map(b=>b.getAttribute('data-testid'))}:null;})()`);
    await screenshot('step4-reload-saved-conditions');
    let reloadCaps = null;
    for (let i = 0; i < 12; i += 1) {
      const builder = (await mainCtx.call('builder')).value;
      const caps = await mainCtx.call('construction-capabilities', {
        snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
        expectedDraftDigest: builder.draftDigest, outputId: builder.workspace.documents[0].output.id, stageId: 'source_projection',
      });
      if (caps.status === 200 && caps.value?.filterSourceInput) { reloadCaps = caps.value.filterSourceInput; break; }
      await wait(200);
    }
    const genderEcho = reloadCaps?.choices?.find((choice) => choice.fieldPath === 'gender');
    const persistedGenderCol = (((await mainCtx.call('builder')).value.workspace?.documents?.[0]?.construction?.sourceProjections) || [])
      .find((projection) => projection.fieldPath === 'gender')?.columnId || '';
    assert.ok(saved && saved.rows.length >= 1, `reload saved-condition tree missing rows: ${JSON.stringify(saved)}`);
    assert.ok(genderEcho && genderEcho.columnId && genderEcho.columnId.length > 1, `RELOAD capability echo: gender columnId not materialized; persisted col=${persistedGenderCol}`);
    assert.equal(genderEcho.columnId, persistedGenderCol, `RELOAD: capability echo columnId ${genderEcho.columnId} != persisted source projection ${persistedGenderCol}`);
    report.steps.push({ step: 'reload', persistedGenderCol, genderColumnId: genderEcho.columnId });
    await screenshot('step4-reload-editor');
    await realClick('[data-testid="construction-close-operation-editor"]');
    await until(() => evaluate(`!document.querySelector('[data-testid="construction-filter-editor"]')`), 'editor closed after reload view', 10000);
    record('step-4-reload-materialized-echo', { genderColumnId: genderEcho.columnId, persistedGenderCol });
  }

  // STEP 4 EDIT -> male => 0 rows, female => oracle rows, both persisted (server-truth assert on
  // the persisted FILTER value, mirroring the derive script's persisted-literal gate).
  {
    await navigate(); await wait(300);
    const persistedFilterValue = async () => {
      const doc = await builderDoc();
      return (doc.steps || []).find((s) => s.operation?.kind === 'FILTER')?.operation?.filter?.values?.[0]?.string ?? null;
    };
    const historyIds = await evaluate(`(()=>[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(e=>e.getAttribute('data-testid').replace('construction-history-step-','')))()`);
    assert.ok(historyIds.length >= 1, 'no construction-history-step-* present to select/edit/remove');
    let editStepId = historyIds[0];
    await realClick(`[data-testid="construction-history-step-${editStepId}"]`);
    await until(() => mounted(`[data-testid="construction-remove-step-${editStepId}"],[data-testid="construction-edit-step-${editStepId}"]`), 'step selected (remove/edit btn)', 15000);
    try {
      await until(() => mounted('[data-testid="construction-filter-editor"] input[aria-label="Value"]'), 'edit-mode value input after history select', 12000);
    } catch {
      await realClick('[data-testid="construction-action-keep-rows"]');
      await until(() => mounted('[data-testid="construction-filter-condition-tree"]'), 'saved tree visible in editor', 20000);
      const editBtn = `[data-testid="construction-filter-edit-${editStepId}"]`;
      await until(() => mounted(editBtn), `edit button ${editStepId}`, 10000);
      await realClick(editBtn);
      await until(() => mounted('[data-testid="construction-filter-editor"] input[aria-label="Value"]'), 'edit value input (fallback)', 15000);
    }
    const loadedValue = await evaluate(`document.querySelector('[data-testid="construction-filter-editor"] input[aria-label="Value"]')?.value`);
    report.steps.push({ step: 'edit-opened', loadedValue });
    await setInput('[data-testid="construction-filter-editor"] input[aria-label="Value"]', 'male');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'proposal ready after edit', 30000);
    await wait(300);
    const dom = await readDomPreview();
    await screenshot('step4-edit-male');
    const maleIds = (dom?.rows || []).flat().map((cell) => String(cell)).filter((cell) => /^dev-p/.test(cell));
    report.steps.push({ step: 'edit-male-preview', identity: maleIds });
    assert.deepEqual(maleIds, [], `EDIT: change value to male should yield 0 preview rows, got ${JSON.stringify(maleIds)}`);
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      const builder = (await mainCtx.call('builder')).value;
      const filter = (builder.workspace.documents[0].construction?.steps || []).find((s) => s.operation?.kind === 'FILTER')?.operation?.filter;
      if (filter?.values?.[0]?.string === 'male') break;
      await wait(300);
    }
    const persistedMale = (await builderDoc()).steps?.find((s) => s.operation?.kind === 'FILTER')?.operation?.filter;
    assert.equal(persistedMale?.values?.[0]?.string, 'male', `EDIT: 'male' was not persisted to the saved FILTER step: ${JSON.stringify(persistedMale)}`);
    record('step-4-edit-male-zero-rows', { loadedValue, maleRowCount: maleIds.length, persistedValue: 'male' });
    await navigate(); await wait(200);
    {
      const reIds = await evaluate(`(()=>[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(e=>e.getAttribute('data-testid').replace('construction-history-step-','')))()`);
      if (reIds.length) editStepId = reIds[0];
      const opened = await mounted('[data-testid="construction-filter-editor"] input[aria-label="Value"]');
      if (!opened) {
        await realClick(`[data-testid="construction-history-step-${editStepId}"]`);
        await until(() => mounted('[data-testid="construction-filter-editor"] input[aria-label="Value"]'), 'value input after history select', 15000);
      }
    }
    await setInput('[data-testid="construction-filter-editor"] input[aria-label="Value"]', 'female');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'proposal ready back-to-female', 30000);
    await wait(300);
    const femaleDom = await readDomPreview();
    const backIds = (femaleDom?.rows || []).flat().filter((cell) => /^dev-p/.test(cell)).sort();
    assertNonEmpty(backIds, 'EDIT-back-to-female preview');
    assert.deepEqual(backIds, oracle.equalFemale, `EDIT-back-to-female preview identity mismatch: ${JSON.stringify(backIds)}`);
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      if (await persistedFilterValue() === 'female') break;
      await wait(300);
    }
    assert.equal(await persistedFilterValue(), 'female', `EDIT: 'female' was not restored in the persisted FILTER step (fire-and-forget apply is a failure)`);
    record('step-4-edit-back-female-rows', { identity: backIds, oracle: oracle.equalFemale, persistedFilterValue: 'female' });
  }

  // STEP 4 REMOVE -> rows return to unfiltered, gender projection unowned
  {
    if (await mounted('[data-testid="construction-close-operation-editor"]')) { await realClick('[data-testid="construction-close-operation-editor"]'); await wait(200); }
    await navigate(); await wait(300);
    // Zero-match discipline: both post-removal asserts below are `length === 0` claims over a filter
    // predicate, so they are only meaningful when the pre-removal census really contained the FILTER
    // step and its filter-owned gender projection. Observing the pre-state first turns "went to zero"
    // into a witnessed transition instead of an empty scan that certifies itself.
    const preRemoveDoc = await builderDoc();
    assert.ok((preRemoveDoc.steps || []).some((step) => step.operation?.kind === 'FILTER'),
      `REMOVE pre-remove census: no FILTER step in the persisted doc (${(preRemoveDoc.steps || []).length} step(s)); the post-removal zero asserts below would be vacuous: ${JSON.stringify((preRemoveDoc.steps || []).map((step) => step.operation?.kind))}`);
    assert.ok((preRemoveDoc.sourceProjections || []).some((projection) => projection.fieldPath === 'gender' && projection.ownerStepId),
      `REMOVE pre-remove census: no filter-owned gender projection in the persisted doc; "no leftover owned projection" must never be certified from a census that was empty to begin with: ${JSON.stringify((preRemoveDoc.sourceProjections || []).map((projection) => ({ path: projection.fieldPath, owner: projection.ownerStepId })))}`);
    const historyIds = await evaluate(`(()=>[...document.querySelectorAll('[data-testid^="construction-history-step-"]')].map(e=>e.getAttribute('data-testid').replace('construction-history-step-','')))()`);
    assert.ok(historyIds.length >= 1, `remove: no history step present; ids=${JSON.stringify(historyIds)}`);
    const editStepId = historyIds[0];
    await realClick(`[data-testid="construction-history-step-${editStepId}"]`);
    await until(() => mounted(`[data-testid="construction-remove-step-${editStepId}"]`), 'remove btn present after select', 15000);
    await realClick(`[data-testid="construction-remove-step-${editStepId}"]`);
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'removal proposal ready', 30000);
    await screenshot('step4-remove-proposal');
    await realClick('[data-testid="construction-apply-proposal"]');
    let removalObserved = false;
    for (let i = 0; i < 25; i += 1) {
      const builder = (await mainCtx.call('builder')).value;
      const hasFilter = (builder.workspace.documents[0].construction?.steps || []).some((s) => s.operation?.kind === 'FILTER');
      if (!hasFilter) { removalObserved = true; break; }
      await wait(300);
    }
    assert.ok(removalObserved, `REMOVE: the persisted doc never showed the FILTER step gone within ${25 * 300}ms; falling through an unobserved removal is a failure`);
    const afterDoc = await builderDoc();
    await wait(400);
    const mainAfter = await evaluate(`(()=>{const t=document.querySelector('[data-testid="preview-table-content"]');if(!t)return null;return [...t.querySelectorAll('[role=row]')].map(r=>[...r.querySelectorAll('[role=cell]')].map(c=>c.textContent.trim()));})()`);
    const afterRemoveIds = (mainAfter || []).flat().filter((cell) => /^dev-p/.test(cell)).sort();
    await screenshot('step4-remove');
    report.steps.push({ step: 'remove', filterStepsAfter: (afterDoc.steps || []).filter((s) => s.operation?.kind === 'FILTER').length, afterRemoveIdentity: afterRemoveIds,
      sourceProjectionsAfter: (afterDoc.sourceProjections || []).map((p) => ({ p: p.fieldPath, o: p.ownerStepId })) });
    assert.equal((afterDoc.steps || []).filter((s) => s.operation?.kind === 'FILTER').length, 0, 'REMOVE: FILTER step still present');
    assert.deepEqual(afterRemoveIds, oracle.all, `REMOVE: rows should return to unfiltered both; got ${JSON.stringify(afterRemoveIds)}`);
    const leftovers = (afterDoc.sourceProjections || []).filter((p) => p.ownerStepId && p.fieldPath === 'gender');
    assert.equal(leftovers.length, 0, `REMOVE: leftover filter-owned gender source projection: ${JSON.stringify(leftovers)}`);
    record('step-4-remove-unfiltered', { afterRemoveIdentity: afterRemoveIds, unowned: leftovers.length === 0 });
  }

  // STEP 4 RE-ADD -> honest re-materialization (new filter-owned columnId, capability echo equal)
  {
    await navigate(); await wait(200);
    await openKeepRowsEditorAndPick('gender', 'female');
    await realClick('[data-testid="construction-apply-proposal"]');
    let reAddObserved = false;
    for (let i = 0; i < 25; i += 1) {
      const builder = (await mainCtx.call('builder')).value;
      const value = (builder.workspace.documents[0].construction?.steps || []).find((s) => s.operation?.kind === 'FILTER')?.operation?.filter?.values?.[0]?.string;
      if (value === 'female') { reAddObserved = true; break; }
      await wait(300);
    }
    assert.ok(reAddObserved, `RE-ADD: the persisted FILTER value never became 'female' within ${25 * 300}ms; falling through an unobserved re-apply is a failure`);
    await wait(400);
    const dom = await readDomPreview() || await evaluate(`(()=>{const t=document.querySelector('[data-testid="preview-table-content"]');if(!t)return null;return{rows:[...t.querySelectorAll('[role=row]')].map(r=>[...r.querySelectorAll('[role=cell]')].map(c=>c.textContent.trim()))};})()`);
    const ids = (dom?.rows || []).flat().filter((cell) => /^dev-p/.test(cell)).sort();
    await screenshot('step4-readd');
    assertNonEmpty(ids, 'RE-ADD preview');
    assert.deepEqual(ids, oracle.equalFemale, `RE-ADD: preview row identity mismatch after re-add (${JSON.stringify(ids)})`);
    const construction = (await mainCtx.call('builder')).value.workspace.documents[0].construction;
    const filterStep = (construction.steps || []).find((s) => s.operation?.kind === 'FILTER');
    const projection = (construction.sourceProjections || []).find((p) => p.fieldPath === 'gender');
    report.steps.push({ step: 're-add', steps: (construction.steps || []).map((s) => ({ k: s.operation?.kind, filter: s.operation?.filter })),
      sourceProjections: (construction.sourceProjections || []).map((p) => ({ p: p.fieldPath, col: p.columnId, o: p.ownerStepId })) });
    assert.ok(filterStep, 'RE-ADD: no FILTER step persisted after re-add');
    assert.ok(projection && projection.columnId && projection.columnId.length > 1, `RE-ADD: gender source projection not re-materialized: ${JSON.stringify(projection)}`);
    assert.equal(projection.ownerStepId, filterStep.id, `RE-ADD: gender projection owner ${projection.ownerStepId} != filter step id ${filterStep.id}`);
    const bb = (await mainCtx.call('builder')).value;
    const cc = await mainCtx.call('construction-capabilities', {
      snapshotToken: bb.catalog.snapshotToken, expectedDraftVersion: bb.draftVersion, expectedDraftDigest: bb.draftDigest,
      outputId: bb.workspace.documents[0].output.id, stageId: 'source_projection',
    });
    const genderEcho = (cc.value?.filterSourceInput?.choices || []).find((choice) => choice.fieldPath === 'gender');
    assert.ok(genderEcho && genderEcho.columnId && genderEcho.columnId.length > 1 && genderEcho.columnId.startsWith('filter-input_'),
      `RE-ADD: capability echo did not materialize a filter-input columnId (got ${genderEcho?.columnId}; persisted ${projection.columnId})`);
    assert.equal(genderEcho.columnId, projection.columnId, `RE-ADD: capability echo gender columnId ${genderEcho.columnId} != re-materialized ${projection.columnId}`);
    record('step-4-re-add-honest-rematerialization', { filterStepId: filterStep.id, projCol: projection.columnId, projOwner: projection.ownerStepId, capEcho: genderEcho.columnId });
  }

  // ---- API negatives (7/7) against the deployed binary, on a pristine zero-step host ----
  await runApiNegatives(negCtx, staleCtx);

  // Post-run build identity: source and running binary must be unchanged across the run.
  const stampAfter = collectBuildIdentity();
  requireRecordedIdentity(stampAfter, 'post-run:');
  report.buildIdentity.after = stampAfter;
  save('build-identity.after.json', stampAfter);
  assert.equal(stampAfter.gitHead, stampBefore.gitHead, `git HEAD moved during run (${stampBefore.gitHead} -> ${stampAfter.gitHead})`);
  assert.equal(stampAfter.runningBinary, stampBefore.runningBinary, `serving binary changed during run (${stampBefore.runningBinary} -> ${stampAfter.runningBinary})`);
  assert.equal(stampAfter.liveSource, stampBefore.liveSource, `serving source digest changed during run (${stampBefore.liveSource} -> ${stampAfter.liveSource})`);

  assert.deepEqual(exceptions, [], `uncaught page exceptions: ${JSON.stringify(exceptions)}`);

  // Exit-code contract: every named check must have run and passed.
  const expectedChecks = [
    'step-0-behavior-capabilities', 'step-1-discovery', 'step-2-configure-oracle-preview', 'step-3-cancel-byte-stable',
    'step-4-apply-exact-rows', 'step-4-reload-materialized-echo', 'step-4-edit-male-zero-rows', 'step-4-edit-back-female-rows',
    'step-4-remove-unfiltered', 'step-4-re-add-honest-rematerialization',
    'negative-N0-control-valid-choice', 'negative-N1-forged-signature', 'negative-N2-stale-choiceId', 'negative-N3-tampered-payload-path',
    'negative-N4-empty-choiceId', 'negative-N5-missing-choiceId', 'negative-N6-unpopulated-field-refusal',
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

// Zero-match discipline for the build-stamp barrier: the stamp script only compares the recorded
// identity when its own --check runs, so a failed or truncated `cat` of the recorded stamp leaves
// recordedBinary/recordedSource null and the checkExit gate alone never notices. An empty identity
// census must FAIL the run rather than certify it (same discipline as assertNonEmpty above and the
// unpivot twin's "no empty running column" gate). Every floor that existed before stays in place.
function requireRecordedIdentity(stamp, when) {
  assert.ok(stamp.recordedBinary && stamp.recordedSource,
    `${when} recorded build identity is unreadable or empty (recordedBinary/recordedSource must be non-null): ${JSON.stringify(stamp)}`);
  assert.equal(stamp.recordedBinary, stamp.runningBinary,
    `${when} recorded binary ${stamp.recordedBinary} must equal the serving binary ${stamp.runningBinary}`);
  assert.equal(stamp.recordedSource, stamp.expectedSource,
    `${when} recorded source ${stamp.recordedSource} must equal the expected source digest ${stamp.expectedSource}`);
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
  // Decodes the `rc1.<payload>.<digest>` row-choice token (see capability/row_choice.go:257-278).
  // Without it, a token that fails to decode makes embeddedSnapshot() null, and `null !== hostToken`
  // would satisfy the staleness check by accident — the zero-match twin of the vacuous-pass class.
  const decodedPayload = (token) => {
    try { return JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString()); } catch { return null; }
  };
  const capabilities = async (ctx) => {
    const builder = (await ctx.call('builder')).value;
    const stepCount = (builder.workspace?.documents?.[0]?.construction?.steps || []).length;
    assert.equal(stepCount, 0, 'the negatives host must be a pristine zero-step fixture so candidateConstruction references only its new step');
    const caps = await ctx.call('construction-capabilities', {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, outputId: builder.workspace.documents[0].output.id, stageId: 'source_projection',
    });
    assert.equal(caps.status, 200, 'capabilities must be 200 for the negatives control');
    return { builder, choices: caps.value.filterSourceInput.choices };
  };
  const hostCaps = await capabilities(host);
  const genderChoice = hostCaps.choices.find((choice) => choice.fieldPath === 'gender');
  assert.ok(genderChoice, 'gender choice must exist on the negatives host');
  assert.ok(decodedPayload(genderChoice.choiceId), `the host gender choiceId must decode as a signed row-choice token, or every token-level negative below is unverifiable: ${String(genderChoice.choiceId).slice(0, 24)}`);
  assert.equal(embeddedSnapshot(genderChoice.choiceId), hostCaps.builder.catalog.snapshotToken,
    `the host gender choiceId must embed the host catalog snapshot (capability/row_choice.go:131 sets it from the minting snapshot); a null snapshot would satisfy the staleness comparison by accident: ${JSON.stringify({ embedded: embeddedSnapshot(genderChoice.choiceId), host: hostCaps.builder.catalog.snapshotToken })}`);
  const activeInCaps = hostCaps.choices.some((choice) => choice.fieldPath === 'active');

  // Provably-stale choiceId: a valid signed token whose embedded snapshot comes from another
  // fixture, so it is stale relative to the proposal host (the v3 cross-snapshot 409 negative).
  const staleCaps = await capabilities(staleCtx);
  const staleGender = staleCaps.choices.find((choice) => choice.fieldPath === 'gender');
  assert.ok(staleGender, 'gender choice must exist on the cross-snapshot fixture');
  const staleCandidate = staleGender.choiceId;
  assert.ok(decodedPayload(staleCandidate), `the cross-fixture gender choiceId must decode as a signed row-choice token: ${String(staleCandidate).slice(0, 24)}`);
  assert.notEqual(embeddedSnapshot(staleCandidate), hostCaps.builder.catalog.snapshotToken,
    'cross-fixture choiceId must embed a snapshot distinct from the proposal host (provably stale)');

  const uuid = () => `${Date.now().toString(16)}-${Math.random().toString(16).slice(2, 8)}`;
  const proposalRequest = async (choiceId) => {
    const builder = (await host.call('builder')).value;
    const stepId = `filter_neg_${uuid()}`;
    const colId = `filter-input_${uuid()}`;
    return {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest,
      outputId: builder.workspace.documents[0].output.id, changedStepId: stepId,
      candidateConstruction: { version: 1, steps: [{ id: stepId, inputs: [{ kind: 'SOURCE_PROJECTION' }],
        operation: { kind: 'FILTER', filter: { columnId: colId, operator: 'EQUALS', values: [{ kind: 'STRING', string: 'female' }] } },
        outputs: [{ id: 'source_neg', name: 'col_neg_id', label: 'ID', type: 'string' }, { id: colId, name: 'gender', label: 'Patient.gender', type: 'string' }] }] },
      limit: 25, filterSources: [{ choiceId, columnId: colId }],
    };
  };
  const expectCase = async (name, body, expected) => {
    const response = await host.call('construction-proposals', body);
    const accepted = response.status === 200;
    const pass = expected === '200' ? accepted : (!accepted && String(response.status) === String(expected));
    report.negatives.push({ name, expected, status: response.status, accepted, bodyExcerpt: JSON.stringify(response.value).slice(0, 300) });
    assert.ok(pass, `${name}: expected ${expected}, got ${response.status} ${JSON.stringify(response.value).slice(0, 160)}`);
    record(`negative-${name}`, { expected, status: response.status });
    return response;
  };

  await expectCase('N0-control-valid-choice', await proposalRequest(genderChoice.choiceId), '200');
  await expectCase('N1-forged-signature', await proposalRequest(swapSignature(genderChoice.choiceId)), '409');
  await expectCase('N2-stale-choiceId', await proposalRequest(staleCandidate), '409');
  const tamperedPathToken = tamperPayload(genderChoice.choiceId, (obj) => { obj.path = 'manually_tampered_path'; });
  assert.equal(decodedPayload(tamperedPathToken)?.path, 'manually_tampered_path', 'N3: the payload tamper must actually land in the re-encoded token; a silent no-op would still answer 409 for the wrong reason');
  await expectCase('N3-tampered-payload-path', await proposalRequest(tamperedPathToken), '409');
  await expectCase('N4-empty-choiceId', await proposalRequest(''), '400');
  {
    const body = await proposalRequest(genderChoice.choiceId);
    delete body.filterSources[0].choiceId;
    await expectCase('N5-missing-choiceId', body, '400');
  }
  {
    const forgedActive = tamperPayload(genderChoice.choiceId, (obj) => { obj.path = 'active'; });
    assert.equal(decodedPayload(forgedActive)?.path, 'active', 'N6: the forged unpopulated-field path must actually land in the re-encoded token');
    const response = await host.call('construction-proposals', await proposalRequest(forgedActive));
    report.negatives.push({ name: 'N6-unpopulated-field-refusal', expected: 'absent+refused', activeInCaps, status: response.status });
    assert.ok(!activeInCaps, 'active must never be offered in capabilities choices');
    assert.notEqual(response.status, 200, `forged unpopulated-field choice must never be accepted (got ${response.status})`);
    record('negative-N6-unpopulated-field-refusal', { activeInCaps, status: response.status });
  }
}
