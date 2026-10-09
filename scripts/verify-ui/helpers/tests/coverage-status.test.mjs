import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, readFileSync, renameSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, relative, sep } from 'node:path';
import { classifyEvidence, classifyFreshness, lifecycleEvidenceContractIssues, readReports, summarizeCoverage, summarizeRenderCheckpoints } from '../coverage-status.mjs';
import { caseNamesFor, coverageDrift, hasLifecycleContract, registry, requiresLifecycleAcceptance, scenarioCaseFor, unmappedLifecycleCoverage } from '../../registry.mjs';
import { buildArangoShellInvocation } from '../owned-arangosh-command.mjs';
import { finalOutputSchema, sourceColumnSchema } from '../unpivot-schema.mjs';

const complete = Object.fromEntries(['usability', 'correctness', 'persistence', 'performance'].map((dimension) => [dimension, { status: 'passed' }]));
const fingerprint = (sha256 = 'a'.repeat(64), files = 12) => ({ sha256, files });
const apiBuildIdentity = '1'.repeat(64) + ':' + '2'.repeat(64) + ':' + '3'.repeat(64);
const freezeAssertion = (before, after = before, status = 'passed') => ({
  name: 'watched source stayed unchanged during browser run',
  status,
  evidence: { before, after },
});
const retainedPostPivotCountTimings = JSON.parse(readFileSync(
  new URL('./fixtures/post-pivot-count-qzzOck-timings.json', import.meta.url), 'utf8',
));
const postPivotCountContract = scenarioCaseFor(
  'standalone-reshape-related-source-after-pivot',
  'related-source-count-after-pivot',
);
const postPivotCountReport = () => {
  const assertions = postPivotCountContract.requiredChecks.map((name) => ({ name, status: 'passed' }));
  assertions.find((assertion) => assertion.name === postPivotCountContract.lifecycleEvidence.performance.check).evidence = {
    actionCount: retainedPostPivotCountTimings.actionCount,
    measuredTransitionCount: retainedPostPivotCountTimings.cases.length,
    maxActionMs: retainedPostPivotCountTimings.maxActionMs,
    workflowCheckpoints: retainedPostPivotCountTimings.cases.map(({ name, elapsedMs }) => ({ name, durationMs: elapsedMs })),
  };
  return {
    schemaVersion: 2,
    status: 'passed',
    dimensions: {
      usability: { status: 'passed' },
      correctness: { status: 'passed' },
      persistence: { status: 'untested' },
      performance: { status: 'untested' },
    },
    assertions,
  };
};

test('render checkpoint summary preserves explicit budgets and outcomes across named transition lists', () => {
  const checks = [
    'both Patient collection replacement click-to-grid transitions complete within five seconds',
    'All native action and action-to-render checkpoints complete within five seconds',
    'all native action-to-render checkpoints complete within five seconds',
    'approved full-population Pivot action-to-render checkpoint',
  ];
  const summary = summarizeRenderCheckpoints({ assertions: [
    {
      name: checks[0],
      dimension: 'performance',
      evidence: { checkpoints: [
        { name: 'one-member Patient attachment Replace click through exact APPEND grid', durationMs: 1219.387416999998, budgetMs: 5000, withinBudget: true },
        { name: 'restored two-member Patient attachment Replace click through exact APPEND grid', durationMs: 1213.6332089999996, budgetMs: 5000, withinBudget: true },
      ] },
    },
    {
      name: checks[1],
      dimension: 'performance',
      evidence: { workflowCheckpoints: [
        { name: 'related-chain-unpivot-preview', durationMs: 939 },
        { name: 'Cancel Unpivot removal to exact saved rows', durationMs: 1624, limitMs: 5000, withinBudget: true },
      ] },
    },
    {
      name: checks[2],
      dimension: 'performance',
      evidence: { timingCheckpoints: [
        { name: 'reload-preserve-parent', durationMs: 1294 },
        { name: 'empty-only-error-repair-cancel', durationMs: 1631 },
      ] },
    },
    {
      name: checks[3],
      dimension: 'performance',
      evidence: { checkpoints: [
        { name: 'full-population-pivot-discovery-to-render', durationMs: 9500, budgetMs: 10_000, withinBudget: true },
      ] },
    },
  ] }, { requiredCheckNames: checks });

  assert.equal(summary.status, 'present');
  assert.equal(summary.count, 7);
  assert.equal(summary.maximumDurationMs, 9500);
  assert.deepEqual(summary.checkpoints.map(({ name, durationMs, budgetMs, limitMs, withinBudget, evidencePath }) => [
    name, durationMs, budgetMs, limitMs, withinBudget, evidencePath,
  ]), [
    ['one-member Patient attachment Replace click through exact APPEND grid', 1219.387416999998, 5000, undefined, true, 'assertions[].evidence.checkpoints[].durationMs'],
    ['restored two-member Patient attachment Replace click through exact APPEND grid', 1213.6332089999996, 5000, undefined, true, 'assertions[].evidence.checkpoints[].durationMs'],
    ['related-chain-unpivot-preview', 939, undefined, undefined, undefined, 'assertions[].evidence.workflowCheckpoints[].durationMs'],
    ['Cancel Unpivot removal to exact saved rows', 1624, undefined, 5000, true, 'assertions[].evidence.workflowCheckpoints[].durationMs'],
    ['reload-preserve-parent', 1294, undefined, undefined, undefined, 'assertions[].evidence.timingCheckpoints[].durationMs'],
    ['empty-only-error-repair-cancel', 1631, undefined, undefined, undefined, 'assertions[].evidence.timingCheckpoints[].durationMs'],
    ['full-population-pivot-discovery-to-render', 9500, 10_000, undefined, true, 'assertions[].evidence.checkpoints[].durationMs'],
  ]);
  assert.equal(Object.hasOwn(summary.checkpoints[2], 'budgetMs'), false,
    'The summary does not infer a five-second budget from the registered check name.');
});

test('declared malformed checkpoint lists report issues and differ from absent timing evidence', () => {
  const checkName = 'declared action-to-render checkpoint list';
  const summary = summarizeRenderCheckpoints({ assertions: [
    {
      name: checkName,
      dimension: 'performance',
      evidence: { checkpoints: [
        { name: 'valid checkpoint', durationMs: 100, budgetMs: 500, withinBudget: true },
        { name: 'nonfinite duration', durationMs: Number.NaN, budgetMs: 500, withinBudget: false },
        { name: 'negative duration', durationMs: -1, budgetMs: 500, withinBudget: false },
        { name: 'nonfinite budget', durationMs: 100, budgetMs: Number.POSITIVE_INFINITY, withinBudget: true },
        { name: 'negative source budget', durationMs: 100, limitMs: -1, withinBudget: true },
        { name: 'invalid recorded outcome', durationMs: 100, budgetMs: 500, withinBudget: 'yes' },
      ] },
    },
    { name: checkName, dimension: 'performance', evidence: { timingCheckpoints: [] } },
  ] }, { requiredCheckNames: [checkName] });

  assert.equal(summary.status, 'malformed');
  assert.equal(summary.count, 1);
  assert.equal(summary.maximumDurationMs, 100);
  assert.deepEqual(summary.issues.map(({ evidencePath, reason }) => [evidencePath, reason]), [
    ['assertions[].evidence.checkpoints[1].durationMs', 'Duration must be a finite non-negative number.'],
    ['assertions[].evidence.checkpoints[2].durationMs', 'Duration must be a finite non-negative number.'],
    ['assertions[].evidence.checkpoints[3].budgetMs', 'budgetMs must be a finite positive number.'],
    ['assertions[].evidence.checkpoints[4].limitMs', 'limitMs must be a finite positive number.'],
    ['assertions[].evidence.checkpoints[5].withinBudget', 'withinBudget must be a boolean.'],
    ['assertions[].evidence.timingCheckpoints', 'Expected a non-empty checkpoint list.'],
  ]);

  const absent = summarizeRenderCheckpoints({ assertions: [{ name: checkName, dimension: 'performance', evidence: {} }] }, {
    requiredCheckNames: [checkName],
  });
  assert.equal(absent.status, 'absent');
  assert.equal(absent.count, 0);
  assert.deepEqual(absent.issues, []);
});

test('action and scalar timing evidence validate and preserve explicit metadata', () => {
  const checkName = 'declared native timing evidence';
  const summary = summarizeRenderCheckpoints({ assertions: [
    {
      name: checkName,
      dimension: 'performance',
      evidence: { actions: [
        { name: 'valid action', durationMs: 25, budgetMs: 5000, withinBudget: true },
        { name: 'invalid action outcome', durationMs: 20, budgetMs: 5000, withinBudget: 'yes' },
        { name: 'invalid action budget', durationMs: 10, limitMs: 0, withinBudget: false },
        { name: 'negative action duration', durationMs: -1, budgetMs: 5000, withinBudget: false },
        { name: 'nonfinite action budget', durationMs: 8, budgetMs: Number.NaN, withinBudget: true },
      ] },
    },
    {
      name: checkName,
      dimension: 'performance',
      evidence: { elapsedMs: 12, limitMs: 5000, withinBudget: false },
    },
    {
      name: checkName,
      dimension: 'performance',
      evidence: { durationMs: 100, budgetMs: Number.NaN, withinBudget: true },
    },
    {
      name: checkName,
      dimension: 'performance',
      evidence: { elapsedMs: Number.POSITIVE_INFINITY, budgetMs: 5000, withinBudget: false },
    },
    {
      name: checkName,
      dimension: 'performance',
      evidence: { elapsedMs: 14, limitMs: -1, withinBudget: 'no' },
    },
  ] }, { requiredCheckNames: [checkName] });

  assert.equal(summary.status, 'malformed');
  assert.equal(summary.count, 2);
  assert.equal(summary.maximumDurationMs, 25);
  assert.deepEqual(summary.checkpoints.map(({ name, durationMs, budgetMs, limitMs, withinBudget, evidencePath }) => [
    name, durationMs, budgetMs, limitMs, withinBudget, evidencePath,
  ]), [
    ['valid action', 25, 5000, undefined, true, 'assertions[].evidence.actions[].durationMs'],
    [checkName, 12, undefined, 5000, false, 'assertions[].evidence.elapsedMs'],
  ]);
  assert.deepEqual(summary.issues.map(({ evidencePath, reason }) => [evidencePath, reason]), [
    ['assertions[].evidence.actions[1].withinBudget', 'withinBudget must be a boolean.'],
    ['assertions[].evidence.actions[2].limitMs', 'limitMs must be a finite positive number.'],
    ['assertions[].evidence.actions[3].durationMs', 'Duration must be a finite non-negative number.'],
    ['assertions[].evidence.actions[4].budgetMs', 'budgetMs must be a finite positive number.'],
    ['assertions[].evidence.budgetMs', 'budgetMs must be a finite positive number.'],
    ['assertions[].evidence.elapsedMs', 'Duration must be a finite non-negative number.'],
    ['assertions[].evidence.limitMs', 'limitMs must be a finite positive number.'],
    ['assertions[].evidence.withinBudget', 'withinBudget must be a boolean.'],
  ]);
});

test('generic checkpoints require declared performance evidence while legacy fields retain compatibility', () => {
  const checkName = 'required correctness assertion';
  const genericReport = { assertions: [{
    name: checkName,
    dimension: 'correctness',
    evidence: { checkpoints: [{ name: 'generic checkpoint', durationMs: 12 }] },
  }] };
  const genericUnclassified = summarizeRenderCheckpoints(genericReport, { requiredCheckNames: [checkName] });
  assert.equal(genericUnclassified.status, 'absent');
  assert.equal(genericUnclassified.count, 0);
  assert.deepEqual(genericUnclassified.issues, []);

  const genericDeclared = summarizeRenderCheckpoints(genericReport, {
    performanceCheckNames: [checkName],
    requiredCheckNames: [checkName],
  });
  assert.deepEqual(genericDeclared.checkpoints.map(({ name, durationMs }) => [name, durationMs]), [['generic checkpoint', 12]]);

  const legacy = summarizeRenderCheckpoints({ assertions: [{
    name: checkName,
    dimension: 'correctness',
    evidence: { workflowCheckpoints: [{ name: 'legacy checkpoint', durationMs: 13 }] },
  }] }, { requiredCheckNames: [checkName] });
  assert.deepEqual(legacy.checkpoints.map(({ name, durationMs }) => [name, durationMs]), [['legacy checkpoint', 13]]);
});



