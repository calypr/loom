import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { root } from './formal-tools.mjs';
import { routineJourneyFixture, routineJourneyIds, journeyFixture, journeySuite, journeyTest, journeyIds, journeyDoctor, validateJourneyFixture, validateJourneyUI, validateJourneyArtifacts, validateJourneyGo, validateJourneyFailure, validateJourneyReloads, journeySourceProofs, runJourney } from './formal-operation-history.mjs';
const fixture = JSON.parse(readFileSync(join(root, journeyFixture), 'utf8'));
const clone = value => structuredClone(value);
const event = (Action, Test) => ({ Action, ...(Test ? { Test } : {}) });
function goOutput(summary) { return [event('run', journeyTest), ...(summary ? [{ Action: 'output', Test: journeyTest, Output: `BUILDER_OPERATION_REPLAY_JSON ${JSON.stringify(summary)}\n` }] : []), event('pass', journeyTest), event('pass')].map(JSON.stringify).join('\n'); }
function artifacts() {
    const seeds = { schemaVersion: 1, cases: fixture.cases.map(trace => ({ id: trace.id, acceptedWorkspace: { documents: [] } })) };
    const identities = { schemaVersion: 1, cases: fixture.cases.map((trace, i) => ({ id: trace.id, acceptedVersion: 1, acceptedDigest: `sha256:${String(i).repeat(64)}`, snapshotToken: 'snapshot', outputId: trace.initial.workspaceContext.outputId })) };
    const candidates = { schemaVersion: 1, cases: fixture.cases.map((trace, i) => ({ id: trace.id, checkpoints: trace.expectations.filter(row => row.proposalStatus === 'READY').map(row => ({ actionIndex: row.actionIndex, candidateConstruction: { steps: [] }, proposalRequest: { requestId: `request-${i}`, expectedDraftVersion: 1, expectedDraftDigest: identities.cases[i].acceptedDigest, snapshotToken: 'snapshot', outputId: identities.cases[i].outputId, candidateConstruction: { steps: [] } } })) })) };
    const responses = { schemaVersion: 1, cases: candidates.cases.map((row, i) => ({ id: row.id, checkpoints: row.checkpoints.map(cp => ({ actionIndex: cp.actionIndex, requestId: cp.proposalRequest.requestId, requestFingerprint: `fingerprint-${i}`, proposalResponse: { proposalId: `receipt-${i}`, draftVersion: 1, draftDigest: identities.cases[i].acceptedDigest, snapshotToken: 'snapshot', outputId: identities.cases[i].outputId, previewStatus: 'PREVIEW_PENDING' }, previewResult: { receiptId: `receipt-${i}`, outputId: identities.cases[i].outputId } })) })) };
    return { fixture, seeds, identities, candidates, responses };
}
function summary() {
    return { reloads: [{ id: journeyIds[0], actionIndex: 3, draftVersion: 2, draftDigest: `sha256:${'f'.repeat(64)}` }], schemaVersion: 1, traceCaseCount: 2, candidateCaseCount: 2, replayedCaseCount: 2, proposalCheckpointCount: 2, readyProposalCount: 2, servicePreviewCount: 2, uniqueRequestFingerprintCount: 2, uniqueAbstractRequestFingerprintCount: 2, uniqueRawRequestFingerprintCount: 2, skippedCaseCount: 0, needsRepairProposalCount: 0, applyAckFailureCount: 0, applyInvocationCount: 1, applyAckSuccessCount: 1, cases: fixture.cases.map((trace, i) => ({ id: trace.id, proposalCheckpoints: trace.expectations.filter(row => row.proposalStatus === 'READY').map(row => ({ actionIndex: row.actionIndex, status: 'READY', proposalIdPresent: true, missingInputCount: 0, stepIds: row.stepIds, groupOutputTypes: { aggregate: row.groupOutputTypes[trace.initial.aggregateOutputId] ?? null, key: row.groupOutputTypes[trace.initial.keyOutputId] ?? null }, removedStepIds: row.removedStepIds, downstreamInputTypes: Object.fromEntries(Object.entries(row.downstreamInputTypes).map(([id, value]) => [id, value.type])), downstreamOutputTypes: Object.fromEntries(Object.entries(row.downstreamOutputTypes).map(([id, value]) => [id, value.type])), requestId: `request-${i}`, requestFingerprint: `fingerprint-${i}` })), applyAck: trace.actions.flatMap((row, index) => row.kind === 'applyAck' ? [{ actionIndex: index, result: 'SUCCESS', acceptedVersionBefore: 1, acceptedVersionAfter: 2, acceptedDigestBefore: `sha256:${'0'.repeat(64)}`, acceptedDigestAfter: `sha256:${'f'.repeat(64)}` }] : []) })) };
}
test('fixed witnesses retain successful Apply and independent Calculate cascade cases', () => {
    assert.equal(validateJourneyFixture(fixture), fixture); assert.equal(fixture.cases.length, 2);
    assert.ok(fixture.cases[0].actions.some(row => row.kind === 'applyAck'));
    assert.ok(fixture.cases[1].expectations.some(row => row.stepIds.includes('calculate-from-aggregate') && row.removedStepIds?.includes('filter-by-group-key')));
    for (const change of [d => d.cases.pop(), d => d.cases.push(d.cases[0]), d => { d.cases[0].id = 'foreign'; }, d => { d.cases[0].actions[1].kind = 'cancel'; }]) { const bad = clone(fixture); change(bad); assert.throws(() => validateJourneyFixture(bad)); }
});
test('artifact inventories reject missing, duplicate, foreign and substituted checkpoints or identities', () => {
    assert.equal(validateJourneyArtifacts(artifacts()), true);
    for (const change of [d => d.seeds.cases.pop(), d => d.identities.cases.push(d.identities.cases[0]), d => { d.responses.cases[0].id = 'foreign'; }, d => { d.candidates.cases[0].checkpoints[0].actionIndex = 99; }, d => { d.candidates.cases[0].checkpoints[0].proposalRequest.expectedDraftDigest = 'foreign'; }, d => { d.responses.cases[0].checkpoints[0].requestId = 'foreign'; }, d => { d.responses.cases[0].checkpoints[0].previewResult.receiptId = 'foreign'; }, d => { d.responses.cases[0].checkpoints[0].proposalResponse.previewStatus = 'FAILED'; }]) { const bad = artifacts(); change(bad); assert.throws(() => validateJourneyArtifacts(bad)); }
    const bound = { ...artifacts(), actualGo: summary() }; assert.equal(validateJourneyArtifacts(bound), true); bound.responses.cases[0].checkpoints[0].requestFingerprint = 'substituted'; assert.throws(() => validateJourneyArtifacts(bound));
});
test('UI proof requires the exact selected phase inventory and actual suite', () => {
    const report = { success: true, numFailedTests: 0, testResults: [{ name: join(root, 'ui/packages/loom-ui', journeySuite), assertionResults: [{ status: 'passed', fullName: 'BuilderWorkspace operation history actual candidate capture reviews proposed removals before explicit Apply' }, { status: 'pending', fullName: 'filtered fixture' }] }] };
    assert.equal(validateJourneyUI(report, 'ui-panel').selected.length, 1);
    for (const change of [r => { r.success = false; }, r => { r.testResults[0].name = '/foreign'; }, r => { r.testResults[0].assertionResults[0].status = 'pending'; }, r => { r.testResults[0].assertionResults[0].fullName = 'foreign'; }, r => r.testResults[0].assertionResults.push(r.testResults[0].assertionResults[0])]) { const bad = clone(report); change(bad); assert.throws(() => validateJourneyUI(bad, 'ui-panel')); }
});
test('actual Go JSON requires named execution with complete matching semantic observations', () => {
    assert.equal(validateJourneyGo(goOutput(summary()), 'full', fixture).applyAckSuccessCount, 1);
    for (const change of [s => { s.schemaVersion = 2; }, s => { s.skippedCaseCount = 1; }, s => { s.servicePreviewCount = 0; }, s => s.cases.pop(), s => { s.cases[1].proposalCheckpoints[0].stepIds = ['group-step']; }, s => { s.cases[0].applyAck[0].acceptedVersionAfter = 1; }, s => { s.cases[1].proposalCheckpoints[0].requestId = 'request-0'; }]) { const bad = summary(); change(bad); assert.throws(() => validateJourneyGo(goOutput(bad), 'full', fixture)); }
    assert.throws(() => validateJourneyGo(goOutput(), 'full', fixture));
    assert.throws(() => validateJourneyGo(goOutput(summary()) + '\n' + JSON.stringify(event('skip', journeyTest)), 'full', fixture));
    assert.throws(() => validateJourneyGo(goOutput(summary()).replace('"run"', '"output"'), 'full', fixture));
});
test('unknown features and empty source overrides cannot initiate a passing journey', () => {
    assert.throws(() => journeyDoctor({ feature: 'foreign' }), /Unknown journey/);
    for (const productionSource of ['', null, false, {}, { foreign: 'package x' }, { 'internal/explorer/lifecycle/construction_proposal.go': '' }]) assert.throws(() => runJourney({ output: '/unused-journey-test', productionSource }));
});


