import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { caseNamesFor, hasLifecycleContract, registry, scenarioCaseFor } from '../../registry.mjs';
import { builderCancelStateEvidence, nativeResponseScopeEvidence, removalProposalEvidence, targetDocumentStateEvidence } from '../builder-combine-nullable-helpers.mjs';
import { classifyNativeBrowserApiRequest } from '../native-browser-api-scope.mjs';
import { installNullableNativeAbortProbe } from '../../workflows/builder-combine-nullable.mjs';

const driver = readFileSync(new URL('../../workflows/builder-combine-nullable.mjs', import.meta.url), 'utf8');

test('nullable KEY_JOIN is registered as a separate owned native lifecycle', () => {
  const scenario = registry.find((entry) => entry.id === 'builder-combine-nullable');
  assert.ok(scenario);
  assert.equal(scenario.script, 'builder-combine-nullable.mjs');
  assert.deepEqual(caseNamesFor(scenario), ['lifecycle']);
  const required = scenarioCaseFor(scenario, 'lifecycle').requiredChecks;
  for (const name of [
    'both published subject.reference fields are nullable scalar strings',
    'source schemas expose compatible nullable scalar ID keys, scalar status fields, and a numeric Observation value',
    'native CREATE_TABLE request and response bind to the owned UI proxy project and Explorer route',
    'INNER preview receipt binds the exact UI proxy route, target, nullable key pair, output columns, and raw rows',
    'nullable-key fixture proves two duplicate rows per side for one shared key, NULL on both sides, and one left-only key',
    'INNER nullable Join preserves all four pairs from the 2x2 duplicate key and never matches NULL to NULL',
    'INNER applied rows preserve all four duplicate-key pairs',
    'INNER duplicate-key pairs survive Builder reload',
    'LEFT preview receipt binds the exact UI proxy route, target, nullable key pair, output columns, and raw rows',
    'LEFT preview preserves four duplicate-key pairs, both unmatched left rows, and NULL non-equality',
    'LEFT applied output preserves duplicate-key multiplicity and both unmatched left rows',
    'LEFT duplicate-key multiplicity and null projections survive Builder reload',
    'nullable KEY_JOIN removal preview receipt binds the exact scoped request, removed step, target, snapshot, and DOM receipt',
    'Cancel leaves saved INNER nullable Join rows unchanged after reload',
    'Cancel leaves the full Builder workspace, draft version, and digest unchanged after reload',
    'Cancel removal leaves the full Builder workspace, draft version, and digest unchanged after reload',
    'removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document',
    'LEFT duplicate-key multiplicity and null projections survive Builder reload',
    'both published nullable-key source tables remain byte-structured unchanged',
  ]) assert.ok(required.includes(name), 'registry must require ' + name);
});

