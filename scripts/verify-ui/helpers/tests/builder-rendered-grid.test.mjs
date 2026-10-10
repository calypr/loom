import assert from 'node:assert/strict';
import test from 'node:test';
import {
  builderRenderedGridWaitPredicate,
  findRenderedBuilderHeaderIndex,
  waitForBuilderRenderedGrid,
} from '../builder-rendered-grid.mjs';

const cell = (text, title) => ({
  innerText: text,
  getAttribute: name => name === 'title' ? null : null,
  querySelector: selector => selector === '[title]' && title !== undefined
    ? { getAttribute: name => name === 'title' ? title : null }
    : null,
});

const row = (cells, header = false) => ({
  querySelectorAll: selector => header && selector === '[role="columnheader"]'
    ? cells
    : !header && selector === '[role="cell"]' ? cells : [],
});

const gridDocument = rows => ({
  querySelector: selector => selector === '[data-testid="preview-table-scroll"] [role="table"]'
    ? { querySelectorAll: query => query === '[role="row"]' ? rows : [] }
    : null,
});

test('rendered-grid wait matches CSS-uppercased headers and exact nested raw cells after render settles', async () => {
  const priorDocument = globalThis.document;
  const contract = {
    tableSelector: '[data-testid="preview-table-scroll"] [role="table"]',
    expectedRows: [
      { 'Patient ID': { titleJsonArray: ['dev-patient-001', 'dev-patient-002'] }, 'Row count': '1' },
    ],
  };
  try {
    const renderedHeaders = ['PATIENT ID', 'ROW COUNT'];
    assert.equal(findRenderedBuilderHeaderIndex(renderedHeaders, 'Patient ID'), 0);
    assert.deepEqual(renderedHeaders, ['PATIENT ID', 'ROW COUNT'], 'semantic matching must preserve literal rendered header evidence');
    globalThis.document = gridDocument([
      row([cell('PATIENT ID'), cell('ROW COUNT')], true),
      row([cell('dev-patient-001; dev-patient-002', '["dev-patient-001"]')]),
    ]);
    assert.equal(builderRenderedGridWaitPredicate(contract), false,
      'the gate must stay closed when the rendered nested raw value is not the exact fixture array');

    globalThis.document = gridDocument([
      row([cell('PATIENT ID'), cell('ROW COUNT')], true),
      row([cell('dev-patient-001; dev-patient-002', '["dev-patient-001","dev-patient-002"]'), cell('1')]),
    ]);
    assert.equal(builderRenderedGridWaitPredicate(contract), true,
      'the gate must open only when the CSS-uppercased header points to the exact nested raw value and count');

    globalThis.document = gridDocument([
      row([cell('PATIENT ID'), cell('ROW COUNT')], true),
      row([cell('dev-patient-001; dev-patient-002', '["dev-patient-001","dev-patient-002"]')]),
    ]);
    assert.doesNotThrow(() => assert.equal(builderRenderedGridWaitPredicate(contract), false),
      'a transient row with fewer cells than headers must stay pending rather than throw');

    globalThis.document = gridDocument([
      row([cell('PATIENT ID'), cell('ROW COUNT')], true),
      row([cell('dev-patient-001; dev-patient-002', '["dev-patient-001","dev-patient-002"]'), cell('1')]),
    ]);
    let waitCall;
    const page = { waitForFunction: async (predicate, argument, options) => {
      waitCall = { predicate, argument, options };
      return predicate(argument);
    } };
    assert.equal(await waitForBuilderRenderedGrid(page, contract), true);
    assert.equal(waitCall.predicate, builderRenderedGridWaitPredicate);
    assert.deepEqual(waitCall.argument, contract);
    assert.deepEqual(waitCall.options, { timeout: 5000 });
  } finally {
    if (priorDocument === undefined) delete globalThis.document;
    else globalThis.document = priorDocument;
  }
});
