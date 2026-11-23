#!/usr/bin/env node
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { relative, resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';

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
        ...['internal', 'api', 'fhir', 'tests/fixtures'].flatMap(directory => sourceFiles(resolve(sourceRoot, directory))),
        resolve(sourceRoot, 'go.mod'),
        resolve(sourceRoot, 'go.sum'),
        resolve(sourceRoot, 'scripts/run-arango-integration.mjs'),
        resolve(sourceRoot, 'tests/scripts/run-arango-integration.test.mjs'),
    ].sort();
    for (const path of sources) {
        hash.update(relative(sourceRoot, path));
        hash.update('\0');
        hash.update(readFileSync(path));
        hash.update('\0');
    }
    return hash.digest('hex');
}

export function runBounded(command, args, { timeout, maxBuffer = 64 * 1024 * 1024, ...options }) {
    return new Promise((resolvePromise) => {
        const child = spawn(command, args, {
            ...options,
            detached: process.platform !== 'win32',
            stdio: ['ignore', 'pipe', 'pipe'],
        });
        const stdout = [], stderr = [];
        let stdoutBytes = 0, stderrBytes = 0;
        let timedOut = false, outputLimitError, spawnError;
        const killGroup = () => {
            try {
                if (child.pid && process.platform !== 'win32') process.kill(-child.pid, 'SIGKILL');
                else child.kill('SIGKILL');
            } catch (error) {
                if (error.code !== 'ESRCH') {
                    try { child.kill('SIGKILL'); } catch (fallbackError) {
                        if (fallbackError.code !== 'ESRCH' && !outputLimitError) outputLimitError = fallbackError;
                    }
                }
            }
        };
        const collect = (chunks, chunk, size) => {
            if (size + chunk.length > maxBuffer) {
                if (!outputLimitError) {
                    outputLimitError = Object.assign(new Error(`${command} exceeded the ${maxBuffer} byte output limit`), { code: 'ERR_CHILD_PROCESS_STDIO_MAXBUFFER' });
                    killGroup();
                }
                return size;
            }
            chunks.push(chunk);
            return size + chunk.length;
        };
        child.stdout.on('data', chunk => { stdoutBytes = collect(stdout, chunk, stdoutBytes); });
        child.stderr.on('data', chunk => { stderrBytes = collect(stderr, chunk, stderrBytes); });
        child.once('error', error => { spawnError = error; });
        const timer = setTimeout(() => {
            timedOut = true;
            killGroup();
        }, timeout);
        child.once('close', (status, signal) => {
            clearTimeout(timer);
            const timeoutError = timedOut
                ? Object.assign(new Error(`${command} timed out after ${timeout}ms`), { code: 'ETIMEDOUT' })
                : undefined;
            resolvePromise({
                stdout: Buffer.concat(stdout).toString('utf8'),
                stderr: Buffer.concat(stderr).toString('utf8'),
                status,
                signal,
                error: spawnError || outputLimitError || timeoutError,
                pid: child.pid,
            });
        });
    });
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
    'TestPreviewSupersededIndexMigrationAgainstArango',
    ...dualStore,
]);

const previewIndexMigrationTests = [
    'TestPreviewSupersededIndexMigrationAgainstArango',
    'TestPreviewSupersededPublicMigrationVersionedAgainstArango',
    'TestOpenExistingMissingDatabaseDoesNotCreateAgainstArango',
];
const previewIndexMigrationPattern = `(${previewIndexMigrationTests.join('|')})`;

