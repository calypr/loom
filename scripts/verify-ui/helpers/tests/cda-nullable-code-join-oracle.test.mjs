import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import {
  cdaNullableEmptyRemovalPreviewEvidence,
  cdaNullableCodeJoinDirectSourceEvidence,
  cdaNullableValueQuantityCodeCandidateEvidence,
  cdaNullableValueQuantityCodeJoinOracle,
} from '../cda-nullable-code-join-oracle.mjs';
import { currentDraftSourceEvidence } from '../builder-combine-draft-helpers.mjs';
import { scenarioCaseFor } from '../../registry.mjs';

const project = 'loom_dev_cda_fhir';
const generation = 'cda-fhir-v1';
const row = (id, code, present = true) => ({
  _id: `Observation/${id}`,
  id,
  project,
  generation,
  resourceType: 'Observation',
  valueQuantityCodePresent: present,
  valueQuantityCode: present ? code : null,
});

const left = [
  row('d9143e11-8d1e-5b43-8957-d433ba2ef6c2', 'd'),
  row('7928b626-b6ad-54d2-8f61-0fd201d2a48d', 'd'),
  row('52f5e622-0181-5feb-936c-e9992ab26616', null, false),
];
const right = [
  row('2103771b-4af2-5eb3-b1b9-6f74d766da86', 'd'),
  row('bfe79f40-5134-53d0-bcc7-9ec6d0843646', 'd'),
  row('485e2567-b566-56f3-b5bd-5f025f37cd95', null, false),
];

test('catalog evidence accepts only the scoped scalar optional Observation.valueQuantity.code candidate', () => {
  const builder = { catalog: {
    generation,
    nodes: [{ nodeId: 'observation-root', resourceType: 'Observation', rowRootEligible: true }],
    candidates: [{ candidateId: 'code-candidate', nodeId: 'observation-root', fieldPath: 'valueQuantity.code',
      logicalType: 'string', cardinality: 'optional_one', repeated: false }],
  } };
  assert.equal(cdaNullableValueQuantityCodeCandidateEvidence(builder, generation).ok, true);
  for (const change of [
    { generation: 'other-generation' },
    { candidate: { logicalType: 'integer' } },
    { candidate: { cardinality: 'many' } },
    { candidate: { repeated: true } },
  ]) {
    const negative = structuredClone(builder);
    if (change.generation) negative.catalog.generation = change.generation;
    if (change.candidate) Object.assign(negative.catalog.candidates[0], change.candidate);
    assert.equal(cdaNullableValueQuantityCodeCandidateEvidence(negative, generation).ok, false);
  }
});

test('raw duplicate and missing-code rows produce the literal 2×2 INNER pairs and LEFT padding only for missing left', () => {
  const oracle = cdaNullableValueQuantityCodeJoinOracle({ leftRows: left, rightRows: right, project, generation });
  assert.deepEqual(oracle.innerRows, [
    ['d9143e11-8d1e-5b43-8957-d433ba2ef6c2', 'd', '2103771b-4af2-5eb3-b1b9-6f74d766da86', 'd'],
    ['d9143e11-8d1e-5b43-8957-d433ba2ef6c2', 'd', 'bfe79f40-5134-53d0-bcc7-9ec6d0843646', 'd'],
    ['7928b626-b6ad-54d2-8f61-0fd201d2a48d', 'd', '2103771b-4af2-5eb3-b1b9-6f74d766da86', 'd'],
    ['7928b626-b6ad-54d2-8f61-0fd201d2a48d', 'd', 'bfe79f40-5134-53d0-bcc7-9ec6d0843646', 'd'],
  ]);
  assert.deepEqual(oracle.leftRows, [
    ...oracle.innerRows,
    ['52f5e622-0181-5feb-936c-e9992ab26616', null, null, null],
  ]);
  assert.deepEqual(oracle.rightMissingIDs, ['485e2567-b566-56f3-b5bd-5f025f37cd95']);
  assert.equal(oracle.evidence.missingCodeEqualityMatches, 0);
  assert.equal(oracle.evidence.explicitNullSemanticsClaimed, false);
});

