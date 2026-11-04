import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { root } from './formal-tools.mjs';
import { workspaceFile, extractReconcileCallback, loadRecoveryImplementation, replayRecoverySchedule, replayRecoveryWitness, validateRecoveryTransition, recoveryCounterexampleWitness, recoverySourceProofs, recoveryProperty } from './formal-recovery-replay.mjs';

const initial = () => ({ refreshPhase: 'idle', refreshKind: 'stop', hasData: false, currentOwner: 'A', currentFingerprint: 'F0', currentGeneration: 0, workspaceAttached: true, cancelled: false, synchronized: false, retried: false });
function schedule(kind, action, hasData = kind !== 'backoff') {
    const state = { ...initial(), refreshPhase: 'waiting', refreshKind: kind, hasData };
    const rows = [{ action: 'AwaitRecovery', to: { ...state } }];
    if (action) {
        if (action === 'ChangeExplorer') state.currentOwner = 'B';
        else if (action === 'EditWorkspace') state.currentFingerprint = 'F1';
        else if (action === 'StartNewCompile') state.currentGeneration = 1;
        else if (action === 'CancelOwner') state.cancelled = true;
        else if (action === 'DetachWorkspace') state.workspaceAttached = false;
        rows.push({ action, to: { ...state } });
    }
    const admitted = !action && (kind === 'backoff' || hasData);
    rows.push({ action: 'ResumeRecovery', to: { ...state, refreshPhase: 'done', synchronized: admitted && kind !== 'backoff', retried: admitted && kind !== 'stop' } });
    return rows;
}

test('extracts the production async callback and rejects changed boundaries/dependencies', () => {
    const text = readFileSync(join(root, workspaceFile), 'utf8');
    assert.match(extractReconcileCallback(text), /^async/);
    assert.throws(() => extractReconcileCallback(text + '\n  const reconcileCurrent = useCallback('), /Expected one/);
    assert.throws(() => extractReconcileCallback(text.replace('  const reconcileCurrent = useCallback(', '  const renamed = useCallback(')), /Expected one/);
    assert.throws(() => extractReconcileCallback(text.replace('    syncBuilderData,\n  ]);\n  const executePreview', '    unreviewedDependency,\n  ]);\n  const executePreview')), /dependencies changed/);
});

test('actual callback preserves all three valid continuation branches and missing-data outcomes', async () => {
    const implementation = await loadRecoveryImplementation();
    for (const kind of ['stop', 'retry', 'backoff']) for (const hasData of kind === 'backoff' ? [false] : [false, true])
        for (const automatic of [false, true]) for (const rename of [false, true]) {
            const result = await replayRecoverySchedule(schedule(kind, undefined, hasData), { implementation, automatic, rename });
            assert.equal(result.status, 'property-held');
            assert.equal(result.returnedReceipt, kind !== 'stop' && (kind === 'backoff' || hasData));
        }
});

test('actual callback ignores each independent ownership change and automatic cancellation', async () => {
    const implementation = await loadRecoveryImplementation();
    for (const kind of ['stop', 'retry', 'backoff']) for (const action of ['ChangeExplorer', 'EditWorkspace', 'StartNewCompile', 'CancelOwner', 'DetachWorkspace'])
        for (const rename of [false, true]) {
            const result = await replayRecoverySchedule(schedule(kind, action), { implementation, automatic: true, rename });
            assert.deepEqual(result.actual, { synchronized: false, retried: false });
        }
    for (const kind of ['stop', 'retry', 'backoff']) for (const rename of [false, true]) {
        const result = await replayRecoverySchedule(schedule(kind, 'DetachWorkspace'), { implementation, rename });
        assert.equal(result.status, 'property-held');
        assert.deepEqual(result.actual, { synchronized: false, retried: false });
    }
    assert.equal((await replayRecoverySchedule(schedule('stop', 'CancelOwner'), { implementation })).status, 'outside-caller-boundary');
});

test('original guard removals fail through real asynchronous callback effects and replay witnesses', async () => {
    const original = readFileSync(join(root, workspaceFile), 'utf8');
    for (const control of recoverySourceProofs) {
        const [, before, after] = control.edits[0];
        const broken = original.replace(before, after);
        const kind = control.branch === 'refresh-and-stop' ? 'stop' : control.branch === 'refresh-and-retry' ? 'retry' : 'backoff';
        let witness;
        await assert.rejects(() => replayRecoverySchedule(schedule(kind, control.branch === 'detached-workspace' ? 'DetachWorkspace' : 'StartNewCompile'), { workspaceSource: broken }), error => {
            witness = error.witness;
            return error.code === 'FORMAL_PROPERTY' && error.property === recoveryProperty;
        });
        assert.equal((await replayRecoveryWitness(witness)).status, 'property-held');
        assert.equal((await replayRecoveryWitness(witness, { workspaceSource: broken })).status, 'reproduced');
        await assert.rejects(() => replayRecoveryWitness({ ...witness, expected: { synchronized: true, retried: true } }), /changed the independent expected/);
    }
});

