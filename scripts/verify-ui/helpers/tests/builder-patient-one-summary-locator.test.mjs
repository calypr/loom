import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import test from 'node:test';
import { chromium } from '@playwright/test';
import { findRenderedBuilderHeaderIndex } from '../builder-rendered-grid.mjs';
import {
  capturePatientOnePreOneState,
  isNativeGroupProposalReady,
  patientOneSavedState,
  openPolicyOneAddColumnsEditor,
  policyOneHistoryRemovalLocator,
  policyOneRowSettingsRemovalLocator,
  readNativeGroupProposal,
  readPatientOneChoiceEditor,
  readNativeGroupSummary,
  readPatientOnePreviewBinding,
  readPatientOneRenderedGrid,
} from '../../workflows/builder-patient-one-disagreement.mjs';

const requireFromLoomUI = createRequire(new URL('../../../../ui/packages/loom-ui/package.json', import.meta.url));
const { JSDOM } = requireFromLoomUI('jsdom');
const rowIntentProposalHTML = `
  <main>
    <section aria-label="Current table preview" data-testid="construction-preview">
      <div data-testid="preview-table-scroll"><table><tbody><tr><td>source rows</td></tr></tbody></table></div>
    </section>
    <dialog aria-label="Row definition settings" open>
      <section aria-label="Reshape editor" data-testid="construction-reshape-editor">
        <section aria-label="Summarize into groups" data-testid="construction-reshape-group">
          <input type="checkbox" aria-label="Group by Patient ID" />
        </section>
        <div role="group" aria-label="Summaries">
          <label>
            Summary
            <select aria-label="Summary 1">
              <option value="COUNT_ROWS" selected>Count rows</option>
              <option value="COUNT_NON_NULL">Count present values</option>
            </select>
          </label>
        </div>
      </section>
      <section aria-label="Proposed change" data-testid="construction-proposal-panel" data-proposal-status="ready">
        <div data-testid="construction-proposal-ready">
          <h3>Proposal preview</h3>
          <p>1 row and 2 columns · checked in 80 ms.</p>
        </div>
      </section>
      <div data-testid="construction-proposal-preview" data-preview-status="ready">
        <table>
          <thead><tr>
            <th><span>Patient ID</span><span>string</span></th>
            <th><span>Row count</span><span>integer</span></th>
          </tr></thead>
          <tbody><tr data-testid="construction-proposal-preview-row">
            <td title='["dev-patient-001","dev-patient-002"]'>dev-patient-001; dev-patient-002</td>
            <td title="2">2</td>
          </tr></tbody>
        </table>
        <p>Showing all 1 row in this proposal.</p>
      </div>
    </dialog>
  </main>
`;
const policyOneAddColumnsHTML = `
  <style>
    #add-columns-action { position: absolute; top: 1rem; left: 1rem; z-index: 1; }
    #row-settings-overlay { position: fixed; inset: 0; z-index: 50; overflow-y: auto; background: white; }
  </style>
  <main>
    <button id="add-columns-action" type="button" data-testid="construction-action-add-columns"
      aria-label="Add columns: Bring more information into each row.">Add columns</button>
    <section aria-label="Add columns editor" data-testid="construction-operation-editor"
      data-output-id="out_patients" hidden>
      <label>
        Values from each row's contributing records
        <select aria-label="Values per grouped row">
          <option value="ALL" selected>Keep all distinct values</option>
          <option value="ONE">Require one distinct value</option>
        </select>
      </label>
      <div role="tablist" aria-label="Column types">
        <button id="add-columns-tab-coded" type="button" role="tab" aria-selected="true">Coded values</button>
        <button id="add-columns-tab-fields" type="button" role="tab" aria-selected="false">Other fields</button>
      </div>
      <div id="add-columns-tabpanel" role="tabpanel" aria-labelledby="add-columns-tab-coded" hidden>
        <div data-testid="feature-catalog-raw-fields">
          <button type="button" aria-label="Select Patient.id" aria-pressed="false">Patient ID</button>
        </div>
      </div>
      <aside aria-label="Selected feature action" hidden>
        <p role="status">1 feature selected</p>
        <button type="button">Add 1 selected feature</button>
      </aside>
    </section>
    <div id="row-settings-overlay">
      <div role="dialog" aria-modal="true" aria-label="Row definition settings">
        <button id="back-to-table" type="button">Back to table</button>
        <button type="button" data-testid="construction-action-group-rows" aria-pressed="true">
          Group and summarize
        </button>
      </div>
    </div>
  </main>
  <script>
    const overlay = document.querySelector('#row-settings-overlay');
    document.querySelector('#back-to-table').addEventListener('click', () => overlay.remove());
    const addColumns = document.querySelector('#add-columns-action');
    const editor = document.querySelector('[aria-label="Add columns editor"]');
    addColumns.addEventListener('click', () => { editor.hidden = false; });
    const codedTab = document.querySelector('#add-columns-tab-coded');
    const fieldsTab = document.querySelector('#add-columns-tab-fields');
    const tabPanel = document.querySelector('#add-columns-tabpanel');
    fieldsTab.addEventListener('click', () => {
      codedTab.setAttribute('aria-selected', 'false');
      fieldsTab.setAttribute('aria-selected', 'true');
      tabPanel.setAttribute('aria-labelledby', fieldsTab.id);
      tabPanel.hidden = false;
    });
    const field = document.querySelector('[aria-label="Select Patient.id"]');
    const action = document.querySelector('[aria-label="Selected feature action"]');
    field.addEventListener('click', () => {
      field.setAttribute('aria-pressed', 'true');
      action.hidden = false;
    });
    action.querySelector('button').addEventListener('click', () => {
      const error = document.createElement('p');
      error.setAttribute('role', 'alert');
      error.textContent = 'Patient.id has multiple distinct values.';
      action.append(error);
    });
  </script>
`;

