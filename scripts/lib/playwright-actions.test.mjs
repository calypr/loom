import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { performAction, prepareNativeAction } from './playwright-actions.mjs';
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
    await assert.rejects(prepareNativeAction(button('Duplicate'), 'duplicate', { timeout: 250 }), /expected one control, found 2/);
    await assert.rejects(prepareNativeAction(button('Disabled'), 'disabled', { timeout: 250 }), /Timeout/i);
    await assert.rejects(prepareNativeAction(button('Intercepted'), 'intercepted', { timeout: 250 }), /Timeout|intercepts pointer events/i);
    const readOnly = browser.page.getByRole('textbox', { name: 'Read only' });
    await assert.rejects(prepareNativeAction(readOnly, 'read only', { editable: true }), /not editable/);
    assert.equal(await browser.page.getByRole('textbox', { name: 'Read only' }).inputValue(), 'unchanged');

    await browser.page.setContent('<label>Transient<select aria-label="Transient" disabled><option value="ready">Ready</option></select></label>');
    const transient = browser.page.getByRole('combobox', { name: 'Transient' });
    await transient.evaluate(select => window.setTimeout(() => { select.disabled = false; }, 150));
    const startedAt = Date.now();
    await prepareNativeAction(transient, 'transiently disabled select', { timeout: 5000 });
    await transient.selectOption('ready', { timeout: 5000 });
    const transientElapsedMs = Date.now() - startedAt;
    assert(transientElapsedMs >= 100 && transientElapsedMs < 5000, `transient select readiness took ${transientElapsedMs}ms`);
    assert.equal(await transient.inputValue(), 'ready');

    await browser.page.setContent('<button onclick="document.querySelector(\'output\').textContent=\'Saved\'">Save</button><output>Waiting</output>');
    const save = browser.page.getByRole('button', { name: 'Save' });
    const elapsedMs = await performAction(tracker, 'save', save, target => target.click());
    await browser.page.getByText('Saved').waitFor({ state: 'visible' });
    assert(elapsedMs >= 0);
    assert.deepEqual(tracker.actions.map(action => action.label), ['save']);
    assert.equal(tracker.activeAction, undefined);
  } finally {
    await browser?.close();
    await rm(evidence, { recursive: true, force: true });
  }
});
