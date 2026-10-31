import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { stripTypeScriptTypes } from 'node:module';
import { root, tools, digest } from './formal-tools.mjs';
const modelName = 'ReconcileAcceptance';
const modelDirectory = join(root, 'verification/builder-interactions');
export const acceptanceFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/reconcileAcceptance.ts';
export const recoveryFile = 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/reconcileRecovery.ts';
export const acceptanceProperties = {
    obsolete: 'Inv_ObsoleteReconcileIsIgnored',
    mismatch: 'Inv_MismatchedReceiptRequiresRepair',
    diagnostics: 'Inv_ErrorReceiptIsNotAdmitted',
    accepted: 'Inv_CurrentReceiptIsAdmitted',
};
export const acceptanceModelControls = [
    [acceptanceProperties.obsolete, 'ClassifyResponse', 'THEN "obsolete"', 'THEN "accepted"'],
    [acceptanceProperties.mismatch, 'ClassifyResponse', 'THEN "mismatch"', 'THEN "accepted"'],
    [acceptanceProperties.diagnostics, 'ClassifyResponse', 'THEN "diagnostics"', 'THEN "accepted"'],
    [acceptanceProperties.accepted, 'ClassifyResponse', 'ELSE "accepted"', 'ELSE "diagnostics"'],
];
export const acceptanceSourceControls = [
    { property: acceptanceProperties.obsolete, before: "if (!reconcileResultIsCurrent(identity)) return 'obsolete';", after: "if (false) return 'obsolete';" },
    { property: acceptanceProperties.mismatch, before: "if (!reconcileReceiptMatchesRequest(value, request)) return 'mismatch';", after: "if (false) return 'mismatch';" },
    { property: acceptanceProperties.diagnostics, before: "(diagnostic) => diagnostic.severity === 'error'", after: '(diagnostic) => false' },
    { property: acceptanceProperties.accepted, before: "return 'accepted';", after: "return 'diagnostics';" },
];
const moduleURL = text => `data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(text)).toString('base64')}`;
export async function loadReconcileImplementation({ acceptanceSource = readFileSync(join(root, acceptanceFile), 'utf8'), recoverySource = readFileSync(join(root, recoveryFile), 'utf8') } = {}) {
    const source = stripTypeScriptTypes(acceptanceSource);
    const dependency = moduleURL(recoverySource);
    assert.equal(source.split("'./reconcileRecovery'").length, 2, 'Unexpected production helper dependency');
    return import(`data:text/javascript;base64,${Buffer.from(source.replace("'./reconcileRecovery'", JSON.stringify(dependency))).toString('base64')}`);
}
export function validateAcceptanceInput(input) {
    assert.ok(input && typeof input === 'object' && !Array.isArray(input), 'Missing acceptance inputs');
    assert.deepEqual(Object.keys(input).sort(), ['currentOwner', 'currentFingerprint', 'requestGeneration', 'currentGeneration', 'requestSnapshot', 'responseSnapshot', 'requestOutput', 'responseOutputs', 'diagnostic'].sort(), 'Unknown/missing acceptance input fields');
    assert.ok(['A', 'B'].includes(input.currentOwner), 'Invalid current owner');
    assert.ok(['F0', 'F1'].includes(input.currentFingerprint), 'Invalid fingerprint');
    for (const field of ['requestGeneration', 'currentGeneration', 'requestSnapshot', 'responseSnapshot'])
        assert.ok([0, 1, 2].includes(input[field]), `Invalid acceptance ${field}`);
    assert.ok([3, 0, 1, 2].includes(input.requestOutput), 'Invalid requested output');
    assert.ok(Array.isArray(input.responseOutputs) && input.responseOutputs.length <= 3 && input.responseOutputs.every(value => [0, 1, 2].includes(value)) && new Set(input.responseOutputs).size === input.responseOutputs.length, 'Invalid response outputs');
    assert.ok(['none', 'warning', 'error'].includes(input.diagnostic), 'Invalid diagnostic');
    return input;
}
function permutations(values) {
    return values.length < 2 ? [values] : values.flatMap((value, index) => permutations(values.filter((_, i) => i !== index)).map(rest => [value, ...rest]));
}
/** This independent reader oracle defines the four observable caller branches. */
export function expectedAcceptance(input) {
    validateAcceptanceInput(input);
    if (input.currentOwner !== 'A' || input.currentFingerprint !== 'F0' || input.requestGeneration !== input.currentGeneration)
        return 'obsolete';
    if (input.requestSnapshot !== input.responseSnapshot || input.requestOutput !== 3 && !input.responseOutputs.includes(input.requestOutput))
        return 'mismatch';
    return input.diagnostic === 'error' ? 'diagnostics' : 'accepted';
}
function invoke(input, implementation, variant = {}) {
    const swapOwner = variant.swapOwner ?? false, swapFingerprint = variant.swapFingerprint ?? false;
    assert.equal(typeof swapOwner, 'boolean');
    assert.equal(typeof swapFingerprint, 'boolean');
    const outputOrder = variant.outputOrder ?? [...input.responseOutputs].sort();
    assert.deepEqual([...outputOrder].sort(), [...input.responseOutputs].sort(), 'Output permutation changed membership');
    const renameOwner = value => swapOwner ? value === 'A' ? 'B' : 'A' : value;
    const renameFingerprint = value => swapFingerprint ? value === 'F0' ? 'F1' : 'F0' : value;
    const value = { snapshotToken: `S${input.responseSnapshot}`, outputs: outputOrder.map(id => ({ outputId: `O${id}` })),
        diagnostics: input.diagnostic === 'none' ? [] : [{ severity: input.diagnostic, code: 'FIXTURE', message: 'controlled response diagnostic' }] };
    const identity = { generation: input.requestGeneration, currentGeneration: input.currentGeneration,
        submittedExplorerId: renameOwner('A'), currentExplorerId: renameOwner(input.currentOwner),
        submittedFingerprint: renameFingerprint('F0'), currentFingerprint: renameFingerprint(input.currentFingerprint) };
    const request = { snapshotToken: `S${input.requestSnapshot}`, ...(input.requestOutput === 3 ? {} : { outputId: `O${input.requestOutput}` }) };
    return implementation.reconcileAcceptance(value, identity, request);
}
function checkOne(input, expected, implementation, variant, property) {
    assert.ok(Object.hasOwn(acceptanceProperties, expected), 'Unknown modeled acceptance outcome');
    const actual = invoke(input, implementation, variant);
    assert.ok(Object.hasOwn(acceptanceProperties, actual), 'Production returned unknown acceptance outcome');
    if (property && acceptanceProperties[expected] !== property)
        return;
    if (actual !== expected) {
        const error = new Error(`${acceptanceProperties[expected]}: expected ${expected}, got ${actual}`);
        error.code = 'FORMAL_PROPERTY';
        error.property = acceptanceProperties[expected];
        error.witness = { schemaVersion: 1, kind: 'reconcile-acceptance', model: modelName, property: error.property, input, variant, expected, actual,
            actions: ['ChooseInputs', 'ClassifyResponse'] };
        throw error;
    }
}
export async function replayReconcileWitness(witness, sources) {
    assert.equal(witness.schemaVersion, 1);
    assert.equal(witness.kind, 'reconcile-acceptance');
    assert.equal(witness.model, modelName);
    assert.ok(Object.values(acceptanceProperties).includes(witness.property), 'Unknown acceptance witness property');
    assert.deepEqual(witness.actions, ['ChooseInputs', 'ClassifyResponse']);
    const expected = expectedAcceptance(witness.input);
    assert.equal(witness.property, acceptanceProperties[expected], 'Witness input does not trigger named property');
    assert.equal(witness.expected, expected, 'Witness changed its expected outcome');
    try {
        checkOne(witness.input, expected, await loadReconcileImplementation(sources), witness.variant, witness.property);
        return { status: 'property-held', property: witness.property, input: witness.input };
    }
    catch (error) {
        if (error.code !== 'FORMAL_PROPERTY')
            throw error;
        return { status: 'reproduced', ...error.witness };
    }
}
/** Convert an upstream decoded TLC path to external inputs for production replay.
 * Recompute the reader promise instead of trusting the broken model's outcome. */
