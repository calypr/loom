import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import test from 'node:test';
import { chromium } from '@playwright/test';
import { findRenderedBuilderHeaderIndex } from '../builder-rendered-grid.mjs';
import { readNativeGroupProposal, readNativeGroupSummary } from '../../workflows/builder-patient-one-disagreement.mjs';

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
