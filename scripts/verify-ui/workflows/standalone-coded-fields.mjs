import assert from 'node:assert/strict';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { readStandaloneCodedFieldFixture } from '../helpers/standalone-coded-field-fixture.mjs';

const ACTION_TIMEOUT_MS = 10_000;
const CODED_RULES = new Set(['CODED_CATEGORY_V1', 'CODED_VALUE_V1']);
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const normalizeHeader = value => normalize(value).toLowerCase();
const semanticIdentity = item => `${item.conceptId}\u0000${item.bindingId}`;

const waitFor = (page, predicate, argument, timeoutMs = ACTION_TIMEOUT_MS) => page.waitForFunction(
  ({ predicateSource, argument: value }) => Boolean(Function('argument', `return (${predicateSource})(argument);`)(value)),
  { predicateSource: predicate.toString(), argument },
  { timeout: timeoutMs },
);

const readVisibleGrid = page => page.locator('[data-testid="preview-table-scroll"] [role="table"]').evaluate(table => ({
  headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.replace(/\s+/g, ' ').trim().toLowerCase()),
  rows: [...table.querySelectorAll('[role="row"]')].slice(1)
    .filter(row => row.querySelector('[role="cell"]'))
    .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.replace(/\s+/g, ' ').trim())),
}));

const fixtureItem = (entries, label, predicate) => {
  const matches = entries.filter(predicate);
  assert.equal(matches.length, 1, `${label} must be one exact compiler-issued semantic entry: ${JSON.stringify(matches)}`);
  const item = matches[0];
  assert(item.constructionChoice?.choiceId, `${label} must have a compiler-issued construction choice.`);
  assert.equal(item.constructionChoice.source.kind, 'SEMANTIC');
  assert(CODED_RULES.has(item.constructionChoice.source.ruleHint), `${label} must use an allowed coded semantic rule.`);
  return item;
};

const codedCategoryItem = (entries, label, predicate) => {
  const item = fixtureItem(entries, label, candidate => candidate.selectionMode === 'SYSTEM_VERSION_CATEGORY' &&
    candidate.familyId === candidate.conceptId && candidate.code === '' &&
    candidate.constructionChoice?.source.ruleHint === 'CODED_CATEGORY_V1' && predicate(candidate));
  const source = item.constructionChoice.source;
  assert.equal(source.system, item.system, `${label} must retain its exact Coding system.`);
  assert.equal(source.version ?? '', item.codingVersion, `${label} must retain its exact Coding version.`);
  assert.equal(source.code ?? '', '', `${label} must identify a system/version family, not one exact code.`);
  return item;
};

const selectionButtonForItem = async (page, item) => {
  const source = item.constructionChoice.source;
  const sourceGroup = page.locator(`[data-source-node-id="${source.nodeId}"]`);
  assert.equal(await sourceGroup.count(), 1, `${semanticLabel(item)} must resolve to its exact source-node group.`);
  const accessibleNames = await sourceGroup.locator('button[aria-pressed]').evaluateAll(buttons =>
    buttons.map(button => button.getAttribute('aria-label') ?? ''));
  const versionQualifier = `version ${semanticVersion(item) || 'unversioned'}`;
  const matches = accessibleNames.filter(name => name.startsWith(`Select ${item.resourceType} `) &&
    name.includes(`system ${item.system}`) && name.includes(versionQualifier));
  assert.equal(matches.length, 1,
    `${semanticLabel(item)} must have one accessible card name containing its exact system and version: ${JSON.stringify(accessibleNames)}`);
  return {
    button: sourceGroup.getByRole('button', { name: matches[0], exact: true }),
    accessibleName: matches[0],
  };
};

const addSourceOptionForResource = async (page, resourceType) => {
  const options = await page.getByTestId('construction-add-columns-source-option').evaluateAll(buttons =>
    buttons.map(button => ({
      kind: button.dataset.sourceKind,
      key: button.dataset.sourceKey,
      label: button.textContent?.trim() ?? '',
      accessibleName: button.getAttribute('aria-label') ?? '',
    })));
  const matches = options.filter(option => option.kind === 'RELATED' && option.label === resourceType);
  assert.equal(matches.length, 1,
    `${resourceType} must have one related-source scope in the fresh fixture: ${JSON.stringify(options)}`);
  return matches[0];
};

const semanticLabel = item => item.display?.trim() || item.code || item.sourcePath;
const semanticVersion = item => item.codingVersion || item.constructionChoice?.source.version || '';
const formLabel = form => ({
  VALUE: 'Use the matching value',
  FIRST: 'Use the first value',
  ALL: 'Keep all matching values',
  DISTINCT: 'Keep unique values',
  COUNT: 'Count matching records',
  PRESENCE: 'Show whether a match exists',
  OWNER_RECORDS: 'Keep each matching record',
})[form];
const routePath = route => {
  const relationshipField = edge => [edge.fromResourceType, edge.toResourceType].reduce((name, type) =>
    name.endsWith(`_${type}`) || name.endsWith(`-${type}`) ? name.slice(0, -type.length - 1) : name, edge.relationship);
  const first = route[0];
  return first ? first.fromResourceType + route.map(edge => edge.storageDirection === 'INBOUND'
    ? ` <-[${relationshipField(edge)}]- ${edge.toResourceType}`
    : ` -[${relationshipField(edge)}]-> ${edge.toResourceType}`).join('') : 'Same record';
};

