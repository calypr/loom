import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { browserURL } from './builder-url.mjs';

const CODE_SYSTEM = 'http://snomed.info/sct';
const CODE = '1222590007';
const VALUE_PATH = 'valueCodeableConcept.text';
const FALLBACK_PATH = 'valueCodeableConcept.coding[].display';
const ROUTE = [
  { fromResourceType: 'Medication', toResourceType: 'MedicationAdministration', relationship: 'medication_reference_Medication', storageDirection: 'INBOUND' },
  { fromResourceType: 'MedicationAdministration', toResourceType: 'Patient', relationship: 'subject_Patient', storageDirection: 'OUTBOUND' },
  { fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'focus_Patient', storageDirection: 'INBOUND' },
].map(step => ({ ...step, matchMode: 'OPTIONAL' }));

const normalizedRoute = route => (route ?? []).map(({ fromResourceType, toResourceType, relationship, storageDirection, matchMode }) => ({
  fromResourceType, toResourceType, relationship, storageDirection, matchMode,
}));
const routeSummary = route => {
  const relationshipField = step => [step.fromResourceType, step.toResourceType].reduce((name, resourceType) =>
    name.endsWith(`_${resourceType}`) || name.endsWith(`-${resourceType}`)
      ? name.slice(0, -resourceType.length - 1) : name, step.relationship);
  return route[0].fromResourceType + route.map(step => step.storageDirection === 'INBOUND'
    ? ` <-[${relationshipField(step)}]- ${step.toResourceType}`
    : ` -[${relationshipField(step)}]-> ${step.toResourceType}`).join('');
};

const fixtureRows = (path, type) => readFileSync(path, 'utf8').trim().split(/\r?\n/).map((line, index) => {
  let value;
  try { value = JSON.parse(line); }
  catch (error) { throw new Error(`${type}.ndjson line ${index + 1} is invalid JSON: ${error.message}`); }
  assert.equal(value.resourceType, type);
  return value;
});

