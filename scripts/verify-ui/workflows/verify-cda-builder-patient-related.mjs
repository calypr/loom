import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { writeFile } from 'node:fs/promises';
import { createInterface } from 'node:readline';
import { basename, resolve } from 'node:path';
import { captureBuilderScreenshot, measuredAction, record, requireUnique } from './verify-cda-builder-related-source-chooser.mjs';

const PREVIEW_LIMIT = 25;
const BASE_HEADERS = ['SPECIMEN ID', 'SUBJECT.REFERENCE', 'COLLECTION.BODYSITE.REFERENCE.REFERENCE'];
export const patientRelatedStepInspectionCases = Object.freeze(['Inspect saved related step', 'Inspect related edit']);
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

export function assertProposalOracle({ rows, expected, headers, patientColumnHeader = 'PATIENT ID' }) {
  assert.deepEqual(headers, [...BASE_HEADERS, patientColumnHeader]);
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
  await table.waitFor({ state: 'visible', timeout: 5000 });
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
  await proposalTable.waitFor({ state: 'visible', timeout: 5000 });
  await requireUnique(proposalTable, 'Patient related proposal preview table');
  const headers = (await proposalTable.getByRole('columnheader').allTextContents()).map(value => value.trim());
  const rows = await proposalTable.getByRole('row').evaluateAll(elements => elements.slice(1).map(row =>
    [...row.querySelectorAll('td')].map(cell => cell.innerText.trim())));
  return { headers, rows };
}

export async function runPatientRelatedApplyReload({ page, cda, explorerId = cda.target.explorer } = {}) {
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = cda.target;
  const evidenceDirectory = cda.evidence;
  const report = cda.report;
  const diagnostics = cda.diagnostics;
  const oracle = await readPatientRelatedOracle({ datasetDir: target.fixtureDir, project: target.fixtureProject, generation: target.fixtureGeneration });
  Object.assign(report, {
    schemaVersion: 1,
    scenario: 'cda-builder-patient-related-column-apply-reload',
    case: 'Verify Patient related column',
    status: 'running',
    target: { ...report.target, sourceRoot: target.sourceRoot,
      composeProject: target.composeProject, apiContainer: target.apiContainer, uiOrigin: target.uiUrl, apiOrigin: target.apiUrl,
      project: target.fixtureProject, generation: target.fixtureGeneration, explorerId },
    sourceOracle: { files: { Specimen: oracle.specimenPath, Patient: oracle.patientPath }, hashes: oracle.sourceHashes,
      specimenCount: oracle.specimenCount, patientCount: oracle.patientCount, previewRows: oracle.rows.length,
      ordering: 'Independent raw Specimen rows ordered by the exact Arango vertex key for project/generation; Patient references checked against raw Patient IDs' },
    path: 'Builder > Add columns > Fields and related data > Patient > Patient.id > Keep all matching values > Apply > reload',
    expectedVisibleResult: 'The full initial and applied 25-row preview windows match raw Specimen values and raw Patient membership; Apply persists one history step and the same result after reload.',
    independentOracle: 'Raw CDA-FHIR/META/Specimen.ndjson and Patient.ndjson; no preview value supplies an expected result.',
    lifecycle: { sourceSelection: 'untested', preview: 'untested', proposal: 'untested', apply: 'untested', reload: 'untested', edit: 'not covered', removal: 'not covered' },
    evidenceDirectory, });
  Object.defineProperty(report, 'nativeCheck', {
    configurable: true,
    value: (name, passed, evidence) => cda.check('correctness', name, passed, evidence),
  });
  const tracker = { actions: [], timings: [], cda };
  let lifecycleEvidence = {};

  let failure;
  try {
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

    const selectedTab = page.locator('button[data-testid^="construction-table-"][aria-pressed="true"]');
    await requireUnique(selectedTab, 'Selected Builder table');
    const outputId = (await selectedTab.getAttribute('data-testid')).slice('construction-table-'.length);
    const builderResponse = await cda.request.get(`${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`);
    assert.equal(builderResponse.status(), 200, 'Independent Builder document identity request must succeed');
    const builder = await builderResponse.json();
    assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder catalog generation must match the explicit raw CDA generation');
    const document = builder.workspace?.documents?.find(item => item.output?.id === outputId);
    assert(document, `Selected output ${outputId} must exist in the scoped Builder API response`);
    assert.equal(document.rowResourceType ?? document.rootResourceType ?? document.document?.rootResourceType, 'Specimen', 'This raw oracle is scoped to a Specimen root table');
    record(report, 'Selected output belongs to the explicit Specimen Builder document', true,
      { outputId, rowResourceType: document.rowResourceType ?? document.rootResourceType ?? document.document?.rootResourceType, catalogGeneration: builder.catalog?.generation });

    const previewTable = await visiblePreview(page);
    await previewTable.waitFor({ state: 'visible', timeout: 5000 });
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
    const reloadStart = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 5000 });
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

    (report.builderTimings ??= []).push(...tracker.timings);
    if (Object.keys(lifecycleEvidence).length) {
      await writeFile(`${evidenceDirectory}/lifecycle.json`, JSON.stringify(lifecycleEvidence, null, 2) + '\n', { mode: 0o600 });
      report.evidence ??= [];
      report.evidence.push('lifecycle.json');
    }
    await captureBuilderScreenshot({ page, cda, report, name: 'patient-related-applied.png' });
    const noUnexpectedDiagnostics = diagnostics.console.length === 0 && diagnostics.pageErrors.length === 0
      && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0;
    record(report, 'No unexpected console, page, or API failures', noUnexpectedDiagnostics, diagnostics);
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