test('Related Unpivot uses the owned authenticated Arango oracle and persists both filter states', () => {
  const workflow = readFileSync(new URL('../../workflows/verify-cda-related-unpivot-browser.mjs', import.meta.url), 'utf8');
  const invocation = buildArangoShellInvocation({ container: 'owned-arango', script: 'print("oracle")', database: 'loom_dev' });
  assert.equal(invocation.command, 'rtk');
  assert.deepEqual(invocation.args.slice(0, 4), ['proxy', 'docker', 'exec', 'owned-arango']);
  const shellCommand = invocation.args.at(-1);
  assert.match(shellCommand, /--server\.username root --server\.password "\$ARANGO_ROOT_PASSWORD"/);
  assert.match(shellCommand, /--server\.database 'loom_dev'/);
  assert.match(shellCommand, /--javascript\.execute "\$script_file"/);
  assert.match(workflow, /buildArangoShellInvocation\(\{ container: arangoContainer, script, database: 'loom_dev' \}\)/);
  assert.match(workflow, /spawnSync\(invocation\.command, invocation\.args, \{ encoding: 'utf8', timeout: 30000 \}\)/);

  const filterLifecycleFragments = [
    'const missingApply = await apply([]);',
    'const missingReload = await open([]);',
    "assert.equal(filter.operation.filter.operator, 'MISSING');",
    "Object.hasOwn(filter.operation.filter, 'values'), false",
    'const equalityApply = await apply(unpivotExpected);',
    'const equalityReload = await open(unpivotExpected);',
    "editedFilter.operation.filter.operator, 'EQUALS'",
    "editedFilter.operation.filter.values, [{ kind: 'STRING', string: source.id }]",
  ];
  const filterLifecycleOffsets = filterLifecycleFragments.map(fragment => workflow.indexOf(fragment));
  assert(filterLifecycleOffsets.every(offset => offset >= 0), 'The native workflow must assert saved MISSING and edited EQUALS states');
  assert.deepEqual(filterLifecycleOffsets, [...filterLifecycleOffsets].sort((left, right) => left - right),
    'The saved MISSING assertion must precede the edited and reloaded EQUALS assertion');
});

test('Related Unpivot binds saved CAS and output schema to the pre-transition oracle', () => {
  const workflow = readFileSync(new URL('../../workflows/verify-cda-related-unpivot-browser.mjs', import.meta.url), 'utf8');
  for (const fragment of [
    'appliedUnpivot.state.draftVersion > expandedVersion',
    'appliedUnpivot.state.draftDigest, expandedDigest',
    'const expectedUnpivotColumnIDs = [',
    '...relatedColumnIDs,',
    'unpivot.operation.unpivot.keyOutputColumnId,',
    'unpivot.operation.unpivot.valueOutputColumnId,',
    'const sourceColumnID = directColumn.columnId;',
    'const relatedSourceOutputs = relatedSourceStep.outputs;',
    'const expectedUnpivotSchema = [',
    '...relatedSourceOutputs.filter(output => output.id !== sourceColumnID).map(finalOutputSchema)',
    'assert.deepEqual(unpivot.outputs.map(finalOutputSchema), expectedUnpivotSchema',
    'assert.deepEqual(reloadedUnpivot.outputs.map(finalOutputSchema), expectedUnpivotSchema',
    'assert.deepEqual(doc(builder).columns.map(sourceColumnSchema), expandedDocument.columns.map(sourceColumnSchema)',
    "keyLabel: 'Variable'",
    "valueLabel: 'Value'",
    'missingApply.state.draftVersion > beforeFilter.draftVersion',
    'missingApply.state.draftDigest, beforeFilter.draftDigest',
    'equalityApply.state.draftVersion > missingApply.state.draftVersion',
    'equalityApply.state.draftDigest, missingApply.state.draftDigest',
    'equalityReload.state.draftVersion, equalityApply.state.draftVersion',
    'equalityReload.state.draftDigest, equalityApply.state.draftDigest',
    "recordLifecycleCheck('performance',\n    'All native action and action-to-render checkpoints complete within five seconds'",
  ]) assert(workflow.includes(fragment), `Related Unpivot lifecycle is missing contract assertion: ${fragment}`);
});

test('Related Unpivot keeps saved source columns and final stage outputs on their distinct wire contracts', () => {
  const savedSourceColumn = {
    column: 'col_0156b5870232e7389f8a618f',
    columnId: 'source_c717740ebe8e76cecec55427',
    label: 'Specimen ID',
    logicalType: 'string',
    occurrenceId: 'base',
    source: { field: { path: 'id', projectionMode: 'VALUE' }, kind: 'field' },
    table: { order: 0, visible: true },
  };
  assert.deepEqual(sourceColumnSchema(savedSourceColumn), {
    columnId: 'source_c717740ebe8e76cecec55427',
    column: 'col_0156b5870232e7389f8a618f',
    label: 'Specimen ID',
    logicalType: 'string',
  });
  assert.equal(Object.hasOwn(savedSourceColumn, 'id'), false,
    'Saved Builder source columns use columnId and column, not construction-output id/name fields');

  const finalStageOutputs = [
    { id: 'source_c717740ebe8e76cecec55427', name: 'specimen_id', label: 'Specimen ID', type: 'string' },
    { id: 'related_patient_id', name: 'related_patient_id', label: 'Patient FHIR resource ID', type: 'string' },
    { id: 'unpivot_key', name: 'variable', label: 'Variable', type: 'string' },
    { id: 'unpivot_value', name: 'value', label: 'Value', type: 'string' },
  ];
  assert.deepEqual(finalStageOutputs.map(finalOutputSchema), [
    { id: 'source_c717740ebe8e76cecec55427', name: 'specimen_id', label: 'Specimen ID', type: 'string' },
    { id: 'related_patient_id', name: 'related_patient_id', label: 'Patient FHIR resource ID', type: 'string' },
    { id: 'unpivot_key', name: 'variable', label: 'Variable', type: 'string' },
    { id: 'unpivot_value', name: 'value', label: 'Value', type: 'string' },
  ]);
  assert.notDeepEqual(sourceColumnSchema(savedSourceColumn), finalOutputSchema(finalStageOutputs[0]),
    'Source projection and final-stage output projections retain distinct identities and field names');

  const workflow = readFileSync(new URL('../../workflows/verify-cda-related-unpivot-browser.mjs', import.meta.url), 'utf8');
  for (const fragment of [
    'directColumn?.columnId',
    'const sourceColumnID = directColumn.columnId;',
    'const relatedSourceOutputs = relatedSourceStep.outputs;',
    'retainedColumnIDs: relatedSteps.at(-1).outputs.filter(output => output.id !== directColumn.columnId).map(output => output.id)',
    'relatedSourceOutputs.filter(output => output.id !== sourceColumnID).map(finalOutputSchema)',
    'unpivot.outputs.map(finalOutputSchema)',
    'reloadedUnpivot.outputs.map(finalOutputSchema)',
    'reloadedDocument.columns.map(sourceColumnSchema)',
  ]) assert(workflow.includes(fragment), `Related Unpivot must use the explicit saved/final schema contract: ${fragment}`);
  assert.equal(workflow.includes('doc(builder).columns.map(column => column.id)'), false,
    'The workflow must not interpret source-projection columns as final construction outputs');
});

test('Related oracle keeps the compiler-required terminal document identity order', () => {
  const workflow = readFileSync(new URL('../../workflows/verify-cda-related-unpivot-browser.mjs', import.meta.url), 'utf8');
  assert.match(workflow, /SORT d\._id RETURN DISTINCT \{ id: d\.id, _id: d\._id \}/,
    'The bounded oracle retains public values but orders target records by the compiler terminal identity');
  assert.match(workflow, /SORT anchor, relatedRecord\._id LIMIT/,
    'The outer bounded query preserves parent identity and terminal identity order');
  assert.doesNotMatch(workflow, /SORT d\.id|SORT anchor, relatedRecord\.id/,
    'Public resource IDs must not override the compiler row-identity order');
  assert.match(workflow, /assertVisibleRowsMatchOracle\(result\.rows, expectedRows, \{ label: `\$\{name\} preview`, exactWindow: true \}\)/,
    'The preview must still match the complete ordered oracle window exactly');

  const conditionAnchor = 'Condition/g_87c53cf5427c19a229db2c2049b18d164f1ac23daddbbb8d4da7b67e46d20849';
  const matches = [
    { anchor: conditionAnchor, id: '38c7c08b-a57d-5d59-b132-a17623d5273e', terminalID: 'Observation/g_988209f5c99e71afe3210b4960f987fa51e1f6281d030753e7d778c6239a0273' },
    { anchor: conditionAnchor, id: 'e50dcc49-e3ff-51a2-94f6-07babfec3005', terminalID: 'Observation/g_35cebad3722897001b7645957d2782a92226d80131bbc1fe15707c13e13d13c5' },
  ];
  const orderedByCompilerIdentity = matches.toSorted((left, right) =>
    left.anchor.localeCompare(right.anchor) || left.terminalID.localeCompare(right.terminalID));
  assert.deepEqual(orderedByCompilerIdentity.map(match => match.id), [
    'e50dcc49-e3ff-51a2-94f6-07babfec3005',
    '38c7c08b-a57d-5d59-b132-a17623d5273e',
  ]);
  assert.deepEqual(matches.toSorted((left, right) => left.anchor.localeCompare(right.anchor) || left.id.localeCompare(right.id))
    .map(match => match.id), [
    '38c7c08b-a57d-5d59-b132-a17623d5273e',
    'e50dcc49-e3ff-51a2-94f6-07babfec3005',
  ], 'The retained witness distinguishes resource ID order from compiler terminal identity order');
});

test('every registry case resolves its Playwright mapping and owned/custom checks', () => {
  const registeredCases = registry.flatMap((scenario) => Object.entries(scenario.cases).map(([caseName, contract]) => ({ scenario, caseName, contract })));
  const resolvedCases = registry.flatMap((scenario) => caseNamesFor(scenario).map((caseName) => ({
    scenario,
    caseName,
    owned: scenarioCaseFor(scenario, caseName),
    custom: scenarioCaseFor(scenario, caseName, true),
  })));
  assert.equal(resolvedCases.length, registeredCases.length);
  assert.deepEqual(resolvedCases.map(({ scenario, caseName }) => `${scenario.id}/${caseName}`), registeredCases.map(({ scenario, caseName }) => `${scenario.id}/${caseName}`));
  const rawCheckEntries = registeredCases.reduce((total, { contract }) => {
    const checks = contract.requiredChecks;
    if (Array.isArray(checks)) return total + checks.length;
    const customChecks = checks.custom ?? checks.owned;
    return total + checks.owned.length + (JSON.stringify(customChecks) === JSON.stringify(checks.owned) ? 0 : customChecks.length);
  }, 0);
  const resolvedCheckEntries = resolvedCases.reduce((total, { owned, custom }) => total + owned.requiredChecks.length + (
    JSON.stringify(custom.requiredChecks) === JSON.stringify(owned.requiredChecks) ? 0 : custom.requiredChecks.length
  ), 0);
  assert.equal(resolvedCheckEntries, rawCheckEntries);
  for (const { scenario, caseName, contract } of registeredCases) {
    const checks = contract.requiredChecks;
    const ownedChecks = Array.isArray(checks) ? checks : checks.owned;
    const customChecks = Array.isArray(checks) ? checks : (checks.custom ?? checks.owned);
    assert.ok(contract.playwrightTest, `${scenario.id}/${caseName} has a native Playwright mapping`);
    assert.deepEqual(scenarioCaseFor(scenario, caseName).requiredChecks, ownedChecks);
    assert.deepEqual(scenarioCaseFor(scenario, caseName, true).requiredChecks, customChecks);
    assert.deepEqual(lifecycleEvidenceContractIssues(contract), [],
      `${scenario.id}/${caseName} has valid declared lifecycle evidence`);
  }
  assert.throws(() => scenarioCaseFor('builder-load', 'unknown'), /unknown case for builder-load: unknown/);
  assert.throws(() => scenarioCaseFor('unknown-scenario', 'case'), /unknown scenario: unknown-scenario/);
});

