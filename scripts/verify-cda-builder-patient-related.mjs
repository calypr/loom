import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { mkdir, writeFile } from 'node:fs/promises';
import { createInterface } from 'node:readline';
import { basename, resolve } from 'node:path';
import { apiBuildIdentity, measuredAction, record, targetFromEnvironment } from './verify-cda-builder-related-source-chooser.mjs';
import { launchBrowser, sanitizeText } from './lib/playwright-browser.mjs';
import { requireUnique } from './lib/playwright-actions.mjs';
import { sourceFingerprintChangedPaths, sourceFingerprintWithManifest } from './verify-ui/source-fingerprint.mjs';

const PREVIEW_LIMIT = 25;
const BASE_HEADERS = ['SPECIMEN ID', 'SUBJECT.REFERENCE', 'COLLECTION.BODYSITE.REFERENCE.REFERENCE'];
const sha256 = value => createHash('sha256').update(value).digest('hex');
const hashFile = async path => {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest('hex');
};
const display = value => value === null || value === undefined || value === '' ? '—' : String(value);
const storageKey = (project, generation, id) => sha256(['vertex', project, generation, 'Specimen', id, ''].join('\0'));

export async function readPatientRelatedOracle({ datasetDir, project, generation }) {
  assert(project && generation, 'Oracle project and generation must be explicit');
  const resourceDirectory = basename(resolve(datasetDir)) === 'META' ? resolve(datasetDir) : resolve(datasetDir, 'META');
  const specimenPath = resolve(resourceDirectory, 'Specimen.ndjson');
  const patientPath = resolve(resourceDirectory, 'Patient.ndjson');
  const specimens = [];
  const patientIds = new Set();
  let patientRecordCount = 0;
  const hashes = {};
  for (const [resourceType, path, visit] of [
    ['Specimen', specimenPath, record => {
      assert.equal(record.resourceType, 'Specimen', 'Specimen source contains another resource type');
      assert(typeof record.id === 'string' && record.id, 'Specimen source contains an empty id');
      specimens.push({
        id: record.id,
        subject: record.subject?.reference ?? null,
        bodySite: record.collection?.bodySite?.reference?.reference ?? null,
      });
    }],
    ['Patient', patientPath, record => {
      assert.equal(record.resourceType, 'Patient', 'Patient source contains another resource type');
      assert(typeof record.id === 'string' && record.id, 'Patient source contains an empty id');
      patientRecordCount += 1;
      patientIds.add(record.id);
    }],
  ]) {
    const hash = createHash('sha256');
    const input = createReadStream(path);
    input.on('data', chunk => hash.update(chunk));
    for await (const line of createInterface({ input, crlfDelay: Infinity })) {
      if (line.trim()) visit(JSON.parse(line));
    }
    hashes[resourceType] = hash.digest('hex');
  }
  assert(specimens.length > 0 && patientIds.size > 0, 'Independent CDA resources must not be empty');
  assert.equal(new Set(specimens.map(specimen => specimen.id)).size, specimens.length, 'Specimen source IDs must be unique');
  assert.equal(patientIds.size, patientRecordCount, 'Patient source IDs must be unique');
  const ordered = specimens.map(specimen => ({
    key: storageKey(project, generation, specimen.id), specimen,
  })).sort((left, right) => left.key.localeCompare(right.key)).slice(0, PREVIEW_LIMIT);
  const rows = ordered.map(({ specimen }) => ({
    id: specimen.id,
    subject: specimen.subject,
    bodySite: specimen.bodySite,
    patientId: typeof specimen.subject === 'string' && specimen.subject.startsWith('Patient/')
      && patientIds.has(specimen.subject.slice('Patient/'.length))
      ? specimen.subject.slice('Patient/'.length) : null,
  }));
  return { specimenPath, patientPath, sourceHashes: hashes, specimenCount: specimens.length, patientCount: patientIds.size, rows };
}

export function assertPreviewOracle({ rows, expected, ariaRowCount, headers, applied }) {
  assert.deepEqual(headers, applied ? [...BASE_HEADERS, 'PATIENT ID'] : BASE_HEADERS);
  assert.equal(Number(ariaRowCount), expected.length + 1, 'Preview must report its complete independent source window and header');
  assert.deepEqual(rows, expected.map((row, index) => ({
    ordinal: index + 1,
    cells: applied
      ? [display(row.id), display(row.subject), display(row.bodySite), display(row.patientId)]
      : [display(row.id), display(row.subject), display(row.bodySite)],
  })), 'Visible Preview rows differ from the independent raw CDA oracle');
}

