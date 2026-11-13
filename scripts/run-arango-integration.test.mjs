import test from 'node:test';
import assert from 'node:assert/strict';
import { checkEvents, ownDatabase, profiles, archBackend12Diagnostics, backendArchitectureConformance } from './run-arango-integration.mjs';
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

// arch-backend-12: the AQL witness is a required server-profile case that
// must execute against the live Arango service, while the PostgreSQL leg
// may only appear as the registered explicit skip in this Arango-only lane.
test('arch-backend-12 diagnostics witnesses keep their lane obligations', () => {
    const server = profiles.find(p => p.id === 'server');
    assert.ok(server.alsoRequired.includes('TestBackendNeutralDiagnosticsAgainstArango'), 'AQL diagnostics witness must be a required server-profile case');
    assert.ok(archBackend12Diagnostics.has('TestBackendNeutralDiagnosticsLivePostgreSQL'), 'PostgreSQL diagnostics leg must be registered as an explicit external skip');
    const name = t2 => ({ Test: t2, Action: 'run' });
    const pass = t2 => ({ Test: t2, Action: 'pass' });
    const events = [
        { Action: 'pass' },
        ...[server.required, ...server.alsoRequired].flatMap(t2 => [name(t2), pass(t2)]),
        { Test: 'TestBackendNeutralDiagnosticsLivePostgreSQL', Action: 'skip' },
    ];
    const result = checkEvents(stream(events), server);
    assert.ok(result.unverified.map(e => e.test).includes('TestBackendNeutralDiagnosticsLivePostgreSQL'), 'the PostgreSQL leg must surface as an explicit unverified external case');
    // Negative control: the AQL witness skipping instead of executing is
    // not a legal outcome in this lane.
    assert.throws(() => checkEvents(stream(events.filter(e => e.Test !== 'TestBackendNeutralDiagnosticsAgainstArango' || e.Action !== 'run')), server), /did not run/);
    // Negative control: an unregistered skip is refused.
    assert.throws(() => checkEvents(stream([...events, { Test: 'TestBackendNeutralDiagnosticsUnregistered', Action: 'skip' }]), server), /Unexpected skipped case/);
});
test('backend-architecture conformance: required legs run, skip and rename fail the gate', () => {
    const profile = { required: 'TestBackendArchitectureConformance', alsoRequired: [
        'TestBackendArchitectureConformance/arangodb',
        'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalars',
    ] };
    const good = [
        { Action: 'pass' },
        { Test: 'TestBackendArchitectureConformance', Action: 'run' }, { Test: 'TestBackendArchitectureConformance', Action: 'pass' },
        { Test: 'TestBackendArchitectureConformance/arangodb', Action: 'run' }, { Test: 'TestBackendArchitectureConformance/arangodb', Action: 'pass' },
        { Test: 'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalars', Action: 'run' }, { Test: 'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalars', Action: 'pass' },
        { Test: 'TestBackendArchitectureConformance/postgresql', Action: 'run' }, { Test: 'TestBackendArchitectureConformance/postgresql', Action: 'skip' },
    ];
    const result = checkEvents(stream(good), profile);
    assert.ok(result.unverified.some(skip => skip.test === 'TestBackendArchitectureConformance/postgresql'), 'absent PG arm must stay visible as unverified');
    // A required leg that skips instead of running never passes.
    assert.throws(() => checkEvents(stream(good.filter(e => !(e.Test === 'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalars' && e.Action === 'run'))), profile), /did not run/);
    assert.throws(() => checkEvents(stream(good.map(e => e.Test === 'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalars' && e.Action === 'run' ? { ...e, Action: 'skip' } : e)), profile), /did not run/);
    // A renamed cell identity (no run event under the frozen name) fails.
    const renamed = good.map(e => e.Test === 'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalars' ? { ...e, Test: 'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalarsRenamed' } : e);
    assert.throws(() => checkEvents(stream(renamed), profile), /did not run/);
    // An unregistered conformance skip fails; only allowlisted identities skip.
    assert.throws(() => checkEvents(stream(good.concat([{ Test: 'TestBackendArchitectureConformance/postgresl', Action: 'skip' }])), profile), /Unexpected skipped case/);
    // Membership of both arm identities is pinned by the exported set.
    assert.ok(backendArchitectureConformance.has('TestBackendArchitectureConformance/postgresql'));
    assert.ok(backendArchitectureConformance.has('TestBackendArchitectureConformance/arangodb'));
});
test('backend-architecture conformance skip-set capture is pinned and self-consistent', async () => {
    const { createHash } = await import('node:crypto');
    const { readFileSync } = await import('node:fs');
    const { join } = await import('node:path');
    const capturePath = join(import.meta.dirname, 'backend-conformance-skipset-capture.json');
    const raw = readFileSync(capturePath);
    const verifier = readFileSync(join(import.meta.dirname, 'backend-conformance-skipset-verify.mjs'), 'utf8');
    const pinned = verifier.match(/CAPTURE_SHA256 = '([0-9a-f]{64})'/)?.[1];
    assert.ok(pinned, 'verifier does not pin the capture sha256');
    assert.equal(createHash('sha256').update(raw).digest('hex'), pinned, 'skip-set capture drifted from its pinned sha256');
    const capture = JSON.parse(raw);
    assert.deepEqual([...capture.skips].sort(), [
        'TestBackendArchitectureConformance/arangodb',
        'TestBackendArchitectureConformance/postgresql',
    ]);
});