test('source faults require an actual named composition failure, never compiler or tool failure', () => {
    const message = 'trace operation-history-group-key-cascade-count-to-mean-ready-preserves-calculate action 2 steps [group-step] != model [group-step calculate-from-aggregate]';
    const failure = { phase: 'go-full', exit: 1, stdout: [event('run', journeyTest), { Action: 'output', Test: journeyTest, Output: message }, event('fail', journeyTest), event('fail')].map(JSON.stringify).join('\n') };
    assert.equal(validateJourneyFailure(failure), true);
    assert.equal(validateJourneyFailure({ ...failure, stdout: failure.stdout.replace('ready-preserves-calculate', 'apply-ack') }), true);
    for (const value of [{ ...failure, phase: 'go-prepare' }, { ...failure, exit: null }, { ...failure, stdout: 'compiler failed' }, { ...failure, stdout: [event('fail')].map(JSON.stringify).join('\n') }, { ...failure, stdout: failure.stdout + '\n' + JSON.stringify(event('skip', journeyTest)) }, { ...failure, stdout: failure.stdout.replace(message, 'unrelated assertion') }]) assert.throws(() => validateJourneyFailure(value));
});

test('missing Go blocks readiness without reporting a passing journey', () => {
    const child = spawnSync(process.execPath, ['--input-type=module', '-e', `import {journeyDoctor} from ${JSON.stringify(new URL('./formal-operation-history.mjs', import.meta.url).href)}; console.log(JSON.stringify(journeyDoctor()));`], { env: { ...process.env, GO: '/missing-journey-go' }, encoding: 'utf8', timeout: 5000 });
    assert.ifError(child.error); assert.equal(child.status, 1); assert.match(child.stderr, /Install Go/); assert.doesNotMatch(child.stdout, /ready-for-journey|passed/);
});


