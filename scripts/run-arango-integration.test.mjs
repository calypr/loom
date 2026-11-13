import test from 'node:test';
import assert from 'node:assert/strict';
import { checkEvents, ownDatabase, profiles, archBackend12Diagnostics, backendArchitectureConformance, cleanupOwnedDatabases } from './run-arango-integration.mjs';
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
    assert.throws(() => checkEvents(stream(events.filter(e => e.Test !== 'TestBackendNeutralDiagnosticsAgainstArango' || e.Action !== 'run')), server), /did not run/);
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
    assert.throws(() => checkEvents(stream(good.filter(e => !(e.Test === 'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalars' && e.Action === 'run'))), profile), /did not run/);
    assert.throws(() => checkEvents(stream(good.map(e => e.Test === 'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalars' && e.Action === 'run' ? { ...e, Action: 'skip' } : e)), profile), /did not run/);
    const renamed = good.map(e => e.Test === 'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalars' ? { ...e, Test: 'TestBackendArchitectureConformance/arangodb/scalarQueryServesLiteralScalarsRenamed' } : e);
    assert.throws(() => checkEvents(stream(renamed), profile), /did not run/);
    assert.throws(() => checkEvents(stream(good.concat([{ Test: 'TestBackendArchitectureConformance/postgresl', Action: 'skip' }])), profile), /Unexpected skipped case/);
    assert.ok(backendArchitectureConformance.has('TestBackendArchitectureConformance/postgresql'));
    assert.ok(backendArchitectureConformance.has('TestBackendArchitectureConformance/arangodb'));
});
test('cleanup transport failure after verification warns without reddening the run', async () => {
    const evidence = { status: 'passed', cleanup: [], cleanupAudit: [] };
    const warnings = [];
    const realWarn = console.warn;
    console.warn = (...args) => warnings.push(args.join(' '));
    try {
        await cleanupOwnedDatabases('http://arangodb:8529', ['a', 'b'], evidence,
            async () => { throw new Error('connect ECONNREFUSED 127.0.0.1:8529'); });
    } finally {
        console.warn = realWarn;
    }
    assert.equal(evidence.status, 'passed', 'transient transport cleanup failure must not fail a passed run');
    assert.deepEqual(evidence.cleanup, [], 'transport failures are audit records, not cleanup verdicts');
    assert.equal(evidence.cleanupAudit.length, 2);
    assert.equal(evidence.cleanupAudit[0].database, 'a');
    assert.match(evidence.cleanupAudit[0].transportError, /ECONNREFUSED/);
    assert.equal(warnings.length, 2, 'each transport failure must be warned once');
    assert.match(warnings[0], /cleanup-audit/);
});
test('cleanup HTTP refusal outside 404 still fails the run', async () => {
    const evidence = { status: 'passed', cleanup: [], cleanupAudit: [] };
    const requests = [];
    await cleanupOwnedDatabases('http://arangodb:8529', ['locked'], evidence,
        async (url, init) => { requests.push([url, init.method]); return { ok: false, status: 403 }; });
    assert.equal(evidence.status, 'failed', 'an explicit cleanup refusal is a hard contract failure');
    assert.deepEqual(requests, [['http://arangodb:8529/_api/database/locked', 'DELETE']]);
    assert.deepEqual(evidence.cleanup, [
        { database: 'locked', status: 403 },
        { database: 'locked', error: 'Database cleanup HTTP 403' },
    ]);
    assert.deepEqual(evidence.cleanupAudit, []);
});
test('cleanup ok and 404 responses pass and are recorded', async () => {
    const evidence = { status: 'passed', cleanup: [], cleanupAudit: [] };
    let next = 0;
    const statuses = [200, 404];
    await cleanupOwnedDatabases('http://arangodb:8529', ['gone', 'absent'], evidence,
        async () => ({ ok: next < 1, status: statuses[next++] ?? 404 }));
    assert.equal(evidence.status, 'passed');
    assert.deepEqual(evidence.cleanup, [
        { database: 'gone', status: 200 },
        { database: 'absent', status: 404 },
    ]);
    assert.deepEqual(evidence.cleanupAudit, []);
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
        'TestBackendArchitectureConformance/crossBackendCandidateIdentityParity',
        'TestBackendArchitectureConformance/postgresql',
    ]);
});
