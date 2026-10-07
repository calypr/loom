import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { scenarioCaseFor } from '../../registry.mjs';

const scenarioID = 'cda-published-upstream-append';
const caseName = 'published-append';

test('published CDA APPEND spec binds its report to all 34 registered lifecycle checks', async () => {
  const spec = await readFile(new URL('../../specs/cda-published-upstream-append.spec.mjs', import.meta.url), 'utf8');
  assert.match(spec, /cdaScenarioID:\s*'cda-published-upstream-append'/);
  assert.match(spec, /cdaCaseName:\s*'published-append'/);

  const registered = scenarioCaseFor(scenarioID, caseName).requiredChecks;
  assert.equal(registered.length, 34);
  assert.equal(new Set(registered).size, registered.length);
  for (const required of [
    'native Builder publication succeeds for the three selected raw CDA source tables',
    'APPEND proposal pins the three exact source revisions and explicit Patient status null padding',
    'Canceling the initial published APPEND proposal preserves the exact rooted empty target',
    'APPEND Apply reload reaches exact null-padded CDA rows within five seconds',
    'saved APPEND label edit retains stable step and explicit Patient null padding',
    'applying APPEND removal restores the exact pre-combine rooted empty target',
    'published raw CDA source tables and exact Explorer scope remain unchanged through APPEND lifecycle',
  ]) assert(registered.includes(required), `registry is missing ${required}`);
});
