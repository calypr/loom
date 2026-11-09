import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, readdirSync, existsSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { root, tools, digest } from './formal-tools.mjs';
import { apiDoctor, checkApiEvents } from './formal-api.mjs';
import { verificationTimeoutMs, goTestTimeout } from './verification-budget.mjs';

export const removalModel = 'PopulationRemoval';
export const removalProperties = ['Inv_RemovalApplyRequiresExactReceipt', 'Inv_RemovalRequiresExactCandidate', 'Inv_RemovalFailureKeepsCollection', 'Inv_RemovalCannotOverwriteCompetingDraft', 'Inv_ValidRemovalChangesOnlyPopulationRevision'];
export const removalFile = 'internal/explorer/lifecycle/population_member_proposal.go';
export const removalAdapter = 'internal/explorer/lifecycle/population_removal_formal_replay_test.go';
const modelDirectory = join(root, 'verification/builder-interactions');
const testName = 'TestPopulationRemovalModelReplay';
const fields = ['receiptMatches', 'provenanceMatches', 'authorizationMatches', 'previewCompletes', 'concurrentDraftChange'];
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(name => [name, canonical(value[name])])) : value;
const key = value => JSON.stringify(canonical(value));
const files = () => [...new Set(['go.mod', 'go.sum', removalAdapter,
    ...['internal/explorer/lifecycle', 'internal/explorer', 'internal/projectid', 'internal/explorer/capability', 'internal/explorer/authoringv2', 'internal/authscope'].flatMap(directory => readdirSync(join(root, directory)).filter(file => file.endsWith('.go') && (directory === 'internal/explorer/lifecycle' || !file.endsWith('_test.go'))).map(file => `${directory}/${file}`))])].sort();
const hashes = () => Object.fromEntries(files().map(file => [file, digest(join(root, file))]));
const observedFields = ['accepted', 'previewCalls', 'saveAttempts', 'successfulSaves', 'preservedHead', 'exactCandidate', 'conflict'];
const initial = () => ({ removalPhase: 'choose', removalInput: Object.fromEntries(fields.map(field => [field, true])), accepted: false, previewCalls: 0, saveAttempts: 0, successfulSaves: 0, preservedHead: true, exactCandidate: false, conflict: false });
export const removalModelControls = [
    [removalProperties[0], 'ApplyRemoval', 'trusted == Trusted(removalInput)', 'trusted == removalInput.provenanceMatches /\\ removalInput.authorizationMatches'],
    [removalProperties[1], 'ApplyRemoval', 'trusted == Trusted(removalInput)', 'trusted == removalInput.receiptMatches /\\ removalInput.authorizationMatches'],
    [removalProperties[2], 'ApplyRemoval', 'previewed == trusted /\\ removalInput.previewCompletes', 'previewed == trusted'],
    [removalProperties[3], 'ApplyRemoval', 'committed == previewed /\\ ~removalInput.concurrentDraftChange', 'committed == previewed'],
    [removalProperties[4], 'ApplyRemoval', 'committed == previewed /\\ ~removalInput.concurrentDraftChange', 'committed == FALSE'],
];
export function validateRemovalInput(value) {
    assert.ok(value && typeof value === 'object' && !Array.isArray(value), 'Missing removal input');
    assert.deepEqual(Object.keys(value).sort(), [...fields].sort(), 'Unknown or missing removal fields');
    for (const field of fields) assert.equal(typeof value[field], 'boolean', `Invalid removal choice ${field}`);
    return value;
}
export function expectedRemoval(value) {
    validateRemovalInput(value);
    const trusted = value.receiptMatches && value.provenanceMatches && value.authorizationMatches;
    const previewed = trusted && value.previewCompletes, committed = previewed && !value.concurrentDraftChange;
    return { accepted: committed, previewCalls: +trusted, saveAttempts: +previewed, successfulSaves: +committed, preservedHead: !committed, exactCandidate: committed, conflict: previewed && value.concurrentDraftChange };
}
const propertyFor = value => removalProperties[!value.receiptMatches ? 0 : !value.provenanceMatches || !value.authorizationMatches ? 1 : !value.previewCompletes ? 2 : value.concurrentDraftChange ? 3 : 4];
const propertyFields = {
    [removalProperties[0]]: ['accepted', 'previewCalls', 'saveAttempts', 'successfulSaves', 'preservedHead'],
    [removalProperties[1]]: ['accepted', 'saveAttempts', 'successfulSaves', 'preservedHead'],
    [removalProperties[2]]: ['accepted', 'previewCalls', 'saveAttempts', 'successfulSaves', 'preservedHead'],
    [removalProperties[3]]: ['accepted', 'previewCalls', 'saveAttempts', 'successfulSaves', 'preservedHead', 'conflict'],
    [removalProperties[4]]: observedFields,
};
const violatesProperty = (property, observed, expected) => propertyFields[property].some(field => key(observed[field]) !== key(expected[field]));

