import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { captureBuilderScreenshot, measuredAction, record, requireUnique } from './verify-cda-builder-related-source-chooser.mjs';
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

export async function runPreviewLimits({ page, cda, explorerId = cda.target.explorer } = {}) {
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = cda.target;
  const evidenceDirectory = cda.evidence;
  const report = cda.report;
  const diagnostics = cda.diagnostics;
  const oracle = await readPatientRelatedOracle({ datasetDir: target.fixtureDir,
    project: target.fixtureProject, generation: target.fixtureGeneration });
  Object.assign(report, {
    schemaVersion: 1,
    scenario: 'cda-builder-preview-row-limits',
    case: 'Preview limits',
    status: 'running',
    target: { ...report.target, sourceRoot: target.sourceRoot,
      composeProject: target.composeProject, apiContainer: target.apiContainer, uiOrigin: target.uiUrl, apiOrigin: target.apiUrl,
      project: target.fixtureProject, generation: target.fixtureGeneration, explorerId },
    sourceOracle: { specimenFile: oracle.specimenPath, patientMembershipFile: oracle.patientPath,
      fileHashes: oracle.sourceHashes, specimenCount: oracle.specimenCount,
      ordering: 'Raw Specimen rows ordered by the Arango vertex key for the explicitly named project and generation' },
    path: 'Builder > Preview row limit > 50 > 100 > 500 > 1000',
    expectedVisibleResult: 'Each selected row limit updates aria-rowcount within five seconds while the first visible Specimen rows remain exactly equal to independent raw CDA source rows.',
    lifecycle: { preview: 'untested', apply: 'not applicable', reload: 'not applicable' },
    evidenceDirectory, limits: [],
  });
  Object.defineProperty(report, 'nativeCheck', {
    configurable: true,
    value: (name, passed, evidence) => cda.check('correctness', name, passed, evidence),
  });
  const tracker = { actions: [], timings: [], cda };
  const previewResponses = [];

  let failure;
  try {
    page.on('response', response => {
      const url = new URL(response.url());
      if (url.pathname.endsWith('/authoring/v2/preview')) previewResponses.push({ path: url.pathname, status: response.status() });
    });
    const builderURL = new URL(target.uiUrl);
    builderURL.searchParams.set('project', target.fixtureProject);
    builderURL.searchParams.set('explorer', explorerId);
    builderURL.searchParams.set('mode', 'builder');
    await page.goto(builderURL.toString(), { waitUntil: 'domcontentloaded', timeout: 5000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 5000 });
    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    record(report, 'Builder is scoped to requested Explorer', await explorer.inputValue() === explorerId,
      { expected: explorerId, actual: await explorer.inputValue() });
    const selectedTable = page.locator('button[data-testid^="construction-table-"][aria-pressed="true"]');
    await requireUnique(selectedTable, 'Selected Builder table');
    const outputId = (await selectedTable.getAttribute('data-testid')).slice('construction-table-'.length);
    const builderResponse = await cda.request.get(`${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`);
    assert.equal(builderResponse.status(), 200, 'Scoped Builder document identity query must succeed');
    const builder = await builderResponse.json();
    assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder catalog generation must match the raw oracle generation');
    const document = builder.workspace?.documents?.find(item => item.output?.id === outputId);
    assert(document, 'The selected output must exist in the scoped Builder API response');
    assert.equal(document.rowResourceType ?? document.rootResourceType ?? document.document?.rootResourceType, 'Specimen');
    const table = page.getByTestId('preview-table-scroll').getByRole('table');
    await table.waitFor({ state: 'visible', timeout: 5000 });
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
    report.actions.push(...tracker.actions);
    (report.builderTimings ??= []).push(...tracker.timings);
    report.previewResponses = previewResponses;
    await captureBuilderScreenshot({ page, cda, report, name: 'preview-limits.png' });
    record(report, 'No unexpected console, page, or API failures', diagnostics.console.length === 0
      && diagnostics.pageErrors.length === 0 && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0, diagnostics);
  } catch (error) {
    failure = error;
  } finally {
    try {
      const sourceHashesAtEnd = {
        Specimen: await hashFile(oracle.specimenPath),
        Patient: await hashFile(oracle.patientPath),
      };
      const sourceUnchanged = JSON.stringify(sourceHashesAtEnd) === JSON.stringify(oracle.sourceHashes);
      report.assertions.push({ name: 'Raw CDA source files stayed unchanged', dimension: 'correctness',
        status: sourceUnchanged ? 'passed' : 'failed', evidence: { before: oracle.sourceHashes, after: sourceHashesAtEnd } });
      if (!sourceUnchanged) failure ??= new Error('Raw CDA source files changed during the native Playwright case');
    } catch (integrityError) {
      report.freezeError = String(integrityError.message ?? integrityError);
      report.assertions.push({ name: 'Raw CDA source files stayed unchanged', dimension: 'correctness',
        status: 'failed', evidence: { message: report.freezeError } });
      failure ??= integrityError;
    }
  }
  report.status = failure || report.assertions.some(assertion => assertion.status === 'failed') ? 'failed' : 'passed';
  if (failure) throw failure;
  return report;
}
