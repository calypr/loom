import assert from 'node:assert/strict';
import { join } from 'node:path';
import { readFileSync } from 'node:fs';
import { browserURL } from './builder-url.mjs';
import { configureNativePage } from '../helpers/playwright-authoring-page.mjs';
import { findRenderedBuilderHeaderIndex, waitForBuilderRenderedGrid } from '../helpers/builder-rendered-grid.mjs';

const expectedPatientIDs = ['dev-patient-001', 'dev-patient-002'];
const proposalPanel = '[data-testid="construction-proposal-panel"]';
const proposalPreview = '[data-testid="construction-proposal-preview"]';
const renderedTableSelector = '[data-testid="preview-table-scroll"] [role="table"]';

const readNDJSON = path => readFileSync(path, 'utf8')
  .split(/\r?\n/)
  .filter(Boolean)
  .map(line => JSON.parse(line));

export const openFreshPatientTable = async ({ page, report, check, action }, context, purpose) => {
  configureNativePage(page);
  assert.equal(context.custom, false, 'This case requires a fresh owned fixture project.');
  assert.equal(context.seed?.fresh, true, 'This case must use a fresh verification project.');
  assert(context.target.fixtureDir && context.target.fixtureGeneration, 'Expected an isolated fixture generation.');

  const fixtureIDs = readNDJSON(join(context.target.fixtureDir, 'Patient.ndjson'))
    .filter(resource => resource?.resourceType === 'Patient')
    .map(resource => resource.id)
    .sort();
  assert.deepEqual(fixtureIDs, expectedPatientIDs, 'fresh Patient.ndjson must provide the exact two-record identity oracle');
  const fixtureOracle = {
    source: 'fresh project Patient.ndjson',
    path: join(context.target.fixtureDir, 'Patient.ndjson'),
    project: context.target.fixtureProject,
    generation: context.target.fixtureGeneration,
    resourceType: 'Patient',
    patientIDs: fixtureIDs,
    expectedCountRows: fixtureIDs.length,
  };
  report.target.fixtureRawOracle = fixtureOracle;
  report.target.qaIsolation = {
    project: context.target.fixtureProject,
    generation: context.target.fixtureGeneration,
    fixtureDir: context.target.fixtureDir,
    fixtureOracle,
    bootstrapExplorerId: context.target.bootstrapExplorerId,
    explorer: null,
    workflowMutations: [],
    cleanupMutations: [],
    workflowCleanup: 'native Group removal restores the original Patient row shape; the harness retains the fresh loom_dev_verify project and its Explorers after the case',
  };
  check('correctness', 'basic Patient fixture contains the two independently known records',
    JSON.stringify(fixtureIDs) === JSON.stringify(expectedPatientIDs), fixtureOracle);

  const explorerName = `Verify ${context.runID.slice(-10)} ${purpose}`;
  const bootstrapBuilderPath = `/api/v1/projects/${encodeURIComponent(context.target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(context.target.bootstrapExplorerId)}/authoring/v2/builder`;
  const bootstrapResponsePromise = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.origin === new URL(context.target.uiUrl).origin
      && url.pathname === bootstrapBuilderPath && response.request().method() === 'GET';
  }, { timeout: 5000 });
  await page.goto(browserURL(context.target, context.target.fixtureProject, context.target.bootstrapExplorerId, 'builder'),
    { waitUntil: 'domcontentloaded' });
  const bootstrapResponse = await bootstrapResponsePromise;
  assert.equal(bootstrapResponse.status(), 200,
    'bootstrap Builder state request must complete before Explorer creation');
  const bootstrapRequestBody = await bootstrapResponse.json();
  assert(bootstrapRequestBody, 'bootstrap Builder state response must be readable');

  const newExplorer = page.getByText('New explorer', { exact: true });
  await newExplorer.waitFor({ state: 'visible' });
  await action('open Explorer creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
    timeout: 5000,
    budget: 5000,
  });
  const explorerNameControl = page.locator('#new-explorer-name');
  await action('name Explorer', explorerNameControl, () => explorerNameControl.fill(explorerName), {
    editable: true,
    timeout: 5000,
    budget: 5000,
  });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await action('create blank Explorer', createBlank, () => createBlank.click(), {
    after: () => page.waitForFunction(expectedTitle => {
      const select = document.querySelector('select[aria-label="Explorer"]');
      return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
    }, explorerName, { timeout: 5000 }),
    timeout: 5000,
    budget: 5000,
  });
  const explorerControl = page.getByRole('combobox', { name: 'Explorer', exact: true });
  const explorer = await explorerControl.inputValue();
  assert(explorer && explorer !== context.target.bootstrapExplorerId,
    'Group entry requires a newly created Explorer');
  report.target.explorer = explorer;
  report.target.qaIsolation.explorer = explorer;
  report.target.qaIsolation.workflowMutations.push('native UI created a blank Explorer in the fresh fixture project');

  const tableName = page.locator('#first-table-name');
  await action('name Patient table', tableName, () => tableName.fill('Patients'), {
    editable: true,
    timeout: 5000,
    budget: 5000,
  });
  const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  await action('choose Patient rows', choosePatients, () => choosePatients.click(), {
    after: () => page.waitForFunction(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return table && Number(table.getAttribute('aria-rowcount')) > 1
        && !document.body.innerText.includes('Loading your table…');
    }, undefined, { timeout: 5000 }),
    timeout: 5000,
    budget: 5000,
  });
  report.target.qaIsolation.workflowMutations.push('native UI created a Patient root table from the two fixture Patient records');

  const apiRoot = `/api/v1/projects/${encodeURIComponent(context.target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(explorer)}`;
  const builderResponse = await fetch(context.target.apiUrl + `${apiRoot}/authoring/v2/builder`, {
    signal: AbortSignal.timeout(30000),
  });
  const builder = await builderResponse.json();
  if (!builderResponse.ok) throw new Error(`Builder read returned HTTP ${builderResponse.status}.`);
  assert.equal(builder.catalog?.generation, context.target.fixtureGeneration,
    'Builder must expose the exact isolated fixture generation.');
  assert(builder.catalog?.snapshotToken && builder.catalog?.authorizationScopeDigest,
    'Expected a scoped current Builder snapshot.');
  const document = builder.workspace?.documents?.find(item => item.rootResourceType === 'Patient');
  assert(document?.output?.id, 'Fresh Builder Explorer must contain its native Patient table.');
  const sourceIdColumn = document.columns?.find(column => column.source?.field?.path === 'id');
  const sourceIdColumnId = sourceIdColumn?.columnId ?? sourceIdColumn?.id;
  assert(sourceIdColumnId && sourceIdColumn.label, 'Fresh Patient table must expose its native Patient.id column.');
  const outputId = document.output.id;
  report.target.table = { outputId, rootResourceType: 'Patient', rawRowCount: fixtureIDs.length,
    patientIDColumn: { id: sourceIdColumnId, label: sourceIdColumn.label } };
  return { fixtureIDs, fixtureOracle, explorerName, explorer, apiRoot, builder, document, outputId, sourceIdColumn, sourceIdColumnId };
};

