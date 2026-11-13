#!/usr/bin/env node
import { verificationTimeoutMs, goTestTimeout } from './lib/verification-budget.mjs';
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { apiDoctor, apiSourceFingerprint } from './lib/formal-api.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const sourceFingerprint = () => createHash('sha256').update(apiSourceFingerprint()).update(readFileSync(fileURLToPath(import.meta.url))).digest('hex');
const formalReplay = new Set([
    'TestCoreAuthoringProtocolModelReplay',
    'TestCoreAuthoringProtocolConcretePrefixReplay',
]);
const dualStore = new Set([
    'TestReconcileDanglesActiveReleaseAgainstArangoClickHouse',
]);
const pgExecDispatch = new Set([
    'TestPostgresEngineExecutesCompiledCommandThroughRealAdapter',
    'TestPostgresEnginePreviewBoundsCompletionOnRealServer',
    'TestPostgresEnginePreviewCancellationRealServer',
    'TestPostgresEngineTwoRealConnectionRaceDistinctBackends',
    'TestPostgresEngineParameterizedValuesCannotChangeShape',
    'TestPostgresEngineCrossTagRefusedBeforeServer',
    'TestPostgresEngineBackendKillConverges',
    'TestZZZeroLeftoverExec02PostgresFixtures',
    'TestVerifyArch01PostgreSQLDispatchZeroChurnRealServer',
    'TestVerifyArch01PostgreSQLBoundaryRaceDistinctBackendsOverlapRounds',
    'TestVerifyArch01PostgreSQLNamedRefusalsTouchNoServer',
    'TestVerifyArch01PostgreSQLCrashBeforeDriverNeverReachesServer',
    'TestPostgresExplainLivePG18IndexedAndSequentialPlans',
    'TestPostgresExplainLivePG18PermissionAndAbsence',
    'TestPostgresExplainLivePG18ConcurrentRounds',
    'TestZZZeroLeftoverExec10PostgresFixtures',
    'TestPostgresExplainWiringLivePG18SanitizedPlan',
    'TestZZZeroLeftoverExec10WiringPostgresFixtures',
]);
const pgExecRootPaging = new Set([
    'TestPostgresRootPagingMultisetAcrossPages',
    'TestPostgresRootPagingTerminatesWithoutExtraQuery/short_final_page_stops_without_another_key_query',
    'TestPostgresRootPagingTerminatesWithoutExtraQuery/empty_final_page_issues_no_row_query',
    'TestPostgresRootPagingCancellation/cancellation_between_key_and_row_steps_prevents_the_row_step',
    'TestPostgresRootPagingCancellation/control:_uncancelled_run_completes_on_the_same_fixture',
    'TestPostgresRootPagingResultContractSabotage/duplicate_root_key_fails_the_typed_result_contract',
    'TestPostgresRootPagingResultContractSabotage/row_outside_the_requested_page_fails_the_typed_result_contract',
    'TestPostgresRootPagingResultContractSabotage/key_page_not_advancing_past_the_previous_tail_is_refused',
    'TestPostgresRootPagingResultContractSabotage/control:_unsabotaged_run_proceeds_on_the_same_fixture',
    'TestPostgresRootPagingExplainBoundedness',
    'TestPostgresRootPagingWholeInputBypass/whole-input_PostgreSQL_output_cannot_reach_the_root-page_loop',
    'TestPostgresRootPagingWholeInputBypass/control:_non-group_PostgreSQL_stream_never_carries_page_fields',
    'TestZZPostgresRootPagingOwnershipAudit',
]);
const pgExec04Preview = new Set([
    'TestPostgresPreviewTruncatedAtLimit',
    'TestPostgresPreviewNaturalExhaustion',
    'TestPostgresPreviewEmptyScopeNoLeak',
    'TestPostgresPreviewCancellationNeverComplete',
    'TestPostgresPreviewDeadlineNeverComplete',
    'TestPostgresPreviewPublicColumnsCompiledOrder',
    'TestPostgresPreviewRootPagePreviewPlanNeverCarries',
    'TestPostgresPreviewMutationControls',
    'TestPostgresPreviewRootPageLoopProductionRowContract',
    'TestZZPostgresPreviewZeroLeftoverFixtures',
    'TestZZPostgresPreviewRootPageZeroLeftoverFixtures',
    'TestPostgresPreviewServerWiringFailClosesPGPlan',
    'TestZZPostgresPreviewServerZeroLeftoverFixtures',
]);
const pgExec08RowLineage = new Set([
    'TestPostgresRowLineageKnownRowExplainsContributors',
    'TestPostgresRowLineageUnknownRowReturnsTypedNotFound',
    'TestPostgresRowLineagePagingAdvancesWithoutDuplicatesAndStopsAtTerminalPage',
    'TestPostgresRowLineageForeignWitnessesAreExcluded',
    'TestPostgresRowLineageMalformedContributorIdentityIsContractError',
    'TestPostgresRowLineageStaleReceiptRejectsBeforeExecution',
]);
export const pgExec09CellTrace = new Set([
    'TestPostgresCellTracePreviewRoundTripServesEvidenceThroughThePort',
    'TestPostgresCellTraceNullValueStatesStayDistinctFromAValue',
    'TestPostgresCellTraceOffsetGateSkipsTheSoleCandidateWithoutChangingStatus',
    'TestPostgresCellTraceAuthoredIdentityResolvesThroughTheExplicitIdentityColumn',
    'TestPostgresCellTraceForgedIdentityIsTypedNotFoundAndRealIdentityResolves',
    'TestPostgresCellTraceWitnessCapIsIncompleteNotAbsence',
    'TestPostgresCellTraceForeignWitnessesAreExcluded',
    'TestPostgresCellTraceArmAReducedSetPagesThroughThePublicSeam',
    'TestPostgresCellTraceArmALossyFirstReadsAmbiguous',
    'TestPostgresCellTraceStaleReceiptRejectsBeforeExecution',
]);
const pgExec07PopulationMapping = new Set([
    'TestPostgresPopulationMappingLiveFlatExactMembership',
    'TestPostgresPopulationMappingLiveGroupDedupe',
    'TestPostgresPopulationMappingLiveCapAndCancellationAreIncomplete',
    'TestPostgresPopulationMappingLivePortFailureIsHardError',
    'TestPostgresPopulationMappingLiveStaleReceiptRejectsBeforeExecution',
    'TestPostgresPopulationMappingLiveNamedGapRefusesAtZeroStatements',
]);
export const pgExec03PostQuery = new Set([
    'TestPostgresPostQueryPreviewComputesNamedUUID5',
    'TestPostgresPostQueryStreamComputesNamedUUID3',
    'TestPostgresPostQueryNamedUUIDSemanticsParity',
    'TestPostgresPostQueryNestedMarkersMaterialize',
    'TestPostgresPostQueryMalformedMarkerFailsBeforeRow',
    'TestPostgresPostQueryThreeStateValues',
    'TestPostgresPostQueryCrossTargetJSONNullConvergence',
    'TestPostgresPostQueryDynamicColumnsRefuseAndMarkersNeedNoDynamicSurface',
    'TestPostgresPostQueryMutationControls',
    'TestZZPostgresPostQueryZeroLeftoverFixtures',
]);
const pgExec06Category = new Set([
    'TestPostgresCategoryExactValuesLivePG18',
    'TestPostgresCategoryDeterministicRerunLivePG18',
    'TestPostgresCategoryOverflowLivePG18',
    'TestPostgresCategoryEmptyScopeNoLeakLivePG18',
    'TestPostgresCategoryCancellationLivePG18',
    'TestPostgresCategoryAuthScopeLivePG18',
    'TestPostgresCategoryStageScanLivePG18',
    'TestPostgresCategoryRefusalsZeroTouchLivePG18',
    'TestZZPostgresCategoryZeroLeftoverFixtures',
]);
const pgQueryRoute = new Set([
    'TestPostgreSQLQueryRouteExplorerAndRowOpsExplicitlyUnavailable',
    'TestPostgreSQLQueryRouteServesExactScalarsThroughProductionHandler',
    'TestPostgreSQLFirstQueryRouteDeadEndpointFailsBounded',
    'TestPostgreSQLFirstQueryRouteLivenessAndReadiness',
    'TestZZQRouteZeroLeftoverFixtures',
]);
const pgIr03PrefixSeam = new Set([
    'TestCompileClickHousePrefixPostgreSQLExecutesExactRows',
]);
const pgRuntimeComposition = new Set([
    'TestPostgresRuntimeFullLadderServesProductionReads',
    'TestPostgresRuntimeServerLoadRouteServesEndToEnd',
    'TestPostgresRuntimeHealthReadinessIsIndependentAndSchemaAware',
    'TestPostgresRuntimeReadinessReportsNeverMigratedSchema',
    'TestPostgresRuntimeReadinessReportsMissingSchema',
    'TestPostgresRuntimeVerifySchemaIsReadOnly',
    'TestPostgresRuntimeRepositoryPublishRouteActivatesThroughFinalFlip',
    'TestPostgresRuntimeRepositoryPublishRetryAfterActivationFailureRevives',
    'TestZZPgCompZeroLeftoverFixtures',
]);
const archBackend08Matrix = new Set([
    'TestBackendCompositionContract/liveProductionCompose_ledgerHolds',
]);
const pgExec12Exclusions = new Set([
    'TestPostgresExclusionsExactEvidenceLivePG18',
    'TestPostgresExclusionsBoundedPagingLivePG18',
    'TestPostgresExclusionsIdentityOmissionLivePG18',
    'TestPostgresExclusionsCategoryThreeStateLivePG18',
    'TestPostgresExclusionsCancellationLivePG18',
    'TestPostgresExclusionsUnsupportedShapeLivePG18',
    'TestPostgresExclusionsAuthScopeLivePG18',
    'TestPostgresExclusionsNoPolicyStatusDistinctLivePG18',
    'TestZZPostgresExclusionsZeroLeftoverFixtures',
]);
const pgExec11Capture = new Set([
    'TestExec11lvLiveRestrictedCaptureFeedsCombine',
    'TestExec11lvLiveCaptureRejectsForgedAuthPath',
    'TestExec11lvLiveCaptureCancellationLeavesNothing',
    'TestExec11lvLiveEvidenceFreeCaptureRefused',
    'TestExec11lvLiveCombineExecutesAgainstClickHouse',
    'TestZZPostgresCaptureZeroLeftoverFixtures',
]);
export const archBackend12Diagnostics = new Set([
    'TestBackendNeutralDiagnosticsLivePostgreSQL',
]);
export const backendArchitectureConformance = new Set([
    'TestBackendArchitectureConformance/postgresql',
    'TestBackendArchitectureConformance/arangodb',
]);
const external = new Set([
    'TestPivotPresenceAudit',
    'TestSemanticGroupProjectionFactorizationSelectsExistingIndexOnFullCDAReadOnly',
    'TestPopulationPlanProfilesAgainstArango',
    'TestProfileCorpusAgainstArango',
    'TestCompoundEdgeIndexExplainAgainstArango',
    'TestReceiptPreviewExplainAgainstArango',
    ...formalReplay,
    ...dualStore,
    ...pgExecDispatch,
    ...pgExecRootPaging,
    ...pgExec04Preview,
    ...pgExec08RowLineage,
    ...pgExec07PopulationMapping,
    ...pgExec09CellTrace,
    ...pgExec03PostQuery,
    ...pgExec06Category,
    ...pgQueryRoute,
    ...pgIr03PrefixSeam,
    ...pgRuntimeComposition,
    ...archBackend08Matrix,
    ...pgExec12Exclusions,
    ...pgExec11Capture,
    ...archBackend12Diagnostics,
    ...backendArchitectureConformance,
]);
export const profiles = [
    ['factory', './internal/backends/arango', 'TestArangoFactoryOwnershipContract'],
    ['catalog', './internal/backends/arango/internal/catalog', 'TestResourceInventoryPersistsAndScopesCountsAcrossStoreReopen'],
    ['compiler', './internal/dataframe/compiler', 'TestPivotPresenceArangoAudit'],
    ['execution', './internal/dataframe/execution', 'TestCohortPreviewWithSmallerRootPageKeepsAllMemberValuesAgainstArango'],
    ['explorer', './internal/backends/arango/internal/explorer', 'TestSelectionStorageAgainstArango'],
    ['store', './internal/backends/arango/internal/store', 'TestReceiptArangoStoreUsesImmutableTenantScopedQueries'],
    ['server', './internal/server', 'TestSavedRelatedRouteRowsAgainstArango'],
    ['artifact', './internal/backends/arango/internal/chartifact', 'TestCatalogLeaseArbitrationAgainstArango'],
    ['publication', './internal/backends/arango/internal/publication', 'TestExecutionReadPinArbitrationAgainstArango'],
    ['backfill', './internal/ingest', 'TestRetainedSemanticInventoryBackfillArangoIntegration'],
].map(([id, pkg, required]) => ({ id, package: pkg, required, ...(id === 'backfill' ? { run: required } : {}) }));
profiles.find(p => p.id === 'store').required = null;
profiles.find(p => p.id === 'server').alsoRequired = ['TestDerivedArithmeticPoliciesAgainstArango', 'TestBackendNeutralDiagnosticsAgainstArango',
    'TestBackendArchitectureConformance', 'TestBackendArchitectureConformance/arangodb',
    ...['scalarQueryServesLiteralScalars', 'repeatedRelatedOccurrenceStaysDistinct', 'nullAndMissingSemanticsPreserved',
        'explicitScopeServesAuthorizedAndExcludesUnauthorized', 'restrictedEmptyScopeServesNothing', 'unrestrictedSeesEverything',
        'generationIsolationServesActiveOnly', 'stablePagingIsDeterministic', 'explorerDraftCASOneWinnerExactPreimage',
        'publicationControlFenceRejectsStaleOwner', 'cancellationTypedFailureNoFallback',
        'backendUnavailableAndWrongTagNoFallback', 'upgradeFromPinnedPreRefactorReopensSafely']
        .map(cell => 'TestBackendArchitectureConformance/arangodb/' + cell)];
