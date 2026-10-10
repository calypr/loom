import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { getScenario } from '../../registry.mjs';
import { assertVisibleListCell, buildFullScopeMultiCodingWitnessQuery, selectNestedAuthoredExpandWitnesses } from '../nested-authored-expand-oracle.mjs';
import { createReport, finishReport, recordCheck } from '../report.mjs';

const project = 'loom_cda_fixture';
const generation = 'cda-fhir-v1';

const observation = (id, components) => ({
  id,
  sourceKey: `${project}_${id}`,
  project,
  generation,
  resourceType: 'Observation',
  components,
});

test('nested EXPAND oracle preserves flattened code values, duplicate order, source keys, and empty-parent witness', () => {
  const result = selectNestedAuthoredExpandWitnesses([
    observation('obs-mixed', [
      { valueString: 'alpha', code: { coding: [
        { system: 'sys-one', code: 'red' },
        { system: 'sys-two', code: 'blue' },
      ] } },
      { valueString: 'empty-inner', code: { coding: [] } },
      { valueString: 'missing-inner', code: {} },
      { valueString: 'beta', code: { coding: [
        { system: 'sys-one', code: 'red' },
      ] } },
    ]),
    observation('obs-empty', [
      { valueString: 'only-empty', code: { coding: [] } },
    ]),
  ], { project, generation });

  assert.deepEqual(result.witness, {
    mode: 'multi-coding-component',
    populatedObservationID: 'obs-mixed',
    emptyObservationID: 'obs-empty',
    hasMultiCodingComponent: true,
    hasEmptyInnerCoding: true,
    hasMissingInnerCoding: true,
    hasDuplicateSystemAndCode: true,
    hasEmptyParentWitness: true,
  });
  assert.deepEqual(result.selected.map(row => ({
    id: row.id,
    sourceKey: row.sourceKey,
    componentLabels: row.componentLabels,
    codes: row.codings.map(item => item.code),
    systems: row.codings.map(item => item.system),
    codingShapes: row.codingShapes,
  })), [
    {
      id: 'obs-mixed',
      sourceKey: 'loom_cda_fixture_obs-mixed',
      componentLabels: ['alpha', 'empty-inner', 'missing-inner', 'beta'],
      codes: ['red', 'blue', 'red'],
      systems: ['sys-one', 'sys-two', 'sys-one'],
      codingShapes: [
        { componentOrdinal: 0, state: 'present', count: 2 },
        { componentOrdinal: 1, state: 'empty' },
        { componentOrdinal: 2, state: 'missing' },
        { componentOrdinal: 3, state: 'present', count: 1 },
      ],
    },
    {
      id: 'obs-empty',
      sourceKey: 'loom_cda_fixture_obs-empty',
      componentLabels: ['only-empty'],
      codes: [],
      systems: [],
      codingShapes: [{ componentOrdinal: 0, state: 'empty' }],
    },
  ]);
  assert.deepEqual(result.expectedRows, [
    { id: 'obs-mixed', sourceKey: 'loom_cda_fixture_obs-mixed', componentOrdinal: 0, codingOrdinal: 0, ordinal: 0, componentLabel: 'alpha', code: 'red', system: 'sys-one', itemPresent: true },
    { id: 'obs-mixed', sourceKey: 'loom_cda_fixture_obs-mixed', componentOrdinal: 0, codingOrdinal: 1, ordinal: 1, componentLabel: 'alpha', code: 'blue', system: 'sys-two', itemPresent: true },
    { id: 'obs-mixed', sourceKey: 'loom_cda_fixture_obs-mixed', componentOrdinal: 3, codingOrdinal: 0, ordinal: 2, componentLabel: 'beta', code: 'red', system: 'sys-one', itemPresent: true },
    { id: 'obs-empty', sourceKey: 'loom_cda_fixture_obs-empty', componentOrdinal: null, codingOrdinal: null, ordinal: null, componentLabel: null, code: null, system: null, itemPresent: false },
  ]);
  assert.equal(result.expectedRows.filter(row => row.code === 'red').length, 2,
    'Duplicate coded values remain separate rows with distinct literal ordinals');
});

