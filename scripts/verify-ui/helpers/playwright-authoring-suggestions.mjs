import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { patientWindow } from './playwright-authoring.mjs';
import { configureNativePage } from './playwright-authoring-page.mjs';
import { browserURL } from '../workflows/builder-url.mjs';
import { recordCheck, recordUntested } from './report.mjs';

const unique = async locator => {
  const count = await locator.count();
  assert.equal(count, 1, `Expected one target for ${locator.toString()}, found ${count}`);
  return locator;
};

const checkUnexpectedDiagnostics = (report, target) => {
  const expectedAbortPaths = new Set([target.bootstrapExplorerId, report.target.explorer]
    .filter(Boolean)
    .flatMap(explorer => {
      const explorerRoot = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
      return ['frame-source-options', 'semantic-inventory', 'construction-capabilities']
        .map(endpoint => `${explorerRoot}/${endpoint}`);
    }));
  const cancelledReads = report.network.filter(item => {
    if (item.kind !== 'network' || item.errorText !== 'net::ERR_ABORTED' || item.method !== 'POST') return false;
    try {
      const url = new URL(item.url);
      return url.origin === new URL(target.uiUrl).origin && expectedAbortPaths.has(url.pathname);
    } catch { return false; }
  });
  report.cancelledOwnedReads = cancelledReads;
  const unexpected = report.network.filter(item => !cancelledReads.includes(item));
  recordCheck(report, 'correctness', 'no unexpected network, API, or browser errors', unexpected.length === 0,
    { console: unexpected.filter(item => item.kind === 'console-error'),
      pageErrors: unexpected.filter(item => item.kind === 'exception'),
      networkFailures: unexpected.filter(item => item.kind === 'network') });
};

export const builderAuthoringSuggestionsWorkflow = async (workflow, context) => {
  const { page, report, action, check } = workflow;
  configureNativePage(page);
  const target = context.target;
  const sourceContents = readFileSync(join(target.fixtureDir, 'Patient.ndjson'));
  const sourceSHA256 = createHash('sha256').update(sourceContents).digest('hex');
  const sourceIds = sourceContents.toString('utf8').trim().split('\n').filter(Boolean)
    .map(line => JSON.parse(line).id).sort();
  const expectedIds = patientWindow(target, sourceIds);
  report.target.fixtureOracle = {
    path: join(target.fixtureDir, 'Patient.ndjson'),
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sourcePatientCount: sourceIds.length,
    sha256: sourceSHA256,
    previewIds: expectedIds,
  };
  const act = async (name, locator, perform, options = {}) => {
    await unique(locator);
    return action(name, locator, perform, options);
  };
  const title = `Verify ${context.runID.slice(-10)} suggestions`;
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), { waitUntil: 'domcontentloaded' });
  await page.getByText('New explorer', { exact: true }).waitFor({ state: 'visible' });
  await act('open Explorer creation', page.getByText('New explorer', { exact: true }),
    () => page.getByText('New explorer', { exact: true }).click());
  const nameInput = page.locator('#new-explorer-name');
  await act('name Explorer', nameInput, () => nameInput.fill(title), { editable: true });
  const createBlank = page.getByRole('button', { name: 'Create blank' });
  await act('create blank Explorer', createBlank, () => createBlank.click());
  await page.waitForFunction(expectedTitle => {
    const select = document.querySelector('select[aria-label="Explorer"]');
    return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
  }, title);
  const explorer = await page.getByRole('combobox', { name: 'Explorer' }).inputValue();
  check('correctness', 'created a fresh Explorer distinct from the bootstrap',
    Boolean(explorer && explorer !== target.bootstrapExplorerId), { title });
  report.target.explorer = explorer;

  const tableName = page.locator('#first-table-name');
  await act('name Patient table', tableName, () => tableName.fill('Patients'), { editable: true });
  await act('choose Patient rows', page.getByRole('button', { name: 'Choose Patient rows' }),
    () => page.getByRole('button', { name: 'Choose Patient rows' }).click());
  await page.waitForFunction(rowCount =>
    document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === String(rowCount),
  expectedIds.length + 1);
  await page.getByTestId('construction-action-add-columns').waitFor({ state: 'visible' });
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-action-add-columns"]');
    return button && !button.disabled;
  });
  const addColumns = page.getByRole('button', { name: /Add columns:/ });
  await act('open Add columns', addColumns, () => addColumns.click());
  await page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible' });
  const fieldsRelated = page.getByRole('button', { name: 'Fields and related data' });
  await act('choose Fields and related data', fieldsRelated, () => fieldsRelated.click());
  const rawFields = page.getByText('Raw FHIR fields (advanced)', { exact: true });
  await act('open Raw FHIR fields', rawFields, () => rawFields.click());
  const rawFieldSection = page.getByTestId('feature-catalog-raw-fields');
  const catalogChoices = rawFieldSection.locator('input[aria-label^="Select Patient."]');
  await catalogChoices.first().waitFor({ state: 'visible' });
  const candidateCount = await catalogChoices.count();
  report.target.candidateEvidence = {
    source: 'Builder rendered catalog-backed Patient candidate controls',
    count: candidateCount,
    labels: await catalogChoices.evaluateAll(inputs => inputs.map(input => input.getAttribute('aria-label')).sort()),
  };
  const idCandidate = rawFieldSection.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
  await unique(idCandidate);
  await idCandidate.waitFor({ state: 'visible' });
  const idCandidateState = {
    visible: await idCandidate.isVisible(),
    enabled: await idCandidate.isEnabled(),
    checked: await idCandidate.isChecked(),
  };
  check('correctness', 'catalog-backed Patient candidates are rendered', Number(candidateCount) > 0,
    { candidateCount, labels: report.target.candidateEvidence.labels });
  check('usability', 'Patient ID candidate control is visible and actionable',
    idCandidateState.visible && idCandidateState.enabled && !idCandidateState.checked,
    idCandidateState);
  await act('select Patient ID candidate', idCandidate, () => idCandidate.check());
  recordUntested(report, 'usability', 'lazy suggestion request failure and recovery',
    'The isolated development catalog already includes Patient candidates, so ensureSuggestions returns without issuing a suggestions request.');
  checkUnexpectedDiagnostics(report, target);

  const sourceSHA256After = createHash('sha256')
    .update(readFileSync(join(target.fixtureDir, 'Patient.ndjson'))).digest('hex');
  recordCheck(report, 'correctness', 'independent Patient source stayed unchanged during browser run',
    sourceSHA256After === sourceSHA256, { before: sourceSHA256, after: sourceSHA256After });
};
