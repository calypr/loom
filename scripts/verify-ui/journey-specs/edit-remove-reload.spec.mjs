import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname } from 'node:path';
import {
  addFieldWithCommandTrace,
  captureBuilderObservation,
  captureJourneyProvenance,
  combineAuthoringObservation,
  expect,
  observeAuthoringAttempt,
  test,
} from '../helpers/fast-fixtures.mjs';
import { addField, isCdaDataset } from '../helpers/cda-journey-fields.mjs';

const TRACE_SCHEMA_VERSION = 1;
const AUTHORING_PREFIX = '/authoring/v2/';
const SOURCE_LABELS = ['id', 'name[].family [0]', 'name[].family [1]', 'name__count', 'gender'];

const isAuthoringResponse = (response) => new URL(response.url()).pathname.includes(AUTHORING_PREFIX);
const authoringPath = (response) => new URL(response.url()).pathname.split(AUTHORING_PREFIX)[1] ?? '';

const waitForPreviewReady = async (page) => {
  await page.waitForFunction(() => {
    const preview = document.querySelector('[data-testid="construction-preview"]');
    return preview?.dataset.previewStatus === 'ready' && Boolean(preview.dataset.previewReceiptId);
  }, undefined, { timeout: 45_000 });
};

const waitForProposalReady = async (page) => {
  await page.waitForFunction(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return panel?.getAttribute('data-proposal-status') === 'ready';
  }, undefined, { timeout: 30_000 });
};

const waitForProposalGone = async (page) => {
  await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined, { timeout: 20_000 });
};

