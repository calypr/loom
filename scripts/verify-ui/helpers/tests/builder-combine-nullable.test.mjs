import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { caseNamesFor, hasLifecycleContract, registry, scenarioCaseFor } from '../../registry.mjs';
import { builderCancelStateEvidence, nativeResponseScopeEvidence, removalProposalEvidence, targetDocumentStateEvidence } from '../builder-combine-nullable-helpers.mjs';
import { fixtureProjectExplorerCollectionPath } from '../fixture-context.mjs';
import { nullableKeyWitnessExpectations } from '../../workflows/builder-combine-nullable.mjs';

const driver = readFileSync(new URL('../../workflows/builder-combine-nullable.mjs', import.meta.url), 'utf8');
const nullableFixtureURL = new URL('../../../../testdata/verify-combine-nullable-duplicates/', import.meta.url);

test('nullable KEY_JOIN fixture distinguishes omitted subject.reference from explicit JSON null', () => {
  const readRows = (name) => readFileSync(new URL(name, nullableFixtureURL), 'utf8').trim().split('\n').map(JSON.parse);
  const observations = readRows('Observation.ndjson');
  const reports = readRows('DiagnosticReport.ndjson');
  const observationMissing = observations.find((row) => row.id === 'combine-observation-preliminary');
  const observationNull = observations.find((row) => row.id === 'combine-observation-explicit-null');
  const reportMissing = reports.find((row) => row.id === 'combine-observation-final-1');
  const reportNull = reports.find((row) => row.id === 'combine-report-explicit-null');

  assert.deepEqual(observations.map((row) => row.id), [
    'combine-observation-final-1',
    'combine-observation-final-2',
    'combine-observation-preliminary',
    'combine-observation-explicit-null',
    'combine-observation-unmatched',
  ]);
  assert.deepEqual(reports.map((row) => row.id), [
    'combine-observation-final-1',
    'combine-report-explicit-null',
    'combine-observation-final-2',
    'combine-observation-preliminary',
  ]);
  assert.equal(Object.hasOwn(observationMissing, 'subject'), false);
  assert.equal(Object.hasOwn(reportMissing, 'subject'), false);
  assert.equal(Object.hasOwn(observationNull.subject, 'reference'), true);
  assert.equal(observationNull.subject.reference, null);
  assert.equal(Object.hasOwn(reportNull.subject, 'reference'), true);
  assert.equal(reportNull.subject.reference, null);
  const keyWitnessRows = (rows) => rows.map((row) => {
    const hasReference = Object.hasOwn(row.subject ?? {}, 'reference');
    const reference = hasReference ? row.subject.reference : null;
    return [row.id, !hasReference ? 'missing' : reference === null ? 'explicit-null' : 'value', reference];
  });
  assert.deepEqual(keyWitnessRows(observations), nullableKeyWitnessExpectations.observations);
  assert.deepEqual(keyWitnessRows(reports), nullableKeyWitnessExpectations.reports);
  assert.match(driver, /JSON\.stringify\(observationKeyRows\) === JSON\.stringify\(nullableKeyWitnessExpectations\.observations\)\s*&&\s*JSON\.stringify\(reportKeyRows\) === JSON\.stringify\(nullableKeyWitnessExpectations\.reports\)/);
  assert.equal(observations.length, 5);
  assert.equal(reports.length, 4);
});

test('nullable source publication waits on the canonical fixture browser route', () => {
  const project = 'loom_dev_verify_mv37bf7o-a7738e2';
  const publishPath = `${fixtureProjectExplorerCollectionPath(project)}/${encodeURIComponent('explorer-123')}/authoring/v2/publish`;
  assert.equal(publishPath, '/api/v1/projects/loom_dev_verify_mv37bf7o%252Fa7738e2/explorers/explorer-123/authoring/v2/publish');
  assert.match(driver, /fixtureProjectExplorerCollectionPath\(context\.target\.fixtureProject\)/);
});

