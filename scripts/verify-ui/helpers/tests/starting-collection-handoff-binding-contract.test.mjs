import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { hasLifecycleContract, registry, scenarioCaseFor } from '../../registry.mjs';

test('starting collection handoff binds the exact owned CDA native case', async () => {
  const scenario = registry.find(entry => entry.id === 'cda-starting-collection-handoff');
  assert(scenario, 'starting collection handoff scenario must be registered');
  const contract = scenarioCaseFor(scenario, 'initial-selection-handoff-route-preview-apply-reload');
  const spec = await readFile(new URL('../../specs/standalone-cda-other.spec.mjs', import.meta.url), 'utf8');

  assert.deepEqual(contract.expectedIdentity, {
    project: 'loom_dev_cda_fhir',
    generation: 'cda-fhir-v1',
  }, 'future brackets must validate against the owned CDA target identity');
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-other.spec.mjs');
  assert.equal(contract.playwrightGrep,
    'CDA starting collection handoff hand off a starting collection, attach its route, preview, and reload$',
    'the registered native selection must identify exactly one existing test title');
  assert.equal(contract.requiredChecks.length, 9,
    'target binding must retain the complete historical lifecycle assertion set');
  assert.deepEqual(contract.focusedChecks.map(check => check.id), [
    'starting-collection-terminal-request-completion',
    'starting-collection-handoff-target-binding',
  ]);

  const coverage = scenario.coverage.find(entry => entry.feature ===
    'initial standalone selection handoff through native route apply, exact preview, and reload');
  assert(coverage, 'the historical handoff coverage row must remain registered');
  assert.equal(coverage.acceptance.kind, 'lifecycle');
  assert.equal(coverage.acceptance.case, 'initial-selection-handoff-route-preview-apply-reload');
  assert.deepEqual(coverage.acceptance.checks, { choice: 2, apply: 3, savedRows: 4, reload: 5 });
  assert.deepEqual(Object.keys(coverage.acceptance.notApplicable).sort(), ['cancel', 'edit', 'proposal', 'restoration']);
  assert(hasLifecycleContract(coverage, scenario, registry),
    'the case mapping must satisfy the registered lifecycle acceptance contract');

  const describeStart = spec.indexOf("test.describe('CDA starting collection handoff'");
  const describeEnd = spec.indexOf("test.describe('CDA zero-column five-hop RelatedExpand'", describeStart);
  assert(describeStart >= 0 && describeEnd > describeStart, 'the registered native describe block must exist');
  const describe = spec.slice(describeStart, describeEnd);
  assert.match(describe, /cdaScenarioID:\s*'cda-starting-collection-handoff'/);
  assert.match(describe, /cdaCaseName:\s*'initial-selection-handoff-route-preview-apply-reload'/);
  assert.match(describe, /test\('hand off a starting collection, attach its route, preview, and reload'/,
    'the registered grep must resolve to the existing native lifecycle test');

  assert.match(scenario.coverage[0].reason,
    /Target validation was environment-only, registry-unbound, and runtime dataset identity not checked; this run does not establish a registry-bound target identity\./,
    'binding the current contract must not relabel the retained historical run as target-bound');
});
