import assert from 'node:assert/strict';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { previewHeaderIndex } from '../helpers/playwright-preview-rows.mjs';
import {
  BASIC_UNPIVOT_PIVOT_EXPECTED,
  readBasicUnpivotPivotFixture,
  expectedBasicUnpivotPivotRows,
} from '../helpers/basic-unpivot-pivot-fixture-oracle.mjs';

const ACTION_TIMEOUT_MS = 5000;
const panelSelector = '[data-testid="construction-proposal-panel"], [data-testid="construction-choice-proposal-panel"]';
const normalizeVisible = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const visibleValue = value => value === null || value === undefined ? '—' : normalizeVisible(value);

const waitFor = (page, predicate, value, timeout = ACTION_TIMEOUT_MS) =>
  page.waitForFunction(({ predicateSource, argument }) =>
    Boolean(Function('argument', `return (${predicateSource})(argument);`)(argument)),
  { predicateSource: predicate.toString(), argument: value }, { timeout: Math.min(timeout, ACTION_TIMEOUT_MS) });

const previewFromPayload = payload => payload?.preview ?? payload;
const panelData = page => page.locator(panelSelector).evaluate(panel => ({
  testId: panel.dataset.testid,
  status: panel.dataset.proposalStatus,
  proposalId: panel.dataset.proposalId,
}));

export const assertBasicUnpivotPivotContext = ({ scenarioID, caseName, report, target, seed }) => {
  assert.equal(scenarioID, 'basic-unpivot-pivot-roundtrip', 'The native fixture context must select the U→P scenario.');
  assert.equal(caseName, 'unpivot-to-pivot', 'The native fixture context must select the U→P case.');
  assert.equal(report.scenario, scenarioID);
  assert.equal(report.case, caseName, 'The report contract stores its case name in `case`.');
  assert.equal(seed?.fresh, true, 'This contract requires a fresh isolated Basic fixture project.');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_'), 'Expected an isolated loom_dev_verify project.');
  assert(target.fixtureGeneration && target.fixtureDir, 'The fixture project, generation, and directory must be explicit.');
  assert.equal(report.target.project, target.fixtureProject, 'The report must identify the exact fixture project.');
  assert.equal(report.target.generation, target.fixtureGeneration, 'The report must identify the exact fixture generation.');
};

const assertPreviewRows = (payload, expectedRows, expectedLabels, label) => {
  const preview = previewFromPayload(payload);
  assert(preview && Array.isArray(preview.columns) && Array.isArray(preview.rows),
    `${label}: preview must contain typed columns and rows`);
  const previewStatus = preview.previewStatus ?? payload.previewStatus;
  if (previewStatus !== undefined) assert.equal(previewStatus, 'READY', `${label}: preview must be ready`);
  else assert(preview.receiptId, `${label}: a saved preview receipt is required`);
  const labels = preview.columns.map(column => column.label);
  assert.equal(new Set(labels).size, labels.length, `${label}: column labels must be unique`);
  assert.deepEqual([...labels].sort(), [...expectedLabels].sort(), `${label}: exact visible columns differ from the independent oracle`);
  assert.equal(preview.rowCount, expectedRows.length, `${label}: typed row count differs from the independent oracle`);
  assert.equal(preview.rows.length, expectedRows.length, `${label}: typed rows differ in multiplicity`);
  const actualRows = preview.rows.map(row => preview.columns.map(column => row[column.column]));
  const expectedMatrices = expectedRows.map(expected => preview.columns.map(column => expected[column.label]));
  assert.deepEqual(actualRows.map(row => JSON.stringify(row)).sort(),
    expectedMatrices.map(row => JSON.stringify(row)).sort(),
    `${label}: typed values or duplicate rows differ from the literal fixture oracle`);
  return preview;
};

