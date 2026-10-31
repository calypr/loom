import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { join } from 'node:path';
import { stripTypeScriptTypes } from 'node:module';
import { root, digest } from './formal-tools.mjs';
const productionFile = join(root, 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/constructionApplyOwnership.ts');
export async function loadApplyImplementation(source = readFileSync(productionFile, 'utf8')) {
    return import(`data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(source)).toString('base64')}`);
}
/** Each adapter invokes the real synchronous production ownership component. */
export function replayApplyPath(path, implementation, property) {
    const owners = { A: { name: 'A' }, B: { name: 'B' } };
    const names = new Map(Object.entries(owners).map(([name, owner]) => [owner, name]));
    let currentOwner = 'none', currentContext = 'W1';
    const contexts = {}, handles = new Map(), slot = { current: undefined };
    let settledOwner = 'none';
    for (const { action, expected } of path) {
        switch (action) {
            case 'SetCurrentOwner': {
                currentOwner = expected.inputOwner;
                contexts[currentOwner] = currentContext;
                break;
            }
            case 'ChangeContext':
                currentContext = expected.currentContext;
                break;
            case 'Cancel':
                currentOwner = 'none';
                slot.current = undefined;
                break;
            case 'BeginApply': {
                const owner = currentOwner;
                const identity = { owner: owners[owner], requestKey: contexts[owner], generation: 0 };
                const finish = implementation.beginConstructionApply(slot, identity, () => currentOwner === 'none' ? undefined : { owner: owners[currentOwner], requestKey: currentContext, generation: 0 }, (applied) => {
                    settledOwner = applied ? currentOwner : owner;
                    if (applied)
                        currentOwner = 'none';
                });
                assert.equal(typeof finish, 'function', 'Model-enabled BeginApply was rejected');
                handles.set(owner, finish);
                break;
            }
            case 'ApplyReturns': {
                settledOwner = 'none';
                const beforeOwner = currentOwner;
                const beforeApply = slot.current ? names.get(slot.current.identity.owner) : 'none';
                const finish = handles.get(expected.inputOwner);
                assert.ok(finish, 'Model response has no real Apply handle');
                const accepted = finish(expected.lastApplied);
                const monitor = (name, condition, message) => {
                    if (property && property !== name && !(property === 'Inv_ApplySettlementTargetsInitiatingOwner' && name === 'Inv_ApplyReturnSettlesItsOwnOwner'))
                        return;
                    if (!condition) {
                        const error = new Error(`${name}: ${message}`);
                        error.code = 'FORMAL_PROPERTY';
                        error.property = property || name;
                        throw error;
                    }
                };
                monitor('Inv_ApplyReturnSettlesItsOwnOwner', settledOwner === 'none' || settledOwner === expected.responseOwner, 'completion settled another owner');
                monitor('Inv_OldReturnPreservesCurrentApply', expected.responseOwner === beforeApply ||
                    ((slot.current ? names.get(slot.current.identity.owner) : 'none') === beforeApply && currentOwner === beforeOwner), 'old completion changed a current owner or slot');
                monitor('Inv_CompletionOwnsCurrentContext', !accepted || contexts[expected.responseOwner] === currentContext, 'completion accepted in a different context');
                if (!property) {
                    assert.equal(accepted, expected.settledOwner !== 'none', 'Completion ownership differs from model');
                    assert.equal(settledOwner, expected.settledOwner, 'Wrong owner received settlement callback');
                }
                break;
            }
            default: throw new Error(`Unmapped model action: ${action}`);
        }
        if (property)
            continue;
        assert.equal(currentOwner, expected.currentOwner, 'Current proposal owner differs from model');
        assert.equal(currentContext, expected.currentContext, 'Current context differs from model');
        assert.equal(slot.current ? names.get(slot.current.identity.owner) : 'none', expected.applyingOwner, 'Current Apply slot differs from model');
    }
}
export function transitionPaths(graph) {
    assert.equal(graph.schemaVersion, 1);
    const states = new Map(graph.states.map((state) => [state.id, state]));
    const starts = graph.states.filter((state) => state.start).map((state) => state.id);
    assert.equal(starts.length, 1, 'Replay requires one modeled initial state');
    const outgoing = new Map();
    for (const step of graph.steps) {
        assert.ok(states.has(step.from) && states.has(step.to), 'Dangling model edge');
        assert.equal(step.action, states.get(step.to).vars.lastAction, 'TLC edge label and modeled action disagree');
        if (!outgoing.has(step.from))
            outgoing.set(step.from, []);
        outgoing.get(step.from).push(step);
    }
    const paths = new Map([[starts[0], []]]), queue = [...starts];
    for (let i = 0; i < queue.length; i++)
        for (const step of outgoing.get(queue[i]) || []) {
            if (paths.has(step.to))
                continue;
            const target = states.get(step.to).vars;
            paths.set(step.to, [...paths.get(step.from), { action: step.action, expected: target }]);
            queue.push(step.to);
        }
    assert.equal(paths.size, states.size, 'Model graph contains unreachable states');
    return graph.steps.map((step) => {
        const expected = states.get(step.to).vars;
        return [...paths.get(step.from), { action: step.action, expected }];
    }).sort((a, b) => a.length - b.length || JSON.stringify(a).localeCompare(JSON.stringify(b)));
}
export async function checkApplyGraph(graph, { source, witnessFile, property } = {}) {
    assert.equal(graph.modelSha256, digest(join(root, 'verification/builder-interactions/ApplySettlementRace.tla')), 'Stale model graph');
    assert.equal(graph.configSha256, digest(join(root, 'verification/builder-interactions/ApplySettlementRace.cfg')), 'Stale model config');
    if (!source)
        assert.equal(graph.productionSha256, digest(productionFile), 'Stale production graph fingerprint');
    const implementation = await loadApplyImplementation(source);
    const paths = transitionPaths(graph);
    const coverage = new Set();
    for (const path of paths) {
        try {
            replayApplyPath(path, implementation, property);
        }
        catch (error) {
            if (witnessFile)
                writeFileSync(witnessFile, JSON.stringify({ schemaVersion: 1, kind: 'model-path', model: 'ApplySettlementRace', modelSha256: graph.modelSha256,
                    productionSha256: graph.productionSha256, path, property: error.property, error: error.message }, null, 2));
            error.message += `\nModel-generated replay: ${path.map((step) => step.action).join(' -> ')}`;
            throw error;
        }
        for (const step of path)
            coverage.add(step.action);
    }
    assert.deepEqual([...coverage].sort(), ['ApplyReturns', 'BeginApply', 'Cancel', 'ChangeContext', 'SetCurrentOwner']);
    return { states: graph.states.length, transitions: graph.steps.length, replays: paths.length, maxDepth: Math.max(...paths.map((path) => path.length)) };
}
/** Run one serializable input against the real component, including duplicate completion. */
export function replayIdentityInput(inputs, implementation, property = 'Inv_CompletionOwnsCurrentContext') {
    const { submittedOwner, liveOwner, submittedKey, liveKey, submittedGeneration, liveGeneration, applied } = inputs;
    assert.ok(['A', 'B'].includes(submittedOwner) && ['A', 'B'].includes(liveOwner), 'Invalid identity witness owner');
    assert.ok(['W1', 'W2'].includes(submittedKey) && ['W1', 'W2'].includes(liveKey), 'Invalid identity witness key');
    assert.ok([0, 1, 2].includes(submittedGeneration) && [0, 1, 2].includes(liveGeneration), 'Invalid identity witness generation');
    assert.equal(typeof applied, 'boolean', 'Invalid identity witness outcome');
    const owners = { A: {}, B: {} }, slot = { current: undefined };
    let settlements = 0;
    const finish = implementation.beginConstructionApply(slot, { owner: owners[submittedOwner], requestKey: submittedKey, generation: submittedGeneration }, () => ({ owner: owners[liveOwner], requestKey: liveKey, generation: liveGeneration }), () => { settlements++; });
    assert.equal(typeof finish, 'function');
    const expected = submittedOwner === liveOwner && submittedKey === liveKey && submittedGeneration === liveGeneration;
    const accepted = finish(applied);
    const duplicateAccepted = finish(!applied);
    if (accepted !== expected || settlements !== Number(expected) || duplicateAccepted !== false) {
        const error = new Error(`${property}: wrong ownership/duplicate completion for ${JSON.stringify(inputs)}`);
        error.code = 'FORMAL_PROPERTY';
        error.property = property;
        error.witness = { schemaVersion: 1, kind: 'identity-input', model: 'ApplySettlementRace', property, inputs,
            expected: { accepted: expected, settlements: Number(expected), duplicateAccepted: false },
            actual: { accepted, settlements, duplicateAccepted } };
        throw error;
    }
}
/** Independent bounded reader inputs include identity dimensions collapsed by W1/W2. */
export async function checkApplyIdentityInputs(source, property = 'Inv_CompletionOwnsCurrentContext') {
    const implementation = await loadApplyImplementation(source);
    let count = 0;
    for (const submittedOwner of ['A', 'B'])
        for (const liveOwner of ['A', 'B'])
            for (const submittedKey of ['W1', 'W2'])
                for (const liveKey of ['W1', 'W2'])
                    for (const submittedGeneration of [0, 1, 2])
                        for (const liveGeneration of [0, 1, 2])
                            for (const applied of [true, false]) {
                                replayIdentityInput({ submittedOwner, liveOwner, submittedKey, liveKey, submittedGeneration, liveGeneration, applied }, implementation, property);
                                count++;
                            }
    return count;
}
/** Replay recorded inputs, not the model's predicted outputs. A held property is
 * model drift/unreproduced, never an assertion that the whole application passed. */
export async function replayWitness(witness, { source } = {}) {
    assert.equal(witness.schemaVersion, 1, 'Unknown witness schema');
    assert.equal(witness.model, 'ApplySettlementRace', 'Unsupported witness model');
    const properties = ['Inv_ApplyReturnSettlesItsOwnOwner', 'Inv_OldReturnPreservesCurrentApply', 'Inv_CompletionOwnsCurrentContext', 'Inv_ApplySettlementTargetsInitiatingOwner'];
    assert.ok(properties.includes(witness.property), 'Unknown witness property');
    assert.ok(['model-path', 'identity-input'].includes(witness.kind), 'Unknown witness kind');
    if (witness.kind === 'model-path') {
        assert.ok(Array.isArray(witness.path) && witness.path.length > 0, 'Empty witness path');
        assert.ok(witness.path.some(step => step.action === 'ApplyReturns'), 'Witness has no completion');
        for (const { action, expected } of witness.path) {
            assert.ok(expected && ['SetCurrentOwner', 'ChangeContext', 'Cancel', 'BeginApply', 'ApplyReturns'].includes(action), 'Unknown witness action');
            if (action === 'SetCurrentOwner' || action === 'ApplyReturns')
                assert.ok(['A', 'B'].includes(expected.inputOwner), 'Missing witness actor input');
            if (action === 'ApplyReturns') {
                assert.equal(expected.responseOwner, expected.inputOwner, 'Response actor differs from input');
                assert.equal(typeof expected.lastApplied, 'boolean', 'Missing completion outcome');
            }
            if (action === 'ChangeContext')
                assert.ok(['W1', 'W2'].includes(expected.currentContext), 'Missing context input');
        }
    }
    else {
        assert.equal(witness.property, 'Inv_CompletionOwnsCurrentContext', 'Invalid identity witness property');
        assert.ok(witness.inputs, 'Missing identity inputs');
    }
    try {
        const implementation = await loadApplyImplementation(source);
        if (witness.kind === 'identity-input')
            replayIdentityInput(witness.inputs, implementation, witness.property);
        else
            replayApplyPath(witness.path, implementation, witness.property);
        return { status: 'property-held', property: witness.property, actions: witness.path?.length, inputs: witness.inputs };
    }
    catch (error) {
        if (error.code !== 'FORMAL_PROPERTY' || error.property !== witness.property)
            throw error;
        return { status: 'reproduced', property: error.property, actions: witness.path?.length, inputs: witness.inputs, message: error.message };
    }
}
export function counterexampleWitness(counterexample) {
    assert.ok(counterexample.states?.length > 1, 'Empty TLC counterexample');
    assert.equal(counterexample.states[0].vars?.lastAction, 'Init', 'Counterexample lacks initial state');
    const path = counterexample.states.slice(1).map(({ action, vars }) => {
        assert.equal(action, vars?.lastAction, 'Counterexample action and input disagree');
        return { action, expected: vars };
    });
    return { schemaVersion: 1, kind: 'model-path', model: 'ApplySettlementRace', property: counterexample.property, path };
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
    if (process.argv[2] === '--witness') {
        const input = JSON.parse(readFileSync(process.argv[3], 'utf8'));
        const witnesses = input.counterexamples ? input.counterexamples.map(counterexampleWitness) : [input];
        assert.ok(witnesses.length, 'No counterexamples to replay');
        const source = process.argv[4] ? readFileSync(process.argv[4], 'utf8') : undefined;
        const results = [];
        for (const witness of witnesses)
            results.push(await replayWitness(witness, { source }));
        console.log(JSON.stringify(results));
        process.exitCode = results.some(result => result.status === 'reproduced') ? 1 : 0;
    }
    else {
        const graph = JSON.parse(readFileSync(process.argv[2], 'utf8'));
        const checked = await checkApplyGraph(graph, { witnessFile: process.argv[3] });
        checked.identityInputs = await checkApplyIdentityInputs();
        console.log(JSON.stringify(checked));
    }
}
