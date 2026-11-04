import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, existsSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { stripTypeScriptTypes } from 'node:module';
import { root, tools, digest } from './formal-tools.mjs';
import { verificationTimeoutMs } from './verification-budget.mjs';
export const exclusionModel = 'PopulationExclusion';
export const exclusionProperties = ['Inv_ExclusionSubmissionUsesCurrentIntent', 'Inv_CurrentExclusionSubmits'];
export const workspaceFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx';
const authoringFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/model.ts';
const identityFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/commandDraftIdentity.ts';
const routesFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/populationRoutes.ts';
const productionFiles = [workspaceFile, authoringFile, identityFile, routesFile, 'ui/package-lock.json'];
export const exclusionSourceHashes = () => Object.fromEntries(productionFiles.map(file => [file, digest(join(root, file))]));
const modelDirectory = join(root, 'verification/builder-interactions');
const source = file => readFileSync(join(root, file), 'utf8');
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(name => [name, canonical(value[name])])) : value;
const key = value => JSON.stringify(canonical(value));
const current = v => v.hostAttached && v.contextKind === 'same' && v.localBase === 'A' && v.localVersion === v.serverVersion;
export const initialExclusionState = () => ({ exclusionPhase: 'creation', localBase: 'A', serverBase: 'A', localVersion: 1, serverVersion: 1, contextKind: 'same', hostAttached: true, changeKind: 'none', routeCalls: 0, submitted: false });
function validateStateShape(v) { assert.ok(v && typeof v === 'object' && !Array.isArray(v)); assert.deepEqual(Object.keys(v).sort(), Object.keys(initialExclusionState()).sort(), 'Unknown or missing exclusion state field'); }
function nextStates(v) {
    const edges = [], add = (action, edit) => edges.push({ action, vars: { ...v, ...edit } });
    if (v.exclusionPhase === 'creation') add('CreationReturns', { exclusionPhase: current(v) ? 'route-one' : 'done', routeCalls: current(v) ? 1 : 0 });
    if (['route-one', 'route-two'].includes(v.exclusionPhase)) for (const found of [true, false]) {
        if (v.exclusionPhase === 'route-two' && !found) continue;
        add('RoutePageReturns', { exclusionPhase: !current(v) ? 'done' : found ? 'drain' : 'route-two', routeCalls: current(v) && !found ? 2 : v.routeCalls });
    }
    if (v.exclusionPhase === 'drain') add('DrainReturnsAndSubmit', { exclusionPhase: 'done', submitted: current(v) });
    if (v.exclusionPhase !== 'done' && v.changeKind === 'none') {
        add('ChangeBase', { changeKind: 'base', localBase: 'B', serverBase: 'B', localVersion: 2, serverVersion: 2 });
        for (const kind of ['snapshot', 'route', 'client', 'output', 'root']) add('ChangeContext', { changeKind: 'context', contextKind: kind });
        add('DetachHost', { changeKind: 'detach', hostAttached: false });
        add('UnrelatedSave', { changeKind: 'unrelated', localVersion: 2, serverVersion: 2 });
        add('BeginHydration', { changeKind: 'hydrating', serverBase: 'B', serverVersion: 2 });
    }
    if (v.exclusionPhase !== 'done' && v.changeKind === 'hydrating') add('FinishHydration', { changeKind: 'hydrated', localBase: 'B', localVersion: 2 });
    return edges;
}
export function exclusionFrontier() {
    const states = new Map(), edges = new Map(), pending = [initialExclusionState()];
    for (let i = 0; i < pending.length; i++) {
        const v = pending[i], id = key(v); if (states.has(id)) continue; states.set(id, v);
        for (const edge of nextStates(v)) { edges.set(key([id, edge.action, key(edge.vars)]), [id, edge.action, key(edge.vars)]); pending.push(edge.vars); }
    }
    return { states: [...states.values()], edges: [...edges.values()] };
}
export function validateExclusionGraph(graph, { workspaceSource } = {}) {
    checkedSource(workspaceSource);
    assert.deepEqual(Object.keys(graph).sort(), ['schemaVersion', 'model', 'modelSha256', 'configSha256', 'productionSha256', 'states', 'steps'].sort());
    assert.equal(graph.schemaVersion, 1); assert.equal(graph.model, exclusionModel);
    assert.equal(graph.modelSha256, digest(join(modelDirectory, `${exclusionModel}.tla`)), 'Stale exclusion model');
    assert.equal(graph.configSha256, digest(join(modelDirectory, `${exclusionModel}.cfg`)), 'Stale exclusion config');
    assert.deepEqual(Object.keys(graph.productionSha256).sort(), productionFiles.slice().sort());
    for (const file of productionFiles) if (!(workspaceSource !== undefined && file === workspaceFile)) assert.equal(graph.productionSha256[file], digest(join(root, file)), `Stale production source ${file}`);
    const expected = exclusionFrontier(), values = new Set(expected.states.map(key)), ids = new Map();
    assert.equal(graph.states.length, values.size, 'Incomplete exclusion states');
    for (const state of graph.states) { assert.deepEqual(Object.keys(state).sort(), ['id', 'start', 'vars']); validateStateShape(state.vars); assert.equal(typeof state.id, 'string'); assert.equal(typeof state.start, 'boolean'); assert.ok(!ids.has(state.id)); assert.ok(values.delete(key(state.vars)), 'Unknown or duplicate exclusion state'); ids.set(state.id, state); }
    assert.equal(values.size, 0); const starts = graph.states.filter(state => state.start); assert.equal(starts.length, 1); assert.deepEqual(starts[0].vars, initialExclusionState());
    const edges = new Set(expected.edges.map(key)); assert.equal(graph.steps.length, edges.size, 'Incomplete exclusion edges');
    for (const edge of graph.steps) { assert.deepEqual(Object.keys(edge).sort(), ['action', 'from', 'to']); assert.ok(ids.has(edge.from) && ids.has(edge.to), 'Dangling exclusion edge'); assert.ok(edges.delete(key([key(ids.get(edge.from).vars), edge.action, key(ids.get(edge.to).vars)])), 'Unknown or duplicate exclusion edge'); }
    assert.equal(edges.size, 0); return graph;
}
export async function exclusionGraph(output) {
    const { scripts, jar } = tools(), scratch = mkdtempSync(join(tmpdir(), 'loom-exclusion-graph-'));
    try {
        const result = spawnSync('java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC', '-workers', '1', '-metadir', join(scratch, 'states'), '-config', `${exclusionModel}.cfg`, '-dump', 'dot,actionlabels', join(scratch, 'graph'), `${exclusionModel}.tla`], { cwd: modelDirectory, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024 });
        assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr);
        const [{ parseDot }, { json }] = await Promise.all([import(pathToFileURL(join(scripts, 'frontends/dot.ts')).href), import(pathToFileURL(join(scripts, 'frontends/view.ts')).href)]);
        const parsed = parseDot(readFileSync(join(scratch, 'graph.dot'), 'utf8'));
        const graph = { schemaVersion: 1, model: exclusionModel, modelSha256: digest(join(modelDirectory, `${exclusionModel}.tla`)), configSha256: digest(join(modelDirectory, `${exclusionModel}.cfg`)), productionSha256: Object.fromEntries(productionFiles.map(file => [file, digest(join(root, file))])), states: [...parsed.states].map(([id, state]) => ({ id, start: state.start, vars: Object.fromEntries(Object.entries(state.vars).map(([name, value]) => [name, json(value)])) })), steps: parsed.steps };
        validateExclusionGraph(graph); if (output) writeFileSync(output, JSON.stringify(graph)); return graph;
    } finally { rmSync(scratch, { recursive: true, force: true }); }
}
function checkedSource(text) { if (text !== undefined) assert.ok(typeof text === 'string' && text.trim().length, 'Exclusion source override must be nonempty'); return text ?? source(workspaceFile); }
const moduleURL = text => `data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(text)).toString('base64')}`;
export async function loadExclusionImplementation({ workspaceSource } = {}) {
    const text = checkedSource(workspaceSource), ts = (await import(pathToFileURL(join(root, 'ui/node_modules/typescript/lib/typescript.js')).href)).default;
    const ast = ts.createSourceFile('BuilderWorkspace.tsx', text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
    assert.equal(ast.parseDiagnostics.length, 0, 'Production TSX does not parse');
    const declarations = name => { const found = []; const visit = node => { if (ts.isVariableDeclaration(node) && node.name.getText(ast) === name) found.push(node); ts.forEachChild(node, visit); }; visit(ast); assert.equal(found.length, 1, `Expected exactly one ${name}`); return found[0].initializer; };
    const callback = name => { const node = declarations(name); assert.ok(ts.isCallExpression(node) && node.expression.getText(ast) === 'useCallback' && node.arguments.length === 2, `Unsupported ${name} wrapper`); assert.ok(ts.isArrowFunction(node.arguments[0])); return stripTypeScriptTypes(`const value = ${node.arguments[0].getText(ast)};`).slice('const value = '.length, -1); };
    const helpers = {};
    for (const file of [authoringFile, identityFile, routesFile]) Object.assign(helpers, await import(moduleURL(source(file))));
    let binding;
    if (text.includes('const populationBindingKey =')) binding = new Function(`return (${stripTypeScriptTypes(`const value = ${declarations('populationBindingKey').getText(ast)};`).slice('const value = '.length, -1)});`)();
    const names = ['latestState', 'workspaceAttached', 'serverDraft', 'populationExclusionContextRef', 'activePopulationSelection', 'populationSelectionContextKey', 'projectId', 'authResourcePath', 'loomClient', 'applyCommands', 'setPopulationVariantPending', 'setPopulationVariantError', 'setPopulationSelectionOverride', 'window', 'selectedTable', 'populationBindingKey', 'sameConstructionRoute', 'cancelAutomaticPreview', 'automaticPreviewGeneration', 'compileGeneration', 'previewGeneration', 'activeCompile', 'activePreview', 'setPendingCommands', 'commandQueue', 'relatedExpandQueryOwnerRef', 'applyBuilderCommands', 'commandDraftIdentity', 'setRestorableDraft', 'builderOwnerKeyFor', 'serverDraftKey', 'builderDataKey', 'acceptedConstructionForApply', 'automaticPreviewRequest', 'appliedChoicePreview', 'builderAuthoringReducer', 'hasCommandSelectedTable', 'rememberSelectedTable', 'setActiveConstructionSelection', 'setLocalState', 'setMessage', 'isDraftDesynchronized', 'isStaleSnapshot', 'refetchBuilder', 'syncBuilderData', 'dispatch', 'diagnosticsFromError', 'applyCommandsWithResult'];
    const create = name => { const factory = new Function(...names, `"use strict"; return (${callback(name)});`); return env => factory(...names.map(key => Object.hasOwn(env, key) ? env[key] : key === 'populationBindingKey' ? binding : helpers[key])); };
    return { exclusion: create('excludePopulationMember'), queue: create('applyCommandsWithResult'), apply: create('applyCommands') };
}
const deferred = () => { let resolve; const promise = new Promise(yes => { resolve = yes; }); return { promise, resolve }; };
async function settle(predicate, name) { for (let i = 0; i < 30; i++) { if (predicate()) return; await Promise.resolve(); } assert.ok(predicate(), `Actual exclusion did not reach ${name}`); }
function modelPaths(graph) {
    const states = new Map(graph.states.map(s => [s.id, s])), paths = new Map(), start = graph.states.find(s => s.start).id; paths.set(start, []);
    const queue = [start]; for (let i = 0; i < queue.length; i++) for (const edge of graph.steps.filter(s => s.from === queue[i])) if (!paths.has(edge.to)) { paths.set(edge.to, [...paths.get(edge.from), { action: edge.action, from: states.get(edge.from).vars, to: states.get(edge.to).vars }]); queue.push(edge.to); }
    const result = [];
    for (const edge of graph.steps) {
        const schedule = [...paths.get(edge.from), { action: edge.action, from: states.get(edge.from).vars, to: states.get(edge.to).vars }]; let state = edge.to;
        while (states.get(state).vars.exclusionPhase !== 'done') { const next = graph.steps.find(e => e.from === state && ['CreationReturns', 'RoutePageReturns', 'DrainReturnsAndSubmit'].includes(e.action)); assert.ok(next); schedule.push({ action: next.action, from: states.get(next.from).vars, to: states.get(next.to).vars }); state = next.to; }
        result.push(schedule);
    }
    return result;
}
export async function replayExclusionSchedule(schedule, { implementation, workspaceSource } = {}) {
    assert.ok(!(implementation && workspaceSource !== undefined), 'Cannot mix cached implementation and source override'); if (workspaceSource !== undefined) checkedSource(workspaceSource);
    assert.ok(Array.isArray(schedule) && schedule.length, 'Missing exclusion schedule'); let previous = initialExclusionState();
    for (const edge of schedule) { validateStateShape(edge.to); assert.deepEqual(edge.from, previous, 'Disconnected exclusion schedule'); assert.ok(nextStates(previous).some(next => next.action === edge.action && key(next.vars) === key(edge.to)), 'Unsupported exclusion transition'); previous = edge.to; }
    assert.equal(previous.exclusionPhase, 'done', 'Exclusion schedule must finish');
    const loaded = implementation ?? await loadExclusionImplementation({ workspaceSource });
    const create = deferred(), drain = deferred(), routes = [], routeRequests = [], createRequests = [], submissions = [];
    let completed = false, queueWaiting = false, created = 0, pending = 0, error, drainResolved = false, outsideModelContinuations = 0;
    let semanticState = initialExclusionState();
    const fixture = (base, version, kind) => ({ explorerId: 'explorer-a', selectedOutputId: kind === 'output' ? 'other-output' : 'patients', draftVersion: version, draftDigest: `draft-${version}`, catalog: { snapshotToken: kind === 'snapshot' ? 'snapshot-b' : 'snapshot-a' }, tables: [{ outputId: kind === 'output' ? 'other-output' : 'patients', document: { rootResourceType: kind === 'root' ? 'Observation' : 'Patient', population: { selectionRevisionId: `selection-${base}`, route: kind === 'route' ? [{ resourceType: 'Patient', relationship: 'link', catalogEdgeId: 'edge-b', storageDirection: 'INBOUND' }] : [] } } }] });
    const client = { createSelection: args => { createRequests.push(args); created++; return create.promise; }, searchPopulationRoutes: args => { routeRequests.push(args); const value = deferred(); routes.push(value); return value.promise; } };
    const initialState = fixture('A', 1, 'same');
    const variant = { id: 'variant-A', project: 'project-a', generation: 'generation-a', resourceType: 'Patient', rule: { kind: 'EXPLICIT' }, source: { kind: 'SELECTION_REVISION', revisionId: 'selection-A', generation: 'generation-a' }, exclusions: [{ project: 'project-a', generation: 'generation-a', resourceType: 'Patient', id: 'removed' }], scopeDigest: 'scope-a', ruleDigest: 'rule-a', membershipDigest: 'members-a', memberCount: 1, memberBytes: 32, complete: true, createdAt: '2026-01-01T00:00:00Z' };
    const routeResponse = (found, more = false) => ({ snapshotToken: 'snapshot-a', outputId: 'patients', selectionRevisionId: variant.id, complete: !more, truncated: more, choices: found ? [{ routeChoiceId: 'route-A', route: [], presentation: { summary: 'Direct Patient rows', facts: [] } }] : [], ...(more ? { nextCursor: 'page-two' } : {}) });
    const env = { latestState: { current: initialState }, workspaceAttached: { current: true }, serverDraft: { current: { version: 1, digest: 'draft-1' } }, populationExclusionContextRef: { current: { contextKey: 'context-a', client, baseSelectionID: 'selection-A', bindingKey: JSON.stringify(['selection-A', []]) } }, activePopulationSelection: { id: 'selection-A' }, populationSelectionContextKey: 'context-a', projectId: 'project-a', authResourcePath: '/project-a', loomClient: client, window: { crypto: { randomUUID: () => 'operation-a' } }, setPopulationVariantPending: () => {}, setPopulationVariantError: () => {}, setPopulationSelectionOverride: () => {}, cancelAutomaticPreview: () => {}, automaticPreviewGeneration: { current: 0 }, compileGeneration: { current: 0 }, previewGeneration: { current: 0 }, activeCompile: { current: undefined }, activePreview: { current: undefined }, setPendingCommands: fn => { pending = fn(pending); }, commandQueue: { current: Promise.resolve() }, relatedExpandQueryOwnerRef: { current: { draftVersion: 1, draftDigest: 'draft-1', pauseAndDrain: () => { queueWaiting = true; return drain.promise; }, resume: () => {} } }, applyBuilderCommands: args => ({ unwrap: () => { submissions.push({ args, state: structuredClone(env.latestState.current), server: { ...env.serverDraft.current }, host: env.workspaceAttached.current, context: { ...env.populationExclusionContextRef.current }, semantic: { ...semanticState }, drainResolved }); return new Promise(() => {}); } }) };
    env.applyCommandsWithResult = loaded.queue(env); env.applyCommands = loaded.apply(env);
    const task = loaded.exclusion(env)({ project: 'project-a', generation: 'generation-a', resourceType: 'Patient', id: 'removed' }, []);
    Promise.resolve(task).then(() => { completed = true; }, value => { error = value; completed = true; });
    await settle(() => created || completed, 'creation');
    let routeIndex = 0;
    const applyState = state => {
        env.workspaceAttached.current = state.hostAttached;
        env.serverDraft.current = { version: state.serverVersion, digest: `draft-${state.serverVersion}` };
        env.latestState.current = fixture(state.localBase, state.localVersion, state.contextKind);
        const table = env.latestState.current.tables[0];
        env.populationExclusionContextRef.current = { contextKey: ['snapshot', 'output', 'root'].includes(state.contextKind) ? 'context-b' : 'context-a', client: state.contextKind === 'client' ? {} : client, baseSelectionID: `selection-${state.localBase}`, bindingKey: JSON.stringify([table.document.population.selectionRevisionId, table.document.population.route.map(step => [step.resourceType, step.relationship, step.catalogEdgeId ?? '', step.storageDirection ?? ''])]) };
    };
    for (const edge of schedule) {
        semanticState = edge.to;
        if (edge.action === 'CreationReturns') { create.resolve(variant); await settle(() => routes.length || completed, 'first route or stop'); }
        else if (edge.action === 'RoutePageReturns') {
            const request = routes[routeIndex++];
            if (request) request.resolve(routeResponse(edge.to.exclusionPhase !== 'route-two', edge.to.exclusionPhase === 'route-two'));
            await settle(() => completed || queueWaiting || submissions.length || routes.length > routeIndex, 'route continuation');
        } else if (edge.action === 'DrainReturnsAndSubmit') { drainResolved = true; drain.resolve(); await settle(() => completed || submissions.length, 'queue admission'); }
        else applyState(edge.to);
        if (error) throw error;
    }
    for (let turn = 0; turn < 4 && !completed && !submissions.length; turn++) {
        const waiting = routes[routeIndex];
        if (waiting) { routeIndex++; outsideModelContinuations++; waiting.resolve(routeResponse(true)); }
        else if (queueWaiting && !drainResolved) { outsideModelContinuations++; drainResolved = true; drain.resolve(); }
        else break;
        await settle(() => completed || submissions.length || queueWaiting || routes.length > routeIndex, 'observable forbidden follow-up');
        for (let tick = 0; tick < 5; tick++) await Promise.resolve();
    }
    if (error) throw error;
    const expected = schedule.at(-1).to, actual = submissions[0];
    if (actual) {
        const table = actual.state.tables[0];
        const match = actual.host && actual.context.contextKey === 'context-a' && actual.context.client === client && actual.context.baseSelectionID === 'selection-A' && actual.context.bindingKey === JSON.stringify(['selection-A', []]) && table.outputId === 'patients' && table.document.rootResourceType === 'Patient' && table.document.population.selectionRevisionId === 'selection-A' && table.document.population.route.length === 0 && actual.state.draftVersion === actual.server.version && actual.state.draftDigest === actual.server.digest && actual.semantic.serverBase === 'A';
        if (!match) throw propertyFailure(exclusionProperties[0], schedule, { submissions: submissions.map(value => value.args) });
    }
    if (current(expected) && expected.serverBase === 'A' && !actual) throw propertyFailure(exclusionProperties[1], schedule, { submissions: [] });
    if (actual) assert.ok(actual.drainResolved, 'FORMAL_CONFORMANCE: Apply moved before its drain boundary');
    assert.equal(submissions.length, expected.submitted ? 1 : 0, 'FORMAL_CONFORMANCE: submission count');
    assert.equal(routes.length, expected.routeCalls, 'FORMAL_CONFORMANCE: route requests');
    assert.equal(created, 1, 'FORMAL_CONFORMANCE: creation invocation count');
    assert.deepEqual(createRequests[0].source, { kind: 'selectionRevision', selectionRevision: { selectionRevisionId: 'selection-A' } }, 'FORMAL_CONFORMANCE: wrong variant source');
    assert.deepEqual(createRequests[0].exclusions, variant.exclusions);
    assert.deepEqual({ project: createRequests[0].project, explorer: createRequests[0].explorerId, snapshot: createRequests[0].snapshotToken }, { project: 'project-a', explorer: 'explorer-a', snapshot: 'snapshot-a' }, 'FORMAL_CONFORMANCE: creation request binding');
    for (const [index, args] of routeRequests.entries()) {
        assert.deepEqual({ project: args.project, explorer: args.explorerId, snapshot: args.snapshotToken, output: args.outputId, selection: args.selectionRevisionId, cursor: args.cursor }, { project: 'project-a', explorer: 'explorer-a', snapshot: 'snapshot-a', output: 'patients', selection: 'variant-A', cursor: index === 0 ? undefined : 'page-two' }, 'FORMAL_CONFORMANCE: route request binding');
    }
    if (actual) {
        assert.equal(actual.args.expectedDraftVersion, expected.serverVersion, 'FORMAL_CONFORMANCE: wrong current CAS');
        assert.equal(actual.args.expectedDraftDigest, `draft-${expected.serverVersion}`);
        assert.deepEqual(actual.args.commands, [{ type: 'SET_TABLE_POPULATION', outputId: 'patients', selectionRevisionId: 'variant-A', routeChoiceId: 'route-A' }]);
    }
    return { submitted: !!actual, version: actual?.args.expectedDraftVersion, routeCalls: routes.length, pendingAtSubmission: pending, outsideModelContinuations };
}
function propertyFailure(property, schedule, observed) { const error = new Error(`${property}: ${key(observed)}`); Object.assign(error, { code: 'FORMAL_PROPERTY', property, witness: { schemaVersion: 1, kind: 'population-exclusion', model: exclusionModel, property, schedule, observed } }); return error; }
export async function checkExclusionGraph(graph, { workspaceSource, output, witnessFile } = {}) {
    validateExclusionGraph(graph, { workspaceSource }); const before = Object.fromEntries(productionFiles.map(file => [file, digest(join(root, file))])); const implementation = await loadExclusionImplementation({ workspaceSource }), schedules = modelPaths(graph), results = [];
    for (const schedule of schedules) try { results.push(await replayExclusionSchedule(schedule, { implementation })); } catch (error) { if (error.witness && witnessFile) writeFileSync(witnessFile, JSON.stringify(error.witness, null, 2)); throw error; }
    assert.deepEqual(Object.fromEntries(productionFiles.map(file => [file, digest(join(root, file))])), before, 'Production changed during exclusion replay');
    const result = { states: graph.states.length, transitions: graph.steps.length, edgeSchedules: schedules.length, submitted: results.filter(v => v.submitted).length, freshCAS2: results.filter(v => v.version === 2).length, boundary: 'actual exclusion callback and actual queued Apply callback through outgoing request submission; one operation, two route pages and one context/base/detachment/unrelated-save/hydration change; controlled refs/client/pause, no React reachability, ACK, HTTP or database commit proof' };
    if (output) { mkdirSync(output, { recursive: true }); writeFileSync(join(output, 'replay.json'), JSON.stringify(result, null, 2)); } return result;
}
const guardedDrain = 'admitted == CurrentIntent';
export const exclusionModelControls = [
    [exclusionProperties[0], 'DrainReturnsAndSubmit', guardedDrain, 'admitted == TRUE'],
    [exclusionProperties[0], 'DrainReturnsAndSubmit', guardedDrain, 'admitted == hostAttached /\\ contextKind = "same" /\\ localVersion = serverVersion'],
    [exclusionProperties[0], 'DrainReturnsAndSubmit', guardedDrain, 'admitted == hostAttached /\\ localBase = "A" /\\ localVersion = serverVersion'],
    [exclusionProperties[0], 'DrainReturnsAndSubmit', guardedDrain, 'admitted == contextKind = "same" /\\ localBase = "A" /\\ localVersion = serverVersion'],
    [exclusionProperties[0], 'DrainReturnsAndSubmit', guardedDrain, 'admitted == hostAttached /\\ contextKind = "same" /\\ localBase = "A"'],
    [exclusionProperties[1], 'DrainReturnsAndSubmit', guardedDrain, 'admitted == FALSE'],
];
export const exclusionSourceProofs = [
    { model: exclusionModel, property: exclusionProperties[0], kind: 'exclusion-replay', branch: 'base-binding', edits: [
        [workspaceFile, 'latestContext.baseSelectionID === baseSelection.id &&', 'true &&'],
        [workspaceFile, 'latestContext.bindingKey === exclusionContext.bindingKey &&', 'true &&'],
        [workspaceFile, 'populationBindingKey(latestTable) === exclusionContext.bindingKey &&', 'true &&'],
    ] },
    { model: exclusionModel, property: exclusionProperties[0], kind: 'exclusion-replay', branch: 'queue-admission', edits: [[workspaceFile, 'if (admitCommand && !admitCommand()) return undefined;', '']] },
    { model: exclusionModel, property: exclusionProperties[0], kind: 'exclusion-replay', branch: 'context', edits: [[workspaceFile, 'latestContext.contextKey === exclusionContext.contextKey &&', 'true &&']] },
    { model: exclusionModel, property: exclusionProperties[0], kind: 'exclusion-replay', branch: 'detached-host', edits: [[workspaceFile, 'return workspaceAttached.current &&\n        latestContext.contextKey', 'return true &&\n        latestContext.contextKey']] },
    { model: exclusionModel, property: exclusionProperties[0], kind: 'exclusion-replay', branch: 'hydration-coherence', edits: [[workspaceFile, 'latest.draftVersion === serverDraft.current.version &&\n        latest.draftDigest === serverDraft.current.digest;', 'true;']] },
    { model: exclusionModel, property: exclusionProperties[1], kind: 'exclusion-replay', branch: 'reject-current', edits: [[workspaceFile, 'if (!currentTable || !baseSelection) return;', 'if (true || !currentTable || !baseSelection) return;']] },
];
function validateWitness(value) {
    assert.deepEqual(Object.keys(value).filter(name => name !== 'observed').sort(), ['schemaVersion', 'kind', 'model', 'property', 'schedule'].sort());
    assert.equal(value.schemaVersion, 1); assert.equal(value.kind, 'population-exclusion'); assert.equal(value.model, exclusionModel); assert.ok(exclusionProperties.includes(value.property));
    assert.ok(Array.isArray(value.schedule) && value.schedule.length > 0);
    let previous = initialExclusionState();
    for (const edge of value.schedule) { validateStateShape(edge.to); assert.deepEqual(Object.keys(edge).sort(), ['action', 'from', 'to']); assert.deepEqual(edge.from, previous); assert.ok(nextStates(previous).some(next => next.action === edge.action && key(next.vars) === key(edge.to)), 'Unsupported witness action/state'); previous = edge.to; }
    assert.equal(previous.exclusionPhase, 'done');
    assert.equal(value.property, current(previous) && previous.serverBase === 'A' ? exclusionProperties[1] : exclusionProperties[0], 'Witness property does not describe its actual current-intent input');
    return value;
}
export async function replayExclusionWitness(witness, { workspaceSource } = {}) {
    validateWitness(witness);
    try { const observation = await replayExclusionSchedule(witness.schedule, { workspaceSource }); return { status: 'property-held', property: witness.property, observation }; }
    catch (error) { if (error.code !== 'FORMAL_PROPERTY') throw error; assert.equal(error.property, witness.property, 'Counterexample violates a different property'); return { status: 'reproduced', ...error.witness }; }
}
export async function checkExclusionSourceProof(proof, graph, output) {
    assert.deepEqual(proof, exclusionSourceProofs.find(value => value.branch === proof.branch)); mkdirSync(output, { recursive: true });
    const original = source(workspaceFile); let broken = original;
    for (const [file, before, after] of proof.edits) { assert.equal(file, workspaceFile); assert.equal(broken.split(before).length, 2, 'Nonunique exclusion fault anchor'); broken = broken.replace(before, after); }
    await checkExclusionGraph(graph, { output: join(output, 'positive') });
    let caught;
    try { await checkExclusionGraph(graph, { workspaceSource: broken, witnessFile: join(output, 'counterexample.json') }); } catch (error) { caught = error; }
    assert.ok(caught && caught.code === 'FORMAL_PROPERTY' && caught.property === proof.property, `Source fault did not reach named actual effect: ${caught?.stack}`);
    assert.equal((await replayExclusionWitness(caught.witness)).status, 'property-held');
    assert.equal((await replayExclusionWitness(caught.witness, { workspaceSource: broken })).status, 'reproduced');
    await checkExclusionGraph(graph, { output: join(output, 'restored') });
    assert.equal(source(workspaceFile), original, 'Live production source changed');
    const result = { model: exclusionModel, property: proof.property, kind: proof.kind, branch: proof.branch, sourceFiles: [workspaceFile], positive: true, negative: true, restored: true, witness: 'counterexample.json' }; writeFileSync(join(output, 'proof.json'), JSON.stringify(result, null, 2)); return result;
}
export function exclusionCounterexampleWitness(counterexample) {
    assert.ok(counterexample && exclusionProperties.includes(counterexample.property)); assert.ok(Array.isArray(counterexample.states) && counterexample.states.length > 1);
    assert.equal(counterexample.states[0].action, 'Initial'); assert.deepEqual(counterexample.states[0].vars, initialExclusionState());
    const schedule = []; let previous = initialExclusionState();
    for (let index = 1; index < counterexample.states.length; index++) {
        const row = counterexample.states[index];
        if (index < counterexample.states.length - 1) { const next = nextStates(previous).find(next => next.action === row.action && key(next.vars) === key(row.vars)); assert.ok(next, 'Unsupported counterexample prefix'); schedule.push({ action: row.action, from: previous, to: next.vars }); previous = next.vars; continue; }
        assert.equal(row.action, 'DrainReturnsAndSubmit', 'Counterexample must reach the modeled queued submission boundary');
        const healthy = nextStates(previous).find(next => next.action === row.action); assert.ok(healthy); assert.deepEqual({ ...row.vars, submitted: healthy.vars.submitted }, healthy.vars, 'Counterexample changes unrelated state');
        const bad = row.vars, candidates = [true, bad.hostAttached && bad.contextKind === 'same' && bad.localVersion === bad.serverVersion, bad.hostAttached && bad.localBase === 'A' && bad.localVersion === bad.serverVersion, bad.contextKind === 'same' && bad.localBase === 'A' && bad.localVersion === bad.serverVersion, bad.hostAttached && bad.contextKind === 'same' && bad.localBase === 'A'];
        if (counterexample.property === exclusionProperties[0]) { assert.equal(bad.submitted, true); assert.ok(!(current(bad) && bad.serverBase === 'A'), 'Counterexample does not violate current-intent submission'); assert.ok(candidates.includes(bad.submitted)); }
        else { assert.equal(bad.submitted, false); assert.ok(current(bad) && bad.serverBase === 'A', 'Counterexample does not violate current completion'); }
        assert.notEqual(bad.submitted, healthy.vars.submitted, 'No supported model fault'); schedule.push({ action: row.action, from: previous, to: healthy.vars });
    }
    return validateWitness({ schemaVersion: 1, kind: 'population-exclusion', model: exclusionModel, property: counterexample.property, schedule });
}
export async function checkExclusionGraphControls(graph) {
    const mutations = [v => v.states.pop(), v => v.steps.pop(), v => v.steps.push({ ...v.steps[0] }), v => { v.steps[0].action = 'Unknown'; }, v => { v.steps[0].to = 'missing'; }, v => { v.modelSha256 = 'stale'; }, v => { v.productionSha256[workspaceFile] = 'stale'; }, v => { v.states.find(s => s.vars.exclusionPhase === 'done').vars.submitted = 'yes'; }];
    for (const mutate of mutations) { const changed = structuredClone(graph); mutate(changed); assert.throws(() => validateExclusionGraph(changed)); } return mutations.length;
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
    const [command, file, extra] = process.argv.slice(2);
    if (command === '--generate') { assert.ok(!extra || extra === '--self-test'); assert.ok(!existsSync(file) || readdirSync(file).length === 0, 'Output must be empty'); mkdirSync(file, { recursive: true }); const graph = await exclusionGraph(join(file, 'graph.json')); const replay = await checkExclusionGraph(graph, { output: join(file, 'positive'), witnessFile: join(file, 'counterexample.json') }); replay.graphControls = await checkExclusionGraphControls(graph); if (extra) { replay.sourceControls = []; for (const proof of exclusionSourceProofs) replay.sourceControls.push(await checkExclusionSourceProof(proof, graph, join(file, proof.branch))); } writeFileSync(join(file, 'replay.json'), JSON.stringify(replay, null, 2)); console.log(JSON.stringify(replay)); }
    else if (command === '--witness') { const record = JSON.parse(readFileSync(file, 'utf8')), witness = record.counterexamples ? exclusionCounterexampleWitness(record.counterexamples.find(v => exclusionProperties.includes(v.property))) : record; const result = await replayExclusionWitness(witness, { workspaceSource: extra === undefined ? undefined : readFileSync(extra, 'utf8') }); console.log(JSON.stringify(result)); process.exitCode = result.status === 'reproduced' ? 1 : 0; }
    else throw new Error('Use --generate DIRECTORY [--self-test] or --witness FILE [SCRATCH_WORKSPACE_SOURCE]');
}