test('Apply acknowledgements are typed and bound to the prepared version and digest', () => {
    for (const mutate of [
        ack => { ack.acceptedVersionBefore = '1'; ack.acceptedVersionAfter = '11'; },
        ack => { ack.acceptedVersionBefore = 999; ack.acceptedVersionAfter = 1000; },
        ack => { ack.acceptedDigestBefore = `sha256:${'e'.repeat(64)}`; },
        ack => { ack.acceptedDigestAfter = 42; },
        ack => { ack.acceptedVersionAfter = Number.MAX_SAFE_INTEGER + 1; },
    ]) {
        const actualGo = summary(); mutate(actualGo.cases[0].applyAck[0]);
        assert.throws(() => validateJourneyArtifacts({ ...artifacts(), actualGo }));
    }
    for (const mutate of [
        ack => { ack.acceptedVersionBefore = '1'; ack.acceptedVersionAfter = '11'; },
        ack => { ack.acceptedDigestBefore = 'foreign'; },
        ack => { ack.acceptedDigestAfter = 42; },
        ack => { ack.acceptedVersionBefore = 0; ack.acceptedVersionAfter = 1; },
    ]) {
        const actualGo = summary(); mutate(actualGo.cases[0].applyAck[0]);
        assert.throws(() => validateJourneyGo(goOutput(actualGo), 'full', fixture));
    }
});


