import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, renameSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, join } from 'node:path';
import { classifyEvidence, classifyFreshness, readReports, summarizeCoverage } from '../coverage-status.mjs';
import { caseNamesFor, coverageDrift, hasLifecycleContract, registry, requiresLifecycleAcceptance, scenarioCaseFor } from '../../registry.mjs';
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



test('Related Unpivot uses the owned authenticated Arango oracle and persists both filter states', () => {
  const workflow = readFileSync(new URL('../../workflows/verify-cda-related-unpivot-browser.mjs', import.meta.url), 'utf8');
  const invocation = buildArangoShellInvocation({ container: 'owned-arango', script: 'print("oracle")', database: 'loom_dev' });
  assert.equal(invocation.command, 'rtk');
  assert.deepEqual(invocation.args.slice(0, 4), ['proxy', 'docker', 'exec', 'owned-arango']);
  const shellCommand = invocation.args.at(-1);
  assert.match(shellCommand, /--server\.username root --server\.password "\$ARANGO_ROOT_PASSWORD"/);
  assert.match(shellCommand, /--server\.database 'loom_dev'/);
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
  assert.match(unpivotFeature.reason, /Epoch130 passed 17\/17 native lifecycle checks/);
  assert.match(unpivotFeature.reason, /dfa2bce29c6c102f79bec2dc56eea6b19c484ebf2f19fb4f6335178e37f06800/);
  assert.match(unpivotFeature.reason, /current-source\/API verification remains pending/);
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
    proposal: [14, /INNER preview returns the three exact rows matched on shared required IDs/],
    cancel: [17, /Canceling the LEFT edit leaves the saved INNER operation unchanged/],
    apply: [15, /INNER Apply preserves the exact joined rows/],
    savedRows: [15, /INNER Apply preserves the exact joined rows/],
    reload: [16, /INNER table reload retains the three exact rows/],
    edit: [20, /LEFT Apply preserves exact matches and unmatched null fields/],
    restoration: [23, /removing KEY_JOIN and reloading restores the rooted empty target/],
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
  assert.equal(filterCoverage.status, 'untested', 'a complete contract does not imply a fresh browser pass');
  assert.equal(filterCoverage.acceptance.kind, 'lifecycle');
  assert.deepEqual(filterCoverage.acceptance.checks,
    { choice: 0, proposal: 1, cancel: 2, apply: 3, savedRows: 4, reload: 5, edit: 6, restoration: 7 });
  assert.equal(hasLifecycleContract(filterCoverage, filterOwner), true,
    'Filter rows must link all lifecycle phases to a registered native CDA case while remaining runtime-unverified');
  assert.deepEqual(scenarioCaseFor(filterScenario, 'filter-lifecycle').requiredChecks, [
    'native Filter rows controls expose an enabled source column and typed condition',
    'filter proposals and rendered result values match an independent scoped CDA source oracle within five seconds',
    'Cancel preserves the exact pre-proposal construction and rendered rows within five seconds',
    'Apply persists the filter construction and exact result rows within five seconds',
    'saved filter rows match the independent scoped CDA oracle',
    'reload restores the saved filter and exact rendered rows',
    'edit reopens the exact saved column and condition before applying a replacement within five seconds',
    'filter removal restores the exact source columns, population, and rows after reload',
  ]);
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
  assert.match(coverageDrift([{ ...scenario, coverage: [implemented({ kind: undefined })] }]).join('\n'), /must be classified/);
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

test('partial long-route collection repair owns one registered case while legacy variants stay unregistered', () => {
  const scenario = registry.find((entry) => entry.id === 'cda-collection-repair-partial');
  assert.ok(scenario, 'the exact partial long-route variant has a registry contract');
  const contract = scenarioCaseFor(scenario, 'partial-long-route-repair-and-reload');
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/standalone-cda-other.spec.mjs');
  assert.equal(contract.requiredChecks.length, 11);
  assert.equal(new Set(contract.requiredChecks).size, contract.requiredChecks.length);
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
