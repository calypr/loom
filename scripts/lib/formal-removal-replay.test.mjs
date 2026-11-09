import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { root } from './formal-tools.mjs';
import { removalModel, removalProperties, removalFile, removalSourceProofs, validateRemovalInput, expectedRemoval, checkRemovalResults, replayRemovalWitness, removalCounterexampleWitness, validateRemovalGraph } from './formal-removal-replay.mjs';
const fields = ['receiptMatches', 'provenanceMatches', 'authorizationMatches', 'previewCompletes', 'concurrentDraftChange'];
const input = (overrides = {}) => ({ receiptMatches: true, provenanceMatches: true, authorizationMatches: true, previewCompletes: true, concurrentDraftChange: false, ...overrides });
const witness = value => ({ schemaVersion: 1, kind: 'population-removal', model: removalModel, property: removalProperties[!value.receiptMatches ? 0 : !value.provenanceMatches || !value.authorizationMatches ? 1 : !value.previewCompletes ? 2 : value.concurrentDraftChange ? 3 : 4], input: value, expected: expectedRemoval(value) });
const result = value => ({ id: 'case', ...expectedRemoval(value), error: expectedRemoval(value).accepted ? '' : 'controlled error' });
test('removal boundary requires valid requests and preserves both initial and competing heads', () => {
    assert.deepEqual(expectedRemoval(input()), { accepted: true, previewCalls: 1, saveAttempts: 1, successfulSaves: 1, preservedHead: false, exactCandidate: true, conflict: false });
    assert.deepEqual(expectedRemoval(input({ concurrentDraftChange: true })), { accepted: false, previewCalls: 1, saveAttempts: 1, successfulSaves: 0, preservedHead: true, exactCandidate: false, conflict: true });
    for (const field of ['receiptMatches', 'provenanceMatches', 'authorizationMatches']) assert.equal(expectedRemoval(input({ [field]: false })).saveAttempts, 0);
    assert.equal(expectedRemoval(input({ previewCompletes: false })).preservedHead, true);
    for (const invalid of [null, {}, { ...input(), extra: false }, { ...input(), receiptMatches: 'true' }]) assert.throws(() => validateRemovalInput(invalid));
});
test('Go result inventory rejects missing, duplicate, foreign and malformed observations', () => {
    const value = input(), cases = [{ id: 'case', ...value }], record = { schemaVersion: 1, results: [result(value)] };
    assert.equal(checkRemovalResults(record, cases).accepted, 1);
    for (const mutate of [r => r.results.pop(), r => r.results.push(r.results[0]), r => { r.results[0].id = 'foreign'; }, r => { r.results[0].accepted = 'true'; }, r => { r.results[0].saveAttempts = -1; }, r => { r.results[0].error = 'bad'; }, r => { r.extra = 1; }]) { const bad = structuredClone(record); mutate(bad); assert.throws(() => checkRemovalResults(bad, cases)); }
    const broken = structuredClone(record); broken.results[0].successfulSaves = 0;
    assert.throws(() => checkRemovalResults(broken, cases), error => error.code === 'FORMAL_PROPERTY' && error.property === removalProperties[4]);
});
test('empty or unknown source overrides block before tool invocation', async () => {
    for (const productionSource of ['', ' ', null, false, 0, {}, { [removalFile]: '' }, { foreign: 'package x' }]) await assert.rejects(() => replayRemovalWitness(witness(input()), { productionSource }));
    await assert.rejects(() => replayRemovalWitness({ ...witness(input()), expected: {} }));
});
test('counterexamples require the exact two-action candidate boundary and a violated property', () => {
    const value = input({ previewCompletes: false }), initial = { removalPhase: 'choose', removalInput: Object.fromEntries(fields.map(field => [field, true])), accepted: false, previewCalls: 0, saveAttempts: 0, successfulSaves: 0, preservedHead: true, exactCandidate: false, conflict: false };
    const record = { property: removalProperties[2], states: [{ action: '', vars: initial }, { action: 'ChooseRemoval', vars: { ...initial, removalPhase: 'candidate', removalInput: value } }, { action: 'ApplyRemoval', vars: { removalPhase: 'checked', removalInput: value, ...expectedRemoval(value), accepted: true } }] };
    assert.deepEqual(removalCounterexampleWitness(record), witness(value));
    for (const mutate of [r => r.states.pop(), r => { r.states[1].action = 'Unknown'; }, r => { r.states[1].vars.previewCalls = 1; }, r => { r.states[2].vars = { ...r.states[2].vars, ...expectedRemoval(value) }; }, r => { r.property = removalProperties[0]; }]) { const bad = structuredClone(record); mutate(bad); assert.throws(() => removalCounterexampleWitness(bad)); }
});
test('unrelated observations cannot impersonate any named removal invariant', () => {
    const families = [
        [input({ receiptMatches: false }), ['exactCandidate', 'conflict']],
        [input({ provenanceMatches: false }), ['previewCalls', 'exactCandidate', 'conflict']],
        [input({ previewCompletes: false }), ['exactCandidate', 'conflict']],
        [input({ concurrentDraftChange: true }), ['exactCandidate']],
    ];
    const initial = { removalPhase: 'choose', removalInput: Object.fromEntries(fields.map(field => [field, true])), accepted: false, previewCalls: 0, saveAttempts: 0, successfulSaves: 0, preservedHead: true, exactCandidate: false, conflict: false };
    for (const [value, unrelated] of families) {
        for (const field of unrelated) {
            const expected = expectedRemoval(value), changed = { ...expected, [field]: typeof expected[field] === 'boolean' ? !expected[field] : 1 - expected[field] };
            const decoded = { property: witness(value).property, states: [{ action: '', vars: initial }, { action: 'ChooseRemoval', vars: { ...initial, removalPhase: 'candidate', removalInput: value } }, { action: 'ApplyRemoval', vars: { removalPhase: 'checked', removalInput: value, ...changed } }] };
            assert.throws(() => removalCounterexampleWitness(decoded), /does not violate its named property/);
            assert.throws(() => checkRemovalResults({ schemaVersion: 1, results: [{ id: 'case', ...changed, error: 'controlled error' }] }, [{ id: 'case', ...value }]), error => error.code !== 'FORMAL_PROPERTY' && /Observation mismatch outside/.test(error.message));
        }
    }
});
test('a later genuine removal violation is distinguished from an earlier unrelated mismatch', () => {
    const earlier = input({ provenanceMatches: false, previewCompletes: false }), later = input({ provenanceMatches: false });
    const rows = [{ id: 'earlier', ...expectedRemoval(earlier), previewCalls: 1, error: 'controlled error' }, { id: 'later', ...expectedRemoval(input()), error: '' }];
    const cases = [{ id: 'earlier', ...earlier }, { id: 'later', ...later }];
    assert.throws(() => checkRemovalResults({ schemaVersion: 1, results: rows }, cases), error => error.code === 'FORMAL_PROPERTY' && error.property === removalProperties[1] && error.witness.input.previewCompletes && error.witness.actual.successfulSaves === 1);
    assert.throws(() => checkRemovalResults({ schemaVersion: 1, results: rows.slice(0, 1) }, cases.slice(0, 1)), error => error.code === 'FORMAL_CONFORMANCE');
});
test('all source controls identify a unique production edit at an actual checked boundary', () => {
    assert.equal(removalSourceProofs.length, 5);
    for (const proof of removalSourceProofs) for (const [file, before, after] of proof.edits) { const original = readFileSync(join(root, file), 'utf8'); assert.equal(original.split(before).length, 2); assert.notEqual(before, after); }
    for (const graph of [null, {}, { schemaVersion: 1, model: 'foreign' }]) assert.throws(() => validateRemovalGraph(graph));
});

test('missing Go remains an infrastructure failure without a proof verdict', () => {
    const child = spawnSync(process.execPath, ['--input-type=module', '-e', `import {replayRemovalWitness} from ${JSON.stringify(new URL('./formal-removal-replay.mjs', import.meta.url).href)}; console.log(JSON.stringify(await replayRemovalWitness(${JSON.stringify(witness(input()))})));`], { cwd: root, env: { ...process.env, GO: '/missing-removal-go' }, encoding: 'utf8', timeout: 5000 });
    assert.ifError(child.error); assert.equal(child.status, 1); assert.match(child.stderr, /Install Go/); assert.doesNotMatch(child.stdout, /property-held|reproduced/);
});