test('row-operation coverage distinguishes lifecycle acceptance from a runnable probe', () => {
  assert.deepEqual(coverageDrift(registry), [], 'the checked-in registry declares valid lifecycle references');
  const directGroup = registry.find((scenario) => scenario.id === 'builder-authoring')
    .coverage.find((coverage) => coverage.feature === 'direct empty-key COUNT_ROWS GROUP automatic entry Preview');
  const unpivotFeature = registry.find((scenario) => scenario.id === 'builder-authoring')
    .coverage.find((coverage) => coverage.feature === 'Unpivot');
  const repeatedExpand = registry.find((scenario) => scenario.id === 'builder-authoring')
    .coverage.find((coverage) => coverage.feature.startsWith('Observation.component literal-empty'));
  assert.equal(directGroup.acceptance.kind, 'probe');
  assert.equal(requiresLifecycleAcceptance(directGroup), true);
  assert.equal(hasLifecycleContract(directGroup), false,
    'an implemented probe remains visible but cannot close the Group lifecycle gap');
  assert.equal(repeatedExpand.acceptance.kind, 'lifecycle');
  assert.equal(hasLifecycleContract(repeatedExpand), true);
  assert.equal(unpivotFeature.status, 'implemented', 'The accepted historical lifecycle pass is represented in the registry');
  assert.equal(unpivotFeature.acceptance.kind, 'lifecycle');
  assert.equal(unpivotFeature.acceptance.scenario, 'standalone-reshape-related-unpivot');
  assert.equal(unpivotFeature.acceptance.case, 'related-unpivot');
  assert.deepEqual(unpivotFeature.acceptance.checks,
    { choice: 3, proposal: 4, cancel: 5, apply: 6, savedRows: 7, reload: 8, edit: 9, restoration: 12 });
  const unpivotScenario = registry.find((scenario) => scenario.id === 'standalone-reshape-related-unpivot');
  const unpivotChecks = scenarioCaseFor(unpivotScenario, 'related-unpivot').requiredChecks;
  assert.equal(unpivotChecks.length, 17);
  assert.match(unpivotChecks[0], /two-to-twenty-four-row/);
  assert.match(unpivotChecks[4], /exact transformed raw multiset/);
  assert.match(unpivotChecks[6], /exact source-column and compiler-stage binding/);
  assert.match(unpivotChecks[12], /restores exact Related construction, schema, bindings, and rows/);
  assert.equal(hasLifecycleContract(unpivotFeature), true,
    'The accepted historical Related→Unpivot lifecycle retains its exact registered phase contract');

  const authoring = registry.find((scenario) => scenario.id === 'builder-authoring');
  for (const feature of [
    'authored list EXPAND from a named-cohort ALL member field',
    'raw ONE disagreement rejection for two Patient IDs',
    'grouping rows',
    'related-record rows',
    'direct related Observation.status chooser ONE→ALL repair',
    'repeated-value rows',
    'coded Pivot',
    'Unpivot',
    'Filter rows',
    'direct columns',
    'coded columns',
    'related columns',
    'ONE/ALL contributing values',
    'contributor rules',
    'missing-match policies',
  ]) {
    assert.equal(requiresLifecycleAcceptance(authoring.coverage.find((coverage) => coverage.feature === feature)), true,
      `${feature} has explicit lifecycle intent even without a keyword used by the gate`);
  }
  assert.deepEqual(coverageDrift([{
    id: 'unclassified-row',
    cases: {},
    coverage: [{ feature: 'a feature label with no operation keyword', status: 'implemented', acceptance: { intent: 'row-lifecycle' } }],
  }]).filter((message) => message.includes('must be classified')).length, 1,
  'once a coverage row declares row-lifecycle intent, its implemented status requires probe or lifecycle classification');
  assert.match(coverageDrift([{
    id: 'unclassified-row',
    cases: {},
    coverage: [{ feature: 'a feature label with no operation keyword', status: 'implemented', acceptance: { intent: 'row-lifecycle' } }],
  }]).join('\n'), /acceptance\.case must name a registered native case/,
  'classification and native-case mapping are independent requirements');

  const duplicateCheckScenarios = ['row-alpha', 'row-beta'].map((id) => ({
    id,
    cases: { complete: { playwrightTest: 'row-operation.spec.mjs', requiredChecks: ['choice', 'proposal', 'cancel', 'apply', 'saved rows', 'reload', 'edit', 'restoration'] } },
    coverage: [{ feature: id, status: 'implemented', acceptance: { intent: 'row-lifecycle', kind: 'lifecycle', case: 'complete',
      checks: { choice: 0, proposal: 1, apply: 3, savedRows: 4, reload: 5, edit: 6, restoration: 7 } } }],
  }));
  const duplicateCheckDrift = coverageDrift(duplicateCheckScenarios);
  assert.equal(duplicateCheckDrift.length, 2,
    'each malformed lifecycle row is reported once regardless of the number of registered scenarios');
  assert.deepEqual(new Set(duplicateCheckDrift).size, 2,
    'the lifecycle errors are unique rather than duplicated once per scenario');
});

test('lifecycle phase references point to the named applied, edited, reloaded, and restored evidence', () => {
  const mappedText = (scenarioId, feature, phase) => {
    const owner = registry.find((entry) => entry.id === scenarioId);
    const row = owner.coverage.find((entry) => entry.feature === feature);
    assert.ok(row, `${scenarioId} has the exact feature row`);
    const scenario = registry.find((entry) => entry.id === (row.acceptance.scenario ?? owner.id));
    const contract = scenarioCaseFor(scenario, row.acceptance.case);
    return { index: row.acceptance.checks[phase], text: contract.requiredChecks[row.acceptance.checks[phase]] };
  };

  assert.equal(mappedText('builder-authoring', 'Observation.component literal-empty and missing-list policies with saved Expand lifecycle', 'edit').index, 48);
  assert.match(mappedText('builder-authoring', 'Observation.component literal-empty and missing-list policies with saved Expand lifecycle', 'savedRows').text, /EXCLUDE source rows retain/);
  assert.match(mappedText('builder-authoring', 'Observation.component literal-empty and missing-list policies with saved Expand lifecycle', 'reload').text, /EXCLUDE source EXPANDED rows.*survive Builder reload/);
  assert.equal(mappedText('builder-authoring', 'ordinary Pivot', 'choice').index, 1);
  assert.match(mappedText('builder-authoring', 'ordinary Pivot', 'choice').text, /offers visible SUM repair/);
  assert.equal(mappedText('builder-combine-draft', 'KEY_JOIN over two independently authored unpublished Group outputs', 'savedRows').index, 11);
  assert.match(mappedText('builder-combine-draft', 'KEY_JOIN over two independently authored unpublished Group outputs', 'savedRows').text, /applied LEFT Join rows survive reload/);
  const publishedJoinFeature = 'published-table KEY_JOIN basic lifecycle';
  const publishedJoinPhases = {
    choice: [10, /native Combine inputs pin the exact current Observation and DiagnosticReport revisions/],
    proposal: [17, /INNER preview shows literal human headers and the three exact matched rows/],
    cancel: [20, /Canceling the LEFT edit leaves the saved INNER operation unchanged/],
    apply: [18, /INNER Apply preserves the exact joined rows/],
    savedRows: [18, /INNER Apply preserves the exact joined rows/],
    reload: [19, /INNER table reload retains the three exact rows/],
    edit: [23, /LEFT Apply preserves exact matches and unmatched null fields/],
    restoration: [26, /removing KEY_JOIN and reloading restores the rooted empty target/],
  };
  for (const [phase, [index, expectedText]] of Object.entries(publishedJoinPhases)) {
    const mapped = mappedText('builder-combine', publishedJoinFeature, phase);
    assert.equal(mapped.index, index, `published Join ${phase} phase points to its registered evidence`);
    assert.match(mapped.text, expectedText, `published Join ${phase} phase resolves to its named lifecycle assertion`);
  }
  assert.equal(mappedText('builder-combine-draft', 'MEMBERSHIP over two unpublished grouped ID sources with INCLUDE, EXCLUDE edit, removal, restoration, and reload', 'edit').index, 41);
  assert.equal(mappedText('builder-combine-draft', 'MEMBERSHIP over two unpublished grouped ID sources with INCLUDE, EXCLUDE edit, removal, restoration, and reload', 'savedRows').index, 43);
  assert.match(mappedText('builder-combine-draft', 'MEMBERSHIP over two unpublished grouped ID sources with INCLUDE, EXCLUDE edit, removal, restoration, and reload', 'savedRows').text, /EXCLUDE values survive reload/);
  assert.equal(mappedText('cda-current-draft-membership', 'real-CDA MEMBERSHIP over two exact unpublished grouped Observation ID populations with INCLUDE, EXCLUDE edit, cancellation, removal, restoration, and reload', 'edit').index, 36);
  assert.equal(mappedText('cda-current-draft-membership', 'real-CDA MEMBERSHIP over two exact unpublished grouped Observation ID populations with INCLUDE, EXCLUDE edit, cancellation, removal, restoration, and reload', 'savedRows').index, 37);
  assert.equal(mappedText('cda-current-draft-membership', 'real-CDA MEMBERSHIP over two exact unpublished grouped Observation ID populations with INCLUDE, EXCLUDE edit, cancellation, removal, restoration, and reload', 'restoration').index, 43);
  assert.match(mappedText('cda-current-draft-membership', 'real-CDA MEMBERSHIP over two exact unpublished grouped Observation ID populations with INCLUDE, EXCLUDE edit, cancellation, removal, restoration, and reload', 'restoration').text, /removal and reloading restores the exact rooted empty target/);

  const fiveHopScenario = registry.find((entry) => entry.id === 'cda-five-hop-related-expansion');
  const fiveHopCoverage = fiveHopScenario.coverage.find((entry) => entry.feature.startsWith('zero-column five-hop Specimen-to-Medication'));
  assert.equal(fiveHopCoverage.acceptance.kind, 'lifecycle');
  assert.deepEqual(fiveHopCoverage.acceptance.checks, { choice: 2, proposal: 3, cancel: 4, apply: 5, savedRows: 5, reload: 6, edit: 7, restoration: 8 });
  const fiveHopChecks = scenarioCaseFor(fiveHopScenario, fiveHopCoverage.acceptance.case).requiredChecks;
  assert.equal(fiveHopChecks.length, 11);
  assert.match(fiveHopChecks[2], /native RelatedExpand editor selects/);
  assert.match(fiveHopChecks[3], /automatic proposal renders exact terminal Medication IDs/);
  assert.match(fiveHopChecks[4], /Cancel leaves/);
  assert.match(fiveHopChecks[5], /Apply saves.*actual Medication table preview rows/);
  assert.match(fiveHopChecks[6], /reload restores/);
  assert.match(fiveHopChecks[7], /editing to EXCLUDE/);
  assert.match(fiveHopChecks[8], /removing RelatedExpand restores/);
  assert.equal(hasLifecycleContract(fiveHopCoverage, fiveHopScenario), true,
    'named lifecycle completeness does not change its untested runtime status');
  const filterOwner = registry.find((entry) => entry.id === 'builder-authoring');
  const filterCoverage = filterOwner.coverage.find((entry) => entry.feature === 'Filter rows');
  const filterScenario = registry.find((entry) => entry.id === 'cda-filter-browser');
  assert.equal(filterCoverage.status, 'implemented', 'the current native lifecycle pass closes the registered Filter rows coverage');
  assert.equal(filterCoverage.acceptance.kind, 'lifecycle');
  assert.deepEqual(filterCoverage.acceptance.checks,
    { choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, edit: 6, restoration: 7 });
  assert.equal(hasLifecycleContract(filterCoverage, filterOwner), true,
    'Filter rows must link all lifecycle phases to the registered native CDA case');
  assert.deepEqual(scenarioCaseFor(filterScenario, 'filter-lifecycle').requiredChecks, [
    'native Filter rows controls expose an enabled source column and typed condition',
    'filter proposals and rendered result values match an independent scoped CDA source oracle within five seconds',
    'Cancel preserves the exact pre-proposal construction and rendered rows within five seconds',
    'Apply persists the filter construction and exact result rows within five seconds',
    'saved filter rows match the independent scoped CDA oracle',
    'reload restores the saved filter and exact rendered rows',
    'edit reopens the exact saved column and condition before applying a replacement within five seconds',
    'filter removal restores the exact source columns, population, and rows after reload',
    'All native Filter actions and action-to-render checkpoints complete within five seconds',
  ]);
});

