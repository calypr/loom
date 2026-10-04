import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { launchBrowser } from '../../lib/playwright-browser.mjs';
import { assertPreviewPatientIds, assertPreviewPatientWindow, assertRestoredBuilder } from '../playwright-authoring.mjs';

test('authoring oracle rejects a wrong visible row and a missing persisted field', async t => {
  const evidence = await mkdtemp(join(tmpdir(), 'loom-playwright-authoring-test-'));
  let browser;
  try {
    try {
      browser = await launchBrowser({ evidence, appOrigins: ['http://127.0.0.1'], noAuth: true });
    } catch (error) {
      if (/Executable doesn't exist|browserType\.launch:.*(?:not found|failed to launch)/i.test(String(error))) {
        t.skip(`Chromium is unavailable: ${error.message}`);
        return;
      }
      throw error;
    }
    const page = browser.page;
    await page.setContent(`
      <div data-testid="preview-table-scroll"><div role="table">
        <div role="row"><div role="columnheader">Patient ID</div></div>
        <div role="row"><div role="cell">dev-patient-001</div></div>
        <div role="row"><div role="cell">wrong-patient</div></div>
      </div></div>
      <select aria-label="Explorer"><option value="saved-explorer" selected>Saved Explorer</option></select>
      <button aria-label="Select Patient ID">Patient ID</button>
    `);
    await assert.rejects(
      assertPreviewPatientIds(page, ['dev-patient-001', 'dev-patient-002']),
      /exact independent fixture Patient IDs/,
    );
    await assert.rejects(
      assertRestoredBuilder(page, { explorer: 'saved-explorer', title: 'Saved Explorer' }),
      /must survive reload/,
    );
    const ids = Array.from({ length: 25 }, (_, index) => `patient-${index}`);
    const rawValues = new Map(ids.map(id => [id, ['same', 'same']]));
    await page.setContent(`<div data-testid="preview-table-scroll"><div role="table" aria-rowcount="26">
      <div role="row"><div role="columnheader">ID</div><div role="columnheader">Identifier Value</div></div>
      ${ids.map((id, index) => `<div role="row"><span>${index + 1}</span><div role="cell">${id}</div><div role="cell">same</div></div>`).join('')}
    </div></div>`);
    await assert.rejects(
      assertPreviewPatientWindow(page, ids, rawValues),
      /Identifier ALL value differs from the independent CDA source/,
    );
  } finally {
    await browser?.close();
    await rm(evidence, { recursive: true, force: true });
  }
});
