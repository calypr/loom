import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { launchBrowser } from '../../lib/playwright-browser.mjs';
import { assertPreviewPatientIds, assertRestoredBuilder } from '../playwright-authoring.mjs';

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
  } finally {
    await browser?.close();
    await rm(evidence, { recursive: true, force: true });
  }
});
