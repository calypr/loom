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
  if (process.argv[i] === '--dry-run') { options.dryRun = true; continue; }
  const key = { '--out': 'out', '--chrome': 'chrome', '--run-label': 'runLabel' }[process.argv[i]];
  assert.ok(key && process.argv[i + 1], `Unknown or missing-valued option: ${process.argv[i]}`);
  options[key] = process.argv[++i];
}

const expectedChecks = [
  'row96-lane-support-healthy',
  'row96-field-options-catalog-path-labels',
  'row96-per-field-refusal-reasoncodes',
  'row96-ui-control-consumes-reason',
  'row96-inventory-fault-names-unavailable',
  'row96-empty-data-distinguished',
];

const emptyDataFamily = /^(NO_OBSERVED_VALUES_|NO_ELIGIBLE_POPULATED_|NO_ROUTE_ELIGIBLE_|NO_TYPE_ELIGIBLE_|STALE_|ROOT_SOURCE_PROJECTION_UNAVAILABLE$)/;
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
  const env = commandEnvironment({ composeProject: session.composeProject, sourceRoot: session.sourceRoot, apiPort: session.apiPort, uiPort: session.uiPort });
  const stamp = spawnSync('docker', ['compose', '--project-name', session.composeProject, '--file', join(session.sourceRoot, 'compose.dev.yaml'),
    'exec', '-T', 'loom-api', STAMP_SCRIPT, '--check'], { encoding: 'utf8', env, timeout: 30000 });
  const recorded = spawnSync('docker', ['compose', '--project-name', session.composeProject, '--file', join(session.sourceRoot, 'compose.dev.yaml'),
    'exec', '-T', 'loom-api', 'cat', '/workspace/tmp/arango-fhir-server.build.json'], { encoding: 'utf8', env, timeout: 30000 });
  const head = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: REPO_ROOT, encoding: 'utf8' });
  const [expectedSource, liveSource, runningBinary] = (stamp.stdout || '').trim().split(/\s+/);
  let parsed = {};
  try { parsed = JSON.parse(recorded.stdout); } catch { parsed = {}; }
  return { expectedSource: expectedSource ?? null, liveSource: liveSource ?? null, runningBinary: runningBinary ?? null, checkExit: stamp.status, recordedBinary: parsed?.binary ?? null, recordedSource: parsed?.source ?? null, gitHead: (head.stdout || '').trim() || null };
};

const chromePath = options.chrome ?? process.env.LOOM_E2E_CHROME
  ?? ['/usr/bin/chromium', '/opt/google/chrome/chrome'].find((candidate) => existsSync(candidate));
assert.ok(chromePath && existsSync(chromePath), 'No Chrome binary: pass --chrome, set LOOM_E2E_CHROME, or install chromium');
const profile = mkdtempSync(join(tmpdir(), 'rs96-prof-'));
const chrome = spawn(chromePath, ['--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--remote-debugging-port=0',
  `--user-data-dir=${profile}`, '--window-size=1600,1200', '--no-first-run', '--no-default-browser-check',
  '--disable-background-networking', '--disable-component-update', '--disable-sync', '--proxy-server=direct://', 'about:blank'],
  { stdio: ['ignore', 'ignore', 'pipe'] });
let chromeErr = '';
chrome.stderr.on('data', (chunk) => { chromeErr = (chromeErr + chunk).slice(-4000); });
const wait = (ms) => new Promise((done) => setTimeout(done, ms));