test('builder journey edits and removes a group, then reloads the source', async ({ fastPage, fastUrl }) => {
  const cda = isCdaDataset();
  const basicBudgetProbe = process.env.LOOM_JOURNEY_DATASET === 'basic-budget';
  expect(cda || basicBudgetProbe,
    'Select the retained CDA lifecycle with LOOM_JOURNEY_DATASET=mini or the Basic call-budget probe with LOOM_JOURNEY_DATASET=basic-budget.')
    .toBe(true);
  test.setTimeout(120_000);
  fastPage.setDefaultTimeout(30_000);
  fastPage.setDefaultNavigationTimeout(20_000);

  const tracePath = process.env.LOOM_JOURNEY_TRACE;
  expect(tracePath, 'LOOM_JOURNEY_TRACE must be set by run-builder-journey.mjs').toBeTruthy();
  const provenance = captureJourneyProvenance();
  expect(provenance.sourceCommit, 'LOOM_SOURCE_COMMIT must be the tested Git commit').toMatch(/^[0-9a-f]{40}$/);

  const backendCalls = [];
  const proposalBodies = [];
  fastPage.on('response', async (response) => {
    if (!isAuthoringResponse(response)) return;
    const path = authoringPath(response);
    backendCalls.push({ method: response.request().method(), path, status: response.status() });
    if (response.request().method() === 'POST' && path === 'construction-proposals' && response.ok()) {
      proposalBodies.push(await response.json().catch(() => null));
    }
  });

  console.log('[journey] navigation');
  const initialPreviewAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'preview',
    owner: { kind: 'preview', attempt: 'P1' },
    timeout: 45_000,
  });
  await fastPage.goto(fastUrl, { waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);
  const initialPreviewExchange = await initialPreviewAttempt.settled;
  const initialObservation = await captureBuilderObservation(fastPage);
  let sourceLabels = SOURCE_LABELS;
  let sourceRows = 2;
  let sourceIDs = ['dev-patient-001', 'dev-patient-002'];
  const setupActions = [];
  if (cda) {
    const added = await addFieldWithCommandTrace(fastPage, 'Select Patient.extension[].valueCode', setupActions, addField);
    sourceLabels = (added.columns ?? []).map((column) => column.label);
    sourceRows = Number(added.rowCount ?? added.rows?.length ?? 0);
    const id = (added.columns ?? []).find((column) => column.label === 'id')?.column;
    sourceIDs = (added.rows ?? []).slice(0, 2).map((row) => row[id]);
    expect(sourceRows, 'CDA source rows').toBeGreaterThan(0);
    expect(sourceLabels, 'CDA source columns').toContain('Extension Value Code');
  }

  const trace = {
    schemaVersion: TRACE_SCHEMA_VERSION,
    ...provenance,
    traces: [{
      name: basicBudgetProbe ? 'journey/edit-remove-reload-basic-budget' : 'journey/edit-remove-reload-cda-manual',
      model: 'BuilderJourney',
      initialObservation,
      initialPreview: {
        owner: initialPreviewExchange.owner,
        request: initialPreviewExchange.request,
        response: initialPreviewExchange.response,
        settlement: initialPreviewExchange.settlement,
      },
      actions: setupActions,
      unmodeledEvents: [],
    }],
  };

  console.log('[journey] apply default group');
  const firstProposalAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'construction-proposals',
    owner: { kind: 'proposal', attempt: 'P1' },
  });
  await fastPage.getByTestId('construction-rows-settings-trigger').click();
  await fastPage.getByTestId('construction-action-group-rows').click();
  const firstProposalStarted = await firstProposalAttempt.started;
  trace.traces[0].actions.push({
    label: 'Preview',
    event: 'preview',
    owner: firstProposalStarted.owner,
    request: firstProposalStarted.request,
    observation: firstProposalStarted.requestObservation,
  });
  await waitForProposalReady(fastPage);
  const firstProposalExchange = await firstProposalAttempt.settled;
  const firstProposalObservation = await captureBuilderObservation(fastPage);
  trace.traces[0].actions.push({
    label: 'PreviewComplete',
    event: 'preview-response',
    owner: firstProposalStarted.owner,
    request: firstProposalExchange.request,
    response: firstProposalExchange.response,
    settlement: firstProposalExchange.settlement,
    observation: combineAuthoringObservation(firstProposalExchange, firstProposalObservation),
  });
  const firstCommandAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'commands',
    owner: { kind: 'apply', attempt: 'P1' },
  });
  await fastPage.getByTestId('construction-apply-proposal').click();
  const firstCommandStarted = await firstCommandAttempt.started;
  const firstCommandExchange = await firstCommandAttempt.settled;
  expect(firstCommandExchange.rawResponse.ok(), 'default group persistence').toBe(true);
  await waitForProposalGone(fastPage);
  const backToTable = fastPage.getByRole('button', { name: 'Back to table' });
  if (await backToTable.isVisible().catch(() => false)) await backToTable.click();
  if (cda) {
    expect(await fastPage.getByTestId('preview-table-scroll').getByRole('row').count(), 'CDA default group rows').toBeGreaterThan(1);
  } else {
    await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(sourceRows);
  }
  trace.traces[0].actions.push({
    label: 'Group',
    event: 'group',
    owner: firstCommandStarted.owner,
    request: firstCommandExchange.request,
    response: firstCommandExchange.response,
    settlement: firstCommandExchange.settlement,
    observation: await captureBuilderObservation(fastPage),
  });

  const groupColumnLabel = cda ? 'Extension Value Code' : 'gender';
  console.log(`[journey] ${basicBudgetProbe ? 'Basic call-budget probe' : 'CDA lifecycle'}: edit group by id and ${groupColumnLabel}`);
  const history = fastPage.locator('[data-testid^="construction-history-step-"]').first();
  await expect(history).toBeVisible();
  const stepTestId = await history.getAttribute('data-testid');
  const stepId = stepTestId.replace('construction-history-step-', '');
  await history.click();
  await fastPage.getByTestId(`construction-edit-step-${stepId}`).click();
  const idGroup = fastPage.getByRole('checkbox', { name: 'Group by id', exact: true });
  const secondaryGroup = fastPage.getByRole('checkbox', { name: `Group by ${groupColumnLabel}`, exact: true });
  await expect(idGroup).toBeVisible();
  await expect(secondaryGroup).toBeVisible();
  const beforeEdit = proposalBodies.length;
  const editProposalAttempts = [];
  const editProposalSettlements = [];
  const editProposalRows = [];
  const observeEditSelection = async (checkbox) => {
    if (await checkbox.isChecked()) {
      await checkbox.check();
      return;
    }
    const attempt = observeAuthoringAttempt(fastPage, {
      method: 'POST',
      path: 'construction-proposals',
      owner: { kind: 'proposal', attempt: editProposalSettlements.length === 0 ? 'P1' : 'P2' },
    });
    editProposalAttempts.push(attempt);
    await checkbox.check();
    const started = await attempt.started;
    editProposalRows.push({
      sequence: started.sequence,
      action: {
        label: 'Preview',
        event: 'preview',
        owner: started.owner,
        request: started.request,
        observation: started.requestObservation,
      },
    });
    editProposalSettlements.push(attempt.settled.then(async (exchange) => {
      const startedAttempts = await Promise.all(editProposalAttempts.map((candidate) => candidate.started));
      const observation = exchange.responseObservation;
      const response = exchange.response;
      const responseMatchesPanel = Boolean(response?.proposalId)
        && response.proposalId === observation.proposalId;
      const newerProposalStarted = startedAttempts.some((candidate) =>
        candidate.sequence > started.sequence && candidate.sequence < exchange.responseSequence);
      const late = newerProposalStarted
        || Boolean(response?.proposalId && observation.proposalId && response.proposalId !== observation.proposalId);
      const requestCancelled = !response
        && exchange.settlement?.kind === 'request-failed'
        && exchange.settlement.errorText === 'net::ERR_ABORTED';
      const currentRequestCancelled = requestCancelled && !late;
      const typedError = !response
        || response.status < 200
        || response.status >= 300
        || Boolean(response.errorCode)
        || response.proposalPreviewStatus === 'NEEDS_REPAIR';
      const observedError = responseMatchesPanel && ['error', 'needs-repair'].includes(observation.status);
      const observedReady = responseMatchesPanel && observation.status === 'ready';
      const responseHasReadyPreview = response?.status >= 200
        && response.status < 300
        && response.proposalPreviewStatus === 'READY'
        && Boolean(response.preview?.receiptId)
        && typeof response.preview?.rowsPresent === 'boolean';
      const failed = typedError || observedError;
      if (!failed && !observedReady && !responseHasReadyPreview) {
        throw new Error(`Cannot classify edit proposal settlement for ${started.owner.attempt} from response and observed panel`);
      }
      const label = currentRequestCancelled
        ? 'PreviewRequestCancelled'
        : late
          ? (failed ? 'LatePreviewErrorIgnored' : 'LatePreviewCompleteIgnored')
          : (failed ? 'PreviewError' : 'PreviewComplete');
      editProposalRows.push({
        sequence: exchange.responseSequence,
        action: {
          label,
          event: currentRequestCancelled ? 'preview-request-cancelled' : 'preview-response',
          owner: started.owner,
          request: exchange.request,
          response: exchange.response,
          settlement: exchange.settlement,
          observation: late || currentRequestCancelled ? observation : combineAuthoringObservation(exchange, observation),
        },
      });
      return exchange;
    }));
  };
  await observeEditSelection(idGroup);
  await observeEditSelection(secondaryGroup);
  const editedBody = () => [...proposalBodies.slice(beforeEdit)].reverse().find((body) => {
    const labels = body?.preview?.columns?.map((column) => column.label) ?? [];
    return body?.preview?.rowCount === sourceRows
      && labels.includes('id')
      && labels.includes(groupColumnLabel)
      && labels.includes('Row count');
  });
  await expect.poll(() => Boolean(editedBody())).toBe(true);
  const editedPreview = editedBody().preview;
  await waitForProposalReady(fastPage);
  await Promise.all(editProposalSettlements);
  trace.traces[0].actions.push(...editProposalRows
    .sort((left, right) => left.sequence - right.sequence)
    .map(({ action }) => action));
  await expect(fastPage.getByTestId('construction-proposal-panel')).toContainText(
    `${sourceRows} rows and 3 columns`);
  const renderedEditObservation = await captureBuilderObservation(fastPage);
  const renderedEditProposalId = renderedEditObservation.proposalId;
  const appliedEditSettlement = editProposalRows.find(({ action }) =>
    action.label === 'PreviewComplete'
      && action.response?.proposalId === renderedEditProposalId
      && action.response?.proposalPreviewStatus === 'READY'
      && Boolean(action.response.preview?.receiptId)
      && typeof action.response.preview.rowsPresent === 'boolean');
  if (renderedEditObservation.status !== 'ready'
    || !renderedEditProposalId
    || !appliedEditSettlement) {
    throw new Error('Cannot identify the ready edit proposal settlement shown by the rendered panel');
  }

  const editedColumns = editedPreview.columns ?? [];
  const editedRows = editedPreview.rows ?? [];
  const editedLabels = editedColumns.map((column) => column.label);
  const editedCount = editedColumns.find((column) => column.label === 'Row count')?.column;
  expect(Number(editedPreview.rowCount), 'edited group preview row count').toBe(sourceRows);
  expect(editedLabels, 'edited group preview labels').toEqual(['id', groupColumnLabel, 'Row count']);
  expect(editedRows, 'edited group rows').toHaveLength(sourceRows);
  expect(editedRows.every((row) => row[editedCount] === 1), 'each edited group contains one record').toBe(true);

  const editCommandAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'commands',
    owner: { kind: 'apply', attempt: appliedEditSettlement.action.owner.attempt },
  });
  await fastPage.getByTestId('construction-apply-proposal').click();
  const editCommandStarted = await editCommandAttempt.started;
  const editCommandExchange = await editCommandAttempt.settled;
  expect(editCommandExchange.rawResponse.ok(), 'group edit persistence').toBe(true);
  await waitForProposalGone(fastPage);
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(sourceRows + 1);
  await expect(fastPage.getByRole('columnheader')).toHaveCount(3);
  await expect(fastPage.getByRole('columnheader', { name: /^id$/i })).toBeVisible();
  await expect(fastPage.getByRole('columnheader', { name: new RegExp(`^${groupColumnLabel}$`, 'i') })).toBeVisible();
  await expect(fastPage.getByRole('columnheader', { name: /^row count$/i })).toBeVisible();
  trace.traces[0].actions.push({
    label: 'EditGroup',
    event: 'edit-group',
    owner: editCommandStarted.owner,
    request: editCommandExchange.request,
    response: editCommandExchange.response,
    settlement: editCommandExchange.settlement,
    observation: await captureBuilderObservation(fastPage),
  });

  console.log('[journey] remove group');
  const removeProposalAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'construction-proposals',
    owner: { kind: 'proposal', attempt: 'P1' },
  });
  const editedHistory = fastPage.locator('[data-testid^="construction-history-step-"]').first();
  const editedStepTestId = await editedHistory.getAttribute('data-testid');
  const editedStepId = editedStepTestId.replace('construction-history-step-', '');
  await editedHistory.click();
  await fastPage.getByTestId(`construction-remove-step-${editedStepId}`).click();
  const removeProposalStarted = await removeProposalAttempt.started;
  trace.traces[0].actions.push({
    label: 'Preview',
    event: 'preview',
    owner: removeProposalStarted.owner,
    request: removeProposalStarted.request,
    observation: removeProposalStarted.requestObservation,
  });
  await waitForProposalReady(fastPage);
  const removeProposalExchange = await removeProposalAttempt.settled;
  await expect(fastPage.getByTestId('construction-proposal-panel')).toContainText(
    `${sourceRows} rows and ${sourceLabels.length} columns`);
  const removeProposalObservation = await captureBuilderObservation(fastPage);
  trace.traces[0].actions.push({
    label: 'PreviewComplete',
    event: 'preview-response',
    owner: removeProposalStarted.owner,
    request: removeProposalExchange.request,
    response: removeProposalExchange.response,
    settlement: removeProposalExchange.settlement,
    observation: combineAuthoringObservation(removeProposalExchange, removeProposalObservation),
  });
  const removeCommandAttempt = observeAuthoringAttempt(fastPage, {
    method: 'POST',
    path: 'commands',
    owner: { kind: 'apply', attempt: 'P1' },
  });
  await fastPage.getByTestId('construction-apply-proposal').click();
  const removeCommandStarted = await removeCommandAttempt.started;
  const removeCommandExchange = await removeCommandAttempt.settled;
  expect(removeCommandExchange.rawResponse.ok(), 'group removal persistence').toBe(true);
  await waitForProposalGone(fastPage);
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(sourceRows + 1);
  await expect(fastPage.getByRole('columnheader')).toHaveCount(sourceLabels.length);
  await expect(fastPage.getByTestId('preview-table-scroll').getByText(sourceIDs[0]).first()).toBeVisible();
  await expect(fastPage.getByTestId('preview-table-scroll').getByText(sourceIDs[1]).first()).toBeVisible();
  trace.traces[0].actions.push({
    label: 'RemoveGroup',
    event: 'remove-group',
    owner: removeCommandStarted.owner,
    request: removeCommandExchange.request,
    response: removeCommandExchange.response,
    settlement: removeCommandExchange.settlement,
    observation: await captureBuilderObservation(fastPage),
  });

  console.log('[journey] reload after removal');
  await fastPage.reload({ waitUntil: 'domcontentloaded' });
  await waitForPreviewReady(fastPage);
  await expect(fastPage.getByTestId('preview-table-scroll').getByRole('row')).toHaveCount(sourceRows + 1);
  await expect(fastPage.getByRole('columnheader')).toHaveCount(sourceLabels.length);
  await expect(fastPage.getByTestId('preview-table-scroll').getByText(sourceIDs[0]).first()).toBeVisible();
  await expect(fastPage.getByTestId('preview-table-scroll').getByText(sourceIDs[1]).first()).toBeVisible();
  trace.traces[0].unmodeledEvents.push({
    kind: 'reload',
    classification: 'unsupported',
    reason: 'Reload remounts workspace; current model covers same-mount proposal/apply lifecycle; native reload assertion remains independent.',
    note: 'Reload after removing the group preserves the restored source table.',
  });

  trace.traces[0].backendCalls = backendCalls;
  trace.traces[0].outcome = {
    kind: 'table-preview',
    status: 'ready',
    rowCount: sourceRows,
    columns: editedLabels,
    restoredRowCount: sourceRows,
    restoredColumns: sourceLabels,
  };
  mkdirSync(dirname(tracePath), { recursive: true });
  writeFileSync(tracePath, `${JSON.stringify(trace, null, 2)}\n`);

  const callBudgetLabel = basicBudgetProbe ? 'Basic call-budget probe' : 'CDA Group journey';
  expect(backendCalls.length, `${callBudgetLabel} must capture at least ten authoring backend responses`).toBeGreaterThanOrEqual(10);
  expect(backendCalls.length, `${callBudgetLabel} must capture no more than forty-five authoring backend responses`).toBeLessThanOrEqual(45);
});
