import assert from 'node:assert/strict';
import test from 'node:test';
import { chromium } from '@playwright/test';
import { readNativeGroupSummary } from '../../workflows/builder-patient-one-disagreement.mjs';

test('Patient ONE reads Summary 1 from the sibling Reshape editor', async () => {
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const page = await browser.newPage();
    await page.setContent(`
      <main>
        <section aria-label="Change editor">
          <section aria-label="Reshape editor">
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
            <div data-testid="construction-proposal-panel">
              <h3>Proposal preview</h3>
              <table>
                <thead><tr><th>Row count</th></tr></thead>
                <tbody><tr><td>2</td></tr></tbody>
              </table>
            </div>
          </section>
        </section>
      </main>
    `);

    assert.equal(await page.getByTestId('construction-proposal-panel')
      .locator('select[aria-label="Summary 1"]').count(), 0,
    'retained DOM shape places Summary 1 outside the proposal panel');
    assert.equal(await readNativeGroupSummary(page), 'COUNT_ROWS');
  } finally {
    await browser.close();
  }
});