test('post-Pivot COUNT coverage records historical derived lifecycle evidence without claiming freshness', () => {
  const scenario = registry.find((entry) => entry.id === 'standalone-reshape-related-source-after-pivot');
  const coverage = scenario.coverage.find((entry) => entry.feature.includes('Patient.id COUNT'));
  assert.equal(coverage.status, 'implemented');
  assert.equal(coverage.acceptance.case, 'related-source-count-after-pivot');
  assert.match(coverage.reason, /Historical qzzOck COUNT lifecycle passed 25\/25 registered checks/);
  assert.match(coverage.reason, /26 action-to-render checkpoints/);
  assert.match(coverage.reason, /qzzOck-count-lifecycle-evidence\.json/);
  assert.match(coverage.reason, /historical freshness/);
  assert.match(coverage.reason, /no current-source browser pass is claimed/);
});

test('Filter rows registry requires the full lifecycle and native performance check', () => {
  const owner = registry.find((entry) => entry.id === 'builder-authoring');
  const coverage = owner.coverage.find((entry) => entry.feature === 'Filter rows');
  const scenario = registry.find((entry) => entry.id === 'cda-filter-browser');
  const requiredChecks = scenarioCaseFor(scenario, 'filter-lifecycle').requiredChecks;

  assert.equal(coverage.status, 'implemented');
  assert.match(coverage.reason, /Current native source-table Filter lifecycle passed 9\/9 registered checks/);
  assert.deepEqual(coverage.acceptance.checks,
    { choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, edit: 6, restoration: 7 });
  assert.equal(requiredChecks.length, 9);
  assert.equal(requiredChecks[8], 'All native Filter actions and action-to-render checkpoints complete within five seconds');
  assert.equal(hasLifecycleContract(coverage, owner), true);
});

test('row-operation gate rejects missing lifecycle links and invalid named-check references', () => {
  const caseContract = {
    playwrightTest: 'row-operation.spec.mjs',
    requiredChecks: ['native choice', 'proposal preview', 'Cancel', 'Apply rows', 'saved rows', 'reload rows', 'edit saved operation', 'remove and restore'],
  };
  const scenario = { id: 'row-test', cases: { probe: { ...caseContract, acceptance: { kind: 'probe' } } }, coverage: [] };
  const implemented = (acceptance) => ({
    feature: 'native GROUP row lifecycle', status: 'implemented', acceptance: { intent: 'row-lifecycle', ...acceptance },
  });
  assert.match(coverageDrift([{ ...scenario, coverage: [implemented({ kind: undefined, case: 'probe' })] }]).join('\n'), /must be classified/);
  assert.match(coverageDrift([{ ...scenario, coverage: [implemented({ kind: 'lifecycle', case: 'probe', checks: {
    choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 99, edit: 6, restoration: 7,
  } })] }]).join('\n'), /reload/);
  assert.match(coverageDrift([{ ...scenario, coverage: [implemented({ kind: 'lifecycle', case: 'probe', checks: {
    choice: 0, proposal: 0, cancel: 0, apply: 0, savedRows: 0, reload: 0, edit: 0, restoration: 0,
  } })] }]).join('\n'), /every lifecycle phase points to one check/);

  const lifecycleScenario = { id: 'row-test', cases: { complete: caseContract }, coverage: [implemented({
    kind: 'lifecycle', case: 'complete', checks: {
      choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, edit: 6, restoration: 7,
    },
  })] };
  assert.deepEqual(coverageDrift([lifecycleScenario]), []);
  assert.equal(hasLifecycleContract(lifecycleScenario.coverage[0], lifecycleScenario, [lifecycleScenario]), true);

  const outOfRange = implemented({ kind: 'lifecycle', case: 'complete', checks: {
    choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, edit: 6, restoration: 8,
  } });
  assert.equal(hasLifecycleContract(outOfRange, lifecycleScenario, [lifecycleScenario]), false,
    'the helper itself rejects a check index outside the case contract');
  const unknownCase = implemented({ kind: 'lifecycle', case: 'missing', checks: {
    choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, edit: 6, restoration: 7,
  } });
  assert.equal(hasLifecycleContract(unknownCase, lifecycleScenario, [lifecycleScenario]), false,
    'the helper itself rejects an unregistered acceptance case');
  const malformedNotApplicable = implemented({ kind: 'lifecycle', case: 'complete', checks: {
    choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, restoration: 7,
  }, notApplicable: { edit: '' , invented: 'not a lifecycle phase' } });
  assert.equal(hasLifecycleContract(malformedNotApplicable, lifecycleScenario, [lifecycleScenario]), false,
    'the helper itself rejects blank and unknown N/A declarations');

  const explicitGap = implemented({ kind: 'lifecycle', case: 'complete', checks: {
    choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, edit: 6,
  }, contractGaps: { restoration: 'The report proves restoration, but the named requiredChecks contract omits it.' } });
  assert.deepEqual(coverageDrift([{ ...lifecycleScenario, coverage: [explicitGap] }]), [],
    'a documented registry-contract gap is distinct from a malformed phase reference');
  assert.equal(hasLifecycleContract(explicitGap, lifecycleScenario, [lifecycleScenario]), false,
    'a report-only restoration assertion cannot make the registered lifecycle contract complete');
  assert.match(coverageDrift([{ ...lifecycleScenario, coverage: [implemented({
    kind: 'lifecycle', case: 'complete', checks: { choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, edit: 6 },
  })] }]).join('\n'), /restoration/,
  'an unexplained missing phase still fails the registry gate');

  const editNotApplicable = implemented({ kind: 'lifecycle', case: 'complete', checks: {
    choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, restoration: 7,
  }, notApplicable: { edit: 'This case removes a row operation and contains no saved operation that can be edited.' } });
  const notApplicableScenario = { ...lifecycleScenario, coverage: [editNotApplicable] };
  assert.deepEqual(coverageDrift([notApplicableScenario]), [],
    'a genuine N/A phase has its own explicit reason and is not a registry contract gap');
  assert.equal(hasLifecycleContract(editNotApplicable, notApplicableScenario, [notApplicableScenario]), true,
    'an explicit N/A phase may coexist with a complete lifecycle contract');
  assert.equal(hasLifecycleContract(explicitGap, lifecycleScenario, [lifecycleScenario]), false,
    'a contract gap remains uncovered even if other phases have an explicit N/A reason');
  assert.match(coverageDrift([{ ...lifecycleScenario, coverage: [implemented({
    kind: 'lifecycle', case: 'complete', checks: { choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, edit: 6 },
    notApplicable: { restoration: 'The report proves restoration, but the named contract does not.' },
    contractGaps: { restoration: 'The named requiredChecks list has no restoration check.' },
  })] }]).join('\n'), /contract gaps/,
  'a phase cannot be both not applicable and an uncovered registry contract gap');
});

test('explicitly unmapped row-lifecycle entries stay visible and never count as a lifecycle pass', () => {
  const unmapped = {
    id: 'row-test',
    cases: {},
    coverage: [{
      feature: 'coded source column lifecycle',
      status: 'untested',
      acceptance: { intent: 'row-lifecycle', kind: 'unmapped', unmappedReason: 'No registered case saves this coded source column.' },
    }],
  };
  const row = unmapped.coverage[0];
  assert.deepEqual(coverageDrift([unmapped]), []);
  assert.equal(row.status, 'untested');
  assert.equal(hasLifecycleContract(row, unmapped, [unmapped]), false);
  assert.deepEqual(unmappedLifecycleCoverage([unmapped]), {
    count: 1,
    rows: [{ scenario: 'row-test', feature: 'coded source column lifecycle', reason: 'No registered case saves this coded source column.' }],
  });

  for (const status of ['implemented', 'failed']) {
    assert.match(coverageDrift([{ ...unmapped, coverage: [{ ...row, status }] }]).join('\n'), /must remain untested/);
  }
  assert.match(coverageDrift([{ ...unmapped, coverage: [{ ...row, acceptance: { intent: 'row-lifecycle', kind: 'unmapped', unmappedReason: '  ' } }] }]).join('\n'), /nonempty unmappedReason/);
  assert.match(coverageDrift([{ ...unmapped, coverage: [{ ...row, acceptance: {
    ...row.acceptance, scenario: 'row-test', case: 'missing', checks: { choice: 0 },
  } }] }]).join('\n'), /cannot include scenario, case, or phase mappings/);
});

test('builder-authoring rows map only registered cases or remain explicit unresolved gaps', () => {
  const owner = registry.find((entry) => entry.id === 'builder-authoring');
  const expectedProbes = new Map([
    ['grouping rows', ['standalone-reshape-group-one-conflict', 'group-one-conflict', /Group by Patient preview matches the exact independent raw rows/]],
    ['related-record rows', ['standalone-reshape-related-unpivot', 'related-unpivot', /native Related choices preserve exact raw rows/]],
    ['ONE/ALL contributing values', ['cda-related-one-all-specimen-reference', 'cda-specimen-reference-raw-oracle-one-all-lifecycle', /related ONE\/ALL lifecycle preserves exact raw-source values/]],
    ['missing-match policies', ['cda-contributor-exists', 'contributor-exists', /Scoped ERROR validation exposes enabled PRESERVE_PARENT and EXCLUDE repair choices/]],
  ]);
  const expectedLifecycles = new Map([
    ['authored list EXPAND from a named-cohort ALL member field', ['builder-authoring', 'cohort-expand']],
    ['direct columns', ['standalone-reshape-group-add-fields', 'group-add-fields']],
    ['related columns', ['standalone-reshape-related-source-after-pivot', 'related-source-after-pivot']],
  ]);
  for (const [feature, [scenarioId, caseName, checkPattern]] of expectedProbes) {
    const row = owner.coverage.find((coverage) => coverage.feature === feature);
    assert.equal(row.status, 'untested', `${feature} remains untested at the current evidence freshness`);
    assert.equal(row.acceptance.kind, 'probe');
    const scenario = registry.find((entry) => entry.id === scenarioId);
    const contract = scenarioCaseFor(scenario, caseName);
    assert.match(contract.requiredChecks.join('\n'), checkPattern, `${feature} points to a check that proves its narrow scope`);
  }
  for (const [feature, [scenarioId, caseName]] of expectedLifecycles) {
    const row = owner.coverage.find((coverage) => coverage.feature === feature);
    assert.equal(row.status, 'untested', `${feature} remains untested at the current evidence freshness`);
    assert.equal(row.acceptance.kind, 'lifecycle');
    assert.equal(row.acceptance.case, caseName);
    if (scenarioId !== 'builder-authoring') assert.equal(row.acceptance.scenario, scenarioId);
    const scenario = registry.find((entry) => entry.id === scenarioId);
    assert.ok(hasLifecycleContract(row, owner, registry), `${feature} resolves to a complete named lifecycle contract`);
    assert.ok(scenarioCaseFor(scenario, caseName).requiredChecks.length > 1);
  }
  const explicitGaps = unmappedLifecycleCoverage(registry);
  assert.equal(explicitGaps.count, 1);
  assert.deepEqual(new Set(explicitGaps.rows.map((row) => row.feature)), new Set([
    'coded Pivot',
  ]));
  for (const row of owner.coverage.filter((coverage) => coverage.acceptance?.kind === 'unmapped')) {
    assert.equal(row.status, 'untested');
    assert.equal(hasLifecycleContract(row, owner, registry), false);
    assert.ok(row.acceptance.unmappedReason.trim());
  }
});

test('untested row-lifecycle coverage still requires a registered acceptance scenario and case', () => {
  const caseContract = {
    playwrightTest: 'row-operation.spec.mjs',
    requiredChecks: ['choice', 'proposal', 'Cancel', 'Apply', 'saved rows', 'reload', 'edit', 'restore'],
  };
  const missingCase = {
    id: 'row-test',
    cases: { complete: caseContract },
    coverage: [{
      feature: 'starting-collection member removal lifecycle',
      status: 'untested',
      acceptance: { intent: 'row-lifecycle', kind: 'lifecycle', scenario: 'row-test' },
    }],
  };
  assert.deepEqual(coverageDrift([missingCase]), [
    'row-test: starting-collection member removal lifecycle: acceptance.case must name a registered native case for scenario row-test',
  ]);

  const unknownScenario = {
    ...missingCase,
    coverage: [{
      ...missingCase.coverage[0],
      acceptance: { intent: 'row-lifecycle', kind: 'probe', scenario: 'missing-scenario', case: 'complete' },
    }],
  };
  assert.deepEqual(coverageDrift([unknownScenario]), [
    'row-test: starting-collection member removal lifecycle: acceptance.scenario must reference a registered scenario',
  ]);

  const mappedUntested = {
    ...missingCase,
    coverage: [{
      ...missingCase.coverage[0],
      acceptance: {
        intent: 'row-lifecycle', kind: 'lifecycle', case: 'complete',
        checks: { choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, edit: 6, restoration: 7 },
      },
    }],
  };
  assert.deepEqual(coverageDrift([mappedUntested]), [],
    'a valid acceptance mapping does not change the row status from untested');
});

