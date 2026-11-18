import assert from 'node:assert/strict';
import test from 'node:test';
import { readFixture, withFreshUIExplorer, createResourceTableInUI, api, assertRowsMatch } from './app.mjs';

test('Group by status with Count rows matches the literal Observation fixture and survives reload', { timeout: 180000 }, async () => {
  const observations = await readFixture('Observation');
  const expectedCount = observations.filter(row => row.status === 'final').length;
  await withFreshUIExplorer('group-count', (scenario, page) => createResourceTableInUI(scenario, page, 'Observation', { addFields: ['status'] }), async ({ scenario, fixture, page }) => {
    const before = await api(`${scenario.base}/builder`);
    const statusLabel = fixture.state.workspace.documents[0].columns.find(column => column.name === 'status')?.label ?? 'Status';
    await page.openRowSettings();
    await page.click('[data-testid="construction-row-intent-records"]');
    await page.click('[data-testid="construction-action-group-rows"]');
    await page.until(() => page.evaluate('Boolean(document.querySelector("[data-testid=construction-reshape-group]"))'), 'Group editor');
    await page.clickLabel(`Group by ${statusLabel}`, 'input');
    await page.click('[data-testid="construction-group-tab-summaries"]');
    await page.select('[aria-label="Add summary"]', 'COUNT_ROWS');
    await page.until(() => page.evaluate("document.querySelector('[data-testid=construction-proposal-panel]')?.dataset.proposalStatus === 'ready'"), 'Group count proposal');
    const proposalRows = await page.evaluate(`Array.from(document.querySelectorAll('[data-testid="construction-proposal-preview-row"]'),row=>row.innerText)`);
    assert.equal(proposalRows.length, 1, 'All fixture Observations share one status group');
    assert(proposalRows[0].includes('final'));
    assert(proposalRows[0].includes(String(expectedCount)), `Count matches ${expectedCount} literal Observation NDJSON rows`);
    await page.click('[data-testid="construction-apply-proposal"]');
    await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'Group count applied');
    const saved = await api(`${scenario.base}/builder`);
    assert.notEqual(saved.draftDigest, before.draftDigest, 'Apply saves Group and Count rows');
    const rows = await page.until(async () => { const result = await page.tableRows(); return result.length ? result : false; }, 'group result rows');
    assert.equal(rows.length, 1);
    assert(rows[0].some(value => value.includes('final')));
    assert(rows[0].some(value => value.includes(String(expectedCount))));
    await page.reload();
    assert.equal((await api(`${scenario.base}/builder`)).draftDigest, saved.draftDigest, 'Reload preserves the applied Group');
    const reloaded = await page.until(async () => { const result = await page.tableRows(); return result.length ? result : false; }, 'reloaded group result');
    assert.equal(reloaded.length, 1);
    assert(reloaded[0].some(value => value.includes('final')));
    assert(reloaded[0].some(value => value.includes(String(expectedCount))));
  });
});

