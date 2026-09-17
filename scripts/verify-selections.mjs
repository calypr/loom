import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { timingTarget } from './measure-authoring-loop.mjs';

const checkSaved = process.argv[3] === '--check-saved';
assert.ok(process.argv.length === 3 || (process.argv.length === 4 && checkSaved), 'usage: node scripts/verify-selections.mjs OWNED_DEV_REPORT | SELECTION_EVIDENCE --check-saved');
const input = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const target = timingTarget(input);
const projectURL = `${target.apiUrl}/api/v1/projects/${encodeURIComponent(target.project)}/explorers`;
const evidence = { target, assertions: [], timings: {} };

async function json(url, body, expected = 200) {
  const response = await fetch(url, {
    ...(body === undefined ? {} : { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  assert.equal(response.status, expected, `${url}: ${JSON.stringify(value)}`);
  return value;
}

if (checkSaved) {
  assert.match(input.explorerId, /^loom-dev-verification-selection-[a-z0-9-]+$/);
  for (const [name, selection] of Object.entries(input.selections)) {
    const actual = [];
    let cursor;
    do {
      const query = new URLSearchParams({ limit: '1', ...(cursor ? { cursor } : {}) });
      const page = await json(`${projectURL}/${encodeURIComponent(input.explorerId)}/selections/${encodeURIComponent(selection.id)}?${query}`);
      assert.equal(page.revision.membershipDigest, selection.membershipDigest);
      assert.equal(page.revision.memberCount, selection.memberCount);
      assert.equal(page.revision.complete, true);
      actual.push(...page.members.map((member) => member.ref));
      assert.ok(actual.length <= selection.memberCount, 'saved pagination repeated membership');
      cursor = page.nextCursor;
    } while (cursor);
    assert.deepEqual(actual.sort((a, b) => a.id.localeCompare(b.id)), name === 'empty' ? [] : input.expectedRefs);
  }
  console.log(JSON.stringify({ savedSelectionCheck: 'passed', selections: Object.keys(input.selections), evidence: process.argv[2] }));
  process.exit(0);
}

const owner = await json(projectURL, { name: `loom-dev-verification-selection-${randomUUID()}`, title: 'Selection verification' }, 201);
const base = `${projectURL}/${encodeURIComponent(owner.explorerId)}`;
const authoring = `${base}/authoring/v2`;
let builder = await json(`${authoring}/builder`);
const filesNode = builder.catalog.nodes.find((node) => node.resourceType === 'DocumentReference');
assert.ok(filesNode, 'hostile fixture DocumentReference catalog is missing');

async function command(commands) {
  const result = await json(`${authoring}/commands`, {
    commandId: randomUUID(), semanticsVersion: 3, snapshotToken: builder.catalog.snapshotToken,
    expectedDraftVersion: builder.draftVersion, expectedDraftDigest: builder.draftDigest, commands,
  });
  builder = await json(`${authoring}/builder`);
  return result;
}

const created = await command([{ type: 'CREATE_TABLE', title: 'Files', rootNodeId: filesNode.nodeId }]);
const outputId = created.results.find((result) => result.type === 'TABLE_CREATED').outputId;
await command([{
  type: 'ADD_COLUMN_SOURCE', outputId, occurrenceId: 'base', title: 'File ID',
  source: { kind: 'field', field: { path: 'id', projectionMode: 'VALUE' } },
}]);
const idColumn = builder.workspace.documents.find((document) => document.output.id === outputId).columns[0].column;

async function publish() {
  const compiled = await json(`${authoring}/reconcile`, {
    snapshotToken: builder.catalog.snapshotToken, draftVersion: builder.draftVersion, draftDigest: builder.draftDigest,
  });
  return json(`${authoring}/publish`, { receiptId: compiled.receiptId });
}

const firstPublication = await publish();
const ref = (id) => ({ project: target.project, generation: builder.catalog.generation, resourceType: 'DocumentReference', id });
const selectedIDs = ['dev-file-001', 'dev-file-002', 'dev-file-004'];
const request = {
  snapshotToken: builder.catalog.snapshotToken, idempotencyKey: randomUUID(),
  source: { kind: 'resources', resources: { refs: selectedIDs.map(ref) } },
};
const started = performance.now();
const explicit = await json(`${base}/selections`, request, 201);
const expectedRefs = selectedIDs.map((id) => ({ ...ref(id), project: target.project.replace('-', '/') }));
evidence.timings.explicit_ms = performance.now() - started;
assert.equal(explicit.complete, true);
assert.equal(explicit.memberCount, 3);
assert.equal((await json(`${base}/selections`, request, 201)).id, explicit.id);
evidence.assertions.push('explicit selection contains two linked files and one unlinked file and replays idempotently');

async function members(selection) {
  const refs = [];
  let cursor;
  do {
    const query = new URLSearchParams({ limit: '1', ...(cursor ? { cursor } : {}) });
    const page = await json(`${base}/selections/${encodeURIComponent(selection.id)}?${query}`);
    assert.equal(page.revision.membershipDigest, selection.membershipDigest);
    assert.equal(page.revision.complete, true);
    refs.push(...page.members.map((member) => member.ref));
    cursor = page.nextCursor;
    assert.ok(refs.length <= selection.memberCount, 'selection pagination repeated a member');
  } while (cursor);
  return refs.sort((a, b) => a.id.localeCompare(b.id));
}

assert.deepEqual(await members(explicit), expectedRefs);
const matching = await json(`${base}/selections`, {
  snapshotToken: builder.catalog.snapshotToken, idempotencyKey: randomUUID(),
  source: { kind: 'publishedOutput', publishedOutput: {
    revisionId: firstPublication.revisionId, outputId,
    filters: [{ column: idColumn, op: 'IN', value: selectedIDs }],
  } },
}, 201);
assert.equal(matching.source.revisionId, firstPublication.revisionId);
assert.deepEqual(await members(matching), expectedRefs);
assert.equal(matching.membershipDigest, explicit.membershipDigest);
evidence.assertions.push('all-matching frozen publication produces exactly the same typed membership');

const empty = await json(`${base}/selections`, {
  snapshotToken: builder.catalog.snapshotToken, idempotencyKey: randomUUID(),
  source: { kind: 'resources', resources: { resourceType: 'DocumentReference', refs: [] } },
}, 201);
assert.equal(empty.memberCount, 0);
assert.deepEqual(await members(empty), []);
evidence.assertions.push('empty typed selection is complete and has no members');

await command([{ type: 'RENAME_TABLE', outputId, title: 'New publication' }]);
const secondPublication = await publish();
assert.notEqual(secondPublication.revisionId, firstPublication.revisionId);
assert.deepEqual(await members(matching), expectedRefs);
evidence.assertions.push('later publication does not alter saved membership');

const stale = await json(`${base}/selections`, {
  ...request, idempotencyKey: randomUUID(),
  source: { kind: 'resources', resources: { refs: [{ ...ref('dev-file-001'), generation: 'stale-generation' }] } },
}, 409);
assert.ok(stale.error.code);
const foreign = await json(`${base}/selections`, {
  ...request, idempotencyKey: randomUUID(),
  source: { kind: 'resources', resources: { refs: [{ ...ref('dev-file-001'), project: 'another-project' }] } },
}, 409);
assert.ok(foreign.error.code);
evidence.assertions.push('stale generation and foreign project references are rejected rather than rewritten');

evidence.explorerId = owner.explorerId;
evidence.selections = { explicit, matching, empty };
evidence.expectedRefs = expectedRefs;
const path = join(process.cwd(), '.artifacts/loom-dev', `selections-${Date.now()}.json`);
mkdirSync(dirname(path), { recursive: true });
writeFileSync(path, JSON.stringify(evidence, null, 2));
console.log(JSON.stringify({ evidence: path, assertions: evidence.assertions, timings: evidence.timings }, null, 2));
