import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import test from 'node:test';
import { chromium } from '@playwright/test';
import { findRenderedBuilderHeaderIndex } from '../builder-rendered-grid.mjs';
import {
  capturePatientOnePreOneState,
  readNativeGroupProposal,
  readNativeGroupSummary,
  readPatientOnePreviewBinding,
} from '../../workflows/builder-patient-one-disagreement.mjs';

const requireFromLoomUI = createRequire(new URL('../../../../ui/packages/loom-ui/package.json', import.meta.url));
const { JSDOM } = requireFromLoomUI('jsdom');
const siblingProposalHTML = `
  <main>
    <section aria-label="Change editor">
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
      <section aria-label="Proposed change">
        <div data-testid="construction-proposal-panel" data-proposal-status="ready">
          <div data-testid="construction-proposal-ready">
            <h3>Proposal preview</h3>
            <p>1 row and 2 columns · checked in 80 ms.</p>
          </div>
        </div>
      </section>
    </section>
    <section aria-label="Table result" data-testid="construction-preview">
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
    </section>
  </main>
`;

test('Patient ONE reads Summary 1 from the sibling Reshape editor', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(siblingProposalHTML);

    assert.equal(await page.getByTestId('construction-proposal-panel')
      .locator('select[aria-label="Summary 1"]').count(), 0,
    'retained DOM shape places Summary 1 outside the proposal panel');
    assert.equal(await readNativeGroupSummary(page), 'COUNT_ROWS');
    assert.equal(await page.getByTestId('construction-proposal-panel').locator('table').count(), 0,
      'the proposal summary contains no table; its preview renders in the sibling result region');
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

test('Patient ONE proposal reader gets raw IDs and count from the sibling rendered table', () => {
  const { window } = new JSDOM(siblingProposalHTML);
  const proposalPanel = window.document.querySelector('[data-testid="construction-proposal-panel"]');
  assert.equal(proposalPanel.querySelector('table'), null,
    'the proposal summary is separate from the rendered preview table');
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
