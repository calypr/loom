#!/usr/bin/env node
// Reuse persisted fixture data through current production reads, compilation and
// exact previews. This setup phase never authors a workspace or relaxes native
// functional native wait bounds. The original creation record is immutable.
import assert from 'node:assert/strict';
import { readFileSync, mkdirSync, existsSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { request as playwrightRequest } from 'playwright';
import { createDevSession, assertOwnedDevSession } from './loom-dev.mjs';
import { assertNamedDevSessionEnvironment, fixtureProjectExplorerCollectionPath } from './verify-ui/helpers/fixture-context.mjs';
import { graphRowsNativeIdentity, assertGraphRowsNativeIdentity } from './verify-ui/helpers/graph-rows-native-identity.mjs';
import { assertHistoryStressRows } from './verify-ui/workflows/graph-rows-history-stress.mjs';
import { loadHistoryFixtureArtifact, assertHistoryRuntimeContinuity, assertCurrentHistoryFixture, writeHistoryPreparationReport } from './verify-ui/helpers/graph-rows-history-fixture.mjs';

const args = {};
for (let index = 2; index < process.argv.length; index += 2) {
  const key = process.argv[index];
  assert(['--fixture', '--sha256'].includes(key) && !args[key] && process.argv[index + 1], 'Use --fixture PATH --sha256 ORIGINAL_SHA256');
  args[key] = process.argv[index + 1];
}
assert(args['--fixture'] && args['--sha256'], 'Use --fixture PATH --sha256 ORIGINAL_SHA256');
assertNamedDevSessionEnvironment();
assert(process.env.LOOM_GRAPH_ROWS_EVIDENCE);
const started = performance.now();
const session = createDevSession({ ...process.env, LOOM_DEV_FIXTURE_DIR: `${process.env.LOOM_DEV_SOURCE_ROOT}/testdata/graph-rows` });
await assertOwnedDevSession(session, { uiRouting: 'explicit-query' });
const originalPath = resolve(args['--fixture']);
const { original, originalSHA256, target } = loadHistoryFixtureArtifact(readFileSync(originalPath), args['--sha256'], session);
const directory = resolve(process.env.LOOM_GRAPH_ROWS_EVIDENCE);
mkdirSync(directory, { recursive: true, mode: 0o700 });
const output = join(directory, 'history-preparation.json');
assert.notEqual(output, originalPath, 'Never overwrite the original fixture creation artifact');
assert(!existsSync(output), 'Use a fresh preflight evidence path; never replace an existing artifact');
const before = graphRowsNativeIdentity(target);
assertHistoryRuntimeContinuity(original, before);
const explorerId = original.seed.bootstrapExplorerId;
const apiPath = `${target.apiUrl}${fixtureProjectExplorerCollectionPath(target.fixtureProject)}/${encodeURIComponent(explorerId)}/authoring/v2`;
const request = await playwrightRequest.newContext({ timeout: 30000 });
const report = { phase: 'loaded-fixture-preflight', requestBudgetMs: 30000, setupDeadlineMs: 120000, status: 'running', target,
  seed: { bootstrapExplorerId: explorerId, reused: true, fresh: false }, before,
  outputId: original.outputId, sourceDocument: original.sourceDocument, idColumn: original.idColumn, savedIdentity: original.savedIdentity,
  originalPreparation: { path: originalPath, sha256: originalSHA256, sourceFingerprint: original.after.sourceFingerprint,
    apiSource: original.after.api.liveSource, apiBinary: original.after.api.runningBinary, elapsedMs: original.elapsedMs, operations: 100 },
  historyStressFixture: original.historyStressFixture };
const state = async () => { const response = await request.get(`${apiPath}/builder`); assert.equal(response.status(), 200); return response.json(); };
let failure;
try {
  const initial = await state();
  assertCurrentHistoryFixture(original, initial);
  const compiledResponse = await request.post(`${apiPath}/reconcile`, { data: { snapshotToken: initial.catalog.snapshotToken,
    draftVersion: initial.draftVersion, draftDigest: initial.draftDigest, outputId: original.outputId } });
  assert.equal(compiledResponse.status(), 200, (await compiledResponse.text()).slice(0, 700));
  const compiled = await compiledResponse.json();
  assert.match(compiled.receiptId ?? '', /^receipt_[a-f0-9]{64}$/);
  const previewResponse = await request.post(`${apiPath}/preview`, { data: { receiptId: compiled.receiptId, outputId: original.outputId, limit: 100 } });
  assert.equal(previewResponse.status(), 200, (await previewResponse.text()).slice(0, 700));
  const preview = await previewResponse.json();
  assert.equal(preview.receiptId, compiled.receiptId);
  assert.equal(preview.outputId, original.outputId);
  assertHistoryStressRows(preview, original.idColumn);
  const score = original.sourceDocument.columns.find(column => column.source.field.path === 'valueInteger').column;
  const scores = { 'obs-one': 7, 'obs-many-A': 11, 'obs-many-B': 13 };
  for (const row of preview.rows) assert.equal(row[score], scores[row[original.idColumn]], 'The original numeric source values must survive');
  const final = await state();
  assertCurrentHistoryFixture(original, final);
  assert.deepEqual(final.workspace, initial.workspace, 'Preflight must not author or repair a workspace');
  assert.equal(final.catalog.snapshotToken, initial.catalog.snapshotToken, 'Dataset or authorized snapshot changed during preflight');
  Object.assign(report, { status: 'passed', currentReceiptId: compiled.receiptId, rows: preview.rows,
    checks: { sourceUnchanged: true, draftUnchanged: true, generation: final.catalog.generation,
      operationIDsAndInputs: 100, completeStageSchemas: 100, originalRows: 3, calculatedValues: 300 } });
} catch (error) { report.status = 'failed'; report.error = error.message; failure = error; }
finally {
  try { report.after = graphRowsNativeIdentity(target); assertGraphRowsNativeIdentity(before, report.after); }
  catch (error) { report.status = 'failed'; report.integrityError = error.message; failure ??= error; }
  report.elapsedMs = performance.now() - started;
  try { writeHistoryPreparationReport(output, report); } finally { await request.dispose(); }
}
if (failure) throw failure;
console.log(JSON.stringify({ status: report.status, preparation: output, elapsedMs: report.elapsedMs, originalPreparationSHA256: originalSHA256, operations: 100 }));
