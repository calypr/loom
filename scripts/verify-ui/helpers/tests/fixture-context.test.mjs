import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { nativeEnvironmentTargetProblems } from '../../../run-builder-journey.mjs';
import { readBasicTemporalFilterOracle } from '../basic-temporal-filter-oracle.mjs';
import { bindFixtureOracleToQaIsolation } from '../fixture-context.mjs';

const repositoryRoot = resolve(fileURLToPath(new URL('../../../../', import.meta.url)));

test('isolated DATE oracle is written under QA isolation with its real project and generation binding', () => {
  const retained = JSON.parse(readFileSync(new URL(
    './fixtures/temporal-filter-native-report-run5-target.json', import.meta.url,
  ), 'utf8'));
  const report = { target: structuredClone(retained.target) };
  const actualOracle = readBasicTemporalFilterOracle({
    fixtureDir: resolve(repositoryRoot, report.target.qaIsolation.fixtureDir.replace(/^\$CHECKOUT\//u, '')),
    caseName: 'date',
  });
  assert.equal(actualOracle.sha256, report.target.fixtureRawOracle.sha256,
    'The literal DATE oracle must match the retained native report file hash.');
  assert.deepEqual(actualOracle.rows, report.target.fixtureRawOracle.rows,
    'The literal DATE rows must match the retained native report.');
  assert.deepEqual(actualOracle.rows, report.target.qaIsolation.fixtureRows,
    'The literal DATE rows must match the retained QA isolation rows.');

  const environmentSummary = {
    targetIdentity: null,
    targetValidation: {
      scope: 'environment-only',
      registryBinding: 'unbound',
      runtimeDatasetIdentity: 'not-checked',
      configPath: null,
      project: null,
      generation: null,
    },
  };
  assert(nativeEnvironmentTargetProblems(environmentSummary, report).some(problem =>
    problem === 'domain report fixture oracle must bind the actual isolated project/generation'),
  'The retained run5 shape must fail while the oracle exists only in the legacy top-level alias.');
  assert.equal(report.target.qaIsolation.fixtureOracle, undefined);

  const fixtureOracle = {
    path: actualOracle.path,
    sha256: actualOracle.sha256,
    project: report.target.project,
    generation: report.target.generation,
    resourceType: actualOracle.resourceType,
    fieldPath: actualOracle.fieldPath,
    rows: actualOracle.rows,
  };
  const bound = bindFixtureOracleToQaIsolation(report, fixtureOracle);
  assert.deepEqual(bound, fixtureOracle, 'The canonical writer must preserve the complete raw oracle record.');
  assert.deepEqual(nativeEnvironmentTargetProblems(environmentSummary, report), [],
    'The canonical producer shape must satisfy the native environment target gate.');

  for (const altered of [
    { ...fixtureOracle, project: `${fixtureOracle.project}-foreign` },
    { ...fixtureOracle, generation: `${fixtureOracle.generation}-foreign` },
  ]) {
    const before = report.target.qaIsolation.fixtureOracle;
    assert.throws(() => bindFixtureOracleToQaIsolation(report, altered), /Fixture oracle (project|generation)/u);
    assert.strictEqual(report.target.qaIsolation.fixtureOracle, before,
      'A project or generation mismatch must fail before replacing the bound oracle.');
  }
});