test('Pivot Observation valueQuantity by status and preserve Cancel then Apply across reload', { timeout: 180000 }, async () => {
  const observations = await readFixture('Observation');
  const expected = observations.map(row => [row.id, row.valueQuantity?.value ?? null]);
  await withFreshUIExplorer('pivot', (scenario, page) => createResourceTableInUI(scenario, page, 'Observation', { addFields: ['status', 'valueQuantity.value'] }), async ({ scenario, fixture, page }) => {
    const before = await api(`${scenario.base}/builder`);
    const document = fixture.state.workspace.documents[0];
    const label = name => document.columns.find(column => column.source?.field?.path === name || column.source?.field?.path?.endsWith(name))?.label ?? name;
    const configurePivot = async () => {
      await page.openRowSettings();
      await page.click('[data-testid="construction-row-intent-reshape"]');
      await page.until(() => page.evaluate('Boolean(document.querySelector("[data-testid=construction-action-table-pivot-rows]"))'), 'table Pivot alternative');
      await page.click('[data-testid="construction-action-table-pivot-rows"]');
      try { await page.until(() => page.evaluate('Boolean(document.querySelector("[data-testid=construction-reshape-pivot]"))'), 'Pivot editor'); }
      catch (error) { throw new Error(`${error.message}; pivot UI=${JSON.stringify(await page.evaluate(`({text:document.body.innerText.slice(-1000),buttons:Array.from(document.querySelectorAll('button'),x=>x.getAttribute('aria-label')||x.innerText.trim()).filter(Boolean)})`))}`); }
      let selectedInputs = await page.evaluate(`Array.from(document.querySelectorAll('[data-testid=construction-column-selection] [aria-selected=true]'),item=>item.getAttribute('aria-label'))`);
      if (selectedInputs.includes('Remove Status (string)')) await page.clickLabel('Remove Status (string)', 'button');
      if (selectedInputs.includes('Remove Value Quantity Value (decimal)')) await page.clickLabel('Remove Value Quantity Value (decimal)', 'button');
      selectedInputs = await page.evaluate(`Array.from(document.querySelectorAll('[data-testid=construction-column-selection] [aria-selected=true]'),item=>item.getAttribute('aria-label'))`);
      if (!selectedInputs.includes('Remove Observation ID (string)')) {
        await page.click('[data-testid=construction-column-selection] [aria-label="Select Observation ID (string)"]');
      }
      selectedInputs = await page.evaluate(`Array.from(document.querySelectorAll('[data-testid=construction-column-selection] [aria-selected=true]'),item=>item.getAttribute('aria-label'))`);
      assert.deepEqual(selectedInputs, ['Remove Observation ID (string)'], 'Observation ID remains the per-row identity while Status and value become Pivot roles');
      await page.until(() => page.evaluate(`Array.from(document.querySelector('[aria-label=\"Pivot category field\"]')?.options ?? []).some(option=>option.textContent.includes(${JSON.stringify(label('status'))}))`), 'Pivot fields updated for the selected row key');
      await page.selectOptionMatching('[aria-label="Pivot category field"]', label('status'));
      await page.selectOptionMatching('[aria-label="Pivot values field"]', label('valueQuantity.value'));
      await page.clickLabel('Discover categories when proposing', 'input');
      await page.until(() => page.evaluate("document.querySelector('[data-testid=construction-proposal-panel]')?.dataset.proposalStatus === 'ready'"), 'Pivot proposal with categories discovered by the proposal');
      const proposalHeaders = await page.evaluate(`Array.from(document.querySelectorAll('[data-testid=construction-proposal-preview] th'),item=>item.innerText.trim().split('\\n')[0])`);
      assert(proposalHeaders.includes('final'), `Proposal exposes the Status category column discovered from the six fixture rows: ${proposalHeaders}`);
      const proposalRows = await page.evaluate(`Array.from(document.querySelectorAll('[data-testid=construction-proposal-preview-row]'),row=>Array.from(row.querySelectorAll('td'),cell=>cell.innerText.trim()))`);
      assert.equal(proposalRows.length, 6, 'Proposal retains all six literal Observation rows');
      assert(proposalRows.some(row=>row.includes('dev-observation-001') && row.includes('172.5')));
      assert(proposalRows.some(row=>row.includes('dev-observation-002') && row.includes('68')));
      assert(proposalRows.some(row=>row.includes('dev-observation-003') && row.includes('180')));
      return proposalHeaders.join(',');
    };

    await configurePivot();
    const proposalText = await page.text('[data-testid="construction-proposal-panel"]');
    assert(proposalText.includes('6 rows') || proposalText.includes('6 row'), 'Pivot preview row count reflects the six literal Observation rows');
    await page.pressEscape();
    const cancelled = await api(`${scenario.base}/builder`);
    assert.equal(cancelled.draftDigest, before.draftDigest, 'Cancel keeps the saved source table unchanged');

    await configurePivot();
    await page.click('[data-testid="construction-apply-proposal"]');
    await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'Pivot applied');
    const saved = await api(`${scenario.base}/builder`);
    assert.notEqual(saved.draftDigest, before.draftDigest, 'Apply saves the Pivot');
    const rows = await page.until(async () => { const result = await page.tableRows(); return result.length ? result : false; }, 'Pivot result rows');
    assertRowsMatch(rows, expected.map(([id, value]) => [id, value == null ? '—' : String(value)]), 'Pivot output');
    await page.reload();
    assert.equal((await api(`${scenario.base}/builder`)).draftDigest, saved.draftDigest, 'Reload preserves the applied Pivot');
    const reloadedRows = await page.until(async () => { const result = await page.tableRows(); return result.length ? result : false; }, 'reloaded Pivot result rows');
    assertRowsMatch(reloadedRows, expected.map(([id, value]) => [id, value == null ? '—' : String(value)]), 'reloaded Pivot output');
  });
});
