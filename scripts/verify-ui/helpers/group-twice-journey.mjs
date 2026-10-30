import { expect } from '@playwright/test';
import { captureBuilderObservation, combineAuthoringObservation, observeAuthoringAttempt } from './fast-fixtures.mjs';

const waitForProposalReady = async (page) => {
  await page.waitForFunction(() => {
    const panel = document.querySelector('[data-testid="construction-proposal-panel"]');
    return ['ready', 'error', 'needs-repair'].includes(panel?.dataset.proposalStatus);
  }, undefined, { timeout: 30_000 });
};

const waitForProposalGone = async (page) => {
  await page.waitForFunction(() => !document.querySelector('[data-testid="construction-proposal-panel"]'), undefined, { timeout: 20_000 });
};

export const applyJourneyGroup = async (page, trace, label, attempt) => {
  const proposal = page.getByTestId('construction-proposal-panel');
  const proposalAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'construction-proposals',
    owner: { kind: 'proposal', attempt },
    timeout: 20_000,
  });
  await page.getByTestId('construction-action-group-rows').click();
  const proposalStarted = await proposalAttempt.started;
  trace.actions.push({
    label: 'Preview',
    event: 'preview',
    owner: proposalStarted.owner,
    request: proposalStarted.request,
    observation: proposalStarted.requestObservation,
  });
  await waitForProposalReady(page);
  const proposalStatus = await proposal.getAttribute('data-proposal-status');
  if (label === 'GroupAgain') expect(proposalStatus).toBe('ready');
  else expect(proposalStatus, `${label} proposal status`).toBe('ready');
  const proposalExchange = await proposalAttempt.settled;
  if (label === 'GroupAgain') expect(proposalExchange.rawResponse.ok(), 'second group proposal preview').toBe(true);
  const proposalObservation = await captureBuilderObservation(page);
  trace.actions.push({
    label: 'PreviewComplete',
    event: 'preview-response',
    owner: proposalStarted.owner,
    request: proposalExchange.request,
    response: proposalExchange.response,
    settlement: proposalExchange.settlement,
    observation: combineAuthoringObservation(proposalExchange, proposalObservation),
  });
  const commandAttempt = observeAuthoringAttempt(page, {
    method: 'POST',
    path: 'commands',
    owner: { kind: 'apply', attempt },
    timeout: 20_000,
  });
  await page.getByTestId('construction-apply-proposal').click();
  const commandStarted = await commandAttempt.started;
  const commandExchange = await commandAttempt.settled;
  if (label === 'GroupAgain') expect(commandExchange.rawResponse.ok()).toBe(true);
  else expect(commandExchange.rawResponse.ok(), `${label} command persistence`).toBe(true);
  await waitForProposalGone(page);
  trace.actions.push({
    label,
    event: 'group',
    owner: commandStarted.owner,
    request: commandExchange.request,
    response: commandExchange.response,
    settlement: commandExchange.settlement,
    observation: await captureBuilderObservation(page),
  });
  return { proposalPreview: proposalExchange.payload.preview ?? proposalExchange.payload };
};

export const assertGroupTwicePreview = (observedPreview) => {
  const rowCount = Number(observedPreview.rowCount ?? observedPreview.rows?.length ?? 0);
  expect(rowCount, 'the applied table preview must contain exactly one grouped row').toBe(1);
  expect(Array.isArray(observedPreview.rows) ? observedPreview.rows.length : 0).toBe(1);
  expect(observedPreview.rows?.[0]?.row_count, 'grouped preview row value').toBe(1);
  expect(observedPreview.columns?.map((column) => column.column), 'grouped preview columns').toContain('row_count');
  const { __loom_row_id: _rowIdentity, ...observedValues } = observedPreview.rows[0] ?? {};
  expect(observedValues, 'exact grouped preview values').toEqual({ row_count: 1 });
  return rowCount;
};
