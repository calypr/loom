import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { root } from './formal-tools.mjs';
import { attachmentCases, attachmentProperties, attachmentFile, validateAttachmentInput, expectedAttachment, checkAttachmentResults, runAttachment, attachmentSourceProofs, replayAttachmentWitness } from './formal-population-attachment.mjs';
const input = () => ({ edge: 'primary', snapshotMatches: true, outputMatches: true, selectionMatches: true, selection: 'current', route: 'authorized', batch: 'single' });
const observed = (id, value) => ({ id, ...expectedAttachment(value), error: expectedAttachment(value).accepted ? '' : value.selection === 'revokedScope' ? 'STALE_POPULATION_SELECTION: controlled revoked scope' : value.batch === 'invalidSecond' ? 'outputId does not identify a valid row-rooted table' : 'controlled rejection' });
const record = (id, value) => ({ schemaVersion: 1, results: [observed(id, value)] });
function witness(value = input(), property = attachmentProperties[0]) { return { schemaVersion: 1, kind: 'population-attachment', property, input: value, expected: expectedAttachment(value) }; }
test('finite generated domain includes independent identity, header, route and atomic-batch choices', () => {
    const cases = attachmentCases(); assert.equal(cases.length, 482); assert.equal(new Set(cases.map(row => row.id)).size, 482);
    const semantic = cases.map(({ id, ...value }) => JSON.stringify(value));
    assert.equal(new Set(semantic).size, 482, 'Generated identities must cover distinct inputs');
    assert.equal(cases.filter(row => row.edge !== 'zero' && row.selection !== 'revokedScope').length, 2 * 8 * 5 * 3 * 2, 'Incomplete Cartesian attachment domain');
    for (const { id, ...value } of cases) { assert.ok(id); validateAttachmentInput(value); }
    assert.equal(cases.filter(({ id, ...value }) => expectedAttachment(value).accepted).length, 3);
    assert.ok(cases.some(row => row.edge === 'zero')); assert.ok(cases.some(row => row.selection === 'revokedScope'));
    const data = { schemaVersion: 1, results: cases.map(({ id, ...value }) => observed(id, value)) };
    assert.deepEqual(checkAttachmentResults(data, cases), { cases: 482, accepted: 3, rejected: 479 });
    for (const invalid of [null, {}, { ...input(), extra: true }, { ...input(), edge: 'foreign' }, { ...input(), snapshotMatches: 'true' }, { ...input(), edge: 'zero', route: 'blockedEdge' }]) assert.throws(() => validateAttachmentInput(invalid));
});
test('actual result inventories reject missing, duplicate, foreign and malformed observations', () => {
    const cases = [{ id: 'one', ...input() }]; assert.equal(checkAttachmentResults(record('one', input()), cases).accepted, 1);
    for (const mutate of [r => r.results.pop(), r => r.results.push(r.results[0]), r => { r.results[0].id = 'foreign'; }, r => { r.results[0].accepted = 'true'; }, r => { r.results[0].versionAfter = '2'; }, r => { r.results[0].writes = -1; }, r => { r.results[0].error = 'contradiction'; }, r => { r.results[0].route[0].unknown = 'value'; }, r => { r.results[0].route[0].storageDirection = 42; }, r => { r.extra = true; }]) { const bad = record('one', input()); mutate(bad); assert.throws(() => checkAttachmentResults(bad, cases)); }
});
test('exact literal route, direction and valid acceptance have named semantic failures', () => {
    const value = input(), cases = [{ id: 'one', ...value }];
    for (const mutate of [r => { r.results[0].route[0].catalogEdgeId = 'foreign'; }, r => { delete r.results[0].route[0].storageDirection; }, r => { r.results[0].exactWorkspace = false; }]) { const bad = record('one', value); mutate(bad); assert.throws(() => checkAttachmentResults(bad, cases), error => error.code === 'FORMAL_PROPERTY' && error.property === attachmentProperties[1]); }
    const rejected = record('one', value); Object.assign(rejected.results[0], { accepted: false, error: 'controlled valid rejection', writes: 0 });
    assert.throws(() => checkAttachmentResults(rejected, cases), error => error.code === 'FORMAL_PROPERTY' && error.property === attachmentProperties[0]);
    const unrelated = record('one', value); unrelated.results[0].searchChoices = 99;
    assert.throws(() => checkAttachmentResults(unrelated, cases), error => error.code === 'FORMAL_CONFORMANCE');
});
test('invalid batches preserve the whole head and eligible prefixes must reach late preparation', () => {
    const value = { ...input(), batch: 'invalidSecond' }, cases = [{ id: 'one', ...value }];
    assert.equal(checkAttachmentResults(record('one', value), cases).rejected, 1);
    const early = record('one', value); early.results[0].error = 'early binding failure'; assert.throws(() => checkAttachmentResults(early, cases), /late preparation/);
    const revoked = { ...input(), selection: 'revokedScope' }, wrongStage = record('one', revoked); wrongStage.results[0].error = 'STALE_AUTHORIZATION_SCOPE'; assert.throws(() => checkAttachmentResults(wrongStage, [{ id: 'one', ...revoked }]), /selection scope reauthorization/);
    const saved = record('one', value); Object.assign(saved.results[0], { accepted: true, error: '', writes: 1, preservedHead: false });
    assert.throws(() => checkAttachmentResults(saved, cases), error => error.code === 'FORMAL_PROPERTY' && error.property === attachmentProperties[2]);
});
test('invalid overrides and fabricated witness scopes block before Go runs', () => {
    for (const productionSource of ['', ' ', null, false, {}, { foreign: 'package x' }, { [attachmentFile]: '' }, { [attachmentFile]: '  ' }]) assert.throws(() => runAttachment({ productionSource }));
    assert.throws(() => runAttachment({ cases: [{ id: 'duplicate', ...input() }, { id: 'duplicate', ...input() }] }), /Duplicate attachment inputs/);
    assert.throws(() => replayAttachmentWitness({ ...witness(), expected: {} }), /Changed attachment/);
    assert.throws(() => replayAttachmentWitness(witness(input(), attachmentProperties[2])), /does not apply/);
    assert.throws(() => replayAttachmentWitness(witness({ ...input(), selection: 'incomplete' }, attachmentProperties[0])), /does not apply/);
});
test('source controls target exact current production boundaries', () => {
    assert.equal(attachmentSourceProofs.length, 4);
    for (const proof of attachmentSourceProofs) for (const [file, before, after] of proof.edits) { assert.ok([attachmentFile, 'internal/explorer/lifecycle/authoring.go'].includes(file)); assert.equal(readFileSync(join(root, file), 'utf8').split(before).length, 2); assert.notEqual(before, after); }
    assert.equal(attachmentSourceProofs.find(proof => proof.property === attachmentProperties[2]).edits.length, 3, 'Stale choice control removes the three overlapping identity defenses');
});
test('missing Go remains an infrastructure failure with no property verdict', () => {
    const child = spawnSync(process.execPath, ['--input-type=module', '-e', `import {runAttachment} from ${JSON.stringify(new URL('./formal-population-attachment.mjs', import.meta.url).href)};console.log(JSON.stringify(runAttachment()));`], { cwd: root, env: { ...process.env, GO: '/missing-attachment-go' }, encoding: 'utf8', timeout: 5000 });
    assert.ifError(child.error); assert.equal(child.status, 1); assert.match(child.stderr, /Install Go/); assert.doesNotMatch(child.stdout, /property-held|reproduced|bounded-public-code/);
});


test('every named attachment property retains an actual source fault with exact anchors', () => {
    assert.deepEqual([...new Set(attachmentSourceProofs.map(proof => proof.property))].sort(), [...attachmentProperties].sort());
    for (const proof of attachmentSourceProofs) {
        const changed = new Map();
        for (const [file, before, after] of proof.edits) {
            const current = changed.get(file) ?? readFileSync(join(root, file), 'utf8');
            assert.equal(current.split(before).length, 2); assert.notEqual(before, after); changed.set(file, current.replace(before, after));
        }
    }
});
