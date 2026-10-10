import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { expectedRelatedSourceOneValidation, selectedRelatedSourceProposal } from '../related-source-capture.mjs';

const expected = {
  outputId: 'output-cda', candidateId: 'observation-id-candidate', nodeId: 'observation-node',
  choiceId: 'signed-observation-id-choice', snapshotToken: 'sha256:fixture-snapshot',
  resourceType: 'Observation', path: 'id', routeTypes: ['Specimen', 'Patient', 'Observation'],
};
const requestFixture = {
  path: '/api/v1/projects/project/explorers/explorer/authoring/v2/construction-proposals', status: 422,
  request: {
    outputId: expected.outputId, changedStepId: 'related-source-step', snapshotToken: expected.snapshotToken,
    candidateConstruction: { steps: [{ id: 'related-source-step', operation: { kind: 'RELATED_SOURCE', relatedSource: {
      source: { candidateId: expected.candidateId, nodeId: expected.nodeId, resourceType: expected.resourceType, path: expected.path },
      choiceId: expected.choiceId, route: [
        { fromResourceType: 'Specimen', toResourceType: 'Patient' },
        { fromResourceType: 'Patient', toResourceType: 'Observation' },
      ], form: 'ALL', contributorRule: { policy: 'ALL_MATCHES' }, rowValuePolicy: 'ONE',
    } } }] },
  },
  response: { error: { code: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' } },
};
const relatedStep = (entry) => entry.request.candidateConstruction.steps.find((step) => step.operation?.kind === 'RELATED_SOURCE');

const verifyExactClassifier = (entry, scope) => {
  const selected = selectedRelatedSourceProposal(entry, scope);
  assert(selected, 'The selected RELATED_SOURCE request must match its exact scope');
  assert.equal(selected.step.id, entry.request.changedStepId);
  assert.equal(selected.rowValuePolicy, 'ONE');
  assert.equal(expectedRelatedSourceOneValidation(entry, scope), true);

  const rejected = (mutateEntry, mutateScope = (value) => value) => {
    const changedEntry = structuredClone(entry);
    const changedScope = structuredClone(scope);
    mutateEntry(changedEntry);
    mutateScope(changedScope);
    assert.equal(expectedRelatedSourceOneValidation(changedEntry, changedScope), false);
  };
  rejected((item) => { item.path = '/construction-choice-proposals'; });
  rejected((item) => { item.request.outputId = 'other-output'; });
  rejected((item) => { item.request.changedStepId = 'other-step'; });
  rejected((item) => { item.request.snapshotToken = 'other-snapshot'; });
  rejected((item) => { relatedStep(item).operation.relatedSource.rowValuePolicy = 'ALL'; });
  rejected((item) => { relatedStep(item).operation.relatedSource.form = 'FIRST'; });
  rejected((item) => { relatedStep(item).operation.relatedSource.contributorRule.policy = 'FIRST_MATCH'; });
  rejected((item) => { item.response.error.code = 'UNRELATED_ERROR'; });
  rejected((item) => { item.status = 200; });
  rejected((item) => { delete item.response.error; item.response.diagnostics = { severity: 'error', code: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' }; });
  rejected((item) => { item.request.candidateConstruction.steps = { 0: relatedStep(item) }; });
  rejected((item) => { relatedStep(item).operation.relatedSource.route = [null, null]; });
  rejected(() => {}, (value) => { value.candidateId = 'other-candidate'; });
  rejected(() => {}, (value) => { value.nodeId = 'other-node'; });
  rejected(() => {}, (value) => { value.choiceId = 'other-choice'; });
  rejected(() => {}, (value) => { value.routeTypes = ['Specimen', 'Observation']; });
  rejected(() => {}, (value) => { delete value.resourceType; });
  rejected(() => {}, (value) => { delete value.path; });
  assert.equal(expectedRelatedSourceOneValidation(entry, {}), false, 'Missing identity evidence must fail closed');

  const diagnosticOnly = structuredClone(entry);
  delete diagnosticOnly.response.error;
  diagnosticOnly.response.diagnostics = [{ severity: 'error', code: 'CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' }];
  assert.equal(expectedRelatedSourceOneValidation(diagnosticOnly, scope), true,
    'A lowercase diagnostic-only error response must retain the exact expected validation classification');
}

verifyExactClassifier(requestFixture, expected);

if (process.argv[2]) {
  const report = JSON.parse(await readFile(process.argv[2], 'utf8'));
  const entry = report.nativeRequests.find((request) => request.status === 422 && request.path.endsWith('/construction-proposals'));
  assert(entry, 'The retained native run must contain its captured 422 request');
  const related = relatedStep(entry).operation.relatedSource;
  const actualScope = {
    outputId: entry.request.outputId, candidateId: related.source.candidateId, nodeId: related.source.nodeId,
    choiceId: related.choiceId, snapshotToken: entry.request.snapshotToken, resourceType: 'Observation',
    path: 'id', routeTypes: ['Specimen', 'Patient', 'Observation'],
  };
  verifyExactClassifier(entry, actualScope);
  assert.deepEqual(related.route.map((hop) => [hop.fromResourceType, hop.toResourceType]), [
    ['Specimen', 'Patient'], ['Patient', 'Observation'],
  ]);
  const builder = report.savedBuilderAtFailure;
  const nodeTypes = new Map(builder.catalog.nodes.map((node) => [node.nodeId, node.resourceType]));
  const idCandidates = builder.catalog.candidates.filter((candidate) =>
    nodeTypes.get(candidate.nodeId) === 'Observation' && candidate.fieldPath === 'id');
  assert(idCandidates.some((candidate) => candidate.candidateId === actualScope.candidateId &&
    candidate.nodeId === actualScope.nodeId && candidate.logicalType === 'string' && candidate.cardinality === 'optional_one' &&
    (candidate.repeatedBoundaries ?? []).length === 0 && candidate.projectionModes.includes('VALUE')),
  'The native proposal candidate must match the authorized Observation.id scalar catalog candidate');
}

process.stdout.write('related-source ONE validation scope: fixture and fail-closed identity checks passed\n');
