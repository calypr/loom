import { expect, test } from '@playwright/test';
import { assertPreviewPatientIds, assertPreviewPatientWindow, assertRestoredBuilder } from '../lib/playwright-authoring.mjs';

test('authoring oracle rejects a wrong visible row and a missing persisted field', async ({ page }) => {
  await page.setContent(`
    <div data-testid="preview-table-scroll"><div role="table">
      <div role="row"><div role="columnheader">Patient ID</div></div>
      <div role="row"><div role="cell">dev-patient-001</div></div>
      <div role="row"><div role="cell">wrong-patient</div></div>
    </div></div>
    <select aria-label="Explorer"><option value="saved-explorer" selected>Saved Explorer</option></select>
    <button aria-label="Select Patient ID">Patient ID</button>
  `);
  await expect(assertPreviewPatientIds(page, ['dev-patient-001', 'dev-patient-002']))
    .rejects.toThrow(/exact independent fixture Patient IDs/);
  await expect(assertRestoredBuilder(page, { explorer: 'saved-explorer', title: 'Saved Explorer' }))
    .rejects.toThrow(/must survive reload/);

  const ids = Array.from({ length: 25 }, (_, index) => `patient-${index}`);
  const rawValues = new Map(ids.map(id => [id, ['same', 'same']]));
  await page.setContent(`<div data-testid="preview-table-scroll"><div role="table" aria-rowcount="26">
    <div role="row"><div role="columnheader">ID</div><div role="columnheader">Identifier Value</div></div>
    ${ids.map((id, index) => `<div role="row"><span>${index + 1}</span><div role="cell">${id}</div><div role="cell">same</div></div>`).join('')}
  </div></div>`);
  await expect(assertPreviewPatientWindow(page, ids, rawValues))
    .rejects.toThrow(/Identifier ALL value differs from the independent CDA source/);
});
