import assert from 'node:assert/strict';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { readStandaloneCodedFieldFixture } from '../helpers/standalone-coded-field-fixture.mjs';

const ACTION_TIMEOUT_MS = 10_000;
const CODED_RULES = new Set(['CODED_CONCEPT_V1', 'CODED_VALUE_V1']);
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();

const waitFor = (page, predicate, argument, timeoutMs = ACTION_TIMEOUT_MS) => page.waitForFunction(
  ({ predicateSource, argument: value }) => Boolean(Function('argument', `return (${predicateSource})(argument);`)(value)),
  { predicateSource: predicate.toString(), argument },
  { timeout: timeoutMs },
);

const readVisibleGrid = page => page.locator('[data-testid="preview-table-scroll"] [role="table"]').evaluate(table => ({
  headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => cell.innerText.replace(/\s+/g, ' ').trim()),
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

const semanticLabel = item => item.display?.trim() || item.code;
const formLabel = form => ({
  VALUE: 'One value',
  FIRST: 'First value',
  ALL: 'All values',
  DISTINCT: 'Distinct values',
  COUNT: 'Matching record count',
  PRESENCE: 'Has matching record',
  OWNER_RECORDS: 'Matching records',
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
  check('correctness', 'literal fixture proves standalone coded sources and paired Observation values with exact versions',
    oracle.patientIDs.length === 2 &&
      oracle.conditionCodesByPatient['coded-catalog-patient-01'].length === 3 &&
      oracle.conditionCodesByPatient['coded-catalog-patient-01'].map(item => item.version ?? '').join(',') === 'v1,v2,' &&
      oracle.medicationCodesByPatient['coded-catalog-patient-01'].length === 2 &&
      oracle.specimenCodesByPatient['coded-catalog-patient-01'][0]?.display === undefined &&
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
  const idIndex = initialGrid.headers.findIndex(header => header === idColumn.label);
  assert(idIndex >= 0, 'The exact Patient ID column must render in the root preview.');
  assert.deepEqual(initialGrid.rows.map(row => row[idIndex]).sort(), oracle.patientIDs,
    'The initial native Patient preview must preserve the exact fixture IDs.');
  const baselineDraft = { version: builder.draftVersion, digest: builder.draftDigest };

  const openCatalogAndReadInventory = async label => {
    const editor = page.locator('[aria-label="Add columns editor"]');
    const fromIndex = networkObserver.requests.length;
    const inventoryPromise = networkObserver.waitForResponse(entry => entry.index >= fromIndex &&
      entry.path.endsWith('/semantic-inventory') && entry.request?.outputId === outputId,
    { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
    await click(`${label}: open Add columns`, page.getByTestId('construction-action-add-columns'),
      () => editor.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
    const inventoryRequest = await inventoryPromise;
    assert.equal(inventoryRequest.status, 200, 'The native semantic inventory request must succeed.');
    assert.equal(inventoryRequest.request.outputId, outputId, 'Coded browsing must be scoped to the current Patient output.');
    assert.equal(inventoryRequest.request.resourceType, undefined, 'The initial coded browse must include all eligible resource types.');
    assert.equal(inventoryRequest.request.sourceNodeId, undefined, 'The initial coded browse must not require a source-node filter.');
    assert.equal(String(inventoryRequest.request.query ?? '').trim(), '', 'The initial coded browse must not require typed search.');
    assert.equal(inventoryRequest.response.outputId, outputId);
    assert.equal(inventoryRequest.response.snapshotToken, inventoryRequest.request.snapshotToken);
    assert.equal(inventoryRequest.response.state, 'complete');
    return inventoryRequest;
  };

  const inventory = await openCatalogAndReadInventory('first browse');
  const entries = inventory.response.entries;
  const allowedEntries = entries.filter(item => CODED_RULES.has(item.constructionChoice?.source.ruleHint));
  const conditionV1 = fixtureItem(allowedEntries, 'Condition v1 Coding', item => item.resourceType === 'Condition' &&
    item.system === 'urn:loom:basic-coded-semantic:condition' && item.code === 'condition-shared' &&
    item.constructionChoice?.source.version === 'v1' && item.constructionChoice.source.ruleHint === 'CODED_CONCEPT_V1');
  const conditionUnversioned = fixtureItem(allowedEntries, 'unversioned Condition Coding', item => item.resourceType === 'Condition' &&
    item.system === 'urn:loom:basic-coded-semantic:condition' && item.code === 'condition-shared' &&
    (item.constructionChoice?.source.version ?? '') === '' && item.constructionChoice.source.ruleHint === 'CODED_CONCEPT_V1');
  const medicationBeta = fixtureItem(allowedEntries, 'Medication Beta Coding', item => item.resourceType === 'Medication' &&
    item.system === 'urn:loom:basic-coded-semantic:medication' && item.code === 'medication-beta' &&
    item.constructionChoice?.source.ruleHint === 'CODED_CONCEPT_V1');
  const specimenCode = fixtureItem(allowedEntries, 'Specimen code-only Coding', item => item.resourceType === 'Specimen' &&
    item.system === 'urn:loom:basic-coded-semantic:specimen' && item.code === 'specimen-type-no-display' &&
    item.constructionChoice?.source.ruleHint === 'CODED_CONCEPT_V1');
  const observationPair = fixtureItem(allowedEntries, 'paired Observation value', item => item.resourceType === 'Observation' &&
    item.system === 'urn:loom:basic-coded-semantic:observation' && item.code === 'diagnostic-method' &&
    item.constructionChoice?.source.ruleHint === 'CODED_VALUE_V1');
  const selectedItems = [conditionV1, conditionUnversioned, medicationBeta, specimenCode, observationPair];
  const expectedGroups = ['Condition', 'Medication', 'Observation', 'Specimen'];
  for (const resourceType of expectedGroups) {
    assert(entries.some(item => item.resourceType === resourceType && CODED_RULES.has(item.constructionChoice?.source.ruleHint)),
      `The output-scoped coded inventory must include an eligible ${resourceType} source.`);
    await page.getByRole('heading', { name: `Coded values from ${resourceType}`, exact: true })
      .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  }
  assert(entries.some(item => item.resourceType === 'Condition' && item.constructionChoice?.source.version === 'v2'),
    'The UI inventory must keep the same system/code at the distinct v2 Coding.');
  for (const item of selectedItems) {
    const label = semanticLabel(item);
    const checkbox = page.getByRole('checkbox', { name: `Select ${label}`, exact: true });
    await checkbox.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    assert.equal(await checkbox.isEnabled(), true, `${label} must be selectable from the current coded inventory.`);
  }
  check('correctness', 'native Coded values browse shows compiler-proven entries from all eligible source resources without search',
    selectedItems.every(item => CODED_RULES.has(item.constructionChoice.source.ruleHint)) &&
      expectedGroups.every(resourceType => entries.some(item => item.resourceType === resourceType && CODED_RULES.has(item.constructionChoice?.source.ruleHint))),
    { outputId, request: inventory.request, groups: expectedGroups, selectedLabels: selectedItems.map(semanticLabel) });

  const selectAndPreview = async phase => {
    for (const item of selectedItems) {
      const label = semanticLabel(item);
      const checkbox = page.getByRole('checkbox', { name: `Select ${label}`, exact: true });
      if (!(await checkbox.isChecked())) await click(`${phase}: select ${label}`, checkbox,
        async () => assert.equal(await checkbox.isChecked(), true));
    }
    const addButton = page.getByRole('button', { name: 'Add 5 selected features', exact: true });
    const choiceStart = networkObserver.requests.length;
    const choicePromises = selectedItems.map(item => networkObserver.waitForResponse(entry =>
      entry.index >= choiceStart && entry.path.endsWith('/construction-choices') &&
      entry.request?.outputId === outputId && entry.request?.source?.kind === 'SEMANTIC' &&
      entry.request.source.conceptId === item.conceptId && entry.request.source.bindingId === item.bindingId,
    { fromIndex: choiceStart, timeoutMs: ACTION_TIMEOUT_MS }));
    await click(`${phase}: resolve selected source routes`, addButton, async () => {
      await page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true })
        .waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    });
    const choiceResponses = await Promise.all(choicePromises);
    const selectedChoiceByLabel = new Map();
    for (const item of selectedItems) {
      const label = semanticLabel(item);
      const response = choiceResponses.find(entry => entry.request.source.conceptId === item.conceptId &&
        entry.request.source.bindingId === item.bindingId);
      assert(response, `${label} must have an output-specific route search.`);
      assert.equal(response.status, 200, `${label} route search must return HTTP 200.`);
      assert.equal(response.response.outputId, outputId);
      const routeChoices = response.response.choices.filter(choice =>
        choice.source.kind === 'SEMANTIC' && choice.source.conceptId === item.conceptId &&
        choice.source.bindingId === item.bindingId && choice.source.nodeId === item.constructionChoice.source.nodeId);
      assert(routeChoices.length > 0, `${label} must retain an exact compiler route for its catalog node.`);
      const shortest = Math.min(...routeChoices.map(choice => choice.route.length));
      const selectedChoice = routeChoices.find(choice => choice.route.length === shortest);
      assert(selectedChoice, `${label} must have a selectable shortest route.`);
      assert.equal(selectedChoice.source.resourceType, item.resourceType);
      assert.equal(selectedChoice.source.ruleHint, item.constructionChoice.source.ruleHint);
      if (selectedChoice.route.length > 0) {
        assert.equal(selectedChoice.route.at(-1)?.toNodeId, item.constructionChoice.source.nodeId,
          `${label} route must end at the exact inventory source node.`);
      }
      const routeRadioName = `${label}: ${routePath(selectedChoice.route)}`;
      const routeRadio = page.getByRole('dialog').getByRole('radio', { name: routeRadioName, exact: true });
      if (await routeRadio.count()) {
        if (!(await routeRadio.isChecked())) await click(`${phase}: choose ${item.resourceType} route for ${label}`, routeRadio);
      }
      const allOption = selectedChoice.options.find(option => option.form === 'ALL' && option.support === 'SUPPORTED');
      const defaultOption = selectedChoice.options.find(option => option.decision === 'DEFAULT' && option.support === 'SUPPORTED')
        ?? selectedChoice.options.find(option => option.support === 'SUPPORTED');
      const form = allOption?.form ?? defaultOption?.form;
      assert(form, `${label} must have a compiler-supported result form.`);
      const formRadio = page.getByRole('dialog').getByRole('radio', { name: `${label}: ${formLabel(form)}`, exact: true });
      assert.equal(await formRadio.count(), 1, `${label} must expose the selected supported ${form} form.`);
      if (!(await formRadio.isChecked())) await click(`${phase}: choose ${form} for ${label}`, formRadio);
      selectedChoiceByLabel.set(label, { item, choice: selectedChoice, form });
    }
    const proposalStart = networkObserver.requests.length;
    await click(`${phase}: submit the selected coded columns`, page.getByRole('dialog').getByRole('button', { name: 'Add 5 columns', exact: true }),
      () => page.getByTestId('construction-choice-proposal-panel').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
    const proposal = await networkObserver.waitForResponse(entry => entry.index >= proposalStart &&
      entry.path.endsWith('/construction-choice-proposals') && entry.request?.outputId === outputId,
    { fromIndex: proposalStart, timeoutMs: ACTION_TIMEOUT_MS });
    assert.equal(proposal.status, 200, 'The native choice proposal must return HTTP 200.');
    assert.equal(proposal.response.previewStatus, 'READY');
    assert.equal(proposal.response.preview.outputId, outputId);
    assert.equal(proposal.response.preview.rowCount, oracle.patientIDs.length);
    assert.deepEqual(proposal.response.constructionChoices.map(choice => choice.title).sort(),
      selectedItems.map(semanticLabel).sort(), 'The proposal must contain the exact five selected source labels.');
    for (const choice of proposal.response.constructionChoices) {
      const selected = selectedChoiceByLabel.get(choice.title);
      assert(selected, `The proposal must retain the selected compiler choice for ${choice.title}.`);
      assert.equal(choice.choiceId, selected.choice.choiceId);
      assert.equal(choice.form, selected.form);
    }
    const preview = proposal.response.preview;
    const previewColumns = new Map(preview.columns.map(column => [column.label, column]));
    for (const label of selectedItems.map(semanticLabel)) assert(previewColumns.has(label), `Proposal preview must include ${label}.`);
    const rowByPatient = new Map(preview.rows.map(row => [row[idColumn.column], row]));
    assert.deepEqual([...rowByPatient.keys()].sort(), oracle.patientIDs, 'Proposal rows must preserve both Patient identities.');
    const expectedValueByLabel = new Map([
      [semanticLabel(conditionV1), { 'coded-catalog-patient-01': 'Condition v1 display', 'coded-catalog-patient-02': null }],
      [semanticLabel(conditionUnversioned), { 'coded-catalog-patient-01': 'Condition unversioned display', 'coded-catalog-patient-02': null }],
      [semanticLabel(medicationBeta), { 'coded-catalog-patient-01': 'Medication Beta', 'coded-catalog-patient-02': null }],
      [semanticLabel(specimenCode), { 'coded-catalog-patient-01': 'specimen-type-no-display', 'coded-catalog-patient-02': null }],
      [semanticLabel(observationPair), { 'coded-catalog-patient-01': 42, 'coded-catalog-patient-02': null }],
    ]);
    for (const [label, expectedByPatient] of expectedValueByLabel) {
      const column = previewColumns.get(label);
      const { form } = selectedChoiceByLabel.get(label);
      for (const patientID of oracle.patientIDs) {
        const value = rowByPatient.get(patientID)?.[column.column];
        const expected = expectedByPatient[patientID];
        if (form === 'ALL') {
          assert.deepEqual(value, expected === null ? [] : [expected],
            `${label} must return only its exact matched Coding value for ${patientID}.`);
        } else {
          assert.equal(value ?? null, expected,
            `${label} must return only its exact matched Coding value for ${patientID}.`);
        }
      }
    }
    return { proposal, preview, selectedChoiceByLabel, expectedValueByLabel };
  };

  const firstPreview = await selectAndPreview('Cancel proof');
  const cancelBaseline = await readBuilder();
  assert.equal(cancelBaseline.draftVersion, baselineDraft.version);
  assert.equal(cancelBaseline.draftDigest, baselineDraft.digest);
  const commandsBeforeCancel = networkObserver.requests.filter(entry => entry.path.endsWith('/commands') && entry.isMutation).length;
  await click('cancel coded-column preview without saving', page.getByTestId('construction-choice-proposal-panel').getByRole('button', { name: 'Cancel', exact: true }),
    () => page.getByTestId('construction-choice-proposal-panel').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS }));
  const afterCancel = await readBuilder();
  assert.equal(afterCancel.draftVersion, baselineDraft.version, 'Cancel must not change the saved draft version.');
  assert.equal(afterCancel.draftDigest, baselineDraft.digest, 'Cancel must not change the saved draft digest.');
  assert.equal(networkObserver.requests.filter(entry => entry.path.endsWith('/commands') && entry.isMutation).length, commandsBeforeCancel,
    'Cancel must not send an authoring command.');
  check('correctness', 'Cancel leaves the saved Patient table and draft unchanged',
    afterCancel.draftVersion === baselineDraft.version && afterCancel.draftDigest === baselineDraft.digest &&
      networkObserver.requests.filter(entry => entry.path.endsWith('/commands') && entry.isMutation).length === commandsBeforeCancel,
    { draft: baselineDraft, afterCancel: { version: afterCancel.draftVersion, digest: afterCancel.draftDigest }, commandsBeforeCancel });

  const appliedPreview = await selectAndPreview('Apply proof');
  const applyStart = networkObserver.requests.length;
  await click('apply standalone coded columns', page.getByTestId('construction-choice-proposal-panel').getByRole('button', { name: 'Apply columns', exact: true }),
    async () => {
      await page.getByTestId('construction-choice-proposal-panel').waitFor({ state: 'detached', timeout: ACTION_TIMEOUT_MS });
      await waitFor(page, expectedOutput => {
        const preview = document.querySelector('[data-testid="construction-preview"]');
        return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expectedOutput;
      }, outputId);
    });
  const applyRequest = await networkObserver.waitForResponse(entry => entry.index >= applyStart &&
    entry.path.endsWith('/commands') && entry.request?.commands?.some(command => command.type === 'APPLY_CONSTRUCTION_CHOICE'),
  { fromIndex: applyStart, timeoutMs: ACTION_TIMEOUT_MS });
  assert.equal(applyRequest.status, 200, 'Applying the selected coded columns must persist successfully.');
  builder = await readBuilder();
  document = documentFor(builder);
  assert(document, 'The applied Patient root table must remain in the Builder workspace.');
  const savedColumns = document.columns ?? [];
  const sourceForLabel = label => {
    const matches = savedColumns.filter(column => column.label === label);
    assert.equal(matches.length, 1, `The saved table must contain one ${label} column.`);
    assert.equal(matches[0].source?.kind, 'codedValue', `${label} must persist as a coded-value column.`);
    return matches[0];
  };
  for (const item of [conditionV1, conditionUnversioned, medicationBeta, specimenCode]) {
    const label = semanticLabel(item);
    const column = sourceForLabel(label);
    assert.equal(column.source.lookup.binding.valueScope, 'CODING');
    assert.equal(column.source.lookup.binding.versionPath, 'version', `${label} must match Coding.version exactly.`);
    assert.equal(column.source.lookup.key.system, item.system);
    assert.equal(column.source.lookup.key.code, item.code);
    assert.equal(column.source.lookup.key.version ?? '', item.constructionChoice.source.version ?? '',
      `${label} must persist the exact version, including the empty unversioned value.`);
  }
  const pairedColumn = sourceForLabel(semanticLabel(observationPair));
  assert.equal(pairedColumn.source.lookup.binding.valueScope, undefined,
    'Legacy paired Observation values must remain owner-scoped.');
  assert.equal(pairedColumn.source.lookup.binding.versionPath, undefined,
    'Legacy paired Observation values must not acquire Coding-scoped version behavior.');
  assert.equal(pairedColumn.source.lookup.key.version, undefined);
  assert.equal(document.construction?.steps?.length ?? 0, 0,
    'Standalone coded value columns must not create a row-shaping or RELATED_SOURCE step.');
  check('correctness', 'Apply persists CODING-scoped version-aware sources and leaves paired values owner-scoped',
    [conditionV1, conditionUnversioned, medicationBeta, specimenCode].every(item => {
      const source = sourceForLabel(semanticLabel(item)).source.lookup;
      return source.binding.valueScope === 'CODING' && source.binding.versionPath === 'version' &&
        source.key.system === item.system && source.key.code === item.code &&
        (source.key.version ?? '') === (item.constructionChoice.source.version ?? '');
    }) && pairedColumn.source.lookup.binding.valueScope === undefined &&
      (document.construction?.steps?.length ?? 0) === 0,
    { outputId, savedLabels: savedColumns.map(column => column.label), versionedLabels: [semanticLabel(conditionV1), semanticLabel(conditionUnversioned)] });

  const expectedVisibleRows = oracle.patientIDs.map(patientID => {
    const cells = [patientID];
    for (const label of appliedPreview.expectedValueByLabel.keys()) {
      const expected = appliedPreview.expectedValueByLabel.get(label)[patientID];
      cells.push(expected === null ? '—' : normalize(expected));
    }
    return cells;
  }).sort((left, right) => left[0].localeCompare(right[0]));
  const assertAppliedGrid = async label => {
    await waitFor(page, expectedOutput => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expectedOutput;
    }, outputId);
    const grid = await readVisibleGrid(page);
    const expectedLabels = [idColumn.label, ...appliedPreview.expectedValueByLabel.keys()];
    assert.deepEqual([...grid.headers].sort(), [...expectedLabels].sort(), `${label} must display the exact root ID and five coded columns.`);
    const columnIndices = expectedLabels.map(header => {
      const matches = grid.headers.flatMap((actual, index) => actual === header ? [index] : []);
      assert.equal(matches.length, 1, `${label} must render one exact ${header} column.`);
      return matches[0];
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
    { outputId, version: builder.draftVersion, digest: builder.draftDigest, headers: (await readVisibleGrid(page)).headers });

  const finalOracle = readStandaloneCodedFieldFixture(target.fixtureDir);
  const sourceFilesAfter = Object.fromEntries(Object.entries(finalOracle.sourceFiles).map(([resourceType, file]) => [resourceType, file.sha256]));
  assert.deepEqual(sourceFilesAfter, sourceFilesBefore, 'Native authoring must not change any fixture source file.');
  check('correctness', 'independent FHIR source fixture remains unchanged through the lifecycle',
    JSON.stringify(sourceFilesAfter) === JSON.stringify(sourceFilesBefore), { sourceFilesBefore, sourceFilesAfter });
};