export function assertProposalOracle({ rows, expected, headers }) {
  assert.deepEqual(headers, [...BASE_HEADERS, 'PATIENT ID']);
  assert(rows.length > 0 && rows.length <= expected.length, 'Proposal must expose a non-empty subset of the independent preview window');
  assert.deepEqual(rows, expected.slice(0, rows.length).map(row => [
    display(row.id), display(row.subject), display(row.bodySite), display(row.patientId),
  ]), 'Proposal rows differ from the independent raw CDA oracle');
}

export function assertPersistedPatientRelated({ expectedHistory, actualHistory, rows, expected, headers, ariaRowCount }) {
  assert.equal(actualHistory, expectedHistory, 'Reload must restore the same selected history step');
  assertPreviewOracle({ rows, expected, ariaRowCount, headers, applied: true });
}

const visiblePreview = async page => page.getByTestId('preview-table-scroll').getByRole('table');

export async function collectPreviewRows(page, { expectedCount, expectedHeaders }) {
  const scroll = page.getByTestId('preview-table-scroll');
  const table = scroll.getByRole('table');
  await requireUnique(table, 'Preview table');
  const headers = await table.getByRole('columnheader').allTextContents();
  assert.deepEqual(headers.map(value => value.trim()), expectedHeaders);
  const ariaRowCount = await table.getAttribute('aria-rowcount');
  const rows = new Map();
  const readVisibleRows = async () => {
    const visible = await table.getByRole('row').evaluateAll(elements => elements.slice(1).map(row => ({
      ordinal: Number(row.querySelector('button[aria-label^="Inspect row "]')?.getAttribute('aria-label')?.match(/Inspect row (\d+) identity/)?.[1]),
      cells: [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()),
    })));
    for (const row of visible) {
      assert(Number.isInteger(row.ordinal) && row.ordinal >= 1, 'Every preview row must expose its ordinal identity');
      rows.set(row.ordinal, row);
    }
  };
  await readVisibleRows();
  let lastMax = Math.max(0, ...rows.keys());
  const bounds = await scroll.boundingBox();
  assert(bounds, 'Preview scroll container must have a visible box');
  await page.mouse.move(bounds.x + Math.min(bounds.width / 2, 80), bounds.y + Math.min(bounds.height / 2, 100));
  while (rows.size < expectedCount) {
    await page.mouse.wheel(0, 420);
    await page.waitForFunction((previousMax) => {
      const tableElement = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      const ordinals = [...(tableElement?.querySelectorAll('[role="row"] button[aria-label^="Inspect row "]') ?? [])]
        .map(button => Number(button.getAttribute('aria-label')?.match(/Inspect row (\d+) identity/)?.[1]));
      return ordinals.some(ordinal => ordinal > previousMax);
    }, lastMax, { timeout: 5000 });
    await readVisibleRows();
    const nextMax = Math.max(...rows.keys());
    assert(nextMax > lastMax, 'Native preview scrolling must reveal a later source ordinal');
    lastMax = nextMax;
    assert(rows.size <= expectedCount, 'Preview exposes more rows than its source oracle');
  }
  const orderedRows = [...rows.values()].sort((left, right) => left.ordinal - right.ordinal);
  assert.equal(orderedRows.length, expectedCount, 'Every expected source row must be observed exactly once');
  return { rows: orderedRows, ariaRowCount };
}

export async function collectProposalRows(page) {
  const proposalTable = page.getByTestId('construction-proposal-preview').getByRole('table');
  await requireUnique(proposalTable, 'Patient related proposal preview table');
  const headers = (await proposalTable.getByRole('columnheader').allTextContents()).map(value => value.trim());
  const rows = await proposalTable.getByRole('row').evaluateAll(elements => elements.slice(1).map(row =>
    [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())));
  return { headers, rows };
}

