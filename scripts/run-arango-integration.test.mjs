import test from 'node:test';
import assert from 'node:assert/strict';
import { checkEvents, ownDatabase } from './run-arango-integration.mjs';
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
        { Test: 'TestCoreAuthoringProtocolModelReplay', Action: 'skip' },
        { Test: 'TestCoreAuthoringProtocolConcretePrefixReplay', Action: 'skip' },
        { Test: 'TestReconcileDanglesActiveReleaseAgainstArangoClickHouse', Action: 'skip' },
    ]), { required: 'TestReal' });
    assert.deepEqual(result.unverified.map(e => e.test), [
        'TestPivotPresenceAudit',
        'TestCoreAuthoringProtocolModelReplay',
        'TestCoreAuthoringProtocolConcretePrefixReplay',
        'TestReconcileDanglesActiveReleaseAgainstArangoClickHouse',
    ]);
    assert.throws(() => checkEvents(stream([
        { Action: 'pass' },
        { Test: 'TestReal', Action: 'run' }, { Test: 'TestReal', Action: 'pass' },
        { Test: 'TestUnexpected', Action: 'skip' },
    ]), { required: 'TestReal' }), /Unexpected skipped case/);
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
