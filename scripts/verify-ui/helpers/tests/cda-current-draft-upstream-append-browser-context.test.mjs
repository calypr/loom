import assert from 'node:assert/strict';
import { runInNewContext } from 'node:vm';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { readSelectOptionsInPage } from '../../workflows/cda-current-draft-upstream-append-workflow.mjs';

test('serialized select-option reader returns raw DOM text without Node normalize closure', async () => {
  const workflow = await readFile(new URL('../../workflows/cda-current-draft-upstream-append-workflow.mjs', import.meta.url), 'utf8');
  assert.match(workflow, /page\.locator\(selector\)\.evaluate\(readSelectOptionsInPage\)/);
  assert.doesNotMatch(workflow, /normalize\(option\.textContent\)/);
  const browserReader = runInNewContext('(' + readSelectOptionsInPage.toString() + ')');
  const options = browserReader({ options: [
    { value: 'table-1', textContent: '  Patient   FHIR ID  ', disabled: false },
    { value: '', textContent: null, disabled: true },
  ] });
  assert.deepEqual(JSON.parse(JSON.stringify(options)), [
    { value: 'table-1', text: '  Patient   FHIR ID  ', disabled: false },
    { value: '', text: null, disabled: true },
  ]);
});
