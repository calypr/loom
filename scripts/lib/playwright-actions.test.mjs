import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { performAction } from './playwright-actions.mjs';
import { launchBrowser } from './playwright-browser.mjs';

test('action wrapper rejects ambiguous, disabled, intercepted, and read-only controls', async t => {
  const evidence = await mkdtemp(join(tmpdir(), 'loom-playwright-actions-'));
  let browser;
  try {
    try {
      browser = await launchBrowser({ evidence });
    } catch (error) {
      if (/Executable doesn't exist|browserType\.launch:.*(?:not found|failed to launch)/i.test(String(error))) {
        t.skip(`Chromium is unavailable in this checkout: ${error.message}`);
        return;
      }
      throw error;
    }
    await browser.page.setContent(`
      <button>Duplicate</button><button>Duplicate</button>
      <button disabled>Disabled</button>
      <div style="position:relative"><button>Intercepted</button><span style="position:absolute;inset:0;z-index:2"></span></div>
      <input aria-label="Read only" readonly value="unchanged">
      <button onclick="document.querySelector('output').textContent='Saved'">Save</button>
      <output>Waiting</output>`);
    const tracker = {};
    const button = name => browser.page.getByRole('button', { name });
    await assert.rejects(performAction(tracker, 'duplicate', button('Duplicate'), target => target.click(), { timeout: 250 }), /strict mode violation|expected one control, found 2/);
    assert.equal(tracker.activeAction.label, 'duplicate');
    await assert.rejects(performAction(tracker, 'disabled', button('Disabled'), target => target.click({ timeout: 250 }), { timeout: 250 }), /Timeout/i);
    await assert.rejects(performAction(tracker, 'intercepted', button('Intercepted'), target => target.click({ timeout: 250 }), { timeout: 250 }), /Timeout|intercepts pointer events/i);
    await assert.rejects(performAction(tracker, 'read only', browser.page.getByRole('textbox', { name: 'Read only' }), target => target.fill('changed'), { editable: true }), /not editable/);
    assert.equal(await browser.page.getByRole('textbox', { name: 'Read only' }).inputValue(), 'unchanged');
    const elapsedMs = await performAction(tracker, 'save', button('Save'), target => target.click());
    await browser.page.getByText('Saved').waitFor({ state: 'visible' });
    assert(elapsedMs >= 0);
    assert.deepEqual(tracker.actions.map(action => action.label), ['save']);
    assert.equal(tracker.activeAction, undefined);
  } finally {
    await browser?.close();
    await rm(evidence, { recursive: true, force: true });
  }
});
