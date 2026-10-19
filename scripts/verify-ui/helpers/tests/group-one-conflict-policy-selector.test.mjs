import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chromium } from 'playwright';
import { selectOption } from '../cda-playwright.mjs';
import {
  groupOneConflictOperationPolicySelector,
  groupOneConflictSelectedFieldSelector,
  inspectGroupOneConflictRecoveryState,
  classifyGroupOneExpectedONEFailure,
} from '../../workflows/verify-cda-group-one-conflict-browser.mjs';

test('Group ONE retry switches to ALL in the open Add Columns editor after its inline error', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`<section aria-label="Change editor">
      <section data-testid="construction-choice-proposal-panel" data-proposal-status="error">
        <h3>Preview new columns</h3>
        <p role="alert">Some grouped records have different values for this column.</p>
        <button>Cancel</button>
      </section>
      <section aria-label="Add columns editor">
        <input type="checkbox" aria-label="Select Specimen.id" checked>
        <select aria-label="Values per grouped row"><option value="ALL">Keep all distinct values</option><option value="ONE" selected>Require one distinct value</option></select>
        <button>Add 1 selected feature</button>
      </section>
    </section>`);

    const broadSelector = 'select[aria-label="Values per grouped row"]';
    assert.equal(await page.locator(broadSelector).count(), 1,
      'the actual recovery policy remains in the Add Columns editor, not a chooser dialog');
    assert.equal(await page.locator('[role="dialog"]').count(), 0, 'the failed preview is inline, not a dialog');
    assert.equal(await page.locator(groupOneConflictOperationPolicySelector).count(), 1);
    assert.equal(await page.locator(groupOneConflictSelectedFieldSelector).isChecked(), true);

    const stateArgs = {
      editorSelector: '[aria-label="Add columns editor"]',
      proposalSelector: '[data-testid="construction-choice-proposal-panel"]',
      selectedFieldSelector: groupOneConflictSelectedFieldSelector,
    };
    assert.deepEqual(await page.evaluate(inspectGroupOneConflictRecoveryState, stateArgs), {
      editorOpen: true,
      proposalStatus: 'error',
      fieldSelected: true,
      policy: 'ONE',
      allAvailable: true,
    }, 'the exact failure leaves the editor, selected field, and recovery policy mounted');

    await selectOption(page, groupOneConflictOperationPolicySelector, 'ALL', {}, {
      action: async (_label, _locator, perform) => perform(),
    });
    assert.deepEqual(await page.evaluate(inspectGroupOneConflictRecoveryState, stateArgs), {
      editorOpen: true,
      proposalStatus: 'error',
      fieldSelected: true,
      policy: 'ALL',
      allAvailable: true,
    }, 'native ALL selection preserves the exact field for retry without re-selection');
    assert.equal(await page.getByRole('button', { name: 'Add 1 selected feature' }).isEnabled(), true);
  } finally {
    await browser.close();
  }
});