test('partial long-route collection repair owns one registered case while legacy variants stay unregistered', () => {
  const scenario = registry.find((entry) => entry.id === 'cda-collection-repair-partial');
  assert.ok(scenario, 'the exact partial long-route variant has a registry contract');
  const contract = scenarioCaseFor(scenario, 'partial-long-route-repair-and-reload');
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-other.spec.mjs');
  const preApplyCancelCheck = 'Cancel before Apply preserves the exact source workspace, selected membership, route, output bindings, and scoped raw preview before the same FILTER EXISTS operation and preview are applied';
  assert.ok(contract.requiredChecks.includes(preApplyCancelCheck));
  assert.equal(new Set(contract.requiredChecks).size, contract.requiredChecks.length);
  assert.equal(classifyEvidence({
    schemaVersion: 2,
    status: 'passed',
    assertions: contract.requiredChecks
      .filter((name) => name !== preApplyCancelCheck)
      .map((name) => ({ name, status: 'passed' })),
  }, contract.requiredChecks), 'partial', 'the added Cancel transition is required to classify the registered case as passed');
  assert.equal(registry.some((entry) => entry.id === 'cda-collection-repair'), false,
    'legacy default and long-route reports retain their previously unregistered scenario identity');
  assert.throws(() => scenarioCaseFor(scenario, 'long-route-repair-and-reload'), /unknown case/);
  assert.throws(() => scenarioCaseFor(scenario, 'unmapped-parent-repair-and-reload'), /unknown case/);
});

test('a scenario pass with untested dimensions is only partial evidence', () => {
  assert.equal(classifyEvidence({ status: 'passed', dimensions: { ...complete, persistence: { status: 'untested' } } }), 'partial');
  assert.equal(classifyEvidence({ status: 'passed', dimensions: complete }), 'passed');
});

test('current reports use passing named requirements while keeping optional dimension gaps out of case status', () => {
  assert.equal(classifyEvidence({
    schemaVersion: 2,
    status: 'passed',
    assertions: [{ name: 'required transition', status: 'passed' }],
    dimensions: { ...complete, persistence: { status: 'untested' } },
  }, ['required transition']), 'passed');
});

test('malformed required performance timing prevents a full classification without a lifecycle timing contract', () => {
  const legacyPerformanceCheck = 'registered native render timing';
  const legacyContract = {
    requiredChecks: [legacyPerformanceCheck],
    performanceCheckName: legacyPerformanceCheck,
  };
  const legacyReport = (evidence) => ({
    schemaVersion: 2,
    status: 'passed',
    dimensions: complete,
    assertions: [{ name: legacyPerformanceCheck, status: 'passed', evidence }],
  });

  assert.equal(classifyEvidence(legacyReport({ checkpoints: [
    { name: 'one-member Patient attachment Replace click through exact APPEND grid', durationMs: 1219.387416999998, budgetMs: 5000, withinBudget: true },
    { name: 'restored two-member Patient attachment Replace click through exact APPEND grid', durationMs: 1213.6332089999996, budgetMs: 5000, withinBudget: true },
  ] }), legacyContract.requiredChecks, legacyContract), 'passed',
  'a valid Patient-style generic checkpoint list remains accepted as a performance check');

  const pivotPerformanceCheck = 'approved full-population Pivot action-to-render checkpoint';
  const pivotContract = {
    requiredChecks: [pivotPerformanceCheck],
    lifecycleEvidence: { performance: { check: pivotPerformanceCheck, checkpointBudgetMs: 10_000 } },
  };
  assert.equal(classifyEvidence({
    schemaVersion: 2,
    status: 'passed',
    dimensions: complete,
    assertions: [{
      name: pivotPerformanceCheck,
      status: 'passed',
      dimension: 'performance',
      evidence: {
        actionCount: 1,
        measuredTransitionCount: 1,
        maxActionMs: 500,
        checkpoints: [{ name: 'full-population-pivot-discovery-to-render', durationMs: 9500, budgetMs: 10_000, withinBudget: true }],
      },
    }],
  }, pivotContract.requiredChecks, pivotContract), 'passed',
  'the approved 10-second Pivot checkpoint remains within its explicit and registered budget');

  assert.equal(classifyEvidence(legacyReport({ checkpoints: [
    { name: 'Patient render with malformed duration', durationMs: Number.NaN, budgetMs: 5000, withinBudget: true },
  ] }), legacyContract.requiredChecks, legacyContract), 'partial',
  'a malformed declared performance checkpoint is not promoted to a full lifecycle pass');

  const dimensionedPerformanceCheck = 'required performance assertion';
  const dimensionedPerformanceContract = { requiredChecks: [dimensionedPerformanceCheck] };
  assert.equal(classifyEvidence({
    schemaVersion: 2,
    status: 'passed',
    dimensions: complete,
    assertions: [{
      name: dimensionedPerformanceCheck,
      status: 'passed',
      dimension: 'performance',
      evidence: { elapsedMs: -1, budgetMs: 5000, withinBudget: false },
    }],
  }, dimensionedPerformanceContract.requiredChecks, dimensionedPerformanceContract), 'partial',
  'a malformed duration on a required performance assertion is incomplete evidence');

  const explicitBudgetCheck = 'performance assertion with a tighter local budget';
  const explicitBudgetContract = {
    requiredChecks: [explicitBudgetCheck],
    lifecycleEvidence: { performance: { check: explicitBudgetCheck, checkpointBudgetMs: 5000 } },
  };
  assert.notEqual(classifyEvidence({
    schemaVersion: 2,
    status: 'passed',
    dimensions: complete,
    assertions: [{
      name: explicitBudgetCheck,
      status: 'passed',
      dimension: 'performance',
      evidence: {
        actionCount: 1,
        measuredTransitionCount: 1,
        maxActionMs: 500,
        checkpoints: [{ name: 'locally-budgeted transition', durationMs: 1000, budgetMs: 500, withinBudget: false }],
      },
    }],
  }, explicitBudgetContract.requiredChecks, explicitBudgetContract), 'passed',
  'a recorded withinBudget:false and tighter explicit budget cannot pass under the larger registered budget');

  const correctnessCheck = 'required correctness assertion';
  assert.equal(classifyEvidence({
    schemaVersion: 2,
    status: 'passed',
    dimensions: complete,
    assertions: [{
      name: correctnessCheck,
      status: 'passed',
      dimension: 'correctness',
      evidence: { checkpoints: [{ name: 'unrelated value', durationMs: Number.NaN }] },
    }],
  }, [correctnessCheck], { requiredChecks: [correctnessCheck] }), 'passed',
  'generic checkpoint-shaped correctness evidence is not interpreted as render timing');
});

test('declared lifecycle dimensions require exact persistence checks and complete timing evidence', () => {
  const base = postPivotCountReport();
  assert.equal(retainedPostPivotCountTimings.sourceSummarySha256,
    '19262bd541a31ae09a0134c9b02e04476ff8b04121edfde59253536deffb309c');
  assert.equal(retainedPostPivotCountTimings.sourceReportSha256,
    '5587cd186fbb00f1c8bd34470e9799522d8c7974c8faa6deb4603b6bd553b829');
  assert.equal(retainedPostPivotCountTimings.actionCount, 60);
  assert.equal(retainedPostPivotCountTimings.maxActionMs, 225);
  assert.equal(Math.max(...retainedPostPivotCountTimings.cases.map(({ elapsedMs }) => elapsedMs)), 1417);
  assert.equal(classifyEvidence(base, postPivotCountContract.requiredChecks, postPivotCountContract), 'passed');

  const missingPersistence = structuredClone(base);
  missingPersistence.assertions = missingPersistence.assertions.filter((assertion) =>
    assertion.name !== postPivotCountContract.lifecycleEvidence.persistence.checks[0]);
  assert.equal(classifyEvidence(missingPersistence, postPivotCountContract.requiredChecks, postPivotCountContract), 'partial');

  const missingTiming = structuredClone(base);
  delete missingTiming.assertions.find((assertion) =>
    assertion.name === postPivotCountContract.lifecycleEvidence.performance.check).evidence.workflowCheckpoints;
  assert.equal(classifyEvidence(missingTiming, postPivotCountContract.requiredChecks, postPivotCountContract), 'partial');

  const failedDimension = structuredClone(base);
  failedDimension.dimensions.performance.status = 'failed';
  assert.equal(classifyEvidence(failedDimension, postPivotCountContract.requiredChecks, postPivotCountContract), 'failed');
  const failedStringDimension = structuredClone(base);
  failedStringDimension.dimensions.performance = 'failed';
  assert.equal(classifyEvidence(failedStringDimension, postPivotCountContract.requiredChecks, postPivotCountContract), 'failed');

  const unknownDimension = structuredClone(base);
  unknownDimension.dimensions.persistence.status = 'unknown';
  unknownDimension.assertions = unknownDimension.assertions.filter((assertion) =>
    assertion.name !== postPivotCountContract.lifecycleEvidence.persistence.checks[1]);
  assert.equal(classifyEvidence(unknownDimension, postPivotCountContract.requiredChecks, postPivotCountContract), 'partial');

  const unexpectedFailure = structuredClone(base);
  unexpectedFailure.assertions.find((assertion) => assertion.name === 'No unexpected native network, module, or browser errors occurred').status = 'failed';
  assert.equal(classifyEvidence(unexpectedFailure, postPivotCountContract.requiredChecks, postPivotCountContract), 'failed');
});

test('latest case result controls coverage and missing cases remain untested', () => {
  const scenarios = [{ id: 'builder', cases: { load: { playwrightTest: 'builder.spec.mjs', requiredChecks: ['load'] }, edit: { playwrightTest: 'builder.spec.mjs', requiredChecks: ['edit'] } } }];
  const reports = [
    { path: 'old.json', report: { scenario: 'builder', case: 'load', finishedAt: '2026-01-01', status: 'passed', dimensions: complete } },
    { path: 'new.json', report: { scenario: 'builder', case: 'load', finishedAt: '2026-01-02', status: 'failed', dimensions: complete } },
  ];
  assert.deepEqual(summarizeCoverage(scenarios, reports).map(({ status, report }) => [status, report]), [['failed', 'new.json'], ['untested', null]]);
});

const groupScenario = registry.find((scenario) => scenario.id === 'builder-authoring');
assert(Object.hasOwn(groupScenario?.cases ?? {}, 'group-entry'), 'Expected the registered direct Group entry case.');
const groupEntryChecks = scenarioCaseFor(groupScenario, 'group-entry').requiredChecks;

const summarizeGroupEntry = (assertions) => summarizeCoverage([groupScenario], [{
  path: 'builder-authoring-group-entry.json',
  report: {
    schemaVersion: 2,
    scenario: 'builder-authoring',
    case: 'group-entry',
    finishedAt: '2026-10-04T00:00:00.000Z',
    status: 'passed',
    target: { kind: 'owned-dev-fixture' },
    requiredChecks: groupEntryChecks,
    assertions,
  },
}]).find((entry) => entry.path === 'builder-authoring/group-entry');

test('the real group-entry report shape passes only with every registered named assertion', () => {
  const assertions = groupEntryChecks.map((name) => ({ name, status: 'passed' }));
  assert.equal(summarizeGroupEntry(assertions)?.status, 'passed');
});

test('a current report missing a registered named assertion is partial despite its passed summary', () => {
  const assertions = groupEntryChecks.slice(0, -1).map((name) => ({ name, status: 'passed' }));
  assert.equal(summarizeGroupEntry(assertions)?.status, 'partial');
});

test('a current report with a failed registered named assertion cannot be classified as passed', () => {
  const assertions = groupEntryChecks.map((name, index) => ({ name, status: index === 0 ? 'failed' : 'passed' }));
  assert.equal(summarizeGroupEntry(assertions)?.status, 'failed');
});

