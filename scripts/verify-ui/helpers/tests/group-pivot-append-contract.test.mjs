import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { workspaceOutputOption } from '../builder-combine-draft-helpers.mjs';
import {
  appendColumnOptionMapping,
  appendControlSnapshot,
  matchAppendChoiceControls,
  resolveAppendCapabilityColumns,
} from '../append-option-evidence.mjs';
import { lifecycleAcceptanceDrift, registry, scenarioCaseFor } from '../../registry.mjs';

const workflow = readFileSync(new URL('../../workflows/builder-combine-draft.mjs', import.meta.url), 'utf8');
const evidence = readFileSync(new URL('../append-option-evidence.mjs', import.meta.url), 'utf8');
const fixtures = readFileSync(new URL('../fixtures.mjs', import.meta.url), 'utf8');
const spec = readFileSync(new URL('../../specs/draft-combine.spec.mjs', import.meta.url), 'utf8');
const scenario = registry.find((entry) => entry.id === 'builder-combine-draft');
const contract = scenarioCaseFor('builder-combine-draft', 'group-pivot-append');
const coverage = scenario.coverage.find((entry) => entry.feature === 'APPEND over unpublished Observation GROUP and DiagnosticReport GROUP→PIVOT siblings');

test('Group→Pivot APPEND binds exact restoration checks and compares complete source documents', () => {
  const checks = contract.requiredChecks;
  const appendWorkflow = workflow.slice(workflow.indexOf('export const groupPivotAppendWorkflow ='));
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/draft-combine.spec.mjs');
  assert.match(spec, /caseName:\s*'group-pivot-append'/);
  assert.match(spec, /await groupPivotAppendWorkflow\(/);
  assert.equal(checks.length, 31);
  assert.match(appendWorkflow, /const previewLifecycle = captureOwnedPreviewLifecycle\(page, context\.target, run\.explorer, \{ diagnostic: true \}\)/);
  assert.match(appendWorkflow, /startCombineTarget\([\s\S]*?beforeTarget\)/);
  assert.match(appendWorkflow, /startCombineTarget\([\s\S]*?retryBase\)/);
  assert.match(appendWorkflow, /const lifecycle = await previewLifecycle\.stop\(\)/);
  assert.match(appendWorkflow, /markExpectedOwnedPreviewAborts\(/);
  assert.match(workflow, /createCommandCAS:[\s\S]*?expectedDraftVersion/);
  assert.match(appendWorkflow, /report\.target\.nativeCandidateRequests = \[\]/);
  assert.match(appendWorkflow, /successorRequests: report\.target\.nativeCandidateRequests/);
  assert.match(appendWorkflow, /targetBindings:\s*\[report\.target\.canceledCombineTarget, report\.target\.combineTarget\]/);
  assert.match(fixtures, /pathname\.endsWith\('\/authoring\/v2\/preview'\)[\s\S]*?receiptId: previewReceiptId/);

  assert.ok(checks.includes('removing Combine and reloading restores its rooted empty target'));
  assert.equal(checks.includes('removing Combine and reloading restores its rooted empty output'), false);
  assert.equal(coverage.status, 'implemented');
  assert.match(coverage.reason, /Epoch143 passed the native basic-fixture Group→Pivot APPEND lifecycle: 31\/31 required checks/);
  assert.match(coverage.reason, /This proves the basic synthetic fixture only; real CDA and published-source APPEND remain separate/);
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
  assert.match(workflow, /resolveAppendCapabilityColumns\(/);
  assert.match(workflow, /sourceColumnRefs:\s*\[/);
  assert.match(workflow, /columnId: sources\[0\]\.groupKeyOutputColumn\?\.id/);
  assert.match(workflow, /columnId: sources\[0\]\.groupCountOutputColumn\?\.id/);
  assert.match(workflow, /columnId: sources\[1\]\.pivotIdentityOutputColumn\?\.id/);
  assert.match(workflow, /sourceOutputIds: sources\.map\(\(source\) => source\.outputId\)/);
  assert.match(workflow, /snapshotToken: builder\.catalog\?\.snapshotToken/);
  assert.match(workflow, /draftVersion: builder\.draftVersion/);
  assert.match(workflow, /draftDigest: builder\.draftDigest/);
  assert.match(workflow, /stageId: 'source_projection'/);
  assert.match(workflow, /capabilityCapture\.waitFor\(\(entry\) => entry\.body\?\.outputId === target\.outputId &&[\s\S]*?entry\.body\?\.expectedDraftVersion === builder\.draftVersion[\s\S]*?entry\.body\?\.expectedDraftDigest === builder\.draftDigest[\s\S]*?entry\.response\?\.outputId === target\.outputId/);
  assert.match(workflow, /matchAppendChoiceControls\(\{ controls, expectedInputs, expectedOutputs, emptyMapping: APPEND_EMPTY_MAPPING \}\)/);
  assert.match(workflow, /mappings\.observationGroupKey/);
  assert.match(workflow, /mappings\.reportPivotIdentity/);
  assert.doesNotMatch(workflow, /appendColumnOptionMapping\(sources\[[01]\]\./);
  assert.match(evidence, /exactlyTwoBoundSourceOutputs/);
  assert.match(evidence, /column\.logicalType/);
  assert.match(evidence, /column\.cardinality/);
  assert.match(evidence, /typeof column\.nullable === 'boolean'/);
  assert.match(evidence, /compiledSchema:/);
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
  assert.match(verify[0], /await assertDraftCandidate\(report, page, candidateCapture, candidateBase, candidateTarget, sources, 'APPEND', \{ requireSelectedPreview: true \}\)/);
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

test('Group→Pivot APPEND derives native option identity and nullability from the exact current-draft receipt', () => {
  const sourceOutputIds = ['out_b625e4b1a453299fb7817c6e', 'out_1862bc4d43b27881e69762fe'];
  const targetOutputId = 'out_0e1a294c1423aa001f641205';
  const snapshotToken = 'sha256:c74ee02cade0a5c22288060a2c676c13b2c551cf8bcedc373fc7a41c1e4660e2';
  const draftDigest = 'sha256:65aec1a1a8a491e11a5a5e2719da1e8c6bd87e813eeaa6f4b6194f3c79402967';
  const sourceColumnRefs = [
    { key: 'observationGroupKey', outputId: sourceOutputIds[0], columnId: 'group-key_1f051d12-b53c-4216-8e11-0105cf2bbfd9' },
    { key: 'observationGroupCount', outputId: sourceOutputIds[0], columnId: 'group-column_d0ee72bd-eb58-4c9e-9ed2-ef63e867b492' },
    { key: 'reportPivotIdentity', outputId: sourceOutputIds[1], columnId: 'group-key_aa0c72b4-9749-4af7-9ac4-74c2ea3a7ce9' },
    { key: 'reportFinal', outputId: sourceOutputIds[1], columnId: 'pivot-column_1f496904-da6b-4500-8aef-2f8e0ab81077' },
    { key: 'reportPreliminary', outputId: sourceOutputIds[1], columnId: 'pivot-column_aba1682b-fa53-432e-a8d5-1484bfe1d35f' },
  ];
  const binding = {
    requestPath: '/api/v1/projects/loom_dev_verify_muxiouom-82d35d7/explorers/verify-om-82d35d7-draft-combine-group-pivot-append/authoring/v2/construction-capabilities',
    requestOrigin: 'http://127.0.0.1:30008',
    outputId: targetOutputId,
    snapshotToken,
    draftVersion: 10,
    draftDigest,
    stageId: 'source_projection',
    builderGeneration: 'group-pivot-append-wave133',
    fixtureGeneration: 'group-pivot-append-wave133',
    sourceOutputIds,
  };
  // Payload/schema copied from retained attempt3 capabilities capture SHA-256 f39c129278be2e2cdf0e9279388fd13783cd77659094cab286ce59798e7b95df; URL models the browser's UI-origin proxy route.
  const event = {
    url: 'http://127.0.0.1:30008' + binding.requestPath,
    status: 200,
    body: {
      outputId: targetOutputId,
      snapshotToken,
      expectedDraftVersion: 10,
      expectedDraftDigest: draftDigest,
      stageId: 'source_projection',
    },
    response: {
      outputId: targetOutputId,
      snapshotToken,
      draftVersion: 10,
      draftDigest,
      stageId: 'source_projection',
      selectedStage: { id: 'source_projection' },
      workspaceInputs: [
        {
          outputId: sourceOutputIds[0],
          columns: [
            { id: sourceColumnRefs[0].columnId, name: 'col_f185e65d9efdcde0512f187f', label: 'Observation ID', logicalType: 'string', cardinality: 'optional_one', nullable: true },
            { id: sourceColumnRefs[1].columnId, name: 'row_count', label: 'Row count', logicalType: 'integer', cardinality: 'required_one', nullable: false },
          ],
        },
        {
          outputId: sourceOutputIds[1],
          columns: [
            { id: sourceColumnRefs[2].columnId, name: 'col_b88b20cd5f057824af8d78f3', label: 'DiagnosticReport ID', logicalType: 'string', cardinality: 'optional_one', nullable: true },
            { id: sourceColumnRefs[3].columnId, name: 'final', label: 'final', logicalType: 'integer', cardinality: 'optional_one', nullable: true },
            { id: sourceColumnRefs[4].columnId, name: 'preliminary', label: 'preliminary', logicalType: 'integer', cardinality: 'optional_one', nullable: true },
          ],
        },
      ],
    },
  };
  // Authored operation outputs can omit nullability; refs deliberately carry IDs only.
  const authoredColumnsWithoutNullability = [
    { id: sourceColumnRefs[0].columnId, label: 'Observation ID' },
    { id: sourceColumnRefs[1].columnId, label: 'Row count' },
    { id: sourceColumnRefs[2].columnId, label: 'DiagnosticReport ID' },
    { id: sourceColumnRefs[3].columnId, label: 'final' },
    { id: sourceColumnRefs[4].columnId, label: 'preliminary' },
  ];
  assert.ok(authoredColumnsWithoutNullability.every((column) => !Object.hasOwn(column, 'nullable')));

  const resolved = resolveAppendCapabilityColumns({ event, binding, sourceColumnRefs });
  assert.equal(resolved.ok, true);
  assert.ok(Object.values(resolved.checks).every(Boolean));
  assert.deepEqual(resolved.expectedSourceOutputIds, sourceOutputIds);
  assert.deepEqual(resolved.actualSourceOutputIds, [...sourceOutputIds].sort());
  assert.deepEqual(resolved.sourceColumns.map((column) => column.compiledSchema), [
    { name: 'col_f185e65d9efdcde0512f187f', label: 'Observation ID', logicalType: 'string', cardinality: 'optional_one', nullable: true },
    { name: 'row_count', label: 'Row count', logicalType: 'integer', cardinality: 'required_one', nullable: false },
    { name: 'col_b88b20cd5f057824af8d78f3', label: 'DiagnosticReport ID', logicalType: 'string', cardinality: 'optional_one', nullable: true },
    { name: 'final', label: 'final', logicalType: 'integer', cardinality: 'optional_one', nullable: true },
    { name: 'preliminary', label: 'preliminary', logicalType: 'integer', cardinality: 'optional_one', nullable: true },
  ]);
  assert.deepEqual(
    sourceColumnRefs.map((reference) => resolved.mappings[reference.key]),
    [
      { kind: 'append-column', value: 'column:' + sourceColumnRefs[0].columnId, selectedLabel: 'Observation ID · string · nullable' },
      { kind: 'append-column', value: 'column:' + sourceColumnRefs[1].columnId, selectedLabel: 'Row count · integer' },
      { kind: 'append-column', value: 'column:' + sourceColumnRefs[2].columnId, selectedLabel: 'DiagnosticReport ID · string · nullable' },
      { kind: 'append-column', value: 'column:' + sourceColumnRefs[3].columnId, selectedLabel: 'final · integer · nullable' },
      { kind: 'append-column', value: 'column:' + sourceColumnRefs[4].columnId, selectedLabel: 'preliminary · integer · nullable' },
    ],
  );

  const empty = Object.freeze({ kind: 'append-empty-for-this-table' });
  const expectedInputs = sourceOutputIds.map(workspaceOutputOption);
  const expectedOutputs = [
    { name: 'record_id', label: 'Record ID', mappings: [resolved.mappings.observationGroupKey, resolved.mappings.reportPivotIdentity] },
    { name: 'observation_rows', label: 'Observation rows', mappings: [resolved.mappings.observationGroupCount, empty] },
    { name: 'report_final', label: 'Report final', mappings: [empty, resolved.mappings.reportFinal] },
    { name: 'report_preliminary', label: 'Report preliminary', mappings: [empty, resolved.mappings.reportPreliminary] },
  ];
  const elements = new Map();
  const addSelect = (selector, value, selectedLabel, group = null) => elements.set(selector, testSelect(value, selectedLabel, group));
  elements.set('button[data-testid="construction-combine-choice-append"]', testButton());
  expectedInputs.forEach((value, index) => addSelect(
    'select[aria-label="Input table ' + (index + 1) + '"]', value, 'current draft source', 'Current draft tables'));
  expectedOutputs.forEach((output, index) => {
    const number = index + 1;
    elements.set('input[aria-label="Output field ' + number + ' name"]', testInput(output.name));
    elements.set('input[aria-label="Output field ' + number + ' label"]', testInput(output.label));
    output.mappings.forEach((mapping, inputIndex) => {
      const selector = 'select[aria-label="Output field ' + number + ' matching field in input ' + (inputIndex + 1) + '"]';
      if (mapping === empty) addSelect(selector, 'empty-for-this-table', 'Empty for this table');
      else addSelect(selector, mapping.value, mapping.selectedLabel);
    });
  });
  const priorDocument = globalThis.document;
  const priorGetComputedStyle = globalThis.getComputedStyle;
  globalThis.document = { querySelector: (selector) => elements.get(selector) ?? null };
  globalThis.getComputedStyle = () => ({ display: 'block', visibility: 'visible' });
  try {
    const controls = appendControlSnapshot();
    const accepted = matchAppendChoiceControls({ controls, expectedInputs, expectedOutputs, emptyMapping: empty });
    assert.equal(accepted.ok, true);
    assert.equal(accepted.exactOutputs, true);

    const wrongTarget = structuredClone(event);
    wrongTarget.response.outputId = 'out_wrong-target';
    assert.equal(resolveAppendCapabilityColumns({ event: wrongTarget, binding, sourceColumnRefs }).checks.responseOutputMatches, false);
    assert.equal(resolveAppendCapabilityColumns({ event: wrongTarget, binding, sourceColumnRefs }).ok, false);

    const wrongCAS = structuredClone(event);
    wrongCAS.response.draftDigest = 'sha256:wrong-draft';
    assert.equal(resolveAppendCapabilityColumns({ event: wrongCAS, binding, sourceColumnRefs }).checks.responseDraftDigestMatches, false);
    const wrongStage = structuredClone(event);
    wrongStage.response.stageId = 'wrong-stage';
    assert.equal(resolveAppendCapabilityColumns({ event: wrongStage, binding, sourceColumnRefs }).checks.responseStageMatches, false);
    assert.equal(resolveAppendCapabilityColumns({ event, binding: { ...binding, fixtureGeneration: 'wrong-generation' }, sourceColumnRefs }).checks.builderGenerationMatches, false);

    const wrongSourceOutput = structuredClone(event);
    wrongSourceOutput.response.workspaceInputs[0].outputId = 'out_wrong-source';
    const wrongOutputResolution = resolveAppendCapabilityColumns({ event: wrongSourceOutput, binding, sourceColumnRefs });
    assert.equal(wrongOutputResolution.checks.exactSourceOutputs, false);
    assert.equal(wrongOutputResolution.checks.exactSourceColumns, false);
    assert.equal(wrongOutputResolution.ok, false);

    const wrongColumnRefs = structuredClone(sourceColumnRefs);
    wrongColumnRefs[0].columnId = 'group-key_wrong-column';
    const wrongColumnResolution = resolveAppendCapabilityColumns({ event, binding, sourceColumnRefs: wrongColumnRefs });
    assert.equal(wrongColumnResolution.checks.exactSourceColumns, false);
    assert.equal(wrongColumnResolution.ok, false);

    const wrongColumnControls = structuredClone(controls);
    wrongColumnControls.outputs[0].mappings[0].value = 'column:group-key_wrong-column';
    const wrongColumnMatch = matchAppendChoiceControls({ controls: wrongColumnControls, expectedInputs, expectedOutputs, emptyMapping: empty });
    assert.equal(wrongColumnMatch.exactOutputs, false);
    assert.equal(wrongColumnMatch.ok, false);
  } finally {
    if (priorDocument === undefined) delete globalThis.document;
    else globalThis.document = priorDocument;
    if (priorGetComputedStyle === undefined) delete globalThis.getComputedStyle;
    else globalThis.getComputedStyle = priorGetComputedStyle;
  }
});