test('Patient ONE reads Summary 1 from the sibling Reshape editor', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(rowIntentProposalHTML);
    await page.waitForFunction(isNativeGroupProposalReady, { expectedCount: '2' }, { timeout: 1000 });

    assert.equal(await page.getByTestId('construction-proposal-panel')
      .locator('select[aria-label="Summary 1"]').count(), 0,
    'retained DOM shape places Summary 1 outside the proposal panel');
    assert.equal(await readNativeGroupSummary(page), 'COUNT_ROWS');
    assert.equal(await page.getByTestId('construction-preview')
      .getByTestId('construction-proposal-preview').count(), 0,
    'row-intent proposal preview is outside the current table preview container');
    assert.equal(await page.getByTestId('construction-proposal-panel').locator('table').count(), 0,
      'the proposal summary contains no table; its preview is a separate row-intent sibling');
    const proposal = await page.evaluate(readNativeGroupProposal);
    assert.equal(proposal.proposalSummary, '1 row and 2 columns · checked in 80 ms.');
    assert.deepEqual(proposal.groupKeys, []);
    assert.equal(proposal.rows.length, 1);
    const idIndex = findRenderedBuilderHeaderIndex(proposal.headers, 'Patient ID');
    const countIndex = findRenderedBuilderHeaderIndex(proposal.headers, 'Row count');
    assert(idIndex >= 0, `rendered proposal must include Patient ID: ${JSON.stringify(proposal.headers)}`);
    assert(countIndex >= 0, `rendered proposal must include Row count: ${JSON.stringify(proposal.headers)}`);
    assert.deepEqual(JSON.parse(proposal.rows[0][idIndex].raw), ['dev-patient-001', 'dev-patient-002']);
    assert.equal(proposal.rows[0][countIndex].text, '2');
  } finally {
    await browser.close();
  }
});

test('Patient ONE proposal reader and ready wait observe the row-intent preview outside the current table preview', () => {
  const { window } = new JSDOM(rowIntentProposalHTML);
  const proposalPanel = window.document.querySelector('[data-testid="construction-proposal-panel"]');
  assert.equal(proposalPanel.querySelector('table'), null,
    'the proposal summary is separate from the rendered preview table');
  assert.equal(window.document.querySelector('[data-testid="construction-preview"] [data-testid="construction-proposal-preview"]'), null,
    'row-intent candidate preview is not nested under the current table preview');
  assert.equal(isNativeGroupProposalReady({ expectedCount: '2' }, window.document), true,
    'the ready predicate must observe the exact empty-key COUNT_ROWS candidate');
  const proposal = readNativeGroupProposal(window.document);
  assert.equal(proposal.proposalSummary, '1 row and 2 columns · checked in 80 ms.');
  assert.deepEqual(proposal.groupKeys, []);
  assert.equal(proposal.rows.length, 1);
  const idIndex = findRenderedBuilderHeaderIndex(proposal.headers, 'Patient ID');
  const countIndex = findRenderedBuilderHeaderIndex(proposal.headers, 'Row count');
  assert(idIndex >= 0, `rendered proposal must include Patient ID: ${JSON.stringify(proposal.headers)}`);
  assert(countIndex >= 0, `rendered proposal must include Row count: ${JSON.stringify(proposal.headers)}`);
  assert.deepEqual(JSON.parse(proposal.rows[0][idIndex].raw), ['dev-patient-001', 'dev-patient-002']);
  assert.equal(proposal.rows[0][countIndex].text, '2');
});

