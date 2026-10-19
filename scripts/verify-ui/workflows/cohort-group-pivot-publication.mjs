import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { browserURL } from './builder-url.mjs';
import {
  COHORT_GROUP_PIVOT_EXPECTED,
  readCohortGroupPivotFixture,
} from '../helpers/cohort-group-pivot-publication-oracle.mjs';

const ACTION_TIMEOUT_MS = 5000;
const previewTableSelector = '[data-testid="preview-table-scroll"] [role="table"]';
const proposalPanelSelector = '[data-testid="construction-proposal-panel"], [data-testid="construction-choice-proposal-panel"]';
const normalize = value => String(value ?? '').replace(/\s+/g, ' ').trim();
const normalizedLabel = value => normalize(value).toLocaleLowerCase('en-US');

export function decodeCohortGroupPivotIntegerValue(value, logicalType) {
  assert.equal(logicalType, 'integer', `Only columns declared as integer can use the published integer decoder, got ${JSON.stringify(logicalType)}.`);
  if (typeof value === 'number') {
    assert(Number.isSafeInteger(value), `Published integer number ${JSON.stringify(value)} is not a safe integer.`);
    return value;
  }
  assert.equal(typeof value, 'string', `Published integer wire value must be a number or canonical decimal string, got ${typeof value}.`);
  assert.match(value, /^(?:0|-?[1-9]\d*)$/, `Published integer string ${JSON.stringify(value)} is not canonical decimal text.`);
  const decoded = Number(value);
  assert(Number.isSafeInteger(decoded), `Published integer string ${JSON.stringify(value)} is outside JavaScript's safe integer range.`);
  assert.equal(String(decoded), value, `Published integer string ${JSON.stringify(value)} cannot round-trip without loss.`);
  return decoded;
}

const readGrid = async page => page.evaluate(() => {
  const proposal = document.querySelector('[data-testid="construction-proposal-preview"][data-preview-status="ready"]');
  const table = proposal?.querySelector('table') ?? document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
  if (!table) return { ready: false, headers: [], rows: [] };
  const clean = value => String(value ?? '').replace(/\s+/g, ' ').trim();
  const headers = proposal
    ? [...table.querySelectorAll('thead th')].map(cell => clean(cell.querySelector('span')?.innerText ?? cell.innerText))
    : [...table.querySelectorAll('[role="columnheader"]')].map(cell => clean(cell.innerText));
  const rows = proposal
    ? [...table.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]')]
      .map(row => [...row.querySelectorAll('td')].map(cell => clean(cell.innerText)))
    : [...table.querySelectorAll('[role="row"]')].slice(1)
      .filter(row => row.querySelector('[role="cell"]'))
      .map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => clean(cell.innerText)));
  return { ready: true, headers, rows };
});

const headerIndex = (headers, expected) => {
  const matches = headers.flatMap((header, index) => normalizedLabel(header) === normalizedLabel(expected) ? [index] : []);
  assert.equal(matches.length, 1, `Expected one exact rendered ${JSON.stringify(expected)} column in ${JSON.stringify(headers)}.`);
  return matches[0];
};

const assertRows = (grid, expectedRows, expectedLabels, phase) => {
  assert(grid.ready, `${phase} did not render a ready Builder or Viewer table.`);
  assert.equal(grid.headers.length, expectedLabels.length, `${phase} has a different exact column count: ${JSON.stringify(grid.headers)}`);
  const byHeader = new Map(expectedLabels.map(label => [headerIndex(grid.headers, label), label]));
  assert.equal(byHeader.size, expectedLabels.length, `${phase} could not map every independent exact label to a unique visible column.`);
  const actual = grid.rows.map(row => [...byHeader.keys()].map(index => normalize(row[index])));
  const expected = expectedRows.map(row => expectedLabels.map(label => normalize(row[label])));
  assert.deepEqual(actual.map(row => JSON.stringify(row)).sort(), expected.map(row => JSON.stringify(row)).sort(),
    `${phase} values or row multiplicity differ from the independent Basic fixture oracle.`);
  return { headers: grid.headers, rows: grid.rows, rowCount: grid.rows.length };
};

const assertCohortGenderRows = (grid, genderLabel, phase) => {
  assert(grid.ready, `${phase} did not render a ready Builder table.`);
  const groupIndex = headerIndex(grid.headers, 'Group label');
  const genderIndex = headerIndex(grid.headers, genderLabel);
  const pairs = grid.rows.map(row => [normalize(row[groupIndex]), normalize(row[genderIndex])])
    .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  assert.deepEqual(pairs, [
    [COHORT_GROUP_PIVOT_EXPECTED.cohortLabel, 'female'],
    [COHORT_GROUP_PIVOT_EXPECTED.cohortLabel, 'male'],
  ].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right))),
  `${phase} must retain the exact cohort label and both source-derived gender values.`);
  return { headers: grid.headers, rows: grid.rows, pairs };
};

const readViewerGrid = async table => {
  const headers = await table.locator('thead th').allInnerTexts();
  const rows = await table.locator('tbody tr').evaluateAll(nodes => nodes.map(row =>
    [...row.querySelectorAll('td')].map(cell => String(cell.innerText ?? '').replace(/\s+/g, ' ').trim())));
  return { ready: true, headers: headers.map(normalize), rows };
};

const assertViewerRows = async (table, expectedRows, expectedLabels, phase) => {
  const grid = await readViewerGrid(table);
  assertRows(grid, expectedRows, expectedLabels, phase);
  return grid;
};

const rowCountReady = count => `(()=>{const table=document.querySelector(${JSON.stringify(previewTableSelector)});return Boolean(table&&table.getAttribute('aria-rowcount')===${JSON.stringify(String(count + 1))}&&!document.body.innerText.includes('Loading your table…')&&!document.body.innerText.includes('Preview failed:'))})()`;

const remainingMs = deadlineAt => {
  const remaining = deadlineAt - Date.now();
  assert(remaining > 0, 'The shared five-second action budget was exhausted.');
  return remaining;
};

