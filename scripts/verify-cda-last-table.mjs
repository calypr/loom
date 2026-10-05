import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { realpath, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { assertVisibleRowsMatchOracle, readNDJSONResourceIdentityOracle } from './lib/ndjson-resource-oracle.mjs';

export async function lastTableWorkflow({ page, cda }) {
const project = cda.project;
const explorer = `last-table-command-qa-${Date.now()}`;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const apiContainer = cda.target.apiContainer;
const composeProject = cda.target.composeProject;
const fixtureDir = cda.target.fixtureDir ?? cda.env.LOOM_CDA_DATASET_DIR;
const generation = cda.generation ?? process.env.LOOM_CDA_GENERATION ?? 'cda-fhir-v1';
const evidence = cda.evidenceDirectory;
const root = project ? `/api/v1/projects/${encodeURIComponent(project)}/explorers` : '';
const base = `${root}/${encodeURIComponent(explorer)}/authoring/v2`;
const report = Object.assign(cda.report, { status: 'running', explorer, project, target: { ...cda.report.target, apiOrigin, uiOrigin, apiContainer, composeProject }, requests: [], nativeChecks: [], errors: [] });
let fatal;
let state;
let browserEvents;
const classifiedInventoryCancellations = [];
const semanticInventoryPath = `${base}/semantic-inventory`;

const performAction = async (_tracker, label, locator, perform, options = {}) => cda.action(
  label, locator, target => perform(target, { timeout: options.timeout ?? 5000 }), options);

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
  assert(fixtureDir, 'Set LOOM_CDA_FIXTURE_DIR or LOOM_CDA_DATASET_DIR to the independently loaded CDA-FHIR/META source directory.');
  const fixtureRoot = await realpath(fixtureDir);
  const specimenPath = await realpath(join(fixtureRoot, 'Specimen.ndjson'));
  report.target.fixtureDirectory = fixtureRoot;
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

  browserEvents = cda.captureRequests(base);
  page.on('requestfailed', request => {
    let url;
    try { url = new URL(request.url()); } catch { return; }
    if (url.origin !== new URL(uiOrigin).origin || url.pathname !== semanticInventoryPath
      || request.method() !== 'POST' || request.failure()?.errorText !== 'net::ERR_ABORTED') return;
    const captured = browserEvents.byRequest.get(request);
    if (!captured) {
      report.errors.push({ kind: 'unmatched-semantic-inventory-cancellation', path: url.pathname });
      return;
    }
    const requestId = request.headers()['x-request-id'] ?? null;
    try {
      const expectedCancellation = cda.expectCanceledRequest(request,
        'A superseded semantic inventory read was canceled during the last-table lifecycle.', {
          phase: 'last-table delete, Undo, or reload',
          path: url.pathname,
          method: request.method(),
          requestId,
          capturedRequestId: captured.requestId,
          browserRequestId: captured.browserRequestId,
        });
      classifiedInventoryCancellations.push({ request: captured, requestId, expectedCancellation });
    } catch (error) {
      report.errors.push({
        kind: 'unclassified-semantic-inventory-cancellation',
        path: url.pathname,
        requestId: captured.requestId,
        browserRequestId: captured.browserRequestId,
        error: error instanceof Error ? error.message : String(error),
      });
    }
  });
  report.browserLifecycle = 'official Playwright fixture page';
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
  let deleteDialog;
  page.once('dialog', async dialog => {
    deleteDialog = { type: dialog.type(), message: dialog.message() };
    if (dialog.type() === 'confirm' && dialog.message() === 'Delete Only table?') await dialog.accept();
    else await dialog.dismiss();
  });
  await performAction(report, 'delete last table', page.locator('[data-testid="construction-delete-table"]'), target => target.click());
  assert.deepEqual(deleteDialog, { type: 'confirm', message: 'Delete Only table?' },
    'Delete must show the native confirmation for the selected table');
  report.nativeChecks.push({ name: 'delete-last-table-confirmation', ...deleteDialog });
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

  const reloadStartedAt = Date.now();
  await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 });
  await table.waitFor({ state: 'visible', timeout: 30000 });
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready', null, { timeout: 30000 });
  assert.deepEqual((await api(`${base}/builder`)).workspace, nativeBaseline);
  const reloadedRows = await renderedRows();
  const reloadedRowCount = await page.locator('[data-testid="preview-table-scroll"] [role="table"]').getAttribute('aria-rowcount');
  assert.deepEqual(assertVisibleRowsMatchOracle({ rows: reloadedRows, sourceIds: specimenOracle.ids, ariaRowCount: reloadedRowCount }), oracleRows,
    'Reload must retain the same independent source window');
  const reloadDurationMs = Date.now() - reloadStartedAt;
  assert(reloadDurationMs <= 5000, `Reload to rendered source rows took ${reloadDurationMs} ms`);
  report.nativeChecks.push({ name: 'reload-to-source-rows', durationMs: reloadDurationMs });
  const fullReloadedRows = await captureFullPreviewWindow('reload-preview');
  assert.deepEqual(assertVisibleRowsMatchOracle({ rows: fullReloadedRows, sourceIds: specimenOracle.ids, ariaRowCount: reloadedRowCount }), fullInitialRows,
    'Reload must retain all 25 independent source identities');
  await browserEvents.flush();
  const inventoryRequests = report.nativeRequests.filter(request => request.path === `${base}/semantic-inventory` && request.method === 'POST');
  const recoveredInventoryCancellation = classifiedInventoryCancellations.flatMap(({ request, requestId, expectedCancellation }) => {
    const diagnostic = cda.diagnostics.networkFailures.find(failure =>
      failure.browserRequestId === request.browserRequestId
        && failure.requestId === requestId
        && failure.method === request.method
        && failure.url === `${request.origin}${request.path}`
        && failure.errorText === 'net::ERR_ABORTED'
        && failure.expectedCancellation?.browserRequestId === request.browserRequestId
        && failure.expectedCancellation?.requestId === (failure.requestId ?? failure.playwrightRequestId)
        && expectedCancellation.browserRequestId === request.browserRequestId
        && expectedCancellation.requestId === (failure.requestId ?? failure.playwrightRequestId));
    const response = inventoryRequests.find(candidate => candidate.status === 200
      && candidate.method === request.method
      && candidate.path === request.path
      && candidate.startedAt > request.startedAt
      && candidate.browserRequestId !== request.browserRequestId);
    return diagnostic && response ? [{ requestId: diagnostic.requestId ?? expectedCancellation.requestId,
      capturedRequestId: request.requestId, browserRequestId: request.browserRequestId,
      diagnostic, recoveryRequestId: response.requestId, recoveryBrowserRequestId: response.browserRequestId }] : [];
  });
  assert.equal(recoveredInventoryCancellation.length, classifiedInventoryCancellations.length,
    'Every classified semantic inventory cancellation must match its exact native failure diagnostic and a later successful read');
  assert(recoveredInventoryCancellation.length <= 1, 'More than one semantic inventory request was canceled');
  report.recoveredInventoryCancellation = recoveredInventoryCancellation;
  const isRecoveredInventoryCancellation = failure => recoveredInventoryCancellation.some(request =>
    request.browserRequestId === failure.browserRequestId
      && request.requestId === (failure.requestId ?? failure.playwrightRequestId)
      && request.diagnostic === failure
      && failure.expectedCancellation?.browserRequestId === request.browserRequestId
      && failure.expectedCancellation?.requestId === (failure.requestId ?? failure.playwrightRequestId));
  report.incidentalAssets = cda.diagnostics.assetFailures;
  report.errors = [...cda.diagnostics.console, ...cda.diagnostics.pageErrors, ...cda.diagnostics.httpFailures,
    ...cda.diagnostics.networkFailures.filter(failure => !isRecoveredInventoryCancellation(failure))];
  assert.deepEqual(report.errors, []);
  report.scope = 'API deletion/revision restore plus native last-table deletion, Undo, and reload';
  report.status = 'passed';
  report.outputId = outputId;
  await page.screenshot({ path: join(evidence, 'passed-reload.png'), fullPage: true });
  await writeFile(join(evidence, 'passed-reload.dom.txt'), await page.locator('body').innerText());
} catch (error) {
  fatal = error;
  report.status = 'failed';
  report.error = String(error.stack ?? error);
  if (report.activeAction) report.firstFailedAction = {
    label: report.activeAction.label,
    locator: report.activeAction.locator,
    elapsedMs: Date.now() - report.activeAction.startedAt,
  };
  report.diagnostics = cda.diagnostics;
} finally {
  if (report.status !== 'failed') {
    report.incidentalAssets = cda.diagnostics.assetFailures;
    report.errors = [...cda.diagnostics.console, ...cda.diagnostics.pageErrors, ...cda.diagnostics.httpFailures,
      ...cda.diagnostics.networkFailures.filter(failure => !report.recoveredInventoryCancellation?.some(request =>
        request.browserRequestId === failure.browserRequestId
          && request.requestId === (failure.requestId ?? failure.playwrightRequestId)
          && request.diagnostic === failure
          && failure.expectedCancellation?.browserRequestId === request.browserRequestId
          && failure.expectedCancellation?.requestId === (failure.requestId ?? failure.playwrightRequestId)))];
    if (report.errors.length) {
      report.status = 'failed';
      report.error = `Unexpected browser diagnostics: ${JSON.stringify(report.errors)}`;
    }
  }
  await browserEvents?.flush();
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
  await cda.attachReport('last-table-domain-report.json', report);
}

if (fatal || report.status === 'failed') throw fatal ?? new Error(report.error ?? 'Last-table lifecycle failed');
return report;
}