test('full-scope nested EXPAND query binds source scope and selects the requested nested coding cardinality', () => {
  const multi = buildFullScopeMultiCodingWitnessQuery({ project: 'loom "fixture"', generation });
  assert(multi.includes('r.project == "loom \\"fixture\\""'));
  assert(multi.includes('r.dataset_generation == "cda-fhir-v1"'));
  assert(multi.includes('r.payload.resourceType == "Observation"'));
  assert(multi.includes('LENGTH(r.payload.component) <= 8'));
  assert(multi.includes('FILTER LENGTH(invalidLabels) == 0'));
  assert(multi.includes('FILTER LENGTH(oversizedCodingLists) == 0'));
  assert(multi.includes('FILTER LENGTH(invalidCodingValues) == 0'));
  assert(multi.includes('FILTER SUM(codingCounts) >= 2 AND SUM(codingCounts) <= 25'));
  assert(multi.indexOf('FILTER LENGTH(multiCodingComponents) > 0') < multi.indexOf('LET invalidLabels'),
    'The selective multi-coding witness filter must run before detailed shape validation');
  assert(multi.includes('FILTER LENGTH(multiCodingComponents) > 0\nLET invalidLabels'));
  assert(!multi.includes('SORT '), 'The candidate query must not sort the full matching scope before LIMIT');
  assert(multi.includes('LIMIT 1'));
  assert(multi.includes('sourceKey: r._key'));
  assert.throws(() => buildFullScopeMultiCodingWitnessQuery({ project: '', generation }), /project is required/);
  assert.throws(() => buildFullScopeMultiCodingWitnessQuery({ project, generation: '' }), /generation is required/);
});

test('default nested EXPAND mode rejects a single-coding-per-component witness', () => {
  assert.throws(() => selectNestedAuthoredExpandWitnesses([
    observation('only-flat', [
      { valueString: 'one', code: { coding: [{ system: 'sys-one', code: 'red' }] } },
      { valueString: 'two', code: { coding: [{ system: 'sys-two', code: 'blue' }] } },
    ]),
  ], { project, generation }), /No bounded Observation has a populated nested coding list with a multi-coding component/);
});

test('full-scope witness selection reports the empty-parent branch as unsearched', () => {
  const result = selectNestedAuthoredExpandWitnesses([
    observation('obs-multi', [{ valueString: 'alpha', code: { coding: [
      { system: 'sys-one', code: 'red' }, { system: 'sys-two', code: 'blue' },
    ] } }]),
  ], { project, generation, scanLimit: 1, searchScope: 'full-scope-candidate' });

  assert.equal(result.searchScope, 'full-scope-candidate');
  assert.equal(result.witness.hasMultiCodingComponent, true);
  assert.equal(result.gaps[0].assertion, 'PRESERVE_PARENT emits an explicit row when the nested coding list is empty');
  assert.match(result.gaps[0].reason, /does not search for an all-empty parent/);
});

test('full-scope candidate failure only rules out the bounded candidate shape', () => {
  assert.throws(() => selectNestedAuthoredExpandWitnesses([], {
    project, generation, scanLimit: 1, searchScope: 'full-scope-candidate',
  }), /No eligible full-scope candidate Observation has a populated nested coding list with a multi-coding component/);
});