const awaitBeforeDeadline = async (work, deadlineAt, label) => {
  const remaining = remainingMs(deadlineAt);
  let timer;
  try {
    return await Promise.race([
      Promise.resolve().then(work),
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(`${label} exceeded the shared five-second budget.`)), remaining);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
};

export async function cohortGroupPivotPublicationWorkflow({
  page,
  action,
  check,
  networkObserver,
  report,
  target,
  seed,
  runID,
}) {
  assert.equal(seed?.fresh, true, 'This case requires a fresh isolated Basic fixture project.');
  assert(target.fixtureProject?.startsWith('loom_dev_verify_'), 'Expected an owned isolated fixture project.');
  assert(target.fixtureGeneration && target.fixtureDir, 'Expected a fixture generation and exact fixture directory.');

  const fixture = readCohortGroupPivotFixture(target.fixtureDir);
  const expectedPatients = fixture.patients;
  const sourceIDs = fixture.patientIDs;
  const expectedViewerRows = fixture.viewerRows;
  const expectedViewerLabels = fixture.viewerColumns;
  const patientSourceBefore = readFileSync(join(target.fixtureDir, 'Patient.ndjson'), 'utf8');
  const observationSourceBefore = readFileSync(join(target.fixtureDir, 'Observation.ndjson'), 'utf8');
  report.target.fixtureOracle = {
    source: 'literal testdata/basic-unpivot-pivot/Patient.ndjson',
    sourceHashes: fixture.sourceHashes,
    patients: expectedPatients,
    patientIDs: sourceIDs,
    cohortLabel: COHORT_GROUP_PIVOT_EXPECTED.cohortLabel,
    expectedGroupRows: fixture.groupRows,
    expectedViewerRows,
  };
  check('correctness', 'Basic fixture independently pins the exact two Patient IDs, genders, and dates', true,
    report.target.fixtureOracle);

  const runAction = (label, locator, perform, after, { editable = false } = {}) => {
    const deadlineAt = Date.now() + ACTION_TIMEOUT_MS;
    return action(label, locator, perform, {
      timeout: ACTION_TIMEOUT_MS,
      budget: ACTION_TIMEOUT_MS,
      ...(editable ? { editable: true } : {}),
      ...(after ? { after: () => awaitBeforeDeadline(() => after(deadlineAt), deadlineAt, `${label} action-to-render`) } : {}),
    });
  };
  const click = (label, locator, after) => runAction(label, locator,
    control => control.click({ timeout: ACTION_TIMEOUT_MS }), after);
  const fill = (label, locator, value, after) => runAction(label, locator,
    control => control.fill(value, { timeout: ACTION_TIMEOUT_MS }), after, { editable: true });
  const select = (label, locator, value, after) => runAction(label, locator,
    control => control.selectOption(value, { timeout: ACTION_TIMEOUT_MS }), after);
  const checkControl = (label, locator, after) => runAction(label, locator,
    control => control.check({ timeout: ACTION_TIMEOUT_MS }), after);
  const waitVisible = locator => locator.waitFor({ state: 'visible', timeout: ACTION_TIMEOUT_MS });
  const waitRows = count => page.waitForFunction(expected => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    return table?.getAttribute('aria-rowcount') === String(expected + 1) &&
      !document.body.innerText.includes('Loading your table…') && !document.body.innerText.includes('Preview failed:');
  }, count, { timeout: ACTION_TIMEOUT_MS });
  const openRowsSettings = async (label, nextActionTestId) => {
    const settings = page.getByLabel('Row definition settings', { exact: true });
    if (await settings.isVisible()) {
      await waitVisible(page.getByTestId(nextActionTestId));
      return;
    }
    const trigger = page.getByTestId('construction-rows-settings-trigger');
    if (!await trigger.isVisible()) {
      const close = page.getByTestId('construction-close-operation-editor');
      await click(`close operation editor before ${label}`, close, () => waitVisible(trigger));
    }
    await click(`open row settings for ${label}`, trigger, () => waitVisible(page.getByTestId(nextActionTestId)));
  };

  await page.goto(browserURL(target, target.fixtureProject, target.bootstrapExplorerId, 'builder'), {
    waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS,
  });
  await page.waitForFunction(() => document.body.innerText.includes('Build your first table') || document.body.innerText.includes('Dataset graph'),
    undefined, { timeout: ACTION_TIMEOUT_MS });
  const newExplorer = page.locator('summary').filter({ hasText: /^New explorer$/ });
  await click('open fresh cohort publication Explorer creation', newExplorer,
    () => waitVisible(page.locator('#new-explorer-name')));
  const explorerTitle = `Verify ${String(runID ?? report.target.project).slice(-10)} cohort Group Pivot publication`;
  const explorerName = page.locator('#new-explorer-name');
  await fill('name fresh cohort Group Pivot Explorer', explorerName, explorerTitle);
  await click('create fresh cohort Group Pivot Explorer', page.getByRole('button', { name: 'Create blank', exact: true }), async () => {
    await page.waitForFunction(title => document.querySelector('select[aria-label="Explorer"] option:checked')?.textContent.trim() === title &&
      document.body.innerText.includes('Build your first table'), explorerTitle, { timeout: ACTION_TIMEOUT_MS });
  });
  const explorer = await page.locator('select[aria-label="Explorer"]').inputValue();
  assert(explorer && explorer !== target.bootstrapExplorerId, 'The case must use a fresh non-bootstrap Explorer.');
  report.target.explorer = explorer;
  report.target.project = target.fixtureProject;
  report.target.generation = target.fixtureGeneration;

  const tableTitle = 'Two Patient cohort';
  await fill('name exact Patient cohort root table', page.locator('#first-table-name'), tableTitle);
  await click('create native Patient cohort root table', page.getByRole('button', { name: 'Choose Patient rows', exact: true }), async () => {
    await waitVisible(page.getByTestId('construction-workspace'));
    await waitRows(expectedPatients.length);
  });
  const apiRoot = `/api/v1/projects/${encodeURIComponent(target.fixtureProject)}/explorers/${encodeURIComponent(explorer)}`;
  const requestJSON = async (path, body, timeoutMs = 30000) => {
    const deadlineAt = Date.now() + timeoutMs;
    const response = await awaitBeforeDeadline(() => fetch(`${target.apiUrl}${apiRoot}${path}`, {
      method: body === undefined ? 'GET' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      signal: AbortSignal.timeout(remainingMs(deadlineAt)),
    }), deadlineAt, `Cohort fixture setup ${path} request`);
    const value = await awaitBeforeDeadline(() => response.json(), deadlineAt, `Cohort fixture setup ${path} JSON body`);
    if (!response.ok) throw new Error(`Cohort fixture setup ${path} returned HTTP ${response.status}: ${JSON.stringify(value).slice(0, 800)}`);
    report.target.fixtureSetupRequests ??= [];
    report.target.fixtureSetupRequests.push({ method: body === undefined ? 'GET' : 'POST', path, status: response.status });
    return value;
  };
  const readBuilder = (timeoutMs) => requestJSON('/authoring/v2/builder', undefined, timeoutMs);
  let builder = await readBuilder();
  assert.equal(builder.catalog?.generation, target.fixtureGeneration, 'Builder catalog must use the exact Basic fixture generation.');
  assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest, 'Builder must return a scoped snapshot.');
  let document = builder.workspace.documents.find(item => item.rootResourceType === 'Patient');
  assert(document?.output?.id, 'Native Patient root table must exist before cohort setup.');
  const outputId = document.output.id;
  report.target.outputId = outputId;

  const selectionProject = target.fixtureProject.replace('-', '/');
  const refs = sourceIDs.map(id => ({ project: selectionProject, generation: target.fixtureGeneration, resourceType: 'Patient', id }));
  const selection = await requestJSON('/selections', {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `verify-cohort-group-pivot-publication-${randomUUID()}`,
    source: { kind: 'resources', resources: { refs } },
  });
  assert.equal(selection.project, selectionProject);
  assert.equal(selection.generation, target.fixtureGeneration);
  assert.equal(selection.resourceType, 'Patient');
  assert.equal(selection.scopeDigest, builder.catalog.authorizationScopeDigest);
  assert.equal(selection.memberCount, sourceIDs.length);
  const selectionPage = await requestJSON(`/selections/${encodeURIComponent(selection.id)}?limit=100`);
  const selectedIDs = (selectionPage.members ?? []).map(member => member.ref?.id).sort();
  assert.deepEqual(selectedIDs, [...sourceIDs].sort(), 'Named cohort selection readback must contain the exact independent Patient IDs.');
  const memberKeys = selectionPage.members.map(member => member.memberKey);
  const cohort = await requestJSON(`/selections/${encodeURIComponent(selection.id)}/explicit-groups`, {
    snapshotToken: builder.catalog.snapshotToken,
    idempotencyKey: `verify-cohort-group-pivot-group-${randomUUID()}`,
    groups: [{ id: 'verify-basic-cohort', label: COHORT_GROUP_PIVOT_EXPECTED.cohortLabel, ordinal: 0, memberIds: memberKeys }],
  });
  assert.equal(cohort.sourceSelectionRevisionId, selection.id);
  assert.equal(cohort.groupCount, 1);
  assert.equal(cohort.memberCount, sourceIDs.length);
  assert.equal(cohort.groups?.[0]?.label, COHORT_GROUP_PIVOT_EXPECTED.cohortLabel);
  assert.equal(cohort.groups?.[0]?.memberCount, sourceIDs.length);
  report.target.cohort = { selectionId: selection.id, revisionId: cohort.revisionId, sourceIDs, label: COHORT_GROUP_PIVOT_EXPECTED.cohortLabel };
  check('correctness', 'named Basic cohort contains exactly the two independently sourced Patients', true, report.target.cohort);

  const outputTab = page.getByTestId(`construction-table-${outputId}`);
  await page.reload({ waitUntil: 'domcontentloaded', timeout: ACTION_TIMEOUT_MS });
  await waitVisible(outputTab);
  if (await outputTab.getAttribute('aria-current') !== 'page') await click('select exact Patient cohort root table after setup', outputTab);
  await page.waitForFunction(rowCountReady(expectedPatients.length), undefined, { timeout: ACTION_TIMEOUT_MS });
  await click('open native cohort row settings', page.getByTestId('construction-rows-settings-trigger'), async () => {
    await waitVisible(page.getByLabel('What should each row represent?', { exact: true }));
  });
  const rowShape = page.getByLabel('What should each row represent?', { exact: true });
  await select('select exact named Basic Patient cohort row shape', rowShape, `explicit:${cohort.revisionId}`, async () => {
    await page.waitForFunction(() => {
      const preview = document.querySelector('[aria-label="Row definition preview"]')?.innerText ?? '';
      const apply = [...document.querySelectorAll('[aria-label="Row definition settings"] button')]
        .find(button => button.innerText.trim() === 'Apply row definition');
      return preview.includes('2 rows → 1 rows') && Boolean(apply && !apply.disabled);
    }, undefined, { timeout: ACTION_TIMEOUT_MS });
  });
  const unmatched = page.getByLabel('Unmatched record policy', { exact: true });
  await select('retain no unmatched cohort rows', unmatched, `explicit:${cohort.revisionId}:ERROR`);
  await click('apply exact named Patient cohort row definition', page.getByRole('button', { name: 'Apply row definition', exact: true }), async () => {
    await waitRows(1);
    const grid = await readGrid(page);
    const labelIndex = headerIndex(grid.headers, 'Group label');
    assert.deepEqual(grid.rows.map(row => row[labelIndex]), [COHORT_GROUP_PIVOT_EXPECTED.cohortLabel]);
  });
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  assert.equal(document?.rows?.kind, 'GROUPS');
  assert.equal(document?.rows?.groups?.source?.explicit?.revisionId, cohort.revisionId);
  check('correctness', 'native cohort row definition renders the exact saved cohort label', true,
    { revisionId: cohort.revisionId, grid: await readGrid(page) });

  await click('open Add columns for exact cohort members', page.getByTestId('construction-action-add-columns'), async () => {
    await waitVisible(page.getByLabel('Add columns editor'));
  });
  await click('open grouped member field choices', page.getByRole('button', { name: 'Fields and related data', exact: true }));
  const valuePolicy = page.getByLabel('Values per grouped row', { exact: true });
  await select('set cohort Gender member policy to ALL', valuePolicy, 'ALL');
  const rawFieldsSummary = page.getByText('Raw FHIR fields (advanced)', { exact: true });
  await click('open raw Patient member fields', rawFieldsSummary);
  const genderChoice = page.getByRole('checkbox', { name: 'Select Patient.gender', exact: true });
  await checkControl('select independent Patient.gender field', genderChoice);
  const addGender = page.getByRole('button', { name: 'Add 1 selected feature', exact: true });
  let genderPreviewReceipt;
  await click('propose Patient.gender ALL member values', addGender, async deadlineAt => {
    await page.waitForFunction(expectedOutputId => {
      const panel = document.querySelector('[data-testid="construction-choice-proposal-panel"]');
      const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
      return panel?.dataset.proposalStatus === 'ready' && preview?.dataset.previewStatus === 'ready' &&
        Boolean(preview.dataset.previewReceiptId) && preview.dataset.previewOutputId === expectedOutputId &&
        preview.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length === 1;
    }, outputId, { timeout: remainingMs(deadlineAt) });
    genderPreviewReceipt = await page.evaluate(() => {
      const panel = document.querySelector('[data-testid="construction-choice-proposal-panel"]');
      const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
      return { status: panel?.dataset.proposalStatus, previewStatus: preview?.dataset.previewStatus,
        receiptId: preview?.dataset.previewReceiptId, outputId: preview?.dataset.previewOutputId };
    });
    assert.equal(genderPreviewReceipt.status, 'ready');
    assert.equal(genderPreviewReceipt.previewStatus, 'ready');
    assert(genderPreviewReceipt.receiptId, 'Ready native Gender preview must expose its receipt identity.');
    assert.equal(genderPreviewReceipt.outputId, outputId, 'Ready native Gender preview must belong to the exact output.');
    const genderProposalGrid = await readGrid(page);
    assert.equal(genderProposalGrid.rows.length, 1, 'ALL Gender must remain one cohort row before expansion.');
    const values = normalize(genderProposalGrid.rows[0][headerIndex(genderProposalGrid.headers, 'Gender')])
      .split(/\s*;\s*/).filter(Boolean).sort();
    assert.deepEqual(values, ['female', 'male'], 'ALL member preview must expose both literal source genders.');
  });
  check('correctness', 'native ready Gender preview has an output receipt and exact literal ALL values', true,
    { receiptId: genderPreviewReceipt.receiptId, outputId, values: ['female', 'male'] });
  const applyColumns = page.getByRole('button', { name: 'Apply columns', exact: true });
  let genderColumn;
  let genderBinding;
  await click('apply exact Patient.gender ALL member field', applyColumns, async deadlineAt => {
    await page.waitForFunction(rowCountReady(1), undefined, { timeout: remainingMs(deadlineAt) });
    const grid = await readGrid(page);
    const values = normalize(grid.rows[0][headerIndex(grid.headers, 'Gender')]).split(/\s*;\s*/).filter(Boolean).sort();
    assert.deepEqual(values, ['female', 'male'], 'Applied ALL Gender field must retain both exact source values.');
    builder = await readBuilder(remainingMs(deadlineAt));
    document = builder.workspace.documents.find(item => item.output.id === outputId);
    genderColumn = document.columns.find(column => column.source?.kind === 'field' && column.source.field?.path === 'gender');
    assert(genderColumn?.columnId && genderColumn.label, 'Applied direct Patient.gender field must have a stable column identity.');
    genderBinding = document.rows.groups.rowValues?.find(binding => binding.columnId === genderColumn.columnId);
    assert.equal(genderBinding?.policy, 'ALL');
  });
  report.target.genderMemberField = { columnId: genderColumn.columnId, label: genderColumn.label, policy: genderBinding.policy, expectedValues: ['female', 'male'] };
  check('correctness', 'saved native cohort field binding retains ALL and the exact two Gender values', true, report.target.genderMemberField);

  const awaitProposal = async (fromIndex, operationKind, expectedRowCount) => {
    const entry = await networkObserver.waitForResponse(candidate => {
      const step = candidate.request?.candidateConstruction?.steps?.at(-1);
      return candidate.index >= fromIndex && candidate.status !== undefined && candidate.response &&
        candidate.path.endsWith('/construction-proposals') && candidate.request?.outputId === outputId &&
        step?.operation?.kind === operationKind && candidate.response.previewStatus === 'READY';
    }, { fromIndex, timeoutMs: ACTION_TIMEOUT_MS });
    assert.equal(entry.status, 200, `${operationKind} proposal must return HTTP 200.`);
    assert.equal(entry.response.outputId, outputId, `${operationKind} response must belong to the exact Builder output.`);
    assert(entry.response.proposalId && entry.response.preview?.receiptId,
      `${operationKind} response must carry its proposal and receipt identities.`);
    const candidate = entry.request.candidateConstruction;
    assert.equal(candidate.steps.at(-1).operation.kind, operationKind);
    assert.equal(entry.response.preview.rowCount, expectedRowCount,
      `${operationKind} response must have the exact independent fixture row count.`);
    await page.waitForFunction(expected => {
      const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
      const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
      return panel?.dataset.proposalStatus === 'ready' && panel.dataset.proposalId === expected.proposalId &&
        preview?.dataset.previewStatus === 'ready' && preview.dataset.previewReceiptId === expected.receiptId &&
        preview.dataset.previewOutputId === expected.outputId &&
        preview.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length === expected.rowCount;
    }, { proposalId: entry.response.proposalId, receiptId: entry.response.preview.receiptId, outputId, rowCount: expectedRowCount },
    { timeout: ACTION_TIMEOUT_MS });
    return entry;
  };

  await openRowsSettings('exact cohort Gender expansion', 'construction-action-expand-rows');
  const expandChoice = page.getByTestId('construction-action-expand-rows');
  const expandFrom = networkObserver.requests.length;
  let expandEntry;
  await click('choose native EXPAND for exact cohort Gender values', expandChoice, async () => {
    await waitVisible(page.getByLabel('Repeated field', { exact: true }));
    expandEntry = await awaitProposal(expandFrom, 'EXPAND', 2);
  });
  const expandStep = expandEntry.request.candidateConstruction.steps.at(-1);
  assert.equal(expandStep.operation.expand.inputColumnId, genderColumn.columnId,
    'Native EXPAND must bind the exact saved Patient.gender ALL column.');
  const expandOutput = expandStep.outputs.find(column => column.id === expandStep.operation.expand.outputColumnId);
  assert(expandOutput?.label, 'Native EXPAND must return a stable output identity and label.');
  let expandedGrid = await readGrid(page);
  const expanded = assertCohortGenderRows(expandedGrid, expandOutput.label, 'Initial cohort EXPAND preview');
  const applyExpand = page.getByTestId('construction-apply-proposal');
  await click('apply native cohort Gender EXPAND', applyExpand, async () => {
    await page.waitForFunction(rowCountReady(2), undefined, { timeout: ACTION_TIMEOUT_MS });
    expandedGrid = await readGrid(page);
    assertCohortGenderRows(expandedGrid, expandOutput.label, 'Applied cohort EXPAND rows');
  });
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  let savedSteps = document.construction?.steps ?? [];
  assert.deepEqual(savedSteps.map(step => step.operation.kind), ['EXPAND']);
  assert.equal(savedSteps[0].operation.expand.inputColumnId, genderColumn.columnId);
  assert.equal(savedSteps[0].operation.expand.outputColumnId, expandOutput.id);
  report.target.expansion = { stepId: savedSteps[0].id, inputColumnId: genderColumn.columnId, outputColumnId: expandOutput.id, label: expandOutput.label, rows: expanded.pairs };
  check('correctness', 'native cohort EXPAND produces exactly the two literal Patient Gender rows', true, report.target.expansion);

  await openRowsSettings('native cohort Group', 'construction-action-group-rows');
  await click('open native Group editor for cohort Gender counts', page.getByTestId('construction-action-group-rows'), async () => {
    await waitVisible(page.getByLabel('Summary 1', { exact: true }));
  });
  const summary = page.getByLabel('Summary 1', { exact: true });
  assert.equal(await summary.inputValue(), 'COUNT_ROWS', 'Group must use the native COUNT_ROWS aggregate.');
  const groupLabels = await page.locator('input[type="checkbox"][aria-label^="Group by "]').evaluateAll(inputs =>
    inputs.map(input => input.getAttribute('aria-label').slice('Group by '.length)).sort());
  assert(groupLabels.includes('Group label') && groupLabels.includes(expandOutput.label),
    `Native Group must offer the cohort label and exact expanded Gender output: ${JSON.stringify(groupLabels)}`);
  const groupLabelChoice = page.getByRole('checkbox', { name: 'Group by Group label', exact: true });
  await checkControl('group exact cohort count by saved cohort label', groupLabelChoice,
    () => page.waitForFunction(() => document.querySelector('input[aria-label="Group by Group label"]')?.checked === true,
      undefined, { timeout: ACTION_TIMEOUT_MS }));
  const genderGroupChoice = page.getByRole('checkbox', { name: `Group by ${expandOutput.label}`, exact: true });
  const groupFrom = networkObserver.requests.length;
  let groupEntry;
  await checkControl('complete native Group by expanded Patient Gender', genderGroupChoice, async () => {
    await page.waitForFunction(label => document.querySelector(`input[aria-label=${JSON.stringify(`Group by ${label}`)}]`)?.checked === true,
      expandOutput.label, { timeout: ACTION_TIMEOUT_MS });
    groupEntry = await awaitProposal(groupFrom, 'GROUP', 2);
  });
  const groupStep = groupEntry.request.candidateConstruction.steps.at(-1);
  assert.equal(groupStep.operation.group.aggregates?.length, 1);
  assert.equal(groupStep.operation.group.aggregates[0].operation, 'COUNT_ROWS');
  const groupKeyOutputs = groupStep.operation.group.keys.map(key => groupStep.outputs.find(output => output.id === key.outputColumnId));
  const groupedGenderOutput = groupKeyOutputs.find(output => output?.label === expandOutput.label);
  const groupedLabelOutput = groupKeyOutputs.find(output => normalizedLabel(output?.label) === 'group label');
  const rowCountOutput = groupStep.outputs.find(output => output.id === groupStep.operation.group.aggregates[0].outputColumnId);
  assert(groupedGenderOutput?.id && groupedLabelOutput?.id && rowCountOutput?.id,
    `GROUP must retain exact cohort label, expanded Gender, and COUNT_ROWS outputs: ${JSON.stringify(groupStep)}`);
  const groupPreview = assertRows(await readGrid(page), fixture.groupRows.map(row => ({
    'Group label': row['Group label'], [expandOutput.label]: row.Gender, [rowCountOutput.label]: row['Row count'],
  })), ['Group label', expandOutput.label, rowCountOutput.label], 'Native cohort COUNT_ROWS Group proposal');
  check('correctness', 'native cohort Group proposal matches exact source-derived gender counts', true,
    { groupStepId: groupStep.id, keys: groupStep.operation.group.keys, aggregate: groupStep.operation.group.aggregates[0], rows: groupPreview });
  await click('apply native cohort COUNT_ROWS Group', page.getByTestId('construction-apply-proposal'), async () => {
    await page.waitForFunction(rowCountReady(2), undefined, { timeout: ACTION_TIMEOUT_MS });
    assertRows(await readGrid(page), fixture.groupRows.map(row => ({
      'Group label': row['Group label'], [expandOutput.label]: row.Gender, [rowCountOutput.label]: row['Row count'],
    })), ['Group label', expandOutput.label, rowCountOutput.label], 'Applied native cohort Group rows');
  });
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  savedSteps = document.construction?.steps ?? [];
  assert.deepEqual(savedSteps.map(step => step.operation.kind), ['EXPAND', 'GROUP']);
  assert.equal(savedSteps[1].id, groupStep.id);
  assert.equal(savedSteps[1].operation.group.aggregates[0].operation, 'COUNT_ROWS');
  report.target.group = { stepId: groupStep.id, cohortLabelColumnId: groupedLabelOutput.id,
    genderColumnId: groupedGenderOutput.id, countColumnId: rowCountOutput.id, rows: fixture.groupRows };
  check('persistence', 'saved cohort EXPAND→GROUP composition retains exact stable field identities and counts', true, report.target.group);

  await openRowsSettings('native cohort Pivot', 'construction-action-pivot-rows');
  const pivotControl = page.getByTestId('construction-action-pivot-rows');
  await click('open native Pivot editor for cohort Gender counts', pivotControl, async () => {
    await waitVisible(page.getByLabel('Pivot category field', { exact: true }));
  });
  const pivotGroup = page.getByRole('checkbox', { name: `Pivot group ${groupedLabelOutput.label}`, exact: true });
  await checkControl('keep one Pivot row for the exact named cohort', pivotGroup,
    () => page.waitForFunction(label => document.querySelector(`input[aria-label=${JSON.stringify(`Pivot group ${label}`)}]`)?.checked === true,
      groupedLabelOutput.label, { timeout: ACTION_TIMEOUT_MS }));
  const categorySelect = page.getByLabel('Pivot category field', { exact: true });
  const valuesSelect = page.getByLabel('Pivot values field', { exact: true });
  const categoryOptions = await categorySelect.locator('option').evaluateAll(options => {
    const normalizeText = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    return options.map(option => ({ value: option.value, label: normalizeText(option.textContent), disabled: option.disabled }));
  });
  const categoryOption = categoryOptions.filter(option => normalizedLabel(option.label) === normalizedLabel(groupedGenderOutput.label) && !option.disabled);
  assert.equal(categoryOption.length, 1, `Pivot must expose the exact grouped Gender output: ${JSON.stringify(categoryOptions)}`);
  const valueOptions = await valuesSelect.locator('option').evaluateAll(options => {
    const normalizeText = value => String(value ?? '').replace(/\s+/g, ' ').trim();
    return options.map(option => ({ value: option.value, label: normalizeText(option.textContent), disabled: option.disabled }));
  });
  const countOption = valueOptions.filter(option => option.value === rowCountOutput.id && !option.disabled);
  assert.equal(countOption.length, 1, `Pivot must expose the exact COUNT_ROWS output as its values field: ${JSON.stringify(valueOptions)}`);
  await select('choose exact COUNT_ROWS Pivot values', valuesSelect, rowCountOutput.id);
  const pivotFrom = networkObserver.requests.length;
  let pivotEntry;
  await select('choose exact grouped Gender Pivot categories', categorySelect, categoryOption[0].value, async () => {
    pivotEntry = await awaitProposal(pivotFrom, 'PIVOT', 1);
  });
  const pivotStep = pivotEntry.request.candidateConstruction.steps.at(-1);
  assert.deepEqual(pivotStep.operation.pivot.groupKeyIds, [groupedLabelOutput.id]);
  assert.equal(pivotStep.operation.pivot.categoryColumnId, groupedGenderOutput.id);
  assert.equal(pivotStep.operation.pivot.valueColumnId, rowCountOutput.id);
  assert.equal(pivotStep.operation.pivot.duplicatePolicy, 'ERROR', 'Unique cohort/category cells must use the native ERROR duplicate policy.');
  const pivotCategories = pivotStep.operation.pivot.categories.map(category => category.key?.string ?? category.key?.value ?? category.key);
  assert.deepEqual(pivotCategories.map(String).sort(), ['female', 'male']);
  const pivotPreview = assertRows(await readGrid(page), expectedViewerRows, expectedViewerLabels, 'Native cohort Group→Pivot proposal');
  check('correctness', 'native cohort Group→Pivot preview is the exact one-row Female/Male count table', true,
    { pivotStepId: pivotStep.id, groupKeyIds: pivotStep.operation.pivot.groupKeyIds, categoryColumnId: pivotStep.operation.pivot.categoryColumnId,
      valueColumnId: pivotStep.operation.pivot.valueColumnId, categories: pivotStep.operation.pivot.categories, rows: pivotPreview });
  await click('apply native cohort Group→Pivot', page.getByTestId('construction-apply-proposal'), async () => {
    await page.waitForFunction(rowCountReady(1), undefined, { timeout: ACTION_TIMEOUT_MS });
    assertRows(await readGrid(page), expectedViewerRows, expectedViewerLabels, 'Applied cohort Group→Pivot rows');
  });
  builder = await readBuilder();
  document = builder.workspace.documents.find(item => item.output.id === outputId);
  savedSteps = document.construction?.steps ?? [];
  assert.deepEqual(savedSteps.map(step => step.operation.kind), ['EXPAND', 'GROUP', 'PIVOT']);
  assert.equal(savedSteps[1].id, groupStep.id);
  assert.equal(savedSteps[2].id, pivotStep.id);
  assert.deepEqual(savedSteps[2].operation.pivot.groupKeyIds, [groupedLabelOutput.id]);
  assert.equal(savedSteps[2].operation.pivot.categoryColumnId, groupedGenderOutput.id);
  assert.equal(savedSteps[2].operation.pivot.valueColumnId, rowCountOutput.id);
  check('persistence', 'saved cohort Group→Pivot composition retains exact operation order, categories, and output identities', true,
    { steps: savedSteps.map(step => ({ id: step.id, kind: step.operation.kind })), pivotStep: savedSteps[2].operation.pivot });

  const closeEditor = page.getByRole('button', { name: 'Close operation editor', exact: true });
  if (await closeEditor.count() && await closeEditor.isVisible()) {
    await click('close native Pivot editor before Builder reload', closeEditor, () => waitVisible(outputTab));
  }
  const builderReloadStarted = performance.now();
  const builderReloadDeadlineAt = Date.now() + ACTION_TIMEOUT_MS;
  await action('reload saved cohort Group→Pivot Builder output', outputTab,
    () => page.reload({ waitUntil: 'domcontentloaded', timeout: remainingMs(builderReloadDeadlineAt) }), {
      timeout: remainingMs(builderReloadDeadlineAt),
      budget: ACTION_TIMEOUT_MS,
      after: () => awaitBeforeDeadline(async () => {
        await outputTab.waitFor({ state: 'visible', timeout: remainingMs(builderReloadDeadlineAt) });
        const selectedOutput = await awaitBeforeDeadline(() => outputTab.getAttribute('aria-current'), builderReloadDeadlineAt,
          'Reloaded Builder output tab state');
        if (selectedOutput !== 'page') await outputTab.click({ timeout: remainingMs(builderReloadDeadlineAt) });
        await page.waitForFunction(rowCountReady(1), undefined, { timeout: remainingMs(builderReloadDeadlineAt) });
        const reloadedBuilder = await readBuilder(remainingMs(builderReloadDeadlineAt));
        const reloadedDocument = reloadedBuilder.workspace.documents.find(item => item.output.id === outputId);
        const reloadedSteps = reloadedDocument?.construction?.steps ?? [];
        assert.deepEqual(reloadedSteps.map(step => step.operation.kind), ['EXPAND', 'GROUP', 'PIVOT']);
        assert.equal(reloadedDocument.rows.groups.source.explicit.revisionId, cohort.revisionId);
        assert.equal(reloadedSteps[0].operation.expand.inputColumnId, genderColumn.columnId);
        assert.equal(reloadedSteps[1].operation.group.aggregates[0].operation, 'COUNT_ROWS');
        assert.deepEqual(reloadedSteps[2].operation.pivot.groupKeyIds, [groupedLabelOutput.id]);
        assert.equal(reloadedSteps[2].operation.pivot.categoryColumnId, groupedGenderOutput.id);
        assert.equal(reloadedSteps[2].operation.pivot.valueColumnId, rowCountOutput.id);
        const reloadedGrid = await awaitBeforeDeadline(() => readGrid(page), builderReloadDeadlineAt,
          'Reloaded saved cohort Group→Pivot rows');
        assertRows(reloadedGrid, expectedViewerRows, expectedViewerLabels, 'Reloaded saved cohort Group→Pivot rows');
      }, builderReloadDeadlineAt, 'Builder reload, saved bindings, and rendered rows'),
    });
  const builderReloadMs = performance.now() - builderReloadStarted;
  check('performance', 'saved cohort Group→Pivot Builder reload renders the exact rows within five seconds', builderReloadMs <= ACTION_TIMEOUT_MS,
    { elapsedMs: Math.round(builderReloadMs), budgetMs: ACTION_TIMEOUT_MS });
  check('persistence', 'exact named cohort, EXPAND, Group, Pivot, and rendered rows survive Builder reload', true,
    { cohortRevisionId: cohort.revisionId, outputId, stepIds: [expandStep.id, groupStep.id, pivotStep.id], rows: await readGrid(page) });

  const publishPath = `${apiRoot}/authoring/v2/publish`;
  const publishDeadlineAt = Date.now() + ACTION_TIMEOUT_MS;
  const publishResponsePromise = page.waitForResponse(response => response.request().method() === 'POST' &&
    new URL(response.url()).origin === new URL(target.uiUrl).origin && new URL(response.url()).pathname === publishPath,
  { timeout: remainingMs(publishDeadlineAt) });
  const publish = page.getByRole('button', { name: 'Publish', exact: true });
  let publishResponse;
  let publishPayload;
  await click('publish exact cohort Group→Pivot table through Builder UI', publish, async actionDeadlineAt => {
    const deadlineAt = Math.min(actionDeadlineAt, publishDeadlineAt);
    publishResponse = await awaitBeforeDeadline(() => publishResponsePromise, deadlineAt, 'Native Publish response');
    assert(publishResponse.status() >= 200 && publishResponse.status() < 300,
      `Native Publish returned HTTP ${publishResponse.status()}.`);
    publishPayload = await awaitBeforeDeadline(() => publishResponse.json(), deadlineAt, 'Native Publish JSON body');
    const publishRequest = publishResponse.request().postDataJSON();
    assert.equal(publishPayload.kind, 'ExplorerBuilderPublication');
    assert(publishPayload.apiVersion && publishPayload.receiptId && publishPayload.revisionId,
      'Native Publish response must include its API, receipt, and revision identities.');
    assert.equal(publishPayload.receiptId, publishRequest?.receiptId,
      'Native Publish response must identify the exact receipt submitted by Builder.');
    assert.equal(publishPayload.state, 'ACTIVE', 'Native Publish must activate the exact Explorer revision.');
    assert.deepEqual(publishPayload.diagnostics, [], 'Successful native Publish must return no error diagnostics.');
    const output = publishPayload.outputs?.filter(candidate => candidate.outputId === outputId) ?? [];
    assert.equal(output.length, 1, 'Native Publish must return one exact authored Builder output.');
    assert.equal(output[0].state, 'READY', 'Native Publish must mark the exact output ready.');
    await page.waitForFunction(() => {
      const button = [...document.querySelectorAll('button')].find(item => item.innerText.trim() === 'Publish');
      return Boolean(button?.disabled);
    }, undefined, { timeout: remainingMs(deadlineAt) });
  });
  check('correctness', 'native Publish returns an active revision for the exact cohort Group→Pivot output', true,
    { status: publishResponse.status(), path: new URL(publishResponse.url()).pathname, receiptId: publishPayload.receiptId,
      revisionId: publishPayload.revisionId, state: publishPayload.state, explorer, outputId });

  const graphPath = '/graphql/graph';
  const runtimePath = `${apiRoot}`;
  const matchesRuntime = response => response.request().method() === 'GET' &&
    new URL(response.url()).origin === new URL(target.uiUrl).origin && new URL(response.url()).pathname === runtimePath;
  const matchesOutputQuery = response => {
    try {
      const request = response.request();
      const body = request.postDataJSON()?.variables?.input;
      return request.method() === 'POST' && new URL(request.url()).origin === new URL(target.uiUrl).origin &&
        new URL(request.url()).pathname === graphPath && body?.projectId === target.fixtureProject &&
        body?.selector?.output === outputId;
    } catch {
      return false;
    }
  };
  const verifyViewerCycle = async ({ reload: shouldReload, label }) => {
    const started = performance.now();
    const deadlineAt = Date.now() + ACTION_TIMEOUT_MS;
    const runtimePromise = page.waitForResponse(matchesRuntime, { timeout: remainingMs(deadlineAt) });
    const queryPromise = page.waitForResponse(matchesOutputQuery, { timeout: remainingMs(deadlineAt) });
    if (shouldReload) await page.reload({ waitUntil: 'domcontentloaded', timeout: remainingMs(deadlineAt) });
    else await page.goto(browserURL(target, target.fixtureProject, explorer, 'viewer'), {
      waitUntil: 'domcontentloaded', timeout: remainingMs(deadlineAt),
    });
    const [runtimeResponse, queryResponse] = await awaitBeforeDeadline(() => Promise.all([runtimePromise, queryPromise]),
      deadlineAt, `${label} runtime and GraphQL response capture`);
    assert(runtimeResponse.ok(), `${label} runtime returned HTTP ${runtimeResponse.status()}.`);
    assert(queryResponse.status() >= 200 && queryResponse.status() < 300, `${label} query returned HTTP ${queryResponse.status()}.`);
    const [runtimeEnvelope, queryPayload] = await awaitBeforeDeadline(() => Promise.all([runtimeResponse.json(), queryResponse.json()]),
      deadlineAt, `${label} response JSON bodies`);
    const runtime = runtimeEnvelope.runtime;
    assert(runtime && Array.isArray(runtime.outputs), `${label} runtime must expose published outputs.`);
    assert.equal(runtime.generation ?? runtime.publication?.generation, target.fixtureGeneration,
      `${label} runtime must retain the exact Basic fixture generation.`);
    const output = runtime.outputs.find(candidate => candidate.outputId === outputId);
    assert(output, `${label} runtime must expose the exact published Builder output.`);
    const queryInput = queryResponse.request().postDataJSON()?.variables?.input;
    assert.equal(queryInput.projectId, target.fixtureProject);
    assert.equal(queryInput.selector?.output, outputId, `${label} query must select the exact published Group→Pivot output.`);
    assert.equal(queryInput.selector?.recipe, output.selector?.recipe, `${label} selector must retain the exact published recipe.`);
    assert.equal(queryInput.selector?.translationVersion, output.selector?.translationVersion, `${label} selector must retain the exact translation version.`);
    assert(!Array.isArray(queryPayload.errors) || queryPayload.errors.length === 0, `${label} GraphQL query must have no errors.`);
    const connection = queryPayload.data?.dataframeRows;
    assert(connection && Array.isArray(connection.columns) && Array.isArray(connection.rows), `${label} GraphQL query must return dataframe rows.`);
    const visibleColumns = output.table.columns.filter(binding => binding.visible && output.columns.some(column => column.column === binding.column && column.visible));
    assert.equal(visibleColumns.length, expectedViewerLabels.length, `${label} runtime visible columns differ from the literal oracle.`);
    const selectedColumns = expectedViewerLabels.map(label => {
      const matches = output.columns.filter(column => column.label === label);
      assert.equal(matches.length, 1, `${label} must map to one exact published output column.`);
      assert(visibleColumns.some(binding => binding.column === matches[0].column), `${label} must be visible in the published Viewer table.`);
      return matches[0];
    });
    assert.equal(new Set(selectedColumns.map(column => column.column)).size, selectedColumns.length,
      `${label} runtime column mapping must be unique.`);
    const materializationColumns = output.materialization?.columns;
    assert(Array.isArray(materializationColumns), `${label} runtime output must expose its exact materialization column types.`);
    const selectedMaterializationColumns = selectedColumns.map(column => {
      const matches = materializationColumns.filter(candidate => candidate.name === column.column);
      assert.equal(matches.length, 1, `${label} ${column.label} must map to one exact materialization column.`);
      assert.equal(matches[0].logicalType, column.logicalType,
        `${label} ${column.label} logical type must agree between runtime and materialization metadata.`);
      const expectedLogicalType = typeof expectedViewerRows[0]?.[column.label] === 'number' ? 'integer' : 'string';
      assert.equal(column.logicalType, expectedLogicalType,
        `${label} ${column.label} runtime type must match the independent literal oracle.`);
      return matches[0];
    });
    const indices = selectedColumns.map(column => connection.columns.indexOf(column.column));
    assert(indices.every(index => index >= 0), `${label} response must include each exact published output column.`);
    const wireResponseRows = connection.rows.map(row => Object.fromEntries(selectedColumns.map((column, index) => [
      expectedViewerLabels[index], Array.isArray(row) ? row[indices[index]] : row?.[column.column],
    ])));
    let responseRows;
    try {
      responseRows = wireResponseRows.map(row => Object.fromEntries(selectedColumns.map((column, index) => {
        const value = row[expectedViewerLabels[index]];
        return [expectedViewerLabels[index], column.logicalType === 'integer'
          ? decodeCohortGroupPivotIntegerValue(value, column.logicalType)
          : value];
      })));
    } catch (error) {
      assert.fail(`${label} GraphQL integer wire decoding failed: ${error.message}; logicalTypes=${JSON.stringify(selectedColumns.map(column => ({ column: column.column, logicalType: column.logicalType })))}; wireRows=${JSON.stringify(wireResponseRows)}; expected=${JSON.stringify(expectedViewerRows)}`);
    }
    assert.deepEqual(responseRows, expectedViewerRows, `${label} GraphQL rows must match exact independent cohort counts.`);
    const table = page.locator('table[aria-label$=" results"]');
    await table.waitFor({ state: 'visible', timeout: remainingMs(deadlineAt) });
    await page.waitForFunction(expectedCount => document.querySelector('table[aria-label$=" results"] tbody')?.querySelectorAll('tr').length === expectedCount,
      expectedViewerRows.length, { timeout: remainingMs(deadlineAt) });
    const visible = await awaitBeforeDeadline(() => assertViewerRows(table, expectedViewerRows, expectedViewerLabels, `${label} visible Viewer rows`),
      deadlineAt, `${label} rendered Viewer rows`);
    const elapsedMs = performance.now() - started;
    assert(elapsedMs <= ACTION_TIMEOUT_MS, `${label} navigation/query/render took ${Math.round(elapsedMs)} ms, over five seconds.`);
    check('performance', `${label} navigation, exact GraphQL response, and visible rows complete within five seconds`, true,
      { elapsedMs: Math.round(elapsedMs), budgetMs: ACTION_TIMEOUT_MS, outputId, requestSelector: queryInput.selector, rows: visible.rows });
    check('correctness', `${label} actual Viewer GraphQL response matches the independent one-row cohort oracle`, true,
      { status: queryResponse.status(), outputId, columns: connection.columns,
        logicalTypes: selectedColumns.map((column, index) => ({ column: column.column, logicalType: column.logicalType,
          clickhouseType: selectedMaterializationColumns[index].clickhouseType })),
        wireRows: wireResponseRows, rows: responseRows });
    check('correctness', `${label} actual Viewer table renders the exact published cohort rows`, true,
      { outputId, headers: visible.headers, rows: visible.rows, expectedViewerRows });
    return { runtime, output, queryInput, wireResponseRows, responseRows, visible, elapsedMs };
  };

  const initialViewer = await verifyViewerCycle({ reload: false, label: 'published Viewer initial load' });
  report.target.publishedViewer = {
    outputId,
    generation: initialViewer.runtime.generation ?? initialViewer.runtime.publication?.generation,
    selector: initialViewer.queryInput.selector,
    columns: initialViewer.output.columns.map(column => ({ column: column.column, label: column.label })),
    wireRows: initialViewer.wireResponseRows,
    rows: initialViewer.responseRows,
  };
  const reloadedViewer = await verifyViewerCycle({ reload: true, label: 'published Viewer reload' });
  check('persistence', 'published cohort Group→Pivot output and exact rows survive full Viewer reload',
    reloadedViewer.responseRows.length === 1 && JSON.stringify(reloadedViewer.responseRows) === JSON.stringify(expectedViewerRows),
    { outputId, wireRows: reloadedViewer.wireResponseRows, rows: reloadedViewer.responseRows, expectedViewerRows });

  assert.equal(readFileSync(join(target.fixtureDir, 'Patient.ndjson'), 'utf8'), patientSourceBefore,
    'The independent Patient fixture source must remain byte-for-byte unchanged.');
  assert.equal(readFileSync(join(target.fixtureDir, 'Observation.ndjson'), 'utf8'), observationSourceBefore,
    'The empty Observation fixture source must remain byte-for-byte unchanged.');
  check('correctness', 'Basic Patient-only fixture stayed unchanged during cohort Group/Pivot publication', true,
    { sourceHashes: fixture.sourceHashes });
}
