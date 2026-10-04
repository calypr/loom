import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { launchBrowser } from '../../lib/playwright-browser.mjs';

test('raw FHIR locator scopes a Patient ID summary among ten resource disclosures', async t => {
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
    const sections = Array.from({ length: 10 }, (_, index) => `
      <details data-testid="feature-catalog-raw-fields">
        <summary>Raw FHIR fields (${index === 0 ? 'Patient' : `resource ${index}`} advanced)</summary>
        <input type="checkbox" aria-label="Select ${index === 0 ? 'Patient.id' : `resource${index}.id`}">
      </details>`).join('');
    await browser.page.setContent(sections);
    const disclosures = browser.page.getByTestId('feature-catalog-raw-fields');
    assert.equal(await disclosures.count(), 10);
    const patientID = browser.page.getByRole('checkbox', { name: 'Select Patient.id', exact: true });
    const patientDisclosure = browser.page.locator(
      '[data-testid="feature-catalog-raw-fields"]:has(input[type="checkbox"][aria-label="Select Patient.id"])');
    assert.equal(await patientDisclosure.count(), 1);
    assert.equal(await patientDisclosure.locator('summary').count(), 1);
    await patientDisclosure.locator('summary').click();
    assert.equal(await patientDisclosure.evaluate(element => element.open), true);
    assert.equal(await patientID.count(), 1);
    assert.equal(await patientDisclosure.getByRole('checkbox', { name: 'Select Patient.id', exact: true }).count(), 1);
  } finally {
    await browser?.close();
    await rm(evidence, { recursive: true, force: true });
  }
});
