import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import test from 'node:test';
import {
  currentPreviewIndexIdentity,
  summarizeExplainResponse,
  validateExplainInputs,
  validateOwnedArangoContainer,
} from './lib/local-preview-index-explain.mjs';

const sha256 = (value) => createHash('sha256').update(value).digest('hex');
const canonicalJSON = (value) => {
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(',')}]`;
  if (value && typeof value === 'object') {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(',')}}`;
  }
  return JSON.stringify(value);
};

async function createArtifacts(t) {
  const dir = await mkdtemp(join(tmpdir(), 'loom-preview-index-explain-'));
  t.after(async () => rm(dir, { recursive: true, force: true }));
  const queryPath = join(dir, 'current-query.json');
  const query = `FOR root IN @@root_collection OPTIONS { indexHint: "${currentPreviewIndexIdentity.name}", forceIndexHint: false } FILTER root.project == @project RETURN root._key`;
  const bindVars = {
    '@root_collection': currentPreviewIndexIdentity.collection,
    dataset_generation: currentPreviewIndexIdentity.generation,
    project: currentPreviewIndexIdentity.project,
  };
  const queryBytes = Buffer.from(`${JSON.stringify({ query, bindVars }, null, 2)}\n`);
  await writeFile(queryPath, queryBytes);
  const spec = {
    collection: currentPreviewIndexIdentity.collection,
    name: currentPreviewIndexIdentity.name,
    fields: currentPreviewIndexIdentity.fields,
    storedValues: currentPreviewIndexIdentity.storedValues,
    fingerprint: {
      recompiledQueryFile: queryPath,
      recompiledQueryFileSha256: sha256(queryBytes),
      recompiledAqlSha256: sha256(query),
      recompiledBindVarsSha256: sha256(canonicalJSON(bindVars)),
      project: currentPreviewIndexIdentity.project,
      generation: currentPreviewIndexIdentity.generation,
      currentProductionSource: {
        verification: 'go-run-wrapper-captured-source-closure-before-compilation-and-verified-after-start',
        sha256: 'a'.repeat(64),
      },
    },
  };
  return { dir, queryPath, queryBytes, query, bindVars, spec, specText: `${JSON.stringify(spec, null, 2)}\n` };
}

test('current query proof verifies the exact compiler AQL, binds, and file bytes', async (t) => {
  const fixture = await createArtifacts(t);
  const evidence = validateExplainInputs(fixture.specText, fixture.queryBytes, fixture.queryPath);
  assert.equal(evidence.aqlSha256, fixture.spec.fingerprint.recompiledAqlSha256);
  assert.equal(evidence.bindVarsSha256, fixture.spec.fingerprint.recompiledBindVarsSha256);
  assert.equal(evidence.queryFileSha256, fixture.spec.fingerprint.recompiledQueryFileSha256);
  assert.equal(evidence.project, currentPreviewIndexIdentity.project);
  assert.equal(evidence.generation, currentPreviewIndexIdentity.generation);
});

test('current query proof rejects altered AQL, altered bindVars, and a different query path', async (t) => {
  const fixture = await createArtifacts(t);
  const alteredQuery = Buffer.from(JSON.stringify({ query: `${fixture.query} `, bindVars: fixture.bindVars }));
  assert.throws(() => validateExplainInputs(fixture.specText, alteredQuery, fixture.queryPath), /query-file SHA256/);

  const changedBinds = { ...fixture.bindVars, project: 'another-project' };
  const changedBindsBytes = Buffer.from(JSON.stringify({ query: fixture.query, bindVars: changedBinds }));
  const changedFileProof = {
    ...fixture.spec,
    fingerprint: { ...fixture.spec.fingerprint, recompiledQueryFileSha256: sha256(changedBindsBytes) },
  };
  assert.throws(
    () => validateExplainInputs(JSON.stringify(changedFileProof), changedBindsBytes, fixture.queryPath),
    /bindVars SHA256/,
  );
  assert.throws(() => validateExplainInputs(fixture.specText, fixture.queryBytes, resolve(fixture.queryPath, '..', 'other.json')), /query file path/);
});

test('owned Arango validation requires the exact no-auth development container', () => {
  const container = {
    Name: '/loom-dev-6d7df93d6a37-arangodb-1',
    State: { Running: true },
    Config: {
      Labels: {
        'com.docker.compose.project': 'loom-dev-6d7df93d6a37',
        'com.docker.compose.service': 'arangodb',
      },
      Env: ['ARANGO_NO_AUTH=1'],
    },
  };
  assert.doesNotThrow(() => validateOwnedArangoContainer(container));
  assert.throws(() => validateOwnedArangoContainer({ ...container, Name: '/other-arango' }), /unexpected Arango container/);
  assert.throws(() => validateOwnedArangoContainer({ ...container, Config: { ...container.Config, Env: [] } }), /no-auth development configuration/);
});

test('EXPLAIN summary reports the candidate index selection without carrying query or bind values', () => {
  const response = {
    plan: {
      estimatedCost: 15.5,
      estimatedNrItems: 42,
      nodes: [
        {
          id: 7,
          type: 'IndexNode',
          collection: 'Observation',
          indexes: [{ id: 'Observation/77', name: currentPreviewIndexIdentity.name, type: 'persistent', fields: currentPreviewIndexIdentity.fields }],
        },
      ],
    },
    warnings: [],
  };
  const summary = summarizeExplainResponse(response, currentPreviewIndexIdentity.name);
  assert.equal(summary.candidateIndexSelected, true);
  assert.deepEqual(summary.candidateIndexUses, [{
    plan: 0, nodeId: 7, nodeType: 'IndexNode', collection: 'Observation', id: 'Observation/77',
    name: currentPreviewIndexIdentity.name, type: 'persistent', fields: currentPreviewIndexIdentity.fields,
  }]);
  assert.equal('query' in summary, false);
  assert.equal('bindVars' in summary, false);

  const notSelected = summarizeExplainResponse({ plan: { nodes: [{ type: 'EnumerateCollectionNode', collection: 'Observation' }] } }, currentPreviewIndexIdentity.name);
  assert.equal(notSelected.candidateIndexSelected, false);
});