test('nullable Combine transport binding accepts canonical project routes across Create, proposal, and removal', () => {
  const project = 'loom_dev_verify_mv37bf7o-a7738e2';
  const explorer = 'verify-a7738e2-combine';
  const protectedExplorer = 'loom-dev-bootstrap';
  const scope = {
    uiOrigin: 'http://127.0.0.1:30008',
    apiOrigin: 'http://127.0.0.1:8188',
    project,
    explorer,
    protectedExplorer,
  };
  const collectionPath = fixtureProjectExplorerCollectionPath(project);
  const explorerPath = `${collectionPath}/${encodeURIComponent(explorer)}`;
  const routes = [
    ['CREATE_TABLE', `${explorerPath}/authoring/v2/commands`],
    ['KEY_JOIN proposal', `${explorerPath}/authoring/v2/construction-proposals`],
    ['KEY_JOIN removal', `${explorerPath}/authoring/v2/construction-proposals`],
  ];

  for (const [action, path] of routes) {
    const url = `${scope.uiOrigin}${path}`;
    const evidence = nativeResponseScopeEvidence(url, url, scope);
    assert.equal(evidence.ok, true, `${action} must bind the exact canonical UI proxy route: ${JSON.stringify(evidence)}`);
    assert.equal(evidence.requestPath, path);
    assert.equal(evidence.responsePath, path);
  }

  const legacyAliasPath = `/api/v1/projects/${encodeURIComponent(project)}/explorers/${encodeURIComponent(explorer)}/authoring/v2/commands`;
  const foreignProjectPath = fixtureProjectExplorerCollectionPath('loom_dev_verify_mv37bf7o-foreign') +
    `/${encodeURIComponent(explorer)}/authoring/v2/commands`;
  const protectedPath = `${collectionPath}/${encodeURIComponent(protectedExplorer)}/authoring/v2/commands`;
  for (const path of [legacyAliasPath, foreignProjectPath, protectedPath]) {
    const url = `${scope.uiOrigin}${path}`;
    assert.equal(nativeResponseScopeEvidence(url, url, scope).ok, false,
      `unowned transport must remain rejected: ${path}`);
  }
  assert.match(driver, /project: context\.target\.fixtureProject/,
    'the workflow must provide the raw fixture identity for canonical route classification');
});

test('nullable KEY_JOIN is registered as a separate owned native lifecycle', () => {
  const scenario = registry.find((entry) => entry.id === 'builder-combine-nullable');
  assert.ok(scenario);
  assert.equal(scenario.script, 'builder-combine-nullable.mjs');
  assert.deepEqual(caseNamesFor(scenario), ['lifecycle']);
  const required = scenarioCaseFor(scenario, 'lifecycle').requiredChecks;
  assert.equal(required.length, 63);
  assert.equal(required.length, new Set(required).size);
  for (const name of [
    'both published subject.reference fields are nullable scalar strings',
    'source schemas expose compatible nullable scalar ID keys, scalar status fields, and a numeric Observation value',
    'native CREATE_TABLE request and response bind to the owned UI proxy project and Explorer route',
    'INNER preview receipt binds the exact UI proxy route, target, nullable key pair, output columns, and raw rows',
    'nullable-key fixture proves duplicate shared keys, separate omitted and explicit-null keys, and one left-only key',
    'INNER nullable Join preserves the four shared-key pairs and matches neither omitted nor explicit-null keys',
    'INNER applied rows preserve all four duplicate-key pairs',
    'INNER duplicate-key pairs survive Builder reload',
    'INNER saved step after Apply retains exact current revisions and the nullable subject.reference key pair',
    'INNER saved step after LEFT Cancel reload retains exact current revisions and the nullable subject.reference key pair',
    'LEFT preview receipt before Cancel binds the exact UI proxy route, target, nullable key pair, output columns, and raw rows',
    'LEFT preview receipt before Apply binds the exact UI proxy route, target, nullable key pair, output columns, and raw rows',
    'LEFT preview preserves four duplicate-key pairs and three unmatched rows including omitted and explicit-null keys',
    'LEFT applied output preserves four duplicate-key pairs and three unmatched source rows',
    'LEFT seven rows and explicit-null non-matches survive Builder reload',
    'nullable KEY_JOIN removal preview receipt binds the exact scoped request, removed step, target, snapshot, and DOM receipt',
    'Cancel leaves saved INNER nullable Join rows unchanged after reload',
    'Cancel leaves the full Builder workspace, draft version, and digest unchanged after reload',
    'Cancel removal leaves the full Builder workspace, draft version, and digest unchanged after reload',
    'removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document',
    'LEFT seven rows and explicit-null non-matches survive Builder reload',
    'both published nullable-key source tables remain byte-structured unchanged',
  ]) assert.ok(required.includes(name), 'registry must require ' + name);
});

