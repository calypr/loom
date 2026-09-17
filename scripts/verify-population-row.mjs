import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { timingTarget } from './measure-authoring-loop.mjs';

assert.equal(process.argv.length, 3, 'usage: node scripts/verify-population-row.mjs SELECTION_EVIDENCE');
const selectionEvidence = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const target = timingTarget(selectionEvidence);
const projectURL = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.project)}/explorers`;
const explorerId = selectionEvidence.explorerId;
const authoring = `${projectURL}/${encodeURIComponent(explorerId)}/authoring/v2`;

async function json(url, body, expected = 200) {
  const response = await fetch(url, {
    ...(body === undefined ? {} : {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    }),
    signal: AbortSignal.timeout(30_000),
  });
  const value = await response.json();
  assert.equal(response.status, expected, `${url}: ${JSON.stringify(value)}`);
  return value;
}

let builder = await json(`${authoring}/builder`);

async function command(commands) {
  const result = await json(`${authoring}/commands`, {
    commandId: randomUUID(),
    semanticsVersion: 3,
    snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion,
    expectedDraftDigest: builder.draftDigest,
    commands,
  });
  builder = await json(`${authoring}/builder`);
  return result;
}

async function preview(outputId) {
  const compiled = await json(`${authoring}/reconcile`, {
    snapshotToken: builder.catalog.snapshotToken,
    draftVersion: builder.draftVersion,
    draftDigest: builder.draftDigest,
  });
  const result = await json(`${authoring}/preview`, { receiptId: compiled.receiptId, outputId, limit: 100 });
  return { compiled, result };
}

const specimenNode = builder.catalog.nodes.find((node) => node.resourceType === 'Specimen' && node.rowRootEligible);
const documentNode = builder.catalog.nodes.find((node) => node.resourceType === 'DocumentReference');
assert.ok(specimenNode, 'fixture catalog has no row-eligible Specimen node');
assert.ok(documentNode, 'fixture catalog has no DocumentReference node');
const populationEdge = builder.catalog.edges.find((edge) => (
  edge.fromNodeId === specimenNode.nodeId
  && edge.toNodeId === documentNode.nodeId
  && edge.populated !== false
));
assert.ok(populationEdge, 'fixture catalog has no populated Specimen-to-DocumentReference relationship');

const created = await command([{ type: 'CREATE_TABLE', title: 'Selected specimens', rootNodeId: specimenNode.nodeId }]);
const outputId = created.results.find((result) => result.type === 'TABLE_CREATED')?.outputId;
assert.ok(outputId, 'CREATE_TABLE did not return an output ID');
await command([{
  type: 'ADD_COLUMN_SOURCE',
  outputId,
  occurrenceId: 'base',
  title: 'Specimen ID',
  source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
}]);
const idColumn = builder.workspace.documents.find((document) => document.output.id === outputId)?.columns[0]?.column;
assert.ok(idColumn, 'Specimen ID column was not authored');

await command([{
  type: 'SET_TABLE_POPULATION',
  outputId,
  selectionRevisionId: selectionEvidence.selections.explicit.id,
  edgeIds: [populationEdge.edgeId],
}]);
const selected = await preview(outputId);
assert.deepEqual(selected.result.rows.map((row) => row[idColumn]), ['dev-specimen-001']);
assert.equal(selected.result.rows.length, 1, 'two files for one specimen must not duplicate the specimen row');
const coverage = await json(`${authoring}/population-mapping`, {
  receiptId: selected.compiled.receiptId,
  outputId,
  limit: 10,
});
assert.equal(coverage.status, 'COMPLETE');
assert.deepEqual(coverage.counts, { selected: 3, mapped: 2, unmapped: 1, emittedRows: 1 });
assert.deepEqual(coverage.unmapped.map((ref) => ref.id), ['dev-file-004']);

await command([{ type: 'CLEAR_TABLE_POPULATION', outputId }]);
const unrestricted = await preview(outputId);
assert.deepEqual(
  unrestricted.result.rows.map((row) => row[idColumn]).sort(),
  ['dev-specimen-001', 'dev-specimen-002'],
  'clearing the population must restore every authorized Specimen row',
);
assert.notEqual(unrestricted.compiled.receiptId, selected.compiled.receiptId, 'population membership must participate in receipt identity');

await command([{
  type: 'SET_TABLE_POPULATION',
  outputId,
  selectionRevisionId: selectionEvidence.selections.empty.id,
  edgeIds: [populationEdge.edgeId],
}]);
const empty = await preview(outputId);
assert.deepEqual(empty.result.rows, [], 'a complete empty selection must produce zero rows');
assert.notEqual(empty.compiled.receiptId, selected.compiled.receiptId, 'different membership digests must produce different receipts');

await command([{ type: 'CLEAR_TABLE_POPULATION', outputId }]);

const artifact = {
  target,
  explorerId,
  outputId,
  relationship: populationEdge.label,
  selected: { receiptId: selected.compiled.receiptId, rows: selected.result.rows },
  coverage,
  unrestricted: { receiptId: unrestricted.compiled.receiptId, rows: unrestricted.result.rows },
  empty: { receiptId: empty.compiled.receiptId, rows: empty.result.rows },
};
const path = join(process.cwd(), '.artifacts/loom-dev', `population-row-${Date.now()}.json`);
mkdirSync(dirname(path), { recursive: true });
writeFileSync(path, JSON.stringify(artifact, null, 2));
console.log(JSON.stringify({
  evidence: path,
  assertions: [
    'two selected files map to one deduplicated Specimen row',
    'selected=3, mapped=2, unmapped=1, emittedRows=1 and only file 004 is returned',
    'no population preserves the resource-first workflow',
    'an empty immutable selection produces zero rows',
    'population membership changes receipt identity',
  ],
}, null, 2));
