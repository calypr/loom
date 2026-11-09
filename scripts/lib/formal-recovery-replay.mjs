import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdtempSync, mkdirSync, rmSync, existsSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { stripTypeScriptTypes } from 'node:module';
import { root, tools, digest } from './formal-tools.mjs';
import { verificationTimeoutMs } from './verification-budget.mjs';

export const recoveryModel = 'ReconcileRefresh';
export const recoveryProperty = 'Inv_ObsoleteRecoveryDoesNotResume';
export const recoveryEffectProperty = 'ReconcileRefresh_EffectAgreement';
export const workspaceFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx';
const recoveryFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/reconcileRecovery.ts';
const acceptanceFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/reconcileAcceptance.ts';
const authoringFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/model.ts';
const productionFiles = [workspaceFile, recoveryFile, acceptanceFile, authoringFile];
const modelDirectory = join(root, 'verification/builder-interactions');
const allowedActions = new Set(['AwaitRecovery', 'ChangeExplorer', 'EditWorkspace', 'StartNewCompile', 'CancelOwner', 'DetachWorkspace', 'ResumeRecovery']);
const source = file => readFileSync(join(root, file), 'utf8');
const tsModule = text => `data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(text, { mode: 'strip' })).toString('base64')}`;

/** Extract one actual production callback; do not duplicate its async branches. */
export function extractReconcileCallback(text) {
    const anchor = '  const reconcileCurrent = useCallback(';
    assert.equal(text.split(anchor).length, 2, 'Expected one production reconcile callback');
    const start = text.indexOf(anchor) + anchor.length;
    const end = text.indexOf('\n  }, [\n', start);
    assert.ok(end > start, 'Missing production reconcile callback boundary');
    const callback = text.slice(start, end) + '\n  }';
    assert.ok(callback.startsWith('async ('), 'Reconcile callback is no longer async');
    const next = text.slice(end + '\n  }, [\n'.length);
    assert.match(next, /^\s+authResourcePath,\n\s+dispatch,\n\s+projectId,\n\s+reconcileBuilder,\n\s+refetchBuilder,\n\s+syncBuilderData,\n\s+\]\);\n/,
        'Production reconcile closure dependencies changed; review adapter');
    const stripped = stripTypeScriptTypes(`const callback = ${callback};`, { mode: 'strip' });
    return stripped.slice('const callback = '.length, -1);
}

export async function loadRecoveryImplementation({ workspaceSource = source(workspaceFile) } = {}) {
    assert.ok(typeof workspaceSource === 'string' && workspaceSource.trim(), 'Recovery source override must be nonempty');
    const [recovery, authoring] = await Promise.all([import(tsModule(source(recoveryFile))), import(tsModule(source(authoringFile)))]);
    const acceptance = source(acceptanceFile);
    assert.equal(acceptance.split("'./reconcileRecovery'").length, 2, 'Acceptance dependency changed');
    const acceptanceJS = stripTypeScriptTypes(acceptance).replace("'./reconcileRecovery'", JSON.stringify(tsModule(source(recoveryFile))));
    const accepted = await import(`data:text/javascript;base64,${Buffer.from(acceptanceJS).toString('base64')}`);
    const callback = extractReconcileCallback(workspaceSource);
    const names = ['latestState', 'workspaceAttached', 'compileGeneration', 'activeCompile', 'projectId', 'authResourcePath', 'dispatch', 'reconcileBuilder', 'refetchBuilder', 'syncBuilderData', 'setMessage', 'diagnosticsFromError', 'window', 'intentFingerprint', 'workspaceFromState', 'reconcileResultIsCurrent', 'reconcileRecoveryAction', 'reconcileAcceptance'];
    // No surrounding component code is evaluated. Missing exercised free variables
    // throw rather than falling back to a test reimplementation.
    const factory = new Function(...names, `"use strict"; return (${callback});`);
    return dependencies => factory(...names.map(name => name in dependencies ? dependencies[name] : ({ ...recovery, ...authoring, ...accepted })[name]));
}

