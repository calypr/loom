import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import {
  cdaNullableEmptyRemovalPreviewEvidence,
  cdaNullableCodeJoinDirectSourceEvidence,
  cdaNullableDirectSourceColumnBindings,
  cdaNullableNewExplorerIdentityEvidence,
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

test('new Explorer identity binds create summary, exact Builder route, NEW null-workspace state, and catalog scope', () => {
  const project = 'loom_dev_cda_fhir';
  const explorer = 'cda-nullable-code-join-123';
  const title = 'CDA Nullable Code Join QA';
  const expectedAPIOrigin = 'https://loom.example';
  const expectedBuilderPath = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/builder`;
  const created = { project, explorerId: explorer, title, management: 'INTERACTIVE' };
  const builder = {
    lifecycleState: 'NEW', draftVersion: 0, draftDigest: '', workspace: null,
    catalog: { generation, snapshotToken: 'snapshot-1', authorizationScopeDigest: 'scope-1' },
  };
  const input = { created, builder, project, explorer, title,
    builderURL: `${expectedAPIOrigin}${expectedBuilderPath}`, expectedAPIOrigin, expectedBuilderPath, generation };
  const evidence = cdaNullableNewExplorerIdentityEvidence(input);
  assert.equal(evidence.ok, true);
  assert(Object.values(evidence.checks).every(Boolean));

  for (const changes of [
    { created: { ...created, project: 'other-project' } },
    { created: { ...created, explorerId: 'other-explorer' } },
    { created: { ...created, title: 'Wrong title' } },
    { builderURL: `https://other.example${expectedBuilderPath}` },
    { builderURL: `${expectedAPIOrigin}/wrong/path` },
    { builderURL: `${expectedAPIOrigin}${expectedBuilderPath}?stale=1` },
    { builder: { ...builder, lifecycleState: 'READY' } },
    { builder: { ...builder, lifecycleState: 'READY', workspace: null, draftVersion: 1, draftDigest: 'draft' } },
    { builder: { ...builder, workspace: { documents: [] } } },
    { builder: { ...builder, draftVersion: 1, draftDigest: 'draft' } },
    { builder: { ...builder, catalog: { ...builder.catalog, generation: 'other-generation' } } },
    { builder: { ...builder, catalog: { generation, snapshotToken: '', authorizationScopeDigest: '' } } },
  ]) {
    assert.equal(cdaNullableNewExplorerIdentityEvidence({ ...input, ...changes }).ok, false);
  }
});

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

