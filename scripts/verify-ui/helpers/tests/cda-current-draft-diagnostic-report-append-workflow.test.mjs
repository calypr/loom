import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { runInNewContext } from 'node:vm';

const workflowPath = new URL('../../workflows/cda-current-draft-diagnostic-report-append-workflow.mjs', import.meta.url);

test('select option reader survives browser callback serialization without Node lexical scope', async () => {
  const workflowSource = await readFile(workflowPath, 'utf8');
  const declarationStart = workflowSource.indexOf('export const readDiagnosticReportAppendSelectOptions = ');
  const declarationEnd = workflowSource.indexOf('\nconst sortRows = ', declarationStart);
  assert(declarationStart >= 0 && declarationEnd > declarationStart,
    'workflow must define the serializable option reader');
  const callbackSource = workflowSource.slice(
    declarationStart + 'export const readDiagnosticReportAppendSelectOptions = '.length,
    declarationEnd,
  ).trim().replace(/;$/, '');
  const browserCallback = runInNewContext(`(${callbackSource})`);
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