function fixture(owner, fingerprint) {
    return { explorerId: owner, catalog: { snapshotToken: 'snapshot-1' }, draftVersion: 1, draftDigest: 'draft-1',
        workspace: { explorer: { title: fingerprint } }, tables: [{ outputId: 'patients', tabId: 'patients-tab', title: 'Patients',
            document: { rootResourceType: 'Patient', output: { id: 'patients', title: 'Patients' }, columns: [] } }] };
}
const deferred = () => {
    let resolve, reject;
    const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
};
async function settleUntil(condition, description) {
    for (let turn = 0; turn < 20; turn++) {
        if (condition()) return;
        await Promise.resolve();
    }
    assert.ok(condition(), `Production callback did not reach ${description}`);
}
const isCurrent = value => value.currentOwner === 'A' && value.currentFingerprint === 'F0' && value.currentGeneration === 0;
const expected = value => {
    const admitted = isCurrent(value) && value.workspaceAttached && !value.cancelled && (value.refreshKind === 'backoff' || value.hasData);
    return { synchronized: admitted && value.refreshKind !== 'backoff', retried: admitted && value.refreshKind !== 'stop' };
};

export function validateRecoveryState(value) {
    assert.deepEqual(Object.keys(value).sort(), ['refreshPhase', 'refreshKind', 'hasData', 'currentOwner', 'currentFingerprint', 'currentGeneration', 'workspaceAttached', 'cancelled', 'synchronized', 'retried'].sort());
    assert.ok(['idle', 'waiting', 'done'].includes(value.refreshPhase));
    assert.ok(['stop', 'retry', 'backoff'].includes(value.refreshKind));
    assert.ok(['A', 'B'].includes(value.currentOwner));
    assert.ok(['F0', 'F1'].includes(value.currentFingerprint));
    assert.ok([0, 1, 2].includes(value.currentGeneration));
    for (const key of ['hasData', 'workspaceAttached', 'cancelled', 'synchronized', 'retried']) assert.equal(typeof value[key], 'boolean', key);
    if (value.refreshKind === 'backoff') assert.equal(value.hasData, false, 'Backoff has no builder DTO');
}