export const profiles = [
    ['catalog', './internal/catalog/arango', 'TestResourceInventoryPersistsAndScopesCountsAcrossStoreReopen'],
    ['compiler', './internal/dataframe/compiler', 'TestPivotPresenceArangoAudit'],
    ['execution', './internal/dataframe/execution', 'TestCohortPreviewWithSmallerRootPageKeepsAllMemberValuesAgainstArango'],
    ['explorer', './internal/explorer/arango', 'TestSelectionStorageAgainstArango'],
    ['store', './internal/store/arango', 'TestReceiptArangoStoreUsesImmutableTenantScopedQueries'],
    ['preview-index-migration', './internal/store/arango', previewIndexMigrationTests[0]],
    ['execution-arango', './internal/dataframe/execution/arango', 'TestDerivedArithmeticPoliciesAgainstArango'],
    ['explorer-api', './internal/api/explorer', 'TestSavedRelatedRouteRowsAgainstArango'],
    ['artifact', './internal/dataframe/execution/chartifact/arango', 'TestCatalogLeaseArbitrationAgainstArango'],
    ['publication', './internal/dataframe/publication/arango', 'TestExecutionReadPinArbitrationAgainstArango'],
    ['publication-clickhouse', './internal/dataframe/publication/clickhouse', 'TestAbortDropsUnpublishedExecutionAgainstArango'],
    ['dataset-arango', './internal/dataset/arango', 'TestRevisionInflationAgainstArango'],
    ['dataset', './internal/dataset', 'TestReleaseRequirementsUnmetActivateAgainstArango'],
    ['compilation', './internal/explorer/compilation', 'TestAcceptanceObservationContributorAQLAndBinds'],
    ['recipe', './internal/dataframe/recipe/arango', 'TestRecipeRegistryReadRoundTripAgainstArango'],
    ['backfill', './internal/semantic/backfill', 'TestRetainedSemanticInventoryBackfillArangoIntegration'],
].map(([id, pkg, required]) => ({ id, package: pkg, required, ...(id === 'backfill' ? { run: required } : {}) }));
// The store package's retained cases require external seeded data. Its ordinary
// unit cases still run; compiler and execution packages supply real row witnesses.
profiles.find(p => p.id === 'store').required = null;
profiles.find(p => p.id === 'store').skip = `^${previewIndexMigrationPattern}$`;
profiles.find(p => p.id === 'preview-index-migration').run = previewIndexMigrationPattern;
profiles.find(p => p.id === 'preview-index-migration').alsoRequired = previewIndexMigrationTests.slice(1);
profiles.find(p => p.id === 'execution').alsoRequired = ['TestPublicationRowStreamRejectsPayloadMetadataSmuggling'];
profiles.find(p => p.id === 'execution-arango').alsoRequired = [
    'TestGraphRowsIndependentFixtureAgainstArango',
    'TestGraphRowsDefaultRelatedInventoryAgainstArango',
    'TestGraphRowsLogicalContributorOrderAgainstArango',
];
profiles.find(p => p.id === 'explorer-api').alsoRequired = [
    'TestCoreAuthoringProtocolAgainstArango',
    'TestRestrictedStartingCollectionRejectsDeniedReferences',
];
profiles.find(p => p.id === 'publication-clickhouse').alsoRequired = ['TestStalePinAbsentReclaimsAllAgainstArango'];
profiles.find(p => p.id === 'explorer').alsoRequired = [
    // WP-17 TableShape conversion: mutable owners, fail-closed lookup, CAS rollback,
    // immutable history conflict, large integer values, and history reuse.
    'TestStoredTableShapeMigrationAgainstArango',
    'TestTableShapeProjectionLookupFailsClosedAgainstArango',
    'TestTableShapeMigrationCASFailureRollsBackExactRevisionHistoryAndProjectionAgainstArango',
    'TestTableShapeMigrationRejectsConflictingImmutableHistoryAgainstArango',
    'TestTableShapeMigrationLargeIntegerProjectionAgainstArango',
    'TestTableShapeMigrationLargeIntegerOwnerAgainstArango',
    'TestTableShapeArchivedLargeIntegerReceiptAgainstArango',
    'TestTableShapeMigrationReusesJSONHistoryWithoutRewritingAgainstArango',
    // WP-18 workspace encoding: mutable and immutable records, CAS rollback, and mixed nil digest.
    'TestStoredWorkspaceMigrationAgainstArango',
    'TestImmutableWorkspaceMigrationAgainstArango',
    'TestStoredWorkspaceMigrationCASFailureRollsBackHistoryAndProjectionAgainstArango',
    'TestStoredWorkspaceMigrationMixedNilDigestAgainstArango',
    'TestDraftCASConcurrentWritersAgainstArango',
    'TestDraftCASAfterWinnerCommitAgainstArango',
    'TestDraftCASPublicApplyConflictClassAgainstArango',
    'TestDraftCASUpdateFailureRollsBackHistoryAgainstArango',
    'TestProjectConfigCatalogAnchorPublishRetryAndRebindAgainstArango',
    'TestViewBuilderCatalogSaveDefaultsDeleteAndSourceRebindAgainstArango',
    'TestPublishProjectConfigsAtomicBatchRetryAndRetirementAgainstArango',
    'TestPublishProjectConfigsRejectsCollisionsImmutableConflictAndReleaseCASAgainstArango',
    'TestPublishProjectConfigsReactivatesSupersededSameExecutionRevisionAgainstArango',
];
profiles.find(p => p.id === 'compiler').alsoRequired = [
    'TestCategoryPairProjectionPreservesPresenceScopeAndOverflowAgainstArango',
    'TestColumnPopulationMatchesExactArangoStageRows',
    'TestNestedExtensionColumnsKeepAncestorOwnersAgainstArango',
    'TestCurrentPivotNullMissingPartitionAgainstArango',
    'TestPivotLineagePartitionsForwardRowsAndTraceReconstructsAgainstArango',
    'TestGroupRowLineagePartitionsForwardRowsAgainstArango',
    'TestFilteredSourceCellTraceMatchesForwardValueAgainstArango',
    'TestS04TableReshapeOracleAgainstArango',
    'TestS04GroupedPivotMaterializationPreservesAuthResourcePathAgainstArango',
    'TestGroupedPivotReservedPhysicalOutputNameAgainstArango',
    'TestRootCategoryScanUsesSharedTypedIndexContractAgainstArango',
    'TestRelatedCategoryScanUsesTypedIndexAgainstArango',
    'TestRelatedStreamingCategoryScanUsesEdgeIndexesAgainstArango',
];
profiles.find(p => p.id === 'compiler').alsoPackages = [
    { package: './internal/dataframe/compiler/render/aql', required: 'TestScalarSelectorArrayMatchesLegacyAgainstArango' },
];
// Dual-store checkpoints run in the job that owns both services.
profiles.find(p => p.id === 'explorer-api').skip = '^TestPublicationRowsAndFreshViewerAgainstArangoClickHouse$';
profiles.find(p => p.id === 'execution').skip = '^TestCurrentDraftAppendRowsAgainstArangoClickHouse$';
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
    const version = await runBounded(goExecutable, ['version'], { cwd: root, env: goEnv, timeout: 30_000 });
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
        LOOM_SELECTOR_ARANGO_URL: `${url}/_db/${encodeURIComponent(database)}`,
        LOOM_TEST_REQUIRED: '1',
    });
    const child = async (profile, setup, id = profile.id, childEnv = env) => {
        const started = performance.now();
        const timeout = Math.min(maxChildBudgetMs, Math.floor(deadline - started));
        assert.ok(timeout > 5_000, 'Eight-minute driver budget exhausted; reserve remaining CI time for cleanup');
        const childGoTimeout = `${Math.floor((timeout - 5_000) / 1000)}s`;
        const args = ['test', '-json', '-p', '1', `-timeout=${childGoTimeout}`, '-count=1'];
        if (setup || profile.run) args.push('-run', setup ? '^$' : `^${profile.run}$`);
        if (profile.skip) args.push('-skip', profile.skip);
        args.push(profile.package);
        const result = await runBounded(goExecutable, args, { cwd: root, env: childEnv, timeout, maxBuffer: 64 * 1024 * 1024 });
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
            await child(profile, true);
            for (const extra of profile.alsoPackages || []) await child({ ...profile, package: extra.package }, true, `${profile.id}-${extra.package.split('/').at(-1)}`);
        }
        const failures = [];
        const verify = async (profile, id, profileEnv) => {
            try { await child(profile, false, id, profileEnv); }
            catch (error) { failures.push({ id, error: error.message }); }
        };
        for (const profile of profiles) {
            const profileEnv = profile.id === 'store'
                ? { ...env, LOOM_TEST_ARANGO_URL: '', LOOM_TEST_ARANGO_DATABASE: '', LOOM_TEST_REQUIRED: '' }
                : env;
            await verify(profile, profile.id, profileEnv);
            for (const extra of profile.alsoPackages || []) {
                await verify({ ...profile, package: extra.package, required: extra.required, alsoRequired: [] }, `${profile.id}-${extra.package.split('/').at(-1)}`, profileEnv);
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
