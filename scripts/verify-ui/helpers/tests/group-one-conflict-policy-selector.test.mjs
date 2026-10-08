import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chromium } from 'playwright';
import { browserEval, selectOption } from '../cda-playwright.mjs';
import {
  groupOneConflictChoicePolicySelector,
  groupOneConflictChoiceDialogSelector,
  groupOneConflictOperationPolicySelector,
  groupOneConflictSelectedFieldSelector,
  inspectGroupOneConflictChooserState,
  classifyGroupOneExpectedONEFailure,
} from '../../workflows/verify-cda-group-one-conflict-browser.mjs';

test('Group ONE retry selects ALL in the chooser when both policy controls are mounted', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`<section aria-label="Add columns editor">
      <input type="checkbox" aria-label="Select Specimen.id" checked>
      <select aria-label="Values per grouped row"><option value="ONE" selected>Require one distinct value</option><option value="ALL">Keep all distinct values</option></select>
    </section>
    <section role="dialog" aria-labelledby="catalog-selection-dialog-title">
      <h2 id="catalog-selection-dialog-title">Choose how to add these fields</h2>
      <select aria-label="Values per grouped row"><option value="ONE" selected>Require one distinct value</option><option value="ALL">Keep all distinct values</option></select>
    </section>`);

    const broadSelector = 'select[aria-label="Values per grouped row"]';
    assert.equal(await page.locator(broadSelector).count(), 2,
      'the operation editor and failed-choice dialog both expose this label');
    await assert.rejects(
      selectOption(page, broadSelector, 'ALL', {}, { action: async (_label, _locator, perform) => perform() }),
      /Expected exactly one target/,
    );
    assert.equal(await page.locator(groupOneConflictOperationPolicySelector).count(), 1);
    assert.equal(await page.locator(groupOneConflictChoicePolicySelector).count(), 1);
    assert.equal(await page.locator(`${groupOneConflictChoiceDialogSelector} input[aria-label="Select Specimen.id"]`).count(), 0,
      'the field checkbox remains in the operation editor outside the chooser portal');
    assert.equal(await page.locator(groupOneConflictSelectedFieldSelector).isChecked(), true);

    const stateArgs = {
      dialogSelector: groupOneConflictChoiceDialogSelector,
      selectedFieldSelector: groupOneConflictSelectedFieldSelector,
    };
    assert.deepEqual(await browserEval(page, inspectGroupOneConflictChooserState, stateArgs), {
      dialogOpen: true,
      fieldSelected: true,
      policy: 'ONE',
      allAvailable: true,
    }, 'ONE failure state must read the exact selected field from the operation editor');

    await selectOption(page, groupOneConflictChoicePolicySelector, 'ALL', {}, {
      action: async (_label, _locator, perform) => perform(),
    });
    const values = await page.locator(broadSelector).evaluateAll(selects => selects.map(select => select.value));
    assert.deepEqual(values, ['ONE', 'ALL'], 'the scoped native action changes only the chooser policy');
    assert.deepEqual(await browserEval(page, inspectGroupOneConflictChooserState, stateArgs), {
      dialogOpen: true,
      fieldSelected: true,
      policy: 'ALL',
      allAvailable: true,
    }, 'ALL recovery keeps the exact Specimen.id field selected');
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
  await rejects(fixtureEntry, makeEntry({ requestId: 'foreign-server-request' }), /same server request/);
  await rejects(fixtureEntry, makeEntry({ body: { ...workflowEntry.body, expectedDraftVersion: 6 } }), /current draft version/);
  await rejects(fixtureEntry, makeEntry({ body: { ...workflowEntry.body, expectedDraftDigest: 'sha256:stale-draft' } }), /current draft digest/);
  await rejects(fixtureEntry, makeEntry({ body: { ...workflowEntry.body, snapshotToken: 'sha256:stale-catalog' } }), /current catalog snapshot/);
  await rejects(fixtureEntry, makeEntry({ body: { ...workflowEntry.body, outputId: 'out_foreign' } }), /exact saved Group output/);
  await rejects(fixtureEntry, makeEntry({ path: '/api/v1/projects/foreign/explorers/other/authoring/v2/construction-choice-proposals' }), /exact owned choice-proposal path/);
});
