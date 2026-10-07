import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { appendColumnOptionMapping, appendControlSnapshot, matchAppendChoiceControls } from '../append-option-evidence.mjs';
import { lifecycleAcceptanceDrift, registry, scenarioCaseFor } from '../../registry.mjs';

const workflow = readFileSync(new URL('../../workflows/builder-combine-draft.mjs', import.meta.url), 'utf8');
const evidence = readFileSync(new URL('../append-option-evidence.mjs', import.meta.url), 'utf8');
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
  assert.match(workflow, /appendControlSnapshot/);
  assert.match(workflow, /matchAppendChoiceControls\(\{ controls, expectedInputs, expectedOutputs, emptyMapping: APPEND_EMPTY_MAPPING \}\)/);
  assert.match(workflow, /appendColumnOptionMapping\(sources\[0\]\.groupKeyOutputColumn\)/);
  assert.match(workflow, /appendColumnOptionMapping\(sources\[1\]\.pivotIdentityOutputColumn\)/);
  assert.match(workflow, /requiredColumns\.some\(\(column\) => !column\?\.id\)/);
  assert.match(evidence, /mapping\.value === expectedMapping\.value && mapping\.selectedLabel === expectedMapping\.selectedLabel/);
  assert.match(workflow, /const APPEND_EMPTY_MAPPING = Object\.freeze\(\{ kind: 'append-empty-for-this-table' \}\)/);
  assert.ok(workflow.includes('[sources[0].appendCountLabel, APPEND_EMPTY_MAPPING]'));
  assert.ok(workflow.includes('[APPEND_EMPTY_MAPPING, finalLabel]'));
  assert.ok(workflow.includes('[APPEND_EMPTY_MAPPING, preliminaryLabel]'));
  assert.match(workflow, /await chooseOption\(page, selector, 'Empty for this table'\)/);
  assert.match(evidence, /expectedMapping === emptyMapping/);
  assert.match(evidence, /mapping\.value === 'empty-for-this-table' && mapping\.selectedLabel === 'Empty for this table'/);
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


const testColumn = (id, label, type, nullable = false) => ({ id, label, name: label, type, nullable });
const testSelect = (value, selectedLabel, group = null, disabled = false) => ({
  tagName: 'SELECT',
  value,
  selectedOptions: [{ textContent: selectedLabel, parentElement: group ? { label: group } : null, disabled }],
  options: [{}, {}, {}],
  getClientRects: () => [{}],
  matches: (selector) => selector === ':disabled' && disabled,
});
const testInput = (value) => ({
  tagName: 'INPUT',
  value,
  getClientRects: () => [{}],
  matches: () => false,
});
const testButton = () => ({
  tagName: 'BUTTON',
  value: '',
  className: 'selected',
  getClientRects: () => [{}],
  matches: () => false,
  getAttribute: (name) => name === 'aria-pressed' ? 'true' : null,
});

test('APPEND evidence preserves full option text and rejects a wrong source column ID', () => {
  const observationID = appendColumnOptionMapping(testColumn('obs-id', 'Observation ID', 'string', true));
  const reportID = appendColumnOptionMapping(testColumn('report-id', 'DiagnosticReport ID', 'string', true));
  const rowCount = appendColumnOptionMapping(testColumn('row-count', 'Row count', 'integer'));
  const finalCount = appendColumnOptionMapping(testColumn('final-count', 'final', 'integer', true));
  const preliminaryCount = appendColumnOptionMapping(testColumn('preliminary-count', 'preliminary', 'integer', true));
  const empty = Object.freeze({ kind: 'append-empty-for-this-table' });
  const expectedInputs = ['workspace-observation', 'workspace-report'];
  assert.deepEqual(
    [observationID.selectedLabel, reportID.selectedLabel, rowCount.selectedLabel, finalCount.selectedLabel, preliminaryCount.selectedLabel],
    ['Observation ID · string · nullable', 'DiagnosticReport ID · string · nullable', 'Row count · integer',
      'final · integer · nullable', 'preliminary · integer · nullable']);
  const expectedOutputs = [
    { name: 'record_id', label: 'Record ID', mappings: [observationID, reportID] },
    { name: 'observation_rows', label: 'Observation rows', mappings: [rowCount, empty] },
    { name: 'report_final', label: 'Report final', mappings: [empty, finalCount] },
    { name: 'report_preliminary', label: 'Report preliminary', mappings: [empty, preliminaryCount] },
  ];
  const elements = new Map();
  const addSelect = (selector, value, label, group = null) => elements.set(selector, testSelect(value, label, group));
  elements.set('button[data-testid="construction-combine-choice-append"]', testButton());
  expectedInputs.forEach((value, index) => addSelect(
    'select[aria-label="Input table ' + (index + 1) + '"]', value, 'source · current draft', 'Current draft tables'));
  const choices = [
    [observationID, reportID],
    [rowCount, empty],
    [empty, finalCount],
    [empty, preliminaryCount],
  ];
  expectedOutputs.forEach((output, index) => {
    const number = index + 1;
    elements.set('input[aria-label="Output field ' + number + ' name"]', testInput(output.name));
    elements.set('input[aria-label="Output field ' + number + ' label"]', testInput(output.label));
    choices[index].forEach((choice, inputIndex) => {
      const selector = 'select[aria-label="Output field ' + number + ' matching field in input ' + (inputIndex + 1) + '"]';
      if (choice === empty) addSelect(selector, 'empty-for-this-table', 'Empty for this table');
      else {
        const selectedText = choice.selectedLabel.replace('Observation', 'Observation\n');
        addSelect(selector, choice.value, selectedText);
      }
    });
  });
  const priorDocument = globalThis.document;
  const priorGetComputedStyle = globalThis.getComputedStyle;
  globalThis.document = { querySelector: (selector) => elements.get(selector) ?? null };
  globalThis.getComputedStyle = () => ({ display: 'block', visibility: 'visible' });
  try {
    const controls = appendControlSnapshot();
    assert.equal(controls.outputs[0].mappings[0].selectedLabel, 'Observation ID · string · nullable');
    assert.equal(controls.outputs[0].mappings[1].selectedLabel, 'DiagnosticReport ID · string · nullable');
    assert.equal(controls.outputs[1].mappings[0].selectedLabel, 'Row count · integer');
    assert.equal(controls.outputs[1].mappings[1].selectedLabel, 'Empty for this table');
    const accepted = matchAppendChoiceControls({ controls, expectedInputs, expectedOutputs, emptyMapping: empty });
    assert.equal(accepted.ok, true);
    assert.equal(accepted.exactOutputs, true);

    const wrongIDControls = structuredClone(controls);
    wrongIDControls.outputs[0].mappings[0].value = 'column:wrong-id';
    const wrongID = matchAppendChoiceControls({ controls: wrongIDControls, expectedInputs, expectedOutputs, emptyMapping: empty });
    assert.equal(wrongID.exactOutputs, false);
    assert.equal(wrongID.ok, false);

    const wrongLabelControls = structuredClone(controls);
    wrongLabelControls.outputs[0].mappings[0].selectedLabel = 'Ob ervation ID ·  tring · nullable';
    const wrongLabel = matchAppendChoiceControls({ controls: wrongLabelControls, expectedInputs, expectedOutputs, emptyMapping: empty });
    assert.equal(wrongLabel.exactOutputs, false);
  } finally {
    if (priorDocument === undefined) delete globalThis.document;
    else globalThis.document = priorDocument;
    if (priorGetComputedStyle === undefined) delete globalThis.getComputedStyle;
    else globalThis.getComputedStyle = priorGetComputedStyle;
  }
});
