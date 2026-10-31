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
    const result = checkEvents(stream([{ Action: 'pass' }, { Test: 'TestReal', Action: 'run' }, { Test: 'TestReal', Action: 'pass' }, { Test: 'TestPivotPresenceAudit', Action: 'skip' }]), { required: 'TestReal' });
    assert.deepEqual(result.unverified.map(e => e.test), ['TestPivotPresenceAudit']);
});
test('only a database successfully created by this run becomes cleanup-owned', async () => {
    const owned = [];
    await assert.rejects(ownDatabase('http://example.invalid', 'existing', owned, async () => ({ ok: false, status: 409 })));
    assert.deepEqual(owned, []);
    await ownDatabase('http://example.invalid', 'fresh', owned, async () => ({ ok: true, status: 201 }));
    assert.deepEqual(owned, ['fresh']);
});
