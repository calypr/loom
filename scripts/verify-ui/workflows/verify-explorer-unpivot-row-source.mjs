#!/usr/bin/env node
// ROW-SOURCE-02 slice 2 UNPIVOT row-source journey — committed repeatable verification.
// Standalone raw-CDP browser journey against the owned loom-dev stack, following the
// scripts/verify-ui/workflows raw-CDP convention (siblings: core-protocol-browser.mjs,
// verify-explorer-filter-row-source.mjs, verify-explorer-derive-row-source.mjs).
//
// Legs (contract from internal/explorer/lifecycle/unpivot_source.go + the slice-2 UI lane):
//   0 BEHAVIOR     capabilities expose unpivotSourceInput with the signed populated root
//                  scalars valueQuantity.unit (string) AND effectiveDateTime (date_time, the
//                  compiler-whitelisted date family widened by the F-1 rework).
//   1 PRELUDE      persisted signed FILTER (status EQUALS final), row-preserving. A persisted
//                  row-preserving step is ALSO the carrier the melt ledger requires: the
//                  reconcile arm (explorer_output_reconcile.go) only finds declared signed
//                  melt inputs on steps strictly before the melt. Melt-first signed candidates
//                  are a server defect (500 CONSTRUCTION_RECEIPT_CONTRACT_INVALID; receipt
//                  /tmp/opencode/unpivot-journey-commit.md F-A, probe proof
//                  /tmp/opencode/unpivot/probe10.log + probe12.log B) — deliberately not pinned
//                  here; the named refusals fire before compilation so the negatives below stay
//                  pristine-host honest.
//   2 DISCOVERY    More actions -> Reshape entry enabled; intent "Reshape table" -> "Turn
//                  columns into rows" choice; melt-row editor exposes the family-consistent
//                  "Fields from your data" dataset optgroup (source:<choiceId> options with the
//                  fieldPath tooltip; testids per slice-2 receipt) and the add-source-field
//                  affordance.
//   3 CONFIGURE    melt = public ID + signed Observation.valueQuantity.unit + signed
//                  Observation.subject.reference (Add a field each, rebound off the sorted-first
//                  effectiveDateTime prebind) with explicit keys; PREVIEW is oracle-exact per
//                  melt row (PRESERVE keeps em-dash/blank cells for rows missing unit/subject;
//                  id melt cells exact). The signed date_time lane is NOT run through the UI:
//                  the shipped editor's reshapeScalarTypes gate refuses date_time synth columns
//                  so a bound date row never reaches proposal-ready (recorded defect F-B,
//                  ConstructionReshapeEditor.tsx:448 'datetime' vs capability 'date_time').
//   3 CANCEL       byte-stable against the post-prelude baseline.
//   5 APPLY        persisted UNPIVOT step + server-owned signed source projections owned by the
//                  melt step, compiled echo facts (fhirType string, logicalType string, server
//                  label), melt outputs Variable/Value; honestly NOT
//                  materialized: melt input columns never appear as displayable columns.
//   6 RELOAD       history click reopens the melt editor with saved dataset-field bindings
//                  restored; capability echo columnId == persisted projection columnId.
//   7 EDIT         rename key/value outputs + null-row policy DROP -> persisted; PREVIEW with
//                  DROP shows no blank melt cells.
//   8 EDIT         rebind the signed unit row to the public Number column, then uncheck it: the
//                  unit lane dissolves (binding dropped, input removed) and STAYS
//                  dissolved after apply (rs05 fourth-lane cleared-melt witness: only the
//                  subject.reference melt lane + the filter lane projections remain; the earlier
//                  UI remove+re-add legs repeat the clearing across applies).
//   9 RE-ADD       honest re-materialization: unit lane re-binds, new owned projection columnId,
//                  capability echo equal.
//  10 REMOVE      melt step removed; Variable/Value headers gone; no melt-owned projection
//                  left behind; the prelude filter lane is untouched.
//  API negatives 15/15 on a pristine proposal host with exact statuses + diagnostic codes
//                  (public-only control 200, forged 409 INVALID_UNPIVOT_SOURCE_CHOICE,
//                  cross-snapshot 409, tampered payload 409, empty/absent choiceId 400
//                  MALFORMED_REQUEST, duplicate 400, column mismatch 422
//                  UNPIVOT_SOURCE_COLUMN_MISMATCH, over-selection 422, kind-incompatible melt
//                  422 UNPIVOT_SOURCE_TYPES_INCOMPATIBLE, already-projected melt 422
//                  UNPIVOT_SOURCE_FIELD_ALREADY_PROJECTED (never a 500), payloadless UNPIVOT +
//                  sources 422 UNPIVOT_SOURCE_REQUIRES_UNPIVOT, non-UNPIVOT step 200-undecorated,
//                  new-unpersisted-carrier 409 STALE_STAGE_REFERENCE).
//
// Exit-code contract (kept from the siblings): every named check must run AND pass. A skipped
// (missing) check or a thrown assertion is a failure. Any failure exits 1.
//
// Reproduce (owned stack env identifies the target, per loom-dev conventions):
//   LOOM_DEV_SOURCE_ROOT=<repo> LOOM_DEV_COMPOSE_PROJECT=loom-dev-stravinsky \
//   LOOM_DEV_API_PORT=8182 LOOM_DEV_UI_PORT=3182 \
//   node scripts/verify-ui/workflows/verify-explorer-unpivot-row-source.mjs --out <FRESH_DIR>
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

const runStem = options.runLabel ?? `upsrc${randomUUID().replace(/-/g, '').slice(0, 8)}`;
assert.match(runStem, /^[a-z0-9][a-z0-9-]{0,16}$/, '--run-label must be a short lowercase slug leaving room for two fixture suffixes');
const mainLabel = `${runStem}-a`;
const negLabel = `${runStem}-b`;
const staleLabel = `${runStem}-c`;
const KEY_ID = 'u_id';
const KEY_UNIT = 'u_unit';
const KEY_SUB = 'u_sub';

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
  const candidateFor = (fieldPath) => builder.catalog.candidates.find((candidate) => candidate.nodeId === root
    && String(candidate.fieldPath || '').replace(/^root\./, '') === fieldPath);
  const commands = async (commandId, list, state) => call('commands', {
    commandId,
    semanticsVersion: AUTHORING_SEMANTICS_VERSION,
    snapshotToken: state.catalog.snapshotToken,
    expectedDraftVersion: state.draftVersion,
    ...(state.draftDigest ? { expectedDraftDigest: state.draftDigest } : {}),
    commands: list,
  });
  let response = await commands(`${runStem}-table`, [{ type: 'CREATE_TABLE', title: 'RSO unpivot row source', rootNodeId: root }], builder);
  assert.equal(response.status, 200, `seed CREATE_TABLE failed (${response.status}): ${JSON.stringify(response.value).slice(0, 400)}`);
  builder = (await call('builder')).value;
  const outputId = builder.workspace.documents[0].output.id;
  // 'ID' is the public melt input; 'Number' (valueInteger) is the kept numeric anchor column;
  // 'Date' (effectiveDateTime) is the unmelted date-family survival witness: since cd39eeaf the
  // signed lane hides the date family from discovery entirely and melt satisfiability refuses it
  // (operator decision #4), so it can only ever be a carried column here. The signed melt lanes
  // (valueQuantity.unit, subject.reference) are never projected columns: they enter only through
  // unpivotSources. (The stage UNPIVOT capability requires >=1 public scalar column on the melt
  // input stage, internal/dataframe/compiler/lower/recipe_construction.go:2245.)
  for (const [fieldPath, title] of [['id', 'ID'], ['valueInteger', 'Number'], ['effectiveDateTime', 'Date']]) {
    const candidate = candidateFor(fieldPath);
    assert.ok(candidate, `seed requires the ${fieldPath} candidate`);
    if (fieldPath === 'effectiveDateTime') {
      assert.equal(candidate.populated, true, 'the date-family survival witness field must be populated in the live dataset (non-vacuous witness)');
    }
    response = await commands(`${runStem}-${fieldPath}`, [{
      type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: candidate.candidateId,
      projectionMode: candidate.defaultProjectionMode, initialPresentation: 'TABLE', title,
    }], builder);
    assert.equal(response.status, 200, `seed ADD_COLUMN ${fieldPath} failed (${response.status}): ${JSON.stringify(response.value).slice(0, 400)}`);
    builder = (await call('builder')).value;
  }
  const cmd = async (commandId, list) => {
    const state = (await call('builder')).value;
    return commands(commandId, list, state);
  };
  return { project, explorer, base, call, cmd };
};

const report = {
  status: 'running',
  schemaVersion: 1,
  mode: 'raw-cdp-unpivot-row-source-journey',
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
// testdata/devloop-fixture ndjson plus the repo-generated J01 concept observations. The melt
// grids are validated cell-by-cell for every visible row against per-observation facts taken
// straight from the seeded data, so no compiler-internal value can silently match itself.
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
      subject: observation?.subject?.reference ?? null,
      valueInteger: observation?.valueInteger ?? null,
      effective: observation?.effectiveDateTime ?? null,
    }));
  const byId = new Map(rows.map((row) => [row.id, row]));
  assert.ok(rows.length >= 1006, `Fixture oracle expects at least six devloop + 1000 generated J01 observations, got ${rows.length}`);
  assert.ok(rows.every((row) => typeof row.id === 'string' && row.id.length > 0), 'every observation must have an id');
  assert.ok(rows.every((row) => row.status === 'final'), 'every fixture observation is status final (the prelude filter is row-preserving)');
  assert.ok(rows.some((row) => row.unit === 'cm'), 'Fixture oracle expects the two devloop cm Observations');
  const unitBlank = rows.filter((row) => row.unit === null);
  assert.ok(unitBlank.length >= 1, 'Fixture oracle expects rows without valueQuantity.unit for the PRESERVE blank-melt evidence');
  const unitPopulated = rows.filter((row) => row.unit !== null);
  assert.ok(unitPopulated.length >= 1, 'Fixture oracle expects rows with valueQuantity.unit for the exact-melt evidence');
  const subjectBlank = rows.filter((row) => row.subject === null);
  assert.ok(subjectBlank.length >= 1, 'Fixture oracle expects rows without subject.reference for the PRESERVE blank-melt evidence');
  const subjectPopulated = rows.filter((row) => row.subject !== null);
  assert.ok(subjectPopulated.length >= 1, 'Fixture oracle expects rows with subject.reference for the exact-melt evidence');
  const effectivePopulated = rows.filter((row) => typeof row.effective === 'string' && row.effective.length > 0);
  assert.ok(effectivePopulated.length >= 1, 'Fixture oracle expects rows with effectiveDateTime for the date-column survival leg');
  const effectiveValues = [...new Set(effectivePopulated.map((row) => row.effective))];
  // The signed melt lanes run through the UI on string-family choices only. Since cd39eeaf
  // the server outright refuses date-family signed melts (422 UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD)
  // and melt satisfiability fails closed on any date-family kind, so the only date witness here
  // is the unmelted public Date column. (Recorded history: UI gate F-B and melt-kind note F-D
  // were fixed in 0dd7be44; every active melt input still shares one tableScalarKind.)
  return { byId, rows, unitBlankCount: unitBlank.length, subjectBlankCount: subjectBlank.length, effectiveValues };
};

// Docker build-stamp identity, using loom-dev.mjs's own command environment so the scoped bridge
// accepts the exec (service loom-api, /workspace stamp script, --check), same as the siblings.
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