export const groupEntryWorkflow = async (workflow, context) => {
  const { page, report, check, action } = workflow;
  const { fixtureIDs, explorer, apiRoot, builder, outputId, sourceIdColumn, sourceIdColumnId } =
    await openFreshPatientTable(workflow, context, 'group entry');

  const groupProposalPath = `${apiRoot}/authoring/v2/construction-proposals`;
  const groupRequests = [];
  const groupRequestsByRequest = new Map();
  let requestSequence = 0;
  page.on('request', request => {
    let url;
    let body;
    try {
      url = new URL(request.url());
      body = request.postDataJSON();
    } catch {
      return;
    }
    if (url.origin !== new URL(context.target.uiUrl).origin || url.pathname !== groupProposalPath
      || request.method() !== 'POST' || body?.outputId !== outputId) return;
    const sequence = ++requestSequence;
    const entry = {
      requestObjectIdentity: `playwright-request-${sequence}`,
      requestId: request.headers()['x-request-id'] ?? null,
      body,
      startedAt: Date.now(),
    };
    groupRequests.push(entry);
    groupRequestsByRequest.set(request, entry);
  });
  const proposalResponsePromise = page.waitForResponse(response =>
    groupRequestsByRequest.has(response.request()), { timeout: 5000 });

  const patientTable = page.getByTestId(`construction-table-${outputId}`);
  await action('select Patient table', patientTable, () => patientTable.click(), {
    after: () => page.waitForFunction(id =>
      document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`)?.getAttribute('aria-current') === 'page',
    outputId, { timeout: 5000 }),
    timeout: 5000,
    budget: 5000,
  });
  const rowSettings = page.getByTestId('construction-rows-settings-trigger');
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-rows-settings-trigger"]');
    return button && !button.disabled;
  }, undefined, { timeout: 5000 });
  await action('open row definition settings', rowSettings, () => rowSettings.click(), {
    after: () => page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible' }),
    timeout: 5000,
    budget: 5000,
  });
  const groupAction = page.getByTestId('construction-action-group-rows');
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-action-group-rows"]');
    return button && !button.disabled;
  }, undefined, { timeout: 5000 });
  const actionability = {
    count: await groupAction.count(),
    visible: await groupAction.isVisible(),
    enabled: await groupAction.isEnabled(),
    receivesEvents: false,
  };
  if (actionability.count === 1 && actionability.visible && actionability.enabled) {
    try {
      await groupAction.click({ trial: true });
      actionability.receivesEvents = true;
    } catch {
      actionability.receivesEvents = false;
    }
  }
  const groupActionIsActionable = actionability.count === 1 && actionability.visible
    && actionability.enabled && actionability.receivesEvents;
  check('correctness', 'Direct Group action is actionable', groupActionIsActionable, actionability);

  await action('direct Group entry automatically previews empty-key COUNT_ROWS', groupAction,
    () => groupAction.click(), {
      after: async () => {
        await page.waitForFunction(({ panelSelector, previewSelector, expectedCount }) => {
          const panel = document.querySelector(panelSelector);
          const preview = document.querySelector(previewSelector);
          const summary = document.querySelector('select[aria-label="Summary 1"]');
          const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
            .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim().toLowerCase());
          const row = preview?.querySelector('tbody tr[data-testid="construction-proposal-preview-row"]');
          const cells = [...(row?.querySelectorAll('td') ?? [])]
            .map(cell => cell.innerText.replace(/\s+/g, ' ').trim());
          const countIndex = headers.findIndex(header => header === 'row count');
          return Boolean(panel?.dataset.proposalStatus === 'ready'
            && preview?.dataset.previewStatus === 'ready'
            && summary?.value === 'COUNT_ROWS'
            && countIndex >= 0
            && cells.length === headers.length
            && cells[countIndex] === expectedCount
            && preview.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]').length === 1);
        }, {
          panelSelector: proposalPanel,
          previewSelector: proposalPreview,
          expectedCount: String(fixtureIDs.length),
        }, { timeout: 5000 });
      },
      timeout: 5000,
      budget: 5000,
    });

  const proposalResponse = await proposalResponsePromise;
  assert.equal(proposalResponse.status(), 200,
    'direct Group construction proposal must return HTTP 200');
  const proposalResponseBody = await proposalResponse.json();
  assert(proposalResponseBody, 'direct Group construction proposal response must be readable');
  const respondedRequest = groupRequestsByRequest.get(proposalResponse.request());
  assert(respondedRequest, 'Group proposal response must match its exact observed Playwright Request.');
  respondedRequest.status = proposalResponse.status();

  const initialView = await page.evaluate(({ panelSelector, previewSelector }) => {
    const preview = document.querySelector(previewSelector);
    const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
    const rows = [...(preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.replace(/\s+/g, ' ').trim()));
    return {
      proposalStatus: document.querySelector(panelSelector)?.dataset.proposalStatus,
      previewStatus: preview?.dataset.previewStatus,
      summary: document.querySelector('select[aria-label="Summary 1"]')?.value,
      headers,
      rows,
      groupKeys: [...document.querySelectorAll('[data-testid="construction-reshape-group"] input[aria-label^="Group by"]')]
        .filter(input => input.checked).map(input => input.getAttribute('aria-label')),
    };
  }, { panelSelector: proposalPanel, previewSelector: proposalPreview });
  assert.equal(initialView.summary, 'COUNT_ROWS', 'Initial standard Group must select Count rows before any edit.');
  assert.deepEqual(initialView.groupKeys, [], 'Initial standard Group must keep the row key list empty.');
  const countHeaderIndex = findRenderedBuilderHeaderIndex(initialView.headers, 'row count');
  assert(countHeaderIndex >= 0, 'Initial standard Group Preview must expose the Row count column.');
  assert.equal(initialView.rows.length, 1, 'A whole-table COUNT_ROWS summary must preview one group row.');
  assert.equal(initialView.rows[0]?.[countHeaderIndex], String(fixtureIDs.length),
    'COUNT_ROWS must equal the independent fixture count.');
  check('correctness', 'direct standard Group entry defaults to empty-key COUNT_ROWS without an edit',
    initialView.summary === 'COUNT_ROWS' && initialView.groupKeys.length === 0,
    initialView);

  const request = groupRequests.findLast(entry => {
    const steps = entry.body?.candidateConstruction?.steps;
    const last = steps?.at(-1);
    return last?.operation?.kind === 'GROUP' && last.operation.group?.keys?.length === 0;
  });
  assert(request, 'Opening direct Group must send a native Builder construction proposal before any field edit.');
  const steps = request.body.candidateConstruction.steps;
  const step = steps.at(-1);
  assert(step.id && step.operation.group.constructionId === step.id,
    'The entry-owned GROUP identity must be preserved in its proposal.');
  assert.deepEqual(step.operation.group.aggregates.map(({ operation }) => operation), ['COUNT_ROWS']);
  assert.equal(request.body.outputId, outputId);
  assert.equal(request.body.snapshotToken, builder.catalog.snapshotToken);
  assert.equal(request.body.expectedDraftVersion, builder.draftVersion);
  assert.equal(request.body.expectedDraftDigest, builder.draftDigest);
  report.target.initialGroupProposal = {
    requestObjectIdentity: request.requestObjectIdentity,
    requestId: request.requestId,
    status: request.status,
    outputId: request.body.outputId,
    snapshotTokenMatched: request.body.snapshotToken === builder.catalog.snapshotToken,
    draftVersion: request.body.expectedDraftVersion,
    draftDigestMatched: request.body.expectedDraftDigest === builder.draftDigest,
    stepId: step.id,
    operation: step.operation,
    outputLabels: step.outputs.map(({ name, label }) => ({ name, label })),
  };
  check('correctness', 'native Builder automatically proposes the exact empty-key COUNT_ROWS GROUP',
    step.operation.kind === 'GROUP' && step.operation.group.keys.length === 0
      && step.operation.group.aggregates.length === 1
      && step.operation.group.aggregates[0].operation === 'COUNT_ROWS'
      && request.body.outputId === outputId
      && request.body.snapshotToken === builder.catalog.snapshotToken
      && request.body.expectedDraftVersion === builder.draftVersion
      && request.body.expectedDraftDigest === builder.draftDigest,
    report.target.initialGroupProposal);
  check('correctness', 'automatic Group proposal Preview returns one row with the independently expected count 2',
    initialView.rows.length === 1 && initialView.rows[0]?.[countHeaderIndex] === String(fixtureIDs.length), {
      headers: initialView.headers,
      rows: initialView.rows,
      expectedCount: fixtureIDs.length,
    });

  const directGroupCheckNames = [
    'direct standard Group entry defaults to empty-key COUNT_ROWS without an edit',
    'native Builder automatically proposes the exact empty-key COUNT_ROWS GROUP',
    'automatic Group proposal Preview returns one row with the independently expected count 2',
  ];
  report.target.qaIsolation.requiredEntryChecks = directGroupCheckNames;

  await action('Apply initial empty-key COUNT_ROWS Group', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(), {
      after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
        expectedRows: [{ 'Row count': String(fixtureIDs.length) }] }),
    });
  report.target.qaIsolation.workflowMutations.push('native UI applied the empty-key COUNT_ROWS Group');

  const rows = await page.evaluate(() => {
    const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
    const rowList = [...(table?.querySelectorAll('[role="row"]') ?? [])];
    return {
      headers: [...(rowList[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim()),
      rows: rowList.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim())),
    };
  });
  const countIndex = findRenderedBuilderHeaderIndex(rows.headers, 'row count');
  assert.equal(rows.rows.length, 1, 'applied empty-key Group must retain one group row');
  assert.equal(rows.rows[0]?.[countIndex], String(fixtureIDs.length), 'applied Group must render the exact fixture count');
  check('correctness', 'applied empty-key Group renders the independently expected count 2', true, {
    headers: rows.headers,
    rows: rows.rows,
    expectedCount: fixtureIDs.length,
  });

  const readBuilder = async () => {
    const response = await fetch(context.target.apiUrl + `${apiRoot}/authoring/v2/builder`, {
      signal: AbortSignal.timeout(30000),
    });
    const value = await response.json();
    if (!response.ok) throw new Error(`Builder read returned HTTP ${response.status}.`);
    return value;
  };
  let savedBuilder = await readBuilder();
  const savedDocument = savedBuilder.workspace.documents.find(item => item.output?.id === outputId);
  const originalGroup = savedDocument?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  assert(originalGroup?.id, 'applied Group must retain its exact saved step identity');
  assert(sourceIdColumnId && sourceIdColumn.label, 'Patient root must expose its source identity as a Group key');

  const openRowsHistory = async () => {
    const history = page.getByTestId('construction-row-operation-history');
    if (!await history.isVisible().catch(() => false)) {
      const settings = page.getByTestId('construction-rows-settings-trigger');
      await action('open saved row operations', settings, () => settings.click(), {
        after: () => history.waitFor({ state: 'visible' }),
      });
    }
  };
  const editSelector = `[data-testid="construction-row-edit-${originalGroup.id}"]`;
  await openRowsHistory();
  await action('open saved Group edit', page.locator(editSelector), () => page.locator(editSelector).click(), {
    after: () => page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true }).waitFor({ state: 'visible' }),
  });
  const sourceIdKey = page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true });
  await action('preview keyed Patient Group edit', sourceIdKey, () => sourceIdKey.check(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
      && document.querySelector('[data-testid="construction-proposal-preview"]')?.dataset.previewStatus === 'ready'),
  });
  const editedPreview = await page.evaluate(() => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
    const rows = [...(preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()));
    const keys = [...document.querySelectorAll('[data-testid="construction-reshape-group"] input[aria-label^="Group by"]')]
      .filter(input => input.checked).map(input => input.getAttribute('aria-label'));
    return { headers, rows, keys };
  });
  const editedIDIndex = findRenderedBuilderHeaderIndex(editedPreview.headers, sourceIdColumn.label);
  const editedCountIndex = findRenderedBuilderHeaderIndex(editedPreview.headers, 'row count');
  const editedIDs = editedPreview.rows.map(row => row[editedIDIndex]).sort();
  assert.deepEqual(editedIDs, fixtureIDs, 'keyed Group edit Preview must show both literal fixture Patient IDs');
  assert(editedPreview.rows.every(row => row[editedCountIndex] === '1'), 'each distinct Patient key must count exactly one source row');
  assert.deepEqual(editedPreview.keys, [`Group by ${sourceIdColumn.label}`]);
  check('correctness', 'saved Group edit previews exact Patient IDs as one row per key', true, editedPreview);

  const beforeEdit = savedBuilder;
  await action('Cancel keyed Group edit', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(), {
      after: () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]')),
    });
  savedBuilder = await readBuilder();
  assert.deepEqual(savedBuilder.workspace, beforeEdit.workspace, 'Cancel must leave the saved empty-key Group unchanged');
  assert.equal(savedBuilder.draftVersion, beforeEdit.draftVersion, 'Cancel must preserve the exact saved draft version');
  assert.equal(savedBuilder.draftDigest, beforeEdit.draftDigest, 'Cancel must preserve the exact saved draft digest');
  check('persistence', 'Cancel preserves the saved empty-key Group and exact draft', true, {
    draftVersion: savedBuilder.draftVersion,
    draftDigest: savedBuilder.draftDigest,
    groupStepId: originalGroup.id,
  });

  await openRowsHistory();
  await action('reopen saved Group edit', page.locator(editSelector), () => page.locator(editSelector).click(), {
    after: () => page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true }).waitFor({ state: 'visible' }),
  });
  await action('reapply keyed Patient Group edit', page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true }),
    () => page.getByRole('checkbox', { name: `Group by ${sourceIdColumn.label}`, exact: true }).check(), {
      after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'),
    });
  await action('Apply keyed Patient Group edit', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(), {
      after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
        expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID, 'Row count': '1' })) }),
    });
  report.target.qaIsolation.workflowMutations.push('native UI edited the saved Group to use the Patient ID key and applied the edit');
  check('correctness', 'saved Group edit applies one row per exact Patient ID', true, {
    stepId: originalGroup.id,
    keyColumnId: sourceIdColumnId,
    patientIDs: fixtureIDs,
    preview: editedPreview,
  });
  savedBuilder = await readBuilder();
  const keyedGroup = savedBuilder.workspace.documents.find(item => item.output?.id === outputId)
    ?.construction?.steps?.find(step => step.operation?.kind === 'GROUP');
  assert.equal(keyedGroup?.id, originalGroup.id, 'editing Group must preserve its saved construction identity');
  assert.deepEqual(keyedGroup.operation.group.keys.map(key => key.inputColumnId), [sourceIdColumnId]);

  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible' });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click();
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible' });
  await waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
    expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID, 'Row count': '1' })) });
  const keyedReload = await page.evaluate(({ tableSelector }) => {
    const table = document.querySelector(tableSelector);
    const rowList = [...(table?.querySelectorAll('[role="row"]') ?? [])];
    const headers = [...(rowList[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim());
    const rows = rowList.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
    return { headers, rows };
  }, { tableSelector: renderedTableSelector });
  const keyedReloadIDIndex = findRenderedBuilderHeaderIndex(keyedReload.headers, sourceIdColumn.label);
  const keyedReloadIDs = keyedReloadIDIndex < 0 ? [] : keyedReload.rows.map(row => row[keyedReloadIDIndex]).sort();
  assert.deepEqual(keyedReloadIDs, fixtureIDs, 'keyed Group output must retain exact Patient IDs after reload');
  check('persistence', 'keyed Group edit and exact Patient rows survive reload', true, {
    groupStepId: keyedGroup.id,
    draftVersion: savedBuilder.draftVersion,
    draftDigest: savedBuilder.draftDigest,
    headers: keyedReload.headers,
    rows: keyedReload.rows,
    patientIDs: keyedReloadIDs,
  });

  const removalSelector = `[data-testid="construction-row-remove-${keyedGroup.id}"]`;
  await openRowsHistory();
  await action('propose removing keyed Group', page.locator(removalSelector), () => page.locator(removalSelector).click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'
      && Boolean(document.querySelector('[data-testid="construction-removal-summary"]'))),
  });
  const restoredPreview = await page.evaluate(() => {
    const preview = document.querySelector('[data-testid="construction-proposal-preview"]');
    const headers = [...(preview?.querySelectorAll('thead th') ?? [])]
      .map(cell => (cell.querySelector('span')?.innerText ?? cell.innerText).replace(/\s+/g, ' ').trim());
    const rows = [...(preview?.querySelectorAll('tbody tr[data-testid="construction-proposal-preview-row"]') ?? [])]
      .map(row => [...row.querySelectorAll('td')].map(cell => cell.innerText.trim()));
    return { headers, rows };
  });
  const restoredIDIndex = findRenderedBuilderHeaderIndex(restoredPreview.headers, sourceIdColumn.label);
  assert.deepEqual(restoredPreview.rows.map(row => row[restoredIDIndex]).sort(), fixtureIDs,
    'removing Group must preview the exact two original Patient rows');
  check('correctness', 'Group removal previews both literal source Patient rows', true, {
    headers: restoredPreview.headers,
    rows: restoredPreview.rows,
    patientIDs: fixtureIDs,
  });
  await action('Cancel keyed Group removal', page.getByTestId('construction-cancel-proposal'),
    () => page.getByTestId('construction-cancel-proposal').click(), {
      after: () => page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]')),
    });
  const afterRemovalCancel = await readBuilder();
  assert.deepEqual(afterRemovalCancel.workspace, savedBuilder.workspace, 'Cancel removal must preserve the saved keyed Group');
  assert.equal(afterRemovalCancel.draftVersion, savedBuilder.draftVersion, 'Cancel removal must preserve the exact saved draft version');
  assert.equal(afterRemovalCancel.draftDigest, savedBuilder.draftDigest, 'Cancel removal must preserve the exact saved draft digest');
  check('persistence', 'Cancel preserves the saved keyed Group and exact draft', true, {
    draftVersion: afterRemovalCancel.draftVersion,
    draftDigest: afterRemovalCancel.draftDigest,
    groupStepId: keyedGroup.id,
  });

  await openRowsHistory();
  await action('reopen keyed Group removal', page.locator(removalSelector), () => page.locator(removalSelector).click(), {
    after: () => page.waitForFunction(() => document.querySelector('[data-testid="construction-removal-summary"]') !== null),
  });
  await action('Apply keyed Group removal', page.getByTestId('construction-apply-proposal'),
    () => page.getByTestId('construction-apply-proposal').click(), {
      after: () => waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
        expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID })) }),
    });
  report.target.qaIsolation.workflowMutations.push('native UI removed the keyed Group and restored source Patient rows');
  report.target.qaIsolation.cleanupMutations.push('native UI removed the case-owned Group step to restore the original Patient row shape');
  const removedBuilder = await readBuilder();
  const removedDocument = removedBuilder.workspace.documents.find(item => item.output?.id === outputId);
  assert.deepEqual(removedDocument?.construction?.steps ?? [], [], 'removing the sole Group must restore the source construction');
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByTestId(`construction-table-${outputId}`).waitFor({ state: 'visible' });
  if (!await page.locator('[data-testid="preview-table-scroll"] [role="table"]').isVisible()) {
    await page.getByTestId(`construction-table-${outputId}`).click();
  }
  await page.locator('[data-testid="preview-table-scroll"] [role="table"]').waitFor({ state: 'visible' });
  await waitForBuilderRenderedGrid(page, { tableSelector: renderedTableSelector,
    expectedRows: fixtureIDs.map(patientID => ({ [sourceIdColumn.label]: patientID })) });
  const restoredRows = await page.evaluate(({ tableSelector }) => {
    const table = document.querySelector(tableSelector);
    const rowList = [...(table?.querySelectorAll('[role="row"]') ?? [])];
    const headers = [...(rowList[0]?.querySelectorAll('[role="columnheader"]') ?? [])].map(cell => cell.innerText.trim());
    const rows = rowList.slice(1).map(row => [...row.querySelectorAll('[role="cell"]')].map(cell => cell.innerText.trim()));
    return { headers, rows };
  }, { tableSelector: renderedTableSelector });
  const restoredRowsIDIndex = findRenderedBuilderHeaderIndex(restoredRows.headers, sourceIdColumn.label);
  const restoredIDs = restoredRowsIDIndex < 0 ? [] : restoredRows.rows.map(row => row[restoredRowsIDIndex]).sort();
  assert.deepEqual(restoredIDs, fixtureIDs, 'removing Group and reloading must restore both exact source Patient IDs');
  check('persistence', 'removing keyed Group restores the exact source Patient rows after reload', true, {
    draftVersion: removedBuilder.draftVersion,
    draftDigest: removedBuilder.draftDigest,
    headers: restoredRows.headers,
    rows: restoredRows.rows,
    patientIDs: restoredIDs,
  });
  report.target.qaIsolation.requiredLifecycleChecks = [
    'applied empty-key Group renders the independently expected count 2',
    'saved Group edit previews exact Patient IDs as one row per key',
    'Cancel preserves the saved empty-key Group and exact draft',
    'saved Group edit applies one row per exact Patient ID',
    'keyed Group edit and exact Patient rows survive reload',
    'Group removal previews both literal source Patient rows',
    'Cancel preserves the saved keyed Group and exact draft',
    'removing keyed Group restores the exact source Patient rows after reload',
  ];
};