function reloadArtifacts() {
    const construction = { version: 1, steps: [{ id: 'group-step', operation: { kind: 'GROUP', group: { aggregates: [{ operation: 'MEAN' }] } } }, { id: 'filter-by-aggregate', operation: { kind: 'FILTER' } }] };
    const storedWorkspace = { documents: [{ output: { id: fixture.cases[0].initial.workspaceContext.outputId }, construction }] };
    const beforeWorkspace = clone(storedWorkspace); beforeWorkspace.documents[0].construction.steps[0].operation.group.aggregates[0].operation = 'COUNT_ROWS';
    return { fixture, identities: artifacts().identities, actualGo: summary(), reloads: { schemaVersion: 1, cases: [{ id: journeyIds[0], actionIndex: 3, outputId: fixture.cases[0].initial.workspaceContext.outputId, stageId: 'source_projection', before: { kind: 'ExplorerBuilderState', draftVersion: 1, draftDigest: `sha256:${'0'.repeat(64)}`, catalog: { snapshotToken: 'snapshot' }, workspace: beforeWorkspace }, after: { kind: 'ExplorerBuilderState', draftVersion: 2, draftDigest: `sha256:${'f'.repeat(64)}`, catalog: { snapshotToken: 'snapshot' }, workspace: clone(storedWorkspace) }, storedWorkspace, capabilities: { snapshotToken: 'snapshot', draftVersion: 2, draftDigest: `sha256:${'f'.repeat(64)}`, outputId: fixture.cases[0].initial.workspaceContext.outputId, stageId: 'source_projection', selectedStage: { id: 'source_projection' }, baseConstruction: clone(construction) } }] } };
}
test('reload artifacts bind the exact stored payload and capabilities to the real Apply acknowledgement', () => {
    assert.equal(validateJourneyReloads(reloadArtifacts()).cases.length, 1);
    for (const mutate of [
        d => d.reloads.cases.pop(), d => d.reloads.cases.push(d.reloads.cases[0]), d => { d.reloads.cases[0].id = 'foreign'; },
        d => { d.reloads.cases[0].actionIndex = 99; }, d => { d.reloads.cases[0].before.draftVersion = 999; },
        d => { d.reloads.cases[0].before.draftDigest = `sha256:${'e'.repeat(64)}`; },
        d => { d.reloads.cases[0].after.draftVersion = '2'; }, d => { d.reloads.cases[0].after.draftDigest = `sha256:${'e'.repeat(64)}`; },
        d => { d.reloads.cases[0].after.workspace.documents[0].construction.steps[0].operation.group.aggregates[0].operation = 'SUM'; },
        d => { d.reloads.cases[0].capabilities.snapshotToken = 'stale'; }, d => { d.reloads.cases[0].capabilities.draftVersion = 1; },
        d => { d.reloads.cases[0].capabilities.baseConstruction.steps.pop(); }, d => { d.actualGo.reloads[0].draftDigest = 'foreign'; },
        d => { d.actualGo.cases[0].applyAck[0].acceptedVersionAfter = '2'; },
    ]) { const bad = reloadArtifacts(); mutate(bad); assert.throws(() => validateJourneyReloads(bad)); }
});
test('Go reload metadata cannot be missing or claim a different saved revision', () => {
    for (const mutate of [s => { s.reloads = []; }, s => s.reloads.push(s.reloads[0]), s => { s.reloads[0].id = journeyIds[1]; }, s => { s.reloads[0].draftVersion = 999; }, s => { s.reloads[0].draftDigest = 'foreign'; }]) { const bad = summary(); mutate(bad); assert.throws(() => validateJourneyGo(goOutput(bad), 'full', fixture)); }
});
test('reload source controls require the exact semantic read or editor assertion', () => {
    const readProof = journeySourceProofs.find(row => row.branch === 'builder-reload'), editorProof = journeySourceProofs.find(row => row.branch === 'editor-reload');
    const readFailure = { phase: 'go-full', exit: 1, stdout: [event('run', journeyTest), { Action: 'output', Test: journeyTest, Output: 'fresh Builder lost saved Group: err=<nil>' }, event('fail', journeyTest), event('fail')].map(JSON.stringify).join('\n') };
    assert.equal(validateJourneyFailure(readFailure, readProof), true);
    assert.throws(() => validateJourneyFailure({ ...readFailure, stdout: readFailure.stdout.replace('fresh Builder lost saved Group:', 'unrelated error:') }, readProof));
    const report = { success: false, numFailedTests: 1, numPassedTests: 0, testResults: [{ name: join(root, 'ui/packages/loom-ui', journeySuite), assertionResults: [{ status: 'failed', fullName: 'BuilderWorkspace operation history actual candidate capture keeps the saved Group operation and identifiers across remounts', failureMessages: ["AssertionError: expected 'SUM' to be 'MEAN'"] }] }] };
    assert.equal(validateJourneyFailure({ phase: 'ui-reload', exit: 1 }, editorProof, report), true);
    for (const mutate of [r => { r.success = true; }, r => { r.testResults[0].name = '/foreign'; }, r => { r.testResults[0].assertionResults[0].failureMessages = ['compile failed']; }, r => { r.testResults[0].assertionResults[0].status = 'pending'; }, r => r.testResults[0].assertionResults.push(r.testResults[0].assertionResults[0])]) { const bad = clone(report); mutate(bad); assert.throws(() => validateJourneyFailure({ phase: 'ui-reload', exit: 1 }, editorProof, bad)); }
});
test('every journey source control matches a unique reviewed production boundary', () => {
    assert.equal(journeySourceProofs.length, 3);
    assert.deepEqual([...new Set(journeySourceProofs.map(row => row.property))].sort(), ['Inv_GroupKeyCascadePreservesIndependentCalculate', 'Inv_ReloadRestoresPersistedGroup']);
    for (const proof of journeySourceProofs) for (const [file, before, after] of proof.edits) { assert.equal(readFileSync(join(root, file), 'utf8').split(before).length, 2); assert.notEqual(before, after); }
});