export async function runPatientRelatedApplyReload({ explorerId, env = process.env } = {}) {
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = await targetFromEnvironment(env);
  const oracle = await readPatientRelatedOracle({ datasetDir: target.fixtureDir, project: target.fixtureProject, generation: target.fixtureGeneration });
  const evidenceDirectory = resolve(target.artifacts, `playwright-patient-related-apply-reload-${new Date().toISOString().replaceAll(':', '-')}`);
  await mkdir(evidenceDirectory, { recursive: true, mode: 0o700 });
  const sourceAtStart = sourceFingerprintWithManifest(target.sourceRoot);
  const buildAtStart = apiBuildIdentity(target);
  const report = {
    schemaVersion: 1,
    scenario: 'cda-builder-patient-related-column-apply-reload',
    case: 'Verify Patient related column',
    status: 'running',
    target: { sourceRoot: target.sourceRoot, sourceFingerprint: sourceAtStart.fingerprint, apiBuildIdentity: buildAtStart,
      composeProject: target.composeProject, apiContainer: env.LOOM_CDA_API_CONTAINER, uiOrigin: target.uiUrl, apiOrigin: target.apiUrl,
      project: target.fixtureProject, generation: target.fixtureGeneration, explorerId },
    sourceOracle: { files: { Specimen: oracle.specimenPath, Patient: oracle.patientPath }, hashes: oracle.sourceHashes,
      specimenCount: oracle.specimenCount, patientCount: oracle.patientCount, previewRows: oracle.rows.length,
      ordering: 'Independent raw Specimen rows ordered by the exact Arango vertex key for project/generation; Patient references checked against raw Patient IDs' },
    path: 'Builder > Add columns > Fields and related data > Patient > Patient.id > Keep all matching values > Apply > reload',
    expectedVisibleResult: 'The full initial and applied 25-row preview windows match raw Specimen values and raw Patient membership; Apply persists one history step and the same result after reload.',
    independentOracle: 'Raw CDA-FHIR/META/Specimen.ndjson and Patient.ndjson; no preview value supplies an expected result.',
    lifecycle: { sourceSelection: 'untested', preview: 'untested', proposal: 'untested', apply: 'untested', reload: 'untested', edit: 'not covered', removal: 'not covered' },
    evidenceDirectory, assertions: [], actions: [], timings: [],
  };
  const tracker = { actions: [], timings: [] };
  let browser;
  let activeAction = { label: 'launch Playwright browser', locator: 'Chromium launch' };
  let failure;
  let lifecycleEvidence = {};
  try {
    browser = await launchBrowser({ evidence: evidenceDirectory, appOrigins: [target.uiUrl, target.apiUrl], noAuth: true });
    const { page, diagnostics } = browser;
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

    const selectedTab = page.locator('button[data-testid^="construction-table-"][aria-pressed="true"]');
    await requireUnique(selectedTab, 'Selected Builder table');
    const outputId = (await selectedTab.getAttribute('data-testid')).slice('construction-table-'.length);
    const builderResponse = await browser.context.request.get(`${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`);
    assert.equal(builderResponse.status(), 200, 'Independent Builder document identity request must succeed');
    const builder = await builderResponse.json();
    assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder catalog generation must match the explicit raw CDA generation');
    const document = builder.workspace?.documents?.find(item => item.output?.id === outputId);
    assert(document, `Selected output ${outputId} must exist in the scoped Builder API response`);
    assert.equal(document.rowResourceType ?? document.rootResourceType ?? document.document?.rootResourceType, 'Specimen', 'This raw oracle is scoped to a Specimen root table');
    record(report, 'Selected output belongs to the explicit Specimen Builder document', true,
      { outputId, rowResourceType: document.rowResourceType ?? document.rootResourceType ?? document.document?.rootResourceType, catalogGeneration: builder.catalog?.generation });

    const previewTable = await visiblePreview(page);
    await previewTable.waitFor({ state: 'visible', timeout: 15000 });
    const initial = await collectPreviewRows(page, { expectedCount: oracle.rows.length, expectedHeaders: BASE_HEADERS });
    assertPreviewOracle({ ...initial, expected: oracle.rows, headers: BASE_HEADERS, applied: false });
    lifecycleEvidence.initial = initial;
    record(report, 'Initial preview matches the independent raw Specimen oracle', true,
      { sourceRowCount: oracle.rows.length, ariaRowCount: initial.ariaRowCount, rows: initial.rows });
    report.lifecycle.preview = 'passed';
    const preexistingHistory = page.locator('[data-testid^="construction-history-step-"]');
    assert.equal(await preexistingHistory.count(), 0, 'Apply case requires a clean Explorer with no existing construction history');
    record(report, 'Explorer starts without saved construction changes', true, { historyStepCount: 0 });

    const addColumns = page.locator('button[aria-label^="Add columns:"]');
    await requireUnique(addColumns, 'Add columns');
    const sourcePanel = page.getByTestId('construction-add-columns-source');
    activeAction = { label: 'open Add columns', locator: addColumns.toString(), targetLocator: addColumns };
    await measuredAction(tracker, 'open Add columns', addColumns, button => button.click({ timeout: 5000 }), () => sourcePanel.waitFor({ state: 'visible', timeout: 5000 }));
    const fieldsTab = page.getByRole('button', { name: 'Fields and related data', exact: true });
    await requireUnique(fieldsTab, 'Fields and related data');
    if (await fieldsTab.getAttribute('aria-pressed') === 'false') {
      await measuredAction(tracker, 'open Fields and related data', fieldsTab, button => button.click({ timeout: 5000 }),
        () => page.getByRole('checkbox', { name: 'Select Patient.id', exact: true }).waitFor({ state: 'visible', timeout: 5000 }));
    }
    const patientSource = sourcePanel.getByRole('button', { name: /^Patient,/ });
    await requireUnique(patientSource, 'Patient related source');
    await measuredAction(tracker, 'select Patient related source', patientSource, button => button.click({ timeout: 5000 }),
      () => page.getByRole('checkbox', { name: 'Select Patient.id', exact: true }).waitFor({ state: 'visible', timeout: 5000 }));
    const patientId = page.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
    await requireUnique(patientId, 'Select Patient.id');
    assert.equal(await patientId.isEnabled(), true);
    await measuredAction(tracker, 'select Patient.id', patientId, checkbox => checkbox.check({ timeout: 5000 }),
      async () => assert.equal(await page.getByRole('button', { name: 'Add 1 selected feature', exact: true }).isEnabled(), true));
    const addField = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
    const dialog = page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true });
    await measuredAction(tracker, 'open Patient.id choice dialog', addField, button => button.click({ timeout: 5000 }),
      () => dialog.waitFor({ state: 'visible', timeout: 5000 }));
    const allValues = dialog.getByRole('radio', { name: 'Patient ID: Keep all matching values', exact: true });
    await requireUnique(allValues, 'Keep all Patient ID values');
    await measuredAction(tracker, 'select Keep all matching values', allValues, radio => radio.check({ timeout: 5000 }),
      async () => assert.equal(await allValues.isChecked(), true));
    const addColumn = dialog.getByRole('button', { name: 'Add 1 column', exact: true });
    await requireUnique(addColumn, 'Add 1 column');
    const proposal = page.getByTestId('construction-proposal-panel');
    await measuredAction(tracker, 'propose Patient ID column', addColumn, button => button.click({ timeout: 5000 }),
      () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready', undefined, { timeout: 5000 }));
    assert.equal(await proposal.getAttribute('data-proposal-status'), 'ready');
    const proposedRows = await collectProposalRows(page);
    assertProposalOracle({ ...proposedRows, expected: oracle.rows });
    lifecycleEvidence.proposal = proposedRows;
    record(report, 'Proposal preview matches raw Specimen and Patient sources', true,
      { rows: proposedRows.rows });
    report.lifecycle.proposal = 'passed';
    const apply = page.getByTestId('construction-apply-proposal');
    await requireUnique(apply, 'Apply Patient related column');
    assert.equal(await apply.isEnabled(), true, 'Apply must be enabled for the ready Patient ID proposal');
    await measuredAction(tracker, 'Apply Patient related column', apply, button => button.click({ timeout: 5000 }),
      async () => {
        await page.locator('[data-testid^="construction-history-step-"]').waitFor({ state: 'visible', timeout: 5000 });
        await page.getByTestId('preview-table-scroll').getByRole('table').waitFor({ state: 'visible', timeout: 5000 });
      });
    const historySteps = page.locator('[data-testid^="construction-history-step-"]');
    assert.equal(await historySteps.count(), 1, 'Apply must persist exactly one construction history step');
    const appliedHistory = await historySteps.first().innerText();
    report.lifecycle.apply = 'passed';
    record(report, 'Apply creates one visible persisted history step', true, { history: appliedHistory });
    const appliedRows = await collectPreviewRows(page, { expectedCount: oracle.rows.length, expectedHeaders: [...BASE_HEADERS, 'PATIENT ID'] });
    assertPreviewOracle({ ...appliedRows, expected: oracle.rows, headers: [...BASE_HEADERS, 'PATIENT ID'], applied: true });
    lifecycleEvidence.applied = appliedRows;

    activeAction = { label: 'reload Builder', locator: builderURL.toString() };
    const reloadStart = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 15000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 15000 });
    const restoredHistory = page.locator('[data-testid^="construction-history-step-"]');
    await restoredHistory.waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await restoredHistory.count(), 1, 'Reload must restore exactly one saved Patient related step');
    const restoredText = await restoredHistory.first().innerText();
    const restoredRows = await collectPreviewRows(page, { expectedCount: oracle.rows.length, expectedHeaders: [...BASE_HEADERS, 'PATIENT ID'] });
    assertPersistedPatientRelated({ expectedHistory: appliedHistory, actualHistory: restoredText, ...restoredRows,
      expected: oracle.rows, headers: [...BASE_HEADERS, 'PATIENT ID'] });
    const reloadElapsed = Date.now() - reloadStart;
    tracker.timings.push({ name: 'reload and verify persisted preview', elapsedMs: reloadElapsed, status: 'passed' });
    assert(reloadElapsed <= 5000, `Reload-to-restored-result took ${reloadElapsed} ms; maximum is 5000 ms`);
    lifecycleEvidence.reloaded = restoredRows;
    report.lifecycle.reload = 'passed';
    record(report, 'Reload restores the saved step and independent result', true,
      { history: restoredText, elapsedMs: reloadElapsed, rows: restoredRows.rows });

    report.timings = tracker.timings;
    await page.screenshot({ path: `${evidenceDirectory}/patient-related-applied.png`, fullPage: true });
    report.evidence = ['patient-related-applied.png'];
    const noUnexpectedDiagnostics = diagnostics.console.length === 0 && diagnostics.pageErrors.length === 0
      && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0;
    record(report, 'No unexpected console, page, or API failures', noUnexpectedDiagnostics, diagnostics);
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
    if (browser) await browser.close().catch(error => { report.closeError = sanitizeText(error.message); });
    try {
      const sourceHashesAtEnd = {
        Specimen: await hashFile(oracle.specimenPath),
        Patient: await hashFile(oracle.patientPath),
      };
      const datasetUnchanged = JSON.stringify(sourceHashesAtEnd) === JSON.stringify(oracle.sourceHashes);
      report.assertions.push({ name: 'Raw CDA source files stayed unchanged', status: datasetUnchanged ? 'passed' : 'failed', evidence: { before: oracle.sourceHashes, after: sourceHashesAtEnd } });
      if (!datasetUnchanged) failure ??= new Error('Raw CDA source files changed during the browser case');
      const sourceAtEnd = sourceFingerprintWithManifest(target.sourceRoot);
      const changedPaths = sourceFingerprintChangedPaths(sourceAtStart.manifest, sourceAtEnd.manifest);
      const unchanged = sourceAtStart.fingerprint.sha256 === sourceAtEnd.fingerprint.sha256;
      report.assertions.push({ name: 'Watched source stayed unchanged', status: unchanged ? 'passed' : 'failed', evidence: { before: sourceAtStart.fingerprint, after: sourceAtEnd.fingerprint, changedPaths } });
      if (!unchanged) failure ??= new Error('Watched source changed during the browser run');
      const buildAtEnd = apiBuildIdentity(target);
      const buildUnchanged = buildAtStart === buildAtEnd;
      report.assertions.push({ name: 'API build identity stayed unchanged', status: buildUnchanged ? 'passed' : 'failed', evidence: { before: buildAtStart, after: buildAtEnd } });
      if (!buildUnchanged) failure ??= new Error('API build identity changed during the browser run');
    } catch (freezeError) {
      report.freezeError = sanitizeText(freezeError.message ?? freezeError);
      report.assertions.push({ name: 'Watched source and API build stayed unchanged', status: 'failed', evidence: { message: report.freezeError } });
      failure ??= freezeError;
    }
    if (Object.keys(lifecycleEvidence).length) await writeFile(`${evidenceDirectory}/lifecycle.json`, JSON.stringify(lifecycleEvidence, null, 2) + '\n', { mode: 0o600 });
    report.evidence ??= [];
    if (Object.keys(lifecycleEvidence).length) report.evidence.push('lifecycle.json');
    report.status = failure || report.assertions.some(assertion => assertion.status === 'failed') ? 'failed' : 'passed';
    report.finishedAt = new Date().toISOString();
    await writeFile(`${evidenceDirectory}/report.json`, JSON.stringify(report, null, 2) + '\n', { mode: 0o600 });
  }
  if (failure) throw failure;
  return report;
}