test('raw Builder document columns bind native Join by stable columnId, not public column name or id', () => {
  // This is the Column wire shape retained by the attempt-2 APPLY response.
  const document = {
    rootResourceType: 'Observation',
    columns: [
      { columnId: 'source_4e077295953de003e04d49de', column: 'col_b595992da229f0370a51fa04',
        logicalType: 'string', occurrenceId: 'base',
        label: 'left Observation ID', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
      { columnId: 'source_064bc175681cffe36bb600b6', column: 'col_b06cd4df2e76017c5a0fc401',
        logicalType: 'string', occurrenceId: 'base',
        label: 'Value Quantity Code', source: { kind: 'field', field: {
          path: 'valueQuantity.code', projectionMode: 'VALUE',
        } } },
    ],
  };
  const expectedCodeColumnName = 'col_b06cd4df2e76017c5a0fc401';
  assert.equal(document.columns[1].logicalType, 'string');
  assert.deepEqual(document.columns[1].source.field, { path: 'valueQuantity.code', projectionMode: 'VALUE' });
  const evidence = cdaNullableDirectSourceColumnBindings({ document, expectedCodeColumnName });
  assert.equal(evidence.ok, true);
  assert.deepEqual(evidence.sourceColumnIDs, [
    'source_4e077295953de003e04d49de',
    'source_064bc175681cffe36bb600b6',
  ]);
  assert.deepEqual(evidence.publicColumnNames, ['col_b595992da229f0370a51fa04', 'col_b06cd4df2e76017c5a0fc401']);
  assert.notDeepEqual(evidence.sourceColumnIDs, evidence.publicColumnNames);

  const missingStableID = structuredClone(document);
  delete missingStableID.columns[0].columnId;
  missingStableID.columns[0].id = 'legacy-id-is-not-the-stable-binding';
  assert.equal(cdaNullableDirectSourceColumnBindings({ document: missingStableID, expectedCodeColumnName }).ok, false);
  const duplicateStableID = structuredClone(document);
  duplicateStableID.columns[1].columnId = duplicateStableID.columns[0].columnId;
  assert.equal(cdaNullableDirectSourceColumnBindings({ document: duplicateStableID, expectedCodeColumnName }).ok, false);
  const wrongPath = structuredClone(document);
  wrongPath.columns[1].source.field.path = 'valueQuantity.value';
  assert.equal(cdaNullableDirectSourceColumnBindings({ document: wrongPath, expectedCodeColumnName }).ok, false);
  const wrongProjection = structuredClone(document);
  wrongProjection.columns[1].source.field.projectionMode = 'ALL';
  assert.equal(cdaNullableDirectSourceColumnBindings({ document: wrongProjection, expectedCodeColumnName }).ok, false);
  const wrongType = structuredClone(document);
  wrongType.columns[1].logicalType = 'integer';
  assert.equal(cdaNullableDirectSourceColumnBindings({ document: wrongType, expectedCodeColumnName }).ok, false);
  const wrongOccurrence = structuredClone(document);
  wrongOccurrence.columns[1].occurrenceId = 'related-observation';
  assert.equal(cdaNullableDirectSourceColumnBindings({ document: wrongOccurrence, expectedCodeColumnName }).ok, false);
  const wrongSourceKind = structuredClone(document);
  wrongSourceKind.columns[1].source.kind = 'aggregate';
  assert.equal(cdaNullableDirectSourceColumnBindings({ document: wrongSourceKind, expectedCodeColumnName }).ok, false);
  const wrongRoot = structuredClone(document);
  wrongRoot.rootResourceType = 'DiagnosticReport';
  assert.equal(cdaNullableDirectSourceColumnBindings({ document: wrongRoot, expectedCodeColumnName }).ok, false);
  const duplicatePath = structuredClone(document);
  duplicatePath.columns.push(structuredClone(document.columns[1]));
  duplicatePath.columns[2].columnId = 'source-duplicate-code';
  assert.equal(cdaNullableDirectSourceColumnBindings({ document: duplicatePath, expectedCodeColumnName }).ok, false);
  assert.equal(cdaNullableDirectSourceColumnBindings({ document, expectedCodeColumnName: 'wrong-column-name' }).ok, false);
  const swappedNames = structuredClone(document);
  for (const column of swappedNames.columns) [column.columnId, column.column] = [column.column, column.columnId];
  assert.equal(cdaNullableDirectSourceColumnBindings({ document: swappedNames, expectedCodeColumnName }).ok, false);
  assert.equal(Object.hasOwn(document.columns[1].source.field, 'candidateId'), false,
    'Persisted Column.source.field records the chosen path and projection mode, not the catalog candidate ID.');
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
  assert.match(workflow, /cdaNullableNewExplorerIdentityEvidence\(\{ created, builder, project, explorer, title/,
    'the initial scope check must bind the API create summary without requiring a draft workspace');
  assert.match(workflow, /builderDraftStateEvidence\(builder, 'empty'\)/,
    'the fresh Builder state must prove NEW empty-draft CAS before the first command');
  assert.match(workflow, /if \(state\.workspace !== null\) assert\.equal\(state\.workspace\?\.explorer\?\.title, title/,
    'later initialized workspace states must retain the create-summary title');
  assert.match(workflow, /const existing = new Set\(\(builder\.workspace\?\.documents \?\? \[\]\)\.map/,
    'the first CREATE_TABLE must accept the supported null-workspace NEW state');
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
  assert.match(workflow, /cdaNullableDirectSourceColumnBindings\(\{[\s\S]*?document,[\s\S]*?expectedCodeColumnName: choiceResponse\.candidateColumnIds\[0\],[\s\S]*?\}\)/,
    'native raw-column resolution must validate the Builder document wire shape');
  assert.match(workflow, /entry\.path === `\$\{authoring\}\/construction-choice-proposals`/,
    'raw-column candidate selection must retain the native choice proposal');
  assert.match(workflow, /appliedChoice\.constructionChoice\.choiceId, choiceRequest\.constructionChoices\[0\]\.choiceId/,
    'Apply must use the exact choice token returned from the native field picker');
  assert.match(workflow, /candidateColumnIds: choiceResponse\.candidateColumnIds, savedColumnId: codeColumn\.columnId,[\s\S]*?savedPublicColumnName: codeColumn\.column/,
    'the proposal binds the public column name while Join references use the distinct stable source columnId');
  assert.match(workflow, /responsePaths: \/commands\|construction-choice-proposals\|construction-proposals\//,
    'native capture must retain choice proposal and Apply response bodies');
  assert.match(workflow, /leftSource\.idColumn\.columnId, leftSource\.codeColumn\.columnId,[\s\S]*?rightSource\.idColumn\.columnId, rightSource\.codeColumn\.columnId/,
    'native KEY_JOIN inputs must use the stable authored columnId, not the physical `column` name');
  assert.doesNotMatch(workflow, /(?:idColumn|codeColumn)\.id\b/,
    'raw workspace Column records do not use `id` as their stable identity');
  assert.match(workflow, /step\.outputs\.map\(output => output\.id\)/,
    'construction StageColumn output IDs retain their separate `id` wire contract');
  assert.match(workflow, /rawSourceBinding: sourceEvidence/,
    'the native proposal report must retain direct-source binding evidence');
  assert.doesNotMatch(workflow, /currentDraftSourceEvidence/,
    'the grouped-source contract must not be applied to these raw zero-step inputs');
  assert.match(workflow, /const builderPath = `\/api\/v1\/projects\/\$\{encode\(project\)\}\/explorers\/\$\{encode\(explorer\)\}\/authoring\/v2\/builder`/);
  assert.match(workflow, /expectedAPIOrigin: apiOrigin, expectedBuilderPath: builderPath, generation/);
  assert.match(workflow, /authorizationScopeDigest: initialScope\.authorizationScopeDigest/);
});