test('single-coding-per-component mode preserves exact flat value order and source/ordinal tuples', () => {
  const result = selectNestedAuthoredExpandWitnesses([
    observation('obs-flat', [
      { valueString: 'aliquot', code: { coding: [{ system: 'https://cda.readthedocs', code: 'specimen_type' }] } },
      { valueString: 'Adenoma, NOS', code: { coding: [{ system: 'https://cda.readthedocs', code: 'primary_disease_type' }] } },
    ]),
  ], { project, generation, witnessMode: 'single-coding-per-component' });

  assert.deepEqual(result.witness, {
    mode: 'single-coding-per-component',
    populatedObservationID: 'obs-flat',
    emptyObservationID: null,
    hasMultiCodingComponent: false,
    hasEmptyInnerCoding: false,
    hasMissingInnerCoding: false,
    hasDuplicateSystemAndCode: false,
    hasEmptyParentWitness: false,
  });
  assert.deepEqual(result.selected.map(record => ({
    id: record.id,
    sourceKey: record.sourceKey,
    componentLabels: record.componentLabels,
    codes: record.codings.map(item => item.code),
    codingShapes: record.codingShapes,
  })), [{
    id: 'obs-flat',
    sourceKey: 'loom_cda_fixture_obs-flat',
    componentLabels: ['aliquot', 'Adenoma, NOS'],
    codes: ['specimen_type', 'primary_disease_type'],
    codingShapes: [
      { componentOrdinal: 0, state: 'present', count: 1 },
      { componentOrdinal: 1, state: 'present', count: 1 },
    ],
  }]);
  assert.deepEqual(result.expectedRows, [
    { id: 'obs-flat', sourceKey: 'loom_cda_fixture_obs-flat', componentOrdinal: 0, codingOrdinal: 0, ordinal: 0, componentLabel: 'aliquot', code: 'specimen_type', system: 'https://cda.readthedocs', itemPresent: true },
    { id: 'obs-flat', sourceKey: 'loom_cda_fixture_obs-flat', componentOrdinal: 1, codingOrdinal: 0, ordinal: 1, componentLabel: 'Adenoma, NOS', code: 'primary_disease_type', system: 'https://cda.readthedocs', itemPresent: true },
  ]);
  assert.deepEqual(result.gaps, [{
    assertion: 'PRESERVE_PARENT emits an explicit row when the nested coding list is empty',
    status: 'untested',
    reason: 'The bounded 1000-Observation scan found no second root whose nested coding arrays are all empty or missing.',
  }]);
  assert.throws(() => selectNestedAuthoredExpandWitnesses([
    observation('obs-multi', [{ valueString: 'item', code: { coding: [
      { system: 'sys-one', code: 'red' }, { system: 'sys-two', code: 'blue' },
    ] } }]),
  ], { project, generation, witnessMode: 'single-coding-per-component' }), /one coding per populated component/);
});

test('nested list DOM compares rendered text while leaving protocol arrays exact', () => {
  const labels = ['alpha; beta', 'Adenoma, NOS', 'alpha; beta'];
  assertVisibleListCell({
    text: 'alpha; beta; Adenoma, NOS; alpha; beta',
    raw: '["misleading title is not a protocol array"]',
  }, labels, 'Visible nested label list must preserve display order and duplicates');
  assert.deepEqual(labels, ['alpha; beta', 'Adenoma, NOS', 'alpha; beta'],
    'Expected protocol label array must remain literal even when a value contains the display delimiter');
  assert.throws(() => assertVisibleListCell({ text: 'alpha; beta; alpha; beta; Adenoma, NOS' }, labels,
    'Visible labels must keep outer-component order'), /Visible labels must keep outer-component order/);
  assert.throws(() => assertVisibleListCell({ text: 'alpha; beta; Adenoma, NOS' }, labels,
    'Visible labels must retain the duplicate component'), /Visible labels must retain the duplicate component/);

  const codes = [null, 'red; blue', 'red', 'red; blue'];
  assertVisibleListCell({ text: 'red; blue; red; red; blue', raw: 'lossless title text' }, codes,
    'Visible nested coding list omits displayed nulls but keeps literal duplicate order');
  assert.deepEqual(codes, [null, 'red; blue', 'red', 'red; blue'],
    'Display comparison must not mutate or reconstruct the protocol array');
  assertVisibleListCell({ text: '—', raw: '[]' }, [], 'Empty protocol list displays as an em dash');
  assert.throws(() => assertVisibleListCell({ text: '' }, [], 'Empty protocol list must render an em dash'),
    /Empty protocol list must render an em dash/);
});