export function acceptanceCounterexampleWitness(counterexample) {
    assert.ok(Array.isArray(counterexample.states) && counterexample.states.length === 3, 'Acceptance counterexample must cross its two synchronous actions');
    const [initial, chosen, checked] = counterexample.states;
    assert.equal(initial.vars?.lastAction, 'Init');
    assert.equal(chosen.action, 'ChooseInputs');
    assert.equal(chosen.vars?.lastAction, 'ChooseInputs');
    assert.equal(checked.action, 'ClassifyResponse');
    assert.equal(checked.vars?.lastAction, 'ClassifyResponse');
    assert.equal(checked.vars?.phase, 'checked');
    assert.deepEqual(chosen.vars.acceptanceInput, checked.vars.acceptanceInput, 'Counterexample changed its external inputs');
    const input = validateAcceptanceInput(checked.vars.acceptanceInput);
    const expected = expectedAcceptance(input);
    assert.equal(counterexample.property, acceptanceProperties[expected], 'Counterexample does not trigger its reader property');
    return { schemaVersion: 1, kind: 'reconcile-acceptance', model: modelName, property: counterexample.property, input,
        variant: { swapOwner: false, swapFingerprint: false, outputOrder: [...input.responseOutputs].sort() }, expected,
        actions: ['ChooseInputs', 'ClassifyResponse'] };
}
/** The checked two-action frontier is parsed by upstream pstack's DOT->IR frontend. */
export async function reconcileAcceptanceGraph(output) {
    const { scripts, jar } = tools();
    const directory = mkdtempSync(join(tmpdir(), 'loom-reconcile-graph-'));
    try {
        const result = spawnSync('java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC', '-workers', '1', '-metadir', join(directory, 'states'),
            '-config', `${modelName}.cfg`, '-dump', 'dot,actionlabels', join(directory, 'graph'), `${modelName}.tla`], { cwd: modelDirectory, encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
        assert.ifError(result.error);
        assert.equal(result.status, 0, result.stdout + result.stderr);
        const [{ parseDot }, { json }] = await Promise.all([import(pathToFileURL(join(scripts, 'frontends/dot.ts')).href), import(pathToFileURL(join(scripts, 'frontends/view.ts')).href)]);
        const graph = parseDot(readFileSync(join(directory, 'graph.dot'), 'utf8'));
        const serialized = { schemaVersion: 1, model: modelName, modelSha256: digest(join(modelDirectory, `${modelName}.tla`)), configSha256: digest(join(modelDirectory, `${modelName}.cfg`)),
            productionSha256: { [acceptanceFile]: digest(join(root, acceptanceFile)), [recoveryFile]: digest(join(root, recoveryFile)) },
            states: [...graph.states].map(([id, state]) => ({ id, start: state.start, vars: Object.fromEntries(Object.entries(state.vars).map(([key, value]) => [key, json(value)])) })), steps: graph.steps };
        assert.ok(serialized.states.length > 1 && serialized.steps.length > 0, 'Empty acceptance model graph');
        writeFileSync(output, JSON.stringify(serialized));
        return serialized;
    }
    finally {
        rmSync(directory, { recursive: true, force: true });
    }
}
export async function checkReconcileGraph(graph, { acceptanceSource, recoverySource, witnessFile, property } = {}) {
    assert.equal(graph.schemaVersion, 1);
    assert.equal(graph.model, modelName);
    assert.equal(graph.modelSha256, digest(join(modelDirectory, `${modelName}.tla`)), 'Stale acceptance model graph');
    assert.equal(graph.configSha256, digest(join(modelDirectory, `${modelName}.cfg`)), 'Stale acceptance config graph');
    for (const file of [acceptanceFile, recoveryFile])
        if (!(file === acceptanceFile ? acceptanceSource : recoverySource))
            assert.equal(graph.productionSha256[file], digest(join(root, file)), 'Stale acceptance production graph');
    assert.ok(!property || Object.values(acceptanceProperties).includes(property), 'Unknown acceptance property');
    const states = new Map(graph.states.map(state => [state.id, state]));
    assert.equal(states.size, graph.states.length, 'Duplicate model state');
    const starts = graph.states.filter(state => state.start);
    assert.equal(starts.length, 1, 'Expected one initial acceptance state');
    assert.equal(starts[0].vars.phase, 'choose');
    const chosen = new Set(), classified = new Set();
    for (const step of graph.steps) {
        const from = states.get(step.from), to = states.get(step.to);
        assert.ok(from && to, 'Dangling acceptance edge');
        assert.equal(step.action, to.vars.lastAction, 'Acceptance edge/action disagree');
        if (step.action === 'ChooseInputs') {
            assert.equal(from.id, starts[0].id, 'Inputs must start from the modeled input boundary');
            assert.equal(to.vars.phase, 'returned');
            validateAcceptanceInput(to.vars.acceptanceInput);
            chosen.add(to.id);
        }
        else if (step.action === 'ClassifyResponse') {
            assert.equal(from.vars.phase, 'returned');
            assert.equal(to.vars.phase, 'checked');
            assert.deepEqual(from.vars.acceptanceInput, to.vars.acceptanceInput, 'Classification changed its external input');
            classified.add(step.from);
        }
        else
            throw new Error(`Unknown acceptance action: ${step.action}`);
    }
    assert.deepEqual([...chosen].sort(), [...classified].sort(), 'An input was not classified');
    assert.equal(chosen.size, 31104, 'Acceptance frontier has missing/extra bounded inputs');
    assert.equal(graph.steps.length, chosen.size * 2, 'Acceptance frontier has duplicate/missing edges');
    assert.equal(graph.states.length, 1 + chosen.size * 2, 'Unreachable or missing acceptance states');
    const implementation = await loadReconcileImplementation({ acceptanceSource, recoverySource });
    const cases = graph.steps.filter(step => step.action === 'ClassifyResponse').map(step => states.get(step.to).vars).sort((a, b) => JSON.stringify(a.acceptanceInput).localeCompare(JSON.stringify(b.acceptanceInput)));
    assert.equal(cases.length, chosen.size, 'Missing classification edge');
    const inputKey = input => JSON.stringify(Object.fromEntries(Object.entries(input).sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => [key, key === 'responseOutputs' ? [...value].sort() : value])));
    assert.equal(new Set(cases.map(state => inputKey(state.acceptanceInput))).size, chosen.size, 'Duplicate or missing distinct external inputs');
    const coverage = new Map(Object.keys(acceptanceProperties).map(outcome => [outcome, 0]));
    let replays = 0;
    for (const state of cases) {
        const input = state.acceptanceInput;
        assert.equal(state.outcome, expectedAcceptance(input), 'Model classification differs from the independent contract');
        for (const swapOwner of [false, true])
            for (const swapFingerprint of [false, true])
                for (const outputOrder of permutations([...input.responseOutputs].sort())) {
                    try {
                        checkOne(input, state.outcome, implementation, { swapOwner, swapFingerprint, outputOrder }, property);
                    }
                    catch (error) {
                        if (witnessFile && error.witness)
                            writeFileSync(witnessFile, JSON.stringify(error.witness, null, 2));
                        throw error;
                    }
                    replays++;
                }
        coverage.set(state.outcome, coverage.get(state.outcome) + 1);
    }
    assert.ok(cases.length > 0 && [...coverage.values()].every(count => count > 0), 'Vacuous acceptance outcome coverage');
    return { states: graph.states.length, transitions: graph.steps.length, inputCases: cases.length, replays, coverage: Object.fromEntries(coverage) };
}
export const acceptanceSourceProofs = acceptanceSourceControls.map(control => ({ model: modelName, property: control.property, kind: 'reconcile-acceptance', edits: [[acceptanceFile, control.before, control.after]] }));
export async function checkReconcileSourceProof(proof, graph, output) {
    mkdirSync(output, { recursive: true });
    const control = acceptanceSourceControls.find(control => control.property === proof.property);
    assert.ok(control, 'Unknown acceptance source proof');
    const source = readFileSync(join(root, acceptanceFile), 'utf8');
    assert.equal(source.split(control.before).length, 2, 'Nonunique acceptance source mutation');
    await checkReconcileGraph(graph, { property: control.property });
    const broken = source.replace(control.before, control.after);
    const witnessFile = join(output, 'counterexample.json');
    let witness;
    await assert.rejects(() => checkReconcileGraph(graph, { acceptanceSource: broken, property: control.property, witnessFile }), error => {
        witness = error.witness;
        return error.code === 'FORMAL_PROPERTY' && error.property === control.property;
    });
    assert.ok(witness, 'Acceptance source proof lost exact inputs');
    assert.equal((await replayReconcileWitness(witness, { acceptanceSource: broken })).status, 'reproduced');
    assert.equal((await replayReconcileWitness(witness)).status, 'property-held');
    await checkReconcileGraph(graph, { property: control.property });
    assert.equal(readFileSync(join(root, acceptanceFile), 'utf8'), source, 'Live acceptance source changed');
    const evidence = { model: modelName, property: control.property, kind: 'reconcile-acceptance', sourceFiles: [acceptanceFile, recoveryFile], positive: true, negative: true, restored: true, witness: 'counterexample.json' };
    writeFileSync(join(output, 'proof.json'), JSON.stringify(evidence, null, 2));
    return evidence;
}
export async function checkReconcileSourceControls(graph, output) {
    mkdirSync(output, { recursive: true });
    const positive = await checkReconcileGraph(graph);
    const proofs = [];
    for (const proof of acceptanceSourceProofs)
        proofs.push(await checkReconcileSourceProof(proof, graph, join(output, proof.property)));
    const evidence = { positive, proofs };
    writeFileSync(join(output, 'proofs.json'), JSON.stringify(evidence, null, 2));
    return evidence;
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
    if (process.argv[2] === '--witness') {
        const input = JSON.parse(readFileSync(process.argv[3], 'utf8'));
        const witnesses = input.counterexamples ? input.counterexamples.map(acceptanceCounterexampleWitness) : [input];
        assert.ok(witnesses.length > 0, 'No acceptance counterexamples to replay');
        const acceptanceSource = process.argv[4] ? readFileSync(process.argv[4], 'utf8') : undefined;
        const results = [];
        for (const witness of witnesses)
            results.push(await replayReconcileWitness(witness, { acceptanceSource }));
        console.log(JSON.stringify(results));
        process.exitCode = results.some(result => result.status === 'reproduced') ? 1 : 0;
    }
    else {
        const graphFile = process.argv[2], output = process.argv[3];
        mkdirSync(output, { recursive: true });
        const graph = graphFile === '--generate' ? await reconcileAcceptanceGraph(join(output, 'graph.json')) : JSON.parse(readFileSync(graphFile, 'utf8'));
        console.log(JSON.stringify(process.argv.includes('--self-test') ? await checkReconcileSourceControls(graph, output) : await checkReconcileGraph(graph, { witnessFile: join(output, 'counterexample.json') })));
    }
}