test('oracle rejects incorrect scope, overlapping inputs, or missing-value multiplicity drift', () => {
  assert.throws(() => cdaNullableValueQuantityCodeJoinOracle({ leftRows: left, rightRows: [row('outside', 'd')], project, generation }));
  const overlap = [...right.slice(1), row(left[0].id, 'd')];
  assert.throws(() => cdaNullableValueQuantityCodeJoinOracle({ leftRows: left, rightRows: overlap, project, generation }));
  assert.throws(() => cdaNullableValueQuantityCodeJoinOracle({ leftRows: left.slice(1), rightRows: right, project, generation }));
});

test('direct-source evidence binds exact raw current-draft Observation inputs without Group or Pivot', () => {
  const expectedOutputIDs = ['out-left', 'out-right'];
  const expectedSelectionRevisionIDs = ['selection-0', 'selection-1'];
  const inputs = expectedOutputIDs.map(outputId => ({ kind: 'WORKSPACE_OUTPUT', outputId }));
  const sourceDocuments = expectedOutputIDs.map((outputId, index) => ({
    output: { id: outputId },
    rootResourceType: 'Observation',
    population: { selectionRevisionId: `selection-${index}`, route: [] },
    construction: { steps: [] },
  }));
  const evidence = cdaNullableCodeJoinDirectSourceEvidence({ inputs, expectedOutputIDs,
    expectedSelectionRevisionIDs, sourceDocuments });
  assert.equal(evidence.ok, true);
  assert.deepEqual(evidence.checks, {
    distinctExpectedOutputs: true,
    exactExpectedSelections: true,
    exactInputRefs: true,
    currentDraftOnlyInputs: true,
    documentsUnique: true,
    directObservationSources: true,
  });
  assert.deepEqual(evidence.sources.map(source => source.constructionSteps), [[], []]);

  // The existing grouped-source contract still requires saved Group/Pivot steps.
  assert.equal(currentDraftSourceEvidence({ inputs, expectedOutputIDs, sourceDocuments }).ok, false);

  assert.equal(cdaNullableCodeJoinDirectSourceEvidence({ inputs, expectedOutputIDs,
    expectedSelectionRevisionIDs: ['selection-0', 'wrong-selection'], sourceDocuments }).ok, false);
});

test('direct-source evidence rejects wrong outputs, pinned refs, duplicate docs, and non-raw source documents', () => {
  const expectedOutputIDs = ['out-left', 'out-right'];
  const expectedSelectionRevisionIDs = ['selection-0', 'selection-1'];
  const inputs = expectedOutputIDs.map(outputId => ({ kind: 'WORKSPACE_OUTPUT', outputId }));
  const sourceDocuments = expectedOutputIDs.map((outputId, index) => ({
    output: { id: outputId },
    rootResourceType: 'Observation',
    population: { selectionRevisionId: `selection-${index}`, route: [] },
    construction: { steps: [] },
  }));
  const evidence = (changes = {}) => cdaNullableCodeJoinDirectSourceEvidence({
    inputs: changes.inputs ?? inputs,
    expectedOutputIDs: changes.expectedOutputIDs ?? expectedOutputIDs,
    expectedSelectionRevisionIDs: changes.expectedSelectionRevisionIDs ?? expectedSelectionRevisionIDs,
    sourceDocuments: changes.sourceDocuments ?? sourceDocuments,
  });

  assert.equal(evidence({ expectedOutputIDs: ['out-left', 'wrong-output'] }).ok, false);
  assert.equal(evidence({ inputs: [{ kind: 'REVISION', outputId: 'out-left', revisionId: 'pinned' }, inputs[1]] }).ok, false);
  assert.equal(evidence({ inputs: [{ ...inputs[0], tableId: 'pinned-table' }, inputs[1]] }).ok, false);
  assert.equal(evidence({ inputs: [{ ...inputs[0], revisionId: 'pinned-revision' }, inputs[1]] }).ok, false);
  assert.equal(evidence({ inputs: [inputs[0], { kind: 'WORKSPACE_OUTPUT', outputId: 'different-output' }] }).ok, false);
  assert.equal(evidence({ expectedSelectionRevisionIDs: ['selection-0', 'wrong-selection'] }).ok, false);
  assert.equal(evidence({ sourceDocuments: [...sourceDocuments, sourceDocuments[0]] }).ok, false);
  assert.equal(evidence({ sourceDocuments: [sourceDocuments[0]] }).ok, false);
  assert.equal(evidence({ sourceDocuments: sourceDocuments.map((document, index) => index === 0
    ? { ...document, rootResourceType: 'DiagnosticReport' } : document) }).ok, false);
  assert.equal(evidence({ sourceDocuments: sourceDocuments.map((document, index) => index === 0
    ? { ...document, population: { route: [] } } : document) }).ok, false);
  assert.equal(evidence({ sourceDocuments: sourceDocuments.map((document, index) => index === 0
    ? { ...document, population: { selectionRevisionId: 'selection-0', route: [{ relationship: 'derived-from' }] } } : document) }).ok, false);
  assert.equal(evidence({ sourceDocuments: sourceDocuments.map((document, index) => index === 0
    ? { ...document, construction: { steps: [{ operation: { kind: 'GROUP' } }] } } : document) }).ok, false);
});

