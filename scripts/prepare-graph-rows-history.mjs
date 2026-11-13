#!/usr/bin/env node
// Bounded production fixture bootstrap runs before the native test clock. Each
// bootstrap request has a declared30s setup budget; native requests/actions
// retain5s budgets and the native case retains its180s total budget.
import assert from 'node:assert/strict';
import { mkdirSync, existsSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { request as playwrightRequest } from 'playwright';
import { createDevSession, createFreshVerificationFixture, assertOwnedDevSession } from './loom-dev.mjs';
import { assertNamedDevSessionEnvironment, fixtureProjectExplorerCollectionPath } from './verify-ui/helpers/fixture-context.mjs';
import { graphRowsNativeIdentity, assertGraphRowsNativeIdentity } from './verify-ui/helpers/graph-rows-native-identity.mjs';
import { seedThroughProductionProposals, assertHistoryStressPlan } from './verify-ui/workflows/graph-rows-history-stress.mjs';
import { historyEvidenceTarget, writeHistoryPreparationReport } from './verify-ui/helpers/graph-rows-history-fixture.mjs';
import { EXPLORER_AUTHORING_SEMANTICS_VERSION } from '../ui/packages/loom-ui/src/authoringSemanticsVersion.mjs';

assertNamedDevSessionEnvironment();
assert(process.env.LOOM_GRAPH_ROWS_EVIDENCE);
const started = performance.now();
const directory = resolve(process.env.LOOM_GRAPH_ROWS_EVIDENCE);
mkdirSync(directory, { recursive: true, mode: 0o700 });
const output = join(directory, 'history-preparation.json');
assert(!existsSync(output), 'Use a fresh preparation evidence path');
const session = createDevSession({ ...process.env, LOOM_DEV_FIXTURE_DIR: `${process.env.LOOM_DEV_SOURCE_ROOT}/testdata/graph-rows` });
await assertOwnedDevSession(session, { uiRouting: 'explicit-query' });
const { target, seed } = await createFreshVerificationFixture(session, `gr-history-${Date.now().toString(36)}`, { populateBootstrap: false });
const before = graphRowsNativeIdentity(target);
const explorerId = seed.bootstrapExplorerId;
const apiPath = `${target.apiUrl}${fixtureProjectExplorerCollectionPath(target.fixtureProject)}/${encodeURIComponent(explorerId)}/authoring/v2`;
const request = await playwrightRequest.newContext({ timeout: 30000 });
const report = { phase: 'production-fixture-preparation', requestBudgetMs: 30000, setupDeadlineMs: 900000, target: historyEvidenceTarget(target), seed, before, status: 'running' };
const state = async () => { const response = await request.get(`${apiPath}/builder`); assert.equal(response.status(), 200); return response.json(); };
const command = async (owner, commands, commandId) => {
  const response = await request.post(`${apiPath}/commands`, { data: { commandId, semanticsVersion: EXPLORER_AUTHORING_SEMANTICS_VERSION,
    snapshotToken: owner.catalog.snapshotToken, expectedDraftVersion: owner.draftVersion, expectedDraftDigest: owner.draftDigest, commands } });
  assert.equal(response.status(), 200, (await response.text()).slice(0, 700));
  return state();
};
try {
  let owner = await state();
  const roots = owner.catalog.nodes.filter(node => node.resourceType === 'Observation');
  assert.equal(roots.length, 1);
  owner = await command(owner, [{ type: 'CREATE_TABLE', title: 'History stress fixture', rootNodeId: roots[0].nodeId }], 'history-source-table');
  const outputId = owner.workspace.documents[0].output.id;
  for (const path of ['id', 'valueInteger']) {
    const candidates = owner.catalog.candidates.filter(candidate => candidate.nodeId === roots[0].nodeId && candidate.fieldPath === path);
    assert.equal(candidates.length, 1, `Unambiguous fixture field ${path}`);
    owner = await command(owner, [{ type: 'ADD_COLUMN', outputId, occurrenceId: 'base', candidateId: candidates[0].candidateId,
      projectionMode: 'VALUE', initialPresentation: 'TABLE', title: path === 'id' ? 'Observation ID' : 'Fixture score' }], `history-source-${path}`);
  }
  const sourceDocument = owner.workspace.documents[0];
  assert.equal(sourceDocument.columns.length, 2);
  const idColumn = sourceDocument.columns.find(column => column.label === 'Observation ID').column;
  const run = { apiPath, report, idColumn };
  const saved = await seedThroughProductionProposals(run, request, owner, outputId, { fixtureRequestTimeoutMs: 30000 });
  assertHistoryStressPlan(saved.workspace.documents[0]);
  Object.assign(report, { outputId, sourceDocument, idColumn, savedIdentity: { version: saved.draftVersion, digest: saved.draftDigest }, status: 'passed' });
} catch (error) { report.status = 'failed'; report.error = error.message; throw error; }
finally {
  report.after = graphRowsNativeIdentity(target);
  assertGraphRowsNativeIdentity(before, report.after);
  report.elapsedMs = performance.now() - started;
  try { writeHistoryPreparationReport(output, report); } finally { await request.dispose(); }
}
console.log(JSON.stringify({ status: report.status, preparation: output, elapsedMs: report.elapsedMs, operations: 100 }));
