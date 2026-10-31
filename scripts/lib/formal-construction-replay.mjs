import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, readdirSync, existsSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { root, tools, digest } from './formal-tools.mjs';
import { apiDoctor, checkApiEvents } from './formal-api.mjs';
import { verificationTimeoutMs, goTestTimeout } from './verification-budget.mjs';

export const constructionModel = 'ConstructionChain';
export const constructionProperties = ['Inv_OnlyImmediatePredecessorAccepted', 'Inv_PredecessorSchemaDeterminesAcceptance'];
export const constructionFile = 'internal/explorer/authoringv2/construction_validation.go';
export const constructionAdapter = 'internal/explorer/authoringv2/construction_formal_replay_test.go';
const modelDirectory = join(root, 'verification/builder-interactions');
const testName = 'TestConstructionModelReplay';
const kinds = ['FILTER', 'DERIVE'], references = ['PREVIOUS', 'SOURCE', 'SELF', 'FOREIGN', 'REVISION'], columns = ['x', 'd', 'foreign'];
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(name => [name, canonical(value[name])])) : value;
const key = value => JSON.stringify(canonical(value));
const files = () => ['go.mod', 'go.sum', constructionAdapter, ...['internal/explorer/authoringv2', 'internal/explorer/capability'].flatMap(directory => readdirSync(join(root, directory)).filter(file => file.endsWith('.go') && !file.endsWith('_test.go')).map(file => `${directory}/${file}`))].sort();
const hashes = () => Object.fromEntries(files().map(file => [file, digest(join(root, file))]));
const initial = () => ({ chainPhase: 'choose', chainInput: { firstKind: 'FILTER', secondInput: 'PREVIOUS', secondColumn: 'x' }, accepted: false });
export const constructionModelControls = [
    [constructionProperties[0], 'ClassifyChain', 'referenceOK == chainInput.secondInput = "PREVIOUS"', 'referenceOK == chainInput.secondInput \\in {"PREVIOUS", "SELF"}'],
    [constructionProperties[1], 'ClassifyChain', 'columnOK == chainInput.secondColumn \\in PredecessorColumns(chainInput.firstKind)', 'columnOK == chainInput.secondColumn \\in {"x"}'],
];
export function validateChainInput(value) {
    assert.ok(value && typeof value === 'object' && !Array.isArray(value), 'Missing chain input');
    assert.deepEqual(Object.keys(value).sort(), ['firstKind', 'secondInput', 'secondColumn'].sort(), 'Unknown or missing chain fields');
    assert.ok(kinds.includes(value.firstKind) && references.includes(value.secondInput) && columns.includes(value.secondColumn), 'Unknown chain choice');
    return value;
}
export function expectedChainAcceptance(value) {
    validateChainInput(value);
    return value.secondInput === 'PREVIOUS' && (value.secondColumn === 'x' || value.firstKind === 'DERIVE' && value.secondColumn === 'd');
}
const inputs = () => kinds.flatMap(firstKind => references.flatMap(secondInput => columns.map(secondColumn => ({ firstKind, secondInput, secondColumn }))));
function validateState(value) {
    assert.deepEqual(Object.keys(value).sort(), ['chainPhase', 'chainInput', 'accepted'].sort());
    assert.ok(['choose', 'candidate', 'checked'].includes(value.chainPhase));
    validateChainInput(value.chainInput); assert.equal(typeof value.accepted, 'boolean');
    if (value.chainPhase !== 'checked') assert.equal(value.accepted, false);
}
function validateProductionSource(value) {
    if (value === undefined) return;
    assert.equal(typeof value, 'string', 'Production source override must be a string');
    assert.ok(value.trim().length > 0, 'Production source override must be nonempty');
}
export function validateConstructionGraph(graph, { productionSource } = {}) {
    validateProductionSource(productionSource);
    assert.equal(graph.schemaVersion, 1); assert.equal(graph.model, constructionModel);
    assert.equal(graph.modelSha256, digest(join(modelDirectory, `${constructionModel}.tla`)), 'Stale construction model');
    assert.equal(graph.configSha256, digest(join(modelDirectory, `${constructionModel}.cfg`)), 'Stale construction config');
    assert.deepEqual(Object.keys(graph.productionSha256).sort(), files(), 'Changed construction source set');
    for (const file of files()) if (!(productionSource !== undefined && file === constructionFile)) assert.equal(graph.productionSha256[file], digest(join(root, file)), `Stale production source: ${file}`);
    assert.ok(Array.isArray(graph.states) && graph.states.length === 61, 'Incomplete construction frontier');
    assert.ok(Array.isArray(graph.steps) && graph.steps.length === 60, 'Incomplete construction edges');
    const states = new Map(graph.states.map(state => [state.id, state]));
    assert.equal(states.size, graph.states.length, 'Duplicate construction state ID');
    const semantics = new Map();
    for (const state of graph.states) { validateState(state.vars); assert.equal(typeof state.start, 'boolean'); assert.ok(!semantics.has(key(state.vars)), 'Duplicate semantic state'); semantics.set(key(state.vars), state); }
    const starts = graph.states.filter(state => state.start); assert.equal(starts.length, 1); assert.deepEqual(starts[0].vars, initial());
    const edges = new Set();
    for (const step of graph.steps) {
        assert.ok(states.has(step.from) && states.has(step.to), 'Dangling construction edge');
        assert.ok(['ChooseChain', 'ClassifyChain'].includes(step.action), 'Unknown construction action');
        const id = key([step.from, step.action, step.to]); assert.ok(!edges.has(id), 'Duplicate construction edge'); edges.add(id);
    }
    const cases = [];
    for (const input of inputs()) {
        const candidate = semantics.get(key({ chainPhase: 'candidate', chainInput: input, accepted: false }));
        const checked = semantics.get(key({ chainPhase: 'checked', chainInput: input, accepted: expectedChainAcceptance(input) }));
        assert.ok(candidate && checked, 'Missing or incorrect model candidate');
        for (const edge of [[starts[0].id, 'ChooseChain', candidate.id], [candidate.id, 'ClassifyChain', checked.id]]) assert.ok(edges.delete(key(edge)), 'Missing model transition');
        cases.push(input);
    }
    assert.equal(edges.size, 0, 'Unexpected construction transition');
    return cases;
}
export async function constructionGraph(output) {
    const { scripts, jar } = tools(), scratch = mkdtempSync(join(tmpdir(), 'loom-chain-graph-'));
    try {
        const result = spawnSync('java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC', '-workers', '1', '-metadir', join(scratch, 'states'), '-config', `${constructionModel}.cfg`, '-dump', 'dot,actionlabels', join(scratch, 'graph'), `${constructionModel}.tla`], { cwd: modelDirectory, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024 });
        assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr);
        const [{ parseDot }, { json }] = await Promise.all([import(pathToFileURL(join(scripts, 'frontends/dot.ts')).href), import(pathToFileURL(join(scripts, 'frontends/view.ts')).href)]);
        const parsed = parseDot(readFileSync(join(scratch, 'graph.dot'), 'utf8'));
        const graph = { schemaVersion: 1, model: constructionModel, modelSha256: digest(join(modelDirectory, `${constructionModel}.tla`)), configSha256: digest(join(modelDirectory, `${constructionModel}.cfg`)), productionSha256: hashes(), states: [...parsed.states].map(([id, state]) => ({ id, start: state.start, vars: Object.fromEntries(Object.entries(state.vars).map(([name, value]) => [name, json(value)])) })), steps: parsed.steps };
        validateConstructionGraph(graph); writeFileSync(output, JSON.stringify(graph)); return graph;
    } finally { rmSync(scratch, { recursive: true, force: true }); }
}
function concreteCases(cases) {
    return cases.flatMap((input, index) => [false, true].flatMap(rename => [false, true].map(reorder => ({ id: `chain-${index}-${+rename}-${+reorder}`, ...input, rename, reorder }))));
}
export function checkConstructionResults(record, cases) {
    assert.deepEqual(Object.keys(record).sort(), ['schemaVersion', 'results'].sort()); assert.equal(record.schemaVersion, 1);
    assert.ok(Array.isArray(record.results)); assert.equal(record.results.length, cases.length, 'Missing Go observations');
    const actual = new Map();
    for (const row of record.results) { assert.deepEqual(Object.keys(row).sort(), ['id', 'accepted', 'error'].sort()); assert.equal(typeof row.id, 'string'); assert.equal(typeof row.accepted, 'boolean'); assert.equal(typeof row.error, 'string'); assert.equal(row.accepted, row.error === '', 'Inconsistent validation result'); assert.ok(!actual.has(row.id), 'Duplicate Go observation'); actual.set(row.id, row); }
    for (const row of cases) {
        const observation = actual.get(row.id); assert.ok(observation, 'Unknown or missing Go case');
        const input = { firstKind: row.firstKind, secondInput: row.secondInput, secondColumn: row.secondColumn };
        const expectedAccepted = expectedChainAcceptance(input);
        if (observation.accepted !== expectedAccepted) {
            const property = input.secondInput === 'PREVIOUS' ? constructionProperties[1] : constructionProperties[0];
            const witness = { schemaVersion: 1, kind: 'construction-chain', model: constructionModel, property, input, rename: row.rename, reorder: row.reorder, expectedAccepted, actualAccepted: observation.accepted, validationError: observation.error };
            const error = new Error(`${property}: expected acceptance ${expectedAccepted}, got ${observation.accepted} for ${key(input)}`); Object.assign(error, { code: 'FORMAL_PROPERTY', property, witness }); throw error;
        }
    }
    return { cases: cases.length, accepted: record.results.filter(row => row.accepted).length, rejected: record.results.filter(row => !row.accepted).length };
}
function runGo(cases, { output, productionSource } = {}) {
    validateProductionSource(productionSource);
    const scratch = mkdtempSync(join(tmpdir(), 'loom-chain-go-')), tool = apiDoctor(), beforeSources = hashes();
    try {
        const inputPath = join(scratch, 'cases.json'), resultPath = join(scratch, 'results.json');
        writeFileSync(inputPath, JSON.stringify({ schemaVersion: 1, cases }));
        const args = ['test', '-json', `-timeout=${goTestTimeout}`, '-count=1', '-run', `^${testName}$`];
        if (productionSource !== undefined) { const replacement = join(scratch, 'validation.go'), overlay = join(scratch, 'overlay.json'); writeFileSync(replacement, productionSource); writeFileSync(overlay, JSON.stringify({ Replace: { [join(root, constructionFile)]: replacement } })); args.push('-overlay', overlay); }
        args.push('./internal/explorer/authoringv2');
        const env = { ...process.env, GOFLAGS: '', GOWORK: 'off', GOTOOLCHAIN: 'local', LOOM_FORMAL_CHAIN_INPUT: inputPath, LOOM_FORMAL_CHAIN_OUTPUT: resultPath };
        if (tool.executable === '/workspace/.loom-setup/go/bin/go') {
            if (!env.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) env.GOMODCACHE = '/workspace/.loom-setup/gomod';
            if (!env.GOCACHE && existsSync('/workspace/loom/.gocache')) env.GOCACHE = '/workspace/loom/.gocache';
        }
        const result = spawnSync(tool.executable, args, { cwd: root, env, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024, detached: true });
        if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
        if (output) { mkdirSync(output, { recursive: true }); writeFileSync(join(output, 'go.jsonl'), result.stdout || ''); writeFileSync(join(output, 'process.json'), JSON.stringify({ command: [tool.executable, ...args], exit: result.status, error: result.error?.message, stderr: result.stderr }, null, 2)); }
        assert.ifError(result.error); assert.equal(result.status, 0, result.stdout + result.stderr); checkApiEvents(result.stdout, { tests: [testName] });
        assert.deepEqual(hashes(), beforeSources, 'Construction sources changed during Go execution');
        assert.ok(existsSync(resultPath), 'Go adapter produced no observations'); return JSON.parse(readFileSync(resultPath, 'utf8'));
    } finally { rmSync(scratch, { recursive: true, force: true }); }
}
export async function checkConstructionGraph(graph, { productionSource, output, witnessFile } = {}) {
    const cases = concreteCases(validateConstructionGraph(graph, { productionSource })), before = hashes();
    try {
        const result = checkConstructionResults(runGo(cases, { output, productionSource }), cases);
        assert.deepEqual(hashes(), before, 'Construction sources changed during replay');
        return { states: graph.states.length, transitions: graph.steps.length, modelCases: cases.length / 4, replays: result.cases, accepted: result.accepted, rejected: result.rejected, boundary: 'actual public Construction.Validate; two ordinary stages, FILTER or DERIVE followed by FILTER; no UI, explicit groups, arbitrary depth or query-result proof' };
    } catch (error) { if (error.witness && witnessFile) writeFileSync(witnessFile, JSON.stringify(error.witness, null, 2)); throw error; }
}
export const constructionSourceProofs = [
    { model: constructionModel, property: constructionProperties[0], kind: 'construction-replay', branch: 'predecessor-reference', edits: [[constructionFile, 'if index == 0 || input.StepID != steps[index-1].ID {', 'if index == 0 {']] },
    { model: constructionModel, property: constructionProperties[1], kind: 'construction-replay', branch: 'predecessor-schema', edits: [[constructionFile, 'return steps[index-1].Outputs, nil', 'return source, nil']] },
];
export async function replayConstructionWitness(witness, { productionSource, output } = {}) {
    assert.equal(witness.schemaVersion, 1); assert.equal(witness.kind, 'construction-chain'); assert.equal(witness.model, constructionModel);
    validateChainInput(witness.input); assert.equal(typeof witness.rename, 'boolean'); assert.equal(typeof witness.reorder, 'boolean');
    const expected = expectedChainAcceptance(witness.input); assert.equal(witness.expectedAccepted, expected, 'Witness changed expected acceptance');
    assert.equal(witness.property, witness.input.secondInput === 'PREVIOUS' ? constructionProperties[1] : constructionProperties[0], 'Witness property mismatch');
    const row = { id: 'witness', ...witness.input, rename: witness.rename, reorder: witness.reorder };
    try { checkConstructionResults(runGo([row], { output, productionSource }), [row]); return { status: 'property-held', property: witness.property, input: witness.input }; }
    catch (error) { if (error.code !== 'FORMAL_PROPERTY') throw error; return { status: 'reproduced', ...error.witness }; }
}
export async function checkConstructionSourceProof(proof, graph, output) {
    assert.deepEqual(proof, constructionSourceProofs.find(row => row.branch === proof.branch), 'Unknown construction proof');
    mkdirSync(output, { recursive: true }); const [[file, before, after]] = proof.edits, original = readFileSync(join(root, file), 'utf8'); assert.equal(original.split(before).length, 2, 'Nonunique construction fault'); const broken = original.replace(before, after);
    await checkConstructionGraph(graph, { output: join(output, 'positive') });
    const witnessFile = join(output, 'counterexample.json');
    await assert.rejects(() => checkConstructionGraph(graph, { productionSource: broken, output: join(output, 'negative'), witnessFile }), error => error.code === 'FORMAL_PROPERTY' && error.property === proof.property);
    const witness = JSON.parse(readFileSync(witnessFile, 'utf8')); assert.equal((await replayConstructionWitness(witness)).status, 'property-held'); assert.equal((await replayConstructionWitness(witness, { productionSource: broken })).status, 'reproduced');
    await checkConstructionGraph(graph, { output: join(output, 'restored') });
    const evidence = { model: constructionModel, property: proof.property, kind: proof.kind, branch: proof.branch, sourceFiles: [file], positive: true, negative: true, restored: true, witness: 'counterexample.json' }; writeFileSync(join(output, 'proof.json'), JSON.stringify(evidence, null, 2)); return evidence;
}
export function constructionCounterexampleWitness(counterexample) {
    assert.ok(counterexample && constructionProperties.includes(counterexample.property), 'Unknown construction counterexample property');
    assert.ok(Array.isArray(counterexample.states) && counterexample.states.length === 3, 'Expected two construction actions');
    const [start, candidate, checked] = counterexample.states; assert.deepEqual(start.vars, initial()); assert.equal(candidate.action, 'ChooseChain'); assert.equal(checked.action, 'ClassifyChain');
    validateState(candidate.vars); validateState(checked.vars); assert.equal(candidate.vars.chainPhase, 'candidate'); assert.equal(checked.vars.chainPhase, 'checked'); assert.deepEqual(candidate.vars.chainInput, checked.vars.chainInput);
    const input = checked.vars.chainInput, expectedAccepted = expectedChainAcceptance(input); assert.equal(counterexample.property, input.secondInput === 'PREVIOUS' ? constructionProperties[1] : constructionProperties[0], 'Counterexample property mismatch'); assert.notEqual(checked.vars.accepted, expectedAccepted, 'Counterexample does not violate acceptance');
    return { schemaVersion: 1, kind: 'construction-chain', model: constructionModel, property: counterexample.property, input, rename: false, reorder: false, expectedAccepted };
}
export async function checkConstructionGraphControls(graph) {
    const mutations = [value => value.states.pop(), value => value.steps.pop(), value => value.steps.push({ ...value.steps[0] }), value => { value.steps[0].action = 'Unknown'; }, value => { value.steps[0].to = 'missing'; }, value => { value.modelSha256 = 'stale'; }, value => { value.productionSha256[constructionFile] = 'stale'; }, value => { value.states.find(row => row.vars.chainPhase === 'checked').vars.accepted = !value.states.find(row => row.vars.chainPhase === 'checked').vars.accepted; }];
    for (const mutation of mutations) { const changed = structuredClone(graph); mutation(changed); assert.throws(() => validateConstructionGraph(changed)); }
    return mutations.length;
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
    const [command, file, extra] = process.argv.slice(2);
    if (command === '--generate') {
        assert.ok(!extra || extra === '--self-test'); assert.ok(!existsSync(file) || readdirSync(file).length === 0, 'Construction output must be empty'); mkdirSync(file, { recursive: true });
        const graph = await constructionGraph(join(file, 'graph.json')); const replay = await checkConstructionGraph(graph, { output: join(file, 'go'), witnessFile: join(file, 'counterexample.json') });
        if (extra) { replay.graphControls = await checkConstructionGraphControls(graph); replay.sourceControls = []; for (const proof of constructionSourceProofs) replay.sourceControls.push(await checkConstructionSourceProof(proof, graph, join(file, proof.branch))); }
        writeFileSync(join(file, 'replay.json'), JSON.stringify(replay, null, 2)); console.log(JSON.stringify(replay));
    } else if (command === '--witness') {
        const record = JSON.parse(readFileSync(file, 'utf8')), witness = record.counterexamples ? constructionCounterexampleWitness(record.counterexamples.find(value => constructionProperties.includes(value.property))) : record;
        const result = await replayConstructionWitness(witness, extra !== undefined ? { productionSource: readFileSync(extra, 'utf8') } : {}); console.log(JSON.stringify(result)); process.exitCode = result.status === 'reproduced' ? 1 : 0;
    } else throw new Error('Use --generate DIRECTORY [--self-test] or --witness FILE [SCRATCH_VALIDATOR_SOURCE]');
}
