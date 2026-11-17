import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { checkEvents, ownDatabase, sourceFingerprint } from './run-arango-integration.mjs';
const stream = events => events.map(e => JSON.stringify(e)).join('\n');
test('real case must pass; empty package and skip cannot masquerade as proof', () => {
    const profile = { required: 'TestReal' };
    assert.throws(() => checkEvents(stream([{ Action: 'pass' }]), profile));
    assert.throws(() => checkEvents(stream([{ Action: 'pass' }, { Test: 'TestOther', Action: 'pass' }]), profile));
    assert.throws(() => checkEvents(stream([{ Action: 'pass' }, { Test: 'TestReal', Action: 'run' }, { Test: 'TestReal', Action: 'pass' }, { Test: 'TestUnexpected', Action: 'skip' }]), profile));
    assert.equal(checkEvents(stream([{ Action: 'pass' }, { Test: 'TestReal', Action: 'run' }, { Test: 'TestReal', Action: 'pass' }]), profile).passes.length, 1);
});
test('external preconditions remain explicit unverified cases', () => {
    const result = checkEvents(stream([
        { Action: 'pass' },
        { Test: 'TestReal', Action: 'run' }, { Test: 'TestReal', Action: 'pass' },
        { Test: 'TestPivotPresenceAudit', Action: 'skip' },
        { Test: 'TestReconcileDanglesActiveReleaseAgainstArangoClickHouse', Action: 'skip' },
    ]), { required: 'TestReal' });
    assert.deepEqual(result.unverified.map(e => e.test), [
        'TestPivotPresenceAudit',
        'TestReconcileDanglesActiveReleaseAgainstArangoClickHouse',
    ]);
    assert.throws(() => checkEvents(stream([
        { Action: 'pass' },
        { Test: 'TestReal', Action: 'run' }, { Test: 'TestReal', Action: 'pass' },
        { Test: 'TestUnexpected', Action: 'skip' },
    ]), { required: 'TestReal' }), /Unexpected skipped case/);
});
test('known product failures remain explicit incomplete evidence, never test passes', () => {
    const events = [
        { Action: 'pass' },
        { Test: 'TestReal', Action: 'run' }, { Test: 'TestReal', Action: 'pass' },
        { Test: 'TestStalePinBlocksEntireReconcileAgainstArango', Action: 'skip' },
        { Test: 'TestStalePinBlocksEntireReconcileAgainstArango', Action: 'output', Output: '    ARC-C13: known failure; opt in to run assertion\n' },
    ];
    const result = checkEvents(stream(events), { required: 'TestReal' });
    assert.deepEqual(result.unverified, [{
        test: 'TestStalePinBlocksEntireReconcileAgainstArango',
        reason: 'ARC-C13: known failure; opt in to run assertion',
        issue: 'ARC-C13',
    }]);
    assert.throws(() => checkEvents(stream([
        { Action: 'pass' },
        { Test: 'TestReal', Action: 'run' }, { Test: 'TestReal', Action: 'pass' },
        { Test: 'TestStalePinBlocksEntireReconcileAgainstArango', Action: 'skip' },
        { Test: 'TestStalePinBlocksEntireReconcileAgainstArango', Action: 'output', Output: 'skipped for another reason\n' },
    ]), { required: 'TestReal' }), /must name ARC-C13/);
});
test('only a database successfully created by this run becomes cleanup-owned', async () => {
    const owned = [];
    await assert.rejects(ownDatabase('http://example.invalid', 'existing', owned, async () => ({ ok: false, status: 409 })));
    assert.deepEqual(owned, []);
    await ownDatabase('http://example.invalid', 'fresh', owned, async () => ({ ok: true, status: 201 }));
    assert.deepEqual(owned, ['fresh']);
});

test('every additional real database witness must execute and pass', () => {
    const profile = { required: 'TestReal', alsoRequired: ['TestCAS', 'TestRollback'] };
    const events = [{ Action: 'pass' }, ...['TestReal', 'TestCAS', 'TestRollback'].flatMap(Test => [{ Test, Action: 'run' }, { Test, Action: 'pass' }])];
    assert.equal(checkEvents(stream(events), profile).passes.length, 3);
    for (const name of profile.alsoRequired) {
        assert.throws(() => checkEvents(stream(events.filter(e => e.Test !== name)), profile), /did not run/);
        assert.throws(() => checkEvents(stream(events.filter(e => e.Test !== name || e.Action !== 'pass')), profile), /did not pass/);
    }
});

test('source fingerprint covers dependency manifests and embedded Go test fixtures', () => {
    const root = mkdtempSync(join(tmpdir(), 'loom-arango-fingerprint-'));
    const fixture = join(root, 'internal/server/testdata/patient_one_pre_one_saved_document.json');
    try {
        for (const directory of ['generated', 'schemas', 'testdata', 'scripts']) mkdirSync(join(root, directory), { recursive: true });
        mkdirSync(join(root, 'internal/server/testdata'), { recursive: true });
        writeFileSync(join(root, 'go.mod'), 'module loom\n');
        writeFileSync(join(root, 'go.sum'), '');
        writeFileSync(join(root, 'scripts/run-arango-integration.mjs'), 'runner\n');
        writeFileSync(join(root, 'scripts/run-arango-integration.test.mjs'), 'runner test\n');
        writeFileSync(fixture, '{"version":1}\n');
        const withFixture = sourceFingerprint(root);
        writeFileSync(fixture, '{"version":2}\n');
        const changedFixture = sourceFingerprint(root);
        assert.notEqual(changedFixture, withFixture, 'embedded test fixture changes must invalidate evidence');
        writeFileSync(join(root, 'go.mod'), 'module loom\n\ngo 1.26\n');
        const changedGoMod = sourceFingerprint(root);
        assert.notEqual(changedGoMod, changedFixture, 'go.mod changes must invalidate evidence');
        writeFileSync(join(root, 'go.sum'), 'example.com/dependency v1.2.3 h1:changed\n');
        assert.notEqual(sourceFingerprint(root), changedGoMod, 'go.sum changes must invalidate evidence');
    } finally {
        rmSync(root, { recursive: true, force: true });
    }
});
