import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { registry, requiredChecksFor } from '../registry.mjs';
import { builderCancelStateEvidence, nativeResponseScopeEvidence, removalProposalEvidence, targetDocumentStateEvidence } from '../builder-combine-nullable-helpers.mjs';
import { classifyNativeBrowserApiRequest } from '../../lib/native-browser-api-scope.mjs';

const driver = readFileSync(new URL('../builder-combine-nullable.mjs', import.meta.url), 'utf8');

test('nullable KEY_JOIN is registered as a separate owned native lifecycle', () => {
  const scenario = registry.find((entry) => entry.id === 'builder-combine-nullable');
  assert.ok(scenario);
  assert.equal(scenario.script, 'builder-combine-nullable.mjs');
  assert.deepEqual(scenario.cases, ['lifecycle']);
  const required = requiredChecksFor(scenario, 'lifecycle');
  for (const name of [
    'both published subject.reference fields are nullable scalar strings',
    'source schemas expose compatible nullable scalar ID keys, scalar status fields, and a numeric Observation value',
    'native CREATE_TABLE request and response bind to the owned UI proxy project and Explorer route',
    'INNER preview receipt binds the exact UI proxy route, target, nullable key pair, output columns, and raw rows',
    'INNER nullable Join matches exactly two equal non-NULL subject references and does not match NULL to NULL',
    'LEFT preview receipt binds the exact UI proxy route, target, nullable key pair, output columns, and raw rows',
    'nullable KEY_JOIN removal preview receipt binds the exact scoped request, removed step, target, snapshot, and DOM receipt',
    'Cancel leaves saved INNER nullable Join rows unchanged after reload',
    'Cancel leaves the full Builder workspace, draft version, and digest unchanged after reload',
    'Cancel removal leaves the full Builder workspace, draft version, and digest unchanged after reload',
    'removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document',
    'LEFT nullable Join rows and null projections survive Builder reload',
    'both published nullable-key source tables remain byte-structured unchanged',
  ]) assert.ok(required.includes(name), 'registry must require ' + name);
});

