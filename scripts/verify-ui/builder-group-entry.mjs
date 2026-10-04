import assert from 'node:assert/strict';
import { join } from 'node:path';
import { readFileSync } from 'node:fs';
import { browserURL, runPlaywrightCase } from './common.mjs';

const expectedPatientIDs = ['dev-patient-001', 'dev-patient-002'];
const proposalPanel = '[data-testid="construction-proposal-panel"]';
const proposalPreview = '[data-testid="construction-proposal-preview"]';

const readNDJSON = path => readFileSync(path, 'utf8')
  .split(/\r?\n/)
  .filter(Boolean)
  .map(line => JSON.parse(line));

const run = context => runPlaywrightCase(context, 'builder-authoring', 'group-entry', async ({ page, report, check, action }) => {
  assert.equal(context.custom, false, 'This case requires a fresh owned fixture project.');
  assert.equal(context.seed?.fresh, true, 'This case must use a fresh verification project.');
  assert(context.target.fixtureDir && context.target.fixtureGeneration, 'Expected an isolated fixture generation.');

  const fixtureIDs = readNDJSON(join(context.target.fixtureDir, 'Patient.ndjson'))
    .filter(resource => resource?.resourceType === 'Patient')
    .map(resource => resource.id)
    .sort();
  const fixtureOracle = {
    source: 'fresh project Patient.ndjson',
    project: context.target.fixtureProject,
    generation: context.target.fixtureGeneration,
    resourceType: 'Patient',
    patientIDs: fixtureIDs,
    expectedCountRows: fixtureIDs.length,
  };
  report.target.fixtureRawOracle = fixtureOracle;
  check('correctness', 'basic Patient fixture contains the two independently known records',
    JSON.stringify(fixtureIDs) === JSON.stringify(expectedPatientIDs), fixtureOracle);

  const explorerName = `Verify ${context.runID.slice(-10)} group entry`;
  const bootstrapPath = `/api/v1/projects/${encodeURIComponent(context.target.fixtureProject)}`
    + `/explorers/${encodeURIComponent(context.target.bootstrapExplorerId)}/authoring/v2/construction-capabilities`;
  const bootstrapResponsePromise = page.waitForResponse(response => {
    const url = new URL(response.url());
    return url.origin === new URL(context.target.uiUrl).origin
      && url.pathname === bootstrapPath && response.request().method() === 'POST';
  });
  await page.goto(browserURL(context.target, context.target.fixtureProject, context.target.bootstrapExplorerId, 'builder'),
    { waitUntil: 'domcontentloaded' });
  const bootstrapResponse = await bootstrapResponsePromise;
  assert.equal(bootstrapResponse.status(), 200,
    'bootstrap construction-capabilities request must complete before Explorer creation');
  const bootstrapRequestBody = await bootstrapResponse.json();
  assert(bootstrapRequestBody, 'bootstrap construction-capabilities response must be readable');

  const newExplorer = page.getByText('New explorer', { exact: true });
  await newExplorer.waitFor({ state: 'visible' });
  await action('open Explorer creation', newExplorer, () => newExplorer.click(), {
    after: () => page.locator('#new-explorer-name').waitFor({ state: 'visible' }),
    timeout: 30000,
    budget: 30000,
  });
  const explorerNameControl = page.locator('#new-explorer-name');
  await action('name Explorer', explorerNameControl, () => explorerNameControl.fill(explorerName), {
    editable: true,
    timeout: 30000,
    budget: 30000,
  });
  const createBlank = page.getByRole('button', { name: 'Create blank', exact: true });
  await action('create blank Explorer', createBlank, () => createBlank.click(), {
    after: () => page.waitForFunction(expectedTitle => {
      const select = document.querySelector('select[aria-label="Explorer"]');
      return select?.selectedOptions[0]?.textContent?.trim() === expectedTitle;
    }, explorerName, { timeout: 30000 }),
    timeout: 30000,
    budget: 30000,
  });
  const explorerControl = page.getByRole('combobox', { name: 'Explorer', exact: true });
  const explorer = await explorerControl.inputValue();
  assert(explorer && explorer !== context.target.bootstrapExplorerId,
    'Group entry requires a newly created Explorer');
  report.target.explorer = explorer;

  const tableName = page.locator('#first-table-name');
  await action('name Patient table', tableName, () => tableName.fill('Patients'), {
    editable: true,
    timeout: 30000,
    budget: 30000,
  });
  const choosePatients = page.getByRole('button', { name: 'Choose Patient rows', exact: true });
  await action('choose Patient rows', choosePatients, () => choosePatients.click(), {
    after: () => page.waitForFunction(() => {
      const table = document.querySelector('[data-testid="preview-table-scroll"] [role="table"]');
      return table && Number(table.getAttribute('aria-rowcount')) > 1
        && !document.body.innerText.includes('Loading your table…');
    }, undefined, { timeout: 30000 }),
    timeout: 30000,
    budget: 30000,
  });

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
  const outputId = document.output.id;
  report.target.table = { outputId, rootResourceType: 'Patient', rawRowCount: fixtureIDs.length };

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
    groupRequestsByRequest.has(response.request()), { timeout: 10000 });

  const patientTable = page.getByTestId(`construction-table-${outputId}`);
  await action('select Patient table', patientTable, () => patientTable.click(), {
    after: () => page.waitForFunction(id =>
      document.querySelector(`[data-testid="construction-table-${CSS.escape(id)}"]`)?.getAttribute('aria-current') === 'page',
    outputId, { timeout: 10000 }),
    timeout: 10000,
    budget: 30000,
  });
  const rowSettings = page.getByTestId('construction-rows-settings-trigger');
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-rows-settings-trigger"]');
    return button && !button.disabled;
  }, undefined, { timeout: 10000 });
  await action('open row definition settings', rowSettings, () => rowSettings.click(), {
    after: () => page.getByTestId('construction-action-group-rows').waitFor({ state: 'visible' }),
    timeout: 10000,
    budget: 30000,
  });
  const groupAction = page.getByTestId('construction-action-group-rows');
  await page.waitForFunction(() => {
    const button = document.querySelector('[data-testid="construction-action-group-rows"]');
    return button && !button.disabled;
  }, undefined, { timeout: 10000 });
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
        }, { timeout: 10000 });
      },
      timeout: 10000,
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
  const countHeaderIndex = initialView.headers.findIndex(header => header.toLowerCase() === 'row count');
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
});

export const runGroupEntry = context => run(context);