test('nested EXPAND removal waits for exact successor capabilities before and after reload', () => {
  const workflow = readFileSync(new URL('../../workflows/verify-cda-nested-authored-expand-browser.mjs', import.meta.url), 'utf8');
  assert(workflow.includes("import { waitForAppliedSourceCapabilities, waitForSourceCapabilities } from './verify-cda-authored-expand-browser.mjs';"));

  const applyStart = workflow.indexOf("await measure('native-EXPAND-remove-apply-restoration'");
  const applyEnd = workflow.indexOf('\n    });\n    builder = (await api(`${base}/builder`)).body;', applyStart);
  assert(applyStart >= 0 && applyEnd > applyStart, 'Removal Apply must have a bounded measured action');
  const apply = workflow.slice(applyStart, applyEnd);
  assert(workflow.lastIndexOf('const removalApplyRequestIndex = report.browserRequests.length;', applyStart) >= 0,
    'Removal Apply must establish its own request census boundary before the click');
  assert(apply.includes("const removeApplyRequest = latestCommand('APPLY_CONSTRUCTION_PROPOSAL');"),
    'Pre-reload capabilities must follow the exact native Apply request');
  assert(apply.includes('await waitForAppliedSourceCapabilities(requestMonitor, {'),
    'Removal Apply must wait for its exact successor source capabilities before reload');
  assert(apply.includes('fromIndex: removalApplyRequestIndex'),
    'Pre-reload capability wait must start at the removal action request boundary');
  assert(apply.includes('deadlineAt: startedAt + 5000'),
    'Pre-reload capability wait must remain within the removal action’s original five-second deadline');
  assert(apply.includes('path: `${base}/construction-capabilities`') && apply.includes('outputId,'),
    'Pre-reload capability wait must bind the owned endpoint and output');
  assert(apply.includes('report.removalApplyCapabilities ='),
    'The pre-reload capability receipt must be retained as workflow evidence');

  const identityCheck = workflow.indexOf('assert.deepEqual(removalReloadSourceIdentity, removalApplySourceIdentity', applyEnd);
  const reloadBoundary = workflow.indexOf('const removalReloadRequestIndex = report.browserRequests.length;', applyEnd);
  assert(identityCheck > applyEnd && reloadBoundary > identityCheck,
    'The saved identity must match the pre-reload capabilities before starting final reload');
  const start = workflow.indexOf("await openTable(sourceRecords.length, 'reload-after-expand-removal', async (startedAt) => {", reloadBoundary);
  assert(start > reloadBoundary, 'Final removal reload must occur after Apply capabilities settle');
  const end = workflow.indexOf('\n    report.removalReloadPreview', start);
  assert(end > start, 'Final reload capability wait must finish before source preview verification');
  const reload = workflow.slice(start, end);
  assert(reload.includes('fromIndex: removalReloadRequestIndex'), 'Final reload wait must begin at its own request census boundary');
  assert(reload.includes('deadlineAt: startedAt + 5000'), 'Final reload wait must keep the original five-second action deadline');
  assert(reload.includes('path: `${base}/construction-capabilities`'), 'Final reload must wait for the owned capabilities endpoint');
  assert(reload.includes('expected: removalReloadSourceIdentity'), 'Final reload must bind the capability response to saved source identity');
});