export async function runPatientRelatedEditRemove({ page, cda, explorerId = cda.target.explorer } = {}) {
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = cda.target;
  const evidenceDirectory = cda.evidence;
  const report = cda.report;
  const diagnostics = cda.diagnostics;
  const oracle = await readPatientRelatedOracle({ datasetDir: target.fixtureDir, project: target.fixtureProject, generation: target.fixtureGeneration });
  Object.assign(report, {
    schemaVersion: 1, scenario: 'cda-builder-patient-related-edit-remove', case: 'Edit and remove Patient related column',
    status: 'running', target: { ...report.target, sourceRoot: target.sourceRoot, composeProject: target.composeProject, apiContainer: target.apiContainer,
      uiOrigin: target.uiUrl, apiOrigin: target.apiUrl, project: target.fixtureProject,
      generation: target.fixtureGeneration, explorerId },
    sourceOracle: { files: { Specimen: oracle.specimenPath, Patient: oracle.patientPath }, hashes: oracle.sourceHashes,
      specimenCount: oracle.specimenCount, patientCount: oracle.patientCount, rows: oracle.rows,
      ordering: 'Raw CDA rows ordered by exact project/generation Arango vertex storage key; Patient reference membership from raw Patient records' },
    path: 'Open saved Patient related step > edit output label > Apply > reload > remove saved step > Apply > reload',
    expectedVisibleResult: 'Editing persists the renamed Patient ID values; removing the related step and reloading restores the exact raw Specimen columns, nulls, order, and row count.',
    lifecycle: { edit: 'untested', apply: 'untested', reload: 'untested', removal: 'untested', restoration: 'untested' },
    evidenceDirectory, });
  Object.defineProperty(report, 'nativeCheck', {
    configurable: true,
    value: (name, passed, evidence) => cda.check('correctness', name, passed, evidence),
  });
  const tracker = { actions: [], timings: [], cda };
  const expectedAddedHeaders = [...BASE_HEADERS, 'PATIENT ID'];
  const readPreview = async (headers, applied) => {
    const result = await collectPreviewRows(page, { expectedCount: oracle.rows.length, expectedHeaders: headers });
    assertPreviewOracle({ ...result, expected: oracle.rows, headers, applied });
    return result;
  };
  const waitProposalReady = () => page.waitForFunction(
    () => document.querySelector('[data-testid="construction-proposal-panel"]')?.getAttribute('data-proposal-status') === 'ready',
    undefined, { timeout: 5000 });

  let failure;
  try {
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
    const response = await cda.request.get(`${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`);
    assert.equal(response.status(), 200, 'Scoped Builder document identity query must succeed');
    const builder = await response.json();
    assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder catalog generation must match the independent CDA source');
    const document = builder.workspace?.documents?.find(item => item.output?.id === outputId);
    assert(document, 'Selected output must exist in the scoped Builder response');
    assert.equal(document.rowResourceType ?? document.rootResourceType ?? document.document?.rootResourceType, 'Specimen');
    const steps = page.locator('[data-testid^="construction-history-step-"]');
    assert.equal(await steps.count(), 1, 'Edit/remove case requires exactly one saved Patient related step');
    let added = await readPreview(expectedAddedHeaders, true);
    report.initialAppliedRows = added.rows;
    record(report, 'Initial saved Patient related result matches independent raw sources', true,
      { headers: expectedAddedHeaders, rows: added.rows });

    const step = steps.first();
    await measuredAction(tracker, 'select saved Patient related step', step, button => button.click({ timeout: 5000 }),
      () => page.locator('[data-testid^="construction-edit-step-"]').waitFor({ state: 'visible', timeout: 5000 }));
    const edit = page.locator('[data-testid^="construction-edit-step-"]');
    await requireUnique(edit, 'Edit saved Patient related step');
    await measuredAction(tracker, 'open saved Patient related editor', edit, button => button.click({ timeout: 5000 }),
      () => page.getByRole('textbox', { name: 'Output column label', exact: true }).waitFor({ state: 'visible', timeout: 5000 }));
    const label = page.getByRole('textbox', { name: 'Output column label', exact: true });
    await requireUnique(label, 'Patient related output label');
    const originalLabel = await label.inputValue();
    assert.equal(originalLabel, 'Patient ID', 'Saved Patient related label must match the named source feature');
    await measuredAction(tracker, 'rename Patient related output', label,
      input => input.fill('Patient ID QA', { timeout: 5000 }), waitProposalReady, { editable: true });
    const proposal = page.getByTestId('construction-proposal-panel');
    assert.equal(await proposal.getAttribute('data-proposal-status'), 'ready');
    const editedProposalRows = await collectProposalRows(page);
    assertProposalOracle({ ...editedProposalRows, expected: oracle.rows, patientColumnHeader: 'PATIENT ID QA' });
    const applyEdit = page.getByTestId('construction-apply-proposal');
    await requireUnique(applyEdit, 'Apply renamed Patient ID step');
    assert.equal(await applyEdit.isEnabled(), true);
    const renamedStep = page.locator('[data-testid^="construction-history-step-"]');
    await measuredAction(tracker, 'Apply Patient ID label edit', applyEdit, button => button.click({ timeout: 5000 }),
      () => page.waitForFunction(() => document.querySelector('[data-testid^="construction-history-step-"]')?.innerText.includes('Patient ID QA') === true, undefined, { timeout: 5000 }));
    const renamedHistory = await renamedStep.first().innerText();
    report.lifecycle.edit = 'passed';
    report.lifecycle.apply = 'passed';
    record(report, 'Rename proposal applies and updates saved step label', true,
      { originalLabel, renamedHistory, proposalRows: editedProposalRows.rows });
    const reloadStart = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 5000 });
    const historyAfterReload = page.locator('[data-testid^="construction-history-step-"]');
    await historyAfterReload.waitFor({ state: 'visible', timeout: 5000 });
    const restoredEditHistory = await historyAfterReload.first().innerText();
    assert.equal(restoredEditHistory, renamedHistory, 'Reload must restore renamed Patient step');
    added = await readPreview([...BASE_HEADERS, 'PATIENT ID QA'], true);
    const editReloadMs = Date.now() - reloadStart;
    tracker.timings.push({ name: 'reload renamed Patient related result', elapsedMs: editReloadMs });
    assert(editReloadMs <= 5000, `Reload after edit took ${editReloadMs} ms; maximum is 5000 ms`);
    report.lifecycle.reload = 'passed';
    report.editedReloadRows = added.rows;
    record(report, 'Reload preserves renamed step and exact raw result', true, { history: restoredEditHistory, elapsedMs: editReloadMs, rows: added.rows });

    const restoredStep = historyAfterReload.first();
    await measuredAction(tracker, 'select renamed step for removal', restoredStep, button => button.click({ timeout: 5000 }),
      () => page.locator('[data-testid^="construction-remove-step-"]').waitFor({ state: 'visible', timeout: 5000 }));
    const remove = page.locator('[data-testid^="construction-remove-step-"]');
    await requireUnique(remove, 'Remove renamed Patient related step');
    await measuredAction(tracker, 'propose Patient related step removal', remove, button => button.click({ timeout: 5000 }), waitProposalReady);
    const removalPreview = await collectProposalRows(page);
    assert.deepEqual(removalPreview.headers, BASE_HEADERS, 'Removal proposal must restore the exact base columns');
    assert.deepEqual(removalPreview.rows, oracle.rows.slice(0, removalPreview.rows.length).map(row => [
      display(row.id), display(row.subject), display(row.bodySite),
    ]), 'Removal proposal differs from independent raw Specimen rows');
    const applyRemoval = page.getByTestId('construction-apply-proposal');
    await requireUnique(applyRemoval, 'Apply Patient related step removal');
    const previewTable = page.getByTestId('preview-table-scroll').getByRole('table');
    await measuredAction(tracker, 'Apply Patient related step removal', applyRemoval,
      button => button.click({ timeout: 5000 }), async () => {
        await page.waitForFunction(() => document.querySelectorAll('[data-testid^="construction-history-step-"]').length === 0, undefined, { timeout: 5000 });
        await previewTable.waitFor({ state: 'visible', timeout: 5000 });
      });
    report.lifecycle.removal = 'passed';
    const restored = await readPreview(BASE_HEADERS, false);
    report.restoredRows = restored.rows;
    record(report, 'Removal restores raw Specimen preview values and multiplicity', true, { rows: restored.rows, historyStepCount: 0 });
    const restoreReloadStart = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 5000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 5000 });
    assert.equal(await page.locator('[data-testid^="construction-history-step-"]').count(), 0,
      'Reload after removal must retain an empty construction history');
    const afterReload = await readPreview(BASE_HEADERS, false);
    const restoreReloadMs = Date.now() - restoreReloadStart;
    tracker.timings.push({ name: 'reload after removal and restore source result', elapsedMs: restoreReloadMs });
    assert(restoreReloadMs <= 5000, `Reload after removal took ${restoreReloadMs} ms; maximum is 5000 ms`);
    assert.deepEqual(afterReload.rows, restored.rows, 'Reload after removal must preserve the exact original source window');
    report.lifecycle.restoration = 'passed';
    record(report, 'Reload after removal restores the exact original result', true, { elapsedMs: restoreReloadMs, rows: afterReload.rows });
    (report.builderTimings ??= []).push(...tracker.timings);
    await captureBuilderScreenshot({ page, cda, report, name: 'patient-related-restored.png' });
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