test('Policy ONE returns from Group settings before exercising the Add Columns through submit DOM contract', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(policyOneAddColumnsHTML);
    const savedState = {
      workspace: { documents: [{ output: { id: 'out_patients' }, construction: { steps: [{ id: 'group-1' }] } }] },
      draftVersion: 3,
      draftDigest: 'draft-after-group',
    };
    const readBuilderCalls = [];
    const workflowActionLabels = [];
    const workflow = {
      page,
      action: async (label, _locator, click, { after } = {}) => {
        workflowActionLabels.push(label);
        await click();
        await after?.();
      },
    };

    await openPolicyOneAddColumnsEditor(workflow, {
      readBuilder: async () => {
        readBuilderCalls.push(true);
        return structuredClone(savedState);
      },
      expectedSavedState: savedState,
    });
    assert.deepEqual(workflowActionLabels, [
      'return to table after applying empty-key COUNT_ROWS Group',
      'open Add Columns for raw Patient ID',
    ]);
    assert.equal(readBuilderCalls.length, 1,
      'the workflow must independently read the saved Group frame after closing row settings and before opening Add Columns');
    assert.equal(await page.getByRole('dialog', { name: 'Row definition settings', exact: true }).count(), 0);

    const editor = page.getByRole('region', { name: 'Add columns editor', exact: true });
    const policy = editor.getByRole('combobox', { name: 'Values per grouped row', exact: true });
    await policy.selectOption('ONE');
    await editor.getByRole('tab', { name: 'Other fields', exact: true }).click();
    const patientIDChoice = editor.locator('[data-testid="feature-catalog-raw-fields"] button[aria-label="Select Patient.id"]');
    assert.equal(await patientIDChoice.count(), 1);
    await patientIDChoice.click();
    assert.equal(await patientIDChoice.getAttribute('aria-pressed'), 'true');

    const selectedOne = await page.evaluate(readPatientOneChoiceEditor);
    assert.equal(selectedOne.editorOpen, true);
    assert.equal(selectedOne.editorDocumentId, 'out_patients');
    assert.equal(selectedOne.selectedField, 'Patient.id');
    assert.equal(selectedOne.fieldSelected, true);
    assert.equal(selectedOne.policy, 'ONE');
    assert.deepEqual(selectedOne.availablePolicies, ['ALL', 'ONE']);
    assert.equal(selectedOne.errorVisible, false);

    await editor.getByRole('button', { name: 'Add 1 selected feature', exact: true }).click();
    await editor.locator('[aria-label="Selected feature action"] [role="alert"]')
      .waitFor({ state: 'visible' });
    const rejectedOne = await page.evaluate(readPatientOneChoiceEditor);
    assert.equal(rejectedOne.editorMountIdentity, selectedOne.editorMountIdentity);
    assert.equal(rejectedOne.selectedField, 'Patient.id');
    assert.equal(rejectedOne.policy, 'ONE');
    assert.equal(rejectedOne.errorVisible, true);
    assert.match(rejectedOne.errorText, /multiple distinct values/);
  } finally {
    await browser.close();
  }
});

