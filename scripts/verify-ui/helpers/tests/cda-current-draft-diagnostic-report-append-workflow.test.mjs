import assert from 'node:assert/strict';
import test from 'node:test';
import { runInNewContext } from 'node:vm';
import { readDiagnosticReportAppendSelectOptions } from '../../workflows/cda-current-draft-diagnostic-report-append-workflow.mjs';

test('select option reader survives browser callback serialization without Node lexical scope', () => {
  const browserCallback = runInNewContext(`(${readDiagnosticReportAppendSelectOptions.toString()})`);
  const select = {
    options: [
      { value: 'COUNT_DISTINCT', textContent: '  Count\n distinct  ', disabled: false },
      { value: '', textContent: 'Choose an aggregate', disabled: true },
    ],
  };

  assert.deepEqual(JSON.parse(JSON.stringify(browserCallback(select))), [
    { value: 'COUNT_DISTINCT', text: 'Count distinct', disabled: false },
    { value: '', text: 'Choose an aggregate', disabled: true },
  ]);
});
