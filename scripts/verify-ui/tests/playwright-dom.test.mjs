import assert from 'node:assert/strict';
import test from 'node:test';
import { click, fill, inspectAction, isActionable } from '../playwright-dom.mjs';

const control = ({ count = 1, visible = true, enabled = true, editable = true, intercepted = false } = {}) => {
  const calls = [];
  return {
    calls,
    count: async () => count,
    isVisible: async () => visible,
    isEnabled: async () => enabled,
    isEditable: async () => editable,
    click: async options => {
      calls.push(['click', options]);
      if (options?.trial && intercepted) throw new Error('another element receives pointer events');
    },
    fill: async value => calls.push(['fill', value]),
    selectOption: async value => calls.push(['selectOption', value]),
    evaluate: async () => false,
    toString: () => 'mock Playwright locator',
  };
};

const pageWith = locator => ({ locator: () => locator });

test('ambiguous and disabled Playwright targets are rejected before user input', async () => {
  const ambiguous = control({ count: 2 });
  await assert.rejects(() => click(pageWith(ambiguous), 'button'), /expected one control, found 2/);
  assert.deepEqual(ambiguous.calls, []);

  const disabled = control({ enabled: false });
  await assert.rejects(() => click(pageWith(disabled), 'button'), /control is disabled/);
  assert.deepEqual(disabled.calls, []);
});

test('pointer interception is checked with Playwright trial click and never forced', async () => {
  const intercepted = control({ intercepted: true });
  await assert.rejects(() => click(pageWith(intercepted), 'button'), /receives pointer events/);
  assert.deepEqual(intercepted.calls, [['click', { trial: true }]]);
  const state = await inspectAction(pageWith(intercepted), 'button');
  assert.equal(isActionable(state), false);
  assert.equal(state.receivesEvents, false);
});

test('read-only controls cannot be filled through the Playwright wrapper', async () => {
  const readOnly = control({ editable: false });
  await assert.rejects(() => fill(pageWith(readOnly), 'input[readonly]', 'value'), /control is read-only/);
  assert.deepEqual(readOnly.calls, []);
});

test('select controls use Playwright selectOption', async () => {
  const select = control();
  select.evaluate = async () => true;
  await fill(pageWith(select), 'select[aria-label="Rows"]', 'EXCLUDE');
  assert.deepEqual(select.calls, [['selectOption', 'EXCLUDE']]);
});