test('nullable lifecycle is discovered by the official Playwright Test runner', () => {
  const spec = readFileSync(new URL('../../specs/nullable-combine.spec.mjs', import.meta.url), 'utf8');
  assert.match(driver, /export const nullableJoinWorkflow = async \(\{ page, report, action, networkObserver \}, context\) =>/);
  assert.match(driver, /import \{ expect, test \} from '@playwright\/test'/);
  assert.doesNotMatch(driver, /runPlaywrightCase|executeScenario|runNullableJoin/);
  assert.doesNotMatch(driver, /activeAction|page\.locator\('body'\)|await Promise\.all\(entries\.map\(entry => entry\.responsePromise\)/);
  assert.match(driver, /test\.step\(name, async \(\) =>/);
  assert.match(driver, /\}, \{ timeout: STEP_TIMEOUT_MS \}\)/);
  assert.match(spec, /import \{ test \} from '\.\.\/helpers\/fixtures\.mjs'/);
  assert.match(spec, /test\.use\(\{ scenarioID: 'builder-combine-nullable', caseName: 'lifecycle', fixtureDir: 'testdata\/verify-combine-nullable-duplicates' \}\)/);
  assert.match(spec, /test\('nullable KEY_JOIN duplicate-key multiplicity and NULL non-equality lifecycle'/);
});

test('nullable native case authors the exact optional source paths and checks the bound proposal receipt', () => {
  assert.match(driver, /Observation:\s*\['status',\s*'valueInteger',\s*'subject\.reference'\]/);
  assert.match(driver, /DiagnosticReport:\s*\['status',\s*'subject\.reference'\]/);
  assert.match(driver, /nullable-key fixture proves duplicate shared keys, separate omitted and explicit-null keys, and one left-only key/);
  assert.match(driver, /const keyWitnessRows = \(rows\) => rows\.map\(\(row\) =>/);
  assert.match(driver, /leftRows\.length !== 7/);
  assert.match(driver, /explicitNullObservationIDs: observationKeyRows\.filter/);
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
    ['leftRows', 'reload applied LEFT nullable-key table', 'LEFT seven rows and explicit-null non-matches survive Builder reload', false],
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
      proposal: /INNER nullable Join preserves the four shared-key pairs and matches neither omitted nor explicit-null keys/,
      cancel: /Cancel leaves saved INNER nullable Join rows unchanged after reload/,
      apply: /Apply INNER nullable Join action-to-render/,
      savedRows: /INNER applied rows preserve all four duplicate-key pairs/,
      reload: /INNER duplicate-key pairs survive Builder reload/,
      edit: /LEFT applied output preserves four duplicate-key pairs and three unmatched source rows/,
      restoration: /removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document/,
    }],
    [duplicate, {
      choice: /choose nullable KEY_JOIN/,
      proposal: /INNER nullable Join preserves the four shared-key pairs and matches neither omitted nor explicit-null keys/,
      cancel: /Cancel leaves saved INNER nullable Join rows unchanged after reload/,
      apply: /Apply INNER nullable Join action-to-render/,
      savedRows: /INNER applied rows preserve all four duplicate-key pairs/,
      reload: /INNER duplicate-key pairs survive Builder reload/,
      edit: /LEFT applied output preserves four duplicate-key pairs and three unmatched source rows/,
      restoration: /removing nullable KEY_JOIN and reloading restores the exact pre-Combine target document/,
    }],
  ]) {
    for (const [phase, pattern] of Object.entries(expected)) assert.match(checks[coverage.acceptance.checks[phase]], pattern);
  }
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
