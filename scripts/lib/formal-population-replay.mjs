import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, readdirSync, existsSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { root, tools, digest } from './formal-tools.mjs';
import { apiDoctor, checkApiEvents } from './formal-api.mjs';
import { verificationTimeoutMs, goTestTimeout } from './verification-budget.mjs';

export const populationModel = 'PopulationMapping';
export const populationProperties = ['Inv_UntrustedPopulationDoesNotExecuteOrDisclose', 'Inv_InterruptedPopulationHasNoCounts', 'Inv_CompletePopulationReturnsBoundResult'];
export const populationFile = 'internal/explorer/lifecycle/population_report.go';
export const populationAdapter = 'internal/explorer/lifecycle/population_formal_replay_test.go';
const modelDirectory = join(root, 'verification/builder-interactions');
const testName = 'TestPopulationModelReplay';
const fields = ['authorizedScopeMatches', 'selectionComplete', 'selectionBindingMatches', 'memberReadCompletes', 'mappingCompletes'];
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(name => [name, canonical(value[name])])) : value;
const key = value => JSON.stringify(canonical(value));
const files = () => [...new Set(['go.mod', 'go.sum', populationAdapter,
    ...['internal/explorer/lifecycle', 'internal/explorer', 'internal/projectid', 'internal/explorer/capability'].flatMap(directory => readdirSync(join(root, directory)).filter(file => file.endsWith('.go') && (directory === 'internal/explorer/lifecycle' || !file.endsWith('_test.go'))).map(file => `${directory}/${file}`))])].sort();
const hashes = () => Object.fromEntries(files().map(file => [file, digest(join(root, file))]));
const initial = () => ({ populationPhase: 'choose', populationInput: Object.fromEntries(fields.map(field => [field, true])), outcome: 'pending', memberReads: 0, mappingCalls: 0, counts: [] });
export const populationModelControls = [
    [populationProperties[0], 'ClassifyPopulation', 'trusted == Trusted(populationInput)', 'trusted == populationInput.authorizedScopeMatches /\\ populationInput.selectionComplete'],
    ["Inv_InterruptedPopulationHasNoCounts", "ClassifyPopulation", "result == IF ~trusted THEN \"rejected\" ELSE IF ~populationInput.memberReadCompletes \\/ ~populationInput.mappingCompletes", "result == IF ~trusted THEN \"rejected\" ELSE IF ~populationInput.memberReadCompletes"],
    [populationProperties[2], 'ClassifyPopulation', 'ELSE "complete"', 'ELSE "incomplete"'],
];
export function validatePopulationInput(value) {
    assert.ok(value && typeof value === 'object' && !Array.isArray(value), 'Missing population input');
    assert.deepEqual(Object.keys(value).sort(), [...fields].sort(), 'Unknown or missing population fields');
    for (const field of fields) assert.equal(typeof value[field], 'boolean', `Invalid population choice ${field}`);
    return value;
}
export function expectedPopulation(value) {
    validatePopulationInput(value);
    const trusted = value.authorizedScopeMatches && value.selectionComplete && value.selectionBindingMatches;
    const outcome = !trusted ? 'rejected' : !value.memberReadCompletes || !value.mappingCompletes ? 'incomplete' : 'complete';
    return { outcome, memberReads: trusted ? 1 : 0, mappingCalls: trusted && value.memberReadCompletes ? 1 : 0, counts: outcome === 'complete' ? [3, 2, 1, 1] : [], bindingCorrect: trusted };
}
const propertyFor = value => populationProperties[expectedPopulation(value).outcome === 'rejected' ? 0 : expectedPopulation(value).outcome === 'incomplete' ? 1 : 2];
const propertyFields = {
    [populationProperties[0]]: ['outcome', 'memberReads', 'mappingCalls', 'counts'],
    [populationProperties[1]]: ['outcome', 'mappingCalls', 'counts'],
    [populationProperties[2]]: ['outcome', 'memberReads', 'mappingCalls', 'counts'],
};
const violatesProperty = (property, observed, expected) => propertyFields[property].some(field => key(observed[field]) !== key(expected[field]));