test('Patient ONE recovery observation retains the same editor controls and exact rendered rows', () => {
  const recoveryMarkup = `
    <section aria-label="Add columns editor" data-output-id="out_patients">
      <select aria-label="Values per grouped row">
        <option value="ALL">Keep all distinct values</option>
        <option value="ONE" selected>Require one distinct value</option>
      </select>
      <div data-testid="feature-catalog-raw-fields">
        <button type="button" aria-label="Select Patient.id" aria-pressed="true">Patient.id</button>
      </div>
      <aside aria-label="Selected feature action">
        <p role="alert">Patient.id has multiple distinct values</p>
      </aside>
    </section>
    <div data-testid="preview-table-scroll"><div role="table">
      <div role="row"><div role="columnheader">Patient ID</div><div role="columnheader">Row count</div></div>
      <div role="row"><div role="cell" title='["dev-patient-001","dev-patient-002"]'>dev-patient-001; dev-patient-002</div><div role="cell" title="2">2</div></div>
    </div></div>`;
  const { window } = new JSDOM(recoveryMarkup);

  const editor = readPatientOneChoiceEditor(window.document);
  assert.equal(typeof editor.documentTimeOrigin, 'number');
  assert.equal(editor.editorDocumentId, 'out_patients');
  assert.match(editor.editorMountIdentity, /^editor-\d+$/);
  assert.equal(editor.selectedField, 'Patient.id');
  assert.deepEqual(editor.availablePolicies, ['ALL', 'ONE']);
  assert.equal(editor.editorOpen, true);
  assert.equal(editor.fieldSelected, true);
  assert.equal(editor.policy, 'ONE');
  assert.equal(editor.allAvailable, true);
  assert.equal(editor.errorVisible, true);
  assert.match(editor.errorText, /multiple distinct values/);
  assert.equal(readPatientOneChoiceEditor(window.document).editorMountIdentity, editor.editorMountIdentity,
    'repeated observation must identify the same actual editor element');

  const mountedEditor = window.document.querySelector('[aria-label="Add columns editor"]');
  mountedEditor.replaceWith(mountedEditor.cloneNode(true));
  assert.notEqual(readPatientOneChoiceEditor(window.document).editorMountIdentity, editor.editorMountIdentity,
    'replacing the editor element must produce a different observed mount identity');

  const grid = readPatientOneRenderedGrid(window.document);
  assert.deepEqual(grid.headers, ['Patient ID', 'Row count']);
  assert.equal(grid.cells.length, 1);
  assert.deepEqual(JSON.parse(grid.cells[0][0].raw), ['dev-patient-001', 'dev-patient-002']);
  assert.equal(grid.cells[0][1].text, '2');
});

test('Patient ONE pre-ONE artifact binds the saved Builder document to its rendered receipt identity', () => {
  const draftDigest = `sha256:${'b'.repeat(64)}`;
  const snapshotToken = `sha256:${'a'.repeat(64)}`;
  const previewMarkup = `
    <section data-testid="construction-preview" data-preview-status="ready"
      data-preview-receipt-id="receipt-group-123" data-preview-output-id="out_patients"
      data-current-draft-version="3" data-current-draft-digest="${draftDigest}">
      <div data-testid="preview-table-scroll"><div role="table">
        <div role="row"><div role="columnheader">ROW COUNT</div></div>
        <div role="row"><div role="cell">2</div></div>
      </div></div>
    </section>`;
  const { window } = new JSDOM(previewMarkup);
  const savedDocument = {
    kind: 'ExplorerBuilderDocument',
    output: { id: 'out_patients', title: 'Patients' },
    rootResourceType: 'Patient',
    route: { occurrenceId: 'root', resourceType: 'Patient' },
    columns: [{ columnId: 'c_count', column: 'row_count', label: 'Row count' }],
    construction: {
      steps: [{ id: 'step_group', operation: {
        kind: 'GROUP', group: { keys: [], aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: 'c_count' }] },
      } }],
    },
  };
  const builder = {
    apiVersion: 'loom.calypr.org/explorer-authoring/v2',
    kind: 'ExplorerBuilderState',
    lifecycleState: 'READY',
    draftVersion: 3,
    draftDigest,
    catalog: { generation: 'devloop-v1', snapshotToken },
    workspace: {
      apiVersion: 'loom.calypr.org/explorer-authoring/v2',
      kind: 'ExplorerBuilderWorkspace',
      explorer: { title: 'Patients' },
      documents: [savedDocument],
      tabs: [{ id: 'patients', title: 'Patients', outputId: 'out_patients', order: 0, visible: true }],
    },
  };
  const previewBinding = readPatientOnePreviewBinding(window.document);
  const artifact = capturePatientOnePreOneState({
    builder,
    previewBinding,
    renderedGrid: { headers: ['ROW COUNT'], rows: [['2']] },
    project: 'loom_dev_verify_owned',
    explorerId: 'verify-owned-patient-one',
    outputId: 'out_patients',
  });

  assert.deepEqual(artifact.savedDocument, savedDocument,
    'capture retains the exact saved API workspace document, including the applied Group step');
  assert.deepEqual(artifact.draftBinding, {
    version: 3, digest: draftDigest, generation: 'devloop-v1', snapshotToken,
  });
  assert.deepEqual(artifact.outputBinding, {
    outputId: 'out_patients', title: 'Patients', rootResourceType: 'Patient',
    receiptId: 'receipt-group-123', previewStatus: 'ready',
  });
  assert.deepEqual(artifact.renderedPreview, {
    outputId: 'out_patients', receiptId: 'receipt-group-123', draftVersion: 3,
    draftDigest, headers: ['ROW COUNT'], rows: [['2']],
  });
  assert.throws(() => capturePatientOnePreOneState({
    builder,
    previewBinding: { ...previewBinding, draftDigest: 'sha256:stale' },
    renderedGrid: { headers: ['ROW COUNT'], rows: [['2']] },
    project: 'loom_dev_verify_owned', explorerId: 'verify-owned-patient-one', outputId: 'out_patients',
  }), /bind the Builder draft digest/);
});