/** Run an actual suspended production callback through a concrete modeled path. */
export async function replayRecoverySchedule(schedule, { workspaceSource, implementation, automatic = false, rename = false } = {}) {
    assert.ok(workspaceSource === undefined || typeof workspaceSource === 'string' && workspaceSource.trim(), 'Recovery source override must be nonempty');
    assert.ok(implementation === undefined || typeof implementation === 'function', 'Invalid recovery implementation');
    assert.ok(!(implementation !== undefined && workspaceSource !== undefined), 'Do not mix recovery implementation and source override');
    assert.equal(typeof automatic, 'boolean'); assert.equal(typeof rename, 'boolean');
    assert.ok(Array.isArray(schedule) && schedule.length >= 1, 'Missing recovery schedule');
    for (const step of schedule) {
        assert.ok(allowedActions.has(step.action), 'Unknown recovery action');
        validateRecoveryState(step.to);
    }
    let previous = initialRecoveryState();
    for (const step of schedule) { validateRecoveryTransition(previous, step.action, step.to, { brokenResume: step.action === 'ResumeRecovery' }); previous = step.to; }
    assert.equal(schedule[0].action, 'AwaitRecovery');
    assert.equal(schedule.at(-1).action, 'ResumeRecovery');
    if (!automatic && schedule.some(step => step.action === 'CancelOwner')) return { status: 'outside-caller-boundary', reason: 'ownerless manual reconcile cannot receive owner cancellation' };
    const chosen = schedule[0].to;
    const name = value => rename ? value === 'A' ? 'B' : 'A' : value;
    const fingerprint = value => rename ? value === 'F0' ? 'F1' : 'F0' : value;
    const latestState = { current: fixture(name('A'), fingerprint('F0')) }, compileGeneration = { current: 0 }, activeCompile = { current: undefined };
    const workspaceAttached = { current: true };
    const refresh = deferred(), first = deferred(), wait = deferred();
    const events = [], requests = [];
    let waiting = false;
    const owner = automatic ? { cancelled: false } : undefined;
    const factory = implementation || await loadRecoveryImplementation({ workspaceSource });
    const callback = factory({ latestState, workspaceAttached, compileGeneration, activeCompile, projectId: 'project-fixture', authResourcePath: '/fixture',
        dispatch: event => events.push({ kind: 'dispatch', event }),
        setMessage: message => events.push({ kind: 'message', message }),
        diagnosticsFromError: error => [{ severity: 'error', code: error.code }],
        reconcileBuilder: request => {
            requests.push(request);
            if (requests.length === 1) return { unwrap: () => first.promise };
            return { unwrap: async () => ({ snapshotToken: request.snapshotToken, outputs: [{ outputId: 'patients' }], diagnostics: [] }) };
        },
        refetchBuilder: request => { events.push({ kind: 'refetch', request }); waiting = true; return refresh.promise; },
        syncBuilderData: (data, mode) => events.push({ kind: 'sync', data, mode }),
        window: { setTimeout: (resolve, milliseconds) => { assert.equal(milliseconds, 250); waiting = true; wait.promise.then(resolve); } },
    });
    const result = callback(owner, 'patients');
    assert.equal(requests.length, 1, 'Actual callback did not submit its initial compile');
    const error = chosen.refreshKind === 'stop' ? { code: 'DRAFT_CONFLICT' } : chosen.refreshKind === 'retry' ? { code: 'STALE_CATALOG_SNAPSHOT' } : { code: 'TRANSIENT_FAILURE', retryable: true };
    first.reject(error);
    await settleUntil(() => waiting, 'the modeled recovery await');
    for (const step of schedule.slice(1)) {
        if (step.action === 'ChangeExplorer') latestState.current = { ...latestState.current, explorerId: name(step.to.currentOwner) };
        else if (step.action === 'EditWorkspace') latestState.current = { ...latestState.current, workspace: { explorer: { title: fingerprint(step.to.currentFingerprint) } } };
        else if (step.action === 'StartNewCompile') compileGeneration.current = 1 + step.to.currentGeneration;
        else if (step.action === 'DetachWorkspace') workspaceAttached.current = false;
        else if (step.action === 'CancelOwner') {
            assert.ok(owner); owner.cancelled = true;
        } else if (step.action === 'ResumeRecovery') {
            assert.equal(step, schedule.at(-1), 'Recovery resumption must terminate the schedule');
            if (chosen.refreshKind === 'backoff') wait.resolve();
            else refresh.resolve(chosen.hasData ? { data: fixture(name('A'), fingerprint('F0')) } : {});
        } else throw new Error('Recovery schedule nested another wait');
    }
    const value = await result;
    const actual = { synchronized: events.some(event => event.kind === 'sync'), retried: requests.length > 1 };
    const contract = expected(schedule.at(-1).to);
    if (actual.synchronized !== contract.synchronized || actual.retried !== contract.retried) {
        const property = !isCurrent(schedule.at(-1).to) || !schedule.at(-1).to.workspaceAttached || schedule.at(-1).to.cancelled ? recoveryProperty : recoveryEffectProperty;
        const witness = { schemaVersion: 1, kind: 'reconcile-recovery', model: recoveryModel, property, automatic, rename, schedule, expected: contract, actual };
        const error = new Error(`${property}: expected ${JSON.stringify(contract)}, got ${JSON.stringify(actual)}`);
        Object.assign(error, { code: 'FORMAL_PROPERTY', property, witness });
        throw error;
    }
    assert.equal(activeCompile.current, undefined, 'Production callback leaked an active request');
    return { status: 'property-held', expected: contract, actual, returnedReceipt: value !== undefined, requests: requests.length };
}

/** The adapter rejects action/state drift before scheduling the production callback. */
export function validateRecoveryTransition(from, action, to, { brokenResume = false } = {}) {
    validateRecoveryState(from); validateRecoveryState(to);
    const next = { ...from };
    if (action === 'AwaitRecovery') {
        assert.equal(from.refreshPhase, 'idle');
        Object.assign(next, { refreshPhase: 'waiting', refreshKind: to.refreshKind, hasData: to.hasData });
    } else {
        assert.equal(from.refreshPhase, 'waiting', 'Recovery action outside the suspended await');
        if (action === 'ChangeExplorer') { assert.equal(from.workspaceAttached, true); next.currentOwner = from.currentOwner === 'A' ? 'B' : 'A'; }
        else if (action === 'EditWorkspace') { assert.equal(from.workspaceAttached, true); next.currentFingerprint = from.currentFingerprint === 'F0' ? 'F1' : 'F0'; }
        else if (action === 'StartNewCompile') { assert.equal(from.workspaceAttached, true); assert.ok(from.currentGeneration < 2); next.currentGeneration++; }
        else if (action === 'CancelOwner') { assert.equal(from.cancelled, false); next.cancelled = true; }
        else if (action === 'DetachWorkspace') { assert.equal(from.workspaceAttached, true); next.workspaceAttached = false; }
        else if (action === 'ResumeRecovery') Object.assign(next, { refreshPhase: 'done', ...expected(from) }, brokenResume ? { synchronized: to.synchronized, retried: to.retried } : {});
        else throw new Error('Unknown recovery transition action');
    }
    assert.deepEqual(to, next, `Recovery transition does not match ${action}`);
}
const initialRecoveryState = () => ({ refreshPhase: 'idle', refreshKind: 'stop', hasData: false, currentOwner: 'A', currentFingerprint: 'F0', currentGeneration: 0, workspaceAttached: true, cancelled: false, synchronized: false, retried: false });