const inputs = () => Array.from({ length: 32 }, (_, mask) => Object.fromEntries(fields.map((field, index) => [field, Boolean(mask & (1 << index))])));
function decodedPopulationState(value) {
    if (Array.isArray(value.counts)) return value;
    assert.ok(value.counts && typeof value.counts === 'object', 'Missing decoded population counts');
    const keys = Object.keys(value.counts).sort();
    assert.ok(keys.length === 0 || key(keys) === key(['1', '2', '3', '4']), 'Malformed decoded count tuple');
    return { ...value, counts: keys.map(name => value.counts[name]) };
}
function validateState(value) {
    assert.deepEqual(Object.keys(value).sort(), ['populationPhase', 'populationInput', 'outcome', 'memberReads', 'mappingCalls', 'counts'].sort());
    assert.ok(['choose', 'candidate', 'checked'].includes(value.populationPhase)); validatePopulationInput(value.populationInput);
    assert.ok(['pending', 'rejected', 'incomplete', 'complete'].includes(value.outcome));
    for (const field of ['memberReads', 'mappingCalls']) assert.ok(Number.isSafeInteger(value[field]) && value[field] >= 0);
    assert.ok(Array.isArray(value.counts) && (value.counts.length === 0 || key(value.counts) === key([3, 2, 1, 1])), key(value.counts));
    if (value.populationPhase !== 'checked') { assert.equal(value.outcome, 'pending'); assert.equal(value.memberReads, 0); assert.equal(value.mappingCalls, 0); assert.deepEqual(value.counts, []); }
}
function validateProductionSource(value) {
    if (value === undefined) return;
    assert.equal(typeof value, 'string', 'Production source override must be a string');
    assert.ok(value.trim().length > 0, 'Production source override must be nonempty');
}
export function validatePopulationGraph(graph, { productionSource } = {}) {
    validateProductionSource(productionSource);
    assert.equal(graph.schemaVersion, 1); assert.equal(graph.model, populationModel);
    assert.equal(graph.modelSha256, digest(join(modelDirectory, `${populationModel}.tla`)), 'Stale population model');
    assert.equal(graph.configSha256, digest(join(modelDirectory, `${populationModel}.cfg`)), 'Stale population config');
    assert.deepEqual(Object.keys(graph.productionSha256).sort(), files(), 'Changed population source set');
    for (const file of files()) if (!(productionSource !== undefined && file === populationFile)) assert.equal(graph.productionSha256[file], digest(join(root, file)), `Stale production source ${file}`);
    assert.ok(Array.isArray(graph.states) && graph.states.length === 65, 'Incomplete population frontier');
    assert.ok(Array.isArray(graph.steps) && graph.steps.length === 64, 'Incomplete population edges');
    const states = new Map(graph.states.map(state => [state.id, state])); assert.equal(states.size, graph.states.length, 'Duplicate population state ID');
    const semantics = new Map();
    for (const state of graph.states) { validateState(state.vars); assert.equal(typeof state.start, 'boolean'); assert.ok(!semantics.has(key(state.vars)), 'Duplicate semantic state'); semantics.set(key(state.vars), state); }
    const starts = graph.states.filter(state => state.start); assert.equal(starts.length, 1); assert.deepEqual(starts[0].vars, initial());
    const edges = new Set();
    for (const step of graph.steps) { assert.ok(states.has(step.from) && states.has(step.to), 'Dangling population edge'); assert.ok(['ChoosePopulation', 'ClassifyPopulation'].includes(step.action), 'Unknown population action'); const id = key([step.from, step.action, step.to]); assert.ok(!edges.has(id), 'Duplicate population edge'); edges.add(id); }
    const cases = [];
    for (const input of inputs()) {
        const expected = expectedPopulation(input); delete expected.bindingCorrect;
        const candidate = semantics.get(key({ ...initial(), populationPhase: 'candidate', populationInput: input }));
        const checked = semantics.get(key({ populationPhase: 'checked', populationInput: input, ...expected })); assert.ok(candidate && checked, 'Missing or incorrect population candidate');
        for (const edge of [[starts[0].id, 'ChoosePopulation', candidate.id], [candidate.id, 'ClassifyPopulation', checked.id]]) assert.ok(edges.delete(key(edge)), 'Missing population transition'); cases.push(input);
    }
    assert.equal(edges.size, 0, 'Unexpected population transition'); return cases;
}
export async function populationGraph(output) {
    const { scripts, jar } = tools(), scratch = mkdtempSync(join(tmpdir(), 'loom-population-graph-'));
    try {
        const result = spawnSync('java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC', '-workers', '1', '-metadir', join(scratch, 'states'), '-config', `${populationModel}.cfg`, '-dump', 'dot,actionlabels', join(scratch, 'graph'), `${populationModel}.tla`], { cwd: modelDirectory, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024 }); assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr);
        const [{ parseDot }, { json }] = await Promise.all([import(pathToFileURL(join(scripts, 'frontends/dot.ts')).href), import(pathToFileURL(join(scripts, 'frontends/view.ts')).href)]);
        const parsed = parseDot(readFileSync(join(scratch, 'graph.dot'), 'utf8'));
        const graph = { schemaVersion: 1, model: populationModel, modelSha256: digest(join(modelDirectory, `${populationModel}.tla`)), configSha256: digest(join(modelDirectory, `${populationModel}.cfg`)), productionSha256: hashes(), states: [...parsed.states].map(([id, state]) => ({ id, start: state.start, vars: decodedPopulationState(Object.fromEntries(Object.entries(state.vars).map(([name, value]) => [name, json(value)]))) })), steps: parsed.steps }; validatePopulationGraph(graph); writeFileSync(output, JSON.stringify(graph)); return graph;
    } finally { rmSync(scratch, { recursive: true, force: true }); }
}
function concreteCases(cases) { return cases.flatMap((input, index) => [false, true].flatMap(alias => [false, true].map(deadline => ({ id: `population-${index}-${+alias}-${+deadline}`, ...input, alias, deadline })))); }
export function checkPopulationResults(record, cases) {
    assert.ok(Array.isArray(cases) && cases.length > 0, 'No population cases selected');
    assert.equal(new Set(cases.map(row => row.id)).size, cases.length, 'Duplicate expected population case');
    assert.deepEqual(Object.keys(record).sort(), ['schemaVersion', 'results'].sort()); assert.equal(record.schemaVersion, 1); assert.ok(Array.isArray(record.results)); assert.equal(record.results.length, cases.length, 'Missing Go observations');
    const actual = new Map();
    for (const row of record.results) {
        assert.deepEqual(Object.keys(row).sort(), ['id', 'outcome', 'memberReads', 'mappingCalls', 'counts', 'bindingCorrect', 'error'].sort()); assert.equal(typeof row.id, 'string'); assert.ok(['rejected', 'incomplete', 'complete'].includes(row.outcome));
        for (const field of ['memberReads', 'mappingCalls']) assert.ok(Number.isSafeInteger(row[field]) && row[field] >= 0);
        assert.ok(Array.isArray(row.counts) && row.counts.every(value => Number.isSafeInteger(value) && value >= 0)); assert.equal(typeof row.bindingCorrect, 'boolean'); assert.equal(typeof row.error, 'string'); assert.equal(row.outcome === 'rejected', row.error !== '', 'Inconsistent population result'); assert.ok(!actual.has(row.id), 'Duplicate Go observation'); actual.set(row.id, row);
    }
    assert.deepEqual([...actual.keys()].sort(), cases.map(row => row.id).sort(), 'Changed Go observation inventory');
    let conformanceError;
    for (const row of cases) {
        const observation = actual.get(row.id); assert.ok(observation, 'Unknown or missing Go case'); const input = Object.fromEntries(fields.map(field => [field, row[field]])); const expected = expectedPopulation(input);
        const observed = Object.fromEntries(Object.keys(expected).map(field => [field, observation[field]]));
        if (key(expected) !== key(observed)) {
            const property = propertyFor(input);
            if (!violatesProperty(property, observed, expected)) {
                conformanceError ??= Object.assign(new Error(`Observation mismatch outside ${property}`), { code: 'FORMAL_CONFORMANCE' });
                continue;
            }
            const witness = { schemaVersion: 1, kind: 'population-mapping', model: populationModel, property, input, alias: row.alias, deadline: row.deadline, expected, actual: observed, serviceError: observation.error };
            const error = new Error(`${property}: expected ${key(expected)}, got ${key(observed)}`); Object.assign(error, { code: 'FORMAL_PROPERTY', property, witness }); throw error;
        }
    }
    if (conformanceError) throw conformanceError;
    return { cases: cases.length, complete: record.results.filter(row => row.outcome === 'complete').length, incomplete: record.results.filter(row => row.outcome === 'incomplete').length, rejected: record.results.filter(row => row.outcome === 'rejected').length };
}
function runGo(cases, { output, productionSource } = {}) {
    validateProductionSource(productionSource);
    const scratch = mkdtempSync(join(tmpdir(), 'loom-population-go-')), tool = apiDoctor(), beforeSources = hashes();
    try {
        const inputPath = join(scratch, 'cases.json'), resultPath = join(scratch, 'results.json');
        writeFileSync(inputPath, JSON.stringify({ schemaVersion: 1, cases }));
        const args = ['test', '-json', `-timeout=${goTestTimeout}`, '-count=1', '-run', `^${testName}$`];
        if (productionSource !== undefined) { const replacement = join(scratch, 'validation.go'), overlay = join(scratch, 'overlay.json'); writeFileSync(replacement, productionSource); writeFileSync(overlay, JSON.stringify({ Replace: { [join(root, populationFile)]: replacement } })); args.push('-overlay', overlay); }
        args.push('./internal/explorer/lifecycle');
        const env = { ...process.env, GOFLAGS: '', GOWORK: 'off', GOTOOLCHAIN: 'local', LOOM_FORMAL_POPULATION_INPUT: inputPath, LOOM_FORMAL_POPULATION_OUTPUT: resultPath };
        if (tool.executable === '/workspace/.loom-setup/go/bin/go') {
            if (!env.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) env.GOMODCACHE = '/workspace/.loom-setup/gomod';
            if (!env.GOCACHE && existsSync('/workspace/loom/.gocache')) env.GOCACHE = '/workspace/loom/.gocache';
        }
        const result = spawnSync(tool.executable, args, { cwd: root, env, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024, detached: true });
        if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
        if (output) { mkdirSync(output, { recursive: true }); writeFileSync(join(output, 'go.jsonl'), result.stdout || ''); writeFileSync(join(output, 'process.json'), JSON.stringify({ command: [tool.executable, ...args], exit: result.status, error: result.error?.message, stderr: result.stderr }, null, 2)); }
        assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr); checkApiEvents(result.stdout, { tests: [testName] });
        assert.deepEqual(hashes(), beforeSources, 'Population sources changed during Go execution');
        assert.ok(existsSync(resultPath), 'Go adapter produced no observations'); return JSON.parse(readFileSync(resultPath, 'utf8'));
    } finally { rmSync(scratch, { recursive: true, force: true }); }
}