profiles.find(p => p.id === 'explorer').alsoRequired = ['TestDraftCASConcurrentWritersAgainstArango', 'TestDraftCASAfterWinnerCommitAgainstArango', 'TestDraftCASPublicApplyConflictClassAgainstArango', 'TestDraftCASUpdateFailureRollsBackHistoryAgainstArango'];
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
    for (const skip of skipped) assert.ok(external.has(skip.test), `Unexpected skipped case: ${skip.test}`);
    return { passes, unverified: skipped };
}
export async function ownDatabase(endpoint, name, owned, request = fetch) {
    const response = await request(`${endpoint}/_api/database`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ name }), signal: AbortSignal.timeout(10000) });
    assert.ok(response.ok, `Refusing existing/unavailable database ${name}: HTTP ${response.status}`);
    owned.push(name);
}
export async function cleanupOwnedDatabases(endpoint, owned, evidence, request = fetch) {
    for (const name of owned) {
        let response;
        try {
            response = await request(`${endpoint}/_api/database/${encodeURIComponent(name)}`, { method: 'DELETE', signal: AbortSignal.timeout(10000) });
        } catch (error) {
            evidence.cleanupAudit.push({ database: name, transportError: error.message });
            console.warn(`[cleanup-audit] transient transport failure dropping ${name}: ${error.message}; run verdict unaffected`);
            continue;
        }
        evidence.cleanup.push({ database: name, status: response.status });
        if (!response.ok && response.status !== 404) {
            evidence.status = 'failed';
            evidence.cleanup.push({ database: name, error: `Database cleanup HTTP ${response.status}` });
        }
    }
}
export async function runArango(output) {
    const endpoint = new URL(process.env.LOOM_TEST_ARANGO_URL || '');
    assert.ok(['http:', 'https:'].includes(endpoint.protocol) && !endpoint.username && !endpoint.password && endpoint.pathname === '/', 'Use an explicit unauthenticated owned Arango origin');
    const url = endpoint.origin, tool = apiDoctor(), directory = resolve(output);
    assert.ok(!existsSync(directory) || readdirSync(directory).length === 0, 'Evidence directory must be empty');
    mkdirSync(directory, { recursive: true });
    const before = sourceFingerprint(), owned = [], deadline = performance.now() + 8 * 60 * 1000;
    const evidence = { tool, sourceBefore: before, profiles: [], cleanup: [], cleanupAudit: [], status: 'running', separatelyRouted: [{ test: 'TestPublicationRowsAndFreshViewerAgainstArangoClickHouse', feature: 'publication-rows', ci: 'go-publication-rows', requires: ['Arango', 'ClickHouse'] }, { test: 'TestCurrentDraftAppendRowsAgainstArangoClickHouse', feature: 'append-rows', ci: 'go-publication-rows', requires: ['Arango', 'ClickHouse'] }, { test: 'TestReconcileDanglesActiveReleaseAgainstArangoClickHouse', feature: 'release-dangle-reconcile', ci: 'go-publication-rows', requires: ['Arango', 'ClickHouse'] }] };
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
        const args = ['test', '-json', '-p', '1', `-timeout=${goTestTimeout}`, '-count=1'];
        if (setup || profile.run) args.push('-run', setup ? '^$' : `^${profile.run}$`);
        if (profile.skip) args.push('-skip', profile.skip);
        args.push(profile.package);
        const start = performance.now();
        const timeout = Math.min(setup ? Math.max(60000, verificationTimeoutMs) : verificationTimeoutMs, Math.floor(deadline - start));
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
        for (const profile of profiles) child(profile, true);
        for (const profile of profiles) child(profile, false);
        evidence.status = evidence.profiles.some(p => p.unverified?.length) ? 'passed-with-explicit-unverified-external-cases' : 'passed';
    } catch (error) { evidence.status = 'failed'; evidence.error = error.message; }
    finally {
        await cleanupOwnedDatabases(url, owned.reverse(), evidence);
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