export async function recoveryGraph(output) {
    const { scripts, jar } = tools();
    const directory = mkdtempSync(join(tmpdir(), 'loom-recovery-graph-'));
    try {
        const result = spawnSync('java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC', '-workers', '1', '-metadir', join(directory, 'states'), '-config', `${recoveryModel}.cfg`, '-dump', 'dot,actionlabels', join(directory, 'graph'), `${recoveryModel}.tla`], { cwd: modelDirectory, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024 });
        assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr);
        const [{ parseDot }, { json }] = await Promise.all([import(pathToFileURL(join(scripts, 'frontends/dot.ts')).href), import(pathToFileURL(join(scripts, 'frontends/view.ts')).href)]);
        const graph = parseDot(readFileSync(join(directory, 'graph.dot'), 'utf8'));
        const serialized = { schemaVersion: 1, model: recoveryModel, modelSha256: digest(join(modelDirectory, `${recoveryModel}.tla`)), configSha256: digest(join(modelDirectory, `${recoveryModel}.cfg`)), productionSha256: Object.fromEntries(productionFiles.map(file => [file, digest(join(root, file))])), states: [...graph.states].map(([id, state]) => ({ id, start: state.start, vars: Object.fromEntries(Object.entries(state.vars).map(([key, value]) => [key, json(value)])) })), steps: graph.steps };
        writeFileSync(output, JSON.stringify(serialized)); return serialized;
    } finally { rmSync(directory, { recursive: true, force: true }); }
}