test('empty removal preview binds the exact receipt/output and the native no-columns status DOM', () => {
  const outputId = 'nullable-join-target';
  const proposal = { responseBody: {
    proposalId: 'nullable-removal-proposal',
    outputId,
    previewStatus: 'READY',
    preview: { receiptId: 'nullable-removal-proposal', outputId, columns: [], rows: [], rowCount: 0 },
  } };
  const dom = {
    proposalPanelCount: 1,
    proposalStatus: 'ready',
    proposalId: 'nullable-removal-proposal',
    resultSectionCount: 1,
    resultStatus: 'ready',
    resultOutputId: outputId,
    resultReceiptId: 'nullable-removal-proposal',
    resultProposalId: 'nullable-removal-proposal',
    proposalPreviewCount: 1,
    previewStatus: 'ready',
    previewOutputId: outputId,
    previewReceiptId: 'nullable-removal-proposal',
    statusText: 'This table has no visible columns.',
    tableCount: 0,
  };
  const evidence = cdaNullableEmptyRemovalPreviewEvidence({ proposal, outputId, dom });
  assert.equal(evidence.ok, true);
  assert.deepEqual(evidence.checks, {
    readyResponse: true,
    receiptBound: true,
    outputBound: true,
    emptyResponse: true,
    readyDOM: true,
    emptyStatus: true,
    tableAbsent: true,
  });
  for (const changes of [
    { outputId: 'wrong-output' },
    { proposal: { responseBody: { ...proposal.responseBody, proposalId: 'wrong-proposal' } } },
    { proposal: { responseBody: { ...proposal.responseBody, preview: { ...proposal.responseBody.preview, rows: [{ id: 'unexpected' }] } } } },
    { proposal: { responseBody: { ...proposal.responseBody, preview: { ...proposal.responseBody.preview, columns: [{ column: 'unexpected' }] } } } },
    { proposal: { responseBody: { ...proposal.responseBody, preview: { ...proposal.responseBody.preview, rowCount: 1 } } } },
    { dom: { ...dom, statusText: 'Loom did not return preview rows for this proposal.' } },
    { dom: { ...dom, tableCount: 1 } },
    { dom: { ...dom, resultReceiptId: 'stale-receipt' } },
  ]) {
    assert.equal(cdaNullableEmptyRemovalPreviewEvidence({ proposal: changes.proposal ?? proposal,
      outputId: changes.outputId ?? outputId, dom: changes.dom ?? dom }).ok, false);
  }
});

