import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { registry, scenarioCaseFor } from '../../registry.mjs';

test('CASE-060 binds the exact CDA target and one existing native EXISTS test', async () => {
  const scenario = registry.find(entry => entry.id === 'cda-contributor-exists');
  assert(scenario, 'dedicated CDA contributor EXISTS scenario must be registered');
  const contract = scenarioCaseFor(scenario, 'contributor-exists');
  const spec = await readFile(new URL('../../specs/standalone-cda-fields.spec.mjs', import.meta.url), 'utf8');

  assert.deepEqual(contract.expectedIdentity, {
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
  }, 'the native bracket must verify runtime identity against the owned CDA project and generation');
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-fields.spec.mjs');
  assert.equal(contract.playwrightGrep, 'CDA contributor exists CDA contributor exists$',
    'the registered native selection must resolve to the exact contributor EXISTS test title');
  assert.match(spec, /test\('CDA contributor exists'/,
    'the registered grep must match the existing native test');
  assert.equal(contract.requiredChecks.length, 16,
    'target binding must retain the existing complete EXISTS lifecycle contract');
});
