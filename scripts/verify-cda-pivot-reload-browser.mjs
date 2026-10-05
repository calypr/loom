import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, readFile, realpath, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { captureSourceFreeze } from './lib/source-freeze.mjs';
import { sanitizeBody, sanitizeText } from './lib/playwright-browser.mjs';
import { sourceFingerprint } from './verify-ui/source-fingerprint.mjs';


export async function runPivotReloadBrowserWorkflow({ page, cda }, originalArgs = {}) {
  const environment = cda.env ?? process.env;
const includeFixtureDiagnostics = domainReport => {
    const diagnostics = cda.diagnostics;
    domainReport.errors ??= [];
    const add = (entry, same) => { if (!domainReport.errors.some(same)) domainReport.errors.push(entry); };
    for (const failure of diagnostics.pageErrors ?? []) add({ kind: 'runtime', message: failure.message }, item => item.kind === 'runtime' && item.message === failure.message);
    for (const failure of diagnostics.console ?? []) add({ kind: 'console', message: failure.text, location: failure.location }, item => item.kind === 'console' && item.message === failure.text);
    for (const failure of diagnostics.networkFailures ?? []) add({ kind: 'network', path: failure.url, failure: failure.failure }, item => item.kind === 'network' && item.path === failure.url);
    for (const failure of diagnostics.httpFailures ?? []) add({ kind: 'http', url: failure.url, status: failure.status, response: failure.body }, item => item.kind === 'http' && item.url === failure.url && item.status === failure.status);
    domainReport.incidentalErrors ??= [];
    for (const failure of diagnostics.assetFailures ?? []) if (!domainReport.incidentalErrors.some(item => item.url === failure.url && item.status === failure.status)) domainReport.incidentalErrors.push(failure);
  };
  const captureFailure = async (error, details = {}) => cda.attachReport('failure-evidence', { error: String(error), details, diagnostics: cda.diagnostics });
const evidence = cda.evidence;
const apiOrigin = cda.apiOrigin;
const uiOrigin = cda.uiOrigin;
const seedPath = (cda.env?.LOOM_PIVOT_RELOAD_SEED ?? process.env.LOOM_PIVOT_RELOAD_SEED);
const apiContainer = (cda.target.apiContainer ?? cda.env?.LOOM_CDA_API_CONTAINER);
const isolatedSourceRoot = fileURLToPath(new URL('..', import.meta.url));
const sourceFreezeStartedAt = new Date().toISOString();
const report = {
  evidence,
  project: cda.project,
  cases: [],
  errors: [],
  apiRequests: [],
  started: new Date().toISOString(),
  sourceFreeze: { startedAt: sourceFreezeStartedAt, available: false, invalidatesRun: false, productFailure: false },
};
let seed;
let baseline;
let table;
let source;
let patient;
let oracleIds;
let expectedCategoryLabels;
let base;
let sourceFreeze;

let verificationPhase = 'setup';
let cycle = 0;
let cycleStartedAt;
let activeAction;
let initialBuildIdentity;
let actualSourceRoot;

function readBuildIdentity() {
  const stdout = execFileSync('docker', ['exec', apiContainer,
    '/workspace/loom-dev-build-stamp.sh', '--check'], { encoding: 'utf8', timeout: 10000 }).trim();
  assert.match(stdout, /^[a-f0-9]{64}\s+[a-f0-9]{64}\s+[a-f0-9]{64}$/i);
  return stdout.split(/\s+/).join(':').toLowerCase();
}

async function runAction(label, locator, action) {
  activeAction = { label, locator: locator?.toString() ?? 'page navigation', targetLocator: locator };
  return action();
}

const ownedPath = url => new URL(url).pathname;

async function fetchBuilder(label) {
  activeAction = undefined;
  const url = apiOrigin + base + '/builder';
  const response = await fetch(url);
  const body = await response.text();
  const record = { label, path: ownedPath(url), status: response.status };
  if (!response.ok) record.diagnosticBody = sanitizeBody(body);
  report.apiRequests.push(record);
  assert(response.ok, `${label} builder request returned ${response.status}: ${record.diagnosticBody ?? ''}`);
  const result = JSON.parse(body);
  Object.assign(record, { draftDigest: result.draftDigest, draftVersion: result.draftVersion });
  return result;
}

const readWindow = async tableLocator => tableLocator.evaluate(tableElement => {
  const scrollElement = tableElement.closest('[data-testid="preview-table-scroll"]');
  if (!scrollElement) throw new Error('Preview table scroll surface is missing');
  const rows = [...tableElement.querySelectorAll('[role="row"]')];
  const headers = [...tableElement.querySelectorAll('[role="columnheader"]')];
  const dataRows = rows.slice(1);
  if (dataRows.length !== 1) throw new Error(`Expected exactly one rendered data row, found ${dataRows.length}`);
  const rowOrdinal = dataRows[0].firstElementChild?.innerText.trim();
  const cells = [...dataRows[0].querySelectorAll('[role="cell"]')];
  if (headers.length !== cells.length) throw new Error(`Mounted header/cell count mismatch: ${headers.length}/${cells.length}`);
  return {
    scrollLeft: scrollElement.scrollLeft,
    rowOrdinal,
    ariaColCount: Number(tableElement.getAttribute('aria-colcount')),
    headers: headers.map(header => ({
      left: parseFloat(header.style.left),
      width: parseFloat(header.style.width),
      label: header.innerText.trim(),
    })),
    cells: cells.map(cell => ({
      left: parseFloat(cell.style.left),
      width: parseFloat(cell.style.width),
      value: cell.innerText.trim(),
    })),
  };
});

async function captureWideCoverage(page) {
  const scroll = page.getByTestId('preview-table-scroll');
  const tableLocator = scroll.getByRole('table');
  await tableLocator.waitFor({ state: 'visible', timeout: 5000 });
  const initial = await scroll.evaluate(element => ({
    originalScrollLeft: element.scrollLeft,
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
    maxScrollLeft: Math.max(0, element.scrollWidth - element.clientWidth),
  }));
  const samples = [await readWindow(tableLocator)];
  const step = Math.max(1, Math.floor(initial.clientWidth * 0.65));

  await runAction('Hover preview scroll surface', scroll, () => scroll.hover({ timeout: 5000 }));
  let currentLeft = samples[0].scrollLeft;
  let windowCount = 0;
  while (currentLeft < initial.maxScrollLeft - 1) {
    assert(++windowCount <= 200, 'Wide Pivot horizontal sweep exceeded 200 input windows');
    const previousLeft = currentLeft;
    await runAction('Scroll horizontally through the Pivot preview', scroll,
      () => page.mouse.wheel(Math.min(step, initial.maxScrollLeft - previousLeft), 0));
    await page.waitForFunction(({ testId, previous }) => {
      const scrollElement = document.querySelector(`[data-testid="${testId}"]`);
      const tableElement = scrollElement?.querySelector('[role="table"]');
      return Boolean(scrollElement && tableElement && scrollElement.scrollLeft > previous + 0.5);
    }, { testId: 'preview-table-scroll', previous: previousLeft }, { timeout: 2000 });
    currentLeft = (await scroll.evaluate(element => element.scrollLeft));
    assert(currentLeft > previousLeft + 0.5, `Horizontal scroll did not advance from ${previousLeft}`);
    samples.push(await readWindow(tableLocator));
  }

  const coverage = {
    ...initial,
    columnCount: samples[0].ariaColCount,
    samples,
  };
  const headerByIndex = new Map();
  const cellByIndex = new Map();
  for (const sample of samples) {
    assert.equal(sample.rowOrdinal, '1', 'The full horizontal sweep must retain data row ordinal 1');
    assert.equal(sample.ariaColCount, coverage.columnCount);
    for (let indexInWindow = 0; indexInWindow < sample.headers.length; indexInWindow++) {
      const header = sample.headers[indexInWindow];
      const cell = sample.cells[indexInWindow];
      const columnIndex = Math.round((header.left - samples[0].headers[0].left) / header.width);
      assert.equal(Math.round((cell.left - samples[0].headers[0].left) / header.width), columnIndex,
        'Cell ordinal must agree with the mounted header identity');
      if (headerByIndex.has(columnIndex)) {
        assert.equal(headerByIndex.get(columnIndex), header.label,
          'A column ordinal must keep the same identity in every virtual window');
      }
      if (cellByIndex.has(columnIndex)) {
        assert.equal(cellByIndex.get(columnIndex), cell.value,
          'A column ordinal must keep the same row value in every virtual window');
      }
      headerByIndex.set(columnIndex, header.label);
      cellByIndex.set(columnIndex, cell.value);
    }
  }
  assert.equal(headerByIndex.size, coverage.columnCount, 'The horizontal sweep must mount every table column');
  assert.equal(cellByIndex.size, coverage.columnCount, 'The horizontal sweep must capture every data cell');
  const labels = new Set([...headerByIndex.values()].map(label => label.trim().toLocaleLowerCase()));
  for (const label of expectedCategoryLabels) {
    assert(labels.has(label), `Missing Pivot category column after horizontal sweep: ${label}`);
  }
  const orderedCells = [...cellByIndex.entries()]
    .sort(([left], [right]) => left - right)
    .map(([, value]) => value);
  assert.equal(orderedCells[0], source, 'Data row ordinal 1 must retain its Specimen source identity');
  assert(orderedCells.slice(1).every(value => value === patient),
    'Every Pivot category cell in data row ordinal 1 must retain its Patient identity');

  const finalScrollLeft = await scroll.evaluate(element => element.scrollLeft);
  if (Math.abs(finalScrollLeft - initial.originalScrollLeft) > 1) {
    await runAction('Restore the original preview scroll position', scroll,
      () => page.mouse.wheel(initial.originalScrollLeft - finalScrollLeft, 0));
    await page.waitForFunction(({ testId, target }) => {
      const element = document.querySelector(`[data-testid="${testId}"]`);
      return Boolean(element && Math.abs(element.scrollLeft - target) <= 1);
    }, { testId: 'preview-table-scroll', target: initial.originalScrollLeft }, { timeout: 2000 });
  }
  coverage.restoredScrollLeft = await scroll.evaluate(element => element.scrollLeft);
  assert.equal(coverage.restoredScrollLeft, initial.originalScrollLeft,
    'The full-width verification must restore the user-visible scroll position');
  coverage.headers = [...headerByIndex.entries()].sort(([left], [right]) => left - right).map(([, label]) => label);
  coverage.cells = orderedCells;
  coverage.observedColumnCount = headerByIndex.size;
  coverage.scrollWindows = samples.length;
  return coverage;
}

try {
  await mkdir(evidence, { recursive: true });
  assert(apiOrigin, 'Set LOOM_CDA_API_ORIGIN to the isolated CDA API origin');
  assert(uiOrigin, 'Set LOOM_CDA_UI_ORIGIN to the isolated CDA UI origin');
  assert(seedPath, 'Set LOOM_PIVOT_RELOAD_SEED to the owned Pivot category seed report');
  assert(apiContainer, 'Set LOOM_CDA_API_CONTAINER to the isolated CDA API container');
  assert(!apiContainer.startsWith('loom-dev-6d7df93d6a37'), 'Do not use the shared CDA API container');
  for (const [name, value, sharedPort] of [['API', apiOrigin, '8188'], ['UI', uiOrigin, '30008']]) {
    const parsed = new URL(value);
    assert(['http:', 'https:'].includes(parsed.protocol), `${name} origin must use HTTP or HTTPS`);
    assert(!parsed.username && !parsed.password, `${name} origin must not embed credentials`);
    assert(['127.0.0.1', 'localhost', '::1'].includes(parsed.hostname), `${name} origin must target a local isolated stack`);
    assert(parsed.port !== sharedPort, `${name} origin must not target the shared CDA port ${sharedPort}`);
    assert(parsed.pathname === '/' && !parsed.search && !parsed.hash, `${name} value must be an origin without a path, query, or fragment`);
  }
  actualSourceRoot = await realpath(isolatedSourceRoot);
  if ((cda.env?.LOOM_SOURCE_FREEZE_ROOT ?? process.env.LOOM_SOURCE_FREEZE_ROOT)) {
    assert.equal(await realpath((cda.env?.LOOM_SOURCE_FREEZE_ROOT ?? process.env.LOOM_SOURCE_FREEZE_ROOT)), actualSourceRoot,
      'LOOM_SOURCE_FREEZE_ROOT must resolve to this isolated source checkout');
  }
  sourceFreeze = await captureSourceFreeze(actualSourceRoot);
  report.sourceFingerprint = { before: sourceFingerprint(actualSourceRoot) };
  initialBuildIdentity = readBuildIdentity();
  report.apiBuildIdentity = initialBuildIdentity;
  report.sourceFreeze = { startedAt: sourceFreezeStartedAt, available: true, watchedFileCount: sourceFreeze.watchedFileCount };
  seed = JSON.parse(await readFile(seedPath, 'utf8'));
  report.explorer = seed.explorer;
  assert(/^pivot-category-edit-browser-\d+$/.test(seed.explorer), 'Only an owned Pivot QA Explorer may be replayed');
  assert.notEqual(seed.explorer, 'cda-builder-full-qa-1790440983382', 'The protected full QA Explorer must never be replayed or mutated');
  base = `/api/v1/projects/loom_dev_cda_fhir/explorers/${seed.explorer}/authoring/v2`;
  baseline = await fetchBuilder('before-browser-reload');
  table = baseline.workspace.documents[0];
  const pivotStep = table.construction.steps.find(step => step.operation.kind === 'PIVOT');
  assert(pivotStep, 'The saved owned workspace must contain a Pivot');
  source = seed.oracle.source.id;
  patient = seed.oracle.chain[0].witnesses[0].values[1];
  oracleIds = new Set(seed.oracle.chain.at(-1).witnesses.map(witness => witness.values.at(-1)));
  assert.deepEqual(new Set(pivotStep.operation.pivot.categories.map(category => category.key.string)), oracleIds);
  const outputsById = new Map(pivotStep.outputs.map(output => [output.id, output]));
  expectedCategoryLabels = new Set(pivotStep.operation.pivot.categories.map(category => {
    const output = outputsById.get(category.outputColumnId);
    assert(output?.label?.trim(), 'Each raw category must resolve to its saved Pivot output by stable outputColumnId');
    return output.label.trim().toLocaleLowerCase();
  }));
  assert.equal(expectedCategoryLabels.size, oracleIds.size,
    'Every raw category must have a distinct saved presentation output label');
  assert.equal(expectedCategoryLabels.size, 31,
    'The wide Pivot replay requires all 31 independently witnessed Observation categories');
  report.sourceFreeze = {
    ...report.sourceFreeze,
    rawOracleCategoryCount: oracleIds.size,
    expectedCategoryLabels: [...expectedCategoryLabels].sort(),
  };
  verificationPhase = 'browser';

  const builderURL = `${uiOrigin}/?project=${encodeURIComponent(cda.project)}&explorer=${seed.explorer}&mode=builder`;
  const tableTestId = `construction-table-${table.output.id}`;
  for (cycle = 1; cycle <= 5; cycle++) {
    cycleStartedAt = Date.now();
    if (cycle === 1) {
      await runAction('Navigate to the owned Builder Explorer', page.locator('body'),
        () => page.goto(builderURL, { waitUntil: 'domcontentloaded', timeout: 5000 }));
    } else {
      await runAction('Reload the owned Builder Explorer', page.locator('body'),
        () => page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 }));
    }
    const constructionTable = page.getByTestId(tableTestId);
    await constructionTable.waitFor({ state: 'visible', timeout: 5000 });
    await runAction('Open the saved construction table', constructionTable,
      () => constructionTable.click({ timeout: 5000 }));
    const previewTable = page.getByTestId('preview-table-scroll').getByRole('table');
    await page.waitForFunction(({ testId }) => {
      const preview = document.querySelector(`[data-testid="${testId}"] [role="table"]`);
      const settings = document.querySelector('[data-testid="construction-rows-settings-trigger"]');
      return preview?.getAttribute('aria-rowcount') === '2' && settings?.disabled === false;
    }, { testId: 'preview-table-scroll' }, { timeout: 5000 });
    await previewTable.waitFor({ state: 'visible', timeout: 5000 });
    const coverage = await captureWideCoverage(page);
    assert.equal(coverage.columnCount, oracleIds.size + 1,
      'Rendered table must expose Specimen ID plus all raw-observed Pivot categories');
    const durationMs = Date.now() - cycleStartedAt;
    report.cases.push({
      cycle,
      durationMs,
      renderedRowOrdinal: 1,
      wideCoverage: {
        columnCount: coverage.columnCount,
        observedColumnCount: coverage.observedColumnCount,
        scrollWindows: coverage.scrollWindows,
        maxScrollLeft: coverage.maxScrollLeft,
        headers: coverage.headers,
        cells: coverage.cells,
      },
    });
  }

  const final = await fetchBuilder('after-browser-reloads');
  assert.equal(final.draftDigest, baseline.draftDigest);
  assert.deepEqual(final.workspace, baseline.workspace);
  const diagnostics = cda.diagnostics;
  const expectedAssetConsole = diagnostics.console.filter(entry => {
    if (!/\b404\b/.test(entry.text) || !entry.location) return false;
    try {
      const url = new URL(entry.location);
      return url.origin === uiOrigin && url.pathname === '/favicon.ico';
    } catch {
      return false;
    }
  });
  const unexpectedConsole = diagnostics.console.filter(entry => !expectedAssetConsole.includes(entry));
  report.browserDiagnostics = { ...diagnostics, expectedAssetConsole, unexpectedConsole };
  assert.deepEqual(unexpectedConsole, [], 'Unexpected browser console errors must be absent');
  assert.deepEqual(diagnostics.pageErrors, [], 'Browser exceptions must be absent');
  assert.deepEqual(diagnostics.networkFailures, [], 'Unexpected first-party network failures must be absent');
  assert.deepEqual(diagnostics.httpFailures, [], 'Unexpected first-party HTTP failures must be absent');
  assert(report.cases.every(item => item.durationMs <= 5000),
    `Reload exceeded five seconds: ${JSON.stringify(report.cases.map(item => item.durationMs))}`);
  report.status = 'passed';
  report.productFailure = false;
} catch (error) {
  const setupFailure = verificationPhase === 'setup';
  report.status = setupFailure ? 'unverified' : 'failed';
  report.productFailure = !setupFailure;
  if (setupFailure) {
    report.unverified = {
      kind: 'harness-setup',
      message: sanitizeText(error.message ?? error),
      diagnostics: { explorer: report.explorer, phase: verificationPhase },
      productFailure: false,
    };
  }
  report.error = sanitizeText(error.stack ?? error);
if (page) {
    report.browserDiagnostics = cda.diagnostics;
    report.failureEvidence = await captureFailure(error, {
      phase: verificationPhase,
      cycle,
      elapsedMs: cycleStartedAt ? Date.now() - cycleStartedAt : undefined,
      explorer: report.explorer,
      ownedBuilderPath: base ? base + '/builder' : undefined,
      action: activeAction,
    });
  }
  report.__nativeFailure = true;
} finally {
  if (sourceFreeze) {
    const sourceFreezeFinishedAt = new Date().toISOString();
    try {
      report.sourceFreeze = { ...report.sourceFreeze, ...(await sourceFreeze.assertUnchanged()), finishedAt: sourceFreezeFinishedAt };
      report.sourceFingerprint.after = sourceFingerprint(actualSourceRoot);
      assert.deepEqual(report.sourceFingerprint.after, report.sourceFingerprint.before,
        'Watched source fingerprint changed during browser verification');
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.productFailure = false;
      report.sourceFreeze = {
        ...report.sourceFreeze,
        unchanged: false,
        changedPaths: error.changedPaths ?? [],
        invalidatesRun: true,
        productFailure: false,
        error: sanitizeText(error),
        finishedAt: sourceFreezeFinishedAt,
      };
      if (!report.failureEvidence) {
        report.failureEvidence = await captureFailure(error, { phase: 'source-freeze', explorer: report.explorer });
      }
      report.__nativeFailure = true;
    }
  } else {
    report.sourceFreeze.finishedAt = new Date().toISOString();
  }
  if (initialBuildIdentity) {
    try {
      report.apiBuildIdentityAfter = readBuildIdentity();
      assert.equal(report.apiBuildIdentityAfter, initialBuildIdentity,
        'Running API build identity changed during browser verification');
    } catch (error) {
      report.priorStatus = report.status;
      report.status = 'invalidated';
      report.productFailure = false;
      report.apiBuildIdentityError = sanitizeText(error.message ?? error);
      report.__nativeFailure = true;
    }
  }
  report.finished = new Date().toISOString();
  await mkdir(evidence, { recursive: true });
  await writeFile(join(evidence, 'report.json'), JSON.stringify(report, null, 2));
}
  if (report.__nativeFailure) throw new Error(report.error ?? report.identityFailure ?? 'verify-cda-pivot-reload-browser.mjs workflow failed');
  await cda.attachReport('verify-cda-pivot-reload-browser.mjs', report);
  return report;
}
