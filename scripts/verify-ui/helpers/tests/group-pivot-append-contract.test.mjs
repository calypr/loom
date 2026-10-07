import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { lifecycleAcceptanceDrift, registry, scenarioCaseFor } from '../../registry.mjs';

const workflow = readFileSync(new URL('../../workflows/builder-combine-draft.mjs', import.meta.url), 'utf8');
const spec = readFileSync(new URL('../../specs/draft-combine.spec.mjs', import.meta.url), 'utf8');
const scenario = registry.find((entry) => entry.id === 'builder-combine-draft');
const contract = scenarioCaseFor('builder-combine-draft', 'group-pivot-append');
const coverage = scenario.coverage.find((entry) => entry.feature === 'APPEND over unpublished Observation GROUP and DiagnosticReport GROUP→PIVOT siblings');

test('Group→Pivot APPEND binds exact restoration checks and compares complete source documents', () => {
  const checks = contract.requiredChecks;
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/draft-combine.spec.mjs');
  assert.match(spec, /caseName:\s*'group-pivot-append'/);
  assert.match(spec, /await groupPivotAppendWorkflow\(/);

  assert.ok(checks.includes('removing Combine and reloading restores its rooted empty target'));
  assert.equal(checks.includes('removing Combine and reloading restores its rooted empty output'), false);
  assert.equal(coverage.status, 'untested');
  assert.deepEqual(lifecycleAcceptanceDrift(registry).filter((message) => message.includes('APPEND over unpublished Observation GROUP and DiagnosticReport GROUP→PIVOT siblings')), []);
  assert.equal(coverage.acceptance.kind, 'lifecycle');
  assert.equal(coverage.acceptance.case, 'group-pivot-append');
  assert.deepEqual(coverage.acceptance.checks, { choice: 9, proposal: 11, cancel: 14, apply: 16, savedRows: 17, reload: 17, edit: 25, restoration: 29 });
  assert.equal(checks[coverage.acceptance.checks.choice], 'Group→Pivot APPEND native choice uses exact current-draft inputs and four output mappings');
  assert.equal(checks[coverage.acceptance.checks.proposal], 'Group→Pivot APPEND preview equals the exact independent union with source-specific null padding');
  assert.equal(checks[coverage.acceptance.checks.cancel], 'Cancel preserves both mixed source shapes, the full workspace, and draft CAS after reload');
  assert.equal(checks[coverage.acceptance.checks.apply], 'Apply Group→Pivot APPEND advances the draft CAS and binds the exact unpublished sources with stable output fields');
  assert.equal(checks[coverage.acceptance.checks.savedRows], 'Group→Pivot APPEND rows survive reload exactly');
  assert.equal(checks[coverage.acceptance.checks.reload], 'Group→Pivot APPEND rows survive reload exactly');
  assert.equal(checks[coverage.acceptance.checks.edit], 'Group→Pivot APPEND edit advances the draft CAS and preserves its step, output columnIDs, and current-draft inputs');
  assert.equal(checks[coverage.acceptance.checks.restoration], 'removing Group→Pivot APPEND advances the draft CAS, restores the rooted empty target, and preserves both source constructions');
  assert.ok(checks.includes('Group→Pivot APPEND native choice uses exact current-draft inputs and four output mappings'));
  assert.match(workflow, /check\(report, 'correctness', 'Group→Pivot APPEND native choice uses exact current-draft inputs and four output mappings'/);
  assert.match(workflow, /controlsVisibleAndEnabled/);
  assert.match(workflow, /appendChoiceSelected = controls\.appendChoice\.ariaPressed === 'true'/);
  assert.match(workflow, /ok: controlsVisibleAndEnabled && appendChoiceSelected && exactChoiceControls/);
  assert.match(workflow, /controlsVisibleAndEnabled, appendChoiceSelected/);
  assert.match(workflow, /exactChoiceControls/);
  assert.match(workflow, /expectedOutputs/);
  assert.match(workflow, /const APPEND_EMPTY_MAPPING = Object\.freeze\(\{ kind: 'append-empty-for-this-table' \}\)/);
  assert.ok(workflow.includes('[sources[0].appendCountLabel, APPEND_EMPTY_MAPPING]'));
  assert.ok(workflow.includes('[APPEND_EMPTY_MAPPING, finalLabel]'));
  assert.ok(workflow.includes('[APPEND_EMPTY_MAPPING, preliminaryLabel]'));
  assert.match(workflow, /await chooseOption\(page, selector, 'Empty for this table'\)/);
  assert.match(workflow, /expectedMapping === APPEND_EMPTY_MAPPING/);
  assert.match(workflow, /mapping\.value === 'empty-for-this-table' && mapping\.selectedLabel === 'Empty for this table'/);
  assert.match(workflow, /verify: async \(\) => \{ if \(verify\) verification = await verify\(\); \}/);
  assert.match(workflow, /reload canceled Group→Pivot APPEND and select its unchanged empty target', async \(\) =>/);
  assert.match(workflow, /reload applied Group→Pivot APPEND and render exact union rows', async \(\) =>/);
  assert.match(workflow, /reload canceled Group→Pivot APPEND edit and render its original union', async \(\) =>/);
  assert.match(workflow, /reload edited Group→Pivot APPEND and render exact union rows', async \(\) =>/);
  assert.match(workflow, /cancelStepRemovalAndPreserve\([\s\S]*?expectedRows\.length, async \(\) =>/);
  assert.match(workflow, /removeStepAndRestoreTarget\([\s\S]*?originalStepId, async \(builderAfterRemoval\) =>/);
  assert.match(workflow, /check\(report, 'persistence', 'removing Combine and reloading restores its rooted empty target'/);
  assert.match(workflow, /isDeepStrictEqual\(source\.document, restoredSources\[index\]\)/);
  assert.doesNotMatch(workflow, /sourceOutputState/);
});

test('Group→Pivot APPEND times exact proposal rows, multiplicity, and receipt/CAS verification', () => {
  const timing = workflow.match(/const recordBrowserTiming = async \([\s\S]*?\n};/);
  const renderer = workflow.match(/const renderFinalMapping = async \([\s\S]*?\n};/);
  const verify = workflow.match(/const verifyAppendPreview = async \([\s\S]*?\n    };/);
  const append = workflow.slice(workflow.indexOf('export const groupPivotAppendWorkflow ='));

  assert.ok(timing, 'timing helper is present');
  assert.ok(renderer, 'mapping renderer is present');
  assert.ok(verify, 'exact append verifier is present');
  const waitIndex = timing[0].indexOf('if (after) await waitFor(page, after, timeout);');
  const finishIndex = timing[0].indexOf('const finishedAtEpochMs = Date.now();', waitIndex);
  const elapsedIndex = timing[0].indexOf('elapsedMs = finishedAtEpochMs - started;', finishIndex);
  const completedActionIndex = timing[0].indexOf("status: 'passed'", elapsedIndex);
  const verifyIndex = timing[0].indexOf('await verify();', completedActionIndex);
  assert.ok(waitIndex >= 0 && finishIndex > waitIndex && elapsedIndex > finishIndex &&
    completedActionIndex > elapsedIndex && verifyIndex > completedActionIndex,
  'action-to-render timing ends after readiness and before post-render verification');
  assert.match(timing[0], /post-render verification completed/);
  assert.match(timing[0], /activeTimedActionId = previousTimedActionId;[\s\S]*?if \(verify\) \{\s*try \{\s*await verify\(\);/);
  assert.match(renderer[0], /verify,/);
  assert.match(renderer[0], /timeout: 5000,[\s\S]*?budget: 5000/);
  assert.match(verify[0], /const grid = await readGrid\(page, 'proposal'\)/);
  assert.match(verify[0], /assertRows\(report, 'Group→Pivot APPEND preview equals the exact independent union with source-specific null padding'/);
  assert.match(verify[0], /Group→Pivot APPEND preserves exact shared-ID multiplicity/);
  assert.match(verify[0], /expectedRowCount: expectedRows.length, actualRowCount: grid.rows.length/);
  assert.match(verify[0], /await assertDraftCandidate\(report, page, candidateCapture, candidateBase, candidateTarget, sources, 'APPEND'\)/);
  assert.match(workflow, /const parseNDJSON = \(path\) => readFileSync\(path, 'utf8'\)\.split/);
  assert.match(workflow, /assert\.deepEqual\(observations, expectedObservations\)/);
  assert.match(append, /expectedRows\.length === 7/);
  assert.match(append, /rawSourceRows: \{ observations: run\.raw\.observations\.length, reports: run\.raw\.reports\.length \}/);
  assert.match(append, /renderFinalMapping\(page, report, 'Group→Pivot current-draft APPEND auto-preview within five seconds'[\s\S]*?\(\) => verifyAppendPreview\(capture, base, target, true\)\)/);
  assert.match(append, /renderFinalMapping\(page, report, 'Group→Pivot current-draft APPEND apply preview within five seconds'[\s\S]*?\(\) => verifyAppendPreview\(applyCapture, applyBase, target\)\)/);
});
