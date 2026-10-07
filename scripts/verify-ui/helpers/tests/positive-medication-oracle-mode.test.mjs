import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { medicationOracleModeForCase } from '../../workflows/verify-cda-zero-column-related-medication.mjs';
import { registry } from '../../registry.mjs';

test('fixture oracle bound is separate from the existing owned-CDA 1,000-root bound', () => {
  assert.deepEqual(medicationOracleModeForCase('medication-preserve-parent'), {
    caseName: 'medication-preserve-parent', expectedScannedRoots: 1000, positiveFixture: false,
  });
  assert.deepEqual(medicationOracleModeForCase('medication-positive-fixture'), {
    caseName: 'medication-positive-fixture', expectedScannedRoots: 2, positiveFixture: true,
  });
  assert.throws(() => medicationOracleModeForCase('unknown-case'), /unsupported related Medication case/);
});

test('positive fixture lifecycle is a separate registered native case from the real-CDA negative case', () => {
  const scenario = registry.find(({ id }) => id === 'cda-five-hop-related-expansion');
  const cdaCase = scenario.cases['medication-preserve-parent'];
  const fixtureCase = scenario.cases['medication-positive-fixture'];
  assert.equal(cdaCase.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-other.spec.mjs');
  assert.equal(cdaCase.requiredChecks.length, 11);
  assert.equal(cdaCase.requiredChecks.at(-1), 'CDA watched source and API build stayed unchanged');
  assert.equal(fixtureCase.playwrightTest, 'scripts/verify-ui/specs/related-medication-positive-fixture.spec.mjs');
  assert.equal(fixtureCase.requiredChecks.length, 14);
  assert.deepEqual(fixtureCase.requiredChecks.slice(0, 10), cdaCase.requiredChecks.slice(0, 10));
  assert.deepEqual(fixtureCase.requiredChecks.slice(10, 12), [
    'Canceling the EXCLUDE edit preserves the saved PRESERVE_PARENT route, policy, exact Medication rows, and draft CAS before reopening and applying the edit',
    'Canceling removal preserves the saved EXCLUDE route, policy, exact Medication rows, and draft CAS before reopening and applying removal',
  ]);
  assert.deepEqual(fixtureCase.requiredChecks.slice(12), [
    'watched source stayed unchanged during browser run',
    'API build identity stayed unchanged during browser run',
  ]);
  assert.equal(scenario.requiredTransitions[0],
    'medication-preserve-parent scans the first 1,000 sorted project/generation Specimen roots; medication-positive-fixture scans exactly its two fixture roots before independently traversing the same five-hop Medication route');
  assert.match(scenario.requiredTransitions[4], /positive fixture also cancels the EXCLUDE edit and removal/);
  const positiveCoverage = scenario.coverage.find(({ feature }) => feature.startsWith('positive match values'));
  assert.equal(positiveCoverage.status, 'untested');
  assert.match(positiveCoverage.reason, /lifecycle map has one cancel phase mapped to the original PRESERVE_PARENT proposal/);

  const spec = readFileSync(new URL('../../specs/related-medication-positive-fixture.spec.mjs', import.meta.url), 'utf8');
  assert.match(spec, /from '\.\.\/helpers\/fixtures\.mjs'/);
  assert.match(spec, /fixtureDir: 'testdata\/cda-zero-column-related-medication-positive'/);
  assert.match(spec, /fixtureGeneration: 'cda-fhir-v1'/);
  assert.match(spec, /caseName: 'medication-positive-fixture'/);
  assert.match(spec, /async \(\{ page, workflow \}, testInfo\)/);
  assert.match(spec, /workflow,\s*testInfo,/);
  assert.match(spec, /zeroColumnRelatedMedicationWorkflow\(\{ page, cda \}\)/);

  const lifecycle = readFileSync(new URL('../../workflows/verify-cda-zero-column-related-medication.mjs', import.meta.url), 'utf8');
  assert.match(lifecycle, /oracleMode\.positiveFixture \? 14 : 11/);
  assert.match(lifecycle, /assertCanceledProposalRestoredRows\(\s*report\.rawOracle\.expectedVisibleMedicationValues, preserveParentSavedBuilder\)/);
  assert.match(lifecycle, /assertCanceledProposalRestoredRows\(excludedExpectedValues, excludeSavedBuilder\)/);
  assert.match(lifecycle, /await wait\(\(\[expectedOutput, expectedVersion, expectedDigest, expectedCount\]\)/);
  assert.match(lifecycle, /preview\.dataset\.currentDraftDigest === expectedDigest/);
  assert.match(lifecycle, /canceledEditStep\.operation\?\.relatedExpand\?\.emptyPolicy, 'PRESERVE_PARENT'/);
  assert.match(lifecycle, /canceledRemovalStep\.operation\?\.relatedExpand\?\.emptyPolicy, 'EXCLUDE'/);
  assert.match(lifecycle, /'PRESERVE_PARENT', 'Reopened editor must hydrate the saved policy after canceling EXCLUDE'/);
  assert.match(lifecycle, /assert\.deepEqual\(builder\.workspace, preserveParentSavedBuilder\.workspace/);
  assert.match(lifecycle, /assert\.deepEqual\(builder\.workspace, excludeSavedBuilder\.workspace/);
  assert.match(lifecycle, /assert\.equal\(builder\.draftVersion, preserveParentSavedBuilder\.draftVersion\)/);
  assert.match(lifecycle, /assert\.equal\(builder\.draftDigest, excludeSavedBuilder\.draftDigest\)/);
  assert.match(lifecycle, /measured\('Cancel EXCLUDE edit to exact saved PRESERVE_PARENT rows', cancelEditStart\)/);
  assert.match(lifecycle, /measured\('Cancel removal to exact saved EXCLUDE Medication rows', cancelRemovalStart\)/);
  assert.match(lifecycle, /check\(10, 'persistence'/);
  assert.match(lifecycle, /check\(11, 'persistence'/);
  const editCancel = lifecycle.indexOf('const cancelEditStart = Date.now()');
  const editRendered = lifecycle.indexOf("measured('Cancel EXCLUDE edit to exact saved PRESERVE_PARENT rows', cancelEditStart)", editCancel);
  const editBuilderRead = lifecycle.indexOf('builder = await api(`${authoringPath}/builder`)', editRendered);
  const editCancelCheck = lifecycle.indexOf('check(10,', editCancel);
  const reopenedEdit = lifecycle.indexOf('const reopenEditStart = Date.now()', editCancelCheck);
  const excludeApply = lifecycle.indexOf('const excludeApplyFromIndex =', reopenedEdit);
  assert(editCancel >= 0 && editCancel < editRendered && editRendered < editBuilderRead && editBuilderRead < editCancelCheck &&
    editCancelCheck < reopenedEdit && reopenedEdit < excludeApply,
    'Positive EXCLUDE edit must be canceled and checked before reopening and applying it');
  const removalCancel = lifecycle.indexOf('const cancelRemovalStart = Date.now()');
  const removalRendered = lifecycle.indexOf("measured('Cancel removal to exact saved EXCLUDE Medication rows', cancelRemovalStart)", removalCancel);
  const removalBuilderRead = lifecycle.indexOf('builder = await api(`${authoringPath}/builder`)', removalRendered);
  const removalCancelCheck = lifecycle.indexOf('check(11,', removalCancel);
  const reopenedRemoval = lifecycle.indexOf('const reopenRemovalStart = Date.now()', removalCancelCheck);
  const removalApply = lifecycle.indexOf('const removeApplyStart =', reopenedRemoval);
  assert(removalCancel >= 0 && removalCancel < removalRendered && removalRendered < removalBuilderRead &&
    removalBuilderRead < removalCancelCheck && removalCancelCheck < reopenedRemoval && reopenedRemoval < removalApply,
    'Positive removal must be canceled and checked before reopening and applying it');
  const standardFixtureRunner = readFileSync(new URL('../fixtures.mjs', import.meta.url), 'utf8');
  assert.match(standardFixtureRunner, /recordCheck\(report, 'correctness', 'watched source stayed unchanged during browser run'/);
  assert.match(standardFixtureRunner, /sourceAtStart\.fingerprint\.sha256 === sourceAtEnd\.fingerprint\.sha256/);
  assert.match(standardFixtureRunner, /recordCheck\(report, 'correctness', 'API build identity stayed unchanged during browser run'/);
  assert.match(standardFixtureRunner, /apiAtStart\.identity === apiAtEnd\.identity/);
});