export async function checkRecoveryGraph(graph, { workspaceSource, witnessFile } = {}) {
    assert.equal(graph.schemaVersion, 1); assert.equal(graph.model, recoveryModel);
    assert.equal(graph.modelSha256, digest(join(modelDirectory, `${recoveryModel}.tla`)), 'Stale recovery model');
    assert.equal(graph.configSha256, digest(join(modelDirectory, `${recoveryModel}.cfg`)), 'Stale recovery config');
    for (const file of productionFiles) if (!(file === workspaceFile && workspaceSource)) assert.equal(graph.productionSha256[file], digest(join(root, file)), `Stale production: ${file}`);
    assert.equal(graph.states.length, 481, 'Recovery state frontier changed');
    assert.equal(graph.steps.length, 805, 'Recovery edge frontier changed');
    const states = new Map(graph.states.map(value => [value.id, value])); assert.equal(states.size, graph.states.length, 'Duplicate recovery states');
    const stateKey = state => JSON.stringify(Object.entries(state.vars).sort(([a], [b]) => a.localeCompare(b)));
    assert.equal(new Set(graph.states.map(stateKey)).size, states.size, 'Duplicate recovery state values');
    for (const state of states.values()) validateRecoveryState(state.vars);
    const initial = graph.states.filter(state => state.start); assert.equal(initial.length, 1);
    assert.deepEqual(initial[0].vars, initialRecoveryState(), 'Invalid initial recovery context');
    const paths = new Map([[initial[0].id, []]]), outgoing = new Map();
    const keys = new Set();
    for (const step of graph.steps) {
        assert.ok(states.has(step.from) && states.has(step.to), 'Dangling recovery edge');
        assert.ok(allowedActions.has(step.action), 'Unknown recovery edge action');
        validateRecoveryTransition(states.get(step.from).vars, step.action, states.get(step.to).vars);
        assert.deepEqual(Object.keys(step).sort(), ['action', 'from', 'to']);
        const key = JSON.stringify([step.from, step.action, step.to]); assert.ok(!keys.has(key), 'Duplicate recovery edge'); keys.add(key);
        const list = outgoing.get(step.from) || []; list.push(step); outgoing.set(step.from, list);
    }
    const stateByValue = new Map(graph.states.map(state => [stateKey(state), state]));
    const requireEdge = (from, action, vars) => {
        const target = stateByValue.get(stateKey({ vars }));
        assert.ok(target, `Missing recovery destination for ${action}`);
        assert.ok(keys.has(JSON.stringify([from.id, action, target.id])), `Missing supported recovery edge: ${action}`);
    };
    for (const state of states.values()) {
        const value = state.vars;
        if (value.refreshPhase === 'idle') {
            for (const refreshKind of ['stop', 'retry', 'backoff']) for (const hasData of refreshKind === 'backoff' ? [false] : [false, true])
                requireEdge(state, 'AwaitRecovery', { ...value, refreshPhase: 'waiting', refreshKind, hasData });
        } else if (value.refreshPhase === 'waiting') {
            if (value.workspaceAttached) {
            requireEdge(state, 'ChangeExplorer', { ...value, currentOwner: value.currentOwner === 'A' ? 'B' : 'A' });
            requireEdge(state, 'EditWorkspace', { ...value, currentFingerprint: value.currentFingerprint === 'F0' ? 'F1' : 'F0' });
            if (value.currentGeneration < 2) requireEdge(state, 'StartNewCompile', { ...value, currentGeneration: value.currentGeneration + 1 });
            requireEdge(state, 'DetachWorkspace', { ...value, workspaceAttached: false });
            }
            if (!value.cancelled) requireEdge(state, 'CancelOwner', { ...value, cancelled: true });
            requireEdge(state, 'ResumeRecovery', { ...value, refreshPhase: 'done', ...expected(value) });
        }
    }
    const queue = [initial[0].id];
    for (let index = 0; index < queue.length; index++) for (const step of outgoing.get(queue[index]) || []) if (!paths.has(step.to)) { paths.set(step.to, [...paths.get(step.from), step]); queue.push(step.to); }
    assert.equal(paths.size, states.size, 'Unreachable recovery state');
    const implementation = await loadRecoveryImplementation({ workspaceSource });
    const coverage = { stop: 0, retry: 0, backoff: 0, current: 0, obsolete: 0, cancelled: 0, detached: 0, noData: 0 };
    let replays = 0, outsideCallerBoundary = 0;
    const coveredEdges = new Set();
    for (const edge of graph.steps) {
        const path = [...paths.get(edge.from), edge];
        if (states.get(edge.to).vars.refreshPhase === 'waiting') {
            const completion = (outgoing.get(edge.to) || []).filter(step => step.action === 'ResumeRecovery');
            assert.equal(completion.length, 1, 'Pending recovery has no unique completion'); path.push(completion[0]);
        }
        assert.equal(path.at(-1).action, 'ResumeRecovery', 'Path has no recovery completion');
        const schedule = path.map(step => ({ action: step.action, to: states.get(step.to).vars }));
        const end = schedule.at(-1).to;
        assert.deepEqual({ synchronized: end.synchronized, retried: end.retried }, expected(end), 'Model result differs from independent reader contract');
        for (const automatic of [false, true]) for (const rename of [false, true]) {
            try {
                const result = await replayRecoverySchedule(schedule, { implementation, automatic, rename });
                if (result.status === 'outside-caller-boundary') outsideCallerBoundary++;
                else { coveredEdges.add(JSON.stringify([edge.from, edge.action, edge.to])); replays++; coverage[end.refreshKind]++; coverage[isCurrent(end) ? 'current' : 'obsolete']++; if (end.cancelled) coverage.cancelled++; if (!end.workspaceAttached) coverage.detached++; if (!end.hasData) coverage.noData++; }
            } catch (error) { if (witnessFile && error.witness) writeFileSync(witnessFile, JSON.stringify(error.witness, null, 2)); throw error; }
        }
    }
    assert.equal(coveredEdges.size, graph.steps.length, 'A model edge has no actual production replay');
    assert.ok(Object.values(coverage).every(count => count > 0), 'Vacuous recovery replay');
    return { states: states.size, transitions: graph.steps.length, edgeSchedules: graph.steps.length, coveredEdges: coveredEdges.size, replays, outsideCallerBoundary, coverage, boundary: 'actual extracted reconcileCurrent callback, real authoring/recovery/acceptance helpers; injected API, timer and state-sync boundaries; no React rendering/storage/full publication claim' };
}