test('ONE 422 classification uses retained command and server IDs across independent captures', async () => {
  const expectedPath = '/api/v1/projects/loom_dev_cda_fhir/explorers/qa-reshape-group-one-conflict-19a476a1-ae2e-4526-8306-efea3e3b14/authoring/v2/construction-choice-proposals';
  const outputId = 'out_ffb2982f02985532d0aa7079';
  const expectedDraftVersion = 5;
  const expectedDraftDigest = 'sha256:9290914e187c41245f2337bcd14298579e104fb22b9f6eff9a944f895ab63f00';
  const expectedSnapshotToken = 'sha256:7e9a025ec2555033fd138a3cbf9eaecebb24f870a6b37cf5e86e0355c84b40f7';
  const commandId = '85e3b853-9d43-4191-a427-d16ffddc0dab';
  const serverRequestId = '4a9ed0a1-70ef-4817-9b8a-4f8c91cc6f73';
  const choiceId = 'cc2.eyJ2ZXJzaW9uIjoiY29uc3RydWN0aW9uLWNob2ljZS92MiIsImtpbmQiOiJGSUVMRCIsInNuYXBzaG90VG9rZW4iOiJzaGEyNTY6N2U5YTAyNWVjMjU1NTAzM2ZkMTM4YTNjYmY5ZWFlY2ViYjI0Zjg3MGE2YjM3Y2Y1ZTg2ZTAzNTVjODRiNDBmNyIsInNvdXJjZSI6eyJraW5kIjoiRklFTEQiLCJjYW5kaWRhdGVJZCI6ImNfNzVjNjdmNzEyZTIxYjFlODY2MDVkMmNhIiwibm9kZUlkIjoibl8wMDljYmIwODQyNTViZGEwNGU5Y2M1ZDIiLCJyZXNvdXJjZVR5cGUiOiJTcGVjaW1lbiIsInBhdGgiOiJpZCIsImNhcmRpbmFsaXR5Ijoib3B0aW9uYWxfb25lIn0sInJvdXRlIjpbXX0.9c9db3c0800d3a116f895d8a6da196649442be28c507f48fb455f55ae2aca58c';
  const makeResponse = (requestId = serverRequestId, code = 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES') => {
    const diagnostic = {
      code,
      message: 'Some grouped records have different values for this column. Keep all distinct values or choose more specific grouping fields.',
      requestId,
      severity: 'error',
      stage: 'preview',
    };
    return { diagnostics: [diagnostic], error: { code, diagnostic, message: diagnostic.message, requestId } };
  };
  const request = {
    commandId,
    snapshotToken: expectedSnapshotToken,
    expectedDraftVersion,
    expectedDraftDigest,
    outputId,
    constructionChoices: [{
      rowValuePolicy: 'ONE',
      choiceId,
      form: 'VALUE',
      title: 'Specimen ID',
    }],
    limit: 25,
  };
  const makeEntry = (browserRequestId, overrides = {}) => ({
    index: browserRequestId === 'playwright-388' ? 387 : 54,
    browserRequestId,
    method: 'POST',
    origin: 'http://127.0.0.1:30008',
    path: expectedPath,
    url: `http://127.0.0.1:30008${expectedPath}`,
    resourceType: 'fetch',
    request: structuredClone(request),
    response: makeResponse(),
    status: 422,
    networkComplete: true,
    responseCapture: 'captured',
    responseCaptureComplete: true,
    responseBodyCaptured: true,
    completedAt: 1791574219869,
    ...overrides,
  });
  const fixtureEntry = makeEntry('playwright-388');
  const workflowEntry = makeEntry('playwright-55');
  assert.notStrictEqual(fixtureEntry, workflowEntry, 'the capture views own distinct entries');
  assert.notEqual(fixtureEntry.browserRequestId, workflowEntry.browserRequestId,
    'browser request IDs are observer-local and are allowed to differ');
  assert.equal(fixtureEntry.requestId, undefined, 'the UI does not send the optional X-Request-ID header');
  assert.equal(workflowEntry.requestId, undefined, 'the workflow capture observes the same absent header');
  let classifiedEntry;
  const args = {
    fixtureEntry,
    workflowEntry,
    expectedPath,
    outputId,
    expectedDraftVersion,
    expectedDraftDigest,
    expectedSnapshotToken,
    classify: entry => { classifiedEntry = entry; return 'fixture-classified'; },
  };
  assert.equal(await classifyGroupOneExpectedONEFailure(args), 'fixture-classified');
  assert.strictEqual(classifiedEntry, fixtureEntry, 'the fixture-owned classifier receives its exact captured object');

  const rejects = async (badFixture, badWorkflow, pattern) => {
    await assert.rejects(classifyGroupOneExpectedONEFailure({
      ...args,
      fixtureEntry: badFixture,
      workflowEntry: badWorkflow,
      classify: () => { throw new Error('invalid captures must not reach the classifier'); },
    }), pattern);
  };
  await rejects(fixtureEntry, makeEntry('playwright-55', { request: { ...request, commandId: 'foreign-command' } }), /same submitted command/);
  await rejects(fixtureEntry, makeEntry('playwright-55', { response: makeResponse('foreign-server-request') }), /same server diagnostic request/);
  await rejects(fixtureEntry, makeEntry('playwright-55', { response: makeResponse(serverRequestId, 'INTERNAL_ERROR') }), /typed multiple-values diagnostic/);
  await rejects(fixtureEntry, makeEntry('playwright-55', { request: { ...request, expectedDraftVersion: 4 } }), /current draft version/);
  await rejects(fixtureEntry, makeEntry('playwright-55', { request: { ...request, expectedDraftDigest: 'sha256:stale-draft' } }), /current draft digest/);
  await rejects(fixtureEntry, makeEntry('playwright-55', { request: { ...request, snapshotToken: 'sha256:stale-catalog' } }), /current catalog snapshot/);
  await rejects(fixtureEntry, makeEntry('playwright-55', { request: { ...request, outputId: 'out_foreign' } }), /exact saved Group output/);
  await rejects(fixtureEntry, makeEntry('playwright-55', { path: '/api/v1/projects/foreign/explorers/other/authoring/v2/construction-choice-proposals' }), /exact owned choice-proposal path/);
});
