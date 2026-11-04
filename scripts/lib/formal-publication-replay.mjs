import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, existsSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { stripTypeScriptTypes } from 'node:module';
import { root, tools, digest } from './formal-tools.mjs';
import { verificationTimeoutMs } from './verification-budget.mjs';

export const publicationModel = 'PublicationAcknowledgement';
export const publicationProperties = ['Inv_OnlyMatchingPublicationAcknowledged', 'Inv_MatchingPublicationSurvivesPreviewRecompile'];
export const workspaceFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx';
const recoveryFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/reconcileRecovery.ts';
const authoringFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/model.ts';
const productionFiles = [workspaceFile, recoveryFile, authoringFile];
const modelDirectory = join(root, 'verification/builder-interactions');
const fields = ['workspaceAttached', 'ownerMatches', 'explorerMatches', 'draftDigestMatches', 'intentMatches', 'snapshotMatches', 'previewRecompiled'];
const source = file => readFileSync(join(root, file), 'utf8');
const tsModule = value => `data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(value)).toString('base64')}`;
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
const key = value => JSON.stringify(canonical(value));
const inputs = () => Array.from({ length: 128 }, (_, mask) => Object.fromEntries(fields.map((field, i) => [field, Boolean(mask & 1 << i)])));
export const publicationModelControls = [
    [publicationProperties[0], 'ReceiveAcknowledgement', "accepted' = Matching(acknowledgementInput)", "accepted' = TRUE"],
    [publicationProperties[1], 'ReceiveAcknowledgement', "accepted' = Matching(acknowledgementInput)", "accepted' = (Matching(acknowledgementInput) /\\ ~acknowledgementInput.previewRecompiled)"],
];
export function validatePublicationInput(input) {
    assert.ok(input && typeof input === 'object' && !Array.isArray(input), 'Missing publication input');
    assert.deepEqual(Object.keys(input).sort(), [...fields].sort(), 'Unknown or missing publication input');
    for (const field of fields) assert.equal(typeof input[field], 'boolean', `Invalid publication input ${field}`);
    return input;
}
export function expectedPublicationAcknowledgement(input) {
    validatePublicationInput(input);
    return fields.filter(field => field !== 'previewRecompiled').every(field => input[field]);
}
const initial = () => ({ acknowledgementPhase: 'choose', acknowledgementInput: Object.fromEntries(fields.map(field => [field, field !== 'previewRecompiled'])), accepted: false });
function validateState(value) {
    assert.ok(value && typeof value === 'object' && !Array.isArray(value));
    assert.deepEqual(Object.keys(value).sort(), ['acknowledgementPhase', 'acknowledgementInput', 'accepted'].sort());
    assert.ok(['choose', 'pending', 'acknowledged'].includes(value.acknowledgementPhase));
    validatePublicationInput(value.acknowledgementInput); assert.equal(typeof value.accepted, 'boolean');
    if (value.acknowledgementPhase !== 'acknowledged') assert.equal(value.accepted, false);
}
function checkedWorkspaceSource(value) {
    if (value === undefined) return source(workspaceFile);
    assert.equal(typeof value, 'string', 'Publication source override must be a string');
    assert.ok(value.trim().length > 0, 'Publication source override must be nonempty');
    return value;
}
export function extractPublicationCallback(text) {
    const anchor = '  const executePublish = useCallback(\n    ';
    assert.equal(text.split(anchor).length, 2, 'Expected one production publication callback');
    const start = text.indexOf(anchor) + anchor.length, end = text.indexOf('\n    },\n    [\n', start);
    assert.ok(end > start, 'Missing publication callback boundary');
    const callback = text.slice(start, end) + '\n    }';
    assert.ok(callback.startsWith('async ('), 'Publication callback is no longer async');
    assert.match(text.slice(end), /^\n    },\n    \[\n      authResourcePath,\n      dispatch,\n      ownerKey,\n      projectId,\n      publishBuilder,\n      reconcileCurrent,\n      refetchBuilder,\n      syncBuilderData,\n    \],\n  \);/, 'Publication closure dependencies changed');
    const stripped = stripTypeScriptTypes(`const callback = ${callback};`);
    return stripped.slice('const callback = '.length, -1);
}
export async function loadPublicationImplementation({ workspaceSource } = {}) {
    const text = checkedWorkspaceSource(workspaceSource);
    const [recovery, authoring] = await Promise.all([import(tsModule(source(recoveryFile))), import(tsModule(source(authoringFile)))]);
    const names = ['latestState', 'compileGeneration', 'projectId', 'authResourcePath', 'ownerKey', 'publicationOwnerKeyRef', 'workspaceAttached', 'publishBuilder', 'setLastPublished', 'dispatch', 'setMessage', 'refetchBuilder', 'builderAuthoringReducer', 'syncBuilderData', 'reconcileCurrent', 'reconcileResultIsCurrent', 'isStaleSnapshot', 'intentFingerprint', 'workspaceFromState'];
    const factory = new Function(...names, `"use strict"; return (${extractPublicationCallback(text)});`);
    return dependencies => factory(...names.map(name => Object.hasOwn(dependencies, name) ? dependencies[name] : ({ ...recovery, ...authoring })[name]));
}
function fixture(explorer, intent, digest, snapshot) {
    return { explorerId: explorer, draftDigest: digest, catalog: { snapshotToken: snapshot }, workspace: { explorer: { title: intent } }, tables: [{ outputId: 'patients', tabId: 'patients-tab', title: 'Patients', document: { rootResourceType: 'Patient', output: { id: 'patients', title: 'Patients' }, columns: [] } }] };
}
export async function replayPublicationInput(input, { implementation, workspaceSource, rename = false } = {}) {
    validatePublicationInput(input); assert.equal(typeof rename, 'boolean');
    if (workspaceSource !== undefined) checkedWorkspaceSource(workspaceSource);
    if (implementation !== undefined) { assert.equal(typeof implementation, 'function'); assert.equal(workspaceSource, undefined, 'Do not mix loaded implementation and explicit publication source'); }
    const owner = rename ? 'owner-B' : 'owner-A', explorer = rename ? 'explorer-B' : 'explorer-A';
    const intent = rename ? 'intent-B' : 'intent-A', draft = rename ? 'draft-B' : 'draft-A', snapshot = rename ? 'snapshot-B' : 'snapshot-A';
    const latestState = { current: fixture(explorer, intent, draft, snapshot) }, compileGeneration = { current: 1 }, publicationOwnerKeyRef = { current: owner };
    let resolvePublish, requests = 0;
    const pending = new Promise(resolve => { resolvePublish = resolve; }), events = [], workspaceAttached = { current: true };
    const unsupported = () => { throw new Error('Publication acknowledgement replay reached an unmodeled recovery path'); };
    const callback = (implementation || await loadPublicationImplementation({ workspaceSource }))({ latestState, compileGeneration, publicationOwnerKeyRef, workspaceAttached, projectId: 'project-fixture', authResourcePath: '/fixture', ownerKey: owner,
        publishBuilder: request => { requests++; assert.deepEqual(request, { project: 'project-fixture', explorerId: explorer, authResourcePath: '/fixture', receiptId: 'receipt-fixture' }); return { unwrap: () => pending }; },
        setLastPublished: marker => events.push({ kind: 'marker', marker }), dispatch: event => events.push({ kind: 'dispatch', event }), setMessage: message => events.push({ kind: 'message', ...(message === undefined ? {} : { message }) }),
        refetchBuilder: unsupported, builderAuthoringReducer: unsupported, syncBuilderData: unsupported, reconcileCurrent: unsupported,
    });
    const running = callback('receipt-fixture'); assert.equal(requests, 1, 'Publication did not reach its pending request');
    workspaceAttached.current = input.workspaceAttached;
    if (!input.ownerMatches) publicationOwnerKeyRef.current = 'foreign-owner';
    if (!input.explorerMatches) latestState.current = { ...latestState.current, explorerId: 'foreign-explorer' };
    if (!input.draftDigestMatches) latestState.current = { ...latestState.current, draftDigest: 'foreign-draft' };
    if (!input.intentMatches) latestState.current = { ...latestState.current, workspace: { ...latestState.current.workspace, explorer: { ...latestState.current.workspace.explorer, title: 'foreign-intent' } } };
    if (!input.snapshotMatches) latestState.current = { ...latestState.current, catalog: { ...latestState.current.catalog, snapshotToken: 'foreign-snapshot' } };
    if (input.previewRecompiled) compileGeneration.current++;
    resolvePublish({}); await running;
    assert.equal(requests, 1, 'Acknowledgement caused another publication request');
    const expected = expectedPublicationAcknowledgement(input), expectedEvents = expected ? [{ kind: 'marker', marker: { ownerKey: owner, draftDigest: draft } }, { kind: 'dispatch', event: { type: 'published' } }, { kind: 'message' }] : [];
    if (key(events) !== key(expectedEvents)) {
        const property = publicationProperties[expected ? 1 : 0];
        const witness = { schemaVersion: 1, kind: 'publication-acknowledgement', model: publicationModel, property, input, rename, expectedAccepted: expected, actualEvents: events };
        const error = new Error(`${property}: expected ${key(expectedEvents)}, got ${key(events)}`);
        Object.assign(error, { code: 'FORMAL_PROPERTY', property, witness }); throw error;
    }
    return { status: 'property-held', accepted: expected, events };
}
export async function publicationGraph(output) {
    const { scripts, jar } = tools(), directory = mkdtempSync(join(tmpdir(), 'loom-publication-graph-'));
    try {
        const result = spawnSync('java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC', '-workers', '1', '-metadir', join(directory, 'states'), '-config', `${publicationModel}.cfg`, '-dump', 'dot,actionlabels', join(directory, 'graph'), `${publicationModel}.tla`], { cwd: modelDirectory, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024 });
        assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr);
        const [{ parseDot }, { json }] = await Promise.all([import(pathToFileURL(join(scripts, 'frontends/dot.ts')).href), import(pathToFileURL(join(scripts, 'frontends/view.ts')).href)]);
        const graph = parseDot(readFileSync(join(directory, 'graph.dot'), 'utf8'));
        const record = { schemaVersion: 1, model: publicationModel, modelSha256: digest(join(modelDirectory, `${publicationModel}.tla`)), configSha256: digest(join(modelDirectory, `${publicationModel}.cfg`)), productionSha256: Object.fromEntries(productionFiles.map(file => [file, digest(join(root, file))])), states: [...graph.states].map(([id, state]) => ({ id, start: state.start, vars: Object.fromEntries(Object.entries(state.vars).map(([key, value]) => [key, json(value)])) })), steps: graph.steps };
        writeFileSync(output, JSON.stringify(record)); return record;
    } finally { rmSync(directory, { recursive: true, force: true }); }
}
export function validatePublicationGraph(graph, { workspaceSource } = {}) {
    if (workspaceSource !== undefined) checkedWorkspaceSource(workspaceSource);
    assert.equal(graph.schemaVersion, 1); assert.equal(graph.model, publicationModel);
    assert.equal(graph.modelSha256, digest(join(modelDirectory, `${publicationModel}.tla`)), 'Stale publication model');
    assert.equal(graph.configSha256, digest(join(modelDirectory, `${publicationModel}.cfg`)), 'Stale publication config');
    assert.deepEqual(Object.keys(graph.productionSha256).sort(), [...productionFiles].sort());
    for (const file of productionFiles) if (!(workspaceSource !== undefined && file === workspaceFile)) assert.equal(graph.productionSha256[file], digest(join(root, file)), `Stale publication source ${file}`);
    assert.ok(Array.isArray(graph.states) && graph.states.length === 257, 'Incomplete publication frontier');
    assert.ok(Array.isArray(graph.steps) && graph.steps.length === 256, 'Incomplete publication edges');
    const states = new Map(), semantics = new Map();
    for (const state of graph.states) { assert.ok(typeof state.id === 'string'); assert.equal(typeof state.start, 'boolean'); validateState(state.vars); assert.ok(!states.has(state.id), 'Duplicate publication state'); assert.ok(!semantics.has(key(state.vars)), 'Duplicate publication semantic state'); states.set(state.id, state); semantics.set(key(state.vars), state); }
    const start = graph.states.filter(state => state.start); assert.equal(start.length, 1); assert.deepEqual(start[0].vars, initial());
    const edges = new Set();
    for (const step of graph.steps) { assert.deepEqual(Object.keys(step).sort(), ['action', 'from', 'to']); assert.ok(states.has(step.from) && states.has(step.to), 'Dangling publication edge'); assert.ok(['ChooseAcknowledgement', 'ReceiveAcknowledgement'].includes(step.action), 'Unknown publication action'); const k = key(step); assert.ok(!edges.has(k), 'Duplicate publication edge'); edges.add(k); }
    const expectedEdges = new Set(), checked = [];
    for (const input of inputs()) {
        const candidate = semantics.get(key({ acknowledgementPhase: 'pending', acknowledgementInput: input, accepted: false })), accepted = expectedPublicationAcknowledgement(input);
        const end = semantics.get(key({ acknowledgementPhase: 'acknowledged', acknowledgementInput: input, accepted }));
        assert.ok(candidate && end, 'Missing publication input classification'); expectedEdges.add(key({ from: start[0].id, action: 'ChooseAcknowledgement', to: candidate.id })); expectedEdges.add(key({ from: candidate.id, action: 'ReceiveAcknowledgement', to: end.id })); checked.push(input);
    }
    assert.deepEqual([...edges].sort(), [...expectedEdges].sort(), 'Missing or invalid publication transition'); return checked;
}
export async function checkPublicationGraph(graph, { workspaceSource, witnessFile } = {}) {
    const cases = validatePublicationGraph(graph, { workspaceSource }), implementation = await loadPublicationImplementation({ workspaceSource });
    let replays = 0, accepted = 0, rejected = 0;
    for (const input of cases) for (const rename of [false, true]) {
        try { const result = await replayPublicationInput(input, { implementation, rename }); replays++; if (result.accepted) accepted++; else rejected++; }
        catch (error) { if (error.witness && witnessFile) writeFileSync(witnessFile, JSON.stringify(error.witness, null, 2)); throw error; }
    }
    assert.ok(accepted > 0 && rejected > 0, 'Vacuous publication replay');
    for (const file of productionFiles) if (!(workspaceSource !== undefined && file === workspaceFile)) assert.equal(graph.productionSha256[file], digest(join(root, file)), `Publication source changed during replay ${file}`);
    return { states: graph.states.length, transitions: graph.steps.length, modelCases: cases.length, replays, accepted, rejected, boundary: 'actual extracted async executePublish success acknowledgement; controlled deferred response/current refs, real identity helpers; no React/full publication/recovery-branch proof' };
}
export const publicationSourceProofs = [
    { model: publicationModel, property: publicationProperties[0], kind: 'publication-replay', branch: 'acknowledgement-context', edits: [[workspaceFile, 'if (!acknowledgementIsCurrent()) return;', '']] },
    { model: publicationModel, property: publicationProperties[0], kind: 'publication-replay', branch: 'detached-acknowledgement', edits: [[workspaceFile, 'workspaceAttached.current && publicationOwnerKeyRef.current === ownerKey', 'publicationOwnerKeyRef.current === ownerKey']] },
    { model: publicationModel, property: publicationProperties[1], kind: 'publication-replay', branch: 'unrelated-preview-generation', edits: [[workspaceFile, 'if (!acknowledgementIsCurrent()) return;', 'if (!acknowledgementIsCurrent() || generation !== compileGeneration.current) return;']] },
];
export async function checkPublicationSourceProof(proof, graph, output) {
    assert.deepEqual(proof, publicationSourceProofs.find(row => row.branch === proof.branch), 'Unknown publication source proof'); mkdirSync(output, { recursive: true });
    const original = source(workspaceFile); let broken = original;
    for (const [file, before, after] of proof.edits) { assert.equal(file, workspaceFile); assert.equal(broken.split(before).length, 2, 'Nonunique publication source fault'); broken = broken.replace(before, after); }
    await checkPublicationGraph(graph); const witnessFile = join(output, 'counterexample.json');
    await assert.rejects(() => checkPublicationGraph(graph, { workspaceSource: broken, witnessFile }), error => error.code === 'FORMAL_PROPERTY' && error.property === proof.property);
    const witness = JSON.parse(readFileSync(witnessFile, 'utf8')); assert.equal((await replayPublicationWitness(witness)).status, 'property-held'); assert.equal((await replayPublicationWitness(witness, { workspaceSource: broken })).status, 'reproduced'); await checkPublicationGraph(graph);
    const evidence = { model: publicationModel, property: proof.property, kind: proof.kind, branch: proof.branch, sourceFiles: [workspaceFile], positive: true, negative: true, restored: true, witness: 'counterexample.json' }; writeFileSync(join(output, 'proof.json'), JSON.stringify(evidence, null, 2)); return evidence;
}
export function publicationCounterexampleWitness(counterexample) {
    assert.ok(counterexample && publicationProperties.includes(counterexample.property), 'Unknown publication counterexample');
    assert.ok(Array.isArray(counterexample.states) && counterexample.states.length === 3, 'Expected publication wait and return');
    const [start, pending, done] = counterexample.states; assert.deepEqual(start.vars, initial()); assert.equal(pending.action, 'ChooseAcknowledgement'); assert.equal(done.action, 'ReceiveAcknowledgement'); validateState(pending.vars); validateState(done.vars); assert.equal(pending.vars.acknowledgementPhase, 'pending'); assert.equal(done.vars.acknowledgementPhase, 'acknowledged'); assert.deepEqual(pending.vars.acknowledgementInput, done.vars.acknowledgementInput);
    const input = done.vars.acknowledgementInput, expectedAccepted = expectedPublicationAcknowledgement(input); assert.notEqual(done.vars.accepted, expectedAccepted, 'Counterexample does not violate acceptance'); assert.equal(counterexample.property, publicationProperties[expectedAccepted ? 1 : 0]);
    return { schemaVersion: 1, kind: 'publication-acknowledgement', model: publicationModel, property: counterexample.property, input, rename: false, expectedAccepted };
}
export async function replayPublicationWitness(witness, options = {}) {
    assert.equal(witness.schemaVersion, 1); assert.equal(witness.kind, 'publication-acknowledgement'); assert.equal(witness.model, publicationModel); validatePublicationInput(witness.input); assert.equal(typeof witness.rename, 'boolean'); const expectedAccepted = expectedPublicationAcknowledgement(witness.input); assert.equal(witness.expectedAccepted, expectedAccepted); assert.equal(witness.property, publicationProperties[expectedAccepted ? 1 : 0]);
    try { return await replayPublicationInput(witness.input, { ...options, rename: witness.rename }); }
    catch (error) { if (error.code !== 'FORMAL_PROPERTY') throw error; return { status: 'reproduced', ...error.witness }; }
}
export async function checkPublicationGraphControls(graph) {
    const changes = [g => g.states.pop(), g => g.steps.pop(), g => g.steps.push({ ...g.steps[0] }), g => { g.steps[0].action = 'Unknown'; }, g => { g.steps[0].to = 'missing'; }, g => { g.modelSha256 = 'stale'; }, g => { g.productionSha256[workspaceFile] = 'stale'; }, g => { g.states.find(row => row.vars.acknowledgementPhase === 'acknowledged').vars.accepted = !g.states.find(row => row.vars.acknowledgementPhase === 'acknowledged').vars.accepted; }];
    for (const change of changes) { const bad = structuredClone(graph); change(bad); assert.throws(() => validatePublicationGraph(bad)); }
    return changes.length;
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
    const [command, file, extra] = process.argv.slice(2);
    if (command === '--generate') { assert.ok(!extra || extra === '--self-test'); assert.ok(!existsSync(file) || readdirSync(file).length === 0, 'Publication output must be empty'); mkdirSync(file, { recursive: true }); const graph = await publicationGraph(join(file, 'graph.json')); const replay = await checkPublicationGraph(graph, { witnessFile: join(file, 'counterexample.json') }); if (extra) { replay.graphControls = await checkPublicationGraphControls(graph); replay.sourceControls = []; for (const proof of publicationSourceProofs) replay.sourceControls.push(await checkPublicationSourceProof(proof, graph, join(file, proof.branch))); } writeFileSync(join(file, 'replay.json'), JSON.stringify(replay, null, 2)); console.log(JSON.stringify(replay)); }
    else if (command === '--witness') { const record = JSON.parse(readFileSync(file, 'utf8')), witness = record.counterexamples ? publicationCounterexampleWitness(record.counterexamples.find(value => publicationProperties.includes(value.property))) : record; const result = await replayPublicationWitness(witness, extra !== undefined ? { workspaceSource: readFileSync(extra, 'utf8') } : {}); console.log(JSON.stringify(result)); process.exitCode = result.status === 'reproduced' ? 1 : 0; }
    else throw new Error('Use --generate DIR [--self-test] or --witness FILE [SCRATCH_WORKSPACE_SOURCE]');
}
