import assert from 'node:assert/strict';
import { operationHistoryFixture as routineJourneyFixture, operationHistoryIds as routineJourneyIds } from './formal-operation-history-model.mjs';
export { routineJourneyFixture, routineJourneyIds };
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { root } from './formal-tools.mjs';
import { apiDoctor, apiSourceFingerprint, checkApiEvents } from './formal-api.mjs';
import { verificationTimeoutMs, goTestTimeout } from './verification-budget.mjs';

export const journeyFixture = 'ui/packages/loom-ui/testSupport/operationHistoryJourneys.json';
export const journeySuite = 'src/features/ExplorerBuilder/BuilderWorkspace.operationHistory.formal.test.tsx';
export const journeyTest = 'TestConstructionProposalOperationHistoryTraces';
export const journeyIds = ['operation-history-count-to-mean-accepted-history-filter', 'operation-history-group-key-cascade-count-to-mean-ready-preserves-calculate'];
const canonical = value => value && typeof value === 'object' ? Array.isArray(value) ? value.map(canonical) : Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
const equal = (a, b, message) => assert.deepEqual(canonical(a), canonical(b), message);
const sha = path => createHash('sha256').update(readFileSync(path)).digest('hex');
const json = path => JSON.parse(readFileSync(path, 'utf8'));
const prefix = 'BuilderWorkspace operation history actual candidate capture ';
const patterns = {
    'ui-prepare': 'replays each operation schedule',
    'ui-capture': 'wires BuilderWorkspace history|replays each operation schedule|accounts for every',
    'ui-panel': 'reviews proposed removals before explicit Apply',
    'ui-reload': 'keeps the saved Group operation and identifiers across remounts',
};
const expectedTitles = {
    'ui-prepare': journeyIds.map(id => name => name === `${prefix}replays each operation schedule through ConstructionReshapeEditor: '${id}'`),
    'ui-capture': [name => name === `${prefix}wires BuilderWorkspace history and the actual Group editor to its proposal request`, ...journeyIds.map(id => name => name === `${prefix}replays each operation schedule through ConstructionReshapeEditor: '${id}'`), name => name === `${prefix}accounts for every exercised schedule and proposal checkpoint`],
    'ui-panel': [name => name === `${prefix}reviews proposed removals before explicit Apply`],
    'ui-reload': [name => name === `${prefix}keeps the saved Group operation and identifiers across remounts`],
};
function fixtureForFeature(feature) {
    assert.ok(['operation-history', 'operation-history-routine'].includes(feature), 'Unknown journey feature');
    return feature === 'operation-history-routine' ? routineJourneyFixture() : json(join(root, journeyFixture));
}
function fingerprint() {
    const hash = createHash('sha256').update(apiSourceFingerprint());
    const visit = directory => { for (const entry of readdirSync(join(root, directory), { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) { const file = `${directory}/${entry.name}`; if (entry.isDirectory()) visit(file); else if (entry.isFile()) hash.update(file).update('\0').update(readFileSync(join(root, file))); } };
    for (const directory of ['ui/packages/loom-ui/src', 'ui/packages/loom-ui/testSupport']) visit(directory);
    for (const file of ['ui/package-lock.json', 'ui/packages/loom-ui/vitest.config.ts', 'ui/packages/loom-ui/vitest.setup.ts', 'scripts/lib/formal-operation-history.mjs', 'scripts/lib/formal-operation-history-model.mjs']) hash.update(file).update(readFileSync(join(root, file)));
    return hash.digest('hex');
}
export function validateJourneyFixture(value) {
    assert.equal(value.schemaVersion, 1); assert.ok(Array.isArray(value.cases));
    if (value.cases.length === routineJourneyIds.length) equal(value, routineJourneyFixture(), 'Routine journey inventory or expectations changed');
    else equal(value.cases.map(row => row.id).sort(), [...journeyIds].sort(), 'Journey fixture must contain exactly the two retained witnesses');
    for (const trace of value.cases) {
        assert.ok(Array.isArray(trace.actions) && Array.isArray(trace.expectations));
        assert.equal(trace.actions.filter(action => action.kind === 'propose').length, 1);
        const ready = trace.expectations.filter(row => row.proposalStatus === 'READY'); assert.equal(ready.length, 1);
        assert.equal(trace.actions[ready[0].actionIndex].kind, 'propose');
        assert.ok(trace.initial.workspaceContext.outputId);
    }
    return value;
}
export function journeyDoctor({ feature = 'operation-history' } = {}) {
    fixtureForFeature(feature);
    assert.ok(existsSync(join(root, 'ui/node_modules/vitest/vitest.mjs')), 'Install UI dependencies');
    const fixture = validateJourneyFixture(fixtureForFeature(feature));
    return { status: 'ready-for-journey-tests', feature, cases: fixture.cases.map(row => row.id), tool: apiDoctor(), boundary: `${fixture.cases.length} recovered UI-to-public-Go operation witnesses; controlled store/compiler/execution; not TLA, browser or durable storage` };
}
export function validateJourneyUI(report, phase, fixture) {
    const ids = fixture ? validateJourneyFixture(fixture).cases.map(row => row.id) : journeyIds;
    const titles = { ...expectedTitles, 'ui-prepare': ids.map(id => name => name === `${prefix}replays each operation schedule through ConstructionReshapeEditor: '${id}'`), 'ui-capture': [expectedTitles['ui-capture'][0], ...ids.map(id => name => name === `${prefix}replays each operation schedule through ConstructionReshapeEditor: '${id}'`), expectedTitles['ui-capture'].at(-1)] };
    assert.ok(Object.hasOwn(expectedTitles, phase), 'Unknown UI journey phase');
    assert.equal(report.success, true); assert.equal(report.numFailedTests, 0);
    assert.ok(Array.isArray(report.testResults) && report.testResults.length === 1);
    assert.equal(resolve(report.testResults[0].name), join(root, 'ui/packages/loom-ui', journeySuite));
    const assertions = report.testResults[0].assertionResults;
    assert.ok(Array.isArray(assertions));
    assert.ok(assertions.every(row => ['passed', 'pending', 'skipped'].includes(row.status)), 'Journey assertion failed');
    const selected = assertions.filter(row => !['pending', 'skipped'].includes(row.status));
    assert.equal(selected.length, titles[phase].length, 'Unexpected or empty journey assertion inventory');
    assert.equal(new Set(selected.map(row => row.fullName)).size, selected.length, 'Duplicate journey assertions');
    for (const matches of titles[phase]) assert.equal(selected.filter(row => matches(row.fullName)).length, 1, 'Missing or foreign journey assertion');
    return { selected: selected.map(row => row.fullName), filtered: assertions.length - selected.length };
}
function casesById(value, fixture) {
    assert.equal(value.schemaVersion, 1); assert.ok(Array.isArray(value.cases));
    equal(value.cases.map(row => row.id).sort(), fixture.cases.map(row => row.id).sort(), 'Missing, duplicate or foreign artifact cases');
    return new Map(value.cases.map(row => [row.id, row]));
}
function validateAcknowledgement(ack) {
    assert.equal(ack.result, 'SUCCESS');
    for (const field of ['acceptedVersionBefore', 'acceptedVersionAfter']) assert.ok(Number.isSafeInteger(ack[field]) && ack[field] > 0, `Invalid acknowledgement ${field}`);
    for (const field of ['acceptedDigestBefore', 'acceptedDigestAfter']) assert.match(ack[field], /^sha256:[a-f0-9]{64}$/, `Invalid acknowledgement ${field}`);
    assert.equal(ack.acceptedVersionAfter, ack.acceptedVersionBefore + 1);
    assert.notEqual(ack.acceptedDigestAfter, ack.acceptedDigestBefore);
}
export function validateJourneyArtifacts({ fixture, seeds, identities, candidates, responses, actualGo }) {
    validateJourneyFixture(fixture);
    const seedMap = seeds && casesById(seeds, fixture), identityMap = identities && casesById(identities, fixture), candidateMap = candidates && casesById(candidates, fixture), responseMap = responses && casesById(responses, fixture);
    for (const trace of fixture.cases) {
        const seed = seedMap?.get(trace.id), identity = identityMap?.get(trace.id), candidate = candidateMap?.get(trace.id), response = responseMap?.get(trace.id);
        if (seeds) assert.ok(seed.acceptedWorkspace && Array.isArray(seed.acceptedWorkspace.documents), 'Missing actual UI seed workspace');
        if (identities) { assert.ok(Number.isSafeInteger(identity.acceptedVersion) && identity.acceptedVersion > 0); assert.match(identity.acceptedDigest, /^sha256:[a-f0-9]{64}$/); assert.ok(typeof identity.snapshotToken === 'string' && identity.snapshotToken); assert.equal(identity.outputId, trace.initial.workspaceContext.outputId); }
        const indexes = trace.expectations.filter(row => row.proposalStatus === 'READY').map(row => row.actionIndex).sort();
        for (const row of [candidate, response].filter(Boolean)) { assert.ok(Array.isArray(row.checkpoints)); equal(row.checkpoints.map(cp => cp.actionIndex).sort(), indexes, 'Missing, duplicate or substituted proposal checkpoint'); }
        if (candidate) for (const cp of candidate.checkpoints) {
            const request = cp.proposalRequest; assert.ok(request && request.requestId);
            assert.equal(request.expectedDraftVersion, identity.acceptedVersion); assert.equal(request.expectedDraftDigest, identity.acceptedDigest); assert.equal(request.snapshotToken, identity.snapshotToken); assert.equal(request.outputId, identity.outputId);
            equal(request.candidateConstruction, cp.candidateConstruction, 'Changed captured candidate');
        }
        if (actualGo) {
            assert.ok(identity, 'Acknowledgements require prepared identities');
            const observed = actualGo.cases.find(row => row.id === trace.id);
            equal(observed.applyAck.map(row => row.actionIndex), trace.actions.flatMap((row, index) => row.kind === 'applyAck' ? [index] : []));
            let version = identity.acceptedVersion, digest = identity.acceptedDigest;
            for (const ack of observed.applyAck) {
                validateAcknowledgement(ack);
                assert.equal(ack.acceptedVersionBefore, version, 'Acknowledgement belongs to another prepared version');
                assert.equal(ack.acceptedDigestBefore, digest, 'Acknowledgement belongs to another prepared digest');
                version = ack.acceptedVersionAfter; digest = ack.acceptedDigestAfter;
            }
        }
        if (response) for (const cp of response.checkpoints) {
            const request = candidate.checkpoints.find(row => row.actionIndex === cp.actionIndex).proposalRequest, result = cp.proposalResponse;
            assert.equal(cp.requestId, request.requestId); assert.ok(typeof cp.requestFingerprint === 'string' && cp.requestFingerprint);
            assert.equal(result.draftVersion, identity.acceptedVersion); assert.equal(result.draftDigest, identity.acceptedDigest); assert.equal(result.snapshotToken, identity.snapshotToken); assert.equal(result.outputId, identity.outputId);
            assert.ok(result.proposalId); assert.equal(result.previewStatus, 'PREVIEW_PENDING');
            if (actualGo) { const observed = actualGo.cases.find(row => row.id === trace.id).proposalCheckpoints.find(row => row.actionIndex === cp.actionIndex); assert.equal(cp.requestId, observed.requestId); assert.equal(cp.requestFingerprint, observed.requestFingerprint); }
            assert.equal(cp.previewResult.receiptId, result.proposalId); assert.equal(cp.previewResult.outputId, identity.outputId);
        }
    }
    return true;
}
export function validateJourneyReloads({ fixture, identities, actualGo, reloads }) {
    validateJourneyFixture(fixture);
    const expectedTraces = fixture.cases.filter(trace => trace.actions.some(row => row.kind === 'applyAck'));
    const records = casesById(reloads, { cases: expectedTraces });
    const prepared = casesById(identities, fixture);
    const metadata = casesById({ schemaVersion: 1, cases: actualGo.reloads }, { cases: expectedTraces });
    for (const trace of expectedTraces) {
        const record = records.get(trace.id), identity = prepared.get(trace.id), observed = actualGo.cases.find(row => row.id === trace.id);
        assert.equal(observed.applyAck.length, 1, 'Reload witness requires one successful Apply');
        const ack = observed.applyAck[0]; validateAcknowledgement(ack);
        assert.equal(record.actionIndex, ack.actionIndex); assert.equal(metadata.get(trace.id).actionIndex, ack.actionIndex);
        assert.equal(record.outputId, identity.outputId); assert.equal(record.stageId, 'source_projection');
        for (const [state, version, digest] of [[record.before, identity.acceptedVersion, identity.acceptedDigest], [record.after, ack.acceptedVersionAfter, ack.acceptedDigestAfter]]) {
            assert.equal(state.kind, 'ExplorerBuilderState'); assert.equal(state.draftVersion, version); assert.equal(state.draftDigest, digest);
            assert.equal(state.catalog.snapshotToken, identity.snapshotToken);
        }
        assert.equal(metadata.get(trace.id).draftVersion, record.after.draftVersion); assert.equal(metadata.get(trace.id).draftDigest, record.after.draftDigest);
        equal(record.after.workspace, record.storedWorkspace, 'Public Builder read differs from independently stored workspace');
        const docs = record.storedWorkspace.documents.filter(doc => doc.output.id === record.outputId); assert.equal(docs.length, 1);
        const construction = docs[0].construction;
        equal(construction.steps.map(step => step.id), trace.expectations.find(row => row.actionIndex === ack.actionIndex).stepIds, 'Reload changed saved step identities');
        assert.equal(construction.steps[0].operation.kind, 'GROUP'); assert.equal(construction.steps[0].operation.group.aggregates[0].operation, 'MEAN');
        const beforeDocs = record.before.workspace.documents.filter(doc => doc.output.id === record.outputId); assert.equal(beforeDocs.length, 1);
        assert.equal(beforeDocs[0].construction.steps[0].operation.group.aggregates[0].operation, 'COUNT_ROWS');
        const caps = record.capabilities;
        assert.equal(caps.snapshotToken, identity.snapshotToken); assert.equal(caps.draftVersion, record.after.draftVersion); assert.equal(caps.draftDigest, record.after.draftDigest);
        assert.equal(caps.outputId, record.outputId); assert.equal(caps.stageId, record.stageId); assert.equal(caps.selectedStage.id, record.stageId);
        equal(caps.baseConstruction, construction, 'Reload capabilities describe another saved construction');
    }
    return { cases: [...records.keys()] };
}
export function validateJourneyGo(stdout, phase, fixture) {
    const check = checkApiEvents(stdout, { tests: [journeyTest] });
    if (phase === 'prepare') return check;
    assert.equal(phase, 'full');
    const events = stdout.trim().split('\n').map(JSON.parse), text = events.filter(event => event.Test === journeyTest && event.Action === 'output').map(event => event.Output || '').join('');
    const matches = [...text.matchAll(/BUILDER_OPERATION_REPLAY_JSON\s+(\{[^\n]+\})/g)]; assert.equal(matches.length, 1, 'Missing or duplicate actual Go replay summary');
    const result = JSON.parse(matches[0][1]); assert.equal(result.schemaVersion, 1);
    const count = fixture.cases.length, unique = new Set(fixture.cases.flatMap(row => row.expectations.filter(e => e.proposalStatus === 'READY').map(e => e.requestFingerprint))).size, applyCount = fixture.cases.reduce((n, row) => n + row.actions.filter(action => action.kind === 'applyAck').length, 0);
    for (const key of ['traceCaseCount', 'candidateCaseCount', 'replayedCaseCount', 'proposalCheckpointCount', 'readyProposalCount', 'servicePreviewCount', 'uniqueRequestFingerprintCount', 'uniqueAbstractRequestFingerprintCount', 'uniqueRawRequestFingerprintCount']) assert.equal(result[key], key.startsWith('unique') ? unique : count, `Incomplete actual Go ${key}`);
    for (const key of ['skippedCaseCount', 'needsRepairProposalCount', 'applyAckFailureCount']) assert.equal(result[key], 0, `Unexpected actual Go ${key}`);
    assert.equal(result.applyInvocationCount, applyCount); assert.equal(result.applyAckSuccessCount, applyCount);
    casesById({ schemaVersion: 1, cases: result.cases }, fixture);
    const requestIds = [], fingerprints = [];
    for (const trace of fixture.cases) {
        const observed = result.cases.find(row => row.id === trace.id), expected = trace.expectations.filter(row => row.proposalStatus === 'READY');
        equal(observed.proposalCheckpoints.map(row => row.actionIndex), expected.map(row => row.actionIndex));
        for (const cp of observed.proposalCheckpoints) {
            const want = expected.find(row => row.actionIndex === cp.actionIndex);
            assert.equal(cp.status, 'READY'); assert.equal(cp.proposalIdPresent, true); assert.equal(cp.missingInputCount, 0);
            equal(cp.stepIds, want.stepIds, 'Actual composition differs from retained witness'); equal(cp.groupOutputTypes, { aggregate: want.groupOutputTypes[trace.initial.aggregateOutputId] ?? null, key: Object.entries(want.groupOutputTypes).find(([id]) => id !== trace.initial.aggregateOutputId)?.[1] ?? null });
            equal(cp.removedStepIds ?? [], want.removedStepIds);
            equal(cp.downstreamInputTypes, Object.fromEntries(Object.entries(want.downstreamInputTypes).map(([id, value]) => [id, value.type])));
            equal(cp.downstreamOutputTypes, Object.fromEntries(Object.entries(want.downstreamOutputTypes).map(([id, value]) => [id, value.type])));
            assert.ok(cp.requestId && cp.requestFingerprint); requestIds.push(cp.requestId); fingerprints.push(cp.requestFingerprint);
        }
        const acks = trace.actions.map((row, index) => ({ row, index })).filter(({ row }) => row.kind === 'applyAck');
        equal(observed.applyAck.map(row => row.actionIndex), acks.map(({ index }) => index));
        for (const ack of observed.applyAck) validateAcknowledgement(ack);
    }
    const expectedReloads = result.cases.filter(row => row.applyAck.length);
    const reloadMetadata = casesById({ schemaVersion: 1, cases: result.reloads }, { cases: expectedReloads });
    for (const row of expectedReloads) {
        assert.equal(row.applyAck.length, 1); const ack = row.applyAck[0], reload = reloadMetadata.get(row.id);
        assert.equal(reload.actionIndex, ack.actionIndex); assert.equal(reload.draftVersion, ack.acceptedVersionAfter); assert.equal(reload.draftDigest, ack.acceptedDigestAfter);
    }
    assert.equal(new Set(requestIds).size, count); assert.equal(new Set(fingerprints).size, unique);
    return result;
}
const faultFile = 'internal/explorer/lifecycle/construction_proposal.go';
const faultBefore = '\treturn remove\n}';
const faultAfter = '\tif len(remove) != 0 {\n\t\tfor _, step := range steps {\n\t\t\tif step.Operation.Kind == authoringv2.ConstructionOperationDerive {\n\t\t\t\tremove = append(remove, step.ID)\n\t\t\t}\n\t\t}\n\t}\n\treturn remove\n}';
const queryFile = 'internal/explorer/lifecycle/query.go';
const editorFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx';
const queryBefore = '\t\tif err := s.migrateLegacyConstructionSteps(ctx, owner, workspace); err != nil {';
const queryAfter = '\t\tfor i := range workspace.Documents {\n\t\t\tconstruction := workspace.Documents[i].Construction\n\t\t\tif construction == nil { continue }\n\t\t\tfor j := range construction.Steps {\n\t\t\t\top := &construction.Steps[j].Operation\n\t\t\t\tif op.Kind == authoringv2.ConstructionOperationGroup && op.Group != nil {\n\t\t\t\t\tfor k := range op.Group.Aggregates {\n\t\t\t\t\t\tif op.Group.Aggregates[k].Operation == authoringv2.ConstructionGroupMean { op.Group.Aggregates[k].Operation = authoringv2.ConstructionGroupSum }\n\t\t\t\t\t}\n\t\t\t\t}\n\t\t\t}\n\t\t}\n' + queryBefore;
const editorBefore = ": { operation: aggregate.operation, inputColumnId: inputColumnId ?? '', ...common };";
const editorAfter = ": { operation: aggregate.operation === 'MEAN' ? 'SUM' : aggregate.operation, inputColumnId: inputColumnId ?? '', ...common };";
export const journeySourceProofs = [
    { model: 'Behavior', property: 'Inv_GroupKeyCascadePreservesIndependentCalculate', kind: 'operation-history', branch: 'cascade', edits: [[faultFile, faultBefore, faultAfter]] },
    { model: 'Behavior', property: 'Inv_ReloadRestoresPersistedGroup', kind: 'operation-history', branch: 'builder-reload', edits: [[queryFile, queryBefore, queryAfter]] },
    { model: 'Behavior', property: 'Inv_ReloadRestoresPersistedGroup', kind: 'operation-history', branch: 'editor-reload', edits: [[editorFile, editorBefore, editorAfter]] },
];
export function runJourney({ feature = 'operation-history', output, productionSource } = {}) {
    if (productionSource !== undefined) {
        assert.ok(productionSource && typeof productionSource === 'object' && !Array.isArray(productionSource)); assert.ok(Object.keys(productionSource).length, 'Empty journey source override');
        for (const [file, source] of Object.entries(productionSource)) { assert.ok([faultFile, queryFile, editorFile].includes(file), 'Unknown journey source override'); assert.ok(typeof source === 'string' && source.trim(), 'Empty journey source override'); }
    }
    const doctor = journeyDoctor({ feature }), directory = resolve(output);
    assert.ok(!existsSync(directory) || readdirSync(directory).length === 0, 'Journey output must be fresh'); mkdirSync(directory, { recursive: true });
    const before = fingerprint(), fixture = validateJourneyFixture(fixtureForFeature(feature)), paths = Object.fromEntries(['traces', 'seeds', 'identities', 'candidates', 'responses', 'reloads'].map(name => [name, join(directory, `${name}.json`)]));
    writeFileSync(paths.traces, JSON.stringify(fixture));
    const seals = new Map([[paths.traces, sha(paths.traces)]]), phases = [];
    const verify = () => { assert.equal(fingerprint(), before, 'Journey production inputs changed'); for (const [path, expected] of seals) assert.equal(sha(path), expected, `Journey artifact changed: ${path}`); };
    const seal = name => { assert.ok(existsSync(paths[name]), `Missing journey ${name} artifact`); seals.set(paths[name], sha(paths[name])); };
    let overlay, uiOverrides;
    const clean = { ...process.env, GOFLAGS: '', GOWORK: 'off', GOTOOLCHAIN: 'local', LOOM_MUTATION_TARGET: '', LOOM_MUTATION_SOURCE: '', LOOM_MUTATION_OVERRIDES: '', LOOM_BUILDER_TRACE_OUTPUT: '', LOOM_VITEST_CACHE_DIR: join(directory, 'cache') };
    for (const key of Object.keys(clean)) if (key.startsWith('BUILDER_')) delete clean[key];
    if (doctor.tool.executable === '/workspace/.loom-setup/go/bin/go') { if (!clean.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) clean.GOMODCACHE = '/workspace/.loom-setup/gomod'; if (!clean.GOCACHE && existsSync('/workspace/loom/.gocache')) clean.GOCACHE = '/workspace/loom/.gocache'; }
    if (productionSource !== undefined) {
        const goReplacements = {}, uiReplacements = {};
        for (const [index, [file, source]] of Object.entries(productionSource).entries()) {
            const replacement = join(directory, `source-${index}${file.endsWith('.go') ? '.go' : '.tsx'}`); writeFileSync(replacement, source); seals.set(replacement, sha(replacement));
            (file.endsWith('.go') ? goReplacements : uiReplacements)[join(root, file)] = replacement;
        }
        if (Object.keys(goReplacements).length) { overlay = join(directory, 'overlay.json'); writeFileSync(overlay, JSON.stringify({ Replace: goReplacements })); seals.set(overlay, sha(overlay)); }
        if (Object.keys(uiReplacements).length) { uiOverrides = join(directory, 'ui-overrides.json'); writeFileSync(uiOverrides, JSON.stringify(uiReplacements)); seals.set(uiOverrides, sha(uiOverrides)); }
    }
    const vars = { BUILDER_OPERATION_TRACES: paths.traces, BUILDER_OPERATION_SEEDS: paths.seeds, BUILDER_OPERATION_SEED_IDENTITIES: paths.identities, BUILDER_OPERATION_CANDIDATES: paths.candidates, BUILDER_OPERATION_RESPONSES: paths.responses, BUILDER_OPERATION_RELOADS: paths.reloads };
    const evidence = { feature, boundary: doctor.boundary, sourceFingerprint: before, fixtureSha256: sha(paths.traces), phases, status: 'running' };
    const run = (phase, executable, args, cwd, extra) => {
        verify(); const result = spawnSync(executable, args, { cwd, env: { ...clean, ...extra }, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 16 * 1024 * 1024, detached: true });
        if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
        writeFileSync(join(directory, `${phase}.stdout`), result.stdout || ''); writeFileSync(join(directory, `${phase}.process.json`), JSON.stringify({ exit: result.status, error: result.error?.message, stderr: result.stderr }, null, 2)); verify();
        assert.ifError(result.error);
        if (result.status !== 0) { const error = new Error(`Journey ${phase} failed; see ${directory}`); Object.assign(error, { phase, exit: result.status, stdout: result.stdout, stderr: result.stderr }); throw error; }
        return result.stdout;
    };
    const ui = (phase, extra) => { const report = join(directory, `${phase}.json`); run(phase, process.execPath, [join(root, 'ui/node_modules/vitest/vitest.mjs'), 'run', journeySuite, '--maxWorkers=1', '--testNamePattern', patterns[phase], '--reporter=json', `--outputFile=${report}`], join(root, 'ui/packages/loom-ui'), { ...extra, ...(uiOverrides ? { LOOM_MUTATION_OVERRIDES: uiOverrides } : {}) }); phases.push({ phase, ...validateJourneyUI(json(report), phase, fixture) }); };
    const go = (phase, mode) => { const stdout = run(phase, doctor.tool.executable, ['test', ...(overlay ? ['-overlay', overlay] : []), '-json', '-count=1', `-timeout=${goTestTimeout}`, '-run', `^${journeyTest}$`, './internal/explorer/lifecycle'], root, { ...vars, BUILDER_OPERATION_PHASE: mode }); const result = validateJourneyGo(stdout, mode, fixture); phases.push({ phase, result }); return result; };
    try {
        ui('ui-prepare', { BUILDER_OPERATION_PREPARE: '1', BUILDER_OPERATION_TRACES: paths.traces, BUILDER_OPERATION_SEEDS: paths.seeds }); seal('seeds'); validateJourneyArtifacts({ fixture, seeds: json(paths.seeds) });
        go('go-prepare', 'prepare'); seal('identities'); validateJourneyArtifacts({ fixture, seeds: json(paths.seeds), identities: json(paths.identities) });
        ui('ui-capture', { BUILDER_OPERATION_TRACES: paths.traces, BUILDER_OPERATION_SEED_IDENTITIES: paths.identities, BUILDER_OPERATION_CANDIDATES: paths.candidates }); seal('candidates'); validateJourneyArtifacts({ fixture, seeds: json(paths.seeds), identities: json(paths.identities), candidates: json(paths.candidates) });
        evidence.actualGo = go('go-full', 'full'); seal('responses'); seal('reloads'); validateJourneyArtifacts({ fixture, seeds: json(paths.seeds), identities: json(paths.identities), candidates: json(paths.candidates), responses: json(paths.responses), actualGo: evidence.actualGo });
        evidence.reload = validateJourneyReloads({ fixture, identities: json(paths.identities), actualGo: evidence.actualGo, reloads: json(paths.reloads) });
        ui('ui-panel', { BUILDER_OPERATION_PANEL_CHECK: '1', BUILDER_OPERATION_TRACES: paths.traces, BUILDER_OPERATION_CANDIDATES: paths.candidates, BUILDER_OPERATION_PROPOSAL_RESPONSES: paths.responses });
        const remountPath = join(directory, 'remount.json');
        writeFileSync(remountPath, JSON.stringify({ schemaVersion: 1, cases: json(paths.reloads).cases.filter(row => row.id === journeyIds[0]) })); seals.set(remountPath, sha(remountPath));
        ui('ui-reload', { BUILDER_OPERATION_RELOADS: remountPath }); verify(); evidence.status = 'passed'; return evidence;
    } finally { if (evidence.status !== 'passed') evidence.status = 'failed'; writeFileSync(join(directory, 'journey.json'), JSON.stringify(evidence, null, 2)); }
}
export function validateJourneyFailure(error, proof = journeySourceProofs[0], report) {
    assert.equal(error.exit, 1);
    if (proof.branch === 'editor-reload' && error.phase === 'ui-capture') {
        assert.ok(report && report.success === false);
        assert.equal(report.numFailedTests, 2); assert.equal(report.numPassedTests, 11);
        assert.equal(report.testResults.length, 1); assert.equal(resolve(report.testResults[0].name), join(root, 'ui/packages/loom-ui', journeySuite));
        assert.ok(report.testResults[0].assertionResults.every(row => ['passed', 'failed', 'pending', 'skipped'].includes(row.status)));
        const failed = report.testResults.flatMap(row => row.assertionResults).filter(row => row.status === 'failed');
        const ids = ['operation-history-request-a1fb7229a61e'];
        equal(failed.map(row => row.fullName).sort(), [...ids.map(id => `${prefix}replays each operation schedule through ConstructionReshapeEditor: '${id}'`), `${prefix}accounts for every exercised schedule and proposal checkpoint`].sort());
        for (const row of failed) {
            assert.equal(row.failureMessages.length, 1);
            if (row.fullName.endsWith('proposal checkpoint')) assert.match(row.failureMessages[0], /^AssertionError: expected 10 to be 11/);
            else assert.match(row.failureMessages[0], /^AssertionError: operation-history-request-.*expected 'SUM' to be 'MEAN'/);
        }
        const diagnostics = JSON.stringify(report) + (error.stderr || '');
        assert.doesNotMatch(diagnostics, /unhandled (?:errors?|rejection|exception)|Vitest caught|Transform failed|Failed to (?:load|resolve)|Cannot find module|SyntaxError|TypeError|ReferenceError|timed out|test timeout|fatal error|panic:/i);
        return true;
    }
    if (proof.branch === 'editor-reload') {
        assert.equal(error.phase, 'ui-reload'); assert.ok(report && report.success === false);
        assert.equal(report.numFailedTests, 1); assert.equal(report.numPassedTests, 0);
        assert.equal(report.testResults.length, 1); assert.equal(resolve(report.testResults[0].name), join(root, 'ui/packages/loom-ui', journeySuite));
        const selected = report.testResults[0].assertionResults.filter(row => !['pending', 'skipped'].includes(row.status));
        assert.equal(selected.length, 1); assert.equal(selected[0].status, 'failed'); assert.equal(selected[0].fullName, `${prefix}keeps the saved Group operation and identifiers across remounts`);
        assert.ok(selected[0].failureMessages.length === 1 && /^AssertionError: expected 'SUM' to be 'MEAN'/.test(selected[0].failureMessages[0]));
        const diagnostics = [error.stdout || '', error.stderr || '', report.testResults[0].message || '', ...selected[0].failureMessages].join('\n');
        assert.doesNotMatch(diagnostics, /unhandled (?:errors?|rejection|exception)|Vitest caught|Transform failed|Failed to (?:load|resolve)|Cannot find module|SyntaxError|TypeError|ReferenceError|timed out|test timeout|fatal error|panic:/i); return true;
    }
    assert.equal(error.phase, 'go-full');
    const events = error.stdout.trim().split('\n').filter(Boolean).map(JSON.parse);
    assert.ok(events.some(event => event.Test === journeyTest && event.Action === 'run'));
    assert.ok(events.some(event => event.Test === journeyTest && event.Action === 'fail'));
    assert.ok(events.some(event => !event.Test && event.Action === 'fail'));
    assert.ok(!events.some(event => event.Action === 'skip'));
    const text = events.filter(event => event.Action === 'output').map(event => event.Output || '').join('');
    assert.doesNotMatch(text + '\n' + (error.stderr || ''), /panic:|fatal error:|test timed out|test timeout/i);
    if (proof.branch === 'builder-reload') assert.match(text, /fresh Builder lost saved Group:/);
    else { assert.equal(proof.branch, 'cascade'); assert.match(text, /trace operation-history-group-key-cascade-count-to-mean-(?:ready-preserves-calculate|apply-ack) action 2 steps \[group-step\] != model \[group-step calculate-from-aggregate\]/); }
    return true;
}
export function checkJourneySourceProof(proof, _graph, output, { feature = 'operation-history' } = {}) {
    equal(proof, journeySourceProofs.find(row => row.branch === proof.branch), 'Unknown journey source proof'); mkdirSync(output, { recursive: true });
    const productionSource = {};
    for (const [file, before, after] of proof.edits) { const original = productionSource[file] ?? readFileSync(join(root, file), 'utf8'); assert.equal(original.split(before).length, 2, 'Nonunique journey fault anchor'); productionSource[file] = original.replace(before, after); }
    runJourney({ feature, output: join(output, 'positive') });
    assert.throws(() => runJourney({ feature, output: join(output, 'negative'), productionSource }), error => validateJourneyFailure(error, proof, proof.branch === 'editor-reload' && ['ui-capture', 'ui-reload'].includes(error.phase) ? json(join(output, 'negative', `${error.phase}.json`)) : undefined));
    runJourney({ feature, output: join(output, 'restored') });
    const evidence = { model: proof.model, property: proof.property, kind: proof.kind, feature, branch: proof.branch, sourceFiles: Object.keys(productionSource), positive: true, negative: true, restored: true, boundary: 'actual editor/service/read/remount fixed-witness pipeline; not a TLA claim' };
    writeFileSync(join(output, 'proof.json'), JSON.stringify(evidence, null, 2)); return evidence;
}