test('nullable lifecycle is discovered by the official Playwright Test runner', () => {
  const spec = readFileSync(new URL('../../specs/nullable-combine.spec.mjs', import.meta.url), 'utf8');
  assert.match(driver, /export const nullableJoinWorkflow = async \(\{ page, report, action, nativeRequestLedger \}, context\) =>/);
  assert.match(driver, /import \{ expect, test \} from '@playwright\/test'/);
  assert.doesNotMatch(driver, /runPlaywrightCase|executeScenario|runNullableJoin/);
  assert.doesNotMatch(driver, /activeAction|page\.locator\('body'\)|await Promise\.all\(entries\.map\(entry => entry\.responsePromise\)/);
  assert.match(driver, /test\.step\(name, async \(\) =>/);
  assert.match(driver, /\}, \{ timeout: STEP_TIMEOUT_MS \}\)/);
  assert.equal((driver.match(/await expect\.poll\(/g) ?? []).length, 3);
  assert.match(spec, /import \{ test \} from '\.\.\/helpers\/fixtures\.mjs'/);
  assert.match(spec, /test\.use\(\{ scenarioID: 'builder-combine-nullable', caseName: 'lifecycle', fixtureDir: 'testdata\/verify-combine-nullable-duplicates' \}\)/);
  assert.match(spec, /test\('nullable KEY_JOIN duplicate-key multiplicity and NULL non-equality lifecycle'/);
  assert.match(spec, /nativeRequestLedger: workflow\.nativeRequestLedger/);
});

test('the registered nullable spec installs its probe on the selected Explorer and UI proxy origin', async () => {
  const spec = readFileSync(new URL('../../specs/nullable-combine.spec.mjs', import.meta.url), 'utf8');
  const installAt = driver.indexOf('await installNullableNativeAbortProbe(');
  assert.ok(installAt > driver.indexOf('report.target.explorer = explorer'));
  assert.ok(installAt < driver.indexOf('const addRoot = async'));
  assert.match(spec, /nullableJoinWorkflow\(/);

  let binding;
  const calls = [];
  const browserContext = {
    exposeBinding: async (name, callback) => { calls.push(['binding', name]); binding = callback; },
    addInitScript: async (source) => calls.push(['init', source]),
  };
  const page = {
    context: () => browserContext,
    evaluate: async (source) => calls.push(['current-page', source]),
  };
  const report = {};
  await installNullableNativeAbortProbe({
    page, report, project: 'loom_dev_verify_case027', explorer: 'verify-case027-combine',
    uiProxyOrigin: 'http://127.0.0.1:30008/path-is-ignored',
  });

  assert.deepEqual(calls.map(([kind]) => kind), ['binding', 'init', 'current-page']);
  assert.equal(calls[0][1], '__loomNativeAbortProbeBinding');
  assert.equal(calls[1][1], calls[2][1]);
  assert.ok(calls[1][1].includes('project: "loom_dev_verify_case027"'));
  assert.ok(calls[1][1].includes('explorer: "verify-case027-combine"'));
  assert.ok(calls[1][1].includes('apiOrigin: "http://127.0.0.1:30008"'));
  assert.ok(!calls[1][1].includes('8188'), 'the probe must use the browser UI proxy origin, not the backend origin');

  const installed = { kind: 'probe-installed', project: 'loom_dev_verify_case027', explorer: 'verify-case027-combine' };
  binding({}, JSON.stringify(installed));
  assert.deepEqual(report.nativeAbortProbeEvents, [installed]);
});

test('nullable native case authors the exact optional source paths and checks the bound proposal receipt', () => {
  assert.match(driver, /Observation:\s*\['status',\s*'valueInteger',\s*'subject\.reference'\]/);
  assert.match(driver, /DiagnosticReport:\s*\['status',\s*'subject\.reference'\]/);
  assert.match(driver, /nullable-key fixture proves two duplicate rows per side for one shared key, NULL on both sides, and one left-only key/);
  assert.match(driver, /duplicatedObservationIDs\.length !== 2 \|\| duplicatedReportIDs\.length !== 2/);
  assert.match(driver, /duplicateKeyInnerMultiplicity: `\$\{duplicatedObservationIDs\.length\}x\$\{duplicatedReportIDs\.length\}`/);
  assert.match(driver, /column\.clickhouseType === 'Nullable\(String\)' && column\.nullable === true && column\.repeated === false/);
  assert.match(driver, /constructionProposalPreviewEvidence\(/);
  assert.match(driver, /leftColumnId === expectedKeyIDs\[0\].*rightColumnId === expectedKeyIDs\[1\]/s);
  assert.match(driver, /nullMatchesNull: false/);
});

test('nullable Join reload budgets include exact rows and rooted target restoration', () => {
  const start = driver.indexOf('const reloadTarget =');
  const end = driver.indexOf('\nconst openSavedEdit', start);
  const reloadHelper = driver.slice(start, end);
  assert.match(reloadHelper, /expectedRows, name, exactRowsName, verifySavedState/);
  assert.match(reloadHelper, /after: async \(\) => \{\s*await waitFor\(page, savedPreview\(expectedRows\.length\), 5000\);\s*exactRows\(report, exactRowsName, await readGrid\(page\), \['Observation ID', 'Report ID'\], expectedRows\);\s*if \(verifySavedState\) await verifySavedState\(\);\s*\}/);
  for (const [rows, timingName, exactRowsName, verifySavedState] of [
    ['innerRows', 'reload INNER nullable-key table', 'INNER duplicate-key pairs survive Builder reload', false],
    ['innerRows', 'reload saved INNER after LEFT Cancel', 'Cancel leaves saved INNER nullable Join rows unchanged after reload', true],
    ['leftRows', 'reload applied LEFT nullable-key table', 'LEFT duplicate-key multiplicity and null projections survive Builder reload', false],
    ['leftRows', 'reload saved LEFT after removal Cancel', 'Cancel removal preserves the exact LEFT nullable Join rows after reload', true],
  ]) {
    const callStart = driver.indexOf(`await reloadTarget(report, page, target.outputId, ${rows}, '${timingName}', '${exactRowsName}'`);
    assert.notEqual(callStart, -1, `reload timing must include exact ${rows} proof: ${timingName}`);
    const callEnd = verifySavedState ? driver.indexOf('\n  });', callStart) : driver.indexOf('\n', callStart);
    const call = driver.slice(callStart, callEnd);
    if (verifySavedState) {
      assert.match(call, /, async \(\) => \{/);
      assert.match(call, /readBuilder\(context, explorer\)/);
      assert.match(call, /builderCancelStateEvidence\(/);
      assert.match(call, /assertSavedStep\(/);
    } else {
      assert.match(call, /;$/);
    }
  }
  const removalReloadStart = driver.indexOf("name: 'reload nullable KEY_JOIN removal result'");
  const removalReloadEnd = driver.indexOf('\n  const finalBuilder', removalReloadStart);
  const removalReload = driver.slice(removalReloadStart, removalReloadEnd);
  assert.match(removalReload, /after: async \(\) => \{\s*await waitFor\(page, emptyTargetReady\(target\.outputId\), 5000\);\s*const afterRemoval = await readBuilder\(context, explorer\);[\s\S]*?check\(report, 'persistence', 'removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document', restoredEvidence\.ok/);
});

test('nullable and duplicate-key coverage map to exact native lifecycle checks without claiming a browser pass', () => {
  const scenario = registry.find((entry) => entry.id === 'builder-combine-nullable');
  const duplicate = registry.find((entry) => entry.id === 'builder-combine')
    .coverage.find((entry) => entry.feature === 'duplicate-key multiplicity on nullable Join keys');
  const nullable = scenario.coverage.find((entry) => entry.feature === 'nullable scalar KEY_JOIN ordinary SQL NULL equality and LEFT preservation');
  assert.equal(hasLifecycleContract(nullable, scenario), true);
  assert.equal(hasLifecycleContract(duplicate, registry.find((entry) => entry.id === 'builder-combine')), true);
  assert.equal(duplicate.acceptance.scenario, scenario.id);
  assert.equal(nullable.acceptance.case, 'lifecycle');
  const checks = scenarioCaseFor(scenario, 'lifecycle').requiredChecks;
  for (const [coverage, expected] of [
    [nullable, {
      choice: /choose nullable KEY_JOIN/,
      proposal: /INNER nullable Join preserves all four pairs.*never matches NULL to NULL/,
      cancel: /Cancel leaves saved INNER nullable Join rows unchanged after reload/,
      apply: /Apply INNER nullable Join action-to-render/,
      savedRows: /INNER applied rows preserve all four duplicate-key pairs/,
      reload: /INNER duplicate-key pairs survive Builder reload/,
      edit: /LEFT applied output preserves duplicate-key multiplicity and both unmatched left rows/,
      restoration: /removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document/,
    }],
    [duplicate, {
      choice: /choose nullable KEY_JOIN/,
      proposal: /INNER nullable Join preserves all four pairs.*never matches NULL to NULL/,
      cancel: /Cancel leaves saved INNER nullable Join rows unchanged after reload/,
      apply: /Apply INNER nullable Join action-to-render/,
      savedRows: /INNER applied rows preserve all four duplicate-key pairs/,
      reload: /INNER duplicate-key pairs survive Builder reload/,
      edit: /LEFT applied output preserves duplicate-key multiplicity and both unmatched left rows/,
      restoration: /removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document/,
    }],
  ]) {
    for (const [phase, pattern] of Object.entries(expected)) assert.match(checks[coverage.acceptance.checks[phase]], pattern);
  }
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
  assert.match(driver, /import \{ classifyNativeBrowserApiRequest \} from '\.\.\/helpers\/native-browser-api-scope\.mjs'/);
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