test('nullable lifecycle is discovered by the official Playwright Test runner', () => {
  const spec = readFileSync(new URL('../../playwright/nullable-combine.spec.mjs', import.meta.url), 'utf8');
  assert.match(driver, /export const nullableJoinWorkflow = async \(\{ page, report, action \}, context\) =>/);
  assert.match(driver, /import \{ expect, test \} from '@playwright\/test'/);
  assert.doesNotMatch(driver, /runPlaywrightCase|executeScenario|runNullableJoin/);
  assert.doesNotMatch(driver, /activeAction|page\.locator\('body'\)|await Promise\.all\(entries\.map\(entry => entry\.responsePromise\)/);
  assert.match(driver, /test\.step\(name, async \(\) =>/);
  assert.match(driver, /\}, \{ timeout: STEP_TIMEOUT_MS \}\)/);
  assert.equal((driver.match(/await expect\.poll\(/g) ?? []).length, 3);
  assert.match(spec, /import \{ test \} from '\.\/fixtures\.mjs'/);
  assert.match(spec, /test\.use\(\{ scenarioID: 'builder-combine-nullable', caseName: 'lifecycle', fixtureDir: 'testdata\/verify-combine' \}\)/);
  assert.match(spec, /nullableJoinWorkflow\(\{ page, report: workflow\.report, action: workflow\.action \}, loomContext\)/);
});

test('nullable native case authors the exact optional source paths and checks the bound proposal receipt', () => {
  assert.match(driver, /Observation:\s*\['status',\s*'valueInteger',\s*'subject\.reference'\]/);
  assert.match(driver, /DiagnosticReport:\s*\['status',\s*'subject\.reference'\]/);
  assert.match(driver, /nullable-key fixture matches the exact ID-to-reference maps with two shared keys and NULL on both sides/);
  assert.match(driver, /column\.clickhouseType === 'Nullable\(String\)' && column\.nullable === true && column\.repeated === false/);
  assert.match(driver, /constructionProposalPreviewEvidence\(/);
  assert.match(driver, /leftColumnId === expectedKeyIDs\[0\].*rightColumnId === expectedKeyIDs\[1\]/s);
  assert.match(driver, /nullMatchesNull: false/);
});

test('nullable Playwright request listener classifies the owned proxy scope', () => {
  const scope = {
    uiOrigin: 'http://127.0.0.1:30008',
    apiOrigin: 'http://127.0.0.1:8188',
    project: 'loom_dev_verify_123',
    explorer: 'verify-123-combine',
    protectedExplorer: 'verify-123-bootstrap',
  };
  const route = `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/${scope.explorer}/authoring/v2/commands`;
  assert.match(driver, /import \{ classifyNativeBrowserApiRequest \} from '\.\.\/lib\/native-browser-api-scope\.mjs'/);
  const request = { url: () => route };
  assert.equal(classifyNativeBrowserApiRequest(request.url(), scope).kind, 'capture');
  assert.match(driver, /classifyNativeBrowserApiRequest\(request\.url\(\), scope\)/);
  assert.match(driver, /findRemoval\(stepID, afterIndex = 0\)/);
  assert.match(driver, /removeStepIds\.includes\(stepID\)/);
});

test('native request/response evidence rejects direct, foreign, protected, and mismatched routes', () => {
  const scope = {
    uiOrigin: 'http://127.0.0.1:30008',
    apiOrigin: 'http://127.0.0.1:8188',
    project: 'loom_dev_verify_123',
    explorer: 'verify-123-combine',
    protectedExplorer: 'verify-123-bootstrap',
  };
  const path = `/api/v1/projects/${scope.project}/explorers/${scope.explorer}/authoring/v2/construction-proposals`;
  const requestURL = scope.uiOrigin + path + '?outputId=target-1';
  assert.equal(nativeResponseScopeEvidence(requestURL, requestURL, scope).ok, true);
  assert.equal(nativeResponseScopeEvidence(scope.apiOrigin + path, scope.apiOrigin + path, scope).ok, false);
  assert.equal(nativeResponseScopeEvidence(
    `${scope.uiOrigin}/api/v1/projects/other-project/explorers/${scope.explorer}/authoring/v2/construction-proposals`,
    `${scope.uiOrigin}/api/v1/projects/other-project/explorers/${scope.explorer}/authoring/v2/construction-proposals`, scope,
  ).ok, false);
  assert.equal(nativeResponseScopeEvidence(
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/authoring/v2/construction-proposals`,
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/authoring/v2/construction-proposals`, scope,
  ).ok, false);
  assert.equal(nativeResponseScopeEvidence(
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/${scope.protectedExplorer}/authoring/v2/construction-proposals`,
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/${scope.protectedExplorer}/authoring/v2/construction-proposals`, scope,
  ).ok, false);
  assert.equal(nativeResponseScopeEvidence(
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/other-explorer/authoring/v2/construction-proposals`,
    `${scope.uiOrigin}/api/v1/projects/${scope.project}/explorers/other-explorer/authoring/v2/construction-proposals`, scope,
  ).ok, false);
  assert.equal(nativeResponseScopeEvidence(requestURL, `${scope.uiOrigin}${path}?outputId=other-target`, scope).ok, false);
});

test('Cancel evidence detects draft metadata and workspace changes', () => {
  const before = {
    draftVersion: 3,
    draftDigest: 'digest-3',
    workspace: { documents: [{ output: { id: 'target-1' }, construction: { steps: [{ id: 'step-1' }] } }] },
  };
  assert.equal(builderCancelStateEvidence(before, structuredClone(before)).ok, true);
  assert.equal(builderCancelStateEvidence(before, { ...structuredClone(before), draftVersion: 4 }).ok, false);
  assert.equal(builderCancelStateEvidence(before, { ...structuredClone(before), draftDigest: 'different' }).ok, false);
  const changedWorkspace = structuredClone(before);
  changedWorkspace.workspace.documents[0].construction.steps[0].id = 'step-2';
  assert.equal(builderCancelStateEvidence(before, changedWorkspace).ok, false);
  assert.equal(builderCancelStateEvidence(undefined, undefined).ok, false);
  assert.equal(builderCancelStateEvidence(before, { ...structuredClone(before), workspace: undefined }).ok, false);
  assert.equal(builderCancelStateEvidence({ ...structuredClone(before), draftVersion: 0 }, before).ok, false);
  assert.equal(builderCancelStateEvidence({ ...structuredClone(before), draftDigest: '' }, before).ok, false);
});

test('removal proposal evidence binds the exact step, target, snapshot, route, and receipt', () => {
  const candidateConstruction = { version: 1, steps: [] };
  const requestBody = {
    outputId: 'target-1',
    removeStepIds: ['step-1'],
    snapshotToken: 'snapshot-3',
    expectedDraftVersion: 3,
    expectedDraftDigest: 'digest-3',
    candidateConstruction,
  };
  const response = {
    outputId: 'target-1',
    snapshotToken: 'snapshot-3',
    draftVersion: 3,
    draftDigest: 'digest-3',
    candidateConstruction: structuredClone(candidateConstruction),
    proposalId: 'proposal-4',
    previewStatus: 'READY',
    preview: { outputId: 'target-1', receiptId: 'proposal-4' },
  };
  const positive = {
    responseStatus: 200,
    response,
    requestBody,
    expectedOutputId: 'target-1',
    expectedStepID: 'step-1',
    expectedSnapshotToken: 'snapshot-3',
    expectedDraftVersion: 3,
    expectedDraftDigest: 'digest-3',
    domProposalId: 'proposal-4',
    domReceiptId: 'proposal-4',
    transportEvidence: { ok: true },
  };
  assert.equal(removalProposalEvidence(positive).ok, true);
  assert.equal(removalProposalEvidence({ ...positive, requestBody: { ...requestBody, removeStepIds: ['other-step'] } }).ok, false);
  assert.equal(removalProposalEvidence({ ...positive, response: { ...response, draftDigest: 'different' } }).ok, false);
  assert.equal(removalProposalEvidence({ ...positive, domReceiptId: 'other-receipt' }).ok, false);
  assert.equal(removalProposalEvidence({ ...positive, transportEvidence: { ok: false } }).ok, false);
});

test('removal restoration compares the complete target document', () => {
  const before = { rootResourceType: 'Observation', output: { id: 'target-1', title: 'Output' }, columns: [], construction: { steps: [] } };
  assert.equal(targetDocumentStateEvidence(before, structuredClone(before)).ok, true);
  assert.equal(targetDocumentStateEvidence(before, { ...structuredClone(before), rootResourceType: 'Patient' }).ok, false);
  assert.equal(targetDocumentStateEvidence(undefined, before).ok, false);
  assert.equal(targetDocumentStateEvidence(null, null).ok, false);
});