export const standaloneCodedFieldsWorkflow = async ({ page, action, check, networkObserver, report, target, seed, scenarioID, caseName }) => {
  configureNativePage(page);
  assert.equal(scenarioID, 'basic-standalone-coded-fields');
  assert.equal(caseName, 'coded-fields');
  assert.equal(seed?.fresh, true, 'The standalone coded-field lifecycle requires a fresh disposable Basic fixture project.');
  assert.equal(target.fixtureProject?.startsWith('loom_dev_verify_'), true);
  assert(target.fixtureGeneration && target.fixtureDir, 'The fresh fixture generation and directory must be explicit.');

  const oracle = readStandaloneCodedFieldFixture(target.fixtureDir);
  const sourceFilesBefore = Object.fromEntries(Object.entries(oracle.sourceFiles).map(([resourceType, file]) => [resourceType, file.sha256]));
  report.target.fixtureOracle = {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    patientIDs: oracle.patientIDs,
    conditionCodesByPatient: oracle.conditionCodesByPatient,
    medicationCodesByPatient: oracle.medicationCodesByPatient,
    specimenCodesByPatient: oracle.specimenCodesByPatient,
    observationPairsByPatient: oracle.observationPairsByPatient,
    sourceFiles: oracle.sourceFiles,
    routes: oracle.routes,
  };
  check('correctness', 'literal fixture proves standalone Coding versions and the unversioned paired Observation 42/null values',
    oracle.patientIDs.length === 2 &&
      oracle.conditionCodesByPatient['coded-catalog-patient-01'].length === 3 &&
      oracle.conditionCodesByPatient['coded-catalog-patient-01'].map(item => item.version ?? '').join(',') === 'v1,v2,' &&
      oracle.medicationCodesByPatient['coded-catalog-patient-01'].length === 2 &&
      oracle.specimenCodesByPatient['coded-catalog-patient-01'][0]?.display === undefined &&
      oracle.observationPairsByPatient['coded-catalog-patient-01'].codes.length === 1 &&
      oracle.observationPairsByPatient['coded-catalog-patient-01'].codes[0]?.version === undefined &&
      oracle.observationPairsByPatient['coded-catalog-patient-01'].quantity === 42,
    report.target.fixtureOracle);

  const click = (label, locator, after) => action(label, locator,
    targetLocator => targetLocator.click({ timeout: ACTION_TIMEOUT_MS }), {
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
      ...(after ? { after } : {}),
    });
  const fill = (label, locator, value, after) => action(label, locator,
    targetLocator => targetLocator.fill(value, { timeout: ACTION_TIMEOUT_MS }), {
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
      editable: true,
      ...(after ? { after } : {}),
    });
  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  await waitFor(page, () => Boolean(document.querySelector('select[aria-label="Explorer"]')),
    undefined, ACTION_TIMEOUT_MS);
  const createExplorerName = `Standalone coded fields ${report.target.project.slice(-10)}`;
  await click('open fresh Explorer creation', page.locator('summary').filter({ hasText: /^New explorer$/ }),
    () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
  await fill('name the standalone coded-field Explorer', page.locator('#new-explorer-name'), createExplorerName);
  await click('create a blank standalone coded-field Explorer', page.getByRole('button', { name: 'Create blank', exact: true }),
    () => waitFor(page, title => document.querySelector('select[aria-label="Explorer"]')?.selectedOptions[0]?.textContent?.trim() === title &&
      document.body.innerText.includes('Build your first table'), createExplorerName));
  const explorerId = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
  assert(explorerId && explorerId !== target.bootstrapExplorerId, 'The native lifecycle must use its newly created Explorer.');
  report.target.explorer = explorerId;

  await fill('name the Patient root table', page.locator('#first-table-name'), 'Standalone coded values by Patient');
  await click('create the Patient root table', page.getByRole('button', { name: 'Choose Patient rows', exact: true }),
    () => waitFor(page, () => document.body.innerText.includes('Standalone coded values by Patient') &&
      document.body.innerText.includes('DATASET WORKSPACE'), undefined));

  const base = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(explorerId)}/authoring/v2`;
  const readBuilder = async () => {
    const response = await page.context().request.get(`${target.apiUrl}${base}/builder`, { timeout: ACTION_TIMEOUT_MS });
    const body = await response.json();
    assert(response.ok(), `Builder read failed with HTTP ${response.status()}: ${JSON.stringify(body).slice(0, 700)}`);
    return body;
  };
  let builder = await readBuilder();
  assert.equal(builder.catalog?.generation, target.fixtureGeneration);
  const documentFor = state => state.workspace.documents.find(document => document.rootResourceType === 'Patient');
  let document = documentFor(builder);
  assert(document?.output?.id, 'The native Patient table must have a saved Builder output.');
  const outputId = document.output.id;
  report.target.outputId = outputId;
  const idColumn = document.columns.find(column => column.source?.kind === 'field' && column.source.field?.path === 'id');
  assert(idColumn?.columnId, 'The Patient table must retain its direct ID column.');
  report.target.qaIsolation = {
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    explorer: explorerId,
    outputId,
    draftVersion: builder.draftVersion,
    draftDigest: builder.draftDigest,
    sourceFilesBefore,
    workflowMutations: ['native UI created a blank Explorer and Patient root table in the fresh disposable fixture project'],
    cleanupMutations: [],
  };
  await waitFor(page, expected => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expected.outputId &&
      table?.getAttribute('aria-rowcount') === String(expected.rowCount);
  }, { outputId, rowCount: oracle.patientIDs.length + 1 });
  const initialGrid = await readVisibleGrid(page);
  const idIndex = initialGrid.headers.findIndex(header => header === normalizeHeader(idColumn.label));
  assert(idIndex >= 0, 'The exact Patient ID column must render in the root preview.');
  assert.deepEqual(initialGrid.rows.map(row => row[idIndex]).sort(), oracle.patientIDs,
    'The initial native Patient preview must preserve the exact fixture IDs.');
  const draftBeforeFilter = { version: builder.draftVersion, digest: builder.draftDigest };

  const filterEditor = page.getByTestId('construction-filter-editor');
  const filterProposalFrom = networkObserver.requests.length;
  const filterProposalPromise = networkObserver.waitForResponse(entry => entry.index >= filterProposalFrom &&
    entry.path.endsWith('/construction-proposals') && entry.request?.outputId === outputId &&
    entry.request?.candidateConstruction?.steps?.some(step => step.operation?.kind === 'FILTER' &&
      step.operation.filter?.columnId === idColumn.columnId && step.operation.filter?.operator === 'EXISTS'),
  { fromIndex: filterProposalFrom, timeoutMs: ACTION_TIMEOUT_MS });
  const keepRowsAction = page.getByTestId('construction-action-keep-rows');
  const rowSettingsDialog = page.getByRole('dialog', { name: 'Row definition settings', exact: true });
  await click('open Patient row settings for Filter', page.getByTestId('construction-rows-settings-trigger'),
    () => keepRowsAction.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
  await click('return to the Patient table before opening Filter',
    rowSettingsDialog.getByRole('button', { name: 'Back to table', exact: true }),
    () => rowSettingsDialog.waitFor({ state: 'hidden', timeout: ACTION_TIMEOUT_MS }));
  await click('open Patient row Filter', keepRowsAction,
    () => filterEditor.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
  const filterColumn = filterEditor.getByRole('combobox', { name: 'Column', exact: true });
  if (await filterColumn.inputValue() !== idColumn.columnId) {
    await action('select Patient ID for the row Filter', filterColumn,
      control => control.selectOption(idColumn.columnId, { timeout: ACTION_TIMEOUT_MS }), {
        timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
        after: async () => assert.equal(await filterColumn.inputValue(), idColumn.columnId),
      });
  }
  const filterCondition = filterEditor.getByRole('combobox', { name: 'Condition', exact: true });
  const filterOperators = await filterCondition.locator('option').evaluateAll(options => options.map(option => option.value));
  assert(filterOperators.includes('EXISTS'), 'Patient ID must support the non-null EXISTS Filter.');
  if (await filterCondition.inputValue() !== 'EXISTS') {
    await action('keep Patient rows with an ID', filterCondition,
      control => control.selectOption('EXISTS', { timeout: ACTION_TIMEOUT_MS }), {
        timeout: ACTION_TIMEOUT_MS, budget: ACTION_TIMEOUT_MS,
        after: async () => assert.equal(await filterCondition.inputValue(), 'EXISTS'),
      });
  }
  await page.waitForFunction(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    return panel?.dataset.proposalStatus === 'ready' && preview?.dataset.previewStatus === 'ready';
  }, undefined, { timeout: ACTION_TIMEOUT_MS });
  const filterProposal = await filterProposalPromise;
  assert.equal(filterProposal.status, 200, 'The Patient ID EXISTS Filter proposal must return HTTP 200.');
  const candidateFilterSteps = filterProposal.response.candidateConstruction?.steps ?? [];
  assert.equal(candidateFilterSteps.length, 1, 'The native Patient ID Filter proposal must contain exactly one row operation.');
  const candidateFilterStep = candidateFilterSteps[0];
  assert.equal(candidateFilterStep.operation?.kind, 'FILTER');
  assert.equal(candidateFilterStep.operation.filter?.columnId, idColumn.columnId);
  assert.equal(candidateFilterStep.operation.filter?.operator, 'EXISTS');
  assert.equal(filterProposal.response.preview?.rowCount, oracle.patientIDs.length,
    'The Patient ID EXISTS Filter must preserve both fixture rows.');
  const proposalIDColumn = filterProposal.response.preview.columns.find(column => column.column === idColumn.column);
  assert(proposalIDColumn, 'The Filter preview must retain the exact public Patient ID column.');
  assert.deepEqual(filterProposal.response.preview.rows.map(row => String(row[proposalIDColumn.column])).sort(), oracle.patientIDs,
    'The Filter preview must retain both exact Patient IDs.');

  const filterCapabilityFrom = networkObserver.requests.length;
  const filterCapabilityPromise = networkObserver.waitForResponse(entry => entry.index >= filterCapabilityFrom &&
    entry.method === 'POST' && entry.path.endsWith('/construction-capabilities') && entry.status === 200 &&
    entry.request?.outputId === outputId && entry.request?.stageId === candidateFilterStep.id &&
    entry.response?.outputId === outputId && entry.response?.stageId === candidateFilterStep.id &&
    entry.response?.selectedStage?.id === candidateFilterStep.id,
  { fromIndex: filterCapabilityFrom, timeoutMs: ACTION_TIMEOUT_MS });
  await click('Apply Patient ID EXISTS Filter', page.getByTestId('construction-apply-proposal'), async () => {
    await page.getByTestId('construction-proposal-panel').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
    await waitFor(page, expected => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expected.outputId &&
        table?.getAttribute('aria-rowcount') === String(expected.rowCount);
    }, { outputId, rowCount: oracle.patientIDs.length + 1 });
  });
  builder = await readBuilder();
  document = documentFor(builder);
  const savedFilterSteps = (document.construction?.steps ?? []).filter(step => step.operation?.kind === 'FILTER');
  assert.equal(savedFilterSteps.length, 1, 'The Patient table must persist one Filter before coded browsing.');
  const savedFilterStep = savedFilterSteps[0];
  assert.equal(savedFilterStep.id, candidateFilterStep.id, 'The saved Filter must retain the proposed exact stage ID.');
  assert.equal(savedFilterStep.operation.filter.columnId, idColumn.columnId);
  assert.equal(savedFilterStep.operation.filter.operator, 'EXISTS');
  const filteredGrid = await readVisibleGrid(page);
  const filteredIDIndex = filteredGrid.headers.findIndex(header => header === normalizeHeader(idColumn.label));
  assert(filteredIDIndex >= 0, 'The filtered preview must retain Patient ID.');
  assert.deepEqual(filteredGrid.rows.map(row => row[filteredIDIndex]).sort(), oracle.patientIDs,
    'The saved Filter must preserve both exact Patient rows.');
  const filterCapabilityEntry = await filterCapabilityPromise;
  assert.equal(filterCapabilityEntry.response.selectedStage.id, savedFilterStep.id,
    'The compiler capability response must describe the exact saved Filter stage.');
  assert.equal(filterCapabilityEntry.response.selectedStage.capabilities.find(capability => capability.kind === 'FILTER')?.supported,
    true, 'The saved Filter stage must remain compiler-supported before coded browsing.');
  report.target.qaIsolation.workflowMutations.push('native UI applied a Patient ID EXISTS Filter that preserves both fixture rows');
  const baselineDraft = { version: builder.draftVersion, digest: builder.draftDigest };
  assert(baselineDraft.version > draftBeforeFilter.version && baselineDraft.digest !== draftBeforeFilter.digest,
    'The saved Patient ID Filter must establish the coded-browse baseline draft.');

  const readInventoryRequest = async (label, resourceType, fromIndex) => {
    const inventoryPromise = networkObserver.waitForResponse(entry => entry.index >= fromIndex &&
      entry.path.endsWith('/semantic-inventory') && entry.request?.outputId === outputId &&
      entry.request?.resourceType === resourceType,
    { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
    const inventoryRequest = await inventoryPromise;
    assert.equal(inventoryRequest.status, 200, `${label}: the native semantic inventory request must succeed.`);
    assert.equal(inventoryRequest.request.outputId, outputId, `${label}: coded browsing must be scoped to the current Patient output.`);
    assert.equal(inventoryRequest.request.resourceType, resourceType, `${label}: browse must retain its selected source scope.`);
    assert.equal(inventoryRequest.request.selectionMode, 'SYSTEM_VERSION_CATEGORY', `${label}: browse must request compiler-issued system/version families.`);
    assert.equal(String(inventoryRequest.request.query ?? '').trim(), '', `${label}: source selection must not require typed search.`);
    assert.equal(typeof inventoryRequest.response.contextToken, 'string');
    assert(inventoryRequest.response.contextToken.trim(), `${label}: output-scoped choices must bind to a context token.`);
    assert.equal(typeof inventoryRequest.response.buildId, 'string');
    assert(inventoryRequest.response.buildId.trim(), `${label}: the catalog response must identify its semantic build.`);
    assert.equal(inventoryRequest.response.state, 'complete');
    return inventoryRequest;
  };

  const initialInventoryFrom = networkObserver.requests.length;
  const editor = page.locator('[aria-label="Add columns editor"]');
  await click('open Add columns in the starting Patient scope', page.getByTestId('construction-action-add-columns'),
    () => editor.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
  const rootScope = page.locator('button[data-source-key="root:Patient"]');
  assert.equal(await rootScope.getAttribute('aria-pressed'), 'true',
    'Coded browsing must start scoped to the current Patient table rows.');
  const rootInventory = await readInventoryRequest('starting Patient scope', 'Patient', initialInventoryFrom);
  assert.equal(rootInventory.request.sourceNodeId, undefined, 'The starting table scope must not require a source-node filter.');
  assert(rootInventory.response.entries.every(item => item.resourceType === 'Patient'),
    'The starting Patient scope must not display categories from other resources.');

  const browseResource = async resourceType => {
    const option = await addSourceOptionForResource(page, resourceType);
    const fromIndex = networkObserver.requests.length;
    await click(`switch coded browse to ${resourceType}`, page.locator(`button[data-source-key="${option.key}"]`), async () => {
      await waitFor(page, expectedKey => document.querySelector(`button[data-source-key="${expectedKey}"]`)?.getAttribute('aria-pressed') === 'true', option.key);
      await page.getByRole('heading', { name: `Coded values from ${resourceType}`, exact: true })
        .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    });
    const inventory = await readInventoryRequest(`${resourceType} source scope`, resourceType, fromIndex);
    assert(inventory.response.entries.every(item => item.resourceType === resourceType),
      `${resourceType} source scope must keep its compiler entries within that resource.`);
    return { inventory, option };
  };

  const selectedItems = [];
  const selectedNames = [];
  const selectedList = page.getByRole('list', { name: 'Selected fields and coded values', exact: true });
  const selectCurrentItem = async (phase, item, sourceOption) => {
    const source = item.constructionChoice.source;
    assert.equal(source.resourceType, item.resourceType);
    assert.equal(sourceOption.key, `node:${source.nodeId}`,
      `${semanticLabel(item)} must be selected from the exact related-source node returned by the compiler.`);
    const { button, accessibleName } = await selectionButtonForItem(page, item);
    await button.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    assert.equal(await button.isEnabled(), true, `${accessibleName} must be selectable in its exact source scope.`);
    await click(`${phase}: select ${accessibleName}`, button, async () => assert.equal(await button.getAttribute('aria-pressed'), 'true'));
    selectedItems.push(item);
    selectedNames.push(accessibleName);
    await selectedList.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    assert.equal(await selectedList.locator('li').count(), selectedItems.length,
      'Selections must accumulate in the action list when browsing source scopes changes.');
    const listText = await selectedList.innerText();
    assert(listText.includes(semanticLabel(item)), `${accessibleName} must remain visible in the selected feature list.`);
  };
  const assertSelectionCount = async expected => {
    assert.equal(await selectedList.locator('li').count(), expected,
      'Switching the related-resource scope must preserve selections from earlier source tabs.');
  };

  const { inventory: conditionInventory, option: conditionOption } = await browseResource('Condition');
  await assertSelectionCount(0);
  const conditionEntries = conditionInventory.response.entries.filter(item => item.resourceType === 'Condition');
  const conditionV1 = codedCategoryItem(conditionEntries, 'Condition v1 Coding family', item =>
    item.system === 'urn:loom:basic-coded-semantic:condition' && item.codingVersion === 'v1');
  const conditionUnversioned = codedCategoryItem(conditionEntries, 'unversioned Condition Coding family', item =>
    item.system === 'urn:loom:basic-coded-semantic:condition' && item.codingVersion === '');
  const conditionV2 = codedCategoryItem(conditionEntries, 'Condition v2 Coding family', item =>
    item.system === 'urn:loom:basic-coded-semantic:condition' && item.codingVersion === 'v2');
  assert.notEqual(conditionV1.familyId, conditionV2.familyId,
    'The same Condition system at v1 and v2 must remain separate family identities.');
  await selectCurrentItem('Condition family', conditionV1, conditionOption);
  await selectCurrentItem('unversioned Condition family', conditionUnversioned, conditionOption);

  const { inventory: medicationInventory, option: medicationOption } = await browseResource('Medication');
  await assertSelectionCount(2);
  const medicationFamily = codedCategoryItem(medicationInventory.response.entries, 'Medication Coding family', item =>
    item.resourceType === 'Medication' && item.system === 'urn:loom:basic-coded-semantic:medication' && item.codingVersion === '1');
  await selectCurrentItem('Medication family', medicationFamily, medicationOption);

  const { inventory: specimenInventory, option: specimenOption } = await browseResource('Specimen');
  await assertSelectionCount(3);
  const specimenFamily = codedCategoryItem(specimenInventory.response.entries, 'Specimen code-fallback family', item =>
    item.resourceType === 'Specimen' && item.system === 'urn:loom:basic-coded-semantic:specimen' && item.codingVersion === '1');
  await selectCurrentItem('Specimen family', specimenFamily, specimenOption);

  const { inventory: observationInventory, option: observationOption } = await browseResource('Observation');
  await assertSelectionCount(4);
  const observationPair = fixtureItem(observationInventory.response.entries, 'paired Observation value', item =>
    item.resourceType === 'Observation' && item.system === 'urn:loom:basic-coded-semantic:observation' &&
    item.code === 'diagnostic-method' && item.constructionChoice?.source.ruleHint === 'CODED_VALUE_V1' &&
    item.constructionChoice.source.fieldPath === 'valueQuantity.value');
  assert.equal(observationPair.selectionMode, undefined,
    'The existing paired Observation value must remain an exact semantic value, not a system/version category.');
  await selectCurrentItem('paired Observation value', observationPair, observationOption);
  await assertSelectionCount(5);
  const selectedItemNames = selectedNames;
  const expectedGroups = ['Condition', 'Medication', 'Observation', 'Specimen'];
  check('correctness', 'native Coded values start in the Patient scope, switch among exact related-resource scopes, preserve accumulated family selections, and keep v1, v2, and unversioned family identities distinct',
    selectedItems.length === 5 && selectedNames.length === 5 &&
      selectedItems.every(item => CODED_RULES.has(item.constructionChoice.source.ruleHint)) &&
      conditionV1.familyId !== conditionV2.familyId &&
      savedFilterStep.operation.kind === 'FILTER' && savedFilterStep.operation.filter.columnId === idColumn.columnId &&
      savedFilterStep.operation.filter.operator === 'EXISTS' && filterCapabilityEntry.response.selectedStage.id === savedFilterStep.id,
    { outputId, initialRequest: rootInventory.request, sourceRequests: [conditionInventory, medicationInventory, specimenInventory, observationInventory].map(result => result.inventory.request),
      groups: expectedGroups, selectedLabels: selectedItems.map(semanticLabel), selectedAccessibleNames: selectedItemNames,
      filterStepId: savedFilterStep.id, filterOperation: savedFilterStep.operation, filterCapabilityStageId: filterCapabilityEntry.response.selectedStage.id });

  const selectAndAdd = async (phase, { cancelBeforeSubmit = false, reuseChoiceResponses } = {}) => {
    const addButton = page.getByRole('button', { name: 'Advanced options', exact: true });
    const choiceStart = networkObserver.requests.length;
    const choicePromises = reuseChoiceResponses ? undefined : selectedItems.map(item => networkObserver.waitForResponse(entry =>
        entry.index >= choiceStart && entry.path.endsWith('/construction-choices') &&
        entry.request?.outputId === outputId && entry.request?.source?.kind === 'SEMANTIC' &&
        entry.request.source.conceptId === item.conceptId && entry.request.source.bindingId === item.bindingId,
      { fromIndex: choiceStart, timeoutMs: ACTION_TIMEOUT_MS }));
    await click(`${phase}: resolve selected source routes`, addButton, async () => {
      await page.getByTestId('catalog-selection-options')
        .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    });
    const selectionOptions = page.getByTestId('catalog-selection-options');
    const choiceResponses = reuseChoiceResponses ?? await Promise.all(choicePromises);
    const selectedChoiceByIdentity = new Map();
    for (const [index, item] of selectedItems.entries()) {
      const identity = semanticIdentity(item);
      const label = semanticLabel(item);
      const response = choiceResponses.find(entry => entry.request.source.conceptId === item.conceptId &&
        entry.request.source.bindingId === item.bindingId);
      assert(response, `${selectedNames[index]} must have an output-specific route search.`);
      assert.equal(response.status, 200, `${selectedNames[index]} route search must return HTTP 200.`);
      assert.equal(response.response.outputId, outputId);
      if (item.selectionMode === 'SYSTEM_VERSION_CATEGORY') {
        assert.equal(response.request.source.selectionMode, 'SYSTEM_VERSION_CATEGORY');
        assert.equal(response.request.source.choiceId, item.constructionChoice.choiceId,
          `${selectedNames[index]} must use its signed system/version family choice.`);
      }
      const routeChoices = response.response.choices.filter(choice =>
        choice.source.kind === 'SEMANTIC' && choice.source.conceptId === item.conceptId &&
        choice.source.bindingId === item.bindingId && choice.source.nodeId === item.constructionChoice.source.nodeId);
      assert(routeChoices.length > 0, `${selectedNames[index]} must retain an exact compiler route for its catalog node.`);
      const shortest = Math.min(...routeChoices.map(choice => choice.route.length));
      const selectedChoice = routeChoices.find(choice => choice.route.length === shortest);
      assert(selectedChoice, `${selectedNames[index]} must have a selectable shortest route.`);
      assert.equal(selectedChoice.source.resourceType, item.resourceType);
      assert.equal(selectedChoice.source.ruleHint, item.constructionChoice.source.ruleHint);
      if (item.selectionMode === 'SYSTEM_VERSION_CATEGORY') {
        assert.equal(selectedChoice.source.system, item.system);
        assert.equal(selectedChoice.source.version ?? '', item.codingVersion);
        assert.equal(selectedChoice.source.code ?? '', '');
      } else {
        assert.equal(selectedChoice.source.system, item.system);
        assert.equal(selectedChoice.source.code, item.code);
        assert.equal(selectedChoice.source.fieldPath, 'valueQuantity.value');
      }
      if (selectedChoice.route.length > 0) {
        assert.equal(selectedChoice.route.at(-1)?.toNodeId, item.constructionChoice.source.nodeId,
          `${selectedNames[index]} route must end at the exact inventory source node.`);
      }
      const itemOptions = selectionOptions.locator('article').nth(index);
      const routeNames = await itemOptions.getByRole('radio').evaluateAll(radios =>
        radios.map(radio => radio.getAttribute('aria-label') ?? ''));
      const routeName = routeNames.find(name => name.includes(routePath(selectedChoice.route)));
      if (routeName) {
        const routeRadio = itemOptions.getByRole('radio', { name: routeName, exact: true });
        if (!(await routeRadio.isChecked())) await click(`${phase}: choose exact route for ${selectedNames[index]}`, routeRadio);
      }
      const allOption = selectedChoice.options.find(option => option.form === 'ALL' && option.support === 'SUPPORTED');
      const defaultOption = selectedChoice.options.find(option => option.decision === 'DEFAULT' && option.support === 'SUPPORTED')
        ?? selectedChoice.options.find(option => option.support === 'SUPPORTED');
      const form = item.selectionMode === 'SYSTEM_VERSION_CATEGORY'
        ? allOption?.form
        : allOption?.form ?? defaultOption?.form;
      assert(form, `${selectedNames[index]} must have a compiler-supported result form.`);
      if (item.selectionMode === 'SYSTEM_VERSION_CATEGORY') {
        assert.equal(form, 'ALL', `${selectedNames[index]} must retain every code in its exact system/version family.`);
      }
      const formNames = await itemOptions.getByRole('radio').evaluateAll(radios =>
        radios.map(radio => radio.getAttribute('aria-label') ?? ''));
      const matchingFormNames = formNames.filter(name => name.includes(formLabel(form)));
      assert.equal(matchingFormNames.length, 1,
        `${selectedNames[index]} must expose one supported ${form} form: ${JSON.stringify(formNames)}`);
      const formRadio = itemOptions.getByRole('radio', { name: matchingFormNames[0], exact: true });
      if (!(await formRadio.isChecked())) await click(`${phase}: choose ${form} for ${selectedNames[index]}`, formRadio);
      selectedChoiceByIdentity.set(identity, { item, choice: selectedChoice, form, accessibleName: selectedNames[index] });
    }
    if (cancelBeforeSubmit) {
      await click(`${phase}: cancel before saving the selected coded columns`,
        selectionOptions.getByRole('button', { name: 'Cancel', exact: true }),
        () => selectionOptions.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }));
      return { selectedChoiceByIdentity, choiceResponses };
    }

    const addStart = networkObserver.requests.length;
    const proposalPromise = networkObserver.waitForResponse(entry => entry.index >= addStart &&
      entry.path.endsWith('/construction-choice-proposals') && entry.request?.outputId === outputId,
    { fromIndex: addStart, timeoutMs: ACTION_TIMEOUT_MS });
    const applyPromise = networkObserver.waitForResponse(entry => entry.index >= addStart &&
      entry.path.endsWith('/commands') && entry.request?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_CHOICE'),
    { fromIndex: addStart, timeoutMs: ACTION_TIMEOUT_MS });
    await click(`${phase}: Add and save the selected coded columns`,
      selectionOptions.getByRole('button', { name: 'Add 5 selected features', exact: true }),
      async () => {
        await selectionOptions.waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
        await waitFor(page, expectedOutput => {
          const preview = document.querySelector('[data-testid="construction-preview"]');
          return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expectedOutput;
        }, outputId);
      });
    const [proposal, applyRequest] = await Promise.all([proposalPromise, applyPromise]);
    assert.equal(proposal.status, 200, 'The native choice proposal must return HTTP 200.');
    assert.equal(applyRequest.status, 200, 'The single Add/Save action must persist the selected coded columns.');
    assert.equal(proposal.response.previewStatus, 'READY');
    assert.equal(proposal.response.preview.outputId, outputId);
    assert.equal(proposal.response.preview.rowCount, oracle.patientIDs.length);
    assert.equal(proposal.response.constructionChoices.length, selectedItems.length);
    assert.deepEqual(proposal.response.constructionChoices.map(choice => choice.choiceId),
      selectedItems.map(item => selectedChoiceByIdentity.get(semanticIdentity(item)).choice.choiceId),
      'The proposal must retain the exact selected family and paired-value identities in selection order.');
    for (const [index, choice] of proposal.response.constructionChoices.entries()) {
      const selected = selectedChoiceByIdentity.get(semanticIdentity(selectedItems[index]));
      assert(selected, `The proposal must retain the selected compiler choice for ${selectedNames[index]}.`);
      assert.equal(choice.title, semanticLabel(selected.item), `${selectedNames[index]} must retain its source-derived base title.`);
      assert.equal(choice.form, selected.form);
    }
    const preview = proposal.response.preview;
    assert.equal(proposal.response.candidateColumnIds.length, selectedItems.length,
      'The proposal must identify one output column per selected family or paired value.');
    const previewColumnsById = new Map(preview.columns.map(column => [column.column, column]));
    for (const columnId of proposal.response.candidateColumnIds) {
      assert(previewColumnsById.has(columnId), `Proposal preview must include candidate output ${columnId}.`);
    }
    const rowByPatient = new Map(preview.rows.map(row => [row[idColumn.column], row]));
    assert.deepEqual([...rowByPatient.keys()].sort(), oracle.patientIDs, 'Proposal rows must preserve both Patient identities.');
    const expectedValueByIdentity = new Map([
      [semanticIdentity(conditionV1), { 'coded-catalog-patient-01': ['Condition v1 display'], 'coded-catalog-patient-02': null }],
      [semanticIdentity(conditionUnversioned), { 'coded-catalog-patient-01': ['Condition unversioned display'], 'coded-catalog-patient-02': null }],
      [semanticIdentity(medicationFamily), { 'coded-catalog-patient-01': ['Medication Alpha', 'Medication Beta'], 'coded-catalog-patient-02': null }],
      [semanticIdentity(specimenFamily), { 'coded-catalog-patient-01': ['specimen-type-no-display'], 'coded-catalog-patient-02': null }],
      [semanticIdentity(observationPair), { 'coded-catalog-patient-01': 42, 'coded-catalog-patient-02': null }],
    ]);
    for (const [index, item] of selectedItems.entries()) {
      const identity = semanticIdentity(item);
      const columnId = proposal.response.candidateColumnIds[index];
      const column = previewColumnsById.get(columnId);
      const { form, accessibleName } = selectedChoiceByIdentity.get(identity);
      for (const patientID of oracle.patientIDs) {
        const value = rowByPatient.get(patientID)?.[column.column];
        const expected = expectedValueByIdentity.get(identity)[patientID];
        if (form === 'ALL') {
          if (expected === null) {
            assert(value === null || (Array.isArray(value) && value.length === 0),
              `${accessibleName} must remain empty when no Coding matches ${patientID}.`);
          } else {
            assert.deepEqual(value, expected,
              `${accessibleName} must return only its exact system/version values for ${patientID}.`);
          }
        } else {
          assert.equal(value ?? null, expected,
            `${accessibleName} must return only its exact paired value for ${patientID}.`);
        }
      }
    }
    return { proposal, applyRequest, preview, selectedChoiceByIdentity, expectedValueByIdentity, choiceResponses };
  };

  const choiceProposalsBeforeCancel = networkObserver.requests.filter(entry => entry.path.endsWith('/construction-choice-proposals')).length;
  const commandsBeforeCancel = networkObserver.requests.filter(entry => entry.path.endsWith('/commands') && entry.isMutation).length;
  const cancelReview = await selectAndAdd('Cancel proof', { cancelBeforeSubmit: true });
  const afterCancel = await readBuilder();
  assert.equal(afterCancel.draftVersion, baselineDraft.version, 'Cancel must not change the saved draft version.');
  assert.equal(afterCancel.draftDigest, baselineDraft.digest, 'Cancel must not change the saved draft digest.');
  assert.equal(networkObserver.requests.filter(entry => entry.path.endsWith('/commands') && entry.isMutation).length, commandsBeforeCancel,
    'Cancel must not send an authoring command.');
  assert.equal(networkObserver.requests.filter(entry => entry.path.endsWith('/construction-choice-proposals')).length,
    choiceProposalsBeforeCancel, 'Cancel before Add/Save must not request a coded-column proposal.');
  check('correctness', 'pre-submit Cancel leaves the saved Patient table and draft unchanged without a coded-column proposal or authoring command',
    afterCancel.draftVersion === baselineDraft.version && afterCancel.draftDigest === baselineDraft.digest &&
      networkObserver.requests.filter(entry => entry.path.endsWith('/commands') && entry.isMutation).length === commandsBeforeCancel &&
      networkObserver.requests.filter(entry => entry.path.endsWith('/construction-choice-proposals')).length === choiceProposalsBeforeCancel,
    { draft: baselineDraft, afterCancel: { version: afterCancel.draftVersion, digest: afterCancel.draftDigest }, commandsBeforeCancel });

  const appliedPreview = await selectAndAdd('Add/Save proof', { reuseChoiceResponses: cancelReview.choiceResponses });
  const applyRequest = appliedPreview.applyRequest;
  assert.equal(applyRequest.status, 200, 'Applying the selected coded columns must persist successfully.');
  builder = await readBuilder();
  document = documentFor(builder);
  assert(document, 'The applied Patient root table must remain in the Builder workspace.');
  const savedColumns = document.columns ?? [];
  const sourceForItem = item => {
    const matches = savedColumns.filter(column => {
      const source = column.source;
      const lookup = source?.kind === 'codedValue' ? source.lookup : undefined;
      if (!lookup || lookup.key.system !== item.system) return false;
      if (item.selectionMode === 'SYSTEM_VERSION_CATEGORY') {
        return lookup.binding.selectionScope === 'SYSTEM_VERSION' &&
          (lookup.key.version ?? '') === item.codingVersion && lookup.key.code === '';
      }
      return lookup.key.code === item.code && (lookup.key.version ?? '') === item.codingVersion;
    });
    assert.equal(matches.length, 1,
      `${semanticLabel(item)} must persist under its exact semantic identity, independent of display-label uniqueness.`);
    return matches[0];
  };
  const categoryItems = [conditionV1, conditionUnversioned, medicationFamily, specimenFamily];
  for (const item of categoryItems) {
    const label = semanticLabel(item);
    const column = sourceForItem(item);
    assert.equal(column.source.lookup.binding.selectionScope, 'SYSTEM_VERSION', `${label} must use the exact system/version family scope.`);
    assert.equal(column.source.lookup.binding.valueScope, 'CODING');
    assert.equal(column.source.lookup.binding.versionPath, 'version', `${label} must match Coding.version exactly.`);
    assert.equal(column.source.lookup.key.system, item.system);
    assert.equal(column.source.lookup.key.code, '', `${label} must not be narrowed to one code inside its family.`);
    assert.equal(column.source.lookup.key.version ?? '', item.codingVersion,
      `${label} must persist the exact version, including the empty unversioned family.`);
  }
  const pairedColumn = sourceForItem(observationPair);
  assert.equal(pairedColumn.source.lookup.binding.selectionScope, undefined,
    'The existing paired Observation value must remain an exact Coding identity.');
  assert.equal(pairedColumn.source.lookup.binding.valueScope, undefined,
    'Legacy paired Observation values must remain owner-scoped.');
  assert.equal(pairedColumn.source.lookup.binding.versionPath, undefined,
    'Legacy paired Observation values must not acquire Coding-scoped version behavior.');
  assert.equal(pairedColumn.source.lookup.key.system, observationPair.system);
  assert.equal(pairedColumn.source.lookup.key.code, observationPair.code);
  assert.equal(pairedColumn.source.lookup.key.version, undefined);
  const persistedFilterSteps = (document.construction?.steps ?? []).filter(step => step.operation?.kind === 'FILTER');
  assert.equal(persistedFilterSteps.length, 1, 'Applying coded values must preserve the existing Patient Filter only.');
  assert.equal(persistedFilterSteps[0].id, savedFilterStep.id);
  assert.equal(persistedFilterSteps[0].operation.filter.operator, 'EXISTS');
  check('correctness', 'one Add/Save action persists CODING-scoped system/version families and leaves the paired Observation value exact and owner-scoped',
    categoryItems.every(item => {
      const source = sourceForItem(item).source.lookup;
      return source.binding.selectionScope === 'SYSTEM_VERSION' && source.binding.valueScope === 'CODING' &&
        source.binding.versionPath === 'version' && source.key.system === item.system && source.key.code === '' &&
        (source.key.version ?? '') === item.codingVersion;
    }) && pairedColumn.source.lookup.binding.valueScope === undefined &&
      persistedFilterSteps.length === 1 && persistedFilterSteps[0].id === savedFilterStep.id &&
      persistedFilterSteps[0].operation.filter.operator === 'EXISTS',
    { outputId, savedLabels: savedColumns.map(column => column.label),
      familyIdentities: categoryItems.map(item => ({ resourceType: item.resourceType, system: item.system, version: item.codingVersion })) });

  const addedColumns = appliedPreview.proposal.response.candidateColumnIds.map(columnId => {
    const matches = savedColumns.filter(column => column.column === columnId);
    assert.equal(matches.length, 1, `Saved document must retain proposal candidate column ${columnId}.`);
    return matches[0];
  });
  for (const [index, item] of selectedItems.entries()) {
    assert.equal(sourceForItem(item).column, appliedPreview.proposal.response.candidateColumnIds[index],
      `${selectedNames[index]} must remain aligned with its exact saved output identity.`);
  }
  const expectedVisibleRows = oracle.patientIDs.map(patientID => {
    const cells = [patientID];
    for (const item of selectedItems) {
      const expected = appliedPreview.expectedValueByIdentity.get(semanticIdentity(item))[patientID];
      cells.push(expected === null ? '—' : normalize(Array.isArray(expected) ? expected.join('; ') : expected));
    }
    return cells;
  }).sort((left, right) => left[0].localeCompare(right[0]));
  const assertAppliedGrid = async label => {
    await waitFor(page, expectedOutput => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expectedOutput;
    }, outputId);
    const grid = await readVisibleGrid(page);
    const expectedLabels = [idColumn.label, ...addedColumns.map(column => column.label)];
    const normalizedExpectedLabels = expectedLabels.map(normalizeHeader);
    assert.deepEqual([...grid.headers].sort(), [...normalizedExpectedLabels].sort(), `${label} must display the exact root ID and five coded columns.`);
    const usedIndices = new Set();
    const columnIndices = expectedLabels.map(header => {
      const expectedHeader = normalizeHeader(header);
      const match = grid.headers.findIndex((actual, index) => actual === expectedHeader && !usedIndices.has(index));
      assert(match >= 0, `${label} must render one exact ${header} column, including repeated readable labels.`);
      usedIndices.add(match);
      return match;
    });
    const actualRows = grid.rows.map(row => columnIndices.map(index => row[index]));
    assert.deepEqual(actualRows.sort((left, right) => left[0].localeCompare(right[0])), expectedVisibleRows,
      `${label} must preserve exact per-patient coded values and version correlation.`);
  };
  await assertAppliedGrid('Applied standalone coded values');
  const savedVersion = builder.draftVersion;
  const savedDigest = builder.draftDigest;
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  await waitFor(page, expected => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expected.outputId &&
      preview.dataset.currentDraftDigest === expected.digest && preview.dataset.currentDraftVersion === String(expected.version);
  }, { outputId, digest: savedDigest, version: savedVersion });
  builder = await readBuilder();
  document = documentFor(builder);
  assert.equal(builder.draftVersion, savedVersion);
  assert.equal(builder.draftDigest, savedDigest);
  await assertAppliedGrid('Reloaded standalone coded values');
  check('correctness', 'Applied coded fields and exact per-patient values survive Builder reload',
    builder.draftVersion === savedVersion && builder.draftDigest === savedDigest && savedColumns.length === 6,
    { outputId, version: builder.draftVersion, digest: builder.draftDigest, headers: (await readVisibleGrid(page)).headers,
      families: categoryItems.map(item => ({ system: item.system, version: item.codingVersion, column: sourceForItem(item).column })) });

  const finalOracle = readStandaloneCodedFieldFixture(target.fixtureDir);
  const sourceFilesAfter = Object.fromEntries(Object.entries(finalOracle.sourceFiles).map(([resourceType, file]) => [resourceType, file.sha256]));
  assert.deepEqual(sourceFilesAfter, sourceFilesBefore, 'Native authoring must not change any fixture source file.');
  check('correctness', 'independent FHIR source fixture remains unchanged through the lifecycle',
    JSON.stringify(sourceFilesAfter) === JSON.stringify(sourceFilesBefore), { sourceFilesBefore, sourceFilesAfter });
};