test('native workflow binds each witness mode to its own registered lifecycle case', () => {
  const workflow = readFileSync(new URL('../../workflows/verify-cda-nested-authored-expand-browser.mjs', import.meta.url), 'utf8');
  const spec = readFileSync(new URL('../../specs/standalone-cda-nested-expand.spec.mjs', import.meta.url), 'utf8');
  const scenario = getScenario('cda-authored-nested-expand');
  assert.equal(scenario.cases['nested-coding-expand-lifecycle'].witnessMode, 'multi-coding-component');
  assert.equal(scenario.cases['nested-coding-expand-lifecycle-single-per-component'].witnessMode, 'single-coding-per-component');
  assert.equal(scenario.cases['nested-coding-expand-lifecycle'].requiredChecks.length, 17);
  assert.deepEqual(
    scenario.cases['nested-coding-expand-lifecycle-single-per-component'].requiredChecks,
    scenario.cases['nested-coding-expand-lifecycle'].requiredChecks,
  );
  assert(workflow.includes("'nested-coding-expand-lifecycle': 'multi-coding-component'"));
  assert(workflow.includes("'nested-coding-expand-lifecycle-single-per-component': 'single-coding-per-component'"));
  assert(spec.includes("caseName: 'nested-coding-expand-lifecycle-single-per-component'"));
  assert(spec.includes("cdaUiRouting: 'explicit-query'"));
  assert(spec.includes('with one coding per component and exact source identity'));
});

test('nested EXPAND oracle keeps duplicate and inner-array shapes optional and reports only the empty-parent gap', () => {
  const result = selectNestedAuthoredExpandWitnesses([
    observation('obs-populated', [
      { valueString: 'alpha', code: { coding: [
        { system: 'sys-one', code: 'red' },
        { system: 'sys-two', code: 'blue' },
      ] } },
    ]),
  ], { project, generation });

  assert.deepEqual(result.selected.map(record => record.id), ['obs-populated']);
  assert.deepEqual(result.expectedRows, [
    { id: 'obs-populated', sourceKey: 'loom_cda_fixture_obs-populated', componentOrdinal: 0, codingOrdinal: 0, ordinal: 0, componentLabel: 'alpha', code: 'red', system: 'sys-one', itemPresent: true },
    { id: 'obs-populated', sourceKey: 'loom_cda_fixture_obs-populated', componentOrdinal: 0, codingOrdinal: 1, ordinal: 1, componentLabel: 'alpha', code: 'blue', system: 'sys-two', itemPresent: true },
  ]);
  assert.equal(result.witness.hasDuplicateSystemAndCode, false);
  assert.equal(result.witness.hasEmptyInnerCoding, false);
  assert.equal(result.witness.hasMissingInnerCoding, false);
  assert.equal(result.witness.hasEmptyParentWitness, false);
  assert.equal(result.gaps.length, 1);
  assert.equal(result.gaps[0].assertion, 'PRESERVE_PARENT emits an explicit row when the nested coding list is empty');
});

test('native reload checks produce registered persistence-dimension report evidence', () => {
  const names = [
    'Reload preserves native EXPAND output and unchanged RECORDS row definition',
    'Edited EXPAND output and unchanged RECORDS definition persist after reload',
    'Reload after removal preserves source-record restoration',
  ];
  const workflow = readFileSync(new URL('../../workflows/verify-cda-nested-authored-expand-browser.mjs', import.meta.url), 'utf8');
  for (const name of names) {
    assert(workflow.includes(`cda.check('persistence', '${name}'`), `${name} must mark the report persistence dimension`);
    assert(!workflow.includes(`recordAssertion('${name}'`), `${name} must not bypass the report dimension producer`);
  }

  const report = createReport({
    scenario: 'cda-authored-nested-expand',
    target: {},
    evidenceDirectory: '/tmp/nested-expand-report-test',
    caseName: 'nested-coding-expand-lifecycle',
    requiredChecks: names,
  });
  for (const name of names) recordCheck(report, 'persistence', name, true, { unchangedSavedState: true });
  finishReport(report);
  assert.equal(report.dimensions.persistence.status, 'passed');
  assert.deepEqual(report.missingRequiredChecks, []);
  assert.equal(report.status, 'passed');
  assert.deepEqual(report.assertions.map(({ dimension, name }) => ({ dimension, name })),
    names.map(name => ({ dimension: 'persistence', name })));
});