test('semantic source-fault text cannot conceal runtime panics, timeouts or UI compilation errors', () => {
    const readProof = journeySourceProofs.find(row => row.branch === 'builder-reload'), editorProof = journeySourceProofs.find(row => row.branch === 'editor-reload');
    for (const diagnostic of ['panic: test timed out after 30s', 'fatal error: concurrent map read and map write', 'test timeout']) {
        const stdout = [event('run', journeyTest), { Action: 'output', Test: journeyTest, Output: `fresh Builder lost saved Group: err=<nil>\n${diagnostic}\n` }, event('fail', journeyTest), event('fail')].map(JSON.stringify).join('\n');
        assert.throws(() => validateJourneyFailure({ phase: 'go-full', exit: 1, stdout }, readProof));
    }
    const report = { success: false, numFailedTests: 1, numPassedTests: 0, testResults: [{ name: join(root, 'ui/packages/loom-ui', journeySuite), assertionResults: [{ status: 'failed', fullName: 'BuilderWorkspace operation history actual candidate capture keeps the saved Group operation and identifiers across remounts', failureMessages: ["AssertionError: expected 'SUM' to be 'MEAN'"] }] }] };
    for (const stderr of ['Vitest caught 1 unhandled error', 'Transform failed: SyntaxError', 'Error: Test timed out']) assert.throws(() => validateJourneyFailure({ phase: 'ui-reload', exit: 1, stderr }, editorProof, report));
    const extra = clone(report); extra.testResults[0].assertionResults[0].failureMessages.push('TypeError: unexpected runtime exception');
    assert.throws(() => validateJourneyFailure({ phase: 'ui-reload', exit: 1 }, editorProof, extra));
});