function validateState(value) {
    assert.ok(value && typeof value === 'object'); assert.deepEqual(Object.keys(value).sort(), Object.keys(initial()).sort());
    validateRemovalInput(value.removalInput); assert.ok(['choose', 'candidate', 'checked'].includes(value.removalPhase));
    for (const field of ['accepted', 'preservedHead', 'exactCandidate', 'conflict']) assert.equal(typeof value[field], 'boolean');
    for (const field of ['previewCalls', 'saveAttempts', 'successfulSaves']) assert.ok([0, 1].includes(value[field]));
}
function inputs() { return Array.from({ length: 32 }, (_, mask) => Object.fromEntries(fields.map((field, index) => [field, Boolean(mask & 1 << index)]))); }
const validateProductionSource = value => {
    if (value !== undefined) { assert.ok(value && typeof value === 'object' && !Array.isArray(value), 'Removal source overrides must be a nonempty file map'); assert.ok(Object.keys(value).length, 'Empty removal source overrides'); for (const [file, source] of Object.entries(value)) { assert.ok(sourceFiles.includes(file), 'Unknown removal source override'); assert.ok(typeof source === 'string' && source.trim(), 'Removal source override must be nonempty'); } }
};
export function validateRemovalGraph(graph, { productionSource } = {}) {
    validateProductionSource(productionSource);
    assert.equal(graph.schemaVersion, 1); assert.equal(graph.model, removalModel);
    assert.equal(graph.modelSha256, digest(join(modelDirectory, `${removalModel}.tla`)), 'Stale removal model');
    assert.equal(graph.configSha256, digest(join(modelDirectory, `${removalModel}.cfg`)), 'Stale removal config');
    assert.deepEqual(Object.keys(graph.productionSha256).sort(), files(), 'Changed removal source set');
    for (const file of files()) if (!(productionSource !== undefined && Object.hasOwn(productionSource, file))) assert.equal(graph.productionSha256[file], digest(join(root, file)), `Stale production source ${file}`);
    assert.ok(Array.isArray(graph.states) && graph.states.length === 65, 'Incomplete removal frontier');
    assert.ok(Array.isArray(graph.steps) && graph.steps.length === 64, 'Incomplete removal edges');
    const states = new Map(graph.states.map(state => [state.id, state])); assert.equal(states.size, graph.states.length, 'Duplicate removal state ID');
    const semantics = new Map();
    for (const state of graph.states) { validateState(state.vars); assert.equal(typeof state.start, 'boolean'); assert.ok(!semantics.has(key(state.vars)), 'Duplicate semantic state'); semantics.set(key(state.vars), state); }
    const starts = graph.states.filter(state => state.start); assert.equal(starts.length, 1); assert.deepEqual(starts[0].vars, initial());
    const edges = new Set();
    for (const step of graph.steps) { assert.ok(states.has(step.from) && states.has(step.to), 'Dangling removal edge'); assert.ok(['ChooseRemoval', 'ApplyRemoval'].includes(step.action), 'Unknown removal action'); const id = key([step.from, step.action, step.to]); assert.ok(!edges.has(id), 'Duplicate removal edge'); edges.add(id); }
    const cases = [];
    for (const input of inputs()) {
        const expected = expectedRemoval(input); 
        const candidate = semantics.get(key({ ...initial(), removalPhase: 'candidate', removalInput: input }));
        const checked = semantics.get(key({ removalPhase: 'checked', removalInput: input, ...expected })); assert.ok(candidate && checked, 'Missing or incorrect removal candidate');
        for (const edge of [[starts[0].id, 'ChooseRemoval', candidate.id], [candidate.id, 'ApplyRemoval', checked.id]]) assert.ok(edges.delete(key(edge)), 'Missing removal transition'); cases.push(input);
    }
    assert.equal(edges.size, 0, 'Unexpected removal transition'); return cases;
}
export async function removalGraph(output) {
    const { scripts, jar } = tools(), scratch = mkdtempSync(join(tmpdir(), 'loom-removal-graph-'));
    try {
        const result = spawnSync('java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC', '-workers', '1', '-metadir', join(scratch, 'states'), '-config', `${removalModel}.cfg`, '-dump', 'dot,actionlabels', join(scratch, 'graph'), `${removalModel}.tla`], { cwd: modelDirectory, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024 }); assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr);
        const [{ parseDot }, { json }] = await Promise.all([import(pathToFileURL(join(scripts, 'frontends/dot.ts')).href), import(pathToFileURL(join(scripts, 'frontends/view.ts')).href)]);
        const parsed = parseDot(readFileSync(join(scratch, 'graph.dot'), 'utf8'));
        const graph = { schemaVersion: 1, model: removalModel, modelSha256: digest(join(modelDirectory, `${removalModel}.tla`)), configSha256: digest(join(modelDirectory, `${removalModel}.cfg`)), productionSha256: hashes(), states: [...parsed.states].map(([id, state]) => ({ id, start: state.start, vars: Object.fromEntries(Object.entries(state.vars).map(([name, value]) => [name, json(value)])) })), steps: parsed.steps }; validateRemovalGraph(graph); writeFileSync(output, JSON.stringify(graph)); return graph;
    } finally { rmSync(scratch, { recursive: true, force: true }); }
}
function concreteCases(cases) { return cases.map((input, index) => ({ id: `removal-${index}`, ...input })); }
export function checkRemovalResults(record, cases) {
    assert.ok(Array.isArray(cases) && cases.length > 0, 'No removal cases selected'); assert.equal(new Set(cases.map(row => row.id)).size, cases.length);
    assert.deepEqual(Object.keys(record).sort(), ['schemaVersion', 'results'].sort()); assert.equal(record.schemaVersion, 1); assert.ok(Array.isArray(record.results)); assert.equal(record.results.length, cases.length, 'Missing Go observations');
    const actual = new Map();
    for (const row of record.results) {
        assert.deepEqual(Object.keys(row).sort(), ['id', 'error', ...observedFields].sort()); assert.equal(typeof row.id, 'string'); assert.equal(typeof row.error, 'string');
        for (const field of ['accepted', 'preservedHead', 'exactCandidate', 'conflict']) assert.equal(typeof row[field], 'boolean');
        for (const field of ['previewCalls', 'saveAttempts', 'successfulSaves']) assert.ok(Number.isSafeInteger(row[field]) && row[field] >= 0);
        assert.equal(row.accepted, row.error === '', 'Inconsistent removal result'); assert.ok(!actual.has(row.id), 'Duplicate Go observation'); actual.set(row.id, row);
    }
    assert.deepEqual([...actual.keys()].sort(), cases.map(row => row.id).sort(), 'Changed Go observation inventory');
    let conformanceError;
    for (const row of cases) {
        const observation = actual.get(row.id); assert.ok(observation, 'Unknown or missing Go case'); const input = Object.fromEntries(fields.map(field => [field, row[field]])); const expected = expectedRemoval(input);
        const observed = Object.fromEntries(observedFields.map(field => [field, observation[field]]));
        if (key(expected) !== key(observed)) {
            const property = propertyFor(input);
            if (!violatesProperty(property, observed, expected)) {
                conformanceError ??= Object.assign(new Error(`Observation mismatch outside ${property}`), { code: 'FORMAL_CONFORMANCE' });
                continue;
            }
            const witness = { schemaVersion: 1, kind: 'population-removal', model: removalModel, property, input, expected, actual: observed, serviceError: observation.error };
            const error = new Error(`${property}: expected ${key(expected)}, got ${key(observed)}`); Object.assign(error, { code: 'FORMAL_PROPERTY', property, witness }); throw error;
        }
    }
    if (conformanceError) throw conformanceError;
    return { cases: cases.length, accepted: record.results.filter(row => row.accepted).length, rejected: record.results.filter(row => !row.accepted).length };
}
function runGo(cases, { output, productionSource } = {}) {
    validateProductionSource(productionSource);
    const scratch = mkdtempSync(join(tmpdir(), 'loom-removal-go-')), tool = apiDoctor(), beforeSources = hashes();
    try {
        const inputPath = join(scratch, 'cases.json'), resultPath = join(scratch, 'results.json');
        writeFileSync(inputPath, JSON.stringify({ schemaVersion: 1, cases }));
        const args = ['test', '-json', `-timeout=${goTestTimeout}`, '-count=1', '-run', `^${testName}$`];
        if (productionSource !== undefined) { const replacements = {}; for (const [file, source] of Object.entries(productionSource)) { const replacement = join(scratch, `${Object.keys(replacements).length}.go`); writeFileSync(replacement, source); replacements[join(root, file)] = replacement; } const overlay = join(scratch, 'overlay.json'); writeFileSync(overlay, JSON.stringify({ Replace: replacements })); args.push('-overlay', overlay); }
        args.push('./internal/explorer/lifecycle');
        const env = { ...process.env, GOFLAGS: '', GOWORK: 'off', GOTOOLCHAIN: 'local', LOOM_FORMAL_REMOVAL_INPUT: inputPath, LOOM_FORMAL_REMOVAL_OUTPUT: resultPath };
        if (tool.executable === '/workspace/.loom-setup/go/bin/go') {
            if (!env.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) env.GOMODCACHE = '/workspace/.loom-setup/gomod';
            if (!env.GOCACHE && existsSync('/workspace/loom/.gocache')) env.GOCACHE = '/workspace/loom/.gocache';
        }
        const result = spawnSync(tool.executable, args, { cwd: root, env, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024, detached: true });
        if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
        if (output) { mkdirSync(output, { recursive: true }); writeFileSync(join(output, 'go.jsonl'), result.stdout || ''); writeFileSync(join(output, 'process.json'), JSON.stringify({ command: [tool.executable, ...args], exit: result.status, error: result.error?.message, stderr: result.stderr }, null, 2)); }
        assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr); checkApiEvents(result.stdout, { tests: [testName] });
        assert.deepEqual(hashes(), beforeSources, 'Removal sources changed during Go execution');
        assert.ok(existsSync(resultPath), 'Go adapter produced no observations'); return JSON.parse(readFileSync(resultPath, 'utf8'));
    } finally { rmSync(scratch, { recursive: true, force: true }); }
}

