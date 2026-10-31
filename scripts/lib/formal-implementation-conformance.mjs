import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
const hookSuite = 'src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx';
const workspaceSuite = 'src/features/ExplorerBuilder/BuilderWorkspace.reconciliation.unit.test.tsx';
export function sourceFingerprint(root) {
    const hash = createHash('sha256');
    function visit(path) {
        for (const entry of readdirSync(join(root, path), { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
            const relative = `${path}/${entry.name}`;
            if (entry.isDirectory())
                visit(relative);
            else if (entry.isFile())
                hash.update(relative).update('\0').update(readFileSync(join(root, relative))).update('\0');
        }
    }
    visit('ui/packages/loom-ui/src');
    visit('ui/packages/loom-ui/testSupport');
    for (const file of ['ui/package-lock.json', 'ui/packages/loom-ui/vitest.config.ts', 'ui/packages/loom-ui/vitest.setup.ts',
        'scripts/lib/formal-implementation-conformance.mjs', 'verification/builder-interactions/ImplementationConformance.tla',
        'verification/builder-interactions/ImplementationConformance.cfg'])
        hash.update(file).update(readFileSync(join(root, file)));
    return hash.digest('hex');
}
function runTests(root, scratch, suite, pattern, env = {}) {
    const report = join(scratch, 'vitest.json');
    const vitest = join(root, 'ui/node_modules/vitest/vitest.mjs');
    assert.ok(existsSync(vitest), 'Install UI dependencies with npm ci in ui before checking implementation conformance');
    const args = [vitest, 'run', suite, '--reporter=json', `--outputFile=${report}`];
    if (pattern)
        args.push('--testNamePattern', pattern);
    const result = spawnSync(process.execPath, args, {
        cwd: join(root, 'ui/packages/loom-ui'), encoding: 'utf8', timeout: 30000,
        maxBuffer: 8 * 1024 * 1024,
        env: { ...process.env, LOOM_VITEST_CACHE_DIR: join(scratch, 'vite-cache'), ...env },
    });
    if (result.error)
        throw result.error;
    assert.ok(existsSync(report), `Vitest produced no report: ${result.stderr}`);
    const parsed = JSON.parse(readFileSync(report, 'utf8'));
    assert.ok(parsed.numPassedTests + parsed.numFailedTests > 0, 'Implementation suite selected no tests');
    return { result, parsed };
}
const text = (value) => value ?? '';
function observationRecord(observation, action, traceName, actorReceipt = '') {
    const common = {
        actorAttempt: '', activeApplyAttempt: '', actorContext: '', currentContext: '',
        completionObserved: false, completionAccepted: false, actorReceipt, name: traceName, event: action?.event ?? 'initial',
        staleEvent: false,
        status: observation.status,
        kind: 'reconcile' in observation ? 'reconcile' : 'lifecycle',
        canApply: false, proposalId: '', proposalOutput: '', proposalSnapshot: '', proposalVersion: 0,
        proposalDigest: '', previewReceipt: '', previewOutput: '', rowsPresent: false,
        currentOutput: '', currentSnapshot: '', currentVersion: 0, currentDigest: '', currentWorkspace: '',
        receiptId: '', receiptWorkspace: '', receiptSnapshot: '', receiptOutput: '', receiptOutputs: [],
    };
    assert.equal(typeof common.status, 'string', 'Missing observed status');
    assert.ok((common.kind === 'lifecycle'
        ? ['idle', 'previewing', 'ready', 'applying', 'needs-repair', 'error']
        : ['empty', 'stale', 'previewing', 'needs-repair', 'error', 'ready']).includes(common.status), 'Unknown observed status');
    if ('reconcile' in observation) {
        const { current, receipt } = observation.reconcile;
        assert.ok(current?.modelKey && current?.snapshotToken && current?.outputId, 'Incomplete mounted workspace observation');
        if (receipt) {
            assert.ok(receipt.snapshotToken && Array.isArray(receipt.outputIds), 'Missing raw receipt fields');
            for (const field of ['receiptId', 'workspaceKey', 'snapshotToken', 'outputId'])
                assert.equal(typeof receipt[field], 'string', `Malformed receipt ${field}`);
            assert.ok(receipt.outputIds.every((id) => typeof id === 'string'), 'Malformed receipt outputs');
        }
        return { ...common, currentOutput: current.outputId, currentSnapshot: current.snapshotToken,
            currentWorkspace: current.modelKey, currentVersion: current.draftVersion, currentDigest: current.draftDigest,
            receiptId: text(receipt?.receiptId), receiptWorkspace: text(receipt?.workspaceKey),
            receiptSnapshot: text(receipt?.snapshotToken), receiptOutput: text(receipt?.outputId), receiptOutputs: receipt?.outputIds ?? [] };
    }
    const request = observation.workspace;
    assert.ok(request?.outputId && request?.snapshotToken && request?.expectedDraftDigest, 'Missing hook input identity');
    for (const field of ['proposalId', 'proposalOutputId', 'proposalSnapshotToken', 'proposalDraftDigest', 'previewReceiptId', 'previewOutputId']) {
        assert.ok(Object.hasOwn(observation, field), `Missing observation field: ${field}`);
        assert.ok(observation[field] === null || typeof observation[field] === 'string', `Malformed observation field: ${field}`);
    }
    assert.ok(observation.proposalDraftVersion === null || (Number.isSafeInteger(observation.proposalDraftVersion) && observation.proposalDraftVersion >= 0), 'Malformed proposal version');
    for (const field of ['project', 'explorerId', 'snapshotToken', 'expectedDraftDigest', 'outputId', 'stageId'])
        assert.equal(typeof request[field], 'string', `Malformed input identity: ${field}`);
    assert.ok(Number.isSafeInteger(request.expectedDraftVersion) && request.expectedDraftVersion >= 0, 'Malformed input draft version');
    assert.equal(typeof observation.canApply, 'boolean');
    assert.equal(typeof observation.rowsPresent, 'boolean');
    return { ...common, currentContext: JSON.stringify([request.project, request.authResourcePath?.trim() ?? '', request.explorerId, request.snapshotToken, request.expectedDraftVersion, request.expectedDraftDigest, request.outputId, request.stageId, request.groupStepId ?? '']),
        canApply: observation.canApply,
        proposalId: text(observation.proposalId), proposalOutput: text(observation.proposalOutputId),
        proposalSnapshot: text(observation.proposalSnapshotToken), proposalVersion: observation.proposalDraftVersion ?? 0,
        proposalDigest: text(observation.proposalDraftDigest), previewReceipt: text(observation.previewReceiptId),
        previewOutput: text(observation.previewOutputId), rowsPresent: observation.rowsPresent,
        currentOutput: request.outputId, currentSnapshot: request.snapshotToken,
        currentVersion: request.expectedDraftVersion, currentDigest: request.expectedDraftDigest };
}
export function tlaValue(value) {
    if (typeof value === 'string')
        return JSON.stringify(value);
    if (typeof value === 'boolean')
        return value ? 'TRUE' : 'FALSE';
    if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 0)
        return String(value);
    if (Array.isArray(value))
        return `<<${value.map(tlaValue).join(', ')}>>`;
    if (value && typeof value === 'object')
        return `[${Object.entries(value).map(([k, v]) => `${k} |-> ${tlaValue(v)}`).join(', ')}]`;
    throw new Error('Unsupported observation value');
}
export function prepareTraces(header, expected) {
    for (const [field, value] of Object.entries(expected))
        assert.equal(header[field], value, `Trace ${field} drift`);
    assert.equal(header.schemaVersion, 1);
    assert.ok(Array.isArray(header.traces) && header.traces.length > 0, 'No implementation traces');
    const model = header.traces[0].model;
    return header.traces.map((trace) => {
        assert.ok(trace && typeof trace.name === 'string' && trace.name && trace.initialObservation && Array.isArray(trace.actions) && trace.actions.length && Array.isArray(trace.unmodeledEvents), 'Malformed/empty trace');
        assert.equal(trace.model, model, 'Mixed trace model families');
        for (const observation of [trace.initialObservation, ...trace.actions.map((action) => action.observation)])
            assert.equal('reconcile' in observation, model === 'ReconcileLifecycle', 'Observation/model family mismatch');
        assert.ok(['BuilderLifecycle', 'ReconcileLifecycle'].includes(trace.model), `Unsupported trace model: ${trace.model}`);
        const events = trace.model === 'BuilderLifecycle'
            ? new Set(['pivot-mounted', 'construction-discovery', 'propose-construction', 'proposal-response', 'preview', 'preview-response', 'begin-apply', 'cancel', 'identity-change', 'retry', 'finish-apply'])
            : new Set(['workspaceChangeB', 'successB', 'lateA']);
        for (const action of trace.actions) {
            assert.ok(action && action.observation && events.has(action.event), `Unsupported/malformed observed event: ${action?.event}`);
            if (model === 'BuilderLifecycle' && (['propose-construction', 'retry', 'proposal-response', 'preview', 'preview-response', 'finish-apply'].includes(action.event) || (action.event === 'begin-apply' && action.observation.status === 'applying')))
                assert.ok(action.owner && ['P1', 'P2'].includes(action.owner.attempt) && ['proposal', 'preview', 'apply'].includes(action.owner.kind), `Missing/unknown event actor: ${action.event}`);
            if (Object.hasOwn(action, 'completionAccepted'))
                assert.equal(typeof action.completionAccepted, 'boolean', 'Malformed completion result');
        }
        assert.equal(trace.expectedFailure, false, 'Expected failures cannot count as conformance');
        assert.equal(trace.unmodeledEvents.length, 0, 'Unmodeled events cannot count as conformance');
        assert.ok(trace.initialObservation && trace.actions.length > 0, `Empty trace: ${trace.name}`);
        const applyReceipts = new Map();
        const applyContexts = new Map();
        let activeApplyAttempt = '';
        let activeProposalAttempt = '';
        let previousStatus = trace.initialObservation.status;
        const proposalContexts = new Map();
        return [observationRecord(trace.initialObservation, null, trace.name),
            ...trace.actions.map((action) => {
                const record = observationRecord(action.observation, action, trace.name, applyReceipts.get(action.owner?.attempt) ?? '');
                if (['propose-construction', 'retry'].includes(action.event) && action.owner) {
                    activeProposalAttempt = action.owner.attempt;
                    proposalContexts.set(action.owner.attempt, record.currentContext);
                }
                else if (action.event === 'cancel')
                    activeProposalAttempt = '';
                record.staleEvent = ['proposal-response', 'preview-response'].includes(action.event) && Boolean(action.owner) &&
                    (action.owner.attempt !== activeProposalAttempt || proposalContexts.get(action.owner.attempt) !== record.currentContext);
                if (action.observation.status !== 'applying')
                    activeApplyAttempt = '';
                else if (action.event === 'begin-apply' && previousStatus === 'ready' && action.owner) {
                    activeApplyAttempt = action.owner.attempt;
                    applyReceipts.set(action.owner.attempt, action.observation.proposalId);
                    record.actorReceipt = action.observation.proposalId;
                    applyContexts.set(action.owner.attempt, record.currentContext);
                }
                previousStatus = action.observation.status;
                return { ...record, actorAttempt: action.owner?.attempt ?? '', activeApplyAttempt,
                    actorContext: applyContexts.get(action.owner?.attempt) ?? '',
                    completionObserved: typeof action.completionAccepted === 'boolean',
                    completionAccepted: action.completionAccepted ?? false };
            })];
    });
}
function verifyTrace(root, scratch, header, expected, checkModel, outputDirectory) {
    const traces = prepareTraces(header, expected);
    if (traces[0][0].kind === 'lifecycle') {
        const completions = traces.flat().filter((record) => record.completionObserved);
        assert.ok(completions.some((record) => record.completionAccepted), 'No accepted completion: context rule would be vacuous');
        assert.ok(completions.some((record) => !record.completionAccepted), 'No rejected completion: stale-caller branch was not exercised');
        assert.ok(traces.flat().some((record) => record.staleEvent), 'No stale response: ignore rule would be vacuous');
    }
    const scopedConfig = readFileSync(join(root, 'verification/builder-interactions/ImplementationConformance.cfg'), 'utf8')
        .split('\n').filter((line) => !line.startsWith('INVARIANT ') ||
        (traces[0][0].kind === 'lifecycle' ? !line.includes('Inv_RenderedReconcileReceiptIsCurrent') : line.includes('Inv_RenderedReconcileReceiptIsCurrent'))).join('\n');
    const directory = join(scratch, 'tla');
    mkdirSync(directory);
    const model = readFileSync(join(root, 'verification/builder-interactions/ImplementationConformance.tla'), 'utf8');
    writeFileSync(join(directory, 'ImplementationConformance.tla'), model);
    writeFileSync(join(directory, 'ImplementationConformance.cfg'), scopedConfig + '\nCONSTANT Traces <- Observations\n');
    const withData = (observations) => model.replace('\n=============================================================================', `\nObservations == ${tlaValue(observations)}\n=============================================================================`);
    writeFileSync(join(directory, 'ImplementationConformance.tla'), withData(traces));
    console.log(`Mounted implementation: ${traces.length} schedules, ${traces.reduce((n, trace) => n + trace.length, 0)} observations checked by TLC`);
    if (outputDirectory) {
        mkdirSync(outputDirectory);
        for (const file of ['ImplementationConformance.tla', 'ImplementationConformance.cfg'])
            copyFileSync(join(directory, file), join(outputDirectory, file));
        writeFileSync(join(outputDirectory, 'trace.json'), `${JSON.stringify(header)}\n`);
        copyFileSync(join(scratch, 'vitest.json'), join(outputDirectory, 'vitest.json'));
    }
    checkModel('ImplementationConformance', directory);
    // A passing full run above pstack's graph budget still gets a clearly scoped
    // reader diagram. Select actual traces covering distinct observed edge kinds.
    const edgeKinds = (trace) => trace.slice(1).map((o, i) => JSON.stringify([trace[i].status, o.event, o.status, o.staleEvent, o.completionObserved, o.completionAccepted]));
    const uncovered = new Set(traces.flatMap(edgeKinds));
    const selected = [];
    const candidates = [...traces];
    while (uncovered.size) {
        candidates.sort((a, b) => edgeKinds(b).filter((key) => uncovered.has(key)).length - edgeKinds(a).filter((key) => uncovered.has(key)).length || a.length - b.length);
        const best = candidates.shift();
        assert.ok(best, 'Representative diagram did not cover observed edge kinds');
        selected.push(best);
        for (const key of edgeKinds(best))
            uncovered.delete(key);
    }
    const sampleDirectory = join(scratch, 'sample');
    mkdirSync(sampleDirectory);
    writeFileSync(join(sampleDirectory, 'ImplementationConformance.tla'), withData(selected));
    writeFileSync(join(sampleDirectory, 'ImplementationConformance.cfg'), scopedConfig + '\nCONSTANT Traces <- Observations\n');
    const sampleResult = checkModel('ImplementationConformance', sampleDirectory);
    assert.ok(sampleResult.chart, 'Representative trace diagram exceeded the pstack chart budget');
    if (outputDirectory)
        writeFileSync(join(outputDirectory, 'diagram-scope.json'), JSON.stringify({ selection: 'greedy coverage of observed edge kinds; not all model schedules', selected: selected.map((trace) => trace[0].name), allTraces: traces.length, selectedStates: selected.reduce((sum, trace) => sum + trace.length, 0) }, null, 2));
    // Controls for the bridge itself: a corrupted real observation must fail its exact rule.
    const isHook = traces[0][0].kind === 'lifecycle';
    const corrupt = structuredClone(traces);
    const record = corrupt.flat().find((o) => isHook ? o.canApply : o.receiptId !== '');
    assert.ok(record, 'Missing accepted implementation outcome');
    if (isHook) {
        record.canApply = true;
        record.previewReceipt = 'wrong-receipt';
    }
    else
        record.receiptSnapshot = 'wrong-snapshot';
    writeFileSync(join(directory, 'ImplementationConformance.tla'), withData(corrupt));
    // Isolate the rule: another valid failure must not mask the intended one.
    const invariant = isHook ? 'Inv_VisibleReadyIsReviewed' : 'Inv_RenderedReconcileReceiptIsCurrent';
    writeFileSync(join(directory, 'ImplementationConformance.cfg'), `SPECIFICATION Spec\nCHECK_DEADLOCK FALSE\nCONSTANT Traces <- Observations\nINVARIANT ${invariant}\n`);
    checkModel('ImplementationConformance', directory, invariant);
    writeFileSync(join(directory, 'ImplementationConformance.tla'), withData(traces));
    writeFileSync(join(directory, 'ImplementationConformance.cfg'), scopedConfig + '\nCONSTANT Traces <- Observations\n');
    checkModel('ImplementationConformance', directory);
}
export function checkMountedImplementation(root, name, checkModel, outputRoot) {
    const outputDirectory = outputRoot && join(outputRoot, name);
    if (outputRoot)
        mkdirSync(outputRoot, { recursive: true });
    if (outputDirectory)
        assert.equal(existsSync(outputDirectory), false, `Refusing to overwrite evidence: ${outputDirectory}`);
    const scratch = mkdtempSync(join(tmpdir(), 'loom-implementation-'));
    try {
        const fingerprint = sourceFingerprint(root);
        const commitResult = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' });
        assert.equal(commitResult.status, 0);
        const commit = commitResult.stdout.trim();
        const tracePath = join(scratch, 'trace.json');
        const expected = { sourceProfile: 'formal', sourceCommit: commit, contractSourceCommit: commit, sourceFingerprint: fingerprint };
        const env = {
            LOOM_BUILDER_TRACE_OUTPUT: tracePath, LOOM_TRACE_SOURCE_PROFILE: 'formal',
            LOOM_SOURCE_COMMIT: commit, LOOM_CONTRACT_SOURCE_COMMIT: commit, LOOM_SOURCE_FINGERPRINT: fingerprint,
        };
        const isHook = name === 'BuilderLifecycle';
        const suite = isHook ? hookSuite : workspaceSuite;
        const { result, parsed } = runTests(root, scratch, suite, isHook ? undefined : 'formal mounted reconcile', env);
        assert.equal(sourceFingerprint(root), fingerprint, 'Source changed during implementation run');
        const header = JSON.parse(readFileSync(tracePath, 'utf8'));
        assert.ok(isHook ? header.traces.length >= 60 : header.traces.length === 1, 'Implementation trace coverage unexpectedly shrank');
        if (isHook) {
            for (const fragment of ['A→B→A', 'old Apply success', 'old Apply failure', 'wrong receipt', 'cancellation'])
                assert.ok(header.traces.some((t) => t.name.includes(fragment)), `Missing schedule family: ${fragment}`);
        }
        console.log(`Real ${isHook ? 'hook' : 'workspace'}: ${parsed.numPassedTests} passed, ${parsed.numFailedTests} failed; fingerprint ${fingerprint.slice(0, 12)}`);
        for (const [label, corrupt] of [
            ['stale fingerprint', { ...header, sourceFingerprint: 'stale' }],
            ['empty traces', { ...header, traces: [] }],
            ['unmodeled event', { ...header, traces: [{ ...header.traces[0], unmodeledEvents: [{ kind: 'unknown' }] }] }],
        ]) {
            assert.throws(() => prepareTraces(corrupt, expected), { code: 'ERR_ASSERTION' }, label);
        }
        console.log('Trace integrity controls: stale fingerprint, empty evidence, and unsupported events rejected');
        verifyTrace(root, scratch, header, expected, checkModel, outputDirectory);
        assert.equal(result.status, 0, JSON.stringify(parsed.testResults.flatMap((file) => file.assertionResults ?? []).filter((test) => test.status === 'failed')));
        assert.equal(parsed.success, true);
        assert.equal(sourceFingerprint(root), fingerprint, 'Source changed during conformance verification');
    }
    finally {
        rmSync(scratch, { recursive: true, force: true });
    }
}