test('Policy removal retry switches from row-settings to current Row operations history after Cancel', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    const outputId = 'out_patients';
    const currentGroupStepId = 'group-after-all-reload';
    const staleGroupStepId = 'group-stale-snapshot';
    await page.setContent(`<!doctype html><html><body>
      <dialog open aria-label="Row definition settings">
        <button type="button" data-testid="construction-row-remove-${currentGroupStepId}">Remove</button>
      </dialog>
      <section aria-label="Row operations" data-testid="construction-history">
        <button type="button" aria-pressed="true" data-testid="construction-history-step-${currentGroupStepId}">Group rows</button>
        <button type="button" data-testid="construction-remove-step-${currentGroupStepId}">Remove</button>
      </section>
      <section data-testid="construction-proposal-panel" hidden>
        <button type="button" data-testid="construction-cancel-proposal">Cancel</button>
      </section>
      <script>
        const rowSettings = document.querySelector('[aria-label="Row definition settings"]');
        const proposal = document.querySelector('[data-testid="construction-proposal-panel"]');
        document.querySelector('[data-testid="construction-row-remove-${currentGroupStepId}"]').addEventListener('click', () => {
          rowSettings.remove();
          proposal.hidden = false;
        });
        document.querySelector('[data-testid="construction-cancel-proposal"]').addEventListener('click', () => proposal.remove());
      </script>
    </body></html>`);

    const builder = {
      draftVersion: 4,
      draftDigest: 'sha256:all-reloaded-draft',
      catalog: { snapshotToken: 'sha256:all-reloaded-snapshot' },
      workspace: { documents: [{
        output: { id: outputId, title: 'Patients' },
        rootResourceType: 'Patient',
        columns: [{ columnId: 'source-patient-id', label: 'Patient ID', source: { field: { path: 'id' } } }],
        construction: { steps: [{
          id: currentGroupStepId,
          operation: { kind: 'GROUP', group: { keys: [], aggregates: [{ operation: 'COUNT_ROWS' }] } },
        }] },
      }] },
    };
    const savedBeforeCancel = patientOneSavedState(builder, outputId);
    assert.equal(savedBeforeCancel.group.stepId, currentGroupStepId);
    assert.equal(await policyOneRowSettingsRemovalLocator(page, currentGroupStepId).count(), 1,
      'the first removal is available from the open row-settings panel');
    assert.equal(await policyOneRowSettingsRemovalLocator(page, staleGroupStepId).count(), 0,
      'the row-settings control rejects a stale Group identity');

    await policyOneRowSettingsRemovalLocator(page, currentGroupStepId).click();
    await page.getByTestId('construction-proposal-panel').waitFor({ state: 'visible' });
    assert.equal(await page.getByRole('dialog', { name: 'Row definition settings', exact: true }).count(), 0,
      'the row-settings removal wrapper closes its dialog before the proposal is canceled');
    await page.getByTestId('construction-cancel-proposal').click();
    await page.getByTestId('construction-proposal-panel').waitFor({ state: 'detached' });

    const savedAfterCancel = patientOneSavedState(structuredClone(builder), outputId);
    assert.deepEqual(savedAfterCancel, savedBeforeCancel,
      'Cancel preserves the saved ALL Group, workspace, and draft identity');
    const retryGroupStepId = savedAfterCancel.group.stepId;
    assert.equal(retryGroupStepId, currentGroupStepId,
      'the retry uses the Group identity from the post-ALL saved frame');
    assert.equal(await policyOneRowSettingsRemovalLocator(page, retryGroupStepId).count(), 0,
      'the row-settings removal control is absent after Cancel closes that surface');
    assert.equal(await page.getByTestId(`construction-history-step-${retryGroupStepId}`).getAttribute('aria-pressed'), 'true');
    assert.equal(await policyOneHistoryRemovalLocator(page, retryGroupStepId).count(), 1,
      'the retry control is the selected Group entry in top-level Row operations history');
    assert.equal(await policyOneHistoryRemovalLocator(page, staleGroupStepId).count(), 0,
      'the Row operations retry locator rejects a stale Group identity');
  } finally {
    await browser.close();
  }
});
