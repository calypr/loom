import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { checkEvents, ownDatabase, profiles, runBounded, sourceFingerprint } from '../../scripts/run-arango-integration.mjs';
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
test('known product regressions cannot be skipped even with an issue ID', () => {
    for (const reason of ['ARC-C13: known failure', 'skipped for another reason']) {
        assert.throws(() => checkEvents(stream([
            { Action: 'pass' },
            { Test: 'TestReal', Action: 'run' }, { Test: 'TestReal', Action: 'pass' },
            { Test: 'TestStalePinBlocksEntireReconcileAgainstArango', Action: 'skip' },
            { Test: 'TestStalePinBlocksEntireReconcileAgainstArango', Action: 'output', Output: reason },
        ]), { required: 'TestReal' }), /Unexpected skipped case/);
    }
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

test('preview index migration runs against the owned Arango database', () => {
    const profile = profiles.find(candidate => candidate.id === 'preview-index-migration');
    const generalStoreProfile = profiles.find(candidate => candidate.id === 'store');
    assert.equal(profile?.package, './internal/store/arango');
    const migrationTests = [
        'TestPreviewSupersededIndexMigrationAgainstArango',
        'TestPreviewSupersededPublicMigrationVersionedAgainstArango',
        'TestOpenExistingMissingDatabaseDoesNotCreateAgainstArango',
    ];
    assert.deepEqual([profile?.required, ...profile.alsoRequired], migrationTests);
    const migrationRun = new RegExp(`^${profile.run}$`);
    const generalSkip = new RegExp(generalStoreProfile.skip);
    for (const name of migrationTests) {
        assert.ok(migrationRun.test(name));
        assert.ok(generalSkip.test(name));
    }
    assert.ok(!migrationRun.test('TestReceiptArangoStoreUsesImmutableTenantScopedQueries'));
    assert.ok(!generalSkip.test('TestReceiptArangoStoreUsesImmutableTenantScopedQueries'));
});

test('source fingerprint covers dependency manifests and embedded Go test fixtures', () => {
    const root = mkdtempSync(join(tmpdir(), 'loom-arango-fingerprint-'));
    const fixture = join(root, 'internal/api/explorer/testdata/patient_one_pre_one_saved_document.json');
    try {
        for (const directory of ['api', 'fhir', 'tests/fixtures', 'tests/scripts', 'scripts']) mkdirSync(join(root, directory), { recursive: true });
        mkdirSync(join(root, 'internal/api/explorer/testdata'), { recursive: true });
        writeFileSync(join(root, 'go.mod'), 'module loom\n');
        writeFileSync(join(root, 'go.sum'), '');
        writeFileSync(join(root, 'scripts/run-arango-integration.mjs'), 'runner\n');
        writeFileSync(join(root, 'tests/scripts/run-arango-integration.test.mjs'), 'runner test\n');
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

const resistantProcessTree = `
    const { spawn } = require('node:child_process');
    process.on('SIGTERM', () => {});
    const descendant = spawn(process.execPath, ['-e', "process.on('SIGTERM', () => {}); setInterval(() => {}, 1000)"], { stdio: 'inherit' });
    console.log(JSON.stringify({ child: process.pid, descendant: descendant.pid }));
    setInterval(() => {}, 1000);
`;

test('bounded child kills a TERM-resistant process group and closes inherited descendant pipes', async () => {
    const started = Date.now();
    const result = await runBounded(process.execPath, ['-e', resistantProcessTree], { timeout: 500 });
    assert.equal(result.error?.code, 'ETIMEDOUT');
    assert.equal(result.status, null);
    assert.match(result.signal, /SIGKILL/);
    assert.ok(Date.now() - started < 2000, 'the bounded runner must return after killing the child group');
    const tree = JSON.parse(result.stdout.trim().split('\n')[0]);
    assert.ok(Number.isInteger(tree.child) && Number.isInteger(tree.descendant));
    assert.notEqual(tree.child, tree.descendant);
});

test('legacy synchronous timeout control is externally bounded and leaves no child group', { timeout: 6000 }, async () => {
    const wrapper = `
        const { spawnSync } = require('node:child_process');
        const result = spawnSync(process.execPath, ['-e', ${JSON.stringify(resistantProcessTree)}], {
            timeout: 500, detached: true, stdio: ['ignore', 'inherit', 'inherit'],
        });
        console.log('legacy-returned', result.error?.code || 'none');
    `;
    const worker = spawn(process.execPath, ['-e', wrapper], { detached: true, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '', stderr = '', childPID;
    worker.stdout.setEncoding('utf8');
    worker.stderr.setEncoding('utf8');
    worker.stdout.on('data', chunk => {
        stdout += chunk;
        const match = stdout.match(/"child":(\d+)/);
        if (match) childPID ??= Number(match[1]);
    });
    worker.stderr.on('data', chunk => { stderr += chunk; });
    let wasBlockedAtRescue = false;
    const rescue = setTimeout(() => {
        wasBlockedAtRescue = worker.exitCode === null;
        if (childPID) {
            try { process.kill(-childPID, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
        } else {
            try { process.kill(-worker.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
        }
    }, 1200);
    const guard = setTimeout(() => {
        if (worker.exitCode === null) {
            if (childPID) { try { process.kill(-childPID, 'SIGKILL'); } catch {} }
            try { process.kill(-worker.pid, 'SIGKILL'); } catch {}
        }
    }, 2500);
    const closed = new Promise((resolvePromise, reject) => {
        worker.once('error', reject);
        worker.once('close', (code, signal) => resolvePromise({ code, signal }));
    });
    const result = await closed;
    clearTimeout(rescue);
    clearTimeout(guard);
    assert.equal(wasBlockedAtRescue, true, `legacy helper unexpectedly returned early; stdout=${stdout} stderr=${stderr}`);
    assert.match(stdout, /legacy-returned (?:ETIMEDOUT|none)/);
    assert.equal(result.code, 0);
});
