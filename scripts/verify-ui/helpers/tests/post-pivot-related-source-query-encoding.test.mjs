import assert from 'node:assert/strict';
import { runInNewContext } from 'node:vm';
import test from 'node:test';
import { buildRawQueryExecuteString } from '../../workflows/verify-cda-related-source-after-pivot-browser.mjs';

test('raw Arango execute-string round-trips scoped query and bind variables', () => {
  const query = 'FOR o IN Observation FILTER o.project == @project AND o.dataset_generation == @generation AND o.payload.resourceType == "Observation" LIMIT 1 RETURN o.id';
  const bindVars = { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' };
  const rows = ['bounded-witness-id'];
  const calls = [];
  let printed;
  const code = buildRawQueryExecuteString(query, bindVars);

  assert.doesNotMatch(code, /@/);
  runInNewContext(code, {
    db: {
      _query(actualQuery, actualBindVars) {
        calls.push({ query: actualQuery, bindVars: actualBindVars });
        return { toArray: () => rows };
      },
    },
    print(value) { printed = value; },
  });

  assert.equal(calls.length, 1);
  assert.deepEqual(JSON.parse(JSON.stringify(calls[0])), { query, bindVars });
  assert.deepEqual(JSON.parse(printed), rows);
});
