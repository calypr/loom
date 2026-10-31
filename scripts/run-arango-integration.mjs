#!/usr/bin/env node
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { apiDoctor, apiSourceFingerprint } from './lib/formal-api.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const sourceFingerprint = () => createHash('sha256').update(apiSourceFingerprint()).update(readFileSync(fileURLToPath(import.meta.url))).digest('hex');
const external = new Set(['TestPivotPresenceAudit', 'TestSemanticGroupProjectionFactorizationSelectsExistingIndexOnFullCDAReadOnly', 'TestPopulationPlanProfilesAgainstArango', 'TestProfileCorpusAgainstArango', 'TestCompoundEdgeIndexExplainAgainstArango', 'TestReceiptPreviewExplainAgainstArango']);
export const profiles = [
    ['catalog', './internal/catalog/arango', 'TestResourceInventoryPersistsAndScopesCountsAcrossStoreReopen'],
    ['compiler', './internal/dataframe/compiler', 'TestPivotPresenceArangoAudit'],
    ['execution', './internal/dataframe/execution', 'TestCohortPreviewWithSmallerRootPageKeepsAllMemberValuesAgainstArango'],
    ['explorer', './internal/explorer/arango', 'TestSelectionStorageAgainstArango'],
    ['store', './internal/store/arango', 'TestReceiptArangoStoreUsesImmutableTenantScopedQueries'],
    ['server', './internal/server', 'TestSavedRelatedRouteRowsAgainstArango'],
    ['artifact', './internal/dataframe/execution/chartifact/arango', 'TestCatalogLeaseArbitrationAgainstArango'],
    ['publication', './internal/dataframe/publication/arango', 'TestExecutionReadPinArbitrationAgainstArango'],
    ['backfill', './internal/ingest', 'TestRetainedSemanticInventoryBackfillArangoIntegration'],
].map(([id, pkg, required]) => ({ id, package: pkg, required, ...(id === 'backfill' ? { run: required } : {}) }));
// The store package's retained cases require external seeded data. Its ordinary
// unit cases still run; compiler, execution and server supply real row witnesses.
profiles.find(p => p.id === 'store').required = null;
export function checkEvents(stdout, profile) {
    const events = stdout.trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
    assert.ok(events.some(e => !e.Test && e.Action === 'pass'), 'No successful package completion');
    assert.ok(!events.some(e => e.Action === 'fail'), 'Go reported failure');
    const passes = events.filter(e => e.Test && e.Action === 'pass').map(e => e.Test);
    assert.ok(passes.length, 'No test assertions completed');
    if (profile.required) {
        assert.ok(events.some(e => e.Test === profile.required && e.Action === 'run'), `Required real integration case did not run: ${profile.required}`);
        assert.ok(passes.includes(profile.required), `Required real integration case did not pass: ${profile.required}`);
    }
    const skipped = events.filter(e => e.Test && e.Action === 'skip').map(e => ({ test: e.Test, reason: events.filter(o => o.Test === e.Test && o.Action === 'output').map(o => o.Output).join('').trim() }));
    for (const skip of skipped) assert.ok(external.has(skip.test), `Unexpected skipped case: ${skip.test}`);
    return { passes, unverified: skipped };
}
export async function ownDatabase(endpoint, name, owned, request = fetch) {
    const response = await request(`${endpoint}/_api/database`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ name }), signal: AbortSignal.timeout(10000) });
    assert.ok(response.ok, `Refusing existing/unavailable database ${name}: HTTP ${response.status}`);
    owned.push(name);
}
export async function runArango(output) {
    const endpoint = new URL(process.env.LOOM_TEST_ARANGO_URL || '');
    assert.ok(['http:', 'https:'].includes(endpoint.protocol) && !endpoint.username && !endpoint.password && endpoint.pathname === '/', 'Use an explicit unauthenticated owned Arango origin');
    const url = endpoint.origin, tool = apiDoctor(), directory = resolve(output);
    assert.ok(!existsSync(directory) || readdirSync(directory).length === 0, 'Evidence directory must be empty');
    mkdirSync(directory, { recursive: true });
    const before = sourceFingerprint(), owned = [], deadline = performance.now() + 8 * 60 * 1000;
    const evidence = { tool, sourceBefore: before, profiles: [], cleanup: [], status: 'running' };
    const database = `loom_test_restricted_starting_collection_${randomUUID()}`;
    const c01 = `loom_backend_c01_${randomUUID().replaceAll('-', '')}`;
    const sc02 = 'loom_qa_semantic_cov_resource_20261016';
    const env = { ...process.env, GOTOOLCHAIN: 'local', GOMAXPROCS: '2' };
    for (const key of Object.keys(env)) if (/^LOOM_.*(ARANGO|PROFILE|FULL_CDA|PIVOT_PRESENCE)/.test(key)) delete env[key];
    Object.assign(env, { LOOM_TEST_ARANGO_URL: url, LOOM_TEST_ARANGO_DATABASE: database, LOOM_C01_ARANGO_INTEGRATION: '1', LOOM_C01_ARANGO_URL: url, LOOM_C01_ARANGO_DATABASE: c01, LOOM_SC02_COVERAGE_ARANGO_INTEGRATION: '1', LOOM_SC02_COVERAGE_ARANGO_URL: url, LOOM_SC02_COVERAGE_ARANGO_DATABASE: sc02 });
    if (tool.executable === '/workspace/.loom-setup/go/bin/go') {
        if (!env.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) env.GOMODCACHE = '/workspace/.loom-setup/gomod';
        if (!env.GOCACHE && existsSync('/workspace/loom/.gocache')) env.GOCACHE = '/workspace/loom/.gocache';
    }
    const child = (profile, setup) => {
        const args = ['test', '-json', '-p', '1', '-timeout=30s', '-count=1'];
        if (setup || profile.run) args.push('-run', setup ? '^$' : `^${profile.run}$`);
        args.push(profile.package);
        const start = performance.now();
        const timeout = Math.min(setup ? 60000 : 30000, Math.floor(deadline - start));
        assert.ok(timeout > 0, 'Eight-minute driver budget exhausted; reserve remaining CI time for cleanup');
        const result = spawnSync(tool.executable, args, { cwd: root, env, encoding: 'utf8', timeout, maxBuffer: 64 * 1024 * 1024, detached: true });
        if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
        const id = `${profile.id}-${setup ? 'setup' : 'test'}`;
        writeFileSync(resolve(directory, `${id}.jsonl`), result.stdout || '');
        writeFileSync(resolve(directory, `${id}.stderr`), result.stderr || '');
        const record = { id, phase: setup ? 'compile-setup-no-proof' : 'verification', command: [tool.executable, ...args], milliseconds: performance.now() - start, exit: result.status, error: result.error?.message };
        evidence.profiles.push(record);
        assert.equal(result.status, 0, `${id} failed: ${result.error?.message || result.stderr}`);
        if (!setup) Object.assign(record, checkEvents(result.stdout, profile));
    };
    try {
        for (const name of [database, c01, sc02]) await ownDatabase(url, name, owned);
        // Compilation is reported separately and never treated as test evidence.
        for (const profile of profiles) child(profile, true);
        for (const profile of profiles) child(profile, false);
        evidence.status = evidence.profiles.some(p => p.unverified?.length) ? 'passed-with-explicit-unverified-external-cases' : 'passed';
    } catch (error) { evidence.status = 'failed'; evidence.error = error.message; }
    finally {
        for (const name of owned.reverse()) {
            try {
                const response = await fetch(`${url}/_api/database/${encodeURIComponent(name)}`, { method: 'DELETE', signal: AbortSignal.timeout(10000) });
                evidence.cleanup.push({ database: name, status: response.status });
                assert.ok(response.ok || response.status === 404, `Database cleanup HTTP ${response.status}`);
            } catch (error) { evidence.status = 'failed'; evidence.cleanup.push({ database: name, error: error.message }); }
        }
        evidence.sourceAfter = sourceFingerprint();
        if (before !== evidence.sourceAfter) { evidence.status = 'failed'; evidence.error = 'Source changed during verification'; }
        writeFileSync(resolve(directory, 'summary.json'), JSON.stringify(evidence, null, 2) + '\n');
    }
    console.log(JSON.stringify(evidence, null, 2));
    return evidence;
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
    const args = process.argv.slice(2);
    assert.ok(args.length === 2 && args[0] === '--out', 'Usage: node scripts/run-arango-integration.mjs --out EMPTY_DIRECTORY');
    const evidence = await runArango(args[1]);
    if (evidence.status === 'failed') process.exitCode = 1;
}