export async function checkRemovalGraph(graph, { productionSource, output, witnessFile } = {}) {
    const cases = concreteCases(validateRemovalGraph(graph, { productionSource })), before = hashes();
    try { const result = checkRemovalResults(runGo(cases, { output, productionSource }), cases); assert.deepEqual(hashes(), before, 'Removal sources changed during replay'); return { states: graph.states.length, transitions: graph.steps.length, modelCases: cases.length, replays: result.cases, accepted: result.accepted, rejected: result.rejected, boundary: 'actual public ProposePopulationMemberRemoval and ApplyCommands with real checked domain service; controlled compilation-scope agreement, preview and CAS store; empty retained route; no full multi-hop route, database atomicity, browser, attachment or arbitrary history proof' }; }
    catch (error) { if (error.witness && witnessFile) writeFileSync(witnessFile, JSON.stringify(error.witness, null, 2)); throw error; }
}
const sourceFiles = [removalFile, 'internal/explorer/lifecycle/authoring.go', 'internal/explorer/service.go'];
const bindingGate = 'if binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest ||\n\t\tbinding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {';
const provenanceGate = 'if validationErr := validatePopulationRemovalSelection(baseSelection, candidateSelection, binding.RemovedMember, snapshot); validationErr != nil {';
const previewCall = 'return previewPopulationMemberRemovalProposal(ctx, s, project, explorerID, populationMemberCandidate)';
const saveError = 'stored, err := s.store.SaveDraft(ctx, *owner, request.ExpectedDraftVersion, request.ExpectedDraftDigest)\n\tif err != nil {\n\t\treturn nil, err\n\t}';
export const removalSourceProofs = [
 { model: removalModel, property: removalProperties[0], kind: 'removal-replay', branch: 'receipt-binding', edits: [[removalFile, bindingGate, 'if false {']] },
 { model: removalModel, property: removalProperties[1], kind: 'removal-replay', branch: 'candidate-provenance', edits: [[removalFile, provenanceGate, 'if validationErr := validatePopulationRemovalSelection(baseSelection, candidateSelection, binding.RemovedMember, snapshot); false {']] },
 { model: removalModel, property: removalProperties[2], kind: 'removal-replay', branch: 'candidate-preview', edits: [[sourceFiles[1], previewCall, 'return nil']] },
 { model: removalModel, property: removalProperties[3], kind: 'removal-replay', branch: 'final-cas-error', edits: [[sourceFiles[2], saveError, 'stored, err := s.store.SaveDraft(ctx, *owner, request.ExpectedDraftVersion, request.ExpectedDraftDigest)\n\tif err != nil {\n\t\treturn &authoringv2.ApplyCommandsResponse{Workspace: workspace}, nil\n\t}']] },
 { model: removalModel, property: removalProperties[4], kind: 'removal-replay', branch: 'valid-removal', edits: [[sourceFiles[1], previewCall, 'if err := previewPopulationMemberRemovalProposal(ctx, s, project, explorerID, populationMemberCandidate); err != nil { return err }; return fmt.Errorf("controlled rejection of valid removal")']] },
];
export async function replayRemovalWitness(witness, { productionSource, output } = {}) {
    assert.equal(witness.schemaVersion, 1); assert.equal(witness.kind, 'population-removal'); assert.equal(witness.model, removalModel); validateRemovalInput(witness.input); const expected = expectedRemoval(witness.input); assert.deepEqual(witness.expected, expected, 'Witness changed expected observation'); assert.equal(witness.property, propertyFor(witness.input), 'Witness property mismatch');
    const row = { id: 'witness', ...witness.input };
    try { checkRemovalResults(runGo([row], { output, productionSource }), [row]); return { status: 'property-held', property: witness.property, input: witness.input }; }
    catch (error) { if (error.code !== 'FORMAL_PROPERTY') throw error; return { status: 'reproduced', ...error.witness }; }
}
export async function checkRemovalSourceProof(proof, graph, output) {
    assert.deepEqual(proof, removalSourceProofs.find(row => row.branch === proof.branch), 'Unknown removal proof'); mkdirSync(output, { recursive: true }); const [[file, before, after]] = proof.edits, original = readFileSync(join(root, file), 'utf8'); assert.equal(original.split(before).length, 2, 'Nonunique removal fault'); const broken = original.replace(before, after);
    await checkRemovalGraph(graph, { output: join(output, 'positive') }); const witnessFile = join(output, 'counterexample.json'); await assert.rejects(() => checkRemovalGraph(graph, { productionSource: { [file]: broken }, output: join(output, 'negative'), witnessFile }), error => error.code === 'FORMAL_PROPERTY' && error.property === proof.property);
    const witness = JSON.parse(readFileSync(witnessFile, 'utf8')); assert.equal((await replayRemovalWitness(witness)).status, 'property-held'); assert.equal((await replayRemovalWitness(witness, { productionSource: { [file]: broken } })).status, 'reproduced'); await checkRemovalGraph(graph, { output: join(output, 'restored') }); const evidence = { model: removalModel, property: proof.property, kind: proof.kind, branch: proof.branch, sourceFiles: [file], positive: true, negative: true, restored: true, witness: 'counterexample.json' }; writeFileSync(join(output, 'proof.json'), JSON.stringify(evidence, null, 2)); return evidence;
}
export function removalCounterexampleWitness(counterexample) {
    assert.ok(counterexample && removalProperties.includes(counterexample.property), 'Unknown removal counterexample property'); assert.ok(Array.isArray(counterexample.states) && counterexample.states.length === 3, 'Expected two removal actions'); const [start, candidate, checked] = counterexample.states.map(state => ({ ...state, vars: state.vars })); assert.deepEqual(start.vars, initial()); assert.equal(candidate.action, 'ChooseRemoval'); assert.equal(checked.action, 'ApplyRemoval'); validateState(candidate.vars); validateState(checked.vars); assert.deepEqual(candidate.vars, { ...initial(), removalPhase: 'candidate', removalInput: checked.vars.removalInput }, 'Changed candidate boundary'); assert.equal(candidate.vars.removalPhase, 'candidate'); assert.equal(checked.vars.removalPhase, 'checked'); assert.deepEqual(candidate.vars.removalInput, checked.vars.removalInput); const input = checked.vars.removalInput, expected = expectedRemoval(input); assert.equal(counterexample.property, propertyFor(input), 'Counterexample property mismatch');
    const observed = Object.fromEntries(observedFields.map(field => [field, checked.vars[field]])); assert.ok(violatesProperty(counterexample.property, observed, expected), 'Counterexample does not violate its named property'); return { schemaVersion: 1, kind: 'population-removal', model: removalModel, property: counterexample.property, input, expected };
}
export async function checkRemovalGraphControls(graph) {
    const mutations = [value => value.states.pop(), value => value.steps.pop(), value => value.steps.push({ ...value.steps[0] }), value => { value.steps[0].action = 'Unknown'; }, value => { value.steps[0].to = 'missing'; }, value => { value.modelSha256 = 'stale'; }, value => { value.productionSha256[removalFile] = 'stale'; }, value => { value.states.find(row => row.vars.removalPhase === 'checked').vars.accepted = 'pending'; }]; for (const mutation of mutations) { const changed = structuredClone(graph); mutation(changed); assert.throws(() => validateRemovalGraph(changed)); } return mutations.length;
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
    const [command, file, extra] = process.argv.slice(2);
    if (command === '--generate') { assert.ok(!extra || extra === '--self-test'); assert.ok(!existsSync(file) || readdirSync(file).length === 0, 'Removal output must be empty'); mkdirSync(file, { recursive: true }); const graph = await removalGraph(join(file, 'graph.json')); const replay = await checkRemovalGraph(graph, { output: join(file, 'go'), witnessFile: join(file, 'counterexample.json') }); if (extra) { replay.graphControls = await checkRemovalGraphControls(graph); replay.sourceControls = []; for (const proof of removalSourceProofs) replay.sourceControls.push(await checkRemovalSourceProof(proof, graph, join(file, proof.branch))); } writeFileSync(join(file, 'replay.json'), JSON.stringify(replay, null, 2)); console.log(JSON.stringify(replay)); }
    else if (command === '--witness') { const record = JSON.parse(readFileSync(file, 'utf8')), witness = record.counterexamples ? removalCounterexampleWitness(record.counterexamples.find(value => removalProperties.includes(value.property))) : record; const result = await replayRemovalWitness(witness, extra !== undefined ? { productionSource: JSON.parse(readFileSync(extra, 'utf8')) } : {}); console.log(JSON.stringify(result)); process.exitCode = result.status === 'reproduced' ? 1 : 0; }
    else throw new Error('Use --generate DIRECTORY [--self-test] or --witness FILE [SCRATCH_SOURCE_MAP_JSON]');
}
