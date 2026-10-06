import assert from 'node:assert/strict';

export async function verifyBrowserDisclosure({ page }) {
  const report = { status: 'running', checks: [], actions: [] };
  await page.setContent('<!doctype html><details id="advanced" open><summary>Advanced</summary><fieldset><input aria-label="Column name" value="Original"></fieldset></details>');
  const input = page.locator('input[aria-label="Column name"]');
  const summary = page.getByText('Advanced', { exact: true });

  await summary.click();
  assert.equal(await input.count(), 1, 'The collapsed control target must be unique');
  assert.equal(await input.isVisible(), false, 'A control inside collapsed details must be hidden');
  await assert.rejects(input.fill('Ignored', { timeout: 1000 }), /not visible|Timeout/i,
    'Playwright must refuse to type into the collapsed control');
  report.checks.push('collapsed control hidden', 'hidden fill rejected');

  await summary.click();
  await input.waitFor({ state: 'visible', timeout: 1000 });
  await input.fill('Renamed');
  assert.equal(await input.inputValue(), 'Renamed', 'Native typing must update the open control');
  report.status = 'passed';
  report.checks.push('summary actionable', 'opened control native typing');
  report.actions = ['collapse Advanced disclosure', 'open Advanced disclosure', 'rename column'];
  return report;
}
