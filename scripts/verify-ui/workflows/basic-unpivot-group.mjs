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

export const readVisibleTableInBrowser = table => {
  const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  return {
    headers: [...table.querySelectorAll('[role="columnheader"]')].map(cell => normalize(cell.innerText)),
    rows: [...table.querySelectorAll('[role="row"]')].slice(1).filter(row => row.querySelector('[role="cell"]'))
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => normalize(cell.innerText))),
  };
};

export const BASIC_UNPIVOT_GROUP_EXPECTED_ROWS = Object.freeze([
  Object.freeze({ Field: 'birthDate', 'Row count': 2 }),
  Object.freeze({ Field: 'gender', 'Row count': 2 }),
]);
export const BASIC_UNPIVOT_GROUP_EDITED_EXPECTED_ROWS = Object.freeze([
  Object.freeze({ 'Source field': 'birthDate', 'Row count': 2 }),
  Object.freeze({ 'Source field': 'gender', 'Row count': 2 }),
]);

export const assertBasicUnpivotGroupContext = ({ scenarioID, caseName, report, target, seed }) => {
  assert.equal(scenarioID, 'basic-unpivot-group', 'The native fixture context must select the Unpivot→Group scenario.');
  assert.equal(caseName, 'unpivot-to-group', 'The native fixture context must select the Unpivot→Group case.');
  assert.equal(report.scenario, scenarioID);
  assert.equal(report.case, caseName, 'The report contract stores its case name in `case`.');
  assert.equal(seed?.fresh, true, 'This contract requires a fresh isolated Basic fixture project.');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_'), 'Expected an isolated loom_dev_verify project.');
  assert(target.fixtureGeneration && target.fixtureDir, 'The fixture project, generation, and directory must be explicit.');
};

const waitFor = (page, predicate, value, timeout = ACTION_TIMEOUT_MS) =>
  page.waitForFunction(({ predicateSource, argument }) =>
    Boolean(Function('argument', `return (${predicateSource})(argument);`)(argument)),
  { predicateSource: predicate.toString(), argument: value }, { timeout: Math.min(timeout, ACTION_TIMEOUT_MS) });

const previewFromPayload = payload => payload?.preview ?? payload;

const assertPreviewRows = (payload, expectedRows, expectedLabels, label) => {
  const preview = previewFromPayload(payload);
  assert(preview && Array.isArray(preview.columns) && Array.isArray(preview.rows),
    `${label}: preview must contain typed columns and rows`);
  if (preview.previewStatus !== undefined) assert.equal(preview.previewStatus, 'READY', `${label}: preview must be ready`);
  const labels = preview.columns.map(column => column.label);
  assert.equal(new Set(labels).size, labels.length, `${label}: preview labels must be unique`);
  assert.deepEqual([...labels].sort(), [...expectedLabels].sort(), `${label}: exact output labels differ from the independent oracle`);
  assert.equal(preview.rowCount, expectedRows.length, `${label}: typed row count differs from the independent oracle`);
  assert.equal(preview.rows.length, expectedRows.length, `${label}: typed row multiplicity differs from the independent oracle`);
  const actual = preview.rows.map(row => preview.columns.map(column => row[column.column]));
  const expected = expectedRows.map(row => preview.columns.map(column => row[column.label]));
  assert.deepEqual(actual.map(row => JSON.stringify(row)).sort(), expected.map(row => JSON.stringify(row)).sort(),
    `${label}: typed values or duplicate rows differ from the literal oracle`);
  return preview;
};