test('rejects fictional action/state paths and converts actual TLC counterexample shape', async () => {
    const path = schedule('stop', 'ChangeExplorer');
    const brokenEnd = structuredClone(path.at(-1).to); brokenEnd.synchronized = true;
    const witness = recoveryCounterexampleWitness({ property: recoveryProperty, states: [{ action: 'Initial', vars: initial() }, ...path.map(step => ({ action: step.action, vars: step.action === 'ResumeRecovery' ? brokenEnd : step.to }))] });
    assert.equal((await replayRecoveryWitness(witness)).status, 'property-held');
    assert.throws(() => validateRecoveryTransition(path[0].to, 'StartNewCompile', path[1].to), /does not match/);
    const fictional = structuredClone(path); fictional[1].action = 'UnknownAction';
    await assert.rejects(() => replayRecoverySchedule(fictional), /Unknown recovery action/);
    const jumped = structuredClone(path); jumped[1].to.currentGeneration = 2;
    await assert.rejects(() => replayRecoverySchedule(jumped), /does not match/);
    assert.throws(() => recoveryCounterexampleWitness({ property: 'Unknown', states: [] }), /Unknown recovery/);
    await assert.rejects(() => replayRecoveryWitness({ ...witness, automatic: 'true' }), /boolean/);
});

test('detachment blocks initial entry, response admission and recovery startup in the actual callback', async () => {
    const original = readFileSync(join(root, workspaceFile), 'utf8');
    async function observe(workspaceSource, boundary) {
        const factory = await loadRecoveryImplementation({ workspaceSource });
        const workspaceAttached = { current: boundary !== 'entry' }, compileGeneration = { current: 0 }, activeCompile = { current: undefined };
        const latestState = { current: { explorerId: 'A', catalog: { snapshotToken: 'snapshot-1' }, draftVersion: 1, draftDigest: 'draft-1', workspace: { explorer: { title: 'F0' } }, tables: [{ outputId: 'patients', tabId: 'patients-tab', title: 'Patients', document: { rootResourceType: 'Patient', output: { id: 'patients', title: 'Patients' }, columns: [] } }] } };
        let resolve, reject;
        const pending = new Promise((yes, no) => { resolve = yes; reject = no; });
        const requests = [], events = [], refreshes = [];
        const callback = factory({ latestState, workspaceAttached, compileGeneration, activeCompile, projectId: 'project-fixture', authResourcePath: '/fixture',
            dispatch: event => events.push(event), setMessage: message => events.push(message), diagnosticsFromError: error => [{ code: error.code }],
            reconcileBuilder: request => { requests.push(request); return { unwrap: () => pending }; },
            refetchBuilder: async request => { refreshes.push(request); return {}; }, syncBuilderData: () => events.push('sync'),
            window: { setTimeout: () => assert.fail('Detached initial response started backoff') },
        });
        const running = callback();
        if (boundary !== 'entry') {
            assert.equal(requests.length, 1);
            events.length = 0;
            workspaceAttached.current = false;
            if (boundary === 'success') resolve({ snapshotToken: 'snapshot-1', outputs: [{ outputId: 'patients' }], diagnostics: [] });
            else reject({ code: 'STALE_CATALOG_SNAPSHOT' });
        }
        assert.equal(await running, undefined);
        assert.deepEqual(events, []);
        assert.deepEqual(refreshes, []);
        if (boundary === 'entry') { assert.deepEqual(requests, []); assert.equal(compileGeneration.current, 0); }
        assert.equal(activeCompile.current, undefined);
    }
    const faults = [
        ['entry', '    if (!workspaceAttached.current) return undefined;\n    const submitted = latestState.current;', '    const submitted = latestState.current;'],
        ['success', 'const value = await request.unwrap();\n        if (owner?.cancelled || !requestIsCurrent()) return undefined;', 'const value = await request.unwrap();\n        if (owner?.cancelled) return undefined;'],
        ['failure', "          !requestIsCurrent() ||\n          apiError.code === 'CLIENT_CANCELLED'", "          generation !== compileGeneration.current ||\n          apiError.code === 'CLIENT_CANCELLED'"],
    ];
    for (const [boundary, before, after] of faults) {
        assert.equal(original.split(before).length, 2);
        await observe(original, boundary);
        await assert.rejects(() => observe(original.replace(before, after), boundary), error => error.code === 'ERR_ASSERTION');
        await observe(original, boundary);
    }
});


test('counterexample decoding rejects obsolete paths without forbidden effects', () => {
    for (const action of ['ChangeExplorer', 'DetachWorkspace', 'CancelOwner']) {
        const path = schedule('stop', action);
        assert.throws(() => recoveryCounterexampleWitness({ property: recoveryProperty, states: [{ action: 'Initial', vars: initial() }, ...path.map(step => ({ action: step.action, vars: step.to }))] }), /does not violate the named/);
    }
});

test('cached implementations cannot silently ignore explicit source overrides', async () => {
    const implementation = await loadRecoveryImplementation();
    const path = schedule('stop', 'DetachWorkspace');
    for (const workspaceSource of ['', ' \n ', null, false, 0]) {
        await assert.rejects(() => replayRecoverySchedule(path, { implementation, workspaceSource }), /nonempty/);
        await assert.rejects(() => loadRecoveryImplementation({ workspaceSource }), /nonempty/);
    }
    await assert.rejects(() => replayRecoverySchedule(path, { implementation, workspaceSource: readFileSync(join(root, workspaceFile), 'utf8') }), /Do not mix/);
    await assert.rejects(() => replayRecoverySchedule(path, { implementation: false }), /Invalid recovery implementation/);
    assert.equal((await replayRecoverySchedule(path, { implementation })).status, 'property-held');
});