export async function basicUnpivotPivotWorkflow({ page, action, check, networkObserver, report, target, seed, scenarioID, caseName }) {
  configureNativePage(page);
  assertBasicUnpivotPivotContext({ scenarioID, caseName, report, target, seed });

  const fixture = readBasicUnpivotPivotFixture(target.fixtureDir);
  const expected = expectedBasicUnpivotPivotRows(fixture.patients);
  const sourceLabels = BASIC_UNPIVOT_PIVOT_EXPECTED.sourceColumnLabels;
  report.target.fixtureSeedManifest = fixture.manifest;
  report.target.fixtureOracle = {
    kind: 'independent literal Basic Patient source oracle',
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    sourceColumnLabels: sourceLabels,
    patients: fixture.patients,
    expectedInitialRows: expected.initialRows,
    expectedUnpivotRows: expected.unpivotRows,
    expectedPivotRows: expected.pivotRows,
  };
  check('correctness', 'Basic fixture has exactly two literal Patients and an empty Observation source',
    fixture.patients.length === 2 && fixture.manifest.find(file => file.name === 'Observation.ndjson')?.sizeBytes === 0,
    report.target.fixtureSeedManifest);
  check('correctness', 'literal Patient source pins both IDs, genders, and dates independently of preview responses',
    fixture.patients.map(patient => patient.id).join(',') === 'reshape-patient-001,reshape-patient-002',
    report.target.fixtureOracle);

  const pageAction = (label, locator, perform, after) => action(label, locator, perform, {
    timeout: ACTION_TIMEOUT_MS,
    budget: ACTION_TIMEOUT_MS,
    ...(after ? { after } : {}),
  });
  const click = (label, locator, after) => pageAction(label, locator,
    () => locator.click({ timeout: ACTION_TIMEOUT_MS }), after);
  const fill = (label, locator, value, after) => pageAction(label, locator,
    () => locator.fill(value, { timeout: ACTION_TIMEOUT_MS }), after);
  const select = (label, locator, value, after) => pageAction(label, locator,
    () => locator.selectOption(value, { timeout: ACTION_TIMEOUT_MS }), after);
  const checkControl = (label, locator, after) => pageAction(label, locator,
    () => locator.check({ timeout: ACTION_TIMEOUT_MS }), after);
  const waitVisible = locator => locator.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const waitHidden = locator => locator.waitFor({ state: 'hidden', timeout: ACTION_TIMEOUT_MS });

  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  await waitFor(page, () => document.body.innerText.includes('Build your first table') ||
    document.body.innerText.includes('Dataset graph'), undefined);

  const newExplorerSummary = page.locator('summary').filter({ hasText: /^New explorer$/ });
  await click('open fresh Explorer creation', newExplorerSummary,
    () => waitVisible(page.locator('#new-explorer-name')));
  const explorerTitle = `Verify ${report.target.project.slice(-10)} Basic Unpivot then Pivot`;
  await fill('name fresh Basic Unpivot→Pivot Explorer', page.locator('#new-explorer-name'), explorerTitle);
  await click('create fresh Basic Unpivot→Pivot Explorer', page.getByRole('button', { name: 'Create blank', exact: true }),
    () => waitFor(page, title => document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === title &&
      document.body.innerText.includes('Build your first table'), explorerTitle));
  const explorer = await page.locator('select[aria-label="Explorer"]').inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'The workflow must use a fresh non-bootstrap Explorer.');
  report.target.explorer = explorer;

  const firstTableName = 'Patient Unpivot Pivot roundtrip';
  await fill('name Patient root table', page.locator('#first-table-name'), firstTableName);
  await click('create native Patient root table', page.getByRole('button', { name: 'Choose Patient rows', exact: true }),
    () => waitFor(page, title => document.body.innerText.includes('DATASET WORKSPACE') &&
      document.body.innerText.includes(title) && !document.body.innerText.includes('Build your first table'), firstTableName));

  const base = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
  report.target.verificationReads = [];
  const readJSON = async (path, body) => {
    const response = await fetch(`${target.apiUrl}${base}${path}`, {
      method: body === undefined ? 'GET' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.timeout(30000),
    });
    const value = await response.json();
    report.target.verificationReads.push({ method: body === undefined ? 'GET' : 'POST', path, status: response.status });
    assert(response.ok, `Read-only Builder verification ${path} failed with HTTP ${response.status}: ${JSON.stringify(value).slice(0, 800)}`);
    return value;
  };
  const readBuilder = () => readJSON('/builder');
  let builder = await readBuilder();
  assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder must read the exact fixture generation.');
  let document = builder.workspace.documents.find(item => item.rootResourceType === 'Patient');
  assert(document?.output?.id, 'Native first table must use Patient as its row root.');
  const outputId = document.output.id;
  report.target.outputId = outputId;
  const documentFor = state => state.workspace.documents.find(item => item.output.id === outputId);
  const stepsFor = state => documentFor(state)?.construction?.steps ?? [];
  const stepFor = (state, kind) => stepsFor(state).find(step => step.operation.kind === kind);
  const sourcePath = column => column.source?.field?.path ?? column.source?.path ?? '';
  const sourceColumnFor = (state, path) => documentFor(state)?.columns.find(column => sourcePath(column) === path);
  const fieldNameFor = (step, columnId) => step.outputs.find(column => column.id === columnId)?.name;
  const rowForLabels = (patient, labels) => Object.fromEntries(labels.map(label => {
    if (label === sourceLabels.patientID) return [label, patient.id];
    if (label === sourceLabels.gender || label === 'Sex') return [label, patient.gender];
    if (label === sourceLabels.birthDate || label === 'Birth date') return [label, patient.birthDate];
    if (label === 'Field') return [label, ''];
    if (label === 'Value') return [label, ''];
    throw new Error(`No independent source mapping for preview column ${label}`);
  }));
  const initialExpected = () => fixture.patients.map(patient => rowForLabels(patient, ['Patient ID', 'Gender', 'Birth date']));
  const unpivotExpected = () => fixture.patients.flatMap(patient => [
    { 'Patient ID': patient.id, Field: 'gender', Value: patient.gender },
    { 'Patient ID': patient.id, Field: 'birthDate', Value: patient.birthDate },
  ]);
  const pivotExpected = sexLabel => fixture.patients.map(patient => ({
    'Patient ID': patient.id,
    [sexLabel]: patient.gender,
    'Birth date': patient.birthDate,
  }));
  const expectStateFields = state => {
    const fields = documentFor(state)?.columns ?? [];
    for (const path of ['id', 'gender', 'birthDate']) {
      assert.equal(fields.filter(column => sourcePath(column) === path).length, 1,
        `Patient.${path} must remain a direct source column`);
    }
    assert.equal(fields.length, 3, 'The exact Patient source projection must contain ID, gender, and birthDate.');
    return fields;
  };

  const assertVisibleRows = async (label, expectedRows, expectedLabels) => {
    await waitFor(page, output => {
      const node = document.querySelector('[data-testid="construction-preview"]');
      return node?.dataset.previewStatus === 'ready' && node.dataset.previewOutputId === output &&
        Boolean(node.dataset.previewReceiptId);
    }, outputId);
    const visible = await page.locator('[data-testid="preview-table-scroll"] [role="table"]').evaluate(table => ({
      headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => String(cell.innerText ?? '').replace(/\s+/g, ' ').trim()),
      rows: [...table.querySelectorAll('[role="row"]')].slice(1)
        .filter(row => row.querySelector('[role="cell"]'))
        .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => String(cell.innerText ?? '').replace(/\s+/g, ' ').trim())),
    }));
    assert.equal(visible.headers.length, expectedLabels.length,
      `${label}: visible header count differs from the independent oracle`);
    const expectedLabelsByIndex = new Map(expectedLabels.map(expectedLabel => [
      previewHeaderIndex(visible.headers, expectedLabel), expectedLabel,
    ]));
    assert.equal(expectedLabelsByIndex.size, expectedLabels.length,
      `${label}: each exact independent header must map to a distinct rendered column`);
    const expectedMatrices = expectedRows.map(row => visible.headers.map((_, index) => {
      const expectedLabel = expectedLabelsByIndex.get(index);
      assert(expectedLabel, `${label}: rendered column ${index} has no exact independent label mapping`);
      return visibleValue(row[expectedLabel]);
    }));
    assert.deepEqual(visible.rows.map(row => JSON.stringify(row)).sort(),
      expectedMatrices.map(row => JSON.stringify(row)).sort(),
      `${label}: rendered values or row multiplicity differ from the independent oracle`);
    return visible;
  };
  const readReceiptPreview = async (label, expectedRows, expectedLabels) => {
    const previewNode = page.getByTestId('construction-preview');
    await waitFor(page, output => {
      const node = document.querySelector('[data-testid="construction-preview"]');
      return node?.dataset.previewStatus === 'ready' && node.dataset.previewOutputId === output &&
        Boolean(node.dataset.previewReceiptId);
    }, outputId);
    const receiptId = await previewNode.getAttribute('data-preview-receipt-id');
    assert(receiptId, `${label}: saved preview receipt is missing.`);
    const preview = await readJSON('/preview', { receiptId, outputId, limit: 100 });
    assert.equal(preview.receiptId, receiptId);
    assert.equal(preview.outputId, outputId);
    assertPreviewRows(preview, expectedRows, expectedLabels, label);
    const visible = await assertVisibleRows(label, expectedRows, expectedLabels);
    report.target.verificationReads.at(-1).visible = { headers: visible.headers, rows: visible.rows };
    return { builder: await readBuilder(), preview };
  };
  const reloadAndRead = async (label, expectedRows, expectedLabels) => {
    builder = await readBuilder();
    const savedDraft = { digest: builder.draftDigest, version: String(builder.draftVersion) };
    assert(savedDraft.digest && savedDraft.version, `${label}: current draft identity is required before reload.`);
    const tableLocator = page.getByTestId(`construction-table-${outputId}`);
    const closeEditor = page.getByTestId('construction-close-operation-editor');
    if (await closeEditor.isVisible()) {
      await click(`close the native operation editor before ${label}`, closeEditor,
        () => waitVisible(tableLocator));
    }
    await pageAction(`reload Builder for ${label}`, tableLocator, async () => {
      await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
      await tableLocator.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
      if (await tableLocator.getAttribute('aria-current') !== 'page') await tableLocator.click({ timeout: ACTION_TIMEOUT_MS });
    }, async () => {
      await waitFor(page, expected => {
        const selected = document.querySelector(`[data-testid="construction-table-${expected.outputId}"][aria-current="page"]`);
        const preview = document.querySelector('[data-testid="construction-preview"]');
        return Boolean(selected && preview?.dataset.previewStatus === 'ready' &&
          preview.dataset.previewOutputId === expected.outputId &&
          preview.dataset.currentDraftDigest === expected.digest &&
          preview.dataset.currentDraftVersion === expected.version && preview.dataset.previewReceiptId);
      }, { outputId, ...savedDraft });
      await assertVisibleRows(label, expectedRows, expectedLabels);
    });
    return readReceiptPreview(label, expectedRows, expectedLabels);
  };
  const awaitProposal = async (fromIndex, label, { kind = 'construction' } = {}) => {
    const isChoice = kind === 'choice';
    const responsePath = isChoice ? '/construction-choice-proposals' : '/construction-proposals';
    const entry = await networkObserver.waitForResponse(candidate =>
      candidate.index >= fromIndex && candidate.status !== undefined && candidate.response &&
      candidate.path.endsWith(responsePath) &&
      candidate.request?.outputId === outputId,
    { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
    assert.equal(entry.status, 200, `${label}: native proposal must return HTTP 200.`);
    assert.equal(entry.response.outputId, outputId, `${label}: response must belong to the exact output.`);
    assert.equal(entry.response.previewStatus, 'READY', `${label}: proposal preview must be ready.`);
    const responseId = isChoice ? entry.response.commandId : entry.response.proposalId;
    assert(responseId, `${label}: completed proposal response must carry its exact identity.`);
    if (isChoice) assert.equal(entry.request.commandId, responseId, `${label}: choice response must match the exact command request.`);
    assert(Array.isArray(entry.response.preview?.rows), `${label}: completed proposal must contain typed preview rows.`);
    assert.equal(entry.response.preview.outputId, outputId, `${label}: preview must belong to the exact output.`);
    const receiptId = entry.response.preview.receiptId;
    assert(receiptId, `${label}: completed proposal preview must carry its receipt identity.`);
    if (!isChoice) assert.equal(receiptId, responseId, `${label}: preview must use the exact proposal receipt.`);
    await waitFor(page, expected => {
      const panel = document.querySelector(expected.isChoice
        ? '[data-testid="construction-choice-proposal-panel"]'
        : '[data-testid="construction-proposal-panel"]');
      const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
      return panel?.dataset.proposalStatus === 'ready' &&
        (expected.isChoice || panel.dataset.proposalId === expected.responseId) &&
        preview?.dataset.previewStatus === 'ready' &&
        preview.dataset.previewReceiptId === expected.receiptId &&
        preview.dataset.previewOutputId === expected.outputId &&
        Boolean(preview.querySelector('table')) &&
        preview.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length === expected.rowCount;
    }, { isChoice, responseId, receiptId, outputId, rowCount: entry.response.preview.rows.length });
    const panel = await panelData(page);
    assert.equal(panel.status, 'ready', `${label}: proposal status is ${panel.status}`);
    if (!isChoice) assert.equal(panel.proposalId, responseId, `${label}: ready panel must match its completed response.`);
    report.target.verificationReads.push({ method: entry.method, path: entry.path, status: entry.status, browserRequestId: entry.browserRequestId });
    return entry.response;
  };
  const cancelProposal = async (label, savedWorkspace) => {
    const panel = await panelData(page);
    const cancel = panel.testId === 'construction-choice-proposal-panel'
      ? page.getByTestId('construction-choice-proposal-panel').getByRole('button', { name: 'Cancel', exact: true })
      : page.getByTestId('construction-cancel-proposal');
    await click(`${label}: Cancel proposal`, cancel, () => waitHidden(page.locator(panelSelector)));
    builder = await readBuilder();
    assert.deepEqual(builder.workspace, savedWorkspace, `${label}: Cancel must preserve the exact saved workspace.`);
    return builder;
  };
  const applyProposal = async (label, expectedRows, expectedLabels) => {
    const panel = await panelData(page);
    const previousDraft = { digest: builder.draftDigest, version: Number(builder.draftVersion) };
    assert(previousDraft.digest && Number.isFinite(previousDraft.version), `${label}: saved draft identity is required before Apply.`);
    const apply = panel.testId === 'construction-choice-proposal-panel'
      ? page.getByTestId('construction-choice-proposal-panel').getByRole('button', { name: 'Apply columns', exact: true })
      : page.getByTestId('construction-apply-proposal');
    await click(`${label}: Apply proposal`, apply, async () => {
      await waitFor(page, () => !document.querySelector('[data-testid="construction-proposal-panel"]') &&
        !document.querySelector('[data-testid="construction-choice-proposal-panel"]'), undefined);
      await waitFor(page, expectedState => {
        const preview = document.querySelector('[data-testid="construction-preview"]');
        return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === expectedState.outputId &&
          preview.dataset.previewReceiptId &&
          (preview.dataset.currentDraftDigest !== expectedState.digest || Number(preview.dataset.currentDraftVersion) > expectedState.version);
      }, { outputId, ...previousDraft });
      await assertVisibleRows(label, expectedRows, expectedLabels);
    });
    builder = await readBuilder();
    const receipt = await readReceiptPreview(label, expectedRows, expectedLabels);
    builder = receipt.builder;
    return { builder, preview: receipt.preview };
  };
  const startProposal = () => networkObserver.requests.length;

  const addEditor = page.locator('[aria-label="Add columns editor"]');
  await waitFor(page, () => Boolean(document.querySelector('[data-testid="construction-action-add-columns"]') &&
    !document.querySelector('[data-testid="construction-action-add-columns"]').disabled), undefined);
  await click('open native Add columns', page.getByTestId('construction-action-add-columns'), () => waitVisible(addEditor));
  const familyButton = page.locator('[aria-label="Column types"]').getByRole('button', { name: 'Fields and related data', exact: true });
  await click('choose native Fields and related data', familyButton, () => waitVisible(page.getByTestId('construction-add-columns-source')));
  const rawFields = page.getByTestId('feature-catalog-raw-fields');
  await waitVisible(rawFields);
  if (!await rawFields.evaluate(element => element.open)) {
    await click('open raw Patient field list', rawFields.locator(':scope > summary'),
      () => waitFor(page, () => document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open === true, undefined));
  }
  for (const fieldPath of ['gender', 'birthDate']) {
    const checkbox = page.getByRole('checkbox', { name: `Select Patient.${fieldPath}`, exact: true });
    await waitVisible(checkbox);
    await checkControl(`select native Patient.${fieldPath}`, checkbox,
      () => waitFor(page, path => document.querySelector(`input[aria-label=${JSON.stringify(`Select Patient.${path}`)}]`)?.checked === true, fieldPath));
  }
  const addFields = page.getByRole('button', { name: 'Add 2 selected features', exact: true });
  const addFrom = startProposal();
  let addProposal;
  await click('propose exact Patient.gender and Patient.birthDate columns', addFields,
    async () => {
      await waitVisible(page.locator(panelSelector));
      addProposal = await awaitProposal(addFrom, 'Add Patient gender and birthDate', { kind: 'choice' });
    });
  const labels = addProposal.preview.columns.map(column => column.label);
  assert.deepEqual([...labels].sort(), [sourceLabels.birthDate, sourceLabels.gender, sourceLabels.patientID].sort());
  const labelRows = fixture.patients.map(patient => ({
    [sourceLabels.patientID]: patient.id,
    [sourceLabels.gender]: patient.gender,
    [sourceLabels.birthDate]: patient.birthDate,
  }));
  assertPreviewRows(addProposal, labelRows, [sourceLabels.patientID, sourceLabels.gender, sourceLabels.birthDate], 'Patient source field proposal');
  const workspaceBeforeCancel = structuredClone(builder.workspace);
  await cancelProposal('cancel Patient field proposal', workspaceBeforeCancel);
  check('correctness', 'Cancel leaves the exact original Patient source workspace unchanged', true,
    { sourceFields: document.columns.map(column => sourcePath(column)) });

  for (const fieldPath of ['gender', 'birthDate']) {
    const checkbox = page.getByRole('checkbox', { name: `Select Patient.${fieldPath}`, exact: true });
    await waitVisible(checkbox);
    await checkControl(`reselect native Patient.${fieldPath} after Cancel`, checkbox,
      () => waitFor(page, path => document.querySelector(`input[aria-label=${JSON.stringify(`Select Patient.${path}`)}]`)?.checked === true, fieldPath));
  }
  const addAgain = page.getByRole('button', { name: 'Add 2 selected features', exact: true });
  const addAgainFrom = startProposal();
  let confirmedFields;
  await click('reopen exact Patient field proposal after Cancel', addAgain, async () => {
    await waitVisible(page.locator(panelSelector));
    confirmedFields = await awaitProposal(addAgainFrom, 'Confirm Patient gender and birthDate', { kind: 'choice' });
  });
  assertPreviewRows(confirmedFields, labelRows, [sourceLabels.patientID, sourceLabels.gender, sourceLabels.birthDate], 'confirmed Patient source field proposal');
  builder = await readBuilder();
  await applyProposal('apply exact Patient fields', labelRows, [sourceLabels.patientID, sourceLabels.gender, sourceLabels.birthDate]);
  builder = await readBuilder();
  document = documentFor(builder);
  const sourceColumns = expectStateFields(builder);
  const idColumn = sourceColumnFor(builder, 'id');
  const genderColumn = sourceColumnFor(builder, 'gender');
  const birthDateColumn = sourceColumnFor(builder, 'birthDate');
  assert(idColumn?.columnId && genderColumn?.columnId && birthDateColumn?.columnId,
    'Applied source projection must retain stable IDs for Patient.id, gender, and birthDate.');
  check('correctness', 'native Patient source projection contains exact ID, gender, and birthDate columns', true,
    { columns: sourceColumns.map(column => ({ path: sourcePath(column), name: column.column, label: column.label })) });
  let restored = await reloadAndRead('reloaded Patient source projection', labelRows, [sourceLabels.patientID, sourceLabels.gender, sourceLabels.birthDate]);
  builder = restored.builder;
  document = documentFor(builder);
  expectStateFields(builder);
  check('persistence', 'Patient source values and exact two-row identity survive reload before reshape', true,
    { rows: restored.preview.rows });

  const chooseSelectedColumn = async (label, column, sourceColumnLabel) => {
    const selection = page.getByTestId(`construction-column-${column.columnId}`);
    assert.equal(await selection.count(), 1, `Native column selection for ${sourceColumnLabel} must be unique.`);
    await click(`select ${sourceColumnLabel} for native Unpivot`, selection,
      () => waitFor(page, expected => document.querySelector(`[data-testid="construction-column-${expected.columnId}"]`)?.getAttribute('aria-pressed') === 'true', { columnId: column.columnId }));
  };
  await chooseSelectedColumn('Unpivot', genderColumn, 'Patient.gender');
  await chooseSelectedColumn('Unpivot', birthDateColumn, 'Patient.birthDate');
  const selection = page.getByTestId('construction-column-selection');
  assert.equal(await selection.getByTestId(`construction-column-${genderColumn.columnId}`).getAttribute('aria-pressed'), 'true');
  assert.equal(await selection.getByTestId(`construction-column-${birthDateColumn.columnId}`).getAttribute('aria-pressed'), 'true');

  await click('open native row settings for Unpivot', page.getByTestId('construction-rows-settings-trigger'),
    () => waitVisible(page.getByTestId('construction-action-unpivot-rows')));
  const unpivotChoice = page.getByTestId('construction-action-unpivot-rows');
  assert.equal(await unpivotChoice.isEnabled(), true, 'Unpivot must be enabled for the selected scalar source columns.');
  const initialUnpivotFrom = startProposal();
  let initialUnpivotProposal;
  await click('choose native Unpivot rows', unpivotChoice,
    async () => {
      await waitVisible(page.getByTestId('construction-reshape-unpivot'));
      initialUnpivotProposal = await awaitProposal(initialUnpivotFrom, 'Initial native Unpivot proposal');
    });
  assert(initialUnpivotProposal, 'Choosing Unpivot must return its completed initial proposal inside the action budget.');
  const unpivotEditor = page.getByTestId('construction-reshape-unpivot');
  for (const column of [genderColumn, birthDateColumn]) {
    const input = unpivotEditor.locator(`input[aria-label=${JSON.stringify(`Unpivot ${column.label}`)}]`);
    assert.equal(await input.isChecked(), true, `Unpivot must be preconfigured for ${column.label}.`);
  }
  const advanced = page.getByTestId('construction-unpivot-advanced');
  await click('open Unpivot field keys and missing-value controls', advanced.locator(':scope > summary'),
    () => waitFor(page, () => document.querySelector('[data-testid="construction-unpivot-advanced"]')?.open === true, undefined));
  let unpivotPayload;
  for (const [column, value] of [[genderColumn, 'gender'], [birthDateColumn, 'birthDate']]) {
    const key = page.locator(`input[aria-label=${JSON.stringify(`Unpivot key value ${column.columnId}`)}]`);
    const fromIndex = startProposal();
    await fill(`set literal Unpivot key ${value}`, key, value,
      async () => {
        await waitFor(page, expected => document.querySelector(`input[aria-label=${JSON.stringify(`Unpivot key value ${expected.columnId}`)}]`)?.value === expected.value, { columnId: column.columnId, value });
        unpivotPayload = await awaitProposal(fromIndex, `Configure Unpivot key ${value}`);
      });
  }
  const keyLabel = page.getByLabel('Unpivot key output label', { exact: true });
  if (await keyLabel.inputValue() !== 'Field') {
    const fromIndex = startProposal();
    await fill('label Unpivot category column Field', keyLabel, 'Field',
      async () => {
        await waitFor(page, () => document.querySelector('input[aria-label="Unpivot key output label"]')?.value === 'Field', undefined);
        unpivotPayload = await awaitProposal(fromIndex, 'Label Unpivot key output Field');
      });
  }
  const valueLabel = page.getByLabel('Unpivot value output label', { exact: true });
  if (await valueLabel.inputValue() !== 'Value') {
    const fromIndex = startProposal();
    await fill('label Unpivot value column Value', valueLabel, 'Value',
      async () => {
        await waitFor(page, () => document.querySelector('input[aria-label="Unpivot value output label"]')?.value === 'Value', undefined);
        unpivotPayload = await awaitProposal(fromIndex, 'Label Unpivot value output Value');
      });
  }
  assert(unpivotPayload, 'The final Unpivot configuration action must return its completed proposal.');
  assertPreviewRows(unpivotPayload, expected.unpivotRows.map(row => ({
    'Patient ID': row.patient_id,
    Field: row.field,
    Value: row.value,
  })), ['Patient ID', 'Field', 'Value'], 'Unpivot proposal');
  builder = await readBuilder();
  await applyProposal('apply Unpivot Patient gender and birthDate', expected.unpivotRows.map(row => ({
    'Patient ID': row.patient_id,
    Field: row.field,
    Value: row.value,
  })), ['Patient ID', 'Field', 'Value']);
  builder = await readBuilder();
  let unpivotStep = stepFor(builder, 'UNPIVOT');
  assert(unpivotStep, 'Native Unpivot Apply must save a UNPIVOT construction step.');
  const unpivotOperation = unpivotStep.operation.unpivot;
  assert.deepEqual(unpivotOperation.inputs.map(input => ({
    path: document.columns.find(column => column.columnId === input.columnId)?.source?.field?.path,
    key: input.key.string,
  })).sort((left, right) => left.path.localeCompare(right.path)), [
    { path: 'birthDate', key: 'birthDate' },
    { path: 'gender', key: 'gender' },
  ]);
  assert.equal(unpivotOperation.nullRowPolicy, 'PRESERVE');
  const unpivotKeyName = fieldNameFor(unpivotStep, unpivotOperation.keyOutputColumnId);
  const unpivotValueName = fieldNameFor(unpivotStep, unpivotOperation.valueOutputColumnId);
  assert(unpivotKeyName && unpivotValueName, 'Saved Unpivot must expose its exact key and value output columns.');
  check('correctness', 'native Unpivot saves exactly the two source paths and literal field-name keys', true,
    { stepId: unpivotStep.id, inputs: unpivotOperation.inputs, keyOutput: unpivotKeyName, valueOutput: unpivotValueName });
  restored = await reloadAndRead('reloaded Unpivot source rows', expected.unpivotRows.map(row => ({
    'Patient ID': row.patient_id, Field: row.field, Value: row.value,
  })), ['Patient ID', 'Field', 'Value']);
  builder = restored.builder;
  unpivotStep = stepFor(builder, 'UNPIVOT');
  assert(unpivotStep, 'Unpivot step must survive reload.');
  check('persistence', 'Unpivot step and exact four literal Patient rows survive reload', true,
    { stepId: unpivotStep.id, rows: restored.preview.rows });

  await click('open row settings on the Unpivot output stage', page.getByTestId('construction-rows-settings-trigger'),
    () => waitVisible(page.getByTestId('construction-action-pivot-rows')));
  const pivotChoice = page.getByTestId('construction-action-pivot-rows');
  assert.equal(await pivotChoice.isEnabled(), true,
    'Pivot must be enabled after Unpivot because the stage contains Patient ID, field key, and value scalars.');
  await click('choose native Pivot after Unpivot', pivotChoice,
    () => waitVisible(page.getByTestId('construction-reshape-pivot')));

  const pivotGroup = page.getByRole('checkbox', { name: 'Pivot group Patient ID', exact: true });
  assert.equal(await pivotGroup.isEnabled(), true, 'Patient ID must be a usable Pivot group key.');
  await checkControl('keep one row per exact Patient ID', pivotGroup,
    () => waitFor(page, () => document.querySelector('input[aria-label="Pivot group Patient ID"]')?.checked === true, undefined));
  const keyStageColumn = unpivotStep.outputs.find(column => column.id === unpivotStep.operation.unpivot.keyOutputColumnId);
  const valueStageColumn = unpivotStep.outputs.find(column => column.id === unpivotStep.operation.unpivot.valueOutputColumnId);
  assert(keyStageColumn && valueStageColumn, 'Unpivot must expose its category and value columns for Pivot.');
  const category = page.getByLabel('Pivot category field', { exact: true });
  await select('use Unpivot field-name column as Pivot category', category, keyStageColumn.id,
    () => waitFor(page, expectedId => document.querySelector('select[aria-label="Pivot category field"]')?.value === expectedId, keyStageColumn.id));
  const value = page.getByLabel('Pivot values field', { exact: true });
  const valueFrom = startProposal();
  let categoriesProposal;
  await select('use Unpivot value column as Pivot values', value, valueStageColumn.id, async () => {
    await waitFor(page, expectedId => document.querySelector('select[aria-label="Pivot values field"]')?.value === expectedId, valueStageColumn.id);
    const discovery = await networkObserver.waitForResponse(candidate =>
      candidate.index >= valueFrom && candidate.status !== undefined && candidate.response &&
      candidate.path.endsWith('/construction-category-discoveries') &&
      candidate.request?.outputId === outputId && candidate.request?.stageId === unpivotStep.id &&
      candidate.request?.categoryColumnId === keyStageColumn.id &&
      candidate.request?.valueColumnId === valueStageColumn.id,
    { fromIndex: valueFrom, timeoutMs: ACTION_TIMEOUT_MS });
    assert.equal(discovery.status, 200, 'Stage-bound Pivot category discovery must return HTTP 200.');
    const discoveryRequest = discovery.request;
    const discoveryResponse = discovery.response;
    assert.equal(discoveryResponse.outcome, 'COMPLETE');
    assert.equal(discoveryResponse.complete, true);
    assert(discoveryResponse.proofFingerprint, 'Pivot discovery must return its complete compiler proof.');
    for (const field of ['snapshotToken', 'outputId', 'stageId', 'categoryColumnId', 'valueColumnId']) {
      assert.equal(discoveryResponse[field], discoveryRequest[field], `Pivot discovery response ${field} must match its exact request.`);
    }
    assert.equal(discoveryResponse.draftVersion, discoveryRequest.expectedDraftVersion);
    assert.equal(discoveryResponse.draftDigest, discoveryRequest.expectedDraftDigest);
    assert.deepEqual(discoveryResponse.categories.map(item => item.key.string).sort(), ['birthDate', 'gender']);
    await waitFor(page, () => document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]')?.textContent
      .includes('Selected 2 of 2 categories'), undefined);
    categoriesProposal = await awaitProposal(valueFrom, 'Discover Pivot categories after Unpivot');
  });
  assert(categoriesProposal, 'Pivot field selection must return its completed proposal inside the action budget.');
  assert.equal(categoriesProposal.preview?.rowCount, 2, 'Stage-scoped Pivot preview must return the two independent Patient rows.');
  await waitFor(page, () => document.querySelector('[data-testid="construction-reshape-pivot-category-summary"]')?.textContent
    .includes('Selected 2 of 2 categories'), undefined);
  const categorySummary = normalizeVisible(await page.getByTestId('construction-reshape-pivot-category-summary').innerText());
  assert(categorySummary.includes('Selected 2 of 2 categories'), `Expected both independent field keys in Pivot discovery: ${categorySummary}`);
  const categoriesDetails = page.getByTestId('construction-reshape-pivot-categories');
  await click('inspect discovered Pivot categories', categoriesDetails.locator(':scope > summary'),
    () => waitFor(page, () => document.querySelector('[data-testid="construction-reshape-pivot-categories"]')?.open === true, undefined));
  const categoryKeys = await categoriesDetails.locator('input[type="checkbox"][aria-label^="Include category "]')
    .evaluateAll(inputs => inputs.map(input => ({ label: input.getAttribute('aria-label'), checked: input.checked, disabled: input.disabled }))
      .sort((left, right) => left.label.localeCompare(right.label)));
  assert.deepEqual(categoryKeys, [
    { label: 'Include category birthDate', checked: true, disabled: false },
    { label: 'Include category gender', checked: true, disabled: false },
  ], 'Pivot discovery must select exactly the two source-derived keys.');
  const duplicatePolicy = page.getByLabel('Pivot duplicate policy', { exact: true });
  assert.equal(await duplicatePolicy.isEnabled(), true);
  assert.equal(await duplicatePolicy.locator('option[value="ERROR"]').isEnabled(), true);
  const numericDuplicateOptions = await duplicatePolicy.locator('option').evaluateAll(options =>
    options.filter(option => ['SUM', 'MIN', 'MAX'].includes(option.value))
      .map(option => ({ value: option.value, disabled: option.disabled })),
  );
  assert.deepEqual(numericDuplicateOptions, [
    { value: 'SUM', disabled: true },
    { value: 'MIN', disabled: true },
    { value: 'MAX', disabled: true },
  ], 'Numeric duplicate policies must remain disabled for the string-valued Pivot input.');
  const policySummary = normalizeVisible(await page.getByTestId('construction-reshape-pivot-policy-summary').innerText());
  assert(policySummary.includes('Stop with an error') || policySummary.includes('error'),
    `Pivot duplicate policy should visibly retain its typed ERROR behavior: ${policySummary}`);

  const pivotAdvanced = page.getByTestId('construction-reshape-pivot-advanced');
  await click('open Pivot advanced settings and output labels', pivotAdvanced.locator(':scope > summary'),
    () => waitFor(page, () => document.querySelector('[data-testid="construction-reshape-pivot-advanced"]')?.open === true, undefined));
  const initialPivotExpected = pivotExpected('Gender');
  const birthDateOutput = page.getByLabel('Pivot output label birthDate', { exact: true });
  const genderOutput = page.getByLabel('Pivot output label gender', { exact: true });
  const birthLabelFrom = startProposal();
  let birthLabelProposal;
  await fill('label Pivot birthDate output Birth date', birthDateOutput, 'Birth date',
    async () => {
      await waitFor(page, () => document.querySelector('input[aria-label="Pivot output label birthDate"]')?.value === 'Birth date', undefined);
      birthLabelProposal = await awaitProposal(birthLabelFrom, 'Label Pivot birthDate output');
    });
  assert(birthLabelProposal, 'Pivot birthDate label action must return its completed proposal.');
  const genderLabelFrom = startProposal();
  let finalPivotProposal;
  await fill('label Pivot gender output Gender', genderOutput, 'Gender',
    async () => {
      await waitFor(page, () => document.querySelector('input[aria-label="Pivot output label gender"]')?.value === 'Gender', undefined);
      finalPivotProposal = await awaitProposal(genderLabelFrom, 'Label Pivot gender output');
    });
  assert(finalPivotProposal, 'Final Pivot configuration action must return its completed proposal.');
  assertPreviewRows(finalPivotProposal, initialPivotExpected, ['Patient ID', 'Gender', 'Birth date'], 'configured Pivot proposal');
  builder = await readBuilder();
  await applyProposal('apply Pivot after Unpivot', initialPivotExpected, ['Patient ID', 'Gender', 'Birth date']);
  builder = await readBuilder();
  let pivotStep = stepFor(builder, 'PIVOT');
  unpivotStep = stepFor(builder, 'UNPIVOT');
  assert(pivotStep && unpivotStep, 'Applied composition must retain ordered Unpivot then Pivot steps.');
  assert.equal(pivotStep.inputs[0]?.stepId, unpivotStep.id,
    'Pivot must consume the immediate Unpivot output stage, proving the requested operation order.');
  const savedPivotCategoryKeys = pivotStep.operation.pivot.categories.map(category => category.key.string).sort();
  assert.deepEqual(savedPivotCategoryKeys, ['birthDate', 'gender']);
  check('correctness', 'native Pivot consumes the Unpivot output and reconstructs exact IDs, genders, and dates', true,
    { unpivotStepId: unpivotStep.id, pivotStepId: pivotStep.id, groupKeyIds: pivotStep.operation.pivot.groupKeyIds,
      categories: pivotStep.operation.pivot.categories, rows: initialPivotExpected });
  restored = await reloadAndRead('reloaded Unpivot→Pivot roundtrip', initialPivotExpected, ['Patient ID', 'Gender', 'Birth date']);
  builder = restored.builder;
  pivotStep = stepFor(builder, 'PIVOT');
  unpivotStep = stepFor(builder, 'UNPIVOT');
  assert(pivotStep && unpivotStep && pivotStep.inputs[0]?.stepId === unpivotStep.id);
  check('persistence', 'ordered Unpivot→Pivot construction and exact two-row result survive reload', true,
    { unpivotStepId: unpivotStep.id, pivotStepId: pivotStep.id, rows: restored.preview.rows });

  const beforeEdit = await readBuilder();
  await click('select saved Pivot step for edit', page.getByTestId(`construction-history-step-${pivotStep.id}`));
  await click('open saved Pivot editor', page.getByTestId(`construction-edit-step-${pivotStep.id}`),
    () => waitVisible(page.getByTestId('construction-reshape-pivot')));
  const editPivotAdvanced = page.getByTestId('construction-reshape-pivot-advanced');
  if (!(await editPivotAdvanced.evaluate(details => details.open))) {
    await click('open saved Pivot advanced settings for output labels', editPivotAdvanced.locator(':scope > summary'),
      () => waitFor(page, () => document.querySelector('[data-testid="construction-reshape-pivot-advanced"]')?.open === true, undefined));
  }
  const genderEdit = page.getByLabel('Pivot output label gender', { exact: true });
  const editFrom = startProposal();
  let editProposal;
  await fill('edit saved Pivot Gender label to Sex', genderEdit, 'Sex',
    async () => {
      await waitFor(page, () => document.querySelector('input[aria-label="Pivot output label gender"]')?.value === 'Sex', undefined);
      editProposal = await awaitProposal(editFrom, 'Edit Pivot gender label');
    });
  assert(editProposal, 'Saved Pivot edit action must return its completed proposal.');
  assertPreviewRows(editProposal, pivotExpected('Sex'), ['Patient ID', 'Sex', 'Birth date'], 'Pivot edit proposal');
  builder = await readBuilder();
  await applyProposal('apply saved Pivot label edit', pivotExpected('Sex'), ['Patient ID', 'Sex', 'Birth date']);
  builder = await readBuilder();
  pivotStep = stepFor(builder, 'PIVOT');
  assert(pivotStep, 'Edited Pivot step must remain saved.');
  const genderCategory = pivotStep.operation.pivot.categories.find(category => category.key.string === 'gender');
  const genderOutputColumn = pivotStep.outputs.find(column => column.id === genderCategory?.outputColumnId);
  assert.equal(genderOutputColumn?.label, 'Sex');
  const editedReload = await reloadAndRead('reloaded edited Pivot output label', pivotExpected('Sex'), ['Patient ID', 'Sex', 'Birth date']);
  builder = editedReload.builder;
  pivotStep = stepFor(builder, 'PIVOT');
  check('persistence', 'saved Pivot output label edit and literal source values survive reload', true,
    { pivotStepId: pivotStep.id, genderLabel: genderOutputColumn.label, rows: editedReload.preview.rows,
      priorDraftDigest: beforeEdit.draftDigest });

  const removeStep = async step => {
    await click(`select saved ${step.operation.kind} history step`, page.getByTestId(`construction-history-step-${step.id}`));
    const fromIndex = startProposal();
    let proposal;
    await click(`propose removal of ${step.operation.kind}`, page.getByTestId(`construction-remove-step-${step.id}`),
      async () => {
        await waitVisible(page.locator(panelSelector));
        proposal = await awaitProposal(fromIndex, `Remove ${step.operation.kind}`);
      });
    assert(proposal, `${step.operation.kind} removal action must return its completed proposal.`);
    return proposal;
  };
  const beforePivotRemoval = await readBuilder();
  const removePivotProposal = await removeStep(pivotStep);
  const unpivotRows = expected.unpivotRows.map(row => ({ 'Patient ID': row.patient_id, Field: row.field, Value: row.value }));
  assertPreviewRows(removePivotProposal, unpivotRows, ['Patient ID', 'Field', 'Value'], 'Pivot removal proposal');
  await cancelProposal('cancel Pivot removal', beforePivotRemoval.workspace);
  check('correctness', 'Cancel preserves the exact two-step Unpivot→Pivot workspace before restoration', true,
    { steps: stepsFor(builder).map(step => step.operation.kind) });
  const appliedRemoval = await removeStep(pivotStep);
  assertPreviewRows(appliedRemoval, unpivotRows, ['Patient ID', 'Field', 'Value'], 'confirmed Pivot removal proposal');
  builder = await readBuilder();
  await applyProposal('remove Pivot and restore Unpivot output', unpivotRows, ['Patient ID', 'Field', 'Value']);
  builder = await readBuilder();
  assert.equal(stepFor(builder, 'PIVOT'), undefined, 'Removing Pivot must restore the Unpivot-only construction.');
  unpivotStep = stepFor(builder, 'UNPIVOT');
  assert(unpivotStep, 'Removing Pivot must retain Unpivot.');
  restored = await reloadAndRead('reloaded Unpivot-only restoration', unpivotRows, ['Patient ID', 'Field', 'Value']);
  builder = restored.builder;
  assert.equal(stepFor(builder, 'PIVOT'), undefined);
  assert(stepFor(builder, 'UNPIVOT'));
  check('persistence', 'removing Pivot and reloading restores exact Unpivot rows and four-row multiplicity', true,
    { rows: restored.preview.rows });

  const currentUnpivot = stepFor(builder, 'UNPIVOT');
  const beforeUnpivotRemoval = await readBuilder();
  const removeUnpivot = await removeStep(currentUnpivot);
  assertPreviewRows(removeUnpivot, labelRows, [sourceLabels.patientID, sourceLabels.gender, sourceLabels.birthDate], 'Unpivot removal proposal');
  await cancelProposal('cancel Unpivot removal', beforeUnpivotRemoval.workspace);
  const removeUnpivotApplied = await removeStep(currentUnpivot);
  assertPreviewRows(removeUnpivotApplied, labelRows, [sourceLabels.patientID, sourceLabels.gender, sourceLabels.birthDate], 'confirmed Unpivot removal proposal');
  builder = await readBuilder();
  await applyProposal('remove Unpivot and restore original source rows', labelRows, [sourceLabels.patientID, sourceLabels.gender, sourceLabels.birthDate]);
  builder = await readBuilder();
  assert.equal(stepFor(builder, 'UNPIVOT'), undefined, 'Removing Unpivot must restore the original direct source projection.');
  expectStateFields(builder);
  restored = await reloadAndRead('reloaded original Patient source restoration', labelRows, [sourceLabels.patientID, sourceLabels.gender, sourceLabels.birthDate]);
  builder = restored.builder;
  assert.equal(stepFor(builder, 'UNPIVOT'), undefined);
  expectStateFields(builder);
  check('persistence', 'removing Unpivot and reloading restores the exact two literal Patient rows and source paths', true,
    { rows: restored.preview.rows, sourcePaths: documentFor(builder).columns.map(sourcePath) });

  const unexpected = networkObserver.failures.map(failure => ({
    kind: failure.kind,
    path: failure.entry?.path,
    status: failure.entry?.status ?? failure.status,
    message: failure.message ?? failure.errorText ?? failure.entry?.failure?.errorText,
  }));
  const actionFailures = report.actions.filter(item => item.status !== 'passed' || item.elapsedMs > ACTION_TIMEOUT_MS);
  check('performance', 'All native actions and action-to-render checkpoints complete within five seconds',
    actionFailures.length === 0, { actionCount: report.actions.length, actionFailures });
  check('correctness', 'No unexpected native HTTP or network errors occurred', unexpected.length === 0, unexpected);
}
