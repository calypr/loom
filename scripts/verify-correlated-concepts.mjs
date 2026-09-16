import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { timingTarget } from './measure-authoring-loop.mjs';

assert.equal(process.argv.length, 3, 'usage: node scripts/verify-correlated-concepts.mjs OWNED_DEV_REPORT');
const target = timingTarget(JSON.parse(readFileSync(process.argv[2], 'utf8')));
const projectURL = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.project)}/explorers`;
async function json(url, body, status = 200) {
  const response = await fetch(url, {
    ...(body === undefined ? {} : { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  assert.equal(response.status, status, `${url}: ${JSON.stringify(value)}`);
  return value;
}
const owner = await json(projectURL, { name: `loom-dev-verification-concepts-${randomUUID()}`, title: 'Correlated concepts verification' }, 201);
const authoring = `${projectURL}/${encodeURIComponent(owner.explorerId)}/authoring/v2`;
let builder = await json(`${authoring}/builder`);
const concepts = builder.catalog.candidates.flatMap((candidate) => candidate.conceptCandidates ?? []);
for (const system of ['urn:study:A', 'urn:study:B']) {
  assert.ok(concepts.some((concept) => concept.system === system && concept.code === 'shared' && concept.logicalType === 'decimal'),
    `catalog must retain a typed shared-code concept for ${system}`);
}
async function command(commands) {
  const result = await json(`${authoring}/commands`, {
    commandId: randomUUID(), semanticsVersion: 3, snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await json(`${authoring}/builder`);
  return result;
}
const observation = builder.catalog.nodes.find((node) => node.resourceType === 'Observation');
assert.ok(observation, 'Observation fixture catalog is required');
const created = await command([{ type: 'CREATE_TABLE', title: 'Paired concepts', rootNodeId: observation.nodeId }]);
const outputId = created.results.find((result) => result.type === 'TABLE_CREATED').outputId;
for (const [kind, path, match] of [
  ['extensionByUrl', 'extension[].extension[]', 'urn:leaf'],
  ['observationComponentByCode', 'component[]', 'shared'],
  ['codingBySystem', 'code.coding[]', 'urn:study:A'],
]) {
  const rejected = await json(`${authoring}/commands`, {
    commandId: randomUUID(), semanticsVersion: 3, snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest,
    commands: [{ type: 'ADD_COLUMN_SOURCE', outputId, occurrenceId: 'base', title: 'Legacy ambiguous lookup', source: { kind, lookup: { path, match } } }],
  }, 422);
  assert.equal(rejected.error.code, 'INVALID_AUTHORING_COMMAND');
}
const binding = {
  ownerPath: 'component[]', keyPath: 'component[].code.coding[]', systemPath: 'system', codePath: 'code',
  valuePath: 'valueQuantity.value', logicalType: 'decimal', choiceArms: ['valueQuantity'], unitPath: 'valueQuantity.unit',
};
await command([
  { type: 'ADD_COLUMN_SOURCE', outputId, occurrenceId: 'base', title: 'Resource ID', source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } } },
  ...[['System A', 'urn:study:A', 'shared'], ['System B', 'urn:study:B', 'shared'], ['Cross coding', 'urn:study:B', 'other']].map(([title, system, code]) => ({
    type: 'ADD_COLUMN_SOURCE', outputId, occurrenceId: 'base', title,
    source: { kind: 'observationComponentByCode', lookup: { binding, key: { system, code }, projectionMode: 'ALL' } },
  })),
  ...[['Left extension', 'urn:parent:left'], ['Right extension', 'urn:parent:right'], ['Missing parent', 'urn:parent:missing']].map(([title, parent]) => ({
    type: 'ADD_COLUMN_SOURCE', outputId, occurrenceId: 'base', title,
    source: { kind: 'extensionByUrl', lookup: {
      extension: { ownerPath: 'extension[].extension[]', urlPath: [parent, 'urn:leaf'], valuePath: 'valueString', logicalType: 'string' },
      projectionMode: 'ALL',
    } },
  })),
  { type: 'ADD_COLUMN_SOURCE', outputId, occurrenceId: 'base', title: 'Wrong extension choice', source: {
    kind: 'extensionByUrl', lookup: {
      extension: { ownerPath: 'extension[].extension[]', urlPath: ['urn:parent:left', 'urn:leaf'], valuePath: 'valueQuantity.value', logicalType: 'decimal' },
      projectionMode: 'ALL',
    },
  } },
]);
const document = builder.workspace.documents.find((item) => item.output.id === outputId);
assert.equal(document.columns.length, 8);
const [id, systemA, systemB, cross, left, right, missing, wrong] = document.columns.map((column) => column.column);
const compiled = await json(`${authoring}/reconcile`, {
  snapshotToken: builder.catalog.snapshotToken, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest,
});
const started = performance.now();
const preview = await json(`${authoring}/preview`, { receiptId: compiled.receiptId, outputId, limit: 100 });
const previewMs = performance.now() - started;
const rows = new Map(preview.rows.map((row) => [row[id], row]));
assert.ok(rows.has('dev-pair-001'), 'preview must include the paired fixture');
assert.deepEqual(rows.get('dev-pair-001')[systemA], [111]);
assert.deepEqual(rows.get('dev-pair-001')[systemB], [222]);
assert.equal(rows.get('dev-pair-001')[cross], null);
assert.deepEqual(rows.get('dev-pair-002')[systemA], { status: 'INVALID_CHOICE_ARM', raw: ['not-numeric'] });
assert.equal(rows.get('dev-pair-003')[systemA], null);
assert.deepEqual(rows.get('dev-pair-001')[left], ['left-only']);
assert.deepEqual(rows.get('dev-pair-001')[right], ['right-only']);
assert.equal(rows.get('dev-pair-001')[missing], null);
assert.deepEqual(rows.get('dev-pair-001')[wrong], { status: 'INVALID_CHOICE_ARM', raw: ['left-only'] });
const path = join(process.cwd(), '.artifacts/loom-dev', `correlated-concepts-${Date.now()}.json`);
mkdirSync(dirname(path), { recursive: true });
writeFileSync(path, JSON.stringify({ target, explorerId: owner.explorerId, receiptId: compiled.receiptId, outputId, previewMs, preview }, null, 2));
console.log(JSON.stringify({ evidence: path, assertions: 13, previewMs }, null, 2));
