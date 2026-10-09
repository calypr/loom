import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { coverageDrift, hasLifecycleContract, registry, scenarioCaseFor } from '../../registry.mjs';
import { cdaNullableEmptyRemovalPreviewEvidence } from '../cda-nullable-code-join-oracle.mjs';
import {
  matchesPublishedAppendSourceProjectionResponse,
} from '../cda-published-upstream-append-oracle.mjs';
import { rootedEmptyTargetRestorationEvidence } from '../builder-combine-helpers.mjs';

const scenarioID = 'cda-published-upstream-append';
const caseName = 'published-append';

test('published CDA APPEND spec binds its report to all 34 registered lifecycle checks', async () => {
  const spec = await readFile(new URL('../../specs/cda-published-upstream-append.spec.mjs', import.meta.url), 'utf8');
  assert.match(spec, /cdaScenarioID:\s*'cda-published-upstream-append'/);
  assert.match(spec, /cdaCaseName:\s*'published-append'/);

  const scenario = registry.find(({ id }) => id === scenarioID);
  const contract = scenarioCaseFor(scenario, caseName);
  assert.equal(contract.playwrightTest, 'scripts/verify-ui/specs/cda-published-upstream-append.spec.mjs');
  assert.equal(contract.playwrightGrep, 'publish exact raw CDA sources, Append with null padding, and restore the rooted target$');
  assert.deepEqual(contract.expectedIdentity, { project: 'loom_dev_cda_fhir', generation: 'cda-fhir-v1' });

  const registered = contract.requiredChecks;
  assert.equal(registered.length, 34);
  assert.equal(new Set(registered).size, registered.length);
  for (const required of [
    'native Builder publication succeeds for the three selected raw CDA source tables',
    'APPEND proposal pins the three exact source revisions and explicit Patient status null padding',
    'Canceling the initial published APPEND proposal preserves the exact rooted empty target',
    'APPEND Apply reload reaches exact null-padded CDA rows within five seconds',
    'saved APPEND label edit retains stable step and explicit Patient null padding',
    'applying APPEND removal restores the exact pre-combine rooted empty target',
    'published raw CDA source tables and exact Explorer scope remain unchanged through APPEND lifecycle',
  ]) assert(registered.includes(required), `registry is missing ${required}`);

  const coverage = scenario.coverage.find(({ feature }) => feature.startsWith('published-source CDA APPEND'));
  assert.deepEqual(coverage.acceptance.checks,
    { choice: 13, proposal: 12, cancel: 14, apply: 16, savedRows: 16, reload: 18, edit: 25, restoration: 31 });
  assert.equal(hasLifecycleContract(coverage, scenario), true);
  assert.deepEqual(coverageDrift([scenario]), [], 'the historical APPEND case remains a valid named lifecycle contract');
  for (const [phase, index, expected] of [
    ['choice', 13, 'APPEND proposal pins the three exact source revisions and explicit Patient status null padding'],
    ['proposal', 12, 'native APPEND preview equals the exact duplicate-preserving CDA union with Patient null padding'],
    ['cancel', 14, 'Canceling the initial published APPEND proposal preserves the exact rooted empty target'],
    ['apply', 16, 'applied APPEND matches the exact CDA multiset and null padding'],
    ['savedRows', 16, 'applied APPEND matches the exact CDA multiset and null padding'],
    ['reload', 18, 'APPEND Apply reload reaches exact null-padded CDA rows within five seconds renders exact saved APPEND rows'],
    ['edit', 25, 'saved APPEND label edit retains stable step and explicit Patient null padding'],
    ['restoration', 31, 'APPEND removal reload restores exact rooted empty target within five seconds'],
  ]) assert.equal(registered[index], expected, `lifecycle ${phase} mapping must resolve to its named APPEND evidence`);
});

test('source-projection response binding accepts exact native evidence and rejects shifted identity', () => {
  const expected = { origin: 'http://127.0.0.1:30008', path: '/api/v1/projects/p/explorers/e/authoring/v2/construction-capabilities',
    outputId: 'observation-source', snapshotToken: 'snapshot-7', draftVersion: 12, draftDigest: 'draft-12' };
  const entry = { origin: expected.origin, path: expected.path, method: 'POST', status: 200,
    responseReceivedAt: 10, completedAt: 11 };
  const requestBody = { snapshotToken: expected.snapshotToken, outputId: expected.outputId,
    stageId: 'source_projection', expectedDraftVersion: expected.draftVersion,
    expectedDraftDigest: expected.draftDigest };
  const responseBody = { outputId: expected.outputId, stageId: 'source_projection',
    snapshotToken: expected.snapshotToken, draftVersion: expected.draftVersion, draftDigest: expected.draftDigest };
  const matches = (entryChange = {}, requestChange = {}, response = responseBody) =>
    matchesPublishedAppendSourceProjectionResponse({ ...entry, ...entryChange }, { ...requestBody, ...requestChange }, response, expected);

  assert.equal(matches(), true);
  assert.equal(matches({ origin: 'http://wrong-origin' }), false);
  assert.equal(matches({}, { outputId: 'other-output' }), false);
  assert.equal(matches({}, { expectedDraftDigest: 'stale-draft' }), false);
  assert.equal(matches({}, { stageId: 'combine' }), false);
  assert.equal(matches({}, {}, { ...responseBody, outputId: 'other-output' }), false);
  assert.equal(matches({}, {}, { ...responseBody, snapshotToken: 'stale-snapshot' }), false);
  assert.equal(matches({}, {}, { ...responseBody, draftVersion: 11 }), false);
  assert.equal(matches({}, {}, { ...responseBody, draftDigest: 'stale-draft' }), false);
  assert.equal(matches({}, {}, { ...responseBody, stageId: 'combine' }), false);
  assert.equal(matches({ status: 500 }), false);
  assert.equal(matches({ failure: 'net::ERR_ABORTED' }), false);
  assert.equal(matches({ responseReadError: 'unreadable' }), false);
});