test('currentness requires exact source and API build identities while preserving report status', () => {
  const sourceFingerprint = fingerprint();
  const report = {
    schemaVersion: 2,
    status: 'passed',
    target: { sourceFingerprint, apiBuildIdentity },
    assertions: [freezeAssertion(sourceFingerprint), { name: 'required transition', status: 'passed' }],
  };
  const freshness = classifyFreshness(report, { sourceFingerprint, apiBuildIdentity });
  assert.deepEqual(freshness, { status: 'current', source: 'current', build: 'current' });
  assert.equal(classifyEvidence(report, ['required transition']), 'passed');
});

test('mismatched source or API build identity is historical without changing pass status', () => {
  const sourceFingerprint = fingerprint();
  const report = {
    scenario: 'builder-load',
    case: 'list',
    finishedAt: '2026-10-01T12:00:00Z',
    schemaVersion: 2,
    status: 'passed',
    target: { sourceFingerprint, apiBuildIdentity },
    assertions: [
      ...scenarioCaseFor(registry.find((scenario) => scenario.id === 'builder-load'), 'list').requiredChecks
        .map((name) => ({ name, status: 'passed' })),
      freezeAssertion(sourceFingerprint),
    ],
  };
  const staleSource = classifyFreshness(report, { sourceFingerprint: fingerprint('b'.repeat(64)), apiBuildIdentity });
  const staleBuild = classifyFreshness(report, {
    sourceFingerprint,
    apiBuildIdentity: '4'.repeat(64) + ':' + '5'.repeat(64) + ':' + '6'.repeat(64),
  });
  assert.deepEqual(staleSource, { status: 'historical', source: 'historical', build: 'current' });
  assert.deepEqual(staleBuild, { status: 'historical', source: 'current', build: 'historical' });
  const scenario = registry.find((entry) => entry.id === report.scenario);
  const rows = summarizeCoverage([scenario], [{ path: 'pass.json', report }], { sourceFingerprint: fingerprint('b'.repeat(64)), apiBuildIdentity });
  assert.equal(rows[0].status, 'passed');
  assert.deepEqual(rows[0].freshness, { status: 'historical', source: 'historical', build: 'current' });
});

test('missing report identity or missing current baseline stays unknown, never current', () => {
  const sourceFingerprint = fingerprint();
  const report = {
    status: 'passed',
    target: { sourceFingerprint },
    assertions: [freezeAssertion(sourceFingerprint)],
  };
  assert.deepEqual(classifyFreshness(report, { sourceFingerprint, apiBuildIdentity }), {
    status: 'unknown', source: 'current', build: 'unknown',
  });
  assert.deepEqual(classifyFreshness({
    status: 'passed',
    target: { sourceFingerprint, apiBuildIdentity },
  }, { sourceFingerprint, apiBuildIdentity }), {
    status: 'unknown', source: 'unknown', build: 'current',
  });
  assert.deepEqual(classifyFreshness({
    ...report,
    apiBuildIdentity,
  }, { apiBuildIdentity }), {
    status: 'unknown', source: 'unknown', build: 'current',
  });
});

test('a source fingerprint that changed during the report is historical even when its start matched', () => {
  const sourceFingerprint = fingerprint();
  const changedFingerprint = fingerprint('b'.repeat(64));
  const freshness = classifyFreshness({
    status: 'passed',
    target: { sourceFingerprint, apiBuildIdentity },
    assertions: [freezeAssertion(sourceFingerprint, changedFingerprint, 'failed')],
  }, { sourceFingerprint, apiBuildIdentity });
  assert.deepEqual(freshness, { status: 'historical', source: 'historical', build: 'current' });
});

const writeCompactRun = (root, { epoch = 78, source = fingerprint(), build = apiBuildIdentity, status = 'passed', integritySource = source } = {}) => {
  const scenario = registry.find((entry) => entry.id === 'cda-current-draft-upstream-append');
  const required = scenarioCaseFor(scenario, 'upstream-append').requiredChecks;
  const reportDir = join(root, 'docs/verification/playwright/runtime');
  mkdirSync(reportDir, { recursive: true });
  const stem = `upstream-append-epoch${epoch}`;
  const reportPath = join(reportDir, `${stem}-report.json`);
  const closurePath = join(reportDir, `${stem}-closure.json`);
  const target = { project: 'loom_dev_cda_fhir', composeProject: 'loom-test-compose', generation: 'cda-fhir-v1', sourceRoot: root };
  const report = {
    epoch,
    scenario: scenario.id,
    case: 'upstream-append',
    title: 'durable compact report fixture',
    status,
    runnerStatus: status,
    coverageStatus: status,
    requiredChecks: { passed: required.length, failed: 0, total: required.length },
    assertions: { passed: 215, failed: 0, total: 215 },
    dimensions: { usability: 'passed', correctness: 'passed', persistence: 'passed', performance: 'passed' },
    network: { unexpectedNetworkErrors: 0, domainErrors: 0 },
    target,
    integrity: {
      closureStatus: 'PASS',
      sourceBeforeAfter: { ...integritySource, unchanged: true },
      apiBuildIdentityUnchanged: true,
      ownedMounts: { before: 'PASS', after: 'PASS', targetUnchanged: true },
      health: { before: { status: 'PASS', samples: 3 }, after: { status: 'PASS', samples: 3 } },
    },
    durableClosurePath: `docs/verification/playwright/runtime/${stem}-closure.json`,
  };
  const closure = {
    epoch,
    status: 'CLOSED_PASS',
    integrityClosure: {
      status: 'PASS',
      source: { before: source, after: source, manifestsEqual: true, changedPaths: [] },
      apiBuildIdentity: { before: build, after: build, precheck: build, unchanged: true },
      ownedMounts: { before: 'PASS', after: 'PASS', targetUnchanged: true, target },
      health: { before: { status: 'PASS', samples: 3 }, after: { status: 'PASS', samples: 3 } },
    },
    case: {
      scenarioId: scenario.id,
      caseName: 'upstream-append',
      status: 'passed',
      requiredChecks: {
        passed: required.length,
        total: required.length,
        missingOrFailed: 0,
        evidence: required.map((name) => ({ name, status: 'passed' })),
      },
    },
  };
  writeFileSync(reportPath, JSON.stringify(report));
  writeFileSync(closurePath, JSON.stringify(closure));
  return { reportPath, closurePath, report, closure, scenario, required };
};

