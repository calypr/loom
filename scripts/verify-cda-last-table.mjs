import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdir, realpath, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureApiBuildFreeze, checkContainerApiBuildStamp } from './lib/api-build-freeze.mjs';
import { assertVisibleRowsMatchOracle, readNDJSONResourceIdentityOracle } from './lib/ndjson-resource-oracle.mjs';
import { assertOwnedCdaTarget } from './lib/owned-cda-target.mjs';
import { launchBrowser } from './lib/playwright-browser.mjs';
import { performAction } from './lib/playwright-actions.mjs';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';

const project = process.env.LOOM_CDA_PROJECT;
const explorer = `last-table-command-qa-${Date.now()}`;
const apiOrigin = process.env.LOOM_CDA_API_ORIGIN;
const uiOrigin = process.env.LOOM_CDA_UI_ORIGIN;
const apiContainer = process.env.LOOM_CDA_API_CONTAINER;
const composeProject = process.env.LOOM_CDA_COMPOSE_PROJECT;
const fixtureDir = process.env.LOOM_CDA_FIXTURE_DIR;
const sourceRoot = resolve(fileURLToPath(new URL('..', import.meta.url)));
const generation = 'cda-fhir-v1';
const evidence = process.argv[2] ?? `/tmp/loom-last-table-command-${Date.now()}`;
const root = project ? `/api/v1/projects/${encodeURIComponent(project)}/explorers` : '';
const base = `${root}/${encodeURIComponent(explorer)}/authoring/v2`;
const report = { status: 'running', explorer, project, target: { apiOrigin, uiOrigin, apiContainer, composeProject }, requests: [], nativeChecks: [], errors: [], sourceFingerprint: {} };
await mkdir(evidence, { recursive: true });
let state;
let browser;
let sourceFreeze;
let apiBuildFreeze;
let apiBuildBefore;

function extractBuildIdentity(observation) {
  assert.equal(observation.status, 0, 'API build stamp check must succeed');
  const values = observation.stdout.trim().match(/^([a-f0-9]{64})\s+([a-f0-9]{64})\s+([a-f0-9]{64})$/i);
  assert(values, 'API build stamp must contain three SHA-256 identities');
  return values.slice(1).join(':').toLowerCase();
}

