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

test('ONE failure classification uses the exact fixture capture after cross-checking the workflow capture', async () => {
  const expectedPath = '/api/v1/projects/loom_dev_cda_fhir/explorers/owned-explorer/authoring/v2/construction-choice-proposals';
  const outputId = 'out_group_1';
  const expectedDraftVersion = 7;
  const expectedDraftDigest = 'sha256:draft-7';
  const expectedSnapshotToken = 'sha256:catalog-7';
  const makeEntry = (overrides = {}) => ({
    method: 'POST',
    path: expectedPath,
    status: 422,
    requestId: 'server-request-9',
    browserRequestId: 'playwright-9',
    completedAt: 123,
    body: {
      outputId,
      expectedDraftVersion,
      expectedDraftDigest,
      snapshotToken: expectedSnapshotToken,
      constructionChoices: [{ rowValuePolicy: 'ONE', title: 'Specimen ID' }],
    },
    response: { error: { code: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' } },
    ...overrides,
  });
  const fixtureEntry = makeEntry();
  const workflowEntry = structuredClone(fixtureEntry);
  assert.notStrictEqual(fixtureEntry, workflowEntry, 'the two capture trackers own different objects for one request');
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
  await rejects(fixtureEntry, makeEntry({ browserRequestId: 'foreign-browser-request' }), /same browser request/);
  await rejects(fixtureEntry, makeEntry({ response: { error: { code: 'INTERNAL_ERROR' } } }), /typed multiple-values diagnostic/);
  await rejects(fixtureEntry, makeEntry({ requestId: 'foreign-server-request' }), /same captured request/);
  await rejects(fixtureEntry, makeEntry({ body: { ...workflowEntry.body, expectedDraftVersion: 6 } }), /current draft version/);
  await rejects(fixtureEntry, makeEntry({ body: { ...workflowEntry.body, expectedDraftDigest: 'sha256:stale-draft' } }), /current draft digest/);
  await rejects(fixtureEntry, makeEntry({ body: { ...workflowEntry.body, snapshotToken: 'sha256:stale-catalog' } }), /current catalog snapshot/);
  await rejects(fixtureEntry, makeEntry({ body: { ...workflowEntry.body, outputId: 'out_foreign' } }), /exact saved Group output/);
  await rejects(fixtureEntry, makeEntry({ path: '/api/v1/projects/foreign/explorers/other/authoring/v2/construction-choice-proposals' }), /exact owned choice-proposal path/);
});