export async function basicUnpivotGroupWorkflow({ page, action, check, networkObserver, report, target, seed, scenarioID, caseName }) {
  configureNativePage(page);
  assertBasicUnpivotGroupContext({ scenarioID, caseName, report, target, seed });

  const fixture = readBasicUnpivotPivotFixture(target.fixtureDir);
  const source = expectedBasicUnpivotPivotRows(fixture.patients);
  const sourceLabels = BASIC_UNPIVOT_PIVOT_EXPECTED.sourceColumnLabels;
  const expectedUnpivot = source.unpivotRows.map(row => ({
    'Patient ID': row.patient_id,
    Field: row.field,
    Value: row.value,
  }));
  const expectedLabels = ['Field', 'Row count'];
  report.target.fixtureSeedManifest = fixture.manifest;
  report.target.fixtureOracle = {
    kind: 'independent literal Basic Patient source oracle',
    project: target.fixtureProject,
    generation: target.fixtureGeneration,
    patients: fixture.patients,
    expectedUnpivotRows: expectedUnpivot,
    expectedGroupRows: BASIC_UNPIVOT_GROUP_EXPECTED_ROWS,
  };
  check('correctness', 'Basic fixture contains exactly two literal Patients and an empty Observation source',
    fixture.patients.length === 2 && fixture.manifest.find(file => file.name === 'Observation.ndjson')?.sizeBytes === 0,
    report.target.fixtureSeedManifest);
  check('correctness', 'independent source pins both Patient IDs, genders, and dates',
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
  const startProposal = () => networkObserver.requests.length;

  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  await waitFor(page, () => document.body.innerText.includes('Build your first table') ||
    document.body.innerText.includes('Dataset graph'), undefined);

  await click('open fresh Explorer creation', page.locator('summary').filter({ hasText: /^New explorer$/ }),
    () => waitVisible(page.locator('#new-explorer-name')));
  const explorerTitle = `Verify ${report.target.project.slice(-10)} Basic Unpivot then Group`;
  await fill('name fresh Basic Unpivot→Group Explorer', page.locator('#new-explorer-name'), explorerTitle);
  await click('create fresh Basic Unpivot→Group Explorer', page.getByRole('button', { name: 'Create blank', exact: true }),
    () => waitFor(page, title => document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === title &&
      document.body.innerText.includes('Build your first table'), explorerTitle));
  const explorer = await page.locator('select[aria-label="Explorer"]').inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'The workflow must use a fresh non-bootstrap Explorer.');
  report.target.explorer = explorer;

  await fill('name Patient root table', page.locator('#first-table-name'), 'Patient Unpivot Group');
  await click('create native Patient root table', page.getByRole('button', { name: 'Choose Patient rows', exact: true }),
    () => waitFor(page, () => document.body.innerText.includes('DATASET WORKSPACE') &&
      document.body.innerText.includes('Patient Unpivot Group') && !document.body.innerText.includes('Build your first table'), undefined));

  const base = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorer)}/authoring/v2`;
  const readBuilder = async () => {
    const response = await fetch(`${target.apiUrl}${base}/builder`, { signal: AbortSignal.timeout(30000) });
    const body = await response.json();
    assert(response.ok, `Read-only Builder verification failed with HTTP ${response.status}: ${JSON.stringify(body).slice(0, 600)}`);
    return body;
  };
  let builder = await readBuilder();
  assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder must read the exact fixture generation.');
  const documentFor = state => state.workspace.documents.find(item => item.rootResourceType === 'Patient');
  let document = documentFor(builder);
  assert(document?.output?.id, 'Native Patient table must have a Builder output.');
  const outputId = document.output.id;
  report.target.outputId = outputId;
  const sourcePath = column => column.source?.field?.path ?? column.source?.path ?? '';
  const waitForProposal = async (fromIndex, label, expectedPath) => {
    const entry = await networkObserver.waitForResponse(candidate => candidate.index >= fromIndex &&
      candidate.status !== undefined && candidate.response && candidate.path.endsWith(expectedPath) &&
      candidate.request?.outputId === outputId, { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
    assert.equal(entry.status, 200, `${label}: native proposal must return HTTP 200.`);
    assert.equal(entry.response.outputId, outputId, `${label}: response must belong to the exact output.`);
    if (expectedPath.endsWith('/construction-proposals')) {
      assert.equal(entry.response.previewStatus, 'READY', `${label}: proposal preview must be ready.`);
      assert(entry.response.proposalId, `${label}: response must carry a proposal identity.`);
      assert.equal(entry.response.preview?.receiptId, entry.response.proposalId, `${label}: preview must use its proposal receipt.`);
    }
    assert(Array.isArray(entry.response.preview?.rows), `${label}: completed response must contain typed preview rows.`);
    assert.equal(entry.response.preview.outputId, outputId, `${label}: preview must belong to the exact output.`);
    await waitFor(page, expected => {
      const panel = document.querySelector(expected.panel);
      const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
      return panel?.dataset.proposalStatus === 'ready' &&
        (expected.choice || panel.dataset.proposalId === expected.proposalId) &&
        preview?.dataset.previewStatus === 'ready' && preview.dataset.previewReceiptId === expected.receiptId &&
        preview.dataset.previewOutputId === expected.outputId &&
        preview.querySelectorAll('tr[data-testid="construction-proposal-preview-row"]').length === expected.rows;
    }, {
      panel: expectedPath.endsWith('/construction-choice-proposals')
        ? '[data-testid="construction-choice-proposal-panel"]' : '[data-testid="construction-proposal-panel"]',
      choice: expectedPath.endsWith('/construction-choice-proposals'),
      proposalId: entry.response.proposalId,
      receiptId: entry.response.preview.receiptId,
      outputId,
      rows: entry.response.preview.rows.length,
    });
    return entry.response;
  };
  const assertRenderedRows = async (label, rows, labels, expectedOutput = outputId) => {
    await waitFor(page, id => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      return preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === id && preview.dataset.previewReceiptId;
    }, expectedOutput);
    const visible = await page.locator('[data-testid="preview-table-scroll"] [role="table"]')
      .evaluate(readVisibleTableInBrowser);
    assert.equal(visible.headers.length, labels.length, `${label}: exact visible header count differs from oracle.`);
    const expectedLabelByIndex = new Map(labels.map(field => [previewHeaderIndex(visible.headers, field), field]));
    assert.equal(expectedLabelByIndex.size, labels.length, `${label}: exact visible headers must map uniquely.`);
    const expectedRows = rows.map(row => visible.headers.map((_, index) => {
      const field = expectedLabelByIndex.get(index);
      assert(field, `${label}: rendered column ${index} has no independent label mapping.`);
      const value = row[field];
      return value === null || value === undefined ? '—' : normalizeVisible(value);
    }));
    assert.deepEqual(visible.rows.map(row => JSON.stringify(row)).sort(), expectedRows.map(row => JSON.stringify(row)).sort(),
      `${label}: rendered values or multiplicity differ from the independent literal oracle.`);
    return visible;
  };
  const applyProposal = async (label, expectedRows, labels) => {
    await click(`Apply ${label}`, page.getByTestId('construction-apply-proposal'), async () => {
      await waitFor(page, selector => !document.querySelector(selector), panelSelector);
      await assertRenderedRows(label, expectedRows, labels);
    });
    builder = await readBuilder();
    await assertRenderedRows(`${label} saved output`, expectedRows, labels);
  };

  const addEditor = page.locator('[aria-label="Add columns editor"]');
  await waitFor(page, () => Boolean(document.querySelector('[data-testid="construction-action-add-columns"]') &&
    !document.querySelector('[data-testid="construction-action-add-columns"]').disabled), undefined);
  await click('open native Add columns', page.getByTestId('construction-action-add-columns'), () => waitVisible(addEditor));
  await click('choose native Fields and related data', page.locator('[aria-label="Column types"]')
    .getByRole('button', { name: 'Fields and related data', exact: true }),
  () => waitVisible(page.getByTestId('construction-add-columns-source')));
  const rawFields = page.getByTestId('feature-catalog-raw-fields');
  await waitVisible(rawFields);
  if (!await rawFields.evaluate(element => element.open)) {
    await click('open raw Patient field list', rawFields.locator(':scope > summary'),
      () => waitFor(page, () => document.querySelector('[data-testid="feature-catalog-raw-fields"]')?.open === true, undefined));
  }
  for (const path of ['gender', 'birthDate']) {
    const checkbox = page.getByRole('checkbox', { name: `Select Patient.${path}`, exact: true });
    await checkControl(`select native Patient.${path}`, checkbox,
      () => waitFor(page, value => document.querySelector(`input[aria-label=${JSON.stringify(`Select Patient.${value}`)}]`)?.checked === true, path));
  }
  const expectedSourceRows = fixture.patients.map(patient => ({
    'Patient ID': patient.id,
    Gender: patient.gender,
    'Birth Date': patient.birthDate,
  }));
  const addFrom = startProposal();
  let sourceProposal;
  await click('propose exact Patient.gender and Patient.birthDate columns',
    page.getByRole('button', { name: 'Add 2 selected features', exact: true }), async () => {
      sourceProposal = await waitForProposal(addFrom, 'Add Patient source fields', '/construction-choice-proposals');
      assertPreviewRows(sourceProposal, expectedSourceRows, ['Patient ID', 'Gender', 'Birth Date'], 'Patient field proposal');
    });
  assertPreviewRows(sourceProposal, expectedSourceRows, ['Patient ID', 'Gender', 'Birth Date'], 'Patient field proposal');
  await click('Apply Patient source fields', page.getByRole('button', { name: 'Apply columns', exact: true }), async () => {
    await waitFor(page, selector => !document.querySelector(selector), panelSelector);
    await assertRenderedRows('Patient source projection', expectedSourceRows, ['Patient ID', 'Gender', 'Birth Date']);
  });
  builder = await readBuilder();
  document = documentFor(builder);
  const columns = document?.columns ?? [];
  assert.equal(columns.length, 3, 'Patient source projection must contain only ID, gender, and birthDate.');
  const idColumn = columns.find(column => sourcePath(column) === 'id');
  const genderColumn = columns.find(column => sourcePath(column) === 'gender');
  const birthDateColumn = columns.find(column => sourcePath(column) === 'birthDate');
  assert(idColumn?.columnId && genderColumn?.columnId && birthDateColumn?.columnId,
    'Patient source fields must retain exact IDs for ID, gender, and birthDate.');
  await assertRenderedRows('applied Patient source projection', expectedSourceRows, ['Patient ID', 'Gender', 'Birth Date']);
  await click('close native Add columns before configuring Unpivot',
    page.getByTestId('construction-close-operation-editor'),
    () => waitVisible(page.getByTestId('construction-rows-settings-trigger')));

  for (const column of [genderColumn, birthDateColumn]) {
    const locator = page.getByTestId(`construction-column-${column.columnId}`);
    await click(`select ${column.label} for native Unpivot`, locator,
      () => waitFor(page, id => document.querySelector(`[data-testid="construction-column-${id}"]`)?.getAttribute('aria-pressed') === 'true', column.columnId));
  }
  await click('open native row settings for Unpivot', page.getByTestId('construction-rows-settings-trigger'),
    () => waitVisible(page.getByTestId('construction-action-unpivot-rows')));
  const unpivotFrom = startProposal();
  let unpivotProposal;
  await click('choose native Unpivot rows', page.getByTestId('construction-action-unpivot-rows'), async () => {
    await waitVisible(page.getByTestId('construction-reshape-unpivot'));
    unpivotProposal = await waitForProposal(unpivotFrom, 'Initial Unpivot proposal', '/construction-proposals');
  });
  const unpivotEditor = page.getByTestId('construction-reshape-unpivot');
  for (const column of [genderColumn, birthDateColumn]) {
    assert.equal(await unpivotEditor.locator(`input[aria-label=${JSON.stringify(`Unpivot ${column.label}`)}]`).isChecked(), true,
      `Unpivot must include ${column.label}.`);
  }
  const advanced = page.getByTestId('construction-unpivot-advanced');
  await click('open Unpivot key controls', advanced.locator(':scope > summary'),
    () => waitFor(page, () => document.querySelector('[data-testid="construction-unpivot-advanced"]')?.open === true, undefined));
  let configuredUnpivot;
  let finalUnpivotCheckedInAction = false;
  const genderKey = page.locator(`input[aria-label=${JSON.stringify(`Unpivot key value ${genderColumn.columnId}`)}]`);
  const birthDateKey = page.locator(`input[aria-label=${JSON.stringify(`Unpivot key value ${birthDateColumn.columnId}`)}]`);
  const keyLabel = page.getByLabel('Unpivot key output label', { exact: true });
  const valueLabel = page.getByLabel('Unpivot value output label', { exact: true });
  const assertFinalUnpivotWhenConfigured = async proposal => {
    const [genderValue, birthDateValue, keyText, valueText] = await Promise.all([
      genderKey.inputValue(), birthDateKey.inputValue(), keyLabel.inputValue(), valueLabel.inputValue(),
    ]);
    if (genderValue === 'gender' && birthDateValue === 'birthDate' && keyText === 'Field' && valueText === 'Value') {
      assertPreviewRows(proposal, expectedUnpivot, ['Patient ID', 'Field', 'Value'], 'configured Unpivot proposal');
      finalUnpivotCheckedInAction = true;
    }
  };
  for (const [column, keyValue] of [[genderColumn, 'gender'], [birthDateColumn, 'birthDate']]) {
    const key = column === genderColumn ? genderKey : birthDateKey;
    if (await key.inputValue() === keyValue) continue;
    const fromIndex = startProposal();
    await fill(`set literal Unpivot key ${keyValue}`, key, keyValue, async () => {
      await waitFor(page, value => document.querySelector(`input[aria-label=${JSON.stringify(`Unpivot key value ${value.columnId}`)}]`)?.value === value.key,
        { columnId: column.columnId, key: keyValue });
      configuredUnpivot = await waitForProposal(fromIndex, `Configure Unpivot key ${keyValue}`, '/construction-proposals');
      await assertFinalUnpivotWhenConfigured(configuredUnpivot);
    });
  }
  for (const [label, locator, value, aria] of [
    ['label Unpivot key column Field', keyLabel, 'Field', 'Unpivot key output label'],
    ['label Unpivot value column Value', valueLabel, 'Value', 'Unpivot value output label'],
  ]) {
    if (await locator.inputValue() === value) continue;
    const fromIndex = startProposal();
    await fill(label, locator, value, async () => {
      await waitFor(page, expected => document.querySelector(`input[aria-label=${JSON.stringify(expected.aria)}]`)?.value === expected.value, { aria, value });
      configuredUnpivot = await waitForProposal(fromIndex, label, '/construction-proposals');
      await assertFinalUnpivotWhenConfigured(configuredUnpivot);
    });
  }
  assert(configuredUnpivot ?? unpivotProposal, 'Unpivot configuration must produce a completed proposal.');
  const readyUnpivot = configuredUnpivot ?? unpivotProposal;
  assertPreviewRows(readyUnpivot, expectedUnpivot, ['Patient ID', 'Field', 'Value'], 'configured Unpivot proposal');
  assert(finalUnpivotCheckedInAction || readyUnpivot === unpivotProposal,
    'The final exact Unpivot values must be checked inside their owning native action.');
  await applyProposal('Unpivot Patient gender and birthDate', expectedUnpivot, ['Patient ID', 'Field', 'Value']);
  builder = await readBuilder();
  const steps = documentFor(builder)?.construction?.steps ?? [];
  const unpivotStep = steps.find(step => step.operation.kind === 'UNPIVOT');
  assert(unpivotStep, 'Native Apply must save an Unpivot step.');
  assert.deepEqual(unpivotStep.operation.unpivot.inputs.map(input => ({
    path: columns.find(column => column.columnId === input.columnId)?.source?.field?.path,
    key: input.key.string,
  })).sort((left, right) => left.path.localeCompare(right.path)), [
    { path: 'birthDate', key: 'birthDate' },
    { path: 'gender', key: 'gender' },
  ], 'Saved Unpivot must use exactly the two independently sourced field paths and keys.');

  const unpivotLabels = ['Patient ID', 'Field', 'Value'];
  const editedGroupRows = BASIC_UNPIVOT_GROUP_EDITED_EXPECTED_ROWS;
  const editedGroupLabels = ['Source field', 'Row count'];
  const table = page.getByTestId(`construction-table-${outputId}`);
  const closeEditor = page.getByTestId('construction-close-operation-editor');
  const waitHidden = locator => locator.waitFor({ state: 'hidden', timeout: ACTION_TIMEOUT_MS });
  const assertDraftUnchanged = (before, after, label) => {
    assert.deepEqual(after.workspace, before.workspace, `${label}: saved workspace must remain byte-for-byte equivalent.`);
    assert.equal(after.draftDigest, before.draftDigest, `${label}: saved draft digest must remain unchanged.`);
    assert.equal(after.draftVersion, before.draftVersion, `${label}: saved draft version must remain unchanged.`);
  };
  const savedSteps = state => documentFor(state)?.construction?.steps ?? [];
  const assertSavedGroup = (state, groupOutputLabel) => {
    const steps = savedSteps(state);
    assert.deepEqual(steps.map(step => step.operation.kind), ['UNPIVOT', 'GROUP'],
      'Saved Builder must contain exactly Unpivot followed by Group.');
    const savedUnpivot = steps[0];
    const savedGroup = steps[1];
    assert.equal(savedGroup.inputs[0]?.stepId, savedUnpivot.id, 'Group must consume the immediate Unpivot output stage.');
    const keys = savedGroup.operation.group.keys;
    assert.equal(keys.length, 1, 'Group must contain exactly one key.');
    assert.equal(keys[0].inputColumnId, savedUnpivot.operation.unpivot.keyOutputColumnId,
      'Group must use the Unpivot Field key as its input.');
    assert.equal(savedUnpivot.outputs.find(column => column.id === keys[0].inputColumnId)?.label, 'Field',
      'Editing the Group output label must not rename the saved Unpivot Field key.');
    assert.equal(savedGroup.outputs.find(column => column.id === keys[0].outputColumnId)?.label, groupOutputLabel,
      `Group key output label must be ${groupOutputLabel}.`);
    assert.deepEqual(savedGroup.operation.group.aggregates.map(aggregate => aggregate.operation), ['COUNT_ROWS'],
      'Group must save exactly the native Count rows aggregate.');
    const countAggregate = savedGroup.operation.group.aggregates[0];
    assert.equal(savedGroup.outputs.find(column => column.id === countAggregate.outputColumnId)?.label, 'Row count',
      'Group must retain the exact Row count output label.');
    return { unpivot: savedUnpivot, group: savedGroup };
  };
  const assertSavedUnpivotOnly = state => {
    const steps = savedSteps(state);
    assert.deepEqual(steps.map(step => step.operation.kind), ['UNPIVOT'], 'Saved Builder must contain only Unpivot.');
    const savedUnpivot = steps[0];
    assert.equal(savedUnpivot.outputs.find(column => column.id === savedUnpivot.operation.unpivot.keyOutputColumnId)?.label, 'Field',
      'Restored Unpivot must retain the exact Field key output label.');
    assert.deepEqual(savedUnpivot.operation.unpivot.inputs.map(input => ({
      path: columns.find(column => column.columnId === input.columnId)?.source?.field?.path,
      key: input.key.string,
    })).sort((left, right) => left.path.localeCompare(right.path)), [
      { path: 'birthDate', key: 'birthDate' },
      { path: 'gender', key: 'gender' },
    ], 'Restored Unpivot must retain the two literal source paths and key values.');
    return savedUnpivot;
  };
  const closeEditorAndVerifyRows = async (label, rows, labels) => {
    if (await closeEditor.isVisible()) {
      await click(label, closeEditor, async () => {
        await waitVisible(table);
        await assertRenderedRows(label, rows, labels);
      });
      return;
    }
    await waitVisible(table);
    await assertRenderedRows(label, rows, labels);
  };
  const cancelProposalAndVerifySaved = async (label, before, rows, labels) => {
    const editorOpen = await closeEditor.isVisible();
    await click(`${label}: Cancel proposal`, page.getByTestId('construction-cancel-proposal'), async () => {
      await waitHidden(page.locator(panelSelector));
      await assertRenderedRows(label, rows, labels);
    });
    if (editorOpen) await closeEditorAndVerifyRows(`${label}: close editor and restore saved rows`, rows, labels);
    const after = await readBuilder();
    assertDraftUnchanged(before, after, label);
    return after;
  };
  const openGroupProposal = async label => {
    await click(`${label}: open Rows settings`, page.getByTestId('construction-rows-settings-trigger'),
      async () => {
        await waitVisible(page.getByTestId('construction-action-group-rows'));
        await assertRenderedRows(`${label}: unchanged Unpivot rows`, expectedUnpivot, unpivotLabels);
      });
    const groupChoice = page.getByTestId('construction-action-group-rows');
    assert.equal(await groupChoice.isEnabled(), true, 'Group must be enabled for the Unpivot output stage.');
    const fromIndex = startProposal();
    let initialProposal;
    await click(`${label}: choose native Group after Unpivot`, groupChoice, async () => {
      await waitVisible(page.getByTestId('construction-reshape-group'));
      initialProposal = await waitForProposal(fromIndex, `${label}: initial Group proposal`, '/construction-proposals');
      assertPreviewRows(initialProposal, [{ 'Row count': 4 }], ['Row count'], `${label}: empty-key Group proposal`);
      assert.equal(await page.getByLabel('Summary 1', { exact: true }).inputValue(), 'COUNT_ROWS',
        `${label}: Group must default to Count rows.`);
    });
    const fieldKey = page.getByRole('checkbox', { name: 'Group by Field', exact: true });
    assert.equal(await fieldKey.isEnabled(), true, 'The Unpivot Field output must be a selectable Group key.');
    assert.equal(await fieldKey.isChecked(), false, 'A fresh Group proposal must begin without a selected key.');
    const fieldFrom = startProposal();
    let proposal;
    await checkControl(`${label}: group Unpivot rows by Field`, fieldKey, async () => {
      await waitFor(page, () => document.querySelector('input[aria-label="Group by Field"]')?.checked === true, undefined);
      proposal = await waitForProposal(fieldFrom, `${label}: Group by Field`, '/construction-proposals');
      assertPreviewRows(proposal, BASIC_UNPIVOT_GROUP_EXPECTED_ROWS, expectedLabels, `${label}: Group by Field proposal`);
    });
    return proposal;
  };
  const openSavedGroupEditor = async (label, groupStep, currentRows, currentLabels) => {
    const history = page.getByTestId(`construction-history-step-${groupStep.id}`);
    const edit = page.getByTestId(`construction-edit-step-${groupStep.id}`);
    await click(`${label}: select saved Group`, history, () => waitVisible(edit));
    await click(`${label}: edit saved Group`, edit, async () => {
      await waitVisible(page.getByTestId('construction-reshape-group'));
      await assertRenderedRows(`${label}: current saved Group rows`, currentRows, currentLabels);
    });
    const advanced = page.getByTestId('construction-reshape-group-advanced');
    if (!await advanced.evaluate(element => element.open)) {
      await click(`${label}: open Group output labels`, advanced.locator(':scope > summary'),
        async () => {
          await waitFor(page, () => document.querySelector('[data-testid="construction-reshape-group-advanced"]')?.open === true, undefined);
          await assertRenderedRows(`${label}: unchanged saved Group rows`, currentRows, currentLabels);
        });
    }
    return page.getByLabel('Group output label 1', { exact: true });
  };
  const editGroupOutputLabel = async (label, labelInput) => {
    const fromIndex = startProposal();
    let proposal;
    await fill(label, labelInput, 'Source field', async () => {
      await waitFor(page, () => document.querySelector('input[aria-label="Group output label 1"]')?.value === 'Source field', undefined);
      proposal = await waitForProposal(fromIndex, label, '/construction-proposals');
      assertPreviewRows(proposal, editedGroupRows, editedGroupLabels, `${label}: exact edited Group rows`);
    });
    return proposal;
  };
  const proposeGroupRemoval = async (label, groupStep) => {
    const history = page.getByTestId(`construction-history-step-${groupStep.id}`);
    const remove = page.getByTestId(`construction-remove-step-${groupStep.id}`);
    await click(`${label}: select saved Group`, history, async () => {
      await waitVisible(remove);
      await assertRenderedRows(`${label}: unchanged saved Group rows`, editedGroupRows, editedGroupLabels);
    });
    const fromIndex = startProposal();
    let proposal;
    await click(`${label}: preview Group removal`, remove, async () => {
      proposal = await waitForProposal(fromIndex, label, '/construction-proposals');
      assertPreviewRows(proposal, expectedUnpivot, unpivotLabels, `${label}: exact restored Unpivot rows`);
    });
    return proposal;
  };

  const firstGroupProposal = await openGroupProposal('first Group attempt');
  assertPreviewRows(firstGroupProposal, BASIC_UNPIVOT_GROUP_EXPECTED_ROWS, expectedLabels, 'first Group attempt');
  const unpivotBeforeGroupCancel = await readBuilder();
  const initialUnpivot = assertSavedUnpivotOnly(unpivotBeforeGroupCancel);
  const afterGroupCancel = await cancelProposalAndVerifySaved(
    'Cancel Field-keyed Group proposal', unpivotBeforeGroupCancel, expectedUnpivot, unpivotLabels,
  );
  const unpivotAfterCancel = assertSavedUnpivotOnly(afterGroupCancel);
  assert.equal(unpivotAfterCancel.id, initialUnpivot.id, 'Cancel must retain the saved Unpivot step identity.');
  check('correctness', 'Cancel Field-keyed Group proposal leaves saved Unpivot workspace, draft, and exact four rows unchanged', true,
    { unpivotStepId: unpivotAfterCancel.id, rows: expectedUnpivot });

  const reopenedGroupProposal = await openGroupProposal('reopened Group');
  assertPreviewRows(reopenedGroupProposal, BASIC_UNPIVOT_GROUP_EXPECTED_ROWS, expectedLabels, 'reopened Group proposal');
  await applyProposal('reopened Group by Field', BASIC_UNPIVOT_GROUP_EXPECTED_ROWS, expectedLabels);
  builder = await readBuilder();
  const appliedGroup = assertSavedGroup(builder, 'Field');
  check('correctness', 'reopened native Group applies the exact two literal source-key counts', true,
    { unpivotStepId: appliedGroup.unpivot.id, groupStepId: appliedGroup.group.id, rows: BASIC_UNPIVOT_GROUP_EXPECTED_ROWS });

  await closeEditorAndVerifyRows('close Group editor before first reload', BASIC_UNPIVOT_GROUP_EXPECTED_ROWS, expectedLabels);
  await pageAction('reload Unpivot→Group and render exact grouped rows', table, async () => {
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
    await table.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    if (await table.getAttribute('aria-current') !== 'page') await table.click({ timeout: ACTION_TIMEOUT_MS });
  }, async () => {
    await waitFor(page, id => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      const selected = document.querySelector(`[data-testid="construction-table-${id}"][aria-current="page"]`);
      return Boolean(selected && preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === id &&
        preview.dataset.previewReceiptId);
    }, outputId);
    await assertRenderedRows('reloaded Unpivot→Group output', BASIC_UNPIVOT_GROUP_EXPECTED_ROWS, expectedLabels);
  });
  builder = await readBuilder();
  const reloadedGroup = assertSavedGroup(builder, 'Field');
  assert.equal(reloadedGroup.unpivot.id, appliedGroup.unpivot.id, 'Reload must retain the Unpivot identity.');
  assert.equal(reloadedGroup.group.id, appliedGroup.group.id, 'Reload must retain the Group identity.');
  check('persistence', 'reloaded Builder retains both ordered steps and the exact two grouped rows', true,
    { unpivotStepId: reloadedGroup.unpivot.id, groupStepId: reloadedGroup.group.id, rows: BASIC_UNPIVOT_GROUP_EXPECTED_ROWS });

  const groupBeforeEditCancel = await readBuilder();
  const groupBeforeEdit = assertSavedGroup(groupBeforeEditCancel, 'Field');
  const firstLabelInput = await openSavedGroupEditor('first Group label edit', groupBeforeEdit.group,
    BASIC_UNPIVOT_GROUP_EXPECTED_ROWS, expectedLabels);
  const firstEditProposal = await editGroupOutputLabel('preview Group label Source field before Cancel', firstLabelInput);
  assertPreviewRows(firstEditProposal, editedGroupRows, editedGroupLabels, 'first Group label edit proposal');
  const afterEditCancel = await cancelProposalAndVerifySaved(
    'Cancel Group output-label edit', groupBeforeEditCancel, BASIC_UNPIVOT_GROUP_EXPECTED_ROWS, expectedLabels,
  );
  const canceledGroup = assertSavedGroup(afterEditCancel, 'Field');
  assert.equal(canceledGroup.group.id, groupBeforeEdit.group.id, 'Cancel must retain the saved Group identity.');
  check('correctness', 'Cancel Group output-label edit preserves saved Group, draft, Field key, COUNT_ROWS, and exact rows', true,
    { unpivotStepId: canceledGroup.unpivot.id, groupStepId: canceledGroup.group.id, rows: BASIC_UNPIVOT_GROUP_EXPECTED_ROWS });

  const labelBeforeApply = await readBuilder();
  const groupForEdit = assertSavedGroup(labelBeforeApply, 'Field');
  const secondLabelInput = await openSavedGroupEditor('reopen Group label edit', groupForEdit.group,
    BASIC_UNPIVOT_GROUP_EXPECTED_ROWS, expectedLabels);
  const appliedEditProposal = await editGroupOutputLabel('re-propose Group label Source field', secondLabelInput);
  assertPreviewRows(appliedEditProposal, editedGroupRows, editedGroupLabels, 'applied Group label edit proposal');
  await applyProposal('Group label Source field', editedGroupRows, editedGroupLabels);
  builder = await readBuilder();
  const editedGroup = assertSavedGroup(builder, 'Source field');
  assert.equal(editedGroup.unpivot.id, groupForEdit.unpivot.id, 'Label edit must preserve the Unpivot step.');
  assert.equal(editedGroup.group.id, groupForEdit.group.id, 'Label edit must preserve the Group step identity.');
  check('correctness', 'applied Group label edit preserves Field grouping and COUNT_ROWS with exact Source field rows', true,
    { unpivotStepId: editedGroup.unpivot.id, groupStepId: editedGroup.group.id, rows: editedGroupRows });
  const editedDraftIdentity = {
    draftDigest: builder.draftDigest,
    draftVersion: builder.draftVersion,
  };
  await closeEditorAndVerifyRows('close edited Group editor before removal', editedGroupRows, editedGroupLabels);

  const editedPreviewIdentityBeforeReload = await page.getByTestId('construction-preview').evaluate(preview => ({
    status: preview.dataset.previewStatus,
    receiptId: preview.dataset.previewReceiptId,
    outputId: preview.dataset.previewOutputId,
    draftVersion: preview.dataset.currentDraftVersion,
    draftDigest: preview.dataset.currentDraftDigest,
  }));
  assert.equal(editedPreviewIdentityBeforeReload.status, 'ready', 'Applied Group preview must be ready before reload.');
  assert.equal(editedPreviewIdentityBeforeReload.outputId, outputId, 'Applied Group preview must use the exact output.');
  assert(editedPreviewIdentityBeforeReload.receiptId, 'Applied Group preview must carry a receipt before reload.');
  assert.equal(editedPreviewIdentityBeforeReload.draftVersion, String(editedDraftIdentity.draftVersion),
    'Applied Group preview must use the exact saved draft version before reload.');
  assert.equal(editedPreviewIdentityBeforeReload.draftDigest, editedDraftIdentity.draftDigest,
    'Applied Group preview must use the exact saved draft digest before reload.');
  let editedReloadBuilder;
  let reloadedEditedGroup;
  await pageAction('reload applied Source field Group and verify exact saved rows', table, async () => {
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
    await table.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    if (await table.getAttribute('aria-current') !== 'page') await table.click({ timeout: ACTION_TIMEOUT_MS });
  }, async () => {
    editedReloadBuilder = await readBuilder();
    reloadedEditedGroup = assertSavedGroup(editedReloadBuilder, 'Source field');
    assert.equal(reloadedEditedGroup.unpivot.id, editedGroup.unpivot.id, 'Reload must retain the exact Unpivot identity.');
    assert.equal(reloadedEditedGroup.group.id, editedGroup.group.id, 'Reload must retain the exact edited Group identity.');
    assert.equal(editedReloadBuilder.draftVersion, editedDraftIdentity.draftVersion,
      'Reload must retain the applied Group edit draft version.');
    assert.equal(editedReloadBuilder.draftDigest, editedDraftIdentity.draftDigest,
      'Reload must retain the applied Group edit draft digest.');

    await waitFor(page, expected => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      const selected = document.querySelector(`[data-testid="construction-table-${expected.outputId}"][aria-current="page"]`);
      return Boolean(selected && preview?.dataset.previewStatus === 'ready' &&
        preview.dataset.previewOutputId === expected.outputId && preview.dataset.previewReceiptId === expected.receiptId &&
        preview.dataset.currentDraftVersion === expected.draftVersion &&
        preview.dataset.currentDraftDigest === expected.draftDigest);
    }, {
      outputId,
      receiptId: editedPreviewIdentityBeforeReload.receiptId,
      draftVersion: String(editedReloadBuilder.draftVersion),
      draftDigest: editedReloadBuilder.draftDigest,
    });
    await assertRenderedRows('reloaded Source field Group output', editedGroupRows, editedGroupLabels);
  });
  check('persistence', 'reload preserves edited Source field rows, Group identity, draft identity, and preview receipt', true,
    { unpivotStepId: reloadedEditedGroup.unpivot.id, groupStepId: reloadedEditedGroup.group.id,
      draftVersion: editedReloadBuilder.draftVersion, draftDigest: editedReloadBuilder.draftDigest,
      receiptId: editedPreviewIdentityBeforeReload.receiptId, rows: editedGroupRows });

  const groupBeforeRemovalCancel = editedReloadBuilder;
  const groupBeforeRemoval = reloadedEditedGroup;
  const firstRemovalProposal = await proposeGroupRemoval('first Group removal proposal', groupBeforeRemoval.group);
  assertPreviewRows(firstRemovalProposal, expectedUnpivot, unpivotLabels, 'first Group removal proposal');
  const afterRemovalCancel = await cancelProposalAndVerifySaved(
    'Cancel Group removal', groupBeforeRemovalCancel, editedGroupRows, editedGroupLabels,
  );
  const groupAfterRemovalCancel = assertSavedGroup(afterRemovalCancel, 'Source field');
  assert.equal(groupAfterRemovalCancel.group.id, groupBeforeRemoval.group.id, 'Cancel must retain the saved Group identity.');
  check('correctness', 'Cancel Group removal preserves exact saved Group workspace, draft, and rows', true,
    { unpivotStepId: groupAfterRemovalCancel.unpivot.id, groupStepId: groupAfterRemovalCancel.group.id, rows: editedGroupRows });

  const groupForRemoval = assertSavedGroup(await readBuilder(), 'Source field');
  const finalRemovalProposal = await proposeGroupRemoval('re-propose Group removal for Apply', groupForRemoval.group);
  assertPreviewRows(finalRemovalProposal, expectedUnpivot, unpivotLabels, 'confirmed Group removal proposal');
  await applyProposal('remove Group and restore Unpivot rows', expectedUnpivot, unpivotLabels);
  builder = await readBuilder();
  const restoredUnpivot = assertSavedUnpivotOnly(builder);
  assert.equal(restoredUnpivot.id, groupForRemoval.unpivot.id, 'Removing Group must preserve the exact Unpivot identity.');
  await assertRenderedRows('removed Group restores exact four Unpivot rows', expectedUnpivot, unpivotLabels);
  check('correctness', 'applying Group removal restores exactly four source-derived Unpivot rows', true,
    { unpivotStepId: restoredUnpivot.id, rows: expectedUnpivot });

  await closeEditorAndVerifyRows('close any restored operation editor before final reload', expectedUnpivot, unpivotLabels);
  await pageAction('reload removed Group and render exact Unpivot rows', table, async () => {
    await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
    await table.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
    if (await table.getAttribute('aria-current') !== 'page') await table.click({ timeout: ACTION_TIMEOUT_MS });
  }, async () => {
    await waitFor(page, id => {
      const preview = document.querySelector('[data-testid="construction-preview"]');
      const selected = document.querySelector(`[data-testid="construction-table-${id}"][aria-current="page"]`);
      return Boolean(selected && preview?.dataset.previewStatus === 'ready' && preview.dataset.previewOutputId === id &&
        preview.dataset.previewReceiptId);
    }, outputId);
    await assertRenderedRows('reloaded Unpivot-only output after Group removal', expectedUnpivot, unpivotLabels);
  });
  builder = await readBuilder();
  const finalUnpivot = assertSavedUnpivotOnly(builder);
  assert.equal(finalUnpivot.id, restoredUnpivot.id, 'Final reload must retain the restored Unpivot identity.');
  check('persistence', 'reload after Group removal retains only Unpivot and all four exact identity/value rows', true,
    { unpivotStepId: finalUnpivot.id, rows: expectedUnpivot });
}
