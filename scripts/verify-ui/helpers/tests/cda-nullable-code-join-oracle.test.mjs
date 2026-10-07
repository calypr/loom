import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { captureRequests } from '../cda-playwright.mjs';
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

test('native request capture uses report-array indexes and rejects wrong origins', async () => {
  const page = new EventEmitter();
  const report = { nativeRequests: [], errors: [] };
  const apiOrigin = 'http://127.0.0.1:8188';
  const ownedPathPrefix = '/api/v1/projects/loom_dev_cda_fhir/explorers/nullable-join';
  const capture = captureRequests(page, report, ownedPathPrefix, { apiOrigin, uiOrigin: apiOrigin });
  assert.equal(typeof capture.startIndex, 'undefined', 'the tracker exposes no startIndex method');

  const emitOwnedResponse = async ({ path, body, responseBody, match }) => {
    const fromIndex = report.nativeRequests.length;
    const request = {
      url: () => `${apiOrigin}${ownedPathPrefix}${path}`,
      method: () => 'POST',
      headers: () => ({ 'x-request-id': `request-${fromIndex}` }),
      postData: () => JSON.stringify(body),
      failure: () => null,
    };
    page.emit('request', request);
    const pending = capture.waitFor(match, { fromIndex, timeoutMs: 1_000 });
    page.emit('response', {
      request: () => request,
      status: () => 200,
      headers: () => ({ 'x-request-id': `response-${fromIndex}` }),
      text: async () => JSON.stringify(responseBody),
    });
    return { fromIndex, event: await pending };
  };

  const choiceBody = { commandId: 'choice-command', outputId: 'out-left', constructionChoices: [{ choiceId: 'choice-1' }] };
  const choiceResponseBody = { commandId: 'choice-command', outputId: 'out-left', candidateColumnIds: ['code-column'] };
  const choice = await emitOwnedResponse({ path: '/authoring/v2/construction-choice-proposals',
    body: choiceBody, responseBody: choiceResponseBody,
    match: entry => entry.path.endsWith('/construction-choice-proposals') && entry.body?.commandId === 'choice-command' });
  assert.equal(choice.fromIndex, 0);
  assert.equal(choice.event, report.nativeRequests[0]);
  assert.deepEqual(capture.rawRequestBody(choice.event), choiceBody);
  assert.deepEqual(capture.rawResponseBody(choice.event), choiceResponseBody);

  const wrongOriginRequest = {
    url: () => `http://127.0.0.1:9999${ownedPathPrefix}/authoring/v2/commands`,
    method: () => 'POST', headers: () => ({}), postData: () => '{"commandId":"wrong-origin"}',
  };
  page.emit('request', wrongOriginRequest);
  assert.equal(report.nativeRequests.length, 1, 'a same-path request from the wrong origin is ignored');

  const commandsMatch = entry => entry.path.endsWith('/commands') && entry.method === 'POST';
  const createBody = { commandId: 'create-command', commands: [{ type: 'CREATE_TABLE' }] };
  const createResponseBody = { commandId: 'create-command', results: [{ type: 'TABLE_CREATED' }] };
  const create = await emitOwnedResponse({ path: '/authoring/v2/commands', body: createBody,
    responseBody: createResponseBody, match: commandsMatch });
  assert.equal(create.fromIndex, 1);
  assert.equal(create.event, report.nativeRequests[1]);
  assert.deepEqual(capture.rawRequestBody(create.event), createBody);
  assert.deepEqual(capture.rawResponseBody(create.event), createResponseBody);

  const applyBody = { commandId: 'apply-command', commands: [{ type: 'APPLY_CONSTRUCTION_CHOICE', outputId: 'out-left' }] };
  const applyResponseBody = { commandId: 'apply-command', results: [{ type: 'COLUMN_ADDED', outputId: 'out-left' }] };
  const apply = await emitOwnedResponse({ path: '/authoring/v2/commands', body: applyBody,
    responseBody: applyResponseBody, match: commandsMatch });
  assert.equal(apply.fromIndex, 2, 'each action starts at the current report-array length');
  assert.equal(apply.event, report.nativeRequests[2], 'the same predicate resolves only the later request');
  assert.notEqual(apply.event, create.event, 'fromIndex excludes the earlier matching command');
  assert.deepEqual(capture.rawRequestBody(apply.event), applyBody);
  assert.deepEqual(capture.rawResponseBody(apply.event), applyResponseBody);

  await capture.flush();
  assert.equal(report.nativeRequests.length, 3);
  assert.deepEqual(report.errors, []);
});

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
  const project = 'loom_dev_cda_fhir';
  const explorer = 'cda-nullable-code-join-test';
  const generation = 'cda-fhir-v1';
  const uiOrigin = 'http://127.0.0.1:30008';
  const routePath = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/construction-proposals`;
  const currentDraft = { draftVersion: 12, draftDigest: 'draft-digest-12', catalog: {
    generation, snapshotToken: 'snapshot-1', authorizationScopeDigest: 'authorization-scope-1',
  } };
  const baselineDocument = { output: { id: outputId }, rootResourceType: 'Observation',
    route: { occurrenceId: 'base', resourceType: 'Observation' }, columns: [], rows: { kind: 'RECORDS', records: {} } };
  const targetCreateBase = { catalog: structuredClone(currentDraft.catalog), workspace: { documents: [baselineDocument] } };
  const rows = Array.from({ length: 25 }, (_, index) => ({ __loom_row_id: String(index + 1).padStart(64, '0') }));
  const rowSources = Array.from({ length: 25 }, (_, index) => ({
    id: `observation-${index + 1}`, kind: 'SINGLE', resourceType: 'Observation',
  }));
  const requestBody = { snapshotToken: 'snapshot-1', expectedDraftVersion: 12,
    expectedDraftDigest: 'draft-digest-12', outputId, limit: 25,
    candidateConstruction: { version: 1, steps: [] } };
  const responseBody = { proposalId: 'nullable-removal-proposal', outputId,
    snapshotToken: 'snapshot-1', draftVersion: 12, draftDigest: 'draft-digest-12', previewStatus: 'READY',
    candidateConstruction: { version: 1, steps: [] },
    preview: { receiptId: 'nullable-removal-proposal', outputId, columns: [], rows, rowSources,
      rowCount: 25, sampled: true } };
  const proposal = { event: { origin: uiOrigin, path: routePath, method: 'POST', status: 200 }, requestBody, responseBody };
  const restorationEvidence = { ok: true, emptyRoot: true, unchanged: true, emptyConstructionNormalized: true };
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
    footerText: 'Showing 25 preview rows. Full-output coverage is unavailable before publication.',
    tableCount: 0,
  };
  const evidence = cdaNullableEmptyRemovalPreviewEvidence({ proposal, outputId, targetCreateBase, baselineDocument, currentDraft,
    restorationEvidence, project, explorer, generation, uiOrigin, dom });
  assert.equal(evidence.ok, true);
  assert.deepEqual(evidence.checks, {
    readyResponse: true,
    catalogScopeBound: true,
    routeBound: true,
    draftCASBound: true,
    receiptBound: true,
    outputBound: true,
    baselineDocumentBound: true,
    candidateRestoresBaseline: true,
    emptyVisibleSchema: true,
    emptyCandidateConstruction: true,
    rowSampleBound: true,
    readyDOM: true,
    emptyStatus: true,
    sampledFooter: true,
    tableAbsent: true,
  });
  assert.equal(evidence.responsePreview.sampled, true);
  assert.equal(evidence.responsePreview.rowCount, 25);
  assert.equal(evidence.responsePreview.rows.length, 25);
  assert.equal(evidence.responsePreview.rowSources.length, 25);

  const negative = (changes = {}) => cdaNullableEmptyRemovalPreviewEvidence({
    proposal: changes.proposal ?? proposal,
    outputId: changes.outputId ?? outputId,
    targetCreateBase: changes.targetCreateBase ?? targetCreateBase,
    baselineDocument: changes.baselineDocument ?? baselineDocument,
    currentDraft: changes.currentDraft ?? currentDraft,
    restorationEvidence: changes.restorationEvidence ?? restorationEvidence,
    project: changes.project ?? project,
    explorer: changes.explorer ?? explorer,
    generation: changes.generation ?? generation,
    uiOrigin: changes.uiOrigin ?? uiOrigin,
    dom: changes.dom ?? dom,
  });
  const withRequest = change => ({ ...proposal, requestBody: { ...requestBody, ...change } });
  const withResponse = change => ({ ...proposal, responseBody: { ...responseBody, ...change } });
  const withPreview = change => withResponse({ preview: { ...responseBody.preview, ...change } });
  assert.equal(negative({ outputId: 'wrong-output' }).ok, false);
  assert.equal(negative({ proposal: withResponse({ proposalId: 'wrong-proposal' }) }).ok, false);
  assert.equal(negative({ proposal: { ...proposal, event: { ...proposal.event, origin: 'http://wrong-origin' } } }).ok, false);
  assert.equal(negative({ proposal: { ...proposal, event: { ...proposal.event, path: routePath.replace(project, 'other-project') } } }).ok, false);
  assert.equal(negative({ proposal: withRequest({ outputId: 'wrong-output' }) }).ok, false);
  assert.equal(negative({ proposal: withRequest({ snapshotToken: 'wrong-snapshot' }) }).ok, false);
  assert.equal(negative({ proposal: withResponse({ draftVersion: 11 }) }).ok, false);
  assert.equal(negative({ proposal: withResponse({ draftDigest: 'wrong-digest' }) }).ok, false);
  assert.equal(negative({ currentDraft: { ...currentDraft, catalog: { ...currentDraft.catalog, generation: 'wrong-generation' } } }).ok, false);
  assert.equal(negative({ currentDraft: { ...currentDraft, catalog: { ...currentDraft.catalog, authorizationScopeDigest: '' } } }).ok, false);
  assert.equal(negative({ targetCreateBase: { ...targetCreateBase, catalog: { ...targetCreateBase.catalog, snapshotToken: 'wrong-snapshot' } } }).ok, false);
  assert.equal(negative({ targetCreateBase: { ...targetCreateBase, workspace: { documents: [] } } }).ok, false);
  assert.equal(negative({ baselineDocument: { ...baselineDocument, columns: [{ column: 'visible' }] } }).ok, false);
  assert.equal(negative({ restorationEvidence: { ...restorationEvidence, unchanged: false } }).ok, false);
  assert.equal(negative({ proposal: withResponse({ candidateConstruction: { version: 1, steps: [{ id: 'unexpected' }] } }) }).ok, false);
  assert.equal(negative({ proposal: withPreview({ columns: [{ column: 'unexpected' }] }) }).ok, false);
  assert.equal(negative({ proposal: withPreview({ rowCount: 24 }) }).ok, false);
  assert.equal(negative({ proposal: withRequest({ limit: 24 }) }).ok, false);
  assert.equal(negative({ proposal: withPreview({ sampled: false }) }).ok, false);
  assert.equal(negative({ proposal: withPreview({ rows: [{ __loom_row_id: 'not-a-sha256' }, ...rows.slice(1)] }) }).ok, false);
  assert.equal(negative({ proposal: withPreview({ rows: [rows[0], ...rows.slice(1, -1), rows[0]] }) }).ok, false);
  assert.equal(negative({ proposal: withPreview({ rows: [{ ...rows[0], visible: 'unexpected' }, ...rows.slice(1)] }) }).ok, false);
  assert.equal(negative({ proposal: withPreview({ rowSources: [{ id: 'source', kind: 'SINGLE', resourceType: 'DiagnosticReport' }, ...rowSources.slice(1)] }) }).ok, false);
  assert.equal(negative({ dom: { ...dom, statusText: 'Loom did not return preview rows for this proposal.' } }).ok, false);
  assert.equal(negative({ dom: { ...dom, footerText: 'Showing 24 preview rows.' } }).ok, false);
  assert.equal(negative({ dom: { ...dom, tableCount: 1 } }).ok, false);
  assert.equal(negative({ dom: { ...dom, resultReceiptId: 'stale-receipt' } }).ok, false);
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
  assert.match(workflow, /target = await makeTarget\(\);[\s\S]*?const initialTargetDocument = structuredClone\(target\.baselineDocument\)[\s\S]*?target = await makeTarget\(false\);/,
    'the scratch target records the native target-binding proof, then Cancel creates a fresh target for the Apply lifecycle');
  const removalBlock = workflow.slice(workflow.indexOf('const removalProposal'), workflow.indexOf('target = await makeTarget(false)'));
  assert.match(removalBlock, /readProposalPreviewState\(page, target\.outputId\)/,
    'removal reads the rendered empty preview state, whose zero-column layout has no table');
  assert.match(removalBlock, /cdaNullableEmptyRemovalPreviewEvidence\(\{ proposal, outputId: target\.outputId,[\s\S]*?targetCreateBase: target\.createBase, baselineDocument: target\.baselineDocument,[\s\S]*?currentDraft: baseState, restorationEvidence: removalEvidence,[\s\S]*?project, explorer, generation, uiOrigin, dom \}\)/,
    'removal binds the create-time baseline, current-draft scope/CAS, response sample, and native status DOM');
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
  assert.match(workflow, /responsePaths: \/commands\|construction-choice-proposals\|construction-proposals\|construction-capabilities\|preview\//,
    'native capture must retain choice, proposal, capability, and preview response bodies for exact successor binding');
  assert.match(workflow, /const ownerSwitchCancellationScopes = async/,
    'only the two observed owner switches may arm scoped cancellation classification');
  const switchScopes = workflow.slice(workflow.indexOf('const ownerSwitchCancellationScopes'), workflow.indexOf('const addDirectCodeColumn'));
  assert.match(switchScopes, /method: 'POST',[\s\S]*?paths: \[capabilitiesPath\],[\s\S]*?requestIdPrefixes: \['cda-request-'\]/,
    'outgoing construction-capability aborts must be classified by exact POST path and request prefix');
  assert.match(switchScopes, /method: 'GET',[\s\S]*?paths: \[selectionPath\],[\s\S]*?requestIdPrefixes: \['cda-request-'\]/,
    'outgoing selection aborts must be classified by exact revision path and request prefix');
  assert.equal((workflow.match(/ownerSwitchCancellationScopes\(\{/g) ?? []).length, 2,
    'only left-to-right and right-to-Combine native owner transitions receive cancellation scopes');
  assert.match(workflow, /action: \{ id: actionRecord\.id, label: actionRecord\.label, locator: actionRecord\.locator \}/,
    'the classifier must bind the specific successful action ID when labels repeat');
  assert.match(workflow, /validateNullableJoinSwitch\(report, {/,
    'raw network cancellations must pass the exact scoped owner and successor proof validator');
  assert.doesNotMatch(workflow, /capture\.startIndex\s*\(/,
    'request boundaries use the capture helper report array, which has no startIndex method');
  assert.equal((workflow.match(/report\.nativeRequests\.length/g) ?? []).length, 5,
    'every native request wait starts from the actual report-array boundary');
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
