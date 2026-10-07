import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { pivotSourceSelectionReady } from '../../workflows/root-quantity-pivot-workflow.mjs';

const installDocument = ({ controls, checkboxes = [] }) => {
  const previous = globalThis.document;
  globalThis.document = {
    querySelector: selector => controls.get(selector) ?? null,
    querySelectorAll: selector => selector === 'input[type="checkbox"]' ? checkboxes : [],
  };
  return () => {
    if (previous === undefined) delete globalThis.document;
    else globalThis.document = previous;
  };
};

test('Pivot source postcondition accepts the exact reset group and generated category/value fields', () => {
  const groupSelector = 'select[aria-label="Add pivot group field"]';
  const categorySelector = 'select[aria-label="Pivot category field"]';
  const valueSelector = 'select[aria-label="Pivot values field"]';
  const groupChoice = 'source:choice-status';
  const categoryChoice = 'source:choice-code';
  const valueChoice = 'source:choice-value';
  const groupControl = { options: [{ value: '' }, { value: 'source:choice-other' }], value: '' };
  const categoryControl = {
    options: [{ value: 'pivot-input_code', textContent: 'Code' }],
    value: 'pivot-input_code',
    selectedOptions: [{ value: 'pivot-input_code', textContent: 'Code' }],
  };
  const valueControl = {
    options: [{ value: 'pivot-input_value', textContent: 'Quantity (decimal)' }],
    value: 'pivot-input_value',
    selectedOptions: [{ value: 'pivot-input_value', textContent: 'Quantity (decimal)' }],
  };
  const checkedGroup = { getAttribute: name => name === 'aria-label' ? 'Pivot group Status' : null, checked: true };
  const restore = installDocument({
    controls: new Map([[groupSelector, groupControl], [categorySelector, categoryControl], [valueSelector, valueControl]]),
    checkboxes: [checkedGroup],
  });
  try {
    assert.equal(pivotSourceSelectionReady({ selector: groupSelector, sourceValue: groupChoice, role: 'group', outputLabel: 'Status' }), true);
    assert.equal(pivotSourceSelectionReady({ selector: categorySelector, sourceValue: categoryChoice, role: 'category', outputLabel: 'Code' }), true);
    assert.equal(pivotSourceSelectionReady({ selector: valueSelector, sourceValue: valueChoice, role: 'value', outputLabel: 'Quantity', outputType: 'decimal' }), true);
  } finally {
    restore();
  }
});

test('Pivot source postcondition rejects a retained source choice, wrong group, and wrong remapped field', () => {
  const groupSelector = 'select[aria-label="Add pivot group field"]';
  const categorySelector = 'select[aria-label="Pivot category field"]';
  const sourceChoice = 'source:choice-status';
  const groupControl = { options: [{ value: sourceChoice }], value: '' };
  const categoryControl = {
    options: [{ value: 'pivot-input_other', textContent: 'Other' }],
    value: 'pivot-input_other',
    selectedOptions: [{ value: 'pivot-input_other', textContent: 'Other' }],
  };
  const uncheckedGroup = { getAttribute: name => name === 'aria-label' ? 'Pivot group Other' : null, checked: true };
  const restore = installDocument({ controls: new Map([[groupSelector, groupControl], [categorySelector, categoryControl]]), checkboxes: [uncheckedGroup] });
  try {
    assert.equal(pivotSourceSelectionReady({ selector: groupSelector, sourceValue: sourceChoice, role: 'group', outputLabel: 'Status' }), false);
    groupControl.options = [{ value: '' }];
    assert.equal(pivotSourceSelectionReady({ selector: groupSelector, sourceValue: sourceChoice, role: 'group', outputLabel: 'Status' }), false);
    assert.equal(pivotSourceSelectionReady({ selector: categorySelector, sourceValue: 'source:choice-code', role: 'category', outputLabel: 'Code' }), false);
    categoryControl.options = [{ value: 'pivot-input_code', textContent: 'Code' }];
    categoryControl.value = 'source:choice-code';
    categoryControl.selectedOptions = [{ value: 'source:choice-code', textContent: 'Code' }];
    assert.equal(pivotSourceSelectionReady({ selector: categorySelector, sourceValue: 'source:choice-code', role: 'category', outputLabel: 'Code' }), false);
  } finally {
    restore();
  }
});

test('workflow times the role-aware selection postcondition instead of requiring the ephemeral source token to persist', async () => {
  const workflow = await readFile(new URL('../../workflows/root-quantity-pivot-workflow.mjs', import.meta.url), 'utf8');
  assert.match(workflow, /after:\s*\(\)\s*=>\s*waitForObservable\(page,\s*pivotSourceSelectionReady/);
  assert.match(workflow, /const selectPivotSource = async \(label, path\) =>[\s\S]*?await action\(/);
  assert.doesNotMatch(workflow, /const selectPivotSource = async \(label, path\) =>[\s\S]*?await selectNative\(page, selector, matches\[0\]\.value\)/);
});
