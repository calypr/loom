import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { median, timingTarget } from './measure-authoring-loop.mjs';

async function json(url, body) {
  const response = await fetch(url, {
    ...(body === undefined ? {} : {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }),
    signal: AbortSignal.timeout(30000),
  });
  const value = await response.json();
  assert.ok(response.ok, `${url}: ${response.status} ${JSON.stringify(value.error ?? value)}`);
  return value;
}

export const routeCount = (route) =>
  (route.children ?? []).reduce((count, child) => count + 1 + routeCount(child), 0);

const candidateFor = (builder, resourceType, fieldPath) => {
  const node = builder.catalog.nodes.find((value) => value.resourceType === resourceType);
  assert.ok(node, `${resourceType} node is missing`);
  const candidate = builder.catalog.candidates.find(
    (value) => value.nodeId === node.nodeId && value.fieldPath === fieldPath,
  );
  assert.ok(candidate, `${resourceType}.${fieldPath} candidate is missing`);
  return candidate;
};

async function prepare(reportPath) {
  const target = timingTarget(JSON.parse(readFileSync(reportPath, 'utf8')));
  let builder = await json(`${target.base}/builder`);
  const document = builder.workspace.documents[0];
  const observation = document.route.children.find((route) => route.resourceType === 'Observation');
  assert.ok(observation, 'Observation occurrence is missing');
  const commands = [];
  for (const fieldPath of ['resourceType', 'meta.lastUpdated', 'birthDate']) {
    if (document.columns.some((column) => column.occurrenceId === 'base' && column.source.field?.path === fieldPath)) continue;
    commands.push({
      type: 'ADD_COLUMN',
      outputId: document.output.id,
      occurrenceId: 'base',
      candidateId: candidateFor(builder, 'Patient', fieldPath).candidateId,
      initialPresentation: 'TABLE',
    });
  }
  if (!document.columns.some((column) => column.label === 'Observation minimum')) {
    commands.push({
      type: 'ADD_COLUMN_SOURCE',
      outputId: document.output.id,
      occurrenceId: observation.occurrenceId,
      title: 'Observation minimum',
      source: { kind: 'aggregate', aggregate: { operation: 'MIN', path: 'valueQuantity.value' } },
    });
  }
  if (commands.length) {
    await json(`${target.base}/commands`, {
      commandId: `b05-preview-${randomUUID()}`,
      semanticsVersion: 4,
      snapshotToken: builder.catalog.snapshotToken,
      expectedDraftVersion: builder.draftVersion,
      expectedDraftDigest: builder.draftDigest,
      commands,
    });
    builder = await json(`${target.base}/builder`);
  }
  const prepared = builder.workspace.documents[0];
  assert.equal(prepared.columns.length, 10, 'B05 preview probe requires exactly ten authored features');
  const receipt = await json(`${target.base}/reconcile`, {
    snapshotToken: builder.catalog.snapshotToken,
    draftVersion: builder.draftVersion,
    draftDigest: builder.draftDigest,
  });
  return {
    target,
    receiptId: receipt.receiptId,
    outputId: prepared.output.id,
    authoredFeatureCount: prepared.columns.length,
    traversalCount: routeCount(prepared.route),
  };
}

async function preview(prepared) {
  const started = performance.now();
  const result = await json(`${prepared.target.base}/preview`, {
    receiptId: prepared.receiptId,
    outputId: prepared.outputId,
    limit: 25,
  });
  const elapsed = performance.now() - started;
  const idIndex = result.columns.findIndex((column) => column.label === 'id');
  assert.notEqual(idIndex, -1, 'preview id column is missing');
  return {
    elapsed,
    physicalColumnCount: result.columns.length,
    ids: result.rows.map((row) => Array.isArray(row) ? row[idIndex] : row[result.columns[idIndex].column]).sort(),
  };
}

async function main() {
  assert.equal(process.argv.length, 4, 'usage: node scripts/measure-b05-preview.mjs BASELINE_REPORT CANDIDATE_REPORT');
  const prepared = await Promise.all(process.argv.slice(2).map(prepare));
  assert.equal(prepared[0].authoredFeatureCount, prepared[1].authoredFeatureCount);
  assert.equal(prepared[0].traversalCount, prepared[1].traversalCount);
  const samples = [[], []];
  let expected;
  for (let index = -1; index < 5; index++) {
    for (const side of index % 2 === 0 ? [1, 0] : [0, 1]) {
      const value = await preview(prepared[side]);
      expected ??= { physicalColumnCount: value.physicalColumnCount, ids: value.ids };
      assert.deepEqual({ physicalColumnCount: value.physicalColumnCount, ids: value.ids }, expected);
      if (index >= 0) samples[side].push(value.elapsed);
    }
  }
  assert.deepEqual(expected.ids, ['dev-patient-001', 'dev-patient-002']);
  const [baseline, candidate] = samples.map(median);
  const evidence = {
    targets: prepared.map(({ target }) => target),
    authoredFeatureCount: prepared[0].authoredFeatureCount,
    physicalColumnCount: expected.physicalColumnCount,
    traversalCount: prepared[0].traversalCount,
    samples,
    preview_ms: { baseline, candidate, ratio: candidate / baseline },
  };
  const output = join(process.cwd(), '.artifacts/loom-dev', `b05-preview-${Date.now()}.json`);
  mkdirSync(dirname(output), { recursive: true });
  writeFileSync(output, JSON.stringify(evidence, null, 2));
  console.log(JSON.stringify({ evidence: output, ...evidence }, null, 2));
  assert.ok(candidate < 30000, 'ten-feature preview exceeded the 30-second warm-loop target');
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch((error) => { console.error(error.message); process.exitCode = 1; });
}
