import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { apiBuildIdentity, measuredAction, record, targetFromEnvironment } from './verify-cda-builder-related-source-chooser.mjs';
import { launchBrowser, sanitizeText } from './lib/playwright-browser.mjs';
import { requireUnique } from './lib/playwright-actions.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './verify-ui/source-fingerprint.mjs';
import { readPatientRelatedOracle } from './verify-cda-builder-patient-related.mjs';

const LIMITS = [50, 100, 500, 1000];
const DISPLAY = value => value === null || value === undefined || value === '' ? '—' : String(value);
const hashFile = async path => {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest('hex');
};

export function assertPreviewLimitState({ limit, sourceCount, ariaRowCount }) {
  assert(LIMITS.includes(limit), `Unsupported Preview row limit ${limit}`);
  assert.equal(Number(ariaRowCount), Math.min(limit, sourceCount) + 1,
    `Preview rowcount must be the lesser of its selected limit and independent source count, plus one header`);
}

export function assertPreviewWindowMatchesSource({ rows, sourceRows }) {
  assert.deepEqual(rows, sourceRows.slice(0, rows.length).map((row, index) => ({
    ordinal: index + 1,
    cells: [DISPLAY(row.id), DISPLAY(row.subject), DISPLAY(row.bodySite)],
  })), 'The Preview row-limit change altered or corrupted the independent first source rows');
}