export const recoverySourceProofs = [
    { branch: 'detached-workspace', before: 'const requestIsCurrent = () => workspaceAttached.current && reconcileResultIsCurrent({', after: 'const requestIsCurrent = () => reconcileResultIsCurrent({' },
    { branch: 'refresh-and-stop', before: "if (recovery === 'refresh-and-stop') {\n          const refreshed = await refetchBuilder({ reload: true });\n          if (owner?.cancelled || !requestIsCurrent()) return undefined;", after: "if (recovery === 'refresh-and-stop') {\n          const refreshed = await refetchBuilder({ reload: true });\n          if (owner?.cancelled) return undefined;" },
    { branch: 'refresh-and-retry', before: 'if (owner?.cancelled || !requestIsCurrent() || !refreshed.data) return undefined;', after: 'if (owner?.cancelled || !refreshed.data) return undefined;' },
    { branch: 'backoff', before: 'for (;;) {\n      if (owner?.cancelled || !requestIsCurrent()) return undefined;', after: 'for (;;) {\n      if (owner?.cancelled) return undefined;' },
].map(control => ({ model: recoveryModel, property: recoveryProperty, kind: 'recovery-replay', branch: control.branch, edits: [[workspaceFile, control.before, control.after]] }));

export async function checkRecoverySourceProof(proof, graph, output) {
    assert.deepEqual(proof, recoverySourceProofs.find(row => row.branch === proof.branch), 'Unknown or changed recovery source proof');
    mkdirSync(output, { recursive: true });
    const [[file, before, after]] = proof.edits;
    assert.equal(file, workspaceFile);
    const original = source(file);
    assert.equal(original.split(before).length, 2, 'Nonunique recovery source fault');
    const broken = original.replace(before, after);
    await checkRecoveryGraph(graph);
    const witnessFile = join(output, 'counterexample.json');
    await assert.rejects(() => checkRecoveryGraph(graph, { workspaceSource: broken, witnessFile }), error => error.code === 'FORMAL_PROPERTY' && error.property === recoveryProperty);
    const witness = JSON.parse(readFileSync(witnessFile, 'utf8'));
    assert.equal((await replayRecoveryWitness(witness)).status, 'property-held');
    assert.equal((await replayRecoveryWitness(witness, { workspaceSource: broken })).status, 'reproduced');
    await checkRecoveryGraph(graph);
    const evidence = { model: recoveryModel, property: recoveryProperty, kind: 'recovery-replay', sourceFiles: [workspaceFile], branch: proof.branch, positive: true, negative: true, restored: true, replay: 'actual extracted production async callback', witness: 'counterexample.json' };
    writeFileSync(join(output, 'proof.json'), JSON.stringify(evidence, null, 2));
    process.stderr.write(`${recoveryProperty} (${proof.branch}): actual async positive / broken variant / restored PASS\n`);
    return evidence;
}

