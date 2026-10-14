import assert from 'node:assert/strict';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { readRelatedExpandFieldLifecycleOracle } from '../helpers/related-expand-field-lifecycle-oracle.mjs';

const ACTION_TIMEOUT_MS = 5_000;
const ROOT_RESOURCE = 'Observation';
const RELATED_RESOURCE = 'MedicationAdministration';
const EXPECTED_ROUTE = [
  { fromResourceType: 'Observation', toResourceType: 'Patient', relationship: 'subject_Patient', storageDirection: 'OUTBOUND' },
  { fromResourceType: 'Patient', toResourceType: 'MedicationAdministration', relationship: 'subject_Patient', storageDirection: 'INBOUND' },
];
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const visibleValue = value => value === null || value === undefined ? '—' : normalize(value);
const sortedRows = rows => [...rows].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));

const readGrid = async (page, proposal = false) => {
  const selector = proposal
    ? '[data-testid="construction-proposal-preview"] table'
    : '[data-testid="preview-table-scroll"] [role="table"]';
  const table = page.locator(selector);
  await table.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  return table.evaluate((element, isProposal) => {
    const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    if (isProposal) {
      const headers = [...element.querySelectorAll('thead th')]
        .map(cell => normalize(cell.querySelector('span')?.innerText ?? cell.innerText));
      const rows = [...element.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
        .map(row => [...row.querySelectorAll('td')].map(cell => normalize(cell.innerText)));
      return { headers, rows };
    }
    const rows = [...element.querySelectorAll('[role="row"]')];
    return {
      headers: [...(rows[0]?.querySelectorAll('[role="columnheader"]') ?? [])]
        .map(cell => normalize(cell.innerText)),
      rows: rows.slice(1).filter(row => row.querySelector('[role="cell"]'))
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => normalize(cell.innerText))),
    };
  }, proposal);
};

const assertRowsByLabels = (grid, labels, expected, phase) => {
  const indices = labels.map(label => grid.headers.findIndex(header => header.toLowerCase() === label.toLowerCase()));
  assert(indices.every(index => index >= 0), `${phase}: missing expected public columns ${JSON.stringify(labels)} in ${JSON.stringify(grid.headers)}`);
  const actual = sortedRows(grid.rows.map(row => indices.map(index => row[index])));
  assert.deepEqual(actual, sortedRows(expected.map(row => row.map(visibleValue))), `${phase}: rendered rows differ from the literal fixture oracle`);
  return actual;
};

const waitForProposal = async page => page.waitForFunction(() => {
  const panel = document.querySelector('[data-testid="construction-proposal-panel"]') ??
    document.querySelector('[data-testid="construction-choice-proposal-panel"]');
  const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
  return panel?.dataset.proposalStatus === 'ready' && preview?.dataset.previewStatus === 'ready';
}, undefined, { timeout: ACTION_TIMEOUT_MS });

const waitForSavedPreview = async (page, outputId, rowCount) => page.waitForFunction(({ outputId, rowCount }) => {
  const preview = document.querySelector('[data-testid="construction-preview"]');
  const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === outputId &&
    Boolean(preview.dataset.previewReceiptId) && table?.getAttribute('aria-rowcount') === String(rowCount + 1) &&
    !document.body.innerText.includes('Loading your table…');
}, { outputId, rowCount }, { timeout: ACTION_TIMEOUT_MS });

const readVisibleWorkspace = async (page, target, authoringBase) => {
  const response = await page.context().request.get(`${target.apiUrl}${authoringBase}/builder`, { timeout: ACTION_TIMEOUT_MS });
  const value = await response.json();
  assert(response.ok(), `Read-only Builder request failed with HTTP ${response.status()}: ${JSON.stringify(value).slice(0, 800)}`);
  assert.equal(value.catalog?.generation, target.fixtureGeneration, 'Builder reads must stay on the exact fixture generation.');
  return value;
};

const documentFor = (builder, outputId) => builder.workspace.documents.find(document => document.output?.id === outputId);
const sourceField = (document, path) => document.columns.find(column =>
  column.source?.field?.path === path && column.occurrenceId === 'base');
const relatedFieldStep = document => document.construction?.steps?.find(step => step.operation?.kind === 'RELATED_FIELD');
const expandStep = document => document.construction?.steps?.find(step => step.operation?.kind === 'RELATED_EXPAND');