function readFixtureOracle(fixtureDir) {
  const names = ['Medication', 'MedicationAdministration', 'Patient', 'Observation'];
  const files = Object.fromEntries(names.map(name => {
    const path = join(fixtureDir, `${name}.ndjson`);
    const bytes = readFileSync(path);
    return [name, { path, bytes, rows: fixtureRows(path, name), sha256: createHash('sha256').update(bytes).digest('hex') }];
  }));
  const medications = new Map(files.Medication.rows.map(row => [row.id, row]));
  const patients = new Map(files.Patient.rows.map(row => [row.id, row]));
  const administrations = files.MedicationAdministration.rows;
  const observations = files.Observation.rows;
  assert.equal(medications.size, 3);
  assert.equal(patients.size, 3);
  assert.equal(administrations.length, 3);
  assert.equal(observations.length, 2);
  const valueByMedication = new Map();
  for (const administration of administrations) {
    const medicationID = administration.medication?.reference?.reference?.replace(/^Medication\//, '');
    const patientID = administration.subject?.reference?.replace(/^Patient\//, '');
    assert(medications.has(medicationID), `Administration/${administration.id} must reference a fixture Medication.`);
    assert(patients.has(patientID), `Administration/${administration.id} must reference a fixture Patient.`);
    const matching = observations.filter(observation => observation.focus?.some(item => item.reference === `Patient/${patientID}`));
    assert(matching.length <= 1, `Patient/${patientID} must match at most one Observation through focus_Patient.`);
    if (matching.length === 0) {
      valueByMedication.set(medicationID, null);
      continue;
    }
    const [observation] = matching;
    assert.equal(observation.code?.coding?.length, 1, `Observation/${observation.id} must have one exact semantic code.`);
    assert.equal(observation.code.coding[0].system, CODE_SYSTEM);
    assert.equal(observation.code.coding[0].code, CODE);
    const text = observation.valueCodeableConcept?.text;
    const displays = (observation.valueCodeableConcept?.coding ?? []).map(item => item.display)
      .filter(value => typeof value === 'string' && value.trim() !== '');
    assert(displays.length <= 1, `Observation/${observation.id} must stay within one fallback display.`);
    valueByMedication.set(medicationID, typeof text === 'string' && text.trim() !== '' ? text : displays[0] ?? null);
  }
  assert.deepEqual(Object.fromEntries([...valueByMedication].sort(([a], [b]) => a.localeCompare(b))), {
    'medication-fallback': 'Fallback semantic text',
    'medication-no-observation': null,
    'medication-primary': 'Primary semantic text',
  });
  return { files, valueByMedication, route: ROUTE };
}

const previewDOM = () => {
  const root = document.querySelector('[data-testid="construction-proposal-preview"]');
  const table = root?.querySelector('table');
  if (!root || !table) return undefined;
  return {
    status: root.dataset.previewStatus,
    outputId: root.dataset.previewOutputId,
    headers: [...table.querySelectorAll('thead th')].map(cell => (cell.innerText ?? '').trim()),
    rows: [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
      .map(row => [...row.querySelectorAll('td')].map(cell => (cell.innerText ?? '').trim())),
  };
};

const matchingHeaderIndex = (headers, label) => {
  const expected = label.trim().toLowerCase();
  const matches = headers.flatMap((header, index) =>
    header.split(/\r?\n/, 1)[0].trim().toLowerCase() === expected ? [index] : []);
  return matches.length === 1 ? matches[0] : -1;
};

const savedPreviewDOM = () => {
  const root = document.querySelector('[data-testid="construction-preview"]');
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!root || !table) return undefined;
  return {
    status: root.dataset.previewStatus,
    outputId: root.dataset.previewOutputId,
    headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.trim()),
    rows: [...table.querySelectorAll('[role="row"]')].slice(1)
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
    rowCount: Number(table.getAttribute('aria-rowcount')) - 1,
  };
};

const builderState = async (page, target, explorerId) => {
  const path = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(explorerId)}/authoring/v2/builder`;
  const response = await page.context().request.get(`${target.apiUrl}${path}`);
  const value = await response.json();
  assert(response.ok(), `Read-only Builder oracle returned HTTP ${response.status()}.`);
  return value;
};

function semanticColumn(builder, expectedColumnId) {
  const documents = builder.workspace?.documents ?? [];
  assert.equal(documents.length, 1, 'The semantic route fixture must have one saved output document.');
  const matches = documents[0].columns.filter(column => column.source?.kind === 'codedValue'
    && column.source.lookup?.binding?.valuePath === VALUE_PATH);
  assert.equal(matches.length, 1, 'The saved workspace must contain exactly one matching semantic value column.');
  if (expectedColumnId !== undefined) {
    assert.equal(matches[0].columnId, expectedColumnId, 'The semantic source must retain its persisted column identity.');
  }
  return matches[0];
}

function assertSemanticBinding(column, route, document) {
  assert(column, 'Saved output must contain the semantic value column.');
  const binding = column.source.lookup.binding;
  assert.equal(binding.valuePath, VALUE_PATH);
  assert.deepEqual(binding.valueFallback, [FALLBACK_PATH]);
  assert.equal(binding.fallbackOnBlankString, true);
  assert.equal(column.source.lookup.key.system, CODE_SYSTEM);
  assert.equal(column.source.lookup.key.code, CODE);
  assert.notEqual(column.occurrenceId, 'base', 'The semantic source must remain bound to the routed Observation occurrence.');
  const routeNodes = [];
  let node = document.route;
  while (node) {
    routeNodes.push(node);
    const children = node.children ?? [];
    assert(children.length <= 1, 'Fixture route must remain a single Medication-to-Observation chain.');
    node = children[0];
  }
  assert.deepEqual(routeNodes.map(item => item.resourceType), ['Medication', 'MedicationAdministration', 'Patient', 'Observation']);
  assert.deepEqual(routeNodes.slice(1).map(({ relationship, matchMode }) => ({ relationship, matchMode })),
    route.map(({ relationship, matchMode }) => ({ relationship, matchMode })));
  assert.equal(column.occurrenceId, routeNodes.at(-1).occurrenceId,
    'Saved semantic value must remain attached to the route terminal Observation occurrence.');
  assert.deepEqual(route.map(({ fromResourceType, toResourceType, relationship, storageDirection, matchMode }) => ({
    fromResourceType, toResourceType, relationship, storageDirection, matchMode,
  })), ROUTE);
}

export async function verifySemanticConstructionChoiceRoute({ page, workflow, context }) {
  const { report, action, check } = workflow;
  const { target } = context;
  configureNativePage(page);
  assert.equal(context.custom, false, 'The semantic route case requires a fresh disposable verification project.');
  assert.equal(context.seed?.fresh, true, 'The semantic route case must use a fresh seeded fixture project.');
  assert.equal(target.fixtureProject?.startsWith('loom_dev_verify_'), true);
  const oracle = readFixtureOracle(target.fixtureDir);
  report.target.semanticConstructionOracle = {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    rowCount: oracle.valueByMedication.size,
    rows: Object.fromEntries(oracle.valueByMedication),
    sourceFiles: Object.fromEntries(Object.entries(oracle.files).map(([name, value]) => [name, { path: value.path, sha256: value.sha256 }])),
    route: oracle.route,
  };
  check('correctness', 'literal fixture proves exact optional Medication route and primary/fallback values', true, report.target.semanticConstructionOracle);

  const act = (label, locator, perform, requiredCheck, options = {}) => action(label, locator, perform, {
    ...options,
    timeout: options.timeout ?? 5_000,
    budget: options.budget ?? 5_000,
    ...(requiredCheck ? { requiredCheck } : {}),
  });
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: 5_000,
  });
  await page.getByText('New explorer', { exact: true }).waitFor({ state: 'visible', timeout: 5_000 });
  await act('open native Explorer creation', page.getByText('New explorer', { exact: true }),
    () => page.getByText('New explorer', { exact: true }).click());
  const title = `Verify ${context.runID.slice(-10)} semantic route`;
  await act('name the disposable semantic route Explorer', page.locator('#new-explorer-name'),
    () => page.locator('#new-explorer-name').fill(title), undefined, { editable: true });
  await act('create a blank semantic route Explorer', page.getByRole('button', { name: 'Create blank', exact: true }),
    () => page.getByRole('button', { name: 'Create blank', exact: true }).click(),
    'native blank Explorer selected in the fresh semantic fixture project', {
      after: () => page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected,
        title, { timeout: 5_000 }),
    });
  await page.waitForFunction(expected => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === expected, title, { timeout: 5_000 });
  const explorerId = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
  assert(explorerId && explorerId !== target.bootstrapExplorerId);
  report.target.explorer = explorerId;

  await act('name the Medication semantic route table', page.locator('#first-table-name'),
    () => page.locator('#first-table-name').fill('Medication semantic route'), undefined, { editable: true });
  await act('choose Medication root rows natively', page.getByRole('button', { name: 'Choose Medication rows', exact: true }),
    () => page.getByRole('button', { name: 'Choose Medication rows', exact: true }).click(),
    'native Medication root Preview renders the three exact fixture rows', {
      after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready'
        && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '4',
      undefined, { timeout: 5_000 }),
    });
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready'
    && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '4', undefined, { timeout: 5_000 });
  const outputId = await page.locator('[data-testid="construction-preview"]').getAttribute('data-preview-output-id');
  assert(outputId, 'Native Medication root must expose its saved output identity.');
  report.target.outputId = outputId;
  let baseline = await builderState(page, target, explorerId);
  assert.equal(baseline.workspace.documents[0].rootResourceType, 'Medication');
  assert.equal(baseline.workspace.documents[0].columns.length, 1);
  check('correctness', 'fresh native table binds the Medication root and direct ID', true, { explorerId, outputId });

  const addColumnsButton = page.getByRole('button', { name: 'Add columns: Bring more information into each row.', exact: true });
  await act('open the native Add columns editor', addColumnsButton, () => addColumnsButton.click(),
    'native Add columns editor opens within five seconds', {
      after: () => page.locator('[aria-label="Add columns editor"]').waitFor({ state: 'visible', timeout: 5_000 }),
    });
  const fieldsAndRelatedData = page.getByRole('button', { name: 'Fields and related data', exact: true });
  await act('select Fields and related data', fieldsAndRelatedData, () => fieldsAndRelatedData.click(),
    'native Fields and related data tab exposes the ConceptCatalog search within five seconds', {
      after: () => page.getByRole('searchbox', { name: 'Search features by field name, concept, or code' })
        .waitFor({ state: 'visible', timeout: 5_000 }),
    });

  const search = page.getByRole('searchbox', { name: 'Search features by field name, concept, or code' });
  await search.waitFor({ state: 'visible', timeout: 5_000 });
  await act('search ConceptCatalog for exact SNOMED code', search, () => search.fill(CODE), undefined, { editable: true });
  const inventoryResponsePromise = page.waitForResponse(response => response.request().method() === 'POST'
    && new URL(response.url()).pathname.endsWith('/semantic-inventory')
    && response.request().postDataJSON()?.query === CODE);
  await act('submit exact ConceptCatalog semantic search', page.getByRole('button', { name: 'Search', exact: true }),
    () => page.getByRole('button', { name: 'Search', exact: true }).click(),
    'ConceptCatalog search returns the exact semantic code within five seconds', { after: () => inventoryResponsePromise });
  const inventoryResponse = await inventoryResponsePromise;
  assert(inventoryResponse.ok(), `ConceptCatalog semantic inventory returned HTTP ${inventoryResponse.status()}.`);
  const inventory = await inventoryResponse.json();
  const semanticItem = inventory.entries?.find(entry => entry.system === CODE_SYSTEM && entry.code === CODE
    && entry.resourceType === 'Observation' && entry.sourcePath === 'code');
  assert(semanticItem, 'ConceptCatalog must show the exact Observation.code SNOMED result.');
  const semanticLabel = semanticItem.display || semanticItem.code;
  const semanticCard = page.locator('[aria-labelledby="feature-catalog-concepts-title"] article').filter({ hasText: CODE }).first();
  const semanticCheckbox = semanticCard.getByRole('checkbox');
  await act('select the exact semantic Observation code', semanticCheckbox, () => semanticCheckbox.check());
  const choicesResponsePromise = page.waitForResponse(response => response.request().method() === 'POST'
    && new URL(response.url()).pathname.endsWith('/construction-choices'));
  const addSelected = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
  await act('resolve and preview the selected semantic route', addSelected, () => addSelected.click(),
    'native ConceptCatalog route lookup completes within five seconds', { after: () => choicesResponsePromise });
  const choicesResponse = await choicesResponsePromise;
  assert(choicesResponse.ok(), `Native ConceptCatalog choice lookup returned HTTP ${choicesResponse.status()}.`);
  const choiceSearch = await choicesResponse.json();
  const exactRouteChoices = (choiceSearch.choices ?? []).filter(candidate =>
    JSON.stringify(normalizedRoute(candidate.route)) === JSON.stringify(ROUTE));
  assert.equal(exactRouteChoices.length, 1,
    'ConceptCatalog route search must contain exactly one choice for the exact Medication → MedicationAdministration → Patient → Observation path.');
  const choice = exactRouteChoices[0];
  const source = choice.source;
  assert.equal(source.kind, 'SEMANTIC');
  assert.equal(source.resourceType, 'Observation');
  assert.equal(source.sourcePath, 'code');
  assert.equal(source.keySelector, 'code.coding[]');
  assert.equal(source.system, CODE_SYSTEM);
  assert.equal(source.code, CODE);
  assert.equal(source.fieldPath, VALUE_PATH);
  assert.equal(source.valueSelector, VALUE_PATH);
  assert.deepEqual(source.valueFallbacks, [FALLBACK_PATH]);
  assert.equal(source.fallbackOnBlankString, true);
  assert(source.repeatedBoundaries?.some(boundary => boundary.path === 'valueCodeableConcept.coding[]' && boundary.maxItems === 1),
    'Semantic choice must retain the bounded repeated fallback coding[] boundary.');
  assert.deepEqual(choice.route.map(({ fromResourceType, toResourceType, relationship, storageDirection, matchMode }) => ({
    fromResourceType, toResourceType, relationship, storageDirection, matchMode,
  })), ROUTE);
  check('correctness', 'ConceptCatalog choice binds exact semantic value and three optional route edges', true, {
    source: { kind: source.kind, sourcePath: source.sourcePath, valueSelector: source.valueSelector,
      valueFallbacks: source.valueFallbacks, fallbackOnBlankString: source.fallbackOnBlankString,
      repeatedBoundaries: source.repeatedBoundaries }, route: choice.route,
  });

  const dialog = page.getByRole('dialog', { name: 'Choose how to add these fields' });
  await dialog.waitFor({ state: 'visible', timeout: 5_000 });
  const exactRouteLabel = `${semanticLabel}: ${routeSummary(choice.route)}`;
  const exactRouteRadio = dialog.getByRole('radio', { name: exactRouteLabel, exact: true });
  assert.equal(await exactRouteRadio.count(), 1, `Native route picker must expose the exact path radio: ${exactRouteLabel}`);
  await act('select the exact Medication → MedicationAdministration → Patient → Observation route', exactRouteRadio,
    () => exactRouteRadio.check(), 'native dialog selects the exact three-edge optional route before confirming VALUE', {
      after: async () => assert.equal(await exactRouteRadio.isChecked(), true,
        'The exact three-edge optional route radio must be checked before confirming VALUE.'),
    });
  check('correctness', 'native dialog selects the exact three-edge optional route before confirming VALUE', true, {
    route: normalizedRoute(choice.route), routeSummary: routeSummary(choice.route), selected: await exactRouteRadio.isChecked(),
  });
  const valueRadio = dialog.getByRole('radio', { name: `${semanticLabel}: Use the matching value`, exact: true });
  assert.equal(await valueRadio.count(), 1, 'Native dialog must expose the exact VALUE form radio.');
  await act('select the semantic matching VALUE form', valueRadio, () => valueRadio.check(),
    'native dialog selects the exact semantic VALUE form before confirming', {
      after: async () => assert.equal(await valueRadio.isChecked(), true,
        'The VALUE form radio must be checked before confirming.'),
    });
  check('correctness', 'native dialog selects the exact semantic VALUE form before confirming', true, {
    form: 'VALUE', checked: await valueRadio.isChecked(),
  });
  const confirm = dialog.getByRole('button', { name: /Add 1 column/ });
  const proposalResponsePromise = page.waitForResponse(response => {
    if (response.request().method() !== 'POST' ||
      !new URL(response.url()).pathname.endsWith('/construction-choice-proposals')) return false;
    return response.request().postDataJSON()?.constructionChoices?.some(selection =>
      selection.choiceId === choice.choiceId && selection.form === 'VALUE');
  });
  await act('confirm the semantic route column', confirm, () => confirm.click());
  const proposalResponse = await proposalResponsePromise;
  assert(proposalResponse.ok(), `Semantic Preview proposal returned HTTP ${proposalResponse.status()}.`);
  const proposalRequest = proposalResponse.request().postDataJSON();
  const proposal = await proposalResponse.json();
  assert.equal(proposal.previewStatus, 'READY', 'Semantic binding Preview endpoint must return exact READY status.');
  assert.equal(proposal.outputId, outputId);
  assert.equal(proposal.constructionChoices.length, 1);
  assert.equal(proposal.constructionChoices[0].choiceId, choice.choiceId);
  assert.equal(proposalRequest.constructionChoices[0].form, 'VALUE');
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-choice-proposal-panel"]')?.dataset.proposalStatus === 'ready'
    && document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready', undefined, { timeout: 5_000 });
  const proposalPreview = await page.evaluate(previewDOM);
  const headerIndex = matchingHeaderIndex(proposalPreview.headers, semanticLabel);
  assert(headerIndex >= 0, 'Ready proposal preview must render the selected semantic output column.');
  const idIndex = proposalPreview.headers.findIndex(header => /Medication ID/i.test(header));
  assert(idIndex >= 0, 'Ready proposal preview must retain the Medication row identity column.');
  const expectedPairs = [...oracle.valueByMedication].map(([id, value]) => [id, value ?? '—']).sort(([a], [b]) => a.localeCompare(b));
  const actualPairs = proposalPreview.rows.map(row => [row[idIndex], row[headerIndex]]).sort(([a], [b]) => a.localeCompare(b));
  assert.deepEqual(actualPairs, expectedPairs, 'Ready proposal rows must match primary text, blank fallback, and optional-null source oracle values.');
  check('correctness', 'semantic binding proposal returns READY and exact independent preview rows', true, {
    previewStatus: proposal.previewStatus, proposalRequest, headers: proposalPreview.headers, rows: actualPairs,
  });

  const applyResponsePromise = page.waitForResponse(response => response.request().method() === 'POST'
    && new URL(response.url()).pathname.endsWith('/commands')
    && response.request().postDataJSON()?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_CHOICE'));
  await act('Apply the READY semantic column proposal', page.getByRole('button', { name: 'Apply columns', exact: true }),
    () => page.getByRole('button', { name: 'Apply columns', exact: true }).click(),
    'native Apply renders the exact semantic result within five seconds', { after: async () => {
      await applyResponsePromise;
      await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready'
        && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '4',
      undefined, { timeout: 5_000 });
    } });
  const applyResponse = await applyResponsePromise;
  assert(applyResponse.ok(), `Native Apply command returned HTTP ${applyResponse.status()}.`);
  const applyRequest = applyResponse.request().postDataJSON();
  const appliedChoice = applyRequest.commands.find(command => command.type === 'APPLY_CONSTRUCTION_CHOICE');
  assert.equal(appliedChoice.constructionChoice.choiceId, choice.choiceId);
  assert.equal(appliedChoice.constructionChoice.form, proposal.constructionChoices[0].form);
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready'
    && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '4', undefined, { timeout: 5_000 });
  let saved = await builderState(page, target, explorerId);
  const savedColumn = semanticColumn(saved);
  assert.equal(typeof savedColumn.columnId, 'string');
  assert(savedColumn.columnId.trim(), 'The saved semantic column must have a stable columnId.');
  const semanticColumnId = savedColumn.columnId;
  assertSemanticBinding(savedColumn, choice.route, saved.workspace.documents[0]);
  const appliedPreview = await page.evaluate(savedPreviewDOM);
  const appliedIDIndex = appliedPreview.headers.findIndex(header => /Medication ID/i.test(header));
  const appliedValueIndex = matchingHeaderIndex(appliedPreview.headers, semanticLabel);
  assert(appliedIDIndex >= 0 && appliedValueIndex >= 0);
  assert.deepEqual(appliedPreview.rows.map(row => [row[appliedIDIndex], row[appliedValueIndex]]).sort(([a], [b]) => a.localeCompare(b)), expectedPairs);
  check('correctness', 'native Apply command persists the exact semantic binding and route', true, { applyRequest, column: savedColumn });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready'
    && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '4', undefined, { timeout: 5_000 });
  saved = await builderState(page, target, explorerId);
  const reloadedColumn = semanticColumn(saved, semanticColumnId);
  assertSemanticBinding(reloadedColumn, choice.route, saved.workspace.documents[0]);
  const reloadedPreview = await page.evaluate(savedPreviewDOM);
  const reloadedIDIndex = reloadedPreview.headers.findIndex(header => /Medication ID/i.test(header));
  const reloadedValueIndex = matchingHeaderIndex(reloadedPreview.headers, semanticLabel);
  assert(reloadedIDIndex >= 0 && reloadedValueIndex >= 0);
  assert.deepEqual(reloadedPreview.rows.map(row => [row[reloadedIDIndex], row[reloadedValueIndex]]).sort(([a], [b]) => a.localeCompare(b)), expectedPairs);
  check('persistence', 'reload retains the exact routed semantic source, fallback binding, and rows', true, { outputId, explorerId });

  const editedLabel = 'Medication semantic value edited';
  await act('open native table column settings', page.getByRole('button', { name: 'Columns', exact: true }),
    () => page.getByRole('button', { name: 'Columns', exact: true }).click());
  const labelInput = page.getByRole('textbox', { name: `Column name for ${savedColumn.label}` });
  const renameResponsePromise = page.waitForResponse(response => response.request().method() === 'POST'
    && new URL(response.url()).pathname.endsWith('/commands'));
  await act('edit the saved semantic column label', labelInput, async () => {
    await labelInput.fill(editedLabel);
    await labelInput.press('Enter');
  }, 'native semantic column label edit completes within five seconds', { editable: true, after: () => renameResponsePromise });
  const renameResponse = await renameResponsePromise;
  assert(renameResponse.ok(), `Native semantic column edit returned HTTP ${renameResponse.status()}.`);
  let edited = await builderState(page, target, explorerId);
  const editedColumn = semanticColumn(edited, semanticColumnId);
  assertSemanticBinding(editedColumn, choice.route, edited.workspace.documents[0]);
  assert.equal(editedColumn.label, editedLabel);
  check('correctness', 'native edit renames the semantic column while preserving its routed fallback binding', true, { editedLabel, outputId });

  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready'
    && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '4', undefined, { timeout: 5_000 });
  edited = await builderState(page, target, explorerId);
  const reloadedEditedColumn = semanticColumn(edited, semanticColumnId);
  assertSemanticBinding(reloadedEditedColumn, choice.route, edited.workspace.documents[0]);
  assert.equal(reloadedEditedColumn.label, editedLabel);
  const editedPreview = await page.evaluate(savedPreviewDOM);
  const editedIDIndex = editedPreview.headers.findIndex(header => /Medication ID/i.test(header));
  const editedValueIndex = matchingHeaderIndex(editedPreview.headers, editedLabel);
  assert(editedIDIndex >= 0 && editedValueIndex >= 0);
  assert.deepEqual(editedPreview.rows.map(row => [row[editedIDIndex], row[editedValueIndex]]).sort(([a], [b]) => a.localeCompare(b)), expectedPairs);
  check('persistence', 'reload retains the edited semantic column, exact fallback binding, and rows', true, { editedLabel, outputId });

  await act('open native columns menu for semantic removal', page.getByRole('button', { name: 'Columns', exact: true }),
    () => page.getByRole('button', { name: 'Columns', exact: true }).click());
  const removeButton = page.getByRole('button', { name: `Remove ${editedLabel} column`, exact: true });
  const removalResponsePromise = page.waitForResponse(response => response.request().method() === 'POST'
    && new URL(response.url()).pathname.endsWith('/commands')
    && response.request().postDataJSON()?.commands?.some(command => command.type === 'REMOVE_COLUMN'));
  await act('remove the edited semantic column natively', removeButton, () => removeButton.click(),
    'native semantic removal restores Medication rows within five seconds', { after: async () => {
      await removalResponsePromise;
      await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready'
        && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === '1',
      undefined, { timeout: 5_000 });
    } });
  const removalResponse = await removalResponsePromise;
  assert(removalResponse.ok(), `Native semantic column removal returned HTTP ${removalResponse.status()}.`);
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready'
    && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === '1', undefined, { timeout: 5_000 });
  const restored = await builderState(page, target, explorerId);
  assert.equal(restored.workspace.documents[0].rootResourceType, 'Medication');
  assert.equal(restored.workspace.documents[0].columns.length, 1, 'Removing the semantic output must restore the direct Medication source table.');
  const restoredPreview = await page.evaluate(savedPreviewDOM);
  const restoredIDIndex = restoredPreview.headers.findIndex(header => /Medication ID/i.test(header));
  assert(restoredIDIndex >= 0);
  assert.deepEqual(restoredPreview.rows.map(row => row[restoredIDIndex]).sort(), [...oracle.valueByMedication.keys()].sort());
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready'
    && document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-colcount') === '1', undefined, { timeout: 5_000 });
  check('persistence', 'native removal and reload restore the exact Medication-only source table', true, { outputId, explorerId });
  const after = Object.fromEntries(Object.entries(oracle.files).map(([name, file]) => [name,
    createHash('sha256').update(readFileSync(file.path)).digest('hex')]));
  assert.deepEqual(after, Object.fromEntries(Object.entries(oracle.files).map(([name, file]) => [name, file.sha256])));
  check('correctness', 'semantic route fixture bytes stayed unchanged through Apply, reload, and removal', true, after);
}