export async function checkPopulationGraph(graph, { productionSource, output, witnessFile } = {}) {
    const cases = concreteCases(validatePopulationGraph(graph, { productionSource })), before = hashes();
    try { const result = checkPopulationResults(runGo(cases, { output, productionSource }), cases); assert.deepEqual(hashes(), before, 'Population sources changed during replay'); return { states: graph.states.length, transitions: graph.steps.length, modelCases: cases.length / 4, replays: result.cases, complete: result.complete, incomplete: result.incomplete, rejected: result.rejected, boundary: 'actual public Service.PopulationMapping; controlled authorization/store/mapping callback; no provider security, database query, pagination, attachment or removal proof' }; }
    catch (error) { if (error.witness && witnessFile) writeFileSync(witnessFile, JSON.stringify(error.witness, null, 2)); throw error; }
}
const bindingGate = "if err := validatePopulationMappingSelection(receipt, authorized, selection, *population); err != nil {\n\t\treturn PopulationMappingResult{}, conflict(\"populationMapping\", \"POPULATION_MAPPING_STALE\", \"the receipt's selected-resource identity is stale\", nil, err)\n\t}";
const incompleteGate = 'if executed.Status != dataframeexecution.PopulationMappingComplete {\n\t\treturn PopulationMappingResult{Report: incompletePopulationReport(receipt, request.OutputID, selection)}, nil\n\t}';
export const populationSourceProofs = [
    { model: populationModel, property: populationProperties[0], kind: 'population-replay', branch: 'selection-binding', edits: [[populationFile, bindingGate, '']] },
    { model: populationModel, property: populationProperties[1], kind: 'population-replay', branch: 'incomplete-counts', edits: [[populationFile, incompleteGate, '']] },
    { model: populationModel, property: populationProperties[2], kind: 'population-replay', branch: 'complete-result', edits: [[populationFile, 'if executed.Status != dataframeexecution.PopulationMappingComplete {', 'if true {']] },
];
export async function replayPopulationWitness(witness, { productionSource, output } = {}) {
    assert.equal(witness.schemaVersion, 1); assert.equal(witness.kind, 'population-mapping'); assert.equal(witness.model, populationModel); validatePopulationInput(witness.input); assert.equal(typeof witness.alias, 'boolean'); assert.equal(typeof witness.deadline, 'boolean'); const expected = expectedPopulation(witness.input); assert.deepEqual(witness.expected, expected, 'Witness changed expected observation'); assert.equal(witness.property, propertyFor(witness.input), 'Witness property mismatch');
    const row = { id: 'witness', ...witness.input, alias: witness.alias, deadline: witness.deadline };
    try { checkPopulationResults(runGo([row], { output, productionSource }), [row]); return { status: 'property-held', property: witness.property, input: witness.input }; }
    catch (error) { if (error.code !== 'FORMAL_PROPERTY') throw error; return { status: 'reproduced', ...error.witness }; }
}
export async function checkPopulationSourceProof(proof, graph, output) {
    assert.deepEqual(proof, populationSourceProofs.find(row => row.branch === proof.branch), 'Unknown population proof'); mkdirSync(output, { recursive: true }); const [[file, before, after]] = proof.edits, original = readFileSync(join(root, file), 'utf8'); assert.equal(original.split(before).length, 2, 'Nonunique population fault'); const broken = original.replace(before, after);
    await checkPopulationGraph(graph, { output: join(output, 'positive') }); const witnessFile = join(output, 'counterexample.json'); await assert.rejects(() => checkPopulationGraph(graph, { productionSource: broken, output: join(output, 'negative'), witnessFile }), error => error.code === 'FORMAL_PROPERTY' && error.property === proof.property);
    const witness = JSON.parse(readFileSync(witnessFile, 'utf8')); assert.equal((await replayPopulationWitness(witness)).status, 'property-held'); assert.equal((await replayPopulationWitness(witness, { productionSource: broken })).status, 'reproduced'); await checkPopulationGraph(graph, { output: join(output, 'restored') }); const evidence = { model: populationModel, property: proof.property, kind: proof.kind, branch: proof.branch, sourceFiles: [file], positive: true, negative: true, restored: true, witness: 'counterexample.json' }; writeFileSync(join(output, 'proof.json'), JSON.stringify(evidence, null, 2)); return evidence;
}
export function populationCounterexampleWitness(counterexample) {
    assert.ok(counterexample && populationProperties.includes(counterexample.property), 'Unknown population counterexample property'); assert.ok(Array.isArray(counterexample.states) && counterexample.states.length === 3, 'Expected two population actions'); const [start, candidate, checked] = counterexample.states.map(state => ({ ...state, vars: decodedPopulationState(state.vars) })); assert.deepEqual(start.vars, initial()); assert.equal(candidate.action, 'ChoosePopulation'); assert.equal(checked.action, 'ClassifyPopulation'); validateState(candidate.vars); validateState(checked.vars); assert.equal(candidate.vars.populationPhase, 'candidate'); assert.equal(checked.vars.populationPhase, 'checked'); assert.deepEqual(candidate.vars.populationInput, checked.vars.populationInput); const input = checked.vars.populationInput, expected = expectedPopulation(input); assert.equal(counterexample.property, propertyFor(input), 'Counterexample property mismatch');
    const observed = Object.fromEntries(['outcome', 'memberReads', 'mappingCalls', 'counts'].map(field => [field, checked.vars[field]])); assert.ok(violatesProperty(counterexample.property, observed, expected), 'Counterexample does not violate its named property'); return { schemaVersion: 1, kind: 'population-mapping', model: populationModel, property: counterexample.property, input, alias: false, deadline: false, expected };
}
export async function checkPopulationGraphControls(graph) {
    const mutations = [value => value.states.pop(), value => value.steps.pop(), value => value.steps.push({ ...value.steps[0] }), value => { value.steps[0].action = 'Unknown'; }, value => { value.steps[0].to = 'missing'; }, value => { value.modelSha256 = 'stale'; }, value => { value.productionSha256[populationFile] = 'stale'; }, value => { value.states.find(row => row.vars.populationPhase === 'checked').vars.outcome = 'pending'; }]; for (const mutation of mutations) { const changed = structuredClone(graph); mutation(changed); assert.throws(() => validatePopulationGraph(changed)); } return mutations.length;
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
    const [command, file, extra] = process.argv.slice(2);
    if (command === '--generate') { assert.ok(!extra || extra === '--self-test'); assert.ok(!existsSync(file) || readdirSync(file).length === 0, 'Population output must be empty'); mkdirSync(file, { recursive: true }); const graph = await populationGraph(join(file, 'graph.json')); const replay = await checkPopulationGraph(graph, { output: join(file, 'go'), witnessFile: join(file, 'counterexample.json') }); if (extra) { replay.graphControls = await checkPopulationGraphControls(graph); replay.sourceControls = []; for (const proof of populationSourceProofs) replay.sourceControls.push(await checkPopulationSourceProof(proof, graph, join(file, proof.branch))); } writeFileSync(join(file, 'replay.json'), JSON.stringify(replay, null, 2)); console.log(JSON.stringify(replay)); }
    else if (command === '--witness') { const record = JSON.parse(readFileSync(file, 'utf8')), witness = record.counterexamples ? populationCounterexampleWitness(record.counterexamples.find(value => populationProperties.includes(value.property))) : record; const result = await replayPopulationWitness(witness, extra !== undefined ? { productionSource: readFileSync(extra, 'utf8') } : {}); console.log(JSON.stringify(result)); process.exitCode = result.status === 'reproduced' ? 1 : 0; }
    else throw new Error('Use --generate DIRECTORY [--self-test] or --witness FILE [SCRATCH_REPORT_SOURCE]');
}
