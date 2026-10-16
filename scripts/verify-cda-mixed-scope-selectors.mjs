import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parseArgs } from 'node:util';

// Public recipe regression. Builder does not currently author field fallbacks.
const { values } = parseArgs({ options: {
  'api-origin': { type: 'string', default: 'http://127.0.0.1:8188' },
  project: { type: 'string', default: 'loom_dev_cda_fhir' },
  generation: { type: 'string', default: 'cda-fhir-v1' },
  'arango-container': { type: 'string', default: 'loom-dev-6d7df93d6a37-arangodb-1' },
  evidence: { type: 'string', default: `/tmp/loom-mixed-scope-${Date.now()}` },
  'source-filter-only': { type: 'boolean', default: false },
} });
const report = { status: 'running', started: new Date().toISOString(), assertions: [], requests: [], gaps: ['Public recipe only; no native Builder fallback authoring control exists.'] };
const name = `cda_mixed_scope_${Date.now()}_${randomUUID().slice(0, 8)}`;
const graphql = async (query, input) => {
  const started = Date.now();
  const headers = { 'Content-Type': 'application/json', 'X-Request-ID': randomUUID() };
  if (process.env.LOOM_CDA_API_TOKEN) headers.Authorization = `Bearer ${process.env.LOOM_CDA_API_TOKEN}`;
  const response = await fetch(`${values['api-origin']}/graphql/graph`, {
    method: 'POST', headers, body: JSON.stringify({ query, variables: { input } }), signal: AbortSignal.timeout(30000),
  });
  const body = await response.json();
  report.requests.push({ query, input, status: response.status, durationMs: Date.now() - started, response: body });
  assert(response.ok, `GraphQL HTTP ${response.status}`);
  assert(!body.errors?.length, JSON.stringify(body.errors));
  if (query.includes('previewDataframeRecipe')) {
    assert(Date.now() - started <= 5000, 'Public recipe preview exceeded the five-second execution budget');
  }
  return body.data;
};
let failure;
try {
  await mkdir(values.evidence, { recursive: true });
  const query = `FOR r IN Observation FILTER r.project == ${JSON.stringify(values.project)} AND r.dataset_generation == ${JSON.stringify(values.generation)} FILTER IS_ARRAY(r.payload.component) SORT r.id LIMIT 1000 RETURN {id:r.id, generation:r.dataset_generation, payload:r.payload}`;
  const raw = spawnSync('rtk', ['proxy', 'docker', 'exec', values['arango-container'], 'arangosh', '--server.database', 'loom_dev', '--javascript.execute-string', `print(JSON.stringify(db._query(${JSON.stringify(query)}).toArray()));`], { encoding: 'utf8', timeout: 30000, maxBuffer: 16 * 1024 * 1024 });
  assert.equal(raw.status, 0, raw.stderr || raw.stdout);
  const offset = raw.stdout.indexOf('[');
  assert(offset >= 0, 'Raw oracle returned no array');
  const scanned = JSON.parse(raw.stdout.slice(offset));
  assert(scanned.length <= 1000);
  const witnesses = [];
  let rowCount = 0;
  for (const resource of scanned) {
    const components = resource.payload.component;
    if (components.length < 2 || !components.every(c => typeof c.valueString === 'string' && c.valueString.length && Array.isArray(c.code?.coding) && c.code.coding.length && c.code.coding.every(k => typeof k.code === 'string'))) continue;
    if (new Set(components.map(c => c.valueString)).size < 2) continue;
    const count = components.reduce((n, c) => n + c.code.coding.length, 0);
    if (rowCount + count > 6) continue;
    const identifiers = (resource.payload.identifier ?? []).flatMap(i => typeof i.value === 'string' ? [i.value] : []);
    witnesses.push({ id: resource.id, generation: resource.generation, identifiers, components });
    rowCount += count;
    if (witnesses.length === 3) break;
  }
  assert(witnesses.length, 'No bounded nested coding witness with distinct component values');
  report.oracle = { project: values.project, generation: values.generation, scanned: scanned.length, witnesses };
  // Persist exact source membership rather than scanning the full CDA population
  // through a post-projection recipe filter.
  const explorer = `cda-mixed-scope-${Date.now()}-${randomUUID().slice(0, 8)}`;
  const root = `/api/v1/projects/${encodeURIComponent(values.project)}/explorers`;
  const rest = async (path, body) => {
    const headers = { 'Content-Type': 'application/json' };
    if (process.env.LOOM_CDA_API_TOKEN) headers.Authorization = `Bearer ${process.env.LOOM_CDA_API_TOKEN}`;
    const response = await fetch(values['api-origin'] + path, { method: body === undefined ? 'GET' : 'POST', headers, ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(30000) });
    const result = await response.json();
    assert(response.ok, `${response.status} ${path}: ${JSON.stringify(result)}`);
    return result;
  };
  let selection;
  if (!values['source-filter-only']) {
    await rest(root, { name: explorer, title: 'Mixed-scope public recipe QA' });
    const builder = await rest(`${root}/${explorer}/authoring/v2/builder`);
    assert.equal(builder.catalog.generation, values.generation);
    selection = await rest(`${root}/${explorer}/selections`, {
      snapshotToken: builder.catalog.snapshotToken, idempotencyKey: explorer,
      source: { kind: 'resources', resources: { refs: witnesses.map(r => ({ project: values.project, generation: r.generation, resourceType: 'Observation', id: r.id })) } },
    });
    report.selection = selection;
    report.explorer = explorer;
  }
  const expected = witnesses.flatMap(r => r.components.flatMap(c => c.code.coding.map(k => ({
    observation_id: r.id, coding_code: k.code,
    mixed_values: [c.valueString, ...r.identifiers],
    mixed_first: c.valueString,
    mixed_distinct: [...new Set([c.valueString, ...r.identifiers])].sort(),
  }))));
  const recipe = {
    recipeSchemaVersion: 1, name, translationVersion: 'mixed-scope-regression-v1', outputs: [{
      name: 'nested_codings', rootResourceType: 'Observation', rootOccurrenceId: 'observation-root', rowGrain: 'expanded', rootColumnNaming: 'EXACT',
      ...(selection ? { population: { selectionRevisionId: selection.id, membershipDigest: selection.membershipDigest, memberCount: selection.memberCount, resourceType: 'Observation' } } : {}),
      fields: [
        { name: 'observation_id', expr: { select: 'root.id' }, valueMode: 'FIRST' },
        { name: 'coding_code', expr: { select: 'item.code' }, valueMode: 'FIRST' },
        { name: 'mixed_values', expr: { select: 'root.component[].valueString' }, fallbacks: [{ select: 'root.identifier[].value' }], valueMode: 'ALL' },
        { name: 'mixed_first', expr: { select: 'root.component[].valueString' }, fallbacks: [{ select: 'root.identifier[].value' }], valueMode: 'FIRST' },
        { name: 'mixed_distinct', expr: { select: 'root.component[].valueString' }, fallbacks: [{ select: 'root.identifier[].value' }], valueMode: 'DISTINCT' },
      ],
      filters: [{ select: 'root.id', operator: 'IN', values: witnesses.map(r => ({ kind: 'STRING', string: r.id })) }],
      expand: { ownerOccurrenceId: 'observation-root', from: { select: 'root.component[].code.coding[]' }, as: 'item', ordinality: 'position', emptyPolicy: 'EXCLUDE' },
      identity: { name: 'row', expansion: {} },
    }],
  };
  report.recipe = recipe;
  const registered = await graphql('mutation Register($input: RegisterDataframeRecipeRevisionInput!) { registerDataframeRecipeRevision(input:$input) { projectId name digest } }', { projectId: values.project, name, recipe });
  const digest = registered.registerDataframeRecipeRevision.digest;
  const input = { name, bindings: { project: values.project, recipeDigest: digest, datasetGeneration: values.generation, previewLimit: 10 }, limit: 10 };
  const previewQuery = 'mutation Preview($input: PreviewDataframeRecipeInput!) { previewDataframeRecipe(input:$input) { recipeDigest sourceGeneration outputs { name columns rows rowCount } } }';
  const normalize = rows => rows.map(({ observation_id, coding_code, mixed_values, mixed_first, mixed_distinct }) => {
    assert(Array.isArray(mixed_distinct), 'DISTINCT must return an array');
    assert.equal(new Set(mixed_distinct).size, mixed_distinct.length, 'DISTINCT must not repeat values');
    // Arango sorts strings with its own collation. Compare distinct membership
    // independently of that ordering; ALL and FIRST retain exact order checks.
    return { observation_id, coding_code, mixed_values, mixed_first, mixed_distinct: [...mixed_distinct].sort() };
  }).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
  for (const phase of ['initial', 'persisted-revision-repeat']) {
    const result = (await graphql(previewQuery, input)).previewDataframeRecipe;
    assert.equal(result.recipeDigest, digest);
    assert.equal(result.sourceGeneration, values.generation);
    assert.equal(result.outputs.length, 1);
    assert.equal(result.outputs[0].rowCount, expected.length);
    assert.deepEqual(normalize(result.outputs[0].rows), normalize(expected), 'Each coding must retain only its own component value, followed by root identifier values');
    report.assertions.push({ phase, status: 'passed', rows: expected.length });
  }
  report.status = 'passed';
} catch (error) {
  failure = error;
  report.status = 'failed';
  report.failure = { message: error.message, stack: error.stack };
} finally {
  report.finished = new Date().toISOString();
  await mkdir(values.evidence, { recursive: true });
  await writeFile(join(values.evidence, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify({ status: report.status, assertions: report.assertions.length, evidence: join(values.evidence, 'report.json'), failure: failure?.message.slice(0, 500) }));
}
if (failure) process.exitCode = 1;