test('published APPEND removal validates its exact zero-column proposal and status-only preview', () => {
  const outputId = 'append-target';
  const project = 'project';
  const explorer = 'explorer';
  const generation = 'cda-fhir-v1';
  const uiOrigin = 'http://127.0.0.1:30008';
  const snapshotToken = 'snapshot-7';
  const draftDigest = 'draft-12';
  const catalog = { generation, snapshotToken, authorizationScopeDigest: 'scope-1' };
  const baselineDocument = { output: { id: outputId }, rootResourceType: 'Observation',
    route: { occurrenceId: 'base', resourceType: 'Observation' }, columns: [], rows: { kind: 'RECORDS', records: {} } };
  const savedDocument = { ...structuredClone(baselineDocument), construction: { version: 1, steps: [
    { id: 'saved-append-step', operation: { kind: 'COMBINE', combine: { kind: 'APPEND' } } },
  ] } };
  const target = { outputId };
  const candidateConstruction = { version: 1, steps: [] };
  const candidateDocument = { ...structuredClone(savedDocument), construction: candidateConstruction };
  const restorationEvidence = rootedEmptyTargetRestorationEvidence(candidateDocument, baselineDocument, target);
  assert.equal(restorationEvidence.ok, true);

  const currentDraft = { draftVersion: 12, draftDigest, catalog };
  const targetCreateBase = { catalog: structuredClone(catalog), workspace: { documents: [baselineDocument] } };
  const rows = Array.from({ length: 25 }, (_, index) => ({ __loom_row_id: String(index + 1).padStart(64, '0') }));
  const rowSources = Array.from({ length: 25 }, (_, index) => ({
    id: `observation-${index + 1}`, kind: 'SINGLE', resourceType: 'Observation',
  }));
  const proposalId = 'append-removal-proposal';
  const path = `/api/v1/projects/${project}/explorers/${explorer}/authoring/v2/construction-proposals`;
  const requestBody = { snapshotToken, expectedDraftVersion: 12, expectedDraftDigest: draftDigest,
    outputId, limit: 25, candidateConstruction };
  const responseBody = { proposalId, outputId, snapshotToken, draftVersion: 12, draftDigest,
    previewStatus: 'READY', candidateConstruction,
    preview: { receiptId: proposalId, outputId, columns: [], rows, rowSources, rowCount: 25, sampled: true } };
  const proposal = { event: { origin: uiOrigin, path, method: 'POST', status: 200 }, requestBody, responseBody };
  const dom = { proposalPanelCount: 1, proposalStatus: 'ready', proposalId,
    resultSectionCount: 1, resultStatus: 'ready', resultOutputId: outputId,
    resultReceiptId: proposalId, resultProposalId: proposalId,
    proposalPreviewCount: 1, previewStatus: 'ready', previewOutputId: outputId,
    previewReceiptId: proposalId, statusText: 'This table has no visible columns.',
    footerText: 'Showing 25 preview rows. Full-output coverage is unavailable before publication.', tableCount: 0 };
  const evidence = cdaNullableEmptyRemovalPreviewEvidence({ proposal, outputId, targetCreateBase,
    baselineDocument, currentDraft, restorationEvidence, project, explorer, generation, uiOrigin, dom });
  assert.equal(evidence.ok, true);

  const changed = (changes = {}) => cdaNullableEmptyRemovalPreviewEvidence({ proposal: changes.proposal ?? proposal,
    outputId: changes.outputId ?? outputId, targetCreateBase: changes.targetCreateBase ?? targetCreateBase,
    baselineDocument: changes.baselineDocument ?? baselineDocument, currentDraft: changes.currentDraft ?? currentDraft,
    restorationEvidence: changes.restorationEvidence ?? restorationEvidence, project, explorer, generation, uiOrigin,
    dom: changes.dom ?? dom });
  assert.equal(changed({ outputId: 'other-output' }).ok, false);
  assert.equal(changed({ proposal: { ...proposal, requestBody: { ...requestBody, expectedDraftDigest: 'stale' } } }).ok, false);
  assert.equal(changed({ proposal: { ...proposal, responseBody: { ...responseBody, proposalId: 'other-proposal' } } }).ok, false);
  assert.equal(changed({ dom: { ...dom, tableCount: 1 } }).ok, false);
  assert.equal(changed({ dom: { ...dom, statusText: 'Loading…' } }).ok, false);
});
