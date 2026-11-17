import assert from 'node:assert/strict';
import test from 'node:test';
import { readFixture, assertRowsMatch, createPatientTable, withScenario, api } from './app.mjs';

test('changing the starting record preserves Cancel and persists Apply after reload', { timeout: 120000 }, async () => {
  const observations = await readFixture('Observation');
  const expected = observations.map(row => [row.subject?.reference?.split('/').at(-1) ?? '—']);
  await withScenario('root-change', scenario => createPatientTable(scenario), async ({ scenario, fixture, page }) => {
    const before = await api(`${scenario.base}/builder`);
    await page.openRowSettings();
    await page.click('[data-testid="construction-row-intent-records"]');
    await page.selectOptionMatching('[aria-label="Record type"]', 'Observation');
    await page.until(() => page.evaluate('Boolean([...document.querySelectorAll("button")].find(button=>button.innerText.trim()==="Apply row change"))'), 'starting-record change proposal');
    const reviewButtons = await page.evaluate(`Array.from(document.querySelectorAll('button'),x=>x.innerText.trim()).filter(text=>text==='Keep current rows'||text==='Apply row change')`);
    assert.deepEqual(reviewButtons.sort(), ['Apply row change', 'Keep current rows'], 'Review offers Cancel and Apply actions');
    await page.clickText('Keep current rows');
    const cancelled = await api(`${scenario.base}/builder`);
    assert.equal(cancelled.draftDigest, before.draftDigest, 'Cancel must preserve saved draft');
    assert.equal(cancelled.workspace.documents[0].rootResourceType, 'Patient');

    await page.openRowSettings();
    await page.click('[data-testid="construction-row-intent-records"]');
    await page.selectOptionMatching('[aria-label="Record type"]', 'Observation');
    await page.until(() => page.evaluate('Boolean([...document.querySelectorAll("button")].find(button=>button.innerText.trim()==="Apply row change"))'), 'starting-record change proposal after reopen');
    await page.clickText('Apply row change');
    await page.until(() => page.evaluate('!document.body.innerText.includes("Keep current rows")'), 'root replacement applied');
    await page.reload();
    const saved = await api(`${scenario.base}/builder`);
    assert.equal(saved.workspace.documents[0].rootResourceType, 'Observation');
    await page.until(() => page.evaluate('document.querySelectorAll("[data-testid=preview-table-scroll] [role=row],tbody tr").length > 1'), 'loaded Observation preview');
    const actual = await page.tableRows();
    assertRowsMatch(actual, expected, 'Observation preview after Apply and reload');
  });
});