const profile = mkdtempSync(join(tmpdir(), 'upsrc-prof-'));
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
  const clickCheckboxByLabel = async (label) => {
    const selector = `input[aria-label=${JSON.stringify(label)}][type=checkbox]`;
    await until(() => mounted(selector), `checkbox ${label}`, 20000);
    await evaluate(`document.querySelector(${JSON.stringify(selector)}).scrollIntoView({block:'center'})`);
    await wait(60);
    const point = await until(() => bounding(selector), `visible checkbox ${label}`);
    const before = await evaluate(`document.querySelector(${JSON.stringify(selector)}).checked`);
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: point.x, y: point.y });
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    await until(() => evaluate(`document.querySelector(${JSON.stringify(selector)}).checked !== ${before}`), `checkbox ${label} toggled`, 15000);
  };
  const setSelect = (selector, value) => evaluate(`(()=>{const el=document.querySelector(${JSON.stringify(selector)});if(!el)return 'missing';const setter=Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype,'value').set;setter.call(el,${JSON.stringify(value)});el.dispatchEvent(new Event('change',{bubbles:true}));return el.value;})()`);
  const setInput = (selector, value) => evaluate(`(()=>{const el=document.querySelector(${JSON.stringify(selector)});if(!el)return 'missing';const setter=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;setter.call(el,${JSON.stringify(value)});el.dispatchEvent(new Event('input',{bubbles:true}));return el.value;})()`);
  const screenshot = async (name) => {
    const shot = await send('Page.captureScreenshot', { format: 'png' });
    const file = `${name}.png`;
    writeFileSync(join(out, file), Buffer.from(shot.data, 'base64'));
    report.screenshots.push(file);
  };
  const readDomPreview = async () => {
    // Preview pages are ordered by an opaque group key, so the handful of exactly-populated
    // fixture records never reach a 25- or 50-row page. Raise the row limit to the maximum so
    // every record (blank AND exact witnesses) is present, and poll until the refetch settles.
    const raw = () => evaluate(`(()=>{const t=document.querySelector('[data-testid="construction-proposal-preview"]')||document.querySelector('[data-testid="construction-preview"]')||document.querySelector('[data-testid="preview-table-content"]');if(!t)return null;const th=[...t.querySelectorAll('thead th, [role=columnheader]')].map(e=>e.textContent.trim());const trs=[...t.querySelectorAll('tbody tr,[role=row]')];const rows=trs.map(tr=>[...tr.querySelectorAll('td,[role=cell]')].map(td=>td.textContent.trim()));return{headers:th,rows};})()`);
    if (await mounted('select[aria-label="Preview row limit"]')) {
      const current = await evaluate(`document.querySelector('select[aria-label="Preview row limit"]')?.value || ''`);
      if (current !== '1000') {
        try { await setSelect('select[aria-label="Preview row limit"]', '1000'); } catch { /* control may be transient */ }
        for (let i = 0; i < 50; i += 1) {
          const probe = await raw();
          if (probe && probe.rows.length >= 900) break;
          await wait(300);
        }
      }
    }
    return raw();
  };
  const navigate = async () => {
    await send('Page.navigate', { url: browserURL({ uiUrl: uiOrigin }, mainCtx.project, mainCtx.explorer, 'builder') });
    await until(() => mounted('[data-testid="construction-workspace"]'), 'workspace mounted', 60000);
    await until(() => mounted('[data-testid="construction-action-keep-rows"]'), 'keep-rows action visible', 60000);
    await wait(300);
  };
  // Production entry (BuilderWorkspace RowDefinitionSettingsPanel): row-definition settings ->
  // Reshape-table intent -> "Stack columns" (construction-action-unpivot-rows), which mounts the
  // UnpivotEditor directly with form kind 'unpivot'.
  const openUnpivotEditor = async () => {
    const trigger = await until(() => evaluate(`(()=>{const e=document.querySelector('[data-testid="construction-rows-settings-trigger"]');return e?!e.disabled&&{aria:e.getAttribute('aria-label')||e.textContent.trim()}:null;})()`),
      'row-definition settings trigger present and enabled', 25000);
    if (!(await mounted('[data-testid="construction-row-intent-reshape"]'))) {
      await realClick('[data-testid="construction-rows-settings-trigger"]');
      await wait(150);
    }
    await until(() => mounted('[data-testid="construction-row-intent-reshape"]'), 'reshape intent present', 20000);
    return trigger;
  };
  const chooseUnpivotForm = async () => {
    if (!(await evaluate(`(()=>{const e=document.querySelector('[data-testid="construction-action-unpivot-rows"]');return !!e;})()`))) {
      await realClick('[data-testid="construction-row-intent-reshape"]');
      await wait(150);
    }
    const choice = await until(() => evaluate(`(()=>{const e=document.querySelector('[data-testid="construction-action-unpivot-rows"]');return e?!e.disabled&&{title:(e.getAttribute('title')||'').trim().slice(0,120)}:null;})()`),
      'Stack columns affordance present and enabled', 20000);
    await realClick('[data-testid="construction-action-unpivot-rows"]');
    await until(() => mounted('[data-testid="construction-reshape-unpivot"]'), 'unpivot editor mounted', 25000);
    return choice;
  };
  const closeUnpivotEditor = async () => {
    if (await mounted('[data-testid="construction-cancel-proposal"]')) { await realClick('[data-testid="construction-cancel-proposal"]'); await wait(200); }
    if (await mounted('[data-testid="construction-close-operation-editor"]')) await realClick('[data-testid="construction-close-operation-editor"]');
    await until(() => evaluate(`!document.querySelector('[data-testid="construction-reshape-unpivot"]')`), 'unpivot editor closed', 15000);
  };
  const meltCheckboxLabels = () => evaluate(`(()=>[...document.querySelectorAll('[data-testid="construction-reshape-unpivot"] input[type=checkbox][aria-label^="Unpivot "]')].map(e=>({label:e.getAttribute('aria-label').replace(/^Unpivot /,''),checked:e.checked})))()`);
  const openAdvanced = async () => {
    if (!(await mounted('[data-testid="construction-unpivot-advanced"][open]'))) {
      await realClick('[data-testid="construction-unpivot-advanced"] summary');
      await until(() => mounted('[data-testid="construction-unpivot-advanced"][open]'), 'advanced options open', 15000);
    }
  };
  const setInputKey = async (index, text) => evaluate(`(()=>{
    const sel=document.querySelector('[data-testid="construction-reshape-unpivot-input-${index}-column"]');
    if(!sel)return 'missing-select';
    const row=sel.closest('div');
    const inputs=[...row.querySelectorAll('input')].filter(e=>(e.getAttribute('aria-label')||'').startsWith('Unpivot key value '));
    if(inputs.length!==1)return 'key-inputs:'+inputs.length;
    const setter=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;
    setter.call(inputs[0],${JSON.stringify(text)});
    inputs[0].dispatchEvent(new Event('input',{bubbles:true}));
    return inputs[0].value;})()`);
  const setRowSource = async (index, sourceOptionValue) => {
    const committed = await setSelect(`[data-testid="construction-reshape-unpivot-input-${index}-column"]`, sourceOptionValue);
    assert.equal(committed, sourceOptionValue, `row ${index} must accept the dataset-field option ${sourceOptionValue}`);
  };
  // The add-source-field affordance pre-binds the first available choice; when that is the
  // date-family field the row binds through the editor's enriched stage (the option arrives
  // bound, excluded from the dataset optgroup), so identity is asserted via the row label and
  // completed with an explicit dataset-option rebind when the first choice differs.
  const bindDatasetRow = async (index, fieldPath, expectedLabel) => {
    await until(() => mounted(`[data-testid="construction-reshape-unpivot-input-${index}-column"]`), `dataset melt row ${index}`, 20000);
    const rows = await readRowDatasetOptions(index);
    assert.ok(rows && Array.isArray(rows.options), `row ${index} dataset optgroup must be readable`);
    const stillOffered = rows.options.find((option) => option.title === fieldPath);
    if (stillOffered) await setRowSource(index, stillOffered.value);
    await until(() => readRowLabel(index).then((label) => label === expectedLabel && label),
      `row ${index} bound to ${expectedLabel}`, 15000);
    const after = await readRowDatasetOptions(index);
    assert.ok(!after.options.some((option) => option.title === fieldPath),
      'a bound dataset choice must disappear from the row options (1:1 selection)');
  };
  const readRowLabel = (index) => evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-reshape-unpivot-input-${index}-column"]');if(!s)return null;const label=s.closest('label');const span=(label&&label.querySelector('span'))||s.previousElementSibling;return span?span.textContent.trim():null;})()`);
  const readRowDatasetOptions = (index) => evaluate(`(()=>{
    const s=document.querySelector('[data-testid="construction-reshape-unpivot-input-${index}-column"]');
    if(!s)return null;
    const og=s.querySelector('optgroup[label="Fields from your data"]');
    const ogMain=s.querySelector('optgroup[label="Fields in this table"]');
    return {mainOptions:ogMain?[...ogMain.querySelectorAll('option')].map(o=>({value:o.value,label:o.textContent.trim()})):[],
      options:og?[...og.querySelectorAll('option')].map(o=>({value:o.value,label:o.textContent.trim(),title:o.getAttribute('title'),testid:o.getAttribute('data-testid')})):null};})()`);
  // Server-truth doc read (bs-62-2 twin fix from verify-explorer-filter-row-source.mjs:329-338).
  // It deliberately does NOT default the construction object to {}: an absent construction turned
  // every `(doc.steps || []).filter(...)` census below into an empty scan that its own
  // `length === 0` claim then certified as success. Callers only reach this after at least one
  // construction proposal has been applied (authoringv2/commands.go:1447 sets Document.Construction
  // on APPLY_CONSTRUCTION_PROPOSAL; live-pinned: a seeded doc carries no construction key yet, so
  // every pre-first-proposal poll in this file keeps a tolerant read instead); swallowing its
  // absence into {} after that point could only mask a broken read.
  const builderDoc = async () => {
    const builder = (await mainCtx.call('builder')).value;
    const doc = builder?.workspace?.documents?.[0]?.construction;
    assert.ok(doc && typeof doc === 'object' && !Array.isArray(doc),
      `server-truth read returned no construction object for document 0; a census over it would pass vacuously: keys=${JSON.stringify(Object.keys(builder?.workspace?.documents?.[0] ?? {}))} doc=${JSON.stringify(builder?.workspace?.documents?.[0] ?? null).slice(0, 300)}`);
    return doc;
  };
  // The authored date-family survival witness: exactly one public column sourced from
  // effectiveDateTime. Returning a copy keeps deep-equality comparisons against the
  // pre-melt baseline meaningful across apply, reload, and melt removal.
  const dateWitnessDefinition = (builderValue) => {
    const columns = builderValue?.workspace?.documents?.[0]?.columns || [];
    const matches = columns.filter((column) => column?.source?.field?.path === 'effectiveDateTime');
    return matches.length === 1 ? { ...matches[0] } : null;
  };
  // The preview grid renders each column header as title+logical-type badge text
  // concatenated (live receipts: 'Numberinteger', 'Observation.statusstring',
  // 'Datedate_time'). The Date witness header is matched by its exact title plus the
  // optional date-family suffix only — never a loose /date/i scan that could admit
  // unrelated columns into the survival assertion.
  const isDateWitnessHeader = (header) => /^Date(?:date_time|date)?$/i.test((header ?? '').trim());
  const isDateWitnessCheckbox = (box) => /^Date(?:date_time|date)?$/i.test(String(box.label ?? ''));
  const meltStep = async () => {
    const doc = await builderDoc();
    return (doc.steps || []).find((step) => step.operation?.kind === 'UNPIVOT');
  };
  const meltProjections = async () => (((await builderDoc()).sourceProjections || []).filter((p) => (p.ownerStepId || '').startsWith('unpivot_')));
  // The melt lane is only "cleared" when NO melt-owned projection for that field remains; the
  // prelude filter lane keeps its own status projection as a distinct rs05 lane witness.
  const filterLaneProjection = async () => (((await builderDoc()).sourceProjections || []).filter((p) => p.fieldPath === 'status'));

  // Oracle: every visible melt page row is checked against the seeded dataset. A melt page shows
  // (rows-per-record) cells per source row; the visible Number anchor identifies each group.
  const evidence = { blankMeltCells: 0, exactMeltCells: 0, idMeltCells: 0, dateSurvivalCells: 0 };
  const checkMeltPreview = (preview, label, activeKeys) => {
    assert.ok(preview && preview.headers?.length >= 3, `${label}: melt preview has no columns`);
    const varIndex = preview.headers.findIndex((header) => /variable|melt name/i.test(header));
    const valIndex = preview.headers.findIndex((header) => /value|melted/i.test(header));
    const numIndex = preview.headers.findIndex((header) => /Number/i.test(header));
    assert.ok(varIndex >= 0 && valIndex >= 0 && numIndex >= 0, `${label}: melt headers ${JSON.stringify(preview.headers)}`);
    const dateIndex = preview.headers.findIndex(isDateWitnessHeader);
    assert.ok(dateIndex >= 0, `${label}: the Date survival-witness column must be carried through the melt: ${JSON.stringify(preview.headers)}`);
    const rows = preview.rows.filter((row) => row.length >= preview.headers.length);
    assertNonEmpty(rows, `${label} melt preview rows`);
    // Coverage is measured in complete melt groups (each record appears once per active key);
    // page truncation only ever affects the trailing partial group.
    const groupCounts = new Map();
    for (const row of rows) {
      const anchor = (row[numIndex] ?? '').trim();
      groupCounts.set(anchor, (groupCounts.get(anchor) || 0) + 1);
    }
    const completeGroups = [...groupCounts.values()].filter((count) => count >= activeKeys.length).length;
    assert.ok(rows.length >= 15 && completeGroups >= 5,
      `${label}: melt preview needs >= 15 rows with >= 5 complete groups, got ${rows.length} rows / ${completeGroups} groups`);
    // Record blocks render contiguously and each block starts with its KEY_ID row (pinned by
    // the probe preview). Numeric anchors collide across records (generated observations share
    // valueInteger values), so attribution must be positional inside a block, never by the
    // anchor value alone.
    const blank = (cell) => { const text = (cell ?? '').trim(); return text === '' || text === '\u2014'; };
    const blocks = [];
    let current = null;
    for (const row of rows) {
      if (row[varIndex] === KEY_ID) { current = [row]; blocks.push({ rows: current, anchored: true }); continue; }
      if (current && current.length < activeKeys.length) { current.push(row); continue; }
      current = null;
      if (blocks.length && !blocks.at(-1).anchored) blocks.at(-1).rows.push(row);
      else blocks.push({ rows: [row], anchored: false });
    }
    const attributedRows = blocks.filter((block) => block.anchored && block.rows.length >= activeKeys.length).reduce((sum, block) => sum + block.rows.length, 0);
    assert.ok(attributedRows >= 4 * activeKeys.length,
      `${label}: expected at least 4 fully attributed record blocks, got ${attributedRows / activeKeys.length | 0}`);
    const unitDomain = new Set(oracle.rows.map((entry) => entry.unit).filter((v) => v !== null).map((v) => String(v)));
    const subjectDomain = new Set(oracle.rows.map((entry) => entry.subject).filter((v) => v !== null).map((v) => String(v)));
    for (const block of blocks) {
      const idRow = block.rows.find((candidate) => candidate[varIndex] === KEY_ID);
      const source = block.anchored && idRow ? oracle.byId.get((idRow[valIndex] ?? '').trim()) : null;
      const complete = block.anchored && block.rows.length >= activeKeys.length
        && new Set(block.rows.map((candidate) => candidate[varIndex])).size === activeKeys.length;
      for (const row of block.rows) {
        const variable = row[varIndex];
        const valueCell = (row[valIndex] ?? '').trim();
        const numberCell = (row[numIndex] ?? '').trim();
        assert.ok(activeKeys.includes(variable), `${label}: record block has unexpected melt key ${JSON.stringify(variable)} (expected one of ${JSON.stringify(activeKeys)})`);
        if (!blank(numberCell)) {
          assert.ok(Number.isFinite(Number.parseFloat(numberCell)), `${label}: Number anchor ${JSON.stringify(numberCell)} is neither blank nor numeric`);
          if (source) assert.equal(Number.parseFloat(numberCell), source.valueInteger, `${label}: Number anchor ${numberCell} != oracle valueInteger ${source.valueInteger}`);
        }
        const dateCell = (row[dateIndex] ?? '').trim();
        if (!blank(dateCell)) {
          if (source && source.effective != null) {
            assert.equal(dateCell, String(source.effective), `${label}: Date survival cell ${JSON.stringify(dateCell)} != oracle effectiveDateTime ${source.effective} — the string melt mutated the carried date column`);
          } else {
            assert.ok(oracle.effectiveValues.includes(dateCell), `${label}: Date cell ${JSON.stringify(dateCell)} is not a fixture effectiveDateTime`);
          }
          evidence.dateSurvivalCells += 1;
        } else if (source && source.effective != null) {
          assert.fail(`${label}: attributed record ${source.id} HAS effectiveDateTime ${source.effective} but its carried Date cell rendered blank — survival witness mutated`);
        }
        if (variable === KEY_ID) {
          assert.ok(/^dev-/.test(valueCell), `${label}: id melt cell ${JSON.stringify(valueCell)} is not a seeded observation id`);
          assert.ok(oracle.byId.has(valueCell), `${label}: melted id ${valueCell} not part of the live observation oracle`);
          // The ID melt cell IS an exact-value witness: the melted value must equal the
          // record's own identity string. (The sparse unit/subject pop values almost never
          // land inside the 1000-row preview page, so they are witnesses-when-present.)
          assert.equal(valueCell, String((source || {}).id || valueCell), `${label}: id melt cell must equal the block record identity`);
          evidence.idMeltCells += 1;
          evidence.exactMeltCells += 1;
        } else if (variable === KEY_UNIT) {
          if (blank(valueCell)) {
            assert.ok(!complete || !source || source.unit === null, `${label}: blank unit cell attributed to record ${source?.id} which HAS a unit`);
            evidence.blankMeltCells += 1;
          } else if (source) {
            assert.equal(valueCell, String(source.unit), `${label}: valueQuantity.unit cell ${JSON.stringify(valueCell)} != oracle ${source.unit}`);
            evidence.exactMeltCells += 1;
          } else {
            assert.ok(unitDomain.has(valueCell), `${label}: unattributed unit cell ${JSON.stringify(valueCell)} not in oracle unit domain`);
            evidence.exactMeltCells += 1;
          }
        } else if (variable === KEY_SUB) {
          if (blank(valueCell)) {
            assert.ok(!complete || !source || source.subject === null, `${label}: blank subject cell attributed to record ${source?.id} which HAS a subject.reference`);
            evidence.blankMeltCells += 1;
          } else if (source) {
            assert.equal(valueCell, String(source.subject), `${label}: subject.reference cell ${JSON.stringify(valueCell)} != oracle ${source.subject}`);
            evidence.exactMeltCells += 1;
          } else {
            assert.ok(subjectDomain.has(valueCell), `${label}: unattributed subject cell ${JSON.stringify(valueCell)} not in oracle subject domain`);
            evidence.exactMeltCells += 1;
          }
        }
      }
      if (complete) {
        const keySet = [...new Set(block.rows.map((candidate) => candidate[varIndex]))].sort();
        assert.deepEqual(keySet, [...activeKeys].sort(), `${label}: complete record block must melt exactly the active keys, got ${JSON.stringify(keySet)}`);
      }
    }
    return rows;
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
      assert.ok(historyIds.length >= 2, `expected filter+melt history steps, got ${JSON.stringify(historyIds)}`);
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
  assert.ok(stampBefore.runningBinary && stampBefore.liveSource && stampBefore.expectedSource && stampBefore.gitHead,
    `build identity fields must be non-empty (a green with an empty running column is vacuous): ${JSON.stringify(stampBefore)}`);
  assert.equal(stampBefore.expectedSource, stampBefore.liveSource, `serving source must match the expected source: ${JSON.stringify(stampBefore)}`);
  requireRecordedIdentity(stampBefore, 'pre-run:');

  // Independent oracle.
  const oracle = computeOracle();
  report.oracle = { rows: oracle.rows, unitBlankCount: oracle.unitBlankCount, subjectBlankCount: oracle.subjectBlankCount, effectiveValues: oracle.effectiveValues };

  // Self-seed three independent fixtures: A carries the raw-CDP UI journey, B is a pristine
  // proposal host for the API negatives (a persisted melt there would mask candidate
  // validation), and C supplies valid signed tokens whose embedded snapshot differs from B.
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

  // STEP 0 BEHAVIOR: capabilities expose unpivotSourceInput with the signed unit string and
  // ZERO date-family choices: the server refuses date/date_time melts (cd39eeaf, operator
  // decision #4) and discovery hides them instead of advertising a refusal.
  await navigate();
  {
    const builder = (await mainCtx.call('builder')).value;
    const caps = await mainCtx.call('construction-capabilities', {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, outputId: builder.workspace.documents[0].output.id, stageId: 'source_projection',
    });
    const usi = caps.value.unpivotSourceInput;
    assert.ok(caps.status === 200 && usi && usi.supported, `step 0 unpivotSourceInput missing/unsupported (status ${caps.status})`);
    const unitChoice = usi?.choices?.find((choice) => choice.fieldPath === 'valueQuantity.unit');
    const effChoice = usi?.choices?.find((choice) => choice.fieldPath === 'effectiveDateTime');
    assert.ok(unitChoice && String(unitChoice.logicalType).toLowerCase() === 'string', `step 0 signed string choice missing: ${JSON.stringify(unitChoice)}`);
    assert.equal(effChoice, undefined, `step 0: the server must hide the refused date-family melt choice (cd39eeaf, operator decision #4); got ${JSON.stringify(effChoice)}`);
    const dateFamilyChoices = (usi.choices || []).filter((choice) => ['date', 'date_time'].includes(String(choice.logicalType).toLowerCase()));
    assert.equal(dateFamilyChoices.length, 0, `step 0: unpivotSourceInput leaked date-family melt eligibility: ${JSON.stringify(dateFamilyChoices)}`);
    assert.ok((usi.choices || []).length > 0, 'step 0: the date-family hide must be selective (non-vacuous) — permitted melt choices must remain listed');
    assert.equal(unitChoice.isPopulated, true, 'step 0 signed string option must report isPopulated');
    record('discovery-hides-date-family', { apiChoicesZero: true, listedChoices: (usi.choices || []).map((choice) => choice.fieldPath) });
    unpopulatedPaths = (usi.choices || []).filter((choice) => choice.isPopulated !== true).map((choice) => choice.fieldPath);
    report.steps.push({ step: 'behave', status: caps.status, supported: usi.supported, choicesPaths: usi.choices.map((c) => c.fieldPath) });
    await screenshot('step0-capabilities');
    record('step-0-behavior-capabilities', { status: caps.status, supported: usi.supported, choicesPaths: usi.choices.map((c) => c.fieldPath) });
  }

  // STEP 1 PRELUDE: a persisted row-preserving signed FILTER (status EQUALS final) which is also
  // the ledger carrier for signed melt inputs.
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
    // Pre-first-persistence window: construction is legitimately nil until the first
    // APPLY_CONSTRUCTION_PROPOSAL (live-pinned 2026-11-02: a seeded doc carries no construction
    // key; authoringv2/commands.go:1447 sets it on proposal apply). This poll therefore keeps the
    // filter lane's tolerant read shape; the post-loop asserts below are the observed-transition
    // floor — a FILTER step or its signed status lane that never persists stays loudly red.
    for (let i = 0; i < 25; i += 1) {
      const builder = (await mainCtx.call('builder')).value;
      if ((builder.workspace.documents[0].construction?.steps || []).some((s) => s.operation?.kind === 'FILTER')) break;
      await wait(300);
    }
    await wait(400);
    const doc = (await mainCtx.call('builder')).value?.workspace?.documents?.[0]?.construction || {};
    assert.ok((doc.steps || []).some((s) => s.operation?.kind === 'FILTER'), 'PRELUDE: FILTER step was not persisted');
    assert.ok((doc.sourceProjections || []).some((p) => p.fieldPath === 'status' && p.ownerStepId), 'PRELUDE: signed filter lane projection not persisted');
    postPreludeBaseline = (await mainCtx.call('builder')).value;
    await screenshot('step1-prelude-filter');
    if (await mounted('[data-testid="construction-close-operation-editor"]')) await realClick('[data-testid="construction-close-operation-editor"]');
    record('step-1-prelude-filter-step', { filterLanes: (doc.sourceProjections || []).filter((p) => p.fieldPath === 'status').length });
  }

  // STEP 2 DISCOVERY: Reshape entry -> intent pivot-branch -> unpivot choice + dataset optgroup.
  await navigate();
  {
    const entry = await openUnpivotEditor();
    assert.ok(entry && typeof entry.aria === 'string' && entry.aria.length > 0, `row-definition settings trigger must announce itself: ${JSON.stringify(entry)}`);
    const choice = await chooseUnpivotForm();
    assert.ok(choice && /field.*value|value.*field|columns/i.test(choice.title || ''), `"Stack columns" affordance must describe the melt: ${JSON.stringify(choice)}`);
    const boxes = await meltCheckboxLabels();
    assert.ok(boxes.some((box) => box.label === 'ID') && boxes.some((box) => box.label === 'Number'),
      `melt checkbox list must expose the staged public columns: ${JSON.stringify(boxes)}`);
    // Configure one public melt input so the dataset-channel affordances render.
    for (const box of boxes) {
      if (box.checked && box.label !== 'ID') await clickCheckboxByLabel(`Unpivot ${box.label}`);
    }
    const idBox = (await meltCheckboxLabels()).find((box) => box.label === 'ID');
    if (!idBox.checked) await clickCheckboxByLabel('Unpivot ID');
    await until(() => mounted('[data-testid="construction-reshape-unpivot-source-kind-note"]'), 'compiler kind note', 15000);
    const unavailable = await mounted('[data-testid="construction-reshape-unpivot-source-unavailable"]');
    assert.equal(unavailable, false, 'supported capability must not render the source-unavailable note');
    const affordance = await mounted('[data-testid="construction-reshape-unpivot-add-source-field"]');
    assert.equal(affordance, true, 'add-a-field-not-in-the-table affordance must render when dataset choices are available');
    const rowOptions = await readRowDatasetOptions(0);
    save('step2-discovery-row0.json', rowOptions);
    await screenshot('step2-discovery');
    assert.ok(Array.isArray(rowOptions.options) && rowOptions.options.length > 0, 'dataset optgroup must offer options');
    assert.equal(rowOptions.mainOptions.length > 0, true, 'in-table optgroup must remain');
    const effOption = rowOptions.options.find((option) => option.title === 'effectiveDateTime');
    const unitOption = rowOptions.options.find((option) => option.title === 'valueQuantity.unit');
    assert.equal(effOption, undefined, `step 2: the refused date-family option must not render in the melt optgroup: ${JSON.stringify(effOption)}`);
    assert.ok(unitOption, `signed unit dataset option missing (selective-hide control): ${JSON.stringify(rowOptions.options)}`);
    assert.ok(!rowOptions.options.some((option) => /date/i.test(`${option.title || ''} ${option.label || ''}`)),
      `dataset optgroup leaked a date-family option: ${JSON.stringify(rowOptions.options.map((option) => option.title))}`);
    assert.ok(/^source:rc1\./.test(unitOption.value), `dataset option value must remain source:<choiceId>, got ${unitOption.value}`);
    record('discovery-hides-date-family', { optgroupZeroDateOptions: true, optgroupOptions: rowOptions.options.map((option) => option.title) });
    assert.ok(!(rowOptions.options || []).some((option) => unpopulatedPaths.includes(option.title)),
      `the editor must NOT offer isPopulated=false dataset fields (unpopulated ${JSON.stringify(unpopulatedPaths)})`);
    record('step-2-discovery', { affordance, optgroupOptions: rowOptions.options.map((o) => o.title), unitOption: unitOption.value });
    await closeUnpivotEditor();
  }

  // STEP 3 CONFIGURE + oracle-exact melt preview with PRESERVE blanks.
  await navigate();
  {
    await openUnpivotEditor();
    await chooseUnpivotForm();
    for (const box of await meltCheckboxLabels()) {
      if (box.checked && box.label !== 'ID') await clickCheckboxByLabel(`Unpivot ${box.label}`);
    }
    if (!(await meltCheckboxLabels()).find((box) => box.label === 'ID')?.checked) await clickCheckboxByLabel('Unpivot ID');
    // Two signed lanes on the string family (the only pairing both the UI scalar gate and
    // the compiler accept). The add button pre-binds the first unbound populated choice
    // (effectiveDateTime, sorted first) — the editor's scalar gate refuses date_time synth
    // columns (recorded defect F-B), so every add is rebound before anything is asserted.
    await realClick('[data-testid="construction-reshape-unpivot-add-source-field"]');
    await bindDatasetRow(1, 'valueQuantity.unit', 'Observation.valueQuantity.unit');
    await realClick('[data-testid="construction-reshape-unpivot-add-source-field"]');
    await bindDatasetRow(2, 'subject.reference', 'Observation.subject.reference');
    await openAdvanced();
    if (await meltCheckboxLabels().then((boxes) => boxes.find((box) => box.label === 'Number')?.checked)) {
      await clickCheckboxByLabel('Unpivot Number');
      assert.fail('STEP 3: the public Number column must stay unmelted as the row-group anchor');
    }
    {
      const boxes = await meltCheckboxLabels();
      const dateBox = boxes.find(isDateWitnessCheckbox);
      assert.ok(dateBox, `STEP 3: the unmelted Date survival witness must be selectable in the melt checkbox list: ${JSON.stringify(boxes.map((box) => box.label))}`);
      if (dateBox.checked) {
        await clickCheckboxByLabel(`Unpivot ${dateBox.label}`);
        assert.fail('STEP 3: the public Date column must stay unmelted as the date-family survival witness');
      }
    }
    // Explicit deterministic keys for the three melt inputs.
    assert.equal(await setInputKey(0, KEY_ID), KEY_ID, 'id melt key set');
    assert.equal(await setInputKey(1, KEY_UNIT), KEY_UNIT, 'unit melt key set');
    assert.equal(await setInputKey(2, KEY_SUB), KEY_SUB, 'subject melt key set');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'melt proposal ready', 30000);
    const effectText = await evaluate(`document.querySelector('[data-testid="construction-unpivot-effect"]')?.textContent || ''`);
    assert.match(effectText, /3 new rows per original row/, `row-growth statement malformed: ${JSON.stringify(effectText)}`);
    const domPreview = await readDomPreview();
    await screenshot('step3-preview-preserve');
    report.steps.push({ step: 'configure', headers: domPreview?.headers });
    const seen = checkMeltPreview(domPreview, 'STEP 3', [KEY_ID, KEY_UNIT, KEY_SUB]);
    record('step-3-configure-oracle-preview-preserve', { previewRows: seen.length, headers: domPreview?.headers });
    await closeUnpivotEditor();
  }

  // STEP 4 CANCEL byte-stability against the post-prelude baseline.
  {
    const baseline = postPreludeBaseline;
    const after = await mainCtx.call('builder');
    const same = JSON.stringify(after.value.workspace) === JSON.stringify(baseline.workspace)
      && after.value.draftVersion === baseline.draftVersion && after.value.draftDigest === baseline.draftDigest;
    save('step4-cancel-after.json', after.value);
    report.steps.push({ step: 'cancel', sameDoc: same, before: { dv: baseline.draftVersion, dd: baseline.draftDigest }, after: { dv: after.value.draftVersion, dd: after.value.draftDigest } });
    assert.ok(same, `step 4 CANCEL changed doc: ${after.value.draftDigest}`);
    await screenshot('step4-after-cancel');
    record('step-4-cancel-byte-stable', { sameDoc: same, draftDigest: after.value.draftDigest });
  }

  // STEP 5 APPLY -> persisted UNPIVOT step + signed melt-lane projections + honest columns.
  await navigate();
  {
    await openUnpivotEditor();
    await chooseUnpivotForm();
    for (const box of await meltCheckboxLabels()) {
      if (box.checked && box.label !== 'ID') await clickCheckboxByLabel(`Unpivot ${box.label}`);
    }
    if (!(await meltCheckboxLabels()).find((box) => box.label === 'ID')?.checked) await clickCheckboxByLabel('Unpivot ID');
    {
      const boxes = await meltCheckboxLabels();
      const dateBox = boxes.find(isDateWitnessCheckbox);
      assert.ok(dateBox, `STEP 5: the unmelted Date survival witness must be selectable in the melt checkbox list: ${JSON.stringify(boxes.map((box) => box.label))}`);
      if (dateBox.checked) {
        await clickCheckboxByLabel(`Unpivot ${dateBox.label}`);
        assert.fail('STEP 5: the public Date column must stay unmelted as the date-family survival witness');
      }
    }
    await realClick('[data-testid="construction-reshape-unpivot-add-source-field"]');
    await bindDatasetRow(1, 'valueQuantity.unit', 'Observation.valueQuantity.unit');
    await realClick('[data-testid="construction-reshape-unpivot-add-source-field"]');
    await bindDatasetRow(2, 'subject.reference', 'Observation.subject.reference');
    await openAdvanced();
    assert.equal(await setInputKey(0, KEY_ID), KEY_ID, 'id melt key set');
    assert.equal(await setInputKey(1, KEY_UNIT), KEY_UNIT, 'unit melt key set');
    assert.equal(await setInputKey(2, KEY_SUB), KEY_SUB, 'subject melt key set');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'melt proposal ready', 30000);
    await until(() => mounted('[data-testid="construction-apply-proposal"]'), 'apply visible', 20000);
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      if (await meltStep()) break;
      await wait(300);
    }
    await wait(400);
    const doc = await builderDoc();
    save('step5-doc-after-apply.json', { steps: doc.steps, sourceProjections: doc.sourceProjections });
    const step = (doc.steps || []).find((s) => s.operation?.kind === 'UNPIVOT');
    assert.ok(step, 'APPLY: no persisted UNPIVOT step');
    const unpivot = step.operation.unpivot;
    assert.ok(unpivot && unpivot.inputs?.length === 3, `APPLY: persisted melt inputs wrong: ${JSON.stringify(unpivot?.inputs)}`);
    assert.equal(String(unpivot.nullRowPolicy), 'PRESERVE', `APPLY: default policy must persist as PRESERVE: ${unpivot.nullRowPolicy}`);
    const sub = (doc.sourceProjections || []).find((p) => p.fieldPath === 'subject.reference');
    const unit = (doc.sourceProjections || []).find((p) => p.fieldPath === 'valueQuantity.unit');
    for (const [lane, name, fhir, logical, label] of [[unit, 'valueQuantity.unit', 'string', 'string', 'Observation.valueQuantity.unit'], [sub, 'subject.reference', 'string', 'string', 'Observation.subject.reference']]) {
      assert.ok(lane && lane.columnId?.length > 1 && lane.columnId.startsWith('unpivot-input_'),
        `APPLY: signed melt lane ${name} not persisted server-shaped: ${JSON.stringify(lane)}`);
      assert.equal(lane.ownerStepId, step.id, `APPLY: lane ${name} owner ${lane.ownerStepId} != melt step ${step.id}`);
      assert.equal(lane.fhirType, fhir, `APPLY: lane ${name} compiled echo fhirType ${lane.fhirType} != ${fhir}`);
      assert.equal(lane.logicalType, logical, `APPLY: lane ${name} logicalType ${lane.logicalType} != ${logical}`);
      assert.equal(lane.label, label, `APPLY: lane ${name} server label ${lane.label} != ${label}`);
    }
    const dom = await readDomPreview();
    await screenshot('step5-after-apply');
    assert.ok((dom?.headers || []).some((header) => /Variable/i.test(header)) && (dom?.headers || []).some((header) => /Value/i.test(header)),
      `APPLY: melt outputs absent from the applied table: ${JSON.stringify(dom?.headers)}`);
    assert.ok(!(dom?.headers || []).some((header) => /ID$|identity/i.test(header)), `APPLY: melted ID column must leave the table: ${JSON.stringify(dom?.headers)}`);
    // Honesty leg: the consumed signed melt inputs are never materialized as displayable columns.
    assert.ok(!(dom?.headers || []).some((header) => /valueQuantity\.unit|subject\.reference/i.test(header)),
      `APPLY: consumed signed melt input must not appear as a column: ${JSON.stringify(dom?.headers)}`);
    const seen = checkMeltPreview(dom, 'STEP 5 APPLY', [KEY_ID, KEY_UNIT, KEY_SUB]);
    {
      report.dateWitness = report.dateWitness || { baseline: dateWitnessDefinition(postPreludeBaseline), postApply: null, reload: null, afterRemoval: null };
      // Observation.effectiveDateTime is a date_time-rooted field (probe-decorated
      // logicalType date_time); the witness must be authored with date-family truth.
      assert.ok(report.dateWitness.baseline && ['date', 'date_time'].includes(String(report.dateWitness.baseline.logicalType).toLowerCase()),
        `SURVIVAL: the baseline Date witness must be an authored date-family column: ${JSON.stringify(report.dateWitness.baseline)}`);
      const postApply = dateWitnessDefinition((await mainCtx.call('builder')).value);
      assert.ok(postApply, 'SURVIVAL: the authored Date column definition disappeared with the melt apply');
      assert.deepEqual(postApply, report.dateWitness.baseline, 'SURVIVAL: the Date column definition changed with the string-melt apply');
      assert.ok((dom?.headers || []).some(isDateWitnessHeader), `SURVIVAL: Date header absent from the applied melt table: ${JSON.stringify(dom?.headers)}`);
      report.dateWitness.postApply = postApply;
      record('date-column-survives-string-melt', { postApply: true, dateCells: evidence.dateSurvivalCells });
    }
    report.steps.push({ step: 'apply', steps: (doc.steps || []).map((s) => s.operation?.kind),
      sourceProjections: (doc.sourceProjections || []).map((p) => ({ col: p.columnId, owner: p.ownerStepId, path: p.fieldPath, fhir: p.fhirType })) });
    await screenshot('step5-preview-post-apply');
    if (await mounted('[data-testid="construction-close-operation-editor"]')) await realClick('[data-testid="construction-close-operation-editor"]');
    report.meltStepId = step.id;
    record('step-5-apply-exact-rows', { previewRows: seen.length, subLane: sub.columnId, unitLane: unit.columnId, meltOwner: step.id });
  }

  // STEP 6 RELOAD -> saved-binding restore + capability echo columnId == persisted lane columnId.
  {
    await navigate();
    const doc = await builderDoc();
    const step = (doc.steps || []).find((s) => s.operation?.kind === 'UNPIVOT');
    const sub = (doc.sourceProjections || []).find((p) => p.fieldPath === 'subject.reference');
    const unit = (doc.sourceProjections || []).find((p) => p.fieldPath === 'valueQuantity.unit');
    const opened = await openEditorFromHistory(async () => {
      const rows = await evaluate(`(()=>[...document.querySelectorAll('[data-testid$="-column"]')].filter(s=>s.getAttribute('data-testid').startsWith('construction-reshape-unpivot-input-')).length)()`);
      // Saved signed rows render their server-declared column (label span), never the
      // 'Selected field (from your data)' placeholder, which only appears for orphans.
      const restored = await evaluate(`(()=>[...document.querySelectorAll('[data-testid$="-column"]')].filter(s=>{if(!s.getAttribute('data-testid').startsWith('construction-reshape-unpivot-input-'))return false;const l=s.closest('label');const sp=l&&l.querySelector('span');const t=sp?sp.textContent.trim():'';return t==='Observation.valueQuantity.unit'||t==='Observation.subject.reference';}).length)()`);
      return rows >= 3 && restored >= 2 && `rows:${rows}:restored:${restored}`;
    });
    // Server-truth read (bs-62-2 class): the echo must be keyed to a melt step that is OBSERVED on
    // the server, never to an undefined handle after a silently-missing construction.
    const meltLiveSource = await builderDoc();
    const meltLive = (meltLiveSource.steps || []).find((s) => s.operation?.kind === 'UNPIVOT');
    assert.ok(meltLive, 'RELOAD: the UNPIVOT step vanished from the server-truth doc between the reload census and the echo keying (the pre-fix bytes died here with an opaque TypeError on undefined.inputs)');
    // Echo is keyed to the consuming step's input stage (the carrier filter stage), not to the
    // melt stage itself — the derive-lane lesson applies verbatim.
    const inputStageId = (meltLive.inputs || []).find((input) => input.kind === 'STEP_OUTPUT')?.stepId;
    let echoSub = null, echoUnit = null;
    for (let i = 0; i < 15 && !(echoSub?.columnId && echoUnit?.columnId); i += 1) {
      const bb = (await mainCtx.call('builder')).value;
      const stageId = inputStageId || 'source_projection';
      const cc = await mainCtx.call('construction-capabilities', {
        snapshotToken: bb.catalog.snapshotToken, expectedDraftVersion: bb.draftVersion, expectedDraftDigest: bb.draftDigest,
        outputId: bb.workspace.documents[0].output.id, stageId,
      });
      const choices = cc.value?.unpivotSourceInput?.choices || [];
      echoSub = choices.find((choice) => choice.fieldPath === 'subject.reference') ?? echoSub;
      echoUnit = choices.find((choice) => choice.fieldPath === 'valueQuantity.unit') ?? echoUnit;
      if (!(echoSub?.columnId && echoUnit?.columnId)) await wait(200);
    }
    await screenshot('step6-reload-restore');
    assert.ok(echoSub?.columnId, `RELOAD: subject.reference echo columnId not materialized (persisted ${sub.columnId})`);
    assert.ok(echoUnit?.columnId, `RELOAD: valueQuantity.unit echo columnId not materialized (persisted ${unit.columnId})`);
    assert.equal(echoSub.columnId, sub.columnId, `RELOAD: echo subject.reference ${echoSub.columnId} != persisted ${sub.columnId}`);
    assert.equal(echoUnit.columnId, unit.columnId, `RELOAD: echo unit ${echoUnit.columnId} != persisted ${unit.columnId}`);
    {
      const reloaded = dateWitnessDefinition((await mainCtx.call('builder')).value);
      assert.ok(report.dateWitness?.baseline, 'SURVIVAL: the step-5 baseline witness is missing at reload');
      assert.ok(reloaded, 'SURVIVAL: the Date witness column disappeared on reload');
      assert.deepEqual(reloaded, report.dateWitness.baseline, 'SURVIVAL: the Date column definition changed across the browser reload with the string melt persisted');
      report.dateWitness.reload = reloaded;
      record('date-column-survives-string-melt', { reload: true });
    }
    await closeUnpivotEditor();
    record('step-6-reload-materialized-echo', { opened, echoSub: echoSub.columnId, echoUnit: echoUnit.columnId });
  }

  // STEP 7 EDIT -> rename the melt outputs + switch blanks policy to DROP (no blank cells).
  {
    await navigate(); await wait(300);
    await openEditorFromHistory(() => mounted('[data-testid="construction-reshape-unpivot"]'));
    await openAdvanced();
    assert.equal(await setInput('[data-testid="construction-reshape-unpivot"] input[aria-label="Unpivot key output name"]', 'melt_name'), 'melt_name');
    assert.equal(await setInput('[data-testid="construction-reshape-unpivot"] input[aria-label="Unpivot value output name"]', 'melt_value'), 'melt_value');
    await setSelect('[data-testid="construction-reshape-unpivot"] select[aria-label="Unpivot null row policy"]', 'DROP');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'edit proposal ready (rename+DROP)', 30000);
    await wait(300);
    const domPreview = await readDomPreview();
    await screenshot('step7-edit-drop-preview');
    const valIndex = (domPreview?.headers || []).findIndex((header) => /melted|Value/i.test(header));
    assert.ok(valIndex >= 0, `DROP preview must still show the melted value column: ${JSON.stringify(domPreview?.headers)}`);
    const blankCount = (domPreview?.rows || []).filter((row) => [' ', '—', ''].includes((row[valIndex] ?? '').trim())).length;
    assert.equal(blankCount, 0, `DROP policy: no blank melt cells may remain on the page, got ${blankCount}`);
    assert.ok((domPreview?.headers || []).some((header) => /melt|Variable|Value/i.test(header)), 'renamed outputs still render melt columns');
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      const current = await meltStep();
      if (String(current?.operation?.unpivot?.nullRowPolicy) === 'DROP') break;
      await wait(300);
    }
    const persisted = await meltStep();
    assert.equal(String(persisted?.operation?.unpivot?.nullRowPolicy), 'DROP', `EDIT: DROP policy not persisted: ${JSON.stringify(persisted?.operation?.unpivot)}`);
    const renamedOutputs = (persisted?.outputs || []).map((o) => o.name);
    assert.ok(renamedOutputs.includes('melt_name') && renamedOutputs.includes('melt_value'),
      `EDIT: renamed melt outputs not persisted: ${JSON.stringify(renamedOutputs)}`);
    record('step-7-edit-rename-and-drop', { persistedPolicy: 'DROP', renamedOutputs: ['melt_name', 'melt_value'], blankCells: 0 });
  }

  // STEP 8 EDIT -> dissolve the signed unit melt lane by rebinding its row to a public column.
  {
    await navigate(); await wait(300);
    await openEditorFromHistory(() => mounted('[data-testid="construction-reshape-unpivot"]'));
    await openAdvanced();
    // Locate the signed unit row by its bound label; row order follows the persisted step's
    // input order, which must not be hard-coded.
    const unitRowLabel = await evaluate(`(()=>{const idx=[];document.querySelectorAll('[data-testid$="-column"]').forEach((s)=>{const t=s.getAttribute('data-testid')||'';if(!t.startsWith('construction-reshape-unpivot-input-'))return;const m=t.match(/input-(\\d+)-column$/);if(!m)return;const l=s.closest('label');const sp=l&&l.querySelector('span');if(sp&&sp.textContent.trim()==='Observation.valueQuantity.unit')idx.push(Number(m[1]));});return idx[0]===undefined?null:idx[0];})()`);
    assert.ok(unitRowLabel !== null, 'a melt row bound to Observation.valueQuantity.unit must exist before the dissolve');
    const numberColumnValue = await evaluate(`(()=>{const s=document.querySelector('[data-testid="construction-reshape-unpivot-input-${unitRowLabel}-column"]');const o=[...s.querySelectorAll('option')].find(o=>o.textContent.trim()==='Number');return o?o.value:null;})()`);
    assert.ok(numberColumnValue, 'the public Number column must remain selectable for the melt row');
    await setRowSource(unitRowLabel, numberColumnValue);
    // The rebind dissolves the signed binding; unchecking the public column then drops the
    // input entirely so Number returns to the table as the anchor (rebind alone would melt it).
    await until(() => meltCheckboxLabels().then((boxes) => boxes.find((box) => box.label === 'Number')?.checked === true), 'Number input present after rebind', 15000);
    await clickCheckboxByLabel('Unpivot Number');
    await until(() => meltCheckboxLabels().then((boxes) => boxes.find((box) => box.label === 'Number')?.checked === false), 'Number input removed after uncheck', 15000);
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'edit proposal ready (unit lane dissolved)', 30000);
    const domPreview = await readDomPreview();
    const varIndex = (domPreview?.headers || []).findIndex((header) => /melt|Variable/i.test(header));
    report.steps.push({ step: 'step8-raw-preview', headers: domPreview?.headers, varIndex, sample: (domPreview?.rows || []).slice(0, 12) });
    const visibleKeys = [...new Set((domPreview?.rows || []).map((row) => row[varIndex]))]
      .filter((cell) => cell && !/^dev-|^final$/.test(cell) && !/^\d+$/.test(cell) && !/^\u2014$/.test(cell));
    // Under the persisted DROP policy, melt rows with missing values are dropped per-value
    // row, so the sparse subject values may fall off the page. The dissolution witness is
    // the ABSENCE of the cleared unit key while the surviving id key stays visible.
    assert.ok((domPreview?.rows || []).length >= 10, `dissolved melt preview lost its row coverage: ${(domPreview?.rows || []).length}`);
    assert.ok(!visibleKeys.includes(KEY_UNIT), `cleared unit lane must not melt rows anymore, got ${JSON.stringify(visibleKeys)}`);
    assert.ok(visibleKeys.includes(KEY_ID), `surviving id lane must keep melting: ${JSON.stringify(visibleKeys)}`);
    await screenshot('step8-unit-lane-dissolved-preview');
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      const current = await meltStep();
      if ((current?.operation?.unpivot?.inputs || []).length === 2 && !(await meltProjections()).some((p) => p.fieldPath === 'valueQuantity.unit')) break;
      if (current && !(await meltProjections()).some((p) => p.fieldPath === 'valueQuantity.unit')) break;
      await wait(300);
    }
    const lanes = await meltProjections();
    assert.ok(!lanes.some((p) => p.fieldPath === 'valueQuantity.unit'), `cleared melt lane must STAY cleared after apply: ${JSON.stringify(lanes)}`);
    assert.ok(lanes.some((p) => p.fieldPath === 'subject.reference'), `the surviving subject melt lane must be untouched: ${JSON.stringify(lanes)}`);
    const filterLanes = await filterLaneProjection();
    assert.equal(filterLanes.length, 1, `the prelude filter lane must be untouched by melt clearing: ${JSON.stringify(filterLanes)}`);
    record('step-8-cleared-melt-lane-stays-cleared', { lanes: lanes.map((p) => p.fieldPath), filterLane: filterLanes[0]?.columnId });
  }

  // STEP 9 RE-ADD -> honest re-materialization of the unit melt lane.
  {
    await navigate(); await wait(200);
    await openEditorFromHistory(() => mounted('[data-testid="construction-reshape-unpivot"]'));
    await openAdvanced();
    await realClick('[data-testid="construction-reshape-unpivot-add-source-field"]');
    await bindDatasetRow(2, 'valueQuantity.unit', 'Observation.valueQuantity.unit');
    assert.equal(await setInputKey(2, KEY_UNIT), KEY_UNIT, 'unit melt key set');
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 're-add proposal ready', 30000);
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      const lanes = await meltProjections();
      if (lanes.some((p) => p.fieldPath === 'valueQuantity.unit' && p.columnId?.startsWith('unpivot-input_'))) break;
      await wait(300);
    }
    const doc = await builderDoc();
    const step = (doc.steps || []).find((s) => s.operation?.kind === 'UNPIVOT');
    const unit = (doc.sourceProjections || []).find((p) => p.fieldPath === 'valueQuantity.unit');
    assert.ok(unit && unit.ownerStepId === step.id && unit.columnId.startsWith('unpivot-input_'),
      `RE-ADD: unit lane not honestly re-materialized: ${JSON.stringify(unit)}`);
    const builder = (await mainCtx.call('builder')).value;
    const inputStageId = (step.inputs || []).find((input) => input.kind === 'STEP_OUTPUT')?.stepId;
    let echo = null;
    for (let i = 0; i < 15 && !echo?.columnId; i += 1) {
      const bb = (await mainCtx.call('builder')).value;
      const cc = await mainCtx.call('construction-capabilities', {
        snapshotToken: bb.catalog.snapshotToken, expectedDraftVersion: bb.draftVersion, expectedDraftDigest: bb.draftDigest,
        outputId: bb.workspace.documents[0].output.id, stageId: inputStageId || 'source_projection',
      });
      echo = (cc.value?.unpivotSourceInput?.choices || []).find((choice) => choice.fieldPath === 'valueQuantity.unit') ?? echo;
      if (!echo?.columnId) await wait(200);
    }
    assert.ok(echo?.columnId, `RE-ADD: capability echo did not re-materialize a columnId (persisted ${unit.columnId})`);
    assert.equal(echo.columnId, unit.columnId, `RE-ADD: echo ${echo.columnId} != re-materialized ${unit.columnId}`);
    await screenshot('step9-re-add-honest');
    await closeUnpivotEditor();
    record('step-9-re-add-honest-rematerialization', { unitLane: unit.columnId, echo: echo.columnId });
  }

  // STEP 10 REMOVE -> melt step gone; melt lanes gone; filter lane intact.
  {
    if (await mounted('[data-testid="construction-close-operation-editor"]')) { await realClick('[data-testid="construction-close-operation-editor"]'); await wait(200); }
    await navigate(); await wait(300);
    const docBefore = await builderDoc();
    const step = (docBefore.steps || []).find((s) => s.operation?.kind === 'UNPIVOT');
    assert.ok(step, 'REMOVE: no UNPIVOT step to remove');
    // Zero-match discipline (filter-lane twin): the post-removal `length === 0` leftover asserts
    // below are only meaningful when the pre-removal census really contained both signed melt
    // lanes; observing the pre-state turns "went to zero" into a witnessed transition instead of an
    // empty scan that certifies itself.
    assert.ok(docBefore.sourceProjections?.some((projection) => projection.fieldPath === 'valueQuantity.unit' && (projection.ownerStepId || '').startsWith('unpivot_')),
      `REMOVE pre-remove census: no melt-owned valueQuantity.unit projection in the persisted doc; "no leftover melt lane" must never be certified from a census that was empty to begin with: ${JSON.stringify((docBefore.sourceProjections || []).map((projection) => ({ path: projection.fieldPath, owner: projection.ownerStepId })))}`);
    assert.ok(docBefore.sourceProjections?.some((projection) => projection.fieldPath === 'subject.reference' && (projection.ownerStepId || '').startsWith('unpivot_')),
      `REMOVE pre-remove census: no melt-owned subject.reference projection in the persisted doc; "no leftover melt lane" must never be certified from a census that was empty to begin with: ${JSON.stringify((docBefore.sourceProjections || []).map((projection) => ({ path: projection.fieldPath, owner: projection.ownerStepId })))}`);
    const historyIds = await historyIdsOf();
    const target = historyIds.includes(step.id) ? step.id : historyIds[0];
    await clickHistoryStep(target);
    if (!(await mounted(`[data-testid="construction-remove-step-${target}"]`))) {
      await wait(1500);
      if (!(await mounted(`[data-testid="construction-remove-step-${target}"]`))) await clickHistoryStep(target);
    }
    await until(() => mounted(`[data-testid="construction-remove-step-${target}"]`), 'remove btn present after select', 15000);
    await realClick(`[data-testid="construction-remove-step-${target}"]`);
    await until(() => mounted('[data-testid="construction-proposal-ready"]'), 'removal proposal ready', 30000);
    await screenshot('step10-remove-proposal');
    await realClick('[data-testid="construction-apply-proposal"]');
    for (let i = 0; i < 25; i += 1) {
      if (!(await meltStep())) break;
      await wait(300);
    }
    await wait(400);
    const afterDoc = await builderDoc();
    assert.equal((afterDoc.steps || []).filter((s) => s.operation?.kind === 'UNPIVOT').length, 0, 'REMOVE: UNPIVOT step still present');
    const leftovers = (afterDoc.sourceProjections || []).filter((p) => (p.ownerStepId || '').startsWith('unpivot_') || ['subject.reference', 'valueQuantity.unit'].includes(p.fieldPath));
    assert.equal(leftovers.length, 0, `REMOVE: leftover melt-owned source projections: ${JSON.stringify(leftovers)}`);
    const filterLanes = (afterDoc.sourceProjections || []).filter((p) => p.fieldPath === 'status');
    assert.equal(filterLanes.length, 1, `REMOVE: the prelude filter lane must survive the melt removal: ${JSON.stringify(afterDoc.sourceProjections)}`);
    const dom = await readDomPreview();
    assert.ok(!(dom?.headers || []).some((header) => /Variable|Value|melt/i.test(header)),
      `REMOVE: melt columns still visible: ${JSON.stringify(dom?.headers)}`);
    {
      assert.ok((dom?.headers || []).some(isDateWitnessHeader),
        `SURVIVAL: the Date witness column must outlive the melt removal entirely: ${JSON.stringify(dom?.headers)}`);
      const afterRemoval = dateWitnessDefinition((await mainCtx.call('builder')).value);
      assert.ok(report.dateWitness?.baseline, 'SURVIVAL: the step-5 baseline witness is missing at removal');
      assert.ok(afterRemoval, 'SURVIVAL: the Date witness column disappeared with the melt removal');
      assert.deepEqual(afterRemoval, report.dateWitness.baseline, 'SURVIVAL: the Date column definition changed when the melt was removed');
      report.dateWitness.afterRemoval = afterRemoval;
      record('date-column-survives-string-melt', { afterRemoval: true });
    }
    await screenshot('step10-removed');
    record('step-10-remove-clean', { meltLanes: leftovers.length, filterLane: filterLanes[0]?.columnId });
  }

  // Post-run build identity: source and running binary must be unchanged across the run.
  const stampAfter = collectBuildIdentity();
  requireRecordedIdentity(stampAfter, 'post-run:');
  report.buildIdentity.after = stampAfter;
  save('build-identity.after.json', stampAfter);
  assert.equal(stampAfter.gitHead, stampBefore.gitHead, `git HEAD moved during run (${stampBefore.gitHead} -> ${stampAfter.gitHead})`);
  assert.equal(stampAfter.runningBinary, stampBefore.runningBinary, `serving binary changed during run (${stampBefore.runningBinary} -> ${stampAfter.runningBinary})`);
  assert.equal(stampAfter.liveSource, stampBefore.liveSource, `serving source digest changed during run (${stampBefore.liveSource} -> ${stampAfter.liveSource})`);

  assert.deepEqual(exceptions, [], `uncaught page exceptions: ${JSON.stringify(exceptions)}`);

  // ---- API negatives against the deployed binary, on a pristine proposal host ----
  await runApiNegatives(negCtx, staleCtx);

  // Exit-code contract: every named check must have run and passed.
  const expectedChecks = [
    'step-0-behavior-capabilities', 'discovery-hides-date-family', 'step-1-prelude-filter-step', 'step-2-discovery', 'step-3-configure-oracle-preview-preserve',
    'step-4-cancel-byte-stable', 'step-5-apply-exact-rows', 'step-6-reload-materialized-echo', 'step-7-edit-rename-and-drop',
    'step-8-cleared-melt-lane-stays-cleared', 'step-9-re-add-honest-rematerialization', 'step-10-remove-clean', 'date-column-survives-string-melt',
    'negative-C0-public-control', 'negative-C1-signed-decorates', 'negative-C2-forged', 'negative-C3-cross-snapshot',
    'negative-C4-tampered', 'negative-C5-empty-choiceId', 'negative-C6-missing-choiceId', 'negative-C7-duplicate',
    'negative-C8-column-mismatch', 'negative-C9-over-selection', 'negative-C10-types-incompatible',
    'negative-C11-already-projected-not-500', 'negative-C12-payloadless-requires-unpivot',
    'contract-C13-non-melt-step-undecorated', 'negative-C14-new-carrier-stale-stage', 'negative-C15-signed-date-melt-refused',
    'blank-and-exact-melt-evidence',
  ];
  const present = new Set(report.checks.filter((entry) => entry.status === 'passed').map((entry) => entry.name));
  // The aggregate evidence check is recorded only after this scan; the post-record
  // re-verification below closes the set.
  const skipped = expectedChecks.filter((name) => name !== 'blank-and-exact-melt-evidence' && !present.has(name));
  assert.equal(skipped.length, 0, `skipped checks (each assertion must run): ${JSON.stringify(skipped)}`);
  assert.ok(evidence.blankMeltCells > 0 && evidence.exactMeltCells > 0 && evidence.idMeltCells > 0,
    `melt evidence: blanks=${evidence.blankMeltCells} exact=${evidence.exactMeltCells} idCells=${evidence.idMeltCells} (all must be > 0)`);
  record('blank-and-exact-melt-evidence', { blankMeltCells: evidence.blankMeltCells, exactMeltCells: evidence.exactMeltCells, idMeltCells: evidence.idMeltCells });
  // Re-verify the closed check set after finally recording the aggregate evidence check.
  {
    const presentNow = new Set(report.checks.filter((entry) => entry.status === 'passed').map((entry) => entry.name));
    const missing = expectedChecks.filter((name) => !presentNow.has(name));
    assert.equal(missing.length, 0, `closed check set incomplete after aggregation: ${JSON.stringify(missing)}`);
  }

  // Evidence floor: every leg must have produced its screenshot; silently dropped captures fail.
  const EXPECTED_SCREENSHOT_FLOOR = 13;
  assert.ok(report.screenshots.length >= EXPECTED_SCREENSHOT_FLOOR,
    `screenshot tally below the per-leg floor: wrote ${report.screenshots.length}, expected at least ${EXPECTED_SCREENSHOT_FLOOR}`);
  assert.equal(new Set(report.screenshots).size, report.screenshots.length, `duplicate screenshot names written: ${JSON.stringify(report.screenshots)}`);

  report.status = 'passed';
  save('journey.json', report);
  console.log(`JOURNEY_OK build=${stampBefore.gitHead} runningBinary=${stampBefore.runningBinary} checks=${report.checks.length} screenshots=${report.screenshots.length}`);
} catch (error) {
  report.status = 'failed';
  report.error = error instanceof Error ? error.message : String(error);
  try { save('FAIL-final.html', await evaluate('document.documentElement.outerHTML')); } catch { /* best effort */ }
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

// Zero-match discipline for the build-stamp barrier (bs-62-2 twin fix from
// verify-explorer-filter-row-source.mjs:676-688): the stamp script compares the recorded identity
// internally but a failed or truncated `cat` of the recorded stamp leaves
// recordedBinary/recordedSource null in OUR hands, and the checkExit gate alone never notices.
// Safety: loom-dev-build-stamp.sh --check exits 0 only when expected_source == source_digest &&
// running_binary == expected_binary read from the same recorded file, so on every legitimate green
// these equalities already hold and this floor cannot false-fail a real run.
function requireRecordedIdentity(stamp, when) {
  assert.ok(stamp.recordedBinary && stamp.recordedSource,
    `${when} recorded build identity is unreadable or empty (recordedBinary/recordedSource must be non-null): ${JSON.stringify(stamp)}`);
  assert.equal(stamp.recordedBinary, stamp.runningBinary,
    `${when} recorded binary ${stamp.recordedBinary} must equal the serving binary ${stamp.runningBinary}`);
  assert.equal(stamp.recordedSource, stamp.expectedSource,
    `${when} recorded source ${stamp.recordedSource} must equal the expected source digest ${stamp.expectedSource}`);
}

async function runApiNegatives(host, staleCtx) {
  const capabilities = async (ctx, { stageId = 'source_projection' } = {}) => {
    const builder = (await ctx.call('builder')).value;
    const caps = await ctx.call('construction-capabilities', {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest, outputId: builder.workspace.documents[0].output.id, stageId,
    });
    assert.equal(caps.status, 200, 'capabilities must be 200 for the negatives control');
    return { builder, choices: caps.value.unpivotSourceInput?.choices || [], caps: caps.value };
  };
  // The proposal host carries exactly one persisted row-preserving signed FILTER: the signed
  // melt ledger requires a persisted carrier step (the melt-first crash is recorded in the
  // journey receipt as a defect and is intentionally NOT offered as an accepted control), and
  // every named refusal above the compile step fires identically with or without it. Any
  // persisted melt or derive lane here would mask candidate validation, hence the filter-only
  // assertion.
  let carrierStep = null;
  {
    // Report-only (bs-62-2 class, same disposition as the filter lane): the pristine-host claim is
    // a zero-step count over an optionally-missing census. A seeded doc legitimately carries NO
    // construction object until the first construction proposal is applied (live-pinned 2026-11-02;
    // authoringv2/commands.go:1447 sets it on APPLY_CONSTRUCTION_PROPOSAL), so a construction-present
    // assert could not gate this read without false-failing a legitimate green. Residual risk is
    // closed only by the carrier deepEqual below plus the 200-status asserts; a proposal-persisted
    // sentinel read would close it fully and is out of this lane's proven-safe scope.
    const stepCount = ((await host.call('builder')).value.workspace?.documents?.[0]?.construction?.steps || []).length;
    assert.equal(stepCount, 0, 'the negatives host must start pristine before the ledger carrier is added');
    const sourceCaps = await capabilities(host);
    const statusChoice = (sourceCaps.caps.filterSourceInput?.choices || []).find((choice) => choice.fieldPath === 'status');
    assert.ok(statusChoice, 'status signed choice must exist for the ledger carrier');
    const builder = sourceCaps.builder;
    const outputId = builder.workspace.documents[0].output.id;
    const baseCols = (sourceCaps.caps.selectedStage?.columns || []).map((column) => ({ id: column.id, name: column.name, label: column.label, type: column.type }));
    const carrierFilter = { id: 'neg_filter_carrier', inputs: [{ kind: 'SOURCE_PROJECTION' }], operation: { kind: 'FILTER', filter: { columnId: 'filter-input_status', operator: 'EQUALS', values: [{ kind: 'STRING', string: 'final' }] } }, outputs: [...baseCols, { id: 'filter-input_status', name: 'status', label: 'Observation.status', type: 'string' }] };
    const proposal = await host.call('construction-proposals', {
      snapshotToken: builder.catalog.snapshotToken, expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest,
      outputId, limit: 25, changedStepId: carrierFilter.id, candidateConstruction: { version: 1, steps: [carrierFilter] },
      filterSources: [{ choiceId: statusChoice.choiceId, columnId: 'filter-input_status' }],
    });
    assert.equal(proposal.status, 200, `carrier filter proposal failed (${proposal.status})`);
    const applied = await host.cmd('carrier', [{ type: 'APPLY_CONSTRUCTION_PROPOSAL', outputId, proposalId: proposal.value.proposalId }]);
    assert.equal(applied.status, 200, `carrier filter apply failed (${applied.status})`);
    // Server-truth census (bs-62-2 `construction || {}` twin at the pre-fix :1173): the
    // deepEqual(kinds, ['FILTER']) below already refused a missing doc, but swallowing it into {}
    // misattributed the failure — name the unreadable server-truth read at the read site instead.
    const carrierBuilder = (await host.call('builder')).value;
    const carrierDoc = carrierBuilder?.workspace?.documents?.[0];
    const doc = carrierDoc?.construction;
    assert.ok(doc && typeof doc === 'object' && !Array.isArray(doc),
      `the carrier server-truth read returned no construction object after the ledger-apply; the FILTER-only census below would scan a missing doc: keys=${JSON.stringify(Object.keys(carrierDoc ?? {}))}`);
    const kinds = (doc.steps || []).map((s) => s.operation?.kind);
    assert.deepEqual(kinds, ['FILTER'], `the negatives host must carry exactly the ledger FILTER lane, got ${JSON.stringify(kinds)}`);
    carrierStep = (doc.steps || []).find((s) => s.operation?.kind === 'FILTER');
    assert.ok(carrierStep?.id === 'neg_filter_carrier', `carrier step missing (found ${carrierStep?.id})`);
  }
  const hostCaps = await capabilities(host, { stageId: 'neg_filter_carrier' });
  const unitChoice = hostCaps.choices.find((choice) => choice.fieldPath === 'valueQuantity.unit');
  const valueChoice = hostCaps.choices.find((choice) => choice.fieldPath === 'valueQuantity.value');
  const alreadyChoice = hostCaps.choices.find((choice) => choice.fieldPath === 'valueInteger');
  assert.ok(unitChoice && valueChoice, 'valueQuantity.unit/value choices must exist on the negatives host');
  assert.ok(alreadyChoice, "valueInteger must remain listable in unpivot choices so the already-projected guard is the live-line defence (pinned behaviour; the guard must refuse it as a melt lane)");

  // Decodes the `rc1.<payload>.<digest>` row-choice token (see capability/row_choice.go:257-278).
  // Without decodedPayload, a token that fails to decode makes embeddedSnapshot() null, and
  // `null !== hostToken` would satisfy the staleness check by accident — the zero-match twin of the
  // vacuous-pass class (bs-62-2 N2 analog, mirrored from verify-explorer-filter-row-source.mjs:707-712).
  const embeddedSnapshot = (token) => {
    try { return JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString()).snapshotToken ?? null; } catch { return null; }
  };
  const decodedPayload = (token) => {
    try { return JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString()); } catch { return null; }
  };
  // The host token is the reference for every token-level negative below: it must actually decode
  // and must embed the host catalog snapshot (row_choice.go sets SnapshotToken from the minting
  // snapshot), otherwise a null-embedded undecodable token could satisfy staleness comparisons by
  // accident (shape from the filter lane :727-729).
  assert.ok(decodedPayload(unitChoice.choiceId),
    `the host valueQuantity.unit choiceId must decode as a signed row-choice token, or every token-level negative below is unverifiable: ${String(unitChoice.choiceId).slice(0, 24)}`);
  assert.equal(embeddedSnapshot(unitChoice.choiceId), hostCaps.builder.catalog.snapshotToken,
    `the host valueQuantity.unit choiceId must embed the host catalog snapshot; a null/foreign snapshot would satisfy the staleness comparison by accident: ${JSON.stringify({ embedded: embeddedSnapshot(unitChoice.choiceId), host: hostCaps.builder.catalog.snapshotToken })}`);

  // Provably-stale choiceId from another fixture (provably different embedded snapshot).
  const staleCaps = await capabilities(staleCtx);
  const staleUnit = staleCaps.choices.find((choice) => choice.fieldPath === 'valueQuantity.unit');
  assert.ok(staleUnit, 'valueQuantity.unit choice must exist on the cross-snapshot fixture');
  assert.ok(decodedPayload(staleUnit.choiceId),
    `the cross-fixture valueQuantity.unit choiceId must decode as a signed row-choice token: ${String(staleUnit.choiceId).slice(0, 24)}`);
  assert.notEqual(embeddedSnapshot(staleUnit.choiceId), hostCaps.builder.catalog.snapshotToken,
    'cross-fixture choiceId must embed a snapshot distinct from the proposal host (provably stale)');

  const uuid = () => `${Date.now().toString(16)}-${Math.random().toString(16).slice(2, 8)}`;
  const stageColumns = (hostCaps.caps.selectedStage?.columns || []).map((column) => ({ id: column.id, name: column.name, label: column.label, type: column.type }));
  const idColumn = stageColumns.find((column) => column.label === 'ID');
  assert.ok(idColumn, 'carrier host stage must expose the ID public column for the public melt control');
  const meltRequest = (selections, mutate) => {
    assert.ok(carrierStep, 'the ledger carrier must be applied before negatives run');
    const stepId = `unpivot_neg_${uuid()}`;
    const mkey = `mkey_${uuid()}`, mval = `mv_${uuid()}`;
    const inputs = [{ columnId: idColumn.id, key: { kind: 'STRING', string: 'k1' } }];
    const bindings = [];
    for (const [index, selection] of (selections || []).entries()) {
      const columnId = `unpivot-input_${uuid()}_${index}`;
      inputs.push({ columnId, key: { kind: 'STRING', string: `k${index + 2}` } });
      bindings.push({ choiceId: selection.choiceId ?? selection, columnId });
    }
    const outputs = stageColumns.filter((column) => column.column !== idColumn.id && column.id !== idColumn.id)
      .concat([{ id: mkey, name: 'variable', label: 'Variable', type: 'string' }, { id: mval, name: 'value', label: 'Value', type: 'string' }]);
    const step = { id: stepId, inputs: [{ kind: 'STEP_OUTPUT', stepId: carrierStep.id }], operation: { kind: 'UNPIVOT', unpivot: { constructionId: stepId, inputs, keyOutputColumnId: mkey, valueOutputColumnId: mval, nullRowPolicy: 'PRESERVE' } }, outputs };
    mutate?.(step);
    const body = {
      snapshotToken: hostCaps.builder.catalog.snapshotToken, expectedDraftVersion: hostCaps.builder.draftVersion,
      expectedDraftDigest: hostCaps.builder.draftDigest, outputId: hostCaps.builder.workspace.documents[0].output.id,
      changedStepId: stepId, candidateConstruction: { version: 1, steps: [JSON.parse(JSON.stringify(carrierStep)), step] }, limit: 25,
    };
    if (bindings.length) body.unpivotSources = bindings;
    return body;
  };
  const expectCase = async (name, body, expected, caseOptions = {}) => {
    const response = await host.call('construction-proposals', body);
    const accepted = response.status === 200;
    const pass = expected === '200' ? accepted : (!accepted && String(response.status) === String(expected));
    report.negatives.push({ name, expected, status: response.status, accepted, bodyExcerpt: JSON.stringify(response.value).slice(0, 300) });
    assert.ok(pass, `${name}: expected ${expected}, got ${response.status} ${JSON.stringify(response.value).slice(0, 160)}`);
    if (caseOptions.diagnosticCode) {
      const encoded = JSON.stringify(response.value);
      assert.ok(encoded.includes(caseOptions.diagnosticCode), `${name}: response missing diagnostic code ${caseOptions.diagnosticCode}`);
      record(`negative-${name}`, { expected, status: response.status, diagnosticCode: caseOptions.diagnosticCode });
    } else if (caseOptions.record !== false) {
      record(`negative-${name}`, { expected, status: response.status });
    }
    return response;
  };

  // C0: public-only melt control (no source channel) must be accepted — the selected-columns
  // regression control proving the new channel changed nothing for public melts.
  const control = await expectCase('C0-public-control', meltRequest(null), '200', { record: false });
  assert.ok(!JSON.stringify(control.value).includes('"unpivot-input_'), 'C0: a public-only melt must not decorate any signed melt lanes');
  record('negative-C0-public-control', { expected: '200', status: 200, undecorated: true });

  // C1 is executed LAST (it is the only accepted-and-decorated case); compute its expectation here.
  {
    const decorated = await host.call('construction-proposals', meltRequest([unitChoice]));
    assert.equal(decorated.status, 200, `C1: signed control on a pristine host compiles the same way the journey applies it server-side; got ${decorated.status}`);
    assert.ok(JSON.stringify(decorated.value).includes('"valueQuantity.unit"'), 'C1: valid choice must decorate the candidate with the signed fieldPath');
    record('negative-C1-signed-decorates', { expected: '200', status: 200, decorated: true });
  }

  await expectCase('C2-forged', meltRequest([{ choiceId: 'rc1.' + 'a'.repeat(40) + '.' + 'b'.repeat(64) }]), '409', { diagnosticCode: 'INVALID_UNPIVOT_SOURCE_CHOICE' });
  await expectCase('C3-cross-snapshot', meltRequest([{ choiceId: staleUnit.choiceId }]), '409', { diagnosticCode: 'INVALID_UNPIVOT_SOURCE_CHOICE' });
  {
    const [prefix, payload, signature] = unitChoice.choiceId.split('.');
    const decoded = JSON.parse(Buffer.from(payload, 'base64url').toString());
    decoded.path = 'manually_tampered_path';
    const tampered = `${prefix}.${Buffer.from(JSON.stringify(decoded)).toString('base64url')}.${signature}`;
    assert.equal(decodedPayload(tampered)?.path, 'manually_tampered_path', 'C4: the payload tamper must actually land in the re-encoded token; a silent no-op would still answer 409 for the wrong reason (bs-62-2 filter :770 shape)');
    await expectCase('C4-tampered', meltRequest([{ choiceId: tampered }]), '409', { diagnosticCode: 'INVALID_UNPIVOT_SOURCE_CHOICE' });
  }
  {
    const body = meltRequest([unitChoice]);
    body.unpivotSources[0].choiceId = '';
    await expectCase('C5-empty-choiceId', body, '400', { diagnosticCode: 'MALFORMED_REQUEST' });
  }
  {
    const body = meltRequest([unitChoice]);
    delete body.unpivotSources[0].choiceId;
    await expectCase('C6-missing-choiceId', body, '400', { diagnosticCode: 'MALFORMED_REQUEST' });
  }
  {
    const body = meltRequest([unitChoice, unitChoice, { choiceId: valueChoice.choiceId }]);
    body.unpivotSources[1] = { ...body.unpivotSources[0] };
    await expectCase('C7-duplicate', body, '400', { diagnosticCode: 'MALFORMED_REQUEST' });
  }
  {
    const body = meltRequest([unitChoice]);
    body.unpivotSources[0].columnId = 'unpivot-input_someone_elses_col';
    await expectCase('C8-column-mismatch', body, '422', { diagnosticCode: 'UNPIVOT_SOURCE_COLUMN_MISMATCH' });
  }
  {
    const body = meltRequest([unitChoice, valueChoice]);
    body.candidateConstruction.steps[1].operation.unpivot.inputs = [
      { columnId: idColumn.id, key: { kind: 'STRING', string: 'k1' } },
      { columnId: body.unpivotSources[0].columnId, key: { kind: 'STRING', string: 'k2' } },
    ];
    await expectCase('C9-over-selection', body, '422', { diagnosticCode: 'UNPIVOT_SOURCE_COLUMN_MISMATCH' });
  }
  {
    // Public STRING melt input + signed NUMBER-kind lane => no physical type satisfies the melt.
    const body = meltRequest([valueChoice]);
    await expectCase('C10-types-incompatible', body, '422', { diagnosticCode: 'UNPIVOT_SOURCE_TYPES_INCOMPATIBLE' });
  }
  {
    // The F-DS1 twin: the field is already a projected column, so choosing it for a melt lane is
    // a named 422 — never a 500 receipt-contract crash (pinned live in probe10/12 runs).
    await expectCase('C11-already-projected-not-500', meltRequest([alreadyChoice]), '422', { diagnosticCode: 'UNPIVOT_SOURCE_FIELD_ALREADY_PROJECTED' });
  }
  {
    // Non-melt kind family witness: an UNPIVOT step with no payload cannot carry melt lanes.
    const body = meltRequest([unitChoice]);
    body.candidateConstruction.steps[1].operation = { kind: 'UNPIVOT' };
    await expectCase('C12-payloadless-requires-unpivot', body, '422', { diagnosticCode: 'UNPIVOT_SOURCE_REQUIRES_UNPIVOT' });
  }
  {
    // Contract behaviour (live-pinned): unpivotSources on a NON-UNPIVOT changed step are ignored,
    // and the signed fieldPath must never decorate the ignored candidate.
    const body = meltRequest([unitChoice]);
    const colId = body.candidateConstruction.steps[1].operation.unpivot.inputs[0].columnId;
    body.candidateConstruction.steps[1].operation = { kind: 'FILTER', filter: { columnId: colId, operator: 'EQUALS', values: [{ kind: 'STRING', string: 'x' }] } };
    const response = await host.call('construction-proposals', body);
    report.negatives.push({ name: 'C13-non-melt-step', expected: '200-undecorated', status: response.status });
    assert.equal(response.status, 200, `C13: live contract is ignore-not-refuse for non-UNPIVOT steps, got ${response.status}`);
    assert.ok(!JSON.stringify(response.value).includes('"valueQuantity.unit"'), 'C13: ignored unpivotSources must never decorate the signed fieldPath');
    record('contract-C13-non-melt-step-undecorated', { status: response.status, undecorated: true });
  }
  {
    // A brand-new (unpersisted) filter carrier cannot host signed melt inputs: the choices are
    // bound to accepted receipt stages, and a candidate-local stage ref is a named 409.
    const cols = stageColumns.map((column) => ({ id: column.id, name: column.name, label: column.label, type: column.type }));
    const fs = `f_${uuid()}`;
    const filterStep = { id: fs, inputs: [{ kind: 'SOURCE_PROJECTION' }], operation: { kind: 'FILTER', filter: { columnId: 'filter-input_status', operator: 'EQUALS', values: [{ kind: 'STRING', string: 'final' }] } }, outputs: [...cols, { id: 'filter-input_status', name: 'status', label: 'Observation.status', type: 'string' }] };
    const stepId = `unpivot_${uuid()}`;
    const synth = `unpivot-input_${uuid()}`;
    const mkey = `mkey_${uuid()}`, mval = `mv_${uuid()}`;
    const melt = { id: stepId, inputs: [{ kind: 'STEP_OUTPUT', stepId: fs }], operation: { kind: 'UNPIVOT', unpivot: { constructionId: stepId, inputs: [{ columnId: synth, key: { kind: 'STRING', string: 'k' } }], keyOutputColumnId: mkey, valueOutputColumnId: mval, nullRowPolicy: 'PRESERVE' } },
      outputs: cols.concat([{ id: mkey, name: 'variable', label: 'Variable', type: 'string' }, { id: mval, name: 'value', label: 'Value', type: 'string' }]) };
    const response = await host.call('construction-proposals', {
      snapshotToken: hostCaps.builder.catalog.snapshotToken, expectedDraftVersion: hostCaps.builder.draftVersion,
      expectedDraftDigest: hostCaps.builder.draftDigest, outputId: hostCaps.builder.workspace.documents[0].output.id,
      limit: 25, changedStepId: stepId, candidateConstruction: { version: 1, steps: [filterStep, melt] },
      unpivotSources: [{ choiceId: unitChoice.choiceId, columnId: synth }],
    });
    const code = response.value?.diagnostics?.[0]?.code;
    report.negatives.push({ name: 'C14-new-carrier', expected: '409', status: response.status, bodyExcerpt: JSON.stringify(response.value).slice(0, 200) });
    assert.equal(response.status, 409, `C14: unpersisted candidate carrier must be refused (probe12 C), got ${response.status}`);
    assert.equal(code, 'STALE_STAGE_REFERENCE', `C14: expected the named stale-stage refusal, got ${code}`);
    record('negative-C14-new-carrier-stale-stage', { expected: '409', status: 409, diagnosticCode: 'STALE_STAGE_REFERENCE' });
  }
  {
    // Operator decision #4 (server cd39eeaf): a signed date-family melt must answer with the
    // named 422 refusal — pre-fix the accepted binary 200-decorated exactly this request
    // (authority /tmp/opencode/jrev/probe-date-melt-dp4.log). The token must be genuine, so it
    // is minted through the filter channel's RowChoice signing (unpivot discovery now hides the
    // date family and cannot mint it). A zero-code-match response fails loudly, never vacuously.
    const filterChoices = hostCaps.caps.filterSourceInput?.choices || [];
    const dateChoiceToken = filterChoices.find((choice) => choice.fieldPath === 'effectiveDateTime' && choice.choiceId);
    assert.ok(dateChoiceToken, `C15 requires a genuine signed effectiveDateTime token from the filter channel; got fieldPaths ${JSON.stringify(filterChoices.map((choice) => choice.fieldPath)).slice(0, 400)}`);
    const body = meltRequest([{ choiceId: dateChoiceToken.choiceId }]);
    const response = await host.call('construction-proposals', body);
    const encoded = JSON.stringify(response.value);
    report.negatives.push({ name: 'C15-signed-date-melt-refused', expected: '422', status: response.status, bodyExcerpt: encoded.slice(0, 300) });
    assert.equal(response.status, 422, `C15: a signed date_time melt must be the named 422 refusal, got ${response.status} ${encoded.slice(0, 240)}`);
    assert.ok(encoded.includes('UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD'), `C15: zero matches for the server-named refusal code UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD: ${encoded.slice(0, 400)}`);
    assert.ok(!encoded.includes('previewStatus'), `C15: a refused date melt must never yield a preview: ${encoded.slice(0, 240)}`);
    record('negative-C15-signed-date-melt-refused', { expected: '422', status: 422, diagnosticCode: 'UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD' });
  }
  {
    // Payload-level hide witness on the proposal host (step 0/2 cover the journey payload and
    // rendered optgroup): zero date-family melt choices while the permitted string lanes stay
    // listed, so the hide is selective and never vacuous.
    const leaked = (hostCaps.choices || []).filter((choice) => ['date', 'date_time'].includes(String(choice.logicalType).toLowerCase()) || choice.fieldPath === 'effectiveDateTime');
    assert.equal(leaked.length, 0, `C16: the melt payload leaked date-family choices on the negatives host: ${JSON.stringify(leaked)}`);
    assert.ok((hostCaps.choices || []).some((choice) => choice.fieldPath === 'valueQuantity.unit'), 'C16: the payload hide must stay selective — the unit lane must remain listed');
    record('discovery-hides-date-family', { negativesHostPayloadZero: true, listedOnNegativesHost: (hostCaps.choices || []).map((choice) => choice.fieldPath) });
  }
}