export const relatedExpandFieldLifecycleWorkflow = async ({ page, action, check, networkObserver, report, target, seed }) => {
  configureNativePage(page);
  assert.equal(report.scenario, 'basic-related-expand-field-lifecycle');
  assert.equal(seed?.fresh, true, 'Related field lifecycle must use a fresh Basic fixture project.');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_'), 'Expected an isolated per-run Basic fixture project.');
  assert.equal(target.fixtureGeneration, 'cda-fhir-v1');
  assert(target.fixtureDir, 'Fixture directory must be explicit.');

  const oracle = readRelatedExpandFieldLifecycleOracle(target.fixtureDir);
  report.target.fixtureOracle = {
    kind: 'independent literal Basic fixture oracle; no loaded CDA dataset claim',
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sourceFiles: oracle.sourceFiles,
    route: oracle.route,
    exactRows: oracle.rows,
    groupedRows: oracle.groups,
  };
  check('correctness', 'literal fixture proves two exact MedicationAdministration matches and one unmatched Observation root',
    oracle.rows.length === 3 && oracle.rows.filter(row => row.medicationAdministrationId !== null).length === 2 &&
      oracle.rows.filter(row => row.medicationAdministrationId === null).length === 1,
    report.target.fixtureOracle);

  const doAction = (label, locator, perform, after, editable = false) => action(label, locator, perform, {
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
    ...(editable ? { editable: true } : {}),
    ...(after ? { after } : {}),
  });
  const click = (label, locator, after) => doAction(label, locator, () => locator.click({ timeout: ACTION_TIMEOUT_MS }), after);
  const fill = (label, locator, value, after) => doAction(label, locator, () => locator.fill(value, { timeout: ACTION_TIMEOUT_MS }), after, true);
  const project = target.fixtureProject;
  const authoringBase = `/api/v1/projects/${encodeURIComponent(project)}/explorers`;
  await page.goto(browserURL(target, project, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  await page.waitForFunction(() => document.body.innerText.includes('Build your first table') ||
    document.body.innerText.includes('Dataset graph'), undefined, { timeout: ACTION_TIMEOUT_MS });

  const newExplorer = page.locator('summary').filter({ hasText: /^New explorer$/ });
  await click('open fresh Explorer creation', newExplorer,
    () => page.locator('#new-explorer-name').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
  const explorerName = `Verify ${report.target.project.slice(-10)} RelatedExpand fields`;
  await fill('name fresh RelatedExpand field Explorer', page.locator('#new-explorer-name'), explorerName);
  await click('create fresh related-field Explorer', page.getByRole('button', { name: 'Create blank', exact: true }), () =>
    page.waitForFunction(name => document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === name,
      explorerName, { timeout: ACTION_TIMEOUT_MS }));
  const explorer = await page.getByRole('combobox', { name: 'Explorer', exact: true }).inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'The lifecycle must use its own fresh Explorer.');
  report.target.explorer = explorer;

  await fill('name Observation root table', page.locator('#first-table-name'), 'Observation RelatedExpand fields');
  await click('create native Observation root table', page.getByRole('button', { name: 'Choose Observation rows', exact: true }), async () => {
    await page.getByTestId('construction-action-add-columns').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    await page.waitForFunction(() => document.querySelector('[data-testid="construction-preview"]')?.dataset.previewStatus === 'ready' &&
      document.querySelector('[data-testid="preview-table-scroll"] [role="table"]')?.getAttribute('aria-rowcount') === '3',
    undefined, { timeout: ACTION_TIMEOUT_MS });
  });

  const builderPath = `${authoringBase}/${encodeURIComponent(explorer)}/authoring/v2`;
  let builder = await readVisibleWorkspace(page, target, builderPath);
  let document = builder.workspace.documents.find(item => item.rootResourceType === ROOT_RESOURCE);
  assert(document?.output?.id, 'Native root setup must create an Observation table.');
  const outputId = document.output.id;
  report.target.outputId = outputId;
  assert.equal(document.rows?.kind, 'RECORDS', 'Native root setup must begin with an Observation RECORDS source.');
  const rootIDs = oracle.rows.map(row => row.observationId).filter((id, index, ids) => ids.indexOf(id) === index).sort();
  const rootGrid = await readGrid(page);
  const rootIDColumn = sourceField(document, 'id');
  assert(rootIDColumn?.label, 'Observation root must retain its public ID column.');
  const rootIDColumnIndex = rootGrid.headers.findIndex(header =>
    header.toLowerCase() === rootIDColumn.label.toLowerCase());
  assert(rootIDColumnIndex >= 0, `Rendered table is missing the Observation ID header: ${JSON.stringify(rootGrid.headers)}`);
  const renderedRootIDs = rootGrid.rows.map(row => row[rootIDColumnIndex]).sort();
  assert.deepEqual(renderedRootIDs, rootIDs, 'Native Observation root rows must equal the exact fixture IDs.');

  const editor = page.locator('[aria-label="Add columns editor"]');
  const openAddColumns = async label => {
    if (!await editor.isVisible()) {
      await click(label, page.getByTestId('construction-action-add-columns'),
        () => editor.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
    }
    const fieldsFamily = editor.getByRole('button', { name: 'Fields and related data', exact: true });
    if (await fieldsFamily.getAttribute('aria-pressed') !== 'true') {
      await click('choose Fields and related data', fieldsFamily,
        () => editor.getByTestId('construction-add-columns-source').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
    }
  };
  const openRelatedExpand = async () => {
    const settings = page.getByTestId('construction-rows-settings-trigger');
    await click('open native row settings for RelatedExpand', settings,
      () => page.getByTestId('construction-action-related-rows').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
    await click('open native RelatedExpand editor', page.getByTestId('construction-action-related-rows'),
      () => page.getByTestId('construction-related-expand-editor').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
    const editorPanel = page.getByTestId('construction-related-expand-editor');
    const targetType = editorPanel.getByRole('combobox', { name: 'Related record type', exact: true });
    await doAction('choose MedicationAdministration related rows', targetType,
      () => targetType.selectOption(RELATED_RESOURCE, { timeout: ACTION_TIMEOUT_MS }), async () => {
        await page.getByRole('radio', {
          name: 'Observation -[subject]-> Patient <-[subject]- MedicationAdministration', exact: true,
        }).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      });
    const emptyPolicy = editorPanel.getByRole('combobox', { name: 'If a current row has no matches', exact: true });
    if (await emptyPolicy.inputValue() !== 'PRESERVE_PARENT') {
      await doAction('preserve unmatched Observation roots', emptyPolicy,
        () => emptyPolicy.selectOption('PRESERVE_PARENT', { timeout: ACTION_TIMEOUT_MS }),
        () => page.waitForFunction(() => document.querySelector('[data-testid="construction-related-expand-editor"] select[aria-label="If a current row has no matches"]')?.value === 'PRESERVE_PARENT',
          undefined, { timeout: ACTION_TIMEOUT_MS }));
    }
    const route = editorPanel.getByRole('radio', {
      name: 'Observation -[subject]-> Patient <-[subject]- MedicationAdministration', exact: true,
    });
    if (!await route.isChecked()) {
      await click('preview the exact Observation to MedicationAdministration route', route, () => waitForProposal(page));
    }
    return { editorPanel, route };
  };

  await openRelatedExpand();
  const postExpandCapabilityFrom = networkObserver.requests.length;
  await click('Apply exact RelatedExpand route', page.getByTestId('construction-apply-proposal'), async () => {
    await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
      { timeout: ACTION_TIMEOUT_MS });
    await waitForSavedPreview(page, outputId, 3);
    const current = await readVisibleWorkspace(page, target, builderPath);
    const expandedDocument = documentFor(current, outputId);
    const step = expandStep(expandedDocument);
    assert(step, 'RelatedExpand Apply must persist its exact route.');
    assert.equal(step.operation.relatedExpand.targetResourceType, RELATED_RESOURCE);
    assert.equal(step.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT');
    assert.deepEqual(step.operation.relatedExpand.route.map(edge => ({
      fromResourceType: edge.fromResourceType,
      toResourceType: edge.toResourceType,
      relationship: edge.relationship,
      storageDirection: edge.storageDirection,
    })), EXPECTED_ROUTE);
    const relatedID = step.outputs.find(column => column.id === step.operation.relatedExpand.relatedRecordColumnId);
    assert(relatedID?.label, 'RelatedExpand must preserve its public MedicationAdministration ID output.');
    const grid = await readGrid(page);
    assertRowsByLabels(grid, [rootIDColumn.label, relatedID.label], [
      ['field-lifecycle-observation-matched', 'field-lifecycle-administration-a'],
      ['field-lifecycle-observation-matched', 'field-lifecycle-administration-b'],
      ['field-lifecycle-observation-unmatched', null],
    ], 'RelatedExpand Apply');
    check('correctness', 'native RelatedExpand proposal uses the exact two-hop route and PRESERVE_PARENT row policy', true,
      { route: step.operation.relatedExpand.route, emptyPolicy: step.operation.relatedExpand.emptyPolicy });
    report.target.relatedExpandOutput = { stepId: step.id, relatedRecordColumnId: relatedID.id, label: relatedID.label };
  });
  builder = await readVisibleWorkspace(page, target, builderPath);
  document = documentFor(builder, outputId);
  const expandedStep = expandStep(document);
  const relatedIDColumn = expandedStep.outputs.find(column => column.id === expandedStep.operation.relatedExpand.relatedRecordColumnId);

  const capabilityEntry = await networkObserver.waitForResponse(entry =>
    entry.index >= postExpandCapabilityFrom && entry.method === 'POST' &&
      entry.path.endsWith('/construction-capabilities') && entry.status === 200 &&
      entry.request?.outputId === outputId && entry.request?.stageId === expandedStep.id &&
      entry.response?.outputId === outputId && entry.response?.stageId === expandedStep.id &&
      entry.response?.selectedStage?.id === expandedStep.id,
  { fromIndex: postExpandCapabilityFrom, timeoutMs: ACTION_TIMEOUT_MS });
  const capabilityResponse = capabilityEntry.response;
  const sourceStage = capabilityResponse.stages.find(stage => stage.id === 'source_projection');
  const finalStage = capabilityResponse.stages.at(-1);
  const sourceProjectionAvailability = capabilityResponse.sourceProjectionAvailability;
  assert.equal(capabilityEntry.request.stageId, expandedStep.id,
    'The Builder capabilities request must bind to the exact saved RelatedExpand stage.');
  assert.equal(capabilityResponse.selectedStage.id, expandedStep.id,
    'The Builder capability response must describe the exact saved RelatedExpand stage.');
  assert.notEqual(sourceStage?.rowIdentityColumn, finalStage?.rowIdentityColumn,
    'RelatedExpand must change the current row identity before direct root-field availability is checked.');
  assert(sourceProjectionAvailability,
    'The Builder capabilities response must expose sourceProjectionAvailability for the selected stage.');
  assert.equal(sourceProjectionAvailability.stageId, expandedStep.id,
    'sourceProjectionAvailability must bind to the exact saved RelatedExpand stage.');
  assert.equal(sourceProjectionAvailability.supported, true,
    'The exact RelatedExpand stage must allow direct root-field carry-through.');
  report.target.postExpandSourceProjectionAvailability = {
    requestStageId: capabilityEntry.request.stageId,
    selectedStageId: capabilityResponse.selectedStage.id,
    sourceRowIdentityColumn: sourceStage.rowIdentityColumn,
    expandedRowIdentityColumn: finalStage.rowIdentityColumn,
    sourceProjectionAvailability,
  };
  check('correctness', 'post-expansion source projection is available for the exact RelatedExpand stage', true,
    report.target.postExpandSourceProjectionAvailability);

  await openAddColumns('open native Add columns for post-expansion Observation.status');
  const currentRowsSource = editor.locator('[data-testid="construction-add-columns-source-option"][data-source-kind="ROOT"]');
  assert.equal(await currentRowsSource.count(), 1, 'Add columns must expose one Current table rows source.');
  if (await currentRowsSource.getAttribute('aria-pressed') !== 'true') {
    await click('search post-expansion Current table rows', currentRowsSource, async () => {
      assert.equal(await currentRowsSource.getAttribute('aria-pressed'), 'true');
    });
  }
  const rawFields = editor.getByTestId('feature-catalog-raw-fields');
  if (!await rawFields.evaluate(element => element.open)) {
    await click('open advanced Observation fields after RelatedExpand', rawFields.locator(':scope > summary'), () =>
      page.waitForFunction(() => document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open === true,
        undefined, { timeout: ACTION_TIMEOUT_MS }));
  }
  const statusCandidate = editor.getByRole('checkbox', { name: 'Select Observation.status', exact: true });
  assert.equal(await statusCandidate.isEnabled(), true,
    'The post-expansion sourceProjectionAvailability result must enable Observation.status from Current table rows.');
  await click('select compiler-issued Observation.status from post-expansion rows', statusCandidate,
    () => page.waitForFunction(() => document.querySelector('input[aria-label="Select Observation.status"]')?.checked === true,
      undefined, { timeout: ACTION_TIMEOUT_MS }));
  await click('propose carried-through post-expansion Observation.status', editor.getByRole('button', { name: 'Add 1 selected feature', exact: true }),
    () => page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true }).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
  const rootFieldDialog = page.getByRole('dialog', { name: 'Choose how to add these fields', exact: true });
  const addRootColumn = rootFieldDialog.getByRole('button', { name: 'Add 1 column', exact: true });
  await click('add Observation.status after RelatedExpand', addRootColumn, () => waitForProposal(page));
  await click('Apply post-expansion Observation.status field', page.getByTestId('construction-choice-proposal-panel').getByRole('button', { name: 'Apply columns', exact: true }), async () => {
    await waitForSavedPreview(page, outputId, 3);
    const grid = await readGrid(page);
    builder = await readVisibleWorkspace(page, target, builderPath);
    document = documentFor(builder, outputId);
    const column = sourceField(document, 'status');
    assert(column?.label, 'Applied post-expansion Observation.status must have a public column label.');
    assertRowsByLabels(grid, [rootIDColumn.label, relatedIDColumn.label, column.label], [
      ['field-lifecycle-observation-matched', 'field-lifecycle-administration-a', 'final'],
      ['field-lifecycle-observation-matched', 'field-lifecycle-administration-b', 'final'],
      ['field-lifecycle-observation-unmatched', null, 'preliminary'],
    ], 'Post-expansion Observation.status Apply');
    check('correctness', 'native Observation root preview preserves exact source IDs and the carried Observation.status values', true,
      { rows: grid.rows, rootIDs, statusLabel: column.label, stageId: expandedStep.id });
  });
  builder = await readVisibleWorkspace(page, target, builderPath);
  document = documentFor(builder, outputId);
  const rootStatusColumn = sourceField(document, 'status');
  assert(rootStatusColumn?.columnId && rootStatusColumn.label, 'Applied root status field must retain its public column identity.');
  const rootStatusLabel = rootStatusColumn.label;
  const expandedBaseline = structuredClone(builder.workspace);

  const openActiveRelatedField = async () => {
    await openAddColumns('open Add columns for the active MedicationAdministration record');
    const activeSource = editor.locator('[data-testid="construction-add-columns-source-option"][data-source-kind="EXACT_RELATED"]');
    assert.equal(await activeSource.count(), 1, 'Add columns must expose the exact active related record source.');
    assert((await activeSource.innerText()).includes('MedicationAdministration — this row’s related record'),
      'The exact active source must identify the current MedicationAdministration row.');
    if (await activeSource.getAttribute('aria-pressed') !== 'true') {
      await click('choose MedicationAdministration — this row’s related record', activeSource, async () => {
        assert.equal(await activeSource.getAttribute('aria-pressed'), 'true');
      });
    }
    const relatedFieldPanel = editor.getByTestId('construction-related-field-editor');
    await relatedFieldPanel.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    const search = relatedFieldPanel.getByRole('textbox', { name: 'Find a field', exact: true });
    await fill('search MedicationAdministration fields for status', search, 'status', () =>
      relatedFieldPanel.getByRole('group', { name: 'Fields from this related record', exact: true })
        .getByRole('button', { name: /status/i }).first().waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
    const fields = relatedFieldPanel.getByRole('group', { name: 'Fields from this related record', exact: true })
      .getByRole('button');
    const statusIndexes = await fields.evaluateAll(buttons => buttons.flatMap((button, index) =>
      button.innerText.split('\n').some(line => line.split('·')[0].trim() === 'status') ? [index] : []));
    assert.equal(statusIndexes.length, 1, 'The active MedicationAdministration source must offer one exact status field.');
    const statusField = fields.nth(statusIndexes[0]);
    await click('select compiler-issued MedicationAdministration.status field', statusField, () => waitForProposal(page));
    const outputLabel = relatedFieldPanel.getByRole('textbox', { name: 'Column label', exact: true });
    if (await outputLabel.inputValue() !== 'MedicationAdministration Status') {
      await fill('give the related status output a distinct public label', outputLabel, 'MedicationAdministration Status', () => waitForProposal(page));
    }
    return statusField;
  };
  const waitActiveFieldProposal = async () => {
    await waitForProposal(page);
    const panel = await page.getByTestId('construction-proposal-panel').getAttribute('data-proposal-status');
    assert.equal(panel, 'ready');
    const grid = await readGrid(page, true);
    assertRowsByLabels(grid, [rootStatusLabel, relatedIDColumn.label, 'MedicationAdministration Status'], [
      ['final', 'field-lifecycle-administration-a', 'completed'],
      ['final', 'field-lifecycle-administration-b', 'in-progress'],
      ['preliminary', null, null],
    ], 'RELATED_FIELD proposal');
    return grid;
  };

  await openActiveRelatedField();
  const initialRelatedFieldPreview = await waitActiveFieldProposal();
  check('correctness', 'native RELATED_FIELD proposal reads MedicationAdministration.status from the active related record', true,
    { headers: initialRelatedFieldPreview.headers, rows: initialRelatedFieldPreview.rows, source: RELATED_RESOURCE + '.status' });
  const relatedFieldProposal = await readVisibleWorkspace(page, target, builderPath);
  await click('Cancel active MedicationAdministration.status preview', page.getByTestId('construction-cancel-proposal'), async () => {
    await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
      { timeout: ACTION_TIMEOUT_MS });
    const after = await readVisibleWorkspace(page, target, builderPath);
    assert.deepEqual(after.workspace, expandedBaseline, 'RELATED_FIELD Cancel must preserve the exact saved RelatedExpand workspace.');
    assert.equal(after.draftVersion, relatedFieldProposal.draftVersion);
    assert.equal(after.draftDigest, relatedFieldProposal.draftDigest);
    const grid = await readGrid(page);
    assertRowsByLabels(grid, [rootStatusLabel, relatedIDColumn.label], [
      ['final', 'field-lifecycle-administration-a'],
      ['final', 'field-lifecycle-administration-b'],
      ['preliminary', null],
    ], 'RELATED_FIELD Cancel');
  });
  check('persistence', 'RELATED_FIELD Cancel preserves the saved expansion and exact rows before Apply', true,
    { draftVersion: relatedFieldProposal.draftVersion, draftDigest: relatedFieldProposal.draftDigest });

  await openActiveRelatedField();
  await waitActiveFieldProposal();
  await click('Apply active MedicationAdministration.status field', page.getByTestId('construction-apply-proposal'), async () => {
    await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
      { timeout: ACTION_TIMEOUT_MS });
    await waitForSavedPreview(page, outputId, 3);
    builder = await readVisibleWorkspace(page, target, builderPath);
    document = documentFor(builder, outputId);
    const fieldStep = relatedFieldStep(document);
    assert(fieldStep, 'RELATED_FIELD Apply must persist a typed active-record field operation.');
    assert.equal(fieldStep.operation.relatedField.source.resourceType, RELATED_RESOURCE);
    assert.equal(fieldStep.operation.relatedField.source.path, 'status');
    assert.equal(fieldStep.inputs.at(-1)?.stepId, expandedStep.id,
      'RELATED_FIELD must bind to the exact RelatedExpand stage that defines each active MedicationAdministration record.');
    const fieldOutput = fieldStep.outputs.find(column => column.id === fieldStep.operation.relatedField.outputColumnId);
    assert(fieldOutput?.label, 'Applied RELATED_FIELD must expose its public output column.');
    const grid = await readGrid(page);
    assertRowsByLabels(grid, [rootStatusLabel, relatedIDColumn.label, fieldOutput.label], [
      ['final', 'field-lifecycle-administration-a', 'completed'],
      ['final', 'field-lifecycle-administration-b', 'in-progress'],
      ['preliminary', null, null],
    ], 'RELATED_FIELD Apply');
    report.target.relatedFieldOutput = {
      stepId: fieldStep.id,
      outputColumnId: fieldOutput.id,
      label: fieldOutput.label,
      source: fieldStep.operation.relatedField.source,
    };
  });
  builder = await readVisibleWorkspace(page, target, builderPath);
  document = documentFor(builder, outputId);
  let savedFieldStep = relatedFieldStep(document);
  assert(savedFieldStep, 'The applied active field must survive a Builder read.');
  let fieldOutput = savedFieldStep.outputs.find(column => column.id === savedFieldStep.operation.relatedField.outputColumnId);
  const originalFieldLabel = fieldOutput.label;
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  await waitForSavedPreview(page, outputId, 3);
  builder = await readVisibleWorkspace(page, target, builderPath);
  document = documentFor(builder, outputId);
  savedFieldStep = relatedFieldStep(document);
  fieldOutput = savedFieldStep?.outputs.find(column => column.id === savedFieldStep.operation.relatedField.outputColumnId);
  assert.equal(fieldOutput?.label, originalFieldLabel, 'Reload must preserve the exact related field output label.');
  assert.equal(savedFieldStep?.operation.relatedField.source.resourceType, RELATED_RESOURCE);
  assert.equal(savedFieldStep?.operation.relatedField.source.path, 'status');
  let grid = await readGrid(page);
  assertRowsByLabels(grid, [rootStatusLabel, relatedIDColumn.label, originalFieldLabel], [
    ['final', 'field-lifecycle-administration-a', 'completed'],
    ['final', 'field-lifecycle-administration-b', 'in-progress'],
    ['preliminary', null, null],
  ], 'RELATED_FIELD reload');
  check('persistence', 'RelatedExpand Apply and RELATED_FIELD reload render both MedicationAdministration rows and the unmatched Observation row', true,
    { rows: grid.rows, route: expandedStep.operation.relatedExpand.route });
  check('persistence', 'RELATED_FIELD Apply and reload preserve exact MedicationAdministration.status values and null unmatched value', true,
    { stepId: savedFieldStep.id, outputColumnId: fieldOutput.id, source: savedFieldStep.operation.relatedField.source });

  const editedFieldLabel = 'Administration Status';
  const columnsButton = page.getByRole('button', { name: 'Columns', exact: true });
  await click('open Columns to edit the active related field label', columnsButton);
  const fieldNameInput = page.getByRole('textbox', { name: `Column name for ${originalFieldLabel}`, exact: true });
  await fill('edit MedicationAdministration status public label', fieldNameInput, editedFieldLabel,
    () => page.waitForFunction(({ label, value }) => {
      const input = [...document.querySelectorAll('input[aria-label^="Column name for "]')]
        .find(element => element.getAttribute('aria-label') === `Column name for ${label}`);
      return input?.value === value;
    }, { label: originalFieldLabel, value: editedFieldLabel }, { timeout: ACTION_TIMEOUT_MS }));
  await doAction('save the edited active field label', fieldNameInput,
    () => fieldNameInput.press('Enter', { timeout: ACTION_TIMEOUT_MS }),
    async () => {
      await waitForSavedPreview(page, outputId, 3);
      builder = await readVisibleWorkspace(page, target, builderPath);
      document = documentFor(builder, outputId);
      const fieldStep = relatedFieldStep(document);
      const output = fieldStep?.outputs.find(column => column.id === fieldStep.operation.relatedField.outputColumnId);
      assert.equal(output?.label, editedFieldLabel, 'Edited output label must persist on its RELATED_FIELD operation.');
      grid = await readGrid(page);
      assertRowsByLabels(grid, [rootStatusLabel, relatedIDColumn.label, editedFieldLabel], [
        ['final', 'field-lifecycle-administration-a', 'completed'],
        ['final', 'field-lifecycle-administration-b', 'in-progress'],
        ['preliminary', null, null],
      ], 'RELATED_FIELD label edit');
    });
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  await waitForSavedPreview(page, outputId, 3);
  builder = await readVisibleWorkspace(page, target, builderPath);
  document = documentFor(builder, outputId);
  savedFieldStep = relatedFieldStep(document);
  fieldOutput = savedFieldStep?.outputs.find(column => column.id === savedFieldStep.operation.relatedField.outputColumnId);
  assert.equal(fieldOutput?.label, editedFieldLabel, 'Reload must preserve the edited public field label.');
  grid = await readGrid(page);
  assertRowsByLabels(grid, [rootStatusLabel, relatedIDColumn.label, editedFieldLabel], [
    ['final', 'field-lifecycle-administration-a', 'completed'],
    ['final', 'field-lifecycle-administration-b', 'in-progress'],
    ['preliminary', null, null],
  ], 'edited RELATED_FIELD reload');
  check('persistence', 'editing the related field label preserves its source binding and exact expanded rows after reload', true,
    { field: fieldOutput, source: savedFieldStep.operation.relatedField.source, rows: grid.rows });

  await click('open Columns to remove the active related field', page.getByRole('button', { name: 'Columns', exact: true }));
  await click('remove MedicationAdministration.status column', page.getByRole('button', { name: `Remove ${editedFieldLabel} column`, exact: true }), async () => {
    await waitForSavedPreview(page, outputId, 3);
    builder = await readVisibleWorkspace(page, target, builderPath);
    document = documentFor(builder, outputId);
    assert.equal(relatedFieldStep(document), undefined, 'Removing the active field must remove its RELATED_FIELD construction.');
    assert(expandStep(document), 'Removing RELATED_FIELD must preserve RelatedExpand.');
    grid = await readGrid(page);
    assertRowsByLabels(grid, [rootStatusLabel, relatedIDColumn.label], [
      ['final', 'field-lifecycle-administration-a'],
      ['final', 'field-lifecycle-administration-b'],
      ['preliminary', null],
    ], 'RELATED_FIELD removal');
  });
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  await waitForSavedPreview(page, outputId, 3);
  builder = await readVisibleWorkspace(page, target, builderPath);
  document = documentFor(builder, outputId);
  assert.equal(relatedFieldStep(document), undefined, 'Reload after removal must keep the related field removed.');
  assert(expandStep(document), 'Reload after removal must keep the RelatedExpand route.');
  grid = await readGrid(page);
  assertRowsByLabels(grid, [rootStatusLabel, relatedIDColumn.label], [
    ['final', 'field-lifecycle-administration-a'],
    ['final', 'field-lifecycle-administration-b'],
    ['preliminary', null],
  ], 'RELATED_FIELD removal reload');
  check('persistence', 'removing the related field restores exact expanded rows and source construction after reload', true,
    { constructionSteps: document.construction.steps.map(step => step.operation.kind), rows: grid.rows });

  const openGroupEditor = async () => {
    await click('open row settings for public Observation status Group', page.getByTestId('construction-rows-settings-trigger'),
      () => page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
    await click('open Group by public Observation status', page.getByTestId('construction-action-group-rows'),
      () => page.getByTestId('construction-reshape-editor').waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS }));
    const key = page.getByRole('checkbox', { name: `Group by ${rootStatusLabel}`, exact: true });
    if (!await key.isChecked()) {
      await click('group by public Observation.status', key, () => waitForProposal(page));
    } else {
      await waitForProposal(page);
    }
    const proposalGrid = await readGrid(page, true);
    assertRowsByLabels(proposalGrid, [rootStatusLabel, 'Row count'], [
      ['final', 2],
      ['preliminary', 1],
    ], 'Group by Observation.status proposal');
    const operation = await page.getByTestId('construction-proposal-panel').getAttribute('data-proposal-status');
    assert.equal(operation, 'ready');
    return proposalGrid;
  };

  await openGroupEditor();
  const groupBaseline = await readVisibleWorkspace(page, target, builderPath);
  await click('Cancel public Observation status Group preview', page.getByTestId('construction-cancel-proposal'), async () => {
    await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
      { timeout: ACTION_TIMEOUT_MS });
    const after = await readVisibleWorkspace(page, target, builderPath);
    assert.deepEqual(after.workspace, groupBaseline.workspace, 'Group Cancel must preserve the exact RelatedExpand source workspace.');
    grid = await readGrid(page);
    assertRowsByLabels(grid, [rootStatusLabel, relatedIDColumn.label], [
      ['final', 'field-lifecycle-administration-a'],
      ['final', 'field-lifecycle-administration-b'],
      ['preliminary', null],
    ], 'Group Cancel');
  });
  await openGroupEditor();
  await click('Apply Group by public Observation.status', page.getByTestId('construction-apply-proposal'), async () => {
    await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined,
      { timeout: ACTION_TIMEOUT_MS });
    await waitForSavedPreview(page, outputId, 2);
    grid = await readGrid(page);
    assertRowsByLabels(grid, [rootStatusLabel, 'Row count'], [
      ['final', 2],
      ['preliminary', 1],
    ], 'Group Apply');
  });
  builder = await readVisibleWorkspace(page, target, builderPath);
  document = documentFor(builder, outputId);
  const groupStep = document.construction.steps.find(step => step.operation?.kind === 'GROUP');
  assert(groupStep, 'Group Apply must persist a GROUP operation.');
  assert(groupStep.operation.group.keys.some(key => key.inputColumnId === rootStatusColumn.columnId),
    'Saved GROUP must bind to the public carried Observation.status column identity.');
  assert(groupStep.operation.group.aggregates.some(aggregate => aggregate.operation === 'COUNT_ROWS'),
    'Saved GROUP must retain the row count aggregate.');
  check('persistence', 'native Group binds the public Observation.status column and returns final count 2 and preliminary count 1', true,
    { groupStepId: groupStep.id, groupKeys: groupStep.operation.group.keys, aggregates: groupStep.operation.group.aggregates });
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  await waitForSavedPreview(page, outputId, 2);
  builder = await readVisibleWorkspace(page, target, builderPath);
  document = documentFor(builder, outputId);
  const reloadedGroup = document.construction.steps.find(step => step.id === groupStep.id);
  assert.deepEqual(reloadedGroup.operation.group.keys, groupStep.operation.group.keys);
  assert.deepEqual(reloadedGroup.operation.group.aggregates, groupStep.operation.group.aggregates);
  grid = await readGrid(page);
  assertRowsByLabels(grid, [rootStatusLabel, 'Row count'], [
    ['final', 2],
    ['preliminary', 1],
  ], 'Group reload');
  check('persistence', 'public Observation.status Group retains exact keys and multiplicity after reload', true,
    { groupStepId: reloadedGroup.id, groupKeys: reloadedGroup.operation.group.keys, groups: oracle.groups });
};