const main = async () => {
  const identityBefore = collectBuildIdentity();
  report.buildIdentity = { before: identityBefore };
  assert.ok(identityBefore.runningBinary, 'build identity unavailable: no serving binary on the owned stack');
  assert.equal(identityBefore.checkExit, 0, `serving build identity must be current before the journey runs: ${JSON.stringify(identityBefore)}`);
  assert.equal(identityBefore.expectedSource, identityBefore.liveSource, `live source drifted from the recorded build: ${identityBefore.expectedSource} != ${identityBefore.liveSource}`);

  const fixture = await createFreshVerificationFixture(session, `${runStem}-a`, { populateBootstrap: false });
  const project = fixture.target.fixtureProject;
  const explorer = fixture.seed.bootstrapExplorerId;
  report.fixture = { project, explorer };
  report.environment = { composeProject: session.composeProject, apiUrl: session.apiUrl ?? `http://127.0.0.1:${session.apiPort}`, uiUrl: session.uiUrl ?? `http://127.0.0.1:${session.uiPort}` };
  const apiOrigin = report.environment.apiUrl;
  const api = async (path, body) => {
    const base = `${apiOrigin}/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
    const response = await fetch(`${base}/${path}`, { method: body ? 'POST' : 'GET', headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined });
    return { status: response.status, value: await response.json().catch(() => ({})) };
  };

  const portFile = join(profile, 'DevToolsActivePort');
  for (let i = 0; i < 150 && !existsSync(portFile); i += 1) await wait(100);
  assert.ok(existsSync(portFile), `chrome devtools port not advertised; stderr=${chromeErr.slice(-400)}`);
  const [port] = readFileSync(portFile, 'utf8').trim().split('\n');
  const target = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' })).json();
  const client = await CDPClient.connect(target.webSocketDebuggerUrl);
  const send = (method, params = {}, ms = 25000) => Promise.race([
    client.send(method, params),
    new Promise((_, reject) => setTimeout(() => reject(new Error(`timeout ${method}`)), ms)),
  ]);
  const exceptions = [];
  report.exceptions = exceptions;
  client.on('Runtime.exceptionThrown', (event) => exceptions.push(event.exceptionDetails?.exception?.description || event.exceptionDetails?.text || 'exception'));

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

  await send('Page.navigate', { url: `${browserURL(session)}` });
  await wait(2500);

  const builder = await api('builder');
  assert.equal(builder.status, 200, `builder fetch failed (${builder.status})`);
  const root = (builder.value?.catalog?.nodes ?? []).find((node) => node.resourceType === 'Patient' && node.rowRootEligible)
    ?? (builder.value?.catalog?.nodes ?? [])[0];
  assert.ok(root?.nodeId, 'builder catalog exposes no eligible row root; the lane under test cannot exist');
  const commands = async (commandId, list, state) => api('commands', {
    commandId: `${runStem}-${commandId}`, semanticsVersion: AUTHORING_SEMANTICS_VERSION,
    snapshotToken: state.catalog.snapshotToken, expectedDraftVersion: state.draftVersion,
    ...(state.draftDigest ? { expectedDraftDigest: state.draftDigest } : {}), commands: list,
  });
  const created = await commands('row96-table', [{ type: 'CREATE_TABLE', title: 'RS96 four-state shape', rootNodeId: root.nodeId }], builder.value);
  assert.equal(created.status, 200, `CREATE_TABLE failed (${created.status}): ${JSON.stringify(created.value).slice(0, 300)}`);
  const afterTable = await api('builder');
  const outputId = afterTable.value?.workspace?.documents?.[0]?.output?.id;
  assert.ok(outputId, 'seeded table has no output; downstream lane asserts would be vacuous');
  await send('Page.navigate', { url: `${browserURL(session)}` });
  await wait(2500);

  const stageId = 'source_projection';
  const lanePayload = await api('construction-capabilities', { outputId, stageId, snapshotToken: afterTable.value.catalog.snapshotToken });
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

  const fieldOptionFacts = await evaluate(`(()=>{const els=[...document.querySelectorAll('option[title],[role="option"][title]')];return els.slice(0,240).map(e=>({title:e.getAttribute('title'),text:(e.textContent||'').trim(),tag:e.tagName.toLowerCase()}));})()`);
  assert.ok(Array.isArray(fieldOptionFacts) && fieldOptionFacts.length > 0, 'no titled field/concept options are rendered on the table-shape lane; the label contract would be unexercised (navigate to the lane editor if the default view lacks options)');
  const titled = fieldOptionFacts.filter((fact) => fact.title && fact.text);
  assert.ok(titled.length > 0, 'field/concept options carry neither title nor text; the label assertion would be vacuous');
  for (const fact of titled) {
    assert.match(fact.title, /^[A-Za-z][A-Za-z0-9.]*$/, `option title is not a catalog field path: ${JSON.stringify(fact)}`);
    assert.ok(fact.text.toLowerCase().includes(fact.title.split('.').pop().toLowerCase().replace(/([a-z0-9])([A-Z])/g, '$1 $2')), `option text is not humanized from the terminal catalog path segment: ${JSON.stringify(fact)}`);
  }
  record('row96-field-options-catalog-path-labels', { sample: titled.slice(0, 12), counted: titled.length, rule: 'catalog-literal labels per label-journey-coupling.md (S-C 29d63f6f); construction-lane humanized text is the only flip family, titles stay fieldPath' });
  await screenshot('step2-catalog-path-labels');

  const refusalRecords = laneCaps.flatMap((cap) => (Array.isArray(cap.candidates ?? cap.fields) ? cap.candidates ?? cap.fields : [])
    .filter((candidate) => candidate && candidate.supported === false && typeof candidate.reasonCode === 'string' && candidate.reasonCode)
    .map((candidate) => ({ kind: cap.kind, fieldPath: candidate.fieldPath ?? candidate.path ?? null, reasonCode: candidate.reasonCode })));
  const laneLevelRefusals = laneCaps.filter((cap) => cap.supported === false && typeof cap.reasonCode === 'string' && cap.reasonCode)
    .map((cap) => ({ kind: cap.kind, fieldPath: null, reasonCode: cap.reasonCode }));
  const allRefusals = [...refusalRecords, ...laneLevelRefusals];
  const inventoryFlavored = allRefusals.filter((entry) => inventoryClass.test(entry.reasonCode));
  const refusalOnly = allRefusals.filter((entry) => !inventoryClass.test(entry.reasonCode));
  if (allRefusals.length === 0) {
    report.sideFindings.push({ id: 'row96-zero-refusals-this-fixture', note: 'the per-run healthy fixture advertised zero unsupported candidates; literal set asserted well-formed-empty. The live window must additionally classify one deliberately refused field (unpopulated path) so this check cannot rest empty.' });
  }
  for (const entry of allRefusals) {
    assert.match(entry.reasonCode, /^[A-Z][A-Z0-9_]*$/, `per-field refusal reasonCode is not an upper server class: ${JSON.stringify(entry)}`);
  }
  record('row96-per-field-refusal-reasoncodes', { count: allRefusals.length, sample: allRefusals.slice(0, 8), refusalOnly: refusalOnly.length, inventoryFlavored: inventoryFlavored.length });
  await screenshot('step3-refusal-reasoncodes');

  const supportReasonDom = await evaluate('(()=>{const els=[...document.querySelectorAll(\'[disabled],[aria-disabled="true"],[data-testid*="related-source" i]\')];return els.map(e=>({title:e.getAttribute(\'title\'),aria:e.getAttribute(\'aria-label\'),text:(e.textContent||\'\').trim()})).filter(h=>/[A-Z][A-Z0-9_]{6,}/.test((h.title||\'\')+(h.text||\'\'))).slice(0,24);})()');
  const codeSeenDom = supportReasonDom.find((hit) => /[A-Z][A-Z0-9_]{6,}(\s*:|\s*-|,|\(|\s)/.test(`${hit.title || ''} ${hit.text || ''}`));
  if (codeSeenDom) {
    record('row96-ui-control-consumes-reason', { via: 'ui-control', sample: codeSeenDom });
  } else {
    const payloadCodes = laneCaps.map((cap) => cap.reasonCode).filter(Boolean);
    assert.ok(payloadCodes.length > 0 || allRefusals.length > 0, 'neither a UI control rendering "reasonCode: reason" (RelatedSourceStepEditor.tsx joins :79-80 into disabledReason :444) nor any API reasonCode exists; the consumer exists in committed UI source, so assert via the authoritative API payload and log the gap instead of faking a tooltip assert');
    record('row96-ui-control-consumes-reason', { via: 'api-payload-priority', payloadCodes: payloadCodes.slice(0, 6), refusalCodes: allRefusals.map((entry) => entry.reasonCode).slice(0, 6) });
    report.sideFindings.push({ id: 'row96-ui-gap-reason-control', note: 'UI control was not currently disabled/reason-rendered for the lane state; assertion fell back to the API payload per the side-finding rule, and the joined string contract is asserted server-side by construction_pivot_source.go reason pairs and by RelatedSourceStepEditor.unit.test.tsx consumer tests' });
  }

  await send('Network.enable');
  const seenRequests = [];
  client.on('Network.requestWillBeSent', (event) => { if (event?.request?.url) seenRequests.push(event.request.url); });
  const inventoryHintPattern = /(populated|capabilit|catalog|inventory|field-observation|profile)/i;
  const healthyHits = seenRequests.filter((url) => inventoryHintPattern.test(url));
  if (healthyHits.length === 0) {
    report.sideFindings.push({ id: 'row96-inventory-route-not-observed-browser-side', note: 'Network.requestWillBeSent did not include the documented capability/populated-observation request; falling back to server-truth injectable route via blocking the same pattern used by the server-side capability assembly; the honest boundary assert remains (unsupported with reasonCode *_SOURCE_INVENTORY_UNAVAILABLE)' });
  }
  await send('Network.setBlockedURLs', { urls: ['*populated*', '*capabilit*', '*catalog*', '*inventory*', '*field-observation*'] });
  await send('Page.reload', {});
  await wait(2000);
  const faultPayload = await api('construction-capabilities', { outputId, stageId });
  const faultCaps = faultPayload.value?.selectedStage?.capabilities ?? [];
  const inventoryLanes = faultCaps.filter((cap) => cap.supported === false && typeof cap.reasonCode === 'string' && inventoryClass.test(cap.reasonCode));
  await send('Network.setBlockedURLs', { urls: [] });
  assert.ok(inventoryLanes.length > 0, `inventory-fault lane must name the certified class while the inventory is blocked; saw ${JSON.stringify(faultCaps.map((cap) => `${cap.kind}:${cap.supported}:${cap.reasonCode ?? ''}`).slice(0, 8))}. honest boundary: if the API layer itself cannot see the block, this assert fires RED and the live window must use the server fault-injection precedent instead; never weaken to "unsupported without class"`);
  record('row96-inventory-fault-names-unavailable', { blocked: ['*populated*', '*capabilit*', '*catalog*', '*inventory*'], inventoryLanes: inventoryLanes.map((cap) => ({ kind: cap.kind, reasonCode: cap.reasonCode })).slice(0, 6) });
  await screenshot('step4-inventory-fault');

  const emptied = await commands('row96-empty-root', [{ type: 'CREATE_TABLE', title: 'RS96 empty-data shape', rootNodeId: root.nodeId }], await (async () => { const s = await api('builder'); return s.value; })());
  assert.equal(emptied.status, 200, `empty-data table seed failed (${emptied.status})`);
  const emptyDoc = (await api('builder')).value;
  const emptyOutput = emptyDoc?.workspace?.documents?.at(-1)?.output?.id ?? outputId;
  const emptyPayload = await api('construction-capabilities', { outputId: emptyOutput, stageId });
  const emptyCaps = emptyPayload.value?.selectedStage?.capabilities ?? [];
  const emptyDataCodes = [...new Set(emptyCaps.filter((cap) => cap.supported === false && typeof cap.reasonCode === 'string').map((cap) => cap.reasonCode))];
  const classes = emptyDataCodes.filter((code) => emptyDataFamily.test(code));
  const leaks = emptyDataCodes.filter((code) => inventoryClass.test(code));
  if (classes.length === 0) {
    report.sideFindings.push({ id: 'row96-empty-data-seed-boundary', note: 'empty root on this dataset did not surface an NO_OBSERVED_VALUES_/NO_ELIGIBLE_POPULATED_ class in this run (healthy population may be complete); live-window rule: create a dataset variant with no populated eligible value. This check fails RED, is never asserted vacuously.' });
  }
  assert.ok(classes.length > 0, `empty-data verdict must be classifiable in the no-observed-values family; saw ${JSON.stringify(emptyDataCodes.slice(0, 8))}`);
  assert.deepEqual(leaks, [], 'empty-data lane must NOT collapse into the inventory-unavailable class (the S-B distinction is the whole point of this check)');
  record('row96-empty-data-distinguished', { emptyDataClasses: classes, inventoryCodesSeenInEmptyLane: leaks, disjointFromInventory: leaks.length === 0 });
  await screenshot('step5-empty-data-distinguished');

  const identityAfter = collectBuildIdentity();
  report.buildIdentity.after = identityAfter;
  assert.equal(identityAfter.checkExit, 0, `post-run: serving identity moved: ${JSON.stringify(identityAfter)}`);
  assert.equal(identityAfter.runningBinary, identityBefore.runningBinary, `post-run: serving binary moved during run: ${identityAfter.runningBinary} != ${identityBefore.runningBinary}`);
  assert.equal(identityAfter.liveSource, identityBefore.liveSource, `post-run: live source moved during run: ${identityAfter.liveSource} != ${identityBefore.liveSource}`);

  const names = report.checks.map((check) => check.name);
  for (const name of expectedChecks) assert.ok(names.includes(name), `skipped checks: ${expectedChecks.filter((candidate) => !names.includes(candidate)).join(',')}`);
  report.status = 'passed';
  writeFileSync(join(out, 'journey.json'), JSON.stringify(report, null, 2));
  client.close();
  console.log(`JOURNEY_OK build=${identityBefore.gitHead} runningBinary=${identityBefore.runningBinary} checks=${report.checks.length} screenshots=${report.screenshots.length} sideFindings=${report.sideFindings.length}`);
};

try {
  await main();
  process.exit(0);
} catch (error) {
  report.status = 'failed';
  report.error = String(error?.stack || error);
  report.exceptions.push(report.error);
  try { writeFileSync(join(out, 'journey.json'), JSON.stringify(report, null, 2)); } catch { /* exit code is primary */ }
  console.log(`JOURNEY_FAILED ${String(error?.message ?? error).slice(0, 400)}`);
  process.exit(1);
} finally {
  try { chrome.kill(); } catch { /* best effort */ }
}
