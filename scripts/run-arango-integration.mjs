#!/usr/bin/env node
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { relative, resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const overallBudgetMs = 8 * 60 * 1000;
const maxChildBudgetMs = 90 * 1000;
const goExecutable = process.env.GO_BINARY || 'go';

function sourceFiles(directory, files = []) {
    for (const entry of readdirSync(directory, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
        if (entry.name === '.git' || entry.name === 'vendor' || entry.name === 'node_modules') continue;
        const path = resolve(directory, entry.name);
        if (entry.isDirectory()) sourceFiles(path, files);
        else if (entry.isFile()) files.push(path);
    }
    return files;
}

export function sourceFingerprint(sourceRoot = root) {
    const hash = createHash('sha256');
    const sources = [
        ...['internal', 'generated', 'schemas', 'testdata'].flatMap(directory => sourceFiles(resolve(sourceRoot, directory))),
        resolve(sourceRoot, 'go.mod'),
        resolve(sourceRoot, 'go.sum'),
        resolve(sourceRoot, 'scripts/run-arango-integration.mjs'),
        resolve(sourceRoot, 'scripts/run-arango-integration.test.mjs'),
    ].sort();
    for (const path of sources) {
        hash.update(relative(sourceRoot, path));
        hash.update('\0');
        hash.update(readFileSync(path));
        hash.update('\0');
    }
    return hash.digest('hex');
}

const dualStore = new Set([
    // Runs as an explicit API proof in go-publication-rows, which owns both stores.
    'TestReconcileDanglesActiveReleaseAgainstArangoClickHouse',
]);
const external = new Set([
    'TestPivotPresenceAudit',
    'TestSemanticGroupProjectionFactorizationSelectsExistingIndexOnFullCDAReadOnly',
    'TestPopulationPlanProfilesAgainstArango',
    'TestProfileCorpusAgainstArango',
    'TestCompoundEdgeIndexExplainAgainstArango',
    'TestReceiptPreviewExplainAgainstArango',
    ...dualStore,
]);

export const profiles = [
    ['catalog', './internal/catalog/arango', 'TestResourceInventoryPersistsAndScopesCountsAcrossStoreReopen'],
    ['compiler', './internal/dataframe/compiler', 'TestPivotPresenceArangoAudit'],
    ['execution', './internal/dataframe/execution', 'TestCohortPreviewWithSmallerRootPageKeepsAllMemberValuesAgainstArango'],
    ['explorer', './internal/explorer/arango', 'TestSelectionStorageAgainstArango'],
    ['store', './internal/store/arango', 'TestReceiptArangoStoreUsesImmutableTenantScopedQueries'],
    ['server', './internal/server', 'TestSavedRelatedRouteRowsAgainstArango'],
    ['artifact', './internal/dataframe/execution/chartifact/arango', 'TestCatalogLeaseArbitrationAgainstArango'],
    ['publication', './internal/dataframe/publication/arango', 'TestExecutionReadPinArbitrationAgainstArango'],
    ['publication-clickhouse', './internal/dataframe/publication/clickhouse', 'TestAbortDropsUnpublishedExecutionAgainstArango'],
    ['dataset-arango', './internal/dataset/arango', 'TestRevisionInflationAgainstArango'],
    ['dataset', './internal/dataset', 'TestReleaseRequirementsUnmetActivateAgainstArango'],
    ['compilation', './internal/explorer/compilation', 'TestAcceptanceObservationContributorAQLAndBinds'],
    ['recipe', './internal/dataframe/recipe/exec/arango', 'TestRecipeRegistryReadRoundTripAgainstArango'],
    ['backfill', './internal/ingest', 'TestRetainedSemanticInventoryBackfillArangoIntegration'],
].map(([id, pkg, required]) => ({ id, package: pkg, required, ...(id === 'backfill' ? { run: required } : {}) }));
// The store package's retained cases require external seeded data. Its ordinary
// unit cases still run; compiler, execution and server supply real row witnesses.
profiles.find(p => p.id === 'store').required = null;
profiles.find(p => p.id === 'execution').alsoRequired = ['TestPublicationRowStreamRejectsPayloadMetadataSmuggling'];
profiles.find(p => p.id === 'server').alsoRequired = ['TestDerivedArithmeticPoliciesAgainstArango'];
profiles.find(p => p.id === 'publication-clickhouse').alsoRequired = ['TestStalePinAbsentReclaimsAllAgainstArango'];
profiles.find(p => p.id === 'explorer').alsoRequired = [
    'TestDraftCASConcurrentWritersAgainstArango',
    'TestDraftCASAfterWinnerCommitAgainstArango',
    'TestDraftCASPublicApplyConflictClassAgainstArango',
    'TestDraftCASUpdateFailureRollsBackHistoryAgainstArango',
    'TestPublishProjectConfigsAtomicBatchRetryAndRetirementAgainstArango',
    'TestPublishProjectConfigsRejectsCollisionsImmutableConflictAndReleaseCASAgainstArango',
    'TestPublishProjectConfigsReactivatesSupersededSameExecutionRevisionAgainstArango',
];
profiles.find(p => p.id === 'compiler').alsoPackages = [
    { package: './internal/dataframe/compiler/render/aql', required: 'TestScalarSelectorArrayMatchesLegacyAgainstArango' },
];
// The assembled dual-store checkpoint has its own API/CI route with both services.
profiles.find(p => p.id === 'server').skip = '^(TestPublicationRowsAndFreshViewerAgainstArangoClickHouse|TestCurrentDraftAppendRowsAgainstArangoClickHouse)$';
export function checkEvents(stdout, profile) {
    const events = stdout.trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
    assert.ok(events.some(e => !e.Test && e.Action === 'pass'), 'No successful package completion');
    assert.ok(!events.some(e => e.Action === 'fail'), 'Go reported failure');
    const passes = events.filter(e => e.Test && e.Action === 'pass').map(e => e.Test);
    assert.ok(passes.length, 'No test assertions completed');
    for (const name of [profile.required, ...(profile.alsoRequired || [])].filter(Boolean)) {
        assert.ok(events.some(e => e.Test === name && e.Action === 'run'), `Required real integration case did not run: ${name}`);
        assert.ok(passes.includes(name), `Required real integration case did not pass: ${name}`);
    }
    const skipped = events.filter(e => e.Test && e.Action === 'skip').map(e => ({ test: e.Test, reason: events.filter(o => o.Test === e.Test && o.Action === 'output').map(o => o.Output).join('').trim() }));
    for (const skip of skipped) {
        assert.ok(external.has(skip.test), `Unexpected skipped case: ${skip.test}`);
    }
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
    const url = endpoint.origin, directory = resolve(output);
    assert.ok(!existsSync(directory) || readdirSync(directory).length === 0, 'Evidence directory must be empty');
    mkdirSync(directory, { recursive: true });
    const goEnv = { ...process.env, GOTOOLCHAIN: 'auto' };
    const version = spawnSync(goExecutable, ['version'], { cwd: root, env: goEnv, encoding: 'utf8', timeout: 30_000 });
    assert.equal(version.status, 0, `Go resolution failed: ${version.error?.message || version.stderr}`);
    const tool = { executable: goExecutable, version: version.stdout.trim() };
    const before = sourceFingerprint(), owned = [], deadline = performance.now() + overallBudgetMs;
    const evidence = { tool, sourceBefore: before, profiles: [], cleanup: [], status: 'running', separatelyRouted: [{ test: 'TestPublicationRowsAndFreshViewerAgainstArangoClickHouse', feature: 'publication-rows', ci: 'go-publication-rows', requires: ['Arango', 'ClickHouse'] }, { test: 'TestCurrentDraftAppendRowsAgainstArangoClickHouse', feature: 'append-rows', ci: 'go-publication-rows', requires: ['Arango', 'ClickHouse'] }, { test: 'TestReconcileDanglesActiveReleaseAgainstArangoClickHouse', feature: 'release-dangle-reconcile', ci: 'go-publication-rows', requires: ['Arango', 'ClickHouse'] }] };
    const database = `loom_test_restricted_starting_collection_${randomUUID()}`;
    const env = { ...process.env, GOTOOLCHAIN: 'auto', GOMAXPROCS: '2' };
    for (const key of Object.keys(env)) if (/^LOOM_.*(ARANGO|PROFILE|FULL_CDA|PIVOT_PRESENCE)/.test(key)) delete env[key];
    Object.assign(env, {
        LOOM_TEST_ARANGO_URL: url,
        LOOM_TEST_ARANGO_DATABASE: database,
        LOOM_TEST_REQUIRED: '1',
    });
    const child = (profile, setup, id = profile.id, childEnv = env) => {
        const started = performance.now();
        const timeout = Math.min(maxChildBudgetMs, Math.floor(deadline - started));
        assert.ok(timeout > 5_000, 'Eight-minute driver budget exhausted; reserve remaining CI time for cleanup');
        const childGoTimeout = `${Math.floor((timeout - 5_000) / 1000)}s`;
        const args = ['test', '-json', '-p', '1', `-timeout=${childGoTimeout}`, '-count=1'];
        if (setup || profile.run) args.push('-run', setup ? '^$' : `^${profile.run}$`);
        if (profile.skip) args.push('-skip', profile.skip);
        args.push(profile.package);
        const result = spawnSync(goExecutable, args, { cwd: root, env: childEnv, encoding: 'utf8', timeout, maxBuffer: 64 * 1024 * 1024, detached: true });
        if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
        const evidenceId = `${id}-${setup ? 'setup' : 'test'}`;
        writeFileSync(resolve(directory, `${evidenceId}.jsonl`), result.stdout || '');
        writeFileSync(resolve(directory, `${evidenceId}.stderr`), result.stderr || '');
        const record = { id: evidenceId, phase: setup ? 'compile-setup-no-proof' : 'verification', command: [goExecutable, ...args], timeoutMs: timeout, milliseconds: performance.now() - started, exit: result.status, error: result.error?.message };
        evidence.profiles.push(record);
        assert.equal(result.status, 0, `${evidenceId} failed: ${result.error?.message || result.stderr}`);
        if (!setup) Object.assign(record, checkEvents(result.stdout, profile));
    };
    try {
        await ownDatabase(url, database, owned);
        // Compilation is reported separately and never treated as test evidence.
        for (const profile of profiles) {
            child(profile, true);
            for (const extra of profile.alsoPackages || []) child({ ...profile, package: extra.package }, true, `${profile.id}-${extra.package.split('/').at(-1)}`);
        }
        const failures = [];
        const verify = (profile, id, profileEnv) => {
            try { child(profile, false, id, profileEnv); }
            catch (error) { failures.push({ id, error: error.message }); }
        };
        for (const profile of profiles) {
            const profileEnv = profile.id === 'store'
                ? { ...env, LOOM_TEST_ARANGO_URL: '', LOOM_TEST_ARANGO_DATABASE: '', LOOM_TEST_REQUIRED: '' }
                : env;
            verify(profile, profile.id, profileEnv);
            for (const extra of profile.alsoPackages || []) {
                verify({ ...profile, package: extra.package, required: extra.required, alsoRequired: [] }, `${profile.id}-${extra.package.split('/').at(-1)}`, profileEnv);
            }
        }
        evidence.failures = failures;
        evidence.status = failures.length ? 'failed' : evidence.profiles.some(p => p.unverified?.length) ? 'passed-with-explicit-unverified-external-cases' : 'passed';
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
