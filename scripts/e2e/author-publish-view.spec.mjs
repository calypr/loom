import assert from 'node:assert/strict';
import test from 'node:test';
import {
  readFixture, api, withFreshUIExplorer, builderURL, createPatientTableInUI,
  waitForDownload, inspectZip,
} from './app.mjs';

test('author, publish, filter in Viewer, reload and download fixture rows', { timeout: 180000 }, async () => {
  const patients = await readFixture('Patient');
  const female = patients.filter(row => row.gender === 'female');
  await withFreshUIExplorer('author-publish-view', (scenario, page) => createPatientTableInUI(scenario, page, {
    addFields: ['name.family', 'gender'],
  }), async ({ scenario, fixture, page, downloadStarts, downloadStates, downloads }) => {
    const previewText = await page.until(async () => {
      const value = await page.evaluate('document.querySelector("[data-testid=preview-table-scroll]")?.innerText');
      return value || false;
    }, 'automatic Patient preview');
    for (const patient of patients) {
      assert(previewText.includes(patient.id), `Preview includes fixture Patient ${patient.id}`);
      assert(previewText.includes(patient.gender ?? '—'), `Preview includes fixture gender for ${patient.id}`);
      for (const name of patient.name ?? []) assert(previewText.includes(name.family), `Preview includes fixture family ${name.family}`);
    }
    await page.click('[data-testid="construction-action-keep-rows"]');
    await page.until(() => page.evaluate('Boolean(document.querySelector("[data-testid=construction-filter-editor]"))'), 'Filter rows editor');
    await page.selectOptionMatching('[data-testid="construction-filter-editor"] select[aria-label="Column"]', 'gender');
    await page.select('[data-testid="construction-filter-editor"] select[aria-label="Condition"]', 'EQUALS');
    await page.fill('[data-testid="construction-filter-editor"] [aria-label="Value"]', 'female');
    await page.until(() => page.evaluate("document.querySelector('[data-testid=construction-proposal-panel]')?.dataset.proposalStatus === 'ready'"), 'gender filter proposal');
    const proposalRows = await page.evaluate(`Array.from(document.querySelectorAll('[data-testid="construction-proposal-preview-row"]'),row=>row.innerText)`);
    assert(proposalRows.some(row => row.includes(female[0].id)), 'Filter proposal contains the literal female Patient row');
    assert(!proposalRows.some(row => patients.some(patient => patient.gender !== 'female' && row.includes(patient.id))), 'Filter proposal excludes non-female Patients');
    await page.click('[data-testid="construction-apply-proposal"]');
    await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'gender filter applied');
    const before = await api(`${scenario.base}/builder`);
    await page.reload();
    const reloaded = await api(`${scenario.base}/builder`);
    assert.equal(reloaded.draftDigest, before.draftDigest, 'Reload preserves the saved authored table and filter');
    const reloadedRows = await page.until(async () => { const rows = await page.tableRows(); return rows.length ? rows : false; }, 'reloaded Builder rows');
    assert(reloadedRows.some(row => row.some(cell => cell.includes(female[0].id))), 'Reloaded preview retains female Patient');
    assert(!reloadedRows.some(row => patients.some(patient => patient.gender !== 'female' && row.some(cell => cell.includes(patient.id)))), 'Reloaded filter excludes non-female Patients');

    await page.clickLabel('Publish');
    const published = await page.until(async () => {
      const explorers = await api(scenario.root);
      return explorers.find(item => item.explorerId === scenario.explorerId && item.activeRevisionId) ?? false;
    }, 'published revision becomes active', 60000);
    assert(published.activeRevisionId, 'Publish activates a new revision');
    await page.navigate(await builderURL(scenario, 'viewer'));
    await page.until(() => page.evaluate('document.body.innerText.includes("Download dataset")'), 'published Viewer');
    const viewerRows = await page.until(async () => { const rows = await page.tableRows(); return rows.length ? rows : false; }, 'published Viewer rows');
    assert(viewerRows.some(row => row.some(cell => cell.includes(female[0].id))), 'Viewer contains the filtered fixture Patient');
    assert(!viewerRows.some(row => patients.some(patient => patient.gender !== 'female' && row.some(cell => cell.includes(patient.id)))), 'Viewer excludes non-female Patients');

    await page.clickLabel('Download dataset');
    await page.until(() => page.evaluate('Boolean(document.querySelector("[role=dialog] a[download]"))'), 'prepared dataset download');
    await page.clickLabel('Download ZIP', 'a');
    const download = await waitForDownload({ downloads, downloadStarts, downloadStates });
    assert.match(download.filename, /\.zip$/i);
    const archive = inspectZip(download.path);
    assert.equal(archive.manifest.version, 2);
    const exported = archive.data.map(file => file.text).join('\n');
    assert(exported.includes(female[0].id), 'ZIP includes the filtered Patient row');
    assert(!patients.filter(patient => patient.gender !== 'female').some(patient => exported.includes(patient.id)), 'ZIP excludes rows removed by the authored Filter operation');
  });
});