export async function runPatientRelatedStepInspection({ page, cda, action, explorerId = cda.target.explorer } = {}) {
  assert(patientRelatedStepInspectionCases.includes(action), `Unsupported saved Patient related inspection: ${action}`);
  assert(String(explorerId ?? '').trim(), 'Pass an explicit Builder Explorer ID');
  const target = cda.target;
  const evidenceDirectory = cda.evidence;
  const report = cda.report;
  const diagnostics = cda.diagnostics;
  Object.assign(report, {
    schemaVersion: 1, scenario: 'cda-builder-patient-related-saved-step-inspection', case: action, status: 'running',
    target: { ...report.target, sourceRoot: target.sourceRoot,
      composeProject: target.composeProject, apiContainer: target.apiContainer, uiOrigin: target.uiUrl, apiOrigin: target.apiUrl,
      project: target.fixtureProject, generation: target.fixtureGeneration, explorerId },
    path: 'Builder > select saved Patient related step > inspect available controls' + (action === 'Inspect related edit' ? ' > Edit' : ''),
    expectedVisibleResult: action === 'Inspect related edit'
      ? 'Saved Patient ID label is editable in a visible enabled editor.'
      : 'One saved Patient related step exposes visible enabled Edit and Remove controls.',
    independentOracle: 'This is a persisted-control inspection only; it makes no computed-row or persistence claim.',
    lifecycle: { savedStep: 'untested', edit: action === 'Inspect related edit' ? 'untested' : 'not applicable', apply: 'not applicable', reload: 'not applicable', removal: 'not applicable' },
    evidenceDirectory, });
  Object.defineProperty(report, 'nativeCheck', {
    configurable: true,
    value: (name, passed, evidence) => cda.check('correctness', name, passed, evidence),
  });
  const tracker = { actions: [], timings: [], cda };

    const url = new URL(target.uiUrl);
    url.searchParams.set('project', target.fixtureProject);
    url.searchParams.set('explorer', explorerId);
    url.searchParams.set('mode', 'builder');
    await page.goto(url.toString(), { waitUntil: 'domcontentloaded', timeout: 5000 });
    await page.getByText('DATASET WORKSPACE', { exact: true }).waitFor({ state: 'visible', timeout: 5000 });
    const explorer = page.getByRole('combobox', { name: 'Explorer', exact: true });
    await requireUnique(explorer, 'Explorer');
    record(report, 'Builder is scoped to requested Explorer', await explorer.inputValue() === explorerId,
      { expected: explorerId, actual: await explorer.inputValue() });
    const steps = page.locator('[data-testid^="construction-history-step-"]');
    await requireUnique(steps, 'Saved Patient related step');
    const step = steps.first();
    await measuredAction(tracker, 'select saved Patient related step', step, button => button.click({ timeout: 5000 }),
      () => page.locator('[data-testid^="construction-edit-step-"]').waitFor({ state: 'visible', timeout: 5000 }));
    const edit = page.locator('[data-testid^="construction-edit-step-"]');
    const remove = page.locator('[data-testid^="construction-remove-step-"]');
    await requireUnique(edit, 'Edit saved Patient related step');
    await requireUnique(remove, 'Remove saved Patient related step');
    assert(await edit.isVisible() && await edit.isEnabled(), 'Saved Patient related step Edit control must be visible and enabled');
    assert(await remove.isVisible() && await remove.isEnabled(), 'Saved Patient related step Remove control must be visible and enabled');
    const controls = await page.locator('[data-testid^="construction-edit-step-"], [data-testid^="construction-remove-step-"]').evaluateAll(elements => elements.map(element => ({
      testId: element.getAttribute('data-testid'), label: element.getAttribute('aria-label'), text: element.innerText.trim(), disabled: element.disabled,
    })));
    report.lifecycle.savedStep = 'passed';
    record(report, 'Saved Patient related step exposes actionable edit and remove controls', true, { controls });
    let editor;
    if (action === 'Inspect related edit') {
      const label = page.getByRole('textbox', { name: 'Output column label', exact: true });
      await measuredAction(tracker, 'open saved Patient related editor', edit, button => button.click({ timeout: 5000 }),
        () => label.waitFor({ state: 'visible', timeout: 5000 }));
      await requireUnique(label, 'Patient related output label');
      assert.equal(await label.isEditable(), true, 'Saved Patient ID output label must be editable');
      editor = { value: await label.inputValue(), enabled: await label.isEnabled() };
      assert.equal(editor.value, 'Patient ID', 'Saved Patient related editor must identify Patient ID');
      report.lifecycle.edit = 'passed';
      record(report, 'Patient ID output label is editable with its saved value', true, editor);
    }
    (report.builderTimings ??= []).push(...tracker.timings);
    await captureBuilderScreenshot({ page, cda, report, name: 'saved-related-step.png' });
    await writeFile(`${evidenceDirectory}/state.json`, JSON.stringify({ action, controls, editor, timings: tracker.timings }, null, 2) + '\n', { mode: 0o600 });
    report.evidence ??= [];
    report.evidence.push('state.json');
    record(report, 'No unexpected console, page, or API failures', diagnostics.console.length === 0
      && diagnostics.pageErrors.length === 0 && diagnostics.networkFailures.length === 0 && diagnostics.httpFailures.length === 0, diagnostics);
    report.status = 'partial';
  return report;
}
