import test from 'node:test';
import assert from 'node:assert/strict';
import { captureNativeFailureEvidence } from '../native-failure-evidence.mjs';

function matchesElement(element, selector) {
  const role = element.getAttribute('role');
  if (selector === 'form') return element.tagName === 'FORM';
  if (selector === '[role="dialog"], dialog') return role === 'dialog' || element.tagName === 'DIALOG';
  if (selector.includes('[data-testid*="editor"]')) {
    return element.getAttribute('data-testid')?.includes('editor') || element.getAttribute('contenteditable') === 'true' ||
      role === 'textbox' || element.getAttribute('class')?.includes('monaco-editor');
  }
  return false;
}

function makeElement({ tagName = 'div', attributes = {}, text = '', hidden = false, style = {} } = {}) {
  const element = {
    tagName: tagName.toUpperCase(),
    attributes,
    innerText: text,
    textContent: text,
    hidden,
    style,
    labels: [],
    parentElement: null,
    children: [],
    get id() { return attributes.id ?? ''; },
    getAttribute(name) { return attributes[name] ?? null; },
    append(child) {
      child.parentElement = this;
      this.children.push(child);
      return child;
    },
    getClientRects() { return hidden || style.display === 'none' ? [] : [{}]; },
    querySelectorAll(selector) {
      const matches = [];
      const visit = parent => {
        for (const child of parent.children) {
          if (matchesElement(child, selector)) matches.push(child);
          visit(child);
        }
      };
      visit(this);
      return matches;
    },
    querySelector(selector) {
      return this.querySelectorAll(selector)[0] ?? null;
    },
    closest(selector) {
      for (let current = this; current; current = current.parentElement) {
        if (matchesElement(current, selector)) return current;
      }
      return null;
    },
  };
  return element;
}

function makeFixture() {
  const validation = 'Validation failed: choose one field to continue. Authorization: Bearer native-private-token csrf_token=native-csrf-secret';
  const hiddenEditor = makeElement({ attributes: { 'data-testid': 'hidden-editor' }, text: 'Hidden editor contents', hidden: true });
  const editor = makeElement({ attributes: { 'data-testid': 'expression-editor' }, text: 'Editor contents' });
  const button = makeElement({ tagName: 'button', attributes: { id: 'apply', 'aria-label': 'Apply' }, text: 'Apply' });
  const form = makeElement({ tagName: 'form', text: `Expression\n${validation}` });
  form.append(hiddenEditor);
  form.append(editor);
  form.append(button);
  const dialog = makeElement({ attributes: { role: 'dialog' }, text: form.innerText });
  dialog.append(form);

  const visibleAlert = makeElement({ attributes: { role: 'alert' }, text: validation });
  const hiddenAlert = makeElement({ attributes: { role: 'alert' }, text: 'Hidden private diagnostic', hidden: true });
  const alerts = [visibleAlert, hiddenAlert];
  const document = {
    defaultView: { getComputedStyle: element => element.style },
    querySelectorAll: selector => selector.includes('[role="alert"]') ? alerts : [],
  };
  for (const element of [hiddenEditor, editor, button, form, dialog, visibleAlert, hiddenAlert]) element.ownerDocument = document;

  const body = makeElement({ text: `${'Unrelated navigation and header text '.repeat(500)}\n${dialog.innerText}\n${validation}` });
  body.ownerDocument = document;
  return { body, button, validation };
}

test('native failure evidence retains bounded visible action context before generic body truncation', async () => {
  const { body, button, validation } = makeFixture();
  const readOrder = [];
  const actionLocator = {
    toString: () => 'getByRole("button", { name: "Apply" })',
    count: async () => 1,
    isVisible: async () => true,
    isEnabled: async () => true,
    isEditable: async () => false,
    evaluate: async callback => {
      readOrder.push('action');
      return callback(button);
    },
  };
  const page = {
    isClosed: () => false,
    url: () => 'http://127.0.0.1:30008/editor',
    locator: selector => ({ evaluate: async callback => {
      readOrder.push('body');
      return callback(body);
    } }),
  };

  const evidence = await captureNativeFailureEvidence({
    page,
    ownedOrigins: ['http://127.0.0.1:30008'],
    reason: 'Apply failed after validation',
    label: 'Apply expression',
    locator: actionLocator,
    elapsedMs: 37,
  });

  assert.equal(evidence.page.bodyText.length, 12_000);
  assert.ok(readOrder.indexOf('body') > readOrder.lastIndexOf('action'));
  assert.doesNotMatch(evidence.page.bodyText, /Validation failed/);
  assert.deepEqual(evidence.locator.context.containers.map(item => item.kind), ['dialog', 'form', 'editor']);
  assert.match(evidence.locator.context.containers.find(item => item.kind === 'form').text, /Validation failed: choose one field/);
  assert.ok(evidence.locator.context.alerts.some(text => text.includes('Validation failed: choose one field')));
  assert.ok(evidence.locator.context.alerts.every(text => !text.includes('Hidden private diagnostic')));

  const serialized = JSON.stringify(evidence);
  assert.doesNotMatch(serialized, /native-private-token|native-csrf-secret|Hidden private diagnostic/);
  assert.ok(evidence.locator.context.containers.every(item => item.text.length <= 1_600));
  assert.ok(evidence.locator.context.alerts.every(text => text.length <= 800));
  assert.ok(serialized.length < 25_000, `failure evidence exceeded its bound: ${serialized.length}`);
});