export function recoveryCounterexampleWitness(counterexample) {
    assert.equal(counterexample.property, recoveryProperty, 'Unknown recovery counterexample property');
    assert.ok(Array.isArray(counterexample.states) && counterexample.states.length >= 3, 'Counterexample did not cross a recovery wait');
    assert.equal(counterexample.states[0].action, 'Initial', 'Counterexample does not start at TLC initial state');
    assert.deepEqual(counterexample.states[0].vars, initialRecoveryState(), 'Counterexample has another initial context');
    const schedule = counterexample.states.slice(1).map(state => ({ action: state.action, to: state.vars }));
    assert.equal(schedule[0].action, 'AwaitRecovery'); assert.equal(schedule.at(-1).action, 'ResumeRecovery');
    let prior = initialRecoveryState();
    for (const step of schedule) { validateRecoveryTransition(prior, step.action, step.to, { brokenResume: step.action === 'ResumeRecovery' }); prior = step.to; }
    assert.ok(!isCurrent(prior) || !prior.workspaceAttached || prior.cancelled, 'Counterexample never obsoleted the current recovery');
    assert.ok(prior.synchronized || prior.retried, 'Counterexample does not violate the named recovery invariant');
    return { schemaVersion: 1, kind: 'reconcile-recovery', model: recoveryModel, property: recoveryProperty, automatic: true, rename: false, schedule, expected: expected(prior) };
}
export async function replayRecoveryWitness(witness, options = {}) {
    assert.equal(witness.schemaVersion, 1); assert.equal(witness.kind, 'reconcile-recovery'); assert.equal(witness.model, recoveryModel);
    assert.equal(typeof witness.automatic, 'boolean'); assert.equal(typeof witness.rename, 'boolean');
    assert.ok(witness.automatic || !witness.schedule.some(step => step.action === 'CancelOwner'), 'Manual owner cancellation is not a reachable witness');
    const end = witness.schedule.at(-1)?.to; validateRecoveryState(end);
    const property = !isCurrent(end) || !end.workspaceAttached || end.cancelled ? recoveryProperty : recoveryEffectProperty;
    assert.equal(witness.property, property, 'Witness does not trigger its named reader promise');
    if (witness.expected) assert.deepEqual(witness.expected, expected(end), 'Witness changed the independent expected effect');
    try { const result = await replayRecoverySchedule(witness.schedule, { ...options, automatic: witness.automatic, rename: witness.rename }); return result; }
    catch (error) { if (error.code !== 'FORMAL_PROPERTY') throw error; return { status: 'reproduced', ...error.witness }; }
}
export async function checkRecoveryGraphControls(graph) {
    const controls = {
        emptyEdges: value => { value.steps = []; },
        missingDetachment: value => { value.steps.splice(value.steps.findIndex(step => step.action === 'DetachWorkspace'), 1); },
        missingCancellation: value => { value.steps.splice(value.steps.findIndex(step => step.action === 'CancelOwner'), 1); },
        duplicateEdge: value => { value.steps[0] = { ...value.steps[1] }; },
        danglingEdge: value => { value.steps[0].to = 'not-a-state'; },
        unknownAction: value => { value.steps[0].action = 'UnreviewedAction'; },
        wrongContext: value => { value.states.find(state => !state.start).vars.currentGeneration = 9; },
        unreachableExtraState: value => { value.states.push({ ...value.states[0], id: 'unreachable' }); },
        staleModel: value => { value.modelSha256 = 'stale'; },
        staleConfig: value => { value.configSha256 = 'stale'; },
        staleProduction: value => { value.productionSha256[workspaceFile] = 'stale'; },
    };
    const result = [];
    for (const [name, change] of Object.entries(controls)) {
        const broken = structuredClone(graph); change(broken);
        await assert.rejects(() => checkRecoveryGraph(broken), error => error.code !== 'FORMAL_PROPERTY');
        result.push({ name, rejected: true });
    }
    return result;
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
    const [command, file, extra] = process.argv.slice(2);
    if (command === '--generate') {
        assert.ok(!extra || extra === '--self-test', 'Unknown recovery generation option');
        assert.ok(!existsSync(file) || readdirSync(file).length === 0, 'Recovery replay output must be empty');
        mkdirSync(file, { recursive: true });
        const graph = await recoveryGraph(join(file, 'graph.json'));
        const replay = await checkRecoveryGraph(graph, { witnessFile: join(file, 'counterexample.json') });
        writeFileSync(join(file, 'replay.json'), JSON.stringify(replay, null, 2));
        if (extra === '--self-test') {
            replay.graphControls = await checkRecoveryGraphControls(graph);
            replay.sourceControls = [];
            for (const proof of recoverySourceProofs) replay.sourceControls.push(await checkRecoverySourceProof(proof, graph, join(file, proof.branch)));
            writeFileSync(join(file, 'self-test.json'), JSON.stringify(replay, null, 2));
        }
        console.log(JSON.stringify(replay));
    }
    else if (command === '--witness') { const record = JSON.parse(readFileSync(file, 'utf8')); const witness = record.counterexamples ? recoveryCounterexampleWitness(record.counterexamples.find(value => value.property === recoveryProperty)) : record; const result = await replayRecoveryWitness(witness, extra ? { workspaceSource: readFileSync(extra, 'utf8') } : {}); console.log(JSON.stringify(result)); process.exitCode = result.status === 'reproduced' ? 1 : 0; }
    else if (command === '--graph') console.log(JSON.stringify(await checkRecoveryGraph(JSON.parse(readFileSync(file, 'utf8')), extra ? { workspaceSource: readFileSync(extra, 'utf8') } : {})));
    else throw new Error('Use --generate DIRECTORY, --graph GRAPH_JSON [WORKSPACE_SOURCE], or --witness WITNESS_JSON [WORKSPACE_SOURCE]');
}