test('native nullable Join registration binds proposal, LEFT edit, removal, and timed action evidence', async () => {
  const contract = scenarioCaseFor('cda-workspace-combine', 'nullable-code-join');
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/cda-current-draft-nullable-code-join.spec.mjs');
  assert.equal(contract.requiredChecks.length, 37);
  assert.equal(new Set(contract.requiredChecks).size, contract.requiredChecks.length);
  for (const check of [
    'Join proposal preview response binds the exact UI route, receipt, output, and draft CAS',
    'LEFT Join proposal binds exact raw sources, code keys, outputs, and draft CAS',
    'Removal proposal binds exact CAS and only removes the saved current-draft KEY_JOIN',
    'All native nullable Join lifecycle actions complete within five seconds',
  ]) assert(contract.requiredChecks.includes(check), `registered lifecycle is missing ${check}`);

  const workflow = await readFile(new URL('../../workflows/cda-current-draft-nullable-code-join-workflow.mjs', import.meta.url), 'utf8');
  assert.match(workflow, /cda\.check\('correctness', 'Join proposal preview response binds the exact UI route, receipt, output, and draft CAS'/);
  assert.match(workflow, /cda\.check\('persistence', 'Removal proposal binds exact CAS and only removes the saved current-draft KEY_JOIN'/);
  assert.match(workflow, /cda\.check\('correctness', `\$\{joinType\} Join proposal binds exact raw sources, code keys, outputs, and draft CAS`/);
  assert.match(workflow, /requiredCheck: 'All native nullable Join lifecycle actions complete within five seconds'/);
  assert.match(workflow, /return \{ baseState, stepId: step\?\.id, proposal \}/,
    'saved LEFT edit must return the candidate step identity to the Apply verifier');
  assert.match(workflow, /const initialTargetDocument = structuredClone\(target\.baselineDocument\)[\s\S]*?target = await makeTarget\(\);/,
    'after canceling the scratch preview, the Apply lifecycle must create and bind a fresh Combine target');
  const removalBlock = workflow.slice(workflow.indexOf('const removalProposal'), workflow.indexOf('target = await makeTarget(false)'));
  assert.match(removalBlock, /readProposalPreviewState\(page, target\.outputId\)/,
    'removal reads the rendered empty preview state, whose zero-column layout has no table');
  assert.match(removalBlock, /cdaNullableEmptyRemovalPreviewEvidence\(\{ proposal, outputId: target\.outputId, dom \}\)/,
    'removal binds exact empty response rows/columns and the native status DOM');
  assert.doesNotMatch(removalBlock, /readGrid\('proposal'\)/,
    'removal must not require a table that the zero-column preview intentionally omits');
  assert.match(workflow, /isDeepStrictEqual\(outputs\.map\(output => output\.id\), expectedOutputColumnIDs\)/,
    'saved INNER/LEFT edits must retain the stable output column IDs');
  assert.equal((workflow.match(/cdaNullableCodeJoinDirectSourceEvidence\(/g) ?? []).length, 3,
    'proposal, saved edit, and persisted Join checks must use the direct raw-source contract');
  assert.match(workflow, /expectedSelectionRevisionIDs: sources\.map\(source => source\.selection\.id\)/,
    'each raw source document must retain its exact selected revision');
  assert.match(workflow, /rawSourceBinding: sourceEvidence/,
    'the native proposal report must retain direct-source binding evidence');
  assert.doesNotMatch(workflow, /currentDraftSourceEvidence/,
    'the grouped-source contract must not be applied to these raw zero-step inputs');
  assert.match(workflow, /exactProjectRoute:[\s\S]*builderURL\.pathname === `\/api\/v1\/projects\/\$\{encode\(project\)\}\/explorers\/\$\{encode\(explorer\)\}\/authoring\/v2\/builder`/);
  assert.match(workflow, /authorizationScopeDigest: initialScope\.authorizationScopeDigest/);
});