const api = async (path, body) => {
  assert(path === root || path.startsWith(`${root}/${explorer}/`));
  const response = await fetch(apiOrigin + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': randomUUID() },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  report.requests.push({ path, status: response.status, ...(response.ok ? {} : { response: value }) });
  assert(response.ok, `${path}: ${response.status} ${JSON.stringify(value)}`);
  return value;
};
const command = async commands => {
  const result = await api(`${base}/commands`, {
    commandId: randomUUID(), semanticsVersion: state.workspace?.semanticsVersion ?? 10,
    snapshotToken: state.catalog.snapshotToken,
    expectedDraftVersion: state.draftVersion, expectedDraftDigest: state.draftDigest, commands,
  });
  assert(Array.isArray(result.workspace.documents), 'Command responses must serialize documents as an array, including an empty workspace');
  assert(Array.isArray(result.workspace.tabs), 'Command responses must serialize tabs as an array, including an empty workspace');
  state = await api(`${base}/builder`);
  return result;
};
try {
  assert(fixtureDir, 'Set LOOM_CDA_FIXTURE_DIR to the independently loaded CDA-FHIR/META source directory.');
  const fixtureRoot = await realpath(fixtureDir);
  const specimenPath = await realpath(join(fixtureRoot, 'Specimen.ndjson'));
  report.target.fixtureDirectory = fixtureRoot;
  report.target.ownership = await assertOwnedCdaTarget({
    project, apiOrigin, uiOrigin, apiContainer, composeProject, sourceRoot,
    arangoContainer: process.env.LOOM_ARANGO_CONTAINER,
  });
  sourceFreeze = await captureSourceFreeze(sourceRoot);
  report.sourceFreeze = { watchedFileCount: sourceFreeze.watchedFileCount, before: sourceFingerprint(sourceRoot) };
  let firstBuildObservation;
  apiBuildFreeze = await captureApiBuildFreeze(async () => {
    firstBuildObservation = await checkContainerApiBuildStamp(apiContainer);
    return firstBuildObservation;
  });
  apiBuildBefore = extractBuildIdentity(firstBuildObservation);
  report.apiBuildIdentity = { before: apiBuildBefore };
  const specimenOracle = await readNDJSONResourceIdentityOracle({ path: specimenPath, project, generation, resourceType: 'Specimen' });
  report.oracle = {
    path: specimenOracle.path,
    sha256: specimenOracle.sha256,
    sourceCount: specimenOracle.count,
    ordering: specimenOracle.ordering,
  };
  await api(root, { name: explorer, title: 'Last table command QA' });
  state = await api(`${base}/builder`);
  assert.equal(state.catalog.generation, generation);
  assert.equal(state.workspace?.documents.length ?? 0, 0);
  const node = state.catalog.nodes.find(candidate => candidate.resourceType === 'Specimen');
  assert(node, 'The CDA generation must expose Specimen');
  await command([{ type: 'CREATE_TABLE', rootNodeId: node.nodeId, title: 'Only table' }]);
  assert.equal(state.workspace.documents.length, 1);
  const before = structuredClone(state.workspace);
  const outputId = before.documents[0].output.id;
  await command([{ type: 'DELETE_TABLE', outputId }]);
  assert.equal(state.workspace.documents.length, 0);
  assert.equal(state.workspace.tabs.length, 0);
  assert(state.previousDraftRevisionId, 'Deletion must retain a revision for Undo');
  const previousRevision = state.previousDraftRevisionId;
  state = await api(`${base}/builder`);
  assert.equal(state.workspace.documents.length, 0, 'Empty workspace must persist after reload');
  await command([{ type: 'RESTORE_DRAFT_REVISION', draftRevisionId: previousRevision }]);
  assert.deepEqual(state.workspace, before, 'Undo must restore the only table exactly');
  state = await api(`${base}/builder`);
  assert.deepEqual(state.workspace, before, 'Undo must persist after reload');
  const field = state.catalog.candidates.find(candidate => candidate.nodeId === node.nodeId && candidate.fieldPath === 'id');
  assert(field);
  await command([{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: field.candidateId, projectionMode: 'VALUE', initialPresentation: 'TABLE', title: 'Specimen ID' }]);
  const nativeBaseline = structuredClone(state.workspace);

  browser = await launchBrowser({ evidence, appOrigins: [uiOrigin, apiOrigin], noAuth: true });
  const { page } = browser;
  await page.setViewportSize({ width: 1440, height: 1000 });
  const uiURL = `${uiOrigin}/?project=${encodeURIComponent(project)}&explorer=${encodeURIComponent(explorer)}&mode=builder`;
  await page.goto(uiURL, { waitUntil: 'domcontentloaded' });
  const table = page.locator(`[data-testid="construction-table-${outputId}"]`);
  await table.waitFor({ state: 'visible', timeout: 30000 });
  await performAction(report, 'open only table', table, target => target.click());
  const rename = page.locator(`[data-testid="construction-rename-table-${outputId}"]`);
  await rename.waitFor({ state: 'visible', timeout: 30000 });
  assert.equal(await rename.isEnabled(), true, 'The selected table rename action must be enabled');
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready', null, { timeout: 30000 });
  const renderedRows = async () => page.locator('[data-testid="preview-table-scroll"] [role="row"]').evaluateAll(rows => rows.slice(1).map(row => ({
    ordinal: Number(row.firstElementChild?.innerText.trim()),
    cells: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()),
  })).filter(row => row.cells.length));
  const captureFullPreviewWindow = async phase => {
    const scroll = page.getByTestId('preview-table-scroll');
    const grid = scroll.getByRole('table');
    const metrics = await scroll.evaluate(element => ({
      initialScrollTop: element.scrollTop,
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
      maxScrollTop: Math.max(0, element.scrollHeight - element.clientHeight),
    }));
    const visible = new Map();
    const collect = async () => {
      for (const row of await renderedRows()) {
        const previous = visible.get(row.ordinal);
        if (previous) assert.deepEqual(row, previous, `${phase}: a virtual row identity changed while scrolling`);
        visible.set(row.ordinal, row);
      }
    };
    await collect();
    const step = Math.max(1, Math.floor(metrics.clientHeight * 0.65));
    let scrollTop = metrics.initialScrollTop;
    let windows = 0;
    while (visible.size < 25 && scrollTop < metrics.maxScrollTop - 1) {
      assert(++windows <= 30, `${phase}: vertical preview sweep exceeded 30 native scroll inputs`);
      const previous = scrollTop;
      await scroll.hover({ timeout: 5000 });
      await page.mouse.wheel(0, Math.min(step, metrics.maxScrollTop - previous));
      await page.waitForFunction(({ prior, max }) => {
        const element = document.querySelector('[data-testid="preview-table-scroll"]');
        return Boolean(element && (element.scrollTop > prior + 0.5 || element.scrollTop >= max - 1));
      }, { prior: previous, max: metrics.maxScrollTop }, { timeout: 2000 });
      scrollTop = await scroll.evaluate(element => element.scrollTop);
      assert(scrollTop > previous + 0.5 || scrollTop >= metrics.maxScrollTop - 1,
        `${phase}: native wheel did not advance the virtualized preview`);
      await collect();
    }
    const rows = [...visible.values()].sort((left, right) => left.ordinal - right.ordinal);
    assert.equal(rows.length, 25, `${phase}: the full preview window must expose 25 exact visible row identities`);
    assert.deepEqual(rows.map(row => row.ordinal), Array.from({ length: 25 }, (_, index) => index + 1),
      `${phase}: the preview must expose every ordinal from 1 through 25`);

    // Restore the visible top with ordinary wheel input so each lifecycle comparison uses the same viewport.
    let current = scrollTop;
    while (current > metrics.initialScrollTop + 0.5) {
      const previous = current;
      await scroll.hover({ timeout: 5000 });
      await page.mouse.wheel(0, -Math.min(step, previous - metrics.initialScrollTop));
      await page.waitForFunction(({ prior, initial }) => {
        const element = document.querySelector('[data-testid="preview-table-scroll"]');
        return Boolean(element && (element.scrollTop < prior - 0.5 || element.scrollTop <= initial + 0.5));
      }, { prior: previous, initial: metrics.initialScrollTop }, { timeout: 2000 });
      current = await scroll.evaluate(element => element.scrollTop);
      assert(current < previous - 0.5 || current <= metrics.initialScrollTop + 0.5,
        `${phase}: native wheel did not restore the preview scroll position`);
    }
    assert.equal(await grid.getAttribute('aria-rowcount'), '26', `${phase}: virtualized preview must retain 25 data rows plus a header`);
    return rows;
  };
  const originalRows = await renderedRows();
  assert(originalRows.length > 0, 'The source table must render real data before deletion');
  const previewRowCount = await page.locator('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-rowcount');
  const oracleRows = assertVisibleRowsMatchOracle({ rows: originalRows, sourceIds: specimenOracle.ids, ariaRowCount: previewRowCount });
  const fullInitialRows = await captureFullPreviewWindow('initial-preview');
  assert.deepEqual(assertVisibleRowsMatchOracle({ rows: fullInitialRows, sourceIds: specimenOracle.ids, ariaRowCount: previewRowCount }),
    Array.from({ length: 25 }, (_, index) => ({ ordinal: index + 1, cells: [specimenOracle.ids[index]] })),
    'All 25 initial data rows must match the independent CDA source window');
  report.oracle.visibleRows = fullInitialRows;
  report.oracle.previewRowCount = Number(previewRowCount) - 1;

  const started = Date.now();
  await performAction(report, 'delete last table', page.locator('[data-testid="construction-delete-table"]'), target => target.click());
  await page.getByText('Build your first table', { exact: false }).waitFor({ state: 'visible', timeout: 5000 });
  state = await api(`${base}/builder`);
  assert.equal(state.workspace.documents.length, 0);
  assert.equal(state.workspace.tabs.length, 0);
  const durationMs = Date.now() - started;
  assert(durationMs <= 5000, `Deleting the last table took ${durationMs} ms`);
  report.nativeChecks.push({ name: 'delete-last-table-to-empty-creator', durationMs });

  const restoreStart = Date.now();
  await performAction(report, 'undo last table deletion', page.locator('[data-testid="construction-undo"]'), target => target.click());
  await table.waitFor({ state: 'visible', timeout: 5000 });
  state = await api(`${base}/builder`);
  assert.deepEqual(state.workspace, nativeBaseline);
  await page.waitForFunction(expected => {
    const element = document.querySelector('[data-testid="construction-preview"]');
    return element?.dataset.previewStatus === 'ready'
      && Number(element.dataset.currentDraftVersion) === expected.version
      && element.dataset.currentDraftDigest === expected.digest;
  }, { version: state.draftVersion, digest: state.draftDigest }, { timeout: 5000 });
  const restoredRows = await renderedRows();
  const restoredRowCount = await page.locator('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-rowcount');
  assert.deepEqual(assertVisibleRowsMatchOracle({ rows: restoredRows, sourceIds: specimenOracle.ids, ariaRowCount: restoredRowCount }), oracleRows,
    'Undo must restore the same independent source window');
  const restoreDurationMs = Date.now() - restoreStart;
  assert(restoreDurationMs <= 5000, `Undo and initial render took ${restoreDurationMs} ms`);
  report.nativeChecks.push({ name: 'undo-last-table-deletion', durationMs: restoreDurationMs });
  const fullRestoredRows = await captureFullPreviewWindow('undo-preview');
  assert.deepEqual(assertVisibleRowsMatchOracle({ rows: fullRestoredRows, sourceIds: specimenOracle.ids, ariaRowCount: restoredRowCount }), fullInitialRows,
    'Undo must restore all 25 independent source identities');

  await page.reload({ waitUntil: 'domcontentloaded' });
  await table.waitFor({ state: 'visible', timeout: 30000 });
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready', null, { timeout: 30000 });
  assert.deepEqual((await api(`${base}/builder`)).workspace, nativeBaseline);
  const reloadedRows = await renderedRows();
  const reloadedRowCount = await page.locator('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-rowcount');
  assert.deepEqual(assertVisibleRowsMatchOracle({ rows: reloadedRows, sourceIds: specimenOracle.ids, ariaRowCount: reloadedRowCount }), oracleRows,
    'Reload must retain the same independent source window');
  const fullReloadedRows = await captureFullPreviewWindow('reload-preview');
  assert.deepEqual(assertVisibleRowsMatchOracle({ rows: fullReloadedRows, sourceIds: specimenOracle.ids, ariaRowCount: reloadedRowCount }), fullInitialRows,
    'Reload must retain all 25 independent source identities');
  report.incidentalAssets = browser.diagnostics.assetFailures;
  report.errors = [...browser.diagnostics.console, ...browser.diagnostics.pageErrors, ...browser.diagnostics.httpFailures, ...browser.diagnostics.networkFailures];
  assert.deepEqual(report.errors, []);
  report.scope = 'API deletion/revision restore plus native last-table deletion, Undo, and reload';
  report.status = 'passed';
  report.outputId = outputId;
  await page.screenshot({ path: join(evidence, 'passed-reload.png'), fullPage: true });
  await writeFile(join(evidence, 'passed-reload.dom.txt'), await page.locator('body').innerText());
} catch (error) {
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  if (report.activeAction) report.firstFailedAction = {
    label: report.activeAction.label,
    locator: report.activeAction.locator,
    elapsedMs: Date.now() - report.activeAction.startedAt,
  };
  report.diagnostics = browser?.diagnostics;
  await browser?.captureFailure(error, {
    phase: 'last-table-lifecycle',
    action: report.activeAction,
    draftVersion: state?.draftVersion,
    draftDigest: state?.draftDigest,
    explorer,
  });
  process.exitCode = 1;
} finally {
  if (browser && report.status !== 'failed') {
    report.incidentalAssets = browser.diagnostics.assetFailures;
    report.errors = [...browser.diagnostics.console, ...browser.diagnostics.pageErrors, ...browser.diagnostics.httpFailures, ...browser.diagnostics.networkFailures];
    if (report.errors.length) {
      report.status = 'failed';
      report.error = `Unexpected browser diagnostics: ${JSON.stringify(report.errors)}`;
      process.exitCode = 1;
    }
  }
  await browser?.close();
  if (apiBuildFreeze) {
    try {
      report.apiBuildFreeze = await apiBuildFreeze.assertUnchanged();
      assert.equal(report.apiBuildFreeze.checked, true);
      assert.equal(report.apiBuildFreeze.unchanged, true);
      assert.equal(report.apiBuildFreeze.invalidatesRun, false);
      const finalObservation = await checkContainerApiBuildStamp(apiContainer);
      report.apiBuildIdentity.after = extractBuildIdentity(finalObservation);
      assert.equal(report.apiBuildIdentity.after, report.apiBuildIdentity.before, 'API build identity changed during the run');
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.apiBuildFreezeError = String(error.stack ?? error);
      process.exitCode = 1;
    }
  }
  if (sourceFreeze) {
    try {
      report.sourceFreeze.after = sourceFingerprint(sourceRoot);
      report.sourceFreeze.check = await sourceFreeze.assertUnchanged();
      assert.equal(report.sourceFreeze.check.unchanged, true);
      assert.equal(report.sourceFreeze.check.invalidatesRun, false);
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.sourceFreeze.error = String(error.stack ?? error);
      process.exitCode = 1;
    }
  }
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
process.stdout.write(`${JSON.stringify({ status: report.status, explorer, evidence, error: report.error })}\n`);