test('durable compact report plus matching closure contributes current or historical evidence only against exact baselines', () => {
  const root = mkdtempSync(join(tmpdir(), 'coverage-compact-'));
  try {
    const source = fingerprint('c'.repeat(64), 1518);
    const fixture = writeCompactRun(root, { source });
    const reports = readReports(join(root, 'docs/verification/playwright/runtime'), { cwd: root });
    const loaded = reports.find((entry) => entry.path === fixture.reportPath);
    assert.ok(loaded?.closure, 'reader pairs the report with its repo-relative durable closure');
    const baseline = { sourceFingerprint: source, apiBuildIdentity };
    const summarize = (current) => summarizeCoverage([fixture.scenario], reports, current)[0];

    assert.deepEqual(
      (({ status, freshness }) => ({ status, freshness }))(summarize(baseline)),
      { status: 'passed', freshness: { status: 'current', source: 'current', build: 'current' } },
    );
    assert.deepEqual(summarize({ sourceFingerprint: fingerprint('d'.repeat(64), 1518), apiBuildIdentity }).freshness,
      { status: 'historical', source: 'historical', build: 'current' });
    assert.deepEqual(summarize({ sourceFingerprint: source, apiBuildIdentity: '4'.repeat(64) + ':' + '5'.repeat(64) + ':' + '6'.repeat(64) }).freshness,
      { status: 'historical', source: 'current', build: 'historical' });
    assert.deepEqual(summarize({ sourceFingerprint: source }).freshness,
      { status: 'unknown', source: 'current', build: 'unknown' });

    const mixedFormat = summarizeCoverage([fixture.scenario], [
      ...reports,
      {
        path: 'later-full-report.json',
        report: {
          scenario: fixture.scenario.id,
          case: 'upstream-append',
          finishedAt: '2026-10-06T12:00:00.000Z',
          schemaVersion: 2,
          status: 'failed',
          assertions: [],
        },
      },
    ], baseline)[0];
    assert.equal(mixedFormat.status, 'partial');
    assert.equal(mixedFormat.freshness.status, 'unknown', 'incomparable timestamp and epoch ordering cannot claim current coverage');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('compact browser fixture target stays separate from the owned stack closure target', () => {
  const root = mkdtempSync(join(tmpdir(), 'coverage-compact-fixture-target-'));
  try {
    const source = fingerprint('c'.repeat(64), 1518);
    const fixture = writeCompactRun(root, { source });
    fixture.report.ownedStackValidationTarget = { ...fixture.closure.integrityClosure.ownedMounts.target };
    fixture.report.target = {
      kind: 'basic-devloop-browser-fixture',
      project: 'loom_dev_verify_owned-case',
      generation: 'fixture-v1',
      cdaDatasetClaim: false,
    };
    fixture.report.integrity.targetRoleNote = 'The closure target validates the owned stack; target names the browser fixture.';
    writeFileSync(fixture.reportPath, JSON.stringify(fixture.report));
    const reports = readReports(join(root, 'docs/verification/playwright/runtime'), { cwd: root });
    const row = summarizeCoverage([fixture.scenario], reports, { sourceFingerprint: source, apiBuildIdentity })[0];
    assert.equal(row.status, 'passed');
    assert.deepEqual(row.freshness, { status: 'current', source: 'current', build: 'current' });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('a compact report with a mismatched closure is partial and never current', () => {
  const root = mkdtempSync(join(tmpdir(), 'coverage-compact-mismatch-'));
  try {
    const source = fingerprint('e'.repeat(64), 1518);
    const fixture = writeCompactRun(root, { source });
    fixture.closure.epoch += 1;
    writeFileSync(fixture.closurePath, JSON.stringify(fixture.closure));
    const reports = readReports(join(root, 'docs/verification/playwright/runtime'), { cwd: root });
    const row = summarizeCoverage([fixture.scenario], reports, { sourceFingerprint: source, apiBuildIdentity })[0];
    assert.equal(row.status, 'partial');
    assert.deepEqual(row.freshness, { status: 'unknown', source: 'unknown', build: 'unknown' });

    rmSync(fixture.closurePath);
    const missingClosureReports = readReports(join(root, 'docs/verification/playwright/runtime'), { cwd: root });
    const missingClosure = summarizeCoverage([fixture.scenario], missingClosureReports, { sourceFingerprint: source, apiBuildIdentity })[0];
    assert.equal(missingClosure.status, 'partial');
    assert.equal(missingClosure.freshness.status, 'unknown');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('compact reports cannot override contradictory integrity summaries or resolve a nonsibling closure', () => {
  const source = fingerprint('f'.repeat(64), 1518);
  const build = apiBuildIdentity;
  const rejectedRow = (fixture, root) => summarizeCoverage(
    [fixture.scenario],
    readReports(join(root, 'docs/verification/playwright/runtime'), { cwd: root }),
    { sourceFingerprint: source, apiBuildIdentity: build },
  )[0];
  const assertRejected = (mutate) => {
    const root = mkdtempSync(join(tmpdir(), 'coverage-compact-contradiction-'));
    try {
      const fixture = writeCompactRun(root, { source, build });
      mutate(fixture, root);
      const row = rejectedRow(fixture, root);
      assert.equal(row.status, 'partial');
      assert.deepEqual(row.freshness, { status: 'unknown', source: 'unknown', build: 'unknown' });
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  };

  assertRejected(({ report, reportPath }) => {
    report.target.composeProject = 'different-compose-project';
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ report, reportPath }) => {
    report.target.composeProject = '';
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ report, reportPath }) => {
    report.target.sourceRoot = '';
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ report, reportPath }) => {
    report.integrity.apiBuildIdentityUnchanged = true;
    report.integrity.apiBuildIdentity = {
      before: '0'.repeat(64) + ':' + '2'.repeat(64) + ':' + '3'.repeat(64),
      after: build,
      unchanged: true,
    };
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ report, reportPath }) => {
    report.integrity.sourceBeforeAfter.manifestsEqual = false;
    report.integrity.sourceBeforeAfter.changedPaths = ['internal/server/example.go'];
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ report, reportPath }) => {
    report.integrity.sourceBeforeAfter.changedPaths = { length: 0 };
    writeFileSync(reportPath, JSON.stringify(report));
  });
  assertRejected(({ closure, closurePath }) => {
    closure.integrityClosure.source.manifestsEqual = 'false';
    writeFileSync(closurePath, JSON.stringify(closure));
  });
  assertRejected(({ closure, closurePath }) => {
    closure.integrityClosure.source.changedPaths = { length: 0 };
    writeFileSync(closurePath, JSON.stringify(closure));
  });
  assertRejected(({ closure, closurePath }) => {
    closure.integrityClosure.apiBuildIdentity.unchanged = 'false';
    writeFileSync(closurePath, JSON.stringify(closure));
  });
  assertRejected(({ closure, closurePath }) => {
    closure.integrityClosure.ownedMounts.targetUnchanged = 'false';
    writeFileSync(closurePath, JSON.stringify(closure));
  });
  assertRejected(({ closurePath }, root) => {
    const alternateDirectory = join(root, 'alternate');
    mkdirSync(alternateDirectory);
    const redirected = join(alternateDirectory, basename(closurePath));
    renameSync(closurePath, redirected);
    symlinkSync(redirected, closurePath);
  });
});

const legacyRuntimePath = (root) => join(root, 'docs/verification/playwright/runtime');
const legacyPairs = {
  'current-draft-append-derived-edit-epoch74': {
    scenario: 'builder-combine-draft', caseName: 'append-derived-edit', epoch: 74, named: false,
  },
  'current-draft-cda-membership-epoch71': {
    scenario: 'cda-current-draft-membership', caseName: 'membership', epoch: 71, named: false,
  },
  'current-draft-group-pivot-epoch63': {
    scenario: 'builder-combine-draft', caseName: 'group-pivot', epoch: 63, named: true,
    closureScenario: 'builder-combine-draft/group-pivot-join',
  },
  'current-draft-membership-epoch66': {
    scenario: 'builder-combine-draft', caseName: 'membership', epoch: 66, named: true, evidenceLinked: true,
  },
  'current-draft-join-epoch41': {
    scenario: 'builder-combine-draft', caseName: 'join', named: true,
  },
};
const legacyFixtureSource = fingerprint('a'.repeat(64), 1511);
const legacyFixtureBuild = '1'.repeat(64) + ':' + '2'.repeat(64) + ':' + '3'.repeat(64);
const writeLegacyPair = (root, stem, mutateClosure = () => {}) => {
  const runtime = legacyRuntimePath(root);
  mkdirSync(runtime, { recursive: true });
  const reportName = `${stem}-report.json`;
  const closureName = `${stem}-closure.json`;
  const pair = legacyPairs[stem];
  assert.ok(pair, `unknown legacy fixture ${stem}`);
  const scenario = registry.find((entry) => entry.id === pair.scenario);
  const required = scenarioCaseFor(scenario, pair.caseName).requiredChecks;
  const reportPath = join(runtime, reportName);
  const closurePath = join(runtime, closureName);
  const evidencePath = pair.evidenceLinked ? join(runtime, 'evidence', `${stem}-evidence.json`) : null;
  if (evidencePath) mkdirSync(dirname(evidencePath), { recursive: true });
  const evidenceBytes = Buffer.from('{"fixture":"minimal-hashed-raw-evidence"}');
  if (evidencePath) writeFileSync(evidencePath, evidenceBytes);
  const evidenceHash = evidencePath ? createHash('sha256').update(evidenceBytes).digest('hex') : null;
  const report = {
    ...(pair.epoch === undefined ? {} : { epoch: pair.epoch }),
    scenario: pair.scenario,
    case: pair.caseName,
    status: 'passed',
    requiredChecks: pair.named ? [...required] : { passed: required.length, failed: 0, missing: 0, notRun: 0, total: required.length },
    sourceFingerprint: legacyFixtureSource,
    apiBuildFreeze: { before: legacyFixtureBuild, after: legacyFixtureBuild, unchanged: true },
    ...(pair.named ? { missingRequiredChecks: [], assertions: required.map((name) => ({ name, status: 'passed' })) } : {}),
    ...(evidencePath ? { evidenceReportPath: evidencePath, evidenceReportSha256: evidenceHash } : {}),
  };
  const target = { project: 'loom_dev_verify_legacy', composeProject: 'loom-dev-legacy', generation: 'legacy-v1', sourceRoot: '/tmp/legacy-source' };
  const closure = {
    ...(pair.epoch === undefined ? {} : { epoch: pair.epoch }),
    status: 'CLOSED_PASS',
    integrityClosure: {
      status: 'PASS',
      source: { before: legacyFixtureSource, after: legacyFixtureSource, manifestsEqual: true, changedPaths: [] },
      apiBuildIdentity: { before: legacyFixtureBuild, after: legacyFixtureBuild, precheck: legacyFixtureBuild, unchanged: true },
      ownedMounts: { before: 'PASS', after: 'PASS', targetUnchanged: true, target },
      health: { before: { status: 'PASS', samples: 3 }, after: { status: 'PASS', samples: 3 } },
    },
    case: {
      ...(pair.closureScenario ? { scenario: pair.closureScenario } : { scenarioId: pair.scenario, caseName: pair.caseName }),
      status: 'passed',
      requiredChecks: {
        passed: required.length, total: required.length, failed: 0, missing: 0, notRun: 0,
        ...(pair.named ? { evidence: required.map((name) => ({ name, status: 'passed' })) } : {}),
      },
      ...(evidencePath ? { reportPath: evidencePath, reportSha256: evidenceHash } : {}),
    },
  };
  if (!pair.evidenceLinked && pair.epoch !== undefined) {
    const relativeReport = relative(root, reportPath).split(sep).join('/');
    const reportBytes = Buffer.from(JSON.stringify(report));
    writeFileSync(reportPath, reportBytes);
    closure.lifecycleReport = relativeReport;
    closure.lifecycleReportSha256 = createHash('sha256').update(reportBytes).digest('hex');
  } else if (!pair.evidenceLinked) {
    writeFileSync(reportPath, JSON.stringify(report));
  }
  let rawEvidencePath = null;
  if (evidencePath) rawEvidencePath = evidencePath;
  mutateClosure(closure);
  if (pair.evidenceLinked) writeFileSync(reportPath, JSON.stringify(report));
  writeFileSync(closurePath, JSON.stringify(closure));
  return { reportName, closureName, runtime, rawEvidencePath };
};

test('legacy sibling report pairs require exact linkage and named registered checks for a pass', () => {
  const root = mkdtempSync(join(tmpdir(), 'coverage-legacy-pairs-'));
  try {
    const pairs = [
      'current-draft-append-derived-edit-epoch74',
      'current-draft-cda-membership-epoch71',
      'current-draft-group-pivot-epoch63',
      'current-draft-membership-epoch66',
      'current-draft-join-epoch41',
    ];
    for (const stem of pairs) writeLegacyPair(root, stem);

    const reports = readReports(legacyRuntimePath(root), { cwd: root });
    const rows = new Map(summarizeCoverage(registry, reports).map((row) => [row.path, row]));
    assert.deepEqual(
      ['builder-combine-draft/membership', 'builder-combine-draft/append-derived-edit',
        'cda-current-draft-membership/membership', 'builder-combine-draft/group-pivot', 'builder-combine-draft/join']
        .map((path) => [path, rows.get(path)?.status]),
      [
        ['builder-combine-draft/membership', 'passed'],
        ['builder-combine-draft/append-derived-edit', 'partial'],
        ['cda-current-draft-membership/membership', 'partial'],
        ['builder-combine-draft/group-pivot', 'partial'],
        ['builder-combine-draft/join', 'untested'],
      ],
    );
    assert.match(rows.get('builder-combine-draft/append-derived-edit').evidenceNote,
      /no named required-check outcomes/);
    assert.match(rows.get('cda-current-draft-membership/membership').evidenceNote,
      /no named required-check outcomes/);
    assert.match(rows.get('builder-combine-draft/group-pivot').evidenceNote,
      /lack exact case/);
    assert.equal(rows.get('builder-combine-draft/join').report, null,
      'the epoch-less closure does not establish an exact report identity or case pair');

    const membershipReport = reports.find((entry) => entry.report.scenario === 'builder-combine-draft'
      && entry.report.case === 'membership');
    const source = membershipReport.report.sourceFingerprint;
    const build = membershipReport.report.apiBuildFreeze.before;
    assert.deepEqual(rows.get('builder-combine-draft/membership').freshness,
      { status: 'unknown', source: 'unknown', build: 'unknown' });
    const currentRow = summarizeCoverage(registry, reports, { sourceFingerprint: source, apiBuildIdentity: build })
      .find((row) => row.path === 'builder-combine-draft/membership');
    assert.deepEqual(currentRow.freshness, { status: 'current', source: 'current', build: 'current' });
    const historicalRow = summarizeCoverage(registry, reports, {
      sourceFingerprint: fingerprint('f'.repeat(64), source.files), apiBuildIdentity: build,
    }).find((row) => row.path === 'builder-combine-draft/membership');
    assert.deepEqual(historicalRow.freshness, { status: 'historical', source: 'historical', build: 'current' });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('legacy pass normalization rejects a mismatched report hash, case identity, or integrity closure', () => {
  const rejectedRow = (mutateClosure, mutateEvidence = () => {}) => {
    const root = mkdtempSync(join(tmpdir(), 'coverage-legacy-reject-'));
    try {
      const fixture = writeLegacyPair(root, 'current-draft-membership-epoch66', mutateClosure);
      if (fixture.rawEvidencePath) mutateEvidence(fixture.rawEvidencePath);
      const row = summarizeCoverage(registry, readReports(legacyRuntimePath(root), { cwd: root }))
        .find((item) => item.path === 'builder-combine-draft/membership');
      assert.equal(row.status, 'partial');
      assert.deepEqual(row.freshness, { status: 'unknown', source: 'unknown', build: 'unknown' });
      return row;
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  };

  rejectedRow((closure) => { closure.case.reportSha256 = '0'.repeat(64); });
  rejectedRow(() => {}, (path) => { writeFileSync(path, `${readFileSync(path, 'utf8')} `); });
  rejectedRow((closure) => { closure.case.scenario = 'builder-combine-draft/other-case'; });
  rejectedRow((closure) => { closure.integrityClosure.health.after.samples = 0; });
});

test('legacy closure pairing uses only the report-named sibling and never scans nearby closures', () => {
  const root = mkdtempSync(join(tmpdir(), 'coverage-legacy-no-scan-'));
  try {
    const runtime = legacyRuntimePath(root);
    mkdirSync(runtime, { recursive: true });
    const fixture = writeLegacyPair(root, 'current-draft-cda-membership-epoch71');
    const report = readFileSync(join(runtime, fixture.reportName));
    const closure = readFileSync(join(runtime, fixture.closureName));
    writeFileSync(join(runtime, 'renamed-report.json'), report);
    writeFileSync(join(runtime, 'unrelated-closure.json'), closure);
    rmSync(join(runtime, fixture.reportName));
    rmSync(join(runtime, fixture.closureName));
    const reports = readReports(runtime, { cwd: root });
    assert.equal(reports.find((entry) => entry.report.scenario)?.legacyPair?.candidate, true);
    assert.equal(reports.find((entry) => entry.report.scenario)?.closure, undefined,
      'the reader does not attach an unrelated nearby closure');
    const row = summarizeCoverage(registry, reports).find((item) => item.path === 'cda-current-draft-membership/membership');
    assert.equal(row.status, 'partial');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('legacy raw evidence references outside the report root need an explicit allowed root', () => {
  const root = mkdtempSync(join(tmpdir(), 'coverage-legacy-allowed-root-'));
  const externalRoot = mkdtempSync(join(tmpdir(), 'coverage-legacy-external-evidence-'));
  try {
    const fixture = writeLegacyPair(root, 'current-draft-membership-epoch66');
    const externalPath = join(externalRoot, 'membership-evidence.json');
    writeFileSync(externalPath, readFileSync(fixture.rawEvidencePath));
    const reportPath = join(fixture.runtime, fixture.reportName);
    const closurePath = join(fixture.runtime, fixture.closureName);
    const report = JSON.parse(readFileSync(reportPath, 'utf8'));
    const closure = JSON.parse(readFileSync(closurePath, 'utf8'));
    report.evidenceReportPath = externalPath;
    closure.case.reportPath = externalPath;
    writeFileSync(reportPath, JSON.stringify(report));
    writeFileSync(closurePath, JSON.stringify(closure));

    const withoutAllowedRoot = summarizeCoverage(registry,
      readReports(fixture.runtime, { cwd: root })).find((row) => row.path === 'builder-combine-draft/membership');
    assert.equal(withoutAllowedRoot.status, 'partial', 'an absolute report reference outside the runtime root is not trusted by default');
    const withAllowedRoot = summarizeCoverage(registry,
      readReports(fixture.runtime, { cwd: root, evidenceRoots: [externalRoot] }))
      .find((row) => row.path === 'builder-combine-draft/membership');
    assert.equal(withAllowedRoot.status, 'passed', 'the exact referenced file hash is accepted only under a caller-authorized root');
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(externalRoot, { recursive: true, force: true });
  }
});

const nullableCodeJoinPairPaths = {
  report: new URL('../../../../docs/verification/playwright/runtime/nullable-code-join-epoch142-report.json', import.meta.url),
  closure: new URL('../../../../docs/verification/playwright/runtime/nullable-code-join-epoch142-closure.json', import.meta.url),
};
const nullableCodeJoinScenario = registry.find((entry) => entry.id === 'cda-workspace-combine');
const nullableCodeJoinChecks = scenarioCaseFor(nullableCodeJoinScenario, 'nullable-code-join').requiredChecks;
const nullableCodeJoinBaseline = {
  sourceFingerprint: fingerprint('d'.repeat(64), 1581),
  apiBuildIdentity: '4'.repeat(64) + ':' + '5'.repeat(64) + ':' + '6'.repeat(64),
};
const projectNullableCodeJoinCheckOutcomes = (outcomes, requiredNames) => requiredNames.map((name) => {
  const matchingOutcomes = outcomes.filter((outcome) => outcome.name === name);
  return {
    name,
    status: matchingOutcomes.length > 0 && matchingOutcomes.every((outcome) => outcome.status === 'passed')
      ? 'passed'
      : 'failed',
  };
});
const writeNullableCodeJoinPair = (root, mutate = () => {}) => {
  const runtime = legacyRuntimePath(root);
  mkdirSync(runtime, { recursive: true });
  const reportPath = join(runtime, 'nullable-code-join-epoch142-report.json');
  const closurePath = join(runtime, 'nullable-code-join-epoch142-closure.json');
  const report = JSON.parse(readFileSync(nullableCodeJoinPairPaths.report, 'utf8'));
  const closure = JSON.parse(readFileSync(nullableCodeJoinPairPaths.closure, 'utf8'));
  mutate(report, closure);
  writeFileSync(reportPath, JSON.stringify(report));
  writeFileSync(closurePath, JSON.stringify(closure));
  return { root, runtime, reportPath, closurePath, report, closure };
};
const summarizeNullableCodeJoinPair = (fixture) => summarizeCoverage(
  [nullableCodeJoinScenario],
  readReports(fixture.runtime, { cwd: fixture.root }),
  nullableCodeJoinBaseline,
).find((entry) => entry.path === 'cda-workspace-combine/nullable-code-join');

test('accepted epoch142 nullable-code-join evidence carries all registered per-check outcomes and remains historical', () => {
  const runtime = nullableCodeJoinPairPaths.report.pathname.replace(/nullable-code-join-epoch142-report\.json$/, '');
  const root = new URL('../../../../', import.meta.url).pathname;
  const report = JSON.parse(readFileSync(nullableCodeJoinPairPaths.report, 'utf8'));
  const closure = JSON.parse(readFileSync(nullableCodeJoinPairPaths.closure, 'utf8'));
  const normalizedEvidence = report.assertions.filter((assertion) => nullableCodeJoinChecks.includes(assertion.name));
  const rawOutcomes = report.provenance.rawDomainRequiredAssertions;
  const repeatedPerformanceName = 'All native nullable Join lifecycle actions complete within five seconds';
  const repeatedPerformanceOutcomes = rawOutcomes.filter((assertion) => assertion.name === repeatedPerformanceName);
  const expectedEvidence = projectNullableCodeJoinCheckOutcomes(rawOutcomes, nullableCodeJoinChecks);
  const source = report.target.sourceFingerprint;
  const build = report.target.apiBuildIdentity;
  const row = summarizeCoverage([nullableCodeJoinScenario], readReports(runtime, { cwd: root }), nullableCodeJoinBaseline)
    .find((entry) => entry.path === 'cda-workspace-combine/nullable-code-join');

  assert.equal(report.epoch, 142);
  assert.equal(report.schemaVersion, 2);
  assert.equal(report.scenario, 'cda-workspace-combine');
  assert.equal(report.case, 'nullable-code-join');
  assert.equal(report.requiredChecks.total, 37);
  assert.equal(rawOutcomes.length, 99);
  assert.equal(repeatedPerformanceOutcomes.length, 46);
  assert.ok(rawOutcomes.every((assertion) => assertion.status === 'passed'));
  const rawOutcomesSha256 = createHash('sha256').update(JSON.stringify(rawOutcomes)).digest('hex');
  assert.equal(report.provenance.normalization.rawRequiredCheckOutcomesSha256, rawOutcomesSha256);
  assert.equal(closure.provenance.normalization.rawRequiredCheckOutcomesSha256, rawOutcomesSha256);
  assert.equal(closure.provenance.normalization.normalizedReportSha256,
    createHash('sha256').update(readFileSync(nullableCodeJoinPairPaths.report)).digest('hex'));
  assert.equal(report.provenance.normalization.requiredCheckNamesSha256,
    report.provenance.acceptedNestedReport.registryAcceptance.requiredCheckNamesSha256);
  assert.deepEqual(expectedEvidence.map(({ name }) => name), nullableCodeJoinChecks);
  assert.deepEqual(normalizedEvidence, expectedEvidence);
  assert.deepEqual(closure.case.requiredChecks.evidence, expectedEvidence);
  assert.equal(report.provenance.acceptedNestedReport.case.scenarioId, report.scenario);
  assert.equal(report.provenance.acceptedNestedReport.case.caseName, report.case);
  assert.equal(report.provenance.acceptedNestedReport.status, 'PASSED');
  assert.equal(report.provenance.acceptedNestedReport.rootReviewStatus, 'ACCEPTED');
  assert.equal(report.provenance.acceptedNestedReport.artifacts.domainReport.sha256,
    '5dc9ac71c412421c011c58c9aeb7121f9be2cace3bce207358c6419054d2dd37');
  assert.equal(report.provenance.acceptedNestedReport.sourceIdentity.sourceFingerprint.sha256, source.sha256);
  assert.equal(report.provenance.acceptedNestedReport.integrity.apiBuildIdentity.before, build);
  assert.equal(closure.provenance.acceptedNestedClosure.rootReviewStatus, 'ACCEPTED');
  assert.equal(row.status, 'passed', 'the accepted record has named pass evidence for every registered requirement');
  assert.deepEqual(row.freshness, { status: 'historical', source: 'historical', build: 'historical' });
  assert.equal(source.sha256, '811a1c37be01e896aa94beb20829dd687a6441d60d14f916547895987c406591');
  assert.equal(build, 'ab11a3b84d44dad4cd628c2a2fdd65065b5f76f53e203186b6134e90a7430df8:ab11a3b84d44dad4cd628c2a2fdd65065b5f76f53e203186b6134e90a7430df8:9c49edecc4e062e3c4e6b9752e24d6b59696d614a9abf18aa3ade24636d169e9');
});

test('epoch142 nullable-code-join report and closure reject wrong identity, incomplete checks, and declined integrity', () => {
  const assertNotPassed = (mutate, expectedStatus = 'partial') => {
    const root = mkdtempSync(join(tmpdir(), 'coverage-epoch142-reject-'));
    try {
      const fixture = writeNullableCodeJoinPair(root, mutate);
      const row = summarizeNullableCodeJoinPair(fixture);
      assert.equal(row.status, expectedStatus);
      assert.deepEqual(row.freshness, { status: 'unknown', source: 'unknown', build: 'unknown' });
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  };

  assertNotPassed((_report, closure) => { closure.case.caseName = 'other-case'; });
  assertNotPassed((_report, closure) => {
    closure.case.requiredChecks.evidence = closure.case.requiredChecks.evidence.slice(1);
  });
  assertNotPassed((_report, closure) => {
    closure.case.requiredChecks.evidence[0].status = 'failed';
  });
  assertNotPassed((report, closure) => {
    const repeated = report.provenance.rawDomainRequiredAssertions.filter((assertion) =>
      assertion.name === 'All native nullable Join lifecycle actions complete within five seconds');
    assert.equal(repeated.length, 46);
    repeated[0].status = 'failed';
    assert.equal(repeated[1].status, 'passed', 'a second same-name raw outcome remains passed');
    const derivedEvidence = projectNullableCodeJoinCheckOutcomes(
      report.provenance.rawDomainRequiredAssertions,
      nullableCodeJoinChecks,
    );
    assert.deepEqual(derivedEvidence.find((assertion) => assertion.name === repeated[0].name), {
      name: repeated[0].name,
      status: 'failed',
    });
    report.assertions = [
      ...derivedEvidence,
      ...report.assertions.filter((assertion) => !nullableCodeJoinChecks.includes(assertion.name)),
    ];
    const passed = derivedEvidence.filter((assertion) => assertion.status === 'passed').length;
    const failed = derivedEvidence.filter((assertion) => assertion.status === 'failed').length;
    report.requiredChecks = { passed, failed, unrun: 0, total: nullableCodeJoinChecks.length };
    closure.case.requiredChecks.evidence = derivedEvidence;
    closure.case.requiredChecks.passed = passed;
    closure.case.requiredChecks.failed = failed;
    closure.case.requiredChecks.unrun = 0;
    closure.case.requiredChecks.missingOrUnrun = 0;
    closure.case.requiredChecks.missingOrFailed = failed;
  });
  assertNotPassed((_report, closure) => { closure.integrityClosure.status = 'FAIL'; });
  assertNotPassed((_report, closure) => { closure.status = 'CLOSED_DECLINED'; });
});

test('epoch142 nullable-code-join authoritative report, closure, and ledger hashes cross-link', () => {
  const reportBytes = readFileSync(nullableCodeJoinPairPaths.report);
  const closureBytes = readFileSync(nullableCodeJoinPairPaths.closure);
  const reportSha256 = createHash('sha256').update(reportBytes).digest('hex');
  const closureSha256 = createHash('sha256').update(closureBytes).digest('hex');
  const report = JSON.parse(reportBytes);
  const closure = JSON.parse(closureBytes);
  const ledgerPath = new URL('../../../../docs/BUILDER_VERIFICATION.tsv', import.meta.url);
  const ledgerRows = readFileSync(ledgerPath, 'utf8').split('\n').map((line) => line.split('\t'))
    .filter((columns) => columns[0] === 'Join'
      && columns[1]?.startsWith('Epoch 142 native CDA current-draft MISSING-code KEY_JOIN'));

  assert.equal(report.durableClosurePath, 'docs/verification/playwright/runtime/nullable-code-join-epoch142-closure.json');
  assert.equal(closure.report.path, 'docs/verification/playwright/runtime/nullable-code-join-epoch142-report.json');
  assert.equal(closure.report.sha256, reportSha256);
  assert.equal(closure.provenance.normalization.normalizedReportSha256, reportSha256);
  assert.equal(ledgerRows.length, 1);
  const ledgerHashes = ledgerRows[0][8].match(
    /docs\/verification\/playwright\/runtime\/nullable-code-join-epoch142-report\.json SHA-256 ([a-f0-9]{64}); closure SHA-256 ([a-f0-9]{64})/,
  );
  assert.ok(ledgerHashes, 'the owned epoch142 row records both normalized artifact hashes');
  assert.equal(ledgerHashes[1], reportSha256);
  assert.equal(ledgerHashes[2], closureSha256);
  assert.equal(report.provenance.normalization.acceptedReportSha256,
    '90ea3fce1de785e3487e642f89d05fd13118b659c12af45b2087822aa2e992fd');
  assert.equal(closure.provenance.normalization.acceptedClosureSha256,
    'b8f3579d5be39c2752d58f4edb87172e0479edf259fa165de11059ecada62919');
  assert.equal(closure.provenance.acceptedNestedClosure.report.sha256,
    '90ea3fce1de785e3487e642f89d05fd13118b659c12af45b2087822aa2e992fd');
});
