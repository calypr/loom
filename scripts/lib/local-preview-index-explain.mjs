import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { resolve } from 'node:path';

export const currentPreviewIndexIdentity = Object.freeze({
  collection: 'Observation',
  name: 'loom_pivot_preview_cdeb305dcc142386',
  fields: ['project', 'dataset_generation', 'auth_resource_path', '_key', 'payload.id', 'payload.status'],
  storedValues: ['payload.valueQuantity'],
  project: 'loom_dev_cda_fhir',
  generation: 'cda-fhir-v1',
});

const sha256 = (value) => createHash('sha256').update(value).digest('hex');

function canonicalJSON(value) {
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(',')}]`;
  if (value && typeof value === 'object') {
    const keys = Object.keys(value).sort();
    return `{${keys.map((key) => `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(',')}}`;
  }
  return JSON.stringify(value);
}

export function validateExplainInputs(specText, queryBytes, queryPath) {
  const spec = JSON.parse(specText);
  const fingerprint = spec.fingerprint ?? {};
  assert.equal(spec.collection, currentPreviewIndexIdentity.collection, 'compiler spec collection changed');
  assert.equal(spec.name, currentPreviewIndexIdentity.name, 'compiler spec index name changed');
  assert.deepEqual(spec.fields, currentPreviewIndexIdentity.fields, 'compiler spec field tuple changed');
  assert.deepEqual(spec.storedValues, currentPreviewIndexIdentity.storedValues, 'compiler spec stored-values tuple changed');
  assert.equal(fingerprint.project, currentPreviewIndexIdentity.project, 'compiler spec project changed');
  assert.equal(fingerprint.generation, currentPreviewIndexIdentity.generation, 'compiler spec generation changed');
  assert.equal(fingerprint.recompiledQueryFile, resolve(queryPath), 'query file path differs from the producer proof');
  assert.match(fingerprint.recompiledQueryFileSha256 ?? '', /^[a-f0-9]{64}$/, 'compiler spec lacks a query-file SHA256');
  assert.equal(sha256(queryBytes), fingerprint.recompiledQueryFileSha256, 'query-file SHA256 differs from the producer proof');
  assert.match(fingerprint.recompiledAqlSha256 ?? '', /^[a-f0-9]{64}$/, 'compiler spec lacks a recompiled AQL SHA256');
  assert.match(fingerprint.recompiledBindVarsSha256 ?? '', /^[a-f0-9]{64}$/, 'compiler spec lacks a recompiled bindVars SHA256');
  assert.match(fingerprint.currentProductionSource?.sha256 ?? '', /^[a-f0-9]{64}$/, 'compiler spec lacks its current source-closure proof');
  assert.equal(
    fingerprint.currentProductionSource?.verification,
    'go-run-wrapper-captured-source-closure-before-compilation-and-verified-after-start',
    'compiler spec lacks verified current production source provenance',
  );

  const queryArtifact = JSON.parse(queryBytes.toString('utf8'));
  assert.deepEqual(Object.keys(queryArtifact).sort(), ['bindVars', 'query'], 'query artifact must contain only query and bindVars');
  assert.equal(typeof queryArtifact.query, 'string');
  assert(queryArtifact.query.trim(), 'query artifact AQL is empty');
  assert(queryArtifact.bindVars && typeof queryArtifact.bindVars === 'object' && !Array.isArray(queryArtifact.bindVars), 'query artifact bindVars are missing');
  assert.equal(sha256(queryArtifact.query), fingerprint.recompiledAqlSha256, 'AQL SHA256 differs from the producer proof');
  assert.equal(sha256(canonicalJSON(queryArtifact.bindVars)), fingerprint.recompiledBindVarsSha256, 'bindVars SHA256 differs from the producer proof');
  assert.equal(queryArtifact.bindVars.project, currentPreviewIndexIdentity.project, 'query project bind differs from the producer proof');
  assert.equal(queryArtifact.bindVars.dataset_generation, currentPreviewIndexIdentity.generation, 'query generation bind differs from the producer proof');
  assert.equal(queryArtifact.bindVars['@root_collection'], currentPreviewIndexIdentity.collection, 'query root collection differs from the compiler spec');
  assert(queryArtifact.query.includes(`indexHint: "${currentPreviewIndexIdentity.name}"`), 'recompiled AQL lacks its exact compiler-owned index hint');

  return {
    specFileSha256: sha256(Buffer.from(specText)),
    queryFileSha256: fingerprint.recompiledQueryFileSha256,
    aqlSha256: fingerprint.recompiledAqlSha256,
    bindVarsSha256: fingerprint.recompiledBindVarsSha256,
    sourceClosureSha256: fingerprint.currentProductionSource.sha256,
    project: fingerprint.project,
    generation: fingerprint.generation,
    collection: spec.collection,
    indexName: spec.name,
    fields: [...spec.fields],
    storedValues: [...spec.storedValues],
  };
}

export function validateOwnedArangoContainer(container) {
  assert.equal(container?.Name?.replace(/^\//, ''), 'loom-dev-6d7df93d6a37-arangodb-1', 'unexpected Arango container name');
  assert.equal(container?.State?.Running, true, 'owned Arango container must already be running');
  const labels = container.Config?.Labels ?? {};
  assert.equal(labels['com.docker.compose.project'], 'loom-dev-6d7df93d6a37', 'Arango container belongs to another Compose project');
  assert.equal(labels['com.docker.compose.service'], 'arangodb', 'container is not the expected Arango service');
  assert((container.Config?.Env ?? []).includes('ARANGO_NO_AUTH=1'), 'owned local Arango must use the no-auth development configuration');
}

function flattenIndexes(value) {
  if (Array.isArray(value)) return value.flatMap(flattenIndexes);
  if (!value || typeof value !== 'object') return [];
  if (['id', 'name', 'type', 'fields'].some((key) => value[key] !== undefined)) return [value];
  return Object.values(value).flatMap(flattenIndexes);
}

export function summarizeExplainResponse(response, expectedIndexName) {
  assert.notEqual(response?.error, true, 'Arango returned an EXPLAIN error');
  const plans = response.plan ? [response.plan, ...(response.plans ?? [])] : (response.plans ?? []);
  assert(plans.length > 0, 'Arango EXPLAIN returned no plan');
  const indexes = [];
  const fullCollectionScans = [];
  const planEstimates = plans.map((plan, planIndex) => {
    for (const node of plan.nodes ?? []) {
      if (node.type === 'EnumerateCollectionNode') {
        fullCollectionScans.push({ plan: planIndex, nodeId: node.id, collection: node.collection });
      }
      for (const index of flattenIndexes(node.indexes)) {
        indexes.push({
          plan: planIndex,
          nodeId: node.id,
          nodeType: node.type,
          collection: index.collection || node.collection || '',
          id: index.id ?? '',
          name: index.name ?? '',
          type: index.type ?? '',
          fields: Array.isArray(index.fields) ? index.fields : [],
        });
      }
    }
    return { plan: planIndex, estimatedCost: plan.estimatedCost, estimatedNrItems: plan.estimatedNrItems };
  });
  const candidateIndexUses = indexes.filter((index) => index.name === expectedIndexName && index.collection === currentPreviewIndexIdentity.collection);
  return {
    candidateIndexSelected: candidateIndexUses.length > 0,
    candidateIndexUses,
    planCount: plans.length,
    planEstimates,
    indexes,
    fullCollectionScans,
    warnings: (response.warnings ?? []).map(({ code, message }) => ({ code, message })),
  };
}