test('recovered editor fault credit requires the exact real initialization assertions and suite', () => {
    const proof = journeySourceProofs.find(row => row.branch === 'editor-reload');
    const ids = ['operation-history-request-a1fb7229a61e'];
    const prefix = 'BuilderWorkspace operation history actual candidate capture ';
    const report = { success: false, numFailedTests: 2, numPassedTests: 11, testResults: [{ name: join(root, 'ui/packages/loom-ui', journeySuite), assertionResults: [
        ...ids.map(id => ({ status: 'failed', fullName: `${prefix}replays each operation schedule through ConstructionReshapeEditor: '${id}'`, failureMessages: [`AssertionError: ${id}: expected 'SUM' to be 'MEAN'`] })),
        { status: 'failed', fullName: `${prefix}accounts for every exercised schedule and proposal checkpoint`, failureMessages: ['AssertionError: expected 10 to be 11'] },
    ] }] };
    const error = { phase: 'ui-capture', exit: 1 };
    assert.equal(validateJourneyFailure(error, proof, report), true);
    for (const mutate of [r => { r.testResults[0].name = '/foreign'; }, r => { r.numFailedTests = 0; }, r => { r.testResults[0].assertionResults[0].fullName = 'foreign'; }, r => { r.testResults[0].assertionResults[0].failureMessages = ['TypeError: broken import']; }, r => { r.testResults[0].assertionResults.pop(); }]) {
        const bad = clone(report); mutate(bad); assert.throws(() => validateJourneyFailure(error, proof, bad));
    }
});


test('routine journeys retain every saved shape, Cancel, identity repair and numeric type directions', () => {
    const selected = routineJourneyFixture();
    assert.equal(validateJourneyFixture(selected), selected);
    assert.deepEqual(selected.cases.map(row => row.id), routineJourneyIds);
    assert.equal(selected.cases.length, 11);
    assert.equal(selected.cases.filter(row => row.actions.some(a => a.kind === 'applyAck')).length, 5);
    assert.equal(selected.cases.filter(row => row.actions.some(a => a.kind === 'cancel')).length, 1);
    assert.ok(selected.cases.some(row => row.actions.some(a => a.kind === 'restoreKey') && row.expectations.at(-1).removedStepIds.includes('filter-by-group-key')));
    assert.ok(selected.cases.some(row => row.actions.some(a => a.key === 'B') && Object.values(row.expectations.at(-1).groupOutputTypes).includes('boolean')));
    assert.ok(selected.cases.some(row => row.initial.aggregate === 'MEAN' && !row.actions.some(a => a.kind === 'editAggregate')));
    assert.ok(selected.cases.some(row => row.initial.aggregate === 'MEAN' && row.expectations.at(-1).candidateAggregate === 'COUNT_ROWS'));
    for (const change of [x => x.cases.pop(), x => { x.cases[1] = x.cases[0]; }, x => { x.cases[0].expectations[0].proposalStatus = 'BROKEN'; }]) {
        const bad = clone(selected); change(bad); assert.throws(() => validateJourneyFixture(bad));
    }
});