export async function runPreviewLimits({ explorerId, env = process.env } = {}) {
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = await targetFromEnvironment(env);
  const oracle = await readPatientRelatedOracle({ datasetDir: target.fixtureDir,
    project: target.fixtureProject, generation: target.fixtureGeneration });
  const evidenceDirectory = resolve(target.artifacts, `playwright-preview-limits-${new Date().toISOString().replaceAll(':', '-')}`);
  await mkdir(evidenceDirectory, { recursive: true, mode: 0o700 });
  const sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
  const buildAtStart = apiBuildIdentity(target);
  const report = {
    schemaVersion: 1,
    scenario: 'cda-builder-preview-row-limits',
    case: 'Preview limits',
    status: 'running',
    target: { sourceRoot: target.sourceRoot, sourceFingerprint: sourceAtStart.fingerprint, apiBuildIdentity: buildAtStart,
      composeProject: target.composeProject, apiContainer: env.LOOM_CDA_API_CONTAINER, uiOrigin: target.uiUrl, apiOrigin: target.apiUrl,
      project: target.fixtureProject, generation: target.fixtureGeneration, explorerId },
    sourceOracle: { specimenFile: oracle.specimenPath, patientMembershipFile: oracle.patientPath,
      fileHashes: oracle.sourceHashes, specimenCount: oracle.specimenCount,
      ordering: 'Raw Specimen rows ordered by the Arango vertex key for the explicitly named project and generation' },
    path: 'Builder > Preview row limit > 50 > 100 > 500 > 1000',
    expectedVisibleResult: 'Each selected row limit updates aria-rowcount within five seconds while the first visible Specimen rows remain exactly equal to independent raw CDA source rows.',
    lifecycle: { preview: 'untested', apply: 'not applicable', reload: 'not applicable' },
    evidenceDirectory, assertions: [], actions: [], timings: [], limits: [],
  };
  const tracker = { actions: [], timings: [] };
  let browser;
  let failure;
  let activeAction = { label: 'launch Playwright browser', locator: 'Chromium launch' };
  const previewResponses = [];
  try {
    browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [target.uiUrl, target.apiUrl], noAuth: true });
    const { page, diagnostics } = browser;
    page.on('response', response => {
      const url = new URL(response.url());
      if (url.pathname.endsWith('/authoring/v2/preview')) previewResponses.push({ path: url.pathname, status: response.status() });
    });
    const builderURL = new URL(target.uiUrl);
    builderURL.searchParams.set('project', target.fixtureProject);
    builderURL.searchParams.set('explorer', explorerId);
    builderURL.searchParams.set('mode', 'builder');
    activeAction = { label: 'open Builder', locator: builderURL.toString() };
    await page.goto(builderURL.toString(), { waitUntil: 'domcontentloaded', timeout: 15000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    record(report, 'Builder is scoped to requested Explorer', await explorer.inputValue() === explorerId,
      { expected: explorerId, actual: await explorer.inputValue() });
    const selectedTable = page.locator('button[data-testid^="construction-table-"][aria-pressed="true"]');
    await requireUnique(selectedTable, 'Selected Builder table');
    const outputId = (await selectedTable.getAttribute('data-testid')).slice('construction-table-'.length);
    const builderResponse = await browser.context.request.get(`${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`);
    assert.equal(builderResponse.status(), 200, 'Scoped Builder document identity query must succeed');
    const builder = await builderResponse.json();
    assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder catalog generation must match the raw oracle generation');
    const document = builder.workspace?.documents?.find(item => item.output?.id === outputId);
    assert(document, 'The selected output must exist in the scoped Builder API response');
    assert.equal(document.rowResourceType ?? document.rootResourceType ?? document.document?.rootResourceType, 'Specimen');
    const table = page.getByTestId('preview-table-scroll').getByRole('table');
    await table.waitFor({ state: 'visible', timeout: 15000 });
    const headers = (await table.getByRole('columnheader').allTextContents()).map(value => value.trim());
    assert.deepEqual(headers, ['SPECIMEN ID', 'SUBJECT.REFERENCE', 'COLLECTION.BODYSITE.REFERENCE.REFERENCE']);
    const previewLimit = page.getByRole('combobox', { name: 'Preview row limit', exact: true });
    await requireUnique(previewLimit, 'Preview row limit');
    assert.equal(await previewLimit.inputValue(), '25', 'Preview limits case must begin with the 25-row setting');
    const initialAriaRowCount = await table.getAttribute('aria-rowcount');
    assert.equal(Number(initialAriaRowCount), Math.min(25, oracle.specimenCount) + 1,
      'Initial Preview must use its independent raw source cardinality');
    const initialRows = await table.getByRole('row').evaluateAll(elements => elements.slice(1).map(row => ({
      ordinal: Number(row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label')?.match(/Inspect row (\d+) identity/)?.[1]),
      cells: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()),
    })));
    assert(initialRows.length > 0, 'Initial 25-row Preview must expose result rows');
    assertPreviewWindowMatchesSource({ rows: initialRows, sourceRows: oracle.rows });
    report.initialRows = initialRows;
    report.initialAriaRowCount = initialAriaRowCount;

    for (const limit of LIMITS) {
      activeAction = { label: `select Preview row limit ${limit}`, locator: previewLimit.toString(), targetLocator: previewLimit };
      const expectedCount = Math.min(limit, oracle.specimenCount) + 1;
      await measuredAction(tracker, `select Preview row limit ${limit}`, previewLimit,
        target => target.selectOption(String(limit), { timeout: 5000 }),
        () => page.waitForFunction(expected => document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(expected), expectedCount, { timeout: 5000 }));
      assert.equal(await previewLimit.inputValue(), String(limit));
      const currentTable = page.getByTestId('preview-table-scroll').getByRole('table');
      const ariaRowCount = await currentTable.getAttribute('aria-rowcount');
      assertPreviewLimitState({ limit, sourceCount: oracle.specimenCount, ariaRowCount });
      const rows = await currentTable.getByRole('row').evaluateAll(elements => elements.slice(1).map(row => ({
        ordinal: Number(row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label')?.match(/Inspect row (\d+) identity/)?.[1]),
        cells: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()),
      })));
      assertPreviewWindowMatchesSource({ rows, sourceRows: oracle.rows });
      report.limits.push({ limit, ariaRowCount, visibleRows: rows });
    }
    assert(previewResponses.filter(response => response.status === 200).length >= LIMITS.length,
      'Each Preview row-limit change must complete a successful production preview request');
    report.lifecycle.preview = 'passed';
    report.actions = tracker.actions;
    report.timings = tracker.timings;
    report.previewResponses = previewResponses;
    await page.screenshot({ path: `${evidenceDirectory}/preview-limits.png`, fullPage: true });
    report.evidence = ['preview-limits.png'];
    record(report, 'No unexpected console, page, or API failures', diagnostics.console.length === 0
      && diagnostics.pageErrors.length === 0 && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0, diagnostics);
  } catch (error) {
    failure = error;
    report.failure = { action: activeAction.label, locator: activeAction.locator,
      elapsedMs: tracker.actionStartedAt ? Date.now() - tracker.actionStartedAt : undefined,
      message: sanitizeText(error.message ?? error) };
    if (browser) {
      report.failureTrace = await browser.captureFailure(error, { action: { ...activeAction, startedAt: tracker.activeAction?.startedAt },
        elapsedMs: report.failure.elapsedMs, target: report.target });
      report.browserDiagnostics = browser.diagnostics;
    }
  } finally {
    report.actions.push(...tracker.actions);
    report.timings = tracker.timings;
    report.previewResponses = previewResponses;
    if (browser) await browser.close().catch(error => { report.closeError = sanitizeText(error.message); });
    try {
      const sourceHashesAtEnd = { Specimen: await hashFile(oracle.specimenPath), Patient: await hashFile(oracle.patientPath) };
      const datasetUnchanged = JSON.stringify(sourceHashesAtEnd) === JSON.stringify(oracle.sourceHashes);
      report.assertions.push({ name: 'Raw CDA source files stayed unchanged', status: datasetUnchanged ? 'passed' : 'failed', evidence: { before: oracle.sourceHashes, after: sourceHashesAtEnd } });
      if (!datasetUnchanged) failure ??= new Error('Raw CDA source files changed during the browser run');
      const sourceAtEnd = sourceFingerprintWithManifest(target.sourceRoot);
      const changedPaths = sourceFingerprintChangedPaths(sourceAtStart.manifest, sourceAtEnd.manifest);
      const sourceUnchanged = sourceAtStart.fingerprint.sha256 === sourceAtEnd.fingerprint.sha256;
      report.assertions.push({ name: 'Watched source stayed unchanged', status: sourceUnchanged ? 'passed' : 'failed', evidence: { before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths } });
      if (!sourceUnchanged) failure ??= new Error('Watched source changed during the browser run');
      const buildAtEnd = apiBuildIdentity(target);
      const buildUnchanged = buildAtStart === buildAtEnd;
      report.assertions.push({ name: 'API build identity stayed unchanged', status: buildUnchanged ? 'passed' : 'failed', evidence: { before: buildAtStart, after: buildAtEnd } });
      if (!buildUnchanged) failure ??= new Error('API build identity changed during the browser run');
    } catch (error) {
      report.freezeError = sanitizeText(error.message ?? error);
      report.assertions.push({ name: 'Source and build identities stayed unchanged', status: 'failed', evidence: { message: report.freezeError } });
      failure ??= error;
    }
    report.status = failure || report.assertions.some(assertion => assertion.status === 'failed') ? 'failed' : 'passed';
    report.finishedAt = new Date().toISOString();
    await writeFile(`${evidenceDirectory}/report.json`, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  }
  if (failure) throw failure;
  return report;
}
