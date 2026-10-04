import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { launchBrowser } from '../../lib/playwright-browser.mjs';

test('raw FHIR locator selects the direct summary among nested resource summaries', async t => {
  const evidence = await mkdtemp(join(tmpdir(), 'loom-playwright-raw-fields-test-'));
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
    const nested = Array.from({ length: 9 }, (_, index) => `<details><summary>Resource ${index}</summary></details>`).join('');
    await browser.page.setContent(`<details data-testid="feature-catalog-raw-fields">
      <summary>Raw FHIR fields (advanced)</summary>${nested}
      <input type="checkbox" aria-label="Select Patient.id">
    </details>`);
    const disclosures = browser.page.getByTestId('feature-catalog-raw-fields');
    assert.equal(await disclosures.count(), 1);
    assert.equal(await disclosures.locator('summary').count(), 10);
    const patientID = browser.page.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
    const patientDisclosure = browser.page.locator(
      '[data-testid="feature-catalog-raw-fields"]:has(input[type="checkbox"][aria-label="Select Patient.id"])');
    assert.equal(await patientDisclosure.count(), 1);
    assert.equal(await patientDisclosure.locator(':scope > summary').count(), 1);
    await patientDisclosure.locator(':scope > summary').click();
    assert.equal(await patientDisclosure.evaluate(element => element.open), true);
    assert.equal(await patientID.count(), 1);
    assert.equal(await patientDisclosure.getByRole('checkbox', { name: 'Select Patient.id', exact: true }).count(), 1);
  } finally {
    await browser?.close();
    await rm(evidence, { recursive: true, force: true });
  }
});
